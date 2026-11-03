package mcp

// Task 16 (Fatia 4): criar_veiculacao_manual, ignorar_veiculacao e
// restaurar_veiculacao. As três são `critico`: a chamada NUNCA executa, grava um
// pedido pendente, e quem executa é a aprovação do dono em /pendentes (b.aprovar,
// sobre a lista REAL das escritas). Cada tool prova: (1) o pedido não escreve,
// (2) aprovar executa e devolve o formato do obter_veiculacao, (3) o efeito é o
// da rota HTTP, (4) cada recusa no pedido E na aprovação, (5) a auditoria com
// meta.mcp.pendente_id. E a PROVA DA COTA: ignorar a in_slot de uma célula de
// meta 1 promove a outra tocada do dia, restaurar devolve a vaga (o mesmo
// cenário do TestResettle_ReenteringPlayRetakesTheQuota, agora pela aprovação).

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/api/handlers"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

// ── fixtures e leitura ──────────────────────────────────────────────────

// tocadaReal grava uma tocada pelo motor de verdade (Create fecha a célula-dia
// da Rádio 1: regra de 1 por dia, 08:00 às 10:00) em vez de semear a categoria à
// mão — as provas de cota precisam da categoria que o motor dá.
func tocadaReal(t *testing.T, b *bancadaTool, c cenarioCritCamp, quando time.Time) uuid.UUID {
	t.Helper()
	det, err := catalog.NewDetections(b.pool).Create(b.ctx, catalog.CreateDetectionInput{
		StationID: c.e1, CommercialID: c.mat, CampaignID: c.camp, DetectedAt: quando,
		MatchEndOffsetMs: 30000, Confidence: 0.95, HashCount: 100, TemporalCoverage: 0.85,
	})
	require.NoError(t, err)
	return det.ID
}

// categoriasDaTocada lê a categoria da tocada-base e a da projeção canônica
// (detection_campaigns): é da projeção que a grade e os relatórios leem, então
// as duas têm de andar juntas.
func categoriasDaTocada(t *testing.T, b *bancadaTool, id uuid.UUID) (string, string) {
	t.Helper()
	var base, proj string
	require.NoError(t, b.pool.QueryRow(b.ctx, `
		SELECT d.category, COALESCE(dc.category, '-')
		  FROM detections d
		  LEFT JOIN detection_campaigns dc ON dc.detection_id = d.id AND dc.detected_at = d.detected_at AND dc.campaign_id = d.campaign_id
		 WHERE d.id = $1`, id).Scan(&base, &proj))
	return base, proj
}

func exigeCategoria(t *testing.T, b *bancadaTool, id uuid.UUID, esperada string, msg string) {
	t.Helper()
	base, proj := categoriasDaTocada(t, b, id)
	require.Equal(t, esperada, base, msg+" (tocada-base)")
	require.Equal(t, esperada, proj, msg+" (projeção canônica)")
}

// linhasDeVeiculacao: uma linha por tocada da campanha (ordenada pelo instante),
// comparável entre fixtures gêmeas. Quem fez (manual_by/ignored_by) entra como
// "tem ou não" — o ator é o mesmo admin nos dois caminhos e é conferido à parte.
func linhasDeVeiculacao(t *testing.T, b *bancadaTool, camp uuid.UUID) []string {
	t.Helper()
	rows, err := b.pool.Query(b.ctx, `
		SELECT to_char(d.detected_at AT TIME ZONE 'America/Sao_Paulo', 'DD/MM HH24:MI:SS') || '|' || d.category || '|' ||
		       COALESCE(dc.category, '-') || '|manual=' || (d.manual_by IS NOT NULL)::text ||
		       '|ignorada=' || (d.ignored_at IS NOT NULL)::text || '/' || (d.ignored_by IS NOT NULL)::text ||
		       '|' || COALESCE(d.manual_note, '-') || '|' || d.confidence::text || '|' || d.hash_count::text || '|' || d.evidence_status
		  FROM detections d
		  LEFT JOIN detection_campaigns dc ON dc.detection_id = d.id AND dc.detected_at = d.detected_at AND dc.campaign_id = d.campaign_id
		 WHERE d.campaign_id = $1 ORDER BY d.detected_at, d.id`, camp)
	require.NoError(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var l string
		require.NoError(t, rows.Scan(&l))
		out = append(out, l)
	}
	require.NoError(t, rows.Err())
	return out
}

func contarVeiculacoes(t *testing.T, b *bancadaTool, camp uuid.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT count(*) FROM detections WHERE campaign_id = $1`, camp).Scan(&n))
	return n
}

// quemIgnorou: ignored_by da tocada (nil = não ignorada).
func quemIgnorou(t *testing.T, b *bancadaTool, id uuid.UUID) *uuid.UUID {
	t.Helper()
	var por *uuid.UUID
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT ignored_by FROM detections WHERE id = $1`, id).Scan(&por))
	return por
}

