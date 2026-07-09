package catalog

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Suggestion is a row in the suggestions table — a single demand submitted by
// an admin/operator ("autor") and managed by the dev ("Central de Comando").
// See docs/features/suggestions-board.md and the design spec
// docs/superpowers/specs/2026-07-09-suggestions-board-design.md.
type Suggestion struct {
	ID                uuid.UUID  `json:"id"`
	RefNum            int32      `json:"ref_num"`
	CreatedBy         *uuid.UUID `json:"created_by,omitempty"`
	Title             string     `json:"title"`
	Description       string     `json:"description"`
	Type              string     `json:"type"`
	TargetScreen      *string    `json:"target_screen,omitempty"`
	RequesterPriority string     `json:"requester_priority"`
	Status            string     `json:"status"`
	DevPriority       *string    `json:"dev_priority,omitempty"`
	Effort            *string    `json:"effort,omitempty"`
	DevFeedback       *string    `json:"dev_feedback,omitempty"`
	// DevNotes is private to the dev. The handler zeroes it (→ omitted from
	// JSON) before serializing to a non-dev caller. Never leaks to an autor.
	DevNotes       *string    `json:"dev_notes,omitempty"`
	AwaitingAuthor bool       `json:"awaiting_author"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// SuggestionComment is a message in a suggestion's thread.
type SuggestionComment struct {
	ID           uuid.UUID  `json:"id"`
	SuggestionID uuid.UUID  `json:"suggestion_id"`
	AuthorID     *uuid.UUID `json:"author_id,omitempty"`
	Body         string     `json:"body"`
	CreatedAt    time.Time  `json:"created_at"`
}

// SuggestionAttachment is an image attached to a suggestion (root) or a
// comment. URL is not a column — the handler fills it with a presigned GET.
type SuggestionAttachment struct {
	ID           uuid.UUID  `json:"id"`
	SuggestionID uuid.UUID  `json:"suggestion_id"`
	CommentID    *uuid.UUID `json:"comment_id,omitempty"`
	StorageKey   string     `json:"storage_key"`
	ContentType  string     `json:"content_type"`
	SizeBytes    int64      `json:"size_bytes"`
	UploadedBy   *uuid.UUID `json:"uploaded_by,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	URL          string     `json:"url,omitempty"`
}

