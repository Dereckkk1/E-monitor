package handlers

// cov_a_admin_misc_test.go — handlers admin que dependem só de interfaces
// (tiering/calibração/threshold, sininho, digest diário, falhas por
// emissora/campanha/dia). Tudo com fakes: sem banco.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"radiocheck/internal/catalog"
)

// covALogger devolve um logger que guarda tudo em memória para os testes
// verificarem O QUE foi logado (mensagem e campos).
func covALogger() (*zap.Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	return zap.New(core), logs
}

func covAHasLog(logs *observer.ObservedLogs, msg string) (observer.LoggedEntry, bool) {
	for _, e := range logs.All() {
		if e.Message == msg {
			return e, true
		}
	}
	return observer.LoggedEntry{}, false
}

// ── AdminHandler: caminhos com logger ────────────────────────────────────────

func TestCovA_Admin_RunTiering_LogaSucessoEFalha(t *testing.T) {
	admin := uuid.New()

	t.Run("sucesso loga quem disparou", func(t *testing.T) {
		log, logs := covALogger()
		tier := &fakeTiering{}
		h := &AdminHandler{Tiering: tier, Log: log}
		req := covAWith(httptest.NewRequest(http.MethodPost, "/x", nil), covAClaims("admin", admin))
		rec := covAServe(http.HandlerFunc(h.RunTiering), req)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, int32(1), tier.calls.Load())
		e, ok := covAHasLog(logs, "admin: tiering run ok")
		require.True(t, ok, "esperava log de sucesso")
		assert.Equal(t, admin.String(), e.ContextMap()["triggered_by"])
	})

	t.Run("falha loga aviso e devolve 500 com a razão", func(t *testing.T) {
		log, logs := covALogger()
		h := &AdminHandler{Tiering: &fakeTiering{err: errors.New("bucket fora")}, Log: log}
		rec := covAServe(http.HandlerFunc(h.RunTiering), httptest.NewRequest(http.MethodPost, "/x", nil))
		require.Equal(t, http.StatusInternalServerError, rec.Code)
		body := covADecode(t, rec)
		assert.Equal(t, "error", body["status"])
		assert.Equal(t, "bucket fora", body["error"])
		_, ok := covAHasLog(logs, "admin: tiering run failed")
		assert.True(t, ok)
	})

	t.Run("sem claims o triggered_by fica vazio", func(t *testing.T) {
		log, logs := covALogger()
		h := &AdminHandler{Tiering: &fakeTiering{}, Log: log}
		rec := covAServe(http.HandlerFunc(h.RunTiering), httptest.NewRequest(http.MethodPost, "/x", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		e, ok := covAHasLog(logs, "admin: tiering run ok")
		require.True(t, ok)
		assert.Equal(t, "", e.ContextMap()["triggered_by"])
	})
}

func TestCovA_Admin_RefreshThreshold_LogaRecusaEFila(t *testing.T) {
	station := uuid.New()
	admin := uuid.New()
	rotear := func(h *AdminHandler) http.Handler {
		r := chi.NewRouter()
		r.Post("/stations/{id}/threshold/refresh", h.RefreshThreshold)
		return r
	}

	t.Run("worker ausente: 404 e log informativo", func(t *testing.T) {
		log, logs := covALogger()
		h := &AdminHandler{Threshold: &fakeThreshold{err: errors.New("no worker running")}, Log: log}
		rec := covAServe(rotear(h), httptest.NewRequest(http.MethodPost, "/stations/"+station.String()+"/threshold/refresh", nil))
		require.Equal(t, http.StatusNotFound, rec.Code)
		assert.Contains(t, rec.Body.String(), "no worker running")
		e, ok := covAHasLog(logs, "admin: threshold refresh refused")
		require.True(t, ok)
		assert.Equal(t, station.String(), e.ContextMap()["station_id"])
	})

	t.Run("aceito: 202 e log com autor", func(t *testing.T) {
		log, logs := covALogger()
		thr := &fakeThreshold{}
		h := &AdminHandler{Threshold: thr, Log: log}
		req := covAWith(httptest.NewRequest(http.MethodPost, "/stations/"+station.String()+"/threshold/refresh", nil), covAClaims("admin", admin))
		rec := covAServe(rotear(h), req)
		require.Equal(t, http.StatusAccepted, rec.Code)
		assert.Equal(t, "queued", covADecode(t, rec)["status"])
		assert.Equal(t, int32(1), thr.called.Load())
		e, ok := covAHasLog(logs, "admin: threshold refresh queued")
		require.True(t, ok)
		assert.Equal(t, admin.String(), e.ContextMap()["triggered_by"])
	})
}

func TestCovA_Admin_RunCalibration_LogaTodosOsCaminhos(t *testing.T) {
	station := uuid.New()
	admin := uuid.New()
	chama := func(h *AdminHandler, corpo string, claims bool) *httptest.ResponseRecorder {
		req := covAJSON(t, http.MethodPost, "/calibrate", corpo)
		if claims {
			req = covAWith(req, covAClaims("admin", admin))
		}
		return covAServe(http.HandlerFunc(h.RunCalibration), req)
	}

	t.Run("passada completa ok", func(t *testing.T) {
		log, logs := covALogger()
		cal := &fakeCalibration{count: 7}
		rec := chama(&AdminHandler{Calibration: cal, Log: log}, "", true)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		body := covADecode(t, rec)
		assert.EqualValues(t, 7, body["stations"])
		assert.Equal(t, int32(1), cal.full.Load())
		e, ok := covAHasLog(logs, "calibration: full pass ok")
		require.True(t, ok)
		assert.Equal(t, admin.String(), e.ContextMap()["triggered_by"])
		assert.EqualValues(t, 7, e.ContextMap()["stations"])
	})

	t.Run("passada completa falha", func(t *testing.T) {
		log, logs := covALogger()
		rec := chama(&AdminHandler{Calibration: &fakeCalibration{fullErr: errors.New("db down")}, Log: log}, "", false)
		require.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Equal(t, "db down", covADecode(t, rec)["error"])
		_, ok := covAHasLog(logs, "calibration: full pass failed")
		assert.True(t, ok)
	})

	t.Run("uma emissora ok", func(t *testing.T) {
		log, logs := covALogger()
		cal := &fakeCalibration{}
		rec := chama(&AdminHandler{Calibration: cal, Log: log}, `{"station_id":"`+station.String()+`"}`, true)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, station, cal.lastStationArg)
		assert.Equal(t, int32(0), cal.full.Load(), "não roda a passada completa")
		e, ok := covAHasLog(logs, "calibration: station ok")
		require.True(t, ok)
		assert.Equal(t, station.String(), e.ContextMap()["station_id"])
	})

	t.Run("uma emissora falha", func(t *testing.T) {
		log, logs := covALogger()
		rec := chama(&AdminHandler{Calibration: &fakeCalibration{stationErr: errors.New("sem amostra")}, Log: log}, `{"station_id":"`+station.String()+`"}`, false)
		require.Equal(t, http.StatusInternalServerError, rec.Code)
		body := covADecode(t, rec)
		assert.Equal(t, "sem amostra", body["error"])
		assert.Equal(t, station.String(), body["station_id"])
		e, ok := covAHasLog(logs, "calibration: station run failed")
		require.True(t, ok)
		assert.Equal(t, station.String(), e.ContextMap()["station_id"])
	})
}

