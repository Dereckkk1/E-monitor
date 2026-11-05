package recording

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

type fakeRepo struct {
	mu   sync.Mutex
	rows map[uuid.UUID]*Recording
	url  string
	// alerted espelha drop_alerted_at.
	alerted map[uuid.UUID]bool
	// completeErr força falha no Complete.
	completeErr error
}

func newFakeRepo(rows ...Recording) *fakeRepo {
	f := &fakeRepo{rows: map[uuid.UUID]*Recording{}, alerted: map[uuid.UUID]bool{}, url: "http://x/s"}
	for i := range rows {
		r := rows[i]
		f.rows[r.ID] = &r
	}
	return f
}

func (f *fakeRepo) snapshot(id uuid.UUID) Recording {
	f.mu.Lock()
	defer f.mu.Unlock()
	return *f.rows[id]
}

func (f *fakeRepo) StationInfo(_ context.Context, _ uuid.UUID) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return "Radio", f.url, nil
}
func (f *fakeRepo) Insert(_ context.Context, st uuid.UUID, name string, by *uuid.UUID, from, to time.Time) (uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := uuid.New()
	f.rows[id] = &Recording{ID: id, StationID: &st, StationName: name, CreatedBy: by, StartsAt: from, EndsAt: to, Status: StatusProgramada, Gaps: []Gap{}}
	return id, nil
}
func (f *fakeRepo) Get(_ context.Context, id uuid.UUID) (*Recording, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[id]
	if !ok {
		return nil, ErrNotFound
	}
	c := *r
	return &c, nil
}
func (f *fakeRepo) List(_ context.Context, _, _ int) ([]Recording, int, error) { return nil, 0, nil }
func (f *fakeRepo) ListByStatus(_ context.Context, s string) ([]Recording, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Recording
	for _, r := range f.rows {
		if r.Status == s {
			out = append(out, *r)
		}
	}
	return out, nil
}
func (f *fakeRepo) ClaimDue(_ context.Context, startBefore, now time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, r := range f.rows {
		if r.Status == StatusProgramada && !r.StartsAt.After(startBefore) && r.EndsAt.After(now) {
			r.Status = StatusGravando
			n++
		}
	}
	return n, nil
}
func (f *fakeRepo) FailMissed(_ context.Context, now time.Time, reason string) ([]Recording, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Recording
	for _, r := range f.rows {
		if r.Status == StatusProgramada && !r.EndsAt.After(now) {
			r.Status = StatusFalhou
			r.LastError = &reason
			out = append(out, *r)
		}
	}
	return out, nil
}
func (f *fakeRepo) StreamURL(_ context.Context, _ uuid.UUID) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.url, nil
}
func (f *fakeRepo) MarkProcessing(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rows[id].Status == StatusGravando {
		f.rows[id].Status = StatusProcessando
	}
	return nil
}
func (f *fakeRepo) MarkDropAlerted(_ context.Context, id uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.alerted[id] {
		return false, nil
	}
	f.alerted[id] = true
	return true, nil
}
func (f *fakeRepo) SetLastError(_ context.Context, _ uuid.UUID, _ string) error { return nil }
func (f *fakeRepo) Complete(_ context.Context, id uuid.UUID, in CompleteInput) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.completeErr != nil {
		return f.completeErr
	}
	r := f.rows[id]
	if r.Status != StatusProcessando {
		return ErrBusy
	}
	r.Status, r.ObjectKey, r.SizeBytes, r.CoveredSeconds = in.Status, in.ObjectKey, in.SizeBytes, in.CoveredSeconds
	r.Gaps, r.FinishedAt, r.ExpiresAt = in.Gaps, &in.FinishedAt, in.ExpiresAt
	r.HasAudio = in.ObjectKey != nil
	return nil
}
func (f *fakeRepo) CancelScheduled(_ context.Context, id uuid.UUID, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rows[id].Status != StatusProgramada {
		return ErrBusy
	}
	f.rows[id].Status, f.rows[id].CanceledAt = StatusCancelada, &now
	return nil
}
func (f *fakeRepo) CancelRecording(_ context.Context, id uuid.UUID, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rows[id].Status != StatusGravando {
		return ErrBusy
	}
	f.rows[id].Status, f.rows[id].CanceledAt = StatusProcessando, &now
	return nil
}
func (f *fakeRepo) Delete(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.rows, id)
	return nil
}
func (f *fakeRepo) Expired(_ context.Context, now time.Time) ([]Recording, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Recording
	for _, r := range f.rows {
		if r.ObjectKey != nil && r.ExpiresAt != nil && r.ExpiresAt.Before(now) {
			out = append(out, *r)
		}
	}
	return out, nil
}
func (f *fakeRepo) MarkExpired(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[id].Status, f.rows[id].ObjectKey = StatusExpirada, nil
	return nil
}

type fakeObjects struct {
	mu        sync.Mutex
	puts      map[string][]byte
	deleted   []string
	putErr    error
	deleteErr error
}

func newFakeObjects() *fakeObjects { return &fakeObjects{puts: map[string][]byte{}} }

func (o *fakeObjects) Put(_ context.Context, key string, body io.Reader, _ string) error {
	if o.putErr != nil {
		return o.putErr
	}
	b, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.puts[key] = b
	return nil
}
func (o *fakeObjects) Get(_ context.Context, key string) (io.ReadCloser, string, int64, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	b, ok := o.puts[key]
	if !ok {
		return nil, "", 0, errors.New("sem objeto")
	}
	return io.NopCloser(bytes.NewReader(b)), "audio/mp4", int64(len(b)), nil
}
func (o *fakeObjects) Delete(_ context.Context, key string) error {
	if o.deleteErr != nil {
		return o.deleteErr
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.deleted = append(o.deleted, key)
	return nil
}

type fakeMedia struct {
	capture  func(ctx context.Context, url, dir string) error
	duration time.Duration
}

func (m *fakeMedia) Capture(ctx context.Context, url, dir string) error {
	return m.capture(ctx, url, dir)
}
func (m *fakeMedia) Remux(_ context.Context, in, out string) error {
	b, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	return os.WriteFile(out, b, 0o644)
}
func (m *fakeMedia) Duration(_ context.Context, _ string) (time.Duration, error) {
	return m.duration, nil
}

type sentMail struct{ to, subject, text string }

type fakeMailer struct {
	mu   sync.Mutex
	sent []sentMail
}

func (m *fakeMailer) Send(_ context.Context, to []string, subject, _, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, sentMail{to[0], subject, text})
	return nil
}
func (m *fakeMailer) all() []sentMail {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]sentMail(nil), m.sent...)
}

func newTestManager(t *testing.T, repo Repo, media Media, objects ObjectStore, mail *fakeMailer) *Manager {
	t.Helper()
	m := NewManager(Config{
		Root:       t.TempDir(),
		BaseURL:    "https://e-monitor.online",
		PollEvery:  10 * time.Millisecond,
		StallAfter: 60 * time.Millisecond,
		RetryDelay: 5 * time.Millisecond,
	}, repo, media, objects, mail, zap.NewNop())
	return m
}
