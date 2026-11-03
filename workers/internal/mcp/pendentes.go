package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"radiocheck/internal/mcp/tokens"
)

// Store de pedidos `critico` (spec §5.2, plano Fatia 4 F4-2/F4-3). Transplante
// de hub backend/src/mcp/write/pendentes.ts para Postgres: toda transição é um
// UPDATE … WHERE estado = … (dois cliques, um vence) e todo SQL filtra pelo
// dono — "não existe" e "não é seu" são o mesmo erro.

const (
	ttlPendente       = 24 * time.Hour
	folgaAposFalha    = time.Hour
	tetoErroPendente  = 2000
	tetoListaPendente = 50
)

var ErrPendenteNaoAchada = errors.New("mcp: pedido não encontrado")

// ErrPendenteMudou: a aprovação veio com uma `tentativa` que já não é a do pedido. A tentativa é a
// versão do que a pessoa leu: cada troca do resumo gravado (preview_desatualizado) vem depois de uma
// aprovação que a incrementou, então quem aprova com a versão velha estaria aprovando um resumo que
// nunca viu. Nada foi executado; o pedido segue `pendente`.
var ErrPendenteMudou = errors.New("mcp: o pedido mudou desde que foi aberto")

// ErrCredencialRevogada: a credencial do MCP que fez o pedido (token pessoal ou conexão OAuth) foi
// revogada, venceu ou sumiu. O store já fechou o pedido como `expirada` (com msgCredencialRevogada
// em `erro`): nada foi executado e ele não volta.
var ErrCredencialRevogada = errors.New("mcp: a conexão de IA que fez o pedido foi revogada")

const (
	// msgCredencialRevogada é o `erro` gravado no pedido expirado pela revogação e o texto que a
	// pessoa e a IA (minhas_pendentes) leem. A função mcp_revogar_credenciais_do_usuario (migration
	// 0072) grava o mesmo texto; o teste de trigger confere que os dois batem.
	msgCredencialRevogada = tokens.TextoPedidoRevogado
	msgPendenteMudou      = "O pedido mudou desde que você abriu; releia antes de aprovar."
)

// ErrEstadoPendente: o pedido existe e é do dono, mas não está em `pendente`. Motivo, quando
// preenchido, é o texto que explica por que (o `erro` de um pedido expirado pela revogação).
type ErrEstadoPendente struct{ Estado, Motivo string }

func (e ErrEstadoPendente) Error() string { return "mcp: pedido em estado " + e.Estado }

type Pendente struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	TokenID     *uuid.UUID
	Tool        string
	Args        json.RawMessage
	Motivo      string
	Preview     previa
	Estado      string
	Tentativa   int
	CriadaEm    time.Time
	ExpiraEm    time.Time
	AprovadaEm  *time.Time
	ExecutadaEm *time.Time
	RecusadaEm  *time.Time
	Resultado   json.RawMessage
	// Ressalvas são os avisos da execução (feito.Ressalvas), gravados ao fechar o
	// pedido; nunca nil depois de lido.
	Ressalvas []string
	Erro      *string
}

type NovaPendente struct {
	UserID  uuid.UUID
	TokenID uuid.UUID
	Tool    string
	Args    []byte // canônicos (argsCanonicos), sem "confirmacao"
	Motivo  string
	Preview previa
}

type Pendentes struct{ DB *pgxpool.Pool }

func hashDosArgs(tool string, args []byte) []byte {
	h := sha256.New()
	h.Write([]byte(tool))
	h.Write([]byte{0})
	h.Write(args)
	return h.Sum(nil)
}

const colunasPendente = `id, user_id, token_id, tool, args, motivo, preview, estado, tentativa,
	criada_em, expira_em, aprovada_em, executada_em, recusada_em, resultado, ressalvas, erro`

func lerPendente(row pgx.Row) (Pendente, error) {
	var p Pendente
	var args, preview, resultado []byte
	if err := row.Scan(&p.ID, &p.UserID, &p.TokenID, &p.Tool, &args, &p.Motivo, &preview, &p.Estado,
		&p.Tentativa, &p.CriadaEm, &p.ExpiraEm, &p.AprovadaEm, &p.ExecutadaEm, &p.RecusadaEm,
		&resultado, &p.Ressalvas, &p.Erro); err != nil {
		return Pendente{}, err
	}
	if p.Ressalvas == nil {
		p.Ressalvas = []string{}
	}
	p.Args = args
	if len(resultado) > 0 {
		p.Resultado = resultado
	}
	if err := json.Unmarshal(preview, &p.Preview); err != nil {
		return Pendente{}, err
	}
	return p, nil
}

