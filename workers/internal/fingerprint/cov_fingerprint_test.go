package fingerprint

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCovZ_SyntheticSignalProducesHashes(t *testing.T) {
	// Guarda do proprio helper: sem hashes os testes abaixo nao provariam nada.
	pcm := covZSignal(1, 4)
	hashes := GenerateHashes(PickPeaks(STFT(pcm)))
	require.Greater(t, len(hashes), 100)

	// Sinais com sementes diferentes geram hashes diferentes; mesma semente, os mesmos.
	again := GenerateHashes(PickPeaks(STFT(covZSignal(1, 4))))
	assert.Equal(t, hashes, again, "determinismo")
	other := GenerateHashes(PickPeaks(STFT(covZSignal(2, 4))))
	assert.NotEqual(t, hashes, other)
}

func TestCovZ_VariantString(t *testing.T) {
	cases := []struct {
		v    VariantID
		want string
	}{
		{VariantClean, "clean"},
		{VariantLight, "light"},
		{VariantMedium, "medium"},
		{VariantHeavy, "heavy"},
		{VariantID(9), "variant_9"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, c.v.String())
		if c.v <= VariantHeavy {
			got, err := ParseVariant(c.want)
			require.NoError(t, err)
			assert.Equal(t, c.v, got, "ParseVariant e o inverso de String")
		}
	}
	_, err := ParseVariant("ultra")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown variant "ultra"`)
}

func TestCovZ_FilterChain(t *testing.T) {
	chain, err := filterChain(VariantClean)
	require.NoError(t, err)
	assert.Contains(t, chain, "loudnorm")
	assert.Contains(t, chain, "highpass=f=80")
	assert.Contains(t, chain, "lowpass=f=7500")

	for _, v := range []VariantID{VariantLight, VariantMedium, VariantHeavy} {
		_, err := filterChain(v)
		assert.ErrorIs(t, err, ErrVariantNotImplemented, v.String())
	}

	_, err = filterChain(VariantID(42))
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrVariantNotImplemented)
	assert.Contains(t, err.Error(), "unknown variant 42")
}

func TestCovZ_DecodePCM_Success(t *testing.T) {
	pcm := covZSignal(7, 1)
	argsFile := covZFakeFFmpegEnv(t, "ok", covZWritePCM(t, pcm))

	got, err := DecodePCM(context.Background(), "/tmp/master-de-teste.mp3", VariantClean)
	require.NoError(t, err)
	require.Len(t, got, len(pcm))
	assert.Equal(t, pcm, got, "amostras f32le decodificadas sem perda")

	args := covZReadArgs(t, argsFile)
	joined := " " + joinArgs(args) + " "
	assert.Contains(t, joined, " -i /tmp/master-de-teste.mp3 ")
	assert.Contains(t, joined, " -ac 1 ")
	assert.Contains(t, joined, " -ar 16000 ")
	assert.Contains(t, joined, " -f f32le ")
	assert.Contains(t, joined, "loudnorm=I=-16:LRA=11:TP=-1.5,highpass=f=80,lowpass=f=7500")
	assert.Equal(t, "-", args[len(args)-1], "saida em stdout")
}

func joinArgs(a []string) string {
	out := ""
	for i, s := range a {
		if i > 0 {
			out += " "
		}
		out += s
	}
	return out
}

func TestCovZ_DecodePCM_Errors(t *testing.T) {
	t.Run("variante nao implementada nem chega a executar o ffmpeg", func(t *testing.T) {
		argsFile := covZFakeFFmpegEnv(t, "ok", "")
		_, err := DecodePCM(context.Background(), "x.mp3", VariantHeavy)
		require.ErrorIs(t, err, ErrVariantNotImplemented)
		_, statErr := os.Stat(argsFile)
		assert.True(t, os.IsNotExist(statErr), "ffmpeg nao deve ter sido executado")
	})

	t.Run("ffmpeg falha: erro carrega o stderr", func(t *testing.T) {
		covZFakeFFmpegEnv(t, "fail", "")
		_, err := DecodePCM(context.Background(), "corrompido.mp3", VariantClean)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ffmpeg decode corrompido.mp3")
		assert.Contains(t, err.Error(), "invalid data found when processing input")
	})

	t.Run("saida vazia", func(t *testing.T) {
		covZFakeFFmpegEnv(t, "empty", "")
		_, err := DecodePCM(context.Background(), "vazio.mp3", VariantClean)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty PCM")
	})

	t.Run("saida desalinhada a float32", func(t *testing.T) {
		covZFakeFFmpegEnv(t, "unaligned", "")
		_, err := DecodePCM(context.Background(), "torto.mp3", VariantClean)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not aligned to float32 (5 bytes)")
	})

	t.Run("contexto cancelado", func(t *testing.T) {
		covZFakeFFmpegEnv(t, "ok", covZWritePCM(t, covZSignal(1, 1)))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := DecodePCM(ctx, "x.mp3", VariantClean)
		require.Error(t, err)
	})

	t.Run("ffmpeg ausente do PATH", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir()) // diretorio vazio
		_, err := DecodePCM(context.Background(), "x.mp3", VariantClean)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ffmpeg decode x.mp3")
	})
}

func TestCovZ_GenerateForVariant_MatchesManualPipeline(t *testing.T) {
	pcm := covZSignal(11, 5)
	covZFakeFFmpegEnv(t, "ok", covZWritePCM(t, pcm))

	res, err := GenerateForVariant(context.Background(), "master.mp3", VariantClean)
	require.NoError(t, err)

	spec := STFT(pcm)
	peaks := PickPeaks(spec)
	want := GenerateHashes(peaks)

	assert.Equal(t, VariantClean, res.Variant)
	assert.Equal(t, want, res.Hashes)
	assert.Equal(t, len(spec), res.Frames)
	assert.Equal(t, len(peaks), res.Peaks)
	assert.InDelta(t, 5.0, res.DurationSec, 1e-9)
	assert.InDelta(t, float64(len(want))/5.0, res.HashesPerSec, 1e-9)
	assert.Positive(t, res.Elapsed)

	unique, entropy := entropyStats(want)
	assert.Equal(t, unique, res.UniqueHashes)
	assert.InDelta(t, entropy, res.HashEntropyEst, 1e-9)
	assert.Positive(t, res.UniqueHashes)
	assert.Positive(t, res.HashEntropyEst)

	// O Validate reflete a densidade medida: o sinal sintetico e denso o bastante.
	v := Validate(res)
	assert.True(t, v.HashesPerSecondOK, "hashes/s = %.1f", res.HashesPerSec)
}

func TestCovZ_GenerateForVariant_Errors(t *testing.T) {
	covZFakeFFmpegEnv(t, "fail", "")
	_, err := GenerateForVariant(context.Background(), "x.mp3", VariantClean)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ffmpeg decode")

	_, err = GenerateForVariant(context.Background(), "x.mp3", VariantMedium)
	assert.ErrorIs(t, err, ErrVariantNotImplemented)
}

// --- banco ------------------------------------------------------------------

// covZCommercial cria cliente + campanha + comercial legado e agenda a limpeza.
func covZCommercial(t *testing.T, ctx context.Context, pool *pgxpool.Pool, title string) (id uuid.UUID, shortID int32) {
	t.Helper()
	var cli, camp uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO clients (name) VALUES ($1) RETURNING id`, "Cliente Teste "+title).Scan(&cli))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO campaigns (client_id, name, start_date, end_date, status)
		 VALUES ($1,$2,'2026-06-01','2026-06-30','ativa') RETURNING id`, cli, "Campanha "+title).Scan(&camp))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO commercials (campaign_id, title, duration_seconds, master_storage_path, master_sha256)
		 VALUES ($1,$2,30,'covz/m.mp3',$3) RETURNING id, short_id`, camp, title, "covz-"+uuid.NewString()).Scan(&id, &shortID))
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM fingerprint_hashes WHERE commercial_id=$1`, id)
		_, _ = pool.Exec(bg, `DELETE FROM commercials WHERE id=$1`, id)
		_, _ = pool.Exec(bg, `DELETE FROM campaigns WHERE id=$1`, camp)
		_, _ = pool.Exec(bg, `DELETE FROM clients WHERE id=$1`, cli)
	})
	return id, shortID
}

func covZCountHashes(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, variant, rate int) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM fingerprint_hashes WHERE commercial_id=$1 AND variant_id=$2 AND rate_id=$3`, id, variant, rate).Scan(&n))
	return n
}

