package catalog

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// insertSuggestionTestUser inserts a bare users row so suggestions.created_by
// (FK → users.id) is satisfied, and registers cleanup that removes any
// suggestions the user authored (cascading their comments/events/reads) plus
// the user itself.
func insertSuggestionTestUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	email := "sug-" + uuid.NewString() + "@test.local"
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, name) VALUES ($1, 'x', 'admin', $1) RETURNING id`,
		email).Scan(&id)
	require.NoError(t, err)
	t.Cleanup(func() {
		bg := context.Background()
		pool.Exec(bg, `DELETE FROM suggestions WHERE created_by = $1`, id) //nolint:errcheck
		pool.Exec(bg, `DELETE FROM users WHERE id = $1`, id)               //nolint:errcheck
	})
	return id
}

func newTestSuggestion(t *testing.T, ctx context.Context, repo *Suggestions, author uuid.UUID) *Suggestion {
	t.Helper()
	sug, err := repo.Create(ctx, CreateSuggestionInput{
		CreatedBy:         author,
		Title:             "Botão some no mobile",
		Description:       "Ao abrir no celular o botão de salvar não aparece.",
		Type:              "bug",
		TargetScreen:      "/campaigns",
		RequesterPriority: "alta",
	})
	require.NoError(t, err)
	return sug
}

func TestSuggestions_Create_RefNumAndCreatedEvent(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewSuggestions(pool)
	author := insertSuggestionTestUser(t, ctx, pool)

	sug := newTestSuggestion(t, ctx, repo, author)
	require.NotEqual(t, uuid.Nil, sug.ID)
	require.Greater(t, sug.RefNum, int32(0), "ref_num must be assigned by the SERIAL")
	require.Equal(t, "nova", sug.Status)
	require.NotNil(t, sug.CreatedBy)
	require.Equal(t, author, *sug.CreatedBy)
	require.NotNil(t, sug.TargetScreen)
	require.Equal(t, "/campaigns", *sug.TargetScreen)

	events, err := repo.ListEvents(ctx, sug.ID)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "created", events[0].EventType)
	require.NotNil(t, events[0].ActorID)
	require.Equal(t, author, *events[0].ActorID)
}

func TestSuggestions_List_OnlyAuthorScopes(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewSuggestions(pool)
	authorA := insertSuggestionTestUser(t, ctx, pool)
	authorB := insertSuggestionTestUser(t, ctx, pool)

	a1 := newTestSuggestion(t, ctx, repo, authorA)
	_ = newTestSuggestion(t, ctx, repo, authorA)
	_ = newTestSuggestion(t, ctx, repo, authorB)

	listA, err := repo.List(ctx, ListSuggestionsFilter{OnlyAuthorID: &authorA})
	require.NoError(t, err)
	require.Len(t, listA, 2)
	for _, s := range listA {
		require.NotNil(t, s.CreatedBy)
		require.Equal(t, authorA, *s.CreatedBy)
	}

	listB, err := repo.List(ctx, ListSuggestionsFilter{OnlyAuthorID: &authorB})
	require.NoError(t, err)
	require.Len(t, listB, 1)
	require.Equal(t, authorB, *listB[0].CreatedBy)

	// Filter by status within an author's scope.
	scoped, err := repo.List(ctx, ListSuggestionsFilter{OnlyAuthorID: &authorA, Status: "nova"})
	require.NoError(t, err)
	require.Len(t, scoped, 2)

	// a1 exists and is one of authorA's.
	require.Contains(t, []uuid.UUID{listA[0].ID, listA[1].ID}, a1.ID)
}

func TestSuggestions_Update_ConcluidaSetsResolvedAt(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewSuggestions(pool)
	author := insertSuggestionTestUser(t, ctx, pool)
	dev := insertSuggestionTestUser(t, ctx, pool)
	sug := newTestSuggestion(t, ctx, repo, author)
	require.Nil(t, sug.ResolvedAt)

	concluida := "concluida"
	feedback := "Corrigido no deploy de hoje."
	updated, err := repo.Update(ctx, sug.ID, UpdateSuggestionInput{
		ActorID:     dev,
		Status:      &concluida,
		DevFeedback: &feedback,
	})
	require.NoError(t, err)
	require.Equal(t, "concluida", updated.Status)
	require.NotNil(t, updated.ResolvedAt, "resolved_at must be set on concluida")
	require.NotNil(t, updated.DevFeedback)
	require.Equal(t, feedback, *updated.DevFeedback)

	events, err := repo.ListEvents(ctx, sug.ID)
	require.NoError(t, err)
	// created + status_changed + feedback_given
	var kinds []string
	for _, e := range events {
		kinds = append(kinds, e.EventType)
	}
	require.Contains(t, kinds, "status_changed")
	require.Contains(t, kinds, "feedback_given")
}

func TestSuggestions_AddComment(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewSuggestions(pool)
	author := insertSuggestionTestUser(t, ctx, pool)
	sug := newTestSuggestion(t, ctx, repo, author)

	c, err := repo.AddComment(ctx, sug.ID, author, "Segue um print do problema.")
	require.NoError(t, err)
	require.Equal(t, sug.ID, c.SuggestionID)
	require.NotNil(t, c.AuthorID)
	require.Equal(t, author, *c.AuthorID)

	comments, err := repo.ListComments(ctx, sug.ID)
	require.NoError(t, err)
	require.Len(t, comments, 1)
	require.Equal(t, "Segue um print do problema.", comments[0].Body)
}

func TestSuggestions_UnreadCount_AuthorSeesDevActivity(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewSuggestions(pool)
	author := insertSuggestionTestUser(t, ctx, pool)
	dev := insertSuggestionTestUser(t, ctx, pool)
	sug := newTestSuggestion(t, ctx, repo, author)

	// Fresh suggestion: only the author's own `created` event exists, so from
	// the author's perspective there is no unread DEV activity.
	n, err := repo.UnreadCount(ctx, author, false)
	require.NoError(t, err)
	require.Equal(t, 0, n)

	// Another user (the dev) comments → now it's unread for the author.
	_, err = repo.AddComment(ctx, sug.ID, dev, "Consegue mandar o modelo do aparelho?")
	require.NoError(t, err)
	n, err = repo.UnreadCount(ctx, author, false)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	// After the author reads it, the badge resets.
	require.NoError(t, repo.MarkRead(ctx, author, sug.ID))
	n, err = repo.UnreadCount(ctx, author, false)
	require.NoError(t, err)
	require.Equal(t, 0, n)
}
