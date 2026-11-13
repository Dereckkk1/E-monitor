package mcp

// Task 15: vincular_material, desvincular_material e editar_emissoras_material
// (sem escalada: F3-4) e o teste de prova de que o passado não muda.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/acoes"
	"radiocheck/internal/api/handlers"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

// ── fixtures e leitura ──────────────────────────────────────────────────

// cenarioVinc: cliente, três emissoras, campanha (E1 e E2 como alvo; E3 fora),
// um tipo e um material do cliente AINDA NÃO vinculado.
type cenarioVinc struct {
	cli, e1, e2, e3, camp, tipo, mat uuid.UUID
}

func semearVinc(t *testing.T, b *bancadaTool, estado string) cenarioVinc {
	t.Helper()
	c := cenarioVinc{}
	c.cli = mcptest.Cliente(t, b.ctx, b.pool, "Cliente "+uuid.NewString()[:6])
	c.e1 = mcptest.Emissora(t, b.ctx, b.pool, "Rádio 1", 1000)
	c.e2 = mcptest.Emissora(t, b.ctx, b.pool, "Rádio 2", 1000)
	c.e3 = mcptest.Emissora(t, b.ctx, b.pool, "Rádio 3", 1000)
	c.camp = mcptest.Campanha(t, b.ctx, b.pool, c.cli, "Campanha Vínculo", "2026-10-01", "2026-11-30", c.e1, c.e2)
	if estado != "" {
		mcptest.EstadoCampanha(t, b.ctx, b.pool, c.camp, estado)
	}
	c.tipo = mcptest.TipoMaterial(t, b.ctx, b.pool, "Spot")
	c.mat = mcptest.Material(t, b.ctx, b.pool, c.cli, c.tipo, "Spot Vínculo")
	return c
}

// vinculoGravado devolve as emissoras do vínculo e se ele existe.
func vinculoGravado(t *testing.T, b *bancadaTool, camp, mat uuid.UUID) ([]uuid.UUID, bool) {
	t.Helper()
	var est []uuid.UUID
	err := b.pool.QueryRow(b.ctx, `SELECT target_stations FROM campaign_materials WHERE campaign_id = $1 AND material_id = $2`, camp, mat).Scan(&est)
	if err != nil {
		require.ErrorContains(t, err, "no rows")
		return nil, false
	}
	return est, true
}

func argsVinc(c cenarioVinc, extra map[string]any) map[string]any {
	a := map[string]any{"campanha_id": c.camp.String(), "material_id": c.mat.String()}
	for k, v := range extra {
		a[k] = v
	}
	return a
}

func vinculoDoResultado(out map[string]any) map[string]any {
	return out["resultado"].(map[string]any)["vinculo"].(map[string]any)
}

// ── vincular_material ───────────────────────────────────────────────────

func TestVincularMaterial_PreviewNaoEscreveEConfirmarVinculaEmTodasAsEmissoras(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearVinc(t, b, "ativa")
	require.Contains(t, propriedades(esquemaDa(t, b.cfg, "vincular_material")), "confirmacao")

	pre := b.preview(t, "vincular_material", argsVinc(c, nil))

	_, existe := vinculoGravado(t, b, c.camp, c.mat)
	require.False(t, existe, "o preview não escreve")
	require.Empty(t, b.sup.Chamadas())
	require.Zero(t, b.totalAuditorias(t))
	texto := textoDoPreview(t, pre)
	require.Contains(t, texto, "Rádio 1")
	require.Contains(t, texto, "recarregam agora", "campanha ativa: recarga imediata")
	require.Contains(t, texto, "Não recategoriza")

	out := b.confirmarEExecutar(t, "vincular_material", argsVinc(c, nil))

	est, existe := vinculoGravado(t, b, c.camp, c.mat)
	require.True(t, existe)
	require.Equal(t, []uuid.UUID{c.e1, c.e2}, est, "omitir emissora_ids = todas as da campanha (o que a tela faz)")
	require.Equal(t, 1, b.sup.Contagem("Reload", c.camp))
	v := vinculoDoResultado(out) // o formato de listar_materiais_campanha
	require.Equal(t, c.mat.String(), v["material"].(map[string]any)["id"])
	require.EqualValues(t, 2, v["emissoras_total"])
	require.Equal(t, c.tipo.String(), v["tipo"].(map[string]any)["id"])
	b.exigeAuditoria(t, "mcp.escrita.vincular_material", "campaign_material", &c.camp)
}

func TestVincularMaterial_EmissorasEscolhidas(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearVinc(t, b, "ativa")
	b.confirmarEExecutar(t, "vincular_material", argsVinc(c, map[string]any{"emissora_ids": strsDe(c.e2)}))
	est, _ := vinculoGravado(t, b, c.camp, c.mat)
	require.Equal(t, []uuid.UUID{c.e2}, est)
}

func TestVincularMaterial_ParidadeComARota(t *testing.T) {
	b := novaBancadaTool(t)
	viaRota := semearVinc(t, b, "ativa")
	viaTool := semearVinc(t, b, "ativa")
	supRota := &mcptest.SupervisorFalso{}
	h := &handlers.CampaignMaterialsHandler{Repo: catalog.NewCampaignMaterials(b.pool), Supervisor: supRota}

	// A tela manda TODAS as emissoras da campanha explicitamente.
	chamarHandlerCorpo(t, h.Link, http.MethodPost, "/campaigns/x/materials", map[string]any{
		"material_id": viaRota.mat.String(), "target_stations": []string{viaRota.e1.String(), viaRota.e2.String()},
	}, map[string]string{"campaignID": viaRota.camp.String()}, b.admin)
	b.confirmarEExecutar(t, "vincular_material", argsVinc(viaTool, nil))

	estRota, _ := vinculoGravado(t, b, viaRota.camp, viaRota.mat)
	estTool, _ := vinculoGravado(t, b, viaTool.camp, viaTool.mat)
	require.Equal(t, posicoes(t, estRota, []uuid.UUID{viaRota.e1, viaRota.e2}), posicoes(t, estTool, []uuid.UUID{viaTool.e1, viaTool.e2}))
	require.Equal(t, supRota.Contagem("Reload", viaRota.camp), b.sup.Contagem("Reload", viaTool.camp), "o mesmo Reload da campanha")
	require.Equal(t, 1, b.sup.Contagem("Reload", viaTool.camp))
	require.Len(t, b.sup.Chamadas(), 1)
	require.Len(t, supRota.Chamadas(), 1)
}

