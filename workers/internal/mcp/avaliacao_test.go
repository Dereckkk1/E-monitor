package mcp

import (
	"encoding/xml"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"radiocheck/internal/mcp/avaliacao"
	"radiocheck/internal/mcp/mcptest"
)

type perguntasXML struct {
	Pares []struct {
		Pergunta string `xml:"question"`
		Resposta string `xml:"answer"`
	} `xml:"qa_pair"`
}

// TestAvaliacao_Gabarito prova que cada resposta do perguntas.xml sai das
// tools sobre a semente — o gabarito não pode estar errado nem instável. O
// caminho de cada pergunta é o que um modelo faria: acha os ids pelos nomes
// (listar_clientes, listar_campanhas, listar_pos_vendas) e chama a tool.
func TestAvaliacao_Gabarito(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	_, err := pool.Exec(ctx, `DELETE FROM audit_log`)
	require.NoError(t, err)
	_, err = avaliacao.Semear(ctx, pool, time.Now())
	require.NoError(t, err)
	cfg := cfgTeste(pool)
	raw, err := os.ReadFile("avaliacao/perguntas.xml")
	require.NoError(t, err)
	var px perguntasXML
	require.NoError(t, xml.Unmarshal(raw, &px))
	require.Len(t, px.Pares, 10)

	idPorNome := func(tool string, args map[string]any, nome string) string {
		t.Helper()
		for _, it := range itens(t, chamarTool(t, cfg, tool, args)) {
			for _, chave := range []string{"nome", "titulo"} {
				if it[chave] == nome {
					return it["id"].(string)
				}
			}
		}
		t.Fatalf("%s: %q não encontrado", tool, nome)
		return ""
	}
	obj := func(v any) map[string]any { return v.(map[string]any) }
	str := func(v any) string { return fmt.Sprint(v) }

	aurora := idPorNome("listar_clientes", map[string]any{"busca": "Aurora"}, "Aurora Alimentos")
	set := idPorNome("listar_campanhas", map[string]any{"cliente_id": aurora, "limit": 100}, "Aurora Setembro")
	agora := idPorNome("listar_campanhas", map[string]any{"cliente_id": aurora, "limit": 100}, "Aurora Agora")

	got := make([]string, 10)
	// 1. insights_campanha: impactos no target de setembro.
	got[0] = str(chamarTool(t, cfg, "insights_campanha", map[string]any{"cliente_id": aurora, "campanha_ids": []string{set},
		"de": "2026-09-01", "ate": "2026-09-30"})["impactos_target"])
	// 2. visao_gerencial: estados das campanhas ativas do cliente.
	got[1] = str(obj(chamarTool(t, cfg, "visao_gerencial", map[string]any{"cliente_id": aurora, "estado": "ativa"})["kpis"])["estados"])
	// 3. relatorio_data_hora: o valor de tabela das linhas da Atlântica (todas iguais).
	for _, l := range itens(t, chamarTool(t, cfg, "relatorio_data_hora", map[string]any{"cliente_id": aurora, "campanha_ids": []string{set},
		"de": "2026-09-01", "ate": "2026-09-30", "limit": 50})) {
		if obj(l["emissora"])["nome"] == "Rádio Atlântica FM" {
			if got[2] != "" {
				require.Equal(t, got[2], str(l["valor_unitario_tabela"]), "valor de tabela muda entre linhas da mesma emissora")
			}
			got[2] = str(l["valor_unitario_tabela"])
		}
	}
	// 4. resumo_relatorio_campanha: contagem por material.
	for _, m := range chamarTool(t, cfg, "resumo_relatorio_campanha", map[string]any{"campanha_id": set})["por_material"].([]any) {
		if obj(obj(m)["material"])["nome"] == "Aurora Promo 30s" {
			got[3] = str(obj(m)["veiculacoes"])
		}
	}
	// 5. mapa_cobertura_campanha: municípios distintos.
	got[4] = str(chamarTool(t, cfg, "mapa_cobertura_campanha", map[string]any{"campanha_ids": []string{agora}})["cidades_distintas"])
	// 6. obter_falhas_campanha: déficit "não tocou" até ontem.
	got[5] = str(obj(chamarTool(t, cfg, "obter_falhas_campanha", map[string]any{"campanha_id": agora})["resumo"])["deficit_nao_tocou"])
	// 7. falhas_por_dia (o pico) → falhas_emissoras no dia do pico (quem caiu).
	pico := obj(chamarTool(t, cfg, "falhas_por_dia", map[string]any{})["resumo"])
	require.EqualValues(t, 2, pico["pico_emissoras"])
	for _, e := range itens(t, chamarTool(t, cfg, "falhas_emissoras", map[string]any{"data": pico["dia_do_pico"]})) {
		if e["segundos_fora_do_ar"].(float64) > 0 {
			require.Empty(t, got[6], "mais de uma emissora caiu no dia do pico")
			got[6] = str(obj(e["emissora"])["nome"])
		}
	}
	// 8. assertividade do último mês fechado.
	got[7] = fmt.Sprintf("%.1f", obj(chamarTool(t, cfg, "assertividade", map[string]any{"cliente_id": aurora})["mes_fechado"])["percentual"])
	// 9. listar_pos_vendas → destinatarios_pos_venda: quem não abriu.
	pv := idPorNome("listar_pos_vendas", map[string]any{}, "Fechamento Aurora Setembro")
	for _, d := range itens(t, chamarTool(t, cfg, "destinatarios_pos_venda", map[string]any{"pos_venda_id": pv})) {
		if d["aberto_em"] == nil {
			require.Empty(t, got[8], "mais de um destinatário sem abrir")
			got[8] = str(d["nome"])
		}
	}
	// 10. auditoria_mcp: o token criado mais recente.
	for _, e := range itens(t, chamarTool(t, cfg, "auditoria_mcp", map[string]any{"acao": "mcp.token.criado", "limit": 1})) {
		got[9] = str(obj(e["detalhes"])["nome"])
	}

	for i, p := range px.Pares {
		require.Equal(t, p.Resposta, got[i], "pergunta %d: %s", i+1, p.Pergunta)
	}
}