// ── Sininho de notificações ──────────────────────────────────────────────────

type covANotifRepo struct {
	listRes    *catalog.NotificationsResult
	listErr    error
	markErr    error
	markN      int
	markAllN   int
	markAllErr error

	gotUser uuid.UUID
	gotKeys []string
}

func (f *covANotifRepo) List(_ context.Context, u uuid.UUID) (*catalog.NotificationsResult, error) {
	f.gotUser = u
	return f.listRes, f.listErr
}
func (f *covANotifRepo) MarkRead(_ context.Context, u uuid.UUID, keys []string) (int, error) {
	f.gotUser, f.gotKeys = u, keys
	return f.markN, f.markErr
}
func (f *covANotifRepo) MarkAllReadInWindow(_ context.Context, u uuid.UUID) (int, error) {
	f.gotUser = u
	return f.markAllN, f.markAllErr
}

func TestCovA_Notifications_UserIDFromReq(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	_, ok := userIDFromReq(req)
	assert.False(t, ok, "sem claims")

	_, ok = userIDFromReq(covAWith(req, covAClaims("admin", uuid.Nil)))
	assert.False(t, ok, "claims com UserID zerado não valem")

	id := uuid.New()
	got, ok := userIDFromReq(covAWith(req, covAClaims("admin", id)))
	assert.True(t, ok)
	assert.Equal(t, id, got)
}