func TestVincularMaterial_EfeitoPorEstadoERessalvasDoMaterial(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearVinc(t, b, "programada")

	pre := b.preview(t, "vincular_material", argsVinc(c, nil))
	require.Contains(t, textoDoPreview(t, pre), "quando ela ativar")
	// O material semeado tem fingerprint 'pending': ressalva.
	require.Contains(t, mustJSON(t, pre["preview"].(map[string]any)["ressalvas"]), "fingerprint")

	mcptest.EstadoCampanha(t, b.ctx, b.pool, c.camp, "concluida")
	pre = b.preview(t, "vincular_material", argsVinc(c, nil))
	require.Contains(t, textoDoPreview(t, pre), "não recebe veiculações novas")

	// Pronto e com tipo: nenhuma ressalva. Sem tipo: ressalva do tipo.
	_, err := b.pool.Exec(b.ctx, `UPDATE materials SET fingerprint_status = 'ready' WHERE id = $1`, c.mat)
	require.NoError(t, err)
	pre = b.preview(t, "vincular_material", argsVinc(c, nil))
	require.Empty(t, pre["preview"].(map[string]any)["ressalvas"])
	_, err = b.pool.Exec(b.ctx, `UPDATE materials SET type_id = NULL WHERE id = $1`, c.mat)
	require.NoError(t, err)
	pre = b.preview(t, "vincular_material", argsVinc(c, nil))
	require.Contains(t, mustJSON(t, pre["preview"].(map[string]any)["ressalvas"]), "não tem tipo")
}

func TestVincularMaterial_Recusas(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearVinc(t, b, "ativa")
	canc := semearVinc(t, b, "cancelada")
	semEmissoras := semearVinc(t, b, "ativa")
	_, err := b.pool.Exec(b.ctx, `UPDATE campaigns SET target_stations = '{}' WHERE id = $1`, semEmissoras.camp)
	require.NoError(t, err)
	outroCli := mcptest.Cliente(t, b.ctx, b.pool, "Outro cliente")
	matAlheio := mcptest.Material(t, b.ctx, b.pool, outroCli, c.tipo, "Spot alheio")
	jaVinculado := semearVinc(t, b, "ativa")
	mcptest.Vincular(t, b.ctx, b.pool, jaVinculado.camp, jaVinculado.mat, jaVinculado.e1)
	muitas := make([]any, 501)
	for i := range muitas {
		muitas[i] = uuid.NewString()
	}

	for nome, caso := range map[string]struct {
		args   map[string]any
		codigo string
		trecho string
	}{
		"campanha inválida":                             {map[string]any{"campanha_id": "x", "material_id": c.mat.String()}, "[entrada_invalida]", ""},
		"campanha inexistente":                          {map[string]any{"campanha_id": uuid.NewString(), "material_id": c.mat.String()}, "[nao_encontrado]", ""},
		"campanha cancelada":                            {argsVinc(canc, nil), "[campanha_cancelada]", ""},
		"material inválido":                             {map[string]any{"campanha_id": c.camp.String(), "material_id": "x"}, "[entrada_invalida]", ""},
		"material inexistente":                          {map[string]any{"campanha_id": c.camp.String(), "material_id": uuid.NewString()}, "[nao_encontrado]", ""},
		"material de outro cliente":                     {map[string]any{"campanha_id": c.camp.String(), "material_id": matAlheio.String()}, "[entrada_invalida]", "outro cliente"},
		"emissora fora da campanha":                     {argsVinc(c, map[string]any{"emissora_ids": strsDe(c.e3)}), "[entrada_invalida]", "fora da campanha"},
		"emissora inválida":                             {argsVinc(c, map[string]any{"emissora_ids": []any{"x"}}), "[entrada_invalida]", ""},
		"mais de 500 emissoras":                         {argsVinc(c, map[string]any{"emissora_ids": muitas}), "[entrada_invalida]", ""},
		"já vinculado":                                  {argsVinc(jaVinculado, nil), "[entrada_invalida]", "sobrescreveria"},
		"campanha sem emissoras e emissora_ids omitido": {argsVinc(semEmissoras, nil), "[entrada_invalida]", "não tem emissoras-alvo"},
	} {
		texto := b.chamarErro(t, "vincular_material", caso.args)
		require.Contains(t, texto, caso.codigo, nome)
		require.Contains(t, texto, caso.trecho, nome)
	}
	_, existe := vinculoGravado(t, b, c.camp, c.mat)
	require.False(t, existe)
	est, _ := vinculoGravado(t, b, jaVinculado.camp, jaVinculado.mat)
	require.Equal(t, []uuid.UUID{jaVinculado.e1}, est, "o vínculo existente não foi sobrescrito")
	require.Empty(t, b.sup.Chamadas())
	require.Zero(t, b.totalAuditorias(t))
}

// ── desvincular_material ────────────────────────────────────────────────

func TestDesvincularMaterial_PreviewNaoEscreveEConfirmarRemove(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearVinc(t, b, "ativa")
	mcptest.Vincular(t, b.ctx, b.pool, c.camp, c.mat, c.e1, c.e2)
	require.Contains(t, propriedades(esquemaDa(t, b.cfg, "desvincular_material")), "confirmacao")

	pre := b.preview(t, "desvincular_material", argsVinc(c, nil))

	_, existe := vinculoGravado(t, b, c.camp, c.mat)
	require.True(t, existe, "o preview não escreve")
	require.Empty(t, b.sup.Chamadas())
	require.Zero(t, b.totalAuditorias(t))
	texto := textoDoPreview(t, pre)
	require.Contains(t, texto, "o passado não muda")
	require.Contains(t, texto, "deixa de ser detectado")

	out := b.confirmarEExecutar(t, "desvincular_material", argsVinc(c, nil))

	_, existe = vinculoGravado(t, b, c.camp, c.mat)
	require.False(t, existe)
	require.Equal(t, 1, b.sup.Contagem("Reload", c.camp))
	res := out["resultado"].(map[string]any)
	require.Equal(t, true, res["desvinculado"])
	require.EqualValues(t, 0, res["materiais_restantes"])
	b.exigeAuditoria(t, "mcp.escrita.desvincular_material", "campaign_material", &c.camp)
}

func TestDesvincularMaterial_ParidadeComARota(t *testing.T) {
	b := novaBancadaTool(t)
	viaRota := semearVinc(t, b, "ativa")
	viaTool := semearVinc(t, b, "ativa")
	for _, c := range []cenarioVinc{viaRota, viaTool} {
		mcptest.Vincular(t, b.ctx, b.pool, c.camp, c.mat, c.e1, c.e2)
	}
	supRota := &mcptest.SupervisorFalso{}
	h := &handlers.CampaignMaterialsHandler{Repo: catalog.NewCampaignMaterials(b.pool), Supervisor: supRota}

	chamarHandlerCorpo(t, h.Unlink, http.MethodDelete, "/campaigns/x/materials/y", nil,
		map[string]string{"campaignID": viaRota.camp.String(), "materialID": viaRota.mat.String()}, b.admin)
	b.confirmarEExecutar(t, "desvincular_material", argsVinc(viaTool, nil))

	_, rota := vinculoGravado(t, b, viaRota.camp, viaRota.mat)
	_, tool := vinculoGravado(t, b, viaTool.camp, viaTool.mat)
	require.False(t, rota)
	require.False(t, tool)
	require.Equal(t, 1, supRota.Contagem("Reload", viaRota.camp))
	require.Equal(t, 1, b.sup.Contagem("Reload", viaTool.camp))
	require.Len(t, b.sup.Chamadas(), 1)
}

