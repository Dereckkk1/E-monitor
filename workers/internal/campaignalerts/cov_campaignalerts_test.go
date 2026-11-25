package campaignalerts

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"radiocheck/internal/calendar"
	"radiocheck/internal/metrics"
	"radiocheck/internal/users"
)

// --- funcoes puras ----------------------------------------------------------

func TestCovZ_FmtDur(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0min"},
		{45 * time.Minute, "45min"},
		{59*time.Minute + 59*time.Second, "59min"},
		{time.Hour, "1h00"},
		{2*time.Hour + 7*time.Minute, "2h07"},
		{11*time.Hour + 32*time.Minute, "11h32"},
		{26 * time.Hour, "26h00"},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) { assert.Equal(t, c.want, fmtDur(c.d)) })
	}
}

func TestCovZ_FormatLocationEdgeCases(t *testing.T) {
	str := func(s string) *string { return &s }
	cases := []struct {
		name        string
		city, state *string
		want        string
	}{
		{"cidade e uf", str("Curitiba"), str("PR"), "Curitiba/PR"},
		{"so cidade", str("Curitiba"), nil, "Curitiba"},
		{"cidade com espacos e uf vazia", str("  Curitiba "), str("  "), "Curitiba"},
		{"so uf", nil, str("PR"), "PR"},
		{"nada", nil, nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { assert.Equal(t, c.want, formatLocation(c.city, c.state)) })
	}
}

func TestCovZ_SubjectCount(t *testing.T) {
	assert.Equal(t, "1 campanha iniciando", subjectCount(1, "campanha iniciando", "campanhas iniciando"))
	assert.Equal(t, "3 campanhas iniciando", subjectCount(3, "campanha iniciando", "campanhas iniciando"))
	assert.Equal(t, "0 campanhas iniciando", subjectCount(0, "campanha iniciando", "campanhas iniciando"))
}

func TestCovZ_DisplayName(t *testing.T) {
	assert.Equal(t, "Maria Teste", displayName(users.User{Name: "Maria Teste", Email: "maria@example.com"}))
	assert.Equal(t, "maria@example.com", displayName(users.User{Email: "maria@example.com"}), "sem nome cai no email")
}

func TestCovZ_RenderEnding(t *testing.T) {
	one := []CampaignAlert{{
		Name: "Promo Teste", ClientName: "Cliente Teste",
		StartDate:    time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:      time.Date(2026, 6, 9, 0, 0, 0, 0, time.UTC),
		StationCount: 4,
	}}
	c, err := RenderEnding("Maria Teste", one, "https://painel.example.com")
	require.NoError(t, err)
	assert.Equal(t, "1 campanha terminando", c.Subject)
	for _, want := range []string{"Promo Teste", "Cliente Teste", "09/06/2026", "Maria Teste", "https://painel.example.com/campaigns"} {
		assert.Contains(t, c.HTML, want)
	}
	for _, want := range []string{"Promo Teste", "09/06/2026", "Maria Teste", "https://painel.example.com/campaigns"} {
		assert.Contains(t, c.Text, want)
	}

	many := append(one, one[0])
	c, err = RenderEnding("Maria Teste", many, "https://painel.example.com")
	require.NoError(t, err)
	assert.Equal(t, "2 campanhas terminando", c.Subject)
}

func TestCovZ_RenderSubjects(t *testing.T) {
	alerts := []CampaignAlert{{Name: "A"}, {Name: "B"}}
	c, err := RenderStartingNoMaterial("X", alerts[:1], "https://x.example.com")
	require.NoError(t, err)
	assert.Contains(t, c.Subject, "1 campanha sem material")

	c, err = RenderStarting("X", alerts, "https://x.example.com")
	require.NoError(t, err)
	assert.Equal(t, "2 campanhas iniciando", c.Subject)
}

