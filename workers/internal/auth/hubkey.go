package auth

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
)

// ErrHubClientNaoLigado espelha `catalog.ErrHubClientNaoLigado`. Declarado aqui
// para o pacote `auth` não importar `catalog` (que importa pgxpool): o
// middleware é lógica de autenticação e precisa ser testável sem banco — um
// teste de segurança que PULA sem TEST_DATABASE_URL é um teste que some.
// `catalog` faz o alias no sentido contrário.
var ErrHubClientNaoLigado = errors.New("hub client not linked")

// HubKeyChecker é `*hub.Client`. Interface para o teste usar um fake.
type HubKeyChecker interface {
	ChaveConfere(apresentada string) bool
}

// HubClientResolver é `*catalog.HubClients`.
type HubClientResolver interface {
	LocalPorHubID(ctx context.Context, hubClientID string) (uuid.UUID, error)
}

// RequireHubKeyScoped autentica o HUB e devolve a ele o escopo de UM cliente.
//
// É o análogo servidor-a-servidor do `APIKeyViewerScope`: em vez de traduzir a
// api_key num viewer de um cliente só, traduz a chave de plataforma + o id do
// cliente NO HUB. A partir daqui os handlers existentes (`Insights.Get`,
// `Campaigns.Get`, `Detections.DailySummary`) resolvem o tenant por
// `ClientScopesFromContext` e recusam campanha alheia com 403/404 — sem uma
// linha de fórmula duplicada. Spec 2026-09-02 §5.3.1.
//
// A ordem das checagens é o desenho, não estilo:
//
//  1. a CHAVE primeiro, antes de ler header, banco ou corpo. Mesma ordem de
//     `HubSync.Receive`: quem não se autenticou não merece nem parsing, e
//     responder 400/403 antes de 401 conta a quem sonda o que existe aqui.
//  2. o header do cliente, que é 400 — falta de argumento, não de permissão.
//  3. a resolução, que é 403 `client_not_linked`.
//
// Falha-fechada em TODOS os caminhos: nunca chama `next` sem escopo. `scopes ==
// nil` é "vê tudo" neste sistema (é como admin passa), então um `next` no ramo
// de erro seria a BOLA do audit de 2026-07-21 outra vez.
func RequireHubKeyScoped(hc HubKeyChecker, res HubClientResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if hc == nil || !hc.ChaveConfere(r.Header.Get("X-Hub-Platform-Key")) {
				http.Error(w, "invalid_platform_key", http.StatusUnauthorized)
				return
			}
			hubClientID := r.Header.Get("X-Hub-Client-Id")
			if hubClientID == "" {
				http.Error(w, "missing_hub_client_id", http.StatusBadRequest)
				return
			}
			if res == nil {
				http.Error(w, "hub_not_configured", http.StatusServiceUnavailable)
				return
			}
			local, err := res.LocalPorHubID(r.Context(), hubClientID)
			if errors.Is(err, ErrHubClientNaoLigado) {
				http.Error(w, "client_not_linked", http.StatusForbidden)
				return
			}
			if err != nil {
				// Banco fora do ar não é "sem escopo": é indisponibilidade.
				http.Error(w, "unavailable", http.StatusServiceUnavailable)
				return
			}
			ctx := ContextWithClaims(r.Context(), &Claims{
				Role: "viewer", ClientID: &local, ClientIDs: []uuid.UUID{local},
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
