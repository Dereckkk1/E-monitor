package mcp

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/mcp/mcptest"
)

func TestArgsCanonicos_OrdemDeChaveNaoImportaEConfirmacaoSai(t *testing.T) {
	type in struct {
		B           int    `json:"b"`
		A           string `json:"a"`
		Confirmacao string `json:"confirmacao,omitempty"`
	}
	x, err := argsCanonicos(in{B: 1, A: "z", Confirmacao: "tok"})
	require.NoError(t, err)
	require.Equal(t, `{"a":"z","b":1}`, string(x))
	y, _ := argsCanonicos(map[string]any{"b": 1, "a": "z"})
	require.Equal(t, string(x), string(y))
}

func TestConfirmacao_VerificaSoComMesmosArgsUsuarioEPrazo(t *testing.T) {
	seg := bytes.Repeat([]byte("s"), 32)
	u := uuid.New()
	agora := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	tok := assinarConfirmacao(seg, "editar_material", []byte(`{"a":1}`), u, agora.Add(ttlConfirmacao))
	require.Regexp(t, `^[1-9][0-9]{0,15}\.[A-Za-z0-9_-]{43}$`, tok)
	require.True(t, verificarConfirmacao(seg, tok, "editar_material", []byte(`{"a":1}`), u, agora))
	require.False(t, verificarConfirmacao(seg, tok, "editar_material", []byte(`{"a":2}`), u, agora), "args mudados")
	require.False(t, verificarConfirmacao(seg, tok, "editar_cliente", []byte(`{"a":1}`), u, agora), "outra tool")
	require.False(t, verificarConfirmacao(seg, tok, "editar_material", []byte(`{"a":1}`), uuid.New(), agora), "outro usuário")
	require.False(t, verificarConfirmacao(seg, tok, "editar_material", []byte(`{"a":1}`), u, agora.Add(11*time.Minute)), "vencido")
	require.False(t, verificarConfirmacao(bytes.Repeat([]byte("x"), 32), tok, "editar_material", []byte(`{"a":1}`), u, agora), "outro segredo")
	for _, ruim := range []string{"", "x", "0" + tok, "+" + tok, " " + tok, tok + "=", strings.Replace(tok, ".", "", 1)} {
		require.False(t, verificarConfirmacao(seg, ruim, "editar_material", []byte(`{"a":1}`), u, agora), ruim)
	}
	longe := assinarConfirmacao(seg, "editar_material", []byte(`{"a":1}`), u, agora.Add(time.Hour))
	require.False(t, verificarConfirmacao(seg, longe, "editar_material", []byte(`{"a":1}`), u, agora), "exp além do TTL+folga")
}

// ── Uso único (Task 3) ──────────────────────────────────────────────────

func tokenDeTeste(t *testing.T, expira time.Time) string {
	t.Helper()
	return assinarConfirmacao(bytes.Repeat([]byte("s"), 32), "tool_teste", []byte(`{"a":1}`), uuid.New(), expira)
}

func TestConfirmacoes_ConsumirEhDeUsoUnicoELiberarDevolve(t *testing.T) {
	ctx, pool := mcptest.DB(t)
	_, err := pool.Exec(ctx, `TRUNCATE mcp_confirmacoes_usadas`)
	require.NoError(t, err)
	c := &Confirmacoes{DB: pool}
	exp := time.Now().Add(ttlConfirmacao)
	tok := tokenDeTeste(t, exp)

	usou, err := c.Consumir(ctx, tok, exp)
	require.NoError(t, err)
	require.True(t, usou, "1ª vez")
	usou, err = c.Consumir(ctx, tok, exp)
	require.NoError(t, err)
	require.False(t, usou, "2ª vez: já usado")

	require.NoError(t, c.Liberar(ctx, tok))
	usou, err = c.Consumir(ctx, tok, exp)
	require.NoError(t, err)
	require.True(t, usou, "depois de Liberar volta a valer")

	outro := tokenDeTeste(t, exp.Add(time.Second))
	usou, err = c.Consumir(ctx, outro, exp)
	require.NoError(t, err)
	require.True(t, usou, "outro token é independente")
}

