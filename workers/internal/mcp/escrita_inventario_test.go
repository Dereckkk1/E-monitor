package mcp

// escrita_inventario_test.go — o inventário das tools de escrita (Fatias 3 e 4):
// trava o conjunto, o nível declarado de cada uma e as regras que valem para
// todas (descrição, anotações, campo de confirmação, campos proibidos). Sem
// banco: só lista o tools/list do servidor real.

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// escritasEsperadas é o catálogo da spec §6 "Fatia 3" com o nível DECLARADO de
// cada tool (não o efetivo). Nenhuma tool livre escala. Das 18 `confirma`, 10 têm
// Escalar e podem subir a crítico (o motivo sai na resposta do pedido pendente):
// editar_material, editar_pmm_target, editar_emissora, criar_campanha,
// editar_campanha, criar/editar/apagar_regra_distribuicao e
// definir/apagar_override. As outras 8 NUNCA escalam: criar/editar_tipo_material,
// criar/editar_cliente, editar_emissoras_campanha e vincular/desvincular/
// editar_emissoras_material. Todas as 18 levam `confirmacao` e destructiveHint
// (a anotação é por nível declarado, não por Escalar). As `critico` (Fatia 4)
// são crítico desde a declaração: nunca recebem token, então NÃO levam
// `confirmacao`; a pessoa aprova em /pendentes. Tool de escrita nova entra aqui
// e em ToolsEsperadas no mesmo commit.
var escritasEsperadas = map[string]string{
	"marcar_notificacoes_lidas":        "livre",
	"reconhecer_similaridade_material": "livre",

	"criar_campanha":            "confirma",
	"editar_campanha":           "confirma",
	"editar_emissoras_campanha": "confirma",

	"vincular_material":         "confirma",
	"desvincular_material":      "confirma",
	"editar_emissoras_material": "confirma",
	"editar_material":           "confirma",
	"criar_tipo_material":       "confirma",
	"editar_tipo_material":      "confirma",

	"criar_regra_distribuicao":  "confirma",
	"editar_regra_distribuicao": "confirma",
	"apagar_regra_distribuicao": "confirma",
	"definir_override":          "confirma",
	"apagar_override":           "confirma",

	"criar_cliente":     "confirma",
	"editar_cliente":    "confirma",
	"editar_pmm_target": "confirma",
	"editar_emissora":   "confirma",

	"cancelar_campanha":      "critico",
	"apagar_campanha":        "critico",
	"editar_cpm_fixo":        "critico",
	"editar_precos_campanha": "critico",

	"criar_veiculacao_manual": "critico",
	"ignorar_veiculacao":      "critico",
	"restaurar_veiculacao":    "critico",

	"criar_usuario":     "critico",
	"editar_usuario":    "critico",
	"desativar_usuario": "critico",

	"desativar_cliente": "critico",
	"ativar_cliente":    "critico",
	"apagar_cliente":    "critico",
	"apagar_material":   "critico",

	"editar_webhook_cliente": "critico",
	"testar_webhook":         "critico",
}

// camposProibidosNaEscrita: nenhuma tool de escrita aceita (nem expõe no schema)
// estes campos. stream_url é não-objetivo da spec (§1: operação técnica);
// hub_id é a ponte, que só a Fatia 4/5 liga; senha/token/password são
// credenciais, que o contrato proíbe trafegar por tool. "confirmacao" é o
// único campo-token permitido e não casa com nenhum destes nomes.
var camposProibidosNaEscrita = []string{"stream_url", "hub_id", "senha", "token", "password", "segredo", "secret"}

func listarToolsDoServidor(t *testing.T) []*sdk.Tool {
	t.Helper()
	ctx := context.Background()
	ct, st := sdk.NewInMemoryTransports()
	_, err := novoServidor(ConfigServidor{Versao: "teste"}, principalTeste).Connect(ctx, st, nil)
	require.NoError(t, err)
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "inventario", Version: "1"}, nil).Connect(ctx, ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { cs.Close() })
	res, err := cs.ListTools(ctx, nil)
	require.NoError(t, err)
	return res.Tools
}

// nomesDePropriedades devolve todo nome de propriedade do schema, em qualquer
// profundidade (entradas[].emissora_id, adicionar[], objetos aninhados).
func nomesDePropriedades(v any, acum *[]string) {
	switch x := v.(type) {
	case map[string]any:
		if props, ok := x["properties"].(map[string]any); ok {
			for nome := range props {
				*acum = append(*acum, nome)
			}
		}
		for _, filho := range x {
			nomesDePropriedades(filho, acum)
		}
	case []any:
		for _, filho := range x {
			nomesDePropriedades(filho, acum)
		}
	}
}

func esquemaComoMapa(t *testing.T, tl *sdk.Tool) map[string]any {
	t.Helper()
	bruto, err := json.Marshal(tl.InputSchema)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(bruto, &m))
	return m
}

var sufixoDeNivel = regexp.MustCompile(` Nível: (livre|confirma|critico)\.$`)

// O conjunto de tools de escrita é exatamente o catálogo: toda tool que não é
// de leitura está em escritasEsperadas, e o inverso. Uma escrita nova sem
// entrada aqui (ou uma leitura sem ReadOnlyHint) falha este teste.
func TestInventarioEscrita_ConjuntoEExatamenteOCatalogo(t *testing.T) {
	var escritas []string
	for _, tl := range listarToolsDoServidor(t) {
		require.NotNil(t, tl.Annotations, tl.Name)
		if !tl.Annotations.ReadOnlyHint {
			escritas = append(escritas, tl.Name)
		}
	}
	var esperadas []string
	for nome := range escritasEsperadas {
		esperadas = append(esperadas, nome)
	}
	sort.Strings(escritas)
	sort.Strings(esperadas)
	require.Len(t, esperadas, 36)
	require.Equal(t, esperadas, escritas)
}

