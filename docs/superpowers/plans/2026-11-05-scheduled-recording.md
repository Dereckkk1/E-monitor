# Programar gravação Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Admin escolhe emissora, dia, início e fim; o sistema grava esse trecho do stream e guarda o áudio por 15 dias.

**Architecture:** Pacote novo `workers/internal/recording`, isolado da detecção. Um `Manager` em goroutine dentro da API lê a tabela `recordings`, abre um ffmpeg próprio por gravação (pedaços de 1 min em `/data/segments/_recordings/<id>/`), junta tudo em `.m4a` no fim, sobe para o bucket e apaga depois de 15 dias. O Postgres é a fonte da verdade, então um reinício da API retoma a gravação.

**Tech Stack:** Go 1.26, chi, pgx, golang-migrate, ffmpeg/ffprobe, S3/MinIO, React + TanStack Query, `node:test`.

**Spec:** [docs/superpowers/specs/2026-11-05-scheduled-recording-design.md](../specs/2026-11-05-scheduled-recording-design.md)

---

## Convenções deste plano

- Trabalhar na worktree `C:\tmp\emonitor-gravacoes`, branch `feat/programar-gravacao`. A pasta principal é compartilhada: não mexer nela.
- Toda mensagem de commit termina com:

  ```
  Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
  ```

- Testes Go rodam de dentro de `workers/`, um pacote por vez (`-p 1`). Testes de store exigem `TEST_DATABASE_URL`; sem ela, são pulados (é assim no repo inteiro).
- Banco de teste local: container descartável `rc-gravacao-pg` (porta **55433**), criado na Task 1. `TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55433/radiocheck_test?sslmode=disable"`. Nunca usar `rc-prodcopy` (5544): é cópia de produção.
- Não rodar `npm install` nesta feature: nenhuma dependência nova (regra 5 do `CLAUDE.md`).
- Nada de push nem deploy sem o Dereck pedir.

## Estrutura de arquivos

| Arquivo | Responsabilidade |
|---|---|
| `migrations/0071_recordings.{up,down}.sql` | Tabela `recordings` |
| `workers/internal/recording/types.go` | Status, structs, erros, constantes |
| `workers/internal/recording/window.go` | Valida dia/início/fim em horário de SP |
| `workers/internal/recording/pieces.go` | Pedaços no disco: listar, juntar, achar buracos, decidir status final |
| `workers/internal/recording/ffmpeg.go` | Único arquivo que chama ffmpeg/ffprobe |
| `workers/internal/recording/store.go` | SQL |
| `workers/internal/recording/manager.go` | Agendador, ciclo de vida, retenção |
| `workers/internal/recording/recorder.go` | Loop de captura e watchdog |
| `workers/internal/recording/finalize.go` | Junta, sobe, fecha o status |
| `workers/internal/recording/notify.go` | E-mails |
| `workers/internal/recording/service.go` | Operações chamadas pelo handler |
| `workers/internal/api/handlers/recordings.go` | HTTP |
| `frontend/src/utils/recordings.js` | Formatação pura, testada com `node:test` |
| `frontend/src/pages/AdminRecordingsPage.{jsx,css}` | Tela |

Modificados: `workers/internal/ingestor/ffmpeg.go`, `workers/internal/api/router.go`, `workers/cmd/api/main.go`, `scripts/deploy.sh`, `frontend/src/api/hooks.js`, `frontend/src/App.jsx`, `frontend/src/components/Sidebar.jsx`, `frontend/src/utils/userJourney.js`, docs.

---

## Task 1: Migration

**Files:**
- Create: `migrations/0071_recordings.up.sql`
- Create: `migrations/0071_recordings.down.sql`

- [ ] **Step 1: Conferir o número livre**

Run: `ls migrations | tail -2`
Expected: maior número é `0070`. Se master já tiver avançado (a `feat/mcp-server` disputa esta faixa), usar `max + 1` e ajustar os nomes.

- [ ] **Step 2: Escrever a up**

`migrations/0071_recordings.up.sql`:

```sql
-- Migration 0071 — programar gravação.
--
-- Admin agenda um trecho de um stream para gravar; o áudio fica 15 dias no
-- bucket. Doc: docs/features/scheduled-recording.md
--
-- SAFETY: tabela nova, sem backfill e sem tocar em dado existente. As FKs para
-- stations e users são validadas sobre uma tabela vazia. A regra 4.8 não
-- encontra nada para falhar aqui.
--
-- ON DELETE SET NULL, e não RESTRICT: as linhas 'expirada' ficam como histórico
-- para sempre, e RESTRICT travaria a exclusão de emissora ou usuário.

BEGIN;

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

CREATE TABLE recordings (
  id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  station_id      UUID REFERENCES stations(id) ON DELETE SET NULL,
  station_name    TEXT NOT NULL,
  created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
  starts_at       TIMESTAMPTZ NOT NULL,
  ends_at         TIMESTAMPTZ NOT NULL,
  status          TEXT NOT NULL DEFAULT 'programada'
                  CHECK (status IN ('programada','gravando','processando','concluida',
                                    'parcial','falhou','cancelada','expirada')),
  object_key      TEXT,
  size_bytes      BIGINT,
  covered_seconds INTEGER,
  gaps            JSONB NOT NULL DEFAULT '[]',
  last_error      TEXT,
  drop_alerted_at TIMESTAMPTZ,
  canceled_at     TIMESTAMPTZ,
  finished_at     TIMESTAMPTZ,
  expires_at      TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT recordings_window CHECK (ends_at > starts_at),
  CONSTRAINT recordings_max_duration CHECK (ends_at - starts_at <= INTERVAL '6 hours')
);

CREATE INDEX recordings_pending_idx ON recordings (starts_at)
  WHERE status IN ('programada','gravando','processando');
CREATE INDEX recordings_expiry_idx ON recordings (expires_at)
  WHERE object_key IS NOT NULL;
CREATE INDEX recordings_list_idx ON recordings (starts_at DESC);

COMMIT;
```

- [ ] **Step 3: Escrever a down**

`migrations/0071_recordings.down.sql`:

```sql
-- Reversão da 0071. Perde o histórico de gravações. Os áudios ficam órfãos no
-- bucket em recordings/ e precisam ser apagados à mão.

BEGIN;

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

DROP TABLE IF EXISTS recordings;

COMMIT;
```

- [ ] **Step 4: Aplicar no banco de teste**

```bash
docker run -d --name rc-gravacao-pg -e POSTGRES_USER=radiocheck -e POSTGRES_PASSWORD=radiocheck \
  -e POSTGRES_DB=radiocheck_test -p 55433:5432 postgres:16-alpine

MIGRATE="postgres://radiocheck:radiocheck@host.docker.internal:55433/radiocheck_test?sslmode=disable"
MSYS_NO_PATHCONV=1 docker run --rm -v "C:/tmp/emonitor-gravacoes/migrations:/migrations" \
  migrate/migrate:v4.17.1 -path=/migrations -database "$MIGRATE" up
MSYS_NO_PATHCONV=1 docker run --rm -v "C:/tmp/emonitor-gravacoes/migrations:/migrations" \
  migrate/migrate:v4.17.1 -path=/migrations -database "$MIGRATE" down 1
MSYS_NO_PATHCONV=1 docker run --rm -v "C:/tmp/emonitor-gravacoes/migrations:/migrations" \
  migrate/migrate:v4.17.1 -path=/migrations -database "$MIGRATE" up
```

Expected: a última linha é `71/u recordings`, e o `down 1` mostra `71/d recordings`.

- [ ] **Step 5: Commit**

```bash
git add migrations/0071_recordings.up.sql migrations/0071_recordings.down.sql
git commit -m "feat(gravacao): tabela recordings"
```

---

## Task 2: Tipos e validação da janela

**Files:**
- Create: `workers/internal/recording/types.go`
- Create: `workers/internal/recording/window.go`
- Test: `workers/internal/recording/window_test.go`

- [ ] **Step 1: Escrever os tipos**

`workers/internal/recording/types.go`:

```go
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
	// MaxDuration é o teto por gravação. Espelha o CHECK da migration 0071.
	MaxDuration = 6 * time.Hour
	// Retention conta a partir do fim da gravação.
	Retention = 15 * 24 * time.Hour
	// minCoverage e maxMissing decidem entre concluida e parcial. Os dois
	// existem porque só o percentual reprovaria gravação curta: em 5 minutos,
	// 2% são 6 segundos, menos do que o ffmpeg leva para conectar.
	minCoverage = 0.98
	maxMissing  = 45 * time.Second
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
	ID             uuid.UUID  `json:"id"`
	StationID      *uuid.UUID `json:"station_id"`
	StationName    string     `json:"station_name"`
	CreatedBy      *uuid.UUID `json:"created_by"`
	CreatorEmail   string     `json:"created_by_email"`
	StartsAt       time.Time  `json:"starts_at"`
	EndsAt         time.Time  `json:"ends_at"`
	Status         string     `json:"status"`
	ObjectKey      *string    `json:"-"`
	HasAudio       bool       `json:"has_audio"`
	SizeBytes      *int64     `json:"size_bytes"`
	CoveredSeconds *int       `json:"covered_seconds"`
	Gaps           []Gap      `json:"gaps"`
	LastError      *string    `json:"last_error"`
	CanceledAt     *time.Time `json:"canceled_at"`
	FinishedAt     *time.Time `json:"finished_at"`
	ExpiresAt      *time.Time `json:"expires_at"`
	CreatedAt      time.Time  `json:"created_at"`
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
```

- [ ] **Step 2: Escrever o teste que falha**

`workers/internal/recording/window_test.go`:

```go
package recording

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 2026-11-05 12:00 em São Paulo = 15:00 UTC.
var windowNow = time.Date(2026, 11, 5, 15, 0, 0, 0, time.UTC)

func TestParseWindow(t *testing.T) {
	cases := []struct {
		name               string
		day, start, end    string
		wantErr            string
		wantFrom, wantTo   string // RFC3339 em UTC
	}{
		{name: "horario de SP vira UTC", day: "2026-11-05", start: "14:00", end: "15:00",
			wantFrom: "2026-11-05T17:00:00Z", wantTo: "2026-11-05T18:00:00Z"},
		{name: "exatamente 6h passa", day: "2026-11-05", start: "13:00", end: "19:00",
			wantFrom: "2026-11-05T16:00:00Z", wantTo: "2026-11-05T22:00:00Z"},
		{name: "6h01 nao passa", day: "2026-11-05", start: "13:00", end: "19:01",
			wantErr: "a gravação pode ter no máximo 6 horas"},
		{name: "fim antes do inicio", day: "2026-11-05", start: "23:30", end: "00:30",
			wantErr: "o fim precisa ser depois do início, no mesmo dia"},
		{name: "fim igual ao inicio", day: "2026-11-05", start: "14:00", end: "14:00",
			wantErr: "o fim precisa ser depois do início, no mesmo dia"},
		{name: "inicio ha mais de 1 minuto", day: "2026-11-05", start: "11:58", end: "13:00",
			wantErr: "o início já passou"},
		{name: "inicio ha exatamente 1 minuto comeca agora, sem retroagir", day: "2026-11-05", start: "11:59", end: "13:00",
			wantFrom: "2026-11-05T15:00:00Z", wantTo: "2026-11-05T16:00:00Z"},
		{name: "dia invalido", day: "ontem", start: "14:00", end: "15:00",
			wantErr: "dia ou horário inválido"},
		{name: "hora invalida", day: "2026-11-05", start: "25:00", end: "26:00",
			wantErr: "dia ou horário inválido"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			from, to, err := ParseWindow(c.day, c.start, c.end, windowNow)
			if c.wantErr != "" {
				var ve *ValidationError
				require.True(t, errors.As(err, &ve), "esperava ValidationError, veio %v", err)
				require.Equal(t, c.wantErr, ve.Msg)
				return
			}
			require.NoError(t, err)
			require.Equal(t, c.wantFrom, from.UTC().Format(time.RFC3339))
			require.Equal(t, c.wantTo, to.UTC().Format(time.RFC3339))
		})
	}
}

// Quem escolhe o minuto em curso (form tem resolução de minuto) começa agora.
func TestParseWindow_MinutoCorrenteComecaAgora(t *testing.T) {
	now := windowNow.Add(30 * time.Second) // 12:00:30 em SP
	from, to, err := ParseWindow("2026-11-05", "12:00", "13:00", now)
	require.NoError(t, err)
	require.True(t, from.Equal(now), "from = %s", from)
	require.Equal(t, "2026-11-05T16:00:00Z", to.UTC().Format(time.RFC3339))
}
```

- [ ] **Step 3: Rodar e ver falhar**

Run: `cd workers && go test ./internal/recording/ -run TestParseWindow -v`
Expected: FAIL com `undefined: ParseWindow`.

- [ ] **Step 4: Implementar**

`workers/internal/recording/window.go`:

```go
package recording

import "time"

// saoPaulo cai no fuso fixo se o tzdata faltar: o Brasil não tem horário de
// verão desde 2019, e derrubar o boot da API por isso seria pior (regra 6.5).
var saoPaulo = func() *time.Location {
	if loc, err := time.LoadLocation("America/Sao_Paulo"); err == nil {
		return loc
	}
	return time.FixedZone("America/Sao_Paulo", -3*60*60)
}()

const windowLayout = "2006-01-02 15:04"

// ParseWindow transforma o que veio do formulário no intervalo a gravar.
//
// A folga de 1 minuto existe porque o form tem resolução de minuto: quem quer
// gravar agora escolhe o minuto em curso, que já começou. Nesse caso `from` é
// puxado para `now`, para a janela não nascer com um buraco que nunca existiu.
func ParseWindow(day, start, end string, now time.Time) (time.Time, time.Time, error) {
	from, err := time.ParseInLocation(windowLayout, day+" "+start, saoPaulo)
	if err != nil {
		return time.Time{}, time.Time{}, &ValidationError{"dia ou horário inválido"}
	}
	to, err := time.ParseInLocation(windowLayout, day+" "+end, saoPaulo)
	if err != nil {
		return time.Time{}, time.Time{}, &ValidationError{"dia ou horário inválido"}
	}
	if !to.After(from) {
		return time.Time{}, time.Time{}, &ValidationError{"o fim precisa ser depois do início, no mesmo dia"}
	}
	if to.Sub(from) > MaxDuration {
		return time.Time{}, time.Time{}, &ValidationError{"a gravação pode ter no máximo 6 horas"}
	}
	if from.Before(now.Add(-time.Minute)) {
		return time.Time{}, time.Time{}, &ValidationError{"o início já passou"}
	}
	if from.Before(now) {
		from = now
	}
	return from, to, nil
}
```

- [ ] **Step 5: Rodar e ver passar**

Run: `cd workers && gofmt -w internal/recording && go test ./internal/recording/ -run TestParseWindow -v`
Expected: PASS em todos os casos.

- [ ] **Step 6: Commit**

```bash
git add workers/internal/recording
git commit -m "feat(gravacao): tipos e validacao da janela"
```

---

## Task 3: Pedaços no disco

**Files:**
- Create: `workers/internal/recording/pieces.go`
- Test: `workers/internal/recording/pieces_test.go`

- [ ] **Step 1: Escrever o teste que falha**

`workers/internal/recording/pieces_test.go`:

