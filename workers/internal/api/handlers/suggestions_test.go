package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
	"radiocheck/internal/db"
	"radiocheck/internal/dbtest"
	"radiocheck/internal/users"
)

const suggestionsTestDevEmail = "dev-tatico@test.local"

func newSuggestionsTestPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, url)
	require.NoError(t, err)
	t.Cleanup(func() { pool.Close() })
	dbtest.GuardOrSkip(t, ctx, pool)
	// TRUNCATE ... CASCADE clears the suggestion_* children and reads too.
	_, err = pool.Exec(ctx, `TRUNCATE suggestions, users, clients RESTART IDENTITY CASCADE`)
	require.NoError(t, err)
	return ctx, pool
}

func newSuggestionsHandler(pool *pgxpool.Pool) *SuggestionsHandler {
	return &SuggestionsHandler{
		Repo:     catalog.NewSuggestions(pool),
		Users:    users.NewRepo(pool),
		Storage:  nil,
		DevEmail: suggestionsTestDevEmail,
	}
}

func mkUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, email string) uuid.UUID {
	t.Helper()
	u, err := users.NewRepo(pool).Create(ctx, users.CreateInput{
		Email: email, PasswordHash: "x", Role: "admin", Name: "T",
	})
	require.NoError(t, err)
	return u.ID
}

func asCaller(req *http.Request, uid uuid.UUID) *http.Request {
	return req.WithContext(auth.ContextWithClaims(req.Context(), &auth.Claims{UserID: uid, Role: "admin"}))
}

// Autor cannot read another autor's suggestion → 403.
func TestSuggestionsHandler_Get_AuthorCannotReadOthers(t *testing.T) {
	ctx, pool := newSuggestionsTestPool(t)
	h := newSuggestionsHandler(pool)
	authorA := mkUser(t, ctx, pool, "a@test.local")
	authorB := mkUser(t, ctx, pool, "b@test.local")
	sug, err := h.Repo.Create(ctx, catalog.CreateSuggestionInput{
		CreatedBy: authorA, Title: "x", Description: "y", Type: "bug", RequesterPriority: "alta",
	})
	require.NoError(t, err)

	req := reqWithIDParam("GET", "/suggestions/x", "", sug.ID.String())
	req = asCaller(req, authorB)
	w := httptest.NewRecorder()
	h.Get(w, req)
	require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
}

// dev_notes is absent from an autor's JSON but present for the dev.
func TestSuggestionsHandler_Get_DevNotesGating(t *testing.T) {
	ctx, pool := newSuggestionsTestPool(t)
	h := newSuggestionsHandler(pool)
	author := mkUser(t, ctx, pool, "a@test.local")
	dev := mkUser(t, ctx, pool, suggestionsTestDevEmail)
	sug, err := h.Repo.Create(ctx, catalog.CreateSuggestionInput{
		CreatedBy: author, Title: "x", Description: "y", Type: "bug", RequesterPriority: "alta",
	})
	require.NoError(t, err)
	notes := "nota privada do dev"
	_, err = h.Repo.Update(ctx, sug.ID, catalog.UpdateSuggestionInput{ActorID: dev, DevNotes: &notes})
	require.NoError(t, err)

	// Autor: dev_notes must be absent.
	req := reqWithIDParam("GET", "/suggestions/x", "", sug.ID.String())
	req = asCaller(req, author)
	w := httptest.NewRecorder()
	h.Get(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var authorView map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &authorView))
	_, present := authorView["dev_notes"]
	require.False(t, present, "dev_notes must NOT leak to the autor")

	// Dev: dev_notes present.
	req = reqWithIDParam("GET", "/suggestions/x", "", sug.ID.String())
	req = asCaller(req, dev)
	w = httptest.NewRecorder()
	h.Get(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var devView map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &devView))
	require.Equal(t, notes, devView["dev_notes"])
}

// Patch by a non-dev caller → 403.
func TestSuggestionsHandler_Patch_NonDevForbidden(t *testing.T) {
	ctx, pool := newSuggestionsTestPool(t)
	h := newSuggestionsHandler(pool)
	author := mkUser(t, ctx, pool, "a@test.local")
	sug, err := h.Repo.Create(ctx, catalog.CreateSuggestionInput{
		CreatedBy: author, Title: "x", Description: "y", Type: "bug", RequesterPriority: "alta",
	})
	require.NoError(t, err)

	req := reqWithIDParam("PATCH", "/suggestions/x", `{"status":"aceita"}`, sug.ID.String())
	req = asCaller(req, author)
	w := httptest.NewRecorder()
	h.Patch(w, req)
	require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
}

// An autor's List is hard-scoped to their own suggestions server-side.
func TestSuggestionsHandler_List_AuthorScoped(t *testing.T) {
	ctx, pool := newSuggestionsTestPool(t)
	h := newSuggestionsHandler(pool)
	authorA := mkUser(t, ctx, pool, "a@test.local")
	authorB := mkUser(t, ctx, pool, "b@test.local")
	for i := 0; i < 2; i++ {
		_, err := h.Repo.Create(ctx, catalog.CreateSuggestionInput{
			CreatedBy: authorA, Title: "x", Description: "y", Type: "bug", RequesterPriority: "alta",
		})
		require.NoError(t, err)
	}
	_, err := h.Repo.Create(ctx, catalog.CreateSuggestionInput{
		CreatedBy: authorB, Title: "z", Description: "w", Type: "feature", RequesterPriority: "baixa",
	})
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/suggestions", nil)
	req = asCaller(req, authorA)
	w := httptest.NewRecorder()
	h.List(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp struct {
		Data []catalog.Suggestion `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Data, 2)
	for _, s := range resp.Data {
		require.NotNil(t, s.CreatedBy)
		require.Equal(t, authorA, *s.CreatedBy)
	}
}