// cortarErro limita o texto a tetoErroPendente runes (o CHECK do banco conta caracteres).
func cortarErro(s string) string {
	r := []rune(s)
	if len(r) <= tetoErroPendente {
		return s
	}
	return string(r[:tetoErroPendente])
}

// Criar grava o pedido ou devolve o aberto igual (F4-3). Numa transação: expira
// o vencido daquele hash, tenta inserir, e no conflito lê o aberto.
func (s *Pendentes) Criar(ctx context.Context, n NovaPendente, agora time.Time) (Pendente, bool, error) {
	hash := hashDosArgs(n.Tool, n.Args)
	pv, err := json.Marshal(n.Preview)
	if err != nil {
		return Pendente{}, false, err
	}
	var tok *uuid.UUID
	if n.TokenID != uuid.Nil {
		tok = &n.TokenID
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Pendente{}, false, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE mcp_acoes_pendentes SET estado = 'expirada'
		WHERE user_id = $1 AND args_hash = $2 AND estado = 'pendente' AND expira_em <= $3`,
		n.UserID, hash, agora); err != nil {
		return Pendente{}, false, err
	}
	// Até duas voltas: se o aberto que causou o conflito saiu dos estados
	// abertos entre o INSERT e o SELECT (recusado, executado, expirado), o
	// SELECT não acha nada e o INSERT precisa de outra chance.
	for volta := 0; volta < 2; volta++ {
		p, err := lerPendente(tx.QueryRow(ctx, `INSERT INTO mcp_acoes_pendentes
			(user_id, token_id, tool, args, args_hash, motivo, preview, expira_em, criada_em)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (user_id, args_hash) WHERE estado IN ('pendente', 'aprovada') DO NOTHING
			RETURNING `+colunasPendente,
			n.UserID, tok, n.Tool, n.Args, hash, n.Motivo, pv, agora.Add(ttlPendente), agora))
		if err == nil {
			return p, false, tx.Commit(ctx)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return Pendente{}, false, err
		}
		p, err = lerPendente(tx.QueryRow(ctx, `SELECT `+colunasPendente+` FROM mcp_acoes_pendentes
			WHERE user_id = $1 AND args_hash = $2 AND estado IN ('pendente', 'aprovada')`, n.UserID, hash))
		if err == nil {
			return p, true, tx.Commit(ctx)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return Pendente{}, false, err
		}
	}
	return Pendente{}, false, errors.New("mcp: o pedido aberto sumiu duas vezes durante a criação")
}

// credencialViva é o predicado SQL "a credencial que fez o pedido `a` ainda vale". O pedido guarda o
// token da CHAMADA (token_id), e o que precisa estar vivo é a conexão dele: um token pessoal vale por
// si; uma conexão OAuth vale enquanto o usuário tem algum token vivo daquele cliente (o access token
// da chamada venceu em uma hora, o refresh segue). token_id NULL (o token foi apagado) ou de outro
// usuário = morta. Usa o relógio do banco, como o resto das credenciais (tokens.Store).
func credencialViva(a string) string {
	return `EXISTS (SELECT 1 FROM mcp_tokens origem
		JOIN mcp_tokens c ON c.user_id = origem.user_id
		 AND (c.id = origem.id OR (origem.client_id IS NOT NULL AND c.client_id = origem.client_id))
		WHERE origem.id = ` + a + `.token_id AND origem.user_id = ` + a + `.user_id
		  AND c.revoked_at IS NULL AND c.expires_at > NOW())`
}

// diagnostico explica por que um UPDATE condicional não pegou nenhuma linha. aprovacao != nil é a
// aprovação (a Recusar passa nil): além do prazo, confere a credencial e a versão (tentativa).
func (s *Pendentes) diagnostico(ctx context.Context, id, dono uuid.UUID, agora time.Time, aprovacao *int) error {
	var estado string
	var expira time.Time
	var tentativa int
	var erro *string
	var viva bool
	err := s.DB.QueryRow(ctx, `SELECT a.estado, a.expira_em, a.tentativa, a.erro, `+credencialViva("a")+`
		FROM mcp_acoes_pendentes a WHERE a.id = $1 AND a.user_id = $2`, id, dono).Scan(&estado, &expira, &tentativa, &erro, &viva)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrPendenteNaoAchada
	}
	if err != nil {
		return err
	}
	if estado == "pendente" && !expira.After(agora) {
		if _, err := s.DB.Exec(ctx, `UPDATE mcp_acoes_pendentes SET estado = 'expirada'
			WHERE id = $1 AND estado = 'pendente' AND expira_em <= $2`, id, agora); err != nil {
			return err
		}
		return ErrEstadoPendente{Estado: "expirada"}
	}
	if estado == "pendente" && aprovacao != nil {
		if !viva {
			// Fecha o pedido: ele não é mais aprovável, e a lista / minhas_pendentes passam a dizer por quê.
			if _, err := s.DB.Exec(ctx, `UPDATE mcp_acoes_pendentes SET estado = 'expirada', erro = $2
				WHERE id = $1 AND estado = 'pendente'`, id, msgCredencialRevogada); err != nil {
				return err
			}
			return ErrCredencialRevogada
		}
		if tentativa != *aprovacao {
			return ErrPendenteMudou
		}
	}
	if estado == "expirada" && erro != nil {
		return ErrEstadoPendente{Estado: estado, Motivo: *erro}
	}
	return ErrEstadoPendente{Estado: estado}
}

// Aprovar abre uma tentativa: pendente → aprovada, só para o dono, dentro do prazo, com a credencial
// do pedido viva e se `tentativa` for a vigente (a versão que a pessoa leu; ver ErrPendenteMudou).
func (s *Pendentes) Aprovar(ctx context.Context, id, dono uuid.UUID, tentativa int, agora time.Time) (Pendente, error) {
	p, err := lerPendente(s.DB.QueryRow(ctx, `UPDATE mcp_acoes_pendentes a
		SET estado = 'aprovada', aprovada_em = $3, tentativa = a.tentativa + 1, erro = NULL
		WHERE a.id = $1 AND a.user_id = $2 AND a.estado = 'pendente' AND a.expira_em > $3 AND a.tentativa = $4
		  AND `+credencialViva("a")+`
		RETURNING `+colunasPendente, id, dono, agora, tentativa))
	if errors.Is(err, pgx.ErrNoRows) {
		return Pendente{}, s.diagnostico(ctx, id, dono, agora, &tentativa)
	}
	return p, err
}

// Recusar fecha o pedido pendente a pedido do dono.
func (s *Pendentes) Recusar(ctx context.Context, id, dono uuid.UUID, agora time.Time) (Pendente, error) {
	p, err := lerPendente(s.DB.QueryRow(ctx, `UPDATE mcp_acoes_pendentes
		SET estado = 'recusada', recusada_em = $3
		WHERE id = $1 AND user_id = $2 AND estado = 'pendente' AND expira_em > $3
		RETURNING `+colunasPendente, id, dono, agora))
	if errors.Is(err, pgx.ErrNoRows) {
		return Pendente{}, s.diagnostico(ctx, id, dono, agora, nil)
	}
	return p, err
}

// MarcarExecutada fecha a tentativa vigente com sucesso e devolve a linha
// atualizada. Aceita também `falhou` da mesma tentativa (o executor terminou
// depois de ser dado como interrompido). false = ninguém foi atualizado.
// ressalvas são os avisos da execução: ficam gravados com o resultado.
func (s *Pendentes) MarcarExecutada(ctx context.Context, id uuid.UUID, tentativa int, resultado json.RawMessage, ressalvas []string, agora time.Time) (Pendente, bool, error) {
	var res any
	if len(resultado) > 0 {
		res = []byte(resultado)
	}
	if ressalvas == nil {
		ressalvas = []string{}
	}
	p, err := lerPendente(s.DB.QueryRow(ctx, `UPDATE mcp_acoes_pendentes
		SET estado = 'executada', executada_em = $4, resultado = $3, ressalvas = $5, erro = NULL
		WHERE id = $1 AND tentativa = $2 AND estado IN ('aprovada', 'falhou')
		RETURNING `+colunasPendente,
		id, tentativa, res, agora, ressalvas))
	if errors.Is(err, pgx.ErrNoRows) {
		return Pendente{}, false, nil
	}
	if err != nil {
		return Pendente{}, false, err
	}
	return p, true, nil
}

// MarcarFalhaDeNegocio devolve o pedido a `pendente` com o erro, para nova
// tentativa. Aceita `aprovada` e também `falhou` da mesma tentativa (uma
// recusa tardia depois de o varredor dar a execução por interrompida: nada
// rodou). estenderPrazo: o prazo nunca encurta e ganha pelo menos
// folgaAposFalha; sem ele (a ação mudou ou sumiu: não adianta insistir) o
// prazo fica como está. novaPrevia != nil troca o resumo gravado pelo novo NA MESMA atualização
// (a aprovação recusada por preview_desatualizado: /pendentes passa a mostrar o que vale agora);
// nil mantém o que está.
func (s *Pendentes) MarcarFalhaDeNegocio(ctx context.Context, id uuid.UUID, tentativa int, erro string, agora time.Time, estenderPrazo bool, novaPrevia *previa) (bool, error) {
	var pv any // nil = NULL: o COALESCE mantém o resumo gravado
	if novaPrevia != nil {
		b, err := json.Marshal(novaPrevia)
		if err != nil {
			return false, err
		}
		pv = b
	}
	tag, err := s.DB.Exec(ctx, `UPDATE mcp_acoes_pendentes
		SET estado = 'pendente', erro = $3, aprovada_em = NULL,
		    expira_em = CASE WHEN $5 THEN GREATEST(expira_em, $4::timestamptz) ELSE expira_em END,
		    preview = COALESCE($6::jsonb, preview)
		WHERE id = $1 AND tentativa = $2 AND estado IN ('aprovada', 'falhou')`,
		id, tentativa, cortarErro(erro), agora.Add(folgaAposFalha), estenderPrazo, pv)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// MarcarInterrompida: exceção na execução — final, sem nova tentativa.
func (s *Pendentes) MarcarInterrompida(ctx context.Context, id uuid.UUID, tentativa int, erro string) (bool, error) {
	tag, err := s.DB.Exec(ctx, `UPDATE mcp_acoes_pendentes SET estado = 'falhou', erro = $3
		WHERE id = $1 AND tentativa = $2 AND estado = 'aprovada'`, id, tentativa, cortarErro(erro))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// Obter lê um pedido do dono.
func (s *Pendentes) Obter(ctx context.Context, id, dono uuid.UUID) (Pendente, error) {
	p, err := lerPendente(s.DB.QueryRow(ctx, `SELECT `+colunasPendente+` FROM mcp_acoes_pendentes
		WHERE id = $1 AND user_id = $2`, id, dono))
	if errors.Is(err, pgx.ErrNoRows) {
		return Pendente{}, ErrPendenteNaoAchada
	}
	return p, err
}

// Listar devolve os pedidos do dono, do mais novo ao mais velho, e o total
// (sem paginar). estados nil = todos.
func (s *Pendentes) Listar(ctx context.Context, dono uuid.UUID, estados []string, limit, offset int) ([]Pendente, int, error) {
	if limit < 1 {
		limit = 1
	}
	if limit > tetoListaPendente {
		limit = tetoListaPendente
	}
	if offset < 0 {
		offset = 0
	}
	var filtro any // nil = sem filtro
	if estados != nil {
		filtro = estados
	}
	var total int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM mcp_acoes_pendentes
		WHERE user_id = $1 AND ($2::text[] IS NULL OR estado = ANY($2))`, dono, filtro).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.DB.Query(ctx, `SELECT `+colunasPendente+` FROM mcp_acoes_pendentes
		WHERE user_id = $1 AND ($2::text[] IS NULL OR estado = ANY($2))
		ORDER BY criada_em DESC, id DESC LIMIT $3 OFFSET $4`, dono, filtro, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Pendente{}
	for rows.Next() {
		p, err := lerPendente(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, p)
	}
	return out, total, rows.Err()
}