```go
package recording

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func writePiece(t *testing.T, dir, attempt string, start, end time.Time, content string) {
	t.Helper()
	p := filepath.Join(dir, "a"+attempt+"-"+start.Local().Format("20060102-150405")+pieceExt)
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	require.NoError(t, os.Chtimes(p, end, end))
}

func TestListPieces(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 11, 5, 20, 0, 0, 0, time.Local)
	writePiece(t, dir, "2", t0.Add(time.Minute), t0.Add(2*time.Minute), "bb")
	writePiece(t, dir, "1", t0, t0.Add(time.Minute), "a")
	writePiece(t, dir, "3", t0.Add(2*time.Minute), t0.Add(3*time.Minute), "") // vazio: ignora
	require.NoError(t, os.WriteFile(filepath.Join(dir, "joined.tmp"), []byte("x"), 0o644))

	got, err := ListPieces(dir)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.True(t, got[0].Start.Equal(t0))
	require.True(t, got[0].End.Equal(t0.Add(time.Minute)))
	require.True(t, got[1].Start.Equal(t0.Add(time.Minute)))
}

// O sidecar segments-cleanup apaga pasta vazia: pasta inexistente é o estado
// normal de uma gravação com o stream fora, não um erro.
func TestListPieces_PastaInexistente(t *testing.T) {
	got, err := ListPieces(filepath.Join(t.TempDir(), "nao-existe"))
	require.NoError(t, err)
	require.Empty(t, got)
	require.Zero(t, DirBytes(filepath.Join(t.TempDir(), "nao-existe")))
}

func TestComputeGaps(t *testing.T) {
	t0 := time.Date(2026, 11, 5, 20, 0, 0, 0, time.UTC)
	min := func(n float64) time.Time { return t0.Add(time.Duration(n * float64(time.Minute))) }
	p := func(a, b float64) Piece { return Piece{Start: min(a), End: min(b), Size: 1} }

	t.Run("sem buraco", func(t *testing.T) {
		got := ComputeGaps([]Piece{p(0, 1), p(1, 2), p(2, 3)}, min(0), min(3), 30*time.Second)
		require.Empty(t, got)
		require.NotNil(t, got, "precisa serializar como [] e não null")
	})
	t.Run("buraco no meio", func(t *testing.T) {
		got := ComputeGaps([]Piece{p(0, 1), p(4, 5)}, min(0), min(5), 30*time.Second)
		require.Equal(t, []Gap{{From: min(1), To: min(4)}}, got)
	})
	t.Run("ffmpeg morreu no meio do pedaco: usa o mtime, nao o minuto cheio", func(t *testing.T) {
		got := ComputeGaps([]Piece{p(0, 0.1), p(2.5, 3)}, min(0), min(3), 30*time.Second)
		require.Equal(t, []Gap{{From: min(0.1), To: min(2.5)}}, got)
	})
	t.Run("comeco e fim faltando", func(t *testing.T) {
		got := ComputeGaps([]Piece{p(2, 3)}, min(0), min(5), 30*time.Second)
		require.Equal(t, []Gap{{From: min(0), To: min(2)}, {From: min(3), To: min(5)}}, got)
	})
	t.Run("oscilacao menor que o minimo nao vira buraco", func(t *testing.T) {
		got := ComputeGaps([]Piece{p(0, 1), p(1.2, 2)}, min(0), min(2), 30*time.Second)
		require.Empty(t, got)
	})
	t.Run("sem pedaco nenhum: a janela inteira", func(t *testing.T) {
		got := ComputeGaps(nil, min(0), min(5), 30*time.Second)
		require.Equal(t, []Gap{{From: min(0), To: min(5)}}, got)
	})
	t.Run("pre-roll antes da janela nao gera buraco", func(t *testing.T) {
		got := ComputeGaps([]Piece{p(-0.3, 1), p(1, 2)}, min(0), min(2), 30*time.Second)
		require.Empty(t, got)
	})
}

func TestFinalStatus(t *testing.T) {
	const h = time.Hour
	cases := []struct {
		name            string
		hasAudio, cancel bool
		covered, window time.Duration
		want            string
	}{
		{"cancelada com audio", true, true, 10 * time.Minute, h, StatusCancelada},
		{"cancelada sem audio", false, true, 0, h, StatusCancelada},
		{"sem audio", false, false, 0, h, StatusFalhou},
		{"98% de 1h", true, false, 3528 * time.Second, h, StatusConcluida},
		{"97% de 1h", true, false, 3492 * time.Second, h, StatusParcial},
		{"gravacao curta: faltam 20s de 5min", true, false, 280 * time.Second, 5 * time.Minute, StatusConcluida},
		{"gravacao curta: faltam 60s de 5min", true, false, 240 * time.Second, 5 * time.Minute, StatusParcial},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, FinalStatus(c.hasAudio, c.cancel, c.covered, c.window))
		})
	}
}

func TestConcat(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a"+pieceExt)
	b := filepath.Join(dir, "b"+pieceExt)
	require.NoError(t, os.WriteFile(a, []byte("aaa"), 0o644))
	require.NoError(t, os.WriteFile(b, []byte("bb"), 0o644))
	out := filepath.Join(dir, "joined.tmp")

	require.NoError(t, Concat([]Piece{{Path: a}, {Path: b}}, out))

	got, err := os.ReadFile(out)
	require.NoError(t, err)
	require.Equal(t, "aaabb", string(got))
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd workers && go test ./internal/recording/ -run "TestListPieces|TestComputeGaps|TestFinalStatus|TestConcat"`
Expected: FAIL com `undefined: pieceExt`.

- [ ] **Step 3: Implementar**

`workers/internal/recording/pieces.go`:

```go
package recording

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

// pieceExt NÃO é .aac de propósito: o sidecar segments-cleanup apaga *.aac com
// mais de 60min, e uma gravação pode durar 6h. Ele ainda apaga pastas vazias,
// por isso quem escreve precisa recriar a pasta a cada tentativa.
const pieceExt = ".adts"

const pieceTimeLayout = "20060102-150405"

// a<unix da tentativa>-<AAAAMMDD-HHMMSS>.adts. O prefixo impede que um ffmpeg
// novo, no mesmo minuto, sobrescreva o pedaço do anterior.
var pieceName = regexp.MustCompile(`^a\d+-(\d{8}-\d{6})\.adts$`)

// Piece é um arquivo de áudio em disco. Start vem do nome; End vem do mtime,
// que é o último instante em que o ffmpeg escreveu nele. É o mtime que deixa o
// cálculo de buraco exato quando o ffmpeg morre no meio de um minuto.
type Piece struct {
	Path  string
	Start time.Time
	End   time.Time
	Size  int64
}

// ListPieces lista os pedaços com áudio, em ordem cronológica. Pasta
// inexistente devolve vazio: é o estado normal com o stream fora.
func ListPieces(dir string) ([]Piece, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("recording: listar pedaços: %w", err)
	}
	var out []Piece
	for _, e := range entries {
		m := pieceName.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		info, err := e.Info()
		if err != nil || info.Size() == 0 {
			continue
		}
		// O ffmpeg expande o strftime no fuso do processo.
		start, err := time.ParseInLocation(pieceTimeLayout, m[1], time.Local)
		if err != nil {
			continue
		}
		out = append(out, Piece{
			Path:  filepath.Join(dir, e.Name()),
			Start: start,
			End:   info.ModTime(),
			Size:  info.Size(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out, nil
}

// DirBytes soma o tamanho dos pedaços. Pasta inexistente vale 0.
func DirBytes(dir string) int64 {
	pieces, err := ListPieces(dir)
	if err != nil {
		return 0
	}
	var total int64
	for _, p := range pieces {
		total += p.Size
	}
	return total
}

// ComputeGaps devolve os intervalos de [from, to] sem áudio. Devolve slice não
// nil para serializar como []. Trechos menores que minGap são oscilação normal
// de rotação de arquivo, não buraco.
func ComputeGaps(pieces []Piece, from, to time.Time, minGap time.Duration) []Gap {
	gaps := []Gap{}
	cursor := from
	for _, p := range pieces {
		start := p.Start
		if start.After(to) {
			start = to
		}
		if start.Sub(cursor) >= minGap {
			gaps = append(gaps, Gap{From: cursor, To: start})
		}
		if p.End.After(cursor) {
			cursor = p.End
		}
	}
	if to.Sub(cursor) >= minGap {
		gaps = append(gaps, Gap{From: cursor, To: to})
	}
	return gaps
}

// FinalStatus decide o status de encerramento.
func FinalStatus(hasAudio, canceled bool, covered, window time.Duration) string {
	if canceled {
		return StatusCancelada
	}
	if !hasAudio {
		return StatusFalhou
	}
	if window-covered <= maxMissing || covered.Seconds() >= window.Seconds()*minCoverage {
		return StatusConcluida
	}
	return StatusParcial
}

// Concat grava os bytes dos pedaços em sequência. ADTS é autodelimitado, então
// concatenar bytes produz um stream válido.
func Concat(pieces []Piece, out string) error {
	dst, err := os.Create(out)
	if err != nil {
		return fmt.Errorf("recording: criar %s: %w", out, err)
	}
	defer dst.Close()
	for _, p := range pieces {
		src, err := os.Open(p.Path)
		if err != nil {
			return fmt.Errorf("recording: abrir pedaço: %w", err)
		}
		_, err = io.Copy(dst, src)
		src.Close()
		if err != nil {
			return fmt.Errorf("recording: copiar pedaço: %w", err)
		}
	}
	return dst.Close()
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd workers && gofmt -w internal/recording && go test ./internal/recording/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/recording
git commit -m "feat(gravacao): pedacos, buracos e status final"
```

---

## Task 4: ffmpeg

**Files:**
- Modify: `workers/internal/ingestor/ffmpeg.go` (após `pickSegmentAudioArgs`)
- Create: `workers/internal/recording/ffmpeg.go`
- Test: `workers/internal/recording/ffmpeg_test.go`

- [ ] **Step 1: Expor os helpers do ingestor**

Acrescentar logo depois de `pickSegmentAudioArgs` em `workers/internal/ingestor/ffmpeg.go`:

```go
// ReconnectArgs e SegmentAudioArgs expõem as duas decisões acima para quem abre
// o próprio ffmpeg (internal/recording). São as regras que já custaram
// incidentes em 2026-05-15: não devem existir em duplicata.
func ReconnectArgs(streamURL string) []string { return pickReconnectArgs(streamURL) }

// SegmentAudioArgs: ver ReconnectArgs.
func SegmentAudioArgs(codec string) []string { return pickSegmentAudioArgs(codec) }
```

- [ ] **Step 2: Escrever o teste que falha**

`workers/internal/recording/ffmpeg_test.go`:

```go
package recording

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCaptureArgs(t *testing.T) {
	aac := strings.Join(captureArgs("http://r/stream", "aac", "/d/a1-%Y.adts"), " ")
	require.Contains(t, aac, "-c:a copy")
	require.Contains(t, aac, "-reconnect_at_eof 1")
	require.Contains(t, aac, "-segment_time 60")
	require.Contains(t, aac, "-segment_format adts")
	require.Contains(t, aac, "-user_agent VLC/3.0.20 LibVLC/3.0.20")
	require.True(t, strings.HasSuffix(aac, "/d/a1-%Y.adts"))

	mp3 := strings.Join(captureArgs("http://r/stream", "mp3", "/d/x"), " ")
	require.Contains(t, mp3, "-c:a aac -b:a 128k")

	hls := strings.Join(captureArgs("http://r/playlist.m3u8", "aac", "/d/x"), " ")
	require.NotContains(t, hls, "-reconnect_at_eof", "HLS com esse flag nunca decodifica (incidente 2026-05-15)")
}

func TestPiecePattern_MantemOPercentDoStrftime(t *testing.T) {
	got := piecePattern("/d", time.Unix(1762380000, 0))
	require.Equal(t, filepath.Join("/d", "a1762380000-%Y%m%d-%H%M%S.adts"), got)
	require.True(t, pieceName.MatchString("a1762380000-20261105-200000.adts"))
}

// Prova com ffmpeg de verdade que ADTS concatenado vira M4A com duração.
func TestRemuxEDuration(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg não instalado")
	}
	dir := t.TempDir()
	in := filepath.Join(dir, "joined.tmp")
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=3",
		"-c:a", "aac", "-f", "adts", in)
	require.NoError(t, gen.Run())

	out := filepath.Join(dir, "out.m4a")
	f := FFmpeg{}
	require.NoError(t, f.Remux(context.Background(), in, out))

	dur, err := f.Duration(context.Background(), out)
	require.NoError(t, err)
	require.InDelta(t, 3.0, dur.Seconds(), 0.2)

	info, err := os.Stat(out)
	require.NoError(t, err)
	require.Positive(t, info.Size())
}
```

- [ ] **Step 3: Rodar e ver falhar**

Run: `cd workers && go test ./internal/recording/ -run "TestCaptureArgs|TestPiecePattern|TestRemux"`
Expected: FAIL com `undefined: captureArgs`.

- [ ] **Step 4: Implementar**

`workers/internal/recording/ffmpeg.go`:

```go
package recording

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"radiocheck/internal/ingestor"
)

const stderrTail = 2048

// tailWriter guarda só o fim do stderr: uma gravação de 6h com stream instável
// escreveria sem limite num bytes.Buffer.
type tailWriter struct{ buf []byte }

func (t *tailWriter) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > stderrTail {
		t.buf = t.buf[len(t.buf)-stderrTail:]
	}
	return len(p), nil
}

// FFmpeg é a implementação real de Media.
type FFmpeg struct{}

func piecePattern(dir string, now time.Time) string {
	return filepath.Join(dir, fmt.Sprintf("a%d-%%Y%%m%%d-%%H%%M%%S%s", now.Unix(), pieceExt))
}

func captureArgs(streamURL, codec, pattern string) []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}
	args = append(args, ingestor.ReconnectArgs(streamURL)...)
	args = append(args,
		"-timeout", "10000000",
		"-user_agent", "VLC/3.0.20 LibVLC/3.0.20",
		"-i", streamURL,
		"-map", "0:a:0",
	)
	args = append(args, ingestor.SegmentAudioArgs(codec)...)
	return append(args,
		"-f", "segment",
		"-segment_time", "60",
		"-segment_format", "adts",
		"-segment_atclocktime", "1",
		"-reset_timestamps", "1",
		"-strftime", "1",
		pattern,
	)
}

// Capture roda até ctx acabar (fim da janela) ou o ffmpeg sair. ctx cancelado
// não é erro: é o fim normal.
func (FFmpeg) Capture(ctx context.Context, streamURL, dir string) error {
	// Falha no probe vira codec "" e cai no reencode, que aceita qualquer entrada.
	codec, _ := ingestor.ProbeAudioCodec(ctx, streamURL)

	cmd := exec.CommandContext(ctx, "ffmpeg", captureArgs(streamURL, codec, piecePattern(dir, time.Now()))...)
	// SIGTERM deixa o ffmpeg fechar o pedaço; WaitDelay é o Kill de garantia.
	// No Windows o Signal falha e o Kill resolve: lá só roda em desenvolvimento.
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	stderr := &tailWriter{}
	cmd.Stderr = stderr

	err := cmd.Run()
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(string(stderr.buf)))
	}
	return errors.New("ffmpeg terminou antes do fim da janela")
}

// Remux troca o contêiner de ADTS para M4A sem reencodar. M4A abre em qualquer
// player e tem índice; ADTS cru não tem duração nem busca confiáveis.
func (FFmpeg) Remux(ctx context.Context, in, out string) error {
	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "aac", "-i", in, "-c", "copy", "-movflags", "+faststart", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("remux: %w: %s", err, strings.TrimSpace(string(b)))
	}
	return nil
}

// Duration devolve a duração real do arquivo.
func (FFmpeg) Duration(ctx context.Context, path string) (time.Duration, error) {
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1", path)
	b, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe: %w", err)
	}
	secs, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
	if err != nil {
		return 0, fmt.Errorf("ffprobe: duração %q: %w", b, err)
	}
	return time.Duration(secs * float64(time.Second)), nil
}
```

- [ ] **Step 5: Rodar e ver passar**

Run: `cd workers && gofmt -w internal/recording && go test ./internal/recording/ ./internal/ingestor/`
Expected: PASS nos dois. `TestRemuxEDuration` precisa rodar de verdade: ffmpeg está em `C:\ffmpeg\bin`. Confirme com `-v -run TestRemux` que não foi `SKIP`.

- [ ] **Step 6: Commit**

```bash
git add workers/internal/recording workers/internal/ingestor/ffmpeg.go
git commit -m "feat(gravacao): captura e remux com ffmpeg"
```

---

## Task 5: Store

**Files:**
- Create: `workers/internal/recording/store.go`
- Test: `workers/internal/recording/store_test.go`

- [ ] **Step 1: Escrever o teste que falha**

`workers/internal/recording/store_test.go`. Sem `TRUNCATE`: o banco de teste pode ter sobras, então cada teste cria e apaga as próprias linhas.

