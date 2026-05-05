# Prompt de Execução — Radiocheck PoC

Este arquivo contém o prompt para iniciar uma sessão nova do Claude Code que vai executar o plano de implementação completo via **subagentes**, com double-review por task (spec + qualidade).

**Como usar:** abra uma sessão nova do Claude Code no diretório `c:\Users\marke\Desktop\Programas\Radiocheck` e cole o bloco abaixo como sua primeira mensagem.

---

## Pré-requisitos antes de iniciar a sessão

A skill `subagent-driven-development` exige um repo git e idealmente um worktree isolado. O projeto ainda não é git. Faça isto **uma única vez** antes de abrir a sessão nova:

```bash
cd c:/Users/marke/Desktop/Programas/Radiocheck
git init
git add CLAUDE.md plano_implementacao.md docs/
git commit -m "chore: bootstrap radiocheck project with spec, plan and docs"
```

Tenha à mão para entregar quando o agente pedir:
- 2-3 URLs de stream de rádio reais (links Icecast/MP3 de FMs brasileiras quaisquer) para testar workers
- 1 arquivo de áudio MP3 ou WAV (15-60s) para testar fingerprint

---

## Prompt para colar na nova sessão

```
Você vai executar o plano de implementação do Radiocheck (Fase 1 PoC) — sistema de monitoramento de veiculação de comerciais em rádio. O projeto está zerado (só existe documentação). Você é o orquestrador, não o implementador.

═══════════════════════════════════════════════════════════════
ARQUIVOS DE CONTEXTO (leia nesta ordem, antes de qualquer ação)
═══════════════════════════════════════════════════════════════

1. CLAUDE.md
   → regras críticas, índice do plano arquitetural, escopo proibido

2. docs/superpowers/specs/2026-05-05-radiocheck-poc-design.md
   → spec funcional da Fase 1 (4 funcionalidades: cadastrar emissora, gerenciar campanha com materiais, ver status, ver veiculações com evidência)

3. docs/superpowers/plans/2026-05-05-radiocheck-poc-implementation.md
   → plano detalhado com 27 tasks divididas em 9 grupos (A até I), cada task com arquivos, código e comandos de verificação

4. plano_implementacao.md
   → blueprint arquitetural completo (não leia inteiro — use o índice em CLAUDE.md para puxar seções específicas quando necessário)

═══════════════════════════════════════════════════════════════
SKILLS OBRIGATÓRIAS (na ordem)
═══════════════════════════════════════════════════════════════

ANTES de tocar em código, invoque estas skills (use o Skill tool):

1. superpowers:using-git-worktrees
   → Configura workspace isolado. O repo já foi git init. Crie um worktree em branch nova (ex: feature/poc-fase1) antes de começar.

2. superpowers:subagent-driven-development
   → Esta é a skill PRINCIPAL. Ela define o workflow completo:
     - Você lê o plano UMA vez e extrai o texto integral de cada task (não faz subagent ler o plano)
     - Por task, dispara 3 subagents em sequência:
        a) Implementer (./implementer-prompt.md) — escreve código, testa, commita
        b) Spec reviewer (./spec-reviewer-prompt.md) — confere se bate com o spec
        c) Code quality reviewer (./code-quality-reviewer-prompt.md) — confere qualidade
     - Loop de fix-and-review até ambas as reviews passarem
     - Só então marca a task como completa no TodoWrite
     - No final de tudo, dispara um final reviewer geral

═══════════════════════════════════════════════════════════════
COMO EXTRAIR AS 27 TASKS DO PLANO
═══════════════════════════════════════════════════════════════

Após ler o plano (item 3 acima), construa em memória uma lista das 27 tasks:

  Grupo A (Foundation):       Tasks 1-4
  Grupo B (Catalog API):      Tasks 5-10
  Grupo C (Python Fingerprint): Tasks 11-14
  Grupo D (Audio Engine):     Tasks 15-19
  Grupo E (Match Engine):     Tasks 20-21
  Grupo F (Stream Ingestor):  Tasks 22-23
  Grupo G (Evidence Service): Task 24
  Grupo H (Supervisor):       Task 25
  Grupo I (React Frontend):   Tasks 26-27

Para cada task, extraia o texto literal do plano (header "### Task N:" até o próximo "### Task" ou "## Group"). Esse texto é o que você passa inline ao implementer subagent — NUNCA peça pro subagent abrir o arquivo de plano.

Crie um TodoWrite com as 27 tasks logo no início.

═══════════════════════════════════════════════════════════════
CONTEXTO ADICIONAL QUE OS SUBAGENTS PRECISAM
═══════════════════════════════════════════════════════════════

Toda vez que despachar um subagent, inclua:

- O texto integral da task (extraído do plano)
- Caminho do spec para o spec-reviewer: docs/superpowers/specs/2026-05-05-radiocheck-poc-design.md
- Caminhos dos arquivos relevantes a ler (ex: para Task 5 o subagent precisa do go.mod já criado em Task 3)
- Lembrete: NÃO desviar do plano sem justificativa documentada (regra do CLAUDE.md)
- Lembrete: usar TDD onde a task pedir; usar `git commit` ao final com a mensagem do plano
- Ambiente: Windows + Docker Desktop, shell bash disponível, repo git inicializado

═══════════════════════════════════════════════════════════════
QUANDO PARAR E PERGUNTAR
═══════════════════════════════════════════════════════════════

Pare e me consulte se:
- Implementer reportar BLOCKED (skill explica como tratar)
- Spec reviewer ou code reviewer encontrar algo que NÃO é fixável apenas com retry (ex: ambiguidade real do spec)
- Algum subagent sugerir mudar o plano (regra do CLAUDE.md: precisa do meu OK)
- Após 3 iterações de review do mesmo aspecto sem convergência
- Task envolver credenciais reais (R2 production, etc.)
- Você precisar de input meu: URL de stream, arquivo de áudio, etc.

NÃO PARE para:
- Avisar progresso normal (só reporte uma linha curta)
- Pedir confirmação de comandos que estão explicitamente no plano

═══════════════════════════════════════════════════════════════
REPORTING DURANTE A EXECUÇÃO
═══════════════════════════════════════════════════════════════

Após cada task concluída (ambas reviews passadas + commit feito), reporte uma linha:
  ✅ Task N (nome): impl=ok, spec_review=ok, quality_review=ok, commit=<sha curto>

Se algo travar:
  ❌ Task N: <descrição específica do bloqueio> — aguardando

A cada 5 tasks, reporte um smoke test de saúde:
  - Postgres responde? curl /health?
  - Build de Go ainda passa? Tests ainda passam?
  - Frontend (a partir do grupo I) compila?

═══════════════════════════════════════════════════════════════
PRIMEIRA AÇÃO (faça AGORA, nesta ordem)
═══════════════════════════════════════════════════════════════

1. Leia os 4 arquivos da seção "ARQUIVOS DE CONTEXTO"
2. Confirme com `git status` que o repo está inicializado e limpo
3. Me responda com:
   - Resumo de 5 linhas do que vamos construir
   - Confirmação dos 9 grupos de tasks
   - Lista do que precisa de mim antes de começar (URLs de stream? sample de áudio? confirmação de branch/worktree?)
4. AGUARDE meu "pode começar" antes de invocar qualquer skill ou despachar qualquer subagent
5. Quando eu autorizar, invoque na ordem:
   - superpowers:using-git-worktrees (cria worktree feature/poc-fase1)
   - superpowers:subagent-driven-development (orquestra o resto)
6. Inicie pela Task 1 seguindo rigorosamente o workflow da skill subagent-driven-development
```

---

## Notas para você (Marke)

**O que muda em relação ao prompt anterior:**
- Agora delega o workflow inteiro pra skill `subagent-driven-development` em vez de inventar protocolo manual
- A skill faz **3 subagents por task** (implementer + spec reviewer + code reviewer) com loop de fix-and-review automático
- Inclui passo obrigatório de `git init` + worktree antes de começar
- Você só precisa responder mensagens curtas durante a execução

**Quanto vai custar/demorar:**
- 27 tasks × 3 subagents = 81 invocações de subagent (mais re-reviews em loops)
- Tempo wall clock estimado: 8 a 16 horas (depende de quantas iterações de review cada task precisa)
- Custo de tokens vai ser maior que execução inline, mas a qualidade compensa — bugs detectados cedo são muito mais baratos

**Durante a execução:**
- Você vai receber mensagens curtas tipo `✅ Task N` por task. Pode só responder "ok" e seguir.
- Se vier `❌`, leia, decida ajuste e responda.
- A qualquer momento pode pedir "para tudo, mostra o que foi feito" pra revisar.
