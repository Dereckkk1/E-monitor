package recording

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store é o acesso à tabela recordings.
type Store struct{ pool *pgxpool.Pool }

// NewStore cria o Store.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const selectCols = `
	r.id, r.station_id, r.station_name, r.created_by, COALESCE(u.email, ''),
	r.starts_at, r.ends_at, r.status, r.object_key, r.size_bytes, r.covered_seconds,
	r.gaps, r.last_error, r.canceled_at, r.finished_at, r.expires_at, r.created_at`

const fromRecordings = ` FROM recordings r LEFT JOIN users u ON u.id = r.created_by `

func scanRecording(row pgx.Row) (*Recording, error) {
	var (
		r    Recording
		gaps []byte
	)
	err := row.Scan(&r.ID, &r.StationID, &r.StationName, &r.CreatedBy, &r.CreatorEmail,
		&r.StartsAt, &r.EndsAt, &r.Status, &r.ObjectKey, &r.SizeBytes, &r.CoveredSeconds,
		&gaps, &r.LastError, &r.CanceledAt, &r.FinishedAt, &r.ExpiresAt, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("recording: scan: %w", err)
	}
	r.Gaps = []Gap{}
	if err := json.Unmarshal(gaps, &r.Gaps); err != nil {
		return nil, fmt.Errorf("recording: gaps: %w", err)
	}
	r.HasAudio = r.ObjectKey != nil
	return &r, nil
}

func (s *Store) collect(ctx context.Context, sql string, args ...any) ([]Recording, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("recording: query: %w", err)
	}
	defer rows.Close()
	out := []Recording{}
	for rows.Next() {
		r, err := scanRecording(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// StationInfo devolve nome e stream da emissora.
func (s *Store) StationInfo(ctx context.Context, id uuid.UUID) (string, string, error) {
	var name, url string
	err := s.pool.QueryRow(ctx, `SELECT name, stream_url FROM stations WHERE id = $1`, id).Scan(&name, &url)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", fmt.Errorf("recording: emissora: %w", err)
	}
	return name, url, nil
}

// Insert cria a gravação. A checagem de sobreposição evita conexão extra à toa,
// mas não é atômica em READ COMMITTED: dois admins exatamente ao mesmo tempo
// ainda podem passar por ela, e a consequência é só uma conexão a mais com o
// stream.
func (s *Store) Insert(ctx context.Context, stationID uuid.UUID, stationName string, createdBy *uuid.UUID, from, to time.Time) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO recordings (station_id, station_name, created_by, starts_at, ends_at)
		SELECT $1::uuid, $2::text, $3::uuid, $4::timestamptz, $5::timestamptz
		WHERE NOT EXISTS (
			SELECT 1 FROM recordings
			WHERE station_id = $1
			  AND status IN ('programada','gravando')
			  AND starts_at < $5 AND ends_at > $4
		)
		RETURNING id`, stationID, stationName, createdBy, from, to).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrOverlap
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("recording: inserir: %w", err)
	}
	return id, nil
}

// Get devolve uma gravação ou ErrNotFound.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (*Recording, error) {
	return scanRecording(s.pool.QueryRow(ctx, `SELECT `+selectCols+fromRecordings+`WHERE r.id = $1`, id))
}

// List pagina, mais recentes primeiro.
func (s *Store) List(ctx context.Context, limit, offset int) ([]Recording, int, error) {
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM recordings`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("recording: contar: %w", err)
	}
	items, err := s.collect(ctx, `SELECT `+selectCols+fromRecordings+
		`ORDER BY r.starts_at DESC, r.id LIMIT $1 OFFSET $2`, limit, offset)
	return items, total, err
}

// ListByStatus é usado pelo agendador.
func (s *Store) ListByStatus(ctx context.Context, status string) ([]Recording, error) {
	return s.collect(ctx, `SELECT `+selectCols+fromRecordings+`WHERE r.status = $1`, status)
}

// ClaimDue passa a `gravando` o que deve começar até startBefore e ainda não
// terminou em `now`.
func (s *Store) ClaimDue(ctx context.Context, startBefore, now time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE recordings SET status = 'gravando', updated_at = now()
		WHERE status = 'programada' AND starts_at <= $1 AND ends_at > $2`, startBefore, now)
	if err != nil {
		return 0, fmt.Errorf("recording: claim: %w", err)
	}
	return tag.RowsAffected(), nil
}

