package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go"

	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
	"radiocheck/internal/events"
)

// MaterialsHandler handles CRUD and upload for /materials and
// /clients/{clientID}/materials routes.
type MaterialsHandler struct {
	Repo        *catalog.Materials
	MastersPath string
	NATS        *nats.Conn
	// DistRules recategoriza detections quando o tipo de um material muda
	// (UpdateType). Nil-safe — se ausente, a recategorização é pulada.
	DistRules *catalog.DistributionRules
}

// ListByClient returns all materials for a given client.
// Optional query param ?q= filters by case-insensitive title substring.
// Viewer scope: if the JWT client_id does not match :clientID, returns 403.
func (h *MaterialsHandler) ListByClient(w http.ResponseWriter, r *http.Request) {
	clientID, err := uuid.Parse(chi.URLParam(r, "clientID"))
	if err != nil {
		http.Error(w, "invalid clientID", http.StatusBadRequest)
		return
	}
	if !auth.ScopeAllows(r.Context(), clientID) {
		http.Error(w, "forbidden_client_scope", http.StatusForbidden)
		return
	}
	q := r.URL.Query().Get("q")
	mats, err := h.Repo.ListByClient(r.Context(), clientID, q)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if mats == nil {
		mats = []catalog.Material{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": mats})
}

// Get returns a single material by UUID.
func (h *MaterialsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	m, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// Upload accepts a multipart/form-data POST with fields:
//   - client_id  (UUID, required)
//   - title      (string, required)
//   - type_id    (UUID, optional)
//   - audio      (file, required) — wav/mp3/m4a/aac/mpeg
//
// The file is SHA-256 hashed and stored under MastersPath.
// A fingerprint-generate event is published to NATS on success.
func (h *MaterialsHandler) Upload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		http.Error(w, "invalid multipart", http.StatusBadRequest)
		return
	}

	clientIDStr := r.FormValue("client_id")
	title := r.FormValue("title")
	typeIDStr := r.FormValue("type_id")
	scriptRaw := strings.TrimSpace(r.FormValue("script"))

	clientID, err := uuid.Parse(clientIDStr)
	if err != nil {
		http.Error(w, "client_id is required and must be a valid UUID", http.StatusBadRequest)
		return
	}
	if title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}

	var typeID *uuid.UUID
	if typeIDStr != "" {
		tid, err := uuid.Parse(typeIDStr)
		if err != nil {
			http.Error(w, "type_id must be a valid UUID", http.StatusBadRequest)
			return
		}
		typeID = &tid
	}

	// Script is optional. Empty-after-trim is persisted as NULL so the
	// frontend can rely on `script === undefined/null` for "no script set".
	var script *string
	if scriptRaw != "" {
		script = &scriptRaw
	}

	file, header, err := r.FormFile("audio")
	if err != nil {
		http.Error(w, "audio file is required", http.StatusBadRequest)
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext != ".wav" && ext != ".mp3" && ext != ".m4a" && ext != ".aac" && ext != ".mpeg" {
		http.Error(w, "unsupported audio format (use wav/mp3/m4a/aac/mpeg)", http.StatusBadRequest)
		return
	}

	if err := os.MkdirAll(h.MastersPath, 0755); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Write to a temp path while hashing; rename to sha-based final name.
	tmpPath := filepath.Join(h.MastersPath, uuid.NewString()+ext)
	f, err := os.Create(tmpPath)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, hasher), file); err != nil {
		f.Close()
		os.Remove(tmpPath)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	f.Close()

	sha := hex.EncodeToString(hasher.Sum(nil))
	finalName := sha + ext
	finalPath := filepath.Join(h.MastersPath, finalName)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		os.Remove(tmpPath)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Dedup por áudio (audit — raiz do double-count UNIUBE 130/138): se o cliente
	// já tem um material com este master_sha256, REUSA o existente em vez de criar
	// um segundo. O mesmo áudio como 2 materiais duplicava a contagem (cada row
	// fazia fan-out F-119 pras 2 campanhas). Reusar = 1 material/áudio, e o link
	// da nova campanha nele credita ambas via fan-out, sem dobrar. Escopado ao
	// cliente (mesmo áudio em clientes distintos pode ser produção legítima).
	// O arquivo já está salvo em <sha>.<ext> (conteúdo idêntico), então nada a limpar.
	if existing, gerr := h.Repo.GetByClientAndSHA(r.Context(), clientID, sha); gerr == nil {
		// O arquivo foi gravado ANTES desta checagem e agora não é referenciado
		// por linha nenhuma. Sem isto cada retentativa do operador deixa uma
		// cópia de vários MB pra trás (prod 2026-08-31: mesmo áudio como .mp3
		// referenciado e .mpeg órfão). Best-effort: falhar em remover o órfão
		// não pode derrubar o upload.
		_ = discardRedundantMaster(finalPath, existing.MasterStoragePath)
		writeJSON(w, http.StatusOK, existing)
		return
	} else if !errors.Is(gerr, pgx.ErrNoRows) {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Probe the real audio duration with ffprobe — same helper commercials.go
	// uses (both live in package handlers). This was stubbed at 30.0 (F-88),
	// which made every uploaded material report 30s regardless of the file.
	duration, err := probeDuration(finalPath)
	if err != nil {
		os.Remove(finalPath)
		http.Error(w, "could not probe audio duration: "+err.Error(), http.StatusBadRequest)
		return
	}

	mat, err := h.Repo.Create(r.Context(), catalog.CreateMaterialInput{
		ClientID:          clientID,
		Title:             title,
		TypeID:            typeID,
		DurationSeconds:   duration,
		MasterStoragePath: finalPath,
		MasterSHA256:      sha,
		Script:            script,
	})
	if err != nil {
		os.Remove(finalPath)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Publish fingerprint-generate event (best-effort; nil NATS is safe).
	if h.NATS != nil {
		payload, _ := json.Marshal(map[string]any{
			"material_id": mat.ID.String(),
		})
		_ = h.NATS.Publish(events.SubjectFingerprintGenerate, payload)
	}

	writeJSON(w, http.StatusCreated, mat)
}

// discardRedundantMaster remove o arquivo que o Upload acabou de gravar quando
// o dedup por master_sha256 decidiu reusar um material que já existia.
//
// Duas guardas, ambas contra perda de dado irreversível:
//
//   - newPath == existingPath: o arquivo recém-gravado É o master do material
//     existente (re-upload do mesmo arquivo com a mesma extensão, que produz o
//     mesmo <sha>.<ext>). Remover aqui apagaria o master de um material vivo e
//     mataria a detecção desse áudio pro cliente inteiro.
//
//   - master do existente ausente do disco: a cópia recém-enviada é a única que
//     restou. Qualquer erro no Stat conta como ausente — na dúvida, não apaga.
func discardRedundantMaster(newPath, existingPath string) error {
	if newPath == existingPath {
		return nil
	}
	if _, err := os.Stat(existingPath); err != nil {
		return nil
	}
	return os.Remove(newPath)
}

// UpdateType sets or clears the type_id for a material.
// Body: {"type_id": "<uuid>" | null}
func (h *MaterialsHandler) UpdateType(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	var p struct {
		TypeID *uuid.UUID `json:"type_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := h.Repo.UpdateType(r.Context(), id, p.TypeID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// O type_id mudou → a category gravada das detections desse material ficou
	// obsoleta (foi computada no insert com o tipo antigo). Sem isto, detections
	// que agora casam uma regra do tipo novo continuam 'bonus' e aparecem como
	// "bonificação (sem meta)" no resumo diário. Recategoriza em background
	// (best-effort, mesmo padrão dos handlers de distribution_rules).
	if h.DistRules != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			recordRecatFailure("material_type_change", h.DistRules.RecategorizeForMaterial(ctx, id))
		}()
	}
	w.WriteHeader(http.StatusNoContent)
}

// UpdateTitle renames a material.
// Body: {"title": "<novo nome>"} → 204. Vazio-após-trim é 400 (title é NOT NULL
// e a UI depende dele pra identificar o material); id inexistente é 404.
//
// Só o rótulo muda: nada de fingerprint, categorização ou atribuição depende
// de materials.title, então não há recategorização a disparar aqui (ao
// contrário de UpdateType). Existe pra corrigir material subido com nome
// errado sem ter que re-uploadar o áudio.
func (h *MaterialsHandler) UpdateTitle(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	var p struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(p.Title)
	if title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}
	if err := h.Repo.UpdateTitle(r.Context(), id, title); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// UpdateScript sets (or clears) the spoken-copy of a material.
// Body: {"script": "<text>" | null}. Empty string after trim is normalized
// to NULL so the field reads as "no script" both server- and client-side.
func (h *MaterialsHandler) UpdateScript(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	var p struct {
		Script *string `json:"script"`
	}
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if p.Script != nil {
		trimmed := strings.TrimSpace(*p.Script)
		if trimmed == "" {
			p.Script = nil
		} else {
			p.Script = &trimmed
		}
	}
	if err := h.Repo.UpdateScript(r.Context(), id, p.Script); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Delete removes a material by UUID.
func (h *MaterialsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := h.Repo.Delete(r.Context(), id); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Acknowledge marks the material's similarity warning as resolved.
// POST /materials/{id}/similarity/acknowledge → 204.
func (h *MaterialsHandler) Acknowledge(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := h.Repo.Acknowledge(r.Context(), id); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