```go
package recording

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/db"
)

func newTestStore(t *testing.T) (context.Context, *Store, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, url, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return ctx, NewStore(pool), pool
}

func seedStation(t *testing.T, ctx context.Context, pool *pgxpool.Pool, streamURL string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO stations (name, band, frequency_mhz, city, state, stream_url)
		VALUES ($1, 'FM', 99.9, 'Teste', 'SC', $2) RETURNING id`,
		"Gravacao Teste "+uuid.NewString()[:8], streamURL).Scan(&id))
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM recordings WHERE station_id = $1`, id) //nolint:errcheck
		pool.Exec(ctx, `DELETE FROM stations WHERE id = $1`, id)           //nolint:errcheck
	})
	return id
}

func TestStore_InsertBarraSobreposicao(t *testing.T) {
	ctx, s, pool := newTestStore(t)
	st := seedStation(t, ctx, pool, "http://x/s")
	t0 := time.Now().Add(time.Hour).Truncate(time.Second)

	id, err := s.Insert(ctx, st, "R", nil, t0, t0.Add(time.Hour))
	require.NoError(t, err)

	_, err = s.Insert(ctx, st, "R", nil, t0.Add(30*time.Minute), t0.Add(90*time.Minute))
	require.ErrorIs(t, err, ErrOverlap)

	// Encostada não é sobreposição.
	_, err = s.Insert(ctx, st, "R", nil, t0.Add(time.Hour), t0.Add(2*time.Hour))
	require.NoError(t, err)

	got, err := s.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, StatusProgramada, got.Status)
	require.False(t, got.HasAudio)
	require.Empty(t, got.Gaps)
	require.NotNil(t, got.Gaps)
}

func TestStore_Get_NaoEncontrada(t *testing.T) {
	ctx, s, _ := newTestStore(t)
	_, err := s.Get(ctx, uuid.New())
	require.ErrorIs(t, err, ErrNotFound)
}

func TestStore_CicloCompleto(t *testing.T) {
	ctx, s, pool := newTestStore(t)
	st := seedStation(t, ctx, pool, "http://x/s")
	now := time.Now().Truncate(time.Second)
	id, err := s.Insert(ctx, st, "R", nil, now.Add(time.Minute), now.Add(time.Hour))
	require.NoError(t, err)

	// Ainda não chegou a hora.
	n, err := s.ClaimDue(ctx, now, now)
	require.NoError(t, err)
	require.Zero(t, n)

	n, err = s.ClaimDue(ctx, now.Add(2*time.Minute), now)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)

	first, err := s.MarkDropAlerted(ctx, id)
	require.NoError(t, err)
	require.True(t, first)
	second, err := s.MarkDropAlerted(ctx, id)
	require.NoError(t, err)
	require.False(t, second, "o e-mail de queda sai uma vez só")

	require.NoError(t, s.MarkProcessing(ctx, id))

	key := "recordings/2026/11/x.m4a"
	size := int64(10)
	cov := 3540
	exp := now.Add(-time.Hour) // já vencida, para o Expired achar
	require.NoError(t, s.Complete(ctx, id, CompleteInput{
		Status: StatusParcial, ObjectKey: &key, SizeBytes: &size, CoveredSeconds: &cov,
		Gaps:       []Gap{{From: now, To: now.Add(time.Minute)}},
		FinishedAt: now, ExpiresAt: &exp,
	}))

	got, err := s.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, StatusParcial, got.Status)
	require.True(t, got.HasAudio)
	require.Len(t, got.Gaps, 1)

	expired, err := s.Expired(ctx, now)
	require.NoError(t, err)
	var achou bool
	for _, r := range expired {
		achou = achou || r.ID == id
	}
	require.True(t, achou)

	require.NoError(t, s.MarkExpired(ctx, id))
	got, err = s.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, StatusExpirada, got.Status)
	require.False(t, got.HasAudio)
}

func TestStore_FailMissed(t *testing.T) {
	ctx, s, pool := newTestStore(t)
	st := seedStation(t, ctx, pool, "http://x/s")
	now := time.Now().Truncate(time.Second)
	id, err := s.Insert(ctx, st, "R", nil, now.Add(-2*time.Hour), now.Add(-time.Hour))
	require.NoError(t, err)

	missed, err := s.FailMissed(ctx, now, "motivo")
	require.NoError(t, err)
	require.Len(t, missed, 1)
	require.Equal(t, id, missed[0].ID)

	got, err := s.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, StatusFalhou, got.Status)
	require.Equal(t, "motivo", *got.LastError)
}

func TestStore_Cancelar(t *testing.T) {
	ctx, s, pool := newTestStore(t)
	st := seedStation(t, ctx, pool, "http://x/s")
	now := time.Now().Truncate(time.Second)

	id, err := s.Insert(ctx, st, "R", nil, now.Add(time.Hour), now.Add(2*time.Hour))
	require.NoError(t, err)
	require.NoError(t, s.CancelScheduled(ctx, id, now))
	got, _ := s.Get(ctx, id)
	require.Equal(t, StatusCancelada, got.Status)
	require.ErrorIs(t, s.CancelScheduled(ctx, id, now), ErrBusy)

	id2, err := s.Insert(ctx, st, "R", nil, now.Add(-time.Minute), now.Add(time.Hour))
	require.NoError(t, err)
	_, err = s.ClaimDue(ctx, now, now)
	require.NoError(t, err)
	require.NoError(t, s.CancelRecording(ctx, id2, now))
	got, _ = s.Get(ctx, id2)
	require.Equal(t, StatusProcessando, got.Status)
	require.NotNil(t, got.CanceledAt)
}

func TestStore_StationInfoEStreamURL(t *testing.T) {
	ctx, s, pool := newTestStore(t)
	st := seedStation(t, ctx, pool, "http://x/s")

	_, url, err := s.StationInfo(ctx, st)
	require.NoError(t, err)
	require.Equal(t, "http://x/s", url)

	_, _, err = s.StationInfo(ctx, uuid.New())
	require.ErrorIs(t, err, ErrNotFound)

	id, err := s.Insert(ctx, st, "R", nil, time.Now().Add(time.Hour), time.Now().Add(2*time.Hour))
	require.NoError(t, err)
	got, err := s.StreamURL(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "http://x/s", got)
}

func TestStore_ListPagina(t *testing.T) {
	ctx, s, pool := newTestStore(t)
	st := seedStation(t, ctx, pool, "http://x/s")
	t0 := time.Now().Add(100 * time.Hour).Truncate(time.Second)
	_, err := s.Insert(ctx, st, "A", nil, t0, t0.Add(time.Hour))
	require.NoError(t, err)
	_, err = s.Insert(ctx, st, "B", nil, t0.Add(2*time.Hour), t0.Add(3*time.Hour))
	require.NoError(t, err)

	items, total, err := s.List(ctx, 1, 0)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.GreaterOrEqual(t, total, 2)
	require.Equal(t, "B", items[0].StationName, "mais recente primeiro")
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd workers && TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55433/radiocheck_test?sslmode=disable" go test -p 1 ./internal/recording/ -run TestStore`
Expected: FAIL com `undefined: Store`.

- [ ] **Step 3: Implementar**

`workers/internal/recording/store.go`:

