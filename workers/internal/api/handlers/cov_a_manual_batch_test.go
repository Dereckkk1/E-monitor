package handlers

// cov_a_manual_batch_test.go — veiculações manuais em lote com comprovante PDF,
// upload tardio de censura e leitura do comprovante (URL pré-assinada e bytes).
// Banco real + S3 falso.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

// covABatchMeta monta o JSON do campo `meta` do multipart.
func covABatchMeta(t *testing.T, c *covADetCen, entries ...map[string]any) map[string]string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"campaign_id": c.campanha.String(),
		"station_id":  c.emissora.String(),
		"note":        "lote de teste",
		"entries":     entries,
	})
	require.NoError(t, err)
	return map[string]string{"meta": string(b)}
}

func covABatchEntry(c *covADetCen, dia, hora string) map[string]any {
	return map[string]any{
		"commercial_id": c.material.String(),
		"detected_at":   mcptest.Quando(dia, hora).Format(time.RFC3339),
		"note":          "linha de teste",
	}
}

func covAPDF() []byte { return []byte("%PDF-1.4\n%comprovante de teste\n") }

func TestCovA_ManualBatch_Recusas(t *testing.T) {
	c := covANewDetCen(t)
	admin := c.adminR()
	post := func(r http.Handler, req *http.Request) *httptest.ResponseRecorder { return covAServe(r, req) }

	t.Run("sem claims 401", func(t *testing.T) {
		req := covAMultipart(t, http.MethodPost, "/detections/manual-batch", covABatchMeta(t, c, covABatchEntry(c, "2026-06-15", "10:30")))
		assert.Equal(t, http.StatusUnauthorized, post(c.router(nil), req).Code)
	})

	t.Run("corpo que não é multipart 400", func(t *testing.T) {
		rec := post(admin, covAJSON(t, http.MethodPost, "/detections/manual-batch", map[string]any{"a": 1}))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "invalid multipart payload")
	})

	t.Run("meta não-JSON 400", func(t *testing.T) {
		rec := post(admin, covAMultipart(t, http.MethodPost, "/detections/manual-batch", map[string]string{"meta": "{x"}))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "invalid meta json")
	})

	t.Run("campanha/emissora zeradas 400", func(t *testing.T) {
		b, _ := json.Marshal(map[string]any{"entries": []any{}})
		rec := post(admin, covAMultipart(t, http.MethodPost, "/detections/manual-batch", map[string]string{"meta": string(b)}))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "campaign_id and station_id are required")
	})

	t.Run("lote vazio 400", func(t *testing.T) {
		rec := post(admin, covAMultipart(t, http.MethodPost, "/detections/manual-batch", covABatchMeta(t, c)))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "entries must not be empty")
	})

	t.Run("erros sintáticos por linha viram 422 com índice", func(t *testing.T) {
		meta := covABatchMeta(t, c,
			map[string]any{"detected_at": mcptest.Quando("2026-06-15", "10:30").Format(time.RFC3339)},                           // sem material
			map[string]any{"commercial_id": c.material.String()},                                                                // sem horário
			map[string]any{"commercial_id": c.material.String(), "detected_at": time.Now().Add(time.Hour).Format(time.RFC3339)}, // futuro
			covABatchEntry(c, "2026-06-15", "10:30"),                                                                            // válida
		)
		rec := post(admin, covAMultipart(t, http.MethodPost, "/detections/manual-batch", meta))
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
		errs := covADecode(t, rec)["errors"].([]any)
		require.Len(t, errs, 3)
		msgs := map[int]string{}
		for _, e := range errs {
			m := e.(map[string]any)
			msgs[int(m["index"].(float64))] = m["message"].(string)
		}
		assert.Equal(t, "material obrigatório", msgs[0])
		assert.Equal(t, "horário obrigatório", msgs[1])
		assert.Equal(t, "horário não pode ser no futuro", msgs[2])
	})

	t.Run("material não vinculado à emissora vira 422 e nada é criado", func(t *testing.T) {
		tipo := mcptest.TipoMaterial(t, c.ctx, c.pool, "Solto")
		solto := mcptest.Material(t, c.ctx, c.pool, c.cliente, tipo, "Material Solto")
		meta := covABatchMeta(t, c,
			covABatchEntry(c, "2026-06-15", "10:30"),
			map[string]any{"commercial_id": solto.String(), "detected_at": mcptest.Quando("2026-06-15", "10:40").Format(time.RFC3339)},
		)
		rec := post(admin, covAMultipart(t, http.MethodPost, "/detections/manual-batch", meta))
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
		errs := covADecode(t, rec)["errors"].([]any)
		require.Len(t, errs, 1)
		assert.EqualValues(t, 1, errs[0].(map[string]any)["index"])
		assert.Contains(t, errs[0].(map[string]any)["message"], "não vinculado")
		var n int
		require.NoError(t, c.pool.QueryRow(c.ctx, `SELECT count(*) FROM detections WHERE manual_at IS NOT NULL`).Scan(&n))
		assert.Zero(t, n, "tudo-ou-nada: a linha boa também não é criada")
	})

	t.Run("PDF com content-type errado 415", func(t *testing.T) {
		req := covAMultipart(t, http.MethodPost, "/detections/manual-batch", covABatchMeta(t, c, covABatchEntry(c, "2026-06-15", "10:30")),
			covAPart{Field: "proof", Filename: "comprovante.png", ContentType: "image/png", Data: []byte("png")})
		rec := post(admin, req)
		assert.Equal(t, http.StatusUnsupportedMediaType, rec.Code)
		assert.Contains(t, rec.Body.String(), "precisa ser PDF")
	})

	t.Run("PDF acima de 25MB 413", func(t *testing.T) {
		grande := bytes.Repeat([]byte{'a'}, manualProofMaxBytes+1)
		req := covAMultipart(t, http.MethodPost, "/detections/manual-batch", covABatchMeta(t, c, covABatchEntry(c, "2026-06-15", "10:30")),
			covAPart{Field: "proof", Filename: "grande.pdf", ContentType: "application/pdf", Data: grande})
		rec := post(admin, req)
		assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
		assert.Contains(t, rec.Body.String(), "acima de 25MB")
	})
}

