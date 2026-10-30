package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/mcp/mcptest"
)

type bancadaPendentes struct {
	ctx  context.Context
	pool *pgxpool.Pool
	st   *Pendentes
	cfg  ConfigServidor
	eu   *Principal
}

func pendentesTool(t *testing.T) *bancadaPendentes {
	t.Helper()
	ctx, pool := mcptest.DB(t)
	_, err := pool.Exec(ctx, `TRUNCATE mcp_acoes_pendentes`)
	require.NoError(t, err)
	eu := &Principal{UserID: uuid.New(), Email: "eu@teste.local", Nome: "Eu", TokenID: uuid.New()}
	semearPrincipal(t, ctx, pool, eu)
	st := &Pendentes{DB: pool}
	cfg := ConfigServidor{Versao: "teste", Portal: "https://portal.teste", Leitura: NovaLeitura(pool),
		Escritas: &Escritas{Pendentes: st}, Log: zap.NewNop()}
	return &bancadaPendentes{ctx: ctx, pool: pool, st: st, cfg: cfg, eu: eu}
}

func (b *bancadaPendentes) chamar(t *testing.T, args any) (string, bool) {
	t.Helper()
	return chamarNoServidor(t, novoServidor(b.cfg, b.eu), "minhas_pendentes", args)
}

func (b *bancadaPendentes) lista(t *testing.T, args any) map[string]any {
	t.Helper()
	txt, erro := b.chamar(t, args)
	require.False(t, erro, txt)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(txt), &out), txt)
	return out
}

func (b *bancadaPendentes) nova(t *testing.T, dono uuid.UUID, args string, quando time.Time) Pendente {
	t.Helper()
	p, _, err := b.st.Criar(b.ctx, novaDeTeste(dono, args), quando)
	require.NoError(t, err)
	return p
}

func (b *bancadaPendentes) estado(t *testing.T, id uuid.UUID, estado string) {
	t.Helper()
	_, err := b.pool.Exec(b.ctx, `UPDATE mcp_acoes_pendentes SET estado = $2 WHERE id = $1`, id, estado)
	require.NoError(t, err)
}

func TestMinhasPendentes_SemFiltroSoOsAbertos(t *testing.T) {
	b := pendentesTool(t)
	aberta := b.nova(t, b.eu.UserID, `{"id":1}`, t0)
	aprov := b.nova(t, b.eu.UserID, `{"id":2}`, t0.Add(time.Minute))
	b.estado(t, aprov.ID, "aprovada")
	for i, est := range []string{"executada", "falhou", "recusada", "expirada"} {
		p := b.nova(t, b.eu.UserID, fmt.Sprintf(`{"id":%d}`, 10+i), t0.Add(time.Duration(i)*time.Second))
		b.estado(t, p.ID, est)
	}
	it := itens(t, b.lista(t, map[string]any{}))
	require.Equal(t, []string{aprov.ID.String(), aberta.ID.String()}, ids(t, it, "id"), "mais novo primeiro")

	for est, n := range map[string]int{"pendente": 1, "aprovada": 1, "executada": 1, "falhou": 1, "recusada": 1, "expirada": 1, "todos": 6} {
		require.Len(t, itens(t, b.lista(t, map[string]any{"estado": est})), n, est)
	}
	txt, erro := b.chamar(t, map[string]any{"estado": "feito"})
	require.True(t, erro)
	require.Contains(t, txt, "[entrada_invalida]")
}