// Toda escrita termina a descrição com o nível, igual ao declarado; nenhuma
// leitura ganha esse sufixo. Anotações: nunca ReadOnly, nunca OpenWorld; só a
// `livre` sem escalada é não-destrutiva.
func TestInventarioEscrita_DescricaoTerminaNoNivelEAnotacoes(t *testing.T) {
	for _, tl := range listarToolsDoServidor(t) {
		nivel, escrita := escritasEsperadas[tl.Name]
		if !escrita {
			require.False(t, sufixoDeNivel.MatchString(tl.Description), "leitura com sufixo de nível: %s", tl.Name)
			continue
		}
		m := sufixoDeNivel.FindStringSubmatch(tl.Description)
		require.NotNil(t, m, "%s: a descrição tem de terminar em ` Nível: livre|confirma|critico.`", tl.Name)
		require.Equal(t, nivel, m[1], "%s: nível da descrição ≠ nível do catálogo", tl.Name)

		require.False(t, tl.Annotations.ReadOnlyHint, "%s: escrita não é ReadOnly", tl.Name)
		require.NotNil(t, tl.Annotations.OpenWorldHint, tl.Name)
		require.False(t, *tl.Annotations.OpenWorldHint, "%s: escrita não fala com o mundo aberto", tl.Name)
		require.NotNil(t, tl.Annotations.DestructiveHint, tl.Name)
		require.Equal(t, nivel != "livre", *tl.Annotations.DestructiveHint,
			"%s: destructiveHint = (nível ≠ livre ou pode escalar)", tl.Name)
	}
}

// O crítico agora vira pedido pendente: nenhuma descrição manda "fazer pela
// tela" nem fala em ramo "barrado".
func TestInventarioEscrita_DescricaoNaoFalaMaisEmBarrado(t *testing.T) {
	for _, tl := range listarToolsDoServidor(t) {
		if _, escrita := escritasEsperadas[tl.Name]; !escrita {
			continue
		}
		d := strings.ToLower(tl.Description)
		require.NotContains(t, d, "faça pela tela", tl.Name)
		require.NotContains(t, d, "barrad", tl.Name)
		require.NotContains(t, d, "link da tela", tl.Name)
	}
}

// O campo `confirmacao` está no schema de toda tool que pode pedir confirmação
// (confirma, ou livre escalável) e de nenhuma `livre` sem escalada nem `critico`
// declarada: sem o campo o modelo não teria como devolver o token, numa `livre`
// ele seria ruído e numa `critico` seria uma porta falsa (o crítico nunca
// recebe token; quem executa é a aprovação em /pendentes).
func TestInventarioEscrita_ConfirmacaoNoSchemaSoOndePodePedir(t *testing.T) {
	vistas := 0
	for _, tl := range listarToolsDoServidor(t) {
		nivel, escrita := escritasEsperadas[tl.Name]
		if !escrita {
			continue
		}
		vistas++
		var props []string
		nomesDePropriedades(esquemaComoMapa(t, tl), &props)
		tem := false
		for _, p := range props {
			if p == "confirmacao" {
				tem = true
			}
		}
		switch nivel {
		case "livre":
			require.False(t, tem, "%s: livre sem escalada não tem `confirmacao`", tl.Name)
		case "critico":
			require.False(t, tem, "%s: critico não recebe token, então não tem `confirmacao`", tl.Name)
		default:
			require.True(t, tem, "%s: confirma/escalável precisa de `confirmacao` no schema", tl.Name)
		}
		// O campo é opcional: a 1ª chamada (o preview) não tem token.
		obrigatorios, _ := esquemaComoMapa(t, tl)["required"].([]any)
		for _, obrigatorio := range obrigatorios {
			require.NotEqual(t, "confirmacao", obrigatorio, "%s: confirmacao não é obrigatória", tl.Name)
		}
	}
	require.Equal(t, 36, vistas)
}

// Nenhum schema de escrita carrega campo proibido, em nenhuma profundidade.
func TestInventarioEscrita_SemCamposProibidos(t *testing.T) {
	for _, tl := range listarToolsDoServidor(t) {
		if _, escrita := escritasEsperadas[tl.Name]; !escrita {
			continue
		}
		var props []string
		nomesDePropriedades(esquemaComoMapa(t, tl), &props)
		for _, p := range props {
			for _, proibido := range camposProibidosNaEscrita {
				require.False(t, strings.Contains(strings.ToLower(p), proibido),
					"%s: o campo %q não pode existir numa tool de escrita (casa %q)", tl.Name, p, proibido)
			}
		}
	}
}

// Prova que o detector enxerga campo aninhado: o teste acima não pode passar
// "por não olhar fundo".
func TestInventarioEscrita_NomesDePropriedadesEntraEmItensAninhados(t *testing.T) {
	var props []string
	nomesDePropriedades(map[string]any{
		"properties": map[string]any{
			"entradas": map[string]any{
				"type":  "array",
				"items": map[string]any{"properties": map[string]any{"stream_url": map[string]any{}}},
			},
		},
	}, &props)
	require.ElementsMatch(t, []string{"entradas", "stream_url"}, props)
}
