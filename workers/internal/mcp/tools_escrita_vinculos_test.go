package mcp

// Task 15: vincular_material, desvincular_material e editar_emissoras_material
// (sem escalada: F3-4) e o teste de prova de que o passado não muda.

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

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

// ── Teste de prova de F3-4: o passado não muda ──────────────────────────

// As tools de vínculo NÃO escalam porque nada recategoriza e a atribuição é
// decidida na entrada da tocada. Este teste PROVA a premissa: com veiculações
// passadas aprovadas (e regras, preço e PMM para os números existirem), grade,
// insights, financeiro, veiculações e o plano do dia saem IDÊNTICOS antes e
// depois de editar_emissoras_material (tirando uma emissora com tocada),
// desvincular_material e vincular_material de novo. Se um dia isto quebrar, a
// escalada dessas tools volta (decisão do dono).
func TestVinculos_ProvaF34_PassadoNaoMuda(t *testing.T) {
	b := novaBancadaTool(t)
	agora := func() time.Time { return mcptest.Quando("2026-10-29", "12:00") }
	b.E.Agora = agora
	b.E.Leitura.Agora = agora // os relatórios leem o "hoje" deste relógio

	cli := mcptest.Cliente(t, b.ctx, b.pool, "Cliente Prova")
	e1 := mcptest.Emissora(t, b.ctx, b.pool, "Rádio Prova 1", 1000)
	e2 := mcptest.Emissora(t, b.ctx, b.pool, "Rádio Prova 2", 1000)
	e3 := mcptest.Emissora(t, b.ctx, b.pool, "Rádio Prova 3", 1000)
	camp := mcptest.Campanha(t, b.ctx, b.pool, cli, "Campanha Prova", "2026-10-01", "2026-10-31", e1, e2, e3)
	mcptest.EstadoCampanha(t, b.ctx, b.pool, camp, "ativa")
	tipo := mcptest.TipoMaterial(t, b.ctx, b.pool, "Spot Prova")
	mat := mcptest.Material(t, b.ctx, b.pool, cli, tipo, "Spot Prova")
	mcptest.Vincular(t, b.ctx, b.pool, camp, mat, e1, e2, e3)
	for _, e := range []uuid.UUID{e1, e2, e3} {
		mcptest.Regra(t, b.ctx, b.pool, camp, tipo, e, "2026-10-01", "2026-10-31", 127, "08:00", "18:00", 2)
		mcptest.Preco(t, b.ctx, b.pool, camp, e, tipo, 100)
		mcptest.PMMTarget(t, b.ctx, b.pool, cli, e, 500)
	}
	// Tocadas passadas, aprovadas, de todas as categorias que contam.
	mcptest.Veiculacao(t, b.ctx, b.pool, camp, mat, e1, "in_slot", mcptest.Quando("2026-10-10", "10:00"))
	mcptest.Veiculacao(t, b.ctx, b.pool, camp, mat, e1, "in_slot", mcptest.Quando("2026-10-10", "11:00"))
	mcptest.Veiculacao(t, b.ctx, b.pool, camp, mat, e1, "bonus", mcptest.Quando("2026-10-10", "12:00"))
	mcptest.Veiculacao(t, b.ctx, b.pool, camp, mat, e2, "in_slot", mcptest.Quando("2026-10-12", "10:00"))
	mcptest.Veiculacao(t, b.ctx, b.pool, camp, mat, e2, "out_slot", mcptest.Quando("2026-10-12", "20:00"))
	mcptest.Veiculacao(t, b.ctx, b.pool, camp, mat, e3, "in_slot", mcptest.Quando("2026-10-15", "09:00"))

	capturar := func() map[string]string {
		leituras := map[string]map[string]any{
			"grade_campanha":       {"campanha_id": camp.String(), "de": "2026-10-01", "ate": "2026-10-31", "limit": 100}, // 3 emissoras × 31 dias = 93 células, todas na página
			"insights_campanha":    {"cliente_id": cli.String(), "campanha_ids": []any{camp.String()}, "de": "2026-10-01", "ate": "2026-10-31"},
			"financeiro_campanhas": {"campanha_ids": []any{camp.String()}},
			"listar_veiculacoes":   {"campanha_ids": []any{camp.String()}, "de": "2026-10-01", "ate": "2026-10-31", "mais_antigas_primeiro": true},
			"plano_do_dia":         {"campanha_id": camp.String(), "emissora_id": e1.String(), "tipo_id": tipo.String(), "data": "2026-10-10"},
		}
		out := map[string]string{}
		for nome, args := range leituras {
			out[nome] = mustJSON(t, b.chamar(t, nome, args))
		}
		return out
	}
	projecoes := func() string {
		var s string
		require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT COALESCE(string_agg(detection_id::text || ':' || campaign_id::text || ':' || commercial_id::text || ':' || category, ',' ORDER BY detection_id), '')
			FROM detection_campaigns`).Scan(&s))
		return s
	}

	antes := capturar()
	projAntes := projecoes()
	// A prova não pode ser vazia: as leituras têm número de verdade.
	require.Contains(t, antes["listar_veiculacoes"], "in_slot")
	require.Contains(t, antes["grade_campanha"], `"in_slot":4`, "os totais da grade somam as 4 tocadas in_slot")
	require.Contains(t, antes["grade_campanha"], `"has_more":false`, "a página tem todas as células")
	require.NotContains(t, antes["insights_campanha"], `"erro"`)
	require.Greater(t, len(antes["insights_campanha"]), 200)

	confere := func(passo string) {
		t.Helper()
		depois := capturar()
		for nome := range antes {
			require.Equal(t, antes[nome], depois[nome], "%s: %s mudou", passo, nome)
		}
		require.Equal(t, projAntes, projecoes(), "%s: as projeções (detection_campaigns) mudaram", passo)
	}

	// 1. tirar do vínculo uma emissora COM tocada passada.
	b.confirmarEExecutar(t, "editar_emissoras_material", argsVinc(cenarioVinc{camp: camp, mat: mat}, map[string]any{"remover": strsDe(e1)}))
	est, _ := vinculoGravado(t, b, camp, mat)
	require.Equal(t, []uuid.UUID{e2, e3}, est)
	confere("editar_emissoras_material (tirou E1, que tem tocada)")

	// 2. tirar o material da campanha.
	b.confirmarEExecutar(t, "desvincular_material", argsVinc(cenarioVinc{camp: camp, mat: mat}, nil))
	_, existe := vinculoGravado(t, b, camp, mat)
	require.False(t, existe)
	confere("desvincular_material")

	// 3. vincular de novo, em todas as emissoras da campanha.
	b.confirmarEExecutar(t, "vincular_material", argsVinc(cenarioVinc{camp: camp, mat: mat}, nil))
	est, _ = vinculoGravado(t, b, camp, mat)
	require.Equal(t, []uuid.UUID{e1, e2, e3}, est)
	confere("vincular_material")
}