func TestCovZ_RenderStationsOffline_Singular(t *testing.T) {
	out := []StationOutage{{
		StationName: "Radio Teste FM", Dial: "FM 99,9", Location: "Cidade Teste/SP",
		Day: time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC), Down: 3*time.Hour + 4*time.Minute,
	}}
	c, err := RenderStationsOffline("Maria Teste", out, "de 11/06", "https://painel.example.com")
	require.NoError(t, err)
	assert.Contains(t, c.Subject, "1 emissora fora do ar por mais de 2h")
	assert.Contains(t, c.HTML, "3h04")
	assert.Contains(t, c.Text, "Radio Teste FM")
	assert.Contains(t, c.Text, "3h04")
}

func TestCovZ_NewServiceAndScheduler_Defaults(t *testing.T) {
	svc := NewService(nil, nil, covZLister{}, &covZMailer{}, "https://x.example.com", nil)
	require.NotNil(t, svc.log, "logger nulo vira Nop")
	assert.Equal(t, 3, svc.sendRetries)
	assert.Equal(t, "https://x.example.com", svc.baseURL)

	sch := NewScheduler(nil, svc, 8, nil)
	require.NotNil(t, sch.log)
	assert.Equal(t, 5*time.Minute, sch.Interval)
	assert.Equal(t, 8, sch.sendHour)
	require.NotNil(t, sch.NowFn)
	assert.WithinDuration(t, time.Now(), sch.NowFn(), time.Minute)
}

func TestCovZ_Service_TypesNames(t *testing.T) {
	svc := NewService(New(nil), nil, covZLister{}, &covZMailer{}, "", nil)
	var names []string
	for _, a := range svc.types() {
		names = append(names, a.name)
		assert.NotNil(t, a.prepare, a.name)
	}
	assert.Equal(t, []string{"starting_no_material", "starting", "ending", "stations_offline"}, names)
}

// --- sendWithRetry ----------------------------------------------------------

func TestCovZ_SendWithRetry(t *testing.T) {
	content := EmailContent{Subject: "S", HTML: "<p>h</p>", Text: "t"}

	t.Run("sucesso na primeira tentativa", func(t *testing.T) {
		m := &covZMailer{}
		svc := &Service{mail: m, log: zap.NewNop(), sendRetries: 3}
		assert.True(t, svc.sendWithRetry(context.Background(), "a@example.com", content))
		sent := m.Sent()
		require.Len(t, sent, 1)
		assert.Equal(t, []string{"a@example.com"}, sent[0].To)
		assert.Equal(t, "S", sent[0].Subject)
		assert.Equal(t, "<p>h</p>", sent[0].HTML)
		assert.Equal(t, "t", sent[0].Text)
	})

	t.Run("zero tentativas configuradas falha sem enviar", func(t *testing.T) {
		m := &covZMailer{}
		core, logs := observer.New(zap.WarnLevel)
		svc := &Service{mail: m, log: zap.New(core), sendRetries: 0}
		assert.False(t, svc.sendWithRetry(context.Background(), "a@example.com", content))
		assert.Zero(t, m.calls)
		assert.Equal(t, 1, logs.FilterMessage("campaignalerts: envio falhou após retries").Len())
	})

	t.Run("contexto cancelado durante a espera aborta o retry", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		m := &covZMailer{failFirst: 99, onSend: cancel}
		svc := &Service{mail: m, log: zap.NewNop(), sendRetries: 3}
		assert.False(t, svc.sendWithRetry(ctx, "a@example.com", content))
		assert.Equal(t, 1, m.calls, "cancelou na 1a tentativa: nao tenta de novo")
	})

	t.Run("falha na primeira e sucesso na segunda", func(t *testing.T) {
		// O backoff do codigo e fixo (attempt*2s): custa ~2s de relogio.
		m := &covZMailer{failFirst: 1}
		svc := &Service{mail: m, log: zap.NewNop(), sendRetries: 3}
		start := time.Now()
		assert.True(t, svc.sendWithRetry(context.Background(), "a@example.com", content))
		assert.Equal(t, 2, m.calls)
		assert.GreaterOrEqual(t, time.Since(start), 1500*time.Millisecond, "houve espera de backoff")
		assert.Len(t, m.Sent(), 1)
	})
}

