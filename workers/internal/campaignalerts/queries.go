// Package campaignalerts seleciona campanhas elegíveis para os 3 emails diários
// de alerta, renderiza o conteúdo e dispara o envio. Ver
// docs/superpowers/specs/2026-06-09-campaign-notification-emails-design.md.
package campaignalerts

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"radiocheck/internal/calendar"
)

// CampaignAlert é a projeção de uma campanha usada nos emails.
type CampaignAlert struct {
	ID            uuid.UUID
	Name          string
	ClientName    string
	StartDate     time.Time
	EndDate       time.Time
	StationCount  int
	MaterialCount int
}

// Repo executa as seleções de campanha.
type Repo struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

// lookaheadDays é a faixa larga buscada no SQL; o refinamento por dias úteis é
// feito em Go via calendar.InAlertWindow. 4 cobre o pior caso (sexta→terça /
// quinta→segunda = 2 dias úteis = 4 dias corridos).
const lookaheadDays = 4

// candidates busca campanhas de um status cuja coluna de data (dateCol) cai na
// faixa [hoje, hoje+lookahead]. dateCol é injetado a partir de constantes
// internas — nunca de input externo.
func (r *Repo) candidates(ctx context.Context, status, dateCol string, today time.Time) ([]CampaignAlert, error) {
	t0 := calendar.Today(today)
	t1 := t0.AddDate(0, 0, lookaheadDays)
	q := fmt.Sprintf(`
		SELECT c.id, c.name, COALESCE(cl.name, ''), c.start_date, c.end_date,
		       COALESCE(array_length(c.target_stations, 1), 0) AS station_count,
		       COALESCE((
		         SELECT COUNT(*)::int FROM campaign_materials cm WHERE cm.campaign_id = c.id
		       ), 0) AS material_count
		FROM campaigns c
		LEFT JOIN clients cl ON cl.id = c.client_id
		WHERE c.status = $1
		  AND c.%s BETWEEN $2 AND $3
		ORDER BY c.%s ASC, c.name ASC`, dateCol, dateCol)
	rows, err := r.pool.Query(ctx, q, status, t0, t1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CampaignAlert
	for rows.Next() {
		var a CampaignAlert
		if err := rows.Scan(&a.ID, &a.Name, &a.ClientName, &a.StartDate, &a.EndDate,
			&a.StationCount, &a.MaterialCount); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// StartingNoMaterial: disparo 1. Campanhas programadas, na janela pelo
// start_date, com zero materiais.
func (r *Repo) StartingNoMaterial(ctx context.Context, today time.Time) ([]CampaignAlert, error) {
	cands, err := r.candidates(ctx, "programada", "start_date", today)
	if err != nil {
		return nil, err
	}
	out := make([]CampaignAlert, 0, len(cands))
	for _, c := range cands {
		if c.MaterialCount == 0 && calendar.InAlertWindow(today, c.StartDate) {
			out = append(out, c)
		}
	}
	return out, nil
}

// Starting: disparo 2. Todas as campanhas programadas na janela pelo start_date
// (com ou sem material).
func (r *Repo) Starting(ctx context.Context, today time.Time) ([]CampaignAlert, error) {
	cands, err := r.candidates(ctx, "programada", "start_date", today)
	if err != nil {
		return nil, err
	}
	out := make([]CampaignAlert, 0, len(cands))
	for _, c := range cands {
		if calendar.InAlertWindow(today, c.StartDate) {
			out = append(out, c)
		}
	}
	return out, nil
}

// Ending: disparo 3. Campanhas ativas na janela pelo end_date.
func (r *Repo) Ending(ctx context.Context, today time.Time) ([]CampaignAlert, error) {
	cands, err := r.candidates(ctx, "ativa", "end_date", today)
	if err != nil {
		return nil, err
	}
	out := make([]CampaignAlert, 0, len(cands))
	for _, c := range cands {
		if calendar.InAlertWindow(today, c.EndDate) {
			out = append(out, c)
		}
	}
	return out, nil
}
