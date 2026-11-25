package audit

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/match"
	"radiocheck/pkg/audio"
)

func TestCovZ_PCMToHashes(t *testing.T) {
	pcm := covZSignal(1, 6)
	hashes := PCMToHashes(pcm)
	require.Greater(t, len(hashes), 100, "o sinal sintetico precisa gerar hashes, senao nada abaixo prova algo")

	// Determinismo.
	assert.Equal(t, hashes, PCMToHashes(pcm))

	// A cadeia e high-pass -> normalizacao RMS -> STFT -> picos -> hashes.
	want := audio.GenerateHashes(audio.PickPeaks(audio.STFT(audio.NormalizeRMS(audio.ApplyHighPass(pcm, 100.0, 16000), -20.0))))
	assert.Equal(t, want, hashes)

	// Entrada vazia ou curta demais para uma janela: nenhum hash, sem panico.
	assert.Empty(t, PCMToHashes(nil))
	assert.Empty(t, PCMToHashes(make([]float32, 100)))

	// Sinais diferentes geram impressoes diferentes.
	assert.NotEqual(t, hashes, PCMToHashes(covZSignal(2, 6)))

	// A normalizacao RMS torna o resultado (quase) independente do ganho:
	// um clipe 3x mais baixo mantem a imensa maioria dos hashes.
	quiet := make([]float32, len(pcm))
	for i, s := range pcm {
		quiet[i] = s / 3
	}
	set := map[audio.Hash]bool{}
	for _, h := range hashes {
		set[h] = true
	}
	common := 0
	for _, h := range PCMToHashes(quiet) {
		if set[h] {
			common++
		}
	}
	assert.Greater(t, float64(common)/float64(len(hashes)), 0.9, "ganho nao pode mudar a impressao")
}

func TestCovZ_MatchHashes_SameAudioPasses(t *testing.T) {
	master := PCMToHashes(covZSignal(10, 12))
	require.NotEmpty(t, master)

	res := MatchHashes(master, master)
	assert.True(t, res.Passed)
	assert.Equal(t, len(master), res.QueryHashes)
	assert.Equal(t, len(master), res.MasterHashes)
	assert.GreaterOrEqual(t, res.Score, len(master)/2)
	assert.InDelta(t, 1.0, res.Coverage, 0.05, "o mesmo audio cobre o master inteiro")
	assert.InDelta(t, 1.0, res.MatchExtent, 0.05, "e chega ate o fim dele")
	assert.Equal(t, 0, res.DeltaBin, "sem deslocamento")
	assert.Equal(t, uint8(0), res.VariantID)
	assert.NotEmpty(t, res.CoveredFrames)
}

func TestCovZ_MatchHashes_UnrelatedAudioFails(t *testing.T) {
	master := PCMToHashes(covZSignal(10, 12))
	other := PCMToHashes(covZSignal(99, 12))
	require.NotEmpty(t, other)

	res := MatchHashes(other, master)
	assert.False(t, res.Passed, "audio sem relacao nao pode passar: score=%d cov=%.3f", res.Score, res.Coverage)
	assert.Less(t, res.Score, DefaultMinScore*3)
}

func TestCovZ_MatchHashes_EmptyQuery(t *testing.T) {
	master := PCMToHashes(covZSignal(10, 6))
	res := MatchHashes(nil, master)
	assert.False(t, res.Passed)
	assert.Zero(t, res.Score)
	assert.Equal(t, 0, res.QueryHashes)
	assert.Equal(t, len(master), res.MasterHashes)
}

func TestCovZ_MatchHashes_ShiftedClipFindsTheOffset(t *testing.T) {
	full := covZSignal(10, 14)
	master := PCMToHashes(full)

	// Corta exatamente k saltos do STFT do comeco: o clipe e o master deslocado.
	const k = 8 // 8 frames = ~1,0s
	clip := full[k*audio.HopSize:]
	res := MatchHashes(PCMToHashes(clip), master)

	require.True(t, res.Passed, "score=%d cov=%.3f", res.Score, res.Coverage)
	// delta = frame_master - frame_clip = +k => bin k/DeltaBinSize.
	assert.InDelta(t, k/match.DeltaBinSize, res.DeltaBin, 1)
	assert.Greater(t, res.Coverage, 0.7, "so os k primeiros frames do master ficam de fora")
}

