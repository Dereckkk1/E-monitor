package catalog

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Incidente 2026-11-08 (Positiva 99.1, Goiânia): /admin/station-failures e a
// tool falhas_emissoras mostraram 37,8 h fora do ar num único dia. Os 'down'
// de stream_health_events se SOBREPÕEM (o watchdog abria um novo down com o
// anterior ainda aberto) e a soma crua de duration_seconds contava a mesma
// hora duas vezes; um down que começa às 23h e dura 3 h também somava as 3 h
// no dia em que começou. Tempo fora do ar de um dia = UNIÃO dos intervalos,
// cortada no fim do dia — nunca passa de 24 h.

const dtOverlapDay = "2026-03-10"

// dtSeedStationWithDeficit cria emissora com déficit em dtOverlapDay (regra de
// 3 inserções, nenhuma tocada) — as duas abas só mostram downtime de emissora
// que teve déficit no dia.
func dtSeedStationWithDeficit(t *testing.T, ctx context.Context, pool *pgxpool.Pool, label string) uuid.UUID {
	t.Helper()
	client := insSeedClient(t, ctx, pool, "DT-"+label)
	camp := insSeedCampaign(t, ctx, pool, client, dtOverlapDay, dtOverlapDay)
	typeID, _ := insSeedTypeAndMaterial(t, ctx, pool, client, "DT-"+label)
	st := insSeedStation(t, ctx, pool, "DT-"+label, 1000, 60, 40, 20, 50, 30, 30, 50, 20)
	insSeedDistributionRule(t, ctx, pool, camp, typeID, st, dtOverlapDay, dtOverlapDay, 0b1111111, "08:00", "20:00", 3)
	return st
}

// dtInsertDowns grava os 3 downs do caso, com o início relativo à meia-noite
// do dia no MESMO referencial que a função lida usa (midnightSQL recebe $1 =
// dia e devolve o timestamptz da meia-noite).
func dtInsertDowns(t *testing.T, ctx context.Context, pool *pgxpool.Pool, st uuid.UUID, midnightSQL string) {
	t.Helper()
	downs := []struct {
		offset string
		dur    int
	}{
		{"10 hours", 2 * 3600}, // 10:00–12:00
		{"11 hours", 2 * 3600}, // 11:00–13:00 → sobreposto ao anterior: união 10:00–13:00 = 3 h
		{"23 hours", 3 * 3600}, // 23:00–02:00 do dia seguinte → só 1 h é deste dia
	}
	for _, d := range downs {
		if _, err := pool.Exec(ctx,
			`INSERT INTO stream_health_events (station_id, event_type, event_at, duration_seconds)
			 VALUES ($2, 'down', (`+midnightSQL+`) + $3::interval, $4)`,
			dtOverlapDay, st, d.offset, d.dur); err != nil {
			t.Fatalf("insert down %s: %v", d.offset, err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM stream_health_events WHERE station_id = $1`, st)
	})
}

const dtWantDownSec = 4 * 3600 // 3 h (união 10–13h) + 1 h (23h–meia-noite)

func TestStationFailures_DowntimeIsUnionClippedToDay(t *testing.T) {
	ctx, pool := newTestDB(t)
	st := dtSeedStationWithDeficit(t, ctx, pool, "station")
	// ListForDate recorta o dia com `$1::date` contra timestamptz → fuso da sessão.
	dtInsertDowns(t, ctx, pool, st, `$1::date::timestamptz`)

	day, _ := time.Parse("2006-01-02", dtOverlapDay)
	res, err := NewStationFailures(pool).ListForDate(ctx, day, 60)
	if err != nil {
		t.Fatalf("ListForDate: %v", err)
	}
	for _, s := range res.Stations {
		if s.Station.ID == st {
			if s.TotalDownSeconds != dtWantDownSec {
				t.Fatalf("TotalDownSeconds = %d (%.1f h), quer %d (4 h): downs sobrepostos/além da meia-noite somados crus",
					s.TotalDownSeconds, float64(s.TotalDownSeconds)/3600, dtWantDownSec)
			}
			return
		}
	}
	t.Fatalf("emissora semeada não apareceu (fixture sem déficit?)")
}

func TestFailuresDaily_DowntimeIsUnionClippedToDay(t *testing.T) {
	ctx, pool := newTestDB(t)
	st := dtSeedStationWithDeficit(t, ctx, pool, "daily")
	// ListDaily recorta o dia em America/Sao_Paulo.
	dtInsertDowns(t, ctx, pool, st, `$1::date::timestamp AT TIME ZONE 'America/Sao_Paulo'`)

	day, _ := time.Parse("2006-01-02", dtOverlapDay)
	res, err := NewFailuresDaily(pool).ListDaily(ctx, day, day, 60)
	if err != nil {
		t.Fatalf("ListDaily: %v", err)
	}
	if len(res.Days) != 1 {
		t.Fatalf("len(Days) = %d, quer 1", len(res.Days))
	}
	if got := res.Days[0].DownSeconds; got != dtWantDownSec {
		t.Fatalf("DownSeconds = %d (%.1f h), quer %d (4 h)", got, float64(got)/3600, dtWantDownSec)
	}
}
