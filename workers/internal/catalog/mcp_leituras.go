package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Leituras que só o MCP usa hoje (spec 2026-10-27 §6 Fatia 1). São leituras
// de coluna, sem regra de negócio: a tela não as tem porque não mostra o dado
// (ou o mostra por outro caminho).

// HubIDs devolve o hub_id (ponte com a Central de Clientes, migration 0068)
// dos clientes pedidos. Cliente sem ponte não aparece no mapa.
func (c *Clients) HubIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	out := make(map[uuid.UUID]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := c.pool.Query(ctx,
		`SELECT id, hub_id FROM clients WHERE id = ANY($1) AND hub_id IS NOT NULL`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var hub string
		if err := rows.Scan(&id, &hub); err != nil {
			return nil, err
		}
		out[id] = hub
	}
	return out, rows.Err()
}

// ListByHubCode devolve as campanhas com este código do hub, da mais nova
// para a mais velha. O código NÃO é único (dois PIs podem dividi-lo — decisão
// 5 da spec do hub 2026-09-18), por isso é lista. code tem de vir canônico
// (hub.NormalizaCodigo), como é gravado.
func (c *Campaigns) ListByHubCode(ctx context.Context, code string) ([]Campaign, error) {
	rows, err := c.pool.Query(ctx, `
		SELECT id, client_id, name, start_date, end_date, status, target_stations,
		       fixed_cpm, hub_code, hub_notified_at, created_at, updated_at
		FROM campaigns WHERE hub_code = $1
		ORDER BY start_date DESC, id`, code)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Campaign
	for rows.Next() {
		var camp Campaign
		if err := rows.Scan(&camp.ID, &camp.ClientID, &camp.Name, &camp.StartDate, &camp.EndDate,
			&camp.Status, &camp.TargetStations, &camp.FixedCPM, &camp.HubCode, &camp.HubNotifiedAt,
			&camp.CreatedAt, &camp.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, camp)
	}
	return out, rows.Err()
}

// CancelledAtFor devolve cancelled_at (migration 0044) das campanhas
// canceladas entre ids. Campaign não carrega a coluna; quem precisa dizer
// "déficit congelado em" pergunta aqui.
func (c *Campaigns) CancelledAtFor(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]time.Time, error) {
	out := map[uuid.UUID]time.Time{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := c.pool.Query(ctx, `
		SELECT id, cancelled_at FROM campaigns
		WHERE id = ANY($1) AND status = 'cancelada' AND cancelled_at IS NOT NULL`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var em time.Time
		if err := rows.Scan(&id, &em); err != nil {
			return nil, err
		}
		out[id] = em
	}
	return out, rows.Err()
}
