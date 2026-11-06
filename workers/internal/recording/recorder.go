package recording

import (
	"context"
	"errors"
	"fmt"
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

// WaitRecorders espera os gravadores saírem e os e-mails em andamento terminarem,
// ou o timeout, o que vier primeiro. Chamar no desligamento, depois de cancelar o
// ctx do Run: o ffmpeg precisa de tempo depois do SIGTERM para gravar o que tem em
// buffer (até 16s de áudio).
//
// Depois desta chamada não nasce gravador novo nem e-mail novo: o tick pode estar
// no meio do laço, e um Add com o contador zerado, concorrente ao Wait, é uso
// inválido de WaitGroup.
func (m *Manager) WaitRecorders(timeout time.Duration) {
	m.mu.Lock()
	m.draining = true
	m.mu.Unlock()

	done := make(chan struct{})
	go func() {
		m.recorders.Wait()
		m.notifications.Wait()
		close(done)
	}()
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
		m.log.Warn("recording: gravador ou e-mail não terminou a tempo no desligamento", zap.Duration("timeout", timeout))
	}
}

// attemptSlot guarda a tentativa em curso para o watchdog poder derrubá-la.
type attemptSlot struct {
	mu      sync.Mutex
	cancel  context.CancelFunc
	started time.Time
}

func (s *attemptSlot) set(c context.CancelFunc) {
	s.mu.Lock()
	s.cancel, s.started = c, time.Now()
	s.mu.Unlock()
}

// clear marca que a tentativa acabou. Entre tentativas não há nada para derrubar.
func (s *attemptSlot) clear() {
	s.mu.Lock()
	s.cancel = nil
	s.mu.Unlock()
}

// restartIfOlder derruba a tentativa em curso se ela tiver pelo menos minAge, e diz
// se derrubou. Zera o cancel para não contar de novo enquanto o ffmpeg fecha.
func (s *attemptSlot) restartIfOlder(minAge time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel == nil || time.Since(s.started) < minAge {
		return false
	}
	s.cancel()
	s.cancel = nil
	return true
}

// record captura até o fim da janela, reconectando sempre que preciso.
//
// liveCtx cancelado (API desligando ou admin cancelando) sai sem mexer no status:
// no desligamento a linha precisa ficar `gravando` para ser retomada no boot, e
// no cancelamento quem mudou o status foi o próprio Cancel.
//
// A linha recebida pode estar velha: o admin pode ter cancelado depois de o tick
// listar `gravando`, e o stopRecorder não teria o que parar. Capturar mesmo assim
// gravaria depois do cancelamento e seguraria a finalização até ends_at. Por isso
// relê o status antes de capturar, e sem resposta do banco também não captura: o
// próximo tick tenta de novo. O gravador já está em live neste ponto, então um
// cancelamento depois desta leitura encontra o que parar.
func (m *Manager) record(liveCtx context.Context, rec Recording) {
	current, err := m.repo.Get(liveCtx, rec.ID)
	if err != nil {
		m.log.Warn("recording: conferir status falhou; o tick tenta de novo",
			zap.Stringer("id", rec.ID), zap.Error(err))
		return
	}
	if current.Status != StatusGravando {
		return
	}

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
	// Antes de marcar processando, para o watchdog não disputar com a finalização.
	// Não prende: o e-mail sai em goroutine própria e o banco respeita windowCtx.
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

// errStationDeleted é o que ErrNotFound de StreamURL significa para quem lê o
// last_error: a gravação existe, e o JOIN só falha sem a emissora.
var errStationDeleted = errors.New("a emissora foi excluída")

// stalledMessage é o last_error de quem morreu pelo watchdog. Sem ele, um ffmpeg
// pendurado terminaria em `falhou` sem motivo nenhum, porque Capture não devolve
// erro quando é cancelado.
func stalledMessage(after time.Duration) string {
	return fmt.Sprintf("stream sem dados há %ds", int(after/time.Second))
}

func (m *Manager) attempt(ctx context.Context, rec Recording, dir string, slot *attemptSlot) {
	// A cada tentativa: o sidecar segments-cleanup apaga pasta vazia, o que
	// acontece justamente com o stream fora.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		m.captureFailed(ctx, rec, err)
		return
	}
	url, err := m.repo.StreamURL(ctx, rec.ID)
	if errors.Is(err, ErrNotFound) {
		err = errStationDeleted
	}
	if err != nil {
		m.captureFailed(ctx, rec, err)
		return
	}
	attemptCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	slot.set(cancel)
	defer slot.clear()
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
//
// Alerta e morte são regras separadas. O alerta segue só o relógio de bytes, que não
// zera entre tentativas: com o stream fora, cada ffmpeg desiste sozinho e o alerta
// nunca sairia. A morte exige também idade de StallAfter, porque quando o stream
// volta um ffmpeg saudável ainda leva ~16s até a primeira escrita.
func (m *Manager) watch(ctx context.Context, rec Recording, dir string, slot *attemptSlot) {
	t := time.NewTicker(m.cfg.PollEvery)
	defer t.Stop()
	last := DirBytes(dir)
	lastGrowth := time.Now()
	alerted := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		// Com os dois prontos o select sorteia, e o banco pode ter segurado o laço até
		// o fim da janela: sem isto o watchdog agiria depois de ela acabar.
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
		if slot.restartIfOlder(m.cfg.StallAfter) {
			if err := m.repo.SetLastError(ctx, rec.ID, stalledMessage(m.cfg.StallAfter)); err != nil {
				m.log.Warn("recording: guardar erro falhou", zap.Stringer("id", rec.ID), zap.Error(err))
			}
		}
		if alerted {
			continue
		}

		first, err := m.repo.MarkDropAlerted(ctx, rec.ID)
		if err != nil {
			m.log.Warn("recording: marcar alerta falhou", zap.Stringer("id", rec.ID), zap.Error(err))
			continue
		}
		alerted = true
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
