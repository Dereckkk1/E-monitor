package mcp

// Task 12: tipos de material e editar_material.

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/acoes"
	"radiocheck/internal/api/handlers"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

func repete(s string, n int) string { return strings.Repeat(s, n) }
func maiusculas(s string) string    { return strings.ToUpper(s) }

func linhaTipo(t *testing.T, b *bancadaTool, id uuid.UUID) (nome, cor string, descricao *string) {
	t.Helper()
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT name, color, description FROM material_types WHERE id = $1`, id).Scan(&nome, &cor, &descricao))
	return nome, cor, descricao
}

func contaTipos(t *testing.T, b *bancadaTool) int {
	t.Helper()
	var n int
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT count(*) FROM material_types`).Scan(&n))
	return n
}

func idDoTipoPorNome(t *testing.T, b *bancadaTool, nome string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT id FROM material_types WHERE name = $1`, nome).Scan(&id))
	return id
}

func TestCriarTipoMaterial_PreviewNaoEscreveEConfirmarCria(t *testing.T) {
	b := novaBancadaTool(t)
	nome := "Vinheta nova " + uuid.NewString()[:8]
	antes := contaTipos(t, b)
	args := map[string]any{"nome": nome, "cor": "#10B981", "descricao": "Vinhetas de 5s"}
	for _, tool := range []string{"criar_tipo_material", "editar_tipo_material", "editar_material"} {
		require.Contains(t, propriedades(esquemaDa(t, b.cfg, tool)), "confirmacao", "toda tool confirma tem o campo no schema: %s", tool)
	}

	pre := b.preview(t, "criar_tipo_material", args)

	require.Equal(t, antes, contaTipos(t, b), "o preview não escreve")
	require.Zero(t, b.totalAuditorias(t), "o preview não audita")
	pv := pre["preview"].(map[string]any)
	require.Contains(t, pv["resumo"], nome)
	campos := map[string]any{}
	for _, m := range pv["mudancas"].([]any) {
		mm := m.(map[string]any)
		campos[mm["campo"].(string)] = mm["para"]
	}
	require.Equal(t, nome, campos["nome"])
	require.Equal(t, "#10b981", campos["cor"], "a cor sai normalizada em minúsculas")

	out := b.confirmarEExecutar(t, "criar_tipo_material", args)

	require.Equal(t, antes+1, contaTipos(t, b))
	tipo := out["resultado"].(map[string]any)["tipo"].(map[string]any)
	id := uuid.MustParse(tipo["id"].(string))
	require.Equal(t, nome, tipo["nome"])
	require.Equal(t, "#10b981", tipo["cor"])
	require.Equal(t, "Vinhetas de 5s", tipo["descricao"])
	b.exigeAuditoria(t, "mcp.escrita.criar_tipo_material", "material_type", &id)
}

func TestCriarTipoMaterial_ParidadeComARota(t *testing.T) {
	b := novaBancadaTool(t)
	sufixo := uuid.NewString()[:8]
	h := &handlers.MaterialTypesHandler{Repo: catalog.NewMaterialTypes(b.pool)}

	for _, caso := range []struct {
		nome string
		args map[string]any
		rota map[string]any
	}{
		{"completo", map[string]any{"cor": "#112233", "descricao": "Texto"}, map[string]any{"color": "#112233", "description": "Texto"}},
		{"sem cor nem descrição", map[string]any{}, map[string]any{}},
	} {
		viaRota := "Rota " + caso.nome + " " + sufixo
		viaTool := "Tool " + caso.nome + " " + sufixo
		corpo := map[string]any{"name": viaRota}
		for k, v := range caso.rota {
			corpo[k] = v
		}
		chamarHandlerCorpo(t, h.Create, http.MethodPost, "/material-types", corpo, nil, b.admin)
		args := map[string]any{"nome": viaTool}
		for k, v := range caso.args {
			args[k] = v
		}
		b.confirmarEExecutar(t, "criar_tipo_material", args)

		_, cor1, desc1 := linhaTipo(t, b, idDoTipoPorNome(t, b, viaRota))
		_, cor2, desc2 := linhaTipo(t, b, idDoTipoPorNome(t, b, viaTool))
		require.Equal(t, cor1, cor2, caso.nome)
		require.Equal(t, desc1, desc2, caso.nome)
	}
}

func TestCriarTipoMaterial_Recusas(t *testing.T) {
	b := novaBancadaTool(t)
	existente := "Existente " + uuid.NewString()[:8]
	_, err := b.pool.Exec(b.ctx, `INSERT INTO material_types (name, color) VALUES ($1, '#3b82f6')`, existente)
	require.NoError(t, err)
	antes := contaTipos(t, b)

	for nome, caso := range map[string]struct {
		args   map[string]any
		codigo string
	}{
		"nome vazio":               {map[string]any{"nome": "   "}, "[entrada_invalida]"},
		"cor sem #":                {map[string]any{"nome": "X", "cor": "10b981"}, "[entrada_invalida]"},
		"cor curta":                {map[string]any{"nome": "X", "cor": "#fff"}, "[entrada_invalida]"},
		"nome longo demais":        {map[string]any{"nome": repete("a", 81)}, "[entrada_invalida]"},
		"descrição longa demais":   {map[string]any{"nome": "X", "descricao": repete("a", 501)}, "[entrada_invalida]"},
		"nome duplicado":           {map[string]any{"nome": existente}, "[nome_duplicado]"},
		"duplicado em outra caixa": {map[string]any{"nome": "  " + maiusculas(existente) + " "}, "[nome_duplicado]"},
	} {
		texto := b.chamarErro(t, "criar_tipo_material", caso.args)
		require.Contains(t, texto, caso.codigo, nome)
	}
	require.Equal(t, antes, contaTipos(t, b))
	require.Zero(t, b.totalAuditorias(t))
}

// A corrida entre a checagem e o INSERT: a violação de unicidade do banco vira
// nome_duplicado (e não erro interno, que gastaria o token); qualquer outro erro passa.
func TestTraduzirUnicidadeDeTipo(t *testing.T) {
	err := traduzirUnicidadeDeTipo(&pgconn.PgError{Code: "23505"}, "Spot")
	var r *acoes.Recusa
	require.ErrorAs(t, err, &r)
	require.Equal(t, acoes.CodigoNomeDuplicado, r.Codigo)

	outro := errors.New("deadlock detectado")
	require.Equal(t, outro, traduzirUnicidadeDeTipo(outro, "Spot"), "o que não é unicidade passa como veio")
	require.False(t, errors.As(traduzirUnicidadeDeTipo(&pgconn.PgError{Code: "40P01"}, "Spot"), &r))
}

// O Executar repete a checagem de nome do preview (é o que a aprovação na Fatia 4
// roda, sem preview), e sem distinguir maiúsculas de minúsculas.
func TestTipoMaterial_ExecutarRepeteAChecagemDeNome(t *testing.T) {
	b := novaBancadaTool(t)
	// Prefixo com letra minúscula: um sufixo hex só de dígitos deixaria
	// maiusculas(nome) == nome, e a edição viraria "nada a alterar".
	nomeA, nomeB := "tipo a "+uuid.NewString()[:8], "tipo b "+uuid.NewString()[:8]
	idA := uuid.New()
	_, err := b.pool.Exec(b.ctx, `INSERT INTO material_types (id, name, color) VALUES ($1, $2, '#3b82f6'), ($3, $4, '#3b82f6')`, idA, nomeA, uuid.New(), nomeB)
	require.NoError(t, err)
	antes := contaTipos(t, b)

	_, err = escritaCriarTipoMaterial(b.cfg).Executar(b.ctx, b.E, b.p, entradaCriarTipoMaterial{Nome: maiusculas(nomeA)})
	var r *acoes.Recusa
	require.ErrorAs(t, err, &r)
	require.Equal(t, acoes.CodigoNomeDuplicado, r.Codigo)
	require.Equal(t, antes, contaTipos(t, b))

	_, err = escritaEditarTipoMaterial(b.cfg).Executar(b.ctx, b.E, b.p, entradaEditarTipoMaterial{TipoID: idA.String(), Nome: maiusculas(nomeB)})
	require.ErrorAs(t, err, &r)
	require.Equal(t, acoes.CodigoNomeDuplicado, r.Codigo)
	n, _, _ := linhaTipo(t, b, idA)
	require.Equal(t, nomeA, n)

	// O próprio nome em outra caixa é do próprio tipo: não é duplicata.
	_, err = escritaEditarTipoMaterial(b.cfg).Executar(b.ctx, b.E, b.p, entradaEditarTipoMaterial{TipoID: idA.String(), Nome: maiusculas(nomeA)})
	require.NoError(t, err)
}

func TestEditarTipoMaterial_ParcialPreviewNaoEscreveEConfirmarGrava(t *testing.T) {
	b := novaBancadaTool(t)
	nome := "Spot " + uuid.NewString()[:8]
	id := uuid.New()
	_, err := b.pool.Exec(b.ctx, `INSERT INTO material_types (id, name, color, description) VALUES ($1, $2, '#3b82f6', 'Descrição antiga')`, id, nome)
	require.NoError(t, err)
	novo := nome + " (revisado)"
	args := map[string]any{"tipo_id": id.String(), "nome": novo}

	pre := b.preview(t, "editar_tipo_material", args)

	n, c, d := linhaTipo(t, b, id)
	require.Equal(t, nome, n, "o preview não escreve")
	require.Equal(t, "#3b82f6", c)
	require.Equal(t, "Descrição antiga", *d)
	ms := pre["preview"].(map[string]any)["mudancas"].([]any)
	require.Len(t, ms, 1, "só o campo enviado aparece")
	require.Equal(t, map[string]any{"campo": "nome", "de": nome, "para": novo}, ms[0])
	require.Zero(t, b.totalAuditorias(t))

	out := b.confirmarEExecutar(t, "editar_tipo_material", args)

	n, c, d = linhaTipo(t, b, id)
	require.Equal(t, novo, n)
	require.Equal(t, "#3b82f6", c, "cor não enviada: fica")
	require.Equal(t, "Descrição antiga", *d, "descrição não enviada: fica")
	require.Equal(t, novo, out["resultado"].(map[string]any)["tipo"].(map[string]any)["nome"])
	b.exigeAuditoria(t, "mcp.escrita.editar_tipo_material", "material_type", &id)

	// Texto vazio limpa a descrição; null/ausente não toca.
	b.confirmarEExecutar(t, "editar_tipo_material", map[string]any{"tipo_id": id.String(), "descricao": ""})
	_, _, d = linhaTipo(t, b, id)
	require.Nil(t, d)
}

func TestEditarTipoMaterial_ParidadeComARota(t *testing.T) {
	b := novaBancadaTool(t)
	h := &handlers.MaterialTypesHandler{Repo: catalog.NewMaterialTypes(b.pool)}
	semear := func(nome string) uuid.UUID {
		id := uuid.New()
		_, err := b.pool.Exec(b.ctx, `INSERT INTO material_types (id, name, color, description) VALUES ($1, $2, '#3b82f6', 'Antiga')`, id, nome)
		require.NoError(t, err)
		return id
	}
	sufixo := uuid.NewString()[:8]
	viaRota, viaTool := semear("Rota "+sufixo), semear("Tool "+sufixo)

	// A rota é PUT total; a tool é parcial. O estado final dos campos é o mesmo.
	chamarHandlerCorpo(t, h.Update, http.MethodPut, "/material-types/x",
		map[string]any{"name": "Rota novo " + sufixo, "color": "#ff0000", "description": "Antiga"}, map[string]string{"id": viaRota.String()}, b.admin)
	b.confirmarEExecutar(t, "editar_tipo_material", map[string]any{"tipo_id": viaTool.String(), "nome": "Tool novo " + sufixo, "cor": "#FF0000"})

	_, c1, d1 := linhaTipo(t, b, viaRota)
	_, c2, d2 := linhaTipo(t, b, viaTool)
	require.Equal(t, c1, c2)
	require.Equal(t, d1, d2)
}

func TestEditarTipoMaterial_Recusas(t *testing.T) {
	b := novaBancadaTool(t)
	a, outro := "A "+uuid.NewString()[:8], "B "+uuid.NewString()[:8]
	idA := uuid.New()
	_, err := b.pool.Exec(b.ctx, `INSERT INTO material_types (id, name, color) VALUES ($1, $2, '#3b82f6'), ($3, $4, '#3b82f6')`, idA, a, uuid.New(), outro)
	require.NoError(t, err)

	for nome, caso := range map[string]struct {
		args   map[string]any
		codigo string
	}{
		"id inválido":            {map[string]any{"tipo_id": "x", "nome": "N"}, "[entrada_invalida]"},
		"tipo inexistente":       {map[string]any{"tipo_id": uuid.NewString(), "nome": "N"}, "[nao_encontrado]"},
		"nenhum campo":           {map[string]any{"tipo_id": idA.String()}, "[entrada_invalida]"},
		"nome em branco":         {map[string]any{"tipo_id": idA.String(), "nome": "   "}, "[entrada_invalida]"},
		"cor inválida":           {map[string]any{"tipo_id": idA.String(), "cor": "azul"}, "[entrada_invalida]"},
		"nome de outro tipo":     {map[string]any{"tipo_id": idA.String(), "nome": outro}, "[nome_duplicado]"},
		"outro tipo, caixa":      {map[string]any{"tipo_id": idA.String(), "nome": maiusculas(outro)}, "[nome_duplicado]"},
		"nada muda (mesmo nome)": {map[string]any{"tipo_id": idA.String(), "nome": a}, "[entrada_invalida]"},
	} {
		texto := b.chamarErro(t, "editar_tipo_material", caso.args)
		require.Contains(t, texto, caso.codigo, nome)
	}
	n, _, _ := linhaTipo(t, b, idA)
	require.Equal(t, a, n)
	require.Zero(t, b.totalAuditorias(t))
}

// O tipo é só rótulo: o nome não entra na atribuição nem na categoria. Mesmo com
// veiculação, renomear é confirma (sem escalar).
func TestEditarTipoMaterial_NaoEscalaMesmoComVeiculacao(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearMaterial(t, b, true)

	pre := b.chamar(t, "editar_tipo_material", map[string]any{"tipo_id": c.tipoA.String(), "nome": "Outro nome " + uuid.NewString()[:8]})

	require.Equal(t, false, pre["executou"])
	require.NotEmpty(t, pre["confirmacao"], "confirma, não crítico")
	require.NotContains(t, pre, "nivel")
}

// cenarioMaterial: cliente, emissora, tipos A e B, um material do tipo A numa
// campanha de outubro e, opcionalmente, uma tocada.
type cenarioMaterial struct {
	cli, est, tipoA, tipoB, mat, camp uuid.UUID
}

func semearMaterial(t *testing.T, b *bancadaTool, comTocada bool) cenarioMaterial {
	t.Helper()
	c := cenarioMaterial{}
	c.cli = mcptest.Cliente(t, b.ctx, b.pool, "Cliente "+uuid.NewString()[:6])
	c.est = mcptest.Emissora(t, b.ctx, b.pool, "Rádio "+uuid.NewString()[:6], 0)
	c.tipoA = mcptest.TipoMaterial(t, b.ctx, b.pool, "Tipo A")
	c.tipoB = mcptest.TipoMaterial(t, b.ctx, b.pool, "Tipo B")
	c.mat = mcptest.Material(t, b.ctx, b.pool, c.cli, c.tipoA, "Título original")
	c.camp = mcptest.Campanha(t, b.ctx, b.pool, c.cli, "Camp", "2026-10-01", "2026-10-31", c.est)
	mcptest.Vincular(t, b.ctx, b.pool, c.camp, c.mat, c.est)
	if comTocada {
		mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, c.mat, c.est, "bonus", mcptest.Quando("2026-10-10", "10:00"))
	}
	return c
}

func linhaMaterial(t *testing.T, b *bancadaTool, id uuid.UUID) (titulo string, tipo *uuid.UUID, roteiro *string) {
	t.Helper()
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT title, type_id, script FROM materials WHERE id = $1`, id).Scan(&titulo, &tipo, &roteiro))
	return titulo, tipo, roteiro
}

