package hub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// O que o hub falso recebeu. ⚠️ Nada de `require` dentro do handler: ele roda
// em outra goroutine, e um FailNow fora da goroutine do teste não para o
// teste. Grava aqui e afirma depois, na goroutine do teste.
type pedidoLigar struct {
	mu     sync.Mutex
	path   string
	method string
	chave  string
	corpo  map[string]string
}

func hubLigar(t *testing.T, status int, corpo string) (*Client, *pedidoLigar) {
	t.Helper()
	p := &pedidoLigar{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var c map[string]string
		_ = json.NewDecoder(r.Body).Decode(&c)
		p.mu.Lock()
		p.path, p.method, p.chave, p.corpo = r.URL.Path, r.Method, r.Header.Get("X-Hub-Platform-Key"), c
		p.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(corpo))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, "chave"), p
}

func (p *pedidoLigar) conferir(t *testing.T) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	require.Equal(t, "/api/platform/clientes/ligar", p.path)
	require.Equal(t, http.MethodPost, p.method)
	require.Equal(t, "chave", p.chave)
}

func erroDoHub(t *testing.T, err error) *Error {
	t.Helper()
	var he *Error
	require.True(t, errors.As(err, &he), "esperava *hub.Error, veio %v", err)
	return he
}

func TestLigarCliente_Ok(t *testing.T) {
	c, p := hubLigar(t, 200, `{"acao":"ligado","clienteId":"66e1"}`)
	r, err := c.LigarCliente(context.Background(), "eh7k4m2x", "b2adf6cd-0fc7-4580-b77a-ab60be742543")
	require.NoError(t, err)
	require.Equal(t, "ligado", r.Acao)
	p.conferir(t)
	require.Equal(t, "EH-7K4M2X", p.corpo["hubCode"])
	require.Equal(t, "b2adf6cd-0fc7-4580-b77a-ab60be742543", p.corpo["idNaPlataforma"])
}

func TestLigarCliente_409TrazONome(t *testing.T) {
	c, p := hubLigar(t, 409, `{"error":{"code":"id_em_uso","message":"x"},"cliente":"LOJAS RENNER"}`)
	_, err := c.LigarCliente(context.Background(), "EH-7K4M2X", "id")
	he := erroDoHub(t, err)
	p.conferir(t)
	require.Equal(t, http.StatusConflict, he.Status)
	require.Contains(t, he.Message, "LOJAS RENNER")
}

// Sem `cliente` no corpo, a mensagem não pode sair com aspas vazias.
func TestLigarCliente_409SemNomeNaoDeixaAspasVazias(t *testing.T) {
	c, _ := hubLigar(t, 409, `{"error":{"code":"id_em_uso"}}`)
	_, err := c.LigarCliente(context.Background(), "EH-7K4M2X", "id")
	he := erroDoHub(t, err)
	require.Equal(t, http.StatusConflict, he.Status)
	require.Equal(t, "Outro cliente do hub já usa este cliente do E-monitor e coleta dele. Fale com um admin.", he.Message)
}

func TestLigarCliente_OutrosStatusViram503(t *testing.T) {
	for _, st := range []int{401, 404, 429, 500} {
		c, _ := hubLigar(t, st, `{}`)
		_, err := c.LigarCliente(context.Background(), "EH-7K4M2X", "id")
		he := erroDoHub(t, err)
		require.Equal(t, http.StatusServiceUnavailable, he.Status, "status %d", st)
	}
}

// 404 é hub sem a rota (deploy faltando) e 401 é chave recusada: apertar de
// novo não resolve nenhum dos dois, e a mensagem tem de dizer isso.
func TestLigarCliente_404E401DizemParaAvisarOSuporte(t *testing.T) {
	casos := map[int]string{
		404: "o hub ainda não foi atualizado para ligar clientes — avise o suporte",
		401: "a chave do hub foi recusada — avise o suporte",
	}
	for st, msg := range casos {
		c, _ := hubLigar(t, st, `{}`)
		_, err := c.LigarCliente(context.Background(), "EH-7K4M2X", "id")
		he := erroDoHub(t, err)
		require.Equal(t, http.StatusServiceUnavailable, he.Status, "status %d", st)
		require.Equal(t, msg, he.Message, "status %d", st)
	}
}

func TestLigarCliente_SemConfiguracao(t *testing.T) {
	_, err := New("", "").LigarCliente(context.Background(), "EH-7K4M2X", "id")
	require.ErrorIs(t, err, ErrNotConfigured)
}
