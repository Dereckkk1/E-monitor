package hub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func hubLigar(t *testing.T, status int, corpo string, recebido *map[string]string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/platform/clientes/ligar", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "chave", r.Header.Get("X-Hub-Platform-Key"))
		if recebido != nil {
			_ = json.NewDecoder(r.Body).Decode(recebido)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(corpo))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, "chave")
}

func TestLigarCliente_Ok(t *testing.T) {
	var corpo map[string]string
	c := hubLigar(t, 200, `{"acao":"ligado","clienteId":"66e1"}`, &corpo)
	r, err := c.LigarCliente(context.Background(), "eh7k4m2x", "b2adf6cd-0fc7-4580-b77a-ab60be742543")
	require.NoError(t, err)
	require.Equal(t, "ligado", r.Acao)
	require.Equal(t, "EH-7K4M2X", corpo["hubCode"])
	require.Equal(t, "b2adf6cd-0fc7-4580-b77a-ab60be742543", corpo["idNaPlataforma"])
}

func TestLigarCliente_409TrazONome(t *testing.T) {
	c := hubLigar(t, 409, `{"error":{"code":"id_em_uso","message":"x"},"cliente":"LOJAS RENNER"}`, nil)
	_, err := c.LigarCliente(context.Background(), "EH-7K4M2X", "id")
	var he *Error
	require.True(t, errors.As(err, &he))
	require.Equal(t, http.StatusConflict, he.Status)
	require.Contains(t, he.Message, "LOJAS RENNER")
}

func TestLigarCliente_OutrosStatusViram503(t *testing.T) {
	for _, st := range []int{401, 404, 429, 500} {
		c := hubLigar(t, st, `{}`, nil)
		_, err := c.LigarCliente(context.Background(), "EH-7K4M2X", "id")
		var he *Error
		require.True(t, errors.As(err, &he), "status %d", st)
		require.Equal(t, http.StatusServiceUnavailable, he.Status, "status %d", st)
	}
}

func TestLigarCliente_SemConfiguracao(t *testing.T) {
	_, err := New("", "").LigarCliente(context.Background(), "EH-7K4M2X", "id")
	require.ErrorIs(t, err, ErrNotConfigured)
}
