package catalog

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// REGRA DE RESOLUÇÃO DO PMM NO TARGET — canônica, vale no sistema inteiro:
//
//	o target de uma linha é client_station_pmm[campanha.client_id, station_id]
//
// O target é uma propriedade do CLIENTE e vale para todas as campanhas dele. Em
// multi-atribuição cada atribuição resolve pelo cliente DELA, que é o
// comportamento correto.
//
// Não há um fragmento SQL compartilhado (ao contrário do ApprovedDetectionsFilter):
// cada consumidor chega na campanha por um alias diferente (`cmp` em detections,
// `cc` em campaigns, a CTE `per_station` em insights), então um literal comum não
// encaixaria em nenhum deles sem renomear queries estáveis. O join é escrito à mão
// em cada lugar, sempre nesta forma:
//
//	LEFT JOIN client_station_pmm cst
//	       ON cst.client_id = <alias da campanha>.client_id
//	      AND cst.station_id = <alias da emissora>.id
//
// Consumidores: insights.go (aggregateCore), campaigns.go (FinancialsByCampaign),
// detections.go (ListPaged, IterateForExport, AggregateByMaterialStation,
// AggregateByStation).

// TargetPMMRow é uma emissora-alvo do cliente com os dois PMMs lado a lado:
// o global da emissora e o do target do cliente (nil quando não cadastrado).
type TargetPMMRow struct {
	StationID    uuid.UUID `json:"station_id"`
	StationShort int32     `json:"short_id"`
	Name         string    `json:"name"`
	Band         *string   `json:"band,omitempty"`
	FrequencyMHz *float64  `json:"frequency_mhz,omitempty"`
	City         *string   `json:"city,omitempty"`
	State        *string   `json:"state,omitempty"`
	PMM          *float64  `json:"pmm,omitempty"`
	PMMTarget    *int      `json:"pmm_target"`
}

// TargetPMMEntry é um item do payload de escrita. PMMTarget nil APAGA a linha
// (volta ao estado "não cadastrado"); 0 grava zero, que é diferente de ausente.
type TargetPMMEntry struct {
	StationID uuid.UUID `json:"station_id"`
	PMMTarget *int      `json:"pmm_target"`
}

type ClientStationPMM struct {
	pool *pgxpool.Pool
}

func NewClientStationPMM(pool *pgxpool.Pool) *ClientStationPMM {
	return &ClientStationPMM{pool: pool}
}

// ListForClient devolve as emissoras-alvo das campanhas do cliente (união dos
// campaigns.target_stations), cada uma com o PMM global e o target já resolvido.
// Emissoras sem cadastro vêm com PMMTarget nil — a tela precisa delas para
// oferecer o input vazio.
//
// Campanhas canceladas entram normalmente: a lista é de cadastro, não uma tela
// operacional (docs/features/cancelled-campaign-handling.md).
func (r *ClientStationPMM) ListForClient(ctx context.Context, clientID uuid.UUID) ([]TargetPMMRow, error) {
	rows, err := r.pool.Query(ctx, `
		WITH alvo AS (
		    SELECT DISTINCT unnest(c.target_stations) AS station_id
		    FROM campaigns c
		    WHERE c.client_id = $1
		)
		SELECT s.id, s.short_id, s.name, s.band, s.frequency_mhz, s.city, s.state,
		       s.pmm, cst.pmm_target
		FROM alvo a
		JOIN stations s ON s.id = a.station_id
		LEFT JOIN client_station_pmm cst
		       ON cst.client_id = $1 AND cst.station_id = s.id
		ORDER BY s.name`, clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]TargetPMMRow, 0)
	for rows.Next() {
		var row TargetPMMRow
		if err := rows.Scan(&row.StationID, &row.StationShort, &row.Name, &row.Band,
			&row.FrequencyMHz, &row.City, &row.State, &row.PMM, &row.PMMTarget); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// BulkUpsert aplica o lote inteiro numa transação: entradas com valor viram
// INSERT ... ON CONFLICT DO UPDATE, entradas com nil viram DELETE. Devolve
// (gravadas, apagadas). Idempotente — reenviar o mesmo lote não muda nada.
func (r *ClientStationPMM) BulkUpsert(ctx context.Context, clientID uuid.UUID, entries []TargetPMMEntry) (int, int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback(ctx)

	var updated, deleted int
	for _, e := range entries {
		if e.PMMTarget == nil {
			tag, err := tx.Exec(ctx,
				`DELETE FROM client_station_pmm WHERE client_id = $1 AND station_id = $2`,
				clientID, e.StationID)
			if err != nil {
				return 0, 0, err
			}
			deleted += int(tag.RowsAffected())
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO client_station_pmm (client_id, station_id, pmm_target)
			VALUES ($1, $2, $3)
			ON CONFLICT (client_id, station_id)
			DO UPDATE SET pmm_target = EXCLUDED.pmm_target`,
			clientID, e.StationID, *e.PMMTarget); err != nil {
			return 0, 0, err
		}
		updated++
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, err
	}
	return updated, deleted, nil
}
