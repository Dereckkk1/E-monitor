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
	avisarHubDaCampanha(hub.New(srv.URL, "k"), c)

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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(30 * time.Second) // aceita a conexão e nunca responde
	}))
	defer srv.Close()

	comecou := time.Now()
	avisarHubDaCampanha(hub.New(srv.URL, "k"), campanhaDeTeste())
	demorou := time.Since(comecou)

	if demorou > 500*time.Millisecond {
		t.Errorf("a chamada esperou o hub: %v — tinha de voltar na hora", demorou)
	}
}

func TestAvisarHubDaCampanha_HubNilNaoPanica(t *testing.T) {
	avisarHubDaCampanha(nil, campanhaDeTeste())
}

func TestAvisarHubDaCampanha_CampanhaNilNaoPanica(t *testing.T) {
	avisarHubDaCampanha(hub.New("http://exemplo", "k"), nil)
}

func TestAvisarHubDaCampanha_SemConfiguracaoNaoChama(t *testing.T) {
	chamou := make(chan bool, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chamou <- true
	}))
	defer srv.Close()

	avisarHubDaCampanha(hub.New("", ""), campanhaDeTeste())

	select {
	case <-chamou:
		t.Error("chamou o hub sem estar configurado")
	case <-time.After(300 * time.Millisecond):
		// certo: nada saiu
	}
}