func TestCovA_ManualBatch_Sucesso(t *testing.T) {
	c := covANewDetCen(t)
	admin := c.adminR()

	t.Run("sem PDF nem áudio: 201, duas linhas, sem batch_id", func(t *testing.T) {
		meta := covABatchMeta(t, c, covABatchEntry(c, "2026-06-15", "10:30"), covABatchEntry(c, "2026-06-15", "10:40"))
		rec := covAServe(admin, covAMultipart(t, http.MethodPost, "/detections/manual-batch", meta))
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		body := covADecode(t, rec)
		assert.Nil(t, body["batch_id"])
		assert.Empty(t, body["warnings"])
		dets := body["detections"].([]any)
		require.Len(t, dets, 2)
		for _, d := range dets {
			assert.Equal(t, "missing", d.(map[string]any)["evidence_status"])
		}
		var porAdmin int
		require.NoError(t, c.pool.QueryRow(c.ctx, `SELECT count(*) FROM detections WHERE manual_by = $1 AND proof_batch_id IS NULL`, c.admin).Scan(&porAdmin))
		assert.Equal(t, 2, porAdmin)
		// A cota do dia (1/dia) fecha uma como dentro da faixa e a outra como bônus.
		var inSlot, bonus int
		require.NoError(t, c.pool.QueryRow(c.ctx, `
			SELECT count(*) FILTER (WHERE dc.category='in_slot'), count(*) FILTER (WHERE dc.category='bonus')
			  FROM detection_campaigns dc JOIN detections d ON d.id = dc.detection_id AND d.detected_at = dc.detected_at
			 WHERE d.manual_by = $1 AND d.detected_at::date = '2026-06-15'`, c.admin).Scan(&inSlot, &bonus))
		assert.Equal(t, 1, inSlot)
		assert.Equal(t, 1, bonus)
	})

	t.Run("com PDF e áudios por linha: tudo no S3, avisos para o que falhou", func(t *testing.T) {
		meta := covABatchMeta(t, c,
			covABatchEntry(c, "2026-06-16", "10:30"),
			covABatchEntry(c, "2026-06-16", "10:40"),
			covABatchEntry(c, "2026-06-16", "10:50"),
		)
		req := covAMultipart(t, http.MethodPost, "/detections/manual-batch", meta,
			covAPart{Field: "proof", Filename: "comprovante.pdf", ContentType: "application/pdf", Data: covAPDF()},
			covAPart{Field: "audio_0", Filename: "a0.mp3", ContentType: "audio/mpeg", Data: []byte("AUDIO-0")},
			covAPart{Field: "audio_1", Filename: "a1.doc", ContentType: "application/msword", Data: []byte("nao-e-audio")},
			// audio_2 ausente: a linha 2 fica sem censura, sem aviso.
		)
		rec := covAServe(admin, req)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		body := covADecode(t, rec)

		batchID, _ := body["batch_id"].(string)
		require.NotEmpty(t, batchID)
		proofKey := fmt.Sprintf("proofs/2026/06/16/%s/%s.pdf", c.emissora, batchID)
		pdf, ok := c.s3.Objeto(proofKey)
		require.True(t, ok, "PDF no S3 sob %s", proofKey)
		assert.Equal(t, covAPDF(), pdf.Data)
		assert.Equal(t, "application/pdf", pdf.ContentType)

		dets := body["detections"].([]any)
		require.Len(t, dets, 3)
		d0 := dets[0].(map[string]any)
		assert.Equal(t, "available", func() any {
			// A resposta é o estado ANTES do upload do áudio: relê do banco.
			det, err := c.h.Repo.Get(c.ctx, uuid.MustParse(d0["id"].(string)))
			require.NoError(t, err)
			return det.EvidenceStatus
		}())
		audio, ok := c.s3.Objeto(fmt.Sprintf("evidences/2026/06/16/%s/%s.mp3", c.emissora, d0["id"]))
		require.True(t, ok)
		assert.Equal(t, []byte("AUDIO-0"), audio.Data)

		warns := body["warnings"].([]any)
		require.Len(t, warns, 1)
		assert.Equal(t, "linha 1: formato de áudio não suportado", warns[0])

		var key string
		var size int64
		require.NoError(t, c.pool.QueryRow(c.ctx, `SELECT proof_pdf_key, proof_pdf_size FROM manual_proof_batches WHERE id = $1`, batchID).Scan(&key, &size))
		assert.Equal(t, proofKey, key)
		assert.EqualValues(t, len(covAPDF()), size)
		var nVinc int
		require.NoError(t, c.pool.QueryRow(c.ctx, `SELECT count(*) FROM detections WHERE proof_batch_id = $1`, batchID).Scan(&nVinc))
		assert.Equal(t, 3, nVinc, "todas as linhas apontam para o lote")
	})

	t.Run("falha no S3: veiculações criadas, sem lote, com avisos", func(t *testing.T) {
		c.s3.FailPut = true
		defer func() { c.s3.FailPut = false }()
		meta := covABatchMeta(t, c, covABatchEntry(c, "2026-06-17", "10:30"))
		req := covAMultipart(t, http.MethodPost, "/detections/manual-batch", meta,
			covAPart{Field: "proof", Filename: "comprovante.pdf", ContentType: "application/pdf", Data: covAPDF()},
			covAPart{Field: "audio_0", Filename: "a0.mp3", ContentType: "audio/mpeg", Data: []byte("AUDIO")},
		)
		rec := covAServe(admin, req)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		body := covADecode(t, rec)
		assert.Nil(t, body["batch_id"])
		warns := body["warnings"].([]any)
		require.Len(t, warns, 2)
		assert.Contains(t, warns[0], "upload do PDF comprovante falhou")
		assert.Equal(t, "linha 0: upload do áudio falhou", warns[1])
		assert.Len(t, body["detections"].([]any), 1)
	})

	t.Run("sem Storage: PDF e áudio são ignorados em silêncio", func(t *testing.T) {
		h := &DetectionsHandler{Repo: c.h.Repo}
		rt := covARouterDet(c, h)
		meta := covABatchMeta(t, c, covABatchEntry(c, "2026-06-18", "10:30"))
		req := covAMultipart(t, http.MethodPost, "/detections/manual-batch", meta,
			covAPart{Field: "proof", Filename: "comprovante.pdf", ContentType: "application/pdf", Data: covAPDF()},
			covAPart{Field: "audio_0", Filename: "a0.mp3", ContentType: "audio/mpeg", Data: []byte("AUDIO")},
		)
		rec := covAServe(rt, req)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		body := covADecode(t, rec)
		assert.Nil(t, body["batch_id"])
		assert.Empty(t, body["warnings"])
	})

	t.Run("áudio acima de 25MB gera aviso e não sobe", func(t *testing.T) {
		meta := covABatchMeta(t, c, covABatchEntry(c, "2026-06-19", "10:30"))
		req := covAMultipart(t, http.MethodPost, "/detections/manual-batch", meta,
			covAPart{Field: "audio_0", Filename: "grande.mp3", ContentType: "audio/mpeg", Data: bytes.Repeat([]byte{'a'}, manualAudioMaxBytes+1)})
		rec := covAServe(admin, req)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		warns := covADecode(t, rec)["warnings"].([]any)
		require.Len(t, warns, 1)
		assert.Equal(t, "linha 0: áudio acima de 25MB", warns[0])
	})
}