func TestDesvincularMaterial_RessalvasDasRegras(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearVinc(t, b, "ativa")
	mcptest.Vincular(t, b.ctx, b.pool, c.camp, c.mat, c.e1)
	// Regra geral do tipo (vigente) + regra escopada a este material.
	mcptest.Regra(t, b.ctx, b.pool, c.camp, c.tipo, c.e1, "2026-10-01", "2026-11-30", 127, "08:00", "18:00", 2)
	_, err := catalog.NewDistributionRules(b.pool).Create(b.ctx, catalog.CreateDistributionRuleInput{
		CampaignID: c.camp, TypeID: c.tipo, StationIDs: []uuid.UUID{c.e1}, MaterialIDs: []uuid.UUID{c.mat}, Name: "Só deste",
		StartDate: mcptest.Dia("2026-10-01"), EndDate: mcptest.Dia("2026-11-30"), WeekdayMask: 127,
		TimeStart: "09:00", TimeEnd: "10:00", PlaysPerDay: 1,
	})
	require.NoError(t, err)
	// Regra já terminada do tipo: não conta.
	mcptest.Regra(t, b.ctx, b.pool, c.camp, c.tipo, c.e1, "2026-09-01", "2026-09-30", 127, "08:00", "18:00", 2)

	pre := b.preview(t, "desvincular_material", argsVinc(c, nil))
	r := mustJSON(t, pre["preview"].(map[string]any)["ressalvas"])
	require.Contains(t, r, "Só deste")
	require.Contains(t, r, "é escopada a este material")
	require.Contains(t, r, "único material do seu tipo")
	require.Contains(t, r, "as 2 regra(s)", "as duas vigentes, sem a que já terminou")

	// Com OUTRO material do mesmo tipo vinculado, as regras do tipo seguem cumpríveis.
	outro := mcptest.Material(t, b.ctx, b.pool, c.cli, c.tipo, "Spot irmão")
	mcptest.Vincular(t, b.ctx, b.pool, c.camp, outro, c.e1)
	pre = b.preview(t, "desvincular_material", argsVinc(c, nil))
	r = mustJSON(t, pre["preview"].(map[string]any)["ressalvas"])
	require.NotContains(t, r, "único material do seu tipo")
	require.Contains(t, r, "é escopada a este material")
}

func TestDesvincularMaterial_Recusas(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearVinc(t, b, "ativa") // o material NÃO está vinculado
	canc := semearVinc(t, b, "cancelada")
	mcptest.Vincular(t, b.ctx, b.pool, canc.camp, canc.mat, canc.e1)
	outroCli := mcptest.Cliente(t, b.ctx, b.pool, "Outro cliente")
	matAlheio := mcptest.Material(t, b.ctx, b.pool, outroCli, c.tipo, "Spot alheio")

	for nome, caso := range map[string]struct {
		args   map[string]any
		codigo string
	}{
		"vínculo inexistente":                   {argsVinc(c, nil), "[nao_encontrado]"},
		"campanha inválida":                     {map[string]any{"campanha_id": "x", "material_id": c.mat.String()}, "[entrada_invalida]"},
		"campanha inexistente":                  {map[string]any{"campanha_id": uuid.NewString(), "material_id": c.mat.String()}, "[nao_encontrado]"},
		"campanha cancelada":                    {argsVinc(canc, nil), "[campanha_cancelada]"},
		"material inexistente":                  {map[string]any{"campanha_id": c.camp.String(), "material_id": uuid.NewString()}, "[nao_encontrado]"},
		"material de outro cliente sem vínculo": {map[string]any{"campanha_id": c.camp.String(), "material_id": matAlheio.String()}, "[nao_encontrado]"},
	} {
		require.Contains(t, b.chamarErro(t, "desvincular_material", caso.args), caso.codigo, nome)
	}
	_, existe := vinculoGravado(t, b, canc.camp, canc.mat)
	require.True(t, existe, "o vínculo da cancelada ficou")
	require.Empty(t, b.sup.Chamadas())
	require.Zero(t, b.totalAuditorias(t))
}

// A rota já deixou vincular material de outro cliente: esse vínculo antigo precisa poder ser
// editado e desfeito pelo MCP (a recusa do cliente alheio vale só para CRIAR o vínculo).
func TestVinculoAntigoDeMaterialDeOutroCliente_PodeSerEditadoEDesfeito(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearVinc(t, b, "ativa")
	outroCli := mcptest.Cliente(t, b.ctx, b.pool, "Outro cliente")
	alheio := mcptest.Material(t, b.ctx, b.pool, outroCli, c.tipo, "Spot alheio")
	mcptest.Vincular(t, b.ctx, b.pool, c.camp, alheio, c.e1)
	args := map[string]any{"campanha_id": c.camp.String(), "material_id": alheio.String()}

	b.confirmarEExecutar(t, "editar_emissoras_material", map[string]any{"campanha_id": c.camp.String(), "material_id": alheio.String(), "adicionar": strsDe(c.e2)})
	est, _ := vinculoGravado(t, b, c.camp, alheio)
	require.Equal(t, []uuid.UUID{c.e1, c.e2}, est)
	b.confirmarEExecutar(t, "desvincular_material", args)
	_, existe := vinculoGravado(t, b, c.camp, alheio)
	require.False(t, existe)
	// ...mas CRIAR esse vínculo continua recusado.
	require.Contains(t, b.chamarErro(t, "vincular_material", args), "outro cliente")
}

// O token liberado pela recusa de negócio: o vínculo some entre o preview e a confirmação.
func TestDesvincularMaterial_RecusaNaConfirmacaoLiberaOToken(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearVinc(t, b, "ativa")
	mcptest.Vincular(t, b.ctx, b.pool, c.camp, c.mat, c.e1)
	pre := b.preview(t, "desvincular_material", argsVinc(c, nil))
	_, err := b.pool.Exec(b.ctx, `DELETE FROM campaign_materials WHERE campaign_id = $1`, c.camp)
	require.NoError(t, err)

	com := argsVinc(c, map[string]any{"confirmacao": pre["confirmacao"]})
	require.Contains(t, b.chamarErro(t, "desvincular_material", com), "[nao_encontrado]")
	require.Zero(t, tokensUsados(t, b), "a recusa de negócio devolve o token")
}

