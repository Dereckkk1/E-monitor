---
status: implementado
ultima-verificacao: 2026-11-09
codigo-relacionado:
  - workers/internal/mcp/
  - workers/internal/acoes/
  - workers/cmd/api/main.go
  - migrations/0070_mcp_fundacao.up.sql
---
# ADR-MCP-01: servidor MCP dentro da API Go, com as tools chamando as mesmas funções das telas

- **Status:** aceito
- **Data:** 2026-10-27 (decisão), 2026-11-09 (registro como ADR)
- **Relacionado:** [features/mcp.md](../features/mcp.md), [operations/mcp.md](../operations/mcp.md),
  [spec de desenho](../superpowers/specs/2026-10-27-mcp-server-design.md) (D1 a D4, D9, D10), contrato comum dos MCPs
  da E-Series (`hub/docs/architecture/mcp-ecossistema.md`, §6), Plura ADR-010.

## Contexto

O dono quer perguntar ao E-monitor em linguagem natural e operar o sistema por um assistente (claude.ai, Claude Code,
Gemini Enterprise), no mesmo padrão dos MCPs da Plura e do hub. O E-monitor tem números delicados: categorização por
cota, base financeira `in_slot + bonus`, impactos, tratamento de `cancelada`. Uma resposta do MCP que discorde de uma
tela é pior do que não ter MCP.

Os caminhos eram: um serviço separado que chama a API HTTP com uma credencial de serviço; o MCP dentro da API Go,
chamando o banco; ou JSON-RPC escrito à mão. O serviço separado precisaria de uma credencial guardada e de um segundo
salto, e o JSON-RPC à mão reimplementaria a negociação de versão e a validação, sem como testar contra os clientes reais.

## Decisão

1. **O MCP mora na API Go** (`POST /mcp`, pacote `workers/internal/mcp/`), com o SDK oficial
   `github.com/modelcontextprotocol/go-sdk`, Streamable HTTP **sem sessão** (um servidor novo por pedido, JSON, sem
   SSE; `GET`/`DELETE` → 405).
2. **Regra de ouro: a tool nunca reimplementa regra de negócio.** Ela chama a mesma função que o handler HTTP chama. Se a
   lógica estava presa no handler, ela foi extraída primeiro, com o handler travado por teste antes da extração (as
   escritas estão em `workers/internal/acoes`, e as leituras em `catalog` e nos serviços das telas). Toda tool que
   espelha uma tela tem teste de paridade (mesmo número no mesmo filtro).
3. **Só `role=admin`**, relido do banco a cada pedido. Token pessoal `emonitor_mcp_` (hash `SHA-256(JWT_SECRET:token)`)
   e **OAuth 2.1 desde a v1**, com a própria API como authorization server, porque claude.ai e Gemini Enterprise só se
   conectam por OAuth. O token do MCP só abre o `/mcp`. Rebaixar ou desativar o usuário revoga as credenciais por
   trigger em `users` (`users_revoga_mcp`).
4. **Rate limit em memória** (240/min por hash do token), reusando o limiter do `/auth/login`. A API não usa Redis e
   roda num processo só.
5. **Relatórios = dados estruturados + deep-link para a tela já filtrada**, nunca URL assinada de arquivo. Os PDFs do
   E-monitor nascem no navegador, e uma URL assinada é credencial ao portador que fica no histórico do chat.

## Consequências

- **Positivas:** um só deploy. Nenhuma credencial de serviço. O MCP herda as correções das telas no mesmo commit. A
  paridade é testável no mesmo banco de teste.
- **Aceitas:** as extrações para `internal/acoes` mudaram três rotas HTTP de comportamento: material vinculado responde
  409, preço com FK inexistente responde 4xx em vez de 500, e `DELETE /campaigns` cancela com `cancelled_at` e responde
  409 com lote manual ou pós-venda. A lista está em [features/mcp.md](../features/mcp.md), em "Recusas que só o MCP faz".
  Como o dono do token é sempre admin, as tools **não** aplicam o escopo de cliente das telas: **nunca** ligue o `/mcp`
  a uma conta de cliente sem revisitar este ADR.
- **Negativas / dívida:** montar um `Server` por pedido custa ~5 ms de CPU com 83 tools (`tools/list` ~13 ms), porque
  o `AddTool` do go-sdk re-serializa cada schema. A saída é um `Server` por processo com o `Principal` vindo do contexto,
  adiada enquanto o tráfego for pequeno (ver "Desempenho" em `features/mcp.md`). O rate limit em memória
  não sobrevive a mais de uma réplica da API: se um dia houver duas, ele precisa ir para um armazenamento
  compartilhado.
