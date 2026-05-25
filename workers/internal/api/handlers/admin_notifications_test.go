package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
)

// withClaims injeta auth.Claims no contexto do request, equivalente ao
// que o middleware RequireJWT faria.
func withClaims(r *http.Request, userID uuid.UUID, role string) *http.Request {
	claims := &auth.Claims{UserID: userID, Role: role}
	ctx := auth.WithClaims(r.Context(), claims)
	return r.WithContext(ctx)
}

func TestNotificationsHandler_List_NilRepo_ReturnsEmpty(t *testing.T) {
	h := &NotificationsHandler{}

	req := httptest.NewRequest("GET", "/", nil)
	req = withClaims(req, uuid.New(), "admin")
	rr := httptest.NewRecorder()

	h.List(rr, req)
	if rr.Code != 200 {
		t.Fatalf("want 200, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"items"`) {
		t.Errorf("expected items in response, got: %s", rr.Body.String())
	}
}

func TestNotificationsHandler_List_NoClaims_Unauthorized(t *testing.T) {
	h := &NotificationsHandler{}

	req := httptest.NewRequest("GET", "/", nil)
	// Sem claims — handler tem que rejeitar.
	rr := httptest.NewRecorder()

	h.List(rr, req)
	if rr.Code != 401 {
		t.Errorf("want 401, got %d", rr.Code)
	}
}

func TestNotificationsHandler_MarkRead_BadJSON(t *testing.T) {
	h := &NotificationsHandler{}
	req := httptest.NewRequest("POST", "/", bytes.NewReader([]byte("not json")))
	req = withClaims(req, uuid.New(), "admin")
	rr := httptest.NewRecorder()

	h.MarkRead(rr, req)
	if rr.Code != 400 {
		t.Errorf("want 400, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestNotificationsHandler_MarkRead_TooManyKeys(t *testing.T) {
	h := &NotificationsHandler{}
	// Body com 201 keys
	body := `{"keys":[`
	for i := 0; i < 201; i++ {
		if i > 0 {
			body += ","
		}
		body += `"k"`
	}
	body += `]}`

	req := httptest.NewRequest("POST", "/", bytes.NewReader([]byte(body)))
	req = withClaims(req, uuid.New(), "admin")
	rr := httptest.NewRecorder()

	h.MarkRead(rr, req)
	if rr.Code != 400 {
		t.Errorf("want 400 for >200 keys, got %d", rr.Code)
	}
}

func TestNotificationsHandler_MarkRead_NilRepo_OK(t *testing.T) {
	h := &NotificationsHandler{}
	req := httptest.NewRequest("POST", "/", bytes.NewReader([]byte(`{"keys":["k1"]}`)))
	req = withClaims(req, uuid.New(), "admin")
	rr := httptest.NewRecorder()

	h.MarkRead(rr, req)
	if rr.Code != 200 {
		t.Errorf("want 200 with nil repo, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// Garante que o handler compila e que a interface bate com o repo real.
// Sem isso, mudanças no shape da NotificationsRepo silenciosamente
// quebrariam só em runtime.
func TestNotificationsHandler_RepoInterface(t *testing.T) {
	var _ NotificationsRepo = (*catalog.Notifications)(nil)
}
