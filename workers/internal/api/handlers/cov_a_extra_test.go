package handlers

// cov_a_extra_test.go — complementos: tradução de recusas de negócio em HTTP,
// defaults de content-type, limites de upload e os caminhos "banco fora do ar"
// das rotas de veiculação/escopo de viewer.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/acoes"
	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

// ── escreveRecusa: um código de negócio vira o status/corpo HTTP de sempre ───

func TestCovA_EscreveRecusa_TabelaDeCodigos(t *testing.T) {
	casos := []struct {
		nome   string
		err    error
		status int
		corpo  string // trecho esperado
	}{
		{"não encontrado", &acoes.Recusa{Codigo: acoes.CodigoNaoEncontrado, Mensagem: "x"}, 404, "not found"},
		{"cnpj duplicado", &acoes.Recusa{Codigo: acoes.CodigoCNPJDuplicado, Dados: map[string]any{"existente": "Cliente Teste"}}, 409, "Cliente Teste"},
		{"estado terminal", &acoes.Recusa{Codigo: acoes.CodigoEstadoTerminal, Dados: map[string]any{"estado_atual": "cancelada"}}, 409, "campaign already in terminal state"},
		{"campanha com projeções (com contagem)", &acoes.Recusa{Codigo: acoes.CodigoCampanhaComProjecoes, Dados: map[string]any{"foreign_projections": 7}}, 409, `"foreign_projections":7`},
		{"campanha com projeções (sem contagem)", &acoes.Recusa{Codigo: acoes.CodigoCampanhaComProjecoes}, 409, "campaign_has_foreign_projections"},
		{"cliente com vínculos", &acoes.Recusa{Codigo: acoes.CodigoClienteComVinculos}, 409, "client_has_dependents"},
		{"material vinculado", &acoes.Recusa{Codigo: acoes.CodigoMaterialVinculado}, 409, acoes.CodigoMaterialVinculado},
		{"campanha com lote manual", &acoes.Recusa{Codigo: acoes.CodigoCampanhaComLoteManual}, 409, acoes.CodigoCampanhaComLoteManual},
		{"campanha em pós-venda", &acoes.Recusa{Codigo: acoes.CodigoCampanhaEmPosVenda}, 409, acoes.CodigoCampanhaEmPosVenda},
		{"material não vinculado", &acoes.Recusa{Codigo: acoes.CodigoMaterialNaoVinculado, Mensagem: "material fora do vínculo"}, 422, "material fora do vínculo"},
		{"hub", &acoes.Recusa{Codigo: acoes.CodigoHub, Mensagem: "código desconhecido no hub"}, 422, "código desconhecido no hub"},
		{"entrada inválida cai no 400 padrão", &acoes.Recusa{Codigo: acoes.CodigoEntradaInvalida, Mensagem: "nome obrigatório"}, 400, "nome obrigatório"},
		{"código novo não mapeado também é 400", &acoes.Recusa{Codigo: "codigo_futuro", Mensagem: "pedido ruim"}, 400, "pedido ruim"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			rec := httptest.NewRecorder()
			require.True(t, escreveRecusa(rec, c.err))
			assert.Equal(t, c.status, rec.Code)
			assert.Contains(t, rec.Body.String(), c.corpo)
		})
	}

	t.Run("erro que não é Recusa não é tratado (quem chama responde 500)", func(t *testing.T) {
		rec := httptest.NewRecorder()
		assert.False(t, escreveRecusa(rec, assert.AnError))
		assert.Equal(t, 200, rec.Code, "nada foi escrito")
		assert.Empty(t, rec.Body.String())
	})

	t.Run("Recusa embrulhada é reconhecida", func(t *testing.T) {
		rec := httptest.NewRecorder()
		err := &acoes.Recusa{Codigo: acoes.CodigoNaoEncontrado}
		require.True(t, escreveRecusa(rec, wrapErr{err}))
		assert.Equal(t, 404, rec.Code)
	})
}

type wrapErr struct{ inner error }

func (w wrapErr) Error() string { return "embrulho: " + w.inner.Error() }
func (w wrapErr) Unwrap() error { return w.inner }

// ── Veiculações: banco fora do ar, multipart quebrado, limites ──────────────

