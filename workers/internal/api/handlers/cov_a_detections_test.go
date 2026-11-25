package handlers

// cov_a_detections_test.go — rotas de veiculação (/detections): listagem
// (legada e paginada), detalhe, evidência (S3 falso), criação manual com áudio,
// export CSV, agregado por material e resumo diário. Banco real + S3 falso.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

// covADetCen: cliente + campanha de junho/2026 com 1 emissora, 1 material e
// regra de 1 inserção/dia entre 10h e 12h. Duas tocadas semeadas em 10/06.
type covADetCen struct {
	ctx      context.Context
	pool     *pgxpool.Pool
	admin    uuid.UUID
	cliente  uuid.UUID
	emissora uuid.UUID
	tipo     uuid.UUID
	material uuid.UUID
	campanha uuid.UUID
	det1     uuid.UUID // in_slot 10:30
	det2     uuid.UUID // bonus 11:30
	h        *DetectionsHandler
	s3       *covAS3
}

func covANewDetCen(t *testing.T) *covADetCen {
	t.Helper()
	ctx, pool := covADB(t)
	c := &covADetCen{ctx: ctx, pool: pool}
	c.admin = mcptest.Usuario(t, ctx, pool, "admin", true)
	c.cliente = mcptest.Cliente(t, ctx, pool, "Cliente Teste")
	c.emissora = mcptest.Emissora(t, ctx, pool, "Rádio Teste FM", 1000)
	c.tipo = mcptest.TipoMaterial(t, ctx, pool, "Spot")
	c.material = mcptest.Material(t, ctx, pool, c.cliente, c.tipo, "Spot Teste")
	c.campanha = mcptest.Campanha(t, ctx, pool, c.cliente, "Campanha Teste", "2026-06-01", "2026-06-30", c.emissora)
	mcptest.Vincular(t, ctx, pool, c.campanha, c.material, c.emissora)
	mcptest.Regra(t, ctx, pool, c.campanha, c.tipo, c.emissora, "2026-06-01", "2026-06-30", 127, "10:00", "12:00", 1)
	c.det1 = mcptest.Veiculacao(t, ctx, pool, c.campanha, c.material, c.emissora, "in_slot", mcptest.Quando("2026-06-10", "10:30"))
	c.det2 = mcptest.Veiculacao(t, ctx, pool, c.campanha, c.material, c.emissora, "bonus", mcptest.Quando("2026-06-10", "11:30"))

	cli, fake := covANewS3(t)
	c.s3 = fake
	c.h = &DetectionsHandler{
		Repo:         catalog.NewDetections(pool),
		CampaignRepo: catalog.NewCampaigns(pool),
		Storage:      cli,
		SummaryRepo:  catalog.NewDailySummary(pool),
	}
	return c
}

func (c *covADetCen) router(claims *auth.Claims) chi.Router {
	return covARouter(claims, func(r chi.Router) {
		r.Get("/detections", c.h.List)
		r.Get("/detections/export", c.h.Export)
		r.Get("/detections/aggregate", c.h.AggregateByMaterial)
		r.Post("/detections/manual", c.h.CreateManual)
		r.Post("/detections/manual-batch", c.h.CreateManualBatch)
		r.Get("/detections/{id}", c.h.Get)
		r.Get("/detections/{id}/evidence-url", c.h.EvidenceURL)
		r.Get("/detections/{id}/evidence", c.h.Evidence)
		r.Post("/detections/{id}/evidence", c.h.UploadEvidence)
		r.Get("/detections/{id}/proof-url", c.h.ProofURL)
		r.Get("/detections/{id}/proof", c.h.Proof)
		r.Get("/campaigns/{campaignID}/daily-summary", c.h.DailySummary)
	})
}

func (c *covADetCen) adminR() chi.Router { return c.router(covAClaims("admin", c.admin)) }

func (c *covADetCen) get(r http.Handler, alvo string) *httptest.ResponseRecorder {
	return covAServe(r, httptest.NewRequest(http.MethodGet, alvo, nil))
}

