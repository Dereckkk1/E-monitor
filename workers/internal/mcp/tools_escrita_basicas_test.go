package mcp

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/api/handlers"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

// campanhaComDeficit semeia uma campanha ativa que não recebeu tocada nenhuma:
// cada dia fechado dos últimos 7 vira uma notificação do sininho.
func campanhaComDeficit(t *testing.T, b *bancadaTool) {
	t.Helper()
	cli := mcptest.Cliente(t, b.ctx, b.pool, "Cliente Sininho")
	est := mcptest.Emissora(t, b.ctx, b.pool, "Rádio Sininho", 0)
	tipo := mcptest.TipoMaterial(t, b.ctx, b.pool, "Spot")
	ini := time.Now().AddDate(0, 0, -10).Format("2006-01-02")
	fim := time.Now().AddDate(0, 0, 10).Format("2006-01-02")
	camp := mcptest.Campanha(t, b.ctx, b.pool, cli, "Com déficit", ini, fim, est)
	mcptest.EstadoCampanha(t, b.ctx, b.pool, camp, "ativa")
	mcptest.Regra(t, b.ctx, b.pool, camp, tipo, est, ini, fim, 127, "06:00", "22:00", 1)
}

func chavesDoSininho(t *testing.T, b *bancadaTool, usuario uuid.UUID) []string {
	t.Helper()
	h := &handlers.NotificationsHandler{Repo: catalog.NewNotifications(b.pool)}
	var res catalog.NotificationsResult
	require.NoError(t, json.Unmarshal(chamarHandlerCorpo(t, h.List, http.MethodGet, "/admin/notifications", nil, nil, usuario), &res))
	chaves := make([]string, len(res.Items))
	for i, n := range res.Items {
		chaves[i] = n.Key
	}
	return chaves
}

func leituras(t *testing.T, b *bancadaTool, usuario uuid.UUID) map[string]bool {
	t.Helper()
	rows, err := b.pool.Query(b.ctx, `SELECT notification_key FROM notification_reads WHERE user_id = $1`, usuario)
	require.NoError(t, err)
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var k string
		require.NoError(t, rows.Scan(&k))
		out[k] = true
	}
	require.NoError(t, rows.Err())
	return out
}

func TestMarcarNotificacoesLidas_LivreExecutaNaPrimeiraChamadaSoParaOPrincipal(t *testing.T) {
	b := novaBancadaTool(t)
	campanhaComDeficit(t, b)
	outro := mcptest.Usuario(t, b.ctx, b.pool, "admin", true)
	chaves := chavesDoSininho(t, b, b.admin)
	require.GreaterOrEqual(t, len(chaves), 2)

	// livre: sem token no schema, executa na primeira chamada.
	require.NotContains(t, propriedades(esquemaDa(t, b.cfg, "marcar_notificacoes_lidas")), "confirmacao")
	out := b.chamar(t, "marcar_notificacoes_lidas", map[string]any{"notificacoes": []string{chaves[0], chaves[1]}})

	require.Equal(t, true, out["executou"])
	require.EqualValues(t, 2, out["resultado"].(map[string]any)["marcadas"])
	require.Equal(t, map[string]bool{chaves[0]: true, chaves[1]: true}, leituras(t, b, b.admin))
	require.Empty(t, leituras(t, b, outro), "a leitura é por usuário")

	// Marcar de novo é idempotente e diz que já estavam lidas.
	de2 := b.chamar(t, "marcar_notificacoes_lidas", map[string]any{"notificacoes": []string{chaves[0]}})
	require.EqualValues(t, 0, de2["resultado"].(map[string]any)["marcadas"])
	require.NotEmpty(t, de2["ressalvas"])

	l := b.auditoria(t, "mcp.escrita.marcar_notificacoes_lidas")
	require.Len(t, l, 2)
	require.Equal(t, "notification", l[0].Recurso)
	require.Equal(t, b.p.TokenID.String(), l[0].Meta["mcp"].(map[string]any)["token_id"])
	require.Equal(t, "livre", l[0].Meta["nivel"])
	require.Equal(t, []any{chaves[0], chaves[1]}, l[0].Meta["args"].(map[string]any)["notificacoes"],
		"o campo chama notificacoes (e não chaves): o mascarador de auditoria esconderia o valor")
}

