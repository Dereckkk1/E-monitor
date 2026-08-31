package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// ── discardRedundantMaster (vazamento de arquivo no caminho de dedup) ────────
//
// Upload grava o master em <sha>.<ext> ANTES de checar o dedup por
// master_sha256. Quando o dedup decide reusar o material existente, o arquivo
// recém-gravado vira órfão: nenhuma linha do banco aponta pra ele. Visto em
// prod 2026-08-31: mesmo áudio como .mp3 (referenciado) e .mpeg (órfão), ~1,2MB
// cada, uma cópia nova a cada retentativa do operador.
//
// A remoção é perigosa: apagar o arquivo errado destrói o master de um material
// vivo e mata a detecção do cliente inteiro. Daí as duas guardas.

func TestDiscardRedundantMaster_SamePathIsNeverRemoved(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "abc.mp3")
	if err := os.WriteFile(p, []byte("audio"), 0644); err != nil {
		t.Fatal(err)
	}

	// Re-upload do arquivo IDÊNTICO com a mesma extensão: finalPath colide com
	// o master do material existente. Removê-lo apagaria o próprio master.
	if err := discardRedundantMaster(p, p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(p); err != nil {
		t.Fatalf("o master do material existente foi apagado: %v", err)
	}
}

func TestDiscardRedundantMaster_RemovesOrphanCopy(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "abc.mp3")
	orphan := filepath.Join(dir, "abc.mpeg")
	for _, p := range []string{existing, orphan} {
		if err := os.WriteFile(p, []byte("audio"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	if err := discardRedundantMaster(orphan, existing); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Error("a cópia órfã deveria ter sido removida")
	}
	if _, err := os.Stat(existing); err != nil {
		t.Errorf("o master referenciado não pode ser tocado: %v", err)
	}
}

func TestDiscardRedundantMaster_KeepsCopyWhenExistingMasterIsGone(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "abc.mp3") // nunca criado no disco
	fresh := filepath.Join(dir, "abc.mpeg")
	if err := os.WriteFile(fresh, []byte("audio"), 0644); err != nil {
		t.Fatal(err)
	}

	// O material existente aponta pra um arquivo que sumiu do disco. A cópia
	// recém-enviada é a ÚNICA que resta — apagá-la é perda de dado.
	if err := discardRedundantMaster(fresh, missing); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("apagou a única cópia do áudio: %v", err)
	}
}