```go
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

// Insert cria a gravação. A checagem de sobreposição fica no mesmo comando para
// dois admins não passarem juntos por ela.
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
		`ORDER BY r.starts_at DESC LIMIT $1 OFFSET $2`, limit, offset)
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
	out := make([]Recording, 0, len(ids))
	for _, id := range ids {
		r, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, nil
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

// Complete fecha uma gravação em `processando`.
func (s *Store) Complete(ctx context.Context, id uuid.UUID, in CompleteInput) error {
	gaps, err := json.Marshal(in.Gaps)
	if err != nil {
		return fmt.Errorf("recording: gaps: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE recordings
		SET status = $2, object_key = $3, size_bytes = $4, covered_seconds = $5,
		    gaps = $6, finished_at = $7, expires_at = $8, updated_at = now()
		WHERE id = $1 AND status = 'processando'`,
		id, in.Status, in.ObjectKey, in.SizeBytes, in.CoveredSeconds, gaps, in.FinishedAt, in.ExpiresAt)
	if err != nil {
		return fmt.Errorf("recording: concluir: %w", err)
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
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd workers && gofmt -w internal/recording && TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55433/radiocheck_test?sslmode=disable" go test -p 1 ./internal/recording/ -v -run TestStore`
Expected: PASS, sem nenhum `SKIP`.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/recording
git commit -m "feat(gravacao): store"
```

---

## Task 6: Manager: esqueleto, e-mails e finalização

**Files:**
- Create: `workers/internal/recording/manager.go`
- Create: `workers/internal/recording/notify.go`
- Create: `workers/internal/recording/finalize.go`
- Test: `workers/internal/recording/fakes_test.go`
- Test: `workers/internal/recording/finalize_test.go`

- [ ] **Step 1: Escrever os fakes**

`workers/internal/recording/fakes_test.go`:

```go
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
```

- [ ] **Step 2: Escrever o teste que falha**

`workers/internal/recording/finalize_test.go`:

```go
package recording

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func processing(t0 time.Time) Recording {
	return Recording{ID: uuid.New(), StationName: "Radio X", CreatorEmail: "a@b.c",
		StartsAt: t0, EndsAt: t0.Add(10 * time.Minute), Status: StatusProcessando, Gaps: []Gap{}}
}

func seedPieces(t *testing.T, m *Manager, r Recording) {
	t.Helper()
	dir := m.dirFor(r.ID)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	writePiece(t, dir, "1", r.StartsAt, r.StartsAt.Add(10*time.Minute), "audio")
}

func TestFinalize_Concluida(t *testing.T) {
	t0 := time.Now().Add(-time.Hour).Truncate(time.Second)
	r := processing(t0)
	repo, objs, mail := newFakeRepo(r), newFakeObjects(), &fakeMailer{}
	m := newTestManager(t, repo, &fakeMedia{duration: 10 * time.Minute}, objs, mail)
	seedPieces(t, m, r)

	require.NoError(t, m.finalize(context.Background(), r))

	got := repo.snapshot(r.ID)
	require.Equal(t, StatusConcluida, got.Status)
	require.Equal(t, "audio", string(objs.puts[*got.ObjectKey]))
	require.Equal(t, 600, *got.CoveredSeconds)
	require.WithinDuration(t, time.Now().Add(Retention), *got.ExpiresAt, time.Minute)
	require.Empty(t, mail.all(), "sucesso não manda e-mail")
	_, err := os.Stat(m.dirFor(r.ID))
	require.True(t, os.IsNotExist(err), "pedaços apagados depois do upload")
}

func TestFinalize_ParcialAvisa(t *testing.T) {
	t0 := time.Now().Add(-time.Hour).Truncate(time.Second)
	r := processing(t0)
	repo, objs, mail := newFakeRepo(r), newFakeObjects(), &fakeMailer{}
	m := newTestManager(t, repo, &fakeMedia{duration: 5 * time.Minute}, objs, mail)
	dir := m.dirFor(r.ID)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	writePiece(t, dir, "1", t0, t0.Add(5*time.Minute), "audio")

	require.NoError(t, m.finalize(context.Background(), r))

	got := repo.snapshot(r.ID)
	require.Equal(t, StatusParcial, got.Status)
	require.Len(t, got.Gaps, 1)
	require.Len(t, mail.all(), 1)
	require.Contains(t, mail.all()[0].subject, "parcial")
}

func TestFinalize_SemAudioFalhaEAvisa(t *testing.T) {
	t0 := time.Now().Add(-time.Hour).Truncate(time.Second)
	r := processing(t0)
	repo, objs, mail := newFakeRepo(r), newFakeObjects(), &fakeMailer{}
	m := newTestManager(t, repo, &fakeMedia{}, objs, mail)

	require.NoError(t, m.finalize(context.Background(), r))

	got := repo.snapshot(r.ID)
	require.Equal(t, StatusFalhou, got.Status)
	require.Nil(t, got.ObjectKey)
	require.Nil(t, got.ExpiresAt)
	require.Empty(t, objs.puts)
	require.Len(t, mail.all(), 1)
}

// A garantia central: falha no upload não pode perder áudio já capturado.
func TestFinalize_FalhaNoUploadPreservaPedacos(t *testing.T) {
	t0 := time.Now().Add(-time.Hour).Truncate(time.Second)
	r := processing(t0)
	repo, objs, mail := newFakeRepo(r), newFakeObjects(), &fakeMailer{}
	objs.putErr = errors.New("bucket fora")
	m := newTestManager(t, repo, &fakeMedia{duration: 10 * time.Minute}, objs, mail)
	seedPieces(t, m, r)

	require.Error(t, m.finalize(context.Background(), r))

	require.Equal(t, StatusProcessando, repo.snapshot(r.ID).Status)
	pieces, err := ListPieces(m.dirFor(r.ID))
	require.NoError(t, err)
	require.Len(t, pieces, 1)

	objs.putErr = nil
	require.NoError(t, m.finalize(context.Background(), r), "a retentativa conclui")
	require.Equal(t, StatusConcluida, repo.snapshot(r.ID).Status)
}

func TestFinalize_CanceladaGuardaAudioSemEmail(t *testing.T) {
	t0 := time.Now().Add(-time.Hour).Truncate(time.Second)
	r := processing(t0)
	cancel := t0.Add(5 * time.Minute)
	r.CanceledAt = &cancel
	repo, objs, mail := newFakeRepo(r), newFakeObjects(), &fakeMailer{}
	m := newTestManager(t, repo, &fakeMedia{duration: 5 * time.Minute}, objs, mail)
	dir := m.dirFor(r.ID)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	writePiece(t, dir, "1", t0, cancel, "audio")

	require.NoError(t, m.finalize(context.Background(), r))

	got := repo.snapshot(r.ID)
	require.Equal(t, StatusCancelada, got.Status)
	require.NotNil(t, got.ObjectKey)
	require.Empty(t, got.Gaps, "o que veio depois do cancelamento não é buraco")
	require.Empty(t, mail.all())
}

func TestObjectKey(t *testing.T) {
	id := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	r := Recording{ID: id, StartsAt: time.Date(2026, 11, 5, 23, 0, 0, 0, time.UTC)}
	require.Equal(t, "recordings/2026/11/"+id.String()+".m4a", objectKey(r))
}
```

- [ ] **Step 3: Rodar e ver falhar**

Run: `cd workers && go test ./internal/recording/ -run "TestFinalize|TestObjectKey"`
Expected: FAIL com `undefined: Repo`.

- [ ] **Step 4: Implementar o Manager**

`workers/internal/recording/manager.go`:

```go
package recording

import (
	"context"
	"io"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"radiocheck/internal/mailer"
)

// Repo é o que o Manager usa do Store.
type Repo interface {
	StationInfo(ctx context.Context, id uuid.UUID) (string, string, error)
	Insert(ctx context.Context, stationID uuid.UUID, stationName string, createdBy *uuid.UUID, from, to time.Time) (uuid.UUID, error)
	Get(ctx context.Context, id uuid.UUID) (*Recording, error)
	List(ctx context.Context, limit, offset int) ([]Recording, int, error)
	ListByStatus(ctx context.Context, status string) ([]Recording, error)
	ClaimDue(ctx context.Context, startBefore, now time.Time) (int64, error)
	FailMissed(ctx context.Context, now time.Time, reason string) ([]Recording, error)
	StreamURL(ctx context.Context, id uuid.UUID) (string, error)
	MarkProcessing(ctx context.Context, id uuid.UUID) error
	MarkDropAlerted(ctx context.Context, id uuid.UUID) (bool, error)
	SetLastError(ctx context.Context, id uuid.UUID, msg string) error
	Complete(ctx context.Context, id uuid.UUID, in CompleteInput) error
	CancelScheduled(ctx context.Context, id uuid.UUID, now time.Time) error
	CancelRecording(ctx context.Context, id uuid.UUID, now time.Time) error
	Delete(ctx context.Context, id uuid.UUID) error
	Expired(ctx context.Context, now time.Time) ([]Recording, error)
	MarkExpired(ctx context.Context, id uuid.UUID) error
}

// Media isola o ffmpeg, para os testes não dependerem dele.
type Media interface {
	Capture(ctx context.Context, streamURL, dir string) error
	Remux(ctx context.Context, in, out string) error
	Duration(ctx context.Context, path string) (time.Duration, error)
}

// ObjectStore é o subconjunto de storage.Client usado aqui.
type ObjectStore interface {
	Put(ctx context.Context, key string, body io.Reader, contentType string) error
	Get(ctx context.Context, key string) (io.ReadCloser, string, int64, error)
	Delete(ctx context.Context, key string) error
}

// Config ajusta o Manager. Zero assume o padrão de produção.
type Config struct {
	Root       string // <SEGMENTS_PATH>/_recordings
	BaseURL    string // link do e-mail
	TickEvery  time.Duration
	PollEvery  time.Duration // medição de bytes do watchdog
	StallAfter time.Duration // sem bytes novos por este tempo = caiu
	RetryDelay time.Duration
	MinGap     time.Duration
	PreRoll    time.Duration
	PruneEvery time.Duration
}

func (c Config) withDefaults() Config {
	def := func(v *time.Duration, d time.Duration) {
		if *v == 0 {
			*v = d
		}
	}
	def(&c.TickEvery, 15*time.Second)
	def(&c.PollEvery, 10*time.Second)
	def(&c.StallAfter, 60*time.Second)
	def(&c.RetryDelay, 5*time.Second)
	def(&c.MinGap, 30*time.Second)
	// PreRoll cobre o atraso do tick somado à conexão do ffmpeg. Sem ele, uma
	// gravação marcada para 20:00 só teria áudio a partir de ~20:00:20.
	def(&c.PreRoll, 20*time.Second)
	def(&c.PruneEvery, time.Hour)
	return c
}

type liveRecorder struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// Manager agenda, grava, finaliza e expira.
type Manager struct {
	cfg     Config
	repo    Repo
	media   Media
	objects ObjectStore
	mail    mailer.Mailer
	log     *zap.Logger
	now     func() time.Time

	mu         sync.Mutex
	live       map[uuid.UUID]*liveRecorder
	finalizing map[uuid.UUID]struct{}
}

// NewManager monta o Manager.
func NewManager(cfg Config, repo Repo, media Media, objects ObjectStore, mail mailer.Mailer, log *zap.Logger) *Manager {
	return &Manager{
		cfg:        cfg.withDefaults(),
		repo:       repo,
		media:      media,
		objects:    objects,
		mail:       mail,
		log:        log,
		now:        time.Now,
		live:       map[uuid.UUID]*liveRecorder{},
		finalizing: map[uuid.UUID]struct{}{},
	}
}

func (m *Manager) dirFor(id uuid.UUID) string {
	return filepath.Join(m.cfg.Root, id.String())
}
```

- [ ] **Step 5: Implementar os e-mails**

`workers/internal/recording/notify.go`:

```go
package recording

import (
	"context"
	"fmt"
	"html"
	"strings"
	"time"

	"go.uber.org/zap"
)

func fmtSP(t time.Time) string { return t.In(saoPaulo).Format("02/01/2006 15:04") }

func windowLabel(r Recording) string {
	return fmt.Sprintf("%s, das %s às %s", r.StartsAt.In(saoPaulo).Format("02/01/2006"),
		r.StartsAt.In(saoPaulo).Format("15:04"), r.EndsAt.In(saoPaulo).Format("15:04"))
}

// notify nunca falha para quem chamou: o e-mail é aviso, e o estado real está
// na tela. Sem criador (usuário excluído), não há para quem mandar.
func (m *Manager) notify(rec Recording, subject, body string) {
	if rec.CreatorEmail == "" {
		return
	}
	text := fmt.Sprintf("%s\n\nEmissora: %s\nGravação: %s\n\n%s/admin/gravacoes\n",
		body, rec.StationName, windowLabel(rec), strings.TrimRight(m.cfg.BaseURL, "/"))
	htmlBody := "<p>" + strings.ReplaceAll(html.EscapeString(text), "\n", "<br>") + "</p>"

	// Contexto próprio: o aviso de queda não pode morrer junto com a tentativa.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	subject = fmt.Sprintf("Gravação de %s: %s", rec.StationName, subject)
	if err := m.mail.Send(ctx, []string{rec.CreatorEmail}, subject, htmlBody, text); err != nil {
		m.log.Warn("recording: e-mail falhou", zap.Stringer("id", rec.ID), zap.Error(err))
	}
}

func (m *Manager) notifyNoConnection(rec Recording) {
	m.notify(rec, "não conectou",
		"O stream não respondeu no início da gravação. O sistema segue tentando até o fim da janela. "+
			"Se a URL mudou, corrija na emissora: a nova vale na próxima tentativa.")
}

func (m *Manager) notifyDropped(rec Recording) {
	m.notify(rec, "stream caiu",
		"O stream parou no meio da gravação. O sistema segue tentando reconectar até o fim da janela. "+
			"Se a URL mudou, corrija na emissora: a nova vale na próxima tentativa.")
}

func (m *Manager) notifyMissed(rec Recording) {
	m.notify(rec, "não gravou", "A plataforma ficou fora do ar durante toda a janela e nada foi gravado.")
}

func (m *Manager) notifyFailed(rec Recording) {
	m.notify(rec, "falhou", "Nenhum áudio foi capturado nesta gravação.")
}

func (m *Manager) notifyPartial(rec Recording, covered, window time.Duration, gaps []Gap) {
	lines := make([]string, 0, len(gaps))
	for _, g := range gaps {
		lines = append(lines, fmt.Sprintf("- sem áudio de %s até %s", fmtSP(g.From), fmtSP(g.To)))
	}
	pct := 0
	if window > 0 {
		pct = int(covered * 100 / window)
	}
	m.notify(rec, fmt.Sprintf("parcial (%d%%)", pct),
		fmt.Sprintf("A gravação terminou com buracos e cobriu %d%% da janela.\n%s", pct, strings.Join(lines, "\n")))
}
```

- [ ] **Step 6: Implementar a finalização**

`workers/internal/recording/finalize.go`:

```go
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
```

- [ ] **Step 7: Rodar e ver passar**

Run: `cd workers && gofmt -w internal/recording && go test ./internal/recording/ -run "TestFinalize|TestObjectKey" -v`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add workers/internal/recording
git commit -m "feat(gravacao): manager, e-mails e finalizacao"
```

---

## Task 7: Gravador

**Files:**
- Create: `workers/internal/recording/recorder.go`
- Test: `workers/internal/recording/recorder_test.go`

- [ ] **Step 1: Escrever o teste que falha**

`workers/internal/recording/recorder_test.go`:

```go
package recording

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func recordingRow(dur time.Duration) Recording {
	now := time.Now()
	return Recording{ID: uuid.New(), StationName: "Radio X", CreatorEmail: "a@b.c",
		StartsAt: now, EndsAt: now.Add(dur), Status: StatusGravando, Gaps: []Gap{}}
}

// feed escreve bytes em ritmo constante, como um ffmpeg com stream saudável.
// Abre e fecha a cada escrita porque o NTFS só atualiza o tamanho na listagem da
// pasta quando o arquivo fecha; no Linux, o caso de produção, é imediato.
func feed(ctx context.Context, dir string) {
	p := filepath.Join(dir, "a1-20261105-200000"+pieceExt)
	t := time.NewTicker(5 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				return
			}
			f.Write([]byte("x")) //nolint:errcheck
			f.Close()
		}
	}
}

func TestRecord_NoFimDaJanelaVaiParaProcessando(t *testing.T) {
	r := recordingRow(150 * time.Millisecond)
	repo, mail := newFakeRepo(r), &fakeMailer{}
	media := &fakeMedia{capture: func(ctx context.Context, _, dir string) error {
		feed(ctx, dir)
		return nil
	}}
	m := newTestManager(t, repo, media, newFakeObjects(), mail)

	m.record(context.Background(), r)

	require.Equal(t, StatusProcessando, repo.snapshot(r.ID).Status)
	require.Empty(t, mail.all())
}

func TestRecord_ReconectaQuandoOFfmpegSai(t *testing.T) {
	r := recordingRow(120 * time.Millisecond)
	repo := newFakeRepo(r)
	var calls atomic.Int32
	media := &fakeMedia{capture: func(context.Context, string, string) error {
		calls.Add(1)
		return errors.New("caiu")
	}}
	m := newTestManager(t, repo, media, newFakeObjects(), &fakeMailer{})

	m.record(context.Background(), r)

	require.Greater(t, calls.Load(), int32(2))
}

// O limpador de 60min apaga pasta vazia, então cada tentativa recria a pasta.
func TestRecord_RecriaAPastaACadaTentativa(t *testing.T) {
	r := recordingRow(120 * time.Millisecond)
	repo := newFakeRepo(r)
	var missing atomic.Int32
	media := &fakeMedia{capture: func(_ context.Context, _, dir string) error {
		if _, err := os.Stat(dir); err != nil {
			missing.Add(1)
		}
		os.RemoveAll(dir) //nolint:errcheck
		return errors.New("caiu")
	}}
	m := newTestManager(t, repo, media, newFakeObjects(), &fakeMailer{})

	m.record(context.Background(), r)

	require.Zero(t, missing.Load())
}

func TestRecord_LeAURLACadaTentativa(t *testing.T) {
	r := recordingRow(120 * time.Millisecond)
	repo := newFakeRepo(r)
	var urls atomic.Value
	urls.Store("")
	media := &fakeMedia{capture: func(_ context.Context, url, _ string) error {
		urls.Store(url)
		repo.mu.Lock()
		repo.url = "http://nova/s"
		repo.mu.Unlock()
		return errors.New("caiu")
	}}
	m := newTestManager(t, repo, media, newFakeObjects(), &fakeMailer{})

	m.record(context.Background(), r)

	require.Equal(t, "http://nova/s", urls.Load())
}

// ffmpeg pendurado: não sai, não escreve. Só o watchdog destrava.
func TestRecord_WatchdogMataFfmpegPenduradoEAvisaUmaVez(t *testing.T) {
	r := recordingRow(300 * time.Millisecond)
	repo, mail := newFakeRepo(r), &fakeMailer{}
	var calls atomic.Int32
	media := &fakeMedia{capture: func(ctx context.Context, _, _ string) error {
		calls.Add(1)
		<-ctx.Done()
		return nil
	}}
	m := newTestManager(t, repo, media, newFakeObjects(), mail)

	m.record(context.Background(), r)

	require.GreaterOrEqual(t, calls.Load(), int32(2), "o watchdog precisa reiniciar a captura")
	require.Len(t, mail.all(), 1, "um aviso por gravação, mesmo com várias quedas")
	require.Contains(t, mail.all()[0].subject, "não conectou")
}

func TestRecord_QuedaDepoisDeTerAudioAvisaComoQueda(t *testing.T) {
	r := recordingRow(300 * time.Millisecond)
	repo, mail := newFakeRepo(r), &fakeMailer{}
	media := &fakeMedia{capture: func(ctx context.Context, _, dir string) error {
		p := filepath.Join(dir, "a1-20261105-200000"+pieceExt)
		os.WriteFile(p, []byte("audio"), 0o644) //nolint:errcheck
		<-ctx.Done()
		return nil
	}}
	m := newTestManager(t, repo, media, newFakeObjects(), mail)

	m.record(context.Background(), r)

	require.Len(t, mail.all(), 1)
	require.Contains(t, mail.all()[0].subject, "stream caiu")
}

// Desligamento da API: a linha fica em `gravando` para ser retomada no boot.
func TestRecord_ContextoCanceladoNaoFecha(t *testing.T) {
	r := recordingRow(time.Hour)
	repo := newFakeRepo(r)
	media := &fakeMedia{capture: func(ctx context.Context, _, _ string) error {
		<-ctx.Done()
		return nil
	}}
	m := newTestManager(t, repo, media, newFakeObjects(), &fakeMailer{})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	m.record(ctx, r)

	require.Equal(t, StatusGravando, repo.snapshot(r.ID).Status)
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd workers && go test ./internal/recording/ -run TestRecord_`
Expected: FAIL com `m.record undefined`.

- [ ] **Step 3: Implementar**

`workers/internal/recording/recorder.go`:

```go
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
	if _, ok := m.live[rec.ID]; ok {
		m.mu.Unlock()
		return
	}
	liveCtx, cancel := context.WithCancel(ctx)
	l := &liveRecorder{cancel: cancel, done: make(chan struct{})}
	m.live[rec.ID] = l
	m.mu.Unlock()

	go func() {
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
	go m.watch(windowCtx, rec, dir, slot)

	for windowCtx.Err() == nil {
		m.attempt(windowCtx, rec, dir, slot)
		select {
		case <-windowCtx.Done():
		case <-time.After(m.cfg.RetryDelay):
		}
	}

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
		cur := DirBytes(dir)
		if cur > last {
			last, lastGrowth = cur, time.Now()
			continue
		}
		if time.Since(lastGrowth) < m.cfg.StallAfter {
			continue
		}
		lastGrowth = time.Now()
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
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd workers && gofmt -w internal/recording && go test -race ./internal/recording/ -run TestRecord_ -v`
Expected: PASS, sem data race. Se `-race` reclamar de CGO no Windows, rodar sem a flag agora e repetir em Linux na Task 16.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/recording
git commit -m "feat(gravacao): gravador com reconexao e watchdog"
```

---

## Task 8: Agendador, retenção e operações do admin

**Files:**
- Modify: `workers/internal/recording/manager.go`
- Create: `workers/internal/recording/service.go`
- Test: `workers/internal/recording/manager_test.go`

- [ ] **Step 1: Escrever o teste que falha**

`workers/internal/recording/manager_test.go`:

```go
package recording

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func holdCapture() *fakeMedia {
	return &fakeMedia{capture: func(ctx context.Context, _, _ string) error {
		<-ctx.Done()
		return nil
	}, duration: time.Minute}
}

func TestTick_RespeitaOPreRoll(t *testing.T) {
	now := time.Now()
	future := Recording{ID: uuid.New(), StartsAt: now.Add(time.Minute), EndsAt: now.Add(time.Hour), Status: StatusProgramada, Gaps: []Gap{}}
	soon := Recording{ID: uuid.New(), StartsAt: now.Add(10 * time.Second), EndsAt: now.Add(time.Hour), Status: StatusProgramada, Gaps: []Gap{}}
	repo := newFakeRepo(future, soon)
	m := newTestManager(t, repo, holdCapture(), newFakeObjects(), &fakeMailer{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	m.tick(ctx)

	require.Equal(t, StatusProgramada, repo.snapshot(future.ID).Status)
	require.Equal(t, StatusGravando, repo.snapshot(soon.ID).Status)
}

func TestTick_RetomaGravacaoDepoisDeReiniciar(t *testing.T) {
	now := time.Now()
	r := Recording{ID: uuid.New(), StartsAt: now.Add(-time.Minute), EndsAt: now.Add(time.Hour), Status: StatusGravando, Gaps: []Gap{}}
	repo := newFakeRepo(r)
	captured := make(chan struct{}, 1)
	media := &fakeMedia{capture: func(ctx context.Context, _, _ string) error {
		select {
		case captured <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return nil
	}}
	m := newTestManager(t, repo, media, newFakeObjects(), &fakeMailer{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	m.tick(ctx)

	select {
	case <-captured:
	case <-time.After(time.Second):
		t.Fatal("a captura não foi retomada")
	}
}

func TestTick_JanelaAcabouDuranteOReinicioFinaliza(t *testing.T) {
	now := time.Now()
	r := Recording{ID: uuid.New(), StartsAt: now.Add(-time.Hour), EndsAt: now.Add(-time.Minute), Status: StatusGravando, Gaps: []Gap{}}
	repo := newFakeRepo(r)
	m := newTestManager(t, repo, holdCapture(), newFakeObjects(), &fakeMailer{})
	dir := m.dirFor(r.ID)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	writePiece(t, dir, "1", r.StartsAt, r.EndsAt, validADTS(3))

	m.tick(context.Background())

	require.Eventually(t, func() bool {
		return repo.snapshot(r.ID).HasAudio
	}, time.Second, 5*time.Millisecond)
}

func TestTick_JanelaPerdidaFalhaEAvisa(t *testing.T) {
	now := time.Now()
	r := Recording{ID: uuid.New(), StationName: "R", CreatorEmail: "a@b.c", StartsAt: now.Add(-2 * time.Hour), EndsAt: now.Add(-time.Hour), Status: StatusProgramada, Gaps: []Gap{}}
	repo, mail := newFakeRepo(r), &fakeMailer{}
	m := newTestManager(t, repo, holdCapture(), newFakeObjects(), mail)

	m.tick(context.Background())

	require.Equal(t, StatusFalhou, repo.snapshot(r.ID).Status)
	require.Len(t, sentMails(m, mail), 1)
	require.Contains(t, sentMails(m, mail)[0].subject, "não gravou")
}

func TestPrune_ApagaObjetoAntesDeMarcar(t *testing.T) {
	now := time.Now()
	key := "recordings/x.m4a"
	past := now.Add(-time.Hour)
	r := Recording{ID: uuid.New(), Status: StatusConcluida, ObjectKey: &key, ExpiresAt: &past, Gaps: []Gap{}}
	repo, objs := newFakeRepo(r), newFakeObjects()
	m := newTestManager(t, repo, holdCapture(), objs, &fakeMailer{})

	objs.deleteErr = errors.New("bucket fora")
	m.prune(context.Background())
	require.Equal(t, StatusConcluida, repo.snapshot(r.ID).Status, "falhou o delete: a linha continua e repete")

	objs.deleteErr = nil
	m.prune(context.Background())
	require.Equal(t, StatusExpirada, repo.snapshot(r.ID).Status)
	require.Equal(t, []string{key}, objs.deleted)
}

func TestCreate(t *testing.T) {
	repo := newFakeRepo()
	m := newTestManager(t, repo, holdCapture(), newFakeObjects(), &fakeMailer{})
	m.now = func() time.Time { return windowNow }

	got, err := m.Create(context.Background(), CreateInput{StationID: uuid.New(), Day: "2026-11-05", Start: "14:00", End: "15:00"})
	require.NoError(t, err)
	require.Equal(t, StatusProgramada, got.Status)

	repo.url = ""
	_, err = m.Create(context.Background(), CreateInput{StationID: uuid.New(), Day: "2026-11-05", Start: "16:00", End: "17:00"})
	var ve *ValidationError
	require.True(t, errors.As(err, &ve))
	require.Equal(t, "esta emissora não tem URL de stream cadastrada", ve.Msg)
}

// O status muda ANTES de parar o gravador: na ordem inversa, um tick no meio
// veria `gravando` sem gravador vivo e religaria a captura.
func TestCancel_Gravando(t *testing.T) {
	now := time.Now()
	r := Recording{ID: uuid.New(), StartsAt: now, EndsAt: now.Add(time.Hour), Status: StatusGravando, Gaps: []Gap{}}
	repo := newFakeRepo(r)
	m := newTestManager(t, repo, holdCapture(), newFakeObjects(), &fakeMailer{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.startRecorder(ctx, r)

	require.NoError(t, m.Cancel(context.Background(), r.ID))

	got := repo.snapshot(r.ID)
	require.Equal(t, StatusProcessando, got.Status)
	require.NotNil(t, got.CanceledAt)
	m.mu.Lock()
	require.Empty(t, m.live)
	m.mu.Unlock()
}

func TestCancel_Programada(t *testing.T) {
	now := time.Now()
	r := Recording{ID: uuid.New(), StartsAt: now.Add(time.Hour), EndsAt: now.Add(2 * time.Hour), Status: StatusProgramada, Gaps: []Gap{}}
	repo := newFakeRepo(r)
	m := newTestManager(t, repo, holdCapture(), newFakeObjects(), &fakeMailer{})

	require.NoError(t, m.Cancel(context.Background(), r.ID))
	require.Equal(t, StatusCancelada, repo.snapshot(r.ID).Status)
	require.ErrorIs(t, m.Cancel(context.Background(), r.ID), ErrBusy)
}

func TestDelete(t *testing.T) {
	key := "recordings/x.m4a"
	done := Recording{ID: uuid.New(), Status: StatusConcluida, ObjectKey: &key, Gaps: []Gap{}}
	busy := Recording{ID: uuid.New(), Status: StatusGravando, Gaps: []Gap{}}
	repo, objs := newFakeRepo(done, busy), newFakeObjects()
	m := newTestManager(t, repo, holdCapture(), objs, &fakeMailer{})

	require.ErrorIs(t, m.Delete(context.Background(), busy.ID), ErrBusy)
	require.NoError(t, m.Delete(context.Background(), done.ID))
	require.Equal(t, []string{key}, objs.deleted)
	_, err := m.repo.Get(context.Background(), done.ID)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestAudio(t *testing.T) {
	key := "recordings/x.m4a"
	with := Recording{ID: uuid.New(), Status: StatusConcluida, ObjectKey: &key, Gaps: []Gap{}}
	without := Recording{ID: uuid.New(), Status: StatusExpirada, Gaps: []Gap{}}
	repo, objs := newFakeRepo(with, without), newFakeObjects()
	objs.puts[key] = []byte("audio")
	m := newTestManager(t, repo, holdCapture(), objs, &fakeMailer{})

	body, _, size, err := m.Audio(context.Background(), with.ID)
	require.NoError(t, err)
	defer body.Close()
	b, _ := io.ReadAll(body)
	require.Equal(t, "audio", string(b))
	require.EqualValues(t, 5, size)

	_, _, _, err = m.Audio(context.Background(), without.ID)
	require.ErrorIs(t, err, ErrNoAudio)
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd workers && go test ./internal/recording/ -run "TestTick|TestPrune|TestCreate|TestCancel|TestDelete|TestAudio"`
Expected: FAIL com `m.tick undefined`.

- [ ] **Step 3: Implementar o agendador**

Acrescentar ao fim de `workers/internal/recording/manager.go`:

```go
// Run bloqueia até ctx acabar.
func (m *Manager) Run(ctx context.Context) {
	tick := time.NewTicker(m.cfg.TickEvery)
	defer tick.Stop()
	prune := time.NewTicker(m.cfg.PruneEvery)
	defer prune.Stop()

	m.tick(ctx)
	m.prune(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			m.tick(ctx)
		case <-prune.C:
			m.prune(ctx)
		}
	}
}

// tick não guarda estado próprio: tudo vem do banco, e é isso que faz o reinício
// da API se resolver sozinho, sem caminho especial de recuperação.
func (m *Manager) tick(ctx context.Context) {
	now := m.now()

	missed, err := m.repo.FailMissed(ctx, now, "a plataforma ficou fora do ar durante toda a janela")
	if err != nil {
		m.log.Warn("recording: janelas perdidas", zap.Error(err))
	}
	for _, rec := range missed {
		m.notifyMissed(rec)
	}

	if _, err := m.repo.ClaimDue(ctx, now.Add(m.cfg.PreRoll), now); err != nil {
		m.log.Warn("recording: iniciar gravações", zap.Error(err))
	}

	recording, err := m.repo.ListByStatus(ctx, StatusGravando)
	if err != nil {
		m.log.Warn("recording: listar gravando", zap.Error(err))
	}
	for _, rec := range recording {
		if rec.EndsAt.After(now) {
			m.startRecorder(ctx, rec)
			continue
		}
		// Janela acabou com a API fora. Se há gravador vivo, ele mesmo fecha.
		m.mu.Lock()
		_, alive := m.live[rec.ID]
		m.mu.Unlock()
		if !alive {
			if err := m.repo.MarkProcessing(ctx, rec.ID); err != nil {
				m.log.Warn("recording: marcar processando", zap.Stringer("id", rec.ID), zap.Error(err))
			}
		}
	}

	processing, err := m.repo.ListByStatus(ctx, StatusProcessando)
	if err != nil {
		m.log.Warn("recording: listar processando", zap.Error(err))
	}
	for _, rec := range processing {
		m.startFinalize(ctx, rec)
	}
}

// prune apaga o objeto primeiro e só então marca a linha, como em
// evidence/prune.go: na ordem inversa, uma falha deixaria objeto que nunca mais
// seria encontrado.
func (m *Manager) prune(ctx context.Context) {
	expired, err := m.repo.Expired(ctx, m.now())
	if err != nil {
		m.log.Warn("recording: listar expiradas", zap.Error(err))
		return
	}
	for _, rec := range expired {
		if err := m.objects.Delete(ctx, *rec.ObjectKey); err != nil {
			m.log.Warn("recording: apagar áudio", zap.Stringer("id", rec.ID), zap.Error(err))
			continue
		}
		if err := m.repo.MarkExpired(ctx, rec.ID); err != nil {
			m.log.Warn("recording: marcar expirada", zap.Stringer("id", rec.ID), zap.Error(err))
		}
	}
}
```

- [ ] **Step 4: Implementar as operações do admin**

`workers/internal/recording/service.go`:

```go
package recording

import (
	"context"
	"errors"
	"io"

	"github.com/google/uuid"
)

// Create valida e agenda.
func (m *Manager) Create(ctx context.Context, in CreateInput) (*Recording, error) {
	from, to, err := ParseWindow(in.Day, in.Start, in.End, m.now())
	if err != nil {
		return nil, err
	}
	name, url, err := m.repo.StationInfo(ctx, in.StationID)
	if errors.Is(err, ErrNotFound) {
		return nil, &ValidationError{"emissora não encontrada"}
	}
	if err != nil {
		return nil, err
	}
	if url == "" {
		return nil, &ValidationError{"esta emissora não tem URL de stream cadastrada"}
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

// Cancel encerra uma gravação programada ou em andamento.
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

// Audio abre o arquivo. Quem chama fecha o corpo.
func (m *Manager) Audio(ctx context.Context, id uuid.UUID) (io.ReadCloser, *Recording, int64, error) {
	rec, err := m.repo.Get(ctx, id)
	if err != nil {
		return nil, nil, 0, err
	}
	if rec.ObjectKey == nil {
		return nil, nil, 0, ErrNoAudio
	}
	body, _, size, err := m.objects.Get(ctx, *rec.ObjectKey)
	if err != nil {
		return nil, nil, 0, err
	}
	return body, rec, size, nil
}
```

- [ ] **Step 5: Rodar e ver passar**

Run: `cd workers && gofmt -w internal/recording && go test ./internal/recording/ -v`
Expected: PASS no pacote inteiro.

- [ ] **Step 6: Commit**

```bash
git add workers/internal/recording
git commit -m "feat(gravacao): agendador, retencao e operacoes do admin"
```

---

## Task 9: Handler e rotas

**Files:**
- Create: `workers/internal/api/handlers/recordings.go`
- Test: `workers/internal/api/handlers/recordings_test.go`
- Test: `workers/internal/api/router_recordings_authz_test.go`
- Modify: `workers/internal/api/router.go:28` (`Deps`) e `:726` (grupo novo)

- [ ] **Step 1: Escrever o teste do handler**

`workers/internal/api/handlers/recordings_test.go`:

```go
package handlers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/auth"
	"radiocheck/internal/recording"
)

type fakeRecordings struct {
	createErr error
	cancelErr error
	deleteErr error
	audioErr  error
	gotInput  recording.CreateInput
	audioCtx  context.Context
}

func (f *fakeRecordings) Create(_ context.Context, in recording.CreateInput) (*recording.Recording, error) {
	f.gotInput = in
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &recording.Recording{ID: uuid.New(), Status: recording.StatusProgramada, Gaps: []recording.Gap{}}, nil
}
func (f *fakeRecordings) List(context.Context, int, int) ([]recording.Recording, int, error) {
	return []recording.Recording{}, 0, nil
}
func (f *fakeRecordings) Cancel(context.Context, uuid.UUID) error { return f.cancelErr }
func (f *fakeRecordings) Delete(context.Context, uuid.UUID) error { return f.deleteErr }
func (f *fakeRecordings) Audio(ctx context.Context, _ uuid.UUID) (io.ReadCloser, *recording.Recording, int64, error) {
	f.audioCtx = ctx
	if f.audioErr != nil {
		return nil, nil, 0, f.audioErr
	}
	return io.NopCloser(strings.NewReader("audio")),
		&recording.Recording{}, 5, nil
}

func recReq(method, target, body string, id uuid.UUID) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id.String())
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	return req.WithContext(auth.ContextWithClaims(ctx, &auth.Claims{UserID: uuid.New(), Role: "admin"}))
}

func TestRecordings_Create(t *testing.T) {
	st := uuid.New()
	f := &fakeRecordings{}
	h := &RecordingsHandler{Service: f}
	rec := httptest.NewRecorder()
	h.Create(rec, recReq("POST", "/recordings",
		`{"station_id":"`+st.String()+`","day":"2026-11-05","start":"14:00","end":"15:00"}`, uuid.Nil))

	require.Equal(t, http.StatusCreated, rec.Code)
	require.Equal(t, st, f.gotInput.StationID)
	require.Equal(t, "14:00", f.gotInput.Start)
	require.NotNil(t, f.gotInput.CreatedBy)
}

func TestRecordings_Create_Erros(t *testing.T) {
	cases := []struct {
		name string
		body string
		err  error
		code int
	}{
		{"json quebrado", `nope`, nil, 400},
		{"emissora invalida", `{"station_id":"x"}`, nil, 400},
		{"validacao vai para a tela", `{"station_id":"` + uuid.NewString() + `"}`, &recording.ValidationError{Msg: "o início já passou"}, 400},
		{"sobreposicao", `{"station_id":"` + uuid.NewString() + `"}`, recording.ErrOverlap, 409},
		{"erro interno nao vaza", `{"station_id":"` + uuid.NewString() + `"}`, errors.New("pg: detalhe interno"), 500},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := &RecordingsHandler{Service: &fakeRecordings{createErr: c.err}}
			rec := httptest.NewRecorder()
			h.Create(rec, recReq("POST", "/recordings", c.body, uuid.Nil))
			require.Equal(t, c.code, rec.Code)
			require.NotContains(t, rec.Body.String(), "pg:")
		})
	}
}

func TestRecordings_CancelEDelete(t *testing.T) {
	h := &RecordingsHandler{Service: &fakeRecordings{cancelErr: recording.ErrBusy, deleteErr: recording.ErrBusy}}
	rec := httptest.NewRecorder()
	h.Cancel(rec, recReq("POST", "/x", "", uuid.New()))
	require.Equal(t, http.StatusConflict, rec.Code)

	rec = httptest.NewRecorder()
	h.Delete(rec, recReq("DELETE", "/x", "", uuid.New()))
	require.Equal(t, http.StatusConflict, rec.Code)

	ok := &RecordingsHandler{Service: &fakeRecordings{}}
	rec = httptest.NewRecorder()
	ok.Delete(rec, recReq("DELETE", "/x", "", uuid.New()))
	require.Equal(t, http.StatusNoContent, rec.Code)
}

func TestRecordings_Audio(t *testing.T) {
	h := &RecordingsHandler{Service: &fakeRecordings{}}
	rec := httptest.NewRecorder()
	h.Audio(rec, recReq("GET", "/x", "", uuid.New()))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "audio/mp4", rec.Header().Get("Content-Type"))
	require.Equal(t, "5", rec.Header().Get("Content-Length"))
	require.Equal(t, "audio", rec.Body.String())
}

func TestRecordings_Audio_SemAudio(t *testing.T) {
	h := &RecordingsHandler{Service: &fakeRecordings{audioErr: recording.ErrNoAudio}}
	rec := httptest.NewRecorder()
	h.Audio(rec, recReq("GET", "/x", "", uuid.New()))
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// middleware.Timeout cancela r.Context() em 60s. Com ele ligado ao S3, o download
// de um arquivo grande seria cortado no meio.
func TestRecordings_Audio_ContextoNaoHerdaOTimeout(t *testing.T) {
	f := &fakeRecordings{}
	h := &RecordingsHandler{Service: f}
	req := recReq("GET", "/x", "", uuid.New())
	ctx, cancel := context.WithCancel(req.Context())
	cancel()

	h.Audio(httptest.NewRecorder(), req.WithContext(ctx))

	require.NoError(t, f.audioCtx.Err())
}
```

- [ ] **Step 2: Escrever o teste de autorização**

`workers/internal/api/router_recordings_authz_test.go`, no padrão de `router_postsale_authz_test.go`. Os helpers `issue` e `do` já existem no pacote de teste.

```go
package api

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"radiocheck/internal/auth"
)

// Gravação é tela exclusiva de admin: operator e viewer não entram.
func minimalRecordingsRouter() http.Handler {
	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	r := chi.NewRouter()
	r.Route("/v1/internal", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireJWT)
			r.Group(func(r chi.Router) {
				r.Use(auth.RequireRole("admin"))
				r.Get("/recordings", stub)
				r.Post("/recordings", stub)
				r.Post("/recordings/{id}/cancel", stub)
				r.Delete("/recordings/{id}", stub)
				r.Get("/recordings/{id}/audio", stub)
			})
		})
	})
	return r
}

func TestRecordings_AdminOnly(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	h := minimalRecordingsRouter()
	id := uuid.NewString()
	routes := []struct{ method, path string }{
		{"GET", "/v1/internal/recordings"},
		{"POST", "/v1/internal/recordings"},
		{"POST", "/v1/internal/recordings/" + id + "/cancel"},
		{"DELETE", "/v1/internal/recordings/" + id},
		{"GET", "/v1/internal/recordings/" + id + "/audio"},
	}
	ad, op, vw := issue(t, "admin"), issue(t, "operator"), issue(t, "viewer")
	for _, rt := range routes {
		if got := do(t, h, rt.method, rt.path, ad); got != http.StatusNoContent {
			t.Errorf("admin %s %s: got %d, want 204", rt.method, rt.path, got)
		}
		if got := do(t, h, rt.method, rt.path, op); got != http.StatusForbidden {
			t.Errorf("operator %s %s: got %d, want 403", rt.method, rt.path, got)
		}
		if got := do(t, h, rt.method, rt.path, vw); got != http.StatusForbidden {
			t.Errorf("viewer %s %s: got %d, want 403", rt.method, rt.path, got)
		}
		if got := do(t, h, rt.method, rt.path, ""); got != http.StatusUnauthorized {
			t.Errorf("sem token %s %s: got %d, want 401", rt.method, rt.path, got)
		}
	}
}
```

Este teste valida o middleware com rotas espelhadas, como os outros `*_authz_test.go` do repo. O que liga as rotas reais ao mesmo grupo é o Step 6.

- [ ] **Step 3: Rodar e ver falhar**

Run: `cd workers && go test ./internal/api/handlers/ -run TestRecordings`
Expected: FAIL com `undefined: RecordingsHandler`.

- [ ] **Step 4: Implementar o handler**

`workers/internal/api/handlers/recordings.go`:

```go
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"radiocheck/internal/auth"
	"radiocheck/internal/recording"
)