// FailMissed fecha o que passou inteiro sem nunca começar (API fora do ar).
func (s *Store) FailMissed(ctx context.Context, now time.Time, reason string) ([]Recording, error) {
	rows, err := s.pool.Query(ctx, `
		UPDATE recordings
		SET status = 'falhou', last_error = $2, finished_at = $1, updated_at = now()
		WHERE status = 'programada' AND ends_at <= $1
		RETURNING id`, now, reason)
	if err != nil {
		return nil, fmt.Errorf("recording: fail missed: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, fmt.Errorf("recording: fail missed: %w", err)
	}
	// Estas linhas já viraram `falhou`: um Get que falha não pode descartar as
	// que deram certo, senão o e-mail delas nunca sairia. Devolve o que juntou
	// junto com o primeiro erro.
	out := make([]Recording, 0, len(ids))
	var firstErr error
	for _, id := range ids {
		r, err := s.Get(ctx, id)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		out = append(out, *r)
	}
	return out, firstErr
}

// StreamURL lê a URL atual: relida a cada tentativa, para uma correção feita
// durante a queda valer na reconexão seguinte.
func (s *Store) StreamURL(ctx context.Context, id uuid.UUID) (string, error) {
	var url string
	err := s.pool.QueryRow(ctx, `
		SELECT st.stream_url FROM recordings r
		JOIN stations st ON st.id = r.station_id WHERE r.id = $1`, id).Scan(&url)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("recording: stream url: %w", err)
	}
	return url, nil
}

// MarkProcessing encerra a captura. Só vale para `gravando`, então é idempotente.
func (s *Store) MarkProcessing(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE recordings SET status = 'processando', updated_at = now()
		WHERE id = $1 AND status = 'gravando'`, id)
	return err
}

// MarkDropAlerted devolve true só na primeira chamada por gravação.
func (s *Store) MarkDropAlerted(ctx context.Context, id uuid.UUID) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE recordings SET drop_alerted_at = now(), updated_at = now()
		WHERE id = $1 AND drop_alerted_at IS NULL`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// SetLastError guarda o motivo da última falha de captura.
func (s *Store) SetLastError(ctx context.Context, id uuid.UUID, msg string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE recordings SET last_error = $2, updated_at = now() WHERE id = $1`, id, msg)
	return err
}

// Complete fecha uma gravação em `processando`. Devolve ErrBusy se ela não estava
// nesse estado.
func (s *Store) Complete(ctx context.Context, id uuid.UUID, in CompleteInput) error {
	gaps, err := json.Marshal(in.Gaps)
	if err != nil {
		return fmt.Errorf("recording: gaps: %w", err)
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE recordings
		SET status = $2, object_key = $3, size_bytes = $4, covered_seconds = $5,
		    gaps = $6, finished_at = $7, expires_at = $8, updated_at = now()
		WHERE id = $1 AND status = 'processando'`,
		id, in.Status, in.ObjectKey, in.SizeBytes, in.CoveredSeconds, gaps, in.FinishedAt, in.ExpiresAt)
	if err != nil {
		return fmt.Errorf("recording: concluir: %w", err)
	}
	// 0 linhas: a gravação não está em `processando` (outro caminho já a fechou).
	// O chamador apaga os pedaços depois de nil, então nil tem que significar
	// que o resultado foi mesmo registrado.
	if tag.RowsAffected() == 0 {
		return ErrBusy
	}
	return nil
}

// CancelScheduled cancela o que ainda não começou.
func (s *Store) CancelScheduled(ctx context.Context, id uuid.UUID, now time.Time) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE recordings
		SET status = 'cancelada', canceled_at = $2, finished_at = $2, updated_at = now()
		WHERE id = $1 AND status = 'programada'`, id, now)
	if err != nil {
		return fmt.Errorf("recording: cancelar: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrBusy
	}
	return nil
}

// CancelRecording encerra uma captura em andamento; o áudio já gravado segue
// para a finalização.
func (s *Store) CancelRecording(ctx context.Context, id uuid.UUID, now time.Time) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE recordings
		SET status = 'processando', canceled_at = $2, updated_at = now()
		WHERE id = $1 AND status = 'gravando'`, id, now)
	if err != nil {
		return fmt.Errorf("recording: cancelar: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrBusy
	}
	return nil
}

// Delete remove a linha.
func (s *Store) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM recordings WHERE id = $1`, id)
	return err
}

// Expired lista o que já pode perder o áudio.
func (s *Store) Expired(ctx context.Context, now time.Time) ([]Recording, error) {
	return s.collect(ctx, `SELECT `+selectCols+fromRecordings+`
		WHERE r.object_key IS NOT NULL AND r.expires_at < $1`, now)
}

// MarkExpired deve ser chamado só depois de o objeto ter sido apagado.
func (s *Store) MarkExpired(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE recordings
		SET status = 'expirada', object_key = NULL, size_bytes = NULL, updated_at = now()
		WHERE id = $1`, id)
	return err
}
