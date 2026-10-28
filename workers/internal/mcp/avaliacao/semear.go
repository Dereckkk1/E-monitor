// Package avaliacao semeia o cenário determinístico da avaliação do MCP
// (docs/superpowers/plans/2026-10-28-mcp-fatia-2-relatorios.md, Task 15).
// As perguntas (perguntas.xml) citam NOMES, não ids: os ids nascem aleatórios
// a cada semente. As datas da campanha ativa são relativas a hoje
// (America/Sao_Paulo), porque as tools de falha e a assertividade têm janela
// móvel; o resto usa datas fixas de 2026.
package avaliacao

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/auditoria"
	"radiocheck/internal/postsale"
)

// EmailAdmin é o admin da avaliação (o CLI o usa como principal).
const EmailAdmin = "avaliacao@e-monitor.local"

var sp = func() *time.Location {
	l, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		return time.FixedZone("BRT", -3*3600)
	}
	return l
}()

type semeador struct {
	ctx  context.Context
	pool *pgxpool.Pool
	err  error
}

func (s *semeador) exec(sql string, args ...any) {
	if s.err == nil {
		_, s.err = s.pool.Exec(s.ctx, sql, args...)
	}
}

func dia(iso string) time.Time {
	d, err := time.Parse("2006-01-02", iso)
	if err != nil {
		panic(err)
	}
	return d
}

// quando: o instante HH:MM do dia d em America/Sao_Paulo.
func quando(d time.Time, hhmm string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", d.Format("2006-01-02")+" "+hhmm, sp)
	if err != nil {
		panic(err)
	}
	return t
}