// ── editar_emissoras_material ───────────────────────────────────────────

func TestEditarEmissorasMaterial_PreviewNaoEscreveEConfirmarGrava(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearVinc(t, b, "ativa")
	mcptest.Vincular(t, b.ctx, b.pool, c.camp, c.mat, c.e1)
	args := argsVinc(c, map[string]any{"adicionar": strsDe(c.e2), "remover": strsDe(c.e1)})
	require.Contains(t, propriedades(esquemaDa(t, b.cfg, "editar_emissoras_material")), "confirmacao")

	pre := b.preview(t, "editar_emissoras_material", args)

	est, _ := vinculoGravado(t, b, c.camp, c.mat)
	require.Equal(t, []uuid.UUID{c.e1}, est, "o preview não escreve")
	require.Empty(t, b.sup.Chamadas())
	require.Zero(t, b.totalAuditorias(t))
	texto := textoDoPreview(t, pre)
	require.Contains(t, texto, "Rádio 2")
	require.Contains(t, texto, "Veiculação manual")

	out := b.confirmarEExecutar(t, "editar_emissoras_material", args)

	est, _ = vinculoGravado(t, b, c.camp, c.mat)
	require.Equal(t, []uuid.UUID{c.e2}, est)
	require.Equal(t, 1, b.sup.Contagem("Reload", c.camp))
	require.EqualValues(t, 1, vinculoDoResultado(out)["emissoras_total"])
	b.exigeAuditoria(t, "mcp.escrita.editar_emissoras_material", "campaign_material", &c.camp)
}

func TestEditarEmissorasMaterial_ParidadeComARota(t *testing.T) {
	b := novaBancadaTool(t)
	viaRota := semearVinc(t, b, "ativa")
	viaTool := semearVinc(t, b, "ativa")
	for _, c := range []cenarioVinc{viaRota, viaTool} {
		mcptest.Vincular(t, b.ctx, b.pool, c.camp, c.mat, c.e1)
	}
	supRota := &mcptest.SupervisorFalso{}
	h := &handlers.CampaignMaterialsHandler{Repo: catalog.NewCampaignMaterials(b.pool), Supervisor: supRota}

	chamarHandlerCorpo(t, h.UpdateStations, http.MethodPut, "/campaigns/x/materials/y/stations", map[string]any{
		"target_stations": []string{viaRota.e1.String(), viaRota.e2.String()},
	}, map[string]string{"campaignID": viaRota.camp.String(), "materialID": viaRota.mat.String()}, b.admin)
	b.confirmarEExecutar(t, "editar_emissoras_material", argsVinc(viaTool, map[string]any{"adicionar": strsDe(viaTool.e2)}))

	estRota, _ := vinculoGravado(t, b, viaRota.camp, viaRota.mat)
	estTool, _ := vinculoGravado(t, b, viaTool.camp, viaTool.mat)
	require.Equal(t, posicoes(t, estRota, []uuid.UUID{viaRota.e1, viaRota.e2}), posicoes(t, estTool, []uuid.UUID{viaTool.e1, viaTool.e2}))
	require.Equal(t, 1, supRota.Contagem("Reload", viaRota.camp))
	require.Equal(t, 1, b.sup.Contagem("Reload", viaTool.camp))
	require.Len(t, b.sup.Chamadas(), 1)
}

func TestEditarEmissorasMaterial_Recusas(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearVinc(t, b, "ativa")
	mcptest.Vincular(t, b.ctx, b.pool, c.camp, c.mat, c.e1)
	semVinculo := semearVinc(t, b, "ativa")
	canc := semearVinc(t, b, "cancelada")
	mcptest.Vincular(t, b.ctx, b.pool, canc.camp, canc.mat, canc.e1)
	muitas := make([]any, 501)
	for i := range muitas {
		muitas[i] = uuid.NewString()
	}

	for nome, caso := range map[string]struct {
		args   map[string]any
		codigo string
		trecho string
	}{
		"nenhuma lista":              {argsVinc(c, nil), "[entrada_invalida]", ""},
		"vínculo inexistente":        {argsVinc(semVinculo, map[string]any{"adicionar": strsDe(semVinculo.e1)}), "[nao_encontrado]", ""},
		"campanha cancelada":         {argsVinc(canc, map[string]any{"adicionar": strsDe(canc.e2)}), "[campanha_cancelada]", ""},
		"campanha inexistente":       {map[string]any{"campanha_id": uuid.NewString(), "material_id": c.mat.String(), "adicionar": strsDe(c.e2)}, "[nao_encontrado]", ""},
		"adicionar fora da campanha": {argsVinc(c, map[string]any{"adicionar": strsDe(c.e3)}), "[entrada_invalida]", "não é da campanha"},
		"adicionar já no vínculo":    {argsVinc(c, map[string]any{"adicionar": strsDe(c.e1)}), "[entrada_invalida]", "já está no vínculo"},
		"remover fora do vínculo":    {argsVinc(c, map[string]any{"remover": strsDe(c.e2)}), "[entrada_invalida]", "não está no vínculo"},
		"mesma nas duas listas":      {argsVinc(c, map[string]any{"adicionar": strsDe(c.e2), "remover": strsDe(c.e2)}), "[entrada_invalida]", "ao mesmo tempo"},
		"vínculo ficaria vazio":      {argsVinc(c, map[string]any{"remover": strsDe(c.e1)}), "[entrada_invalida]", "desvincular_material"},
		"emissora inválida":          {argsVinc(c, map[string]any{"adicionar": []any{"x"}}), "[entrada_invalida]", ""},
		"mais de 500":                {argsVinc(c, map[string]any{"adicionar": muitas}), "[entrada_invalida]", ""},
	} {
		texto := b.chamarErro(t, "editar_emissoras_material", caso.args)
		require.Contains(t, texto, caso.codigo, nome)
		require.Contains(t, texto, caso.trecho, nome)
	}
	est, _ := vinculoGravado(t, b, c.camp, c.mat)
	require.Equal(t, []uuid.UUID{c.e1}, est)
	require.Empty(t, b.sup.Chamadas())
	require.Zero(t, b.totalAuditorias(t))
}

// ── Concorrência de atribuição e webhooks (preview) ─────────────────────

// comMultiAtribuicao liga a flag MULTI_ATTRIBUTION só dentro do teste.
func comMultiAtribuicao(t *testing.T, ligada bool) {
	t.Helper()
	antes := multiAtribuicaoLigada
	multiAtribuicaoLigada = func() bool { return ligada }
	t.Cleanup(func() { multiAtribuicaoLigada = antes })
}

