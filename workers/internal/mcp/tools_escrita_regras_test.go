package mcp

// Task 16: criar_regra_distribuicao, editar_regra_distribuicao e
// apagar_regra_distribuicao.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/api/handlers"
	"radiocheck/internal/catalog"
	"radiocheck/internal/categorizer"
	"radiocheck/internal/mcp/mcptest"
)

// ── fixtures e leitura ──────────────────────────────────────────────────

// cenarioRegra: cliente, três emissoras (E3 fora da campanha), campanha ativa com
// E1 e E2, dois tipos, e três materiais: MAT (tipo A, vinculado), MATB (tipo B,
// vinculado) e SOLTO (tipo A, NÃO vinculado).
type cenarioRegra struct {
	cli, e1, e2, e3, camp, tipoA, tipoB, mat, matB, solto uuid.UUID
}

// semearRegra cria o cenário. tipoA/tipoB nil = cria os tipos (passe os mesmos às duas
// fixtures de um teste de paridade: o nome do tipo entra na comparação).
func semearRegra(t *testing.T, b *bancadaTool, tipoA, tipoB uuid.UUID, inicio, fim string) cenarioRegra {
	t.Helper()
	c := cenarioRegra{tipoA: tipoA, tipoB: tipoB}
	if c.tipoA == uuid.Nil {
		c.tipoA = mcptest.TipoMaterial(t, b.ctx, b.pool, "Spot")
		c.tipoB = mcptest.TipoMaterial(t, b.ctx, b.pool, "Jingle")
	}
	c.cli = mcptest.Cliente(t, b.ctx, b.pool, "Cliente "+uuid.NewString()[:6])
	c.e1 = mcptest.Emissora(t, b.ctx, b.pool, "Rádio 1", 1000)
	c.e2 = mcptest.Emissora(t, b.ctx, b.pool, "Rádio 2", 1000)
	c.e3 = mcptest.Emissora(t, b.ctx, b.pool, "Rádio 3", 1000)
	c.camp = mcptest.Campanha(t, b.ctx, b.pool, c.cli, "Campanha Regras", inicio, fim, c.e1, c.e2)
	mcptest.EstadoCampanha(t, b.ctx, b.pool, c.camp, "ativa")
	c.mat = mcptest.Material(t, b.ctx, b.pool, c.cli, c.tipoA, "Spot Regras")
	c.matB = mcptest.Material(t, b.ctx, b.pool, c.cli, c.tipoB, "Jingle Regras")
	c.solto = mcptest.Material(t, b.ctx, b.pool, c.cli, c.tipoA, "Spot Solto")
	mcptest.Vincular(t, b.ctx, b.pool, c.camp, c.mat, c.e1, c.e2)
	mcptest.Vincular(t, b.ctx, b.pool, c.camp, c.matB, c.e1, c.e2)
	return c
}

// argsCriarRegraTeste: uma regra válida (seg a sex, 08–10h, 3/dia, só em E1), na vigência dada.
func argsCriarRegraTeste(c cenarioRegra, ini, fim string, extra map[string]any) map[string]any {
	a := map[string]any{
		"campanha_id": c.camp.String(), "tipo_id": c.tipoA.String(), "emissora_ids": strsDe(c.e1),
		"inicio": ini, "fim": fim, "dias": []any{"seg", "ter", "qua", "qui", "sex"},
		"inicio_horario": "08:00", "fim_horario": "10:00", "insercoes_por_dia": 3, "nome": "Manhã",
	}
	for k, v := range extra {
		if v == nil {
			delete(a, k)
		} else {
			a[k] = v
		}
	}
	return a
}

// seedRegra grava uma regra diretamente (como o banco a teria hoje).
func seedRegra(t *testing.T, b *bancadaTool, c cenarioRegra, tipo uuid.UUID, ini, fim string, mascara int16,
	faixaIni, faixaFim string, porDia int16, nome string, emissoras []uuid.UUID, materiais []uuid.UUID) uuid.UUID {
	t.Helper()
	if materiais == nil {
		materiais = []uuid.UUID{}
	}
	r, err := catalog.NewDistributionRules(b.pool).Create(b.ctx, catalog.CreateDistributionRuleInput{
		CampaignID: c.camp, TypeID: tipo, StationIDs: emissoras, MaterialIDs: materiais, Name: nome,
		StartDate: mcptest.Dia(ini), EndDate: mcptest.Dia(fim), WeekdayMask: mascara,
		TimeStart: faixaIni, TimeEnd: faixaFim, PlaysPerDay: porDia,
	})
	require.NoError(t, err)
	return r.ID
}

// linhasDeRegra é a forma normalizada das regras da campanha (sem ids nem carimbos):
// emissoras e materiais por NOME, o tipo pelo nome — comparável entre duas fixtures.
func linhasDeRegra(t *testing.T, b *bancadaTool, camp uuid.UUID) []string {
	t.Helper()
	rows, err := b.pool.Query(b.ctx, `
		SELECT r.name, t.name, r.weekday_mask, to_char(r.time_start,'HH24:MI'), to_char(r.time_end,'HH24:MI'),
		       r.plays_per_day, r.start_date::text, r.end_date::text,
		       COALESCE((SELECT string_agg(s.name, ',' ORDER BY s.name) FROM stations s WHERE s.id = ANY(r.station_ids)), ''),
		       COALESCE((SELECT string_agg(m.title, ',' ORDER BY m.title) FROM materials m WHERE m.id = ANY(r.material_ids)), '')
		  FROM distribution_rules r JOIN material_types t ON t.id = r.type_id
		 WHERE r.campaign_id = $1 ORDER BY r.name, r.start_date, r.plays_per_day`, camp)
	require.NoError(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var nome, tipo, ini, fim, fi, ff, est, mats string
		var mask, porDia int16
		require.NoError(t, rows.Scan(&nome, &tipo, &mask, &fi, &ff, &porDia, &ini, &fim, &est, &mats))
		out = append(out, fmt.Sprintf("%s|%s|mask=%d|%s-%s|%d/dia|%s..%s|est=%s|mat=%s", nome, tipo, mask, fi, ff, porDia, ini, fim, est, mats))
	}
	require.NoError(t, rows.Err())
	return out
}

