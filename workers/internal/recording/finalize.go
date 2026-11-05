package recording

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"
)

func objectKey(r Recording) string {
	return fmt.Sprintf("recordings/%s/%s.m4a", r.StartsAt.UTC().Format("2006/01"), r.ID)
}

// startFinalize roda em goroutine para um arquivo grande não atrasar o tick, e
// com isso o início de outra gravação. Enquanto o gravador ainda fecha o último
// pedaço (cancelamento), espera o próximo tick.
func (m *Manager) startFinalize(ctx context.Context, rec Recording) {
	m.mu.Lock()
	_, running := m.finalizing[rec.ID]
	_, recording := m.live[rec.ID]
	if running || recording {
		m.mu.Unlock()
		return
	}
	m.finalizing[rec.ID] = struct{}{}
	m.mu.Unlock()

	go func() {
		defer func() {
			m.mu.Lock()
			delete(m.finalizing, rec.ID)
			m.mu.Unlock()
		}()
		if err := m.finalize(ctx, rec); err != nil {
			m.log.Warn("recording: finalização falhou, repete no próximo tick",
				zap.Stringer("id", rec.ID), zap.Error(err))
		}
	}()
}

// finalize é idempotente: a chave do objeto é fixa e os pedaços só somem depois
// do Complete, então qualquer falha aqui é repetida sem perder áudio.
func (m *Manager) finalize(ctx context.Context, rec Recording) error {
	dir := m.dirFor(rec.ID)
	pieces, err := ListPieces(dir)
	if err != nil {
		return err
	}

	canceled := rec.CanceledAt != nil
	end := rec.EndsAt
	if canceled && rec.CanceledAt.Before(end) {
		end = *rec.CanceledAt
	}
	window := end.Sub(rec.StartsAt)
	gaps := ComputeGaps(pieces, rec.StartsAt, end, m.cfg.MinGap)
	now := m.now()

	if len(pieces) == 0 {
		status := FinalStatus(false, canceled, gaps)
		if err := m.repo.Complete(ctx, rec.ID, CompleteInput{Status: status, Gaps: gaps, FinishedAt: now}); err != nil {
			return err
		}
		os.RemoveAll(dir) //nolint:errcheck
		if status == StatusFalhou {
			m.notifyFailed(rec)
		}
		return nil
	}

	joined := filepath.Join(dir, "joined.tmp")
	out := filepath.Join(dir, "out.m4a")
	if err := Concat(pieces, joined); err != nil {
		return err
	}
	if err := m.media.Remux(ctx, joined, out); err != nil {
		return err
	}
	covered, err := m.media.Duration(ctx, out)
	if err != nil {
		return err
	}
	info, err := os.Stat(out)
	if err != nil {
		return fmt.Errorf("recording: stat: %w", err)
	}

	// *os.File é seekable, o que o S3 exige para assinar o corpo.
	f, err := os.Open(out)
	if err != nil {
		return fmt.Errorf("recording: abrir m4a: %w", err)
	}
	defer f.Close()
	key := objectKey(rec)
	if err := m.objects.Put(ctx, key, f, "audio/mp4"); err != nil {
		return err
	}
	f.Close()

	status := FinalStatus(true, canceled, gaps)
	size := info.Size()
	secs := int(covered.Seconds())
	expires := now.Add(Retention)
	if err := m.repo.Complete(ctx, rec.ID, CompleteInput{
		Status: status, ObjectKey: &key, SizeBytes: &size, CoveredSeconds: &secs,
		Gaps: gaps, FinishedAt: now, ExpiresAt: &expires,
	}); err != nil {
		return err
	}
	os.RemoveAll(dir) //nolint:errcheck
	if status == StatusParcial {
		m.notifyPartial(rec, covered, window, gaps)
	}
	return nil
}
