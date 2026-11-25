package handlers

// cov_a_commercials_test.go — rotas legadas de /commercials (upload do master,
// lista, detalhe, apagar, trocar emissoras, áudio com Range). Banco real; o
// upload bem-sucedido precisa do ffprobe no PATH e é pulado sem ele.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

type covAComCen struct {
	h        *CommercialsHandler
	sup      *mcptest.SupervisorFalso
	r        chi.Router
	campanha uuid.UUID
	emissora uuid.UUID
	dir      string
	ctx      context.Context
	pool     *pgxpool.Pool
}

func covANewComCen(t *testing.T) *covAComCen {
	t.Helper()
	ctx, pool := covADB(t)
	c := &covAComCen{dir: t.TempDir(), sup: &mcptest.SupervisorFalso{}}
	cliente := mcptest.Cliente(t, ctx, pool, "Cliente Teste")
	c.emissora = mcptest.Emissora(t, ctx, pool, "Rádio Teste FM", 1000)
	c.campanha = mcptest.Campanha(t, ctx, pool, cliente, "Campanha Legada", "2026-06-01", "2026-06-30", c.emissora)
	c.h = &CommercialsHandler{Repo: catalog.NewCommercials(pool), MastersPath: c.dir, Supervisor: c.sup}
	c.r = chi.NewRouter()
	c.r.Get("/commercials", c.h.List)
	c.r.Post("/commercials", c.h.Upload)
	c.r.Get("/commercials/{id}", c.h.Get)
	c.r.Delete("/commercials/{id}", c.h.Delete)
	c.r.Put("/commercials/{id}/stations", c.h.UpdateStations)
	c.r.Get("/commercials/{id}/audio", c.h.Audio)
	c.ctx, c.pool = ctx, pool
	return c
}

// semeia grava um master em disco e a linha em commercials.
func (c *covAComCen) semeia(t *testing.T, titulo, nome string, conteudo []byte) *catalog.Commercial {
	t.Helper()
	path := filepath.Join(c.dir, nome)
	require.NoError(t, os.WriteFile(path, conteudo, 0o644))
	com, err := c.h.Repo.Create(c.ctx, catalog.CreateCommercialInput{
		CampaignID: c.campanha, Title: titulo, DurationSeconds: 30,
		MasterStoragePath: path, MasterSHA256: "sha-" + uuid.NewString(),
	})
	require.NoError(t, err)
	return com
}

func (c *covAComCen) pedido(method, alvo string) *httptest.ResponseRecorder {
	return covAServe(c.r, httptest.NewRequest(method, alvo, nil))
}

func TestCovA_Commercials_ListEGet(t *testing.T) {
	c := covANewComCen(t)

	t.Run("lista de campanha sem peças é [] e não null", func(t *testing.T) {
		rec := c.pedido(http.MethodGet, "/commercials?campaign_id="+c.campanha.String())
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, []any{}, covADecode(t, rec)["data"])
	})

	com := c.semeia(t, "Spot Legado", "a.wav", []byte("RIFF"))

	t.Run("lista devolve as peças da campanha", func(t *testing.T) {
		rec := c.pedido(http.MethodGet, "/commercials?campaign_id="+c.campanha.String())
		require.Equal(t, http.StatusOK, rec.Code)
		data := covADecode(t, rec)["data"].([]any)
		require.Len(t, data, 1)
		assert.Equal(t, com.ID.String(), data[0].(map[string]any)["id"])
		assert.Equal(t, "Spot Legado", data[0].(map[string]any)["title"])
	})

	t.Run("get devolve a peça", func(t *testing.T) {
		rec := c.pedido(http.MethodGet, "/commercials/"+com.ID.String())
		require.Equal(t, http.StatusOK, rec.Code)
		body := covADecode(t, rec)
		assert.Equal(t, com.ID.String(), body["id"])
		assert.EqualValues(t, 30, body["duration_seconds"])
		assert.Equal(t, "pending", body["fingerprint_status"])
	})

	t.Run("get de id inexistente 404", func(t *testing.T) {
		assert.Equal(t, http.StatusNotFound, c.pedido(http.MethodGet, "/commercials/"+uuid.NewString()).Code)
	})

	t.Run("falha do banco 500 na lista e no get", func(t *testing.T) {
		h := &CommercialsHandler{Repo: catalog.NewCommercials(covAClosedPool(t))}
		rt := chi.NewRouter()
		rt.Get("/commercials", h.List)
		rt.Get("/commercials/{id}", h.Get)
		assert.Equal(t, http.StatusInternalServerError, covAServe(rt, httptest.NewRequest(http.MethodGet, "/commercials?campaign_id="+c.campanha.String(), nil)).Code)
		assert.Equal(t, http.StatusInternalServerError, covAServe(rt, httptest.NewRequest(http.MethodGet, "/commercials/"+com.ID.String(), nil)).Code)
	})
}