// covARouterDet monta o roteador de manual-batch para um handler alternativo.
func covARouterDet(c *covADetCen, h *DetectionsHandler) http.Handler {
	old := c.h
	c.h = h
	defer func() { c.h = old }()
	return c.adminR()
}

// ── UploadEvidence ───────────────────────────────────────────────────────────

func TestCovA_UploadEvidence(t *testing.T) {
	c := covANewDetCen(t)
	admin := c.adminR()
	alvo := func(id uuid.UUID) string { return "/detections/" + id.String() + "/evidence" }
	audio := covAPart{Field: "audio", Filename: "censura.mp3", ContentType: "audio/mpeg", Data: []byte("CENSURA")}

	t.Run("id inválido 400", func(t *testing.T) {
		rec := covAServe(admin, covAMultipart(t, http.MethodPost, "/detections/xx/evidence", nil, audio))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
	t.Run("veiculação inexistente 404", func(t *testing.T) {
		rec := covAServe(admin, covAMultipart(t, http.MethodPost, alvo(uuid.New()), nil, audio))
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
	t.Run("multipart inválido 400", func(t *testing.T) {
		rec := covAServe(admin, covAJSON(t, http.MethodPost, alvo(c.det1), map[string]any{"x": 1}))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "invalid multipart payload")
	})
	t.Run("sem o campo audio 400", func(t *testing.T) {
		rec := covAServe(admin, covAMultipart(t, http.MethodPost, alvo(c.det1), map[string]string{"x": "y"}))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "'audio' obrigatório")
	})
	t.Run("formato não suportado 415", func(t *testing.T) {
		rec := covAServe(admin, covAMultipart(t, http.MethodPost, alvo(c.det1), nil,
			covAPart{Field: "audio", Filename: "x.exe", ContentType: "application/octet-stream", Data: []byte("x")}))
		assert.Equal(t, http.StatusUnsupportedMediaType, rec.Code)
	})
	t.Run("sem Storage 500", func(t *testing.T) {
		h := &DetectionsHandler{Repo: c.h.Repo}
		rec := covAServe(covARouterDet(c, h), covAMultipart(t, http.MethodPost, alvo(c.det1), nil, audio))
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Contains(t, rec.Body.String(), "storage indisponível")
	})
	t.Run("falha no S3 500 e a veiculação segue sem áudio", func(t *testing.T) {
		c.s3.FailPut = true
		defer func() { c.s3.FailPut = false }()
		rec := covAServe(admin, covAMultipart(t, http.MethodPost, alvo(c.det1), nil, audio))
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Contains(t, rec.Body.String(), "falha no upload")
		det, err := c.h.Repo.Get(c.ctx, c.det1)
		require.NoError(t, err)
		assert.NotEqual(t, "available", det.EvidenceStatus)
	})
	t.Run("sucesso anexa o áudio e devolve a veiculação atualizada", func(t *testing.T) {
		rec := covAServe(admin, covAMultipart(t, http.MethodPost, alvo(c.det1), nil, audio))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		body := covADecode(t, rec)
		assert.Equal(t, "available", body["evidence_status"])
		key := body["evidence_key"].(string)
		assert.Equal(t, fmt.Sprintf("evidences/2026/06/10/%s/%s.mp3", c.emissora, c.det1), key)
		obj, ok := c.s3.Objeto(key)
		require.True(t, ok)
		assert.Equal(t, []byte("CENSURA"), obj.Data)
		assert.EqualValues(t, len("CENSURA"), body["evidence_size_bytes"])
	})
	t.Run("segunda tentativa na mesma veiculação 409", func(t *testing.T) {
		rec := covAServe(admin, covAMultipart(t, http.MethodPost, alvo(c.det1), nil, audio))
		assert.Equal(t, http.StatusConflict, rec.Code)
		assert.Contains(t, rec.Body.String(), "já tem censura")
	})
}

// ── Comprovante (ProofURL / Proof) ───────────────────────────────────────────

func TestCovA_Proof(t *testing.T) {
	c := covANewDetCen(t)
	admin := c.adminR()

	// Cria um lote com PDF pelo próprio handler e usa o id da primeira linha.
	meta := covABatchMeta(t, c, covABatchEntry(c, "2026-06-20", "10:30"))
	rec := covAServe(admin, covAMultipart(t, http.MethodPost, "/detections/manual-batch", meta,
		covAPart{Field: "proof", Filename: "comprovante.pdf", ContentType: "application/pdf", Data: covAPDF()}))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	body := covADecode(t, rec)
	batchID := body["batch_id"].(string)
	detID := body["detections"].([]any)[0].(map[string]any)["id"].(string)

	for _, rota := range []string{"proof-url", "proof"} {
		t.Run(rota+": id inválido 400", func(t *testing.T) {
			assert.Equal(t, http.StatusBadRequest, c.get(admin, "/detections/xx/"+rota).Code)
		})
		t.Run(rota+": veiculação sem lote 404", func(t *testing.T) {
			r := c.get(admin, "/detections/"+c.det1.String()+"/"+rota)
			assert.Equal(t, http.StatusNotFound, r.Code)
			assert.Contains(t, r.Body.String(), "sem comprovante")
		})
		t.Run(rota+": sem Storage 500", func(t *testing.T) {
			h := &DetectionsHandler{Repo: c.h.Repo}
			r := c.get(covARouterDet(c, h), "/detections/"+detID+"/"+rota)
			assert.Equal(t, http.StatusInternalServerError, r.Code)
			assert.Contains(t, r.Body.String(), "storage indisponível")
		})
	}

	t.Run("proof-url devolve link assinado do PDF do lote", func(t *testing.T) {
		r := c.get(admin, "/detections/"+detID+"/proof-url")
		require.Equal(t, http.StatusOK, r.Code, r.Body.String())
		b := covADecode(t, r)
		assert.Contains(t, b["url"], batchID+".pdf")
		assert.Contains(t, b["url"], "X-Amz-Signature=")
		_, err := time.Parse(time.RFC3339, b["expires_at"].(string))
		assert.NoError(t, err)
	})

	t.Run("proof devolve os bytes do PDF com cache privado", func(t *testing.T) {
		r := c.get(admin, "/detections/"+detID+"/proof")
		require.Equal(t, http.StatusOK, r.Code, r.Body.String())
		assert.Equal(t, covAPDF(), r.Body.Bytes())
		assert.Equal(t, "application/pdf", r.Header().Get("Content-Type"))
		assert.Equal(t, "private, max-age=300", r.Header().Get("Cache-Control"))
		assert.Equal(t, `inline; filename="`+detID+`.pdf"`, r.Header().Get("Content-Disposition"))
	})

	t.Run("proof com objeto sem content-type usa application/pdf", func(t *testing.T) {
		var key string
		require.NoError(t, c.pool.QueryRow(c.ctx, `SELECT proof_pdf_key FROM manual_proof_batches WHERE id=$1`, batchID).Scan(&key))
		c.s3.Poe(key, covAPDF(), "")
		r := c.get(admin, "/detections/"+detID+"/proof")
		require.Equal(t, http.StatusOK, r.Code)
		assert.NotEmpty(t, r.Header().Get("Content-Type"))
	})

	t.Run("proof com falha no S3 500", func(t *testing.T) {
		c.s3.FailGet = true
		defer func() { c.s3.FailGet = false }()
		assert.Equal(t, http.StatusInternalServerError, c.get(admin, "/detections/"+detID+"/proof").Code)
	})

	t.Run("falha do banco 500", func(t *testing.T) {
		h := &DetectionsHandler{Repo: catalog.NewDetections(covAClosedPool(t)), Storage: c.h.Storage}
		for _, rota := range []string{"proof-url", "proof"} {
			r := c.get(covARouterDet(c, h), "/detections/"+detID+"/"+rota)
			assert.Equal(t, http.StatusInternalServerError, r.Code, rota)
		}
	})
}
