package campaignalerts

import (
	"context"
	"sort"
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
	Day         time.Time // dia civil (meia-noite UTC, convenção do calendar)
	Down        time.Duration
}

type interval struct{ start, end time.Time }

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
		SELECT e.station_id, st.name,
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

	type stationKey struct {
		id   uuid.UUID
		name string
	}
	raw := map[stationKey][]interval{}
	for rows.Next() {
		var k stationKey
		var iv interval
		if err := rows.Scan(&k.id, &k.name, &iv.start, &iv.end); err != nil {
			return nil, err
		}
		if iv.end.After(iv.start) {
			raw[k] = append(raw[k], iv)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []StationOutage
	for k, ivs := range raw {
		perDay := map[time.Time]time.Duration{}
		for _, iv := range mergeIntervals(ivs) {
			for _, p := range splitByCivilDay(iv) {
				perDay[p.day] += p.iv.end.Sub(p.iv.start)
			}
		}
		for day, down := range perDay {
			if down >= OfflineThreshold {
				out = append(out, StationOutage{StationID: k.id, StationName: k.name, Day: day, Down: down})
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