// --- ProcessType (LogStore real, mailer e destinatarios falsos) -------------

func covZCounter(name string) float64 {
	return testutil.ToFloat64(metrics.NotificationsSentTotal.WithLabelValues(name))
}

func covZFailedCounter(name string) float64 {
	return testutil.ToFloat64(metrics.NotificationsFailedTotal.WithLabelValues(name))
}

// covZAlertType monta um alertType com count e renderer controlados.
func covZAlertType(name string, count int, prepErr error, render func(string) (EmailContent, error)) alertType {
	return alertType{name: name, prepare: func(context.Context, time.Time) (int, func(string) (EmailContent, error), error) {
		if prepErr != nil {
			return 0, nil, prepErr
		}
		return count, render, nil
	}}
}

func TestCovZ_ProcessType(t *testing.T) {
	ctx, pool := newTestDB(t)
	logs := NewLogStore(pool)
	okRender := func(name string) (EmailContent, error) {
		return EmailContent{Subject: "Assunto para " + name, HTML: "<p>" + name + "</p>", Text: name}, nil
	}

	// uma data (e tipo) distinta por subteste: PK do notification_log e
	// (data, tipo).
	day := time.Date(2031, 3, 3, 0, 0, 0, 0, time.UTC)
	covZCleanLog(t, pool, day, day.AddDate(0, 0, 1), day.AddDate(0, 0, 2), day.AddDate(0, 0, 3), day.AddDate(0, 0, 4), day.AddDate(0, 0, 5))

	newSvc := func(m *covZMailer, l covZLister) *Service {
		return &Service{logs: logs, users: l, mail: m, log: zap.NewNop(), baseURL: "https://x.example.com", sendRetries: 0}
	}

	t.Run("prepare com erro registra failed e conta a falha", func(t *testing.T) {
		d := day
		before := covZFailedCounter("starting")
		m := &covZMailer{}
		svc := newSvc(m, covZLister{list: []users.User{{Email: "a@example.com"}}})
		svc.ProcessType(ctx, d, covZAlertType("starting", 0, errors.New("consulta quebrou"), nil))

		got := covZLogRows(t, ctx, pool, d)["starting"]
		assert.Equal(t, "failed", got.Status)
		assert.Equal(t, "consulta quebrou", got.Error)
		assert.Empty(t, m.Sent())
		assert.Equal(t, before+1, covZFailedCounter("starting"))
	})

	t.Run("sem itens registra skipped_empty e nao envia", func(t *testing.T) {
		d := day.AddDate(0, 0, 1)
		m := &covZMailer{}
		svc := newSvc(m, covZLister{list: []users.User{{Email: "a@example.com"}}})
		svc.ProcessType(ctx, d, covZAlertType("ending", 0, nil, okRender))

		assert.Equal(t, "skipped_empty", covZLogRows(t, ctx, pool, d)["ending"].Status)
		assert.Empty(t, m.Sent())
	})

	t.Run("falha ao listar destinatarios registra failed com a contagem", func(t *testing.T) {
		d := day.AddDate(0, 0, 2)
		before := covZFailedCounter("ending")
		m := &covZMailer{}
		svc := newSvc(m, covZLister{err: errors.New("db fora")})
		svc.ProcessType(ctx, d, covZAlertType("ending", 4, nil, okRender))

		got := covZLogRows(t, ctx, pool, d)["ending"]
		assert.Equal(t, "failed", got.Status)
		assert.Equal(t, 4, got.CampaignCount)
		assert.Equal(t, "db fora", got.Error)
		assert.Empty(t, m.Sent())
		assert.Equal(t, before+1, covZFailedCounter("ending"))
	})

	t.Run("envia para todos, pula email vazio e usa o nome de exibicao", func(t *testing.T) {
		d := day.AddDate(0, 0, 3)
		beforeSent := covZCounter("starting_no_material")
		m := &covZMailer{}
		svc := newSvc(m, covZLister{list: []users.User{
			{Email: "maria@example.com", Name: "Maria Teste"},
			{Email: ""}, // sem email: ignorado
			{Email: "joao@example.com"},
		}})
		svc.sendRetries = 1
		svc.ProcessType(ctx, d, covZAlertType("starting_no_material", 2, nil, okRender))

		sent := m.Sent()
		require.Len(t, sent, 2)
		assert.Equal(t, []string{"maria@example.com"}, sent[0].To)
		assert.Equal(t, "Assunto para Maria Teste", sent[0].Subject)
		assert.Equal(t, []string{"joao@example.com"}, sent[1].To)
		assert.Equal(t, "Assunto para joao@example.com", sent[1].Subject, "sem nome usa o email")

		got := covZLogRows(t, ctx, pool, d)["starting_no_material"]
		assert.Equal(t, "sent", got.Status)
		assert.Equal(t, 2, got.RecipientCount)
		assert.Equal(t, 2, got.CampaignCount)
		assert.Equal(t, beforeSent+2, covZCounter("starting_no_material"))
		assert.Equal(t, float64(3), testutil.ToFloat64(metrics.NotificationsRecipients.WithLabelValues("starting_no_material")),
			"gauge conta destinatarios listados, incluindo o sem email")
	})

	t.Run("render com erro para um destinatario gera status partial", func(t *testing.T) {
		d := day.AddDate(0, 0, 4)
		beforeFailed := covZFailedCounter("starting")
		m := &covZMailer{}
		svc := newSvc(m, covZLister{list: []users.User{
			{Email: "ok@example.com", Name: "Ok"},
			{Email: "ruim@example.com", Name: "Ruim"},
		}})
		svc.sendRetries = 1
		render := func(name string) (EmailContent, error) {
			if name == "Ruim" {
				return EmailContent{}, errors.New("template quebrou")
			}
			return okRender(name)
		}
		svc.ProcessType(ctx, d, covZAlertType("starting", 1, nil, render))

		require.Len(t, m.Sent(), 1)
		got := covZLogRows(t, ctx, pool, d)["starting"]
		assert.Equal(t, "partial", got.Status)
		assert.Equal(t, 1, got.RecipientCount)
		assert.Equal(t, beforeFailed+1, covZFailedCounter("starting"))
	})

	t.Run("todos os envios falhando gera status failed", func(t *testing.T) {
		d := day.AddDate(0, 0, 5)
		m := &covZMailer{}
		svc := newSvc(m, covZLister{list: []users.User{{Email: "a@example.com"}, {Email: "b@example.com"}}})
		svc.sendRetries = 0 // nenhuma tentativa: cada destinatario conta como falha
		svc.ProcessType(ctx, d, covZAlertType("ending", 1, nil, okRender))

		got := covZLogRows(t, ctx, pool, d)["ending"]
		assert.Equal(t, "failed", got.Status)
		assert.Equal(t, 0, got.RecipientCount)
		assert.Empty(t, m.Sent())
	})
}

