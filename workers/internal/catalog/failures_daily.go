package catalog

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// FailuresDaily — série temporal de falhas por dia, alimentando a aba "Por dia"
// de /admin/station-failures. As outras duas abas respondem "quem falhou HOJE";
// esta responde "em que parte do mês a operação quebra".
//
// A definição de falha é a MESMA das outras abas (docs/features/admin-station-
// failures.md): emissora com deficit > 0 no dia, campanha não-cancelada. Um
// worker travado sem campanha agendada continua não contando. Isso é o que
// permite clicar num dia do gráfico e cair na aba "Por emissora" com o mesmo
// número na tela.
type FailuresDaily struct {
	pool *pgxpool.Pool
}

func NewFailuresDaily(pool *pgxpool.Pool) *FailuresDaily {
	return &FailuresDaily{pool: pool}
}

// DailyPoint é um dia da série. Dias sem falha aparecem com tudo zerado — a
// série é densa de propósito: um gráfico de barras com dias faltando mente
// sobre o padrão do mês (um domingo sem falha vira "não existiu domingo").
type DailyPoint struct {
	Date        string `json:"date"` // YYYY-MM-DD
	Stations    int    `json:"stations"`
	Campaigns   int    `json:"campaigns"`
	Deficit     int    `json:"deficit"`
	DownSeconds int    `json:"down_seconds"`
	DownEvents  int    `json:"down_events"`
}

type FailuresDailySummary struct {
	DaysTotal        int     `json:"days_total"`
	DaysWithFailure  int     `json:"days_with_failure"`
	PeakStations     int     `json:"peak_stations"`
	PeakDate         string  `json:"peak_date"` // "" quando não houve falha alguma
	TotalDeficit     int     `json:"total_deficit"`
	TotalDownSeconds int     `json:"total_down_seconds"`
	AvgStations      float64 `json:"avg_stations"` // média sobre TODOS os dias do range
}

type FailuresDailyResult struct {
	From    string               `json:"from"`
	To      string               `json:"to"`
	Summary FailuresDailySummary `json:"summary"`
	Days    []DailyPoint         `json:"days"`
}

// ListDaily devolve a série [from, to] inclusive, um ponto por dia.
//
// minDownSeconds filtra o downtime por (emissora, dia) antes de somar — igual
// ao ListForDate, pra que "≥ 5 min" signifique a mesma coisa nas duas abas.
func (r *FailuresDaily) ListDaily(ctx context.Context, from, to time.Time, minDownSeconds int) (*FailuresDailyResult, error) {
	fromStr := from.Format("2006-01-02")
	toStr := to.Format("2006-01-02")

	// (dia, emissora) → campanhas afetadas + déficit. Espelha o filtro das
	// outras abas: deficit > 0 e campanha não-cancelada.
	//
	// O recorte por for_date é feito DENTRO da daily_play_summary_for (pushdown
	// da 0052) — não como WHERE por cima. Todo agregado aqui é BETWEEN de dia,
	// então não cai na armadilha do out_date sem lower bound.
	deficitRows, err := r.pool.Query(ctx, `
SELECT dps.for_date::text,
       dps.station_id,
       COUNT(DISTINCT dps.campaign_id)::int AS campaigns,
       SUM(dps.deficit)::int                AS deficit
FROM daily_play_summary_for($1::date, $2::date, NULL) dps
JOIN campaigns c ON c.id = dps.campaign_id
WHERE dps.deficit > 0
  AND c.status <> 'cancelada'
GROUP BY dps.for_date, dps.station_id`,
		fromStr, toStr)
	if err != nil {
		return nil, fmt.Errorf("query 1 deficit por dia: %w", err)
	}
	defer deficitRows.Close()

	acc := map[string]*dayAcc{}
	touch := func(day string) *dayAcc {
		a := acc[day]
		if a == nil {
			a = &dayAcc{stations: map[uuid.UUID]struct{}{}}
			acc[day] = a
		}
		return a
	}

	for deficitRows.Next() {
		var day string
		var sid uuid.UUID
		var campaigns, deficit int
		if err := deficitRows.Scan(&day, &sid, &campaigns, &deficit); err != nil {
			return nil, err
		}
		a := touch(day)
		a.stations[sid] = struct{}{}
		a.campaigns += campaigns
		a.deficit += deficit
	}
	if err := deficitRows.Err(); err != nil {
		return nil, err
	}

	// (dia, emissora) → segundos fora do ar. O HAVING aplica minDownSeconds no
	// total do dia daquela emissora, não por evento — uma emissora que piscou
	// 20× por 5s soma 100s e some com "≥ 5 min", que é a leitura correta.
	//
	// O bound superior é `< to + 1 day` (não `<= to`) pra pegar o dia inteiro.
	downRows, err := r.pool.Query(ctx, `
SELECT (date_trunc('day', event_at AT TIME ZONE 'America/Sao_Paulo'))::date::text AS d,
       station_id,
       SUM(COALESCE(duration_seconds, EXTRACT(EPOCH FROM (NOW() - event_at))::int))::int AS down_sec,
       COUNT(*)::int AS evts
FROM stream_health_events
WHERE event_type = 'down'
  AND event_at >= ($1::date::timestamp AT TIME ZONE 'America/Sao_Paulo')
  AND event_at <  (($2::date + 1)::timestamp AT TIME ZONE 'America/Sao_Paulo')
GROUP BY 1, 2
HAVING SUM(COALESCE(duration_seconds, EXTRACT(EPOCH FROM (NOW() - event_at))::int)) >= $3`,
		fromStr, toStr, minDownSeconds)
	if err != nil {
		return nil, fmt.Errorf("query 2 downtime por dia: %w", err)
	}
	defer downRows.Close()

	for downRows.Next() {
		var day string
		var sid uuid.UUID
		var downSec, evts int
		if err := downRows.Scan(&day, &sid, &downSec, &evts); err != nil {
			return nil, err
		}
		// Só soma downtime de emissora que TEVE déficit naquele dia. Sem isso a
		// métrica "tempo fora do ar" contaria queda sem campanha agendada, que
		// a página inteira decidiu não mostrar (spec 2026-05-25) — o gráfico
		// mostraria pico num dia que a aba "Por emissora" abre vazia.
		a := acc[day]
		if a == nil {
			continue
		}
		if _, ok := a.stations[sid]; !ok {
			continue
		}
		a.downSec += downSec
		a.downEvts += evts
	}
	if err := downRows.Err(); err != nil {
		return nil, err
	}

	return buildDailyResult(from, to, acc2points(acc)), nil
}

