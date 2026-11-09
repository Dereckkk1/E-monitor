package api

// Gravação é tela exclusiva de admin: operator e viewer não entram.
//
// Usa `NewRouter`, não um router espelho: o que se prova aqui é ONDE as rotas
// estão montadas (dentro do grupo com JWT e atrás de RequireRole("admin")). Um
// espelho provaria o espelho, e passaria igual com o gate removido do router de
// verdade.

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"radiocheck/internal/api/handlers"
	"radiocheck/internal/recording"
)

// recordingsStub responde sem tocar banco nem bucket. Sem ele, o admin passaria do
// gate e daria panic num service nil.
type recordingsStub struct{}

func (recordingsStub) Create(context.Context, recording.CreateInput) (*recording.Recording, error) {
	return &recording.Recording{ID: uuid.New(), Gaps: []recording.Gap{}}, nil
}
func (recordingsStub) List(context.Context, int, int) ([]recording.Recording, int, error) {
	return nil, 0, nil
}
func (recordingsStub) Cancel(context.Context, uuid.UUID) error { return nil }
func (recordingsStub) Delete(context.Context, uuid.UUID) error { return nil }
func (recordingsStub) Audio(context.Context, uuid.UUID) (io.ReadCloser, *recording.Recording, int64, error) {
	return io.NopCloser(strings.NewReader("audio")), &recording.Recording{}, 5, nil
}

func routerComRecordings() http.Handler {
	return NewRouter(Deps{Recordings: &handlers.RecordingsHandler{Service: recordingsStub{}}})
}

func TestRecordings_AdminOnly(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	h := routerComRecordings()
	id := uuid.NewString()
	// O status do admin só pode vir do handler: é a prova de que a rota existe e de
	// que o gate o deixou passar. O POST /recordings recebe "{}" do `do`, sem
	// emissora, e o handler responde 400.
	routes := []struct {
		method, path string
		admin        int
	}{
		{"GET", "/v1/internal/recordings", http.StatusOK},
		{"POST", "/v1/internal/recordings", http.StatusBadRequest},
		{"POST", "/v1/internal/recordings/" + id + "/cancel", http.StatusNoContent},
		{"DELETE", "/v1/internal/recordings/" + id, http.StatusNoContent},
		{"GET", "/v1/internal/recordings/" + id + "/audio", http.StatusOK},
	}
	ad, op, vw := issue(t, "admin"), issue(t, "operator"), issue(t, "viewer")
	for _, rt := range routes {
		if got := do(t, h, rt.method, rt.path, ad); got != rt.admin {
			t.Errorf("admin %s %s: got %d, want %d", rt.method, rt.path, got, rt.admin)
		}
		if got := do(t, h, rt.method, rt.path, op); got != http.StatusForbidden {
			t.Errorf("operator %s %s: got %d, want 403", rt.method, rt.path, got)
		}
		if got := do(t, h, rt.method, rt.path, vw); got != http.StatusForbidden {
			t.Errorf("viewer %s %s: got %d, want 403", rt.method, rt.path, got)
		}
		if got := do(t, h, rt.method, rt.path, ""); got != http.StatusUnauthorized {
			t.Errorf("sem token %s %s: got %d, want 401", rt.method, rt.path, got)
		}
	}
}

// As rotas só existem com a dependência, como as outras deste router.
func TestRecordings_RotaNaoExisteSemADependencia(t *testing.T) {
	if got := do(t, NewRouter(Deps{}), "GET", "/v1/internal/recordings", issue(t, "admin")); got != http.StatusNotFound {
		t.Errorf("got %d, want 404", got)
	}
}