func TestCovZ_LogStore_RecordWithErrorIsIdempotent(t *testing.T) {
	ctx, pool := newTestDB(t)
	store := NewLogStore(pool)
	day := time.Date(2031, 4, 7, 0, 0, 0, 0, time.UTC)
	covZCleanLog(t, pool, day)

	require.NoError(t, store.Record(ctx, LogEntry{Date: day, Type: "ending", Status: "failed", Error: "boom", CampaignCount: 2}))
	// Segunda gravacao para a mesma (data,tipo) e ignorada: o primeiro resultado prevalece.
	require.NoError(t, store.Record(ctx, LogEntry{Date: day, Type: "ending", Status: "sent", RecipientCount: 9}))

	got := covZLogRows(t, ctx, pool, day)["ending"]
	assert.Equal(t, "failed", got.Status)
	assert.Equal(t, "boom", got.Error)
	assert.Equal(t, 2, got.CampaignCount)
	assert.Zero(t, got.RecipientCount)

	// Tipo fora do CHECK da tabela e rejeitado e o erro e propagado.
	assert.Error(t, store.Record(ctx, LogEntry{Date: day, Type: "tipo_invalido", Status: "sent"}))
}

// --- StationsOffline (consulta real) ----------------------------------------

func TestCovZ_StationsOffline(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := New(pool)

	d := covZFutureWeekday(time.Tuesday, 12)
	ws := calendar.BRMidnight(d)
	we := calendar.BRMidnight(d.AddDate(0, 0, 2))

	// A: dois downs sobrepostos [0h,3h] e [1h,4h] => 4h no dia D (merge).
	stA := covZStation(t, ctx, pool, "Radio Teste A FM", "FM", 103.9, "Curitiba", "PR")
	covZEvent(t, ctx, pool, stA, "down", ws, 3*3600)
	covZEvent(t, ctx, pool, stA, "down", ws.Add(time.Hour), 3*3600)
	// B: [23h, 27h] atravessa a meia-noite: 1h em D, 3h em D+1 => so D+1 passa de 2h.
	stB := covZStation(t, ctx, pool, "Radio Teste B AM", "AM", 860, "Cidade Teste", "SP")
	covZEvent(t, ctx, pool, stB, "down", ws.Add(23*time.Hour), 4*3600)
	// C: 1h de down => abaixo do limite.
	stC := covZStation(t, ctx, pool, "Radio Teste C FM", "FM", 88.5, "Cidade Teste", "SP")
	covZEvent(t, ctx, pool, stC, "down", ws.Add(5*time.Hour), 3600)
	// E: evento que nao e 'down' nao conta.
	stE := covZStation(t, ctx, pool, "Radio Teste E FM", "FM", 90.1, "Cidade Teste", "SP")
	covZEvent(t, ctx, pool, stE, "up", ws.Add(time.Hour), 5*3600)
	// G: comeca 3h antes da janela e dura 5h => recortado em [ws, ws+2h] = exatamente 2h (limite inclusivo).
	stG := covZStation(t, ctx, pool, "Radio Teste G FM", "FM", 95.5, "Cidade Teste", "SP")
	covZEvent(t, ctx, pool, stG, "down", ws.Add(-3*time.Hour), 5*3600)

	// Campanha ativa que monitora A; inativa nao entra na lista de campanhas.
	covZCampaign(t, ctx, pool, "ativa", d.AddDate(0, 0, -5), d.AddDate(0, 0, 30), []uuid.UUID{stA}, "Campanha Teste Z")
	covZCampaign(t, ctx, pool, "programada", d.AddDate(0, 0, 1), d.AddDate(0, 0, 30), []uuid.UUID{stA}, "Campanha Programada")

	got, err := repo.StationsOffline(ctx, ws, we)
	require.NoError(t, err)

	byStation := map[uuid.UUID][]StationOutage{}
	for _, o := range got {
		byStation[o.StationID] = append(byStation[o.StationID], o)
	}

	require.Len(t, byStation[stA], 1)
	a := byStation[stA][0]
	assert.Equal(t, "Radio Teste A FM", a.StationName)
	assert.Equal(t, "FM 103,9", a.Dial)
	assert.Equal(t, "Curitiba/PR", a.Location)
	assert.Equal(t, "Campanha Teste Z", a.Campaigns, "so campanhas ativas aparecem")
	assert.Equal(t, d, a.Day)
	assert.Equal(t, 4*time.Hour, a.Down, "downs sobrepostos nao somam em dobro")

	require.Len(t, byStation[stB], 1, "so o dia com >=2h entra")
	b := byStation[stB][0]
	assert.Equal(t, d.AddDate(0, 0, 1), b.Day)
	assert.Equal(t, 3*time.Hour, b.Down)
	assert.Equal(t, "AM 860,0", b.Dial)
	assert.Empty(t, b.Campaigns)

	assert.Empty(t, byStation[stC], "1h fica abaixo do limite")
	assert.Empty(t, byStation[stE], "evento up nao e downtime")
	require.Len(t, byStation[stG], 1)
	assert.Equal(t, 2*time.Hour, byStation[stG][0].Down, "recorte na janela, limite de 2h inclusivo")

	// Ordenacao: mais downtime primeiro (A 4h > B 3h > G 2h), considerando so as nossas.
	var order []uuid.UUID
	for _, o := range got {
		if o.StationID == stA || o.StationID == stB || o.StationID == stG {
			order = append(order, o.StationID)
		}
	}
	assert.Equal(t, []uuid.UUID{stA, stB, stG}, order)
}