func categoriasDoMaterial(t *testing.T, b *bancadaTool, mat uuid.UUID) []string {
	t.Helper()
	rows, err := b.pool.Query(b.ctx, `SELECT category FROM detection_campaigns WHERE commercial_id = $1 ORDER BY detected_at`, mat)
	require.NoError(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		require.NoError(t, rows.Scan(&c))
		out = append(out, c)
	}
	require.NoError(t, rows.Err())
	return out
}

func TestEditarMaterial_TituloERoteiroPreviewNaoEscreveEConfirmarGrava(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearMaterial(t, b, true) // com veiculação: título e roteiro não escalam
	args := map[string]any{"material_id": c.mat.String(), "titulo": "  Título corrigido  ", "roteiro": "Leia com calma."}

	pre := b.preview(t, "editar_material", args)

	titulo, tipo, roteiro := linhaMaterial(t, b, c.mat)
	require.Equal(t, "Título original", titulo, "o preview não escreve")
	require.Nil(t, roteiro)
	require.Equal(t, c.tipoA, *tipo)
	require.Zero(t, b.totalAuditorias(t))
	campos := map[string]map[string]any{}
	for _, m := range pre["preview"].(map[string]any)["mudancas"].([]any) {
		mm := m.(map[string]any)
		campos[mm["campo"].(string)] = mm
	}
	require.Equal(t, "Título original", campos["titulo"]["de"])
	require.Equal(t, "Título corrigido", campos["titulo"]["para"])
	require.Equal(t, "Leia com calma.", campos["roteiro"]["para"])
	require.NotContains(t, campos, "tipo")

	out := b.confirmarEExecutar(t, "editar_material", args)

	titulo, tipo, roteiro = linhaMaterial(t, b, c.mat)
	require.Equal(t, "Título corrigido", titulo)
	require.Equal(t, "Leia com calma.", *roteiro)
	require.Equal(t, c.tipoA, *tipo, "tipo não enviado: fica")
	res := out["resultado"].(map[string]any) // o mesmo formato do obter_material
	require.Equal(t, "Título corrigido", res["material"].(map[string]any)["titulo"])
	require.Equal(t, "Leia com calma.", res["roteiro"])
	require.Equal(t, []string{"bonus"}, categoriasDoMaterial(t, b, c.mat), "título e roteiro não mexem na categoria")
	b.exigeAuditoria(t, "mcp.escrita.editar_material", "material", &c.mat)

	// Texto vazio limpa o roteiro.
	b.confirmarEExecutar(t, "editar_material", map[string]any{"material_id": c.mat.String(), "roteiro": ""})
	_, _, roteiro = linhaMaterial(t, b, c.mat)
	require.Nil(t, roteiro)
}

