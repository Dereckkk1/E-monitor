package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/mcp/mcptest"
)

var t0 = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

func novaDeTeste(user uuid.UUID, args string) NovaPendente {
	return NovaPendente{UserID: user, Tool: "arquivar_proposta", Args: []byte(args), Motivo: "m",
		Preview: previa{Resumo: "arquivar"}}
}

func pendentesDB(t *testing.T) (context.Context, *pgxpool.Pool, *Pendentes, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx, pool := mcptest.DB(t)
	_, err := pool.Exec(ctx, `TRUNCATE mcp_acoes_pendentes`)
	require.NoError(t, err)
	return ctx, pool, &Pendentes{DB: pool},
		mcptest.Usuario(t, ctx, pool, "admin", true), mcptest.Usuario(t, ctx, pool, "admin", true)
}

func TestPendentes_CriarGravaEDeduplicaOAberto(t *testing.T) {
	ctx, _, s, dono, outro := pendentesDB(t)
	p, ja, err := s.Criar(ctx, novaDeTeste(dono, `{"id":1}`), t0)
	require.NoError(t, err)
	require.False(t, ja)
	require.Equal(t, "pendente", p.Estado)
	require.Equal(t, 0, p.Tentativa)
	require.WithinDuration(t, t0.Add(24*time.Hour), p.ExpiraEm, time.Second)
	require.Equal(t, "arquivar", p.Preview.Resumo)

	q, ja, err := s.Criar(ctx, novaDeTeste(dono, `{"id":1}`), t0.Add(time.Minute))
	require.NoError(t, err)
	require.True(t, ja)
	require.Equal(t, p.ID, q.ID)

	r, ja, err := s.Criar(ctx, novaDeTeste(outro, `{"id":1}`), t0)
	require.NoError(t, err)
	require.False(t, ja)
	require.NotEqual(t, p.ID, r.ID)
}

func TestPendentes_CriarExpiraOVencidoECriaNovo(t *testing.T) {
	ctx, _, s, dono, _ := pendentesDB(t)
	p, _, err := s.Criar(ctx, novaDeTeste(dono, `{"id":1}`), t0)
	require.NoError(t, err)
	q, ja, err := s.Criar(ctx, novaDeTeste(dono, `{"id":1}`), t0.Add(25*time.Hour))
	require.NoError(t, err)
	require.False(t, ja)
	require.NotEqual(t, p.ID, q.ID)
	velho, err := s.Obter(ctx, p.ID, dono)
	require.NoError(t, err)
	require.Equal(t, "expirada", velho.Estado)
}

func TestPendentes_Aprovar(t *testing.T) {
	ctx, _, s, dono, outro := pendentesDB(t)
	p, _, _ := s.Criar(ctx, novaDeTeste(dono, `{"id":1}`), t0)
	a, err := s.Aprovar(ctx, p.ID, dono, t0.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, "aprovada", a.Estado)
	require.Equal(t, 1, a.Tentativa)
	require.NotNil(t, a.AprovadaEm)

	_, err = s.Aprovar(ctx, p.ID, dono, t0.Add(time.Minute))
	require.Equal(t, ErrEstadoPendente{Estado: "aprovada"}, err)
	_, err = s.Aprovar(ctx, p.ID, outro, t0.Add(time.Minute))
	require.ErrorIs(t, err, ErrPendenteNaoAchada)
	_, err = s.Aprovar(ctx, uuid.New(), dono, t0)
	require.ErrorIs(t, err, ErrPendenteNaoAchada)
}

func TestPendentes_AprovarVencidoMarcaExpirada(t *testing.T) {
	ctx, _, s, dono, _ := pendentesDB(t)
	p, _, _ := s.Criar(ctx, novaDeTeste(dono, `{"id":1}`), t0)
	_, err := s.Aprovar(ctx, p.ID, dono, t0.Add(25*time.Hour))
	require.Equal(t, ErrEstadoPendente{Estado: "expirada"}, err)
	o, _ := s.Obter(ctx, p.ID, dono)
	require.Equal(t, "expirada", o.Estado)
}

