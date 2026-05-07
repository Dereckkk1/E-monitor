package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// TestCommercials_List_BadCampaignID rejects unparseable query param with 400.
func TestCommercials_List_BadCampaignID(t *testing.T) {
	h := &CommercialsHandler{}
	req := httptest.NewRequest(http.MethodGet, "/commercials?campaign_id=garbage", nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "campaign_id") {
		t.Fatalf("error should mention campaign_id: %s", rec.Body.String())
	}
}

// TestCommercials_List_MissingCampaignID rejects empty value with 400.
func TestCommercials_List_MissingCampaignID(t *testing.T) {
	h := &CommercialsHandler{}
	req := httptest.NewRequest(http.MethodGet, "/commercials", nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestCommercials_Get_InvalidID short-circuits with 400.
func TestCommercials_Get_InvalidID(t *testing.T) {
	h := &CommercialsHandler{}
	r := chi.NewRouter()
	r.Get("/commercials/{id}", h.Get)

	req := httptest.NewRequest(http.MethodGet, "/commercials/garbage", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestCommercials_Delete_InvalidID short-circuits with 400.
func TestCommercials_Delete_InvalidID(t *testing.T) {
	h := &CommercialsHandler{}
	r := chi.NewRouter()
	r.Delete("/commercials/{id}", h.Delete)

	req := httptest.NewRequest(http.MethodDelete, "/commercials/garbage", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestCommercials_UpdateStations_InvalidID short-circuits with 400.
func TestCommercials_UpdateStations_InvalidID(t *testing.T) {
	h := &CommercialsHandler{}
	r := chi.NewRouter()
	r.Put("/commercials/{id}/stations", h.UpdateStations)

	req := httptest.NewRequest(http.MethodPut, "/commercials/garbage/stations",
		strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestCommercials_UpdateStations_BadJSON rejects malformed body with 400 once
// id is valid.
func TestCommercials_UpdateStations_BadJSON(t *testing.T) {
	h := &CommercialsHandler{}
	r := chi.NewRouter()
	r.Put("/commercials/{id}/stations", h.UpdateStations)

	id := uuid.New()
	req := httptest.NewRequest(http.MethodPut, "/commercials/"+id.String()+"/stations",
		strings.NewReader(`{not json`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestCommercials_Upload_BadMultipart rejects non-multipart bodies with 400.
func TestCommercials_Upload_BadMultipart(t *testing.T) {
	h := &CommercialsHandler{}
	req := httptest.NewRequest(http.MethodPost, "/commercials/upload", strings.NewReader(`not-multipart`))
	rec := httptest.NewRecorder()
	h.Upload(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestCommercials_Audio_InvalidID short-circuits with 400.
func TestCommercials_Audio_InvalidID(t *testing.T) {
	h := &CommercialsHandler{}
	r := chi.NewRouter()
	r.Get("/commercials/{id}/audio", h.Audio)

	req := httptest.NewRequest(http.MethodGet, "/commercials/garbage/audio", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