func ignoradaEm(t *testing.T, b *bancadaTool, id uuid.UUID) bool {
	t.Helper()
	var ign bool
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT ignored_at IS NOT NULL FROM detections WHERE id = $1`, id).Scan(&ign))
	return ign
}

// exigeAuditoriaDeVeiculacao: a execução da aprovação grava mcp.escrita.<tool>
// com o alvo (a veiculação), o token do pedido e o id do pedido em
// meta.mcp.pendente_id.
func exigeAuditoriaDeVeiculacao(t *testing.T, b *bancadaTool, tool string, id uuid.UUID, pendenteID string) {
	t.Helper()
	l := b.exigeAuditoria(t, "mcp.escrita."+tool, "detection", &id)
	require.Equal(t, pendenteID, l.Meta["mcp"].(map[string]any)["pendente_id"])
	require.Equal(t, "critico", l.Meta["nivel"])
	require.Equal(t, tool, l.Meta["tool"])
}

func linkDaVeiculacaoNoPortal(id uuid.UUID) string {
	return "https://portal.teste/detections/" + id.String()
}

func argsVeiculacao(id uuid.UUID) map[string]any {
	return map[string]any{"veiculacao_id": id.String()}
}

// exigeIgualAoObter: o resultado da aprovação é, valor por valor, o do
// obter_veiculacao lido depois.
func exigeIgualAoObter(t *testing.T, b *bancadaTool, res map[string]any) {
	t.Helper()
	lido := b.chamar(t, "obter_veiculacao", map[string]any{"veiculacao_id": res["id"]})
	require.Equal(t, lido, res, "mesmo formato (e mesmos valores) do obter_veiculacao")
}

// ── criar_veiculacao_manual ─────────────────────────────────────────────

const toolCriarVeiculacao = "criar_veiculacao_manual"

func argsCriarVeiculacao(c cenarioCritCamp, quando, nota string) map[string]any {
	a := map[string]any{
		"campanha_id": c.camp.String(), "emissora_id": c.e1.String(), "material_id": c.mat.String(), "quando": quando,
	}
	if nota != "" {
		a["nota"] = nota
	}
	return a
}

// 08:30 em São Paulo, escrito em UTC: a meta do dia (1, das 08:00 às 10:00) é dela.
const quandoNaFaixa = "2026-10-20T11:30:00Z"

func TestCriarVeiculacaoManual_PedidoNaoEscreveEAprovarCria(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearCritCamp(t, b, "ativa", uuid.Nil)
	require.NotContains(t, propriedades(esquemaDa(t, b.cfg, toolCriarVeiculacao)), "confirmacao", "crítico nunca recebe token")

	out := b.pedePendente(t, toolCriarVeiculacao, argsCriarVeiculacao(c, quandoNaFaixa, "  Lançada pela operação  "))

	require.Contains(t, out["motivo"], "entra na cobrança e pode mudar a categoria das outras veiculações do mesmo dia")
	require.Equal(t, "https://portal.teste/detections?campaign_id="+c.camp.String(), out["links"].(map[string]any)["tela"])
	require.Contains(t, out["aprovar_em"], "/pendentes?id="+out["pendente_id"].(string))
	require.Contains(t, out["aviso"], "Nada aconteceu ainda")
	require.Zero(t, contarVeiculacoes(t, b, c.camp), "o pedido não escreve")
	pv := out["preview"].(map[string]any)
	texto := mustJSON(t, pv)
	require.Contains(t, pv["resumo"], "Rádio 1")
	require.Contains(t, pv["resumo"], "Campanha Escrita")
	require.Contains(t, pv["resumo"], "20/10/2026 08:30 (America/Sao_Paulo)", "o instante é mostrado em São Paulo, não em UTC")
	require.Contains(t, texto, nomeDoTipo(t, b, c.tipo))
	require.Contains(t, mustJSON(t, pv["mudancas"]), `"para":"Lançada pela operação"`, "a nota é aparada")
	require.Contains(t, mustJSON(t, pv["efeitos"]), "20/10/2026")
	require.Contains(t, mustJSON(t, pv["efeitos"]), "disputa a cota do dia")
	require.Contains(t, mustJSON(t, pv["ressalvas"]), "comprovante (PDF)")
	require.Contains(t, mustJSON(t, pv["ressalvas"]), "só entra na criação, pela tela")
	pid := out["pendente_id"].(string)
	exigeSemEscritaNoPedido(t, b, toolCriarVeiculacao, pid)
	criada := b.auditoria(t, "mcp.pendente.criada")
	require.Equal(t, "detection", criada[0].Recurso)
	require.Nil(t, criada[0].RecursoID, "a veiculação ainda não existe")

	ap := b.aprovar(t, pid)

	res := ap.resultadoJSON(t)
	id := uuid.MustParse(res["id"].(string))
	require.Equal(t, 1, contarVeiculacoes(t, b, c.camp))
	require.Equal(t, "in_slot", res["categoria"], "a primeira dentro da faixa pega a vaga")
	require.Equal(t, quandoNaFaixa, res["quando"])
	require.Equal(t, true, res["aprovada"])
	manual := res["manual"].(map[string]any)
	require.Equal(t, "Lançada pela operação", manual["nota"])
	require.Equal(t, b.admin.String(), manual["por"].(map[string]any)["id"], "o ator é o dono que aprovou")
	exigeIgualAoObter(t, b, res)
	exigeCategoria(t, b, id, "in_slot", "a projeção canônica nasce junto")
	exigeAuditoriaDeVeiculacao(t, b, toolCriarVeiculacao, id, pid)
	require.Equal(t, "executada", b.estadoDaPendente(t, pid))
}

// Duas chamadas iguais não duplicam o pedido (F4-3): aprovar uma lança UMA tocada.
func TestCriarVeiculacaoManual_ChamadaRepetidaNaoLancaDuasVezes(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearCritCamp(t, b, "ativa", uuid.Nil)
	args := argsCriarVeiculacao(c, quandoNaFaixa, "")

	primeiro := b.pedePendente(t, toolCriarVeiculacao, args)
	segundo := b.pedePendente(t, toolCriarVeiculacao, args)

	require.Equal(t, primeiro["pendente_id"], segundo["pendente_id"])
	require.Equal(t, true, segundo["ja_existia"])
	b.aprovar(t, primeiro["pendente_id"].(string))
	require.Equal(t, 1, contarVeiculacoes(t, b, c.camp))
}

// O efeito é o da rota HTTP: duas fixtures, uma pela rota, outra pela aprovação.
// A manual (08:30) chega ANTES de uma automática já gravada (08:40) e de uma fora
// da faixa (07:30): ela toma a vaga e rebaixa a automática — nos dois caminhos.
func TestCriarVeiculacaoManual_ParidadeComARota(t *testing.T) {
	b := novaBancadaTool(t)
	tipo := mcptest.TipoMaterial(t, b.ctx, b.pool, "Spot")
	viaRota := semearCritCamp(t, b, "ativa", tipo)
	viaTool := semearCritCamp(t, b, "ativa", tipo)
	for _, c := range []cenarioCritCamp{viaRota, viaTool} {
		tocadaReal(t, b, c, mcptest.Quando("2026-10-20", "08:40"))
		tocadaReal(t, b, c, mcptest.Quando("2026-10-20", "07:30"))
	}
	antes := linhasDeVeiculacao(t, b, viaTool.camp)
	require.Equal(t, []string{"in_slot", "bonus"}, []string{strings.Split(antes[1], "|")[1], strings.Split(antes[0], "|")[1]},
		"fixture: a de 08:40 tem a vaga e a de 07:30 (fora da faixa) é excedente")
	h := &handlers.DetectionsHandler{Repo: catalog.NewDetections(b.pool)}

	chamarHandlerCorpo(t, h.CreateManual, http.MethodPost, "/detections/manual", map[string]any{
		"campaign_id": viaRota.camp.String(), "station_id": viaRota.e1.String(), "commercial_id": viaRota.mat.String(),
		"detected_at": "2026-10-20T08:30:00-03:00", "note": "Lançada pela operação",
	}, nil, b.admin)
	out := b.pedePendente(t, toolCriarVeiculacao, argsCriarVeiculacao(viaTool, "2026-10-20T08:30:00-03:00", "Lançada pela operação"))
	b.aprovar(t, out["pendente_id"].(string))

	rota, tool := linhasDeVeiculacao(t, b, viaRota.camp), linhasDeVeiculacao(t, b, viaTool.camp)
	require.Len(t, rota, 3)
	require.Equal(t, rota, tool, "as mesmas linhas gravadas pela rota e pela tool (categorias das outras do dia inclusive)")
	require.Contains(t, tool[1], "|in_slot|in_slot|manual=true|", "a manual (08:30) toma a vaga")
	require.Contains(t, tool[2], "|bonus|bonus|manual=false|", "a automática de 08:40 é rebaixada")
	var porRota, porTool uuid.UUID
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT manual_by FROM detections WHERE campaign_id = $1 AND manual_at IS NOT NULL`, viaRota.camp).Scan(&porRota))
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT manual_by FROM detections WHERE campaign_id = $1 AND manual_at IS NOT NULL`, viaTool.camp).Scan(&porTool))
	require.Equal(t, b.admin, porRota)
	require.Equal(t, b.admin, porTool)
}

func TestCriarVeiculacaoManual_ForaDoPeriodoVemComoOutDate(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearCritCamp(t, b, "ativa", uuid.Nil) // a campanha vai de 2026-10-01 a 2026-12-31

	out := b.pedePendente(t, toolCriarVeiculacao, argsCriarVeiculacao(c, "2026-09-30T10:00:00-03:00", ""))

	ef := mustJSON(t, out["preview"].(map[string]any)["efeitos"])
	require.Contains(t, ef, "fora do período da campanha (2026-10-01 a 2026-12-31)")
	require.Contains(t, ef, "out_date")
	ap := b.aprovar(t, out["pendente_id"].(string))
	require.Equal(t, "out_date", ap.resultadoJSON(t)["categoria"])
}

func TestCriarVeiculacaoManual_DentroDoPeriodoNaoAvisaOutDate(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearCritCamp(t, b, "ativa", uuid.Nil)

	out := b.pedePendente(t, toolCriarVeiculacao, argsCriarVeiculacao(c, quandoNaFaixa, ""))

	require.NotContains(t, mustJSON(t, out["preview"]), "out_date")
}

func TestCriarVeiculacaoManual_PossivelDuplicataAvisaSemRecusar(t *testing.T) {
	// a nova é sempre 08:30 de 20/10 em São Paulo
	novaEm := mcptest.Quando("2026-10-20", "08:30")

	t.Run("aprovada a 5 min: avisa, não recusa, e aprovar lança a segunda", func(t *testing.T) {
		b := novaBancadaTool(t)
		c := semearCritCamp(t, b, "ativa", uuid.Nil)
		tocadaReal(t, b, c, mcptest.Quando("2026-10-20", "08:25"))

		out := b.pedePendente(t, toolCriarVeiculacao, argsCriarVeiculacao(c, "2026-10-20T08:30:00-03:00", ""))

		rs := mustJSON(t, out["preview"].(map[string]any)["ressalvas"])
		require.Contains(t, rs, "20/10/2026 08:25", "nomeia o horário da que já existe")
		require.Contains(t, rs, "ser a mesma tocada")
		b.aprovar(t, out["pendente_id"].(string))
		require.Equal(t, 2, contarVeiculacoes(t, b, c.camp), "o aviso não impede: a pessoa decide")
	})
	t.Run("a 10 min exatos ainda avisa; a 11 não", func(t *testing.T) {
		b := novaBancadaTool(t)
		c := semearCritCamp(t, b, "ativa", uuid.Nil)
		mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, c.mat, c.e1, "in_slot", novaEm.Add(-10*time.Minute))
		mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, c.mat, c.e1, "bonus", novaEm.Add(11*time.Minute))

		out := b.pedePendente(t, toolCriarVeiculacao, argsCriarVeiculacao(c, "2026-10-20T08:30:00-03:00", ""))

		rs := mustJSON(t, out["preview"].(map[string]any)["ressalvas"])
		require.Contains(t, rs, "20/10/2026 08:20")
		require.NotContains(t, rs, "08:41", "a 11 min está fora da janela")
	})
	t.Run("sem outra no entorno: sem aviso de duplicata", func(t *testing.T) {
		b := novaBancadaTool(t)
		c := semearCritCamp(t, b, "ativa", uuid.Nil)
		mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, c.mat, c.e1, "in_slot", novaEm.Add(-16*time.Minute))

		out := b.pedePendente(t, toolCriarVeiculacao, argsCriarVeiculacao(c, "2026-10-20T08:30:00-03:00", ""))

		require.NotContains(t, mustJSON(t, out["preview"].(map[string]any)["ressalvas"]), "ser a mesma tocada")
	})
	t.Run("só conta aprovada, da mesma campanha, emissora e material", func(t *testing.T) {
		b := novaBancadaTool(t)
		c := semearCritCamp(t, b, "ativa", uuid.Nil)
		ignorada := mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, c.mat, c.e1, "in_slot", novaEm.Add(-1*time.Minute))
		retratada := mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, c.mat, c.e1, "in_slot", novaEm.Add(-2*time.Minute))
		_, err := b.pool.Exec(b.ctx, `UPDATE detections SET ignored_at = now() WHERE id = $1`, ignorada)
		require.NoError(t, err)
		_, err = b.pool.Exec(b.ctx, `UPDATE detections SET retracted_at = now() WHERE id = $1`, retratada)
		require.NoError(t, err)
		outroMat := mcptest.Material(t, b.ctx, b.pool, c.cli, c.tipo, "Outro "+uuid.NewString()[:6])
		mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, outroMat, c.e1, "in_slot", novaEm.Add(-3*time.Minute))
		mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, c.mat, c.e2, "in_slot", novaEm.Add(-4*time.Minute))
		outraCamp := mcptest.Campanha(t, b.ctx, b.pool, c.cli, "Outra", "2026-10-01", "2026-12-31", c.e1)
		mcptest.Veiculacao(t, b.ctx, b.pool, outraCamp, c.mat, c.e1, "in_slot", novaEm.Add(-5*time.Minute))

		out := b.pedePendente(t, toolCriarVeiculacao, argsCriarVeiculacao(c, "2026-10-20T08:30:00-03:00", ""))

		require.NotContains(t, mustJSON(t, out["preview"].(map[string]any)["ressalvas"]), "ser a mesma tocada")
	})
}

func TestCriarVeiculacaoManual_RecusasNoPedido(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearCritCamp(t, b, "ativa", uuid.Nil)
	cancelada := semearCritCamp(t, b, "cancelada", uuid.Nil)
	semVinculo := mcptest.Material(t, b.ctx, b.pool, c.cli, c.tipo, "Sem vínculo")
	soNaE1 := mcptest.Material(t, b.ctx, b.pool, c.cli, c.tipo, "Só na Rádio 1")
	mcptest.Vincular(t, b.ctx, b.pool, c.camp, soNaE1, c.e1)
	agora := time.Now().UTC()

	com := func(chave string, valor any) map[string]any {
		a := argsCriarVeiculacao(c, quandoNaFaixa, "")
		a[chave] = valor
		return a
	}
	for nome, caso := range map[string]struct {
		args   map[string]any
		codigo string
		texto  string // trecho esperado da mensagem
	}{
		"campanha cancelada":                {argsCriarVeiculacao(cancelada, quandoNaFaixa, ""), "[campanha_cancelada]", ""},
		"campanha inexistente":              {com("campanha_id", uuid.NewString()), "[nao_encontrado]", "campanha"},
		"campanha_id inválido":              {com("campanha_id", "xyz"), "[entrada_invalida]", "campanha_id"},
		"emissora inexistente":              {com("emissora_id", uuid.NewString()), "[nao_encontrado]", "emissora"},
		"emissora_id inválido":              {com("emissora_id", ""), "[entrada_invalida]", "emissora_id"},
		"material inexistente":              {com("material_id", uuid.NewString()), "[nao_encontrado]", "material"},
		"material_id inválido":              {com("material_id", "xyz"), "[entrada_invalida]", "material_id"},
		"material fora da campanha":         {com("material_id", semVinculo.String()), "[material_nao_vinculado]", "vincular_material"},
		"quando sem fuso":                   {com("quando", "2026-10-20T08:30:00"), "[entrada_invalida]", "fuso"},
		"quando com espaço no lugar do T":   {com("quando", "2026-10-20 08:30:00-03:00"), "[entrada_invalida]", "RFC3339"},
		"quando vazio":                      {com("quando", ""), "[entrada_invalida]", "quando"},
		"quando em texto":                   {com("quando", "ontem de manhã"), "[entrada_invalida]", "quando"},
		"quando no futuro":                  {com("quando", agora.Add(time.Hour).Format(time.RFC3339)), "[entrada_invalida]", "futuro"},
		"quando passou da tolerância":       {com("quando", agora.Add(7*time.Minute).Format(time.RFC3339)), "[entrada_invalida]", "futuro"},
		"quando antes da primeira partição": {com("quando", "2010-01-01T10:00:00-03:00"), "[entrada_invalida]", "fora do período"},
		"quando depois da última partição":  {com("quando", "2090-01-01T10:00:00-03:00"), "[entrada_invalida]", "futuro"},
		"nota acima de 500 caracteres":      {com("nota", strings.Repeat("é", 501)), "[entrada_invalida]", "500"},
		"nota com 500 e mais um no meio":    {com("nota", strings.Repeat("é", 250)+" "+strings.Repeat("é", 250)), "[entrada_invalida]", "500"},
	} {
		texto := b.chamarErro(t, toolCriarVeiculacao, caso.args)
		require.Contains(t, texto, caso.codigo, nome)
		if caso.texto != "" {
			require.Contains(t, texto, caso.texto, nome)
		}
	}
	// O material vinculado só à Rádio 1 não vale na Rádio 2: o vínculo é por emissora.
	texto := b.chamarErro(t, toolCriarVeiculacao, map[string]any{
		"campanha_id": c.camp.String(), "emissora_id": c.e2.String(), "material_id": soNaE1.String(), "quando": quandoNaFaixa,
	})
	require.Contains(t, texto, "[material_nao_vinculado]")
	require.Contains(t, texto, "Rádio 2")
	require.Contains(t, texto, "editar_emissoras_material")

	require.Zero(t, contarVeiculacoes(t, b, c.camp))
	require.Zero(t, b.totalAuditorias(t), "recusa não deixa pedido nem auditoria")
	var n int
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT count(*) FROM mcp_acoes_pendentes`).Scan(&n))
	require.Zero(t, n)

	// Os limites que passam: 500 caracteres de nota (contados em caracteres, não em
	// bytes, e depois de aparar as pontas) e 3 min à frente (a tolerância de relógio).
	b.pedePendente(t, toolCriarVeiculacao, argsCriarVeiculacao(c, quandoNaFaixa, strings.Repeat("é", 500)))
	b.pedePendente(t, toolCriarVeiculacao, argsCriarVeiculacao(c, quandoNaFaixa, "  "+strings.Repeat("ç", 500)+" \n"))
	b.pedePendente(t, toolCriarVeiculacao, argsCriarVeiculacao(c, agora.Add(3*time.Minute).Format(time.RFC3339), ""))
}