func TestPendentes_MarcarExecutada(t *testing.T) {
	ctx, _, s, dono, _ := pendentesDB(t)
	p, _, _ := s.Criar(ctx, novaDeTeste(dono, `{"id":1}`), t0)
	_, _ = s.Aprovar(ctx, p.ID, dono, t0)
	ok, err := s.MarcarExecutada(ctx, p.ID, 2, nil, t0)
	require.NoError(t, err)
	require.False(t, ok, "tentativa errada")
	ok, err = s.MarcarExecutada(ctx, p.ID, 1, json.RawMessage(`{"ok":true}`), t0.Add(time.Second))
	require.NoError(t, err)
	require.True(t, ok)
	o, _ := s.Obter(ctx, p.ID, dono)
	require.Equal(t, "executada", o.Estado)
	require.JSONEq(t, `{"ok":true}`, string(o.Resultado))
	require.NotNil(t, o.ExecutadaEm)

	// também fecha um `falhou` da mesma tentativa
	q, _, _ := s.Criar(ctx, novaDeTeste(dono, `{"id":2}`), t0)
	_, _ = s.Aprovar(ctx, q.ID, dono, t0)
	ok, err = s.MarcarInterrompida(ctx, q.ID, 1, "caiu")
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = s.MarcarExecutada(ctx, q.ID, 1, nil, t0)
	require.NoError(t, err)
	require.True(t, ok)
	o, _ = s.Obter(ctx, q.ID, dono)
	require.Equal(t, "executada", o.Estado)
	require.Nil(t, o.Resultado)
	require.Nil(t, o.Erro)
}

func TestPendentes_FalhaDeNegocioVoltaAPendente(t *testing.T) {
	ctx, _, s, dono, _ := pendentesDB(t)

	// expiração perto do fim: sobe para agora+1h
	p, _, _ := s.Criar(ctx, novaDeTeste(dono, `{"id":1}`), t0)
	_, _ = s.Aprovar(ctx, p.ID, dono, t0.Add(23*time.Hour+30*time.Minute))
	ag := t0.Add(23*time.Hour + 40*time.Minute)
	ok, err := s.MarcarFalhaDeNegocio(ctx, p.ID, 1, "x", ag)
	require.NoError(t, err)
	require.True(t, ok)
	o, _ := s.Obter(ctx, p.ID, dono)
	require.Equal(t, "pendente", o.Estado)
	require.Equal(t, "x", *o.Erro)
	require.Nil(t, o.AprovadaEm)
	require.WithinDuration(t, ag.Add(time.Hour), o.ExpiraEm, time.Second)

	a, err := s.Aprovar(ctx, p.ID, dono, ag)
	require.NoError(t, err)
	require.Equal(t, 2, a.Tentativa)

	// expiração ainda longe: não encurta
	q, _, _ := s.Criar(ctx, novaDeTeste(dono, `{"id":2}`), t0)
	_, _ = s.Aprovar(ctx, q.ID, dono, t0)
	ok, err = s.MarcarFalhaDeNegocio(ctx, q.ID, 1, "y", t0.Add(time.Minute))
	require.NoError(t, err)
	require.True(t, ok)
	o, _ = s.Obter(ctx, q.ID, dono)
	require.WithinDuration(t, t0.Add(24*time.Hour), o.ExpiraEm, time.Second)

	ok, err = s.MarcarFalhaDeNegocio(ctx, q.ID, 1, "z", t0)
	require.NoError(t, err)
	require.False(t, ok, "não está aprovada")
}

func TestPendentes_InterrompidaEhFinal(t *testing.T) {
	ctx, _, s, dono, _ := pendentesDB(t)
	p, _, _ := s.Criar(ctx, novaDeTeste(dono, `{"id":1}`), t0)
	_, _ = s.Aprovar(ctx, p.ID, dono, t0)
	ok, err := s.MarcarInterrompida(ctx, p.ID, 1, "boom")
	require.NoError(t, err)
	require.True(t, ok)
	_, err = s.Aprovar(ctx, p.ID, dono, t0)
	require.Equal(t, ErrEstadoPendente{Estado: "falhou"}, err)
}