// RecordingService é o que o handler usa do recording.Manager.
type RecordingService interface {
	Create(ctx context.Context, in recording.CreateInput) (*recording.Recording, error)
	List(ctx context.Context, limit, offset int) ([]recording.Recording, int, error)
	Cancel(ctx context.Context, id uuid.UUID) error
	Delete(ctx context.Context, id uuid.UUID) error
	Audio(ctx context.Context, id uuid.UUID) (io.ReadCloser, *recording.Recording, int64, error)
}

// RecordingsHandler serve /recordings. Doc: docs/features/scheduled-recording.md
type RecordingsHandler struct {
	Service RecordingService
	Log     *zap.Logger
}

func (h *RecordingsHandler) fail(w http.ResponseWriter, err error) {
	var ve *recording.ValidationError
	switch {
	case errors.As(err, &ve):
		http.Error(w, ve.Msg, http.StatusBadRequest)
	case errors.Is(err, recording.ErrOverlap), errors.Is(err, recording.ErrBusy):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, recording.ErrNotFound), errors.Is(err, recording.ErrNoAudio):
		http.Error(w, err.Error(), http.StatusNotFound)
	default:
		if h.Log != nil {
			h.Log.Error("recordings: erro interno", zap.Error(err))
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

type createRecordingRequest struct {
	StationID string `json:"station_id"`
	Day       string `json:"day"`
	Start     string `json:"start"`
	End       string `json:"end"`
}

// Create agenda uma gravação.
func (h *RecordingsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createRecordingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	stationID, err := uuid.Parse(req.StationID)
	if err != nil {
		http.Error(w, "emissora inválida", http.StatusBadRequest)
		return
	}
	in := recording.CreateInput{StationID: stationID, Day: req.Day, Start: req.Start, End: req.End}
	if claims, ok := auth.ClaimsFromContext(r.Context()); ok {
		in.CreatedBy = &claims.UserID
	}
	rec, err := h.Service.Create(r.Context(), in)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rec)
}

// List pagina as gravações.
func (h *RecordingsHandler) List(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	items, total, err := h.Service.List(r.Context(), limit, offset)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

// Cancel encerra uma gravação programada ou em andamento.
func (h *RecordingsHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := h.Service.Cancel(r.Context(), id); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Delete apaga áudio e linha.
func (h *RecordingsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := h.Service.Delete(r.Context(), id); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Audio entrega os bytes pela API: URL pré-assinada do MinIO não funciona no
// navegador em prod (docs/features/evidence-presigned-urls.md).
func (h *RecordingsHandler) Audio(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	// WithoutCancel: o middleware.Timeout do router cancela r.Context() em 60s, e
	// esse contexto governa a leitura do S3. Um arquivo de 340 MB seria cortado no
	// meio. A queda do cliente segue detectada pelo erro de escrita do io.Copy.
	body, _, size, err := h.Service.Audio(context.WithoutCancel(r.Context()), id)
	if err != nil {
		h.fail(w, err)
		return
	}
	defer body.Close()

	w.Header().Set("Content-Type", "audio/mp4")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s.m4a"`, id))
	io.Copy(w, body) //nolint:errcheck
}
```

- [ ] **Step 5: Registrar as rotas**

Em `workers/internal/api/router.go`, acrescentar ao `Deps`, logo após `Assertiveness`:

```go
	Recordings            *handlers.RecordingsHandler
```

E logo depois do bloco `if d.DailyFailuresDigest != nil { ... }`:

```go
				// /recordings — programar gravação de um trecho de stream.
				// Admin-only por completo. Doc: docs/features/scheduled-recording.md
				if d.Recordings != nil {
					r.Group(func(r chi.Router) {
						r.Use(auth.RequireRole("admin"))
						r.Get("/recordings", d.Recordings.List)
						r.Post("/recordings", d.Recordings.Create)
						r.Post("/recordings/{id}/cancel", d.Recordings.Cancel)
						r.Delete("/recordings/{id}", d.Recordings.Delete)
						r.Get("/recordings/{id}/audio", d.Recordings.Audio)
					})
				}
```

- [ ] **Step 6: Conferir o grupo real**

Run: `grep -n "d.Recordings" workers/internal/api/router.go`
Confirmar que as rotas estão num `r.Use(auth.RequireRole("admin"))`, dentro do bloco `RequireJWTAtivo`, no mesmo nível de `d.DailyFailuresDigest`.

- [ ] **Step 7: Rodar e ver passar**

Run: `cd workers && gofmt -w internal/api internal/recording && go test ./internal/api/ && go test ./internal/api/handlers/ -run TestRecordings -v`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add workers/internal/api workers/internal/recording
git commit -m "feat(gravacao): API admin de gravacoes"
```

---

## Task 10: Ligar na API

**Files:**
- Modify: `workers/cmd/api/main.go` (após o bloco do `storage.New`, por volta da linha 118, e no `deps := api.Deps{`)

- [ ] **Step 1: Criar o Manager antes da carga do índice**

Logo depois do bloco `s3Client, err := storage.New(...)`:

```go
	// Programar gravação. Sobe ANTES da carga do índice, que leva minutos: depois
	// de um reinício, cada minuto de boot seria um minuto de buraco na gravação.
	// Enabled true: sem credenciais SMTP, mailer.New já devolve noop.
	// Doc: docs/features/scheduled-recording.md
	recordings := recording.NewManager(
		recording.Config{
			Root:    filepath.Join(cfg.SegmentsPath, "_recordings"),
			BaseURL: cfg.NotificationsBaseURL,
		},
		recording.NewStore(pool),
		recording.FFmpeg{},
		s3Client,
		mailer.New(mailer.Config{
			Enabled: true,
			Host:    cfg.SMTPHost,
			Port:    cfg.SMTPPort,
			User:    cfg.SMTPUser,
			Pass:    cfg.SMTPPass,
			From:    cfg.MailFrom,
		}, logger),
		logger,
	)
	go recordings.Run(ctx)
```

Acrescentar os imports `"path/filepath"` (se faltar) e `"radiocheck/internal/recording"`.

- [ ] **Step 2: Entregar ao router**

No `deps := api.Deps{`, junto de `Assertiveness:`:

```go
		Recordings:            &handlers.RecordingsHandler{Service: recordings, Log: logger},
```

- [ ] **Step 3: Conferir `ctx`**

Run: `grep -n "ctx, pararDeOuvirSinais\|recordings.Run" workers/cmd/api/main.go`
Expected: `ctx` é o `signal.NotifyContext` e nasce antes de `recordings.Run`. É ele que faz o SIGTERM do deploy chegar ao ffmpeg.

- [ ] **Step 4: Compilar como o Dockerfile**

Run: `cd workers && CGO_ENABLED=0 GOOS=linux go build ./... && go vet ./internal/recording/ ./internal/api/...`
Expected: sem saída.

- [ ] **Step 5: Commit**

```bash
git add workers/cmd/api/main.go
git commit -m "feat(gravacao): liga o manager na API"
```

---

## Task 11: Guarda no deploy

**Files:**
- Modify: `scripts/deploy.sh` (helper perto de `snapshot_counts`; chamada antes de `step "git pull"`)

- [ ] **Step 1: Escrever a função**

Em `scripts/deploy.sh`, logo depois de `snapshot_counts()`:

```bash
# ── guarda de gravação em andamento ──────────────────────────────────────────
#
# A gravação programada roda dentro da API: recriar o container corta o áudio
# enquanto ela reinicia, e numa entrevista ao vivo não existe segunda chance.
# Doc: docs/features/scheduled-recording.md
#
# `processando` não bloqueia: a finalização é repetível, retoma sozinha depois
# do reinício, e bloquear travaria todo deploy se uma finalização falhasse para
# sempre.
#
# Tabela inexistente conta como zero, para esta guarda não travar o próprio
# deploy que cria a tabela. psql inacessível só avisa: guarda que dá alarme falso
# ensina a usar o bypass.
guard_active_recordings() {
  if [ "${SKIP_RECORDING_GUARD:-no}" = "yes" ]; then
    warn "SKIP_RECORDING_GUARD=yes — pulando a guarda de gravações."
    return 0
  fi
  service_running postgres || return 0

  local sql rows
  sql="SELECT station_name || ' | ' || status || ' | ' || to_char(starts_at AT TIME ZONE 'America/Sao_Paulo', 'DD/MM HH24:MI') || ' a ' || to_char(ends_at AT TIME ZONE 'America/Sao_Paulo', 'HH24:MI') FROM recordings WHERE status = 'gravando' OR (status = 'programada' AND starts_at < now() + INTERVAL '10 minutes') ORDER BY starts_at"

  local exists
  exists=$($COMPOSE exec -T postgres sh -c \
    'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -At -c "SELECT to_regclass('"'"'public.recordings'"'"') IS NOT NULL"' 2>/dev/null) \
    || { warn "não consegui consultar gravações em andamento — seguindo."; return 0; }
  [ "$exists" = "t" ] || return 0

  rows=$(printf '%s' "$sql" | $COMPOSE exec -T postgres sh -c \
    'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -At' 2>/dev/null) \
    || { warn "não consegui consultar gravações em andamento — seguindo."; return 0; }

  if [ -n "$rows" ]; then
    printf '%s\n' "$rows" | sed 's/^/    /' >&2
    err "há gravação em andamento ou começando em menos de 10 minutos. Espere terminar ou cancele em /admin/gravacoes.
     (bypass de emergência: SKIP_RECORDING_GUARD=yes — corta o áudio enquanto a API reinicia)"
  fi
}
```

- [ ] **Step 2: Chamar antes do pull**

Logo acima de `step "git pull"`:

```bash
# ── gravações em andamento (antes de qualquer build/up) ──────────────────────
guard_active_recordings

```

- [ ] **Step 3: Validar a sintaxe**

Run: `bash -n scripts/deploy.sh`
Expected: sem saída.

- [ ] **Step 4: Testar a função com compose simulado**

Criar `C:\Users\marke\AppData\Local\Temp\claude\a677b43df84118d2\6e0a7098-5f26-46f9-a015-0e2cea667486\scratchpad\test_guard.sh`:

```bash
#!/usr/bin/env bash
# Testa guard_active_recordings extraída do deploy.sh, com $COMPOSE simulado.
set -u
script="$1"
warn() { echo "WARN: $1" >&2; }
err()  { echo "ERRO: $1" >&2; exit 1; }
service_running() { return 0; }
eval "$(sed -n '/^guard_active_recordings() {/,/^}/p' "$script")"

COMPOSE="bash $(dirname "$0")/fake_compose.sh"
export FAKE_EXISTS FAKE_ROWS

FAKE_EXISTS=t FAKE_ROWS="" ; (guard_active_recordings) && echo "ok: sem gravação passa" || echo "FALHOU 1"
FAKE_EXISTS=f FAKE_ROWS="" ; (guard_active_recordings) && echo "ok: tabela ausente passa" || echo "FALHOU 2"
FAKE_EXISTS=t FAKE_ROWS="Radio X | gravando | 05/11 20:00 a 21:00"
(guard_active_recordings) 2>/dev/null && echo "FALHOU 3" || echo "ok: gravação ativa aborta"
SKIP_RECORDING_GUARD=yes ; (guard_active_recordings) 2>/dev/null && echo "ok: bypass passa" || echo "FALHOU 4"
```

E `fake_compose.sh`, na mesma pasta:

```bash
#!/usr/bin/env bash
# Ignora os argumentos; decide a resposta pelo comando recebido.
if printf '%s' "$*" | grep -q to_regclass; then echo "$FAKE_EXISTS"; else cat >/dev/null; printf '%s' "$FAKE_ROWS"; fi
```

Run: `bash "C:/Users/marke/AppData/Local/Temp/claude/a677b43df84118d2/6e0a7098-5f26-46f9-a015-0e2cea667486/scratchpad/test_guard.sh" scripts/deploy.sh`
Expected: quatro linhas `ok:`, nenhuma `FALHOU`.

- [ ] **Step 5: Commit**

```bash
git add scripts/deploy.sh
git commit -m "feat(gravacao): deploy para se houver gravacao em andamento"
```

---

## Task 12: Frontend: formatação e hooks

**Files:**
- Create: `frontend/src/utils/recordings.js`
- Test: `frontend/src/utils/recordings.test.mjs`
- Modify: `frontend/src/api/hooks.js` (fim do arquivo)
- Modify: `frontend/src/utils/userJourney.js:178`

- [ ] **Step 1: Escrever o teste que falha**

`frontend/src/utils/recordings.test.mjs`:

```js
import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  coveragePercent, downloadName, gapLabels, isLive, statusInfo, todayInSaoPaulo, windowLabel, expiresLabel,
} from './recordings.js'

const rec = {
  status: 'parcial',
  starts_at: '2026-11-05T23:00:00Z',
  ends_at: '2026-11-06T00:00:00Z',
  covered_seconds: 1800,
  gaps: [{ from: '2026-11-05T23:10:00Z', to: '2026-11-05T23:40:00Z' }],
}

test('janela aparece em horário de São Paulo', () => {
  assert.equal(windowLabel(rec), '05/11/2026, 20:00 às 21:00')
})

test('cobertura em percentual da janela', () => {
  assert.equal(coveragePercent(rec), 50)
  assert.equal(coveragePercent({ ...rec, covered_seconds: 3700 }), 100, 'pre-roll não passa de 100')
})

test('sem cobertura é null, não zero', () => {
  assert.equal(coveragePercent({ ...rec, covered_seconds: null }), null)
})

test('buracos em horário de São Paulo', () => {
  assert.deepEqual(gapLabels(rec), ['sem áudio das 20:10:00 às 20:40:00'])
  assert.deepEqual(gapLabels({ ...rec, gaps: [] }), [])
})

test('isLive pede atualização automática da lista', () => {
  assert.equal(isLive([{ status: 'concluida' }]), false)
  assert.equal(isLive([{ status: 'concluida' }, { status: 'gravando' }]), true)
  assert.equal(isLive([{ status: 'programada' }]), true)
  assert.equal(isLive([{ status: 'processando' }]), true)
  assert.equal(isLive(undefined), false)
})

test('todo status tem rótulo', () => {
  for (const s of ['programada', 'gravando', 'processando', 'concluida', 'parcial', 'falhou', 'cancelada', 'expirada']) {
    assert.ok(statusInfo(s).label, s)
    assert.ok(statusInfo(s).badge, s)
  }
})

test('dia de hoje em São Paulo, não em UTC', () => {
  assert.equal(todayInSaoPaulo(new Date('2026-11-06T01:00:00Z')), '2026-11-05')
})

test('nome do arquivo sem acento e em horário de São Paulo', () => {
  assert.equal(
    downloadName({ station_name: 'Rádio Canção FM 99,9', starts_at: '2026-11-05T23:00:00Z' }),
    'gravacao-radio-cancao-fm-99-9-2026-11-05-20h00.m4a',
  )
})

test('expiração só existe com áudio', () => {
  assert.equal(expiresLabel({ expires_at: '2026-11-20T23:00:00Z', has_audio: true }), '20/11/2026')
  assert.equal(expiresLabel({ expires_at: null, has_audio: false }), '—')
  assert.equal(expiresLabel({ expires_at: '2026-11-20T23:00:00Z', has_audio: false }), '—')
})
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd frontend && node --test src/utils/recordings.test.mjs`
Expected: FAIL com `Cannot find module`.

- [ ] **Step 3: Implementar**

`frontend/src/utils/recordings.js`:

```js
// Formatação da tela /admin/gravacoes. Tudo em horário de São Paulo: é o que o
// admin digitou e o que a rádio usa, independente do fuso do navegador.
const TZ = 'America/Sao_Paulo'

const dayFmt = new Intl.DateTimeFormat('pt-BR', { timeZone: TZ, day: '2-digit', month: '2-digit', year: 'numeric' })
const timeFmt = new Intl.DateTimeFormat('pt-BR', { timeZone: TZ, hour: '2-digit', minute: '2-digit' })
// Só para buracos: um de 40s pode caber dentro do mesmo minuto.
const gapTimeFmt = new Intl.DateTimeFormat('pt-BR', { timeZone: TZ, hour: '2-digit', minute: '2-digit', second: '2-digit' })

const STATUS = {
  programada: { label: 'Programada', badge: 'badge-planned' },
  gravando: { label: 'Gravando', badge: 'badge-active' },
  processando: { label: 'Processando', badge: 'badge-planned' },
  concluida: { label: 'Concluída', badge: 'badge-active' },
  parcial: { label: 'Parcial', badge: 'badge-paused' },
  falhou: { label: 'Falhou', badge: 'badge-error' },
  cancelada: { label: 'Cancelada', badge: 'badge-ended' },
  expirada: { label: 'Expirada', badge: 'badge-ended' },
}

const LIVE = new Set(['programada', 'gravando', 'processando'])

export function statusInfo(status) {
  return STATUS[status] ?? { label: status, badge: 'badge-ended' }
}

export function isLive(items) {
  return (items ?? []).some(r => LIVE.has(r.status))
}

export function windowLabel(r) {
  const from = new Date(r.starts_at)
  return `${dayFmt.format(from)}, ${timeFmt.format(from)} às ${timeFmt.format(new Date(r.ends_at))}`
}

/** null = ainda não medida. Não é a mesma coisa que 0%. */
export function coveragePercent(r) {
  if (r.covered_seconds == null) return null
  const window = (new Date(r.ends_at) - new Date(r.starts_at)) / 1000
  if (window <= 0) return null
  return Math.min(100, Math.round((r.covered_seconds / window) * 100))
}

export function gapLabels(r) {
  return (r.gaps ?? []).map(g =>
    `sem áudio das ${gapTimeFmt.format(new Date(g.from))} às ${gapTimeFmt.format(new Date(g.to))}`)
}

export function expiresLabel(r) {
  if (!r.has_audio || !r.expires_at) return '—'
  return dayFmt.format(new Date(r.expires_at))
}

/** AAAA-MM-DD, o formato do input type=date. */
export function todayInSaoPaulo(now = new Date()) {
  const parts = new Intl.DateTimeFormat('en-CA', { timeZone: TZ, year: 'numeric', month: '2-digit', day: '2-digit' })
  return parts.format(now)
}

/** O nome é montado aqui porque o arquivo chega como blob, sem o header da API. */
export function downloadName(r) {
  const slug = r.station_name
    .normalize('NFD').replace(/\p{Diacritic}/gu, '')
    .toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '')
  const from = new Date(r.starts_at)
  const [hh, mm] = timeFmt.format(from).split(':')
  return `gravacao-${slug}-${todayInSaoPaulo(from)}-${hh}h${mm}.m4a`
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd frontend && node --test src/utils/recordings.test.mjs`
Expected: PASS.

- [ ] **Step 5: Acrescentar os hooks**

No topo de `frontend/src/api/hooks.js`, junto dos outros imports:

```js
import { isLive } from '../utils/recordings'
```

Ao fim do arquivo:

```js
// ── Gravações programadas ────────────────────────────────────────────────
// Só admin. Ver docs/features/scheduled-recording.md.
const RECORDINGS_KEY = ['recordings']
const RECORDINGS_POLL_MS = 5000

export function useRecordings({ limit = 50, offset = 0 } = {}) {
  return useQuery({
    queryKey: [...RECORDINGS_KEY, limit, offset],
    queryFn: () => api.get('/recordings', { params: { limit, offset } }).then(r => r.data),
    // Só atualiza sozinha enquanto houver gravação que ainda pode mudar de status.
    refetchInterval: query => (isLive(query.state.data?.items) ? RECORDINGS_POLL_MS : false),
  })
}

export function useCreateRecording() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: body => api.post('/recordings', body).then(r => r.data),
    onSuccess: () => qc.invalidateQueries({ queryKey: RECORDINGS_KEY }),
  })
}

export function useCancelRecording() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: id => api.post(`/recordings/${id}/cancel`),
    onSuccess: () => qc.invalidateQueries({ queryKey: RECORDINGS_KEY }),
  })
}

export function useDeleteRecording() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: id => api.delete(`/recordings/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: RECORDINGS_KEY }),
  })
}