// O estado muda entre o pedido e a aprovação: a recusa vem do Preview rerodado.
func TestCriarVeiculacaoManual_RecusaNaAprovacao(t *testing.T) {
	pedir := func(t *testing.T) (*bancadaTool, cenarioCritCamp, string) {
		b := novaBancadaTool(t)
		c := semearCritCamp(t, b, "ativa", uuid.Nil)
		out := b.pedePendente(t, toolCriarVeiculacao, argsCriarVeiculacao(c, quandoNaFaixa, ""))
		return b, c, out["pendente_id"].(string)
	}
	t.Run("a campanha foi cancelada no meio", func(t *testing.T) {
		b, c, pid := pedir(t)
		mcptest.EstadoCampanha(t, b.ctx, b.pool, c.camp, "cancelada")

		exigeRecusaNaAprovacao(t, b, toolCriarVeiculacao, pid, "campanha_cancelada")
		require.Zero(t, contarVeiculacoes(t, b, c.camp))
	})
	t.Run("o material foi desvinculado no meio", func(t *testing.T) {
		b, c, pid := pedir(t)
		_, err := b.pool.Exec(b.ctx, `DELETE FROM campaign_materials WHERE campaign_id = $1 AND material_id = $2`, c.camp, c.mat)
		require.NoError(t, err)

		exigeRecusaNaAprovacao(t, b, toolCriarVeiculacao, pid, "material_nao_vinculado")
		require.Zero(t, contarVeiculacoes(t, b, c.camp))
	})
	t.Run("a emissora saiu do vínculo do material no meio", func(t *testing.T) {
		b, c, pid := pedir(t)
		mcptest.Vincular(t, b.ctx, b.pool, c.camp, c.mat, c.e2) // só a Rádio 2

		exigeRecusaNaAprovacao(t, b, toolCriarVeiculacao, pid, "material_nao_vinculado")
		require.Zero(t, contarVeiculacoes(t, b, c.camp))
	})
	t.Run("a campanha foi apagada no meio", func(t *testing.T) {
		b, c, pid := pedir(t)
		_, err := b.pool.Exec(b.ctx, `DELETE FROM campaigns WHERE id = $1`, c.camp)
		require.NoError(t, err)

		exigeRecusaNaAprovacao(t, b, toolCriarVeiculacao, pid, "nao_encontrado")
	})
	t.Run("o material foi apagado no meio", func(t *testing.T) {
		b, c, pid := pedir(t)
		_, err := b.pool.Exec(b.ctx, `DELETE FROM campaign_materials WHERE material_id = $1`, c.mat)
		require.NoError(t, err)
		_, err = b.pool.Exec(b.ctx, `DELETE FROM materials WHERE id = $1`, c.mat)
		require.NoError(t, err)

		exigeRecusaNaAprovacao(t, b, toolCriarVeiculacao, pid, "nao_encontrado")
	})
	t.Run("o instante virou futuro para o relógio do serviço", func(t *testing.T) {
		b, c, pid := pedir(t)
		b.E.Acoes.Agora = func() time.Time { return mcptest.Quando("2026-10-19", "08:00") }

		exigeRecusaNaAprovacao(t, b, toolCriarVeiculacao, pid, "entrada_invalida")
		require.Zero(t, contarVeiculacoes(t, b, c.camp))
	})
}