func TestCovA_Notifications_List(t *testing.T) {
	user := uuid.New()
	req := func() *http.Request {
		return covAWith(httptest.NewRequest(http.MethodGet, "/", nil), covAClaims("admin", user))
	}

	t.Run("devolve o resultado do repo para o usuário logado", func(t *testing.T) {
		repo := &covANotifRepo{listRes: &catalog.NotificationsResult{
			Items:       []catalog.Notification{{Key: "campaign_failure:x:2026-06-01", Kind: "campaign_failure", CampaignName: "Campanha Teste", OccurredOn: "2026-06-01"}},
			UnreadCount: 1,
		}}
		h := &NotificationsHandler{Repo: repo}
		rec := covAServe(http.HandlerFunc(h.List), req())
		require.Equal(t, http.StatusOK, rec.Code)
		body := covADecode(t, rec)
		assert.EqualValues(t, 1, body["unread_count"])
		items := body["items"].([]any)
		require.Len(t, items, 1)
		assert.Equal(t, "Campanha Teste", items[0].(map[string]any)["campaign_name"])
		assert.Equal(t, user, repo.gotUser)
	})

	t.Run("erro do repo: 500 e log", func(t *testing.T) {
		log, logs := covALogger()
		h := &NotificationsHandler{Repo: &covANotifRepo{listErr: errors.New("db")}, Log: log}
		rec := covAServe(http.HandlerFunc(h.List), req())
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		_, ok := covAHasLog(logs, "notifications.list_failed")
		assert.True(t, ok)
	})

	t.Run("erro do repo sem logger não panica", func(t *testing.T) {
		h := &NotificationsHandler{Repo: &covANotifRepo{listErr: errors.New("db")}}
		assert.Equal(t, http.StatusInternalServerError, covAServe(http.HandlerFunc(h.List), req()).Code)
	})
}

func TestCovA_Notifications_MarkRead(t *testing.T) {
	user := uuid.New()
	post := func(h *NotificationsHandler, corpo any, claims bool) *httptest.ResponseRecorder {
		req := covAJSON(t, http.MethodPost, "/mark-read", corpo)
		if claims {
			req = covAWith(req, covAClaims("admin", user))
		}
		return covAServe(http.HandlerFunc(h.MarkRead), req)
	}

	t.Run("sem claims 401", func(t *testing.T) {
		assert.Equal(t, http.StatusUnauthorized, post(&NotificationsHandler{}, map[string]any{}, false).Code)
	})
	t.Run("json inválido 400", func(t *testing.T) {
		rec := post(&NotificationsHandler{Repo: &covANotifRepo{}}, "{x", true)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "bad json")
	})
	t.Run("mais de 200 chaves 400", func(t *testing.T) {
		keys := make([]string, 201)
		for i := range keys {
			keys[i] = "k"
		}
		repo := &covANotifRepo{}
		rec := post(&NotificationsHandler{Repo: repo}, map[string]any{"keys": keys}, true)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "too many keys")
		assert.Nil(t, repo.gotKeys, "não chega ao repo")
	})
	t.Run("200 chaves exatas passam", func(t *testing.T) {
		keys := make([]string, 200)
		for i := range keys {
			keys[i] = "k"
		}
		repo := &covANotifRepo{markN: 200}
		rec := post(&NotificationsHandler{Repo: repo}, map[string]any{"keys": keys}, true)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Len(t, repo.gotKeys, 200)
	})
	t.Run("sem repo devolve zero", func(t *testing.T) {
		rec := post(&NotificationsHandler{}, map[string]any{"keys": []string{"a"}}, true)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.EqualValues(t, 0, covADecode(t, rec)["marked"])
	})
	t.Run("erro do repo 500", func(t *testing.T) {
		log, logs := covALogger()
		rec := post(&NotificationsHandler{Repo: &covANotifRepo{markErr: errors.New("db")}, Log: log}, map[string]any{"keys": []string{"a"}}, true)
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		_, ok := covAHasLog(logs, "notifications.mark_read_failed")
		assert.True(t, ok)
	})
	t.Run("sucesso repassa usuário e chaves", func(t *testing.T) {
		repo := &covANotifRepo{markN: 2}
		rec := post(&NotificationsHandler{Repo: repo}, map[string]any{"keys": []string{"a", "b"}}, true)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.EqualValues(t, 2, covADecode(t, rec)["marked"])
		assert.Equal(t, user, repo.gotUser)
		assert.Equal(t, []string{"a", "b"}, repo.gotKeys)
	})
}