func TestCovZ_LookupCommercialByShortID(t *testing.T) {
	ctx, pool := covZPool(t)
	id, shortID := covZCommercial(t, ctx, pool, "Lookup")

	got, err := LookupCommercialByShortID(ctx, pool, shortID)
	require.NoError(t, err)
	assert.Equal(t, id, got.ID)
	assert.Equal(t, shortID, got.ShortID)
	assert.Equal(t, "Lookup", got.Title)

	_, err = LookupCommercialByShortID(ctx, pool, -1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "short_id=-1 not found")

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = LookupCommercialByShortID(cancelled, pool, shortID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lookup commercial")
}

func TestCovZ_Persist_InsertsAndMarksReady(t *testing.T) {
	ctx, pool := covZPool(t)
	id, _ := covZCommercial(t, ctx, pool, "Persist")

	hashes := GenerateHashes(PickPeaks(STFT(covZSignal(3, 4))))
	require.Greater(t, len(hashes), 50)

	n, err := Persist(ctx, pool, hashes, PersistOptions{CommercialID: id, Variant: VariantClean})
	require.NoError(t, err)
	assert.Equal(t, int64(len(hashes)), n)
	assert.Equal(t, len(hashes), covZCountHashes(t, ctx, pool, id, 0, 0))

	var status string
	var count int32
	var generatedAt *string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT fingerprint_status, fingerprint_hash_count, fingerprint_generated_at::text FROM commercials WHERE id=$1`, id).
		Scan(&status, &count, &generatedAt))
	assert.Equal(t, "ready", status)
	assert.Equal(t, int32(len(hashes)), count)
	assert.NotNil(t, generatedAt)

	// Os valores gravados sao os mesmos que o pipeline gerou (uint32 preservado em BIGINT).
	var maxVal int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT MAX(hash_value) FROM fingerprint_hashes WHERE commercial_id=$1`, id).Scan(&maxVal))
	var wantMax uint32
	for _, h := range hashes {
		if h.Value > wantMax {
			wantMax = h.Value
		}
	}
	assert.Equal(t, int64(wantMax), maxVal)
}