// ── ignorar_veiculacao e restaurar_veiculacao ───────────────────────────

const (
	toolIgnorarVeiculacao   = "ignorar_veiculacao"
	toolRestaurarVeiculacao = "restaurar_veiculacao"
)

// celulaDeCotaUm: a Rádio 1 tem meta 1/dia (08:00 às 10:00) em 20/10: a tocada
// de 08:10 pega a vaga (in_slot) e a de 08:20 é excedente (bonus).
func celulaDeCotaUm(t *testing.T, b *bancadaTool, c cenarioCritCamp) (uuid.UUID, uuid.UUID) {
	t.Helper()
	p1 := tocadaReal(t, b, c, mcptest.Quando("2026-10-20", "08:10"))
	p2 := tocadaReal(t, b, c, mcptest.Quando("2026-10-20", "08:20"))
	exigeCategoria(t, b, p1, "in_slot", "fixture: a mais antiga dentro da faixa pega a vaga")
	exigeCategoria(t, b, p2, "bonus", "fixture: a segunda é excedente")
	return p1, p2
}

// A PROVA DA COTA: ignorar a in_slot de uma célula de meta 1 promove a outra
// tocada do dia a in_slot; restaurar devolve a vaga e rebaixa a outra.
func TestIgnorarERestaurarVeiculacao_ProvaDaCota(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearCritCamp(t, b, "ativa", uuid.Nil)
	p1, p2 := celulaDeCotaUm(t, b, c)
	for _, tool := range []string{toolIgnorarVeiculacao, toolRestaurarVeiculacao} {
		require.NotContains(t, propriedades(esquemaDa(t, b.cfg, tool)), "confirmacao", "crítico nunca recebe token")
	}

	// ── ignorar ──
	out := b.pedePendente(t, toolIgnorarVeiculacao, argsVeiculacao(p1))

	require.Contains(t, out["motivo"], "a vaga liberada pode promover outra veiculação do mesmo dia")
	require.Equal(t, linkDaVeiculacaoNoPortal(p1), out["links"].(map[string]any)["tela"])
	pv := out["preview"].(map[string]any)
	require.Contains(t, pv["resumo"], "Rádio 1")
	require.Contains(t, pv["resumo"], "20/10/2026 08:10 (America/Sao_Paulo)")
	require.Contains(t, pv["resumo"], "in_slot")
	ef := mustJSON(t, pv["efeitos"])
	require.Contains(t, ef, `campanha \"Campanha Escrita\" × emissora \"Rádio 1\" × tipo \"`+nomeDoTipo(t, b, c.tipo)+`\" × dia 20/10/2026`)
	require.Contains(t, ef, "a próxima veiculação elegível do dia")
	require.Contains(t, ef, "o déficit do dia sobe")
	mud := pv["mudancas"].([]any)[0].(map[string]any)
	require.Equal(t, "conta (in_slot)", mud["de"])
	require.Equal(t, "ignorada", mud["para"])
	require.False(t, ignoradaEm(t, b, p1), "o pedido não escreve")
	exigeCategoria(t, b, p1, "in_slot", "o pedido não recategoriza")
	exigeCategoria(t, b, p2, "bonus", "o pedido não recategoriza")
	pid := out["pendente_id"].(string)
	exigeSemEscritaNoPedido(t, b, toolIgnorarVeiculacao, pid)
	criada := b.auditoria(t, "mcp.pendente.criada")
	require.Equal(t, "detection", criada[0].Recurso)
	require.Equal(t, p1, *criada[0].RecursoID)

	ap := b.aprovar(t, pid)

	require.True(t, ignoradaEm(t, b, p1))
	require.Equal(t, b.admin, *quemIgnorou(t, b, p1), "ignored_by é o dono que aprovou")
	exigeCategoria(t, b, p2, "in_slot", "a vaga liberada promove a excedente")
	res := ap.resultadoJSON(t)
	require.Equal(t, p1.String(), res["id"])
	require.Equal(t, false, res["aprovada"], "ignorada sai do conjunto aprovado")
	require.NotNil(t, res["ignorada_em"])
	exigeIgualAoObter(t, b, res)
	exigeAuditoriaDeVeiculacao(t, b, toolIgnorarVeiculacao, p1, pid)
	require.Equal(t, "executada", b.estadoDaPendente(t, pid))

	// ── restaurar ──
	out = b.pedePendente(t, toolRestaurarVeiculacao, argsVeiculacao(p1))

	require.Contains(t, out["motivo"], "a veiculação volta a disputar a cota do dia e pode rebaixar outra")
	require.Equal(t, linkDaVeiculacaoNoPortal(p1), out["links"].(map[string]any)["tela"])
	pv = out["preview"].(map[string]any)
	require.Contains(t, pv["resumo"], "Restaurar")
	ef = mustJSON(t, pv["efeitos"])
	require.Contains(t, ef, "retoma o lugar cronológico")
	require.Contains(t, ef, "rebaixa")
	mud = pv["mudancas"].([]any)[0].(map[string]any)
	require.Contains(t, mud["de"], "ignorada")
	require.Equal(t, "conta de novo", mud["para"])
	require.True(t, ignoradaEm(t, b, p1), "o pedido não escreve")
	exigeCategoria(t, b, p2, "in_slot", "o pedido não recategoriza")
	pid = out["pendente_id"].(string)

	ap = b.aprovar(t, pid)

	require.False(t, ignoradaEm(t, b, p1))
	require.Nil(t, quemIgnorou(t, b, p1))
	exigeCategoria(t, b, p1, "in_slot", "restaurada, retoma a vaga (é a mais antiga)")
	exigeCategoria(t, b, p2, "bonus", "rebaixada de volta a excedente")
	res = ap.resultadoJSON(t)
	require.Equal(t, true, res["aprovada"])
	require.Nil(t, res["ignorada_em"])
	require.Equal(t, "in_slot", res["categoria"])
	exigeIgualAoObter(t, b, res)
	l := b.exigeAuditoria(t, "mcp.escrita."+toolRestaurarVeiculacao, "detection", &p1)
	require.Equal(t, pid, l.Meta["mcp"].(map[string]any)["pendente_id"])
}

