package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
	"radiocheck/internal/postsale"
	"radiocheck/internal/users"
)

// covBPSStore é o ObjectStore em memória do serviço de pós-venda.
type covBPSStore struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func (s *covBPSStore) Put(_ context.Context, key string, body io.Reader, _ string) error {
	b, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.objects == nil {
		s.objects = map[string][]byte{}
	}
	s.objects[key] = b
	return nil
}

func (s *covBPSStore) Get(_ context.Context, key string) (io.ReadCloser, string, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.objects[key]
	if !ok {
		return nil, "", 0, errors.New("objeto inexistente")
	}
	ct := ""
	if strings.HasSuffix(key, ".png") {
		ct = "image/png"
	}
	return io.NopCloser(bytes.NewReader(b)), ct, int64(len(b)), nil
}

func (s *covBPSStore) del(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
}

// covBMailer registra os destinatários dos emails enviados.
type covBMailer struct {
	mu   sync.Mutex
	sent []string
}

func (m *covBMailer) Send(_ context.Context, to []string, _, _, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, to...)
	return nil
}

func (m *covBMailer) recipients() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.sent...)
}

type covBPS struct {
	ctx      context.Context
	pool     *pgxpool.Pool
	svc      *postsale.Service
	h        *PostSaleHandler
	pub      *PostSalePublicHandler
	store    *covBPSStore
	mail     *covBMailer
	admin    uuid.UUID
	client   uuid.UUID
	campaign uuid.UUID
	station  uuid.UUID
}

// covBPSSetup semeia 1 cliente, 1 emissora, 1 campanha (junho/2026) com regra e
// 2 veiculações in_slot, e monta serviço + handlers de pós-venda com fakes.
func covBPSSetup(t *testing.T) *covBPS {
	t.Helper()
	ctx, pool := covBDB(t)
	s := &covBPS{ctx: ctx, pool: pool, store: &covBPSStore{}, mail: &covBMailer{}}
	s.svc = postsale.New(postsale.Config{
		Repo:        postsale.NewRepo(pool),
		Insights:    catalog.NewInsights(pool),
		Detections:  catalog.NewDetections(pool),
		Storage:     s.store,
		Mailer:      s.mail,
		MailEnabled: true,
		BaseURL:     "https://app.example.com",
		Footer:      postsale.Footer{Email: "contato@example.com", City: "Cidade Teste"},
		Log:         zap.NewNop(),
	})
	s.h = NewPostSaleHandler(s.svc, nil)
	s.pub = NewPostSalePublicHandler(s.svc, nil)

	s.admin = mcptest.Usuario(t, ctx, pool, "admin", true)
	s.client = mcptest.Cliente(t, ctx, pool, "Cliente Teste")
	s.station = mcptest.Emissora(t, ctx, pool, "Rádio Teste FM", 1000)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	mat := mcptest.Material(t, ctx, pool, s.client, tipo, "Spot Teste")
	s.campaign = mcptest.Campanha(t, ctx, pool, s.client, "Campanha Teste", "2026-06-01", "2026-06-30", s.station)
	mcptest.Vincular(t, ctx, pool, s.campaign, mat, s.station)
	mcptest.Regra(t, ctx, pool, s.campaign, tipo, s.station, "2026-06-01", "2026-06-05", 127, "08:00", "20:00", 2)
	mcptest.Veiculacao(t, ctx, pool, s.campaign, mat, s.station, "in_slot", mcptest.Quando("2026-06-02", "10:00"))
	mcptest.Veiculacao(t, ctx, pool, s.campaign, mat, s.station, "in_slot", mcptest.Quando("2026-06-02", "11:00"))
	return s
}

func (s *covBPS) adminClaims() *auth.Claims { return covBClaims("admin", s.admin) }

// viewer cria um usuário Cliente ativo vinculado ao cliente do cenário.
func (s *covBPS) viewer(t *testing.T, email string) uuid.UUID {
	t.Helper()
	u, err := users.NewRepo(s.pool).Create(s.ctx, users.CreateInput{
		Email: email, PasswordHash: "x", Role: "viewer", ClientID: &s.client, Name: "Pessoa Cliente",
	})
	require.NoError(t, err)
	return u.ID
}