func idDaUnicaRegra(t *testing.T, b *bancadaTool, camp uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT id FROM distribution_rules WHERE campaign_id = $1`, camp).Scan(&id))
	return id
}

// categoriasDasTocadas: a categoria da projeção e da tocada-base, por horário.
func categoriasDasTocadas(t *testing.T, b *bancadaTool, camp uuid.UUID) string {
	t.Helper()
	rows, err := b.pool.Query(b.ctx, `
		SELECT to_char(dc.detected_at AT TIME ZONE 'America/Sao_Paulo','YYYY-MM-DD HH24:MI'), dc.category, d.category
		  FROM detection_campaigns dc JOIN detections d ON d.id = dc.detection_id AND d.detected_at = dc.detected_at
		 WHERE dc.campaign_id = $1 ORDER BY dc.detected_at`, camp)
	require.NoError(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var quando, proj, base string
		require.NoError(t, rows.Scan(&quando, &proj, &base))
		out = append(out, quando+"="+proj+"/"+base)
	}
	return strings.Join(out, ";")
}

// semearTocadasDeQuarta: quartas de out/2026 (14 e 21), todas gravadas como bonus.
func semearTocadasDeQuarta(t *testing.T, b *bancadaTool, c cenarioRegra) {
	t.Helper()
	for _, q := range [][2]string{{"2026-10-14", "10:00"}, {"2026-10-14", "11:00"}, {"2026-10-14", "15:00"}, {"2026-10-21", "15:00"}} {
		mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, c.mat, c.e1, "bonus", mcptest.Quando(q[0], q[1]))
	}
}

func regraDoResultado(out map[string]any) map[string]any {
	return out["resultado"].(map[string]any)["regra"].(map[string]any)
}

func entradaDe[T any](t *testing.T, m map[string]any) T {
	t.Helper()
	bruto, err := json.Marshal(m)
	require.NoError(t, err)
	var in T
	require.NoError(t, json.Unmarshal(bruto, &in))
	return in
}

func handlerRegras(b *bancadaTool) *handlers.DistributionRulesHandler {
	return &handlers.DistributionRulesHandler{
		Repo: catalog.NewDistributionRules(b.pool), CampaignRepo: catalog.NewCampaigns(b.pool), EmSegundoPlano: mcptest.Sincrono,
	}
}

// corpoRegra: o corpo JSON da rota para os mesmos valores da regra de argsCriarRegraTeste.
func corpoRegra(c cenarioRegra, ini, fim string, extra map[string]any) map[string]any {
	a := map[string]any{
		"type_id": c.tipoA.String(), "station_ids": strsDe(c.e1), "material_ids": []any{}, "name": "Manhã",
		"start_date": ini, "end_date": fim, "weekday_mask": 62, "time_start": "08:00", "time_end": "10:00", "plays_per_day": 3,
	}
	for k, v := range extra {
		a[k] = v
	}
	return a
}

// ── dias da semana ↔ máscara ────────────────────────────────────────────

func TestMascaraDosDias_BateComAOrdemDaTelaDoBancoEDoCategorizador(t *testing.T) {
	// Os valores que a TELA usa (DistributionStep.jsx formatWeekdays, RuleSidePanel.jsx default).
	casos := map[string]struct {
		dias []string
		mask int16
	}{
		"seg a sex (default da tela)": {[]string{"seg", "ter", "qua", "qui", "sex"}, 62},
		"fim de semana":               {[]string{"sab", "dom"}, 65},
		"todos":                       {[]string{"dom", "seg", "ter", "qua", "qui", "sex", "sab"}, 127},
		"só domingo":                  {[]string{"dom"}, 1},
		"só sábado":                   {[]string{"sab"}, 64},
		"sáb com acento e caixa":      {[]string{" SÁB "}, 64},
		"repetido":                    {[]string{"seg", "seg"}, 2},
	}
	for nome, c := range casos {
		m, err := mascaraDosDias(c.dias)
		require.NoError(t, err, nome)
		require.Equal(t, c.mask, m, nome)
	}
	for _, ruim := range [][]string{nil, {}, {"segunda"}, {"xyz"}, {""}, {"seg", "feriado"}} {
		_, err := mascaraDosDias(ruim)
		require.Error(t, err, "%v", ruim)
	}

	// Contra o categorizador (time.Weekday) e contra o Postgres (EXTRACT(DOW), a
	// expressão da daily_play_summary): domingo = bit 0 … sábado = bit 6.
	_, pool := mcptest.DBCatalogo(t)
	domingo := mcptest.Dia("2026-10-25")
	for i, nome := range []string{"dom", "seg", "ter", "qua", "qui", "sex", "sab"} {
		m, err := mascaraDosDias([]string{nome})
		require.NoError(t, err)
		require.Equal(t, int16(1<<i), m, nome)
		for j := 0; j < 7; j++ {
			d := domingo.AddDate(0, 0, j)
			quer := j == i
			require.Equal(t, quer, categorizer.RuleCoversDay(categorizer.Rule{StartDate: domingo, EndDate: domingo.AddDate(0, 0, 6), WeekdayMask: m}, d),
				"%s no categorizador, dia %s", nome, d.Weekday())
			var noBanco bool
			require.NoError(t, pool.QueryRow(t.Context(), `SELECT ((1 << EXTRACT(DOW FROM $1::date)::int) & $2::int) != 0`, d, m).Scan(&noBanco))
			require.Equal(t, quer, noBanco, "%s no Postgres, dia %s", nome, d.Weekday())
		}
	}
}

// Trava de deriva: a ordem dos bits está escrita no painel de regras da tela. Se
// alguém mudá-la, o MCP passa a gravar a máscara errada em silêncio.
func TestMascaraDosDias_OrdemEscritaNoPainelDeRegrasDaTela(t *testing.T) {
	bruto, err := os.ReadFile("../../../frontend/src/components/RuleSidePanel.jsx")
	if err != nil {
		t.Skip("o frontend não está na árvore deste teste")
	}
	src := string(bruto)
	require.Contains(t, src, "const WEEKDAY_NAMES = ['D','S','T','Q','Q','S','S']", "domingo primeiro")
	require.Contains(t, src, "(weekdayMask & (1 << i)) !== 0", "o bit i é o i-ésimo dia, a partir de domingo")
	require.Contains(t, src, "initial?.weekday_mask ?? 62", "default seg a sex = 62")
}

func TestFaixaDaEntrada(t *testing.T) {
	i, f, err := faixaDaEntrada(" 08:00 ", "23:59")
	require.NoError(t, err)
	require.Equal(t, "08:00", i)
	require.Equal(t, "23:59", f)
	for nome, par := range map[string][2]string{
		"24:00":               {"08:00", "24:00"},
		"sem zero":            {"8:00", "10:00"},
		"com segundos":        {"08:00:00", "10:00"},
		"minuto inválido":     {"08:60", "10:00"},
		"fim igual ao início": {"08:00", "08:00"},
		"fim antes":           {"10:00", "08:00"},
		"vazio":               {"", "10:00"},
		"atravessa a noite":   {"22:00", "02:00"},
	} {
		_, _, err := faixaDaEntrada(par[0], par[1])
		require.Error(t, err, nome)
		require.Contains(t, err.Error(), "[entrada_invalida]", nome)
	}
}

// ── criar_regra_distribuicao ────────────────────────────────────────────

func TestCriarRegra_PreviewNaoEscreveEConfirmarGrava(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-12-01", "2026-12-31")
	require.Contains(t, propriedades(esquemaDa(t, b.cfg, "criar_regra_distribuicao")), "confirmacao")
	args := argsCriarRegraTeste(c, "2026-12-01", "2026-12-31", map[string]any{"emissora_ids": strsDe(c.e1, c.e2), "material_ids": strsDe(c.mat)})

	pre := b.preview(t, "criar_regra_distribuicao", args)

	require.Empty(t, linhasDeRegra(t, b, c.camp), "o preview não escreve")
	require.Zero(t, b.totalAuditorias(t))
	require.Zero(t, tokensUsados(t, b))
	texto := textoDoPreview(t, pre)
	for _, trecho := range []string{"Manhã", "seg, ter, qua, qui, sex", "08:00–10:00", "Rádio 1", "Rádio 2", "Spot Regras", "recategorizadas em segundo plano", "tolerância de 15 min", "Escopo por material"} {
		require.Contains(t, texto, trecho)
	}

	out := b.confirmarEExecutar(t, "criar_regra_distribuicao", args)

	require.Equal(t, []string{"Manhã|" + nomeDoTipo(t, b, c.tipoA) + "|mask=62|08:00-10:00|3/dia|2026-12-01..2026-12-31|est=Rádio 1,Rádio 2|mat=Spot Regras"}, linhasDeRegra(t, b, c.camp))
	id := idDaUnicaRegra(t, b, c.camp)
	r := regraDoResultado(out) // o formato de listar_regras_distribuicao
	require.Equal(t, id.String(), r["id"])
	require.Equal(t, c.tipoA.String(), r["tipo"].(map[string]any)["id"])
	require.EqualValues(t, 2, r["emissoras_total"])
	require.Equal(t, []any{"seg", "ter", "qua", "qui", "sex"}, r["dias_da_semana"])
	require.Equal(t, map[string]any{"inicio": "08:00", "fim": "10:00"}, r["faixa"])
	require.Contains(t, mustJSON(t, out["ressalvas"]), "até 30 s")
	l := b.exigeAuditoria(t, "mcp.escrita.criar_regra_distribuicao", "distribution_rule", &id)
	require.Equal(t, "confirma", l.Meta["nivel"])

	// O resultado é o mesmo item que listar_regras_distribuicao devolve.
	lista := itens(t, b.chamar(t, "listar_regras_distribuicao", map[string]any{"campanha_id": c.camp.String()}))
	require.Len(t, lista, 1)
	require.Equal(t, lista[0], r)
}

func nomeDoTipo(t *testing.T, b *bancadaTool, id uuid.UUID) string {
	t.Helper()
	var n string
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT name FROM material_types WHERE id = $1`, id).Scan(&n))
	return n
}

func TestCriarRegra_ParidadeComARota(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	viaRota := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-12-01", "2026-12-31")
	viaTool := semearRegra(t, b, viaRota.tipoA, viaRota.tipoB, "2026-12-01", "2026-12-31")

	chamarHandlerCorpo(t, handlerRegras(b).Create, http.MethodPost, "/campaigns/x/distribution-rules", corpoRegra(viaRota, "2026-12-01", "2026-12-31", map[string]any{
		"station_ids": strsDe(viaRota.e1, viaRota.e2), "material_ids": strsDe(viaRota.mat),
	}), map[string]string{"campaignID": viaRota.camp.String()}, b.admin)
	b.confirmarEExecutar(t, "criar_regra_distribuicao", argsCriarRegraTeste(viaTool, "2026-12-01", "2026-12-31",
		map[string]any{"emissora_ids": strsDe(viaTool.e1, viaTool.e2), "material_ids": strsDe(viaTool.mat)}))

	rota, tool := linhasDeRegra(t, b, viaRota.camp), linhasDeRegra(t, b, viaTool.camp)
	require.Len(t, rota, 1)
	require.Equal(t, rota, tool, "a mesma regra gravada pela rota e pela tool")
}