// Ignorar uma excedente não mexe na cota: o preview diz que as outras ficam.
func TestIgnorarVeiculacao_ExcedenteNaoMexeNasOutras(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearCritCamp(t, b, "ativa", uuid.Nil)
	p1, p2 := celulaDeCotaUm(t, b, c)

	out := b.pedePendente(t, toolIgnorarVeiculacao, argsVeiculacao(p2))

	ef := mustJSON(t, out["preview"].(map[string]any)["efeitos"])
	require.Contains(t, ef, "não ocupa vaga na cota")
	require.NotContains(t, ef, "o déficit do dia sobe")
	b.aprovar(t, out["pendente_id"].(string))
	exigeCategoria(t, b, p1, "in_slot", "quem tinha a vaga a mantém")
	require.True(t, ignoradaEm(t, b, p2))
}

func TestIgnorarERestaurarVeiculacao_ParidadeComARota(t *testing.T) {
	b := novaBancadaTool(t)
	tipo := mcptest.TipoMaterial(t, b.ctx, b.pool, "Spot")
	viaRota := semearCritCamp(t, b, "ativa", tipo)
	viaTool := semearCritCamp(t, b, "ativa", tipo)
	rp1, _ := celulaDeCotaUm(t, b, viaRota)
	tp1, _ := celulaDeCotaUm(t, b, viaTool)
	h := &handlers.DetectionsHandler{Repo: catalog.NewDetections(b.pool)}
	rota := func() []string { return linhasDeVeiculacao(t, b, viaRota.camp) }
	tool := func() []string { return linhasDeVeiculacao(t, b, viaTool.camp) }
	require.Equal(t, rota(), tool(), "fixture: células gêmeas")

	chamarHandlerCorpo(t, h.Ignore, http.MethodPost, "/detections/x/ignore", nil, map[string]string{"id": rp1.String()}, b.admin)
	out := b.pedePendente(t, toolIgnorarVeiculacao, argsVeiculacao(tp1))
	b.aprovar(t, out["pendente_id"].(string))

	require.Equal(t, rota(), tool(), "ignorar: as mesmas linhas pela rota e pela tool")
	require.Contains(t, tool()[0], "|ignorada=true/true|")
	require.Contains(t, tool()[1], "|in_slot|in_slot|", "a outra foi promovida")
	require.Equal(t, b.admin, *quemIgnorou(t, b, rp1))
	require.Equal(t, b.admin, *quemIgnorou(t, b, tp1))

	chamarHandlerCorpo(t, h.Restore, http.MethodPost, "/detections/x/restore", nil, map[string]string{"id": rp1.String()}, b.admin)
	out = b.pedePendente(t, toolRestaurarVeiculacao, argsVeiculacao(tp1))
	b.aprovar(t, out["pendente_id"].(string))

	require.Equal(t, rota(), tool(), "restaurar: as mesmas linhas pela rota e pela tool")
	require.Contains(t, tool()[0], "|in_slot|in_slot|manual=false|ignorada=false/false|")
	require.Contains(t, tool()[1], "|bonus|bonus|")
}

