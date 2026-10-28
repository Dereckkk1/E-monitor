package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// WebhookDelivery é uma entrega de webhook (migration 0012) SEM payload nem
// response_body: o payload traz dado de detecção e o response_body é conteúdo
// do sistema do cliente — nenhum dos dois sai pela API.
type WebhookDelivery struct {
	ID            uuid.UUID  `json:"id"`
	EventType     string     `json:"event_type"`
	Status        string     `json:"status"`
	AttemptCount  int        `json:"attempt_count"`
	ResponseCode  *int       `json:"response_code,omitempty"`
	LastError     *string    `json:"last_error,omitempty"`
	NextAttemptAt time.Time  `json:"next_attempt_at"`
	DeliveredAt   *time.Time `json:"delivered_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// ListWebhookDeliveries: entregas do cliente, da mais nova para a mais velha.
// status "" = todas. total = quantas existem com o filtro (a tela ignora; o MCP
// pagina com ele). Página além do fim devolve total 0 (COUNT(*) OVER ()).
func (c *Clients) ListWebhookDeliveries(ctx context.Context, clientID uuid.UUID, status string, limit, offset int) ([]WebhookDelivery, int, error) {
	rows, err := c.pool.Query(ctx, `
		SELECT id, event_type, status, attempt_count, response_code, last_error,
		       next_attempt_at, delivered_at, created_at, updated_at, COUNT(*) OVER ()
		FROM webhook_deliveries
		WHERE client_id = $1
		  AND ($2 = '' OR status = $2)
		ORDER BY created_at DESC, id
		LIMIT $3 OFFSET $4`, clientID, status, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]WebhookDelivery, 0)
	total := 0
	for rows.Next() {
		var it WebhookDelivery
		if err := rows.Scan(&it.ID, &it.EventType, &it.Status, &it.AttemptCount,
			&it.ResponseCode, &it.LastError, &it.NextAttemptAt, &it.DeliveredAt,
			&it.CreatedAt, &it.UpdatedAt, &total); err != nil {
			return nil, 0, err
		}
		out = append(out, it)
	}
	return out, total, rows.Err()
}

// APIKeyInfo é o metadado de uma chave de API ativa. KeyMasked são os 8
// primeiros hex do SHA-256 da chave (nunca a chave, que só existe na resposta
// da criação).
type APIKeyInfo struct {
	ID         uuid.UUID
	KeyMasked  string
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

// ListAPIKeys: chaves não revogadas do cliente, da mais nova para a mais velha.
func (c *Clients) ListAPIKeys(ctx context.Context, clientID uuid.UUID) ([]APIKeyInfo, error) {
	rows, err := c.pool.Query(ctx, `
		SELECT id, LEFT(key_hash, 8) || '...', created_at, last_used_at
		FROM api_keys WHERE client_id = $1 AND revoked_at IS NULL
		ORDER BY created_at DESC`, clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIKeyInfo{}
	for rows.Next() {
		var k APIKeyInfo
		if err := rows.Scan(&k.ID, &k.KeyMasked, &k.CreatedAt, &k.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