func TestCovZ_StationsOffline_EmptyWindow(t *testing.T) {
	ctx, pool := newTestDB(t)
	d := covZFutureWeekday(time.Monday, 400)
	got, err := New(pool).StationsOffline(ctx, calendar.BRMidnight(d), calendar.BRMidnight(d.AddDate(0, 0, 1)))
	require.NoError(t, err)
	assert.Empty(t, got)
}

// --- servico ponta a ponta com o banco --------------------------------------

func TestCovZ_Service_StationsOfflineEndToEnd(t *testing.T) {
	ctx, pool := newTestDB(t)

	// Evento numa terca; "hoje" = quarta => janela [terca 00h, quarta 00h) => "de DD/MM".
	tue := covZFutureWeekday(time.Tuesday, 20)
	today := tue.AddDate(0, 0, 1)
	covZCleanLog(t, pool, today)
	st := covZStation(t, ctx, pool, "Radio Teste Fim FM", "FM", 101.1, "Cidade Teste", "SP")
	covZEvent(t, ctx, pool, st, "down", calendar.BRMidnight(tue).Add(2*time.Hour), 5*3600)

	m := &covZMailer{}
	svc := NewService(New(pool), NewLogStore(pool), covZLister{list: []users.User{{Email: "adm@example.com", Name: "Adm Teste"}}}, m, "https://painel.example.com", nil)
	svc.sendRetries = 1
	svc.ProcessType(ctx, today, svc.stationsOfflineType())

	sent := m.Sent()
	require.Len(t, sent, 1)
	assert.Contains(t, sent[0].Subject, "fora do ar por mais de 2h")
	assert.Contains(t, sent[0].HTML, "Radio Teste Fim FM")
	assert.Contains(t, sent[0].HTML, "5h00")
	assert.Contains(t, sent[0].HTML, "de "+tue.Format("02/01"))
	assert.NotContains(t, sent[0].HTML, tue.Format("02/01")+" a ", "janela de um dia so nao tem intervalo")
	assert.Contains(t, sent[0].Text, "Adm Teste")

	got := covZLogRows(t, ctx, pool, today)["stations_offline"]
	assert.Equal(t, "sent", got.Status)
	assert.Equal(t, 1, got.RecipientCount)
	assert.GreaterOrEqual(t, got.CampaignCount, 1)
}