// Paridade de efeito com as três rotas (título, roteiro e tipo). Sem veiculação,
// então a troca de tipo NÃO escala (e recategoriza, sem nada a recategorizar).
func TestEditarMaterial_ParidadeComAsRotas(t *testing.T) {
	b := novaBancadaTool(t)
	viaRota := semearMaterial(t, b, false)
	viaTool := semearMaterial(t, b, false)
	h := &handlers.MaterialsHandler{Repo: catalog.NewMaterials(b.pool), DistRules: catalog.NewDistributionRules(b.pool), EmSegundoPlano: mcptest.Sincrono}
	rota := map[string]string{"id": viaRota.mat.String()}

	chamarHandlerCorpo(t, h.UpdateTitle, http.MethodPatch, "/materials/x/title", map[string]any{"title": "Novo título"}, rota, b.admin)
	chamarHandlerCorpo(t, h.UpdateScript, http.MethodPatch, "/materials/x/script", map[string]any{"script": "Novo roteiro"}, rota, b.admin)
	chamarHandlerCorpo(t, h.UpdateType, http.MethodPatch, "/materials/x/type", map[string]any{"type_id": viaRota.tipoB.String()}, rota, b.admin)
	b.confirmarEExecutar(t, "editar_material", map[string]any{
		"material_id": viaTool.mat.String(), "titulo": "Novo título", "roteiro": "Novo roteiro", "tipo_id": viaTool.tipoB.String()})

	t1, ty1, r1 := linhaMaterial(t, b, viaRota.mat)
	t2, ty2, r2 := linhaMaterial(t, b, viaTool.mat)
	require.Equal(t, t1, t2)
	require.Equal(t, r1, r2)
	require.Equal(t, viaRota.tipoB, *ty1)
	require.Equal(t, viaTool.tipoB, *ty2)
}

