package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"radiocheck/internal/catalog"
	"radiocheck/internal/hub"
)

func s(v string) *string { return &v }

func clienteDeTeste() *catalog.Client {
	return &catalog.Client{
		ID:           uuid.New(),
		Name:         "Acme",
		CNPJ:         s("12345678000190"),
		LogoURL:      s("https://cdn/acme.png"),
		ContactName:  s("Maria"),
		ContactEmail: s("maria@acme.com"),
		Phone:        s("5511999999999"),
		City:         s("Porto Alegre"),
		State:        s("RS"),
	}
}

func TestAvisarHubDoCliente_MandaOsCamposCertos(t *testing.T) {
	recebido := make(chan map[string]any, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var env map[string]any
		_ = json.Unmarshal(b, &env)
		recebido <- env
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := clienteDeTeste()
	avisarHubDoCliente(hub.New(srv.URL, "k"), c)

	select {
	case env := <-recebido:
		if env["tipo"] != "cliente.upsert" {
			t.Errorf("tipo = %v", env["tipo"])
		}
		d, _ := env["dados"].(map[string]any)
		if d["idNaPlataforma"] != c.ID.String() {
			t.Errorf("idNaPlataforma = %v, esperava %s", d["idNaPlataforma"], c.ID)
		}
		if d["nome"] != "Acme" {
			t.Errorf("nome = %v", d["nome"])
		}
		if d["cidade"] != "Porto Alegre" {
			t.Errorf("cidade = %v", d["cidade"])
		}
		if d["uf"] != "RS" {
			t.Errorf("uf = %v", d["uf"])
		}
		// ⚠️ O `Phone` do E-monitor vira `contatoTelefone` no contrato do hub.
		// Os nomes NÃO batem entre os dois lados, e essa é a tradução.
		if d["contatoTelefone"] != "5511999999999" {
			t.Errorf("contatoTelefone = %v", d["contatoTelefone"])
		}
		if d["contatoNome"] != "Maria" {
			t.Errorf("contatoNome = %v", d["contatoNome"])
		}
		if d["contatoEmail"] != "maria@acme.com" {
			t.Errorf("contatoEmail = %v", d["contatoEmail"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("o hub não recebeu nada em 5s")
	}
}

func TestAvisarHubDoCliente_CampoVazioNaoViaja(t *testing.T) {
	recebido := make(chan map[string]any, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var env map[string]any
		_ = json.Unmarshal(b, &env)
		recebido <- env
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Cliente com só o mínimo: todos os ponteiros nil.
	avisarHubDoCliente(hub.New(srv.URL, "k"), &catalog.Client{ID: uuid.New(), Name: "Acme"})

	select {
	case env := <-recebido:
		d, _ := env["dados"].(map[string]any)
		for _, campo := range []string{"cnpj", "logoUrl", "contatoNome", "contatoEmail", "contatoTelefone", "cidade", "uf"} {
			if _, existe := d[campo]; existe {
				t.Errorf("campo %q viajou mesmo nil — do outro lado isso diria 'o valor de hoje é vazio'", campo)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("o hub não recebeu nada em 5s")
	}
}

// ⚠️ O TESTE QUE DEFINE O DESENHO. Se ele ficar vermelho, a escolha mudou: o
// E-monitor passou a depender do hub estar de pé para criar cliente.
func TestAvisarHubDoCliente_NaoBloqueiaQuandoOHubPendura(t *testing.T) {
	/* Aceita a conexão e NUNCA responde — é exatamente isso que o teste mede.

	   ⚠️ O `solta` não é enfeite: `httptest.Server.Close()` ESPERA os handlers
	   em voo terminarem, então um handler parado aqui faz o teste custar o
	   tempo inteiro da espera. E esperar só por `r.Context().Done()` NÃO basta
	   — medido nesta máquina em 2026-09-21: a requisição que o emissor dispara
	   em goroutine não teve o contexto cancelado quando o cliente desistiu, e o
	   pacote passou de 284s para estourar o timeout de 2 min.

	   Os defers são LIFO: o `close(solta)` roda ANTES do `Close()`. */
	solta := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-solta:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(solta)

	comecou := time.Now()
	avisarHubDoCliente(hub.New(srv.URL, "k"), clienteDeTeste())
	demorou := time.Since(comecou)

	if demorou > 500*time.Millisecond {
		t.Errorf("a chamada esperou o hub: %v — tinha de voltar na hora", demorou)
	}
}

func TestAvisarHubDoCliente_HubNilNaoPanica(t *testing.T) {
	avisarHubDoCliente(nil, clienteDeTeste())
}

func TestAvisarHubDoCliente_ClienteNilNaoPanica(t *testing.T) {
	avisarHubDoCliente(hub.New("http://exemplo", "k"), nil)
}

func TestAvisarHubDoCliente_SemConfiguracaoNaoChama(t *testing.T) {
	chamou := make(chan bool, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chamou <- true
	}))
	defer srv.Close()

	avisarHubDoCliente(hub.New("", ""), clienteDeTeste())

	select {
	case <-chamou:
		t.Error("chamou o hub sem estar configurado")
	case <-time.After(300 * time.Millisecond):
		// certo: nada saiu
	}
}