func TestIgnorarVeiculacao_Recusas(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearCritCamp(t, b, "ativa", uuid.Nil)
	p1, p2 := celulaDeCotaUm(t, b, c)
	ignoraNaTela(t, b, p1)

	for nome, caso := range map[string]struct {
		args   map[string]any
		codigo string
		texto  string
	}{
		"já ignorada":  {argsVeiculacao(p1), "[ja_ignorada]", "restaurar_veiculacao"},
		"inexistente":  {argsVeiculacao(uuid.New()), "[nao_encontrado]", "veiculação"},
		"id inválido":  {map[string]any{"veiculacao_id": "xyz"}, "[entrada_invalida]", "veiculacao_id"},
		"id em branco": {map[string]any{"veiculacao_id": ""}, "[entrada_invalida]", "veiculacao_id"},
	} {
		texto := b.chamarErro(t, toolIgnorarVeiculacao, caso.args)
		require.Contains(t, texto, caso.codigo, nome)
		require.Contains(t, texto, caso.texto, nome)
	}
	exigeCategoria(t, b, p2, "in_slot", "nenhuma recusa mexeu na cota")
	require.Zero(t, b.totalAuditorias(t))
	var n int
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT count(*) FROM mcp_acoes_pendentes`).Scan(&n))
	require.Zero(t, n)
}

// ignoraNaTela ignora a tocada como a tela faz (a rota chama o mesmo repositório),
// por um usuário DIFERENTE do dono para o ignored_by dizer quem foi.
func ignoraNaTela(t *testing.T, b *bancadaTool, id uuid.UUID) uuid.UUID {
	t.Helper()
	outro := mcptest.Usuario(t, b.ctx, b.pool, "admin", true)
	require.NoError(t, catalog.NewDetections(b.pool).Ignore(b.ctx, id, outro))
	return outro
}

func TestIgnorarVeiculacao_RecusaNaAprovacao(t *testing.T) {
	t.Run("a tela ignorou no meio: ja_ignorada e o ignored_by da tela não é sobrescrito", func(t *testing.T) {
		b := novaBancadaTool(t)
		c := semearCritCamp(t, b, "ativa", uuid.Nil)
		p1, _ := celulaDeCotaUm(t, b, c)
		out := b.pedePendente(t, toolIgnorarVeiculacao, argsVeiculacao(p1))
		pela := ignoraNaTela(t, b, p1)

		exigeRecusaNaAprovacao(t, b, toolIgnorarVeiculacao, out["pendente_id"].(string), "ja_ignorada")
		require.Equal(t, pela, *quemIgnorou(t, b, p1))
	})
	t.Run("a veiculação foi apagada no meio", func(t *testing.T) {
		b := novaBancadaTool(t)
		c := semearCritCamp(t, b, "ativa", uuid.Nil)
		p1, _ := celulaDeCotaUm(t, b, c)
		out := b.pedePendente(t, toolIgnorarVeiculacao, argsVeiculacao(p1))
		_, err := b.pool.Exec(b.ctx, `DELETE FROM detection_campaigns WHERE detection_id = $1`, p1)
		require.NoError(t, err)
		_, err = b.pool.Exec(b.ctx, `DELETE FROM detections WHERE id = $1`, p1)
		require.NoError(t, err)

		exigeRecusaNaAprovacao(t, b, toolIgnorarVeiculacao, out["pendente_id"].(string), "nao_encontrado")
	})
}

func TestRestaurarVeiculacao_Recusas(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearCritCamp(t, b, "ativa", uuid.Nil)
	p1, p2 := celulaDeCotaUm(t, b, c)

	for nome, caso := range map[string]struct {
		args   map[string]any
		codigo string
		texto  string
	}{
		"não ignorada": {argsVeiculacao(p1), "[nao_ignorada]", "não está ignorada"},
		"inexistente":  {argsVeiculacao(uuid.New()), "[nao_encontrado]", "veiculação"},
		"id inválido":  {map[string]any{"veiculacao_id": "xyz"}, "[entrada_invalida]", "veiculacao_id"},
	} {
		texto := b.chamarErro(t, toolRestaurarVeiculacao, caso.args)
		require.Contains(t, texto, caso.codigo, nome)
		require.Contains(t, texto, caso.texto, nome)
	}
	exigeCategoria(t, b, p1, "in_slot", "nenhuma recusa mexeu na cota")
	exigeCategoria(t, b, p2, "bonus", "nenhuma recusa mexeu na cota")
	require.Zero(t, b.totalAuditorias(t))
	var n int
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT count(*) FROM mcp_acoes_pendentes`).Scan(&n))
	require.Zero(t, n)
}

