package campaignalerts

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// LogEntry é uma linha do notification_log.
type LogEntry struct {
	Date           time.Time
	Type           string
	RecipientCount int
	CampaignCount  int
	Status         string // sent | partial | failed | skipped_empty
	Error          string
}

// LogStore lê/grava o registro de dedup diário.
type LogStore struct {
	pool *pgxpool.Pool
}

func NewLogStore(pool *pgxpool.Pool) *LogStore { return &LogStore{pool: pool} }

// AlreadySent indica se já existe registro para (date, type) — a barreira de
// dedup diário.
func (s *LogStore) AlreadySent(ctx context.Context, date time.Time, typ string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM notification_log WHERE notification_date = $1::date AND type = $2)`,
		date, typ).Scan(&exists)
	return exists, err
}

// Record grava o resultado do disparo. ON CONFLICT garante idempotência se dois
// caminhos correrem (o advisory lock já serializa, isto é cinto e suspensório).
func (s *LogStore) Record(ctx context.Context, e LogEntry) error {
	var errPtr *string
	if e.Error != "" {
		errPtr = &e.Error
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO notification_log
		    (notification_date, type, recipient_count, campaign_count, status, error)
		VALUES ($1::date, $2, $3, $4, $5, $6)
		ON CONFLICT (notification_date, type) DO NOTHING`,
		e.Date, e.Type, e.RecipientCount, e.CampaignCount, e.Status, errPtr)
	return err
}
