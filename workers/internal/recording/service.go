package recording

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"radiocheck/internal/storage"
)

// Create valida e agenda.
func (m *Manager) Create(ctx context.Context, in CreateInput) (*Recording, error) {
	from, to, err := ParseWindow(in.Day, in.Start, in.End, m.now())
	if err != nil {
		return nil, err
	}
	name, url, err := m.repo.StationInfo(ctx, in.StationID)
	if errors.Is(err, ErrNotFound) {
		return nil, &ValidationError{Msg: "emissora não encontrada"}
	}
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(url) == "" {
		return nil, &ValidationError{Msg: "esta emissora não tem URL de stream cadastrada"}
	}
	id, err := m.repo.Insert(ctx, in.StationID, name, in.CreatedBy, from, to)
	if err != nil {
		return nil, err
	}
	return m.repo.Get(ctx, id)
}

// List pagina as gravações.
func (m *Manager) List(ctx context.Context, limit, offset int) ([]Recording, int, error) {
	return m.repo.List(ctx, limit, offset)
}

// Cancel encerra uma gravação programada ou em andamento. Em andamento, volta depois
// de o gravador parar, o que leva no máximo os ~5s do WaitDelay do ffmpeg.
func (m *Manager) Cancel(ctx context.Context, id uuid.UUID) error {
	rec, err := m.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	switch rec.Status {
	case StatusProgramada:
		return m.repo.CancelScheduled(ctx, id, m.now())
	case StatusGravando:
		if err := m.repo.CancelRecording(ctx, id, m.now()); err != nil {
			return err
		}
		m.stopRecorder(id)
		return nil
	default:
		return ErrBusy
	}
}

// Delete apaga o áudio e depois a linha. Gravação viva precisa ser cancelada antes.
func (m *Manager) Delete(ctx context.Context, id uuid.UUID) error {
	rec, err := m.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if rec.Status == StatusGravando || rec.Status == StatusProcessando {
		return ErrBusy
	}
	if rec.ObjectKey != nil {
		if err := m.objects.Delete(ctx, *rec.ObjectKey); err != nil {
			return err
		}
	}
	return m.repo.Delete(ctx, id)
}

// Audio abre o arquivo. Quem chama fecha o corpo. Objeto ausente no bucket também
// é ErrNoAudio: a linha pode ter expirado ou sido excluída depois da leitura.
func (m *Manager) Audio(ctx context.Context, id uuid.UUID) (io.ReadCloser, *Recording, int64, error) {
	rec, err := m.repo.Get(ctx, id)
	if err != nil {
		return nil, nil, 0, err
	}
	if rec.ObjectKey == nil {
		return nil, nil, 0, ErrNoAudio
	}
	body, _, size, err := m.objects.Get(ctx, *rec.ObjectKey)
	if storage.IsNotFound(err) {
		m.log.Warn("recording: objeto ausente no bucket",
			zap.Stringer("id", id), zap.String("key", *rec.ObjectKey))
		return nil, nil, 0, ErrNoAudio
	}
	if err != nil {
		return nil, nil, 0, err
	}
	return body, rec, size, nil
}