// Paridade de EFEITO: o Executar da tool (o mesmo que a Fatia 4 rodará depois da
// aprovação humana) numa campanha COM veiculação — o caso que a tool barra pelo
// protocolo — recategoriza igual à rota. Mesmo seed nas duas fixtures.
func TestCriarRegra_ParidadeDeEfeitoNaRecategorizacao(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	viaRota := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-10-01", "2026-10-31")
	viaTool := semearRegra(t, b, viaRota.tipoA, viaRota.tipoB, "2026-10-01", "2026-10-31")
	semearTocadasDeQuarta(t, b, viaRota)
	semearTocadasDeQuarta(t, b, viaTool)

	chamarHandlerCorpo(t, handlerRegras(b).Create, http.MethodPost, "/campaigns/x/distribution-rules", corpoRegra(viaRota, "2026-10-01", "2026-10-31", map[string]any{
		"weekday_mask": 8, "time_start": "08:00", "time_end": "12:00", "plays_per_day": 1,
	}), map[string]string{"campaignID": viaRota.camp.String()}, b.admin)
	in := entradaDe[entradaCriarRegra](t, argsCriarRegraTeste(viaTool, "2026-10-01", "2026-10-31", map[string]any{
		"dias": []any{"qua"}, "inicio_horario": "08:00", "fim_horario": "12:00", "insercoes_por_dia": 1,
	}))
	f, err := escritaCriarRegra(b.cfg).Executar(b.ctx, b.E, b.p, in)
	require.NoError(t, err)
	require.NotNil(t, f.RecursoID)

	rota, tool := categoriasDasTocadas(t, b, viaRota.camp), categoriasDasTocadas(t, b, viaTool.camp)
	require.Equal(t, rota, tool, "a mesma categoria depois do recat")
	require.Contains(t, rota, "10:00=in_slot/in_slot", "a prova não é vazia: a regra mudou categoria")
	require.Contains(t, rota, "2026-10-21 15:00=out_slot/out_slot")
	require.Equal(t, linhasDeRegra(t, b, viaRota.camp), linhasDeRegra(t, b, viaTool.camp))
}

func TestCriarRegra_Recusas(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-12-01", "2026-12-31")
	canc := semearRegra(t, b, c.tipoA, c.tipoB, "2026-12-01", "2026-12-31")
	mcptest.EstadoCampanha(t, b.ctx, b.pool, canc.camp, "cancelada")
	matSemTipo := mcptest.Material(t, b.ctx, b.pool, c.cli, c.tipoA, "Sem tipo")
	_, err := b.pool.Exec(b.ctx, `UPDATE materials SET type_id = NULL WHERE id = $1`, matSemTipo)
	require.NoError(t, err)
	mcptest.Vincular(t, b.ctx, b.pool, c.camp, matSemTipo, c.e1)
	muitas := make([]any, 501)
	for i := range muitas {
		muitas[i] = uuid.NewString()
	}
	muitosMats := make([]any, 201)
	for i := range muitosMats {
		muitosMats[i] = uuid.NewString()
	}
	ini, fim := "2026-12-01", "2026-12-31"
	base := func(extra map[string]any) map[string]any { return argsCriarRegraTeste(c, ini, fim, extra) }

	for nome, caso := range map[string]struct {
		args   map[string]any
		codigo string
		trecho string
	}{
		"campanha inválida":           {base(map[string]any{"campanha_id": "x"}), "[entrada_invalida]", ""},
		"campanha inexistente":        {base(map[string]any{"campanha_id": uuid.NewString()}), "[nao_encontrado]", ""},
		"campanha cancelada":          {argsCriarRegraTeste(canc, ini, fim, nil), "[campanha_cancelada]", ""},
		"tipo inválido":               {base(map[string]any{"tipo_id": "x"}), "[entrada_invalida]", ""},
		"tipo inexistente":            {base(map[string]any{"tipo_id": uuid.NewString()}), "[nao_encontrado]", "tipo de material"},
		"sem emissoras":               {base(map[string]any{"emissora_ids": []any{}}), "[entrada_invalida]", "ao menos uma emissora"},
		"emissora fora da campanha":   {base(map[string]any{"emissora_ids": strsDe(c.e3)}), "[entrada_invalida]", "fora da campanha"},
		"emissora inválida":           {base(map[string]any{"emissora_ids": []any{"x"}}), "[entrada_invalida]", ""},
		"mais de 500 emissoras":       {base(map[string]any{"emissora_ids": muitas}), "[entrada_invalida]", "500"},
		"mais de 200 materiais":       {base(map[string]any{"material_ids": muitosMats}), "[entrada_invalida]", "200"},
		"material não vinculado":      {base(map[string]any{"material_ids": strsDe(c.solto)}), "[entrada_invalida]", "não está vinculado"},
		"material de outro tipo":      {base(map[string]any{"material_ids": strsDe(c.matB)}), "[entrada_invalida]", "não do tipo da regra"},
		"material sem tipo":           {base(map[string]any{"material_ids": strsDe(matSemTipo)}), "[entrada_invalida]", "sem tipo"},
		"material inexistente":        {base(map[string]any{"material_ids": strsDe(uuid.New())}), "[nao_encontrado]", "material"},
		"material inválido":           {base(map[string]any{"material_ids": []any{"x"}}), "[entrada_invalida]", ""},
		"nenhum dia":                  {base(map[string]any{"dias": []any{}}), "[entrada_invalida]", "ao menos um dia"},
		"dia inválido":                {base(map[string]any{"dias": []any{"segunda"}}), "[entrada_invalida]", "segunda"},
		"início inválido":             {base(map[string]any{"inicio": "01/12/2026"}), "[entrada_invalida]", ""},
		"fim antes do início":         {base(map[string]any{"inicio": "2026-12-31", "fim": "2026-12-01"}), "[entrada_invalida]", "anterior"},
		"início antes da campanha":    {base(map[string]any{"inicio": "2026-11-30"}), "[entrada_invalida]", "sai do período da campanha"},
		"fim depois da campanha":      {base(map[string]any{"fim": "2027-01-01"}), "[entrada_invalida]", "sai do período da campanha"},
		"data fora de 2000-2100":      {base(map[string]any{"fim": "2162-12-31"}), "[entrada_invalida]", ""},
		"início do horário em branco": {base(map[string]any{"inicio_horario": ""}), "[entrada_invalida]", "HH:MM"},
		"fim 24:00":                   {base(map[string]any{"fim_horario": "24:00"}), "[entrada_invalida]", "24:00 não existe"},
		"horário H:MM":                {base(map[string]any{"inicio_horario": "8:00"}), "[entrada_invalida]", "HH:MM"},
		"fim do horário não maior":    {base(map[string]any{"inicio_horario": "10:00", "fim_horario": "10:00"}), "[entrada_invalida]", "maior que o início"},
		"zero inserções":              {base(map[string]any{"insercoes_por_dia": 0}), "[entrada_invalida]", "1 a 100"},
		"mais de 100 inserções":       {base(map[string]any{"insercoes_por_dia": 101}), "[entrada_invalida]", "1 a 100"},
		"nome com mais de 60 runas":   {base(map[string]any{"nome": strings.Repeat("ã", 61)}), "[entrada_invalida]", "60"},
	} {
		texto := b.chamarErro(t, "criar_regra_distribuicao", caso.args)
		require.Contains(t, texto, caso.codigo, nome)
		require.Contains(t, texto, caso.trecho, nome)
	}
	require.Empty(t, linhasDeRegra(t, b, c.camp))
	require.Empty(t, linhasDeRegra(t, b, canc.camp))
	require.Zero(t, b.totalAuditorias(t))
	require.Zero(t, tokensUsados(t, b))
}

// Faixa 23:59 e nome de 60 runas são os extremos aceitos.
func TestCriarRegra_ExtremosAceitos(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-12-01", "2026-12-31")
	b.confirmarEExecutar(t, "criar_regra_distribuicao", argsCriarRegraTeste(c, "2026-12-01", "2026-12-31", map[string]any{
		"inicio_horario": "00:00", "fim_horario": "23:59", "insercoes_por_dia": 100, "nome": strings.Repeat("ã", 60), "dias": []any{"dom"},
	}))
	require.Len(t, linhasDeRegra(t, b, c.camp), 1)
}