func TestCovA_Detections_BancoForaDoAr(t *testing.T) {
	c := covANewDetCen(t)
	bad := &DetectionsHandler{
		Repo: catalog.NewDetections(covAClosedPool(t)), CampaignRepo: catalog.NewCampaigns(covAClosedPool(t)), Storage: c.h.Storage,
		SummaryRepo: catalog.NewDailySummary(covAClosedPool(t)),
	}
	id := c.det1.String()

	montar := func(claims *auth.Claims) chi.Router {
		return covARouter(claims, func(r chi.Router) {
			r.Get("/detections/{id}", bad.Get)
			r.Get("/detections/{id}/evidence-url", bad.EvidenceURL)
			r.Get("/detections/{id}/evidence", bad.Evidence)
			r.Post("/detections/{id}/ignore", bad.Ignore)
			r.Post("/detections/{id}/restore", bad.Restore)
			r.Post("/detections/manual", bad.CreateManual)
			r.Post("/detections/{id}/evidence", bad.UploadEvidence)
			r.Get("/campaigns/{campaignID}/daily-summary", bad.DailySummary)
		})
	}
	for _, claims := range []*auth.Claims{covAClaims("admin", c.admin), covAClaims("viewer", c.admin, c.cliente)} {
		r := montar(claims)
		t.Run(claims.Role, func(t *testing.T) {
			for _, rota := range []string{"/detections/" + id, "/detections/" + id + "/evidence-url", "/detections/" + id + "/evidence"} {
				assert.Equal(t, http.StatusInternalServerError, c.get(r, rota).Code, rota)
			}
			assert.Equal(t, http.StatusInternalServerError, c.get(r, "/campaigns/"+c.campanha.String()+"/daily-summary?from=2026-06-01&to=2026-06-02").Code,
				"o viewer falha na checagem de posse; o admin, na consulta do resumo")
		})
	}

	r := montar(covAClaims("admin", c.admin))
	t.Run("ignorar, restaurar, criar manual e subir censura viram 500", func(t *testing.T) {
		assert.Equal(t, http.StatusInternalServerError, covAServe(r, covAJSON(t, http.MethodPost, "/detections/"+id+"/ignore", nil)).Code)
		assert.Equal(t, http.StatusInternalServerError, covAServe(r, covAJSON(t, http.MethodPost, "/detections/"+id+"/restore", nil)).Code)
		corpo := c.manualJSON("2026-06-14", "10:30")
		assert.Equal(t, http.StatusInternalServerError, covAServe(r, covAJSON(t, http.MethodPost, "/detections/manual", corpo)).Code)
		req := covAMultipart(t, http.MethodPost, "/detections/"+id+"/evidence", nil,
			covAPart{Field: "audio", Filename: "a.mp3", ContentType: "audio/mpeg", Data: []byte("x")})
		assert.Equal(t, http.StatusInternalServerError, covAServe(r, req).Code)
	})
}

func (c *covADetCen) manualJSON(dia, hora string) map[string]any {
	return map[string]any{
		"campaign_id": c.campanha.String(), "station_id": c.emissora.String(), "commercial_id": c.material.String(),
		"detected_at": mcptest.Quando(dia, hora).Format(time.RFC3339),
	}
}

func TestCovA_Detections_MultipartQuebradoEUploadGrande(t *testing.T) {
	c := covANewDetCen(t)
	admin := c.adminR()

	t.Run("CreateManual com multipart corrompido 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/detections/manual", strings.NewReader("isto-nao-e-multipart"))
		req.Header.Set("Content-Type", "multipart/form-data; boundary=xyz")
		rec := covAServe(admin, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "invalid multipart payload")
	})

	t.Run("UploadEvidence com áudio acima de 25MB 413", func(t *testing.T) {
		grande := bytes.Repeat([]byte{'a'}, manualAudioMaxBytes+1)
		req := covAMultipart(t, http.MethodPost, "/detections/"+c.det1.String()+"/evidence", nil,
			covAPart{Field: "audio", Filename: "grande.mp3", ContentType: "audio/mpeg", Data: grande})
		rec := covAServe(admin, req)
		assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, rec.Body.String())
		assert.Zero(t, c.s3.Quantos(), "nada vai ao S3")
	})

	t.Run("campanhas com vírgulas sobrando são toleradas", func(t *testing.T) {
		rec := c.get(admin, "/detections?page=1&campaigns="+c.campanha.String()+",,%20,")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.EqualValues(t, 2, covADecode(t, rec)["total"])
	})
}