// fixaAddedAt grava o instante de criação de um vínculo (o desempate da atribuição é added_at DESC).
func fixaAddedAt(t *testing.T, b *bancadaTool, camp, mat uuid.UUID, quando string) {
	t.Helper()
	_, err := b.pool.Exec(b.ctx, `UPDATE campaign_materials SET added_at = $3::timestamptz WHERE campaign_id = $1 AND material_id = $2`, camp, mat, quando)
	require.NoError(t, err)
}

// ressalvasDe devolve as ressalvas do preview como um texto só.
func ressalvasDoPreview(t *testing.T, out map[string]any) string {
	t.Helper()
	return mustJSON(t, out["preview"].(map[string]any)["ressalvas"])
}

// concorrenteDe cria uma campanha ativa do MESMO cliente, de vigência igual à de c, com o material
// vinculado às emissoras dadas e o vínculo criado em `quando`.
func concorrenteDe(t *testing.T, b *bancadaTool, c cenarioVinc, nome, estado, inicio, fim, quando string, mat uuid.UUID, emissoras ...uuid.UUID) uuid.UUID {
	t.Helper()
	camp := mcptest.Campanha(t, b.ctx, b.pool, c.cli, nome, inicio, fim, emissoras...)
	mcptest.EstadoCampanha(t, b.ctx, b.pool, camp, estado)
	mcptest.Vincular(t, b.ctx, b.pool, camp, mat, emissoras...)
	fixaAddedAt(t, b, camp, mat, quando)
	return camp
}

func TestVincularMaterial_RessalvaDeConcorrenciaDeAtribuicao(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearVinc(t, b, "ativa") // E1 e E2; o material ainda não está vinculado
	concorrente := concorrenteDe(t, b, c, "Campanha Concorrente", "ativa", "2026-10-15", "2026-12-31", "2026-09-01T10:00:00Z", c.mat, c.e1)
	_ = concorrente

	// Multi-atribuição DESLIGADA: a tocada vai para UMA campanha, a do vínculo mais recente — este.
	comMultiAtribuicao(t, false)
	pre := b.preview(t, "vincular_material", argsVinc(c, nil))
	r := ressalvasDoPreview(t, pre)
	require.Contains(t, r, "Campanha Concorrente")
	require.Contains(t, r, "UMA campanha só")
	require.Contains(t, r, "este vínculo passa a levar as tocadas futuras")
	require.Contains(t, r, "Rádio 1")
	require.NotContains(t, r, "Rádio 2", "só as emissoras em comum")
	require.Empty(t, b.sup.Chamadas(), "o preview não escreve")

	// Multi-atribuição LIGADA: a tocada conta nas duas, ninguém perde.
	comMultiAtribuicao(t, true)
	pre = b.preview(t, "vincular_material", argsVinc(c, nil))
	r = ressalvasDoPreview(t, pre)
	require.Contains(t, r, "multi-atribuição ligada")
	require.Contains(t, r, "conta nas duas campanhas")
	require.NotContains(t, r, "deixa de recebê-las")
}

// Os vizinhos que NÃO disputam a atribuição não geram ressalva.
func TestVincularMaterial_ConcorrenciaVizinhosQueNaoDisputam(t *testing.T) {
	b := novaBancadaTool(t)
	comMultiAtribuicao(t, false)
	c := semearVinc(t, b, "ativa")
	cli2 := mcptest.Cliente(t, b.ctx, b.pool, "Outro cliente")
	outroMat := mcptest.Material(t, b.ctx, b.pool, c.cli, c.tipo, "Outro áudio") // sha diferente

	// Cada concorrente abaixo está errado em UM aspecto:
	concorrenteDe(t, b, c, "Cancelada", "cancelada", "2026-10-01", "2026-11-30", "2026-09-01T10:00:00Z", c.mat, c.e1)
	concorrenteDe(t, b, c, "Concluída", "concluida", "2026-10-01", "2026-11-30", "2026-09-01T10:00:00Z", c.mat, c.e1)
	concorrenteDe(t, b, c, "Outra emissora", "ativa", "2026-10-01", "2026-11-30", "2026-09-01T10:00:00Z", c.mat, c.e3)
	concorrenteDe(t, b, c, "Vigência que não cruza", "ativa", "2027-01-01", "2027-01-31", "2026-09-01T10:00:00Z", c.mat, c.e1)
	concorrenteDe(t, b, c, "Outro áudio", "ativa", "2026-10-01", "2026-11-30", "2026-09-01T10:00:00Z", outroMat, c.e1)
	_ = cli2

	pre := b.preview(t, "vincular_material", argsVinc(c, nil))
	r := ressalvasDoPreview(t, pre)
	for _, nome := range []string{"Cancelada", "Concluída", "Outra emissora", "Vigência que não cruza", "Outro áudio"} {
		require.NotContains(t, r, nome)
	}
	require.NotContains(t, r, "MULTI_ATTRIBUTION")

	// Programada que cruza a vigência CONTA (a atribuição aceita programada e ativa).
	concorrenteDe(t, b, c, "Programada que cruza", "programada", "2026-11-15", "2026-12-31", "2026-09-01T10:00:00Z", c.mat, c.e2)
	pre = b.preview(t, "vincular_material", argsVinc(c, nil))
	require.Contains(t, ressalvasDoPreview(t, pre), "Programada que cruza")
}

// Outro material com o MESMO áudio (mesmo master_sha256) em outra campanha.
func TestVincularMaterial_ConcorrenciaDeMaterialDuplicado(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearVinc(t, b, "ativa")
	duplicado := mcptest.Material(t, b.ctx, b.pool, c.cli, c.tipo, "Spot duplicado")
	_, err := b.pool.Exec(b.ctx, `UPDATE materials SET master_sha256 = 'mesmo-audio' WHERE id IN ($1, $2)`, c.mat, duplicado)
	require.NoError(t, err)
	concorrenteDe(t, b, c, "Campanha do duplicado", "ativa", "2026-10-01", "2026-11-30", "2026-09-01T10:00:00Z", duplicado, c.e1)

	comMultiAtribuicao(t, false)
	pre := b.preview(t, "vincular_material", argsVinc(c, nil))
	r := ressalvasDoPreview(t, pre)
	require.Contains(t, r, "Campanha do duplicado")
	require.Contains(t, r, "OUTRO material com o MESMO áudio")
	require.Contains(t, r, "Spot duplicado")
	require.Contains(t, r, "pode ficar sem receber veiculação")

	comMultiAtribuicao(t, true)
	pre = b.preview(t, "vincular_material", argsVinc(c, nil))
	r = ressalvasDoPreview(t, pre)
	require.Contains(t, r, "OUTRO material com o MESMO áudio")
	require.Contains(t, r, "conta nas duas campanhas")
}

