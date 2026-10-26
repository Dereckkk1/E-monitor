package handlers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCacheNaoLidas(t *testing.T) {
	agora := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	c := novoCacheNaoLidas(30 * time.Second)
	c.agora = func() time.Time { return agora }

	_, ok := c.pega("Ana@T.local")
	require.False(t, ok)

	c.guarda("ana@t.local", 4)
	n, ok := c.pega(" ANA@t.local ")
	require.True(t, ok)
	require.Equal(t, 4, n)

	agora = agora.Add(30 * time.Second)
	_, ok = c.pega("ana@t.local")
	require.False(t, ok, "venceu aos 30 s")

	c.guarda("ana@t.local", 1)
	c.esquece("ana@t.local")
	_, ok = c.pega("ana@t.local")
	require.False(t, ok)
}
