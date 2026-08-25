package assertiveness

import (
	"context"
	"time"

	"github.com/google/uuid"

	"radiocheck/internal/calendar"
)

// Summary é a leitura do card: um mês fechado, opcionalmente escopado por
// cliente/campanhas.
//
// Pct é ponteiro de propósito: sem nenhuma veiculação no período o percentual
// não existe (não é 0%, não é 100%). A UI mostra "—" e não um número inventado.
type Summary struct {
	Month         string   `json:"month"` // "2026-07"
	Auto          int64    `json:"auto"`
	ManualTotal   int64    `json:"manual_total"`
	Miss          int64    `json:"miss"`
	Duplicate     int64    `json:"x_duplicate"`
	NoFingerprint int64    `json:"x_no_fingerprint"`
	StreamDown    int64    `json:"x_stream_down"`
	Unmonitored   int64    `json:"x_unmonitored"`
	Pct           *float64 `json:"pct"`
}

// Result é o que o card consome: o mês fechado + o anterior, pra tendência.
type Result struct {
	Current  Summary  `json:"current"`
	Previous *Summary `json:"previous,omitempty"`
	// DeltaPP é a variação em pontos percentuais vs o mês anterior. nil quando
	// falta base de comparação (mês anterior sem veiculação nenhuma).
	DeltaPP *float64 `json:"delta_pp,omitempty"`
}

type Filter struct {
	ClientID    *uuid.UUID
	CampaignIDs []uuid.UUID
}

// ClosedMonth devolve o primeiro e o último dia do mês fechado anterior a now
// (horário de Brasília). O card usa SEMPRE mês fechado, ignorando o filtro de
// período da tela: no dia 3 do mês corrente quase nenhuma manual daquele mês
// foi digitada ainda — a emissora manda o comprovante depois —, então o
// percentual do mês em curso é sempre otimista e desaba no fim do mês.
func ClosedMonth(now time.Time) (from, to time.Time) {
	local := now.In(calendar.BR)
	firstOfThis := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, calendar.BR)
	from = firstOfThis.AddDate(0, -1, 0)
	to = firstOfThis.AddDate(0, 0, -1)
	return from, to
}

// Get devolve o mês fechado anterior a now e o mês antes dele (tendência).
func (r *Repo) Get(ctx context.Context, now time.Time, f Filter) (Result, error) {
	var res Result

	curFrom, curTo := ClosedMonth(now)
	cur, err := r.summarize(ctx, curFrom, curTo, f)
	if err != nil {
		return res, err
	}
	res.Current = cur

	prevFrom := curFrom.AddDate(0, -1, 0)
	prevTo := curFrom.AddDate(0, 0, -1)
	prev, err := r.summarize(ctx, prevFrom, prevTo, f)
	if err != nil {
		return res, err
	}
	res.Previous = &prev

	if cur.Pct != nil && prev.Pct != nil {
		d := *cur.Pct - *prev.Pct
		res.DeltaPP = &d
	}
	return res, nil
}

func (r *Repo) summarize(ctx context.Context, from, to time.Time, f Filter) (Summary, error) {
	s := Summary{Month: from.Format("2006-01")}

	ids := f.CampaignIDs
	if ids == nil {
		ids = []uuid.UUID{}
	}
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(a.auto), 0)::bigint,
		       COALESCE(SUM(a.miss), 0)::bigint,
		       COALESCE(SUM(a.x_duplicate), 0)::bigint,
		       COALESCE(SUM(a.x_no_fingerprint), 0)::bigint,
		       COALESCE(SUM(a.x_stream_down), 0)::bigint,
		       COALESCE(SUM(a.x_unmonitored), 0)::bigint
		FROM assertiveness_daily a
		WHERE a.for_date >= $1::date AND a.for_date <= $2::date
		  AND ($3::uuid IS NULL OR EXISTS (
		        SELECT 1 FROM campaigns c
		         WHERE c.id = a.campaign_id AND c.client_id = $3))
		  AND ($4::uuid[] = '{}' OR a.campaign_id = ANY($4))`,
		from, to, f.ClientID, ids,
	).Scan(&s.Auto, &s.Miss, &s.Duplicate, &s.NoFingerprint, &s.StreamDown, &s.Unmonitored)
	if err != nil {
		return s, err
	}

	s.ManualTotal = s.Miss + s.Duplicate + s.NoFingerprint + s.StreamDown + s.Unmonitored
	if den := s.Auto + s.Miss; den > 0 {
		pct := 100 * float64(s.Auto) / float64(den)
		s.Pct = &pct
	}
	return s, nil
}