func TestCriarRegra_RegraExistenteDaMesmaCelulaSeSoma(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-12-01", "2026-12-31")
	seedRegra(t, b, c, c.tipoA, "2026-12-01", "2026-12-31", 62, "07:00", "09:00", 2, "Existente", []uuid.UUID{c.e1}, nil)

	pre := b.preview(t, "criar_regra_distribuicao", argsCriarRegraTeste(c, "2026-12-01", "2026-12-31", nil))
	r := mustJSON(t, pre["preview"].(map[string]any)["ressalvas"])
	require.Contains(t, r, "se SOMA")
	require.Contains(t, r, "Existente")
	require.Contains(t, r, "editar_regra_distribuicao")

	// Outro tipo, outra emissora ou outro dia da semana: não se soma.
	for nome, extra := range map[string]map[string]any{
		"outra emissora": {"emissora_ids": strsDe(c.e2)},
		"outro tipo":     {"tipo_id": c.tipoB.String()},
		"fim de semana":  {"dias": []any{"sab", "dom"}},
	} {
		pre = b.preview(t, "criar_regra_distribuicao", argsCriarRegraTeste(c, "2026-12-01", "2026-12-31", extra))
		require.NotContains(t, mustJSON(t, pre["preview"].(map[string]any)["ressalvas"]), "se SOMA", nome)
	}
}

// ── escalada de criar ───────────────────────────────────────────────────

func TestCriarRegra_Escalada(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b) // hoje = 2026-10-29 (SP)
	// A campanha vai de 01/10 a 31/12: já passaram 28 dias.
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-10-01", "2026-12-31")
	futura := func(extra map[string]any) map[string]any {
		return argsCriarRegraTeste(c, "2026-11-01", "2026-12-31", extra)
	}

	// Vizinho 1: vigência só no futuro e nenhuma veiculação → confirma.
	pre := b.chamar(t, "criar_regra_distribuicao", futura(nil))
	require.NotEmpty(t, pre["confirmacao"])
	require.NotContains(t, pre, "nivel")

	// Vizinho 2: veiculação só de OUTRO tipo (B) → criar regra do tipo A não escala por veiculação.
	mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, c.matB, c.e1, "bonus", mcptest.Quando("2026-10-10", "10:00"))
	pre = b.chamar(t, "criar_regra_distribuicao", futura(nil))
	require.NotEmpty(t, pre["confirmacao"], "veiculação de outro tipo não escala")

	// Gatilho 1: veiculação aprovada do MESMO tipo, mesmo com a vigência só no futuro.
	mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, c.mat, c.e2, "bonus", mcptest.Quando("2026-10-11", "10:00"))
	out := b.pedePendente(t, "criar_regra_distribuicao", futura(nil))
	require.Contains(t, out["motivo"], "veiculação aprovada do tipo")
	require.NotContains(t, out["motivo"], "dias já passados")
	require.Equal(t, "https://portal.teste/campaigns/"+c.camp.String()+"/edit", out["links"].(map[string]any)["tela"])
	require.Contains(t, textoDoPreview(t, out), "Manhã", "o pedido pendente traz o preview")
	require.Empty(t, linhasDeRegra(t, b, c.camp), "nada foi criado")
	l := b.auditoria(t, "mcp.pendente.criada")
	require.Len(t, l, 1)
	require.Equal(t, "distribution_rule", l[0].Recurso)
	require.Empty(t, b.auditoria(t, "mcp.escrita.criar_regra_distribuicao"))
	require.Zero(t, tokensUsados(t, b))
}

func TestCriarRegra_EscaladaPelaDataSemNenhumaVeiculacao(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-10-01", "2026-12-31")

	// Vigência que começa ontem: um dia já passou. Sem tocada nenhuma, mesmo assim barra.
	out := b.pedePendente(t, "criar_regra_distribuicao", argsCriarRegraTeste(c, "2026-10-28", "2026-12-31", nil))
	require.Contains(t, out["motivo"], "dias já passados")
	require.Contains(t, out["motivo"], "2026-10-28 a 2026-10-28")
	require.NotContains(t, out["motivo"], "veiculação aprovada")

	// Vizinho: a vigência começa HOJE (29/10): nenhum dia passado → confirma.
	pre := b.chamar(t, "criar_regra_distribuicao", argsCriarRegraTeste(c, "2026-10-29", "2026-12-31", nil))
	require.NotEmpty(t, pre["confirmacao"])
	require.Empty(t, linhasDeRegra(t, b, c.camp))
}

// F3-2: o preview é confirma (futuro, sem tocada); entra uma tocada do tipo antes da
// confirmação — a confirmação barra e o token não é consumido.
func TestCriarRegra_EscaladaNaConfirmacaoBarraENaoConsomeOToken(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-10-01", "2026-12-31")
	args := argsCriarRegraTeste(c, "2026-11-01", "2026-12-31", nil)
	pre := b.preview(t, "criar_regra_distribuicao", args)

	mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, c.mat, c.e1, "bonus", mcptest.Quando("2026-10-10", "10:00"))
	args["confirmacao"] = pre["confirmacao"]
	out := b.chamar(t, "criar_regra_distribuicao", args)

	require.Equal(t, "critico", out["nivel"])
	require.Empty(t, linhasDeRegra(t, b, c.camp))
	require.Zero(t, tokensUsados(t, b))
}

// Fronteira do dia em SP: às 22h e às 23h30 de SP já é o dia seguinte em UTC.
func TestCriarRegra_FronteiraDoDiaEmSP(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-10-01", "2026-12-31")
	for _, hora := range []string{"22:00", "23:30"} {
		b.E.Agora = func() time.Time { return mcptest.Quando("2026-10-29", hora) }
		require.Equal(t, 30, b.E.Agora().UTC().Day(), "%s em SP já é dia 30 em UTC", hora)

		// Começa "hoje em SP" (29/10): nenhum dia passado — NÃO escala.
		pre := b.chamar(t, "criar_regra_distribuicao", argsCriarRegraTeste(c, "2026-10-29", "2026-12-31", nil))
		require.NotEmpty(t, pre["confirmacao"], "início hoje (SP) às %s não escala", hora)
		// Começou ontem em SP (28/10): já há dia passado — escala.
		b.pedePendente(t, "criar_regra_distribuicao", argsCriarRegraTeste(c, "2026-10-28", "2026-12-31", nil))
		// Começa amanhã em SP (30/10), que já é "hoje" em UTC: NÃO escala.
		pre = b.chamar(t, "criar_regra_distribuicao", argsCriarRegraTeste(c, "2026-10-30", "2026-12-31", nil))
		require.NotEmpty(t, pre["confirmacao"], "início amanhã (SP) às %s não escala, mesmo sendo hoje em UTC", hora)
	}
}

// ── editar_regra_distribuicao ───────────────────────────────────────────

// semearRegraBase: regra de E1 e E2, tipo A, seg a sex, 08–12h, 2/dia, nome "Base", na vigência dada.
func semearRegraBase(t *testing.T, b *bancadaTool, c cenarioRegra, ini, fim string) uuid.UUID {
	t.Helper()
	return seedRegra(t, b, c, c.tipoA, ini, fim, 62, "08:00", "12:00", 2, "Base", []uuid.UUID{c.e1, c.e2}, nil)
}

func argsEditarRegra(c cenarioRegra, regra uuid.UUID, extra map[string]any) map[string]any {
	a := map[string]any{"campanha_id": c.camp.String(), "regra_id": regra.String()}
	for k, v := range extra {
		a[k] = v
	}
	return a
}

func TestEditarRegra_ParcialPreservaOResto(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-12-01", "2026-12-31")
	r := seedRegra(t, b, c, c.tipoA, "2026-12-01", "2026-12-31", 62, "08:00", "12:00", 2, "Base", []uuid.UUID{c.e1, c.e2}, []uuid.UUID{c.mat})
	require.Contains(t, propriedades(esquemaDa(t, b.cfg, "editar_regra_distribuicao")), "confirmacao")
	tipo := nomeDoTipo(t, b, c.tipoA)
	antes := linhasDeRegra(t, b, c.camp)

	pre := b.preview(t, "editar_regra_distribuicao", argsEditarRegra(c, r, map[string]any{"insercoes_por_dia": 5}))

	require.Equal(t, antes, linhasDeRegra(t, b, c.camp), "o preview não escreve")
	require.Zero(t, b.totalAuditorias(t))
	mud := pre["preview"].(map[string]any)["mudancas"].([]any)
	require.Len(t, mud, 1, "só o campo enviado muda")
	require.Equal(t, map[string]any{"campo": "inserções por dia", "de": float64(2), "para": float64(5)}, mud[0])

	out := b.confirmarEExecutar(t, "editar_regra_distribuicao", argsEditarRegra(c, r, map[string]any{"insercoes_por_dia": 5}))

	require.Equal(t, []string{"Base|" + tipo + "|mask=62|08:00-12:00|5/dia|2026-12-01..2026-12-31|est=Rádio 1,Rádio 2|mat=Spot Regras"}, linhasDeRegra(t, b, c.camp),
		"nome, emissoras, material, dias, faixa e vigência ficaram")
	require.EqualValues(t, 5, regraDoResultado(out)["insercoes_por_dia"])
	require.Contains(t, mustJSON(t, out["ressalvas"]), "até 30 s")
	b.exigeAuditoria(t, "mcp.escrita.editar_regra_distribuicao", "distribution_rule", &r)

	// Vários campos de uma vez: dias (a lista inteira), faixa só no fim, nome apagado.
	b.confirmarEExecutar(t, "editar_regra_distribuicao", argsEditarRegra(c, r, map[string]any{
		"dias": []any{"sab", "dom"}, "fim_horario": "13:30", "nome": "",
	}))
	require.Equal(t, []string{"|" + tipo + "|mask=65|08:00-13:30|5/dia|2026-12-01..2026-12-31|est=Rádio 1,Rádio 2|mat=Spot Regras"}, linhasDeRegra(t, b, c.camp))
}

