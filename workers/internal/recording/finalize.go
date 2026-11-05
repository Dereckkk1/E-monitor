package recording

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"
)

func objectKey(r Recording) string {
	return fmt.Sprintf("recordings/%s/%s.m4a", r.StartsAt.UTC().Format("2006/01"), r.ID)
}

// startFinalize roda em goroutine para um arquivo grande não atrasar o tick, e
// com isso o início de outra gravação. Enquanto o gravador ainda fecha o último
// pedaço (cancelamento), espera o próximo tick. Depois de uma falha o id continua
// em finalizing por FinalizeRetry, e é isso que espaça as tentativas.
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
		err := m.finalize(ctx, rec)
		// ErrBusy: outro caminho já fechou a linha. Não há o que repetir, e um
		// motivo gravado aqui ficaria numa gravação encerrada.
		if err == nil || errors.Is(err, ErrBusy) {
			return
		}
		m.log.Warn("recording: finalização falhou, vai repetir",
			zap.Stringer("id", rec.ID), zap.Duration("em", m.cfg.FinalizeRetry), zap.Error(err))
		m.recordFinalizeError(rec, err)

		timer := time.NewTimer(m.cfg.FinalizeRetry)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
		}
	}()
}

// recordFinalizeError mostra o motivo na tela. Contexto próprio: ctx pode ser
// justamente o que cancelou a finalização.
func (m *Manager) recordFinalizeError(rec Recording, cause error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.repo.SetLastError(ctx, rec.ID, cause.Error()); err != nil {
		m.log.Warn("recording: gravar motivo da falha", zap.Stringer("id", rec.ID), zap.Error(err))
	}
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
