package hub

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConferirCodigoDevolveACampanha(t *testing.T) {
	var chaveRecebida, caminho string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chaveRecebida = r.Header.Get("X-Hub-Platform-Key")
		caminho = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hubCampaignId":"66f0","nome":"Verão 2026","inicio":"2026-12-01",
			"fim":"2027-02-28","cliente":{"id":"66e1","nome":"Rôgga","idNaPlataforma":"a7f3"}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "chave-secreta")
	got, err := c.ConferirCodigo(context.Background(), "EH-7K4M2X")
	if err != nil {
		t.Fatalf("ConferirCodigo: %v", err)
	}
	if got.Nome != "Verão 2026" {
		t.Fatalf("Nome = %q", got.Nome)
	}
	// A ponte: é com este campo que o handler compara o cliente do wizard, sem
	// o hub precisar saber qual cliente o wizard escolheu.
	if got.Cliente.IDNaPlataforma != "a7f3" {
		t.Fatalf("IDNaPlataforma = %q", got.Cliente.IDNaPlataforma)
	}
	if got.Inicio != "2026-12-01" || got.Fim != "2027-02-28" {
		t.Fatalf("período = %q a %q", got.Inicio, got.Fim)
	}
	if caminho != "/api/platform/campaigns/by-code/EH-7K4M2X" {
		t.Fatalf("caminho = %q", caminho)
	}
	if chaveRecebida != "chave-secreta" {
		t.Fatalf("chave = %q, quero a da plataforma", chaveRecebida)
	}
}

/*
⚠️ O código vai NORMALIZADO na URL, e não como a pessoa digitou.

A tabela §10 da spec põe "normaliza antes de mandar" do lado do E-monitor. O hub
também normaliza na entrada, então mandar cru funcionaria hoje — mas é o mesmo
valor que este repositório GRAVA em `campaigns.hub_code`, e ali a §10 exige a
forma canônica. Duas campanhas com "EH-7K4M2X" e "eh7k4m2x" seriam o mesmo
código do ponto de vista do hub e códigos diferentes aqui — e a decisão 5 diz
que dois PIs compartilham um código DE PROPÓSITO, ou seja, agrupar por ele tem
significado.
*/
func TestConferirCodigoMandaAFormaCanonica(t *testing.T) {
	var caminho string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caminho = r.URL.Path
		_, _ = w.Write([]byte(`{"nome":"x","cliente":{}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "chave")
	if _, err := c.ConferirCodigo(context.Background(), "  eh 7k4m2x "); err != nil {
		t.Fatalf("ConferirCodigo: %v", err)
	}
	if !strings.HasSuffix(caminho, "/EH-7K4M2X") {
		t.Fatalf("caminho = %q, quero terminar em /EH-7K4M2X", caminho)
	}
}

// Código que não passa no formato NÃO vira requisição: perguntar ao hub por
// "banana" é gastar uma ida à rede para receber o 404 que dá para dar daqui.
func TestConferirCodigoMalFormadoNaoChamaOHub(t *testing.T) {
	chamou := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chamou = true
	}))
	defer srv.Close()

	c := New(srv.URL, "chave")
	/* ⚠️ "nao-e-codigo" e não "banana": **BANANA É UM CÓDIGO VÁLIDO**. São seis
	   caracteres e todos estão no alfabeto de 30 — foi o que este teste me
	   mostrou na primeira rodada, ao sair com "resposta ilegível" em vez de
	   404. Um corpo de código é qualquer sequência de 6 símbolos permitidos, e
	   palavras curtas em português caem nisso com facilidade. Aqui o texto
	   limpo tem 10 caracteres, que não é 6 nem 8: aí sim não é código. */
	_, err := c.ConferirCodigo(context.Background(), "nao-e-codigo")

	var e *Error
	if !errors.As(err, &e) || e.Status != http.StatusNotFound {
		t.Fatalf("err = %v, quero *Error 404", err)
	}
	if chamou {
		t.Fatal("o hub foi chamado com um código que nem é código")
	}
}

func TestConferirCodigo404Vira404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := New(srv.URL, "chave")
	_, err := c.ConferirCodigo(context.Background(), "EH-ZZZZZZ")

	var e *Error
	if !errors.As(err, &e) || e.Status != http.StatusNotFound {
		t.Fatalf("err = %v, quero um *Error com Status 404", err)
	}
}

/*
⚠️ Hub fora do ar vira 503, e NÃO o 502 que o `Exchange` usa neste mesmo arquivo.

A diferença é de propósito e vem da decisão 2 da spec: hub fora do ar não trava
o cadastro. A tela precisa distinguir "não deu para conferir agora" (âmbar, siga
em frente) de "este código é de outro cliente" (vermelho, pare) — e o handler da
Task 5 só consegue fazer isso se o status disser qual dos dois é. 503 é o mesmo
status do `ErrNotConfigured`, e isso também é certo: instalação sem hub e hub
mudo dão a MESMA tela âmbar.
*/
func TestConferirCodigoHubMudoVira503(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // fechado ANTES de chamar: conexão recusada

	c := New(srv.URL, "chave")
	_, err := c.ConferirCodigo(context.Background(), "EH-7K4M2X")

	var e *Error
	if !errors.As(err, &e) || e.Status != http.StatusServiceUnavailable {
		t.Fatalf("err = %v, quero *Error 503", err)
	}
}

// Qualquer outro status do hub é 502: é resposta que chegou e não dá para usar,
// diferente de resposta que não chegou.
func TestConferirCodigoOutroStatusVira502(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New(srv.URL, "chave")
	_, err := c.ConferirCodigo(context.Background(), "EH-7K4M2X")

	var e *Error
	if !errors.As(err, &e) || e.Status != http.StatusBadGateway {
		t.Fatalf("err = %v, quero *Error 502", err)
	}
}

func TestConferirCodigoRespostaIlegivelVira502(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`isto não é json`))
	}))
	defer srv.Close()

	c := New(srv.URL, "chave")
	_, err := c.ConferirCodigo(context.Background(), "EH-7K4M2X")

	var e *Error
	if !errors.As(err, &e) || e.Status != http.StatusBadGateway {
		t.Fatalf("err = %v, quero *Error 502", err)
	}
}

func TestConferirCodigoSemConfiguracao(t *testing.T) {
	c := New("", "")
	if _, err := c.ConferirCodigo(context.Background(), "EH-7K4M2X"); err != ErrNotConfigured {
		t.Fatalf("err = %v, quero ErrNotConfigured", err)
	}
}

// O `CampanhaUpsert` carrega o código: sem ele o hub IGNORA o evento (§6.1), e
// a campanha simplesmente não entra — sem erro, sem retentativa.
func TestCampanhaUpsertCarregaOHubCode(t *testing.T) {
	e := CampanhaUpsert{IDNaPlataforma: "a", IDClienteNaPlataforma: "b", HubCode: "EH-7K4M2X"}
	if e.HubCode != "EH-7K4M2X" {
		t.Fatalf("HubCode = %q", e.HubCode)
	}
}