func TestEditarRegra_EmissorasEMateriais(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-12-01", "2026-12-31")
	outroMat := mcptest.Material(t, b.ctx, b.pool, c.cli, c.tipoA, "Spot Dois")
	mcptest.Vincular(t, b.ctx, b.pool, c.camp, outroMat, c.e1)
	r := seedRegra(t, b, c, c.tipoA, "2026-12-01", "2026-12-31", 62, "08:00", "12:00", 2, "Base", []uuid.UUID{c.e1}, nil)
	tipo := nomeDoTipo(t, b, c.tipoA)
	linha := func(est, mat string) string {
		return "Base|" + tipo + "|mask=62|08:00-12:00|2/dia|2026-12-01..2026-12-31|est=" + est + "|mat=" + mat
	}

	// Acrescenta E2 e passa a regra de GERAL a específica (com ressalva).
	pre := b.preview(t, "editar_regra_distribuicao", argsEditarRegra(c, r, map[string]any{"adicionar_emissoras": strsDe(c.e2), "adicionar_materiais": strsDe(c.mat)}))
	require.Contains(t, mustJSON(t, pre["preview"].(map[string]any)["ressalvas"]), "de GERAL a específica")
	b.confirmarEExecutar(t, "editar_regra_distribuicao", argsEditarRegra(c, r, map[string]any{"adicionar_emissoras": strsDe(c.e2), "adicionar_materiais": strsDe(c.mat)}))
	require.Equal(t, []string{linha("Rádio 1,Rádio 2", "Spot Regras")}, linhasDeRegra(t, b, c.camp))

	// Tira E1 e acrescenta outro material.
	b.confirmarEExecutar(t, "editar_regra_distribuicao", argsEditarRegra(c, r, map[string]any{"remover_emissoras": strsDe(c.e1), "adicionar_materiais": strsDe(outroMat)}))
	require.Equal(t, []string{linha("Rádio 2", "Spot Dois,Spot Regras")}, linhasDeRegra(t, b, c.camp))

	// Volta a regra a geral.
	b.confirmarEExecutar(t, "editar_regra_distribuicao", argsEditarRegra(c, r, map[string]any{"todos_os_materiais_do_tipo": true}))
	require.Equal(t, []string{linha("Rádio 2", "")}, linhasDeRegra(t, b, c.camp))
}

func TestEditarRegra_ParidadeComARota(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	viaRota := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-12-01", "2026-12-31")
	viaTool := semearRegra(t, b, viaRota.tipoA, viaRota.tipoB, "2026-12-01", "2026-12-31")
	rRota := semearRegraBase(t, b, viaRota, "2026-12-01", "2026-12-31")
	rTool := semearRegraBase(t, b, viaTool, "2026-12-01", "2026-12-31")

	// A tela manda a regra INTEIRA; a tool só o que muda.
	chamarHandlerCorpo(t, handlerRegras(b).Update, http.MethodPut, "/campaigns/x/distribution-rules/y", corpoRegra(viaRota, "2026-12-01", "2026-12-31", map[string]any{
		"station_ids": strsDe(viaRota.e1, viaRota.e2), "name": "Base", "weekday_mask": 62, "time_start": "09:00", "time_end": "12:00", "plays_per_day": 4,
	}), map[string]string{"campaignID": viaRota.camp.String(), "ruleID": rRota.String()}, b.admin)
	b.confirmarEExecutar(t, "editar_regra_distribuicao", argsEditarRegra(viaTool, rTool, map[string]any{"inicio_horario": "09:00", "insercoes_por_dia": 4}))

	rota, tool := linhasDeRegra(t, b, viaRota.camp), linhasDeRegra(t, b, viaTool.camp)
	require.Contains(t, rota[0], "09:00-12:00|4/dia")
	require.Equal(t, rota, tool)
}

// Paridade de EFEITO da edição, inclusive a troca de tipo (o tipo ANTIGO também é
// recategorizado — F3-8b): o Executar da tool numa campanha com veiculação.
func TestEditarRegra_ParidadeDeEfeitoNaRecategorizacao(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	viaRota := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-10-01", "2026-10-31")
	viaTool := semearRegra(t, b, viaRota.tipoA, viaRota.tipoB, "2026-10-01", "2026-10-31")
	var regras [2]uuid.UUID
	for i, c := range []cenarioRegra{viaRota, viaTool} {
		semearTocadasDeQuarta(t, b, c)
		regras[i] = seedRegra(t, b, c, c.tipoA, "2026-10-01", "2026-10-31", 8, "08:00", "12:00", 1, "Quartas", []uuid.UUID{c.e1}, nil)
		// O seed por SQL não recategoriza: fecha as células como o gatilho faria.
		require.NoError(t, catalog.NewDistributionRules(b.pool).RecategorizeForRule(b.ctx, regras[i]))
	}
	require.Equal(t, categoriasDasTocadas(t, b, viaRota.camp), categoriasDasTocadas(t, b, viaTool.camp))
	require.Contains(t, categoriasDasTocadas(t, b, viaRota.camp), "10:00=in_slot/in_slot")

	// Troca o tipo da regra de A para B (as tocadas são do tipo A): a meta de A some e as
	// tocadas de A voltam a ser bonus — só acontece se o tipo ANTIGO for recategorizado.
	chamarHandlerCorpo(t, handlerRegras(b).Update, http.MethodPut, "/campaigns/x/distribution-rules/y", corpoRegra(viaRota, "2026-10-01", "2026-10-31", map[string]any{
		"type_id": viaRota.tipoB.String(), "name": "Quartas", "weekday_mask": 8, "time_start": "08:00", "time_end": "12:00", "plays_per_day": 1,
	}), map[string]string{"campaignID": viaRota.camp.String(), "ruleID": regras[0].String()}, b.admin)
	in := entradaDe[entradaEditarRegra](t, argsEditarRegra(viaTool, regras[1], map[string]any{"tipo_id": viaTool.tipoB.String()}))
	_, err := escritaEditarRegra(b.cfg).Executar(b.ctx, b.E, b.p, in)
	require.NoError(t, err)

	rota, tool := categoriasDasTocadas(t, b, viaRota.camp), categoriasDasTocadas(t, b, viaTool.camp)
	require.Equal(t, rota, tool)
	require.NotContains(t, rota, "in_slot", "o tipo antigo foi recategorizado: nenhuma tocada de A segue in_slot")
	require.Equal(t, linhasDeRegra(t, b, viaRota.camp), linhasDeRegra(t, b, viaTool.camp))
}

