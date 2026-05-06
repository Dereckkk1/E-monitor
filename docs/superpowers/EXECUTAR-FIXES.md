# Prompt de Execução — Fixes PoC Fase 1 (Conclusão)

Este arquivo contém o prompt para iniciar uma sessão nova do Claude Code que vai finalizar o PoC aplicando os fixes pendentes e fazendo commit do que já está aplicado.

**Como usar:** abra uma sessão nova do Claude Code **no diretório do worktree**:
```
c:\Users\marke\Desktop\Programas\Radiocheck\.worktrees\poc-fase1
```
E cole o bloco abaixo como sua primeira mensagem.

**Pré-requisitos:** nenhum — repo já inicializado, worktree `poc-fase1` já existe.

---

## Prompt para colar na nova sessão

```
Você vai concluir o PoC do Radiocheck aplicando fixes específicos e commitando o que já está feito. O sistema está majoritariamente implementado — seu trabalho é finalizar as partes pendentes até o PoC estar 100% pronto.

═══════════════════════════════════════════════════════════════
DIRETÓRIO DE TRABALHO
═══════════════════════════════════════════════════════════════

Você está no worktree isolado do PoC:
  c:\Users\marke\Desktop\Programas\Radiocheck\.worktrees\poc-fase1

NÃO trabalhe no diretório principal do projeto.

═══════════════════════════════════════════════════════════════
ARQUIVOS DE CONTEXTO (leia nesta ordem)
═══════════════════════════════════════════════════════════════

1. CLAUDE.md
   → regras críticas, índice do plano, escopo proibido

2. docs/status-e-roadmap.md
   → FONTE ÚNICA DE VERDADE do que está feito e do que falta.
     Seção 1 = estado atual de cada componente
     Seção 2 = fixes pendentes com spec exata de cada mudança (C2–H)
     "Critério de conclusão do PoC" = checklist de verificação final

3. docs/superpowers/specs/2026-05-05-radiocheck-poc-design.md
   → spec funcional do PoC (use para verificar se cada fix bate com o spec)

NÃO leia o plano_implementacao.md inteiro — use o índice do CLAUDE.md
para puxar seções específicas somente quando necessário.

═══════════════════════════════════════════════════════════════
AS 10 TASKS (em ordem obrigatória)
═══════════════════════════════════════════════════════════════

--- BLOCO 1: COMMIT DOS FIXES JÁ APLICADOS ---

Task 1 — Commit Fix A (router.go)
  Arquivo: workers/internal/api/router.go
  O quê: PUT /{id}/start e PUT /{id}/pause já foram adicionados nesta sessão anterior.
  Ação: git add + git commit com mensagem "fix: add PUT /start and /pause routes to campaign router"

Task 2 — Commit Fix B1 (peaks.go)
  Arquivo: workers/pkg/audio/peaks.go
  O quê: neighborFrames e neighborBins corrigidos de 10/5 para 15/15.
  Ação: git add + git commit com mensagem "fix: correct peak picker neighborhood to 15x15"

Task 3 — Commit Fix B2 (hashes.go)
  Arquivo: workers/pkg/audio/hashes.go
  O quê: FanOut=5, TargetZoneTMax=16, TargetZoneTMin=1, TargetZoneF=50; filtros de delta mínimo e distância de frequência adicionados.
  Ação: git add + git commit com mensagem "fix: align hash constants with plan (FanOut=5, TMax=16, F=50)"

Task 4 — Commit Fix C1 (store.go)
  Arquivo: workers/internal/index/store.go
  O quê: struct Entry agora tem VariantID uint8 e RateID uint8.
  Ação: git add + git commit com mensagem "fix: add VariantID/RateID fields to index Entry struct"

--- BLOCO 2: FIXES PENDENTES ---

Task 5 — Fix C2: Loader SQL (variant/rate + filtro campanha ativa)
  Arquivos: workers/internal/index/loader.go
  Spec (do status-e-roadmap.md §Fix C2):
    - LoadAll(): adicionar fh.variant_id, fh.rate_id ao SELECT e JOIN com campaigns WHERE ca.status = 'active'
    - Subscribe(): mesma adição nas duas queries internas
  Verificação: go build ./workers/... deve passar; go test ./workers/internal/index/... deve passar
  Commit: "fix: loader SQL now includes variant_id/rate_id and filters active campaigns"

Task 6 — Fix D: Match Engine (histograma variant/rate + DeltaBin + MinWindowCoverage)
  Arquivos: workers/internal/match/engine.go
  Spec (do status-e-roadmap.md §Fix D):
    - MatchResult ganha VariantID uint8 e RateID uint8
    - Histograma keyed por (commercialID, variantID, rateID, delta/DeltaBinSize)
    - Constante DeltaBinSize = 2 (~256ms de tolerância por bin)
    - Filtro MinWindowCoverage = 0.4 antes de emitir resultado
  Verificação: go build ./workers/... deve passar; go test ./workers/internal/match/... deve passar
  Commit: "fix: match engine histograms keyed by variant/rate with DeltaBin and MinWindowCoverage"

Task 7 — Fix E: State Machine + Worker (StateCooldown)
  Arquivos: workers/internal/match/statemachine.go, workers/internal/ingestor/worker.go, workers/internal/match/statemachine_test.go
  Spec (do status-e-roadmap.md §Fix E):
    - Adicionar StateCooldown — evita dupla detecção do mesmo comercial
    - Cooldown = duração do comercial + 5s
    - worker.go calcula e passa cooldownDuration ao criar a state machine
    - Testes atualizados para verificar transição Detected → Cooldown → Idle
  Verificação: go test ./workers/internal/match/... deve incluir o teste de transição Cooldown
  Commit: "fix: add StateCooldown state to prevent double-detection"

Task 8 — Fix F: Supervisor RestoreActive + main.go
  Arquivos: workers/internal/supervisor/supervisor.go, workers/cmd/api/main.go
  Spec (do status-e-roadmap.md §Fix F):
    - Método RestoreActive(ctx) busca campanhas com status = 'active' e relança workers
    - main.go chama sup.RestoreActive(ctx) após loader.LoadAll()
  Verificação: go build ./workers/cmd/api/... deve passar
  Commit: "fix: supervisor RestoreActive restarts workers for active campaigns on boot"

Task 9 — Fix G: Frontend upload de comercial
  Arquivos: frontend/src/api/hooks.js, frontend/src/pages/CampaignsPage.jsx
  Spec (do status-e-roadmap.md §Fix G):
    - Hook useUploadCommercial — POST multipart para /commercials
    - Formulário inline em CampaignsPage com campos: título, cut label, arquivo de áudio
    - ATENÇÃO: campo do arquivo DEVE se chamar "audio" (handler Go espera r.FormFile("audio"))
  Verificação: npm run build deve passar sem erros
  Commit: "fix: add commercial upload form to CampaignsPage with useUploadCommercial hook"

Task 10 — Fix H: Frontend player de evidência inline
  Arquivo: frontend/src/pages/DetectionsPage.jsx
  Spec (do status-e-roadmap.md §Fix H):
    - Substituir window.open(...) por <audio controls src="..." /> inline
  Verificação: npm run build deve passar
  Commit: "fix: replace window.open with inline audio player in DetectionsPage"

--- BLOCO 3: VERIFICAÇÃO FINAL ---

Task 11 — Suite de verificação completa
  Execute em sequência:
  1. cd workers && go build ./... → deve passar sem erros
  2. cd workers && go test ./... → deve passar sem falhas
  3. cd frontend && npm run build → deve passar sem erros
  4. docker compose -f infra/docker/docker-compose.yml up -d → sistema deve subir
  5. curl http://localhost:8080/health → deve retornar {"status":"ok"}
  6. docker compose -f infra/docker/docker-compose.yml down
  
  Se qualquer passo falhar: pare, relate o erro exato, aguarde instrução.
  Se tudo passar: atualize docs/status-e-roadmap.md — mude todos os ⚡ e 🔧 para ✅ nos itens que você corrigiu.
  Commit final: "chore: mark all PoC fixes as complete in status-e-roadmap"

═══════════════════════════════════════════════════════════════
SKILLS (use somente se precisar)
═══════════════════════════════════════════════════════════════

Para os fixes do Bloco 2 (Tasks 5–10), se algum fix for ambíguo ou falhar na
verificação após 2 tentativas, invoque:
  superpowers:systematic-debugging

Se quiser revisão de qualidade após aplicar todos os fixes:
  superpowers:requesting-code-review

NÃO invoque superpowers:using-git-worktrees — o worktree já existe.
NÃO invoque superpowers:subagent-driven-development — as tasks são cirúrgicas demais.

═══════════════════════════════════════════════════════════════
QUANDO PARAR E PERGUNTAR
═══════════════════════════════════════════════════════════════

Pare e consulte o usuário se:
- go test falhar com erro que não é do fix sendo aplicado (regressão inesperada)
- A spec de um fix for ambígua e houver mais de uma interpretação válida
- docker compose up crashar em serviço que não está sendo modificado
- O build do frontend falhar por dependência não instalada (npm install necessário?)

NÃO PARE para:
- Avisar progresso normal de cada task
- Pedir confirmação de commits (todos os commits acima estão pré-aprovados)

═══════════════════════════════════════════════════════════════
REPORTING
═══════════════════════════════════════════════════════════════

Após cada task:
  ✅ Task N (nome): <uma linha do que foi feito>, commit=<sha curto>

Se travar:
  ❌ Task N: <erro exato> — aguardando

═══════════════════════════════════════════════════════════════
PRIMEIRA AÇÃO
═══════════════════════════════════════════════════════════════

1. Leia os 3 arquivos de contexto (CLAUDE.md, docs/status-e-roadmap.md, spec)
2. Rode git status para confirmar quais arquivos estão modificados (os 4 fixes ⚡)
3. Me responda com:
   - Confirmação dos 4 arquivos com mudanças não commitadas
   - Confirmação dos 6 fixes pendentes (C2, D, E, F, G, H) e seus arquivos alvo
   - Qualquer dúvida antes de começar
4. AGUARDE meu "pode começar" antes de executar qualquer ação
5. Execute as 11 tasks na ordem, sem pular nenhuma
```

---

## Notas para você (Marke)

**O que este prompt faz de diferente do EXECUTAR.md original:**
- Não faz git init nem worktree (já feitos)
- Scope é 10 tasks cirúrgicas + 1 de verificação, não 27 tasks do zero
- Sem subagent-driven-development — os fixes são menores e não justificam o overhead
- O agente commita cada fix individualmente (histórico limpo)
- Task 11 valida tudo e fecha o loop no status-e-roadmap.md

**O que você precisa fazer depois do PoC:**
Ver `docs/status-e-roadmap.md` seção 6 para os critérios Go/No-Go da Fase 2
(precision ≥ 95% em 5 emissoras validadas manualmente).
