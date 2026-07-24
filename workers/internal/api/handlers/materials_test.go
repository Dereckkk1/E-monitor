package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func TestMaterialsHandler_ListByClient_BadID(t *testing.T) {
	h := &MaterialsHandler{}
	r := chi.NewRouter()
	r.Get("/clients/{clientID}/materials", h.ListByClient)

	req := httptest.NewRequest("GET", "/clients/not-a-uuid/materials", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestMaterialsHandler_Get_BadID(t *testing.T) {
	h := &MaterialsHandler{}
	r := chi.NewRouter()
	r.Get("/materials/{id}", h.Get)

	req := httptest.NewRequest("GET", "/materials/not-uuid", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestMaterialsHandler_Upload_MissingClientID(t *testing.T) {
	h := &MaterialsHandler{}
	// Multipart form with no client_id
	body := strings.NewReader("--boundary\r\nContent-Disposition: form-data; name=\"title\"\r\n\r\nfoo\r\n--boundary--\r\n")
	req := httptest.NewRequest("POST", "/materials", body)
	req.Header.Set("Content-Type", `multipart/form-data; boundary=boundary`)
	rr := httptest.NewRecorder()
	h.Upload(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (missing client_id)", rr.Code)
	}
}

func TestMaterialsHandler_UpdateType_BadID(t *testing.T) {
	h := &MaterialsHandler{}
	r := chi.NewRouter()
	r.Patch("/materials/{id}/type", h.UpdateType)

	req := httptest.NewRequest("PATCH", "/materials/not-uuid/type", strings.NewReader(`{"type_id":null}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestMaterialsHandler_UpdateTitle_BadID(t *testing.T) {
	h := &MaterialsHandler{}
	r := chi.NewRouter()
	r.Patch("/materials/{id}/title", h.UpdateTitle)

	req := httptest.NewRequest("PATCH", "/materials/not-uuid/title", strings.NewReader(`{"title":"novo"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

// Vazio-após-trim tem que bater em 400 ANTES de tocar no repo — h.Repo é nil
// aqui, então um teste que passasse da validação entraria em panic. É de
// propósito: title é NOT NULL e a UI identifica o material por ele.
func TestMaterialsHandler_UpdateTitle_BlankTitle(t *testing.T) {
	h := &MaterialsHandler{}
	r := chi.NewRouter()
	r.Patch("/materials/{id}/title", h.UpdateTitle)

	for _, body := range []string{`{"title":""}`, `{"title":"   "}`, `{}`} {
		req := httptest.NewRequest("PATCH", "/materials/"+uuid.NewString()+"/title", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("body %s: status = %d, want 400", body, rr.Code)
		}
	}
}

func TestMaterialsHandler_UpdateTitle_InvalidJSON(t *testing.T) {
	h := &MaterialsHandler{}
	r := chi.NewRouter()
	r.Patch("/materials/{id}/title", h.UpdateTitle)

	req := httptest.NewRequest("PATCH", "/materials/"+uuid.NewString()+"/title", strings.NewReader(`{"title":`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestMaterialsHandler_Delete_BadID(t *testing.T) {
	h := &MaterialsHandler{}
	r := chi.NewRouter()
	r.Delete("/materials/{id}", h.Delete)
	req := httptest.NewRequest("DELETE", "/materials/not-uuid", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}