// SuggestionEvent is one entry in the activity timeline of a suggestion.
type SuggestionEvent struct {
	ID           uuid.UUID  `json:"id"`
	SuggestionID uuid.UUID  `json:"suggestion_id"`
	ActorID      *uuid.UUID `json:"actor_id,omitempty"`
	EventType    string     `json:"event_type"`
	FromValue    *string    `json:"from_value,omitempty"`
	ToValue      *string    `json:"to_value,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// SuggestionSummary feeds the dev's header KPIs.
type SuggestionSummary struct {
	ByStatus   map[string]int `json:"by_status"`
	Total      int            `json:"total"`
	OldestOpen *time.Time     `json:"oldest_open,omitempty"`
}

// Suggestions is the repository for the suggestions* tables.
type Suggestions struct {
	pool *pgxpool.Pool
}

// NewSuggestions returns a new Suggestions repo backed by pool.
func NewSuggestions(pool *pgxpool.Pool) *Suggestions { return &Suggestions{pool: pool} }

const suggestionColumns = `id, ref_num, created_by, title, description, type,
       target_screen, requester_priority, status, dev_priority, effort,
       dev_feedback, dev_notes, awaiting_author, resolved_at, created_at, updated_at`

func scanSuggestion(row interface {
	Scan(...any) error
}, s *Suggestion) error {
	return row.Scan(&s.ID, &s.RefNum, &s.CreatedBy, &s.Title, &s.Description,
		&s.Type, &s.TargetScreen, &s.RequesterPriority, &s.Status, &s.DevPriority,
		&s.Effort, &s.DevFeedback, &s.DevNotes, &s.AwaitingAuthor, &s.ResolvedAt,
		&s.CreatedAt, &s.UpdatedAt)
}

// sqlExec is satisfied by both *pgxpool.Pool and pgx.Tx, so addEvent can run
// inside a transaction (Create/Update) or standalone.
type sqlExec interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// addEvent appends one row to the suggestion_events timeline.
func (s *Suggestions) addEvent(ctx context.Context, q sqlExec, sid uuid.UUID, actorID *uuid.UUID, eventType string, from, to *string) error {
	_, err := q.Exec(ctx, `
		INSERT INTO suggestion_events (suggestion_id, actor_id, event_type, from_value, to_value)
		VALUES ($1, $2, $3, $4, $5)`, sid, actorID, eventType, from, to)
	return err
}

// CreateSuggestionInput holds the fields to insert a new suggestion.
type CreateSuggestionInput struct {
	CreatedBy         uuid.UUID
	Title             string
	Description       string
	Type              string
	TargetScreen      string // empty → stored as NULL
	RequesterPriority string
}

// Create inserts a suggestion and its `created` event atomically, returning
// the persisted row (with ref_num).
func (s *Suggestions) Create(ctx context.Context, in CreateSuggestionInput) (*Suggestion, error) {
	var target *string
	if strings.TrimSpace(in.TargetScreen) != "" {
		t := in.TargetScreen
		target = &t
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var sug Suggestion
	row := tx.QueryRow(ctx, `
		INSERT INTO suggestions (created_by, title, description, type, target_screen, requester_priority)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+suggestionColumns,
		in.CreatedBy, in.Title, in.Description, in.Type, target, in.RequesterPriority,
	)
	if err := scanSuggestion(row, &sug); err != nil {
		return nil, err
	}
	actor := in.CreatedBy
	if err := s.addEvent(ctx, tx, sug.ID, &actor, "created", nil, nil); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &sug, nil
}

// ListSuggestionsFilter carries the dynamic filters for List. When
// OnlyAuthorID is set, the query is scoped to that author server-side (the
// non-dev caller can never widen it). AuthorID is the dev's optional
// "filter by author" facet.
type ListSuggestionsFilter struct {
	OnlyAuthorID *uuid.UUID // hard server-side scope (non-dev)
	Status       string
	Type         string
	Priority     string // matched against dev_priority (the dev's triage board)
	Query        string // ILIKE on title/description
	AuthorID     *uuid.UUID
	Sort         string // "" (updated_at DESC) | "created_at" | "oldest" | "ref_num"
}

// List returns suggestions matching the filter. Order defaults to
// updated_at DESC.
func (s *Suggestions) List(ctx context.Context, f ListSuggestionsFilter) ([]Suggestion, error) {
	where := []string{"1=1"}
	args := []any{}
	idx := 1
	push := func(cond string, val any) {
		where = append(where, strings.Replace(cond, "$?", "$"+strconv.Itoa(idx), 1))
		args = append(args, val)
		idx++
	}
	if f.OnlyAuthorID != nil {
		push("created_by = $?", *f.OnlyAuthorID)
	} else if f.AuthorID != nil {
		push("created_by = $?", *f.AuthorID)
	}
	if f.Status != "" {
		push("status = $?", f.Status)
	}
	if f.Type != "" {
		push("type = $?", f.Type)
	}
	if f.Priority != "" {
		push("dev_priority = $?", f.Priority)
	}
	if f.Query != "" {
		like := "%" + strings.ToLower(f.Query) + "%"
		where = append(where, "(LOWER(title) LIKE $"+strconv.Itoa(idx)+
			" OR LOWER(description) LIKE $"+strconv.Itoa(idx+1)+")")
		args = append(args, like, like)
		idx += 2
	}

	order := "updated_at DESC"
	switch f.Sort {
	case "created_at":
		order = "created_at DESC"
	case "oldest":
		order = "created_at ASC"
	case "ref_num":
		order = "ref_num DESC"
	}

	q := `SELECT ` + suggestionColumns + ` FROM suggestions WHERE ` +
		strings.Join(where, " AND ") + ` ORDER BY ` + order
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Suggestion
	for rows.Next() {
		var sug Suggestion
		if err := scanSuggestion(rows, &sug); err != nil {
			return nil, err
		}
		out = append(out, sug)
	}
	return out, rows.Err()
}