func TestCovZ_MatchHashes_FirstHalfOnlyHasHalfExtent(t *testing.T) {
	full := covZSignal(10, 16)
	master := PCMToHashes(full)
	half := PCMToHashes(full[:len(full)/2])

	res := MatchHashes(half, master)
	require.True(t, res.Passed)
	// Um corte de 15s de um master de 30s so alcanca a primeira metade (sinal de desambiguacao).
	assert.InDelta(t, 0.5, res.MatchExtent, 0.12)
	assert.Less(t, res.Coverage, 0.65)
	assert.Greater(t, res.Coverage, 0.3)
}

func TestCovZ_Decode_UsesStdinAndFormatFlag(t *testing.T) {
	pcm := covZSignal(3, 1)
	payload := []byte("bytes-aac-de-teste-0123456789")

	cases := []struct {
		name    string
		decode  func([]byte) ([]float32, error)
		wantFmt string
	}{
		{"adts", DecodeADTSToPCM, "aac"},
		{"m4a", DecodeM4AToPCM, "mp4"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ff := covZFakeFFmpegEnv(t, "ok", covZWritePCM(t, pcm))
			got, err := c.decode(payload)
			require.NoError(t, err)
			assert.Equal(t, pcm, got)

			assert.Equal(t, len(payload), ff.stdinLen(t), "o audio vai pelo stdin, sem arquivo temporario")
			args := ff.args(t)
			joined := " " + covZJoin(args) + " "
			assert.Contains(t, joined, " -f "+c.wantFmt+" -i pipe:0 ")
			assert.Contains(t, joined, " -ac 1 -ar 16000 ")
			assert.Contains(t, joined, " -f f32le -c:a pcm_f32le pipe:1")
		})
	}
}

func covZJoin(a []string) string {
	out := ""
	for i, s := range a {
		if i > 0 {
			out += " "
		}
		out += s
	}
	return out
}

