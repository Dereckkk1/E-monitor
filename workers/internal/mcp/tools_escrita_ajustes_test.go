package mcp

// Task 17: definir_override e apagar_override.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/acoes"
	"radiocheck/internal/api/handlers"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

// ── fixtures e leitura ──────────────────────────────────────────────────

func argsOverride(c cenarioRegra, data string, insercoes int, extra map[string]any) map[string]any {
	a := map[string]any{
		"campanha_id": c.camp.String(), "tipo_id": c.tipoA.String(), "emissora_id": c.e1.String(), "data": data, "insercoes": insercoes,
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

// argsApagarOverride: a célula (sem meta, faixa nem motivo).
func argsApagarOverride(c cenarioRegra, data string) map[string]any {
	return map[string]any{"campanha_id": c.camp.String(), "tipo_id": c.tipoA.String(), "emissora_id": c.e1.String(), "data": data}
}

// linhasDeAjuste: os ajustes da campanha normalizados (nomes no lugar de ids; sem
// o carimbo de criação). created_by sai à parte: a rota nunca o preenche, a tool sim.
func linhasDeAjuste(t *testing.T, b *bancadaTool, camp uuid.UUID) []string {
	t.Helper()
	rows, err := b.pool.Query(b.ctx, `
		SELECT t.name, s.name, o.for_date::text, o.plays_expected, to_char(o.time_start,'HH24:MI'), to_char(o.time_end,'HH24:MI'), COALESCE(o.reason,'')
		  FROM distribution_overrides o
		  JOIN material_types t ON t.id = o.type_id JOIN stations s ON s.id = o.station_id
		 WHERE o.campaign_id = $1 ORDER BY o.for_date, s.name`, camp)
	require.NoError(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var tipo, est, d, ini, fim, motivo string
		var n int16
		require.NoError(t, rows.Scan(&tipo, &est, &d, &n, &ini, &fim, &motivo))
		out = append(out, fmt.Sprintf("%s|%s|%s|%d|%s-%s|%s", tipo, est, d, n, ini, fim, motivo))
	}
	require.NoError(t, rows.Err())
	return out
}

func criadoPorDoAjuste(t *testing.T, b *bancadaTool, camp uuid.UUID) *uuid.UUID {
	t.Helper()
	var id *uuid.UUID
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT created_by FROM distribution_overrides WHERE campaign_id = $1`, camp).Scan(&id))
	return id
}

func handlerAjustes(b *bancadaTool) *handlers.DistributionOverridesHandler {
	return &handlers.DistributionOverridesHandler{
		Repo: catalog.NewDistributionOverrides(b.pool), Recat: catalog.NewDistributionRules(b.pool),
	}
}

// corpoAjusteRota: o corpo da rota para a célula de argsOverride.
func corpoAjusteRota(c cenarioRegra, data string, n int, ini, fim string, motivo *string) map[string]any {
	m := map[string]any{
		"type_id": c.tipoA.String(), "station_id": c.e1.String(), "for_date": data, "plays_expected": n, "time_start": ini, "time_end": fim,
	}
	if motivo != nil {
		m["reason"] = *motivo
	}
	return m
}

func planoDoResultado(out map[string]any) map[string]any {
	return out["resultado"].(map[string]any)["plano_do_dia"].(map[string]any)
}

// semearAjusteDireto grava o ajuste e fecha a célula como o gatilho faria.
func semearAjusteDireto(t *testing.T, b *bancadaTool, c cenarioRegra, data string, n int16, ini, fim string) {
	t.Helper()
	mcptest.Ajuste(t, b.ctx, b.pool, c.camp, c.tipoA, c.e1, data, n, ini, fim)
	require.NoError(t, catalog.NewDistributionRules(b.pool).RecategorizeForOverride(b.ctx, c.camp, c.tipoA, c.e1, mcptest.Dia(data)))
}

// ── definir_override ────────────────────────────────────────────────────

func TestDefinirOverride_PreviewNaoEscreveEConfirmarGrava(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-12-01", "2026-12-31")
	semearRegraBase(t, b, c, "2026-12-01", "2026-12-31") // seg a sex, 08–12h, 2/dia em E1 e E2
	require.Contains(t, propriedades(esquemaDa(t, b.cfg, "definir_override")), "confirmacao")
	args := argsOverride(c, "2026-12-07", 5, map[string]any{"motivo": "Emissora trocou a grade"}) // segunda-feira

	pre := b.preview(t, "definir_override", args)

	require.Empty(t, linhasDeAjuste(t, b, c.camp), "o preview não escreve")
	require.Zero(t, b.totalAuditorias(t))
	require.Zero(t, tokensUsados(t, b))
	texto := textoDoPreview(t, pre)
	for _, trecho := range []string{"2 (soma de 1 regra(s))", "08:00–12:00", "SUBSTITUI as regras", "AGORA (síncrono)", "Rádio 1", "Emissora trocou a grade", "A faixa não foi informada"} {
		require.Contains(t, texto, trecho)
	}

	out := b.confirmarEExecutar(t, "definir_override", args)

	tipo := nomeDoTipo(t, b, c.tipoA)
	require.Equal(t, []string{tipo + "|Rádio 1|2026-12-07|5|08:00-12:00|Emissora trocou a grade"}, linhasDeAjuste(t, b, c.camp))
	require.Equal(t, &b.admin, criadoPorDoAjuste(t, b, c.camp), "created_by é o usuário do token (a rota nunca o preenche)")
	aj := out["resultado"].(map[string]any)["ajuste"].(map[string]any) // o formato de listar_overrides
	require.Equal(t, "2026-12-07", aj["data"])
	require.EqualValues(t, 5, aj["insercoes_esperadas"])
	require.Equal(t, map[string]any{"inicio": "08:00", "fim": "12:00"}, aj["faixa"])
	// A célula recalculada (o formato de plano_do_dia): a meta vem do ajuste.
	plano := planoDoResultado(out)
	require.Equal(t, "ajuste_do_dia", plano["meta_vem_de"])
	require.EqualValues(t, 5, plano["saldo"].(map[string]any)["esperado"])
	require.Contains(t, mustJSON(t, out["ressalvas"]), "já traz as categorias recalculadas")
	l := b.exigeAuditoria(t, "mcp.escrita.definir_override", "campaign", &c.camp)
	require.Equal(t, "confirma", l.Meta["nivel"])

	// O ajuste devolvido é o mesmo que listar_overrides mostra.
	lista := itens(t, b.chamar(t, "listar_overrides", map[string]any{"campanha_id": c.camp.String(), "de": "2026-12-01", "ate": "2026-12-31"}))
	require.Len(t, lista, 1)
	require.Equal(t, lista[0], aj)

	// Trocar o ajuste: a mesma célula-dia vira outro valor (upsert), sem segunda linha.
	// motivo omitido MANTÉM o motivo atual (F3-10: omitir não apaga), e o preview não mostra o motivo.
	pre = b.preview(t, "definir_override", argsOverride(c, "2026-12-07", 1, nil))
	for _, m := range pre["preview"].(map[string]any)["mudancas"].([]any) {
		require.NotEqual(t, "motivo", m.(map[string]any)["campo"], "o motivo não muda: não aparece")
	}
	b.confirmarEExecutar(t, "definir_override", argsOverride(c, "2026-12-07", 1, nil))
	require.Equal(t, []string{tipo + "|Rádio 1|2026-12-07|1|08:00-12:00|Emissora trocou a grade"}, linhasDeAjuste(t, b, c.camp))

	// Trocar o motivo por outro aparece de → para; "" APAGA o motivo (explícito).
	pre = b.preview(t, "definir_override", argsOverride(c, "2026-12-07", 1, map[string]any{"motivo": "Novo motivo"}))
	require.Contains(t, textoDoPreview(t, pre), `"campo":"motivo","de":"Emissora trocou a grade","para":"Novo motivo"`)
	b.confirmarEExecutar(t, "definir_override", argsOverride(c, "2026-12-07", 1, map[string]any{"motivo": "Novo motivo"}))
	require.Equal(t, []string{tipo + "|Rádio 1|2026-12-07|1|08:00-12:00|Novo motivo"}, linhasDeAjuste(t, b, c.camp))
	pre = b.preview(t, "definir_override", argsOverride(c, "2026-12-07", 1, map[string]any{"motivo": ""}))
	require.Contains(t, textoDoPreview(t, pre), `"campo":"motivo","de":"Novo motivo","para":null`)
	b.confirmarEExecutar(t, "definir_override", argsOverride(c, "2026-12-07", 1, map[string]any{"motivo": ""}))
	require.Equal(t, []string{tipo + "|Rádio 1|2026-12-07|1|08:00-12:00|"}, linhasDeAjuste(t, b, c.camp))
	// Só o motivo mudou (mesma meta e faixa): é uma alteração, não "nada a alterar".
	b.confirmarEExecutar(t, "definir_override", argsOverride(c, "2026-12-07", 1, map[string]any{"motivo": "De novo"}))
	require.Equal(t, []string{tipo + "|Rádio 1|2026-12-07|1|08:00-12:00|De novo"}, linhasDeAjuste(t, b, c.camp))
	// Ajuste NOVO sem motivo: nenhum motivo, e o preview não inventa a linha.
	pre = b.preview(t, "definir_override", argsOverride(c, "2026-12-08", 1, nil))
	for _, m := range pre["preview"].(map[string]any)["mudancas"].([]any) {
		require.NotEqual(t, "motivo", m.(map[string]any)["campo"])
	}
}

// recatQueFalhaNoMCP faz a recategorização síncrona da célula-dia falhar (as ações a
// engolem: o ajuste já foi gravado), para provar que a resposta não afirma o contrário.
type recatQueFalhaNoMCP struct{}

func (recatQueFalhaNoMCP) RecategorizeForOverride(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, time.Time) error {
	return errors.New("recat caiu")
}

func TestOverrides_RecatQueFalhaNaoAfirmaCategoriasRecalculadas(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-12-01", "2026-12-31")
	args := argsOverride(c, "2026-12-07", 3, map[string]any{"inicio_horario": "08:00", "fim_horario": "10:00"})

	// Recat saudável: afirma.
	out := b.confirmarEExecutar(t, "definir_override", args)
	require.Contains(t, mustJSON(t, out["ressalvas"]), "já traz as categorias recalculadas")

	// Recat falhando: o ajuste é gravado (a ação não falha), mas a resposta diz a verdade.
	b.E.Acoes.RecatCelula = recatQueFalhaNoMCP{}
	args["insercoes"] = 4
	out = b.confirmarEExecutar(t, "definir_override", args)
	r := mustJSON(t, out["ressalvas"])
	require.Contains(t, r, "recategorização da célula-dia falhou")
	require.Contains(t, r, "15 min")
	require.NotContains(t, r, "já traz as categorias recalculadas")
	require.Len(t, linhasDeAjuste(t, b, c.camp), 1)
	require.Contains(t, linhasDeAjuste(t, b, c.camp)[0], "|4|", "o ajuste foi gravado mesmo assim")

	out = b.confirmarEExecutar(t, "apagar_override", argsApagarOverride(c, "2026-12-07"))
	r = mustJSON(t, out["ressalvas"])
	require.Contains(t, r, "recategorização da célula-dia falhou")
	require.NotContains(t, r, "já traz as categorias recalculadas")
	require.Empty(t, linhasDeAjuste(t, b, c.camp))
}

func TestDefinirOverride_FaixaOmitidaSegueATela(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-12-01", "2026-12-31")
	tipo := nomeDoTipo(t, b, c.tipoA)
	linha := func(data string, n int, faixa string) string {
		return fmt.Sprintf("%s|Rádio 1|%s|%d|%s|", tipo, data, n, faixa)
	}

	// Nenhuma regra no dia: 06:00–22:00 (último recurso da tela), com ressalva.
	pre := b.preview(t, "definir_override", argsOverride(c, "2026-12-07", 2, nil))
	r := mustJSON(t, pre["preview"].(map[string]any)["ressalvas"])
	require.Contains(t, r, "06:00–22:00")
	require.Contains(t, r, "Nenhuma regra de distribuição vale neste dia")
	b.confirmarEExecutar(t, "definir_override", argsOverride(c, "2026-12-07", 2, nil))
	require.Equal(t, []string{linha("2026-12-07", 2, "06:00-22:00")}, linhasDeAjuste(t, b, c.camp))

	// Faixa informada: usada como veio, sem ressalva de herança.
	pre = b.preview(t, "definir_override", argsOverride(c, "2026-12-08", 2, map[string]any{"inicio_horario": "13:00", "fim_horario": "15:30"}))
	require.NotContains(t, textoDoPreview(t, pre), "A faixa não foi informada")

	// Já existe ajuste na célula: a faixa omitida é a dele.
	b.confirmarEExecutar(t, "definir_override", argsOverride(c, "2026-12-07", 3, nil))
	require.Equal(t, []string{linha("2026-12-07", 3, "06:00-22:00")}, linhasDeAjuste(t, b, c.camp))
	_, err := b.pool.Exec(b.ctx, `UPDATE distribution_overrides SET time_start = '09:15', time_end = '11:45'`)
	require.NoError(t, err)
	b.confirmarEExecutar(t, "definir_override", argsOverride(c, "2026-12-07", 4, nil))
	require.Equal(t, []string{linha("2026-12-07", 4, "09:15-11:45")}, linhasDeAjuste(t, b, c.camp))
	_, err = b.pool.Exec(b.ctx, `DELETE FROM distribution_overrides`)
	require.NoError(t, err)

	// Regra única: a faixa dela. Sábado e domingo ficam sem regra (o mask é seg a sex).
	semearRegraBase(t, b, c, "2026-12-01", "2026-12-31") // 08–12h em E1 e E2
	b.confirmarEExecutar(t, "definir_override", argsOverride(c, "2026-12-09", 1, nil))
	require.Equal(t, []string{linha("2026-12-09", 1, "08:00-12:00")}, linhasDeAjuste(t, b, c.camp))

	// Faixa mais estreita que a regra única: o mesmo aviso do popover da tela.
	pre = b.preview(t, "definir_override", argsOverride(c, "2026-12-10", 1, map[string]any{"inicio_horario": "09:00", "fim_horario": "11:00"}))
	require.Contains(t, mustJSON(t, pre["preview"].(map[string]any)["ressalvas"]), "mais estreita que a da regra")

	// Duas regras com faixas diferentes: com meta > 0 o modelo precisa escolher...
	seedRegra(t, b, c, c.tipoA, "2026-12-01", "2026-12-31", 62, "14:00", "16:00", 1, "Tarde", []uuid.UUID{c.e1}, nil)
	texto := b.chamarErro(t, "definir_override", argsOverride(c, "2026-12-11", 2, nil))
	require.Contains(t, texto, "[entrada_invalida]")
	require.Contains(t, texto, "08:00–12:00")
	require.Contains(t, texto, "14:00–16:00")
	// ...com meta 0 a faixa não pesa e é aceita (a da primeira regra), com ressalva.
	pre = b.preview(t, "definir_override", argsOverride(c, "2026-12-11", 0, nil))
	require.Contains(t, mustJSON(t, pre["preview"].(map[string]any)["ressalvas"]), "com meta 0 a faixa não pesa")
}

func TestDefinirOverride_MetaZeroEhMetaZeroENaoNada(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-12-01", "2026-12-31")
	semearRegraBase(t, b, c, "2026-12-01", "2026-12-31")

	desc := descricaoDaTool(t, b.cfg, "definir_override")
	require.Contains(t, desc, "insercoes=0 NÃO é 'nada esperado'")
	require.Contains(t, desc, "vira bonus")

	pre := b.preview(t, "definir_override", argsOverride(c, "2026-12-07", 0, nil))
	require.Contains(t, mustJSON(t, pre["preview"].(map[string]any)["ressalvas"]), "TODA tocada desta célula-dia vira bonus")
	out := b.confirmarEExecutar(t, "definir_override", argsOverride(c, "2026-12-07", 0, nil))
	plano := planoDoResultado(out)
	require.EqualValues(t, 0, plano["saldo"].(map[string]any)["esperado"])
	require.Contains(t, mustJSON(t, plano["ressalvas"]), "O ajuste zera a meta")
}

// descricaoDaTool devolve a descrição da tool como o cliente MCP a vê.
func descricaoDaTool(t *testing.T, cfg ConfigServidor, nome string) string {
	t.Helper()
	ctx := context.Background()
	ct, st := sdk.NewInMemoryTransports()
	_, err := novoServidor(cfg, principalTeste).Connect(ctx, st, nil)
	require.NoError(t, err)
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "teste", Version: "1"}, nil).Connect(ctx, ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { cs.Close() })
	lista, err := cs.ListTools(ctx, nil)
	require.NoError(t, err)
	for _, tl := range lista.Tools {
		if tl.Name == nome {
			return tl.Description
		}
	}
	t.Fatalf("tool %s não registrada", nome)
	return ""
}

func TestDefinirOverride_ParidadeComARota(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	viaRota := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-12-01", "2026-12-31")
	viaTool := semearRegra(t, b, viaRota.tipoA, viaRota.tipoB, "2026-12-01", "2026-12-31")
	motivo := "Grade trocada"

	chamarHandlerCorpo(t, handlerAjustes(b).Upsert, http.MethodPut, "/campaigns/x/distribution-overrides",
		corpoAjusteRota(viaRota, "2026-12-07", 5, "13:00", "15:00", &motivo), map[string]string{"campaignID": viaRota.camp.String()}, b.admin)
	b.confirmarEExecutar(t, "definir_override", argsOverride(viaTool, "2026-12-07", 5,
		map[string]any{"inicio_horario": "13:00", "fim_horario": "15:00", "motivo": motivo}))

	rota, tool := linhasDeAjuste(t, b, viaRota.camp), linhasDeAjuste(t, b, viaTool.camp)
	require.Len(t, rota, 1)
	require.Equal(t, rota, tool, "o mesmo ajuste gravado pela rota e pela tool")
	require.Nil(t, criadoPorDoAjuste(t, b, viaRota.camp))
	require.Equal(t, &b.admin, criadoPorDoAjuste(t, b, viaTool.camp))
}

// Paridade de EFEITO: o Executar da tool (o que a Fatia 4 rodará depois da aprovação)
// numa célula-dia COM veiculação recategoriza igual à rota — meta zero e faixa estreita.
func TestDefinirOverride_ParidadeDeEfeitoNaRecategorizacao(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	viaRota := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-10-01", "2026-10-31")
	viaTool := semearRegra(t, b, viaRota.tipoA, viaRota.tipoB, "2026-10-01", "2026-10-31")
	for _, c := range []cenarioRegra{viaRota, viaTool} {
		semearTocadasDeQuarta(t, b, c)
		r := seedRegra(t, b, c, c.tipoA, "2026-10-01", "2026-10-31", 8, "08:00", "12:00", 1, "Quartas", []uuid.UUID{c.e1}, nil)
		require.NoError(t, catalog.NewDistributionRules(b.pool).RecategorizeForRule(b.ctx, r))
	}
	require.Equal(t, categoriasDasTocadas(t, b, viaRota.camp), categoriasDasTocadas(t, b, viaTool.camp))
	base := categoriasDasTocadas(t, b, viaRota.camp)
	require.Contains(t, base, "2026-10-14 10:00=in_slot/in_slot")

	for nome, caso := range map[string]struct {
		n        int
		ini, fim string
		quer     string
	}{
		"meta zero":              {0, "08:00", "12:00", "2026-10-14 10:00=bonus/bonus;2026-10-14 11:00=bonus/bonus;2026-10-14 15:00=bonus/bonus;2026-10-21 15:00=out_slot/out_slot"},
		"meta 2 numa faixa nova": {2, "14:30", "16:00", "2026-10-14 10:00=out_slot/out_slot;2026-10-14 11:00=out_slot/out_slot;2026-10-14 15:00=in_slot/in_slot;2026-10-21 15:00=out_slot/out_slot"},
	} {
		chamarHandlerCorpo(t, handlerAjustes(b).Upsert, http.MethodPut, "/campaigns/x/distribution-overrides",
			corpoAjusteRota(viaRota, "2026-10-14", caso.n, caso.ini, caso.fim, nil), map[string]string{"campaignID": viaRota.camp.String()}, b.admin)
		in := entradaDe[entradaDefinirOverride](t, argsOverride(viaTool, "2026-10-14", caso.n, map[string]any{"inicio_horario": caso.ini, "fim_horario": caso.fim}))
		_, err := escritaDefinirOverride(b.cfg).Executar(b.ctx, b.E, b.p, in)
		require.NoError(t, err, nome)

		rota, tool := categoriasDasTocadas(t, b, viaRota.camp), categoriasDasTocadas(t, b, viaTool.camp)
		require.Equal(t, rota, tool, nome)
		require.NotEqual(t, base, rota, "%s: a prova não é vazia, o ajuste mudou categoria", nome)
		require.Equal(t, caso.quer, rota, nome)
		require.Equal(t, linhasDeAjuste(t, b, viaRota.camp), linhasDeAjuste(t, b, viaTool.camp), nome)
	}
}

func TestDefinirOverride_Recusas(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-12-01", "2026-12-31")
	canc := semearRegra(t, b, c.tipoA, c.tipoB, "2026-12-01", "2026-12-31")
	mcptest.EstadoCampanha(t, b.ctx, b.pool, canc.camp, "cancelada")
	base := func(extra map[string]any) map[string]any { return argsOverride(c, "2026-12-07", 3, extra) }

	for nome, caso := range map[string]struct {
		args   map[string]any
		codigo string
		trecho string
	}{
		"campanha inválida":            {base(map[string]any{"campanha_id": "x"}), "[entrada_invalida]", ""},
		"campanha inexistente":         {base(map[string]any{"campanha_id": uuid.NewString()}), "[nao_encontrado]", ""},
		"campanha cancelada":           {argsOverride(canc, "2026-12-07", 3, nil), "[campanha_cancelada]", ""},
		"tipo inválido":                {base(map[string]any{"tipo_id": "x"}), "[entrada_invalida]", ""},
		"tipo inexistente":             {base(map[string]any{"tipo_id": uuid.NewString()}), "[nao_encontrado]", "tipo de material"},
		"emissora inválida":            {base(map[string]any{"emissora_id": "x"}), "[entrada_invalida]", ""},
		"emissora inexistente":         {base(map[string]any{"emissora_id": uuid.NewString()}), "[nao_encontrado]", "emissora"},
		"emissora fora da campanha":    {base(map[string]any{"emissora_id": c.e3.String()}), "[entrada_invalida]", "não é da campanha"},
		"data inválida":                {base(map[string]any{"data": "07/12/2026"}), "[entrada_invalida]", ""},
		"data vazia":                   {base(map[string]any{"data": ""}), "[entrada_invalida]", ""},
		"data antes da campanha":       {base(map[string]any{"data": "2026-11-30"}), "[entrada_invalida]", "fora do período da campanha"},
		"data depois da campanha":      {base(map[string]any{"data": "2027-01-01"}), "[entrada_invalida]", "fora do período da campanha"},
		"inserções negativas":          {base(map[string]any{"insercoes": -1}), "[entrada_invalida]", "0 a 100"},
		"mais de 100 inserções":        {base(map[string]any{"insercoes": 101}), "[entrada_invalida]", "0 a 100"},
		"só o início da faixa":         {base(map[string]any{"inicio_horario": "08:00"}), "[entrada_invalida]", "juntos"},
		"só o fim da faixa":            {base(map[string]any{"fim_horario": "10:00"}), "[entrada_invalida]", "juntos"},
		"faixa invertida":              {base(map[string]any{"inicio_horario": "10:00", "fim_horario": "08:00"}), "[entrada_invalida]", "maior que o início"},
		"faixa até 24:00":              {base(map[string]any{"inicio_horario": "08:00", "fim_horario": "24:00"}), "[entrada_invalida]", "24:00 não existe"},
		"faixa H:MM":                   {base(map[string]any{"inicio_horario": "8:00", "fim_horario": "10:00"}), "[entrada_invalida]", "HH:MM"},
		"motivo com mais de 500 runas": {base(map[string]any{"motivo": strings.Repeat("ã", 501)}), "[entrada_invalida]", "500"},
	} {
		texto := b.chamarErro(t, "definir_override", caso.args)
		require.Contains(t, texto, caso.codigo, nome)
		require.Contains(t, texto, caso.trecho, nome)
	}
	require.Empty(t, linhasDeAjuste(t, b, c.camp))
	require.Empty(t, linhasDeAjuste(t, b, canc.camp))
	require.Zero(t, b.totalAuditorias(t))
	require.Zero(t, tokensUsados(t, b))

	// Ajuste idêntico ao que a célula já tem: nada a alterar (e o token volta a valer, se for na confirmação).
	b.confirmarEExecutar(t, "definir_override", base(map[string]any{"inicio_horario": "08:00", "fim_horario": "10:00"}))
	texto := b.chamarErro(t, "definir_override", base(map[string]any{"inicio_horario": "08:00", "fim_horario": "10:00"}))
	require.Contains(t, texto, "[entrada_invalida]")
	require.Contains(t, texto, "já tem exatamente este ajuste")
}

// Extremos aceitos: meta 100, faixa 00:00–23:59, motivo de 500 runas, primeiro e último dia da campanha.
func TestDefinirOverride_ExtremosAceitos(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-12-01", "2026-12-31")
	b.confirmarEExecutar(t, "definir_override", argsOverride(c, "2026-12-01", 100, map[string]any{
		"inicio_horario": "00:00", "fim_horario": "23:59", "motivo": strings.Repeat("ã", 500)}))
	b.confirmarEExecutar(t, "definir_override", argsOverride(c, "2026-12-31", 0, map[string]any{"inicio_horario": "00:00", "fim_horario": "00:01"}))
	require.Len(t, linhasDeAjuste(t, b, c.camp), 2)
}

// ── escalada de definir ─────────────────────────────────────────────────

func TestDefinirOverride_Escalada(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b) // hoje = 2026-10-29
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-10-01", "2026-12-31")
	hoje := "2026-10-29"
	faixa := map[string]any{"inicio_horario": "08:00", "fim_horario": "10:00"}

	// Vizinhos que NÃO escalam: hoje e o futuro, sem veiculação alguma.
	for _, d := range []string{hoje, "2026-10-30", "2026-12-31"} {
		pre := b.chamar(t, "definir_override", argsOverride(c, d, 2, faixa))
		require.NotEmpty(t, pre["confirmacao"], d)
		require.NotContains(t, pre, "nivel", d)
	}

	// Gatilho (data): um dia que já passou, mesmo sem nenhuma veiculação.
	out := b.pedePendente(t, "definir_override", argsOverride(c, "2026-10-28", 2, faixa))
	require.Contains(t, out["motivo"], "já passou")
	require.NotContains(t, out["motivo"], "veiculação aprovada")
	require.Equal(t, "https://portal.teste/detections?campaign_id="+c.camp.String(), out["links"].(map[string]any)["tela"])
	require.Contains(t, textoDoPreview(t, out), "SUBSTITUI as regras", "o pedido pendente traz o preview")
	require.Empty(t, linhasDeAjuste(t, b, c.camp), "nada foi gravado")
	l := b.auditoria(t, "mcp.pendente.criada")
	require.Len(t, l, 1)
	require.Equal(t, "campaign", l[0].Recurso, "a linha de auditoria aponta a campanha (tipo/emissora/dia estão nos args)")
	require.Equal(t, c.camp, *l[0].RecursoID)
	require.Empty(t, b.auditoria(t, "mcp.escrita.definir_override"))
	require.Zero(t, tokensUsados(t, b))

	// Gatilho (veiculação): tocada aprovada NA célula-dia — hoje, para a data não escalar sozinha.
	mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, c.mat, c.e1, "bonus", mcptest.Quando(hoje, "10:00"))
	out = b.pedePendente(t, "definir_override", argsOverride(c, hoje, 2, faixa))
	require.Contains(t, out["motivo"], "já tem veiculação aprovada")
	require.NotContains(t, out["motivo"], "já passou")

	// Vizinhos da célula-dia: outra emissora, outro tipo e outro dia NÃO escalam.
	pre := b.chamar(t, "definir_override", argsOverride(c, hoje, 2, map[string]any{"emissora_id": c.e2.String(), "inicio_horario": "08:00", "fim_horario": "10:00"}))
	require.NotEmpty(t, pre["confirmacao"], "tocada em outra emissora")
	pre = b.chamar(t, "definir_override", argsOverride(c, hoje, 2, map[string]any{"tipo_id": c.tipoB.String(), "inicio_horario": "08:00", "fim_horario": "10:00"}))
	require.NotEmpty(t, pre["confirmacao"], "tocada de outro tipo")
	pre = b.chamar(t, "definir_override", argsOverride(c, "2026-10-30", 2, faixa))
	require.NotEmpty(t, pre["confirmacao"], "tocada em outro dia")

	// Tocada retratada não conta (mesmo filtro do recat): célula-dia só com ela não escala.
	vazia := "2026-10-31"
	idRetratada := mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, c.mat, c.e1, "bonus", mcptest.Quando(vazia, "10:00"))
	_, err := b.pool.Exec(b.ctx, `UPDATE detections SET retracted_at = now() WHERE id = $1`, idRetratada)
	require.NoError(t, err)
	pre = b.chamar(t, "definir_override", argsOverride(c, vazia, 2, faixa))
	require.NotEmpty(t, pre["confirmacao"], "tocada retratada não escala")

	// Os dois motivos juntos.
	mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, c.mat, c.e1, "bonus", mcptest.Quando("2026-10-27", "10:00"))
	out = b.pedePendente(t, "definir_override", argsOverride(c, "2026-10-27", 2, faixa))
	require.Contains(t, out["motivo"], "já passou")
	require.Contains(t, out["motivo"], "já tem veiculação aprovada")
}

// F3-2: o preview é confirma (hoje, sem tocada); entra uma tocada na célula-dia antes da
// confirmação — a confirmação barra e o token não é consumido.
func TestDefinirOverride_EscaladaNaConfirmacaoBarraENaoConsomeOToken(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-10-01", "2026-12-31")
	args := argsOverride(c, "2026-10-29", 2, map[string]any{"inicio_horario": "08:00", "fim_horario": "10:00"})
	pre := b.preview(t, "definir_override", args)

	mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, c.mat, c.e1, "bonus", mcptest.Quando("2026-10-29", "09:00"))
	args["confirmacao"] = pre["confirmacao"]
	out := b.chamar(t, "definir_override", args)

	require.Equal(t, "critico", out["nivel"])
	require.Empty(t, linhasDeAjuste(t, b, c.camp))
	require.Zero(t, tokensUsados(t, b))
}

// E o tempo passa: o preview era para amanhã e a confirmação chega no dia seguinte.
func TestDefinirOverride_DataViraPassadaNaConfirmacao(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-10-01", "2026-12-31")
	args := argsOverride(c, "2026-10-29", 2, map[string]any{"inicio_horario": "08:00", "fim_horario": "10:00"})
	pre := b.preview(t, "definir_override", args)

	b.E.Agora = func() time.Time { return mcptest.Quando("2026-10-30", "08:00") }
	args["confirmacao"] = pre["confirmacao"]
	out := b.chamar(t, "definir_override", args)

	require.Equal(t, "critico", out["nivel"])
	require.Contains(t, out["motivo"], "já passou")
	require.Zero(t, tokensUsados(t, b))
}

// Fronteira do dia em SP: às 22h e às 23h30 de SP já é o dia seguinte em UTC.
func TestDefinirOverride_FronteiraDoDiaEmSP(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-10-01", "2026-12-31")
	faixa := map[string]any{"inicio_horario": "08:00", "fim_horario": "10:00"}
	for _, hora := range []string{"22:00", "23:30"} {
		b.E.Agora = func() time.Time { return mcptest.Quando("2026-10-29", hora) }
		require.Equal(t, 30, b.E.Agora().UTC().Day(), "%s em SP já é dia 30 em UTC", hora)

		pre := b.chamar(t, "definir_override", argsOverride(c, "2026-10-29", 2, faixa))
		require.NotEmpty(t, pre["confirmacao"], "hoje (SP) às %s não escala", hora)
		b.pedePendente(t, "definir_override", argsOverride(c, "2026-10-28", 2, faixa))
		pre = b.chamar(t, "definir_override", argsOverride(c, "2026-10-30", 2, faixa))
		require.NotEmpty(t, pre["confirmacao"], "amanhã (SP) às %s não escala, mesmo sendo hoje em UTC", hora)
	}
}

// ── apagar_override ─────────────────────────────────────────────────────

func TestApagarOverride_PreviewNaoEscreveEConfirmarApaga(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-12-01", "2026-12-31")
	semearRegraBase(t, b, c, "2026-12-01", "2026-12-31")
	mcptest.Ajuste(t, b.ctx, b.pool, c.camp, c.tipoA, c.e1, "2026-12-07", 5, "13:00", "15:00")
	mcptest.Ajuste(t, b.ctx, b.pool, c.camp, c.tipoA, c.e1, "2026-12-08", 1, "13:00", "15:00")
	require.Contains(t, propriedades(esquemaDa(t, b.cfg, "apagar_override")), "confirmacao")
	args := argsApagarOverride(c, "2026-12-07")

	pre := b.preview(t, "apagar_override", args)

	require.Len(t, linhasDeAjuste(t, b, c.camp), 2, "o preview não escreve")
	require.Zero(t, b.totalAuditorias(t))
	texto := textoDoPreview(t, pre)
	for _, trecho := range []string{"meta 5, faixa 13:00–15:00", "removido", "2 (soma de 1 regra(s))", "AGORA (síncrono)"} {
		require.Contains(t, texto, trecho)
	}

	out := b.confirmarEExecutar(t, "apagar_override", args)

	linhas := linhasDeAjuste(t, b, c.camp)
	require.Len(t, linhas, 1)
	require.Contains(t, linhas[0], "2026-12-08", "só a célula-dia pedida foi apagada")
	res := out["resultado"].(map[string]any)
	require.Equal(t, true, res["apagado"])
	require.EqualValues(t, 5, res["ajuste_removido"].(map[string]any)["insercoes_esperadas"])
	plano := planoDoResultado(out)
	require.Equal(t, "regras", plano["meta_vem_de"], "a célula voltou às regras")
	require.EqualValues(t, 2, plano["saldo"].(map[string]any)["esperado"])
	b.exigeAuditoria(t, "mcp.escrita.apagar_override", "campaign", &c.camp)
}

// ajustesApagadosAntes simula o ajuste sumindo ENTRE o plano do Executar (que ainda o via) e o
// DELETE: apaga a linha de verdade, por fora, e só então chama o repositório real.
type ajustesApagadosAntes struct {
	acoes.RepositorioAjustes
	apaga func()
}

func (a ajustesApagadosAntes) Delete(ctx context.Context, c, t, s uuid.UUID, d time.Time) (int64, error) {
	a.apaga()
	return a.RepositorioAjustes.Delete(ctx, c, t, s, d)
}

// Revisão final: o DELETE que não apaga nenhuma linha é nao_encontrado — a tool não diz
// "apagado: true" nem grava auditoria de uma escrita que não houve. A rota HTTP segue 204.
func TestApagarOverride_AjusteQueSumiuEntreOPlanoEODeleteViraNaoEncontrado(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-12-01", "2026-12-31")
	semearRegraBase(t, b, c, "2026-12-01", "2026-12-31")
	mcptest.Ajuste(t, b.ctx, b.pool, c.camp, c.tipoA, c.e1, "2026-12-07", 5, "13:00", "15:00")
	args := argsApagarOverride(c, "2026-12-07")
	b.E.Acoes.Ajustes = ajustesApagadosAntes{RepositorioAjustes: b.E.Acoes.Ajustes, apaga: func() {
		_, err := b.pool.Exec(b.ctx, `DELETE FROM distribution_overrides WHERE campaign_id = $1`, c.camp)
		require.NoError(t, err)
	}}

	// Executar direto (é o que a Fatia 4 chama depois da aprovação): o plano ainda vê o ajuste,
	// o DELETE não apaga nada.
	in := entradaDe[entradaApagarOverride](t, args)
	f, err := escritaApagarOverride(b.cfg).Executar(b.ctx, b.E, b.p, in)
	var r *acoes.Recusa
	require.ErrorAs(t, err, &r)
	require.Equal(t, acoes.CodigoNaoEncontrado, r.Codigo)
	require.ErrorIs(t, err, acoes.ErrAlvoInexistente)
	require.Nil(t, f.Resultado, "nada de apagado: true")

	// Pela tool (preview → confirmar): erro nao_encontrado, token liberado, sem auditoria de sucesso.
	mcptest.Ajuste(t, b.ctx, b.pool, c.camp, c.tipoA, c.e1, "2026-12-07", 5, "13:00", "15:00")
	pre := b.preview(t, "apagar_override", args)
	args["confirmacao"] = pre["confirmacao"]
	texto := b.chamarErro(t, "apagar_override", args)
	require.Contains(t, texto, "[nao_encontrado]")
	require.Zero(t, tokensUsados(t, b), "a recusa de negócio devolve o token")
	require.Empty(t, b.auditoria(t, "mcp.escrita.apagar_override"))
}

func TestApagarOverride_ParidadeComARota(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	viaRota := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-12-01", "2026-12-31")
	viaTool := semearRegra(t, b, viaRota.tipoA, viaRota.tipoB, "2026-12-01", "2026-12-31")
	for _, c := range []cenarioRegra{viaRota, viaTool} {
		mcptest.Ajuste(t, b.ctx, b.pool, c.camp, c.tipoA, c.e1, "2026-12-07", 5, "13:00", "15:00")
		mcptest.Ajuste(t, b.ctx, b.pool, c.camp, c.tipoA, c.e1, "2026-12-08", 1, "13:00", "15:00")
	}

	chamarHandlerCorpo(t, handlerAjustes(b).Delete, http.MethodDelete, "/campaigns/x/distribution-overrides",
		corpoAjusteRota(viaRota, "2026-12-07", 0, "00:00", "00:00", nil), map[string]string{"campaignID": viaRota.camp.String()}, b.admin)
	b.confirmarEExecutar(t, "apagar_override", argsApagarOverride(viaTool, "2026-12-07"))

	rota, tool := linhasDeAjuste(t, b, viaRota.camp), linhasDeAjuste(t, b, viaTool.camp)
	require.Len(t, rota, 1)
	require.Equal(t, rota, tool)
}

// Paridade de EFEITO: apagar um ajuste que zerava a meta devolve a célula às regras — o
// Executar numa célula-dia COM veiculação recategoriza igual à rota.
func TestApagarOverride_ParidadeDeEfeitoNaRecategorizacao(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	viaRota := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-10-01", "2026-10-31")
	viaTool := semearRegra(t, b, viaRota.tipoA, viaRota.tipoB, "2026-10-01", "2026-10-31")
	for _, c := range []cenarioRegra{viaRota, viaTool} {
		semearTocadasDeQuarta(t, b, c)
		r := seedRegra(t, b, c, c.tipoA, "2026-10-01", "2026-10-31", 8, "08:00", "12:00", 1, "Quartas", []uuid.UUID{c.e1}, nil)
		require.NoError(t, catalog.NewDistributionRules(b.pool).RecategorizeForRule(b.ctx, r))
		semearAjusteDireto(t, b, c, "2026-10-14", 0, "08:00", "12:00") // meta zero: tudo bonus
	}
	zerada := categoriasDasTocadas(t, b, viaRota.camp)
	require.Equal(t, zerada, categoriasDasTocadas(t, b, viaTool.camp))
	require.Contains(t, zerada, "2026-10-14 10:00=bonus/bonus")

	chamarHandlerCorpo(t, handlerAjustes(b).Delete, http.MethodDelete, "/campaigns/x/distribution-overrides",
		corpoAjusteRota(viaRota, "2026-10-14", 0, "00:00", "00:00", nil), map[string]string{"campaignID": viaRota.camp.String()}, b.admin)
	in := entradaDe[entradaApagarOverride](t, argsApagarOverride(viaTool, "2026-10-14"))
	_, err := escritaApagarOverride(b.cfg).Executar(b.ctx, b.E, b.p, in)
	require.NoError(t, err)

	rota, tool := categoriasDasTocadas(t, b, viaRota.camp), categoriasDasTocadas(t, b, viaTool.camp)
	require.Equal(t, rota, tool)
	require.Contains(t, rota, "2026-10-14 10:00=in_slot/in_slot", "a regra voltou a valer e a primeira tocada preenche a vaga")
	require.Empty(t, linhasDeAjuste(t, b, viaRota.camp))
	require.Empty(t, linhasDeAjuste(t, b, viaTool.camp))
}

func TestApagarOverride_Escalada(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b) // hoje = 2026-10-29
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-10-01", "2026-12-31")
	hoje := "2026-10-29"
	mcptest.Ajuste(t, b.ctx, b.pool, c.camp, c.tipoA, c.e1, hoje, 2, "08:00", "10:00")
	mcptest.Ajuste(t, b.ctx, b.pool, c.camp, c.tipoA, c.e1, "2026-10-30", 2, "08:00", "10:00")
	mcptest.Ajuste(t, b.ctx, b.pool, c.camp, c.tipoA, c.e1, "2026-10-28", 2, "08:00", "10:00")

	// Vizinhos: hoje e amanhã, sem veiculação, confirmam.
	for _, d := range []string{hoje, "2026-10-30"} {
		pre := b.chamar(t, "apagar_override", argsApagarOverride(c, d))
		require.NotEmpty(t, pre["confirmacao"], d)
	}
	// Gatilho (data): o dia já passou.
	out := b.pedePendente(t, "apagar_override", argsApagarOverride(c, "2026-10-28"))
	require.Contains(t, out["motivo"], "já passou")
	require.Equal(t, "https://portal.teste/detections?campaign_id="+c.camp.String(), out["links"].(map[string]any)["tela"])
	require.Len(t, linhasDeAjuste(t, b, c.camp), 3, "nada foi apagado")
	// Gatilho (veiculação na célula-dia de hoje).
	mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, c.mat, c.e1, "bonus", mcptest.Quando(hoje, "09:00"))
	out = b.pedePendente(t, "apagar_override", argsApagarOverride(c, hoje))
	require.Contains(t, out["motivo"], "já tem veiculação aprovada")
	// O vizinho de amanhã segue confirmando.
	pre := b.chamar(t, "apagar_override", argsApagarOverride(c, "2026-10-30"))
	require.NotEmpty(t, pre["confirmacao"])
	require.Zero(t, tokensUsados(t, b))
	require.Len(t, b.auditoria(t, "mcp.pendente.criada"), 2)
	require.Empty(t, b.auditoria(t, "mcp.escrita.apagar_override"))
}

func TestApagarOverride_EscaladaNaConfirmacaoBarraENaoConsomeOToken(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-10-01", "2026-12-31")
	mcptest.Ajuste(t, b.ctx, b.pool, c.camp, c.tipoA, c.e1, "2026-10-29", 2, "08:00", "10:00")
	args := argsApagarOverride(c, "2026-10-29")
	pre := b.preview(t, "apagar_override", args)

	mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, c.mat, c.e1, "bonus", mcptest.Quando("2026-10-29", "09:00"))
	args["confirmacao"] = pre["confirmacao"]
	out := b.chamar(t, "apagar_override", args)

	require.Equal(t, "critico", out["nivel"])
	require.Len(t, linhasDeAjuste(t, b, c.camp), 1)
	require.Zero(t, tokensUsados(t, b))
}

func TestApagarOverride_FronteiraDoDiaEmSP(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-10-01", "2026-12-31")
	for _, d := range []string{"2026-10-28", "2026-10-29", "2026-10-30"} {
		mcptest.Ajuste(t, b.ctx, b.pool, c.camp, c.tipoA, c.e1, d, 2, "08:00", "10:00")
	}
	for _, hora := range []string{"22:00", "23:30"} {
		b.E.Agora = func() time.Time { return mcptest.Quando("2026-10-29", hora) }
		pre := b.chamar(t, "apagar_override", argsApagarOverride(c, "2026-10-29"))
		require.NotEmpty(t, pre["confirmacao"], "hoje (SP) às %s não escala", hora)
		b.pedePendente(t, "apagar_override", argsApagarOverride(c, "2026-10-28"))
		pre = b.chamar(t, "apagar_override", argsApagarOverride(c, "2026-10-30"))
		require.NotEmpty(t, pre["confirmacao"], "amanhã (SP) às %s não escala, mesmo sendo hoje em UTC", hora)
	}
}

func TestApagarOverride_Recusas(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-10-01", "2026-12-31")
	canc := semearRegra(t, b, c.tipoA, c.tipoB, "2026-12-01", "2026-12-31")
	mcptest.Ajuste(t, b.ctx, b.pool, canc.camp, canc.tipoA, canc.e1, "2026-12-07", 2, "08:00", "10:00")
	mcptest.EstadoCampanha(t, b.ctx, b.pool, canc.camp, "cancelada")
	mcptest.Ajuste(t, b.ctx, b.pool, c.camp, c.tipoA, c.e1, "2026-12-07", 2, "08:00", "10:00")

	for nome, caso := range map[string]struct {
		args   map[string]any
		codigo string
		trecho string
	}{
		"ajuste inexistente (a rota devolveria 204)":     {argsApagarOverride(c, "2026-12-08"), "[nao_encontrado]", "ajuste do dia"},
		"ajuste inexistente num dia passado (não barra)": {argsApagarOverride(c, "2026-10-02"), "[nao_encontrado]", "ajuste do dia"},
		"outra emissora sem ajuste":                      {map[string]any{"campanha_id": c.camp.String(), "tipo_id": c.tipoA.String(), "emissora_id": c.e2.String(), "data": "2026-12-07"}, "[nao_encontrado]", ""},
		"outro tipo sem ajuste":                          {map[string]any{"campanha_id": c.camp.String(), "tipo_id": c.tipoB.String(), "emissora_id": c.e1.String(), "data": "2026-12-07"}, "[nao_encontrado]", ""},
		"campanha cancelada":                             {argsApagarOverride(canc, "2026-12-07"), "[campanha_cancelada]", ""},
		"campanha inválida":                              {map[string]any{"campanha_id": "x", "tipo_id": c.tipoA.String(), "emissora_id": c.e1.String(), "data": "2026-12-07"}, "[entrada_invalida]", ""},
		"campanha inexistente":                           {map[string]any{"campanha_id": uuid.NewString(), "tipo_id": c.tipoA.String(), "emissora_id": c.e1.String(), "data": "2026-12-07"}, "[nao_encontrado]", ""},
		"tipo inexistente":                               {map[string]any{"campanha_id": c.camp.String(), "tipo_id": uuid.NewString(), "emissora_id": c.e1.String(), "data": "2026-12-07"}, "[nao_encontrado]", "tipo de material"},
		"emissora inexistente":                           {map[string]any{"campanha_id": c.camp.String(), "tipo_id": c.tipoA.String(), "emissora_id": uuid.NewString(), "data": "2026-12-07"}, "[nao_encontrado]", "emissora"},
		"data inválida":                                  {argsApagarOverride(c, "07-12-2026"), "[entrada_invalida]", ""},
	} {
		texto := b.chamarErro(t, "apagar_override", caso.args)
		require.Contains(t, texto, caso.codigo, nome)
		require.Contains(t, texto, caso.trecho, nome)
	}
	require.Len(t, linhasDeAjuste(t, b, c.camp), 1)
	require.Len(t, linhasDeAjuste(t, b, canc.camp), 1, "o ajuste da cancelada ficou")
	require.Zero(t, b.totalAuditorias(t))
	require.Zero(t, tokensUsados(t, b))
}

// O ajuste some entre o preview e a confirmação: recusa de negócio e o token volta a valer.
func TestApagarOverride_RecusaNaConfirmacaoLiberaOToken(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-12-01", "2026-12-31")
	mcptest.Ajuste(t, b.ctx, b.pool, c.camp, c.tipoA, c.e1, "2026-12-07", 2, "08:00", "10:00")
	args := argsApagarOverride(c, "2026-12-07")
	pre := b.preview(t, "apagar_override", args)
	_, err := b.pool.Exec(b.ctx, `DELETE FROM distribution_overrides WHERE campaign_id = $1`, c.camp)
	require.NoError(t, err)

	args["confirmacao"] = pre["confirmacao"]
	require.Contains(t, b.chamarErro(t, "apagar_override", args), "[nao_encontrado]")
	require.Zero(t, tokensUsados(t, b))
}

// Um ajuste que a rota gravou FORA do período da campanha (ela aceita) ainda pode ser limpo pelo MCP.
func TestApagarOverride_LimpaAjusteGravadoForaDoPeriodo(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-12-01", "2026-12-31")
	mcptest.Ajuste(t, b.ctx, b.pool, c.camp, c.tipoA, c.e1, "2027-02-10", 2, "08:00", "10:00")

	b.confirmarEExecutar(t, "apagar_override", argsApagarOverride(c, "2027-02-10"))

	require.Empty(t, linhasDeAjuste(t, b, c.camp))
}