func TestEditarEmissorasMaterial_ConcorrenciaAoAcrescentarDependeDeQuemFoiVinculadoPorUltimo(t *testing.T) {
	b := novaBancadaTool(t)
	comMultiAtribuicao(t, false)
	c := semearVinc(t, b, "ativa")
	_, err := b.pool.Exec(b.ctx, `UPDATE campaigns SET target_stations = $2 WHERE id = $1`, c.camp, []uuid.UUID{c.e1, c.e2, c.e3})
	require.NoError(t, err)
	mcptest.Vincular(t, b.ctx, b.pool, c.camp, c.mat, c.e1)
	fixaAddedAt(t, b, c.camp, c.mat, "2026-10-10T10:00:00Z")
	// O concorrente disputa só o E2 (E1 já é do vínculo; E3 ninguém disputa).
	concorrenteDe(t, b, c, "Mais antigo", "ativa", "2026-10-01", "2026-11-30", "2026-09-01T10:00:00Z", c.mat, c.e2)
	args := argsVinc(c, map[string]any{"adicionar": strsDe(c.e2)})

	r := ressalvasDoPreview(t, b.preview(t, "editar_emissoras_material", args))
	require.Contains(t, r, "Mais antigo")
	require.Contains(t, r, "este vínculo passa a levar", "o vínculo desta campanha é mais recente: leva as tocadas")

	// Concorrente MAIS RECENTE que o vínculo: continua levando.
	fixaAddedAt(t, b, c.camp, c.mat, "2026-08-01T10:00:00Z")
	r = ressalvasDoPreview(t, b.preview(t, "editar_emissoras_material", args))
	require.Contains(t, r, "o vínculo dela é mais recente")
	require.Contains(t, r, "essas emissoras continuam indo para")
	require.NotContains(t, r, "este vínculo passa a levar")

	// Só as emissoras ACRESCENTADAS entram na conta: o concorrente em E2 não aparece ao acrescentar E3.
	r = ressalvasDoPreview(t, b.preview(t, "editar_emissoras_material", argsVinc(c, map[string]any{"adicionar": strsDe(c.e3)})))
	require.NotContains(t, r, "Mais antigo")
}

func TestDesvincularMaterial_ConcorrenciaVoltaParaQuemTemOMaterial(t *testing.T) {
	b := novaBancadaTool(t)
	comMultiAtribuicao(t, false)
	c := semearVinc(t, b, "ativa")
	mcptest.Vincular(t, b.ctx, b.pool, c.camp, c.mat, c.e1, c.e2)
	fixaAddedAt(t, b, c.camp, c.mat, "2026-10-10T10:00:00Z")
	concorrenteDe(t, b, c, "Mais antiga", "ativa", "2026-10-01", "2026-11-30", "2026-09-01T10:00:00Z", c.mat, c.e1)

	// Este vínculo é o mais recente: hoje leva as tocadas; ao tirar, voltam para a mais antiga.
	r := ressalvasDoPreview(t, b.preview(t, "desvincular_material", argsVinc(c, nil)))
	require.Contains(t, r, "Mais antiga")
	require.Contains(t, r, "voltam para")
	require.Contains(t, r, "Rádio 1")

	// Multi-atribuição ligada: a outra segue contando, só esta deixa de contar.
	comMultiAtribuicao(t, true)
	r = ressalvasDoPreview(t, b.preview(t, "desvincular_material", argsVinc(c, nil)))
	require.Contains(t, r, "segue contando")
	require.NotContains(t, r, "voltam para")

	// Concorrente MAIS RECENTE que este vínculo: a tocada já vai para ele, nada volta.
	comMultiAtribuicao(t, false)
	fixaAddedAt(t, b, c.camp, c.mat, "2026-08-01T10:00:00Z")
	r = ressalvasDoPreview(t, b.preview(t, "desvincular_material", argsVinc(c, nil)))
	require.NotContains(t, r, "Mais antiga")
	require.Contains(t, r, "Webhook de detecção", "o aviso do webhook não depende da concorrência")
}

func TestEditarEmissorasMaterial_ConcorrenciaAoTirarSoOlhaAsEmissorasTiradas(t *testing.T) {
	b := novaBancadaTool(t)
	comMultiAtribuicao(t, false)
	c := semearVinc(t, b, "ativa")
	mcptest.Vincular(t, b.ctx, b.pool, c.camp, c.mat, c.e1, c.e2)
	fixaAddedAt(t, b, c.camp, c.mat, "2026-10-10T10:00:00Z")
	concorrenteDe(t, b, c, "Mais antiga", "ativa", "2026-10-01", "2026-11-30", "2026-09-01T10:00:00Z", c.mat, c.e1)

	r := ressalvasDoPreview(t, b.preview(t, "editar_emissoras_material", argsVinc(c, map[string]any{"remover": strsDe(c.e1)})))
	require.Contains(t, r, "voltam para")
	r = ressalvasDoPreview(t, b.preview(t, "editar_emissoras_material", argsVinc(c, map[string]any{"remover": strsDe(c.e2)})))
	require.NotContains(t, r, "Mais antiga", "E2 não é disputada")
}

// Item do webhook: só a REMOÇÃO de emissora do vínculo (ou o desvínculo) fala do webhook pendente.
func TestVinculos_RessalvaDeWebhookPendenteSoNaRemocao(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearVinc(t, b, "ativa")
	mcptest.Vincular(t, b.ctx, b.pool, c.camp, c.mat, c.e1)

	r := ressalvasDoPreview(t, b.preview(t, "desvincular_material", argsVinc(c, nil)))
	require.Contains(t, r, "Webhook de detecção")
	require.Contains(t, r, "já enfileirados para entrega não são afetados")
	r = ressalvasDoPreview(t, b.preview(t, "editar_emissoras_material", argsVinc(c, map[string]any{"adicionar": strsDe(c.e2), "remover": strsDe(c.e1)})))
	require.Contains(t, r, "Webhook de detecção")
	// Só acrescentar não tira vínculo nenhum: sem ressalva de webhook.
	pre := b.preview(t, "editar_emissoras_material", argsVinc(c, map[string]any{"adicionar": strsDe(c.e2)}))
	if pre["preview"].(map[string]any)["ressalvas"] != nil {
		require.NotContains(t, ressalvasDoPreview(t, pre), "Webhook")
	}
}

// ── Teste de prova de F3-4: o passado não muda ──────────────────────────

