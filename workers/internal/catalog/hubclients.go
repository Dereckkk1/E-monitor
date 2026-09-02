package catalog

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrHubClientNaoLigado: o hub nomeou um cliente que não tem ponte com este
// tenant. É o mesmo desfecho para "não existe" e para "existe mas ninguém
// mapeou": distinguir os dois contaria a quem sonda qual id é real.
var ErrHubClientNaoLigado = errors.New("hub client not linked")

// HubClients resolve o tenant local a partir do id que o CLIENTE tem NO HUB.
//
// A direção importa (decisão do Dereck, 2026-09-02, contra a redação da spec
// §5.3.1): o hub manda o id que ele próprio possui, e o mapa `hub_id -> id`
// mora AQUI. Assim não existe id que o hub possa apresentar para ler um cliente
// que não é dele — mesmo de posse de uma chave de plataforma válida.
//
// É a mesma ponte de `HubSSOHandler.clientePorHubID` (api/handlers/hubsso.go),
// preenchida pelo sync `client.upsert` da Fase 3.
type HubClients struct {
	pool *pgxpool.Pool
}

func NewHubClients(pool *pgxpool.Pool) *HubClients {
	return &HubClients{pool: pool}
}

func (h *HubClients) LocalPorHubID(ctx context.Context, hubClientID string) (uuid.UUID, error) {
	// Antes da query: `WHERE hub_id = ''` casaria com qualquer linha de hub_id
	// vazio, e um header ausente viraria acesso a um tenant.
	if hubClientID == "" || h == nil || h.pool == nil {
		return uuid.Nil, ErrHubClientNaoLigado
	}
	var id uuid.UUID
	err := h.pool.QueryRow(ctx,
		`SELECT id FROM clients WHERE hub_id = $1`, hubClientID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrHubClientNaoLigado
	}
	if err != nil {
		return uuid.Nil, err
	}
	// uuid.Nil nunca é um cliente real (clients.id é uuid_generate_v4), e é a
	// sentinela de falha-fechada de ClientScopesFromContext. Barrar aqui evita
	// que uma linha corrompida vire escopo.
	if id == uuid.Nil {
		return uuid.Nil, ErrHubClientNaoLigado
	}
	return id, nil
}