// covAOutroCliente cria um segundo cliente com campanha própria e uma tocada.
func (c *covADetCen) outroCliente(t *testing.T) (cliente, campanha, det uuid.UUID) {
	t.Helper()
	cliente = mcptest.Cliente(t, c.ctx, c.pool, "Outro Cliente Teste")
	emissora := mcptest.Emissora(t, c.ctx, c.pool, "Rádio Outra FM", 500)
	tipo := mcptest.TipoMaterial(t, c.ctx, c.pool, "Spot Outro")
	material := mcptest.Material(t, c.ctx, c.pool, cliente, tipo, "Spot Outro Teste")
	campanha = mcptest.Campanha(t, c.ctx, c.pool, cliente, "Campanha Outra", "2026-06-01", "2026-06-30", emissora)
	mcptest.Vincular(t, c.ctx, c.pool, campanha, material, emissora)
	det = mcptest.Veiculacao(t, c.ctx, c.pool, campanha, material, emissora, "in_slot", mcptest.Quando("2026-06-10", "10:45"))
	return cliente, campanha, det
}

// disponibilizaEvidencia grava o objeto no S3 falso e marca a detecção como
// "available" apontando para ele.
func (c *covADetCen) disponibilizaEvidencia(t *testing.T, id uuid.UUID, quando time.Time, key string, data []byte, ct string) {
	t.Helper()
	c.s3.Poe(key, data, ct)
	require.NoError(t, c.h.Repo.UpdateEvidence(c.ctx, id, quando, "available", key, int64(len(data))))
}

// ── List (legado) ────────────────────────────────────────────────────────────

