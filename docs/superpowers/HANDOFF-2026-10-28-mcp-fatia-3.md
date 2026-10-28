# Handoff — MCP do E-monitor (fim da Fatia 2, início da Fatia 3)

Data: 2026-10-28. Abra uma sessão nova do Claude Code e cole o bloco abaixo como primeira mensagem.
**O trabalho acontece no worktree `C:\tmp\emonitor-mcp`** (branch `feat/mcp-server`), não na pasta
principal `C:\Users\marke\Desktop\Programas\E-Series\E-monitor`, que é compartilhada com outras sessões.

**Pré-requisito:** Docker Desktop aberto (os testes de integração usam um Postgres em container).

---

## Prompt para colar na nova sessão

```
Você vai continuar o servidor MCP do E-monitor. As Fatias 0 (fundação), 1 (leitura, 27 tools) e 2
(relatórios, +17 = 44 tools) estão implementadas e revisadas na branch feat/mcp-server. Seu trabalho
agora é a Fatia 3 (escrita nos níveis `livre` e `confirma`).

═══════════════════════════════════════════════════════════════
REGRAS DO DONO (não negociáveis)
═══════════════════════════════════════════════════════════════
- Trabalhe SÓ no worktree C:\tmp\emonitor-mcp, branch feat/mcp-server (confira com
  `git branch --show-current`). A pasta principal é compartilhada com outras sessões: nunca rode
  git checkout/switch/stash nela e não a toque. A branch não tem upstream de propósito.
- NADA vai para o master, e nada é pushado, até o MCP estar 100% pronto. O dono decide quando.
- Leia o CLAUDE.md da raiz antes de tudo: regras 4.8, 5 (nunca `npm install` no frontend; só
  `npm ci`) e 6.x.
- Você NÃO tem acesso à VM nem ao banco de prod (CLAUDE.md §7).
- Mensagens de commit terminam com a linha Co-Authored-By exigida pela sessão.

═══════════════════════════════════════════════════════════════
LEIA NESTA ORDEM
═══════════════════════════════════════════════════════════════
1. docs/superpowers/HANDOFF-2026-10-28-mcp-fatia-3.md  (este arquivo — seções abaixo do prompt)
2. docs/superpowers/specs/2026-10-27-mcp-server-design.md  (spec: D1–D10, §3 regra de ouro, §4.4
   resultado, §4.6 auditoria, §5 níveis de escrita, §6 "Fatia 3", §7 critério da fatia)
3. docs/superpowers/plans/2026-10-28-mcp-fatia-2-relatorios.md  (decisões F2-1..F2-11 e o padrão de
   tasks/testes; as divergências da execução estão em docs/features/mcp.md)
4. docs/features/mcp.md e docs/operations/mcp.md  (o que está implementado HOJE, limitações,
   divergências do plano, avaliação)
5. Contrato comum dos MCPs da E-Series, no repo do hub, branch origin/main:
   `git -C ../hub show origin/main:docs/architecture/mcp-ecossistema.md`  (§3, §4 níveis de escrita)
6. O código: workers/internal/mcp/ (toolkit.go, toolkit_relatorios.go, leitura.go, servidor.go,
   tools_*.go, auditoria/, ferramentas_test.go) — é o padrão que as tools novas seguem.

═══════════════════════════════════════════════════════════════
O QUE FAZER
═══════════════════════════════════════════════════════════════
1. Use a skill superpowers:writing-plans para escrever o plano da Fatia 3 em
   docs/superpowers/plans/AAAA-MM-DD-mcp-fatia-3-escrita.md, a partir da spec §5 e §6 "Fatia 3":
   o motor de níveis (`livre` executa; `confirma` devolve preview + token de confirmação de uso
   único, amarrado aos argumentos, com validade; `mcp_confirmacoes_usadas` nasce nesta fatia),
   `EscalarPara` (dia anterior a hoje / tocada já veiculada → `critico`, que só registra pedido e
   nasce na Fatia 4), a auditoria das escritas com `meta.mcp = {token_id}` (o `Principal.TokenID` já
   existe e não é lido), e as tools da tabela §6 Fatia 3. Cada escrita chama a MESMA função que a rota
   HTTP chama (paridade de efeito tool × rota) e dispara os mesmos gatilhos (recategorização,
   reconciler, projeção). Antes de escrever, leia de verdade cada handler de escrita que uma tool vai
   reaproveitar.
2. Mostre o plano ao dono e espere o OK.
3. Execute com superpowers:subagent-driven-development (lotes coerentes; revisão de aderência +
   qualidade por lote; revisão final da branch). Leia docs/subagentic-use/Subagents-usage.md.
4. Ao fim: atualize docs/features/mcp.md, a memória do projeto e um handoff novo para a Fatia 4.
```

