package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// TestMaterialTypes_Create_BadJSON rejects malformed body with 400.
func TestMaterialTypes_Create_BadJSON(t *testing.T) {
	h := &MaterialTypesHandler{}
	req := httptest.NewRequest(http.MethodPost, "/material-types", strings.NewReader(`not json`))
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestMaterialTypes_Create_MissingName rejects a body without a name with 400.
func TestMaterialTypes_Create_MissingName(t *testing.T) {
	h := &MaterialTypesHandler{}
	cases := []string{
		`{}`,
		`{"name":""}`,
		`{"color":"#ff0000"}`,
	}
	for _, body := range cases {
		req := httptest.NewRequest(http.MethodPost, "/material-types", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.Create(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body=%s: status = %d, want 400", body, rec.Code)
		}
	}
}

// TestMaterialTypes_Update_InvalidID short-circuits with 400 before touching the repo.
func TestMaterialTypes_Update_InvalidID(t *testing.T) {
	h := &MaterialTypesHandler{}
	r := chi.NewRouter()
	r.Put("/material-types/{id}", h.Update)

	req := httptest.NewRequest(http.MethodPut, "/material-types/not-a-uuid",
		strings.NewReader(`{"name":"x","color":"#aabbcc"}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestMaterialTypes_Update_BadJSON rejects malformed body with 400.
func TestMaterialTypes_Update_BadJSON(t *testing.T) {
	h := &MaterialTypesHandler{}
	r := chi.NewRouter()
	r.Put("/material-types/{id}", h.Update)

	id := uuid.New()
	req := httptest.NewRequest(http.MethodPut, "/material-types/"+id.String(),
		strings.NewReader(`not json`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestMaterialTypes_Delete_InvalidID short-circuits with 400 before touching the repo.
func TestMaterialTypes_Delete_InvalidID(t *testing.T) {
	h := &MaterialTypesHandler{}
	r := chi.NewRouter()
	r.Delete("/material-types/{id}", h.Delete)

	req := httptest.NewRequest(http.MethodDelete, "/material-types/not-a-uuid", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}