func TestCovA_Detections_List_Legado(t *testing.T) {
	c := covANewDetCen(t)
	r := c.adminR()
	_, _, detOutro := c.outroCliente(t)

	t.Run("recusas de parâmetro", func(t *testing.T) {
		casos := []struct{ qs, trecho string }{
			{"campaign_id=x", "invalid campaign_id"},
			{"station_id=x", "invalid station_id"},
			{"start_date=2026-06-10", "invalid start_date"},
			{"end_date=amanha", "invalid end_date"},
		}
		for _, cs := range casos {
			rec := c.get(r, "/detections?"+cs.qs)
			assert.Equal(t, http.StatusBadRequest, rec.Code, cs.qs)
			assert.Contains(t, rec.Body.String(), cs.trecho)
		}
	})

	ids := func(rec *httptest.ResponseRecorder) []string {
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var out []string
		// "data" pode vir null quando a lista é vazia (o repo devolve slice nil).
		itens, _ := covADecode(t, rec)["data"].([]any)
		for _, it := range itens {
			out = append(out, it.(map[string]any)["id"].(string))
		}
		return out
	}

	t.Run("sem filtro admin vê tudo", func(t *testing.T) {
		got := ids(c.get(r, "/detections"))
		assert.ElementsMatch(t, []string{c.det1.String(), c.det2.String(), detOutro.String()}, got)
	})

	t.Run("filtra por campanha e por emissora", func(t *testing.T) {
		got := ids(c.get(r, "/detections?campaign_id="+c.campanha.String()))
		assert.ElementsMatch(t, []string{c.det1.String(), c.det2.String()}, got)
		got = ids(c.get(r, "/detections?station_id="+c.emissora.String()))
		assert.ElementsMatch(t, []string{c.det1.String(), c.det2.String()}, got)
	})

	t.Run("janela de datas RFC3339", func(t *testing.T) {
		q := url.Values{}
		q.Set("campaign_id", c.campanha.String())
		q.Set("start_date", "2026-06-10T00:00:00Z")
		q.Set("end_date", "2026-06-11T00:00:00Z")
		assert.Len(t, ids(c.get(r, "/detections?"+q.Encode())), 2)

		q.Set("start_date", "2026-06-11T00:00:00Z")
		q.Set("end_date", "2026-06-12T00:00:00Z")
		assert.Empty(t, ids(c.get(r, "/detections?"+q.Encode())))
	})

	t.Run("limit e offset paginam", func(t *testing.T) {
		primeira := ids(c.get(r, "/detections?campaign_id="+c.campanha.String()+"&limit=1"))
		require.Len(t, primeira, 1)
		segunda := ids(c.get(r, "/detections?campaign_id="+c.campanha.String()+"&limit=1&offset=1"))
		require.Len(t, segunda, 1)
		assert.NotEqual(t, primeira[0], segunda[0])
	})

	t.Run("viewer só enxerga a própria carteira", func(t *testing.T) {
		vr := c.router(covAClaims("viewer", uuid.New(), c.cliente))
		got := ids(c.get(vr, "/detections"))
		assert.ElementsMatch(t, []string{c.det1.String(), c.det2.String()}, got)
	})

	t.Run("falha do banco vira 500", func(t *testing.T) {
		h := &DetectionsHandler{Repo: catalog.NewDetections(covAClosedPool(t))}
		rec := covAServe(http.HandlerFunc(h.List), httptest.NewRequest(http.MethodGet, "/detections", nil))
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}

// ── List paginado ────────────────────────────────────────────────────────────

func TestCovA_Detections_List_Paginado(t *testing.T) {
	c := covANewDetCen(t)
	r := c.adminR()
	_, campOutra, detOutro := c.outroCliente(t)

	t.Run("recusas", func(t *testing.T) {
		casos := []struct{ qs, trecho string }{
			{"page=0", "invalid page"},
			{"page=abc", "invalid page"},
			{"page=1&page_size=0", "invalid page_size"},
			{"page=1&page_size=201", "invalid page_size"},
			{"page=1&campaigns=lixo", "invalid campaign_id"},
			{"page=1&from=ontem", "invalid from"},
			{"page=1&to=amanha", "invalid to"},
		}
		for _, cs := range casos {
			rec := c.get(r, "/detections?"+cs.qs)
			assert.Equal(t, http.StatusBadRequest, rec.Code, cs.qs)
			assert.Contains(t, rec.Body.String(), cs.trecho, cs.qs)
		}
	})

	t.Run("página 1 de 2 e ordenação", func(t *testing.T) {
		rec := c.get(r, "/detections?page=1&page_size=1&sort=detected_at_asc&campaigns="+c.campanha.String())
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		body := covADecode(t, rec)
		assert.EqualValues(t, 2, body["total"])
		assert.EqualValues(t, 2, body["total_pages"])
		assert.EqualValues(t, 1, body["page_size"])
		data := body["data"].([]any)
		require.Len(t, data, 1)
		assert.Equal(t, c.det1.String(), data[0].(map[string]any)["id"], "asc: a das 10:30 vem primeiro")

		rec = c.get(r, "/detections?page=1&page_size=1&sort=detected_at_desc&campaigns="+c.campanha.String())
		data = covADecode(t, rec)["data"].([]any)
		assert.Equal(t, c.det2.String(), data[0].(map[string]any)["id"], "desc: a das 11:30 vem primeiro")
	})

	t.Run("campaign_id legado e busca textual", func(t *testing.T) {
		rec := c.get(r, "/detections?page=1&campaign_id="+campOutra.String())
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		data := covADecode(t, rec)["data"].([]any)
		require.Len(t, data, 1)
		assert.Equal(t, detOutro.String(), data[0].(map[string]any)["id"])

		rec = c.get(r, "/detections?page=1&q="+url.QueryEscape("nada-parecido-com-isso"))
		assert.EqualValues(t, 0, covADecode(t, rec)["total"])
	})

	t.Run("janela from/to", func(t *testing.T) {
		q := url.Values{"page": {"1"}, "campaigns": {c.campanha.String()},
			"from": {"2026-06-10T00:00:00Z"}, "to": {"2026-06-11T00:00:00Z"}}
		assert.EqualValues(t, 2, covADecode(t, c.get(r, "/detections?"+q.Encode()))["total"])
		q.Set("from", "2026-07-01T00:00:00Z")
		q.Set("to", "2026-07-02T00:00:00Z")
		assert.EqualValues(t, 0, covADecode(t, c.get(r, "/detections?"+q.Encode()))["total"])
	})

	t.Run("viewer não vê campanha alheia mesmo pedindo o id", func(t *testing.T) {
		vr := c.router(covAClaims("viewer", uuid.New(), c.cliente))
		rec := c.get(vr, "/detections?page=1&campaigns="+campOutra.String())
		require.Equal(t, http.StatusOK, rec.Code)
		assert.EqualValues(t, 0, covADecode(t, rec)["total"])
	})

	t.Run("falha do banco vira 500", func(t *testing.T) {
		h := &DetectionsHandler{Repo: catalog.NewDetections(covAClosedPool(t))}
		rec := covAServe(http.HandlerFunc(h.List), httptest.NewRequest(http.MethodGet, "/detections?page=1", nil))
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}

// ── Get / escopo ─────────────────────────────────────────────────────────────

func TestCovA_Detections_Get(t *testing.T) {
	c := covANewDetCen(t)
	_, _, detOutro := c.outroCliente(t)
	admin := c.adminR()
	viewer := c.router(covAClaims("viewer", uuid.New(), c.cliente))

	rec := c.get(admin, "/detections/nao-uuid")
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = c.get(admin, "/detections/"+uuid.NewString())
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = c.get(admin, "/detections/"+c.det1.String())
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := covADecode(t, rec)
	assert.Equal(t, c.det1.String(), body["id"])
	assert.Equal(t, "in_slot", body["category"])
	assert.Equal(t, "Spot Teste", body["commercial_name"])

	t.Run("viewer: própria carteira 200", func(t *testing.T) {
		assert.Equal(t, http.StatusOK, c.get(viewer, "/detections/"+c.det1.String()).Code)
	})
	t.Run("viewer: veiculação alheia 404 (anti-oracle)", func(t *testing.T) {
		assert.Equal(t, http.StatusNotFound, c.get(viewer, "/detections/"+detOutro.String()).Code)
	})
	t.Run("viewer: id inexistente 404", func(t *testing.T) {
		assert.Equal(t, http.StatusNotFound, c.get(viewer, "/detections/"+uuid.NewString()).Code)
	})
	t.Run("falha do banco 500 (admin e viewer)", func(t *testing.T) {
		h := &DetectionsHandler{Repo: catalog.NewDetections(covAClosedPool(t))}
		for _, claims := range []*auth.Claims{covAClaims("admin", c.admin), covAClaims("viewer", uuid.New(), c.cliente)} {
			rt := covARouter(claims, func(r chi.Router) { r.Get("/detections/{id}", h.Get) })
			assert.Equal(t, http.StatusInternalServerError, c.get(rt, "/detections/"+c.det1.String()).Code)
		}
	})
}

// ── Evidência ────────────────────────────────────────────────────────────────

func TestCovA_Detections_EvidenciaURLeBytes(t *testing.T) {
	c := covANewDetCen(t)
	_, _, detOutro := c.outroCliente(t)
	admin := c.adminR()
	viewer := c.router(covAClaims("viewer", uuid.New(), c.cliente))
	quando := mcptest.Quando("2026-06-10", "10:30")

	for _, rota := range []string{"evidence-url", "evidence"} {
		t.Run(rota+": id inválido e inexistente", func(t *testing.T) {
			assert.Equal(t, http.StatusBadRequest, c.get(admin, "/detections/xx/"+rota).Code)
			assert.Equal(t, http.StatusNotFound, c.get(admin, "/detections/"+uuid.NewString()+"/"+rota).Code)
		})
		t.Run(rota+": sem evidência disponível é 404", func(t *testing.T) {
			rec := c.get(admin, "/detections/"+c.det1.String()+"/"+rota)
			assert.Equal(t, http.StatusNotFound, rec.Code)
			assert.Contains(t, rec.Body.String(), "evidence not available")
		})
		t.Run(rota+": viewer de outro cliente leva 404", func(t *testing.T) {
			assert.Equal(t, http.StatusNotFound, c.get(viewer, "/detections/"+detOutro.String()+"/"+rota).Code)
			assert.Equal(t, http.StatusNotFound, c.get(viewer, "/detections/"+uuid.NewString()+"/"+rota).Code)
		})
	}

	key := "evidences/2026/06/10/" + c.emissora.String() + "/" + c.det1.String() + ".m4a"
	c.disponibilizaEvidencia(t, c.det1, quando, key, []byte("AUDIO-FALSO"), "audio/mp4")

	t.Run("evidence-url devolve URL pré-assinada da chave", func(t *testing.T) {
		rec := c.get(admin, "/detections/"+c.det1.String()+"/evidence-url")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		body := covADecode(t, rec)
		u := body["url"].(string)
		assert.Contains(t, u, "bucket-teste/"+key)
		assert.Contains(t, u, "X-Amz-Signature=")
		exp, err := time.Parse(time.RFC3339, body["expires_at"].(string))
		require.NoError(t, err)
		assert.WithinDuration(t, time.Now().Add(5*time.Minute), exp, 30*time.Second)

		// viewer dono da campanha também recebe.
		assert.Equal(t, http.StatusOK, c.get(viewer, "/detections/"+c.det1.String()+"/evidence-url").Code)
	})

	t.Run("evidence faz streaming do objeto com o content-type guardado", func(t *testing.T) {
		rec := c.get(admin, "/detections/"+c.det1.String()+"/evidence")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, "AUDIO-FALSO", rec.Body.String())
		assert.Equal(t, "audio/mp4", rec.Header().Get("Content-Type"))
		assert.Equal(t, `inline; filename="`+c.det1.String()+`.m4a"`, rec.Header().Get("Content-Disposition"))
	})

	t.Run("evidence sem content-type no objeto cai em audio/mp4", func(t *testing.T) {
		key2 := "evidences/2026/06/10/" + c.emissora.String() + "/" + c.det2.String() + ".bin"
		c.disponibilizaEvidencia(t, c.det2, mcptest.Quando("2026-06-10", "11:30"), key2, []byte("XYZ"), "")
		rec := c.get(admin, "/detections/"+c.det2.String()+"/evidence")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.NotEmpty(t, rec.Header().Get("Content-Type"))
		assert.Equal(t, "XYZ", rec.Body.String())
	})

	t.Run("evidence com o objeto sumido do bucket vira 500", func(t *testing.T) {
		c.s3.FailGet = true
		defer func() { c.s3.FailGet = false }()
		rec := c.get(admin, "/detections/"+c.det1.String()+"/evidence")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}

// ── CreateManual com áudio (multipart) ───────────────────────────────────────

func TestCovA_Detections_CreateManual_ComAudio(t *testing.T) {
	c := covANewDetCen(t)
	admin := c.adminR()
	campos := func(hora string) map[string]string {
		return map[string]string{
			"campaign_id":   c.campanha.String(),
			"station_id":    c.emissora.String(),
			"commercial_id": c.material.String(),
			"detected_at":   mcptest.Quando("2026-06-12", hora).Format(time.RFC3339),
			"note":          "censura enviada pela rádio",
		}
	}

	t.Run("sem claims 401", func(t *testing.T) {
		req := covAMultipart(t, http.MethodPost, "/detections/manual", campos("10:30"))
		rec := covAServe(c.router(nil), req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("JSON inválido 400", func(t *testing.T) {
		rec := covAServe(admin, covAJSON(t, http.MethodPost, "/detections/manual", "{x"))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("áudio com formato não aceito 415 e nada é criado", func(t *testing.T) {
		req := covAMultipart(t, http.MethodPost, "/detections/manual", campos("10:30"),
			covAPart{Field: "audio", Filename: "censura.txt", ContentType: "text/plain", Data: []byte("x")})
		rec := covAServe(admin, req)
		assert.Equal(t, http.StatusUnsupportedMediaType, rec.Code)
		var n int
		require.NoError(t, c.pool.QueryRow(c.ctx, `SELECT count(*) FROM detections WHERE manual_at IS NOT NULL`).Scan(&n))
		assert.Zero(t, n)
	})

	t.Run("áudio mp3 é gravado no S3 e a evidência fica disponível", func(t *testing.T) {
		req := covAMultipart(t, http.MethodPost, "/detections/manual", campos("10:40"),
			covAPart{Field: "audio", Filename: "censura.mp3", ContentType: "audio/mpeg", Data: []byte("BYTES-MP3")})
		rec := covAServe(admin, req)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		body := covADecode(t, rec)
		id := body["id"].(string)
		assert.Equal(t, "available", body["evidence_status"])
		key, _ := body["evidence_key"].(string)
		assert.Equal(t, "evidences/2026/06/12/"+c.emissora.String()+"/"+id+".mp3", key)
		obj, ok := c.s3.Objeto(key)
		require.True(t, ok, "o objeto tem de estar no S3")
		assert.Equal(t, []byte("BYTES-MP3"), obj.Data)
		assert.Equal(t, "audio/mpeg", obj.ContentType)
		assert.EqualValues(t, len("BYTES-MP3"), body["evidence_size_bytes"])
	})

	t.Run("extensão resolvida por nome de arquivo grava content-type canônico", func(t *testing.T) {
		req := covAMultipart(t, http.MethodPost, "/detections/manual", campos("10:50"),
			covAPart{Field: "audio", Filename: "censura.MPEG", ContentType: "video/mpeg", Data: []byte("MPEG")})
		rec := covAServe(admin, req)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		key := covADecode(t, rec)["evidence_key"].(string)
		assert.True(t, strings.HasSuffix(key, ".mp3"))
		obj, _ := c.s3.Objeto(key)
		assert.Equal(t, "audio/mpeg", obj.ContentType)
	})

	t.Run("falha no upload devolve 207 e a veiculação permanece sem áudio", func(t *testing.T) {
		c.s3.FailPut = true
		defer func() { c.s3.FailPut = false }()
		req := covAMultipart(t, http.MethodPost, "/detections/manual", campos("11:00"),
			covAPart{Field: "audio", Filename: "censura.wav", ContentType: "audio/wav", Data: []byte("WAV")})
		rec := covAServe(admin, req)
		require.Equal(t, http.StatusMultiStatus, rec.Code, rec.Body.String())
		body := covADecode(t, rec)
		assert.NotEmpty(t, body["audio_error"])
		assert.Contains(t, body["audio_warning"], "upload do áudio falhou")
		det := body["detection"].(map[string]any)
		assert.Equal(t, "missing", det["evidence_status"])
	})

	t.Run("sem Storage configurado o áudio é ignorado mas a veiculação nasce", func(t *testing.T) {
		h := &DetectionsHandler{Repo: c.h.Repo}
		rt := covARouter(covAClaims("admin", c.admin), func(r chi.Router) { r.Post("/detections/manual", h.CreateManual) })
		req := covAMultipart(t, http.MethodPost, "/detections/manual", campos("11:10"),
			covAPart{Field: "audio", Filename: "censura.mp3", ContentType: "audio/mpeg", Data: []byte("x")})
		rec := covAServe(rt, req)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		assert.Equal(t, "missing", covADecode(t, rec)["evidence_status"])
	})

	t.Run("multipart sem áudio é aceito", func(t *testing.T) {
		req := covAMultipart(t, http.MethodPost, "/detections/manual", campos("11:20"))
		rec := covAServe(admin, req)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	})

	t.Run("material fora do vínculo é recusado", func(t *testing.T) {
		tipo := mcptest.TipoMaterial(t, c.ctx, c.pool, "Solto")
		solto := mcptest.Material(t, c.ctx, c.pool, c.cliente, tipo, "Material Solto")
		corpo := map[string]any{
			"campaign_id": c.campanha.String(), "station_id": c.emissora.String(), "commercial_id": solto.String(),
			"detected_at": mcptest.Quando("2026-06-13", "10:30").Format(time.RFC3339),
		}
		rec := covAServe(admin, covAJSON(t, http.MethodPost, "/detections/manual", corpo))
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), "material is not linked")
	})
}

// ── Export CSV ───────────────────────────────────────────────────────────────

func TestCovA_Detections_Export(t *testing.T) {
	c := covANewDetCen(t)
	admin := c.adminR()

	t.Run("recusas", func(t *testing.T) {
		assert.Equal(t, http.StatusBadRequest, c.get(admin, "/detections/export?campaigns=lixo").Code)
		assert.Equal(t, http.StatusBadRequest, c.get(admin, "/detections/export?from=ontem").Code)
		rec := c.get(admin, "/detections/export?to=amanha")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "invalid to")
	})

	t.Run("uma campanha com período: nome do arquivo usa o cliente", func(t *testing.T) {
		q := url.Values{"campaigns": {c.campanha.String()}, "from": {"2026-06-01T03:00:00Z"}, "to": {"2026-06-30T23:59:59Z"}, "sort": {"detected_at_asc"}}
		rec := c.get(admin, "/detections/export?"+q.Encode())
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, "text/csv; charset=utf-8", rec.Header().Get("Content-Type"))
		cd := rec.Header().Get("Content-Disposition")
		assert.Contains(t, cd, "Cliente-Teste-Veiculacoes-01-06-2026-30-06-2026.csv")

		body := rec.Body.String()
		assert.True(t, strings.HasPrefix(body, "\xEF\xBB\xBF"), "BOM para o Excel pt-BR")
		linhas := strings.Split(strings.TrimSpace(body), "\n")
		assert.Contains(t, linhas[0], "Identificador;Data;Hora;Rádio")
		assert.Contains(t, body, "Spot Teste")
		assert.Contains(t, body, "10/06/2026;10:30:00")
		assert.Contains(t, body, "10/06/2026;11:30:00")
	})

	t.Run("uma campanha sem período usa carimbo de data no nome", func(t *testing.T) {
		rec := c.get(admin, "/detections/export?campaign_id="+c.campanha.String())
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Regexp(t, `Cliente-Teste-Veiculacoes-\d{8}_\d{6}\.csv`, rec.Header().Get("Content-Disposition"))
	})

	t.Run("sem campanha o nome é genérico", func(t *testing.T) {
		rec := c.get(admin, "/detections/export")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Regexp(t, `filename="veiculacoes_\d{8}_\d{6}\.csv"`, rec.Header().Get("Content-Disposition"))
		assert.Contains(t, rec.Body.String(), "Spot Teste")
	})

	t.Run("campanha inexistente cai no nome genérico em vez de 500", func(t *testing.T) {
		rec := c.get(admin, "/detections/export?campaigns="+uuid.NewString())
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Header().Get("Content-Disposition"), "veiculacoes_")
	})

	t.Run("busca textual filtra as linhas", func(t *testing.T) {
		rec := c.get(admin, "/detections/export?q=inexistente-xyz")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.NotContains(t, rec.Body.String(), "Spot Teste")
	})
}

// ── Agregado por material ────────────────────────────────────────────────────

func TestCovA_Detections_AggregateByMaterial(t *testing.T) {
	c := covANewDetCen(t)
	_, campOutra, _ := c.outroCliente(t)
	admin := c.adminR()

	t.Run("recusas", func(t *testing.T) {
		assert.Equal(t, http.StatusBadRequest, c.get(admin, "/detections/aggregate").Code)
		assert.Equal(t, http.StatusBadRequest, c.get(admin, "/detections/aggregate?campaigns=lixo").Code)
		rec := c.get(admin, "/detections/aggregate?campaigns="+c.campanha.String()+"&to=amanha")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "invalid to")
	})

	t.Run("conta tocadas por material", func(t *testing.T) {
		rec := c.get(admin, "/detections/aggregate?campaigns="+c.campanha.String())
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		body := covADecode(t, rec)
		assert.EqualValues(t, 2, body["total_detections"])
		assert.EqualValues(t, 1, body["distinct_materials"])
		rows := body["data"].([]any)
		require.Len(t, rows, 1)
	})

	t.Run("janela e busca textual", func(t *testing.T) {
		q := url.Values{"campaigns": {c.campanha.String()}, "from": {"2026-06-10T00:00:00Z"}, "to": {"2026-06-11T00:00:00Z"}, "q": {"Spot"}}
		assert.EqualValues(t, 2, covADecode(t, c.get(admin, "/detections/aggregate?"+q.Encode()))["total_detections"])
		q.Set("from", "2026-07-01T00:00:00Z")
		q.Set("to", "2026-07-02T00:00:00Z")
		assert.EqualValues(t, 0, covADecode(t, c.get(admin, "/detections/aggregate?"+q.Encode()))["total_detections"])
	})

	t.Run("viewer: campanha alheia não soma", func(t *testing.T) {
		vr := c.router(covAClaims("viewer", uuid.New(), c.cliente))
		rec := c.get(vr, "/detections/aggregate?campaigns="+campOutra.String())
		require.Equal(t, http.StatusOK, rec.Code)
		assert.EqualValues(t, 0, covADecode(t, rec)["total_detections"])
	})

	t.Run("falha do banco 500", func(t *testing.T) {
		h := &DetectionsHandler{Repo: catalog.NewDetections(covAClosedPool(t))}
		rec := covAServe(http.HandlerFunc(h.AggregateByMaterial),
			httptest.NewRequest(http.MethodGet, "/x?campaigns="+c.campanha.String(), nil))
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}

// ── Resumo diário ────────────────────────────────────────────────────────────

func TestCovA_Detections_DailySummary(t *testing.T) {
	c := covANewDetCen(t)
	_, campOutra, _ := c.outroCliente(t)
	admin := c.adminR()
	base := "/campaigns/" + c.campanha.String() + "/daily-summary"

	t.Run("recusas", func(t *testing.T) {
		assert.Equal(t, http.StatusBadRequest, c.get(admin, "/campaigns/xx/daily-summary?from=2026-06-01&to=2026-06-30").Code)
		rec := c.get(admin, base+"?from=01/06/2026&to=2026-06-30")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "from must be YYYY-MM-DD")
		rec = c.get(admin, base+"?from=2026-06-01&to=fim")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "to must be YYYY-MM-DD")
	})

	t.Run("devolve esperado e executado da célula", func(t *testing.T) {
		rec := c.get(admin, base+"?from=2026-06-10&to=2026-06-10")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var rows []map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rows))
		require.Len(t, rows, 1)
		assert.EqualValues(t, 1, rows[0]["expected"])
		assert.EqualValues(t, 1, rows[0]["in_slot"])
		assert.EqualValues(t, 1, rows[0]["bonus"])
		assert.EqualValues(t, 0, rows[0]["deficit"])
	})

	t.Run("campanha sem regra devolve lista vazia, não null", func(t *testing.T) {
		rec := c.get(admin, "/campaigns/"+campOutra.String()+"/daily-summary?from=2026-06-20&to=2026-06-21")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "[]", strings.TrimSpace(rec.Body.String()))
	})

	t.Run("viewer", func(t *testing.T) {
		vr := c.router(covAClaims("viewer", uuid.New(), c.cliente))
		assert.Equal(t, http.StatusOK, c.get(vr, base+"?from=2026-06-10&to=2026-06-10").Code)
		assert.Equal(t, http.StatusNotFound, c.get(vr, "/campaigns/"+campOutra.String()+"/daily-summary?from=2026-06-10&to=2026-06-10").Code)
		assert.Equal(t, http.StatusNotFound, c.get(vr, "/campaigns/"+uuid.NewString()+"/daily-summary?from=2026-06-10&to=2026-06-10").Code)
	})

	t.Run("falha do banco 500", func(t *testing.T) {
		h := &DetectionsHandler{SummaryRepo: catalog.NewDailySummary(covAClosedPool(t))}
		rt := covARouter(covAClaims("admin", c.admin), func(r chi.Router) { r.Get("/campaigns/{campaignID}/daily-summary", h.DailySummary) })
		assert.Equal(t, http.StatusInternalServerError, c.get(rt, base+"?from=2026-06-10&to=2026-06-10").Code)
	})
}