---

## Estado em 2026-10-28

**Branch `feat/mcp-server`**: 105 commits sobre `origin/master` (`git rev-list --count
origin/master..HEAD`, já com este handoff). Nada pushado, mergeado nem deployado. A Fatia 2 vai de
`91873d2` (o plano) ao topo.

Fatias 0, 1 e 2 estão implementadas. Cada lote da Fatia 2 passou por revisão de aderência e de
qualidade, com re-revisão das correções, e a branch inteira foi revisada no fim ("pronta com
ressalvas"; os achados foram corrigidos ou registrados em `docs/features/mcp.md`).

**Verificação final da Fatia 2** (HEAD `0837c8f`, refeita nos pacotes tocados depois da última rodada):
cross-compile linux com e sem `-tags avaliacao`, `go vet ./...`, `go list -deps ./cmd/api` sem
`mcp/avaliacao`; `ok` em `internal/mcp/...`, `supervisor`, `postsale`, `assertiveness` e
`api/handlers`; no `catalog`, só as 4 falhas pré-existentes (provadas também no `91873d2`, antes da
fatia: `TestDetections_ListPaged_IgnoredExcluded`, `TestDetections_CreateManualBatch_WithProof`/`NoProof`,
`TestMaterialTypes_List`); frontend `node --test` 92/92, `vite build` ok, lockfile igual ao master
(emnapi 11 = 11).

**Avaliação mcp-builder: 10/10** (subagente às cegas, só pelo CLI; roteiro em `docs/operations/mcp.md`).

**Desempenho:** `BenchmarkNovoServidor` ≈ **2,75 ms/op** com 44 tools (~2,06 MB, ~5.300 allocs),
abaixo do limite de 4 ms. Não memoizado. Remeça ao crescer o catálogo com as escritas; acima de 4 ms,
memoize o `*sdk.Server` por pedido (o SDK monta duas vezes por pedido).

**As 44 tools** (todas de leitura; lista e fonte de cada uma em `docs/features/mcp.md`, "Tools (44)").
As 17 da Fatia 2: `assertividade`, `auditoria_mcp`, `destinatarios_pos_venda`, `exportar_consolidado`,
`exportar_veiculacoes`, `falhas_campanhas`, `falhas_emissoras`, `falhas_por_dia`, `insights_campanha`,
`listar_pos_vendas`, `mapa_cobertura_campanha`, `obter_falhas_campanha`, `obter_pos_venda`,
`relatorio_data_hora`, `resumo_diario_falhas`, `resumo_relatorio_campanha`, `visao_gerencial`.

**Não verificado ainda (só depois do deploy):** conexão real pelos 3 clientes; paridade com dado real
(roteiro "Conferência com dado real" em `docs/operations/mcp.md`); os deep-links abertos num navegador
logado (o frontend foi verificado por teste do parser, lint e build, não clicando).

## Ambiente de teste

- Container `rc-mcp-test-pg` (Postgres 16, porta **55440**, usuário/senha `radiocheck`, rede
  `rc-mcp-test`). Template migrado até a 0070: `radiocheck_mcp_test_d2`. Ele tem partições de
  `detections`, `detection_campaigns` e `stream_health_events` de 2026_01 em diante, e foi limpo de
  usuários em 2026-10-28.
- **Um banco por agente**, criado do template:
  `docker exec rc-mcp-test-pg psql -U radiocheck -d postgres -c "CREATE DATABASE radiocheck_mcp_test_<x> TEMPLATE radiocheck_mcp_test_d2;"`
- **`export TEST_DATABASE_URL=…` em TODA chamada de Bash** (sem ela os testes de banco dão SKIP e
  parecem verdes). Sempre `go test -p 1`. O pacote `./internal/mcp` leva agora **~8 a 9 min**: timeout
  600000 ms e, se precisar, rode em segundo plano.
- Rode `./internal/mcp/...` antes de `./internal/catalog/`, e recrie o banco entre os dois (o catalog
  deixa clientes e o guard passa a recusar). Testes de pior caso semeiam até 100 emissoras e zeram ao
  terminar; se o guard reclamar, é ambiental: recrie o banco.
- Template antigo nunca re-migrado quebra datas relativas (`no partition of relation`): rode
  `SELECT ensure_month_partitions(18);` nele ou recrie-o (`docs/operations/mcp.md`).
- Avaliação: banco `radiocheck_mcp_avaliacao`, variável `AVALIACAO_DATABASE_URL` (nunca
  `DATABASE_URL`), CLI com `go run -tags avaliacao ./internal/mcp/avaliacao/cli semear|tools|chamar`.
  O `semear` recusa banco não vazio e roda numa transação.

## Convenções novas da Fatia 2 (somam às da Fatia 1)

- `toolkit_relatorios.go`: `links{tela, como_baixar?}` / `linkTela` (descarta valor vazio),
  `cortar`/`tetoPorItem` (20) com `*_total` e ressalva montada com `fmt.Sprintf`, `Paginacao.comTeto(n)`
  (teto de página próprio), lista por cursor (`listaCursor`, `total: null`), `hojeSP`/`ontemSP`/
  `mesCorrenteSP`, `lerIDs`/`lerIDsObrigatorios`/`lerDias`, `campanhaResumida`,
  `ressalvaCanceladaRelatorio`.
- **Alvo de saída < 60 KB por resposta**, provado por teste `*_PiorCasoCabe` em toda tool com lista
  interna. Tetos de página reais: mapa 25, data-hora 50, falhas_emissoras 7, falhas_campanhas (dia) 8,
  obter_falhas_campanha 50, obter_pos_venda 5 campanhas, destinatarios 50, listar_pos_vendas 50,
  auditoria 20. O `jsonschema` genérico ainda diz "máximo 100".
- **Desempate por id no SQL** (não ordene por nome em Go: a collation do Postgres difere da ordem de
  bytes).
- **Regra de exibição da tela vale no MCP:** quando a tela esconde um número (bonificação em bloco
  100% consolidado, CPM sem impactos), a tool devolve `null` + ressalva, nunca 0. Helper:
  `mostraBonificacaoDe`.
- **Texto livre do cliente/admin é higienizado antes de cortar** (`higienizador` no pós-venda): anexo
  literal → domínio, token → `[omitido]`, URL com ou sem esquema → host, `pos-venda/<43>` →
  `pos-venda/[omitido]`; corte por runa e por bytes de JSON. Auditoria: chaves de segredo →
  `[omitido]` em qualquer nível, URLs nos valores → host.
- `Leitura` ganhou os repositórios de relatório, `Workers` (supervisor; nil = `null`, nunca 0),
  `Agora` injetável e `Auditoria *auditoria.Repo`. `Leitura.Pool` não existe mais.
- `NovoServidorLocal` mora em `servidor_local.go` com `//go:build avaliacao`: fora do binário de prod.

## Armadilhas novas (valem para a Fatia 3)

- **Rotas de CSV/PDF só aceitam `Authorization: Bearer`** (download por `fetch` + blob): nunca link
  direto de arquivo, nunca URL com token (contrato §3.6).
- **`postsale.Recipient.Token` sai no JSON do handler HTTP** e é a credencial do link público sem
  expiração: no MCP só por whitelist.
- **Fuso:** `station_failures` e o modal do digest usam `time.Local` (UTC no container);
  `failures_daily` usa SP. As tools espelham a tela (paridade) e o desvio está documentado como
  follow-up. Escrita com `EscalarPara` por "dia anterior a hoje" precisa decidir o fuso explicitamente
  (SP) — não herde `time.Local`.
- **A recusa de campo obrigatório vem do SDK sem `[codigo]`** (decisão: mantém, o `required` ajuda o
  modelo). Escrita com validação própria deve recusar com `[entrada_invalida]`.
- **`material_types` não é truncada entre testes**; fixtures que criam tipo usam `ON CONFLICT (name)`.
- **`stream_health_events` não tem FK** e é truncada à parte no `DBCatalogo`.
- **Teste de "hoje"**: semeie com `SELECT now()` do banco, não `time.Now()` do Go, ou injete `L.Agora`.
- **Teste de desempate** precisa forçar hash aggregate (`enable_sort=off`) para falhar sem o
  desempate; com `GROUP BY` o plano padrão já ordena por acaso.
- Herdadas e ainda válidas: `tools/list` em ordem alfabética (`ToolsEsperadas`, travada em 44 por
  `TestToolsEsperadas_OrdemAlfabeticaE44` — atualize o número); `ferramentaLeitura` recupera panic
  (as tools de escrita precisam de um equivalente); offset > 100000 recusado; `gofmt -l` só nos
  arquivos tocados (CRLF); `window.confirm` do app é Promise; `RealIP` forjável (use `oauth.IPDe`).

## Pendências registradas

Follow-ups do MCP:
- Fuso do `station_failures` e do modal do digest em UTC (bug de tela, anterior ao MCP).
- `visao_gerencial` paga `queryStations`/`queryRecentDetections` e descarta (custo).
- `insights_campanha` sem limite de período (o handler também não tem); link de `/insights` com
  muitos ids pode passar de ~16 KB de URL.
- A tela limpa a URL antes de um 401 numa aba com sessão vencida (o filtro se perde no re-login);
  período fora da janela na aba "Por dia" volta para "Este mês" sem aviso.
- `/insights`: o seletor junta as campanhas da URL fora da página 1/200 com 1 request por id (até 50).
- Herdadas da Fatia 0/1: detecção de reuso de refresh; revogar o access antigo na rotação; podar
  `mcp_tokens`; variável própria para a origem do portal; anti-frame em `/pendentes` (Fatia 4);
  `actor_type`/`meta.mcp` da auditoria (Fatia 3); MCP Apps (fora).

Para o dono (fora do MCP — **não faça sem pedir**): limitador do `/auth/login` burlável; porta da API
só em `127.0.0.1`; imagens `minio/minio`/`minio/mc`; mount de `migrations/` em `initdb.d` (F-111);
a migration `0070_mcp_fundacao` colide em número com a `0070_users_username` da `feat/username-login`
(que já está no master: quem mergear o MCP renumera); a pasta `C:\tmp\emonitor-base-check` pode ser
apagada.

## Quando o MCP estiver 100% (fim de todas as fatias) — deploy

Roteiro completo em `docs/operations/mcp.md` (inclui "Conferência com dado real" das tools de
relatório). Resumo: merge com OK do dono (renumerar a 0070) → push do backend → `./scripts/deploy.sh`
na VM → smoke `POST /mcp` = 401 com `WWW-Authenticate` → frontend → conectar os 3 clientes e chamar
`quem_sou_eu` → paridade com dado real → hub (Fatia 5).