// cenarioProva: cliente, três emissoras, campanha ativa de outubro com regras gerais, preço e PMM no
// target, um material vinculado a todas e tocadas PASSADAS aprovadas de todas as categorias — mais um
// CARVE-OUT: no E3 há uma regra que NOMEIA o material (faixa 09:00–09:30) e uma tocada dele fora
// dessa faixa, que o carve-out julga out_slot (a regra geral a julgaria in_slot). Se a retirada do
// vínculo fizesse o material perder o carve-out, essa tocada mudaria na recategorização.
type cenarioProva struct {
	cli, e1, e2, e3, camp, tipo, mat uuid.UUID
	regraGeral                       map[uuid.UUID]uuid.UUID // emissora → regra geral do tipo
}

func semearProva(t *testing.T, b *bancadaTool) cenarioProva {
	t.Helper()
	agora := func() time.Time { return mcptest.Quando("2026-10-29", "12:00") }
	b.E.Agora = agora
	b.E.Leitura.Agora = agora // os relatórios leem o "hoje" deste relógio

	p := cenarioProva{regraGeral: map[uuid.UUID]uuid.UUID{}}
	p.cli = mcptest.Cliente(t, b.ctx, b.pool, "Cliente Prova")
	p.e1 = mcptest.Emissora(t, b.ctx, b.pool, "Rádio Prova 1", 1000)
	p.e2 = mcptest.Emissora(t, b.ctx, b.pool, "Rádio Prova 2", 1000)
	p.e3 = mcptest.Emissora(t, b.ctx, b.pool, "Rádio Prova 3", 1000)
	p.camp = mcptest.Campanha(t, b.ctx, b.pool, p.cli, "Campanha Prova", "2026-10-01", "2026-10-31", p.e1, p.e2, p.e3)
	mcptest.EstadoCampanha(t, b.ctx, b.pool, p.camp, "ativa")
	p.tipo = mcptest.TipoMaterial(t, b.ctx, b.pool, "Spot Prova")
	p.mat = mcptest.Material(t, b.ctx, b.pool, p.cli, p.tipo, "Spot Prova")
	mcptest.Vincular(t, b.ctx, b.pool, p.camp, p.mat, p.e1, p.e2, p.e3)
	for _, e := range []uuid.UUID{p.e1, p.e2, p.e3} {
		p.regraGeral[e] = mcptest.Regra(t, b.ctx, b.pool, p.camp, p.tipo, e, "2026-10-01", "2026-10-31", 127, "08:00", "18:00", 2)
		mcptest.Preco(t, b.ctx, b.pool, p.camp, e, p.tipo, 100)
		mcptest.PMMTarget(t, b.ctx, b.pool, p.cli, e, 500)
	}
	// O carve-out do E3: regra que nomeia o material.
	_, err := catalog.NewDistributionRules(b.pool).Create(b.ctx, catalog.CreateDistributionRuleInput{
		CampaignID: p.camp, TypeID: p.tipo, StationIDs: []uuid.UUID{p.e3}, MaterialIDs: []uuid.UUID{p.mat}, Name: "Carve-out do E3",
		StartDate: mcptest.Dia("2026-10-01"), EndDate: mcptest.Dia("2026-10-31"), WeekdayMask: 127,
		TimeStart: "09:00", TimeEnd: "09:30", PlaysPerDay: 1,
	})
	require.NoError(t, err)

	mcptest.Veiculacao(t, b.ctx, b.pool, p.camp, p.mat, p.e1, "in_slot", mcptest.Quando("2026-10-10", "10:00"))
	mcptest.Veiculacao(t, b.ctx, b.pool, p.camp, p.mat, p.e1, "in_slot", mcptest.Quando("2026-10-10", "11:00"))
	mcptest.Veiculacao(t, b.ctx, b.pool, p.camp, p.mat, p.e1, "bonus", mcptest.Quando("2026-10-10", "12:00"))
	mcptest.Veiculacao(t, b.ctx, b.pool, p.camp, p.mat, p.e2, "in_slot", mcptest.Quando("2026-10-12", "10:00"))
	mcptest.Veiculacao(t, b.ctx, b.pool, p.camp, p.mat, p.e2, "out_slot", mcptest.Quando("2026-10-12", "20:00"))
	mcptest.Veiculacao(t, b.ctx, b.pool, p.camp, p.mat, p.e3, "in_slot", mcptest.Quando("2026-10-15", "09:00"))
	mcptest.Veiculacao(t, b.ctx, b.pool, p.camp, p.mat, p.e3, "out_slot", mcptest.Quando("2026-10-15", "14:00"))
	return p
}

// renomearRegra edita a regra geral de uma emissora SÓ no nome — pela mesma ação que a rota e a tool de
// regras usam, com o recat rodando na hora (EmSegundoPlano = Sincrono). Recategoriza o tipo inteiro da
// campanha, inclusive as tocadas do material (vinculado ou não).
func (p cenarioProva) renomearRegra(t *testing.T, b *bancadaTool, emissora uuid.UUID, nome string) {
	t.Helper()
	require.NoError(t, b.E.Acoes.EditarRegra(b.ctx, p.camp, p.regraGeral[emissora], acoes.EntradaRegra{
		TipoID: p.tipo, EmissoraIDs: []uuid.UUID{emissora}, MaterialIDs: []uuid.UUID{}, Nome: nome,
		Inicio: mcptest.Dia("2026-10-01"), Fim: mcptest.Dia("2026-10-31"), MascaraDias: 127,
		HoraInicio: "08:00", HoraFim: "18:00", InsercoesPorDia: 2,
	}))
}

// capturar lê, pelas tools de leitura, tudo o que o cliente vê do passado. comFalhas acrescenta as
// falhas da campanha (lidas só no teste da remoção de emissora da campanha).
func (p cenarioProva) capturar(t *testing.T, b *bancadaTool, comFalhas bool) map[string]string {
	t.Helper()
	leituras := map[string]map[string]any{
		"grade_campanha":       {"campanha_id": p.camp.String(), "de": "2026-10-01", "ate": "2026-10-31", "limit": 100}, // 3 emissoras × 31 dias = 93 células, todas na página
		"insights_campanha":    {"cliente_id": p.cli.String(), "campanha_ids": []any{p.camp.String()}, "de": "2026-10-01", "ate": "2026-10-31"},
		"financeiro_campanhas": {"campanha_ids": []any{p.camp.String()}},
		"listar_veiculacoes":   {"campanha_ids": []any{p.camp.String()}, "de": "2026-10-01", "ate": "2026-10-31", "mais_antigas_primeiro": true},
		"plano_do_dia":         {"campanha_id": p.camp.String(), "emissora_id": p.e1.String(), "tipo_id": p.tipo.String(), "data": "2026-10-10"},
	}
	if comFalhas {
		leituras["obter_falhas_campanha"] = map[string]any{"campanha_id": p.camp.String()}
	}
	out := map[string]string{}
	for nome, args := range leituras {
		out[nome] = mustJSON(t, b.chamar(t, nome, args))
	}
	out["plano_do_dia (carve-out do E3)"] = mustJSON(t, b.chamar(t, "plano_do_dia", map[string]any{
		"campanha_id": p.camp.String(), "emissora_id": p.e3.String(), "tipo_id": p.tipo.String(), "data": "2026-10-15"}))
	return out
}