func TestCovZ_Persist_ReplaceExistingOnlyTouchesTheSameVariantAndRate(t *testing.T) {
	ctx, pool := covZPool(t)
	id, _ := covZCommercial(t, ctx, pool, "Replace")

	first := GenerateHashes(PickPeaks(STFT(covZSignal(4, 4))))
	second := GenerateHashes(PickPeaks(STFT(covZSignal(5, 3))))
	require.NotEmpty(t, first)
	require.NotEmpty(t, second)

	_, err := Persist(ctx, pool, first, PersistOptions{CommercialID: id, Variant: VariantClean})
	require.NoError(t, err)
	// Outra taxa (rate 2) do mesmo comercial: nao pode ser apagada pelo replace da taxa 0.
	_, err = Persist(ctx, pool, first[:20], PersistOptions{CommercialID: id, Variant: VariantClean, RateID: 2})
	require.NoError(t, err)
	assert.Equal(t, 20, covZCountHashes(t, ctx, pool, id, 0, 2))

	_, err = Persist(ctx, pool, second, PersistOptions{CommercialID: id, Variant: VariantClean, ReplaceExisting: true})
	require.NoError(t, err)
	assert.Equal(t, len(second), covZCountHashes(t, ctx, pool, id, 0, 0), "replace troca o conjunto da variante/taxa")
	assert.Equal(t, 20, covZCountHashes(t, ctx, pool, id, 0, 2), "taxa 2 intacta")

	var count int32
	require.NoError(t, pool.QueryRow(ctx, `SELECT fingerprint_hash_count FROM commercials WHERE id=$1`, id).Scan(&count))
	assert.Equal(t, int32(len(second)), count)
}

func TestCovZ_Persist_Errors(t *testing.T) {
	ctx, pool := covZPool(t)

	t.Run("comercial obrigatorio", func(t *testing.T) {
		_, err := Persist(ctx, pool, nil, PersistOptions{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "CommercialID is required")
	})

	t.Run("chave duplicada derruba a transacao inteira, inclusive o delete", func(t *testing.T) {
		id, _ := covZCommercial(t, ctx, pool, "Dup")
		good := GenerateHashes(PickPeaks(STFT(covZSignal(6, 3))))
		require.NotEmpty(t, good)
		_, err := Persist(ctx, pool, good, PersistOptions{CommercialID: id})
		require.NoError(t, err)

		dup := []Hash{{Value: 1, TimeFrame: 1}, {Value: 1, TimeFrame: 1}}
		_, err = Persist(ctx, pool, dup, PersistOptions{CommercialID: id, ReplaceExisting: true})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "copy hashes")
		assert.Equal(t, len(good), covZCountHashes(t, ctx, pool, id, 0, 0), "rollback preserva os hashes antigos")
	})

	t.Run("hashes de id sem linha em commercials sao gravados sem erro", func(t *testing.T) {
		ghost := uuid.New()
		t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM fingerprint_hashes WHERE commercial_id=$1`, ghost) })
		n, err := Persist(ctx, pool, []Hash{{Value: 5, TimeFrame: 2}}, PersistOptions{CommercialID: ghost})
		require.NoError(t, err)
		assert.Equal(t, int64(1), n)
		assert.Equal(t, 1, covZCountHashes(t, ctx, pool, ghost, 0, 0))
	})

	t.Run("banco indisponivel", func(t *testing.T) {
		_, closed := covZPool(t)
		closed.Close()
		_, err := Persist(ctx, closed, nil, PersistOptions{CommercialID: uuid.New()})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "begin tx")
	})
}

func TestCovZ_DecodePCM_UsesOnlyTheGivenPath(t *testing.T) {
	// Garante que o caminho vai como argumento unico (nada de shell): nomes com
	// espaco e caracteres especiais chegam intactos ao ffmpeg.
	weird := filepath.Join(t.TempDir(), "meu master (v2) & final.mp3")
	argsFile := covZFakeFFmpegEnv(t, "ok", covZWritePCM(t, covZSignal(1, 1)))
	_, err := DecodePCM(context.Background(), weird, VariantClean)
	require.NoError(t, err)
	args := covZReadArgs(t, argsFile)
	idx := -1
	for i, a := range args {
		if a == "-i" {
			idx = i
		}
	}
	require.GreaterOrEqual(t, idx, 0)
	assert.Equal(t, weird, args[idx+1])
}
