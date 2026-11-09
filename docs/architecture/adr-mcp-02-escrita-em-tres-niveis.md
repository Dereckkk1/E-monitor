---
status: implementado
ultima-verificacao: 2026-11-09
codigo-relacionado:
  - workers/internal/mcp/escrita.go
  - workers/internal/mcp/registro_escritas.go
  - workers/internal/mcp/instrucoes.go
  - workers/internal/acoes/recusa.go
  - migrations/0073_mcp_confirmacoes.up.sql
---
# ADR-MCP-02: escrita em três níveis, com o nível subindo quando a escrita atinge dia já veiculado

- **Status:** aceito
- **Data:** 2026-10-29 (decisão, plano da Fatia 3), 2026-11-09 (registro como ADR)
- **Relacionado:** [features/mcp.md](../features/mcp.md), [spec](../superpowers/specs/2026-10-27-mcp-server-design.md)
  §5 (D5, D6, D8), [plano da Fatia 3](../superpowers/plans/2026-10-29-mcp-fatia-3-escrita.md), contrato comum
  (`hub/docs/architecture/mcp-ecossistema.md`, §3.5 e §6), [ADR-MCP-03](adr-mcp-03-critico-aprovado-em-pendentes.md).

## Contexto

Quase toda escrita do E-monitor mexe, direta ou indiretamente, na categoria de uma tocada, e a categoria decide o
déficit, a bonificação e a cobrança ([quota-aware-categorization](../features/quota-aware-categorization.md)). Os MCPs
da Plura e do hub já classificam a escrita em `livre` / `confirma` / `critico`. A regra pura ("mexe em dinheiro? é
crítico") jogaria quase todo o catálogo do E-monitor em `critico`, inclusive mudanças no futuro que nenhum cliente viu.

## Decisão

1. **Três níveis, com o mesmo texto de instruções do hub.** `livre` executa na hora. `confirma` devolve um preview e um
   token na primeira chamada e só executa na segunda, com os mesmos argumentos e o token. `critico` nunca executa na
   chamada (ADR-MCP-03). Uma escrita é `critico` quando é irreversível na prática, mostra ao cliente algo novo, mexe em
   dinheiro já realizado ou dispara email para fora; quando é interna e desfazível, é `livre`; o resto é `confirma`.
2. **O nível sobe com os argumentos (`EscalarPara`) e nunca desce.** Escrita que recategoriza um **dia anterior a hoje
   (America/Sao_Paulo)** vira `critico`. Assim, mudanças no futuro ficam em `confirma`, e o humano entra quando muda um
   número que o cliente já viu ou que já foi faturado.
3. **Token de confirmação:** HMAC-SHA256 (`JWT_SECRET` com separação de domínio) sobre a tool, os argumentos
   canônicos, o usuário e a validade (10 min + 1 de folga), comparado em tempo constante. O uso único é um `INSERT … ON
   CONFLICT DO NOTHING` em `mcp_confirmacoes_usadas`, porque o E-monitor não tem Redis. O token é **consumido antes de
   executar** e só é liberado quando a falha é de negócio (`acoes.Recusa`): erro interno ou `panic` não libera, porque
   algo pode ter sido gravado.
4. **Uma lista única de escritas** (`escritasDe`) registra as tools e alimenta o executor dos pedidos críticos. O mesmo
   `Escrita[In]` serve aos dois caminhos, então nenhuma lógica tem segunda cópia. Toda descrição de escrita termina em
   `Nível:`, e o inventário trava a lista.
5. **A recusa nunca é ambígua.** `acoes.Recusa` significa "nada foi gravado" e `acoes.ErroAposEscrita` significa "gravou
   e não terminou". Quando a escrita já foi gravada e só a releitura falha, o resultado não vira erro (`feitoRelido`),
   porque o modelo repetiria a escrita.

## Consequências

- **Positivas:** o caso comum (mexer no futuro) custa uma confirmação, não uma ida ao portal. O replay de um token de
  confirmação nunca duplica uma criação.
- **Aceitas:** o consumo do token não está na mesma transação da escrita. Nenhuma das funções reaproveitadas das rotas
  aceita `pgx.Tx`, e passar uma transação por todas mudaria o comportamento das rotas. A auditoria também é gravada
  depois do sucesso, fora da transação.
- **Negativas / dívida:** a escalada depende da data corrente em SP. Um pedido que atravessa a meia-noite pode mudar de
  preview (ver ADR-MCP-03, `preview_desatualizado`).