func TestEditarRegra_Recusas(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-12-01", "2026-12-31")
	r := semearRegraBase(t, b, c, "2026-12-01", "2026-12-31")
	especifica := seedRegra(t, b, c, c.tipoA, "2026-12-01", "2026-12-31", 62, "14:00", "16:00", 1, "Específica", []uuid.UUID{c.e1}, []uuid.UUID{c.mat})
	umaEmissora := seedRegra(t, b, c, c.tipoA, "2026-12-01", "2026-12-31", 62, "18:00", "19:00", 1, "Uma", []uuid.UUID{c.e1}, nil)
	outra := semearRegra(t, b, c.tipoA, c.tipoB, "2026-12-01", "2026-12-31")
	rOutra := semearRegraBase(t, b, outra, "2026-12-01", "2026-12-31")
	canc := semearRegra(t, b, c.tipoA, c.tipoB, "2026-12-01", "2026-12-31")
	rCanc := semearRegraBase(t, b, canc, "2026-12-01", "2026-12-31")
	mcptest.EstadoCampanha(t, b.ctx, b.pool, canc.camp, "cancelada")
	// Regra "legada" com a emissora E3, que não é da campanha.
	legada := seedRegra(t, b, c, c.tipoA, "2026-12-01", "2026-12-31", 62, "20:00", "21:00", 1, "Legada", []uuid.UUID{c.e1, c.e3}, nil)
	ed := func(regra uuid.UUID, extra map[string]any) map[string]any { return argsEditarRegra(c, regra, extra) }

	for nome, caso := range map[string]struct {
		args   map[string]any
		codigo string
		trecho string
	}{
		"nada a alterar (nenhum campo)":         {ed(r, nil), "[entrada_invalida]", "ao menos um campo"},
		"nada a alterar (mesmos valores)":       {ed(r, map[string]any{"insercoes_por_dia": 2, "nome": "Base"}), "[entrada_invalida]", "nada a alterar"},
		"regra inexistente":                     {ed(uuid.New(), map[string]any{"insercoes_por_dia": 3}), "[nao_encontrado]", "regra"},
		"regra inválida":                        {map[string]any{"campanha_id": c.camp.String(), "regra_id": "x", "insercoes_por_dia": 3}, "[entrada_invalida]", ""},
		"regra de outra campanha":               {ed(rOutra, map[string]any{"insercoes_por_dia": 3}), "[nao_encontrado]", "regra"},
		"campanha cancelada":                    {argsEditarRegra(canc, rCanc, map[string]any{"insercoes_por_dia": 3}), "[campanha_cancelada]", ""},
		"campanha inexistente":                  {map[string]any{"campanha_id": uuid.NewString(), "regra_id": r.String(), "insercoes_por_dia": 3}, "[nao_encontrado]", ""},
		"tipo inexistente":                      {ed(r, map[string]any{"tipo_id": uuid.NewString()}), "[nao_encontrado]", "tipo de material"},
		"zero inserções":                        {ed(r, map[string]any{"insercoes_por_dia": 0}), "[entrada_invalida]", "1 a 100"},
		"101 inserções":                         {ed(r, map[string]any{"insercoes_por_dia": 101}), "[entrada_invalida]", "1 a 100"},
		"dias vazio":                            {ed(r, map[string]any{"dias": []any{}}), "[entrada_invalida]", "ao menos um dia"},
		"início do horário depois do fim":       {ed(r, map[string]any{"inicio_horario": "12:00"}), "[entrada_invalida]", "maior que o início"},
		"fim 24:00":                             {ed(r, map[string]any{"fim_horario": "24:00"}), "[entrada_invalida]", "24:00 não existe"},
		"vigência fora da campanha":             {ed(r, map[string]any{"fim": "2027-01-15"}), "[entrada_invalida]", "sai do período da campanha"},
		"fim antes do início":                   {ed(r, map[string]any{"fim": "2026-11-15"}), "[entrada_invalida]", "anterior à inicial"},
		"tirar a última emissora":               {ed(umaEmissora, map[string]any{"remover_emissoras": strsDe(c.e1)}), "[entrada_invalida]", "sem nenhuma emissora"},
		"acrescentar emissora que já está":      {ed(r, map[string]any{"adicionar_emissoras": strsDe(c.e1)}), "[entrada_invalida]", "já está na regra"},
		"acrescentar emissora fora":             {ed(r, map[string]any{"adicionar_emissoras": strsDe(c.e3)}), "[entrada_invalida]", "fora da campanha"},
		"remover emissora que não está":         {ed(umaEmissora, map[string]any{"remover_emissoras": strsDe(c.e2)}), "[entrada_invalida]", "não há o que remover"},
		"mesma emissora nas duas listas":        {ed(r, map[string]any{"adicionar_emissoras": strsDe(c.e3), "remover_emissoras": strsDe(c.e3)}), "[entrada_invalida]", "ao mesmo tempo"},
		"material não vinculado":                {ed(r, map[string]any{"adicionar_materiais": strsDe(c.solto)}), "[entrada_invalida]", "não está vinculado"},
		"material de outro tipo":                {ed(r, map[string]any{"adicionar_materiais": strsDe(c.matB)}), "[entrada_invalida]", "não do tipo da regra"},
		"tirar o último material":               {ed(especifica, map[string]any{"remover_materiais": strsDe(c.mat)}), "[entrada_invalida]", "todos_os_materiais_do_tipo"},
		"remover material que não está":         {ed(especifica, map[string]any{"remover_materiais": strsDe(c.solto)}), "[entrada_invalida]", "não há o que remover"},
		"todos + adicionar":                     {ed(especifica, map[string]any{"todos_os_materiais_do_tipo": true, "adicionar_materiais": strsDe(c.mat)}), "[entrada_invalida]", "não combina"},
		"todos numa regra já geral":             {ed(r, map[string]any{"todos_os_materiais_do_tipo": true}), "[entrada_invalida]", "já vale para todos"},
		"trocar o tipo com materiais do antigo": {ed(especifica, map[string]any{"tipo_id": c.tipoB.String()}), "[entrada_invalida]", "não do tipo da regra"},
		"mais de 500 emissoras":                 {ed(r, map[string]any{"adicionar_emissoras": muitosIDs(501)}), "[entrada_invalida]", "500"},
		"nome com mais de 60 runas":             {ed(r, map[string]any{"nome": strings.Repeat("ã", 61)}), "[entrada_invalida]", "60"},
	} {
		texto := b.chamarErro(t, "editar_regra_distribuicao", caso.args)
		require.Contains(t, texto, caso.codigo, nome)
		require.Contains(t, texto, caso.trecho, nome)
	}
	require.Equal(t, "Base", strings.Split(linhasDeRegra(t, b, c.camp)[0], "|")[0])
	require.Zero(t, b.totalAuditorias(t))
	require.Zero(t, tokensUsados(t, b))

	// A regra legada com emissora fora da campanha continua editável no que não mexe nessa
	// emissora (a tela faz igual: preserva o escopo gravado), com ressalva; e trocar a faixa não a recusa.
	pre := b.preview(t, "editar_regra_distribuicao", ed(legada, map[string]any{"insercoes_por_dia": 3}))
	require.Contains(t, mustJSON(t, pre["preview"].(map[string]any)["ressalvas"]), "já não estão na campanha")
}

func muitosIDs(n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = uuid.NewString()
	}
	return out
}

// Vigência legada que ultrapassa o período da campanha: editar o que NÃO mexe na
// vigência é aceito (com ressalva), e a escalada olha só o recorte dentro da campanha.
func TestEditarRegra_VigenciaLegadaForaDaCampanha(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b) // hoje = 29/10
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-11-01", "2026-12-31")
	// A regra começa em 01/10, antes da campanha (01/11): o recorte na campanha é só futuro.
	r := seedRegra(t, b, c, c.tipoA, "2026-10-01", "2026-12-31", 62, "08:00", "12:00", 2, "Legada", []uuid.UUID{c.e1}, nil)

	pre := b.chamar(t, "editar_regra_distribuicao", argsEditarRegra(c, r, map[string]any{"insercoes_por_dia": 3}))
	require.NotEmpty(t, pre["confirmacao"], "o recorte na campanha (01/11 em diante) não tem dia passado: não escala")
	require.Contains(t, mustJSON(t, pre["preview"].(map[string]any)["ressalvas"]), "ultrapassa o período da campanha")

	// Mexer na vigência para outra que também sai da campanha é recusado.
	require.Contains(t, b.chamarErro(t, "editar_regra_distribuicao", argsEditarRegra(c, r, map[string]any{"inicio": "2026-10-15"})), "sai do período da campanha")
}

