package catalog

import (
	"testing"

	"github.com/google/uuid"
)

func TestNotifications_List_EmptyForUnknownUser(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewNotifications(pool)

	// UUID aleatório → não existe na users table, sem reads, sem matches.
	// Mesmo que haja failures recentes em prod, este user vê tudo como
	// não-lido — mas só checamos que não dá erro e retorna estrutura
	// não-nula.
	someUser := uuid.New()
	res, err := repo.List(ctx, someUser)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil result")
	}
	if res.Items == nil {
		t.Error("expected non-nil Items (even when empty)")
	}
	if res.UnreadCount < 0 {
		t.Errorf("UnreadCount negativo: %d", res.UnreadCount)
	}
}

func TestNotifications_MarkRead_EmptyKeysIsNoop(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewNotifications(pool)

	someUser := uuid.New()
	n, err := repo.MarkRead(ctx, someUser, nil)
	if err != nil {
		t.Fatalf("MarkRead nil: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 rows on empty input, got %d", n)
	}

	n, err = repo.MarkRead(ctx, someUser, []string{})
	if err != nil {
		t.Fatalf("MarkRead empty: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 rows on empty slice, got %d", n)
	}
}

func TestNotifications_MarkRead_Idempotent(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewNotifications(pool)

	// Tem que existir um usuário real pra FK. Pegamos qualquer um.
	var userID uuid.UUID
	err := pool.QueryRow(ctx, `SELECT id FROM users LIMIT 1`).Scan(&userID)
	if err != nil {
		t.Skipf("no users in DB to FK against: %v", err)
	}

	key := "test_kind:test:" + uuid.NewString()

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx,
			`DELETE FROM notification_reads WHERE user_id = $1 AND notification_key = $2`,
			userID, key,
		)
	})

	// Primeira chamada: insere 1
	n, err := repo.MarkRead(ctx, userID, []string{key})
	if err != nil {
		t.Fatalf("first MarkRead: %v", err)
	}
	if n != 1 {
		t.Errorf("first call: expected 1 inserted, got %d", n)
	}

	// Segunda chamada com a mesma key: insere 0 (ON CONFLICT)
	n, err = repo.MarkRead(ctx, userID, []string{key})
	if err != nil {
		t.Fatalf("second MarkRead: %v", err)
	}
	if n != 0 {
		t.Errorf("second call: expected 0 inserted (conflict), got %d", n)
	}
}
