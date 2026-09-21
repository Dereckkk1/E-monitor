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

func campanhaDeTeste() *catalog.Campaign {
	return &catalog.Campaign{
		ID:       uuid.New(),
		ClientID: uuid.New(),
		Name:     "Campanha Acme",
	}
}

func TestAvisarHubDaCampanha_MandaOsCamposCertos(t *testing.T) {
	recebido := make(chan map[string]any, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var env map[string]any
		_ = json.Unmarshal(b, &env)
		recebido <- env
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := campanhaDeTeste()
	avisarHubDaCampanha(hub.New(srv.URL, "k"), nil, c)

	select {
	case env := <-recebido:
		if env["tipo"] != "campanha.upsert" {
			t.Errorf("tipo = %v", env["tipo"])
		}
		d, _ := env["dados"].(map[string]any)
		if d["idNaPlataforma"] != c.ID.String() {
			t.Errorf("idNaPlataforma = %v, esperava %s", d["idNaPlataforma"], c.ID)
		}
		if d["idClienteNaPlataforma"] != c.ClientID.String() {
			t.Errorf("idClienteNaPlataforma = %v, esperava %s", d["idClienteNaPlataforma"], c.ClientID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("o hub não recebeu nada em 5s")
	}
}

// ⚠️ O TESTE QUE DEFINE O DESENHO. Se ele ficar vermelho, a escolha mudou: o
// E-monitor passou a depender do hub estar de pé para criar campanha.
func TestAvisarHubDaCampanha_NaoBloqueiaQuandoOHubPendura(t *testing.T) {
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
	avisarHubDaCampanha(hub.New(srv.URL, "k"), nil, campanhaDeTeste())
	demorou := time.Since(comecou)

	if demorou > 500*time.Millisecond {
		t.Errorf("a chamada esperou o hub: %v — tinha de voltar na hora", demorou)
	}
}

func TestAvisarHubDaCampanha_HubNilNaoPanica(t *testing.T) {
	avisarHubDaCampanha(nil, nil, campanhaDeTeste())
}

func TestAvisarHubDaCampanha_CampanhaNilNaoPanica(t *testing.T) {
	avisarHubDaCampanha(hub.New("http://exemplo", "k"), nil, nil)
}

func TestAvisarHubDaCampanha_SemConfiguracaoNaoChama(t *testing.T) {
	chamou := make(chan bool, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chamou <- true
	}))
	defer srv.Close()

	avisarHubDaCampanha(hub.New("", ""), nil, campanhaDeTeste())

	select {
	case <-chamou:
		t.Error("chamou o hub sem estar configurado")
	case <-time.After(300 * time.Millisecond):
		// certo: nada saiu
	}
}