// Imperativo: <audio> e <a download> não mandam o header Authorization, então o
// arquivo vem como blob. Gravação longa pesa: o "Baixar" é o caminho indicado.
export async function fetchRecordingAudio(id) {
  const resp = await api.get(`/recordings/${id}/audio`, { responseType: 'blob' })
  return resp.data
}
```

- [ ] **Step 6: Mapear na jornada do usuário**

Em `frontend/src/utils/userJourney.js`, logo depois de `'GET /admin/station-failures'`:

```js
  'GET /recordings': A('Gravações', 'Abriu as gravações', CATEGORY.navigate),
  'POST /recordings': A('Gravações', 'Programou uma gravação', CATEGORY.create),
  'POST /recordings/{id}/cancel': A('Gravações', 'Cancelou uma gravação', CATEGORY.update),
  'DELETE /recordings/{id}': A('Gravações', 'Excluiu uma gravação', CATEGORY.destructive),
  'GET /recordings/{id}/audio': A('Gravações', 'Baixou uma gravação', CATEGORY.navigate),
```

- [ ] **Step 7: Commit**

```bash
git add frontend/src/utils/recordings.js frontend/src/utils/recordings.test.mjs frontend/src/api/hooks.js frontend/src/utils/userJourney.js
git commit -m "feat(gravacao): formatacao e hooks do frontend"
```

---

## Task 13: Frontend: tela

**Files:**
- Create: `frontend/src/pages/AdminRecordingsPage.jsx`
- Create: `frontend/src/pages/AdminRecordingsPage.css`
- Modify: `frontend/src/App.jsx` (import e rota, junto de `/admin/pos-venda`)
- Modify: `frontend/src/components/Sidebar.jsx:186` (ícone) e `:258` (link)

- [ ] **Step 1: Escrever a tela**

`frontend/src/pages/AdminRecordingsPage.jsx`:

```jsx
// AdminRecordingsPage.jsx — /admin/gravacoes.
//
// Programa a gravação de um trecho de stream e lista o que já foi gravado. O
// áudio fica 15 dias e depois é apagado. Ver docs/features/scheduled-recording.md.
import { useEffect, useState } from 'react'

