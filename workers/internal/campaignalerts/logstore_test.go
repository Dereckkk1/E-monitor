package campaignalerts

import (
	"testing"
	"time"
)

func TestLogStore_DedupRoundTrip(t *testing.T) {
	ctx, pool := newTestDB(t)
	store := NewLogStore(pool)
	day := time.Date(2026, 6, 8, 0, 0, 0, 0, time.UTC)
	typ := "starting"
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM notification_log WHERE notification_date=$1 AND type=$2`, day, typ) //nolint:errcheck
	})

	sent, err := store.AlreadySent(ctx, day, typ)
	if err != nil {
		t.Fatalf("AlreadySent: %v", err)
	}
	if sent {
		t.Fatal("não deveria estar enviado ainda")
	}
	if err := store.Record(ctx, LogEntry{Date: day, Type: typ, RecipientCount: 3, CampaignCount: 2, Status: "sent"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	sent, err = store.AlreadySent(ctx, day, typ)
	if err != nil {
		t.Fatalf("AlreadySent#2: %v", err)
	}
	if !sent {
		t.Fatal("deveria estar marcado como enviado")
	}
}
