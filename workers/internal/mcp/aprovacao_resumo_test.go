package mcp

// aprovacao_resumo_test.go — o resumo que a pessoa aprovou é o que executa. A aprovação reroda o
// Preview e compara o resumo inteiro com o gravado no pedido: se o estado mudou entre os dois (o
// que o Preview não recusa, mas muda o que ela veria), a aprovação devolve preview_desatualizado,
// grava o resumo novo e só executa na aprovação seguinte. Aqui, os casos reais das tools críticas
// — e a prova de que aprovar um pedido que não mudou continua executando (cada tool crítica tem o
// seu teste de "aprovar executa"; estes cobrem a divergência).

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/mcp/mcptest"
)

// exigeResumoDesatualizado aprova o pedido e exige a recusa preview_desatualizado: nada executou
// (nenhuma linha mcp.escrita.<tool> nova), o pedido voltou a `pendente` com o porquê e o resumo
// gravado agora é o NOVO — devolvido, como /pendentes o mostra.
func exigeResumoDesatualizado(t *testing.T, b *bancadaTool, tool, pendenteID string) previa {
	t.Helper()
	execucoesAntes := len(b.auditoria(t, "mcp.escrita."+tool))
	_, err := b.tentarAprovar(t, pendenteID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "[preview_desatualizado]")
	require.Equal(t, "pendente", b.estadoDaPendente(t, pendenteID), "recusa de negócio devolve o pedido a pendente")
	require.Len(t, b.auditoria(t, "mcp.escrita."+tool), execucoesAntes, "nada executou")
	id, perr := uuid.Parse(pendenteID)
	require.NoError(t, perr)
	p, perr := b.E.Pendentes.Obter(b.ctx, id, b.admin)
	require.NoError(t, perr)
	require.NotNil(t, p.Erro)
	require.Contains(t, *p.Erro, "O estado mudou desde o pedido")
	return p.Preview
}

// Dois pedidos da MESMA tocada, escritos de jeitos diferentes (o horário em UTC ou em -03:00): os
// argumentos diferem, então o pedido não é deduplicado — e nenhum dos dois resumos avisa de
// duplicata, porque nenhuma tocada existe ainda. Aprovar o primeiro lança a tocada; o segundo,
// agora, mostra o aviso de possível duplicata — e por isso NÃO executa na primeira tentativa:
// a pessoa vê o aviso antes de cobrar duas vezes.
func TestCriarVeiculacaoManual_SegundoPedidoDaMesmaTocadaVemComOAvisoDeDuplicata(t *testing.T) {
	for nome, caso := range map[string]struct{ quandoB, notaB string }{
		"o horário escrito em UTC": {"2026-10-20T11:30:00Z", ""},
		"uma nota diferente":       {"2026-10-20T08:30:00-03:00", "Segunda via"},
		"o mesmo minuto, segundos": {"2026-10-20T08:30:30-03:00", ""},
		"cinco minutos depois":     {"2026-10-20T08:35:00-03:00", ""},
	} {
		t.Run(nome, func(t *testing.T) {
			b := novaBancadaTool(t)
			c := semearCritCamp(t, b, "ativa", uuid.Nil)
			um := b.pedePendente(t, toolCriarVeiculacao, argsCriarVeiculacao(c, "2026-10-20T08:30:00-03:00", ""))
			dois := b.pedePendente(t, toolCriarVeiculacao, argsCriarVeiculacao(c, caso.quandoB, caso.notaB))
			require.NotEqual(t, um["pendente_id"], dois["pendente_id"], "argumentos diferentes: dois pedidos")
			for _, o := range []map[string]any{um, dois} {
				require.NotContains(t, mustJSON(t, o["preview"].(map[string]any)["ressalvas"]), "Já existe veiculação aprovada",
					"nenhuma tocada existia quando os pedidos foram feitos")
			}

			b.aprovar(t, um["pendente_id"].(string))
			require.Equal(t, 1, contarVeiculacoes(t, b, c.camp))

			novo := exigeResumoDesatualizado(t, b, toolCriarVeiculacao, dois["pendente_id"].(string))

			require.Contains(t, mustJSON(t, novo.Ressalvas), "Já existe veiculação aprovada deste material",
				"o resumo gravado agora avisa da duplicata")
			require.Equal(t, 1, contarVeiculacoes(t, b, c.camp), "a segunda NÃO foi lançada")
			// A pessoa viu o aviso e insiste (é outra tocada): agora o resumo é o que ela viu e executa.
			b.aprovar(t, dois["pendente_id"].(string))
			require.Equal(t, 2, contarVeiculacoes(t, b, c.camp))
		})
	}
}

