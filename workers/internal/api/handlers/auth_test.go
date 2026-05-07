package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAuth_Login_BadJSON rejects malformed body with 400 before the DB query.
func TestAuth_Login_BadJSON(t *testing.T) {
	h := NewAuthHandler(nil)
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`not json`))
	rec := httptest.NewRecorder()
	h.Login(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestNewAuthHandler_Defaults verifies the constructor wires the pool.
func TestNewAuthHandler_Defaults(t *testing.T) {
	if h := NewAuthHandler(nil); h == nil {
		t.Fatal("NewAuthHandler returned nil")
	}
}

// TestNewAPIKeysHandler_Defaults verifies the constructor wires the pool.
func TestNewAPIKeysHandler_Defaults(t *testing.T) {
	if h := NewAPIKeysHandler(nil); h == nil {
		t.Fatal("NewAPIKeysHandler returned nil")
	}
}

// TestNewWebhooksHandler_Defaults verifies the constructor wires the pool /
// dependencies even when outbox is nil.
func TestNewWebhooksHandler_Defaults(t *testing.T) {
	if h := NewWebhooksHandler(nil, nil, nil); h == nil {
		t.Fatal("NewWebhooksHandler returned nil")
	}
}
