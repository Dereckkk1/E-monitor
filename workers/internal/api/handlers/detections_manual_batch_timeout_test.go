package handlers

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"
)

// O handler de upload roda sob middleware.Timeout(60s) global (router.go:77),
// mas aceita corpo de até 600MB (600MB@60s = 80Mbps — impossível). uploadContext
// desacopla o deadline do upload do deadline das rotas JSON.
func TestUploadContext_DetachesFromShortDeadline(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest("POST", "/x", nil).WithContext(parent)

	ctx, cancelUp := uploadContext(req)
	defer cancelUp()

	time.Sleep(100 * time.Millisecond) // parent já expirou

	if err := ctx.Err(); err != nil {
		t.Fatalf("upload ctx morreu junto com o parent de 50ms: %v", err)
	}
	dl, ok := ctx.Deadline()
	if !ok {
		t.Fatal("upload ctx deve ter deadline próprio (não pode ser infinito)")
	}
	if remaining := time.Until(dl); remaining < 5*time.Minute {
		t.Fatalf("deadline do upload muito curto: %v restante", remaining)
	}
}

// Valores do context (auth claims!) TÊM que sobreviver ao detach — senão o
// handler perde o usuário autenticado.
func TestUploadContext_PreservesValues(t *testing.T) {
	type ctxKey string
	const k ctxKey = "claims"
	parent := context.WithValue(context.Background(), k, "user-42")
	req := httptest.NewRequest("POST", "/x", nil).WithContext(parent)

	ctx, cancel := uploadContext(req)
	defer cancel()

	if got := ctx.Value(k); got != "user-42" {
		t.Fatalf("valor do context perdido no detach: got %v", got)
	}
}