// Semear grava o cenário num banco VAZIO (template migrado, sem clientes nem
// emissoras). Devolve o id do admin da avaliação.
func Semear(ctx context.Context, pool *pgxpool.Pool, agora time.Time) (uuid.UUID, error) {
	s := &semeador{ctx: ctx, pool: pool}
	hojeSP := agora.In(sp)
	hoje := time.Date(hojeSP.Year(), hojeSP.Month(), hojeSP.Day(), 0, 0, 0, 0, time.UTC)

	var admin uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash, role, name, is_active)
		VALUES ($1, 'x', 'admin', 'Avaliação MCP', true) RETURNING id`, EmailAdmin).Scan(&admin); err != nil {
		return uuid.Nil, fmt.Errorf("avaliacao: admin (o banco já foi semeado?): %w", err)
	}

	clientes := catalog.NewClients(pool)
	aurora, err := clientes.Create(ctx, catalog.CreateClientInput{Name: "Aurora Alimentos"})
	if err != nil {
		return uuid.Nil, err
	}
	brisa, err := clientes.Create(ctx, catalog.CreateClientInput{Name: "Brisa Seguros"})
	if err != nil {
		return uuid.Nil, err
	}
	s.exec(`UPDATE clients SET target_label = 'Mulheres 25-49' WHERE id = $1`, aurora.ID)

	emissora := func(nome, banda, cidade, uf string, pmm float64) uuid.UUID {
		if s.err != nil {
			return uuid.Nil
		}
		st, err := catalog.NewStations(pool).Create(ctx, catalog.CreateStationInput{Name: nome, Band: banda, StreamURL: "http://avaliacao/" + uuid.NewString()})
		if err != nil {
			s.err = err
			return uuid.Nil
		}
		s.exec(`UPDATE stations SET pmm = $2, city = $3, state = $4, latitude = -20, longitude = -47 WHERE id = $1`, st.ID, pmm, cidade, uf)
		return st.ID
	}
	atlantica := emissora("Rádio Atlântica FM", "FM", "São Paulo", "SP", 2000)
	serra := emissora("Rádio Serra FM", "FM", "Campinas", "SP", 1000)
	vale := emissora("Rádio Vale AM", "AM", "Uberaba", "MG", 500)
	s.exec(`UPDATE stations SET anatel_class = 'A1', anatel_coverage_km = 40, anatel_reach_km = 60 WHERE id = $1`, atlantica)
	s.exec(`UPDATE stations SET anatel_class = 'B1', anatel_coverage_km = 30, anatel_reach_km = 45 WHERE id = $1`, serra)
	cidade := func(st uuid.UUID, ibge int, nome, uf string, km float64, sede bool) {
		s.exec(`INSERT INTO station_coverage_cities (station_id, ibge_code, city, state, distance_km, is_home, latitude, longitude)
			VALUES ($1, $2, $3, $4, $5, $6, -20, -47)`, st, ibge, nome, uf, km, sede)
	}
	cidade(atlantica, 3550308, "São Paulo", "SP", 0, true)
	cidade(atlantica, 3518800, "Guarulhos", "SP", 18, false)
	cidade(atlantica, 3534401, "Osasco", "SP", 15, false)
	cidade(atlantica, 3525904, "Jundiaí", "SP", 55, false)
	cidade(serra, 3509502, "Campinas", "SP", 0, true)
	cidade(serra, 3556206, "Valinhos", "SP", 12, false)
	cidade(serra, 3519071, "Hortolândia", "SP", 18, false)
	cidade(serra, 3525904, "Jundiaí", "SP", 38, false)
	cidade(vale, 3170107, "Uberaba", "MG", 0, true)

	// material_types não é truncada pelos testes: reaproveita o tipo se já existir.
	var tipo uuid.UUID
	if s.err == nil {
		s.err = pool.QueryRow(ctx, `INSERT INTO material_types (name, color) VALUES ('Spot 30s', '#3b82f6')
			ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name RETURNING id`).Scan(&tipo)
	}
	material := func(cli uuid.UUID, titulo string) uuid.UUID {
		if s.err != nil {
			return uuid.Nil
		}
		m, err := catalog.NewMaterials(pool).Create(ctx, catalog.CreateMaterialInput{ClientID: cli, Title: titulo, TypeID: &tipo,
			DurationSeconds: 30, MasterStoragePath: "/tmp/avaliacao-" + titulo, MasterSHA256: "avaliacao-" + uuid.NewString()})
		if err != nil {
			s.err = err
			return uuid.Nil
		}
		return m.ID
	}
	verao := material(aurora.ID, "Aurora Verão 30s")
	promo := material(aurora.ID, "Aurora Promo 30s")
	auto := material(brisa.ID, "Brisa Auto 30s")

	campanha := func(cli uuid.UUID, nome string, ini, fim time.Time, estado string, ests ...uuid.UUID) uuid.UUID {
		if s.err != nil {
			return uuid.Nil
		}
		c, err := catalog.NewCampaigns(pool).Create(ctx, catalog.CreateCampaignInput{ClientID: cli, Name: nome, StartDate: ini, EndDate: fim, TargetStations: ests})
		if err != nil {
			s.err = err
			return uuid.Nil
		}
		s.exec(`UPDATE campaigns SET status = $2 WHERE id = $1`, c.ID, estado)
		return c.ID
	}
	vincular := func(camp, mat uuid.UUID, ests ...uuid.UUID) {
		if s.err == nil {
			s.err = catalog.NewCampaignMaterials(pool).Link(ctx, camp, mat, ests)
		}
	}
	regra := func(camp, est uuid.UUID, ini, fim time.Time) {
		if s.err != nil {
			return
		}
		_, s.err = catalog.NewDistributionRules(pool).Create(ctx, catalog.CreateDistributionRuleInput{CampaignID: camp, TypeID: tipo,
			StationIDs: []uuid.UUID{est}, MaterialIDs: []uuid.UUID{}, Name: "Dia todo até 22h", StartDate: ini, EndDate: fim,
			WeekdayMask: 127, TimeStart: "00:00", TimeEnd: "22:00", PlaysPerDay: 2})
	}
	preco := func(camp, est uuid.UUID, v float64) {
		if s.err != nil {
			return
		}
		_, s.err = catalog.NewPricing(pool).Upsert(ctx, catalog.UpsertInput{CampaignID: camp, StationID: est,
			Mode: catalog.PricingModePerInsertion, PerType: []catalog.TypePricing{{TypeID: tipo, UnitValue: v}}})
	}
	// tocada: veiculação com categoria explícita + a projeção canônica
	// (detection_campaigns), o mesmo atalho do mcptest.Veiculacao.
	tocada := func(camp, mat, est uuid.UUID, cat string, em time.Time) {
		if s.err != nil {
			return
		}
		var id uuid.UUID
		s.err = pool.QueryRow(ctx, `INSERT INTO detections (station_id, commercial_id, campaign_id, detected_at,
			match_start_offset_ms, match_end_offset_ms, confidence, hash_count, temporal_coverage, variant_used, rate_used, category)
			VALUES ($1, $2, $3, $4, 0, 30000, 0.95, 100, 0.85, 0, 0, $5) RETURNING id`, est, mat, camp, em, cat).Scan(&id)
		s.exec(`INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
			VALUES ($1, $2, $3, $4, $5)`, id, em, camp, mat, cat)
	}

	// Aurora Setembro (concluída): sem déficit. Atlântica 2/dia de 01 a 05/09
	// com o Verão + 2 bônus em 01/09 + 1 fora da faixa em 03/09; Serra 2/dia
	// em 01 e 02/09 com o Promo. Preço por inserção: Atlântica 200, Serra 100.
	// PMM no target da Aurora só na Atlântica (800).
	set := campanha(aurora.ID, "Aurora Setembro", dia("2026-09-01"), dia("2026-09-05"), "concluida", atlantica, serra)
	vincular(set, verao, atlantica)
	vincular(set, promo, serra)
	regra(set, atlantica, dia("2026-09-01"), dia("2026-09-05"))
	regra(set, serra, dia("2026-09-01"), dia("2026-09-02"))
	preco(set, atlantica, 200)
	preco(set, serra, 100)
	s.exec(`INSERT INTO client_station_pmm (client_id, station_id, pmm_target) VALUES ($1, $2, 800)`, aurora.ID, atlantica)
	for d := dia("2026-09-01"); !d.After(dia("2026-09-05")); d = d.AddDate(0, 0, 1) {
		tocada(set, verao, atlantica, "in_slot", quando(d, "08:00"))
		tocada(set, verao, atlantica, "in_slot", quando(d, "09:00"))
	}
	tocada(set, verao, atlantica, "bonus", quando(dia("2026-09-01"), "10:00"))
	tocada(set, verao, atlantica, "bonus", quando(dia("2026-09-01"), "11:00"))
	tocada(set, verao, atlantica, "out_slot", quando(dia("2026-09-03"), "23:00"))
	for _, d := range []time.Time{dia("2026-09-01"), dia("2026-09-02")} {
		tocada(set, promo, serra, "in_slot", quando(d, "08:00"))
		tocada(set, promo, serra, "in_slot", quando(d, "09:00"))
	}

	// Aurora Agora (ativa, hoje−20 a hoje+10): as três emissoras tocam 2/dia
	// às 00:01 e 00:02 (sempre no passado), menos no dia D = hoje−3: a Serra
	// tocou 1 vez às 23:00 (fora da faixa) e ficou 1h30 fora do ar; a Vale não
	// tocou.
	ini, fim := hoje.AddDate(0, 0, -20), hoje.AddDate(0, 0, 10)
	agoraC := campanha(aurora.ID, "Aurora Agora", ini, fim, "ativa", atlantica, serra, vale)
	vincular(agoraC, verao, atlantica, serra, vale)
	for _, e := range []uuid.UUID{atlantica, serra, vale} {
		regra(agoraC, e, ini, fim)
	}
	diaD := hoje.AddDate(0, 0, -3)
	for d := ini; !d.After(hoje); d = d.AddDate(0, 0, 1) {
		for _, e := range []uuid.UUID{atlantica, serra, vale} {
			if d.Equal(diaD) && e != atlantica {
				continue
			}
			tocada(agoraC, verao, e, "in_slot", quando(d, "00:01"))
			tocada(agoraC, verao, e, "in_slot", quando(d, "00:02"))
		}
	}
	tocada(agoraC, verao, serra, "out_slot", quando(diaD, "23:00"))
	s.exec(`INSERT INTO stream_health_events (station_id, event_type, event_at, duration_seconds) VALUES ($1, 'down', $2, 5400)`,
		serra, quando(diaD, "10:00"))

	// Brisa Outubro (ativa, sem regra): só tocadas, nenhuma meta — não entra em falhas.
	out := campanha(brisa.ID, "Brisa Outubro", dia("2026-10-01"), dia("2026-10-31"), "ativa", vale)
	vincular(out, auto, vale)
	tocada(out, auto, vale, "bonus", quando(dia("2026-10-05"), "12:00"))

	// Assertividade da Aurora: mês fechado 95,0% (380 de 400), anterior 90,0% (90 de 100).
	mesFechado := time.Date(hoje.Year(), hoje.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	s.exec(`INSERT INTO assertiveness_daily (for_date, campaign_id, station_id, commercial_id, auto, miss) VALUES ($1, $2, $3, $4, 380, 20)`,
		mesFechado.AddDate(0, 0, 9), agoraC, atlantica, verao)
	s.exec(`INSERT INTO assertiveness_daily (for_date, campaign_id, station_id, commercial_id, auto, miss) VALUES ($1, $2, $3, $4, 90, 10)`,
		mesFechado.AddDate(0, -1, 9), agoraC, atlantica, verao)

	// Pós-venda enviado da Aurora Setembro: 3 destinatários, 2 abriram.
	if s.err == nil {
		s.err = semearPosVenda(ctx, pool, aurora.ID, set)
	}

	// Auditoria: dois tokens do admin; o mais recente é "Notebook do escritório".
	if s.err == nil {
		s.err = auditoria.Registrar(ctx, pool, admin, "mcp.token.criado", "mcp_token", nil, map[string]any{"nome": "Celular", "validade_dias": 90})
	}
	s.exec(`UPDATE audit_log SET created_at = now() - interval '2 hours' WHERE metadata->>'nome' = 'Celular'`)
	if s.err == nil {
		s.err = auditoria.Registrar(ctx, pool, admin, "mcp.token.criado", "mcp_token", nil, map[string]any{"nome": "Notebook do escritório", "validade_dias": 90})
	}
	if s.err != nil {
		return uuid.Nil, fmt.Errorf("avaliacao: semear: %w", s.err)
	}
	return admin, nil
}

func semearPosVenda(ctx context.Context, pool *pgxpool.Pool, cliente, campanha uuid.UUID) error {
	repo := postsale.NewRepo(pool)
	rep, err := repo.CreateDraft(ctx, postsale.CreateDraftInput{ClientID: cliente, Title: "Fechamento Aurora Setembro"})
	if err != nil {
		return err
	}
	if err := repo.ReplaceBlocks(ctx, rep.ID, []postsale.BlockRow{{CampaignID: campanha, From: dia("2026-09-01"), To: dia("2026-09-05")}}); err != nil {
		return err
	}
	dest, err := repo.CreateRecipients(ctx, rep.ID, []postsale.RecipientInput{
		{Email: "ana@aurora.com", Name: "Ana", Token: uuid.NewString()},
		{Email: "bruno@aurora.com", Name: "Bruno", Token: uuid.NewString()},
		{Email: "carla@aurora.com", Name: "Carla", Token: uuid.NewString()},
	})
	if err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `UPDATE post_sale_report_recipients SET email_status = 'sent' WHERE report_id = $1`, rep.ID); err != nil {
		return err
	}
	for _, d := range dest[:2] {
		if _, err := pool.Exec(ctx, `UPDATE post_sale_report_recipients SET opened_at = now(), open_count = 1 WHERE id = $1`, d.ID); err != nil {
			return err
		}
	}
	// Os números congelados batem com a semente: entregue = 10×200 + 4×100,
	// bônus = 2×200, impactos = 2000×12 + 1000×4, target = 800×12;
	// CPM = (2400 + 400) ÷ impactos × 1000.
	rotulo := "Mulheres 25-49"
	b, err := json.Marshal(postsale.Payload{Version: postsale.PayloadVersion, Title: "Fechamento Aurora Setembro", Campaigns: []postsale.CampaignBlock{{
		CampaignID: campanha, Name: "Aurora Setembro", Status: "concluida", PeriodFrom: "2026-09-01", PeriodTo: "2026-09-05",
		KPIs: postsale.BlockKPIs{ValorEntregue: 2400, Bonificacao: 400, BonificacaoCount: 2, Impactos: 28000, CPM: 100, StationsCount: 2,
			ImpactosTarget: 9600, CPMTarget: 291.67, StationsWithTarget: 1, TargetLabel: &rotulo},
		CheckingRows: []postsale.StationRow{}, ConformingCount: 2}}})
	if err != nil {
		return err
	}
	return repo.MarkSent(ctx, rep.ID, b)
}