// Paridade de efeito: o mesmo conjunto de chaves, pela rota e pela tool, deixa
// as mesmas linhas em notification_reads (cada uma para o seu usuário).
func TestMarcarNotificacoesLidas_ParidadeComARota(t *testing.T) {
	b := novaBancadaTool(t)
	campanhaComDeficit(t, b)
	viaRota := mcptest.Usuario(t, b.ctx, b.pool, "admin", true)
	viaTool := mcptest.Usuario(t, b.ctx, b.pool, "admin", true)
	chaves := chavesDoSininho(t, b, viaRota)
	require.GreaterOrEqual(t, len(chaves), 3)
	h := &handlers.NotificationsHandler{Repo: catalog.NewNotifications(b.pool)}

	// Lote de chaves.
	var marcadasRota struct {
		Marked int `json:"marked"`
	}
	require.NoError(t, json.Unmarshal(chamarHandlerCorpo(t, h.MarkRead, http.MethodPost, "/admin/notifications/mark-read",
		map[string]any{"keys": chaves[:2]}, nil, viaRota), &marcadasRota))
	pTool := &Principal{UserID: viaTool, Email: "t@mcp.teste", Nome: "T", TokenID: uuid.New()}
	out := b.chamarComo(t, pTool, "marcar_notificacoes_lidas", map[string]any{"notificacoes": chaves[:2]})
	require.EqualValues(t, marcadasRota.Marked, out["resultado"].(map[string]any)["marcadas"])
	require.Equal(t, leituras(t, b, viaRota), leituras(t, b, viaTool))

	// "Marcar todas": a janela inteira.
	require.NoError(t, json.Unmarshal(chamarHandlerCorpo(t, h.MarkAllRead, http.MethodPost, "/admin/notifications/mark-all-read", nil, nil, viaRota), &marcadasRota))
	out = b.chamarComo(t, pTool, "marcar_notificacoes_lidas", map[string]any{"todas": true})
	require.EqualValues(t, marcadasRota.Marked, out["resultado"].(map[string]any)["marcadas"])
	require.Equal(t, leituras(t, b, viaRota), leituras(t, b, viaTool))
	require.Len(t, leituras(t, b, viaTool), len(chaves))
}

func TestMarcarNotificacoesLidas_Recusas(t *testing.T) {
	b := novaBancadaTool(t)
	campanhaComDeficit(t, b)
	chave := chavesDoSininho(t, b, b.admin)[0]
	muitas := make([]string, 201)
	for i := range muitas {
		muitas[i] = "campaign_failure:" + uuid.NewString() + ":2026-10-01"
	}

	for nome, args := range map[string]map[string]any{
		"as duas formas":         {"notificacoes": []string{chave}, "todas": true},
		"nenhuma":                {},
		"lista vazia":            {"notificacoes": []string{}},
		"acima de 200":           {"notificacoes": muitas},
		"chave fora do formato":  {"notificacoes": []string{"qualquer-coisa"}},
		"chave inventada (tipo)": {"notificacoes": []string{"daily_failures_digest:2026-10-01"}},
		"elemento vazio":         {"notificacoes": []string{chave, ""}},
	} {
		texto := b.chamarErro(t, "marcar_notificacoes_lidas", args)
		require.Contains(t, texto, "[entrada_invalida]", nome)
	}
	require.Empty(t, leituras(t, b, b.admin), "nenhuma recusa gravou")
	require.Zero(t, b.totalAuditorias(t), "recusa de entrada não audita")
}

func materialComSimilaridade(t *testing.T, b *bancadaTool, cliente, tipo uuid.UUID, titulo string, parecido uuid.UUID, score float64) uuid.UUID {
	t.Helper()
	m := mcptest.Material(t, b.ctx, b.pool, cliente, tipo, titulo)
	_, err := b.pool.Exec(b.ctx, `UPDATE materials SET similarity_check_status = 'ready', most_similar_material_id = $1, similarity_score = $2 WHERE id = $3`,
		parecido, score, m)
	require.NoError(t, err)
	return m
}