func TestCovA_Notifications_MarkAllRead(t *testing.T) {
	user := uuid.New()
	post := func(h *NotificationsHandler, claims bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/mark-all-read", nil)
		if claims {
			req = covAWith(req, covAClaims("admin", user))
		}
		return covAServe(http.HandlerFunc(h.MarkAllRead), req)
	}

	assert.Equal(t, http.StatusUnauthorized, post(&NotificationsHandler{}, false).Code)

	rec := post(&NotificationsHandler{}, true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.EqualValues(t, 0, covADecode(t, rec)["marked"], "sem repo")

	log, logs := covALogger()
	rec = post(&NotificationsHandler{Repo: &covANotifRepo{markAllErr: errors.New("db")}, Log: log}, true)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	_, ok := covAHasLog(logs, "notifications.mark_all_read_failed")
	assert.True(t, ok)

	repo := &covANotifRepo{markAllN: 5}
	rec = post(&NotificationsHandler{Repo: repo}, true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.EqualValues(t, 5, covADecode(t, rec)["marked"])
	assert.Equal(t, user, repo.gotUser)
}

// ── Digest diário de falhas ──────────────────────────────────────────────────

type covADigestCamp struct {
	res *catalog.DailyResult
	err error
	day time.Time
}

func (f *covADigestCamp) ListForDate(_ context.Context, day time.Time) (*catalog.DailyResult, error) {
	f.day = day
	return f.res, f.err
}

type covADigestSeen struct {
	seen    bool
	readErr error
	markErr error
	keys    []string
	user    uuid.UUID
}

func (f *covADigestSeen) HasRead(_ context.Context, u uuid.UUID, key string) (bool, error) {
	f.user = u
	f.keys = append(f.keys, key)
	return f.seen, f.readErr
}
func (f *covADigestSeen) MarkRead(_ context.Context, u uuid.UUID, keys []string) (int, error) {
	f.user = u
	f.keys = append(f.keys, keys...)
	return len(keys), f.markErr
}

func TestCovA_Digest_Get(t *testing.T) {
	user := uuid.New()
	ontem := yesterdayLocal().Format("2006-01-02")
	get := func(h *DailyFailuresDigestHandler, claims bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if claims {
			req = covAWith(req, covAClaims("admin", user))
		}
		return covAServe(http.HandlerFunc(h.Get), req)
	}
	camp := uuid.New()
	daily := &catalog.DailyResult{
		Date:    ontem,
		Summary: catalog.CampaignDailySummary{Campaigns: 1, Stations: 2},
		Campaigns: []catalog.CampaignDailyFailure{{
			Campaign: catalog.CampaignInfo{ID: camp, Name: "Campanha Teste", ClientName: "Cliente Teste"},
			Stations: []catalog.CampaignFailureStation{{}, {}},
		}},
	}

	t.Run("sem claims 401", func(t *testing.T) {
		assert.Equal(t, http.StatusUnauthorized, get(&DailyFailuresDigestHandler{}, false).Code)
	})

	t.Run("sem repo de campanhas devolve vazio com a data de ontem", func(t *testing.T) {
		rec := get(&DailyFailuresDigestHandler{}, true)
		require.Equal(t, http.StatusOK, rec.Code)
		body := covADecode(t, rec)
		assert.Equal(t, ontem, body["date"])
		assert.Equal(t, []any{}, body["campaigns"])
	})

	t.Run("erro ao listar falhas 500 com log", func(t *testing.T) {
		log, logs := covALogger()
		h := &DailyFailuresDigestHandler{Campaigns: &covADigestCamp{err: errors.New("db")}, Log: log}
		assert.Equal(t, http.StatusInternalServerError, get(h, true).Code)
		_, ok := covAHasLog(logs, "daily_failures_digest_list_failed")
		assert.True(t, ok)
	})

	t.Run("erro ao ler o visto 500 com log", func(t *testing.T) {
		log, logs := covALogger()
		h := &DailyFailuresDigestHandler{Campaigns: &covADigestCamp{res: daily}, Seen: &covADigestSeen{readErr: errors.New("db")}, Log: log}
		assert.Equal(t, http.StatusInternalServerError, get(h, true).Code)
		_, ok := covAHasLog(logs, "daily_failures_digest_hasread_failed")
		assert.True(t, ok)
	})

	t.Run("monta o digest leve e consulta a chave do dia", func(t *testing.T) {
		seen := &covADigestSeen{seen: true}
		h := &DailyFailuresDigestHandler{Campaigns: &covADigestCamp{res: daily}, Seen: seen}
		rec := get(h, true)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		body := covADecode(t, rec)
		assert.Equal(t, true, body["seen"])
		assert.EqualValues(t, 1, body["summary"].(map[string]any)["campaigns"])
		assert.EqualValues(t, 2, body["summary"].(map[string]any)["stations"])
		cs := body["campaigns"].([]any)
		require.Len(t, cs, 1)
		assert.Equal(t, "Campanha Teste", cs[0].(map[string]any)["name"])
		assert.Equal(t, "Cliente Teste", cs[0].(map[string]any)["client_name"])
		assert.EqualValues(t, 2, cs[0].(map[string]any)["stations_failed"])
		assert.Equal(t, []string{"daily_failures_digest:" + ontem}, seen.keys)
		assert.Equal(t, user, seen.user)
	})

	t.Run("sem repo de visto: seen=false", func(t *testing.T) {
		h := &DailyFailuresDigestHandler{Campaigns: &covADigestCamp{res: daily}}
		rec := get(h, true)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, false, covADecode(t, rec)["seen"])
	})
}

func TestCovA_Digest_Ack(t *testing.T) {
	user := uuid.New()
	ontem := yesterdayLocal().Format("2006-01-02")
	post := func(h *DailyFailuresDigestHandler, claims bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/ack", nil)
		if claims {
			req = covAWith(req, covAClaims("admin", user))
		}
		return covAServe(http.HandlerFunc(h.Ack), req)
	}

	assert.Equal(t, http.StatusUnauthorized, post(&DailyFailuresDigestHandler{}, false).Code)

	rec := post(&DailyFailuresDigestHandler{}, true)
	require.Equal(t, http.StatusOK, rec.Code, "sem repo de visto o ack é no-op")
	assert.Equal(t, true, covADecode(t, rec)["acked"])

	log, logs := covALogger()
	assert.Equal(t, http.StatusInternalServerError,
		post(&DailyFailuresDigestHandler{Seen: &covADigestSeen{markErr: errors.New("db")}, Log: log}, true).Code)
	_, ok := covAHasLog(logs, "daily_failures_digest_ack_failed")
	assert.True(t, ok)

	seen := &covADigestSeen{}
	rec = post(&DailyFailuresDigestHandler{Seen: seen}, true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []string{"daily_failures_digest:" + ontem}, seen.keys)
	assert.Equal(t, user, seen.user)
}

func TestCovA_Digest_FromDailyEhSliceNaoNil(t *testing.T) {
	out := digestFromDaily(&catalog.DailyResult{Date: "2026-06-01"}, true)
	assert.NotNil(t, out.Campaigns, "o JSON tem de sair [] e não null")
	assert.Empty(t, out.Campaigns)
	assert.True(t, out.Seen)
	assert.Equal(t, "2026-06-01", out.Date)
}

// ── Falhas por campanha ──────────────────────────────────────────────────────

type covACampFailRepo struct {
	daily    *catalog.DailyResult
	dailyErr error
	hist     *catalog.HistoricalResult
	histErr  error
	detail   *catalog.DetailResult
	detErr   error

	gotDay      time.Time
	gotPage     int
	gotPageSize int
	gotID       uuid.UUID
}

func (f *covACampFailRepo) ListForDate(_ context.Context, d time.Time) (*catalog.DailyResult, error) {
	f.gotDay = d
	return f.daily, f.dailyErr
}
func (f *covACampFailRepo) ListHistorical(_ context.Context, p, s int) (*catalog.HistoricalResult, error) {
	f.gotPage, f.gotPageSize = p, s
	return f.hist, f.histErr
}
func (f *covACampFailRepo) Get(_ context.Context, id uuid.UUID) (*catalog.DetailResult, error) {
	f.gotID = id
	return f.detail, f.detErr
}

func TestCovA_CampaignFailures_GetList(t *testing.T) {
	get := func(h *CampaignFailuresHandler, qs string) *httptest.ResponseRecorder {
		return covAServe(http.HandlerFunc(h.GetList), httptest.NewRequest(http.MethodGet, "/?"+qs, nil))
	}
	hoje := time.Now().Format("2006-01-02")
	ontem := time.Now().AddDate(0, 0, -1).Format("2006-01-02")

	t.Run("recusas de parâmetro", func(t *testing.T) {
		h := &CampaignFailuresHandler{Repo: &covACampFailRepo{}}
		casos := []struct{ nome, qs, trecho string }{
			{"historical com date", "mode=historical&date=" + ontem, "does not accept date"},
			{"modo desconhecido", "mode=lixo", "invalid mode"},
			{"data malformada", "date=01/06/2026", "expected YYYY-MM-DD"},
			{"data no futuro", "date=" + time.Now().AddDate(0, 0, 3).Format("2006-01-02"), "future not supported"},
			{"data com mais de 90 dias", "date=" + time.Now().AddDate(0, 0, -120).Format("2006-01-02"), "older than 90 days"},
		}
		for _, c := range casos {
			t.Run(c.nome, func(t *testing.T) {
				rec := get(h, c.qs)
				assert.Equal(t, http.StatusBadRequest, rec.Code)
				assert.Contains(t, rec.Body.String(), c.trecho)
			})
		}
	})

	t.Run("modo por data sem repo devolve vazio", func(t *testing.T) {
		rec := get(&CampaignFailuresHandler{}, "date="+hoje)
		require.Equal(t, http.StatusOK, rec.Code)
		body := covADecode(t, rec)
		assert.Equal(t, "by_date", body["mode"])
		assert.Equal(t, hoje, body["date"])
		assert.Equal(t, []any{}, body["campaigns"])
	})

	t.Run("modo histórico sem repo devolve página pedida", func(t *testing.T) {
		rec := get(&CampaignFailuresHandler{}, "mode=historical&page=3&page_size=10")
		require.Equal(t, http.StatusOK, rec.Code)
		body := covADecode(t, rec)
		assert.Equal(t, "historical", body["mode"])
		assert.EqualValues(t, 3, body["page"])
		assert.EqualValues(t, 10, body["page_size"])
		assert.EqualValues(t, 0, body["total"])
	})

	t.Run("histórico repassa página e tamanho (com defaults)", func(t *testing.T) {
		repo := &covACampFailRepo{hist: &catalog.HistoricalResult{Mode: "historical", Total: 4, Campaigns: []catalog.CampaignHistoricalRow{{TotalDeficit: 9}}}}
		h := &CampaignFailuresHandler{Repo: repo}
		rec := get(h, "mode=historical")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, 1, repo.gotPage)
		assert.Equal(t, 50, repo.gotPageSize)
		assert.EqualValues(t, 4, covADecode(t, rec)["total"])

		get(h, "mode=historical&page=abc&page_size=7")
		assert.Equal(t, 1, repo.gotPage, "página malformada cai no default")
		assert.Equal(t, 7, repo.gotPageSize)
	})

	t.Run("histórico com erro do repo vira 500 e loga", func(t *testing.T) {
		log, logs := covALogger()
		h := &CampaignFailuresHandler{Repo: &covACampFailRepo{histErr: errors.New("db")}, Log: log}
		assert.Equal(t, http.StatusInternalServerError, get(h, "mode=historical").Code)
		_, ok := covAHasLog(logs, "campaign_failures_historical_failed")
		assert.True(t, ok)
	})

	t.Run("por data: usa ontem por padrão e repassa o dia", func(t *testing.T) {
		repo := &covACampFailRepo{daily: &catalog.DailyResult{Mode: "by_date", Date: ontem}}
		h := &CampaignFailuresHandler{Repo: repo}
		rec := get(h, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, ontem, repo.gotDay.Format("2006-01-02"))

		rec = get(h, "mode=by_date&date="+hoje)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, hoje, repo.gotDay.Format("2006-01-02"))
	})

	t.Run("por data com erro do repo vira 500 e loga", func(t *testing.T) {
		log, logs := covALogger()
		h := &CampaignFailuresHandler{Repo: &covACampFailRepo{dailyErr: errors.New("db")}, Log: log}
		assert.Equal(t, http.StatusInternalServerError, get(h, "date="+ontem).Code)
		_, ok := covAHasLog(logs, "campaign_failures_daily_failed")
		assert.True(t, ok)
	})
}