func TestCovA_Commercials_Delete(t *testing.T) {
	c := covANewComCen(t)

	t.Run("inexistente 404", func(t *testing.T) {
		assert.Equal(t, http.StatusNotFound, c.pedido(http.MethodDelete, "/commercials/"+uuid.NewString()).Code)
		assert.Zero(t, c.sup.Contagem("Reload", c.campanha))
	})

	t.Run("com histórico de detecções 409 e nada é apagado", func(t *testing.T) {
		com := c.semeia(t, "Com Histórico", "h.wav", []byte("x"))
		mcptest.Veiculacao(t, c.ctx, c.pool, c.campanha, com.ID, c.emissora, "in_slot", mcptest.Quando("2026-06-10", "10:30"))
		rec := c.pedido(http.MethodDelete, "/commercials/"+com.ID.String())
		assert.Equal(t, http.StatusConflict, rec.Code)
		assert.Contains(t, rec.Body.String(), "detection history")
		_, err := os.Stat(com.MasterStoragePath)
		assert.NoError(t, err, "o master continua em disco")
	})

	t.Run("sucesso apaga linha e arquivo e recarrega a campanha", func(t *testing.T) {
		com := c.semeia(t, "Sem Histórico", "s.wav", []byte("x"))
		rec := c.pedido(http.MethodDelete, "/commercials/"+com.ID.String())
		require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
		_, err := os.Stat(com.MasterStoragePath)
		assert.True(t, os.IsNotExist(err), "o master some do disco")
		assert.Equal(t, http.StatusNotFound, c.pedido(http.MethodGet, "/commercials/"+com.ID.String()).Code)
		assert.Equal(t, 1, c.sup.Contagem("Reload", c.campanha))
	})

	t.Run("falha no reload é logada mas a rota segue 204", func(t *testing.T) {
		log, logs := covALogger()
		c.h.Log = log
		c.sup.ErroReload = assert.AnError
		defer func() { c.h.Log = nil; c.sup.ErroReload = nil }()
		com := c.semeia(t, "Reload Falha", "r.wav", []byte("x"))
		rec := c.pedido(http.MethodDelete, "/commercials/"+com.ID.String())
		assert.Equal(t, http.StatusNoContent, rec.Code)
		e, ok := covAHasLog(logs, "commercials.Delete: supervisor reload failed")
		require.True(t, ok)
		assert.Equal(t, com.ID.String(), e.ContextMap()["commercial_id"])
	})

	t.Run("sem supervisor também apaga", func(t *testing.T) {
		h := &CommercialsHandler{Repo: c.h.Repo, MastersPath: c.dir}
		com := c.semeia(t, "Sem Sup", "n.wav", []byte("x"))
		rt := chi.NewRouter()
		rt.Delete("/commercials/{id}", h.Delete)
		assert.Equal(t, http.StatusNoContent, covAServe(rt, httptest.NewRequest(http.MethodDelete, "/commercials/"+com.ID.String(), nil)).Code)
	})

	t.Run("falha do banco 500", func(t *testing.T) {
		h := &CommercialsHandler{Repo: catalog.NewCommercials(covAClosedPool(t))}
		rt := chi.NewRouter()
		rt.Delete("/commercials/{id}", h.Delete)
		assert.Equal(t, http.StatusInternalServerError, covAServe(rt, httptest.NewRequest(http.MethodDelete, "/commercials/"+uuid.NewString(), nil)).Code)
	})
}