func TestEditarMaterial_TrocaDeTipoComVeiculacaoEhBarradaENadaMuda(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearMaterial(t, b, true)
	// Regra do tipo B que, se a rota recategorizasse, tiraria a tocada de "bonus".
	mcptest.Regra(t, b.ctx, b.pool, c.camp, c.tipoB, c.est, "2026-10-01", "2026-10-31", 127, "06:00", "22:00", 1)
	args := map[string]any{"material_id": c.mat.String(), "tipo_id": c.tipoB.String()}

	out := b.pedePendente(t, "editar_material", args)

	require.Contains(t, out["motivo"], "veiculação")
	require.Equal(t, "https://portal.teste/materials", out["links"].(map[string]any)["tela"])
	require.Contains(t, out["aviso"], "Nada aconteceu")
	require.Contains(t, out["preview"].(map[string]any)["efeitos"].([]any)[0], "já tem veiculação aprovada",
		"o preview do ramo crítico diz a verdade sobre o estado")
	_, tipo, _ := linhaMaterial(t, b, c.mat)
	require.Equal(t, c.tipoA, *tipo, "nada foi gravado")
	require.Equal(t, []string{"bonus"}, categoriasDoMaterial(t, b, c.mat), "e nada foi recategorizado")
	require.Empty(t, b.sup.Chamadas())
	l := b.auditoria(t, "mcp.pendente.criada")
	require.Len(t, l, 1)
	require.Equal(t, "material", l[0].Recurso)
	require.Equal(t, c.mat, *l[0].RecursoID)
	require.Empty(t, b.auditoria(t, "mcp.escrita.editar_material"))

	// Com o mesmo pedido a rota mudaria o tipo E a categoria: é o que o crítico protege.
	h := &handlers.MaterialsHandler{Repo: catalog.NewMaterials(b.pool), DistRules: catalog.NewDistributionRules(b.pool), EmSegundoPlano: mcptest.Sincrono}
	chamarHandlerCorpo(t, h.UpdateType, http.MethodPatch, "/materials/x/type", map[string]any{"type_id": c.tipoB.String()},
		map[string]string{"id": c.mat.String()}, b.admin)
	require.NotEqual(t, []string{"bonus"}, categoriasDoMaterial(t, b, c.mat), "a rota recategoriza o passado")
}