func TestConfirmacoes_PodaOQueExpirouHaMaisDeUmaHora(t *testing.T) {
	ctx, pool := mcptest.DB(t)
	_, err := pool.Exec(ctx, `TRUNCATE mcp_confirmacoes_usadas`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO mcp_confirmacoes_usadas (hash, expira_em)
		VALUES (decode(repeat('aa', 32), 'hex'), now() - interval '2 hours'),
		       (decode(repeat('bb', 32), 'hex'), now() - interval '10 minutes'),
		       (decode(repeat('cc', 32), 'hex'), now() + interval '5 minutes')`)
	require.NoError(t, err)

	exp := time.Now().Add(ttlConfirmacao)
	usou, err := (&Confirmacoes{DB: pool}).Consumir(ctx, tokenDeTeste(t, exp), exp)
	require.NoError(t, err)
	require.True(t, usou)

	var restam int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM mcp_confirmacoes_usadas`).Scan(&restam))
	require.Equal(t, 3, restam, "só a de 2 h atrás sai; a recém-vencida e a viva ficam; mais a nova")
	var velha bool
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM mcp_confirmacoes_usadas WHERE hash = decode(repeat('aa', 32), 'hex'))`).Scan(&velha))
	require.False(t, velha)
}

func TestConfirmacoes_TokenForaDaFormaNaoTocaOBanco(t *testing.T) {
	// DB nil: se Consumir chegasse ao banco, entraria em pânico.
	c := &Confirmacoes{}
	for _, ruim := range []string{"", "x", "0123.abc", "+1.AAAA"} {
		usou, err := c.Consumir(t.Context(), ruim, time.Now())
		require.NoError(t, err, ruim)
		require.False(t, usou, ruim)
	}
}

func TestExpiraDoToken(t *testing.T) {
	exp := time.UnixMilli(1793000000123)
	require.True(t, exp.Equal(expiraDoToken(tokenDeTeste(t, exp))))
	require.True(t, expiraDoToken("lixo").IsZero())
}

// A poda oportunista nunca pode travar uma confirmação: uma linha vencida que
// outra transação está segurando (ex.: o varredor da Fatia 4) é pulada
// (FOR UPDATE SKIP LOCKED), não esperada.
func TestConfirmacoes_PodaNaoEsperaLinhaTravada(t *testing.T) {
	ctx, pool := mcptest.DB(t)
	_, err := pool.Exec(ctx, `TRUNCATE mcp_confirmacoes_usadas`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO mcp_confirmacoes_usadas (hash, expira_em)
		VALUES (decode(repeat('aa', 32), 'hex'), now() - interval '2 hours'),
		       (decode(repeat('bb', 32), 'hex'), now() - interval '3 hours')`)
	require.NoError(t, err)

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `SELECT 1 FROM mcp_confirmacoes_usadas WHERE hash = decode(repeat('aa', 32), 'hex') FOR UPDATE`)
	require.NoError(t, err)

	exp := time.Now().Add(ttlConfirmacao)
	curto, cancela := context.WithTimeout(ctx, 8*time.Second)
	defer cancela()
	inicio := time.Now()
	usou, err := (&Confirmacoes{DB: pool}).Consumir(curto, tokenDeTeste(t, exp), exp)
	require.NoError(t, err, "a poda esperou a linha travada em vez de pulá-la")
	require.True(t, usou)
	require.Less(t, time.Since(inicio), 5*time.Second)

	var velhaLivre bool
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT NOT EXISTS (SELECT 1 FROM mcp_confirmacoes_usadas WHERE hash = decode(repeat('bb', 32), 'hex'))`).Scan(&velhaLivre))
	require.True(t, velhaLivre, "a vencida que NÃO estava travada foi podada")
}

// Se a poda falha (aqui, um trigger que recusa o DELETE), o INSERT roda do
// mesmo jeito: a poda é higiene, nunca pré-condição da confirmação.
func TestConfirmacoes_FalhaDaPodaNaoImpedeOConsumo(t *testing.T) {
	ctx, pool := mcptest.DB(t)
	_, err := pool.Exec(ctx, `TRUNCATE mcp_confirmacoes_usadas`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		CREATE OR REPLACE FUNCTION mcp_teste_recusa_delete() RETURNS trigger AS $$
		BEGIN RAISE EXCEPTION 'poda recusada pelo teste'; END; $$ LANGUAGE plpgsql;
		CREATE TRIGGER mcp_teste_recusa_delete BEFORE DELETE ON mcp_confirmacoes_usadas
		FOR EACH ROW EXECUTE FUNCTION mcp_teste_recusa_delete()`)
	require.NoError(t, err)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS mcp_teste_recusa_delete ON mcp_confirmacoes_usadas;
			DROP FUNCTION IF EXISTS mcp_teste_recusa_delete()`)
	})
	_, err = pool.Exec(ctx, `INSERT INTO mcp_confirmacoes_usadas (hash, expira_em)
		VALUES (decode(repeat('aa', 32), 'hex'), now() - interval '2 hours')`)
	require.NoError(t, err)

	exp := time.Now().Add(ttlConfirmacao)
	tok := tokenDeTeste(t, exp)
	usou, err := (&Confirmacoes{DB: pool}).Consumir(ctx, tok, exp)
	require.NoError(t, err, "erro da poda não pode virar erro da confirmação")
	require.True(t, usou)

	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM mcp_confirmacoes_usadas WHERE hash = $1`, hashDoToken(tok)).Scan(&n))
	require.Equal(t, 1, n, "o token ficou marcado como usado")
}