// dayAcc acumula um dia enquanto as duas queries são varridas. `stations` é um
// set porque a mesma emissora aparece uma vez por campanha na Q1 — contar linha
// inflaria o número que a aba "Por emissora" mostra como lista.
type dayAcc struct {
	stations  map[uuid.UUID]struct{}
	campaigns int
	deficit   int
	downSec   int
	downEvts  int
}

// acc2points converte o mapa de acumuladores num mapa simples dia→ponto, pra
// que buildDailyResult (puro, testável sem DB) não dependa dos tipos internos.
func acc2points(acc map[string]*dayAcc) map[string]DailyPoint {
	out := make(map[string]DailyPoint, len(acc))
	for day, a := range acc {
		out[day] = DailyPoint{
			Date:        day,
			Stations:    len(a.stations),
			Campaigns:   a.campaigns,
			Deficit:     a.deficit,
			DownSeconds: a.downSec,
			DownEvents:  a.downEvts,
		}
	}
	return out
}

// buildDailyResult preenche todos os dias de [from, to] (inclusive) com o que
// veio do banco, zerando os dias sem falha, e calcula o sumário. Puro — testado
// isoladamente em failures_daily_test.go.
func buildDailyResult(from, to time.Time, byDay map[string]DailyPoint) *FailuresDailyResult {
	res := &FailuresDailyResult{
		From: from.Format("2006-01-02"),
		To:   to.Format("2006-01-02"),
		Days: []DailyPoint{},
	}

	cursor := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, from.Location())
	end := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, to.Location())

	sum := FailuresDailySummary{}
	totalStations := 0
	for !cursor.After(end) {
		key := cursor.Format("2006-01-02")
		p, ok := byDay[key]
		if !ok {
			p = DailyPoint{Date: key}
		}
		p.Date = key // defensivo: a chave manda, não o que veio no struct
		res.Days = append(res.Days, p)

		sum.DaysTotal++
		totalStations += p.Stations
		sum.TotalDeficit += p.Deficit
		sum.TotalDownSeconds += p.DownSeconds
		if p.Stations > 0 || p.Deficit > 0 {
			sum.DaysWithFailure++
		}
		// Empate resolve pelo dia mais antigo (`>` estrito) — determinístico.
		if p.Stations > sum.PeakStations {
			sum.PeakStations = p.Stations
			sum.PeakDate = key
		}
		cursor = cursor.AddDate(0, 0, 1)
	}
	if sum.DaysTotal > 0 {
		sum.AvgStations = float64(totalStations) / float64(sum.DaysTotal)
	}
	res.Summary = sum
	return res
}
