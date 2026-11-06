package recording

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	// finalizeStuckAfter é a falha seguida que gera o aviso: uma falha transitória
	// de bucket não pode virar alarme.
	finalizeStuckAfter = 3
	// maxDurationDrift é a folga do m4a em relação aos frames entregues ao remux. A
	// razão medida contra o ffmpeg real foi 1,000000, então cobre só arredondamento.
	maxDurationDrift = time.Second
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
		runCtx, cancel := context.WithTimeout(ctx, m.cfg.FinalizeTimeout)
		defer cancel()
		err := m.finalize(runCtx, rec)
		// ErrBusy: outro caminho já fechou a linha. Não há o que repetir, e um
		// motivo gravado aqui ficaria numa gravação encerrada.
		if err == nil || errors.Is(err, ErrBusy) {
			m.clearFinalizeFailures(rec.ID)
			return
		}
		// Reinício da API: a finalização retoma sozinha no boot, não é falha. Olha
		// ctx, não runCtx: estourar FinalizeTimeout conta como falha.
		if ctx.Err() != nil {
			m.log.Info("recording: finalização interrompida pelo desligamento", zap.Stringer("id", rec.ID))
			return
		}
		m.log.Warn("recording: finalização falhou, vai repetir",
			zap.Stringer("id", rec.ID), zap.Duration("em", m.cfg.FinalizeRetry), zap.Error(err))
		m.recordFinalizeError(rec, err)
		if m.countFinalizeFailure(rec.ID) == finalizeStuckAfter {
			m.notifyFinalizeStuck(rec, err)
		}

		timer := time.NewTimer(m.cfg.FinalizeRetry)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
		}
	}()
}

func (m *Manager) countFinalizeFailure(id uuid.UUID) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finalizeFailures[id]++
	return m.finalizeFailures[id]
}

func (m *Manager) clearFinalizeFailures(id uuid.UUID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.finalizeFailures, id)
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

// removeDir roda depois de o resultado estar registrado: a gravação já fechou, e
// uma pasta que sobrou só ocupa disco.
func (m *Manager) removeDir(rec Recording) {
	if err := os.RemoveAll(m.dirFor(rec.ID)); err != nil {
		m.log.Warn("recording: apagar pedaços", zap.Stringer("id", rec.ID), zap.Error(err))
	}
}

// closeWithoutAudio encerra a gravação que não tem nenhum frame para subir.
func (m *Manager) closeWithoutAudio(ctx context.Context, rec Recording, canceled bool, gaps []Gap, now time.Time) error {
	status := FinalStatus(false, canceled, gaps)
	if err := m.repo.Complete(ctx, rec.ID, CompleteInput{Status: status, Gaps: gaps, FinishedAt: now}); err != nil {
		return err
	}
	m.removeDir(rec)
	if status == StatusFalhou {
		m.notifyFailed(rec)
	}
	return nil
}

// finalize é idempotente: a chave do objeto é fixa e, havendo frames para subir, os
// pedaços só somem depois de o m4a passar na conferência pela contagem de frames e
// de o Complete confirmar. Falha nesse caminho é repetida sem perder áudio. O único
// descarte de propósito são até maxADTSFrame bytes por pedaço depois do último frame
// completo: é resto de frame. Acima disso o Concat falha e os pedaços ficam. Por isso
// frames == 0 depois de um Concat sem erro significa que só havia restos.
func (m *Manager) finalize(ctx context.Context, rec Recording) (err error) {
	dir := m.dirFor(rec.ID)
	pieces, err := ListPieces(dir)
	if err != nil {
		return err
	}
	if err := TrimToAudio(pieces); err != nil {
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
		return m.closeWithoutAudio(ctx, rec, canceled, gaps, now)
	}

	joined := filepath.Join(dir, "joined.tmp")
	out := filepath.Join(dir, "out.m4a")
	// Registrado antes do defer do Close, para rodar depois dele: arquivo aberto
	// não se apaga no Windows. Com o bucket fora, o m4a não fica ocupando disco
	// até a próxima tentativa.
	defer func() {
		if err != nil {
			os.Remove(out) //nolint:errcheck
		}
	}()
	frames, err := Concat(pieces, joined)
	if err != nil {
		os.Remove(joined) //nolint:errcheck
		return err
	}
	if frames == 0 {
		os.Remove(joined) //nolint:errcheck
		return m.closeWithoutAudio(ctx, rec, canceled, ComputeGaps(nil, rec.StartsAt, end, m.cfg.MinGap), now)
	}
	err = m.media.Remux(ctx, joined, out)
	os.Remove(joined) //nolint:errcheck
	if err != nil {
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
	// O remux pode parar no meio com exit 0: esta conferência é o que impede um
	// arquivo curto de virar concluida e levar os pedaços embora.
	expected := framesDuration(frames)
	if info.Size() == 0 || covered < expected-maxDurationDrift {
		return fmt.Errorf("recording: m4a incompleto: %d bytes e %s de áudio, esperados %s (%d frames)",
			info.Size(), covered.Round(time.Millisecond), expected.Round(time.Millisecond), frames)
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
	m.removeDir(rec)
	if status == StatusParcial {
		m.notifyPartial(rec, covered, window, gaps)
	}
	return nil
}