func TestCovA_Commercials_UpdateStations(t *testing.T) {
	c := covANewComCen(t)
	com := c.semeia(t, "Spot", "u.wav", []byte("x"))
	outra := mcptest.Emissora(t, c.ctx, c.pool, "Rádio Outra FM", 0)
	put := func(id string, corpo any) *httptest.ResponseRecorder {
		return covAServe(c.r, covAJSON(t, http.MethodPut, "/commercials/"+id+"/stations", corpo))
	}

	assert.Equal(t, http.StatusNotFound, put(uuid.NewString(), map[string]any{"target_stations": []string{}}).Code)
	assert.Zero(t, c.sup.Contagem("Reload", c.campanha))

	rec := put(com.ID.String(), map[string]any{"target_stations": []string{c.emissora.String(), outra.String()}})
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	got, err := c.h.Repo.Get(c.ctx, com.ID)
	require.NoError(t, err)
	assert.ElementsMatch(t, []uuid.UUID{c.emissora, outra}, got.TargetStations)
	assert.Equal(t, 1, c.sup.Contagem("Reload", c.campanha), "a mudança recarrega os workers da campanha")

	t.Run("lista ausente zera as emissoras (peça inativa)", func(t *testing.T) {
		rec := put(com.ID.String(), map[string]any{})
		require.Equal(t, http.StatusNoContent, rec.Code)
		got, err := c.h.Repo.Get(c.ctx, com.ID)
		require.NoError(t, err)
		assert.Empty(t, got.TargetStations)
	})

	t.Run("falha no reload é logada e a rota segue 204", func(t *testing.T) {
		log, logs := covALogger()
		c.h.Log = log
		c.sup.ErroReload = assert.AnError
		defer func() { c.h.Log = nil; c.sup.ErroReload = nil }()
		assert.Equal(t, http.StatusNoContent, put(com.ID.String(), map[string]any{"target_stations": []string{}}).Code)
		_, ok := covAHasLog(logs, "commercials.UpdateStations: supervisor reload failed")
		assert.True(t, ok)
	})

	t.Run("falha do banco 500", func(t *testing.T) {
		h := &CommercialsHandler{Repo: catalog.NewCommercials(covAClosedPool(t))}
		rt := chi.NewRouter()
		rt.Put("/commercials/{id}/stations", h.UpdateStations)
		rec := covAServe(rt, covAJSON(t, http.MethodPut, "/commercials/"+com.ID.String()+"/stations", map[string]any{}))
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}

func TestCovA_Commercials_Audio(t *testing.T) {
	c := covANewComCen(t)
	conteudo := []byte("0123456789ABCDEFGHIJ")
	com := c.semeia(t, `Spot "Aspas"/Barra`, "audio.mp3", conteudo)

	t.Run("id inexistente 404", func(t *testing.T) {
		assert.Equal(t, http.StatusNotFound, c.pedido(http.MethodGet, "/commercials/"+uuid.NewString()+"/audio").Code)
	})

	t.Run("serve o arquivo inline com nome seguro", func(t *testing.T) {
		rec := c.pedido(http.MethodGet, "/commercials/"+com.ID.String()+"/audio")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, conteudo, rec.Body.Bytes())
		assert.Equal(t, `inline; filename="Spot _Aspas__Barra.mp3"`, rec.Header().Get("Content-Disposition"))
		assert.Equal(t, "bytes", rec.Header().Get("Accept-Ranges"))
		assert.True(t, strings.HasPrefix(rec.Header().Get("Content-Type"), "audio/"), rec.Header().Get("Content-Type"))
	})

	t.Run("download=1 força attachment", func(t *testing.T) {
		rec := c.pedido(http.MethodGet, "/commercials/"+com.ID.String()+"/audio?download=1")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.True(t, strings.HasPrefix(rec.Header().Get("Content-Disposition"), "attachment;"))
	})

	t.Run("Range devolve 206 com o trecho pedido", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/commercials/"+com.ID.String()+"/audio", nil)
		req.Header.Set("Range", "bytes=5-9")
		rec := covAServe(c.r, req)
		require.Equal(t, http.StatusPartialContent, rec.Code)
		assert.Equal(t, "56789", rec.Body.String())
		assert.Equal(t, "bytes 5-9/20", rec.Header().Get("Content-Range"))
	})

	t.Run("extensão desconhecida cai em octet-stream", func(t *testing.T) {
		raro := c.semeia(t, "Raro", "peca.zzzraro", []byte("x"))
		rec := c.pedido(http.MethodGet, "/commercials/"+raro.ID.String()+"/audio")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "application/octet-stream", rec.Header().Get("Content-Type"))
	})

	t.Run("arquivo sumido do disco 404", func(t *testing.T) {
		require.NoError(t, os.Remove(com.MasterStoragePath))
		rec := c.pedido(http.MethodGet, "/commercials/"+com.ID.String()+"/audio")
		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.Contains(t, rec.Body.String(), "audio file not available")
	})

	t.Run("falha do banco 500", func(t *testing.T) {
		h := &CommercialsHandler{Repo: catalog.NewCommercials(covAClosedPool(t))}
		rt := chi.NewRouter()
		rt.Get("/commercials/{id}/audio", h.Audio)
		assert.Equal(t, http.StatusInternalServerError, covAServe(rt, httptest.NewRequest(http.MethodGet, "/commercials/"+com.ID.String()+"/audio", nil)).Code)
	})
}

