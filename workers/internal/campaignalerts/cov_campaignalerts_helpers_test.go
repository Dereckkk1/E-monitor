package campaignalerts

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/calendar"
	"radiocheck/internal/users"
)

// covZSentMail e um email capturado pelo mailer falso.
type covZSentMail struct {
	To      []string
	Subject string
	HTML    string
	Text    string
}

// covZMailer implementa mailer.Mailer em memoria. failFirst faz as N primeiras
// chamadas devolverem erro; failFor faz um destinatario especifico sempre falhar.
type covZMailer struct {
	mu        sync.Mutex
	sent      []covZSentMail
	calls     int
	failFirst int
	failFor   map[string]bool
	onSend    func() // chamado a cada Send (antes de decidir erro)
}

func (m *covZMailer) Send(_ context.Context, to []string, subject, html, text string) error {
	m.mu.Lock()
	m.calls++
	calls := m.calls
	fail := calls <= m.failFirst
	for _, addr := range to {
		if m.failFor[addr] {
			fail = true
		}
	}
	onSend := m.onSend
	m.mu.Unlock()
	if onSend != nil {
		onSend()
	}
	if fail {
		return errors.New("smtp indisponivel (teste)")
	}
	m.mu.Lock()
	m.sent = append(m.sent, covZSentMail{To: to, Subject: subject, HTML: html, Text: text})
	m.mu.Unlock()
	return nil
}

func (m *covZMailer) Sent() []covZSentMail {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]covZSentMail, len(m.sent))
	copy(out, m.sent)
	return out
}

// covZLister implementa recipientLister.
type covZLister struct {
	list []users.User
	err  error
}

func (l covZLister) ActiveInternal(context.Context) ([]users.User, error) { return l.list, l.err }

// covZFutureWeekday devolve o primeiro dia civil (meia-noite UTC) com o
// weekday pedido a pelo menos minDays dias de hoje. Datas futuras evitam
// colisao com dados reais e caem nas particoes mensais ja criadas.
func covZFutureWeekday(wd time.Weekday, minDays int) time.Time {
	d := calendar.Today(time.Now()).AddDate(0, 0, minDays)
	for d.Weekday() != wd {
		d = d.AddDate(0, 0, 1)
	}
	return d
}

func covZDate(d time.Time) string { return d.Format("2006-01-02") }

// covZStation cria uma emissora de teste e agenda a limpeza dela e dos eventos.
func covZStation(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name, band string, freq float64, city, state string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO stations (name, band, frequency_mhz, city, state, stream_url)
		 VALUES ($1,$2,$3,$4,$5,'http://127.0.0.1:1/teste') RETURNING id`,
		name, band, freq, city, state).Scan(&id))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM stream_health_events WHERE station_id=$1`, id)
		_, _ = pool.Exec(context.Background(), `DELETE FROM stations WHERE id=$1`, id)
	})
	return id
}

// covZEvent insere um stream_health_event (dur<0 = duration_seconds NULL).
func covZEvent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, station uuid.UUID, typ string, at time.Time, durSecs int) {
	t.Helper()
	var dur any
	if durSecs >= 0 {
		dur = durSecs
	}
	_, err := pool.Exec(ctx,
		`INSERT INTO stream_health_events (station_id, event_type, event_at, duration_seconds) VALUES ($1,$2,$3,$4)`,
		station, typ, at, dur)
	require.NoError(t, err)
}

// covZCampaign cria campanha (com cliente proprio) e agenda a limpeza.
func covZCampaign(t *testing.T, ctx context.Context, pool *pgxpool.Pool, status string, start, end time.Time, stations []uuid.UUID, name string) uuid.UUID {
	t.Helper()
	if stations == nil {
		stations = []uuid.UUID{} // NOT NULL: nil viraria NULL
	}
	var cli uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO clients (name) VALUES ($1) RETURNING id`, "Cliente Teste "+name).Scan(&cli))
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO campaigns (client_id, name, start_date, end_date, status, target_stations)
		 VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		cli, name, covZDate(start), covZDate(end), status, stations).Scan(&id))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM campaign_materials WHERE campaign_id=$1`, id)
		_, _ = pool.Exec(context.Background(), `DELETE FROM campaigns WHERE id=$1`, id)
		_, _ = pool.Exec(context.Background(), `DELETE FROM clients WHERE id=$1`, cli)
	})
	return id
}

// covZLogRows devolve status por tipo do notification_log de uma data.
func covZLogRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, day time.Time) map[string]LogEntry {
	t.Helper()
	rows, err := pool.Query(ctx,
		`SELECT type, status, recipient_count, campaign_count, COALESCE(error,'') FROM notification_log WHERE notification_date=$1::date`, day)
	require.NoError(t, err)
	defer rows.Close()
	out := map[string]LogEntry{}
	for rows.Next() {
		var e LogEntry
		require.NoError(t, rows.Scan(&e.Type, &e.Status, &e.RecipientCount, &e.CampaignCount, &e.Error))
		e.Date = day
		out[e.Type] = e
	}
	require.NoError(t, rows.Err())
	return out
}

// covZCleanLog remove as linhas do notification_log de uma data.
func covZCleanLog(t *testing.T, pool *pgxpool.Pool, days ...time.Time) {
	t.Helper()
	t.Cleanup(func() {
		for _, d := range days {
			_, _ = pool.Exec(context.Background(), `DELETE FROM notification_log WHERE notification_date=$1::date`, d)
		}
	})
}