func TestMinhasPendentes_ItemEPaginacao(t *testing.T) {
	b := pendentesTool(t)
	p := b.nova(t, b.eu.UserID, `{"id":1}`, t0)
	q := b.nova(t, b.eu.UserID, `{"id":2}`, t0.Add(time.Minute))
	b.estado(t, q.ID, "aprovada")
	_, err := b.pool.Exec(b.ctx, `UPDATE mcp_acoes_pendentes SET erro = 'deu ruim', aprovada_em = $2 WHERE id = $1`, q.ID, t0)
	require.NoError(t, err)

	out := b.lista(t, map[string]any{"limit": 1})
	require.EqualValues(t, 2, pag(t, out)["total"])
	require.Equal(t, true, pag(t, out)["has_more"])
	out = b.lista(t, map[string]any{"limit": 1, "offset": 1})
	require.Equal(t, false, pag(t, out)["has_more"])
	require.EqualValues(t, 1, pag(t, out)["offset"])
	it := itens(t, out)[0]
	require.Equal(t, p.ID.String(), it["id"])
	require.Equal(t, "arquivar_proposta", it["tool"])
	require.Equal(t, "pendente", it["estado"])
	require.Equal(t, "m", it["motivo"])
	require.Equal(t, "arquivar", it["resumo"])
	require.Equal(t, "https://portal.teste/pendentes?id="+p.ID.String(), it["aprovar_em"])
	for _, c := range []string{"criada_em", "expira_em"} {
		require.NotNil(t, it[c], c)
	}
	require.Nil(t, it["aprovada_em"])
	require.Nil(t, it["executada_em"])
	require.Nil(t, it["erro"])

	ap := itens(t, b.lista(t, map[string]any{"estado": "aprovada"}))[0]
	require.Nil(t, ap["aprovar_em"], "só pendente tem link de aprovação")
	require.Equal(t, "deu ruim", ap["erro"])
	require.NotNil(t, ap["aprovada_em"])

	txt := b.lista(t, map[string]any{"estado": "todos"})
	bruto, _ := json.Marshal(txt)
	for _, proibido := range []string{`"args"`, `"resultado"`, `"token_id"`} {
		require.NotContains(t, string(bruto), proibido)
	}
	// offset acima do teto é recusado como nas outras listas.
	r, erro := b.chamar(t, map[string]any{"offset": offsetTeto + 1})
	require.True(t, erro)
	require.Contains(t, r, "[entrada_invalida]")
	// limit acima do teto do store é rebaixado e ecoado.
	require.EqualValues(t, tetoListaPendente, pag(t, b.lista(t, map[string]any{"limit": 100}))["limit"])
}

func TestMinhasPendentes_NuncaDeOutroUsuario(t *testing.T) {
	b := pendentesTool(t)
	outro := mcptest.Usuario(t, b.ctx, b.pool, "admin", true)
	alheia := b.nova(t, outro, `{"id":1}`, t0)
	minha := b.nova(t, b.eu.UserID, `{"id":1}`, t0)
	it := itens(t, b.lista(t, map[string]any{"estado": "todos"}))
	require.Equal(t, []string{minha.ID.String()}, ids(t, it, "id"))
	require.NotEqual(t, alheia.ID, minha.ID)
}

func TestMinhasPendentes_SomenteLeitura(t *testing.T) {
	b := pendentesTool(t)
	ct, st := sdk.NewInMemoryTransports()
	_, err := novoServidor(b.cfg, b.eu).Connect(b.ctx, st, nil)
	require.NoError(t, err)
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "1"}, nil).Connect(b.ctx, ct, nil)
	require.NoError(t, err)
	defer cs.Close()
	res, err := cs.ListTools(b.ctx, nil)
	require.NoError(t, err)
	achou := false
	for _, tl := range res.Tools {
		if tl.Name == "minhas_pendentes" {
			achou = true
			require.True(t, tl.Annotations.ReadOnlyHint)
			require.Contains(t, tl.Description, "só `executada` significa feito")
		}
	}
	require.True(t, achou)
}

func TestMinhasPendentes_SemStoreFalhaInterna(t *testing.T) {
	b := pendentesTool(t)
	b.cfg.Escritas = nil
	txt, erro := b.chamar(t, map[string]any{})
	require.True(t, erro)
	require.NotContains(t, txt, "store")
}

func TestMinhasPendentes_PiorCasoCabe(t *testing.T) {
	b := pendentesTool(t)
	longo := strings.Repeat("ç", 2000) // 2 000 runas, 4 KB em UTF-8
	for i := range tetoListaPendente + 1 {
		p := NovaPendente{UserID: b.eu.UserID, Tool: strings.Repeat("t", 100), Args: fmt.Appendf(nil, `{"id":%d}`, i),
			Motivo: longo[:len("ç")*500], Preview: previa{Resumo: longo}}
		_, _, err := b.st.Criar(b.ctx, p, t0.Add(time.Duration(i)*time.Second))
		require.NoError(t, err)
	}
	_, err := b.pool.Exec(b.ctx, `UPDATE mcp_acoes_pendentes SET erro = $1`, longo)
	require.NoError(t, err)
	out := b.lista(t, map[string]any{"limit": 100})
	require.Len(t, itens(t, out), tetoListaPendente)
	n := tamanhoDoResultado(t, out)
	t.Logf("minhas_pendentes: %d itens no pior caso = %d bytes", tetoListaPendente, n)
	require.Less(t, n, 60_000)
}