func TestCovA_Detections_ContentTypeAusenteUsaDefault(t *testing.T) {
	c := covANewDetCen(t)
	admin := c.adminR()

	// Evidência sem Content-Type no objeto: o handler assume audio/mp4.
	key := "evidences/2026/06/10/" + c.emissora.String() + "/" + c.det1.String() + ".bin"
	c.disponibilizaEvidencia(t, c.det1, mcptest.Quando("2026-06-10", "10:30"), key, []byte("XYZ"), "")
	rec := c.get(admin, "/detections/"+c.det1.String()+"/evidence")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "audio/mp4", rec.Header().Get("Content-Type"))

	// Comprovante sem Content-Type: application/pdf.
	meta := covABatchMeta(t, c, covABatchEntry(c, "2026-06-22", "10:30"))
	resp := covAServe(admin, covAMultipart(t, http.MethodPost, "/detections/manual-batch", meta,
		covAPart{Field: "proof", Filename: "c.pdf", ContentType: "application/pdf", Data: covAPDF()}))
	require.Equal(t, http.StatusCreated, resp.Code, resp.Body.String())
	body := covADecode(t, resp)
	detID := body["detections"].([]any)[0].(map[string]any)["id"].(string)
	var proofKey string
	require.NoError(t, c.pool.QueryRow(c.ctx,
		`SELECT proof_pdf_key FROM manual_proof_batches WHERE id=$1`, body["batch_id"].(string)).Scan(&proofKey))
	c.s3.Poe(proofKey, covAPDF(), "")
	rec = c.get(admin, "/detections/"+detID+"/proof")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/pdf", rec.Header().Get("Content-Type"))
}

// ── Escopo de viewer com banco fora do ar (rotas de leitura por campanha) ────

func TestCovA_ViewerComBancoForaDoAr(t *testing.T) {
	c := covANewCampCen(t)
	viewer := covAClaims("viewer", uuid.New(), c.cliA)

	mats := &CampaignMaterialsHandler{Repo: catalog.NewCampaignMaterials(c.pool), CampaignRepo: catalog.NewCampaigns(covAClosedPool(t))}
	regras := &DistributionRulesHandler{Repo: catalog.NewDistributionRules(c.pool), CampaignRepo: catalog.NewCampaigns(covAClosedPool(t))}
	r := covARouter(viewer, func(r chi.Router) {
		r.Get("/campaigns/{campaignID}/materials", mats.ListByCampaign)
		r.Get("/campaigns/{campaignID}/distribution-rules", regras.ListByCampaign)
	})
	for _, sub := range []string{"materials", "distribution-rules"} {
		rec := c.get(r, "/campaigns/"+c.campA1.String()+"/"+sub)
		assert.Equal(t, http.StatusInternalServerError, rec.Code, sub)
	}
}

func TestCovA_Clients_ListaPaginadaVaziaTemUmaPagina(t *testing.T) {
	ctx, pool := covADB(t)
	mcptest.Cliente(t, ctx, pool, "Cliente Teste")
	h := &ClientsHandler{Repo: catalog.NewClients(pool)}
	r := covAClientesRouter(h, covAClaims("admin", uuid.New()))
	rec := covAServe(r, httptest.NewRequest(http.MethodGet, "/clients?q=nada-que-case", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	body := covADecode(t, rec)
	assert.EqualValues(t, 0, body["total"])
	assert.EqualValues(t, 1, body["total_pages"], "lista vazia ainda tem 1 página para o paginador")
}

// ── Áudio da peça legada: content-type por extensão ──────────────────────────

func TestCovA_Commercials_AudioContentTypePorExtensao(t *testing.T) {
	c := covANewComCen(t)
	for _, ext := range []string{".mp3", ".m4a", ".aac", ".wav"} {
		t.Run(ext, func(t *testing.T) {
			com := c.semeia(t, "Spot "+ext, "peca"+ext, []byte("x"))
			rec := c.pedido(http.MethodGet, "/commercials/"+com.ID.String()+"/audio")
			require.Equal(t, http.StatusOK, rec.Code)
			assert.True(t, strings.HasPrefix(rec.Header().Get("Content-Type"), "audio/"),
				"Content-Type de %s = %q", ext, rec.Header().Get("Content-Type"))
			assert.Contains(t, rec.Header().Get("Content-Disposition"), "Spot "+ext+ext)
		})
	}
}