func TestCovA_CampaignFailures_GetByID(t *testing.T) {
	id := uuid.New()
	rotear := func(h *CampaignFailuresHandler) http.Handler {
		r := chi.NewRouter()
		r.Get("/campaign-failures/{id}", h.GetByID)
		return r
	}
	get := func(h *CampaignFailuresHandler, raw string) *httptest.ResponseRecorder {
		return covAServe(rotear(h), httptest.NewRequest(http.MethodGet, "/campaign-failures/"+raw, nil))
	}

	assert.Equal(t, http.StatusBadRequest, get(&CampaignFailuresHandler{}, "nao-uuid").Code)
	assert.Equal(t, http.StatusNotFound, get(&CampaignFailuresHandler{}, id.String()).Code, "sem repo")

	repo := &covACampFailRepo{detErr: catalog.ErrCampaignNotFound}
	rec := get(&CampaignFailuresHandler{Repo: repo}, id.String())
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "campaign not found")
	assert.Equal(t, id, repo.gotID)

	log, logs := covALogger()
	rec = get(&CampaignFailuresHandler{Repo: &covACampFailRepo{detErr: errors.New("db")}, Log: log}, id.String())
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	_, ok := covAHasLog(logs, "campaign_failures_detail_failed")
	assert.True(t, ok)

	repo = &covACampFailRepo{detail: &catalog.DetailResult{
		Campaign: catalog.CampaignInfo{ID: id, Name: "Campanha Teste"},
		Summary:  catalog.DetailSummary{TotalDeficit: 12},
	}}
	rec = get(&CampaignFailuresHandler{Repo: repo}, id.String())
	require.Equal(t, http.StatusOK, rec.Code)
	body := covADecode(t, rec)
	assert.Equal(t, "Campanha Teste", body["campaign"].(map[string]any)["name"])
	assert.EqualValues(t, 12, body["summary"].(map[string]any)["total_deficit"])
}