// Uma chave de API nova nasce depois do pedido de apagar o cliente: apagar acabaria com uma chave
// ativa que a pessoa não viu. O resumo recalculado a conta e a aprovação recusa.
func TestApagarCliente_ChaveDeApiNovaDepoisDoPedidoPedeNovaAprovacao(t *testing.T) {
	b := novaBancadaTool(t)
	r := semearClienteComRastro(t, b, "Cliente")
	out := b.pedePendente(t, toolApagarCliente, argsCliente(r.cli))
	require.Contains(t, mustJSON(t, out["preview"].(map[string]any)["efeitos"]), "chaves de API: 2 (1 ativa)")
	_, err := b.pool.Exec(b.ctx, `INSERT INTO api_keys (client_id, key_hash) VALUES ($1, $2)`, r.cli, uuid.NewString())
	require.NoError(t, err)
	antes := contarDoCliente(t, b, r.cli)

	novo := exigeResumoDesatualizado(t, b, toolApagarCliente, out["pendente_id"].(string))

	require.Contains(t, mustJSON(t, novo.Efeitos), "chaves de API: 3 (2 ativas)")
	require.Equal(t, antes, contarDoCliente(t, b, r.cli), "o cliente e o que é dele seguem lá")
	b.aprovar(t, out["pendente_id"].(string))
	require.Equal(t, contagemCli{}, contarDoCliente(t, b, r.cli))
}

// Alguém ganha acesso ao cliente depois do pedido de desativar: o resumo ("2 usuários perdem o
// acesso") já não diz o que a aprovação faria.
func TestDesativarCliente_UsuarioNovoDepoisDoPedidoPedeNovaAprovacao(t *testing.T) {
	b := novaBancadaTool(t)
	r := semearClienteComRastro(t, b, "Cliente Alvo")
	semearCarteiraDeClientes(t, b, r.cli)
	out := b.pedePendente(t, toolDesativarCliente, argsCliente(r.cli))
	require.Contains(t, mustJSON(t, out["preview"].(map[string]any)["efeitos"]), "2 usuário(s) ativo(s) deixam de entrar")
	novoUsuarioDe(t, b, "viewer", true, r.cli)

	novo := exigeResumoDesatualizado(t, b, toolDesativarCliente, out["pendente_id"].(string))

	require.Contains(t, mustJSON(t, novo.Efeitos), "3 usuário(s) ativo(s) deixam de entrar")
	require.True(t, clienteAtivo(t, b, r.cli), "o cliente seguiu ativo")
	b.aprovar(t, out["pendente_id"].(string))
	require.False(t, clienteAtivo(t, b, r.cli))
}

// Outra pessoa preenche o preço de uma emissora entre o pedido e a aprovação: o "de" do resumo
// (sem preço) já não é o que está gravado — a aprovação regravaria por cima de um valor que a pessoa
// nunca viu.
func TestEditarPrecosCampanha_PrecoMudadoPorOutraPessoaPedeNovaAprovacao(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearCritCamp(t, b, "ativa", uuid.Nil)
	out := b.pedePendente(t, "editar_precos_campanha", argsPrecos(c.camp, precoConsolidado(c.e2, 1500.75)))
	require.Contains(t, mustJSON(t, out["preview"].(map[string]any)["mudancas"]), `"de":"sem preço"`)
	mcptest.PrecoConsolidado(t, b.ctx, b.pool, c.camp, c.e2, 900)
	antes := linhasDePreco(t, b, c.camp)

	novo := exigeResumoDesatualizado(t, b, "editar_precos_campanha", out["pendente_id"].(string))

	require.Contains(t, mustJSON(t, novo.Mudancas), "consolidado: R$ 900,00", "o resumo novo mostra o preço que está gravado")
	require.Equal(t, antes, linhasDePreco(t, b, c.camp), "nada foi regravado")
	b.aprovar(t, out["pendente_id"].(string))
	require.NotEqual(t, antes, linhasDePreco(t, b, c.camp))
}

// O mesmo pelo handler HTTP de /pendentes: 422 preview_desatualizado, e o GET seguinte já mostra o
// resumo novo no pedido.
func TestAprovarPelaRota_ResumoDesatualizadoDa422(t *testing.T) {
	b := novaBancadaTool(t)
	r := semearClienteComRastro(t, b, "Cliente")
	out := b.pedePendente(t, toolApagarCliente, argsCliente(r.cli))
	_, err := b.pool.Exec(b.ctx, `INSERT INTO api_keys (client_id, key_hash) VALUES ($1, $2)`, r.cli, uuid.NewString())
	require.NoError(t, err)

	rec := b.aprovarPelaRotaBruto(t, out["pendente_id"].(string))

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"error":"preview_desatualizado"`)
	require.Equal(t, "pendente", b.estadoDaPendente(t, out["pendente_id"].(string)))
}