// projecoes: o que o grafo de atribuição guarda (tocada → campanha, material, categoria).
func projecoes(t *testing.T, b *bancadaTool) string {
	t.Helper()
	var s string
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT COALESCE(string_agg(detection_id::text || ':' || campaign_id::text || ':' || commercial_id::text || ':' || category, ',' ORDER BY detection_id), '')
		FROM detection_campaigns`).Scan(&s))
	return s
}

// exigeIgual compara duas capturas e, se diferirem, diz EXATAMENTE o que mudou.
func exigeIgual(t *testing.T, passo string, antes, depois map[string]string) {
	t.Helper()
	for nome := range antes {
		if antes[nome] == depois[nome] {
			continue
		}
		var a, d any
		require.NoError(t, json.Unmarshal([]byte(antes[nome]), &a))
		require.NoError(t, json.Unmarshal([]byte(depois[nome]), &d))
		var dif []string
		caminhosDiferentes(a, d, nome, &dif)
		t.Errorf("%s: %s mudou:\n  %s", passo, nome, strings.Join(dif, "\n  "))
	}
}

func caminhosDiferentes(a, b any, caminho string, out *[]string) {
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok {
			*out = append(*out, caminho+": o tipo mudou")
			return
		}
		chaves := map[string]bool{}
		for k := range x {
			chaves[k] = true
		}
		for k := range y {
			chaves[k] = true
		}
		ordenadas := make([]string, 0, len(chaves))
		for k := range chaves {
			ordenadas = append(ordenadas, k)
		}
		sort.Strings(ordenadas)
		for _, k := range ordenadas {
			caminhosDiferentes(x[k], y[k], caminho+"."+k, out)
		}
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			*out = append(*out, fmt.Sprintf("%s: %d itens -> %d itens", caminho, len(x), len(y)))
			return
		}
		for i := range x {
			caminhosDiferentes(x[i], y[i], fmt.Sprintf("%s[%d]", caminho, i), out)
		}
	default:
		if mustJSONSemT(a) != mustJSONSemT(b) {
			*out = append(*out, fmt.Sprintf("%s: %s -> %s", caminho, mustJSONSemT(a), mustJSONSemT(b)))
		}
	}
}

func mustJSONSemT(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// As tools de vínculo NÃO escalam porque nada recategoriza e a atribuição é decidida na entrada da
// tocada. Este teste PROVA a premissa: com veiculações passadas aprovadas (regras, preço e PMM para os
// números existirem, e um carve-out que nomeia o material), grade, insights, financeiro, veiculações e
// os planos do dia saem IDÊNTICOS — e as projeções (detection_campaigns) também — antes e depois de
// editar_emissoras_material (tirando uma emissora com tocada), de desvincular_material, de EDITAR UMA
// REGRA com o material já desvinculado (o recat roda sobre as tocadas dele, e o carve-out segue
// valendo) e de vincular_material de novo. Se um dia isto quebrar, a escalada dessas tools volta
// (decisão do dono).
func TestVinculos_ProvaF34_PassadoNaoMuda(t *testing.T) {
	b := novaBancadaTool(t)
	p := semearProva(t, b)

	// As categorias semeadas são as que o categorizador calcula: um recat não muda nenhuma.
	semeadas := projecoes(t, b)
	p.renomearRegra(t, b, p.e2, "Regra do E2 v1")
	require.Equal(t, semeadas, projecoes(t, b), "as categorias semeadas já são as do categorizador (o recat não mexe)")

	antes := p.capturar(t, b, false)
	projAntes := projecoes(t, b)
	// A prova não pode ser vazia: as leituras têm número de verdade e o carve-out está em jogo.
	require.Contains(t, antes["listar_veiculacoes"], "in_slot")
	require.Contains(t, antes["grade_campanha"], `"in_slot":4`, "os totais da grade somam as 4 tocadas in_slot")
	require.Contains(t, antes["grade_campanha"], `"has_more":false`, "a página tem todas as células")
	require.NotContains(t, antes["insights_campanha"], `"erro"`)
	require.Greater(t, len(antes["insights_campanha"]), 200)
	require.Contains(t, antes["plano_do_dia (carve-out do E3)"], "out_slot", "o carve-out julga a tocada de 14:00 out_slot")

	confere := func(passo string) {
		t.Helper()
		exigeIgual(t, passo, antes, p.capturar(t, b, false))
		require.Equal(t, projAntes, projecoes(t, b), "%s: as projeções (detection_campaigns) mudaram", passo)
	}
	ref := cenarioVinc{camp: p.camp, mat: p.mat}

	// 1. tirar do vínculo uma emissora COM tocada passada.
	b.confirmarEExecutar(t, "editar_emissoras_material", argsVinc(ref, map[string]any{"remover": strsDe(p.e1)}))
	est, _ := vinculoGravado(t, b, p.camp, p.mat)
	require.Equal(t, []uuid.UUID{p.e2, p.e3}, est)
	confere("editar_emissoras_material (tirou E1, que tem tocada)")

	// 2. tirar o material da campanha.
	b.confirmarEExecutar(t, "desvincular_material", argsVinc(ref, nil))
	_, existe := vinculoGravado(t, b, p.camp, p.mat)
	require.False(t, existe)
	confere("desvincular_material")

	// 3. com o material DESVINCULADO, editar uma regra: o recat varre o tipo da campanha, inclusive as
	// tocadas dele (que o recat acha pela projeção, não pelo vínculo). As categorias — e o carve-out
	// que o nomeia — ficam como estavam.
	p.renomearRegra(t, b, p.e2, "Regra do E2 v2")
	confere("editar regra com o material desvinculado (recat)")
	require.Contains(t, projecoes(t, b), ":out_slot", "a tocada do carve-out continua out_slot")

	// 4. vincular de novo, em todas as emissoras da campanha.
	b.confirmarEExecutar(t, "vincular_material", argsVinc(ref, nil))
	est, _ = vinculoGravado(t, b, p.camp, p.mat)
	require.Equal(t, []uuid.UUID{p.e1, p.e2, p.e3}, est)
	confere("vincular_material")
}
