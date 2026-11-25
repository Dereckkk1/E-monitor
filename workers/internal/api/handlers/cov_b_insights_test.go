package handlers

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/catalog"
)

func covBUUIDs(n int) string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = uuid.NewString()
	}
	return strings.Join(ids, ",")
}

func TestCovB_Insights_Validation(t *testing.T) {
	client := uuid.NewString()
	camp := uuid.NewString()
	base := "client_id=" + client + "&campaigns=" + camp

	tests := []struct {
		name  string
		query string
		code  int
		body  string
	}{
		{"campanhas acima de 50", "client_id=" + client + "&campaigns=" + covBUUIDs(51), 400, "campaigns max=50"},
		{"exatamente 50 campanhas passa", "client_id=" + client + "&campaigns=" + covBUUIDs(50), 200, ""},
		{"campanhas só com vírgulas e espaços vira lista vazia", "client_id=" + client + "&campaigns=,%20,", 400, "campaigns required"},
		{"campanha inválida na lista", "client_id=" + client + "&campaigns=" + camp + ",xx", 400, "invalid campaigns"},
		{"emissora inválida", base + "&stations=xx", 400, "invalid stations"},
		{"material inválido", base + "&materials=xx", 400, "invalid materials"},
		{"materiais acima de 200", base + "&materials=" + covBUUIDs(201), 400, "materials max=200"},
		{"exatamente 200 materiais passa", base + "&materials=" + covBUUIDs(200), 200, ""},
		{"to antes de from", base + "&from=2026-06-10&to=2026-06-09", 400, "to must be on or after from"},
		{"from = to é válido", base + "&from=2026-06-10&to=2026-06-10", 200, ""},
		{"client_id malformado", "client_id=xx&campaigns=" + camp, 400, "invalid client_id"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeInsightsRepo{out: &catalog.InsightsPayload{}}
			h := NewInsightsHandler(repo)
			rec := covBServe(h.Get, covBReq("GET", "/insights?"+tc.query, "", nil, covBClaims("admin", uuid.New())))
			assert.Equal(t, tc.code, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), tc.body)
			if tc.code != 200 {
				assert.Equal(t, 0, repo.hits, "pedido inválido não pode chegar ao repositório")
			} else {
				assert.Equal(t, 1, repo.hits)
			}
		})
	}
}

func TestCovB_Insights_ParamsForwarded(t *testing.T) {
	client, c1, c2 := uuid.New(), uuid.New(), uuid.New()
	st, mat := uuid.New(), uuid.New()
	repo := &fakeInsightsRepo{out: &catalog.InsightsPayload{}}
	h := NewInsightsHandler(repo)

	t.Run("tudo preenchido, com espaços e entradas vazias na lista", func(t *testing.T) {
		q := "/insights?client_id=" + client.String() +
			"&campaigns=" + c1.String() + ",%20" + c2.String() + "%20,," +
			"&stations=" + st.String() + "&materials=" + mat.String() +
			"&from=2026-03-01&to=2026-03-31"
		rec := covBServe(h.Get, covBReq("GET", q, "", nil, covBClaims("admin", uuid.New())))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, client, repo.got.ClientID)
		assert.Equal(t, []uuid.UUID{c1, c2}, repo.got.CampaignIDs)
		assert.Equal(t, []uuid.UUID{st}, repo.got.StationIDs)
		assert.Equal(t, []uuid.UUID{mat}, repo.got.MaterialIDs)
		assert.Equal(t, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), repo.got.From)
		assert.Equal(t, time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC), repo.got.To)
		assert.Equal(t, catalog.TodaySaoPaulo(), repo.got.Today)
	})
	t.Run("emissoras e materiais ausentes chegam como slice vazio e NÃO nil (o SQL usa '{}' como 'sem filtro')", func(t *testing.T) {
		rec := covBServe(h.Get, covBReq("GET", "/insights?client_id="+client.String()+"&campaigns="+c1.String(), "", nil,
			covBClaims("admin", uuid.New())))
		require.Equal(t, http.StatusOK, rec.Code)
		assert.NotNil(t, repo.got.StationIDs)
		assert.Empty(t, repo.got.StationIDs)
		assert.NotNil(t, repo.got.MaterialIDs)
		assert.Empty(t, repo.got.MaterialIDs)
	})
	t.Run("datas inválidas caem no mês corrente, sem erro", func(t *testing.T) {
		rec := covBServe(h.Get, covBReq("GET", "/insights?client_id="+client.String()+"&campaigns="+c1.String()+"&from=ontem&to=31/12/2026",
			"", nil, covBClaims("admin", uuid.New())))
		require.Equal(t, http.StatusOK, rec.Code)
		now := time.Now().UTC()
		assert.Equal(t, time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC), repo.got.From)
		assert.Equal(t, repo.got.From.AddDate(0, 1, 0).Add(-time.Second), repo.got.To)
	})
}

func TestCovB_Insights_ErrorMapping(t *testing.T) {
	q := "/insights?client_id=" + uuid.NewString() + "&campaigns=" + uuid.NewString()
	admin := covBClaims("admin", uuid.New())

	t.Run("erro cross-client vira 403 sem detalhe", func(t *testing.T) {
		h := NewInsightsHandler(&fakeInsightsRepo{err: errors.New("campaign abc: cross-client access")})
		rec := covBServe(h.Get, covBReq("GET", q, "", nil, admin))
		assert.Equal(t, http.StatusForbidden, rec.Code)
		assert.NotContains(t, rec.Body.String(), "abc")
	})
	t.Run("qualquer outro erro vira 500 genérico sem vazar", func(t *testing.T) {
		h := NewInsightsHandler(&fakeInsightsRepo{err: errors.New("pq: relation \"x\" does not exist")})
		rec := covBServe(h.Get, covBReq("GET", q, "", nil, admin))
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.NotContains(t, rec.Body.String(), "relation")
	})
	t.Run("construtor guarda o repositório", func(t *testing.T) {
		repo := &fakeInsightsRepo{}
		assert.Same(t, repo, NewInsightsHandler(repo).Repo)
	})
}

func TestCovB_Insights_ParseHelpers(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	t.Run("parseUUIDList", func(t *testing.T) {
		got, err := parseUUIDList("")
		assert.NoError(t, err)
		assert.Nil(t, got)

		got, err = parseUUIDList(" " + a.String() + " , ," + b.String())
		require.NoError(t, err)
		assert.Equal(t, []uuid.UUID{a, b}, got)

		got, err = parseUUIDList(",")
		require.NoError(t, err)
		assert.Empty(t, got)
		assert.NotNil(t, got)

		_, err = parseUUIDList(a.String() + ",nao-uuid")
		assert.Error(t, err)
	})
	t.Run("parseDateOr", func(t *testing.T) {
		def := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
		assert.Equal(t, def, parseDateOr("", def))
		assert.Equal(t, def, parseDateOr("2026-02-30", def), "dia inexistente")
		assert.Equal(t, def, parseDateOr("2026-02-10T10:00:00Z", def), "só YYYY-MM-DD")
		assert.Equal(t, time.Date(2026, 2, 10, 0, 0, 0, 0, time.UTC), parseDateOr("2026-02-10", def))
	})
	t.Run("todaySaoPaulo é meia-noite UTC", func(t *testing.T) {
		d := todaySaoPaulo()
		assert.Equal(t, 0, d.Hour())
		assert.Equal(t, 0, d.Minute())
		assert.Equal(t, time.UTC, d.Location())
	})
}