func reconhecidoEm(t *testing.T, b *bancadaTool, material uuid.UUID) *time.Time {
	t.Helper()
	var quando *time.Time
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT similarity_acknowledged_at FROM materials WHERE id = $1`, material).Scan(&quando))
	return quando
}

func TestReconhecerSimilaridade_LivreExecutaEDevolveOMaterial(t *testing.T) {
	b := novaBancadaTool(t)
	cli := mcptest.Cliente(t, b.ctx, b.pool, "Cliente")
	tipo := mcptest.TipoMaterial(t, b.ctx, b.pool, "Spot")
	original := mcptest.Material(t, b.ctx, b.pool, cli, tipo, "Original")
	copia := materialComSimilaridade(t, b, cli, tipo, "Cópia", original, 0.8)

	require.NotContains(t, propriedades(esquemaDa(t, b.cfg, "reconhecer_similaridade_material")), "confirmacao")
	out := b.chamar(t, "reconhecer_similaridade_material", map[string]any{"material_id": copia.String()})

	require.Equal(t, true, out["executou"])
	require.NotNil(t, reconhecidoEm(t, b, copia))
	res := out["resultado"].(map[string]any)
	m := res["material"].(map[string]any)
	require.Equal(t, copia.String(), m["id"])
	require.NotNil(t, m["similaridade"].(map[string]any)["reconhecida_em"], "mesmo formato do obter_material, já com o reconhecimento")
	require.Empty(t, res["ressalvas"], "o aviso de duplicata sumiu")
	b.exigeAuditoria(t, "mcp.escrita.reconhecer_similaridade_material", "material", &copia)
}

func TestReconhecerSimilaridade_ParidadeComARota(t *testing.T) {
	b := novaBancadaTool(t)
	cli := mcptest.Cliente(t, b.ctx, b.pool, "Cliente")
	tipo := mcptest.TipoMaterial(t, b.ctx, b.pool, "Spot")
	original := mcptest.Material(t, b.ctx, b.pool, cli, tipo, "Original")
	viaRota := materialComSimilaridade(t, b, cli, tipo, "Cópia A", original, 0.8)
	viaTool := materialComSimilaridade(t, b, cli, tipo, "Cópia B", original, 0.8)

	h := &handlers.MaterialsHandler{Repo: catalog.NewMaterials(b.pool)}
	chamarHandlerCorpo(t, h.Acknowledge, http.MethodPost, "/materials/x/similarity/acknowledge", nil,
		map[string]string{"id": viaRota.String()}, b.admin)
	b.chamar(t, "reconhecer_similaridade_material", map[string]any{"material_id": viaTool.String()})

	linha := func(id uuid.UUID) (string, bool, *float32, *uuid.UUID) {
		var status string
		var ack bool
		var score *float32
		var parecido *uuid.UUID
		require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT similarity_check_status, similarity_acknowledged_at IS NOT NULL, similarity_score, most_similar_material_id
			FROM materials WHERE id = $1`, id).Scan(&status, &ack, &score, &parecido))
		return status, ack, score, parecido
	}
	s1, a1, sc1, p1 := linha(viaRota)
	s2, a2, sc2, p2 := linha(viaTool)
	require.Equal(t, s1, s2)
	require.True(t, a1)
	require.Equal(t, a1, a2)
	require.Equal(t, sc1, sc2)
	require.Equal(t, p1, p2)
}

func TestReconhecerSimilaridade_Recusas(t *testing.T) {
	b := novaBancadaTool(t)
	cli := mcptest.Cliente(t, b.ctx, b.pool, "Cliente")
	tipo := mcptest.TipoMaterial(t, b.ctx, b.pool, "Spot")
	original := mcptest.Material(t, b.ctx, b.pool, cli, tipo, "Original")

	require.Contains(t, b.chamarErro(t, "reconhecer_similaridade_material", map[string]any{"material_id": "nao-e-uuid"}), "[entrada_invalida]")
	// A rota daria 204 para um id inexistente; a tool diz que não existe.
	require.Contains(t, b.chamarErro(t, "reconhecer_similaridade_material", map[string]any{"material_id": uuid.NewString()}), "[nao_encontrado]")

	// Sem aviso pendente (checagem ainda não pronta ou score baixo): reconhecer
	// de antemão esconderia, sem ninguém ver, o aviso que a checagem ainda vai gerar.
	semAviso := mcptest.Material(t, b.ctx, b.pool, cli, tipo, "Sem aviso")
	texto := b.chamarErro(t, "reconhecer_similaridade_material", map[string]any{"material_id": semAviso.String()})
	require.Contains(t, texto, "[entrada_invalida]")
	require.Nil(t, reconhecidoEm(t, b, semAviso))
	baixo := materialComSimilaridade(t, b, cli, tipo, "Score baixo", original, 0.2)
	require.Contains(t, b.chamarErro(t, "reconhecer_similaridade_material", map[string]any{"material_id": baixo.String()}), "[entrada_invalida]")
	require.Nil(t, reconhecidoEm(t, b, baixo))
	require.Zero(t, b.totalAuditorias(t), "recusa não audita")
}

// Já reconhecido: idempotente, não regrava o instante e avisa.
func TestReconhecerSimilaridade_JaReconhecidoNaoRegravaEAvisa(t *testing.T) {
	b := novaBancadaTool(t)
	cli := mcptest.Cliente(t, b.ctx, b.pool, "Cliente")
	tipo := mcptest.TipoMaterial(t, b.ctx, b.pool, "Spot")
	original := mcptest.Material(t, b.ctx, b.pool, cli, tipo, "Original")
	copia := materialComSimilaridade(t, b, cli, tipo, "Cópia", original, 0.8)
	b.chamar(t, "reconhecer_similaridade_material", map[string]any{"material_id": copia.String()})
	antes := reconhecidoEm(t, b, copia)
	require.NotNil(t, antes)

	out := b.chamar(t, "reconhecer_similaridade_material", map[string]any{"material_id": copia.String()})

	require.Equal(t, true, out["executou"])
	require.NotEmpty(t, out["ressalvas"])
	require.Equal(t, antes, reconhecidoEm(t, b, copia), "o instante original fica")
}