func TestRestaurarVeiculacao_RecusaNaAprovacao(t *testing.T) {
	t.Run("a tela restaurou no meio: nao_ignorada", func(t *testing.T) {
		b := novaBancadaTool(t)
		c := semearCritCamp(t, b, "ativa", uuid.Nil)
		p1, p2 := celulaDeCotaUm(t, b, c)
		ignoraNaTela(t, b, p1)
		out := b.pedePendente(t, toolRestaurarVeiculacao, argsVeiculacao(p1))
		require.NoError(t, catalog.NewDetections(b.pool).Restore(b.ctx, p1))

		exigeRecusaNaAprovacao(t, b, toolRestaurarVeiculacao, out["pendente_id"].(string), "nao_ignorada")
		exigeCategoria(t, b, p1, "in_slot", "a restauração da tela vale")
		exigeCategoria(t, b, p2, "bonus", "e nada mudou depois dela")
	})
	t.Run("a veiculação foi apagada no meio", func(t *testing.T) {
		b := novaBancadaTool(t)
		c := semearCritCamp(t, b, "ativa", uuid.Nil)
		p1, _ := celulaDeCotaUm(t, b, c)
		ignoraNaTela(t, b, p1)
		out := b.pedePendente(t, toolRestaurarVeiculacao, argsVeiculacao(p1))
		_, err := b.pool.Exec(b.ctx, `DELETE FROM detection_campaigns WHERE detection_id = $1`, p1)
		require.NoError(t, err)
		_, err = b.pool.Exec(b.ctx, `DELETE FROM detections WHERE id = $1`, p1)
		require.NoError(t, err)

		exigeRecusaNaAprovacao(t, b, toolRestaurarVeiculacao, out["pendente_id"].(string), "nao_encontrado")
	})
}