import {
  fetchRecordingAudio, useCancelRecording, useCreateRecording, useDeleteRecording,
  useRecordings, useStationConnectionTest,
} from '../api/hooks'
import AudioPlayer from '../components/AudioPlayer'
import { useAlert, useConfirm } from '../components/ConfirmModal'
import StationSearch from '../components/StationSearch'
import {
  coveragePercent, downloadName, expiresLabel, gapLabels, statusInfo, todayInSaoPaulo, windowLabel,
} from '../utils/recordings'
import './AdminRecordingsPage.css'

function apiMessage(err) {
  const data = err?.response?.data
  return typeof data === 'string' && data.trim() ? data.trim() : 'Não foi possível concluir. Tente de novo.'
}

function RecordingForm() {
  const [selection, setSelection] = useState(null)
  const [day, setDay] = useState(() => todayInSaoPaulo())
  const [start, setStart] = useState('')
  const [end, setEnd] = useState('')
  const [error, setError] = useState('')
  const [warning, setWarning] = useState('')
  const create = useCreateRecording()
  const testStream = useStationConnectionTest()

  const stationId = selection?.kind === 'station' ? selection.id : null
  const ready = Boolean(stationId && day && start && end)

  async function submit(e) {
    e.preventDefault()
    setError('')
    setWarning('')
    let created
    try {
      created = await create.mutateAsync({ station_id: stationId, day, start, end })
    } catch (err) {
      setError(apiMessage(err))
      return
    }
    setStart('')
    setEnd('')
    // Falha aqui avisa e não bloqueia: dá tempo de corrigir a URL até o horário.
    try {
      const r = await testStream.mutateAsync({ id: created.station_id, tests: ['stream'] })
      if (r.results?.stream?.status !== 'ok') {
        setWarning(`Gravação programada, mas o stream de ${created.station_name} não respondeu agora. Confira a URL antes do horário.`)
      }
    } catch {
      setWarning('Gravação programada, mas não consegui testar o stream agora.')
    }
  }

  return (
    <form onSubmit={submit}>
      <div className="flow-filters flow-filters--auto ar-form">
        <div className={`flow-filter ${stationId ? 'flow-filter--done' : 'flow-filter--active'}`}>
          <label className="flow-filter-label">
            <span className="flow-filter-label-step">1</span>
            Emissora
          </label>
          <StationSearch value={selection} onChange={setSelection} placeholder="Buscar emissora…" />
        </div>
        <div className={`flow-filter ${day ? 'flow-filter--done' : 'flow-filter--active'}`}>
          <label className="flow-filter-label" htmlFor="ar-day">
            <span className="flow-filter-label-step">2</span>
            Dia
          </label>
          <input id="ar-day" className="ar-input" type="date" value={day} onChange={e => setDay(e.target.value)} />
        </div>
        <div className={`flow-filter ${start ? 'flow-filter--done' : 'flow-filter--active'}`}>
          <label className="flow-filter-label" htmlFor="ar-start">
            <span className="flow-filter-label-step">3</span>
            Início
          </label>
          <input id="ar-start" className="ar-input" type="time" value={start} onChange={e => setStart(e.target.value)} />
        </div>
        <div className={`flow-filter ${end ? 'flow-filter--done' : 'flow-filter--active'}`}>
          <label className="flow-filter-label" htmlFor="ar-end">
            <span className="flow-filter-label-step">4</span>
            Fim
          </label>
          <input id="ar-end" className="ar-input" type="time" value={end} onChange={e => setEnd(e.target.value)} />
        </div>
        <button type="submit" className="btn btn-primary ar-submit" disabled={!ready || create.isPending}>
          {create.isPending ? 'Programando…' : 'Programar'}
        </button>
      </div>
      <p className="ar-hint">
        Horário de Brasília. Até 6 horas, no mesmo dia. O áudio fica disponível por 15 dias.
      </p>
      {error && <p className="ar-msg ar-msg--error" role="alert">{error}</p>}
      {warning && <p className="ar-msg ar-msg--warn" role="status">{warning}</p>}
    </form>
  )
}

function RecordingRow({ rec }) {
  const confirm = useConfirm()
  const alert = useAlert()
  const cancelRec = useCancelRecording()
  const deleteRec = useDeleteRecording()
  const [audioUrl, setAudioUrl] = useState('')
  const [playing, setPlaying] = useState(false)
  const [busy, setBusy] = useState(false)

  useEffect(() => () => { if (audioUrl) URL.revokeObjectURL(audioUrl) }, [audioUrl])

  const info = statusInfo(rec.status)
  const coverage = coveragePercent(rec)
  const gaps = gapLabels(rec)
  const showIssue = rec.status === 'parcial' || rec.status === 'falhou'
  const showError = ['falhou', 'processando'].includes(rec.status) && rec.last_error
  const canCancel = rec.status === 'programada' || rec.status === 'gravando'
  const canDelete = !['programada', 'gravando', 'processando'].includes(rec.status)

  async function listen() {
    if (audioUrl) {
      setPlaying(true)
      return
    }
    setBusy(true)
    try {
      const blob = await fetchRecordingAudio(rec.id)
      setAudioUrl(URL.createObjectURL(blob))
      setPlaying(true)
    } catch (err) {
      await alert(apiMessage(err))
    } finally {
      setBusy(false)
    }
  }

  async function download() {
    setBusy(true)
    try {
      const blob = await fetchRecordingAudio(rec.id)
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = downloadName(rec)
      document.body.appendChild(a)
      a.click()
      a.remove()
      URL.revokeObjectURL(url)
    } catch (err) {
      await alert(apiMessage(err))
    } finally {
      setBusy(false)
    }
  }

  async function cancel() {
    const live = rec.status === 'gravando'
    const ok = await confirm(live
      ? 'Parar esta gravação agora? O que já foi gravado fica salvo.'
      : 'Cancelar esta gravação programada?')
    if (!ok) return
    try { await cancelRec.mutateAsync(rec.id) } catch (err) { await alert(apiMessage(err)) }
  }

  async function remove() {
    const ok = await confirm('Excluir esta gravação? O áudio será apagado e não dá para recuperar.')
    if (!ok) return
    try { await deleteRec.mutateAsync(rec.id) } catch (err) { await alert(apiMessage(err)) }
  }

  return (
    <>
      <tr>
        <td className="ar-station">{rec.station_name}</td>
        <td>{windowLabel(rec)}</td>
        <td><span className={`badge ${info.badge}`}>{info.label}</span></td>
        <td>{coverage == null ? '—' : `${coverage}%`}</td>
        <td>{expiresLabel(rec)}</td>
        <td className="ar-actions">
          {rec.has_audio && (
            <>
              <button type="button" className="btn btn-secondary btn-sm" onClick={listen} disabled={busy}>Ouvir</button>
              <button type="button" className="btn btn-secondary btn-sm" onClick={download} disabled={busy}>Baixar</button>
            </>
          )}
          {canCancel && (
            <button type="button" className="btn btn-danger-ghost btn-sm" onClick={cancel} disabled={cancelRec.isPending}>
              {rec.status === 'gravando' ? 'Parar' : 'Cancelar'}
            </button>
          )}
          {canDelete && (
            <button type="button" className="btn btn-danger-ghost btn-sm" onClick={remove} disabled={deleteRec.isPending}>Excluir</button>
          )}
        </td>
      </tr>
      {(audioUrl || (showIssue && gaps.length > 0) || showError) && (
        <tr className="ar-detail">
          <td colSpan={6}>
            {audioUrl && (
              <AudioPlayer src={audioUrl} isPlaying={playing} onPlay={() => setPlaying(true)} onPause={() => setPlaying(false)} />
            )}
            {showIssue && gaps.length > 0 && (
              <ul className="ar-gaps">{gaps.map(g => <li key={g}>{g}</li>)}</ul>
            )}
            {showError && <p className="ar-error-detail">{rec.last_error}</p>}
          </td>
        </tr>
      )}
    </>
  )
}