// Os vizinhos que NÃO escalam.
func TestEditarMaterial_VizinhosQueNaoEscalam(t *testing.T) {
	b := novaBancadaTool(t)

	// (a) tipo igual ao atual junto com outro campo: não há troca de tipo.
	c := semearMaterial(t, b, true)
	pre := b.chamar(t, "editar_material", map[string]any{"material_id": c.mat.String(), "titulo": "Outro", "tipo_id": c.tipoA.String()})
	require.NotEmpty(t, pre["confirmacao"], "tipo igual ao atual não é troca")
	require.NotContains(t, pre, "nivel")

	// (b) troca de tipo SEM veiculação: confirma.
	sem := semearMaterial(t, b, false)
	pre = b.chamar(t, "editar_material", map[string]any{"material_id": sem.mat.String(), "tipo_id": sem.tipoB.String()})
	require.NotEmpty(t, pre["confirmacao"])
	require.NotContains(t, pre, "nivel")

	// (d) OUTRO material do mesmo tipo, na mesma campanha, tem tocada aprovada; o X não tem
	// nenhuma. O recat do X só alcança as projeções do X: não escala.
	x := semearMaterial(t, b, false)
	y := mcptest.Material(t, b.ctx, b.pool, x.cli, x.tipoA, "Outro do mesmo tipo")
	mcptest.Vincular(t, b.ctx, b.pool, x.camp, y, x.est)
	mcptest.Veiculacao(t, b.ctx, b.pool, x.camp, y, x.est, "bonus", mcptest.Quando("2026-10-10", "10:00"))
	pre = b.chamar(t, "editar_material", map[string]any{"material_id": x.mat.String(), "tipo_id": x.tipoB.String()})
	require.NotEmpty(t, pre["confirmacao"], "a tocada é de outro material do mesmo tipo e campanha")
	require.NotContains(t, pre, "nivel")

	// (c) tocada que o recat não consideraria (retratada) não conta.
	retr := semearMaterial(t, b, true)
	_, err := b.pool.Exec(b.ctx, `UPDATE detections SET retracted_at = now() WHERE commercial_id = $1`, retr.mat)
	require.NoError(t, err)
	pre = b.chamar(t, "editar_material", map[string]any{"material_id": retr.mat.String(), "tipo_id": retr.tipoB.String()})
	require.NotEmpty(t, pre["confirmacao"], "tocada retratada não é veiculação aprovada")
}