// A tocada já fora do conjunto aprovado por outro motivo (retratada, rejeitada
// pelo audit): ignorar/restaurar não muda o que conta, e o preview diz.
func TestIgnorarERestaurarVeiculacao_ForaDoConjuntoPorOutroMotivo(t *testing.T) {
	for nome, caso := range map[string]struct {
		sql, rotulo string
	}{
		"retratada":            {`UPDATE detections SET retracted_at = now() WHERE id = $1`, "retratada"},
		"rejeitada pelo audit": {`UPDATE detections SET evidence_status = 'audit_rejected' WHERE id = $1`, "rejeitada pelo audit"},
		"ambígua":              {`UPDATE detections SET retracted_at = now(), evidence_status = 'ambiguous' WHERE id = $1`, "ambígua"},
	} {
		t.Run(nome, func(t *testing.T) {
			b := novaBancadaTool(t)
			c := semearCritCamp(t, b, "ativa", uuid.Nil)
			_, p2 := celulaDeCotaUm(t, b, c)
			_, err := b.pool.Exec(b.ctx, caso.sql, p2)
			require.NoError(t, err)

			out := b.pedePendente(t, toolIgnorarVeiculacao, argsVeiculacao(p2))

			rs := mustJSON(t, out["preview"].(map[string]any)["ressalvas"])
			require.Contains(t, rs, "já está fora do conjunto aprovado ("+caso.rotulo)
			require.Contains(t, rs, "ignorar não muda nenhum número agora")

			ignoraNaTela(t, b, p2)
			out = b.pedePendente(t, toolRestaurarVeiculacao, argsVeiculacao(p2))

			rs = mustJSON(t, out["preview"].(map[string]any)["ressalvas"])
			require.Contains(t, rs, "continua fora do conjunto aprovado ("+caso.rotulo)
		})
	}
}

// A tocada que conta para N campanhas refecha a célula de cada uma.
func TestIgnorarVeiculacao_MultiAtribuicaoNomeiaAsOutrasCampanhas(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearCritCamp(t, b, "ativa", uuid.Nil)
	p1, _ := celulaDeCotaUm(t, b, c)
	outra := mcptest.Campanha(t, b.ctx, b.pool, c.cli, "Campanha Gêmea", "2026-10-01", "2026-12-31", c.e1)
	var quando time.Time
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT detected_at FROM detections WHERE id = $1`, p1).Scan(&quando))
	mcptest.Projecao(t, b.ctx, b.pool, p1, quando, outra, c.mat, "bonus")

	out := b.pedePendente(t, toolIgnorarVeiculacao, argsVeiculacao(p1))

	ef := mustJSON(t, out["preview"].(map[string]any)["efeitos"])
	require.Contains(t, ef, "também conta para 1 outra(s) campanha(s)")
	require.Contains(t, ef, "Campanha Gêmea")
}

// ── regras que valem para as três ───────────────────────────────────────

func TestCriticasDeVeiculacao_DescricaoPedeAprovacaoENuncaInventarIds(t *testing.T) {
	visto := 0
	for _, tl := range listarToolsDoServidor(t) {
		switch tl.Name {
		case toolCriarVeiculacao, toolIgnorarVeiculacao, toolRestaurarVeiculacao:
		default:
			continue
		}
		visto++
		require.Contains(t, tl.Description, "/pendentes", tl.Name)
		require.Contains(t, tl.Description, "aprovar_em", tl.Name)
		require.Contains(t, tl.Description, "nada aconteceu ainda", tl.Name)
		require.Contains(t, tl.Description, "Nunca invente ids", tl.Name)
		require.True(t, strings.HasSuffix(tl.Description, " Nível: critico."), tl.Name)
		require.False(t, tl.Annotations.ReadOnlyHint, tl.Name)
		require.NotNil(t, tl.Annotations.DestructiveHint, tl.Name)
		require.True(t, *tl.Annotations.DestructiveHint, tl.Name)
	}
	require.Equal(t, 3, visto)
}

// A descrição de criar manda o modelo PERGUNTAR do comprovante antes de chamar
// (F4-13) e pede o fuso explícito.
func TestCriarVeiculacaoManual_DescricaoMandaPerguntarDoComprovante(t *testing.T) {
	for _, tl := range listarToolsDoServidor(t) {
		if tl.Name != toolCriarVeiculacao {
			continue
		}
		require.Contains(t, tl.Description, "PERGUNTE")
		require.Contains(t, tl.Description, "comprovante")
		require.Contains(t, tl.Description, "PDF")
		require.Contains(t, tl.Description, "pela tela")
		require.Contains(t, tl.Description, "RFC3339")
		require.Contains(t, tl.Description, "fuso")
		require.Contains(t, tl.Description, "material_nao_vinculado")
		esq := esquemaComoMapa(t, tl)
		obrigatorios, _ := esq["required"].([]any)
		require.ElementsMatch(t, []any{"campanha_id", "emissora_id", "material_id", "quando"}, obrigatorios, "nota é opcional")
		return
	}
	t.Fatal("tool não registrada")
}

// O resultado de uma escrita de veiculação nunca leva campo proibido nem
// segredo (stream_url, hub_id…): o formato é o do obter_veiculacao.
func TestCriticasDeVeiculacao_ResultadoSemCampoProibido(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearCritCamp(t, b, "ativa", uuid.Nil)
	out := b.pedePendente(t, toolCriarVeiculacao, argsCriarVeiculacao(c, quandoNaFaixa, ""))
	ap := b.aprovar(t, out["pendente_id"].(string))

	texto := string(ap.ResultadoJSON)
	for _, proibido := range camposProibidosNaEscrita {
		require.NotContains(t, texto, fmt.Sprintf("%q", proibido), proibido)
	}
	require.NotContains(t, texto, "teste.mcp", "a URL do stream não aparece")
}
