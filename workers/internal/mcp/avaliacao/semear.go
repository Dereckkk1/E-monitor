// Package avaliacao semeia o cenário determinístico da avaliação do MCP
// (docs/superpowers/plans/2026-10-28-mcp-fatia-2-relatorios.md, Task 15).
// As perguntas (perguntas.xml) citam NOMES, não ids: os ids nascem aleatórios
// a cada semente. As datas da campanha ativa são relativas a hoje
// (America/Sao_Paulo), porque as tools de falha e a assertividade têm janela
// móvel; o resto usa datas fixas de 2026.
//
// A semente só roda num banco VAZIO (recriado do template de teste) e numa
// transação só: rodada por engano contra uma cópia de prod, ela recusa antes
// de escrever qualquer coisa — sem isso criaria um admin, uma campanha ATIVA
// com emissoras falsas (que o supervisor tentaria monitorar) e mexeria na
// assertividade global.
package avaliacao

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/auditoria"
	"radiocheck/internal/postsale"
)

// EmailAdmin é o admin da avaliação (o CLI o usa como principal). O
// created_at dele é o instante da semente (o CLI avisa se for de outro dia).
const EmailAdmin = "avaliacao@e-monitor.local"

// ErrBancoNaoVazio: a semente recusa banco com cliente, emissora, campanha ou
// usuário — o cenário só vale num banco recriado do template.
var ErrBancoNaoVazio = errors.New("avaliacao: o banco não está vazio (tem clientes, emissoras, campanhas ou usuários); " +
	"a semente só roda num banco recriado do template (DROP DATABASE + CREATE DATABASE ... TEMPLATE radiocheck_mcp_test_d2) " +
	"e, se o template trouxer usuários de teste, depois de TRUNCATE users CASCADE NESSE banco — nunca num banco de prod ou cópia dele")

// SP é o fuso das datas de calendário da semente (e do aviso do CLI).
var SP = func() *time.Location {
	l, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		return time.FixedZone("BRT", -3*3600)
	}
	return l
}()

type semeador struct {
	ctx context.Context
	tx  pgx.Tx
	err error
}

func (s *semeador) exec(sql string, args ...any) {
	if s.err == nil {
		_, s.err = s.tx.Exec(s.ctx, sql, args...)
	}
}

// id roda um INSERT ... RETURNING id.
func (s *semeador) id(sql string, args ...any) uuid.UUID {
	var id uuid.UUID
	if s.err == nil {
		s.err = s.tx.QueryRow(s.ctx, sql, args...).Scan(&id)
	}
	return id
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
	t, err := time.ParseInLocation("2006-01-02 15:04", d.Format("2006-01-02")+" "+hhmm, SP)
	if err != nil {
		panic(err)
	}
	return t
}

// bancoVazio: nenhuma linha em clients, stations, campaigns ou users.
// material_types não conta: os testes não a truncam e a semente reaproveita o tipo.
func bancoVazio(ctx context.Context, tx pgx.Tx) error {
	var ocupado bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM clients) OR EXISTS (SELECT 1 FROM stations)
		OR EXISTS (SELECT 1 FROM campaigns) OR EXISTS (SELECT 1 FROM users)`).Scan(&ocupado); err != nil {
		return fmt.Errorf("avaliacao: conferir banco vazio: %w", err)
	}
	if ocupado {
		return ErrBancoNaoVazio
	}
	return nil
}

// Semear grava o cenário num banco VAZIO, numa transação (falha no meio →
// nada fica). Devolve o id do admin da avaliação.
func Semear(ctx context.Context, pool *pgxpool.Pool, agora time.Time) (uuid.UUID, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := bancoVazio(ctx, tx); err != nil {
		return uuid.Nil, err
	}
	admin, err := semear(ctx, tx, agora)
	if err != nil {
		return uuid.Nil, fmt.Errorf("avaliacao: semear: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return admin, nil
}

func semear(ctx context.Context, tx pgx.Tx, agora time.Time) (uuid.UUID, error) {
	s := &semeador{ctx: ctx, tx: tx}
	hojeSP := agora.In(SP)
	hoje := time.Date(hojeSP.Year(), hojeSP.Month(), hojeSP.Day(), 0, 0, 0, 0, time.UTC)

	admin := s.id(`INSERT INTO users (email, password_hash, role, name, is_active, created_at)
		VALUES ($1, 'x', 'admin', 'Avaliação MCP', true, $2) RETURNING id`, EmailAdmin, agora)

	cliente := func(nome string) uuid.UUID {
		return s.id(`INSERT INTO clients (name) VALUES ($1) RETURNING id`, nome)
	}
	aurora := cliente("Aurora Alimentos")
	brisa := cliente("Brisa Seguros")
	s.exec(`UPDATE clients SET target_label = 'Mulheres 25-49' WHERE id = $1`, aurora)

	// Emissora como catalog.Stations.Create (linha + station_thresholds).
	emissora := func(nome, banda, cidade, uf string, pmm float64) uuid.UUID {
		id := s.id(`INSERT INTO stations (name, band, city, state, stream_url, latitude, longitude, pmm)
			VALUES ($1, $2, $3, $4, $5, -20, -47, $6) RETURNING id`, nome, banda, cidade, uf, "http://avaliacao/"+uuid.NewString(), pmm)
		s.exec(`INSERT INTO station_thresholds (station_id) VALUES ($1)`, id)
		return id
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
	tipo := s.id(`INSERT INTO material_types (name, color) VALUES ('Spot 30s', '#3b82f6')
		ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name RETURNING id`)
	material := func(cli uuid.UUID, titulo string) uuid.UUID {
		return s.id(`INSERT INTO materials (client_id, title, type_id, duration_seconds, master_storage_path, master_sha256)
			VALUES ($1, $2, $3, 30, $4, $5) RETURNING id`, cli, titulo, tipo, "/tmp/avaliacao-"+titulo, "avaliacao-"+uuid.NewString())
	}
	verao := material(aurora, "Aurora Verão 30s")
	promo := material(aurora, "Aurora Promo 30s")
	auto := material(brisa, "Brisa Auto 30s")

	campanha := func(cli uuid.UUID, nome string, ini, fim time.Time, estado string, ests ...uuid.UUID) uuid.UUID {
		return s.id(`INSERT INTO campaigns (client_id, name, start_date, end_date, target_stations, status)
			VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`, cli, nome, ini, fim, ests, estado)
	}
	vincular := func(camp, mat uuid.UUID, ests ...uuid.UUID) {
		s.exec(`INSERT INTO campaign_materials (campaign_id, material_id, target_stations) VALUES ($1, $2, $3)`, camp, mat, ests)
	}
	regra := func(camp, est uuid.UUID, ini, fim time.Time) {
		s.exec(`INSERT INTO distribution_rules (campaign_id, type_id, station_ids, material_ids, name, start_date, end_date,
			weekday_mask, time_start, time_end, plays_per_day)
			VALUES ($1, $2, $3, '{}', 'Dia todo até 22h', $4, $5, 127, '00:00'::time, '22:00'::time, 2)`,
			camp, tipo, []uuid.UUID{est}, ini, fim)
	}
	// Preço por inserção como catalog.Pricing.Upsert (cabeça + valor por tipo).
	preco := func(camp, est uuid.UUID, v float64) {
		s.exec(`INSERT INTO campaign_station_pricing (campaign_id, station_id, mode) VALUES ($1, $2, $3)`,
			camp, est, catalog.PricingModePerInsertion)
		s.exec(`INSERT INTO campaign_station_type_pricing (campaign_id, station_id, type_id, unit_value) VALUES ($1, $2, $3, $4)`,
			camp, est, tipo, v)
	}
	// tocada: veiculação com categoria explícita + a projeção canônica
	// (detection_campaigns), o mesmo atalho do mcptest.Veiculacao.
	tocada := func(camp, mat, est uuid.UUID, cat string, em time.Time) {
		id := s.id(`INSERT INTO detections (station_id, commercial_id, campaign_id, detected_at,
			match_start_offset_ms, match_end_offset_ms, confidence, hash_count, temporal_coverage, variant_used, rate_used, category)
			VALUES ($1, $2, $3, $4, 0, 30000, 0.95, 100, 0.85, 0, 0, $5) RETURNING id`, est, mat, camp, em, cat)
		s.exec(`INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
			VALUES ($1, $2, $3, $4, $5)`, id, em, camp, mat, cat)
	}

	// Aurora Setembro (concluída): sem déficit. Atlântica 2/dia de 01 a 05/09
	// com o Verão + 2 bônus em 01/09 + 1 fora da faixa em 03/09; Serra 2/dia
	// em 01 e 02/09 com o Promo. Preço por inserção: Atlântica 200, Serra 100.
	// PMM no target da Aurora só na Atlântica (800).
	set := campanha(aurora, "Aurora Setembro", dia("2026-09-01"), dia("2026-09-05"), "concluida", atlantica, serra)
	vincular(set, verao, atlantica)
	vincular(set, promo, serra)
	regra(set, atlantica, dia("2026-09-01"), dia("2026-09-05"))
	regra(set, serra, dia("2026-09-01"), dia("2026-09-02"))
	preco(set, atlantica, 200)
	preco(set, serra, 100)
	s.exec(`INSERT INTO client_station_pmm (client_id, station_id, pmm_target) VALUES ($1, $2, 800)`, aurora, atlantica)
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
	agoraC := campanha(aurora, "Aurora Agora", ini, fim, "ativa", atlantica, serra, vale)
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
	out := campanha(brisa, "Brisa Outubro", dia("2026-10-01"), dia("2026-10-31"), "ativa", vale)
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
		s.err = semearPosVenda(ctx, tx, aurora, set)
	}

	// Auditoria: dois tokens do admin; o mais recente é "Notebook do escritório".
	// O UPDATE só alcança o evento desta semente (ator + ação + nome).
	if s.err == nil {
		s.err = auditoria.Registrar(ctx, tx, admin, "mcp.token.criado", "mcp_token", nil, map[string]any{"nome": "Celular", "validade_dias": 90})
	}
	s.exec(`UPDATE audit_log SET created_at = $2::timestamptz - interval '2 hours'
		WHERE actor_id = $1 AND action = 'mcp.token.criado' AND metadata->>'nome' = 'Celular'`, admin, agora)
	if s.err == nil {
		s.err = auditoria.Registrar(ctx, tx, admin, "mcp.token.criado", "mcp_token", nil, map[string]any{"nome": "Notebook do escritório", "validade_dias": 90})
	}
	return admin, s.err
}

// semearPosVenda grava o pós-venda como postsale.Repo (CreateDraft,
// ReplaceBlocks, CreateRecipients, MarkSent), dentro da transação.
func semearPosVenda(ctx context.Context, tx pgx.Tx, cliente, campanha uuid.UUID) error {
	var rep uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO post_sale_reports (client_id, title) VALUES ($1, 'Fechamento Aurora Setembro') RETURNING id`,
		cliente).Scan(&rep); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO post_sale_report_campaigns (report_id, campaign_id, period_from, period_to)
		VALUES ($1, $2, '2026-09-01', '2026-09-05')`, rep, campanha); err != nil {
		return err
	}
	for i, p := range []struct{ email, nome string }{{"ana@aurora.com", "Ana"}, {"bruno@aurora.com", "Bruno"}, {"carla@aurora.com", "Carla"}} {
		// Ana e Bruno abriram; Carla não.
		if _, err := tx.Exec(ctx, `INSERT INTO post_sale_report_recipients (report_id, email, name, token, email_status, opened_at, open_count)
			VALUES ($1, $2, $3, $4, 'sent', CASE WHEN $5 THEN now() END, CASE WHEN $5 THEN 1 ELSE 0 END)`,
			rep, p.email, p.nome, uuid.NewString(), i < 2); err != nil {
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
	_, err = tx.Exec(ctx, `UPDATE post_sale_reports SET status = 'sent', payload_json = $2, sent_at = now() WHERE id = $1`, rep, b)
	return err
}