func TestPendentes_Recusar(t *testing.T) {
	ctx, _, s, dono, outro := pendentesDB(t)
	p, _, _ := s.Criar(ctx, novaDeTeste(dono, `{"id":1}`), t0)
	_, err := s.Recusar(ctx, p.ID, outro, t0)
	require.ErrorIs(t, err, ErrPendenteNaoAchada)
	r, err := s.Recusar(ctx, p.ID, dono, t0.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, "recusada", r.Estado)
	require.NotNil(t, r.RecusadaEm)
	_, err = s.Recusar(ctx, p.ID, dono, t0.Add(time.Minute))
	require.Equal(t, ErrEstadoPendente{Estado: "recusada"}, err)
}

func TestPendentes_ListarEObter(t *testing.T) {
	ctx, _, s, dono, outro := pendentesDB(t)
	a, _, _ := s.Criar(ctx, novaDeTeste(dono, `{"id":1}`), t0)
	b, _, _ := s.Criar(ctx, novaDeTeste(dono, `{"id":2}`), t0.Add(time.Minute))
	c, _, _ := s.Criar(ctx, novaDeTeste(dono, `{"id":3}`), t0.Add(2*time.Minute))
	_, _ = s.Recusar(ctx, c.ID, dono, t0.Add(3*time.Minute))
	_, _ = s.Aprovar(ctx, a.ID, dono, t0.Add(3*time.Minute))
	x, _, _ := s.Criar(ctx, novaDeTeste(outro, `{"id":1}`), t0)

	lista, total, err := s.Listar(ctx, dono, []string{"pendente", "aprovada"}, 10, 0)
	require.NoError(t, err)
	require.Equal(t, 2, total)
	require.Equal(t, []uuid.UUID{b.ID, a.ID}, []uuid.UUID{lista[0].ID, lista[1].ID})

	todas, total, err := s.Listar(ctx, dono, nil, 10, 0)
	require.NoError(t, err)
	require.Equal(t, 3, total)
	require.Len(t, todas, 3)

	pag, total, err := s.Listar(ctx, dono, nil, 1, 1)
	require.NoError(t, err)
	require.Equal(t, 3, total)
	require.Len(t, pag, 1)
	require.Equal(t, b.ID, pag[0].ID)

	_, err = s.Obter(ctx, x.ID, dono)
	require.ErrorIs(t, err, ErrPendenteNaoAchada)
}

func TestPendentes_ListarDesempataPorIDDesc(t *testing.T) {
	ctx, _, s, dono, _ := pendentesDB(t)
	for _, a := range []string{`{"id":1}`, `{"id":2}`, `{"id":3}`} {
		_, _, err := s.Criar(ctx, novaDeTeste(dono, a), t0)
		require.NoError(t, err)
	}
	lista, _, err := s.Listar(ctx, dono, nil, 10, 0)
	require.NoError(t, err)
	require.Len(t, lista, 3)
	for i := 1; i < len(lista); i++ {
		require.Greater(t, lista[i-1].ID.String(), lista[i].ID.String())
	}
}

func TestPendentes_ErroLongoEhCortadoEmRunes(t *testing.T) {
	ctx, _, s, dono, _ := pendentesDB(t)
	p, _, _ := s.Criar(ctx, novaDeTeste(dono, `{"id":1}`), t0)
	_, _ = s.Aprovar(ctx, p.ID, dono, t0)
	longo := strings.Repeat("é", 5000)
	ok, err := s.MarcarFalhaDeNegocio(ctx, p.ID, 1, longo, t0)
	require.NoError(t, err)
	require.True(t, ok)
	o, _ := s.Obter(ctx, p.ID, dono)
	require.Equal(t, 2000, len([]rune(*o.Erro)))

	_, _ = s.Aprovar(ctx, p.ID, dono, t0)
	ok, err = s.MarcarInterrompida(ctx, p.ID, 2, longo)
	require.NoError(t, err)
	require.True(t, ok)
	o, _ = s.Obter(ctx, p.ID, dono)
	require.Equal(t, 2000, len([]rune(*o.Erro)))
}