func TestCovZ_Service_StationsOfflinePeriodSpansWeekend(t *testing.T) {
	ctx, pool := newTestDB(t)

	// Segunda cobre sex+sab+dom: periodo "de DD/MM a DD/MM".
	mon := covZFutureWeekday(time.Monday, 22)
	fri := mon.AddDate(0, 0, -3)
	sun := mon.AddDate(0, 0, -1)
	covZCleanLog(t, pool, mon)
	st := covZStation(t, ctx, pool, "Radio Teste Fds FM", "FM", 102.2, "Cidade Teste", "SP")
	sat := mon.AddDate(0, 0, -2)
	covZEvent(t, ctx, pool, st, "down", calendar.BRMidnight(sat).Add(time.Hour), 4*3600)

	m := &covZMailer{}
	svc := NewService(New(pool), NewLogStore(pool), covZLister{list: []users.User{{Email: "adm@example.com"}}}, m, "https://painel.example.com", nil)
	svc.sendRetries = 1
	svc.ProcessType(ctx, mon, svc.stationsOfflineType())

	sent := m.Sent()
	require.Len(t, sent, 1)
	assert.Contains(t, sent[0].HTML, "de "+fri.Format("02/01")+" a "+sun.Format("02/01"))
}

func TestCovZ_Service_CampaignTypesEndToEnd(t *testing.T) {
	ctx, pool := newTestDB(t)

	// Segunda no futuro: campanha programada comecando na terca sem material e
	// campanha ativa terminando na quarta.
	mon := covZFutureWeekday(time.Monday, 700)
	covZCleanLog(t, pool, mon)
	covZCampaign(t, ctx, pool, "programada", mon.AddDate(0, 0, 1), mon.AddDate(0, 0, 20), nil, "Inicia Sem Material")
	covZCampaign(t, ctx, pool, "ativa", mon.AddDate(0, 0, -10), mon.AddDate(0, 0, 2), nil, "Termina Logo")

	m := &covZMailer{}
	svc := NewService(New(pool), NewLogStore(pool), covZLister{list: []users.User{{Email: "adm@example.com", Name: "Adm Teste"}}}, m, "https://painel.example.com", nil)
	svc.sendRetries = 1
	for _, a := range svc.types() {
		svc.ProcessType(ctx, mon, a)
	}

	logs := covZLogRows(t, ctx, pool, mon)
	assert.Equal(t, "sent", logs["starting_no_material"].Status)
	assert.Equal(t, "sent", logs["starting"].Status)
	assert.Equal(t, "sent", logs["ending"].Status)
	assert.Equal(t, "skipped_empty", logs["stations_offline"].Status)
	assert.Equal(t, 1, logs["starting_no_material"].CampaignCount)

	bySubject := map[string]covZSentMail{}
	for _, s := range m.Sent() {
		bySubject[s.Subject] = s
	}
	require.Len(t, m.Sent(), 3)
	nm := bySubject["⚠️ 1 campanha sem material"]
	assert.Contains(t, nm.HTML, "Inicia Sem Material")
	end := bySubject["1 campanha terminando"]
	assert.Contains(t, end.HTML, "Termina Logo")
}

