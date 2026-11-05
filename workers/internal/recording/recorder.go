package recording

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

func (m *Manager) startRecorder(ctx context.Context, rec Recording) {
	m.mu.Lock()
	if _, ok := m.live[rec.ID]; ok || m.draining {
		m.mu.Unlock()
		return
	}
	liveCtx, cancel := context.WithCancel(ctx)
	l := &liveRecorder{cancel: cancel, done: make(chan struct{})}
	m.live[rec.ID] = l
	m.recorders.Add(1)
	m.mu.Unlock()

	go func() {
		defer m.recorders.Done()
		defer close(l.done)
		defer func() {
			m.mu.Lock()
			delete(m.live, rec.ID)
			m.mu.Unlock()
		}()
		defer cancel()
		m.record(liveCtx, rec)
	}()
}

// stopRecorder encerra a captura e espera o ffmpeg fechar o último pedaço.
func (m *Manager) stopRecorder(id uuid.UUID) {
	m.mu.Lock()
	l, ok := m.live[id]
	m.mu.Unlock()
	if !ok {
		return
	}
	l.cancel()
	<-l.done
}

// WaitRecorders espera os gravadores saírem, ou o timeout, o que vier primeiro.
// Chamar no desligamento, depois de cancelar o ctx do Run: o ffmpeg precisa de
// tempo depois do SIGTERM para gravar o que tem em buffer (até 16s de áudio).
//
// Depois desta chamada não nasce gravador novo: o tick pode estar no meio do laço,
// e um Add com o contador zerado, concorrente ao Wait, é uso inválido de WaitGroup.
func (m *Manager) WaitRecorders(timeout time.Duration) {
	m.mu.Lock()
	m.draining = true
	m.mu.Unlock()

	done := make(chan struct{})
	go func() {
		m.recorders.Wait()
		close(done)
	}()
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
		m.log.Warn("recording: gravador não saiu a tempo no desligamento", zap.Duration("timeout", timeout))
	}
}

// attemptSlot guarda o cancel da tentativa atual para o watchdog poder derrubá-la.
type attemptSlot struct {
	mu     sync.Mutex
	cancel context.CancelFunc
}

func (s *attemptSlot) set(c context.CancelFunc) {
	s.mu.Lock()
	s.cancel = c
	s.mu.Unlock()
}

func (s *attemptSlot) restart() {
	s.mu.Lock()
	c := s.cancel
	s.mu.Unlock()
	if c != nil {
		c()
	}
}

// record captura até o fim da janela, reconectando sempre que preciso.
//
// liveCtx cancelado (API desligando ou admin cancelando) sai sem mexer no status:
// no desligamento a linha precisa ficar `gravando` para ser retomada no boot, e
// no cancelamento quem mudou o status foi o próprio Cancel.
func (m *Manager) record(liveCtx context.Context, rec Recording) {
	dir := m.dirFor(rec.ID)
	windowCtx, cancelWindow := context.WithDeadline(liveCtx, rec.EndsAt)
	defer cancelWindow()

	slot := &attemptSlot{}
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		m.watch(windowCtx, rec, dir, slot)
	}()

	for windowCtx.Err() == nil {
		m.attempt(windowCtx, rec, dir, slot)
		select {
		case <-windowCtx.Done():
		case <-time.After(m.cfg.RetryDelay):
		}
	}
	// Antes de marcar processando: um aviso de queda que sobrasse daqui para a
	// frente correria junto com a finalização. Só atrasa se houver e-mail saindo,
	// e notify limita isso a 30s.
	<-watchDone

	if liveCtx.Err() != nil {
		return
	}
	// Contexto próprio: windowCtx já expirou.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.repo.MarkProcessing(ctx, rec.ID); err != nil {
		m.log.Warn("recording: marcar processando falhou; o tick resolve",
			zap.Stringer("id", rec.ID), zap.Error(err))
	}
}

func (m *Manager) attempt(ctx context.Context, rec Recording, dir string, slot *attemptSlot) {
	// A cada tentativa: o sidecar segments-cleanup apaga pasta vazia, o que
	// acontece justamente com o stream fora.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		m.captureFailed(ctx, rec, err)
		return
	}
	url, err := m.repo.StreamURL(ctx, rec.ID)
	if err != nil {
		m.captureFailed(ctx, rec, err)
		return
	}
	attemptCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	slot.set(cancel)
	if err := m.media.Capture(attemptCtx, url, dir); err != nil {
		m.captureFailed(ctx, rec, err)
	}
}

func (m *Manager) captureFailed(ctx context.Context, rec Recording, cause error) {
	if ctx.Err() != nil {
		return
	}
	m.log.Warn("recording: captura falhou", zap.Stringer("id", rec.ID), zap.Error(cause))
	if err := m.repo.SetLastError(ctx, rec.ID, cause.Error()); err != nil {
		m.log.Warn("recording: guardar erro falhou", zap.Stringer("id", rec.ID), zap.Error(err))
	}
}

// watch mede bytes em disco, não o processo: o ffmpeg pode ficar pendurado, vivo
// e sem escrever (mesma classe do incidente 2026-05-15).
//
// O ffmpeg grava em blocos de ~256 KiB, um a cada ~16s a 128 kbps, então o
// StallAfter de 60s comporta o intervalo normal entre escritas.
func (m *Manager) watch(ctx context.Context, rec Recording, dir string, slot *attemptSlot) {
	t := time.NewTicker(m.cfg.PollEvery)
	defer t.Stop()
	last := DirBytes(dir)
	lastGrowth := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		// Com os dois prontos o select sorteia, e o aviso pode ter demorado a janela
		// inteira: sem isto o watchdog agiria depois de ela acabar.
		if ctx.Err() != nil {
			return
		}
		cur := DirBytes(dir)
		if cur > last {
			last, lastGrowth = cur, time.Now()
			continue
		}
		if time.Since(lastGrowth) < m.cfg.StallAfter {
			continue
		}
		lastGrowth = time.Now()
		// Reinicia antes de avisar: SMTP lento não pode segurar um ffmpeg travado.
		slot.restart()

		first, err := m.repo.MarkDropAlerted(ctx, rec.ID)
		if err != nil {
			m.log.Warn("recording: marcar alerta falhou", zap.Stringer("id", rec.ID), zap.Error(err))
			continue
		}
		if !first {
			continue
		}
		if cur == 0 {
			m.notifyNoConnection(rec)
		} else {
			m.notifyDropped(rec)
		}
	}
}