func TestCovA_Commercials_Upload_Recusas(t *testing.T) {
	c := covANewComCen(t)
	post := func(campos map[string]string, partes ...covAPart) *httptest.ResponseRecorder {
		return covAServe(c.r, covAMultipart(t, http.MethodPost, "/commercials", campos, partes...))
	}
	audio := covAPart{Field: "audio", Filename: "spot.wav", ContentType: "audio/wav", Data: []byte("RIFF")}
	ok := map[string]string{"campaign_id": c.campanha.String(), "title": "Spot Novo"}

	casos := []struct {
		nome   string
		campos map[string]string
		partes []covAPart
		status int
		texto  string
	}{
		{"sem título", map[string]string{"campaign_id": c.campanha.String()}, []covAPart{audio}, 400, "campaign_id and title are required"},
		{"sem campanha", map[string]string{"title": "x"}, []covAPart{audio}, 400, "campaign_id and title are required"},
		{"campanha inválida", map[string]string{"campaign_id": "xx", "title": "x"}, []covAPart{audio}, 400, "invalid campaign_id"},
		{"sem arquivo", ok, nil, 400, "audio file required"},
		{"extensão não aceita", ok, []covAPart{{Field: "audio", Filename: "spot.ogg", ContentType: "audio/ogg", Data: []byte("x")}}, 400, "unsupported audio format"},
	}
	for _, cs := range casos {
		t.Run(cs.nome, func(t *testing.T) {
			rec := post(cs.campos, cs.partes...)
			assert.Equal(t, cs.status, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), cs.texto)
		})
	}

	t.Run("pasta de masters impossível de criar 500", func(t *testing.T) {
		arquivo := filepath.Join(c.dir, "ocupado")
		require.NoError(t, os.WriteFile(arquivo, []byte("x"), 0o644))
		h := &CommercialsHandler{Repo: c.h.Repo, MastersPath: filepath.Join(arquivo, "sub")}
		rt := chi.NewRouter()
		rt.Post("/commercials", h.Upload)
		rec := covAServe(rt, covAMultipart(t, http.MethodPost, "/commercials", ok, audio))
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}

func covAExigeFFprobe(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe não está no PATH")
	}
}

func TestCovA_Commercials_ProbeDuration(t *testing.T) {
	covAExigeFFprobe(t)
	dir := t.TempDir()

	wav := filepath.Join(dir, "tres.wav")
	require.NoError(t, os.WriteFile(wav, covAWAV(3), 0o644))
	d, err := probeDuration(wav)
	require.NoError(t, err)
	assert.InDelta(t, 3.0, d, 0.1)

	lixo := filepath.Join(dir, "lixo.wav")
	require.NoError(t, os.WriteFile(lixo, []byte("isto nao e audio"), 0o644))
	_, err = probeDuration(lixo)
	assert.Error(t, err)

	_, err = probeDuration(filepath.Join(dir, "nao-existe.wav"))
	assert.Error(t, err)
}

func TestCovA_Commercials_Upload_ComFFprobe(t *testing.T) {
	covAExigeFFprobe(t)
	c := covANewComCen(t)
	post := func(partes ...covAPart) *httptest.ResponseRecorder {
		campos := map[string]string{"campaign_id": c.campanha.String(), "title": "Spot Novo", "cut_label": "30s"}
		return covAServe(c.r, covAMultipart(t, http.MethodPost, "/commercials", campos, partes...))
	}

	t.Run("arquivo que o ffprobe não entende 400 e não deixa lixo em disco", func(t *testing.T) {
		rec := post(covAPart{Field: "audio", Filename: "spot.wav", ContentType: "audio/wav", Data: []byte("isto nao e audio")})
		require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), "could not probe audio duration")
		entradas, err := os.ReadDir(c.dir)
		require.NoError(t, err)
		assert.Empty(t, entradas, "o temporário é removido")
	})

	t.Run("WAV válido: 202, linha criada, master gravado e sha confere", func(t *testing.T) {
		wav := covAWAV(2)
		rec := post(covAPart{Field: "audio", Filename: "spot.wav", ContentType: "audio/wav", Data: wav})
		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
		body := covADecode(t, rec)
		assert.Equal(t, "Spot Novo", body["title"])
		assert.Equal(t, "30s", body["cut_label"])
		assert.InDelta(t, 2.0, body["duration_seconds"], 0.1)
		assert.Equal(t, "pending", body["fingerprint_status"])
		path := body["master_storage_path"].(string)
		assert.Equal(t, c.dir, filepath.Dir(path))
		assert.Equal(t, ".wav", filepath.Ext(path))
		gravado, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, wav, gravado)
		assert.Len(t, body["master_sha256"], 64)
	})

	t.Run("campanha inexistente 500 e o arquivo é removido", func(t *testing.T) {
		campos := map[string]string{"campaign_id": uuid.NewString(), "title": "Órfão"}
		antes, _ := os.ReadDir(c.dir)
		rec := covAServe(c.r, covAMultipart(t, http.MethodPost, "/commercials", campos,
			covAPart{Field: "audio", Filename: "spot.wav", ContentType: "audio/wav", Data: covAWAV(1)}))
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		depois, _ := os.ReadDir(c.dir)
		assert.Len(t, depois, len(antes), "nada novo sobra em disco")
	})
}