func TestCovZ_Decode_Errors(t *testing.T) {
	t.Run("ffmpeg falha: erro traz formato e stderr", func(t *testing.T) {
		covZFakeFFmpegEnv(t, "fail", "")
		_, err := DecodeADTSToPCM([]byte("x"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ffmpeg decode (aac)")
		assert.Contains(t, err.Error(), "invalid data found when processing input")

		_, err = DecodeM4AToPCM([]byte("x"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ffmpeg decode (mp4)")
	})

	t.Run("saida desalinhada a float32", func(t *testing.T) {
		covZFakeFFmpegEnv(t, "unaligned", "")
		_, err := DecodeADTSToPCM([]byte("x"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not aligned to float32: 5 bytes")
	})

	t.Run("saida vazia vira PCM vazio, nao erro", func(t *testing.T) {
		covZFakeFFmpegEnv(t, "ok", "")
		got, err := DecodeADTSToPCM([]byte("x"))
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("ffmpeg ausente do PATH", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		_, err := DecodeADTSToPCM([]byte("x"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ffmpeg decode (aac)")
	})
}

// --- AuditEvidence com banco ------------------------------------------------

func TestCovZ_AuditEvidence_Database(t *testing.T) {
	ctx, pool := covZPool(t)
	masterPCM := covZSignal(20, 20)
	masterHashes := PCMToHashes(masterPCM)
	require.NotEmpty(t, masterHashes)
	id, rows := covZLoadMaster(t, ctx, pool, masterHashes, 0, 0, false)
	a := NewAuditor(pool, zap.NewNop(), 0, 0)

	t.Run("clipe com o master inteiro passa", func(t *testing.T) {
		res, err := a.AuditEvidence(ctx, id, masterPCM)
		require.NoError(t, err)
		assert.True(t, res.Passed)
		assert.Equal(t, rows, res.MasterHashes, "total de linhas do master")
		assert.Equal(t, len(masterHashes), res.QueryHashes)
		assert.GreaterOrEqual(t, res.Score, DefaultMinScore)
		assert.GreaterOrEqual(t, res.Coverage, DefaultMinCoverage)
		assert.InDelta(t, 1.0, res.MatchExtent, 0.05)
		assert.Equal(t, uint8(0), res.VariantID)
		assert.Equal(t, uint8(0), res.RateID)
		assert.Positive(t, res.Duration)
	})

	t.Run("clipe de outro audio e reprovado", func(t *testing.T) {
		res, err := a.AuditEvidence(ctx, id, covZSignal(77, 20))
		require.NoError(t, err)
		assert.False(t, res.Passed, "score=%d cov=%.3f", res.Score, res.Coverage)
	})

	t.Run("clipe vazio reprova sem erro", func(t *testing.T) {
		res, err := a.AuditEvidence(ctx, id, nil)
		require.NoError(t, err)
		assert.False(t, res.Passed)
		assert.Equal(t, 0, res.QueryHashes)
		assert.Equal(t, rows, res.MasterHashes)
	})

	t.Run("master sem hashes e erro de infraestrutura", func(t *testing.T) {
		_, err := a.AuditEvidence(ctx, uuid.New(), masterPCM)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no master hashes for commercial_id=")
	})

	t.Run("banco indisponivel", func(t *testing.T) {
		_, closed := covZPool(t)
		closed.Close()
		_, err := NewAuditor(closed, zap.NewNop(), 0, 0).AuditEvidence(ctx, id, masterPCM)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "load master hashes")
	})
}

func TestCovZ_AuditEvidence_SharedMasterKeepsCoverageGate(t *testing.T) {
	ctx, pool := covZPool(t)
	masterPCM := covZSignal(21, 24)
	masterHashes := PCMToHashes(masterPCM)
	// Clipe curto (~2,5s de 24s): score alto mas cobertura baixa.
	clip := masterPCM[:40000]

	plainID, _ := covZLoadMaster(t, ctx, pool, masterHashes, 0, 0, false)
	sharedID, _ := covZLoadMaster(t, ctx, pool, masterHashes, 0, 0, true)
	a := NewAuditor(pool, zap.NewNop(), 0, 0)

	plain, err := a.AuditEvidence(ctx, plainID, clip)
	require.NoError(t, err)
	shared, err := a.AuditEvidence(ctx, sharedID, clip)
	require.NoError(t, err)

	// Pre-condicoes do cenario: placar acima do bypass e cobertura abaixo do minimo.
	require.GreaterOrEqual(t, plain.Score, coverageBypassScore, "cenario precisa de score alto")
	require.Less(t, plain.Coverage, DefaultMinCoverage, "cenario precisa de cobertura baixa")

	assert.True(t, plain.Passed, "master sem hash compartilhado: score esmagador dispensa a cobertura")
	assert.False(t, shared.Passed, "master com hash compartilhado mantem o portao de cobertura (guarda contra sting)")
	assert.Equal(t, plain.Score, shared.Score)
}

func TestCovZ_AuditEvidence_ReportsWinningVariantAndRate(t *testing.T) {
	ctx, pool := covZPool(t)
	masterPCM := covZSignal(22, 10)
	id, _ := covZLoadMaster(t, ctx, pool, PCMToHashes(masterPCM), 2, 3, false)

	res, err := NewAuditor(pool, zap.NewNop(), 0, 0).AuditEvidence(ctx, id, masterPCM)
	require.NoError(t, err)
	assert.True(t, res.Passed)
	assert.Equal(t, uint8(2), res.VariantID)
	assert.Equal(t, uint8(3), res.RateID)
}

func TestCovZ_AuditEvidence_VariantOutOfRange(t *testing.T) {
	ctx, pool := covZPool(t)
	pcm := covZSignal(23, 6)
	id, _ := covZLoadMaster(t, ctx, pool, PCMToHashes(pcm), 300, 0, false) // smallint comporta 300; uint8 nao

	_, err := NewAuditor(pool, zap.NewNop(), 0, 0).AuditEvidence(ctx, id, pcm)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of uint8 range")
}

func TestCovZ_AuditEvidence_CustomThresholdsAreHonored(t *testing.T) {
	ctx, pool := covZPool(t)
	pcm := covZSignal(24, 10)
	id, _ := covZLoadMaster(t, ctx, pool, PCMToHashes(pcm), 0, 0, false)

	// Limiar de score impossivel de atingir: o mesmo audio passa a ser reprovado.
	strict, err := NewAuditor(pool, zap.NewNop(), 1_000_000, 0).AuditEvidence(ctx, id, pcm)
	require.NoError(t, err)
	assert.False(t, strict.Passed)
	assert.Greater(t, strict.Score, 0)
}
