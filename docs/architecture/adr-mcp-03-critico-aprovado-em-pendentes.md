---
status: implementado
ultima-verificacao: 2026-11-09
codigo-relacionado:
  - workers/internal/mcp/aprovacao.go
  - workers/internal/mcp/pendentes.go
  - workers/internal/mcp/pendentes_http.go
  - workers/internal/mcp/varredor.go
  - workers/internal/mcp/principal.go
  - frontend/src/pages/PendentesPage.jsx
  - migrations/0072_mcp_acoes_pendentes.up.sql
---
# ADR-MCP-03: escrita crítica vira pedido aprovado por quem pediu em `/pendentes`, e a aprovação confere o que a pessoa leu

- **Status:** aceito
- **Data:** 2026-10-30 (decisão, plano da Fatia 4), 2026-11-09 (registro como ADR, depois da revisão final)
- **Relacionado:** [features/mcp.md](../features/mcp.md) ("Crítico e /pendentes (Fatia 4)"),
  [operations/mcp.md](../operations/mcp.md), [plano da Fatia 4](../superpowers/plans/2026-10-30-mcp-fatia-4-critico.md)
  (F4-1 a F4-16), [ADR-MCP-02](adr-mcp-02-escrita-em-tres-niveis.md), contrato comum (§6, "só quem pediu aprova").

## Contexto

Uma escrita `critico` (cancelar ou apagar campanha, mexer em preço ou CPM fixo, criar ou ignorar veiculação, criar
ou desativar usuário, reenviar pós-venda…) não pode depender só do modelo, nem de um token que o próprio modelo
reenvia. O contrato dos MCPs da E-Series pede um humano numa tela do produto, e que só quem pediu possa aprovar. Entre
o pedido e a aprovação, o mundo muda: a campanha recebe tocadas, o usuário é rebaixado, o pedido é reescrito.

## Decisão

1. **A tool crítica registra um pedido** em `mcp_acoes_pendentes` (24 h) e devolve `aprovar_em` (`/pendentes?id=…`).
   Nada é escrito na chamada. O mesmo pedido aberto não duplica (índice único parcial em `(user_id, args_hash)`).
2. **Só quem pediu vê e aprova.** Todo SQL filtra `user_id`, e "não existe" e "não é seu" respondem igual. A tela
   `/pendentes` é do portal (JWT admin) e tem defesa contra moldura (cabeçalho em `_headers` e guarda em JS).
3. **A aprovação reexecuta a conferência, sem confiar no retrato.** Ela revalida os argumentos pelo mesmo schema do
   SDK, **reroda o `Preview` e compara o resumo com o gravado**: se mudou, recusa com `preview_desatualizado` e grava o
   resumo novo. A aprovação leva a `tentativa` que a pessoa leu, e o servidor responde 409 `pendente_mudou` se o pedido
   mudou desde então. Por isso **o `Preview` tem de ser determinístico**: nada de contador volátil, "agora" ou ordem de
   mapa no texto.
4. **Estados, como no hub:** `pendente → aprovada → executada`. Uma recusa de negócio na execução volta o pedido a
   `pendente`, com o erro e uma folga de prazo. Erro depois de gravar vira `falhou`, que é final. `recusada` e
   `expirada` também são finais. Toda transição é `UPDATE … WHERE estado = … AND tentativa = …`.
5. **A credencial revogada mata os pedidos dela.** Reset de senha, desativação, rebaixamento ou revogação manual expiram
   na hora os pedidos `pendente` daquela credencial, e a aprovação recusa um pedido de credencial revogada.
6. **A auditoria fica no executor.** O pedido, a aprovação e o desfecho entram no `audit_log` (`mcp.pendente.*`,
   `mcp.escrita.<tool>` com `pendente_id`). `ArgsParaAuditoria` resume o que não pode ficar inteiro, como a URL do
   webhook. As ressalvas da execução são gravadas e aparecem na resposta da aprovação, em `/pendentes` e em
   `minhas_pendentes`.
7. **Um varredor na API** (1 min, advisory lock) expira pedidos, fecha como `falhou` o que ficou `aprovada` por mais de
   10 min e poda `mcp_confirmacoes_usadas`.

## Consequências

- **Positivas:** ninguém aprova às cegas uma coisa diferente da que leu. Um token vazado ou uma conta rebaixada não deixa
  pedido aprovável para trás. O mesmo `Escrita[In]` serve à tool e à aprovação, então o efeito aprovado é o efeito
  testado.
- **Aceitas:** a aprovação é síncrona. Uma execução lenta pode terminar depois de a tela ter recebido timeout, e o pedido
  fecha `executada` mesmo assim; a tela diz "não sabemos se foi feito, recarregue". A guarda de "último admin" só é
  exercitada pela corrida de duas aprovações cruzadas, porque quem pede é sempre um admin ativo diferente do alvo.
- **Negativas / dívida:** a corrida do último admin (a contagem fica fora da transação da escrita), a poda de
  `mcp_acoes_pendentes`, que guarda os args para sempre, e o item `/pendentes` na barra lateral. Lista completa no handoff
  da Fatia 5 (`docs/superpowers/HANDOFF-2026-11-03-mcp-fatia-5.md`).
