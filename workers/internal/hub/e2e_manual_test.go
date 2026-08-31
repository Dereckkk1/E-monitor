package hub

import (
	"context"
	"os"
	"testing"
)

// TestExchange_ContraHubReal troca um código EMITIDO PELO HUB DE VERDADE.
//
// É o único teste que prova a costura que mais pode quebrar aqui: o hub é
// Node/MongoDB e este cliente é Go. Um `hubUserId` que o hub serializa como
// ObjectId, um `client: null` que vira zero-value em vez de ponteiro nil, um
// campo renomeado — nada disso aparece num httptest.Server escrito por quem
// escreveu o parser.
//
// Pula por padrão. Para rodar, com o hub no ar:
//
//	HUB_E2E_URL=http://localhost:3010 HUB_E2E_KEY=pk_... HUB_E2E_CODE=hs_... \
//	  go test ./internal/hub -run ContraHubReal -v
func TestExchange_ContraHubReal(t *testing.T) {
	url, key, code := os.Getenv("HUB_E2E_URL"), os.Getenv("HUB_E2E_KEY"), os.Getenv("HUB_E2E_CODE")
	if url == "" || key == "" || code == "" {
		t.Skip("HUB_E2E_URL/HUB_E2E_KEY/HUB_E2E_CODE não setados")
	}

	p, err := New(url, key).Exchange(context.Background(), code)
	if err != nil {
		t.Fatalf("exchange falhou: %v", err)
	}

	if p.HubUserID == "" {
		t.Error("hubUserId veio vazio — o campo do hub não casou com a tag do struct")
	}
	if p.Email == "" {
		t.Error("email veio vazio")
	}
	if p.Level != "internal" && p.Level != "client" {
		t.Errorf("level = %q, esperado internal|client", p.Level)
	}
	if p.Level == "client" {
		if p.Client == nil {
			t.Fatal("level=client sem bloco client — o JIT não teria como resolver o tenant")
		}
		// O campo que o JIT REALMENTE usa. Uma tag errada aqui não quebraria
		// nada visível: viria "" e a busca por clients.hub_id procuraria string
		// vazia, achando nada — e o sintoma seria `client_not_provisioned` num
		// cliente perfeitamente mapeado.
		if p.Client.HubClientID == "" {
			t.Error("client.hubClientId veio vazio — a tag do struct não casou com o hub")
		}
		if p.Client.Name == "" {
			t.Error("client.name veio vazio")
		}
	}
	if p.Level == "internal" && p.Client != nil {
		t.Error("level=internal com bloco client")
	}
	t.Logf("OK — hubUserId=%s level=%s client=%v phone=%v externalId=%v",
		p.HubUserID, p.Level, p.Client != nil, p.Phone != nil, p.ExternalID)
}
