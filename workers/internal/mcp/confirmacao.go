package mcp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// Token do nível confirma (spec §5.2; transplante de hub
// backend/src/mcp/write/confirmacao.ts): HMAC-SHA256 com o JWT_SECRET, com
// separação de domínio, sobre [tool, args canônicos, usuário, exp]. Os ARGS
// entram na assinatura: o que se executa é o que o preview mostrou. Forma
// canônica "<exp_ms>.<mac43>" — sem ela, "01…"/"+1…" dariam hashes de uso
// único diferentes para a mesma confirmação.

const (
	ttlConfirmacao   = 10 * time.Minute
	folgaConfirmacao = time.Minute
)

var formaDoToken = regexp.MustCompile(`^([1-9][0-9]{0,15})\.([A-Za-z0-9_-]{43})$`)

// argsCanonicos: JSON com chaves ordenadas em todo nível (map do Go serializa
// ordenado), sem a chave "confirmacao". Números passam como json.Number para
// não perder precisão nem mudar de forma entre as duas chamadas.
func argsCanonicos(in any) ([]byte, error) {
	b, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("mcp: args precisam ser objeto JSON: %w", err)
	}
	delete(m, "confirmacao")
	return json.Marshal(m)
}

func macConfirmacao(segredo []byte, tool string, args []byte, user uuid.UUID, expMs int64) string {
	payload, _ := json.Marshal([]any{tool, json.RawMessage(args), user.String(), expMs})
	h := hmac.New(sha256.New, segredo)
	h.Write([]byte("mcp-confirmacao."))
	h.Write(payload)
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

func assinarConfirmacao(segredo []byte, tool string, args []byte, user uuid.UUID, exp time.Time) string {
	expMs := exp.UnixMilli()
	return strconv.FormatInt(expMs, 10) + "." + macConfirmacao(segredo, tool, args, user, expMs)
}

// verificarConfirmacao nunca entra em pânico: qualquer coisa fora do esperado é false.
func verificarConfirmacao(segredo []byte, token, tool string, args []byte, user uuid.UUID, agora time.Time) bool {
	m := formaDoToken.FindStringSubmatch(token)
	if m == nil {
		return false
	}
	expMs, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return false
	}
	exp := time.UnixMilli(expMs)
	if exp.Before(agora) || exp.After(agora.Add(ttlConfirmacao+folgaConfirmacao)) {
		return false
	}
	return hmac.Equal([]byte(m[2]), []byte(macConfirmacao(segredo, tool, args, user, expMs)))
}

func hashDoToken(token string) []byte { s := sha256.Sum256([]byte(token)); return s[:] }

// expiraDoToken lê o prefixo "<exp_ms>" de um token na forma canônica; zero
// se o token estiver fora da forma. Chame depois de verificarConfirmacao.
func expiraDoToken(token string) time.Time {
	m := formaDoToken.FindStringSubmatch(token)
	if m == nil {
		return time.Time{}
	}
	ms, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

// Confirmacoes marca o uso único (F3-5): consome ANTES de executar; só a falha
// de negócio libera. Erro interno não libera — a escrita pode ter gravado algo.
type Confirmacoes struct {
	DB  *pgxpool.Pool
	Log *zap.Logger // opcional; nil = sem log
}

func (c *Confirmacoes) log() *zap.Logger {
	if c.Log == nil {
		return zap.NewNop()
	}
	return c.Log
}

// Consumir reivindica o token: true para quem chegou primeiro, false se já foi
// usado (ou se o token está fora da forma — nesse caso o banco nem é tocado).
func (c *Confirmacoes) Consumir(ctx context.Context, token string, exp time.Time) (bool, error) {
	if !formaDoToken.MatchString(token) {
		return false, nil
	}
	// Poda oportunista: sem varredor nesta fatia (o da Fatia 4 assume). É
	// higiene, nunca pré-condição: SKIP LOCKED pula linha que outra transação
	// segura (não espera), e erro da poda é logado e ignorado — a confirmação
	// segue para o INSERT.
	if _, err := c.DB.Exec(ctx, `DELETE FROM mcp_confirmacoes_usadas WHERE hash IN (
		SELECT hash FROM mcp_confirmacoes_usadas WHERE expira_em < now() - interval '1 hour'
		ORDER BY expira_em LIMIT 500 FOR UPDATE SKIP LOCKED)`); err != nil {
		c.log().Warn("mcp: poda de mcp_confirmacoes_usadas falhou; seguindo com a confirmação", zap.Error(err))
	}
	tag, err := c.DB.Exec(ctx, `INSERT INTO mcp_confirmacoes_usadas (hash, expira_em) VALUES ($1, $2)
		ON CONFLICT (hash) DO NOTHING`, hashDoToken(token), exp)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// Liberar devolve o token (falha de negócio na execução): a pessoa pode tentar
// a mesma confirmação de novo.
func (c *Confirmacoes) Liberar(ctx context.Context, token string) error {
	_, err := c.DB.Exec(ctx, `DELETE FROM mcp_confirmacoes_usadas WHERE hash = $1`, hashDoToken(token))
	return err
}