func TestEditarRegra_Escalada(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b) // hoje = 2026-10-29
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-10-01", "2026-12-31")
	futura := semearRegraBase(t, b, c, "2026-11-01", "2026-12-31")                                                                 // só no futuro
	passada := seedRegra(t, b, c, c.tipoA, "2026-10-01", "2026-12-31", 62, "14:00", "16:00", 1, "Passada", []uuid.UUID{c.e1}, nil) // inclui dias passados
	ed := func(regra uuid.UUID, extra map[string]any) map[string]any { return argsEditarRegra(c, regra, extra) }

	// Vizinho: regra só no futuro, sem nenhuma veiculação → confirma (e uma veiculação de outro tipo não muda isso).
	pre := b.chamar(t, "editar_regra_distribuicao", ed(futura, map[string]any{"insercoes_por_dia": 5}))
	require.NotEmpty(t, pre["confirmacao"])
	mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, c.matB, c.e1, "bonus", mcptest.Quando("2026-10-10", "10:00"))
	pre = b.chamar(t, "editar_regra_distribuicao", ed(futura, map[string]any{"insercoes_por_dia": 5}))
	require.NotEmpty(t, pre["confirmacao"], "veiculação só do tipo B: editar a regra do tipo A, sem trocar o tipo, não escala")

	// Gatilho (data): a vigência ATUAL inclui dias passados, mesmo sem veiculação do tipo A.
	out := b.pedePendente(t, "editar_regra_distribuicao", ed(passada, map[string]any{"insercoes_por_dia": 5}))
	require.Contains(t, out["motivo"], "vigência atual")
	require.Contains(t, out["motivo"], "dias já passados")
	require.NotContains(t, out["motivo"], "veiculação aprovada")

	// Gatilho (data): a vigência NOVA passa a incluir dias passados (a antiga era só futuro).
	out = b.pedePendente(t, "editar_regra_distribuicao", ed(futura, map[string]any{"inicio": "2026-10-20"}))
	require.Contains(t, out["motivo"], "vigência nova")
	// ...e a antiga passada encolhida para o futuro também barra, pela ANTIGA.
	out = b.pedePendente(t, "editar_regra_distribuicao", ed(passada, map[string]any{"inicio": "2026-11-15"}))
	require.Contains(t, out["motivo"], "vigência atual")

	// Troca de tipo A → B com veiculação só do tipo B (o NOVO): barra pelo tipo novo.
	out = b.pedePendente(t, "editar_regra_distribuicao", ed(futura, map[string]any{"tipo_id": c.tipoB.String()}))
	require.Contains(t, out["motivo"], "veiculação aprovada")
	require.Contains(t, out["motivo"], "do tipo ")

	// Gatilho (veiculação do tipo da regra): uma tocada do tipo A barra mesmo a regra só-futura.
	mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, c.mat, c.e2, "bonus", mcptest.Quando("2026-10-11", "10:00"))
	out = b.pedePendente(t, "editar_regra_distribuicao", ed(futura, map[string]any{"insercoes_por_dia": 5}))
	require.Contains(t, out["motivo"], "veiculação aprovada do tipo")
	require.Equal(t, "https://portal.teste/campaigns/"+c.camp.String()+"/edit", out["links"].(map[string]any)["tela"])
	require.Equal(t, "Base", strings.Split(linhasDeRegra(t, b, c.camp)[0], "|")[0])
	require.Contains(t, linhasDeRegra(t, b, c.camp)[0], "2/dia", "nada foi gravado")
	require.Zero(t, tokensUsados(t, b))
	l := b.auditoria(t, "mcp.pendente.criada")
	require.NotEmpty(t, l)
	require.Equal(t, "distribution_rule", l[len(l)-1].Recurso)
	require.NotNil(t, l[len(l)-1].RecursoID)
	require.Equal(t, futura, *l[len(l)-1].RecursoID)
}

// Troca de tipo com veiculação só do tipo ANTIGO: escala pelo antigo (o recat dele também roda).
// Com veiculação só de um terceiro tipo, nenhum dos dois tipos da troca tem tocada: não escala.
func TestEditarRegra_EscaladaPeloTipoAntigoETipoNeutro(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-10-01", "2026-12-31")
	r := semearRegraBase(t, b, c, "2026-11-01", "2026-12-31")
	mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, c.mat, c.e1, "bonus", mcptest.Quando("2026-10-10", "10:00")) // tipo A (o antigo)

	out := b.pedePendente(t, "editar_regra_distribuicao", argsEditarRegra(c, r, map[string]any{"tipo_id": c.tipoB.String()}))
	require.Contains(t, out["motivo"], "veiculação aprovada do tipo")
	require.Contains(t, out["motivo"], "ou do tipo", "o texto cita os dois tipos")

	c2 := semearRegra(t, b, c.tipoA, c.tipoB, "2026-10-01", "2026-12-31")
	tipoC := mcptest.TipoMaterial(t, b.ctx, b.pool, "Vinheta")
	matC := mcptest.Material(t, b.ctx, b.pool, c2.cli, tipoC, "Vinheta Regras")
	mcptest.Vincular(t, b.ctx, b.pool, c2.camp, matC, c2.e1)
	mcptest.Veiculacao(t, b.ctx, b.pool, c2.camp, matC, c2.e1, "bonus", mcptest.Quando("2026-10-10", "10:00"))
	r2 := semearRegraBase(t, b, c2, "2026-11-01", "2026-12-31")
	pre := b.chamar(t, "editar_regra_distribuicao", argsEditarRegra(c2, r2, map[string]any{"tipo_id": c2.tipoB.String()}))
	require.NotEmpty(t, pre["confirmacao"], "veiculação de um terceiro tipo não escala")
}

func TestEditarRegra_EscaladaNaConfirmacaoBarraENaoConsomeOToken(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-10-01", "2026-12-31")
	r := semearRegraBase(t, b, c, "2026-11-01", "2026-12-31")
	args := argsEditarRegra(c, r, map[string]any{"insercoes_por_dia": 5})
	pre := b.preview(t, "editar_regra_distribuicao", args)

	mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, c.mat, c.e1, "bonus", mcptest.Quando("2026-10-10", "10:00"))
	args["confirmacao"] = pre["confirmacao"]
	out := b.chamar(t, "editar_regra_distribuicao", args)

	require.Equal(t, "critico", out["nivel"])
	require.Contains(t, linhasDeRegra(t, b, c.camp)[0], "2/dia")
	require.Zero(t, tokensUsados(t, b))
}

func TestEditarRegra_FronteiraDoDiaEmSP(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-10-01", "2026-12-31")
	for _, hora := range []string{"22:00", "23:30"} {
		b.E.Agora = func() time.Time { return mcptest.Quando("2026-10-29", hora) }
		hoje := semearRegraBase(t, b, c, "2026-10-29", "2026-12-31") // começa hoje em SP: nenhum dia passado
		pre := b.chamar(t, "editar_regra_distribuicao", argsEditarRegra(c, hoje, map[string]any{"insercoes_por_dia": 7}))
		require.NotEmpty(t, pre["confirmacao"], "vigência começando hoje (SP) às %s não escala", hora)
		ontem := semearRegraBase(t, b, c, "2026-10-28", "2026-12-31")
		b.pedePendente(t, "editar_regra_distribuicao", argsEditarRegra(c, ontem, map[string]any{"insercoes_por_dia": 7}))
		_, err := b.pool.Exec(b.ctx, `DELETE FROM distribution_rules WHERE campaign_id = $1`, c.camp)
		require.NoError(t, err)
	}
}

// ── apagar_regra_distribuicao ───────────────────────────────────────────

func TestApagarRegra_PreviewNaoEscreveEConfirmarApaga(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-12-01", "2026-12-31")
	r := semearRegraBase(t, b, c, "2026-12-01", "2026-12-31")
	seedRegra(t, b, c, c.tipoA, "2026-12-01", "2026-12-31", 62, "14:00", "16:00", 1, "Fica", []uuid.UUID{c.e1}, nil)
	require.Contains(t, propriedades(esquemaDa(t, b.cfg, "apagar_regra_distribuicao")), "confirmacao")
	args := map[string]any{"campanha_id": c.camp.String(), "regra_id": r.String()}

	pre := b.preview(t, "apagar_regra_distribuicao", args)

	require.Len(t, linhasDeRegra(t, b, c.camp), 2, "o preview não escreve")
	require.Zero(t, b.totalAuditorias(t))
	texto := textoDoPreview(t, pre)
	require.Contains(t, texto, "Base")
	require.Contains(t, texto, "campanha INTEIRA")

	out := b.confirmarEExecutar(t, "apagar_regra_distribuicao", args)

	linhas := linhasDeRegra(t, b, c.camp)
	require.Len(t, linhas, 1)
	require.Contains(t, linhas[0], "Fica")
	res := out["resultado"].(map[string]any)
	require.Equal(t, true, res["apagada"])
	require.EqualValues(t, 1, res["regras_restantes"])
	require.Equal(t, r.String(), res["regra_apagada"].(map[string]any)["id"])
	require.Contains(t, mustJSON(t, out["ressalvas"]), "até 30 s")
	b.exigeAuditoria(t, "mcp.escrita.apagar_regra_distribuicao", "distribution_rule", &r)
}

func TestApagarRegra_ParidadeComARota(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	viaRota := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-12-01", "2026-12-31")
	viaTool := semearRegra(t, b, viaRota.tipoA, viaRota.tipoB, "2026-12-01", "2026-12-31")
	var ids [2]uuid.UUID
	for i, c := range []cenarioRegra{viaRota, viaTool} {
		ids[i] = semearRegraBase(t, b, c, "2026-12-01", "2026-12-31")
		seedRegra(t, b, c, c.tipoA, "2026-12-01", "2026-12-31", 62, "14:00", "16:00", 1, "Fica", []uuid.UUID{c.e1}, nil)
	}

	chamarHandlerCorpo(t, handlerRegras(b).Delete, http.MethodDelete, "/campaigns/x/distribution-rules/y", nil,
		map[string]string{"campaignID": viaRota.camp.String(), "ruleID": ids[0].String()}, b.admin)
	b.confirmarEExecutar(t, "apagar_regra_distribuicao", map[string]any{"campanha_id": viaTool.camp.String(), "regra_id": ids[1].String()})

	rota, tool := linhasDeRegra(t, b, viaRota.camp), linhasDeRegra(t, b, viaTool.camp)
	require.Len(t, rota, 1)
	require.Equal(t, rota, tool)
}