// --- Scheduler --------------------------------------------------------------

func TestCovZ_Scheduler_Tick(t *testing.T) {
	ctx, pool := newTestDB(t)

	mon := covZFutureWeekday(time.Monday, 720)
	covZCleanLog(t, pool, mon)
	covZCampaign(t, ctx, pool, "programada", mon.AddDate(0, 0, 1), mon.AddDate(0, 0, 20), nil, "Tick Sem Material")
	covZCampaign(t, ctx, pool, "ativa", mon.AddDate(0, 0, -10), mon.AddDate(0, 0, 2), nil, "Tick Termina")

	m := &covZMailer{}
	svc := NewService(New(pool), NewLogStore(pool), covZLister{list: []users.User{{Email: "adm@example.com", Name: "Adm Teste"}}}, m, "https://painel.example.com", nil)
	svc.sendRetries = 1
	sch := NewScheduler(pool, svc, 8, nil)

	now := time.Date(mon.Year(), mon.Month(), mon.Day(), 9, 0, 0, 0, calendar.BR)
	sch.NowFn = func() time.Time { return now }

	t.Run("antes do horario de envio nao faz nada", func(t *testing.T) {
		sch.NowFn = func() time.Time { return time.Date(mon.Year(), mon.Month(), mon.Day(), 7, 59, 0, 0, calendar.BR) }
		sch.tick(ctx)
		assert.Empty(t, covZLogRows(t, ctx, pool, mon))
		assert.Empty(t, m.Sent())
	})

	t.Run("outra instancia segurando o advisory lock pula o tick", func(t *testing.T) {
		sch.NowFn = func() time.Time { return now }
		conn, err := pool.Acquire(ctx)
		require.NoError(t, err)
		defer conn.Release()
		var got bool
		require.NoError(t, conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", advisoryLockKey).Scan(&got))
		require.True(t, got)
		defer func() { _, _ = conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", advisoryLockKey) }()

		sch.tick(ctx)
		assert.Empty(t, covZLogRows(t, ctx, pool, mon), "sem o lock nada e processado nem registrado")
		assert.Empty(t, m.Sent())
	})

	t.Run("dentro da janela processa os 4 tipos uma unica vez", func(t *testing.T) {
		sch.NowFn = func() time.Time { return now }
		sch.tick(ctx)

		logs := covZLogRows(t, ctx, pool, mon)
		require.Len(t, logs, 4)
		assert.Equal(t, "sent", logs["starting_no_material"].Status)
		assert.Equal(t, "sent", logs["starting"].Status)
		assert.Equal(t, "sent", logs["ending"].Status)
		assert.Equal(t, "skipped_empty", logs["stations_offline"].Status)
		assert.Len(t, m.Sent(), 3)

		// Segundo tick no mesmo dia: dedup pelo notification_log, nada novo sai.
		sch.tick(ctx)
		assert.Len(t, m.Sent(), 3)
	})

	t.Run("fim de semana nunca dispara", func(t *testing.T) {
		sat := mon.AddDate(0, 0, 5)
		covZCleanLog(t, pool, sat)
		sch.NowFn = func() time.Time { return time.Date(sat.Year(), sat.Month(), sat.Day(), 12, 0, 0, 0, calendar.BR) }
		sch.tick(ctx)
		assert.Empty(t, covZLogRows(t, ctx, pool, sat))
	})
}

func TestCovZ_Scheduler_TickRecoversFromPanic(t *testing.T) {
	ctx, pool := newTestDB(t)

	mon := covZFutureWeekday(time.Monday, 740)
	covZCleanLog(t, pool, mon)
	covZCampaign(t, ctx, pool, "programada", mon.AddDate(0, 0, 1), mon.AddDate(0, 0, 20), nil, "Panic Sem Material")

	core, logs := observer.New(zap.ErrorLevel)
	// users == nil: ProcessType derreferencia nil ao listar destinatarios e entra em panic.
	svc := NewService(New(pool), NewLogStore(pool), nil, &covZMailer{}, "https://painel.example.com", nil)
	sch := NewScheduler(pool, svc, 8, zap.New(core))
	sch.NowFn = func() time.Time { return time.Date(mon.Year(), mon.Month(), mon.Day(), 10, 0, 0, 0, calendar.BR) }

	require.NotPanics(t, func() { sch.tick(ctx) })
	assert.GreaterOrEqual(t, logs.FilterMessage("campaign-alerts: panic no disparo").Len(), 1)

	// Os tipos sem itens ainda foram processados depois do panic (ending e stations_offline).
	rows := covZLogRows(t, ctx, pool, mon)
	assert.Equal(t, "skipped_empty", rows["ending"].Status)
	assert.Equal(t, "skipped_empty", rows["stations_offline"].Status)
	assert.NotContains(t, rows, "starting_no_material", "o tipo que deu panic nao grava resultado")
}

func TestCovZ_Scheduler_RunTicksAndStops(t *testing.T) {
	_, pool := newTestDB(t)
	svc := NewService(New(pool), NewLogStore(pool), covZLister{}, &covZMailer{}, "", nil)
	sch := NewScheduler(pool, svc, 8, nil)
	sch.Interval = 10 * time.Millisecond

	var calls atomic.Int32
	// Sabado: o tick consulta o relogio e sai sem tocar no banco.
	sch.NowFn = func() time.Time {
		calls.Add(1)
		return time.Date(2031, 3, 1, 12, 0, 0, 0, calendar.BR)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		sch.Run(ctx)
		close(done)
	}()
	require.Eventually(t, func() bool { return calls.Load() >= 3 }, 10*time.Second, 10*time.Millisecond,
		"tick inicial + pelo menos dois ticks do ticker")
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Scheduler.Run nao terminou apos cancelar o contexto")
	}
}