// Get returns one suggestion by ID. Returns pgx.ErrNoRows when not found.
func (s *Suggestions) Get(ctx context.Context, id uuid.UUID) (*Suggestion, error) {
	var sug Suggestion
	row := s.pool.QueryRow(ctx,
		`SELECT `+suggestionColumns+` FROM suggestions WHERE id = $1`, id)
	if err := scanSuggestion(row, &sug); err != nil {
		return nil, err
	}
	return &sug, nil
}

// UpdateSuggestionInput is the dev-only partial update (triage/management).
// nil pointer == field unchanged. ActorID is the dev performing the change —
// it authors the timeline events.
type UpdateSuggestionInput struct {
	ActorID        uuid.UUID
	Status         *string
	DevPriority    *string
	Effort         *string
	DevFeedback    *string
	DevNotes       *string
	AwaitingAuthor *bool
}

func isResolvedStatus(st string) bool { return st == "concluida" || st == "recusada" }

// Update applies the partial change, refreshes updated_at, sets/clears
// resolved_at on status transitions, writes the appropriate timeline events
// (status_changed/reopened, priority_changed, feedback_given), and returns the
// updated row — all in one transaction.
func (s *Suggestions) Update(ctx context.Context, id uuid.UUID, in UpdateSuggestionInput) (*Suggestion, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var cur Suggestion
	if err := scanSuggestion(tx.QueryRow(ctx,
		`SELECT `+suggestionColumns+` FROM suggestions WHERE id = $1 FOR UPDATE`, id), &cur); err != nil {
		return nil, err
	}

	sets := []string{"updated_at = NOW()"}
	args := []any{}
	idx := 1
	push := func(col string, val any) {
		sets = append(sets, col+" = $"+strconv.Itoa(idx))
		args = append(args, val)
		idx++
	}

	// Collect events to write after the UPDATE succeeds.
	type pendingEvent struct {
		eventType string
		from, to  *string
	}
	var events []pendingEvent
	actor := in.ActorID

	if in.Status != nil && *in.Status != cur.Status {
		push("status", *in.Status)
		switch {
		case isResolvedStatus(*in.Status):
			sets = append(sets, "resolved_at = NOW()")
		case isResolvedStatus(cur.Status):
			// Reopening a resolved suggestion — clear the timestamp.
			sets = append(sets, "resolved_at = NULL")
		}
		etype := "status_changed"
		if isResolvedStatus(cur.Status) && !isResolvedStatus(*in.Status) {
			etype = "reopened"
		}
		from := cur.Status
		events = append(events, pendingEvent{etype, &from, in.Status})
	}
	if in.DevPriority != nil && (cur.DevPriority == nil || *cur.DevPriority != *in.DevPriority) {
		push("dev_priority", *in.DevPriority)
		events = append(events, pendingEvent{"priority_changed", cur.DevPriority, in.DevPriority})
	}
	if in.Effort != nil {
		push("effort", *in.Effort)
	}
	if in.DevFeedback != nil {
		changed := cur.DevFeedback == nil || *cur.DevFeedback != *in.DevFeedback
		push("dev_feedback", *in.DevFeedback)
		if changed && strings.TrimSpace(*in.DevFeedback) != "" {
			events = append(events, pendingEvent{"feedback_given", nil, nil})
		}
	}
	if in.DevNotes != nil {
		push("dev_notes", *in.DevNotes)
	}
	if in.AwaitingAuthor != nil {
		push("awaiting_author", *in.AwaitingAuthor)
	}

	args = append(args, id)
	var updated Suggestion
	q := `UPDATE suggestions SET ` + strings.Join(sets, ", ") +
		` WHERE id = $` + strconv.Itoa(idx) + ` RETURNING ` + suggestionColumns
	if err := scanSuggestion(tx.QueryRow(ctx, q, args...), &updated); err != nil {
		return nil, err
	}

	for _, e := range events {
		if err := s.addEvent(ctx, tx, id, &actor, e.eventType, e.from, e.to); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &updated, nil
}

// ListComments returns the thread of a suggestion, oldest first.
func (s *Suggestions) ListComments(ctx context.Context, sid uuid.UUID) ([]SuggestionComment, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, suggestion_id, author_id, body, created_at
		FROM suggestion_comments
		WHERE suggestion_id = $1
		ORDER BY created_at ASC`, sid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SuggestionComment
	for rows.Next() {
		var c SuggestionComment
		if err := rows.Scan(&c.ID, &c.SuggestionID, &c.AuthorID, &c.Body, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// AddComment appends a comment to a suggestion's thread.
func (s *Suggestions) AddComment(ctx context.Context, sid, authorID uuid.UUID, body string) (*SuggestionComment, error) {
	var c SuggestionComment
	row := s.pool.QueryRow(ctx, `
		INSERT INTO suggestion_comments (suggestion_id, author_id, body)
		VALUES ($1, $2, $3)
		RETURNING id, suggestion_id, author_id, body, created_at`,
		sid, authorID, body)
	if err := row.Scan(&c.ID, &c.SuggestionID, &c.AuthorID, &c.Body, &c.CreatedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

// ListAttachments returns all attachments of a suggestion (root + comment).
func (s *Suggestions) ListAttachments(ctx context.Context, sid uuid.UUID) ([]SuggestionAttachment, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, suggestion_id, comment_id, storage_key, content_type, size_bytes, uploaded_by, created_at
		FROM suggestion_attachments
		WHERE suggestion_id = $1
		ORDER BY created_at ASC`, sid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SuggestionAttachment
	for rows.Next() {
		var a SuggestionAttachment
		if err := rows.Scan(&a.ID, &a.SuggestionID, &a.CommentID, &a.StorageKey,
			&a.ContentType, &a.SizeBytes, &a.UploadedBy, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AddAttachmentInput holds the fields to record an uploaded attachment.
type AddAttachmentInput struct {
	SuggestionID uuid.UUID
	CommentID    *uuid.UUID
	StorageKey   string
	ContentType  string
	SizeBytes    int64
	UploadedBy   uuid.UUID
}

// AddAttachment records an attachment row (the object is already in storage)
// and writes an attachment_added event.
func (s *Suggestions) AddAttachment(ctx context.Context, in AddAttachmentInput) (*SuggestionAttachment, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var a SuggestionAttachment
	row := tx.QueryRow(ctx, `
		INSERT INTO suggestion_attachments
			(suggestion_id, comment_id, storage_key, content_type, size_bytes, uploaded_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, suggestion_id, comment_id, storage_key, content_type, size_bytes, uploaded_by, created_at`,
		in.SuggestionID, in.CommentID, in.StorageKey, in.ContentType, in.SizeBytes, in.UploadedBy)
	if err := row.Scan(&a.ID, &a.SuggestionID, &a.CommentID, &a.StorageKey,
		&a.ContentType, &a.SizeBytes, &a.UploadedBy, &a.CreatedAt); err != nil {
		return nil, err
	}
	actor := in.UploadedBy
	if err := s.addEvent(ctx, tx, in.SuggestionID, &actor, "attachment_added", nil, nil); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &a, nil
}

// GetAttachment returns one attachment by ID. Returns pgx.ErrNoRows when
// not found.
func (s *Suggestions) GetAttachment(ctx context.Context, id uuid.UUID) (*SuggestionAttachment, error) {
	var a SuggestionAttachment
	row := s.pool.QueryRow(ctx, `
		SELECT id, suggestion_id, comment_id, storage_key, content_type, size_bytes, uploaded_by, created_at
		FROM suggestion_attachments WHERE id = $1`, id)
	if err := row.Scan(&a.ID, &a.SuggestionID, &a.CommentID, &a.StorageKey,
		&a.ContentType, &a.SizeBytes, &a.UploadedBy, &a.CreatedAt); err != nil {
		return nil, err
	}
	return &a, nil
}

// ListEvents returns the activity timeline of a suggestion, oldest first.
func (s *Suggestions) ListEvents(ctx context.Context, sid uuid.UUID) ([]SuggestionEvent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, suggestion_id, actor_id, event_type, from_value, to_value, created_at
		FROM suggestion_events
		WHERE suggestion_id = $1
		ORDER BY created_at ASC`, sid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SuggestionEvent
	for rows.Next() {
		var e SuggestionEvent
		if err := rows.Scan(&e.ID, &e.SuggestionID, &e.ActorID, &e.EventType,
			&e.FromValue, &e.ToValue, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// MarkRead upserts the caller's last_read_at for a suggestion to NOW().
func (s *Suggestions) MarkRead(ctx context.Context, userID, sid uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO suggestion_reads (user_id, suggestion_id, last_read_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (user_id, suggestion_id) DO UPDATE SET last_read_at = NOW()`,
		userID, sid)
	return err
}

// Summary returns per-status counts, the total, and the created_at of the
// oldest still-open suggestion (status ∉ concluida/recusada).
func (s *Suggestions) Summary(ctx context.Context) (SuggestionSummary, error) {
	out := SuggestionSummary{ByStatus: map[string]int{}}
	rows, err := s.pool.Query(ctx, `SELECT status, COUNT(*) FROM suggestions GROUP BY status`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var st string
		var c int
		if err := rows.Scan(&st, &c); err != nil {
			return out, err
		}
		out.ByStatus[st] = c
		out.Total += c
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	var oldest *time.Time
	if err := s.pool.QueryRow(ctx,
		`SELECT MIN(created_at) FROM suggestions WHERE status NOT IN ('concluida','recusada')`).
		Scan(&oldest); err != nil {
		return out, err
	}
	out.OldestOpen = oldest
	return out, nil
}

// UnreadCount returns the badge count for a caller.
//
//   - dev  → suggestions with any comment/event newer than the dev's last_read
//     (never-read counts as unread).
//   - autor → the caller's OWN suggestions that have DEV activity (an event by
//     someone other than the author, or a comment by a non-author) newer than
//     the author's last_read.
func (s *Suggestions) UnreadCount(ctx context.Context, userID uuid.UUID, isDev bool) (int, error) {
	var count int
	if isDev {
		err := s.pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM suggestions s
			WHERE EXISTS (
				SELECT 1 FROM (
					SELECT created_at FROM suggestion_comments WHERE suggestion_id = s.id
					UNION ALL
					SELECT created_at FROM suggestion_events   WHERE suggestion_id = s.id
				) act
				WHERE act.created_at > COALESCE(
					(SELECT last_read_at FROM suggestion_reads
					 WHERE user_id = $1 AND suggestion_id = s.id),
					'-infinity'::timestamptz)
			)`, userID).Scan(&count)
		return count, err
	}
	err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM suggestions s
		WHERE s.created_by = $1
		  AND EXISTS (
			SELECT 1 FROM (
				SELECT created_at FROM suggestion_events
					WHERE suggestion_id = s.id AND (actor_id IS NULL OR actor_id <> $1)
				UNION ALL
				SELECT created_at FROM suggestion_comments
					WHERE suggestion_id = s.id AND (author_id IS NULL OR author_id <> $1)
			) act
			WHERE act.created_at > COALESCE(
				(SELECT last_read_at FROM suggestion_reads
				 WHERE user_id = $1 AND suggestion_id = s.id),
				'-infinity'::timestamptz)
		  )`, userID).Scan(&count)
	return count, err
}
