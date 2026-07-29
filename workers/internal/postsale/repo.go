package postsale

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"radiocheck/internal/catalog"
)

// Repo é o acesso às três tabelas de pós-venda e às views que alimentam o
// Checking. Sem estado além do pool.
type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

// StationRows agrega daily_play_summary por emissora no período do pós-venda e
// classifica cada linha.
//
// O recorte é `for_date BETWEEN from AND to`: veiculações out_date fora da
// janela ficam de fora, o que é correto aqui — o pós-venda fala de um período
// declarado, não da campanha inteira.
//
// Emissora sem plano e sem tocada no período simplesmente não aparece (a view
// não emite linha), então o total de emissoras do bloco é o tamanho deste
// retorno.
func (r *Repo) StationRows(ctx context.Context, campaignID uuid.UUID, from, to time.Time) ([]StationRow, error) {
	rows, err := r.pool.Query(ctx, `
SELECT s.id, s.name, s.city, s.state, s.band, s.frequency_mhz, s.logo_url,
       COALESCE(SUM(dps.expected), 0)::int AS programmed,
       COALESCE(SUM(dps.in_slot), 0)::int  AS identified,
       COALESCE(SUM(dps.deficit), 0)::int  AS deficit,
       COALESCE(SUM(dps.out_slot + dps.out_date + dps.bonus), 0)::int AS extras,
       COALESCE(SUM(dps.bonus), 0)::int    AS bonus_count
  FROM daily_play_summary dps
  JOIN stations s ON s.id = dps.station_id
 WHERE dps.campaign_id = $1
   AND dps.for_date BETWEEN $2::date AND $3::date
 GROUP BY s.id, s.name, s.city, s.state, s.band, s.frequency_mhz, s.logo_url
 ORDER BY s.name`, campaignID, from, to)
	if err != nil {
		return nil, fmt.Errorf("postsale: station rows: %w", err)
	}
	defer rows.Close()

	var out []StationRow
	for rows.Next() {
		var sr StationRow
		if err := rows.Scan(&sr.StationID, &sr.Name, &sr.City, &sr.State,
			&sr.Band, &sr.FrequencyMHz, &sr.LogoURL,
			&sr.Programmed, &sr.Identified, &sr.Deficit, &sr.Extras, &sr.BonusCount); err != nil {
			return nil, err
		}
		sr.Kind = Classify(sr.Deficit, sr.Extras)
		sr.DeliveryPct = DeliveryPct(sr.Programmed, sr.Identified)
		sr.Compensated = catalog.IsBonified(sr.Deficit, sr.Extras)
		out = append(out, sr)
	}
	return out, rows.Err()
}