// F3-2: se entre o preview e a confirmação entrou uma tocada, a confirmação
// devolve o ramo crítico e o token NÃO é consumido.
func TestEditarMaterial_EscaladaNaConfirmacaoBarraENaoConsomeOToken(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearMaterial(t, b, false)
	args := map[string]any{"material_id": c.mat.String(), "tipo_id": c.tipoB.String()}
	pre := b.preview(t, "editar_material", args)

	mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, c.mat, c.est, "bonus", mcptest.Quando("2026-10-10", "10:00"))
	out := b.chamar(t, "editar_material", map[string]any{"material_id": c.mat.String(), "tipo_id": c.tipoB.String(), "confirmacao": pre["confirmacao"]})

	require.Equal(t, "critico", out["nivel"])
	require.Equal(t, false, out["executou"])
	_, tipo, _ := linhaMaterial(t, b, c.mat)
	require.Equal(t, c.tipoA, *tipo)
	var usadas int
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT count(*) FROM mcp_confirmacoes_usadas`).Scan(&usadas))
	require.Zero(t, usadas)
}

func TestEditarMaterial_Recusas(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearMaterial(t, b, false)
	id := c.mat.String()

	for nome, caso := range map[string]struct {
		args   map[string]any
		codigo string
	}{
		"material inválido":      {map[string]any{"material_id": "x", "titulo": "T"}, "[entrada_invalida]"},
		"material inexistente":   {map[string]any{"material_id": uuid.NewString(), "titulo": "T"}, "[nao_encontrado]"},
		"nenhum campo":           {map[string]any{"material_id": id}, "[entrada_invalida]"},
		"título em branco":       {map[string]any{"material_id": id, "titulo": "   "}, "[entrada_invalida]"},
		"título longo demais":    {map[string]any{"material_id": id, "titulo": repete("t", 201)}, "[entrada_invalida]"},
		"tipo inválido":          {map[string]any{"material_id": id, "tipo_id": "x"}, "[entrada_invalida]"},
		"tipo inexistente":       {map[string]any{"material_id": id, "tipo_id": uuid.NewString()}, "[nao_encontrado]"},
		"roteiro longo demais":   {map[string]any{"material_id": id, "roteiro": repete("r", 20001)}, "[entrada_invalida]"},
		"nada muda":              {map[string]any{"material_id": id, "titulo": "Título original", "tipo_id": c.tipoA.String()}, "[entrada_invalida]"},
		"roteiro vazio já vazio": {map[string]any{"material_id": id, "roteiro": ""}, "[entrada_invalida]"},
	} {
		texto := b.chamarErro(t, "editar_material", caso.args)
		require.Contains(t, texto, caso.codigo, nome)
	}
	titulo, tipo, roteiro := linhaMaterial(t, b, c.mat)
	require.Equal(t, "Título original", titulo)
	require.Equal(t, c.tipoA, *tipo)
	require.Nil(t, roteiro)
	require.Zero(t, b.totalAuditorias(t))
}

// Título e roteiro são gravados ANTES do tipo: o tipo é o que dispara o recat, e
// uma falha dele depois de uma escrita já feita NÃO pode ser Recusa (a Recusa
// libera o token; aqui algo foi gravado).
func TestEditarMaterial_ExecutarGravaTituloAntesDoTipoEFalhaTardiaNaoEhRecusa(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearMaterial(t, b, false)
	roteiro := "Roteiro novo"
	var r *acoes.Recusa

	// O tipo é inexistente (o banco recusaria pela FK). Título e roteiro já foram.
	err := aplicarEdicaoDeMaterial(b.ctx, b.E, &edicaoDeMaterial{
		id: c.mat, mudaTitulo: true, titulo: "Título novo", mudaRoteiro: true, roteiro: &roteiro,
		mudaTipo: true, tipoNovo: uuid.New(),
	})
	require.Error(t, err)
	require.False(t, errors.As(err, &r), "falha depois de escrita é erro interno, não Recusa: %v", err)
	titulo, tipo, gravado := linhaMaterial(t, b, c.mat)
	require.Equal(t, "Título novo", titulo, "o título foi gravado antes do tipo")
	require.Equal(t, roteiro, *gravado)
	require.Equal(t, c.tipoA, *tipo, "o tipo não mudou")

	// Sem escrita antes, a mesma falha do tipo segue sendo Recusa (o token volta).
	err = aplicarEdicaoDeMaterial(b.ctx, b.E, &edicaoDeMaterial{id: c.mat, mudaTipo: true, tipoNovo: uuid.New()})
	require.ErrorAs(t, err, &r)
	require.Equal(t, acoes.CodigoEntradaInvalida, r.Codigo)

	// Material que sumiu antes de qualquer escrita: nao_encontrado, Recusa.
	err = aplicarEdicaoDeMaterial(b.ctx, b.E, &edicaoDeMaterial{id: uuid.New(), mudaTitulo: true, titulo: "x"})
	require.ErrorAs(t, err, &r)
	require.Equal(t, acoes.CodigoNaoEncontrado, r.Codigo)
}