func TestCovA_CampaignFailures_ParseIntDefault(t *testing.T) {
	assert.Equal(t, 5, parseIntDefault("", 5))
	assert.Equal(t, 5, parseIntDefault("x", 5))
	assert.Equal(t, 9, parseIntDefault("9", 5))
	assert.Equal(t, -3, parseIntDefault("-3", 5))
}

// ── Falhas por emissora ──────────────────────────────────────────────────────

type covAStationFailRepo struct {
	res     *catalog.Result
	err     error
	gotDay  time.Time
	gotDown int
}

func (f *covAStationFailRepo) ListForDate(_ context.Context, d time.Time, m int) (*catalog.Result, error) {
	f.gotDay, f.gotDown = d, m
	return f.res, f.err
}

func TestCovA_StationFailures_Get(t *testing.T) {
	get := func(h *StationFailuresHandler, qs string) *httptest.ResponseRecorder {
		return covAServe(http.HandlerFunc(h.Get), httptest.NewRequest(http.MethodGet, "/?"+qs, nil))
	}
	hoje := time.Now().Format("2006-01-02")
	ontem := time.Now().AddDate(0, 0, -1).Format("2006-01-02")

	recusas := []struct{ nome, qs, trecho string }{
		{"data malformada", "date=ontem", "expected YYYY-MM-DD"},
		{"futuro", "date=" + time.Now().AddDate(0, 0, 2).Format("2006-01-02"), "future not supported"},
		{"antiga", "date=" + time.Now().AddDate(0, 0, -100).Format("2006-01-02"), "older than 90 days"},
	}
	for _, c := range recusas {
		t.Run(c.nome, func(t *testing.T) {
			rec := get(&StationFailuresHandler{}, c.qs)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Contains(t, rec.Body.String(), c.trecho)
		})
	}

	t.Run("sem repo devolve vazio na data pedida", func(t *testing.T) {
		rec := get(&StationFailuresHandler{}, "date="+hoje)
		require.Equal(t, http.StatusOK, rec.Code)
		body := covADecode(t, rec)
		assert.Equal(t, hoje, body["date"])
		assert.Equal(t, []any{}, body["stations"])
	})

	t.Run("min_down_seconds: padrão 60 e limites", func(t *testing.T) {
		casos := []struct {
			qs   string
			want int
		}{
			{"", 60},
			{"min_down_seconds=0", 0},
			{"min_down_seconds=3600", 3600},
			{"min_down_seconds=3601", 60},
			{"min_down_seconds=-1", 60},
			{"min_down_seconds=abc", 60},
			{"min_down_seconds=300", 300},
		}
		for _, c := range casos {
			repo := &covAStationFailRepo{res: &catalog.Result{}}
			rec := get(&StationFailuresHandler{Repo: repo}, c.qs)
			require.Equal(t, http.StatusOK, rec.Code, "qs=%q", c.qs)
			assert.Equal(t, c.want, repo.gotDown, "qs=%q", c.qs)
			assert.Equal(t, ontem, repo.gotDay.Format("2006-01-02"), "data padrão é ontem")
		}
	})

	t.Run("erro do repo vira 500 e loga a data", func(t *testing.T) {
		log, logs := covALogger()
		h := &StationFailuresHandler{Repo: &covAStationFailRepo{err: errors.New("db")}, Log: log}
		assert.Equal(t, http.StatusInternalServerError, get(h, "date="+ontem).Code)
		e, ok := covAHasLog(logs, "station_failures_query_failed")
		require.True(t, ok)
		assert.Equal(t, ontem, e.ContextMap()["date"])
	})
}