export default function AdminRecordingsPage() {
  const { data, isLoading, isError } = useRecordings()
  const items = data?.items ?? []
  const total = data?.total ?? 0

  return (
    <div>
      <div className="page-header">
        <h1>Gravações</h1>
      </div>

      <RecordingForm />

      {isError && <p className="ar-msg ar-msg--error">Não foi possível carregar as gravações.</p>}
      {!isLoading && !isError && items.length === 0 && (
        <p className="ar-empty">Nenhuma gravação programada ainda.</p>
      )}
      {items.length > 0 && (
        <div className="ar-table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th>Emissora</th>
                <th>Horário</th>
                <th>Status</th>
                <th>Cobertura</th>
                <th>Expira em</th>
                <th aria-label="Ações" />
              </tr>
            </thead>
            <tbody>
              {items.map(r => <RecordingRow key={r.id} rec={r} />)}
            </tbody>
          </table>
          {total > items.length && (
            <p className="ar-hint">Mostrando as {items.length} mais recentes de {total}.</p>
          )}
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 2: Escrever o CSS**

`frontend/src/pages/AdminRecordingsPage.css`:

```css
/* /admin/gravacoes. Prefixo ar- para não vazar. */
.ar-form {
  --flow-cols: minmax(260px, 1.6fr) 160px 120px 120px auto;
  margin-bottom: 8px;
  align-items: end;
}

.ar-input {
  width: 100%;
  height: 38px;
  padding: 0 10px;
  border: 1px solid var(--c-border);
  border-radius: var(--radius-md);
  background: var(--c-surface);
  color: var(--c-text);
  font: inherit;
}

.ar-submit {
  height: 38px;
  white-space: nowrap;
}

.ar-hint {
  margin: 0 0 16px;
  color: var(--c-text-2);
  font-size: 13px;
}

.ar-msg {
  margin: 0 0 16px;
  padding: 10px 12px;
  border-radius: var(--radius-md);
  font-size: 14px;
}

.ar-msg--error {
  background: color-mix(in srgb, var(--c-danger) 12%, transparent);
  color: var(--c-danger);
}

.ar-msg--warn {
  background: color-mix(in srgb, var(--c-warning) 14%, transparent);
  color: var(--c-text);
}

.ar-empty {
  padding: 32px 0;
  color: var(--c-text-2);
  text-align: center;
}

.ar-table-wrap {
  overflow-x: auto;
}

.ar-station {
  font-weight: 600;
}

.ar-actions {
  display: flex;
  gap: 6px;
  justify-content: flex-end;
  white-space: nowrap;
}

.ar-detail td {
  padding-top: 0;
}

.ar-gaps {
  margin: 8px 0 0;
  padding-left: 18px;
  color: var(--c-text-2);
  font-size: 13px;
}

.ar-error-detail {
  margin: 8px 0 0;
  color: var(--c-text-2);
  font-size: 13px;
  overflow-wrap: anywhere;
}
```

- [ ] **Step 3: Registrar a rota**

Em `frontend/src/App.jsx`, junto dos imports de admin:

```jsx
import AdminRecordingsPage from './pages/AdminRecordingsPage'
```

E antes da rota `/admin/pos-venda`:

```jsx
            <Route path="/admin/gravacoes" element={
              <RequireRole roles={['admin']}><AdminRecordingsPage /></RequireRole>
            } />
```

- [ ] **Step 4: Adicionar ao menu**

Em `frontend/src/components/Sidebar.jsx`, depois de `IconPostSale`:

```jsx
function IconRecordings() {
  // Círculo com ponto: o "rec" de qualquer gravador.
  return (
    <svg className="sidebar-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="8" cy="8" r="6" />
      <circle cx="8" cy="8" r="2.4" fill="currentColor" stroke="none" />
    </svg>
  )
}
```

E na seção Administração, depois de "Falhas por emissora":

```jsx
      <SidebarLink to="/admin/gravacoes"         icon={<IconRecordings />}        onClose={onClose}>Gravações</SidebarLink>
```

- [ ] **Step 5: Compilar e rodar o lint**

Run: `cd frontend && npx eslint src/pages/AdminRecordingsPage.jsx src/utils/recordings.js && npx vite build`
Expected: sem erro. Se `node_modules` não existir na worktree, rodar `npm ci`, nunca `npm install`.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/pages/AdminRecordingsPage.jsx frontend/src/pages/AdminRecordingsPage.css frontend/src/App.jsx frontend/src/components/Sidebar.jsx
git commit -m "feat(gravacao): tela /admin/gravacoes"
```

---

## Task 14: Documentação

**Files:**
- Create: `docs/features/scheduled-recording.md`
- Modify: `CLAUDE.md` (mapa de consulta, depois da linha de `/admin/station-failures`)
- Modify: `docs/README.md` (tabela de `features/`)
- Modify: `docs/operations/deploy.md` (seção sobre o `deploy.sh`)

- [ ] **Step 1: Escrever o doc da feature**

`docs/features/scheduled-recording.md`:

```markdown
---
status: implementado
ultima-verificacao: 2026-11-05
codigo-relacionado:
  - workers/internal/recording/manager.go
  - workers/internal/recording/recorder.go
  - workers/internal/recording/finalize.go
  - workers/internal/recording/ffmpeg.go
  - workers/internal/api/handlers/recordings.go
  - migrations/0071_recordings.up.sql
  - scripts/deploy.sh
  - frontend/src/pages/AdminRecordingsPage.jsx
---

# Programar gravação

Admin escolhe emissora, dia, início e fim em `/admin/gravacoes`; o sistema grava
esse trecho do stream e guarda o áudio por 15 dias. Vale para qualquer emissora
com `stream_url`, com ou sem campanha.

Existe porque o sistema só guarda áudio contínuo por 60 minutos: uma entrevista
paga que deu problema não tinha prova. **Não recupera nada do passado.**

Spec: [2026-11-05-scheduled-recording-design.md](../superpowers/specs/2026-11-05-scheduled-recording-design.md)

## Como funciona

1. A cada 15s o `Manager` passa a `gravando` o que começa em até 20s. O pré-roll
   compensa o tick e a conexão do ffmpeg.
2. Um ffmpeg dedicado grava pedaços de 1 minuto em
   `/data/segments/_recordings/<id>/`. A extensão é `.adts` de propósito: o
   `segments-cleanup` apaga `*.aac` com mais de 60min.
3. No fim da janela, os pedaços viram um `.m4a` que sobe para
   `recordings/<ano>/<mês>/<id>.m4a` no bucket de evidência.
4. Os pedaços só são apagados depois de o banco confirmar. Qualquer falha grava o
   motivo em `last_error`, que a tela mostra, e repete a cada 2 minutos, sem perder
   áudio.
5. Um job de hora em hora apaga o áudio vencido e marca `expirada`.

| Status | Significado |
|---|---|
| `programada` | Aguardando o horário |
| `gravando` | Dentro da janela |
| `processando` | Juntando e subindo o arquivo |
| `concluida` | Sem buraco de 30s ou mais |
| `parcial` | Com buraco de 30s ou mais; a tela lista os intervalos |
| `falhou` | Nenhum áudio |
| `cancelada` | Cancelada; o que já tinha sido gravado fica |
| `expirada` | Passou dos 15 dias; áudio apagado |

## O que não é óbvio

- **Buraco não vira silêncio.** Depois de um buraco, o tempo do player deixa de
  bater com o relógio. A prova do que foi ao ar em qual horário é a lista `gaps`.
- **Cada pedaço é aparado no último frame ADTS completo.** Um pedaço cortado no meio
  de um frame (kill -9, crash da API) faria o remux parar ali com exit 0 e perder o
  resto. Por isso o `.m4a` é conferido pela contagem de frames antes do upload.
- **O `segments-cleanup` apaga pasta vazia.** Com o stream fora a pasta da gravação
  fica vazia e some. Por isso o gravador faz `MkdirAll` a cada tentativa.
- **`stream_url` é relida a cada tentativa.** Corrigir a URL durante uma queda vale
  na reconexão seguinte; o e-mail de aviso diz isso.
- **O `/audio` usa `context.WithoutCancel`.** O `middleware.Timeout` de 60s do router
  cancelaria a leitura do S3 no meio de um download grande.
- **O watchdog mede bytes em disco**, não o processo: ffmpeg pode ficar vivo sem
  escrever.
- **Segunda conexão com o stream:** emissora com campanha ativa fica com 2 conexões
  do mesmo IP durante a gravação. Risco aceito; alguns painéis já baniram o IP da
  VM em 2026-06.

## Deploy

A captura roda dentro da API. O `scripts/deploy.sh` aborta se houver gravação em
andamento ou começando em menos de 10 minutos. Bypass:
`SKIP_RECORDING_GUARD=yes`, que corta o áudio enquanto a API reinicia. Depois de
um reinício a gravação retoma sozinha e o trecho perdido entra em `gaps`.

`processando` não bloqueia o deploy: a finalização é repetível e retoma sozinha
depois do reinício. Bloquear travaria todo deploy se uma finalização falhasse para
sempre.

## Tamanho

~56 MB por hora. O limite de 6h dá ~340 MB por gravação.

## Fora do escopo

Repetição, vínculo com cliente ou campanha, acesso de cliente ou operator,
prorrogar os 15 dias, tools no MCP. A gravação não altera `monitoring_status`, não
entra em falhas nem uptime e não gera detecção.
```

- [ ] **Step 2: Atualizar o mapa do `CLAUDE.md`**

Depois da linha de `/admin/station-failures`:

```markdown
| **Programar gravação** (`/admin/gravacoes`, só admin; escolhe emissora + dia + início/fim, grava o stream com ffmpeg próprio e guarda 15 dias) | [docs/features/scheduled-recording.md](docs/features/scheduled-recording.md) — pacote `recording` é isolado da detecção; pedaços em `/data/segments/_recordings/` com extensão `.adts` para o limpador de 60min não apagar; **o `deploy.sh` aborta com gravação em andamento** (`SKIP_RECORDING_GUARD=yes`); buraco não vira silêncio, a lista `gaps` é o que mantém a prova |
```

- [ ] **Step 3: Atualizar `docs/README.md`**

Na tabela de `features/`:

```markdown
| [scheduled-recording.md](features/scheduled-recording.md) | Programar gravação: admin agenda um trecho de stream de qualquer emissora; áudio fica 15 dias. `deploy.sh` para se houver gravação em andamento |
```

- [ ] **Step 4: Atualizar `docs/operations/deploy.md`**

Achar a seção que descreve o `deploy.sh` (`grep -n "deploy.sh" docs/operations/deploy.md`) e acrescentar:

```markdown
**Gravação em andamento:** o `deploy.sh` aborta se houver gravação programada em
andamento ou começando em menos de 10 minutos, porque recriar a API corta o áudio.
Espere terminar ou cancele em `/admin/gravacoes`. Bypass de emergência:
`SKIP_RECORDING_GUARD=yes ./scripts/deploy.sh`. Ver
[scheduled-recording.md](../features/scheduled-recording.md).
```

- [ ] **Step 5: Commit**

```bash
git add docs/features/scheduled-recording.md CLAUDE.md docs/README.md docs/operations/deploy.md
git commit -m "docs(gravacao): documentacao da feature"
```

---

## Task 15: Teste ponta a ponta local

Sem código novo. Prova o que os fakes não provam: ffmpeg de verdade, stream de verdade, reinício de verdade.

**Files:** nenhum, salvo correção do que aparecer.

- [ ] **Step 1: Subir o ambiente**

Ler `docs/operations/simulacao-radio.md` e subir o simulador **na variante Icecast**: o sim original (`-listen 1`) aceita uma conexão só e o ffprobe consome essa conexão. Nos comandos docker: `MSYS_NO_PATHCONV=1`.

Subir a API **no Docker**, não nativa no Windows: o NTFS só atualiza o tamanho na listagem da pasta quando o arquivo fecha, e o watchdog veria um ffmpeg saudável como travado. Em Linux, o caso de produção, o tamanho é imediato. Subir também Postgres e MinIO, apontar o SMTP para o Mailpit (o container `platform-mailpit-1` já roda) e criar uma emissora com `stream_url` do simulador.

- [ ] **Step 2: Gravação normal (3 minutos)**

Pela tela, programar 3 minutos a partir do próximo minuto. Conferir:
- Status: `programada` → `gravando` (20s antes) → `processando` → `concluida`.
- O `.m4a` baixado abre, dura ~3 minutos e permite pular trechos.
- Nenhum e-mail no Mailpit.
- `_recordings/<id>/` foi apagada.

- [ ] **Step 3: Stream derrubado no meio (5 minutos)**

Aos 2 minutos, parar o simulador. Aguardar 2 minutos e subir de novo. Conferir:
- E-mail "captura parou" em ~60s, **um só**.
- A gravação reconecta sozinha.
- Termina `parcial`, com o intervalo certo na tela e no e-mail final.

- [ ] **Step 4: API reiniciada no meio (5 minutos)**

Aos 2 minutos, reiniciar a API. Conferir: volta para `gravando` sem ação manual, o buraco cobre só o tempo fora, e o áudio anterior foi preservado.

- [ ] **Step 5: Stream que nunca sobe (3 minutos)**

Programar com o simulador desligado. Conferir: e-mail "não conectou" em ~60s; pasta pode sumir e voltar sem quebrar nada; final `falhou` com o motivo na tela; e-mail final.

- [ ] **Step 6: Cancelar**

Parar uma gravação no meio pelo botão. Conferir: status final `cancelada`, com áudio disponível, sem e-mail. Cancelar uma `programada`: some da fila sem áudio.

- [ ] **Step 7: Expiração**

Forçar `UPDATE recordings SET expires_at = now() - interval '1 hour' WHERE id = '<id>'` no banco local e reiniciar a API. Conferir: objeto apagado do MinIO, status `expirada`, botões de áudio somem.

- [ ] **Step 8: Registrar**

Anotar o resultado de cada cenário no relatório ao Dereck. Bug achado aqui vira teste primeiro, depois correção, em commit próprio.

---

## Task 16: Verificação antes de entregar

- [ ] **Step 1: Cross-compile como o Dockerfile (regra 6.1)**

Run: `cd workers && CGO_ENABLED=0 GOOS=linux go build ./...`
Expected: sem saída.

- [ ] **Step 2: Imagem completa, se o Docker estiver de pé**

Run: `docker build -f infra/docker/Dockerfiles/workers.Dockerfile -t rc-verify .`
Expected: build ok. Nenhum CLI novo, então o Dockerfile não muda (regra 6.7).

- [ ] **Step 3: Testes do que foi tocado**

Run:

```bash
cd workers
go test ./internal/recording/ ./internal/ingestor/ ./internal/api/
TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55433/radiocheck_test?sslmode=disable" go test -p 1 ./internal/recording/
go test ./internal/api/handlers/ -run TestRecordings
```

Expected: PASS. Falhas em `internal/api/handlers` fora de `TestRecordings` com o banco de teste apodrecido são ambientais: confirmar no master antes de atribuir a esta branch.

- [ ] **Step 4: Testes do frontend**

Run: `cd frontend && node --test src/utils/recordings.test.mjs && npx vite build`
Expected: PASS.

- [ ] **Step 5: Lockfile intacto (regra 5.4)**

Run: `git diff master --stat -- frontend/package.json frontend/package-lock.json`
Expected: sem saída.

- [ ] **Step 6: Boot da API (regra 6.5)**

Subir a API local com o banco migrado e conferir `/v1/internal/health`. Sem métrica nova, sem map nil: `NewManager` inicializa `live` e `finalizing`.

- [ ] **Step 7: Migration contra dados de prod (regra 4.8)**

Risco baixo por ser tabela nova, mas a FK valida contra `stations` e `users`. Seguir `docs/operations/migrations.md` §Testar migration contra dados de prod, se houver dump recente; o `shadow_migration_test` do deploy repete isso de qualquer forma.

- [ ] **Step 8: Requisitar revisão**

Usar `superpowers:requesting-code-review` na branch inteira contra a spec e, depois de resolver os achados, `superpowers:finishing-a-development-branch`.

**Rollout (decisão do Dereck, nada automático):** dois pushes. Backend primeiro com `deploy.sh`, smoke test (`401` em `/v1/internal/recordings`, não `404`), depois o frontend, porque o CF Pages publica sozinho a cada push em `master`. Primeira gravação real: 5 minutos numa emissora conhecida, ouvida até o fim antes de confiar numa entrevista paga.
