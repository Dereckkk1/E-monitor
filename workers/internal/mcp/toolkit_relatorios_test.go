package mcp

import (
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestLinkTela_FiltrosNaQuery(t *testing.T) {
	cfg := ConfigServidor{Portal: "https://portal.teste/"}
	q := url.Values{}
	q.Set("client_id", "c1")
	q.Set("campaigns", "a,b")
	require.Equal(t, "https://portal.teste/insights?campaigns=a%2Cb&client_id=c1", *cfg.linkTela("/insights", q))
	require.Equal(t, "https://portal.teste/management", *cfg.linkTela("/management", nil))
	require.Nil(t, ConfigServidor{}.linkTela("/insights", q), "sem portal configurado o link é null")
}

func TestCsvIDs(t *testing.T) {
	a, b := uuid.MustParse("00000000-0000-0000-0000-00000000000a"), uuid.MustParse("00000000-0000-0000-0000-00000000000b")
	require.Equal(t, a.String()+","+b.String(), csvIDs([]uuid.UUID{a, b}))
	require.Equal(t, "", csvIDs(nil))
}

func TestCortar_DevolveTotalReal(t *testing.T) {
	xs, total := cortar([]int{1, 2, 3}, 2)
	require.Equal(t, []int{1, 2}, xs)
	require.Equal(t, 3, total)
	vazio, total := cortar[int](nil, 20)
	require.NotNil(t, vazio, "lista vazia sai [] e não null")
	require.Equal(t, 0, total)
}

func TestListaCursor_TotalNulo(t *testing.T) {
	l := novaListaCursor([]string{"a", "b"}, 2, "prox")
	require.Nil(t, l.Page.Total)
	require.True(t, l.Page.HasMore)
	require.Equal(t, "prox", *l.Page.NextCursor)
	fim := novaListaCursor([]string{}, 25, "")
	require.False(t, fim.Page.HasMore)
	require.Nil(t, fim.Page.NextCursor)
}

func TestDatasDeSP(t *testing.T) {
	// 2026-10-28 01:30 UTC ainda é 27/10 em São Paulo.
	agora := time.Date(2026, 10, 28, 1, 30, 0, 0, time.UTC)
	require.Equal(t, "2026-10-27", dia(hojeSP(agora)))
	require.Equal(t, "2026-10-26", dia(ontemSP(agora)))
	ini, fim := mesCorrenteSP(agora)
	require.Equal(t, "2026-10-01", dia(ini))
	require.Equal(t, "2026-10-31", dia(fim))
}