// draft cria um rascunho pelo handler e devolve o id.
func (s *covBPS) draft(t *testing.T) uuid.UUID {
	t.Helper()
	body := `{"client_id":"` + s.client.String() + `","title":"Pós-venda Teste","intro_message":"olá"}`
	rec := covBServe(s.h.Create, covBReq("POST", "/post-sale/reports", body, nil, s.adminClaims()))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	id, err := uuid.Parse(covBJSON(t, rec)["id"].(string))
	require.NoError(t, err)
	return id
}

func (s *covBPS) withBlock(t *testing.T, id uuid.UUID) {
	t.Helper()
	body := `{"blocks":[{"campaign_id":"` + s.campaign.String() + `","period_from":"2026-06-01","period_to":"2026-06-30","position":0,"checking_text":"ok"}]}`
	rec := covBServe(s.h.Update, covBReq("PUT", "/x", body, map[string]string{"id": id.String()}, s.adminClaims()))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func (s *covBPS) upload(t *testing.T, id uuid.UUID) {
	t.Helper()
	req := covBMultipartReq(t, "/x?campaign_id="+s.campaign.String(), nil, map[string]string{"id": id.String()}, s.adminClaims(),
		covBFile{Field: "map_png", Filename: "map.png", ContentType: "image/png", Data: covBPNG},
		covBFile{Field: "insights_png", Filename: "ins.png", ContentType: "image/png", Data: covBPNG})
	rec := covBServe(s.h.UploadAssets, req)
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
}

func TestCovB_PostSale_CreateGetList(t *testing.T) {
	s := covBPSSetup(t)

	t.Run("create: json inválido 400", func(t *testing.T) {
		rec := covBServe(s.h.Create, covBReq("POST", "/x", `{"client_id":`, nil, s.adminClaims()))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
	t.Run("create: sem client_id 400", func(t *testing.T) {
		rec := covBServe(s.h.Create, covBReq("POST", "/x", `{"title":"x"}`, nil, s.adminClaims()))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "client_id")
	})
	t.Run("create: sem título 400", func(t *testing.T) {
		rec := covBServe(s.h.Create, covBReq("POST", "/x", `{"client_id":"`+s.client.String()+`"}`, nil, s.adminClaims()))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "title")
	})
	t.Run("create: cliente inexistente é erro de servidor, não 201", func(t *testing.T) {
		rec := covBServe(s.h.Create, covBReq("POST", "/x", `{"client_id":"`+uuid.NewString()+`","title":"x"}`, nil, s.adminClaims()))
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})

	id := s.draft(t)

	t.Run("create: rascunho nasce em draft e guarda o autor", func(t *testing.T) {
		rec := covBServe(s.h.Get, covBReq("GET", "/x", "", map[string]string{"id": id.String()}, s.adminClaims()))
		require.Equal(t, http.StatusOK, rec.Code)
		m := covBJSON(t, rec)
		assert.Equal(t, "draft", m["status"])
		assert.Equal(t, "Pós-venda Teste", m["title"])
		assert.Equal(t, "olá", m["intro_message"])
		assert.Equal(t, s.client.String(), m["client_id"])
		assert.Nil(t, m["sent_at"])
		var by *uuid.UUID
		require.NoError(t, s.pool.QueryRow(s.ctx, `SELECT created_by FROM post_sale_reports WHERE id = $1`, id).Scan(&by))
		require.NotNil(t, by)
		assert.Equal(t, s.admin, *by)
	})
	t.Run("get: id inválido 400", func(t *testing.T) {
		rec := covBServe(s.h.Get, covBReq("GET", "/x", "", map[string]string{"id": "x"}, s.adminClaims()))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
	t.Run("get: inexistente 404", func(t *testing.T) {
		rec := covBServe(s.h.Get, covBReq("GET", "/x", "", map[string]string{"id": uuid.NewString()}, s.adminClaims()))
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	// segundo relatório, de outro cliente, para exercitar os filtros.
	otherClient := mcptest.Cliente(t, s.ctx, s.pool, "Outro Cliente Teste")
	_, err := postsale.NewRepo(s.pool).CreateDraft(s.ctx, postsale.CreateDraftInput{ClientID: otherClient, Title: "Relatório Beta"})
	require.NoError(t, err)

	listTotal := func(t *testing.T, query string) (int, []any) {
		t.Helper()
		rec := covBServe(s.h.List, covBReq("GET", "/post-sale/reports"+query, "", nil, s.adminClaims()))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		m := covBJSON(t, rec)
		items, _ := m["items"].([]any)
		total, _ := m["total"].(float64)
		return int(total), items
	}
	t.Run("list: sem filtro devolve os dois", func(t *testing.T) {
		total, _ := listTotal(t, "")
		assert.Equal(t, 2, total)
	})
	t.Run("list: filtra por cliente", func(t *testing.T) {
		total, items := listTotal(t, "?client_id="+otherClient.String())
		assert.Equal(t, 1, total)
		require.Len(t, items, 1)
		assert.Equal(t, "Relatório Beta", items[0].(map[string]any)["title"])
	})
	t.Run("list: busca textual", func(t *testing.T) {
		total, _ := listTotal(t, "?q=Beta")
		assert.Equal(t, 1, total)
	})
	t.Run("list: status draft e sent", func(t *testing.T) {
		total, _ := listTotal(t, "?status=draft")
		assert.Equal(t, 2, total)
		total, _ = listTotal(t, "?status=sent")
		assert.Equal(t, 0, total)
	})
	t.Run("list: status e mês inválidos são ignorados (não viram erro nem filtro)", func(t *testing.T) {
		total, _ := listTotal(t, "?status=hack&month=2026-13")
		assert.Equal(t, 2, total)
	})
	t.Run("list: mês válido sem relatório zera o resultado", func(t *testing.T) {
		total, _ := listTotal(t, "?month=1999-01")
		assert.Equal(t, 0, total)
	})
	t.Run("list: client_id inválido 400", func(t *testing.T) {
		rec := covBServe(s.h.List, covBReq("GET", "/x?client_id=abc", "", nil, s.adminClaims()))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
	t.Run("list: per_page absurdo é limitado a 100", func(t *testing.T) {
		rec := covBServe(s.h.List, covBReq("GET", "/x?per_page=100000", "", nil, s.adminClaims()))
		require.Equal(t, http.StatusOK, rec.Code)
		assert.EqualValues(t, 100, covBJSON(t, rec)["per_page"])
	})
}

func TestCovB_PostSale_Update(t *testing.T) {
	s := covBPSSetup(t)
	id := s.draft(t)
	params := map[string]string{"id": id.String()}
	put := func(body string) *http.Request { return covBReq("PUT", "/x", body, params, s.adminClaims()) }

	t.Run("id inválido 400", func(t *testing.T) {
		rec := covBServe(s.h.Update, covBReq("PUT", "/x", `{}`, map[string]string{"id": "x"}, s.adminClaims()))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
	t.Run("json inválido 400", func(t *testing.T) {
		assert.Equal(t, http.StatusBadRequest, covBServe(s.h.Update, put(`{"title":`)).Code)
	})
	t.Run("relatório inexistente 404", func(t *testing.T) {
		rec := covBServe(s.h.Update, covBReq("PUT", "/x", `{"title":"x"}`, map[string]string{"id": uuid.NewString()}, s.adminClaims()))
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
	t.Run("edita só o título e preserva o resto", func(t *testing.T) {
		rec := covBServe(s.h.Update, put(`{"title":"Título novo"}`))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		m := covBJSON(t, rec)
		assert.Equal(t, "Título novo", m["title"])
		assert.Equal(t, "olá", m["intro_message"], "campo ausente não pode ser apagado")
	})
	t.Run("link de anexos: define e depois remove com string vazia", func(t *testing.T) {
		rec := covBServe(s.h.Update, put(`{"attachments_url":"https://example.com/anexos"}`))
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "https://example.com/anexos", covBJSON(t, rec)["attachments_url"])
		rec = covBServe(s.h.Update, put(`{"attachments_url":""}`))
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "", covBJSON(t, rec)["attachments_url"])
	})
	t.Run("blocos: datas inválidas e intervalo invertido 400", func(t *testing.T) {
		cid := s.campaign.String()
		cases := map[string]string{
			"period_from inválido":    `{"blocks":[{"campaign_id":"` + cid + `","period_from":"01/06/2026","period_to":"2026-06-30"}]}`,
			"period_to inválido":      `{"blocks":[{"campaign_id":"` + cid + `","period_from":"2026-06-01","period_to":"amanhã"}]}`,
			"to antes do from":        `{"blocks":[{"campaign_id":"` + cid + `","period_from":"2026-06-30","period_to":"2026-06-01"}]}`,
			"período vazio (vírgula)": `{"blocks":[{"campaign_id":"` + cid + `","period_from":"","period_to":""}]}`,
		}
		for name, body := range cases {
			t.Run(name, func(t *testing.T) {
				assert.Equal(t, http.StatusBadRequest, covBServe(s.h.Update, put(body)).Code)
			})
		}
	})
	t.Run("blocos válidos persistem com as edições do admin", func(t *testing.T) {
		body := `{"blocks":[{"campaign_id":"` + s.campaign.String() + `","period_from":"2026-06-01","period_to":"2026-06-30",
			"position":1,"checking_text":"tudo certo","checking_edited":true,
			"checking_rows":[{"station_name":"Rádio Teste FM"}],
			"kpi_overrides":{"impactos":777}}]}`
		rec := covBServe(s.h.Update, put(body))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		blocks := covBJSON(t, rec)["blocks"].([]any)
		require.Len(t, blocks, 1)
		b := blocks[0].(map[string]any)
		assert.Equal(t, s.campaign.String(), b["campaign_id"])
		assert.Equal(t, "tudo certo", b["checking_text"])
		assert.Equal(t, true, b["checking_edited"])
		assert.EqualValues(t, 777, b["kpi_overrides"].(map[string]any)["impactos"])
	})
	t.Run("blocos vazios [] removem todos", func(t *testing.T) {
		rec := covBServe(s.h.Update, put(`{"blocks":[]}`))
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Empty(t, covBJSON(t, rec)["blocks"])
	})
	t.Run("trocar de cliente descarta os blocos", func(t *testing.T) {
		s.withBlock(t, id)
		other := mcptest.Cliente(t, s.ctx, s.pool, "Cliente Novo Teste")
		rec := covBServe(s.h.Update, put(`{"client_id":"`+other.String()+`"}`))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		m := covBJSON(t, rec)
		assert.Equal(t, other.String(), m["client_id"])
		assert.Empty(t, m["blocks"], "campanha de outro cliente não pode sobreviver à troca")
	})
}

func TestCovB_PostSale_Recipients(t *testing.T) {
	s := covBPSSetup(t)
	s.viewer(t, "cliente.a@example.com")
	s.viewer(t, "cliente.b@example.com")
	// admin com opt-in recebe cópia; admin sem opt-in não.
	_, err := s.pool.Exec(s.ctx, `UPDATE users SET receive_post_sale_emails = TRUE, name = 'Admin Copia' WHERE id = $1`, s.admin)
	require.NoError(t, err)
	mcptest.Usuario(t, s.ctx, s.pool, "admin", true) // sem opt-in
	id := s.draft(t)

	groups := func(t *testing.T, body []byte) (client, internal []map[string]any) {
		t.Helper()
		var m struct {
			Client   []map[string]any `json:"client"`
			Internal []map[string]any `json:"internal"`
		}
		require.NoError(t, json.Unmarshal(body, &m))
		return m.Client, m.Internal
	}

	t.Run("por relatório: separa cliente e cópia interna", func(t *testing.T) {
		rec := covBServe(s.h.Recipients, covBReq("GET", "/x", "", map[string]string{"id": id.String()}, s.adminClaims()))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		client, internal := groups(t, rec.Body.Bytes())
		require.Len(t, client, 2)
		assert.Equal(t, "cliente.a@example.com", client[0]["email"], "ordenado por e-mail")
		assert.Equal(t, "cliente.b@example.com", client[1]["email"])
		require.Len(t, internal, 1, "só o admin com opt-in entra")
		assert.Equal(t, "Admin Copia", internal[0]["name"])
	})
	t.Run("por relatório: id inválido 400", func(t *testing.T) {
		rec := covBServe(s.h.Recipients, covBReq("GET", "/x", "", map[string]string{"id": "x"}, s.adminClaims()))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
	t.Run("por relatório: inexistente 404", func(t *testing.T) {
		rec := covBServe(s.h.Recipients, covBReq("GET", "/x", "", map[string]string{"id": uuid.NewString()}, s.adminClaims()))
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
	t.Run("preview por cliente: mesmo resultado sem relatório", func(t *testing.T) {
		rec := covBServe(s.h.RecipientsPreview, covBReq("GET", "/x?client_id="+s.client.String(), "", nil, s.adminClaims()))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		client, internal := groups(t, rec.Body.Bytes())
		assert.Len(t, client, 2)
		assert.Len(t, internal, 1)
	})
	t.Run("preview por cliente: cliente sem usuários devolve listas vazias (não null)", func(t *testing.T) {
		empty := mcptest.Cliente(t, s.ctx, s.pool, "Cliente Sem Usuarios")
		rec := covBServe(s.h.RecipientsPreview, covBReq("GET", "/x?client_id="+empty.String(), "", nil, s.adminClaims()))
		require.Equal(t, http.StatusOK, rec.Code)
		var raw map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
		assert.JSONEq(t, `[]`, string(raw["client"]))
	})
	t.Run("preview por cliente: client_id ausente ou inválido 400", func(t *testing.T) {
		assert.Equal(t, http.StatusBadRequest, covBServe(s.h.RecipientsPreview, covBReq("GET", "/x", "", nil, s.adminClaims())).Code)
		assert.Equal(t, http.StatusBadRequest, covBServe(s.h.RecipientsPreview, covBReq("GET", "/x?client_id=x", "", nil, s.adminClaims())).Code)
	})
}

func TestCovB_PostSale_UploadAssets(t *testing.T) {
	s := covBPSSetup(t)
	id := s.draft(t)
	params := map[string]string{"id": id.String()}
	target := "/x?campaign_id=" + s.campaign.String()
	png := func(field string) covBFile {
		return covBFile{Field: field, Filename: field + ".png", ContentType: "image/png", Data: covBPNG}
	}

	t.Run("id inválido 400", func(t *testing.T) {
		req := covBMultipartReq(t, target, nil, map[string]string{"id": "x"}, s.adminClaims(), png("map_png"), png("insights_png"))
		assert.Equal(t, http.StatusBadRequest, covBServe(s.h.UploadAssets, req).Code)
	})
	t.Run("campaign_id ausente ou inválido 400", func(t *testing.T) {
		for _, tg := range []string{"/x", "/x?campaign_id=zzz"} {
			req := covBMultipartReq(t, tg, nil, params, s.adminClaims(), png("map_png"), png("insights_png"))
			rec := covBServe(s.h.UploadAssets, req)
			assert.Equal(t, http.StatusBadRequest, rec.Code, tg)
			assert.Contains(t, rec.Body.String(), "campaign_id")
		}
	})
	t.Run("corpo não multipart 400", func(t *testing.T) {
		rec := covBServe(s.h.UploadAssets, covBReq("POST", target, `{}`, params, s.adminClaims()))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "multipart")
	})
	t.Run("sem map_png 400", func(t *testing.T) {
		req := covBMultipartReq(t, target, nil, params, s.adminClaims(), png("insights_png"))
		rec := covBServe(s.h.UploadAssets, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "map_png")
	})
	t.Run("sem insights_png 400", func(t *testing.T) {
		req := covBMultipartReq(t, target, nil, params, s.adminClaims(), png("map_png"))
		rec := covBServe(s.h.UploadAssets, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "insights_png")
	})
	t.Run("arquivo vazio é recusado pelo serviço (captura incompleta)", func(t *testing.T) {
		empty := covBFile{Field: "map_png", Filename: "m.png", ContentType: "image/png", Data: nil}
		req := covBMultipartReq(t, target, nil, params, s.adminClaims(), empty, png("insights_png"))
		assert.Equal(t, http.StatusInternalServerError, covBServe(s.h.UploadAssets, req).Code)
	})
	t.Run("sucesso 204", func(t *testing.T) {
		req := covBMultipartReq(t, target, nil, params, s.adminClaims(), png("map_png"), png("insights_png"))
		assert.Equal(t, http.StatusNoContent, covBServe(s.h.UploadAssets, req).Code)
	})
}

func TestCovB_PostSale_PreviewPublishResendRevokeAndPublic(t *testing.T) {
	s := covBPSSetup(t)
	s.viewer(t, "cliente.a@example.com")
	id := s.draft(t)
	params := map[string]string{"id": id.String()}

	t.Run("preview: id inválido 400 e inexistente 404", func(t *testing.T) {
		assert.Equal(t, http.StatusBadRequest,
			covBServe(s.h.Preview, covBReq("GET", "/x", "", map[string]string{"id": "x"}, s.adminClaims())).Code)
		assert.Equal(t, http.StatusNotFound,
			covBServe(s.h.Preview, covBReq("GET", "/x", "", map[string]string{"id": uuid.NewString()}, s.adminClaims())).Code)
	})
	t.Run("publish: id inválido 400 e inexistente 404", func(t *testing.T) {
		assert.Equal(t, http.StatusBadRequest,
			covBServe(s.h.Publish, covBReq("POST", "/x", "", map[string]string{"id": "x"}, s.adminClaims())).Code)
		assert.Equal(t, http.StatusNotFound,
			covBServe(s.h.Publish, covBReq("POST", "/x", "", map[string]string{"id": uuid.NewString()}, s.adminClaims())).Code)
	})
	t.Run("publish: rascunho sem campanha não envia nada", func(t *testing.T) {
		rec := covBServe(s.h.Publish, covBReq("POST", "/x", "", params, s.adminClaims()))
		assert.GreaterOrEqual(t, rec.Code, 400, "não pode publicar relatório sem campanhas")
		assert.Empty(t, s.mail.recipients())
		var status string
		require.NoError(t, s.pool.QueryRow(s.ctx, `SELECT status FROM post_sale_reports WHERE id=$1`, id).Scan(&status))
		assert.Equal(t, "draft", status)
	})

	s.withBlock(t, id)

	t.Run("publish: sem as capturas do navegador não envia nada", func(t *testing.T) {
		rec := covBServe(s.h.Publish, covBReq("POST", "/x", "", params, s.adminClaims()))
		assert.GreaterOrEqual(t, rec.Code, 400)
		assert.Empty(t, s.mail.recipients(), "S3/captura faltando aborta ANTES de qualquer email")
	})
	t.Run("preview do rascunho devolve o documento calculado", func(t *testing.T) {
		rec := covBServe(s.h.Preview, covBReq("GET", "/x", "", params, s.adminClaims()))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		m := covBJSON(t, rec)
		assert.Equal(t, "Pós-venda Teste", m["title"])
		assert.NotEmpty(t, m["campaigns"])
	})

	s.upload(t, id)

	t.Run("publish: sucesso congela, sobe artefatos e dispara 1 email por destinatário", func(t *testing.T) {
		rec := covBServe(s.h.Publish, covBReq("POST", "/x", "", params, s.adminClaims()))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		m := covBJSON(t, rec)
		assert.EqualValues(t, 1, m["recipients"])
		assert.EqualValues(t, 1, m["sent"])
		assert.Equal(t, []string{"cliente.a@example.com"}, s.mail.recipients())
		assert.Len(t, s.store.objects, 3, "mapa, indicadores e zip")
	})
	t.Run("publish: segunda tentativa 409", func(t *testing.T) {
		rec := covBServe(s.h.Publish, covBReq("POST", "/x", "", params, s.adminClaims()))
		assert.Equal(t, http.StatusConflict, rec.Code)
		assert.Contains(t, rec.Body.String(), "already_sent")
		assert.Len(t, s.mail.recipients(), 1, "republicar não reenvia email")
	})
	t.Run("update depois de enviado não altera o que o cliente leu", func(t *testing.T) {
		rec := covBServe(s.h.Update, covBReq("PUT", "/x", `{"title":"Adulterado"}`, params, s.adminClaims()))
		assert.GreaterOrEqual(t, rec.Code, 400)
		var title string
		require.NoError(t, s.pool.QueryRow(s.ctx, `SELECT title FROM post_sale_reports WHERE id=$1`, id).Scan(&title))
		assert.Equal(t, "Pós-venda Teste", title)
	})
	t.Run("preview do enviado devolve o payload congelado", func(t *testing.T) {
		rec := covBServe(s.h.Preview, covBReq("GET", "/x", "", params, s.adminClaims()))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, "Pós-venda Teste", covBJSON(t, rec)["title"])
	})

	// destinatário gerado pelo publish
	rep, err := s.svc.Repo().Get(s.ctx, id)
	require.NoError(t, err)
	require.Len(t, rep.Recipients, 1)
	rcpt := rep.Recipients[0]

	t.Run("resend: validações", func(t *testing.T) {
		assert.Equal(t, http.StatusBadRequest,
			covBServe(s.h.Resend, covBReq("POST", "/x", `{}`, map[string]string{"id": "x"}, s.adminClaims())).Code)
		assert.Equal(t, http.StatusBadRequest,
			covBServe(s.h.Resend, covBReq("POST", "/x", `{"recipient_id":`, params, s.adminClaims())).Code)
		assert.Equal(t, http.StatusBadRequest,
			covBServe(s.h.Resend, covBReq("POST", "/x", `{}`, params, s.adminClaims())).Code, "recipient_id é obrigatório")
		assert.Equal(t, http.StatusNotFound,
			covBServe(s.h.Resend, covBReq("POST", "/x", `{"recipient_id":"`+uuid.NewString()+`"}`, params, s.adminClaims())).Code)
		assert.Equal(t, http.StatusNotFound,
			covBServe(s.h.Resend, covBReq("POST", "/x", `{"recipient_id":"`+rcpt.ID.String()+`"}`,
				map[string]string{"id": uuid.NewString()}, s.adminClaims())).Code)
	})
	t.Run("resend: sucesso reenvia o email com o MESMO token", func(t *testing.T) {
		rec := covBServe(s.h.Resend, covBReq("POST", "/x", `{"recipient_id":"`+rcpt.ID.String()+`"}`, params, s.adminClaims()))
		require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
		assert.Len(t, s.mail.recipients(), 2)
		after, err := s.svc.Repo().Get(s.ctx, id)
		require.NoError(t, err)
		assert.Equal(t, rcpt.Token, after.Recipients[0].Token)
	})

	// ── rotas públicas (token) ──
	cid := s.campaign.String()
	pubReq := func(token, cidParam, kind string) *http.Request {
		p := map[string]string{"token": token}
		if cidParam != "" {
			p["cid"] = cidParam
		}
		if kind != "" {
			p["kind"] = kind
		}
		return covBReq("GET", "/public/post-sale/x", "", p, nil)
	}

	t.Run("público/resolve: token válido devolve o JSON congelado e conta a abertura", func(t *testing.T) {
		rec := covBServe(s.pub.Resolve, pubReq(rcpt.Token, "", ""))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
		assert.Equal(t, "Pós-venda Teste", covBJSON(t, rec)["title"])
		var opens int
		require.NoError(t, s.pool.QueryRow(s.ctx, `SELECT open_count FROM post_sale_report_recipients WHERE id=$1`, rcpt.ID).Scan(&opens))
		assert.Equal(t, 1, opens)
	})
	t.Run("público/resolve: token vazio e desconhecido respondem o mesmo 404 (nunca 401)", func(t *testing.T) {
		a := covBServe(s.pub.Resolve, pubReq("", "", ""))
		b := covBServe(s.pub.Resolve, pubReq("token-que-nao-existe", "", ""))
		assert.Equal(t, http.StatusNotFound, a.Code)
		assert.Equal(t, http.StatusNotFound, b.Code)
		assert.Equal(t, a.Body.String(), b.Body.String(), "não pode virar oráculo de existência")
	})
	t.Run("público/image: mapa e indicadores passam pelo proxy com content-type png", func(t *testing.T) {
		for _, kind := range []string{"map", "insights"} {
			rec := covBServe(s.pub.Image, pubReq(rcpt.Token, cid, kind))
			require.Equal(t, http.StatusOK, rec.Code, kind)
			assert.Equal(t, "image/png", rec.Header().Get("Content-Type"))
			assert.Equal(t, "inline", rec.Header().Get("Content-Disposition"))
			assert.Equal(t, "private, max-age=300", rec.Header().Get("Cache-Control"))
			assert.Equal(t, covBPNG, rec.Body.Bytes())
		}
	})
	t.Run("público/image: kind fora da whitelist, cid inválido e token ruim dão 404", func(t *testing.T) {
		assert.Equal(t, http.StatusNotFound, covBServe(s.pub.Image, pubReq(rcpt.Token, cid, "../../etc/passwd")).Code)
		assert.Equal(t, http.StatusNotFound, covBServe(s.pub.Image, pubReq(rcpt.Token, "nao-uuid", "map")).Code)
		assert.Equal(t, http.StatusNotFound, covBServe(s.pub.Image, pubReq("", cid, "map")).Code)
		assert.Equal(t, http.StatusNotFound, covBServe(s.pub.Image, pubReq("ruim", cid, "map")).Code)
		assert.Equal(t, http.StatusNotFound, covBServe(s.pub.Image, pubReq(rcpt.Token, uuid.NewString(), "map")).Code,
			"campanha que não pertence ao relatório")
	})
	t.Run("público/bundle: zip como anexo com Content-Length", func(t *testing.T) {
		rec := covBServe(s.pub.Bundle, pubReq(rcpt.Token, cid, ""))
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "application/zip", rec.Header().Get("Content-Type"))
		assert.Equal(t, `attachment; filename="relatorios.zip"`, rec.Header().Get("Content-Disposition"))
		assert.Equal(t, strconv.Itoa(rec.Body.Len()), rec.Header().Get("Content-Length"))
		assert.Equal(t, []byte("PK"), rec.Body.Bytes()[:2], "assinatura de arquivo zip")
	})
	t.Run("público/bundle: cid inválido, token ruim e objeto sumido", func(t *testing.T) {
		assert.Equal(t, http.StatusNotFound, covBServe(s.pub.Bundle, pubReq(rcpt.Token, "x", "")).Code)
		assert.Equal(t, http.StatusNotFound, covBServe(s.pub.Bundle, pubReq("ruim", cid, "")).Code)
		var key string
		require.NoError(t, s.pool.QueryRow(s.ctx,
			`SELECT assets->>'bundle_zip' FROM post_sale_report_campaigns WHERE report_id=$1`, id).Scan(&key))
		require.NotEmpty(t, key)
		s.store.del(key)
		assert.Equal(t, http.StatusInternalServerError, covBServe(s.pub.Bundle, pubReq(rcpt.Token, cid, "")).Code,
			"objeto ausente no bucket é falha de infraestrutura, não 404 de token")
	})

	t.Run("revoke: validações", func(t *testing.T) {
		assert.Equal(t, http.StatusBadRequest,
			covBServe(s.h.RevokeRecipient, covBReq("DELETE", "/x", "", map[string]string{"rid": "x"}, s.adminClaims())).Code)
		assert.Equal(t, http.StatusNotFound,
			covBServe(s.h.RevokeRecipient, covBReq("DELETE", "/x", "", map[string]string{"rid": uuid.NewString()}, s.adminClaims())).Code)
	})
	t.Run("revoke: mata só o link revogado e o reenvio passa a 404", func(t *testing.T) {
		rec := covBServe(s.h.RevokeRecipient, covBReq("DELETE", "/x", "", map[string]string{"rid": rcpt.ID.String()}, s.adminClaims()))
		require.Equal(t, http.StatusNoContent, rec.Code)
		var by *uuid.UUID
		require.NoError(t, s.pool.QueryRow(s.ctx, `SELECT revoked_by FROM post_sale_report_recipients WHERE id=$1`, rcpt.ID).Scan(&by))
		require.NotNil(t, by)
		assert.Equal(t, s.admin, *by)

		assert.Equal(t, http.StatusNotFound, covBServe(s.pub.Resolve, pubReq(rcpt.Token, "", "")).Code)
		assert.Equal(t, http.StatusNotFound, covBServe(s.pub.Image, pubReq(rcpt.Token, cid, "map")).Code)
		assert.Equal(t, http.StatusNotFound,
			covBServe(s.h.Resend, covBReq("POST", "/x", `{"recipient_id":"`+rcpt.ID.String()+`"}`, params, s.adminClaims())).Code,
			"revogado não ressuscita por reenvio")
	})
	t.Run("revoke sem claims funciona (ator nulo)", func(t *testing.T) {
		rec := covBServe(s.h.RevokeRecipient, covBReq("DELETE", "/x", "", map[string]string{"rid": rcpt.ID.String()}, nil))
		assert.Equal(t, http.StatusNoContent, rec.Code, "revogação é ação de segurança e idempotente")
	})
}

func TestCovB_PostSale_FailMapsErrors(t *testing.T) {
	h := NewPostSaleHandler(nil, nil)
	cases := []struct {
		name string
		err  error
		code int
		body string
	}{
		{"não encontrado", postsale.ErrNotFound, http.StatusNotFound, "not_found"},
		{"já enviado", postsale.ErrAlreadySent, http.StatusConflict, "already_sent"},
		{"envelopado ainda mapeia", errors.Join(errors.New("ctx"), postsale.ErrNotFound), http.StatusNotFound, "not_found"},
		{"desconhecido vira 500 genérico", errors.New("boom secreto"), http.StatusInternalServerError, "internal error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := covBRecorder()
			h.fail(rec, tc.err, "teste")
			assert.Equal(t, tc.code, rec.Code)
			assert.Contains(t, rec.Body.String(), tc.body)
			assert.NotContains(t, rec.Body.String(), "boom secreto", "detalhe interno não pode vazar")
		})
	}
}