// Paridade de EFEITO do apagar: o Executar numa campanha com veiculação recategoriza igual à rota.
func TestApagarRegra_ParidadeDeEfeitoNaRecategorizacao(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	viaRota := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-10-01", "2026-10-31")
	viaTool := semearRegra(t, b, viaRota.tipoA, viaRota.tipoB, "2026-10-01", "2026-10-31")
	var ids [2]uuid.UUID
	for i, c := range []cenarioRegra{viaRota, viaTool} {
		semearTocadasDeQuarta(t, b, c)
		ids[i] = seedRegra(t, b, c, c.tipoA, "2026-10-01", "2026-10-31", 8, "08:00", "12:00", 1, "Quartas", []uuid.UUID{c.e1}, nil)
		require.NoError(t, catalog.NewDistributionRules(b.pool).RecategorizeForRule(b.ctx, ids[i]))
	}
	require.Contains(t, categoriasDasTocadas(t, b, viaRota.camp), "in_slot")

	chamarHandlerCorpo(t, handlerRegras(b).Delete, http.MethodDelete, "/campaigns/x/distribution-rules/y", nil,
		map[string]string{"campaignID": viaRota.camp.String(), "ruleID": ids[0].String()}, b.admin)
	in := entradaDe[entradaApagarRegra](t, map[string]any{"campanha_id": viaTool.camp.String(), "regra_id": ids[1].String()})
	_, err := escritaApagarRegra(b.cfg).Executar(b.ctx, b.E, b.p, in)
	require.NoError(t, err)

	rota, tool := categoriasDasTocadas(t, b, viaRota.camp), categoriasDasTocadas(t, b, viaTool.camp)
	require.Equal(t, rota, tool)
	require.NotContains(t, rota, "in_slot", "sem a regra a meta some e toda tocada volta a bonus")
}

func TestApagarRegra_Escalada(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-10-01", "2026-12-31")
	futura := semearRegraBase(t, b, c, "2026-11-01", "2026-12-31")
	passada := seedRegra(t, b, c, c.tipoA, "2026-10-01", "2026-12-31", 62, "14:00", "16:00", 1, "Passada", []uuid.UUID{c.e1}, nil)
	ap := func(regra uuid.UUID) map[string]any {
		return map[string]any{"campanha_id": c.camp.String(), "regra_id": regra.String()}
	}

	// Vizinho: regra só no futuro e nenhuma veiculação → confirma.
	pre := b.chamar(t, "apagar_regra_distribuicao", ap(futura))
	require.NotEmpty(t, pre["confirmacao"])

	// Gatilho (data): a vigência inclui dias passados, sem veiculação alguma.
	out := b.pedePendente(t, "apagar_regra_distribuicao", ap(passada))
	require.Contains(t, out["motivo"], "dias já passados")
	require.NotContains(t, out["motivo"], "veiculação aprovada")

	// Gatilho (veiculação): apagar recategoriza a campanha inteira — veiculação de QUALQUER
	// tipo (aqui o B) barra, mesmo a regra só-futura do tipo A.
	mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, c.matB, c.e1, "bonus", mcptest.Quando("2026-10-10", "10:00"))
	out = b.pedePendente(t, "apagar_regra_distribuicao", ap(futura))
	require.Contains(t, out["motivo"], "veiculação aprovada de algum tipo")
	require.Len(t, linhasDeRegra(t, b, c.camp), 2, "nada foi apagado")
	require.Zero(t, tokensUsados(t, b))
	require.Len(t, b.auditoria(t, "mcp.pendente.criada"), 2)
	require.Empty(t, b.auditoria(t, "mcp.escrita.apagar_regra_distribuicao"))
}

func TestApagarRegra_EscaladaNaConfirmacaoBarraENaoConsomeOToken(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-10-01", "2026-12-31")
	r := semearRegraBase(t, b, c, "2026-11-01", "2026-12-31")
	args := map[string]any{"campanha_id": c.camp.String(), "regra_id": r.String()}
	pre := b.preview(t, "apagar_regra_distribuicao", args)

	mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, c.mat, c.e1, "bonus", mcptest.Quando("2026-10-10", "10:00"))
	args["confirmacao"] = pre["confirmacao"]
	out := b.chamar(t, "apagar_regra_distribuicao", args)

	require.Equal(t, "critico", out["nivel"])
	require.Len(t, linhasDeRegra(t, b, c.camp), 1)
	require.Zero(t, tokensUsados(t, b))
}

func TestApagarRegra_FronteiraDoDiaEmSP(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-10-01", "2026-12-31")
	for _, hora := range []string{"22:00", "23:30"} {
		b.E.Agora = func() time.Time { return mcptest.Quando("2026-10-29", hora) }
		hoje := semearRegraBase(t, b, c, "2026-10-29", "2026-12-31")
		pre := b.chamar(t, "apagar_regra_distribuicao", map[string]any{"campanha_id": c.camp.String(), "regra_id": hoje.String()})
		require.NotEmpty(t, pre["confirmacao"], "regra começando hoje (SP) às %s: nenhum dia passado", hora)
		ontem := semearRegraBase(t, b, c, "2026-10-28", "2026-12-31")
		b.pedePendente(t, "apagar_regra_distribuicao", map[string]any{"campanha_id": c.camp.String(), "regra_id": ontem.String()})
		_, err := b.pool.Exec(b.ctx, `DELETE FROM distribution_rules WHERE campaign_id = $1`, c.camp)
		require.NoError(t, err)
	}
}

func TestApagarRegra_Recusas(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-12-01", "2026-12-31")
	r := semearRegraBase(t, b, c, "2026-12-01", "2026-12-31")
	outra := semearRegra(t, b, c.tipoA, c.tipoB, "2026-12-01", "2026-12-31")
	rOutra := semearRegraBase(t, b, outra, "2026-12-01", "2026-12-31")
	canc := semearRegra(t, b, c.tipoA, c.tipoB, "2026-12-01", "2026-12-31")
	rCanc := semearRegraBase(t, b, canc, "2026-12-01", "2026-12-31")
	mcptest.EstadoCampanha(t, b.ctx, b.pool, canc.camp, "cancelada")

	for nome, caso := range map[string]struct {
		args   map[string]any
		codigo string
	}{
		"regra inexistente (a rota devolveria 204)": {map[string]any{"campanha_id": c.camp.String(), "regra_id": uuid.NewString()}, "[nao_encontrado]"},
		"regra de outra campanha":                   {map[string]any{"campanha_id": c.camp.String(), "regra_id": rOutra.String()}, "[nao_encontrado]"},
		"regra inválida":                            {map[string]any{"campanha_id": c.camp.String(), "regra_id": "x"}, "[entrada_invalida]"},
		"campanha inválida":                         {map[string]any{"campanha_id": "x", "regra_id": r.String()}, "[entrada_invalida]"},
		"campanha inexistente":                      {map[string]any{"campanha_id": uuid.NewString(), "regra_id": r.String()}, "[nao_encontrado]"},
		"campanha cancelada":                        {map[string]any{"campanha_id": canc.camp.String(), "regra_id": rCanc.String()}, "[campanha_cancelada]"},
	} {
		require.Contains(t, b.chamarErro(t, "apagar_regra_distribuicao", caso.args), caso.codigo, nome)
	}
	require.Len(t, linhasDeRegra(t, b, c.camp), 1)
	require.Len(t, linhasDeRegra(t, b, outra.camp), 1, "a regra de outra campanha ficou")
	require.Len(t, linhasDeRegra(t, b, canc.camp), 1)
	require.Zero(t, b.totalAuditorias(t))
	require.Zero(t, tokensUsados(t, b))
}

// A regra some entre o preview e a confirmação: recusa de negócio e o token volta a valer.
func TestApagarRegra_RecusaNaConfirmacaoLiberaOToken(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-12-01", "2026-12-31")
	r := semearRegraBase(t, b, c, "2026-12-01", "2026-12-31")
	args := map[string]any{"campanha_id": c.camp.String(), "regra_id": r.String()}
	pre := b.preview(t, "apagar_regra_distribuicao", args)
	_, err := b.pool.Exec(b.ctx, `DELETE FROM distribution_rules WHERE id = $1`, r)
	require.NoError(t, err)

	args["confirmacao"] = pre["confirmacao"]
	require.Contains(t, b.chamarErro(t, "apagar_regra_distribuicao", args), "[nao_encontrado]")
	require.Zero(t, tokensUsados(t, b))
}