// ── Falhas por dia ───────────────────────────────────────────────────────────

type covADailyRepo struct {
	res    *catalog.FailuresDailyResult
	err    error
	from   time.Time
	to     time.Time
	minSec int
}

func (f *covADailyRepo) ListDaily(_ context.Context, from, to time.Time, m int) (*catalog.FailuresDailyResult, error) {
	f.from, f.to, f.minSec = from, to, m
	return f.res, f.err
}

func TestCovA_FailuresDaily_Get(t *testing.T) {
	get := func(h *FailuresDailyHandler, qs string) *httptest.ResponseRecorder {
		return covAServe(http.HandlerFunc(h.Get), httptest.NewRequest(http.MethodGet, "/?"+qs, nil))
	}
	fmtD := func(d time.Time) string { return d.Format("2006-01-02") }
	hoje := time.Now()
	dia := func(n int) string { return fmtD(hoje.AddDate(0, 0, n)) }

	recusas := []struct{ nome, qs, trecho string }{
		{"to malformado", "to=amanha", "invalid to"},
		{"from malformado", "from=ontem", "invalid from: expected"},
		{"from depois de to", "from=" + dia(-1) + "&to=" + dia(-5), "from after to"},
		{"from com mais de 90 dias", "from=" + dia(-95) + "&to=" + dia(-90), "older than 90 days"},
	}
	for _, c := range recusas {
		t.Run(c.nome, func(t *testing.T) {
			rec := get(&FailuresDailyHandler{Repo: &covADailyRepo{}}, c.qs)
			assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), c.trecho)
		})
	}

	t.Run("sem repo: janela padrão de 30 dias terminando hoje", func(t *testing.T) {
		rec := get(&FailuresDailyHandler{}, "")
		require.Equal(t, http.StatusOK, rec.Code)
		body := covADecode(t, rec)
		assert.Equal(t, dia(-29), body["from"])
		assert.Equal(t, dia(0), body["to"])
		assert.Equal(t, []any{}, body["days"])
	})

	t.Run("to no futuro é cortado em hoje, não é erro", func(t *testing.T) {
		repo := &covADailyRepo{res: &catalog.FailuresDailyResult{}}
		rec := get(&FailuresDailyHandler{Repo: repo}, "from="+dia(-10)+"&to="+dia(20))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, dia(0), fmtD(repo.to))
		assert.Equal(t, dia(-10), fmtD(repo.from))
		assert.Equal(t, 60, repo.minSec)
	})

	t.Run("min_down_seconds válido é repassado", func(t *testing.T) {
		repo := &covADailyRepo{res: &catalog.FailuresDailyResult{}}
		rec := get(&FailuresDailyHandler{Repo: repo}, "min_down_seconds=300")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, 300, repo.minSec)
		get(&FailuresDailyHandler{Repo: repo}, "min_down_seconds=99999")
		assert.Equal(t, 60, repo.minSec, "fora do intervalo cai no padrão")
	})

	t.Run("erro do repo 500 com log", func(t *testing.T) {
		log, logs := covALogger()
		h := &FailuresDailyHandler{Repo: &covADailyRepo{err: errors.New("db")}, Log: log}
		assert.Equal(t, http.StatusInternalServerError, get(h, "").Code)
		_, ok := covAHasLog(logs, "failures_daily_query_failed")
		assert.True(t, ok)
	})

	t.Run("sucesso devolve o corpo do repo", func(t *testing.T) {
		repo := &covADailyRepo{res: &catalog.FailuresDailyResult{
			From: "2026-06-01", To: "2026-06-03",
			Days: []catalog.DailyPoint{{Date: "2026-06-02", Stations: 3, Deficit: 8}},
		}}
		rec := get(&FailuresDailyHandler{Repo: repo}, "")
		require.Equal(t, http.StatusOK, rec.Code)
		days := covADecode(t, rec)["days"].([]any)
		require.Len(t, days, 1)
		assert.EqualValues(t, 3, days[0].(map[string]any)["stations"])
		assert.True(t, strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json"))
	})
}
