package ingestor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/events"
	"radiocheck/internal/index"
	"radiocheck/internal/match"
	"radiocheck/internal/metrics"
)

// covIHasSeq diz se `args` contém a sequência contígua `seq`.
func covIHasSeq(args []string, seq ...string) bool {
	for i := 0; i+len(seq) <= len(args); i++ {
		ok := true
		for j := range seq {
			if args[i+j] != seq[j] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// ── ProbeAudioCodec ─────────────────────────────────────────────────────────

func TestCovI_ProbeAudioCodec_ParsesCodecAndPassesExpectedArgs(t *testing.T) {
	argsDir := covIUseFakeTools(t)
	t.Setenv("COV_I_FFPROBE_OUT", `{"streams":[{"codec_name":"AAC"}]}`)

	codec, err := ProbeAudioCodec(context.Background(), "http://stream.example.com/live")

	require.NoError(t, err)
	require.Equal(t, "aac", codec, "o codec deve vir em minúsculas")
	args := covIReadArgs(t, argsDir, "ffprobe")
	require.True(t, covIHasSeq(args, "-select_streams", "a:0"), "só o primeiro stream de áudio: %v", args)
	require.True(t, covIHasSeq(args, "-print_format", "json"), "%v", args)
	require.True(t, covIHasSeq(args, "-user_agent", "VLC/3.0.20 LibVLC/3.0.20"), "mesmo User-Agent do ffmpeg: %v", args)
	require.Equal(t, "http://stream.example.com/live", args[len(args)-1], "a URL é o último argumento")
}

func TestCovI_ProbeAudioCodec_NoAudioStream_ReturnsEmptyWithoutError(t *testing.T) {
	covIUseFakeTools(t)
	t.Setenv("COV_I_FFPROBE_OUT", `{"streams":[]}`)

	codec, err := ProbeAudioCodec(context.Background(), "http://stream.example.com/live")

	require.NoError(t, err)
	require.Equal(t, "", codec)
}

func TestCovI_ProbeAudioCodec_Failures(t *testing.T) {
	t.Run("saida que nao e JSON vira erro de parse", func(t *testing.T) {
		covIUseFakeTools(t)
		t.Setenv("COV_I_FFPROBE_OUT", "isto nao e json")
		codec, err := ProbeAudioCodec(context.Background(), "http://stream.example.com/live")
		require.Error(t, err)
		require.Contains(t, err.Error(), "ffprobe parse")
		require.Equal(t, "", codec)
	})
	t.Run("ffprobe com exit code != 0 vira erro de execucao", func(t *testing.T) {
		covIUseFakeTools(t)
		t.Setenv("COV_I_FFPROBE_EXIT", "1")
		_, err := ProbeAudioCodec(context.Background(), "http://stream.example.com/live")
		require.Error(t, err)
		require.Contains(t, err.Error(), "ffprobe run")
		var exitErr *exec.ExitError
		require.True(t, errors.As(err, &exitErr), "o erro deve embrulhar o ExitError: %v", err)
	})
	t.Run("ffprobe ausente do PATH vira erro de execucao", func(t *testing.T) {
		covINoTools(t)
		_, err := ProbeAudioCodec(context.Background(), "http://stream.example.com/live")
		require.Error(t, err)
		require.Contains(t, err.Error(), "ffprobe run")
	})
}

// ── StartFFmpeg ─────────────────────────────────────────────────────────────

func TestCovI_StartFFmpeg_RequiresSegmentsPattern(t *testing.T) {
	proc, err := StartFFmpeg(context.Background(), "http://stream.example.com/live", "", zap.NewNop())
	require.Error(t, err)
	require.Nil(t, proc)
	require.Contains(t, err.Error(), "segmentsOutputPattern is required")
}

func TestCovI_StartFFmpeg_BinaryMissing_ReturnsStartError(t *testing.T) {
	covINoTools(t)
	log, logs := covIObservedLogger()

	proc, err := StartFFmpeg(context.Background(), "http://stream.example.com/live", "/tmp/seg/%Y.aac", log)

	require.Error(t, err)
	require.Nil(t, proc)
	require.Contains(t, err.Error(), "ffmpeg start")
	require.Equal(t, 1, logs.FilterMessage("ffmpeg: codec probe failed; falling back to AAC re-encode").Len(),
		"falha do ffprobe é não-fatal: loga e segue pro re-encode")
}

// O ffmpeg grava o PCM em pipe:3 (ExtraFiles), que o Windows não suporta —
// os testes com o processo "vivo" só fazem sentido em Linux/macOS (= CI e prod).
func covISkipIfNoExtraFiles(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("ExtraFiles (pipe:3) não é suportado no Windows; coberto no CI Linux")
	}
}

func TestCovI_StartFFmpeg_BuildsCommandAndStreamsPCM(t *testing.T) {
	covISkipIfNoExtraFiles(t)
	cases := []struct {
		name      string
		probeOut  string
		url       string
		wantCodec []string
		wantAtEOF bool
	}{
		{"aac e copiado", `{"streams":[{"codec_name":"aac"}]}`, "http://stream.example.com/live", []string{"-c:a", "copy"}, true},
		{"mp3 e reencodado", `{"streams":[{"codec_name":"mp3"}]}`, "http://stream.example.com/live.mp3", []string{"-c:a", "aac", "-b:a", "128k"}, true},
		{"HLS nao usa reconnect_at_eof", `{"streams":[{"codec_name":"aac"}]}`, "http://stream.example.com/live.m3u8", []string{"-c:a", "copy"}, false},
		{"codec desconhecido reencoda", `{"streams":[]}`, "http://stream.example.com/x", []string{"-c:a", "aac", "-b:a", "128k"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			argsDir := covIUseFakeTools(t)
			t.Setenv("COV_I_FFPROBE_OUT", c.probeOut)
			t.Setenv("COV_I_FFMPEG_PCM_BYTES", "1000")
			const pattern = "/tmp/seg/%Y%m%d-%H%M%S.aac"

			proc, err := StartFFmpeg(context.Background(), c.url, pattern, zap.NewNop())
			require.NoError(t, err)
			buf := make([]byte, 2000)
			n, _ := readAll(proc.PCMReader(), buf)
			require.NoError(t, proc.Wait())

			require.Equal(t, 1000, n, "todo o PCM escrito no pipe:3 chega ao leitor")
			args := covIReadArgs(t, argsDir, "ffmpeg")
			require.Equal(t, "-y", args[0])
			require.True(t, covIHasSeq(args, "-i", c.url), "%v", args)
			require.True(t, covIHasSeq(args, "-timeout", "10000000"), "%v", args)
			require.True(t, covIHasSeq(args, append([]string{"-map", "0:a:0"}, c.wantCodec...)...),
				"codec args %v ausentes em %v", c.wantCodec, args)
			require.True(t, covIHasSeq(args, "-f", "segment", "-segment_time", "30", "-segment_format", "adts"), "%v", args)
			require.True(t, covIHasSeq(args, "-strftime", "1", pattern), "o padrão de segmentos vai pro muxer: %v", args)
			require.True(t, covIHasSeq(args, "-ar", "16000", "-ac", "1", "-f", "f32le", "pipe:3"), "%v", args)
			require.Equal(t, c.wantAtEOF, covIHasSeq(args, "-reconnect_at_eof", "1"), "%v", args)
			require.True(t, covIHasSeq(args, "-reconnect", "1", "-reconnect_streamed", "1", "-reconnect_delay_max", "5"), "%v", args)
		})
	}
}

// readAll lê até EOF (ou encher buf) e devolve quantos bytes vieram.
func readAll(r interface{ Read([]byte) (int, error) }, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := r.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func TestCovI_FFmpegProcess_WaitReportsExitCode(t *testing.T) {
	covISkipIfNoExtraFiles(t)
	covIUseFakeTools(t)
	t.Setenv("COV_I_FFMPEG_EXIT", "3")

	proc, err := StartFFmpeg(context.Background(), "http://stream.example.com/live", "/tmp/seg/%Y.aac", zap.NewNop())
	require.NoError(t, err)

	werr := proc.Wait()

	var exitErr *exec.ExitError
	require.True(t, errors.As(werr, &exitErr), "esperava ExitError, veio %v", werr)
	require.Equal(t, 3, exitErr.ExitCode())
	proc.Stop() // idempotente depois do Wait: não pode travar nem entrar em pânico
}

func TestCovI_FFmpegProcess_StopKillsHungProcess(t *testing.T) {
	covISkipIfNoExtraFiles(t)
	covIUseFakeTools(t)
	t.Setenv("COV_I_FFMPEG_HANG", "1")

	proc, err := StartFFmpeg(context.Background(), "http://stream.example.com/live", "/tmp/seg/%Y.aac", zap.NewNop())
	require.NoError(t, err)

	done := make(chan struct{})
	go func() { proc.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("Stop não matou o ffmpeg pendurado")
	}
}

// ── Worker.Run ──────────────────────────────────────────────────────────────

func TestCovI_Run_FFmpegStartFailure_SignalsDownAndCountsReconnect(t *testing.T) {
	covINoTools(t)
	station := uuid.New()
	var down atomic.Int32
	w := NewWorker(WorkerConfig{
		StationID:             station,
		StreamURL:             "http://stream.example.com/live",
		SegmentsOutputPattern: "/tmp/seg/%Y.aac",
		OnStreamDown:          func() { down.Add(1) },
	}, index.New(), nil, zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); w.Run(ctx) }()

	require.Eventually(t, func() bool { return w.Reconnects() >= 1 }, 10*time.Second, 5*time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run não terminou após cancelar o contexto durante o backoff")
	}

	require.EqualValues(t, 1, w.Reconnects(), "uma única tentativa falha antes do cancel")
	require.EqualValues(t, 1, down.Load(), "OnStreamDown dispara na primeira falha da queda")
	require.Equal(t, float64(1), testutil.ToFloat64(metrics.WorkerReconnectsTotal.WithLabelValues(station.String())))
	require.EqualValues(t, 0, w.BytesReceived())
}

func TestCovI_Run_StreamsPCMThenReconnectsAfterEOF(t *testing.T) {
	covISkipIfNoExtraFiles(t)
	covIUseFakeTools(t)
	const pcmBytes = 16 * 4096 * 4 // 16 blocos = 2 ticks de 2s
	t.Setenv("COV_I_FFMPEG_PCM_BYTES", "262144")
	station := uuid.New()
	var up, down, hb atomic.Int32
	w := NewWorker(WorkerConfig{
		StationID:             station,
		StreamURL:             "http://stream.example.com/live",
		SegmentsOutputPattern: "/tmp/seg/%Y.aac",
		OnStreamUp:            func() { up.Add(1) },
		OnStreamDown:          func() { down.Add(1) },
		HeartbeatFn:           func() { hb.Add(1) },
	}, index.New(), nil, zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); w.Run(ctx) }()

	require.Eventually(t, func() bool { return down.Load() >= 1 }, 30*time.Second, 10*time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run não terminou")
	}

	require.EqualValues(t, pcmBytes, w.BytesReceived())
	require.EqualValues(t, 1, up.Load(), "OnStreamUp uma vez por conexão, ~2s de áudio depois")
	require.EqualValues(t, 1, hb.Load(), "heartbeat imediato no primeiro tick")
	require.False(t, w.LastPCMAt().IsZero())
	require.EqualValues(t, 1, w.Reconnects(), "EOF do ffmpeg conta como reconexão")
}

// ── runPCMReader ────────────────────────────────────────────────────────────

type covIReaderCalls struct {
	up, hb int
	noise  []int
}

func covINewReaderWorker(station uuid.UUID, store *index.Store, calls *covIReaderCalls) *Worker {
	return NewWorker(WorkerConfig{
		StationID:        station,
		StreamURL:        "http://stream.example.com/live",
		MatchThreshold:   NewMatchThreshold(5),
		MinScoreCoverage: 0,
		OnStreamUp:       func() { calls.up++ },
		HeartbeatFn:      func() { calls.hb++ },
		OnNoiseSample:    func(score int) { calls.noise = append(calls.noise, score) },
	}, store, nil, zap.NewNop())
}

// 200 blocos de 4096 amostras = 25 ticks de 2s (32000 amostras). O primeiro
// tick só tem 32768 amostras no anel (< janela de 64000), então 24 janelas são
// analisadas; a cada 5 janelas sai uma amostra de ruído (4 no total) e o
// heartbeat dispara no tick 1 (stream up) e no tick 15.
func TestCovI_RunPCMReader_CountsBytesTicksHeartbeatsAndNoiseSamples(t *testing.T) {
	station := uuid.New()
	calls := &covIReaderCalls{}
	w := covINewReaderWorker(station, index.New(), calls)
	samples := covINoise(1, 200*4096)
	payload := covIPCMBytes(samples)

	w.runPCMReader(bytes.NewReader(payload), covINewRing(), map[int32]*match.StateMachine{}, station.String())

	require.EqualValues(t, len(payload), w.BytesReceived())
	require.Equal(t, float64(len(payload)), testutil.ToFloat64(metrics.WorkerBytesTotal.WithLabelValues(station.String())))
	require.False(t, w.LastPCMAt().IsZero())
	require.Equal(t, 1, calls.up, "OnStreamUp só no primeiro tick")
	require.Equal(t, 2, calls.hb, "heartbeat no tick de stream-up e no 15º tick")
	require.Equal(t, []int{0, 0, 0, 0}, calls.noise, "índice vazio: o maior pico de cada amostra é 0")
}

// Um último bloco parcial é descartado (io.ReadFull → ErrUnexpectedEOF) e um
// stream vazio simplesmente retorna, sem callbacks.
func TestCovI_RunPCMReader_PartialTrailingBlockIsDropped(t *testing.T) {
	station := uuid.New()
	calls := &covIReaderCalls{}
	w := covINewReaderWorker(station, index.New(), calls)
	payload := covIPCMBytes(covINoise(2, 3*4096+100)) // 3 blocos + 100 amostras

	w.runPCMReader(bytes.NewReader(payload), covINewRing(), nil, station.String())
	require.EqualValues(t, 3*4096*4, w.BytesReceived())

	w2 := covINewReaderWorker(uuid.New(), index.New(), calls)
	w2.runPCMReader(bytes.NewReader(nil), covINewRing(), nil, "empty")
	require.EqualValues(t, 0, w2.BytesReceived())
	require.True(t, w2.LastPCMAt().IsZero())
	require.Zero(t, calls.up)
}

// Erro de leitura que não é EOF encerra o leitor (e o worker reconecta).
func TestCovI_RunPCMReader_ReadErrorStopsReader(t *testing.T) {
	calls := &covIReaderCalls{}
	w := covINewReaderWorker(uuid.New(), index.New(), calls)
	boom := errors.New("conexão resetada")
	r := &covIFailingReader{data: covIPCMBytes(covINoise(3, 4096)), err: boom}

	w.runPCMReader(r, covINewRing(), nil, "read-error")

	require.EqualValues(t, 4096*4, w.BytesReceived(), "o bloco lido antes do erro foi contabilizado")
}

type covIFailingReader struct {
	data []byte
	err  error
}

func (r *covIFailingReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

// Ponta a ponta: um material de ~8s indexado, tocando no meio do stream, é
// confirmado pela janela única (material <10s), vira UM evento em
// detections.pending (o cooldown segura as janelas seguintes) e o payload
// carrega a janela de evidência calculada pelo worker.
func TestCovI_RunPCMReader_ConfirmsShortMaterialAndPublishesPending(t *testing.T) {
	const shortID = int32(42)
	clip := covISynthClip(7, 8.0)
	store := covIStoreFromClip(clip, shortID)

	stream := append([]float32{}, covINoise(11, 6*16000)...)
	stream = append(stream, clip...)
	stream = append(stream, covINoise(12, 6*16000)...)
	payload := covIPCMBytes(covIPadToChunks(stream))

	srv := covIStartNATS(t)
	nc := covIConnect(t, srv)
	station := uuid.New()
	totalFrames := 62 // 8s * 16000 / 2048 (arredondado p/ baixo): < 10s => elegível à janela única
	w := NewWorker(WorkerConfig{
		StationID:        station,
		StreamURL:        "http://stream.example.com/live",
		CommercialFrames: map[int32]int{shortID: totalFrames},
		MatchThreshold:   NewMatchThreshold(5),
		MinScoreCoverage: 0,
	}, store, nc, zap.NewNop())
	sm := match.NewStateMachine(station.String(), shortID, totalFrames, 5, 0.15, 30*time.Second, 13*time.Second, zap.NewNop())
	sm.EnableShortSingleWindow(1.0)

	w.runPCMReader(bytes.NewReader(payload), covINewRing(), map[int32]*match.StateMachine{shortID: sm}, station.String())
	require.NoError(t, nc.Flush())

	msgs := srv.Messages(events.SubjectDetectionPending)
	require.Len(t, msgs, 1, "uma tocada = um evento; o cooldown descarta as janelas seguintes")
	var ev DetectionEvent
	require.NoError(t, json.Unmarshal(msgs[0].Data, &ev))
	require.Equal(t, station.String(), ev.StationID)
	require.Equal(t, shortID, ev.CommercialShortID)
	require.Greater(t, ev.HashCount, int32(0))
	require.InDelta(t, 32.0/62.0, ev.Confidence, 1e-9, "janela única: cobertura = 32 frames da janela / frames do material")
	require.Equal(t, ev.Confidence, ev.TemporalCoverage)
	require.Equal(t, ev.MatchStartOffsetMs, ev.MatchEndOffsetMs, "confirmou na própria janela que iniciou a detecção")
	detected, err := time.Parse(time.RFC3339, ev.DetectedAt)
	require.NoError(t, err)
	evStart, err := time.Parse(time.RFC3339, ev.EvidenceWindowStart)
	require.NoError(t, err)
	evEnd, err := time.Parse(time.RFC3339, ev.EvidenceWindowEnd)
	require.NoError(t, err)
	require.Equal(t, 64*time.Second, detected.Sub(evStart), "evidência começa 4s (janela) + 60s antes da detecção")
	require.Contains(t, []time.Duration{63 * time.Second, 64 * time.Second}, evEnd.Sub(detected),
		"evidência termina ~63,9s depois (duração do material + 60s)")
}

// ── publishDetection ────────────────────────────────────────────────────────

func TestCovI_PublishDetection_BuildsEventWithEvidenceWindow(t *testing.T) {
	srv := covIStartNATS(t)
	nc := covIConnect(t, srv)
	station := uuid.New()
	w := NewWorker(WorkerConfig{
		StationID:        station,
		CommercialFrames: map[int32]int{77: 234}, // 234 frames = 29,952s
	}, index.New(), nc, zap.NewNop())
	first := time.Date(2026, 5, 7, 10, 0, 10, 500_000_000, time.UTC)
	det := &match.ConfirmedDetection{
		CommercialShortID: 77,
		StationID:         station.String(),
		DetectedAt:        first.Add(9 * time.Second),
		FirstMatchAt:      first,
		OffsetFrames:      40,
		FirstOffsetFrames: 3,
		Confidence:        0.8125,
		TemporalCoverage:  0.8125,
		HashCount:         123,
		VariantID:         2,
		RateID:            1,
	}

	w.publishDetection(context.Background(), det, station.String())
	require.NoError(t, nc.Flush())

	msgs := srv.Messages(events.SubjectDetectionPending)
	require.Len(t, msgs, 1)
	var ev DetectionEvent
	require.NoError(t, json.Unmarshal(msgs[0].Data, &ev))
	require.Equal(t, DetectionEvent{
		StationID:           station.String(),
		CommercialShortID:   77,
		DetectedAt:          "2026-05-07T10:00:19Z",
		OffsetFrames:        40,
		Confidence:          0.8125,
		EvidenceWindowStart: "2026-05-07T09:59:06Z", // 10:00:10,5 - 4s (janela) - 60s
		EvidenceWindowEnd:   "2026-05-07T10:01:36Z", // 10:00:06,5 + 29,952s + 60s
		HashCount:           123,
		TemporalCoverage:    0.8125,
		MatchStartOffsetMs:  3 * 128,
		MatchEndOffsetMs:    40 * 128,
		VariantUsed:         2,
		RateUsed:            1,
	}, ev)
}

func TestCovI_PublishDetection_NilConnectionIsLoggedNotFatal(t *testing.T) {
	log, logs := covIObservedLogger()
	station := uuid.New()
	w := NewWorker(WorkerConfig{StationID: station, CommercialFrames: map[int32]int{5: 10}}, index.New(), nil, log)

	require.NotPanics(t, func() {
		w.publishDetection(context.Background(),
			&match.ConfirmedDetection{CommercialShortID: 5, StationID: station.String(), DetectedAt: time.Now(), FirstMatchAt: time.Now()},
			station.String())
	})
	require.Equal(t, 1, logs.FilterMessage("nats publish failed").Len())
}

// ── acessores ───────────────────────────────────────────────────────────────

func TestCovI_CommercialShortIDs_ReturnsDefensiveCopy(t *testing.T) {
	w := NewWorker(WorkerConfig{CommercialShortIDs: []int32{3, 1, 2}}, nil, nil, nil)

	got := w.CommercialShortIDs()
	require.Equal(t, []int32{3, 1, 2}, got)
	got[0] = 99
	require.Equal(t, []int32{3, 1, 2}, w.CommercialShortIDs(), "mutar a cópia não pode alterar a config do worker")

	require.Nil(t, NewWorker(WorkerConfig{}, nil, nil, nil).CommercialShortIDs())
}

func TestCovI_ReconnectArgs_MatchesInternalDecision(t *testing.T) {
	require.Equal(t, pickReconnectArgs("http://stream.example.com/a.mp3"), ReconnectArgs("http://stream.example.com/a.mp3"))
	require.NotContains(t, ReconnectArgs("http://stream.example.com/a.m3u8"), "-reconnect_at_eof")
}

// Cancelar o contexto com o ffmpeg ainda rodando: o processo é morto, o leitor
// de PCM vê EOF e Run retorna SEM sinalizar queda nem contar reconexão (é
// parada pedida, não falha do stream).
func TestCovI_Run_ContextCancelWhileStreaming_ReturnsWithoutDownOrReconnect(t *testing.T) {
	covISkipIfNoExtraFiles(t)
	argsDir := covIUseFakeTools(t)
	t.Setenv("COV_I_FFMPEG_HANG", "1")
	var down atomic.Int32
	w := NewWorker(WorkerConfig{
		StationID:             uuid.New(),
		StreamURL:             "http://stream.example.com/live",
		SegmentsOutputPattern: "/tmp/seg/%Y.aac",
		OnStreamDown:          func() { down.Add(1) },
	}, index.New(), nil, zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); w.Run(ctx) }()
	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(argsDir, "ffmpeg.args.json"))
		return err == nil
	}, 30*time.Second, 10*time.Millisecond, "o ffmpeg falso deveria ter sido iniciado")

	cancel()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Run não retornou após cancelar o contexto com o ffmpeg rodando")
	}

	require.EqualValues(t, 0, down.Load(), "parada pedida não é queda do stream")
	require.EqualValues(t, 0, w.Reconnects())
}
