package campaignalerts

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"radiocheck/internal/calendar"
)

// OfflineThreshold é o mínimo de downtime acumulado num dia civil para a
// emissora entrar no email de "emissoras fora do ar".
const OfflineThreshold = 2 * time.Hour

// StationOutage é uma linha do email: emissora X ficou Down horas no dia Day.
type StationOutage struct {
	StationID   uuid.UUID
	StationName string
	Dial        string // ex.: "FM 103,9" (band + frequency_mhz; vazio se não cadastrado)
	Location    string // ex.: "Curitiba/PR" (city + state; vazio se não cadastrado)
	Campaigns   string // campanhas ativas que monitoram a emissora, "a, b, c"
	Day         time.Time // dia civil (meia-noite UTC, convenção do calendar)
	Down        time.Duration
}

type interval struct{ start, end time.Time }

// formatDial monta "FM 103,9" a partir de band + frequency_mhz (decimal com
// vírgula, padrão brasileiro). Campos ausentes degradam graciosamente.
func formatDial(band *string, freqMHz *float64) string {
	b := ""
	if band != nil {
		b = strings.ToUpper(strings.TrimSpace(*band))
	}
	if freqMHz == nil {
		return b
	}
	f := strings.ReplaceAll(strconv.FormatFloat(*freqMHz, 'f', 1, 64), ".", ",")
	if b == "" {
		return f
	}
	return b + " " + f
}

// formatLocation monta "Cidade/UF"; degrada pra só cidade ou só UF.
func formatLocation(city, state *string) string {
	c, s := "", ""
	if city != nil {
		c = strings.TrimSpace(*city)
	}
	if state != nil {
		s = strings.TrimSpace(*state)
	}
	switch {
	case c != "" && s != "":
		return c + "/" + s
	case c != "":
		return c
	default:
		return s
	}
}

// mergeIntervals funde intervalos sobrepostos/aninhados (entrada em qualquer
// ordem). O histórico real de stream_health_events tem downs que se sobrepõem
// (incidente 2026-06-12, Jovem Pan) — somar cru contaria dobrado.
func mergeIntervals(in []interval) []interval {
	if len(in) == 0 {
		return nil
	}
	sorted := make([]interval, len(in))
	copy(sorted, in)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].start.Before(sorted[j].start) })
	out := []interval{sorted[0]}
	for _, iv := range sorted[1:] {
		last := &out[len(out)-1]
		if !iv.start.After(last.end) {
			if iv.end.After(last.end) {
				last.end = iv.end
			}
			continue
		}
		out = append(out, iv)
	}
	return out
}

type dayPart struct {
	day time.Time // dia civil (convenção calendar.CivilDate)
	iv  interval
}

// splitByCivilDay fatia um intervalo nos limites de meia-noite BRT, devolvendo
// uma parte por dia civil tocado — um down que atravessa a meia-noite conta
// nas duas datas, cada uma com sua fração.
func splitByCivilDay(iv interval) []dayPart {
	var out []dayPart
	cur := iv.start
	for cur.Before(iv.end) {
		day := calendar.CivilDate(cur.In(calendar.BR))
		nextMidnight := calendar.BRMidnight(day).AddDate(0, 0, 1)
		end := iv.end
		if nextMidnight.Before(end) {
			end = nextMidnight
		}
		out = append(out, dayPart{day: day, iv: interval{cur, end}})
		cur = end
	}
	return out
}

// StationsOffline retorna, por (emissora, dia civil), o downtime acumulado em
// [windowStart, windowEnd) — instantes reais — filtrado por OfflineThreshold.
// Eventos 'down' ainda abertos (duration NULL) contam até now().
func (r *Repo) StationsOffline(ctx context.Context, windowStart, windowEnd time.Time) ([]StationOutage, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT e.station_id, st.name, st.band, st.frequency_mhz, st.city, st.state,
		       COALESCE((
		         SELECT string_agg(c.name, ', ' ORDER BY c.name)
		         FROM campaigns c
		         WHERE c.status = 'ativa' AND e.station_id = ANY(c.target_stations)
		       ), '') AS campaigns,
		       GREATEST(e.event_at, $1::timestamptz) AS s,
		       LEAST(COALESCE(e.event_at + make_interval(secs => e.duration_seconds), now()), $2::timestamptz) AS f
		FROM stream_health_events e
		JOIN stations st ON st.id = e.station_id
		WHERE e.event_type = 'down'
		  AND e.event_at < $2
		  AND COALESCE(e.event_at + make_interval(secs => e.duration_seconds), now()) > $1`,
		windowStart, windowEnd)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type stationMeta struct {
		name, dial, location, campaigns string
	}
	metas := map[uuid.UUID]stationMeta{}
	raw := map[uuid.UUID][]interval{}
	for rows.Next() {
		var id uuid.UUID
		var name, campaigns string
		var band *string
		var freq *float64
		var city, state *string
		var iv interval
		if err := rows.Scan(&id, &name, &band, &freq, &city, &state, &campaigns, &iv.start, &iv.end); err != nil {
			return nil, err
		}
		metas[id] = stationMeta{
			name:      name,
			dial:      formatDial(band, freq),
			location:  formatLocation(city, state),
			campaigns: campaigns,
		}
		if iv.end.After(iv.start) {
			raw[id] = append(raw[id], iv)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []StationOutage
	for id, ivs := range raw {
		perDay := map[time.Time]time.Duration{}
		for _, iv := range mergeIntervals(ivs) {
			for _, p := range splitByCivilDay(iv) {
				perDay[p.day] += p.iv.end.Sub(p.iv.start)
			}
		}
		m := metas[id]
		for day, down := range perDay {
			if down >= OfflineThreshold {
				out = append(out, StationOutage{
					StationID: id, StationName: m.name, Dial: m.dial,
					Location: m.location, Campaigns: m.campaigns,
					Day: day, Down: down,
				})
			}
		}
	}
	// Ordena: mais downtime primeiro; empate por nome+dia pra estabilidade.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Down != out[j].Down {
			return out[i].Down > out[j].Down
		}
		if out[i].StationName != out[j].StationName {
			return out[i].StationName < out[j].StationName
		}
		return out[i].Day.Before(out[j].Day)
	})
	return out, nil
}
