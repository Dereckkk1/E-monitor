// Package recording implementa "programar gravação": um trecho de stream
// escolhido pelo admin, gravado por um ffmpeg próprio e guardado por 15 dias.
//
// Fica isolado da detecção de propósito: não usa supervisor, worker nem índice,
// e não altera o monitoring_status da emissora.
//
// Doc: docs/features/scheduled-recording.md
package recording

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

const (
	StatusProgramada  = "programada"
	StatusGravando    = "gravando"
	StatusProcessando = "processando"
	StatusConcluida   = "concluida"
	StatusParcial     = "parcial"
	StatusFalhou      = "falhou"
	StatusCancelada   = "cancelada"
	StatusExpirada    = "expirada"
)

const (
	// MinDuration é a janela efetiva mínima, medida depois de o início ser
	// puxado para agora. Abaixo disso o ffmpeg nem conecta.
	MinDuration = time.Minute
	// MaxDuration é o teto por gravação. Espelha o CHECK da migration 0071.
	MaxDuration = 6 * time.Hour
	// Retention conta a partir do fim da gravação.
	Retention = 15 * 24 * time.Hour
)

// Gap é um intervalo da janela sem áudio. Buraco não vira silêncio no arquivo,
// então depois dele o tempo do player deixa de bater com o relógio: é esta
// lista que mantém a gravação utilizável como prova.
type Gap struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// Recording é uma linha de `recordings`.
type Recording struct {
	ID          uuid.UUID  `json:"id"`
	StationID   *uuid.UUID `json:"station_id"`
	StationName string     `json:"station_name"`
	// Dial, cidade e logo vêm da emissora atual (LEFT JOIN), para a tela mostrar a
	// emissora como o seletor. Nulos se ela foi excluída: o nome acima fica guardado
	// na gravação porque é o que identifica a prova.
	StationFrequencyMHz *float64   `json:"station_frequency_mhz"`
	StationBand         *string    `json:"station_band"`
	StationCity         *string    `json:"station_city"`
	StationState        *string    `json:"station_state"`
	StationLogoURL      *string    `json:"station_logo_url"`
	CreatedBy           *uuid.UUID `json:"created_by"`
	CreatorEmail        string     `json:"created_by_email"`
	StartsAt            time.Time  `json:"starts_at"`
	EndsAt              time.Time  `json:"ends_at"`
	Status              string     `json:"status"`
	ObjectKey           *string    `json:"-"`
	HasAudio            bool       `json:"has_audio"`
	SizeBytes           *int64     `json:"size_bytes"`
	CoveredSeconds      *int       `json:"covered_seconds"`
	Gaps                []Gap      `json:"gaps"`
	LastError           *string    `json:"last_error"`
	CanceledAt          *time.Time `json:"canceled_at"`
	FinishedAt          *time.Time `json:"finished_at"`
	ExpiresAt           *time.Time `json:"expires_at"`
	CreatedAt           time.Time  `json:"created_at"`
}

// CreateInput é o que o formulário manda.
type CreateInput struct {
	StationID uuid.UUID
	Day       string // AAAA-MM-DD, horário de São Paulo
	Start     string // HH:MM
	End       string // HH:MM
	CreatedBy *uuid.UUID
}

// CompleteInput fecha uma gravação em `processando`.
type CompleteInput struct {
	Status         string
	ObjectKey      *string
	SizeBytes      *int64
	CoveredSeconds *int
	Gaps           []Gap
	FinishedAt     time.Time
	ExpiresAt      *time.Time
}

var (
	ErrNotFound = errors.New("gravação não encontrada")
	ErrNoAudio  = errors.New("gravação sem áudio")
	ErrOverlap  = errors.New("já existe gravação nesse horário para esta emissora")
	ErrBusy     = errors.New("a gravação está em um estado que não permite esta ação")
)

// ValidationError carrega uma mensagem que pode ir direto para a tela.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }
