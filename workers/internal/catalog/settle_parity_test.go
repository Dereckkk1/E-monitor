package catalog

// settle_parity_test.go — prova que os DOIS motores de categorização por cota
// concordam, veredito a veredito, sobre a mesma célula-dia:
//
//   - Go  — categorizer.Settle, alimentado pelos LOADERS DE PRODUÇÃO
//     (loadRulesForCell / loadOverrideForCell / loadCellDayPlays), exatamente
//     como o insert-path o alimenta em settleCellDay;
//   - SQL — a CTE `classified` (recatClassifiedCTE), rodada aqui como
//     recatScopeByCampaignSQL + recatSelectTailSQL, ou seja, o mesmo pipeline
//     que todo caminho de recategorização, o reconciler projrecon (a cada 15
//     min sobre 48h em prod) e o backfill usam.
//
// POR QUE ESTE TESTE EXISTE. A regra de cota está implementada duas vezes. Se as
// duas divergirem, o projrecon reescreve em silêncio o que o insert-path acabou
// de gravar (e vice-versa), e o número que vai pra cobrança muda sem ninguém
// mandar. Já aconteceu: a tolerância de 15 min existia no Go e não no SQL — ver
// o comentário no topo de recatClassifiedCTE. Os godocs dos dois motores
// prometem que "o teste de paridade prova isso"; é este arquivo.
//
// REGRAS DE OURO AO MEXER AQUI:
//  1. O lado Go tem que usar os loaders de produção. Se os dois lados
//     receberem input montado por código de teste, o teste não prova nada sobre
//     produção.
//  2. O lado SQL é SELECT PURO — nada aqui pode gravar category. Por isso as
//     detections nascem por INSERT cru (categoria 'orphan' de placeholder) e
//     nenhum teste chama Recategorize*/Upsert de override.
//  3. Divergência aqui é BUG DE UM DOS MOTORES. Conserte o motor, nunca o
//     teste.
//
// Requer TEST_DATABASE_URL (newTestDB dá skip silencioso sem ela).

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/categorizer"
)

// settleParitySeed é FIXO de propósito: o sweep aleatório precisa ser
// reproduzível em CI. Toda falha imprime o seed (e o âncora) usados, e
// SETTLE_PARITY_SEED permite re-rodar um seed específico à mão.
const settleParitySeed = 20260814

// Custo medido no rc-test-pg local: ~67 ms por cenário (600 cenários = 40 s).
// 150 cenários ≈ 10 s é o que cabe na suíte normal do pacote sem dobrar o tempo
// dela; o sweep completo (que já rodou 600 cenários / 3.369 vereditos com 0
// divergência) fica disponível via SETTLE_PARITY_SCENARIOS=600. Em -short o
// sweep encolhe pra 25 cenários — os testes determinísticos continuam rodando,
// porque são eles que cobrem a tabela-verdade.
const (
	settleParityScenarios      = 150
	settleParityScenariosShort = 25
)

func settleParityScenarioCount(t *testing.T) int {
	t.Helper()
	if v := os.Getenv("SETTLE_PARITY_SCENARIOS"); v != "" {
		n, err := strconv.Atoi(v)
		require.NoErrorf(t, err, "SETTLE_PARITY_SCENARIOS=%q não é um inteiro", v)
		return n
	}
	if testing.Short() {
		return settleParityScenariosShort
	}
	return settleParityScenarios
}

func settleParitySeedValue(t *testing.T) int64 {
	t.Helper()
	if v := os.Getenv("SETTLE_PARITY_SEED"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		require.NoErrorf(t, err, "SETTLE_PARITY_SEED=%q não é um inteiro", v)
		return n
	}
	return settleParitySeed
}

// ---------------------------------------------------------------- fixture --

// parityFixture é o cenário de fundo compartilhado: um cliente, duas emissoras,
// dois tipos de material e três materiais (A e B no tipo 1, C no tipo 2). Duas
// emissoras e dois tipos existem pra provar que a cota PARTICIONA por célula.
type parityFixture struct {
	ctx    context.Context
	pool   *pgxpool.Pool
	sp     *time.Location
	anchor time.Time // segunda-feira de referência, meia-noite SP

	client   uuid.UUID
	stA, stB uuid.UUID
	tp1, tp2 uuid.UUID
	matA     uuid.UUID // tp1
	matB     uuid.UUID // tp1
	matC     uuid.UUID // tp2
}

func newParityFixture(t *testing.T) *parityFixture {
	t.Helper()
	ctx, pool := newTestDB(t)
	sp, err := time.LoadLocation("America/Sao_Paulo")
	require.NoError(t, err)

	cli, err := NewClients(pool).Create(ctx, CreateClientInput{Name: "parity-cli-" + uuid.NewString()[:8]})
	require.NoError(t, err)
	mkStation := func(tag string) uuid.UUID {
		s, err := NewStations(pool).Create(ctx, CreateStationInput{
			Name: "Parity " + tag + " " + uuid.NewString()[:6], Band: "FM",
			StreamURL: "http://example.com/parity/" + uuid.NewString()[:8]})
		require.NoError(t, err)
		return s.ID
	}
	stA, stB := mkStation("A"), mkStation("B")
	tp1 := seedType(t, ctx, pool, "parity-t1")
	tp2 := seedType(t, ctx, pool, "parity-t2")

	mats := NewMaterials(pool)
	mkMat := func(title string, typeID uuid.UUID, dur float64) uuid.UUID {
		m, err := mats.Create(ctx, CreateMaterialInput{ClientID: cli.ID, Title: title,
			TypeID: &typeID, DurationSeconds: dur, MasterStoragePath: "/tmp/" + title,
			MasterSHA256: title + "-" + uuid.NewString()})
		require.NoError(t, err)
		return m.ID
	}

	f := &parityFixture{
		ctx: ctx, pool: pool, sp: sp, anchor: parityAnchor(sp),
		client: cli.ID, stA: stA, stB: stB, tp1: tp1, tp2: tp2,
		matA: mkMat("parityA", tp1, 30), matB: mkMat("parityB", tp1, 30),
		matC: mkMat("parityC", tp2, 15),
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM materials WHERE client_id = $1`, cli.ID)
		pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, cli.ID)
		pool.Exec(ctx, `DELETE FROM stations WHERE id IN ($1,$2)`, stA, stB)
	})
	return f
}

// parityAnchor é a segunda-feira mais recente com pelo menos 7 dias de idade, à
// meia-noite SP.
//
// Por que âncora móvel e não uma data fixa: as detections são particionadas por
// detected_at e a manutenção de partições acompanha o "agora" — uma data fixa
// no código sai da janela particionada com o tempo e o teste passa a falhar por
// ambiente. Por que SEGUNDA-FEIRA: prender o dia-da-semana mantém o cenário
// ISOMÓRFICO entre execuções (todo offset aqui é relativo à âncora), então o
// mesmo seed produz a mesma estrutura de regras/máscaras/vereditos amanhã. O SP
// não tem horário de verão desde 2019, então não há dia de 23/25 horas no meio.
func parityAnchor(sp *time.Location) time.Time {
	d := time.Now().In(sp).AddDate(0, 0, -7)
	d = time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, sp)
	for d.Weekday() != time.Monday {
		d = d.AddDate(0, 0, -1)
	}
	return d
}

// parityDate normaliza pra meia-noite UTC do mesmo dia calendário — como o pgx
// devolve colunas DATE, e como as colunas date de campanhas/regras esperam.
func parityDate(d time.Time) time.Time {
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
}

// day devolve o dia anchor+off à meia-noite SP.
func (f *parityFixture) day(off int) time.Time { return f.anchor.AddDate(0, 0, off) }

// at devolve o instante hh:mm:ss do dia anchor+off, em SP.
func (f *parityFixture) at(off, h, m, s int) time.Time {
	d := f.day(off)
	return time.Date(d.Year(), d.Month(), d.Day(), h, m, s, 0, f.sp)
}

func (f *parityFixture) matName(id uuid.UUID) string {
	switch id {
	case f.matA:
		return "A(tp1)"
	case f.matB:
		return "B(tp1)"
	case f.matC:
		return "C(tp2)"
	}
	return id.String()[:8]
}

func (f *parityFixture) stationName(id uuid.UUID) string {
	switch id {
	case f.stA:
		return "stA"
	case f.stB:
		return "stB"
	}
	return id.String()[:8]
}

// --------------------------------------------------------------- cenário ---

type parityScenario struct {
	campStart, campEnd time.Time // meia-noite SP, inclusivos
	rules              []CreateDistributionRuleInput
	overrides          []parityOverride
	plays              []parityPlay
}

type parityOverride struct {
	typeID  uuid.UUID
	station uuid.UUID
	day     time.Time
	plays   int16
	start   string // "HH:MM"
	end     string // "HH:MM"
}

// parityPlay é uma tocada a gravar. state vazio = aprovada; os demais valores
// são as quatro formas de sair do conjunto aprovado (ApprovedDetectionsFilter).
type parityPlay struct {
	at      time.Time
	mat     uuid.UUID
	station uuid.UUID // zero = stA
	state   string    // "", "retracted", "ignored", "audit_rejected", "ambiguous"
}

// window é o recorte de dias passado ao scope do SQL. Tem que conter TODAS as
// tocadas do cenário — uma tocada fora do recorte não vira célula no SQL e o
// conjunto de chaves divergiria por construção, não por bug.
func (s parityScenario) window() (time.Time, time.Time) {
	from, to := s.campStart, s.campEnd
	for _, p := range s.plays {
		if p.at.Before(from) {
			from = p.at
		}
		if p.at.After(to) {
			to = p.at
		}
	}
	return parityDate(from.AddDate(0, 0, -2)), parityDate(to.AddDate(0, 0, 2))
}

// parityRun é um cenário já materializado no banco.
type parityRun struct {
	f      *parityFixture
	s      parityScenario
	campID uuid.UUID
	ids    []uuid.UUID // paralelo a s.plays
}

// build materializa o cenário. As detections entram por INSERT cru, com
// category 'orphan' de placeholder: nada aqui pode passar pelo insert-path (que
// GRAVA categoria) — os dois motores têm que ser avaliados do zero.
func (f *parityFixture) build(t *testing.T, s parityScenario) *parityRun {
	t.Helper()
	cmp, err := NewCampaigns(f.pool).Create(f.ctx, CreateCampaignInput{
		Name: "parity-camp-" + uuid.NewString()[:8], ClientID: f.client,
		StartDate: parityDate(s.campStart), EndDate: parityDate(s.campEnd),
		TargetStations: []uuid.UUID{f.stA, f.stB}})
	require.NoError(t, err)

	rules := NewDistributionRules(f.pool)
	for _, r := range s.rules {
		r.CampaignID = cmp.ID
		_, err := rules.Create(f.ctx, r)
		require.NoError(t, err)
	}
	for _, o := range s.overrides {
		_, err := f.pool.Exec(f.ctx, `
			INSERT INTO distribution_overrides
			  (campaign_id, type_id, station_id, for_date, plays_expected, time_start, time_end)
			VALUES ($1,$2,$3,$4::date,$5,$6::time,$7::time)`,
			cmp.ID, o.typeID, o.station, o.day.Format("2006-01-02"), o.plays, o.start, o.end)
		require.NoError(t, err)
	}

	run := &parityRun{f: f, s: s, campID: cmp.ID, ids: make([]uuid.UUID, len(s.plays))}
	for i, p := range s.plays {
		station := p.station
		if station == uuid.Nil {
			station = f.stA
		}
		evStatus := "available"
		var retracted, ignored any
		switch p.state {
		case "":
		case "retracted":
			retracted = time.Now()
		case "ignored":
			ignored = time.Now()
		case "audit_rejected":
			evStatus = "audit_rejected"
		case "ambiguous":
			// invariante ambiguous ⟺ retracted (ver detection_filter.go)
			evStatus, retracted = "ambiguous", time.Now()
		default:
			t.Fatalf("estado de tocada desconhecido: %q", p.state)
		}
		id := uuid.New()
		run.ids[i] = id
		_, err := f.pool.Exec(f.ctx, `
			INSERT INTO detections (id, station_id, commercial_id, campaign_id, detected_at,
			  match_start_offset_ms, match_end_offset_ms, confidence, hash_count,
			  evidence_status, retracted_at, ignored_at, category)
			VALUES ($1,$2,$3,$4,$5,0,30000,0.95,100,$6,$7,$8,'orphan')`,
			id, station, p.mat, cmp.ID, p.at, evStatus, retracted, ignored)
		require.NoError(t, err)
		_, err = f.pool.Exec(f.ctx, `
			INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
			VALUES ($1,$2,$3,$4,'orphan')`, id, p.at, cmp.ID, p.mat)
		require.NoError(t, err)
	}
	t.Cleanup(run.teardown)
	return run
}

func (r *parityRun) teardown() {
	f := r.f
	f.pool.Exec(f.ctx, `DELETE FROM detection_campaigns WHERE campaign_id = $1`, r.campID)
	f.pool.Exec(f.ctx, `DELETE FROM detections WHERE campaign_id = $1`, r.campID)
	f.pool.Exec(f.ctx, `DELETE FROM distribution_overrides WHERE campaign_id = $1`, r.campID)
	f.pool.Exec(f.ctx, `DELETE FROM distribution_rules WHERE campaign_id = $1`, r.campID)
	f.pool.Exec(f.ctx, `DELETE FROM campaigns WHERE id = $1`, r.campID)
}

// -------------------------------------------------------------- vereditos --

// sqlVerdicts roda o pipeline de classificação como SELECT PURO — nenhuma
// escrita. É a metade SQL da paridade.
func (r *parityRun) sqlVerdicts(t *testing.T) map[uuid.UUID]string {
	t.Helper()
	from, to := r.s.window()
	rows, err := r.f.pool.Query(r.f.ctx, recatScopeByCampaignSQL+recatSelectTailSQL,
		r.campID, (*uuid.UUID)(nil), ([]uuid.UUID)(nil), from, to)
	require.NoError(t, err)
	defer rows.Close()

	out := map[uuid.UUID]string{}
	for rows.Next() {
		var id, cid uuid.UUID
		var at time.Time
		var cat string
		require.NoError(t, rows.Scan(&id, &at, &cid, &cat))
		if prev, dup := out[id]; dup {
			t.Fatalf("SQL emitiu veredito DUPLICADO pra %s: %s e %s", id, prev, cat)
		}
		out[id] = cat
	}
	require.NoError(t, rows.Err())
	return out
}

// goVerdicts replica o insert-path: pra cada célula-dia (campanha, tipo,
// emissora, dia SP) com tocada, roda categorizer.Settle alimentado pelos
// LOADERS DE PRODUÇÃO. É a metade Go da paridade — e é o que faz este teste
// falar sobre produção, e não sobre uma reimplementação de teste.
func (r *parityRun) goVerdicts(t *testing.T) map[uuid.UUID]string {
	t.Helper()
	f := r.f
	var cs, ce time.Time
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`SELECT start_date, end_date FROM campaigns WHERE id=$1`, r.campID).Scan(&cs, &ce))

	type cellKey struct {
		station, typeID uuid.UUID
		day             time.Time
	}
	rows, err := f.pool.Query(f.ctx, `
		SELECT DISTINCT d.station_id, m.type_id,
		       date_trunc('day', dc.detected_at AT TIME ZONE 'America/Sao_Paulo')::date
		FROM detection_campaigns dc
		JOIN detections d ON d.id = dc.detection_id AND d.detected_at = dc.detected_at
		JOIN materials m ON m.id = dc.commercial_id
		WHERE dc.campaign_id = $1 AND m.type_id IS NOT NULL`, r.campID)
	require.NoError(t, err)
	var cells []cellKey
	for rows.Next() {
		var c cellKey
		require.NoError(t, rows.Scan(&c.station, &c.typeID, &c.day))
		cells = append(cells, c)
	}
	rows.Close()
	require.NoError(t, rows.Err())

	det := NewDetections(f.pool)
	out := map[uuid.UUID]string{}
	for _, c := range cells {
		dayLocal := time.Date(c.day.Year(), c.day.Month(), c.day.Day(), 0, 0, 0, 0, f.sp)
		dayEnd := dayLocal.AddDate(0, 0, 1)

		rules, err := det.loadRulesForCell(f.ctx, f.pool, r.campID, c.typeID, c.station)
		require.NoError(t, err)
		ov, err := det.loadOverrideForCell(f.ctx, f.pool, r.campID, c.typeID, c.station, dayLocal)
		require.NoError(t, err)
		plays, err := det.loadCellDayPlays(f.ctx, f.pool,
			CreateDetectionInput{CampaignID: r.campID, StationID: c.station},
			c.typeID, dayLocal, dayEnd, nil)
		require.NoError(t, err)
		if len(plays) == 0 {
			continue // célula só com linhas fora do conjunto aprovado
		}
		cp := make([]categorizer.Play, len(plays))
		for i, p := range plays {
			cp[i] = categorizer.Play{DetectedAt: p.detectedAt, MaterialID: p.materialID}
		}
		cats := categorizer.Settle(dayLocal, cp,
			categorizer.Campaign{StartDate: cs, EndDate: ce}, rules, ov)
		for i, p := range plays {
			out[p.id] = cats[i]
		}
	}
	return out
}

// metaN devolve o N (meta do dia) de cada célula-dia LENDO A CTE `meta` REAL do
// recatClassifiedCTE — não uma cópia dela. Serve de diagnóstico na falha e é o
// que TestSettleParity_MetaNMatchesViewExpected cruza com a view.
func (r *parityRun) metaN(t *testing.T) map[string]int {
	t.Helper()
	from, to := r.s.window()
	rows, err := r.f.pool.Query(r.f.ctx,
		recatScopeByCampaignSQL+recatClassifiedCTE+`
		SELECT station_id, type_id, for_date, n FROM meta`,
		r.campID, (*uuid.UUID)(nil), ([]uuid.UUID)(nil), from, to)
	require.NoError(t, err)
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var station, typeID uuid.UUID
		var forDate time.Time
		var n int
		require.NoError(t, rows.Scan(&station, &typeID, &forDate, &n))
		out[fmt.Sprintf("%s/%s/%s", r.f.stationName(station),
			r.f.typeName(typeID), forDate.Format("2006-01-02"))] = n
	}
	require.NoError(t, rows.Err())
	return out
}

func (f *parityFixture) typeName(id uuid.UUID) string {
	switch id {
	case f.tp1:
		return "tp1"
	case f.tp2:
		return "tp2"
	}
	return id.String()[:8]
}

// dump é a mensagem de falha: regras, override, N por célula, e a lista de
// tocadas com timestamp + veredito dos DOIS motores lado a lado. Uma falha de
// paridade que só diz "not equal" é inútil às 3 da manhã.
func (r *parityRun) dump(t *testing.T, sqlv, gov map[uuid.UUID]string) string {
	f := r.f
	out := fmt.Sprintf("campanha %s..%s (âncora %s, %s)\n",
		r.s.campStart.Format("2006-01-02"), r.s.campEnd.Format("2006-01-02"),
		f.anchor.Format("2006-01-02"), f.anchor.Weekday())
	for i, rule := range r.s.rules {
		var mats []string
		for _, m := range rule.MaterialIDs {
			mats = append(mats, f.matName(m))
		}
		var stations []string
		for _, s := range rule.StationIDs {
			stations = append(stations, f.stationName(s))
		}
		out += fmt.Sprintf("  regra[%d] tipo=%s %s..%s mask=%d %s-%s ppd=%d emissoras=%v materiais=%v\n",
			i, f.typeName(rule.TypeID),
			rule.StartDate.Format("2006-01-02"), rule.EndDate.Format("2006-01-02"),
			rule.WeekdayMask, rule.TimeStart, rule.TimeEnd, rule.PlaysPerDay, stations, mats)
	}
	for i, o := range r.s.overrides {
		out += fmt.Sprintf("  override[%d] tipo=%s emissora=%s dia=%s plays_expected=%d %s-%s\n",
			i, f.typeName(o.typeID), f.stationName(o.station),
			o.day.Format("2006-01-02"), o.plays, o.start, o.end)
	}
	nByCell := r.metaN(t)
	for _, k := range sortedKeys(nByCell) {
		out += fmt.Sprintf("  N[%s] = %d\n", k, nByCell[k])
	}
	out += "  tocadas (ordem cronológica; '-' = fora do conjunto aprovado, sem veredito):\n"
	order := make([]int, len(r.s.plays))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return r.s.plays[order[a]].at.Before(r.s.plays[order[b]].at)
	})
	for _, i := range order {
		p := r.s.plays[i]
		station := p.station
		if station == uuid.Nil {
			station = f.stA
		}
		mark := "  "
		if sqlv[r.ids[i]] != gov[r.ids[i]] {
			mark = "!!"
		}
		out += fmt.Sprintf("  %s play[%02d] %s (%s) mat=%-7s emissora=%-4s estado=%-14s go=%-9s sql=%-9s id=%s\n",
			mark, i, p.at.In(f.sp).Format("2006-01-02 15:04:05"), p.at.In(f.sp).Format("Mon"),
			f.matName(p.mat), f.stationName(station), "\""+p.state+"\"",
			orDash(gov[r.ids[i]]), orDash(sqlv[r.ids[i]]), r.ids[i])
	}
	return out
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// requireParity é o coração do arquivo: roda os dois motores e exige vereditos
// idênticos, chave a chave. `note` entra no cabeçalho da falha (índice do
// cenário, seed etc.).
func (r *parityRun) requireParity(t *testing.T, note string) map[uuid.UUID]string {
	t.Helper()
	sqlv := r.sqlVerdicts(t)
	gov := r.goVerdicts(t)

	var bad []string
	seen := map[uuid.UUID]bool{}
	for id, gc := range gov {
		seen[id] = true
		if sc, ok := sqlv[id]; !ok {
			bad = append(bad, fmt.Sprintf("    %s: go=%s sql=<ausente>", id, gc))
		} else if sc != gc {
			bad = append(bad, fmt.Sprintf("    %s: go=%s sql=%s", id, gc, sc))
		}
	}
	for id, sc := range sqlv {
		if !seen[id] {
			bad = append(bad, fmt.Sprintf("    %s: go=<ausente> sql=%s", id, sc))
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		t.Fatalf("DIVERGÊNCIA Go × SQL (%s) — um dos dois motores está errado, conserte o motor:\n%s  vereditos divergentes:\n%s",
			note, r.dump(t, sqlv, gov), joinParityLines(bad))
	}
	return gov
}

// requireCategories confere o veredito de cada tocada contra o modelo da spec
// (§2). want[i] == "" significa "não deve ter veredito nenhum" — é como as
// linhas fora do conjunto aprovado são afirmadas.
func (r *parityRun) requireCategories(t *testing.T, got map[uuid.UUID]string, want ...string) {
	t.Helper()
	require.Lenf(t, want, len(r.s.plays), "esperado um veredito por tocada")
	for i, w := range want {
		g, ok := got[r.ids[i]]
		if w == "" {
			require.Falsef(t, ok, "play[%d] (%s) está fora do conjunto aprovado e não pode ter veredito, veio %q",
				i, r.s.plays[i].at.In(r.f.sp).Format("15:04:05"), g)
			continue
		}
		require.Truef(t, ok, "play[%d] (%s) não recebeu veredito", i,
			r.s.plays[i].at.In(r.f.sp).Format("15:04:05"))
		require.Equalf(t, w, g, "play[%d] (%s, mat=%s): modelo da spec diz %q",
			i, r.s.plays[i].at.In(r.f.sp).Format("15:04:05"), r.f.matName(r.s.plays[i].mat), w)
	}
}

func joinParityLines(ss []string) string {
	out := ""
	for _, s := range ss {
		out += s + "\n"
	}
	return out
}

// rule é o atalho pra uma regra geral (todos os materiais do tipo) da célula.
func (f *parityFixture) rule(typeID uuid.UUID, stations []uuid.UUID, fromOff, toOff int,
	mask int16, start, end string, ppd int16, mats ...uuid.UUID) CreateDistributionRuleInput {
	return CreateDistributionRuleInput{
		TypeID: typeID, StationIDs: stations, MaterialIDs: mats,
		StartDate: parityDate(f.day(fromOff)), EndDate: parityDate(f.day(toOff)),
		WeekdayMask: mask, TimeStart: start, TimeEnd: end, PlaysPerDay: ppd,
	}
}

const parityAllWeekdays int16 = 127

// ------------------------------------------------------- tabela-verdade ----

// TestSettleParity_TruthTable percorre as seis linhas da tabela-verdade da spec
// (docs/superpowers/specs/2026-08-14-quota-aware-categorization-design.md §2) e
// exige TRÊS coisas por linha: o veredito do Go, o do SQL e o do modelo escrito
// pelo dono do produto — todos iguais.
func TestSettleParity_TruthTable(t *testing.T) {
	f := newParityFixture(t)

	// Faixa 10:00-12:00 em todos os casos; "dentro" = 10:30/11:00/11:30/11:45,
	// "fora" = 03:00/04:00/05:00 (bem além dos 15 min de tolerância).
	// A âncora é SEGUNDA-FEIRA (parityAnchor), o que deixa a linha "célula
	// zerada" montável das duas formas que ela existe em produção.
	const tueWed int16 = 1<<int(time.Tuesday) | 1<<int(time.Wednesday)

	cases := []struct {
		name      string
		n         int16
		rules     []CreateDistributionRuleInput
		overrides []parityOverride
		times     []time.Time
		want      []string
	}{
		{
			name:  "N=2, 1 dentro 1 fora -> 1 in_slot + 1 out_slot (deficit 1)",
			n:     2,
			times: []time.Time{f.at(0, 3, 0, 0), f.at(0, 10, 30, 0)},
			want:  []string{"out_slot", "in_slot"},
		},
		{
			name:  "N=2, 2 dentro 1 fora -> 2 in_slot + 1 bonus",
			n:     2,
			times: []time.Time{f.at(0, 3, 0, 0), f.at(0, 10, 30, 0), f.at(0, 11, 0, 0)},
			// a de 03:00 nasce out_slot e vira bonus quando a meta fecha dentro da faixa
			want: []string{"bonus", "in_slot", "in_slot"},
		},
		{
			name:  "N=2, 0 dentro 3 fora -> 3 out_slot (bonus 0)",
			n:     2,
			times: []time.Time{f.at(0, 3, 0, 0), f.at(0, 4, 0, 0), f.at(0, 5, 0, 0)},
			want:  []string{"out_slot", "out_slot", "out_slot"},
		},
		{
			name:  "N=2, 4 dentro -> 2 in_slot + 2 bonus",
			n:     2,
			times: []time.Time{f.at(0, 10, 30, 0), f.at(0, 11, 0, 0), f.at(0, 11, 30, 0), f.at(0, 11, 45, 0)},
			want:  []string{"in_slot", "in_slot", "bonus", "bonus"},
		},
		{
			name: "N=3 em 2 faixas, 3 dentro da 1a faixa -> 3 in_slot (a meta é do DIA)",
			rules: []CreateDistributionRuleInput{
				f.rule(f.tp1, []uuid.UUID{f.stA}, -3, 3, parityAllWeekdays, "10:00", "12:00", 2),
				f.rule(f.tp1, []uuid.UUID{f.stA}, -3, 3, parityAllWeekdays, "18:00", "20:00", 1),
			},
			times: []time.Time{f.at(0, 10, 30, 0), f.at(0, 11, 0, 0), f.at(0, 11, 30, 0)},
			want:  []string{"in_slot", "in_slot", "in_slot"},
		},
		{
			// Célula zerada por OVERRIDE — o caso da campanha 270. A regra do dia
			// vale (ppd=3) mas o override plays_expected=0 supersede: N=0.
			// (plays_per_day = 0 não é montável: o CHECK de distribution_rules
			// proíbe. As duas formas reais de N=0 são esta e a de baixo.)
			name: "N=0 por override zerado, 3 quaisquer -> 3 bonus",
			n:    3,
			overrides: []parityOverride{
				{typeID: f.tp1, station: f.stA, day: f.day(0), plays: 0, start: "10:00", end: "12:00"}},
			times: []time.Time{f.at(0, 3, 0, 0), f.at(0, 10, 30, 0), f.at(0, 14, 0, 0)},
			want:  []string{"bonus", "bonus", "bonus"},
		},
		{
			// Célula zerada por AUSÊNCIA de meta: a regra existe mas não cobre
			// segunda-feira → nenhuma faixa vale hoje e N=0.
			name: "N=0 por regra que nao cobre o dia, 3 quaisquer -> 3 bonus",
			rules: []CreateDistributionRuleInput{
				f.rule(f.tp1, []uuid.UUID{f.stA}, -3, 3, tueWed, "10:00", "12:00", 2)},
			times: []time.Time{f.at(0, 3, 0, 0), f.at(0, 10, 30, 0), f.at(0, 14, 0, 0)},
			want:  []string{"bonus", "bonus", "bonus"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rules := tc.rules
			if rules == nil {
				rules = []CreateDistributionRuleInput{
					f.rule(f.tp1, []uuid.UUID{f.stA}, -3, 3, parityAllWeekdays, "10:00", "12:00", tc.n),
				}
			}
			s := parityScenario{
				campStart: f.day(-3), campEnd: f.day(3), rules: rules,
				overrides: tc.overrides,
			}
			for _, at := range tc.times {
				s.plays = append(s.plays, parityPlay{at: at, mat: f.matA})
			}
			run := f.build(t, s)
			got := run.requireParity(t, "tabela-verdade: "+tc.name)
			run.requireCategories(t, got, tc.want...)
		})
	}
}

// ----------------------------------------------------------- casos-limite --

// TestSettleParity_ToleranceBoundaries fixa os 900 s de folga
// (categorizer.SlotToleranceSeconds × o literal 900 do SQL) nos dois extremos
// da faixa, e a 1 segundo além. É a divergência que já aconteceu de verdade:
// a tolerância existia num motor e não no outro.
func TestSettleParity_ToleranceBoundaries(t *testing.T) {
	f := newParityFixture(t)

	// N grande o bastante pra que "dentro da faixa" apareça como in_slot e
	// "fora" como out_slot (a meta nunca fecha) — assim o veredito distingue
	// exatamente o que queremos medir.
	const n = 9
	times := []time.Time{
		f.at(0, 9, 44, 59), // 901 s antes de 10:00 → FORA
		f.at(0, 9, 45, 0),  // 900 s antes de 10:00 → dentro (limite exato)
		f.at(0, 12, 15, 0), // 900 s depois de 12:00 → dentro (limite exato)
		f.at(0, 12, 15, 1), // 901 s depois de 12:00 → FORA
	}
	want := []string{"out_slot", "in_slot", "in_slot", "out_slot"}

	t.Run("faixa da regra", func(t *testing.T) {
		s := parityScenario{campStart: f.day(-3), campEnd: f.day(3),
			rules: []CreateDistributionRuleInput{
				f.rule(f.tp1, []uuid.UUID{f.stA}, -3, 3, parityAllWeekdays, "10:00", "12:00", n)}}
		for _, at := range times {
			s.plays = append(s.plays, parityPlay{at: at, mat: f.matA})
		}
		run := f.build(t, s)
		run.requireCategories(t, run.requireParity(t, "tolerância na faixa da regra"), want...)
	})

	t.Run("faixa do override", func(t *testing.T) {
		// Override com faixa DIFERENTE da regra: a faixa considerada tem que ser
		// só a do override, com a mesma tolerância.
		s := parityScenario{campStart: f.day(-3), campEnd: f.day(3),
			rules: []CreateDistributionRuleInput{
				f.rule(f.tp1, []uuid.UUID{f.stA}, -3, 3, parityAllWeekdays, "18:00", "20:00", 2)},
			overrides: []parityOverride{
				{typeID: f.tp1, station: f.stA, day: f.day(0), plays: n, start: "10:00", end: "12:00"}}}
		for _, at := range times {
			s.plays = append(s.plays, parityPlay{at: at, mat: f.matA})
		}
		run := f.build(t, s)
		run.requireCategories(t, run.requireParity(t, "tolerância na faixa do override"), want...)
	})
}

// TestSettleParity_SameSecondTie: duas tocadas no MESMO segundo com uma vaga só.
// O desempate é por id nos dois motores (ORDER BY detected_at, id no SQL;
// loadCellDayPlays na mesma ordem + sort estável no Settle). Se um dos lados
// perder o desempate por id, a vaga muda de dono e este teste pega.
func TestSettleParity_SameSecondTie(t *testing.T) {
	f := newParityFixture(t)
	at := f.at(0, 10, 30, 0)
	s := parityScenario{campStart: f.day(-3), campEnd: f.day(3),
		rules: []CreateDistributionRuleInput{
			f.rule(f.tp1, []uuid.UUID{f.stA}, -3, 3, parityAllWeekdays, "10:00", "12:00", 1)},
		plays: []parityPlay{{at: at, mat: f.matA}, {at: at, mat: f.matB}}}
	run := f.build(t, s)
	got := run.requireParity(t, "empate no mesmo segundo")

	// O menor id leva a vaga nos dois motores.
	winner, loser := 0, 1
	if run.ids[1].String() < run.ids[0].String() {
		winner, loser = 1, 0
	}
	require.Equalf(t, categorizer.CatInSlot, got[run.ids[winner]],
		"empate: o menor id (%s) tem que levar a vaga", run.ids[winner])
	require.Equal(t, categorizer.CatBonus, got[run.ids[loser]],
		"empate: o perdedor é excedente, não in_slot (in_slot nunca passa de N)")
}

// TestSettleParity_CampaignPeriodBoundary: o primeiro e o último dia do período
// são INCLUSIVOS nos dois motores. O bug de 2026-05-26 (seis tocadas do último
// dia marcadas out_date por comparação em UTC) mora exatamente aqui.
func TestSettleParity_CampaignPeriodBoundary(t *testing.T) {
	f := newParityFixture(t)
	s := parityScenario{campStart: f.day(0), campEnd: f.day(2),
		rules: []CreateDistributionRuleInput{
			f.rule(f.tp1, []uuid.UUID{f.stA}, -3, 5, parityAllWeekdays, "10:00", "12:00", 1)},
		plays: []parityPlay{
			{at: f.at(-1, 10, 30, 0), mat: f.matA}, // véspera do início
			{at: f.at(0, 10, 30, 0), mat: f.matA},  // primeiro dia (inclusivo)
			{at: f.at(0, 23, 59, 59), mat: f.matA}, // último segundo do primeiro dia
			{at: f.at(2, 10, 30, 0), mat: f.matA},  // último dia (inclusivo)
			{at: f.at(3, 10, 30, 0), mat: f.matA},  // dia seguinte ao fim
		}}
	run := f.build(t, s)
	run.requireCategories(t, run.requireParity(t, "bordas do período da campanha"),
		"out_date", "in_slot", "bonus", "in_slot", "out_date")
}

// TestSettleParity_NonApprovedDoesNotConsumeQuota: retratada / ignorada /
// audit_rejected / ambiguous não entram na cota em NENHUM dos dois motores — e
// nenhum dos dois emite veredito pra elas (o recat deixa a categoria como está).
// Sem isso, uma linha não-aprovada roubaria a vaga só de um lado e as duas
// máquinas divergiriam em toda célula-dia que tivesse uma.
func TestSettleParity_NonApprovedDoesNotConsumeQuota(t *testing.T) {
	f := newParityFixture(t)
	s := parityScenario{campStart: f.day(-3), campEnd: f.day(3),
		rules: []CreateDistributionRuleInput{
			f.rule(f.tp1, []uuid.UUID{f.stA}, -3, 3, parityAllWeekdays, "10:00", "12:00", 1)},
		plays: []parityPlay{
			{at: f.at(0, 10, 0, 0), mat: f.matA, state: "retracted"},
			{at: f.at(0, 10, 10, 0), mat: f.matA, state: "ignored"},
			{at: f.at(0, 10, 20, 0), mat: f.matA, state: "audit_rejected"},
			{at: f.at(0, 10, 30, 0), mat: f.matA, state: "ambiguous"},
			{at: f.at(0, 11, 0, 0), mat: f.matA}, // única aprovada: leva a vaga
			{at: f.at(0, 3, 0, 0), mat: f.matA, state: "retracted"},
		}}
	run := f.build(t, s)
	run.requireCategories(t, run.requireParity(t, "linhas fora do conjunto aprovado"),
		"", "", "", "", "in_slot", "")
}

// TestSettleParity_CarveOut cobre o material escopado a regras que o nomeiam
// (material_ids), incluindo o caso que já mudou de veredito uma vez: dia DENTRO
// do período das regras dele mas em dia-da-semana que nenhuma delas cobre.
func TestSettleParity_CarveOut(t *testing.T) {
	f := newParityFixture(t)

	// A âncora é SEGUNDA. A regra do carve-out (matB) cobre só TERÇA e QUARTA:
	// no dia 0 (segunda) ela não vale, mas o dia está no período dela.
	const tueWed int16 = 1<<int(time.Tuesday) | 1<<int(time.Wednesday)

	s := parityScenario{campStart: f.day(-7), campEnd: f.day(7),
		rules: []CreateDistributionRuleInput{
			// regra GERAL do tipo (não vale pro carve-out), todos os dias
			f.rule(f.tp1, []uuid.UUID{f.stA}, -7, 7, parityAllWeekdays, "10:00", "12:00", 2),
			// regra do carve-out (matB): só ter/qua, período 0..2
			f.rule(f.tp1, []uuid.UUID{f.stA}, 0, 2, tueWed, "14:00", "15:00", 1, f.matB),
		},
		plays: []parityPlay{
			// matB na TERÇA (dia 1), dentro da faixa dele → in_slot
			{at: f.at(1, 14, 30, 0), mat: f.matB},
			// matB na SEGUNDA (dia 0): dentro do PERÍODO da regra dele, mas em
			// dia-da-semana sem meta. Não é out_date; a célula tem N=2 (da regra
			// geral) e a meta ainda está aberta → out_slot.
			{at: f.at(0, 14, 30, 0), mat: f.matB},
			// matB fora do período das regras que o nomeiam → out_date, e não
			// consome cota nenhuma
			{at: f.at(5, 14, 30, 0), mat: f.matB},
			// matA (comum) julgado só pelas regras gerais
			{at: f.at(0, 10, 30, 0), mat: f.matA},
			{at: f.at(0, 14, 30, 0), mat: f.matA}, // fora da faixa geral, meta aberta
		}}
	run := f.build(t, s)
	run.requireCategories(t, run.requireParity(t, "carve-out"),
		"in_slot", "out_slot", "out_date", "in_slot", "out_slot")
}

// TestSettleParity_ZeroExpectedOverride é o caso da campanha 270: override com
// plays_expected = 0 zera a meta do dia, e TODA tocada do dia (dentro ou fora
// da faixa gravada, que fica inerte) vira bonus nos dois motores. O ramo
// especial de plays_expected = 0 sumiu dos dois — este teste é quem garante que
// ele não volta em só um deles.
func TestSettleParity_ZeroExpectedOverride(t *testing.T) {
	f := newParityFixture(t)
	s := parityScenario{campStart: f.day(-3), campEnd: f.day(3),
		rules: []CreateDistributionRuleInput{
			f.rule(f.tp1, []uuid.UUID{f.stA}, -3, 3, parityAllWeekdays, "10:00", "12:00", 3)},
		overrides: []parityOverride{
			{typeID: f.tp1, station: f.stA, day: f.day(0), plays: 0, start: "10:00", end: "12:00"}},
		plays: []parityPlay{
			{at: f.at(0, 10, 30, 0), mat: f.matA}, // dentro da faixa do override
			{at: f.at(0, 3, 0, 0), mat: f.matA},   // fora dela
			{at: f.at(0, 23, 0, 0), mat: f.matB},
			// dia SEGUINTE (sem override): a regra volta a valer — prova que o
			// override zera só a célula-dia dele.
			{at: f.at(1, 10, 30, 0), mat: f.matA},
		}}
	run := f.build(t, s)
	run.requireCategories(t, run.requireParity(t, "override com plays_expected=0"),
		"bonus", "bonus", "bonus", "in_slot")
}

// TestSettleParity_MultiTypeIndependence: dois TIPOS de material na mesma
// (campanha, emissora, dia) fecham independentemente — a cota particiona por
// célula, e a célula inclui o tipo. Um N vazando entre tipos daria in_slot a
// mais num e a menos no outro; o fuzz de um tipo só não pegaria.
func TestSettleParity_MultiTypeIndependence(t *testing.T) {
	f := newParityFixture(t)
	s := parityScenario{campStart: f.day(-3), campEnd: f.day(3),
		rules: []CreateDistributionRuleInput{
			f.rule(f.tp1, []uuid.UUID{f.stA}, -3, 3, parityAllWeekdays, "08:00", "09:00", 1),
			f.rule(f.tp2, []uuid.UUID{f.stA}, -3, 3, parityAllWeekdays, "14:00", "15:00", 2),
		},
		plays: []parityPlay{
			{at: f.at(0, 8, 10, 0), mat: f.matA},  // tp1 dentro #1 (N=1) → in_slot
			{at: f.at(0, 8, 40, 0), mat: f.matA},  // tp1 dentro #2 → bonus
			{at: f.at(0, 20, 0, 0), mat: f.matB},  // tp1 fora, meta fechada → bonus
			{at: f.at(0, 14, 5, 0), mat: f.matC},  // tp2 dentro #1 (N=2) → in_slot
			{at: f.at(0, 14, 30, 0), mat: f.matC}, // tp2 dentro #2 → in_slot
			{at: f.at(0, 14, 50, 0), mat: f.matC}, // tp2 dentro #3 → bonus
			{at: f.at(0, 3, 0, 0), mat: f.matC},   // tp2 fora, meta fechada → bonus
		}}
	run := f.build(t, s)
	run.requireCategories(t, run.requireParity(t, "independência entre tipos"),
		"in_slot", "bonus", "bonus", "in_slot", "in_slot", "bonus", "bonus")
}

// TestSettleParity_MultiStationIndependence: a mesma prova pra emissora — duas
// emissoras no mesmo dia/tipo têm cotas separadas.
func TestSettleParity_MultiStationIndependence(t *testing.T) {
	f := newParityFixture(t)
	s := parityScenario{campStart: f.day(-3), campEnd: f.day(3),
		rules: []CreateDistributionRuleInput{
			f.rule(f.tp1, []uuid.UUID{f.stA, f.stB}, -3, 3, parityAllWeekdays, "10:00", "12:00", 1)},
		plays: []parityPlay{
			{at: f.at(0, 10, 30, 0), mat: f.matA, station: f.stA},
			{at: f.at(0, 11, 0, 0), mat: f.matA, station: f.stA}, // excedente em stA
			{at: f.at(0, 11, 30, 0), mat: f.matA, station: f.stB},
			{at: f.at(0, 3, 0, 0), mat: f.matA, station: f.stB}, // fora, meta de stB já fechada
		}}
	run := f.build(t, s)
	run.requireCategories(t, run.requireParity(t, "independência entre emissoras"),
		"in_slot", "bonus", "in_slot", "bonus")
}

// TestSettleParity_MetaNMatchesViewExpected cruza o N calculado pela CTE `meta`
// (a mesma que decide quantas tocadas viram in_slot) com o `expected` de
// daily_play_summary_for(), que é de onde o déficit sai.
//
// Por que isto importa mesmo com as categorias em paridade: se os dois números
// discordarem, as categorias podem estar certas e o `deficit = N − in_slot`
// sair errado — e nada mais na suíte perceberia.
func TestSettleParity_MetaNMatchesViewExpected(t *testing.T) {
	f := newParityFixture(t)
	day := f.day(0)
	dayStr := day.Format("2006-01-02")

	s := parityScenario{campStart: f.day(-3), campEnd: f.day(3),
		rules: []CreateDistributionRuleInput{
			// duas regras sobrepostas em stA (o N SOMA) + a mesma valendo em stB
			f.rule(f.tp1, []uuid.UUID{f.stA, f.stB}, -3, 3, parityAllWeekdays, "08:00", "12:00", 3),
			f.rule(f.tp1, []uuid.UUID{f.stA}, -3, 3, parityAllWeekdays, "18:00", "22:00", 2),
			// carve-out: também entra no N (N é da célula-dia, não do material)
			f.rule(f.tp1, []uuid.UUID{f.stA}, -3, 3, parityAllWeekdays, "23:00", "23:59", 4, f.matA),
		},
		// uma tocada por célula: sem tocada não existe célula no scope do recat
		plays: []parityPlay{
			{at: f.at(0, 9, 0, 0), mat: f.matA, station: f.stA},
			{at: f.at(0, 9, 0, 0), mat: f.matA, station: f.stB},
		}}
	run := f.build(t, s)
	run.requireParity(t, "meta.N × view.expected")

	viewExpected := func(station uuid.UUID) int {
		var n int
		require.NoError(t, f.pool.QueryRow(f.ctx, `
			SELECT COALESCE(SUM(expected),0)::int
			FROM daily_play_summary_for($1::date, $1::date, ARRAY[$2::uuid])
			WHERE station_id = $3 AND type_id = $4`,
			dayStr, run.campID, station, f.tp1).Scan(&n))
		return n
	}
	check := func(what string, station uuid.UUID, want int) {
		t.Helper()
		key := fmt.Sprintf("%s/tp1/%s", f.stationName(station), dayStr)
		n := run.metaN(t)
		v := viewExpected(station)
		t.Logf("%s: meta.N=%d  view.expected=%d", what, n[key], v)
		require.Equalf(t, v, n[key],
			"%s: meta.N (categorização) e expected (déficit) TÊM que ser o mesmo número", what)
		require.Equalf(t, want, n[key], "%s: N esperado pelo modelo", what)
	}
	check("stA (3+2+4, o carve-out conta)", f.stA, 9)
	check("stB (só a regra compartilhada)", f.stB, 3)

	// Override supersede as regras nos dois lados.
	_, err := f.pool.Exec(f.ctx, `
		INSERT INTO distribution_overrides
		  (campaign_id, type_id, station_id, for_date, plays_expected, time_start, time_end)
		VALUES ($1,$2,$3,$4::date,1,'10:00'::time,'11:00'::time)`,
		run.campID, f.tp1, f.stA, dayStr)
	require.NoError(t, err)
	check("stA com override=1", f.stA, 1)

	// E o caso da campanha 270: plays_expected = 0 → N = 0 dos dois lados.
	_, err = f.pool.Exec(f.ctx, `
		UPDATE distribution_overrides SET plays_expected = 0
		WHERE campaign_id=$1 AND type_id=$2 AND station_id=$3 AND for_date=$4::date`,
		run.campID, f.tp1, f.stA, dayStr)
	require.NoError(t, err)
	check("stA com override=0", f.stA, 0)

	// A categorização tem que continuar em paridade com a meta zerada.
	run.requireParity(t, "meta.N × view.expected, com override=0")
}

// ------------------------------------------------------------ fuzz -------- //

// TestSettleParity_Randomized é o sweep diferencial: gera cenários aleatórios
// (regras sobrepostas, carve-outs, overrides, tocadas na borda exata da
// tolerância, empates, linhas não-aprovadas) e exige paridade em cada um.
//
// É ele que pega a divergência que ninguém pensou em escrever à mão — as três
// mutações usadas pra calibrar sensibilidade (rn_in <= n+1; tolerância 899;
// teste extra de dia-da-semana no ramo is_out_date do carve-out) caem em menos
// de 20 cenários.
//
// Determinismo: seed constante (settleParitySeed) e âncora sempre numa
// segunda-feira, então o mesmo seed gera o mesmo cenário. A falha imprime seed,
// âncora e índice do cenário — SETTLE_PARITY_SEED / SETTLE_PARITY_SCENARIOS
// re-rodam exatamente aquilo.
func TestSettleParity_Randomized(t *testing.T) {
	f := newParityFixture(t)
	seed := settleParitySeedValue(t)
	n := settleParityScenarioCount(t)
	rnd := rand.New(rand.NewSource(seed))
	t.Logf("sweep diferencial: %d cenários, seed=%d, âncora=%s (%s)",
		n, seed, f.anchor.Format("2006-01-02"), f.anchor.Weekday())

	cover := map[string]int{}
	verdicts := 0
	for i := 0; i < n; i++ {
		s := f.genScenario(rnd, i%2 == 0)
		run := f.build(t, s)
		got := run.requireParity(t,
			fmt.Sprintf("sweep aleatório: cenário %d/%d, seed=%d (SETTLE_PARITY_SEED=%d pra reproduzir)",
				i, n, seed, seed))
		verdicts += len(got)
		for _, c := range got {
			cover["cat:"+c]++
		}
		for _, r := range s.rules {
			if len(r.MaterialIDs) > 0 {
				cover["regra:carve-out"]++
			}
		}
		for _, o := range s.overrides {
			cover["override"]++
			if o.plays == 0 {
				cover["override:zero"]++
			}
		}
		for _, p := range s.plays {
			if p.state != "" {
				cover["tocada:nao-aprovada"]++
			}
			if p.station == f.stB {
				cover["tocada:stB"]++
			}
			if p.mat == f.matC {
				cover["tocada:tp2"]++
			}
		}
		run.teardown()
	}
	for _, k := range sortedKeys(cover) {
		t.Logf("cobertura %-22s %d", k, cover[k])
	}
	t.Logf("paridade: %d cenários, %d vereditos, 0 divergências", n, verdicts)
	require.Positivef(t, verdicts, "o sweep não produziu veredito nenhum — o gerador quebrou")
}

// genScenario monta um cenário aleatório. `dense` alterna entre um cenário
// permissivo (regra ampla, muitas tocadas — faz a cota REALMENTE cortar e os
// ramos de bonus/out_slot dispararem) e um esparso (regras estreitas, poucas
// tocadas — exercita out_date, célula sem regra e cota zerada).
func (f *parityFixture) genScenario(rnd *rand.Rand, dense bool) parityScenario {
	var s parityScenario
	s.campStart = f.day(-rnd.Intn(3))
	s.campEnd = f.day(rnd.Intn(3))
	if dense {
		s.campStart, s.campEnd = f.day(-3), f.day(3)
	}

	stationsFor := func() []uuid.UUID {
		if rnd.Intn(3) == 0 {
			return []uuid.UUID{f.stA, f.stB}
		}
		return []uuid.UUID{f.stA}
	}

	nRules := rnd.Intn(4)
	if dense && nRules == 0 {
		nRules = 1
	}
	for i := 0; i < nRules; i++ {
		from := -rnd.Intn(5)
		to := from + rnd.Intn(7)
		h1 := rnd.Intn(22)
		h2 := h1 + 1 + rnd.Intn(2)
		mask := int16(rnd.Intn(128))
		if dense {
			// regra permissiva: todo dia, faixa larga → muita tocada DENTRO da
			// faixa, e aí o corte da cota (rn_in <= n) e o ramo de excedente do
			// passo 4 disparam em vez de tudo cair fora da faixa
			from, to, mask, h1, h2 = -2, 2, parityAllWeekdays, 6, 20
		}
		var mats []uuid.UUID
		switch rnd.Intn(6) {
		case 0, 1:
			mats = []uuid.UUID{f.matA}
		case 2:
			mats = []uuid.UUID{f.matA, f.matB}
		case 3:
			mats = []uuid.UUID{f.matB}
		default:
			mats = nil
		}
		typeID := f.tp1
		if rnd.Intn(5) == 0 {
			typeID, mats = f.tp2, nil // tp2 só tem matC; carve-out fica no tp1
		}
		s.rules = append(s.rules, f.rule(typeID, stationsFor(), from, to, mask,
			hhmmParity(h1, rnd.Intn(60)), hhmmParity(h2, rnd.Intn(60)),
			int16(1+rnd.Intn(3)), mats...))
	}

	if rnd.Intn(3) == 0 {
		s.overrides = append(s.overrides, parityOverride{
			typeID: f.tp1, station: f.stA, day: f.day(rnd.Intn(3) - 1),
			plays: int16(rnd.Intn(4)),
			start: hhmmParity(rnd.Intn(22), 0), end: hhmmParity(23, 0)})
		// a faixa fica larga de propósito: o interesse aqui é o N do override
		s.overrides[len(s.overrides)-1].end = hhmmParity(23, rnd.Intn(60))
	}

	nPlays := rnd.Intn(8)
	if dense {
		nPlays = 3 + rnd.Intn(6)
	}
	for i := 0; i < nPlays; i++ {
		off := rnd.Intn(3) - 1
		at := f.at(off, rnd.Intn(24), rnd.Intn(60), rnd.Intn(60))
		mat := f.matA
		switch rnd.Intn(5) {
		case 0, 1:
			mat = f.matB
		case 2:
			mat = f.matC // tipo 2
		}
		station := f.stA
		if rnd.Intn(4) == 0 {
			station = f.stB
		}
		state := ""
		switch rnd.Intn(12) {
		case 0:
			state = "retracted"
		case 1:
			state = "ignored"
		case 2:
			state = "audit_rejected"
		case 3:
			state = "ambiguous"
		}
		s.plays = append(s.plays, parityPlay{at: at, mat: mat, station: station, state: state})
	}

	// Adversarial 1: tocadas cravadas no limite ±900 s de uma regra (e 1 s além).
	if len(s.rules) > 0 && rnd.Intn(2) == 0 {
		r := s.rules[rnd.Intn(len(s.rules))]
		var h1, m1, h2, m2 int
		fmt.Sscanf(r.TimeStart, "%d:%d", &h1, &m1)
		fmt.Sscanf(r.TimeEnd, "%d:%d", &h2, &m2)
		off := rnd.Intn(3) - 1
		mat := f.matA
		if rnd.Intn(2) == 0 {
			mat = f.matB
		}
		for _, sec := range []int{
			h1*3600 + m1*60 - 900, h1*3600 + m1*60 - 901,
			h2*3600 + m2*60 + 900, h2*3600 + m2*60 + 901,
		} {
			if sec < 0 || sec >= 86400 {
				continue
			}
			s.plays = append(s.plays, parityPlay{
				at: f.at(off, sec/3600, (sec%3600)/60, sec%60), mat: mat, station: f.stA})
		}
	}

	// Adversarial 2: empate exato no mesmo segundo (desempate por id).
	if len(s.plays) > 0 && rnd.Intn(3) == 0 {
		p := s.plays[rnd.Intn(len(s.plays))]
		s.plays = append(s.plays,
			parityPlay{at: p.at, mat: f.matA, station: p.station},
			parityPlay{at: p.at, mat: f.matB, station: p.station})
	}
	return s
}

func hhmmParity(h, m int) string { return fmt.Sprintf("%02d:%02d", h, m) }
