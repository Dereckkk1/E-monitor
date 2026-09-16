package hub

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func ptr(s string) *string { return &s }

func TestEmitir_MandaEnvelopeEChave(t *testing.T) {
	var caminho, chave, corpo string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caminho = r.URL.Path
		chave = r.Header.Get("X-Hub-Platform-Key")
		b, _ := io.ReadAll(r.Body)
		corpo = string(b)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"acao":"criar"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "chave-secreta")
	err := c.Emitir(context.Background(), "cliente.upsert", ClienteUpsert{
		IDNaPlataforma: "abc", Nome: "Acme", Cidade: ptr("Porto Alegre"),
	})
	if err != nil {
		t.Fatalf("esperava sucesso, veio %v", err)
	}
	if caminho != "/api/platform/events" {
		t.Errorf("caminho = %q", caminho)
	}
	if chave != "chave-secreta" {
		t.Errorf("chave = %q", chave)
	}

	var env struct {
		Tipo       string          `json:"tipo"`
		OcorridoEm string          `json:"ocorridoEm"`
		Dados      json.RawMessage `json:"dados"`
	}
	if err := json.Unmarshal([]byte(corpo), &env); err != nil {
		t.Fatalf("corpo ilegível: %v — %s", err, corpo)
	}
	if env.Tipo != "cliente.upsert" {
		t.Errorf("tipo = %q", env.Tipo)
	}
	if _, err := time.Parse(time.RFC3339, env.OcorridoEm); err != nil {
		t.Errorf("ocorridoEm não é RFC3339: %q", env.OcorridoEm)
	}
}

func TestEmitir_CampoAusenteNaoVaiNoCorpo(t *testing.T) {
	var corpo string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		corpo = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, "k")
	if err := c.Emitir(context.Background(), "cliente.upsert",
		ClienteUpsert{IDNaPlataforma: "abc", Nome: "Acme"}); err != nil {
		t.Fatalf("erro: %v", err)
	}

	var env struct {
		Dados map[string]any `json:"dados"`
	}
	_ = json.Unmarshal([]byte(corpo), &env)
	// ⚠️ O par com a regra do hub: ausente tem de estar AUSENTE, não null.
	// Do outro lado, campo presente significa "é este o valor de hoje" — um
	// null ou "" viajando diria que o cliente não tem cidade.
	for _, campo := range []string{"cnpj", "logoUrl", "contatoNome", "contatoEmail", "contatoTelefone", "cidade", "uf"} {
		if _, existe := env.Dados[campo]; existe {
			t.Errorf("campo %q foi enviado mesmo vazio", campo)
		}
	}
}

func TestEmitir_SemConfiguracaoNaoChama(t *testing.T) {
	chamou := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chamou = true
	}))
	defer srv.Close()

	c := New("", "")
	err := c.Emitir(context.Background(), "cliente.upsert", ClienteUpsert{IDNaPlataforma: "a", Nome: "b"})
	if err != ErrNotConfigured {
		t.Errorf("esperava ErrNotConfigured, veio %v", err)
	}
	if chamou {
		t.Error("chamou o servidor sem estar configurado")
	}
}

func TestEmitir_StatusRuimViraErro(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New(srv.URL, "k")
	err := c.Emitir(context.Background(), "cliente.upsert", ClienteUpsert{IDNaPlataforma: "a", Nome: "b"})
	if err == nil {
		t.Fatal("401 tinha de virar erro")
	}
	e, ok := AsError(err)
	if !ok || e.Status != http.StatusUnauthorized {
		t.Errorf("erro = %v", err)
	}
}
