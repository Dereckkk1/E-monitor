# Prompt de Execução — Radiocheck Fase 2 (Hardening e Coexistência)

Este arquivo contém o prompt para iniciar uma sessão nova do Claude Code que vai planejar e executar a Fase 2 do Radiocheck via **subagentes**, com double-review por task (spec + qualidade).

**Como usar:** abra uma sessão nova do Claude Code no diretório `c:\Users\marke\Desktop\Programas\Radiocheck` e cole o bloco abaixo como sua primeira mensagem.

---

## O que a Fase 2 entrega

Scope: 30 emissoras, 50 comerciais, semanas 7–18 do plano.

- Stream workers escalados para 30 com restart preventivo escalonado (horários distintos por emissora)
- Calibração adaptativa de threshold por emissora (§9.4)
- Camada de verificação neural com CLAP exportado e servido (§10)
- Evidence Service completo com upload para Cloudflare R2 (§11)
- API REST v1 completa com webhooks funcionais (§13.1)
- Frontend administrativo mínimo (gestão de emissoras, comerciais, campanhas, listagem de detecções)
- Dashboards Grafana completos (§15.4)
- Alertas em produção com runbooks documentados (§15.5/15.6)
- Backup de Postgres configurado e testado — restore de teste mensal (§14.4)
- Política de retenção de evidências com tiering automático após 30 dias (§11.4)

**Critério de saída:** ≥95% concordância com fornecedor por 3 semanas consecutivas, uptime ≥99% mensal, latência p95 de confirmação <10s.

---

## Pré-requisitos antes de iniciar a sessão

1. **Fase 1 concluída e mergeada:** confirme que o branch `feature/poc-fase1` foi mergeado ou está como base do trabalho.

2. **PoC validado em produção:** você deve ter em mãos os números reais de precision/recall do PoC antes de avançar — o Go/No-Go da Fase 2 (§18.6) exige precision ≥95% em 5 emissoras.

3. **Credenciais de infra prontas** para entregar quando o agente pedir:
   - Cloudflare R2: Account ID, Access Key ID, Secret Access Key, nome do bucket
   - SMTP ou serviço de notificação para alertas (ex: Alertmanager → email/Slack)
   - Acesso ao servidor de produção (SSH ou IP) onde os 30 workers vão rodar

4. **Grafana e Prometheus** já deployados ou planejados — o agente vai precisar saber os endpoints.

---

## Prompt para colar na nova sessão

```
Você vai planejar e executar a Fase 2 do Radiocheck (Hardening e Coexistência) — 30 emissoras, 50 comerciais, semanas 7–18. A Fase 1 (PoC) está concluída. Você é o orquestrador, não o implementador.

Esta sessão tem DUAS fases distintas: primeiro PLANEJAMENTO (escrever spec e plano), depois EXECUÇÃO (subagents implementam o plano). Não pule para a execução antes de eu aprovar o plano.

═══════════════════════════════════════════════════════════════
ARQUIVOS DE CONTEXTO (leia nesta ordem, antes de qualquer ação)
═══════════════════════════════════════════════════════════════

1. CLAUDE.md
   → regras críticas, índice do plano arquitetural, escopo proibido

2. plano_implementacao.md (seções específicas via índice do CLAUDE.md):
   § 18.2 (linha 1873) — escopo e entregas da Fase 2
   § 9.4  (linha 871)  — threshold adaptativo por emissora
   § 10   (linha 981)  — verificação neural CLAP
   § 11   (linha 1033) — Evidence Service e Cloudflare R2
   § 13.1 (linha 1373) — API REST v1 e webhooks
   § 14.4 (linha 1581) — Backup e DR
   § 15   (linha 1622) — Observabilidade: Prometheus, Grafana, alertas
   § 16   (linha 1736) — Segurança
   § 17   (linha 1797) — Migração e Coexistência com fornecedor atual

3. Código já implementado no branch feature/poc-fase1 (ou main se já mergeado)
   → estrutura de pastas, interfaces Go existentes, schema do banco

NÃO leia o plano inteiro — use o índice do CLAUDE.md para puxar seções pelo número de linha.

═══════════════════════════════════════════════════════════════
SKILLS OBRIGATÓRIAS (na ordem)
═══════════════════════════════════════════════════════════════

ANTES de tocar em código, invoque estas skills (use o Skill tool):

1. superpowers:using-git-worktrees
   → Crie worktree em branch nova: feature/fase2-hardening

2. superpowers:brainstorming
   → Use ANTES de escrever o spec. Explore: quais interfaces da Fase 1 precisam evoluir?
     Quais são novos serviços vs extensões? O CLAP roda in-process ou como sidecar?
     Os 30 workers são processos novos ou o Supervisor da Fase 1 já suporta escala?

3. superpowers:writing-plans
   → Após o brainstorm aprovado, escreva o plano de implementação detalhado.
     Salve em: docs/superpowers/plans/<data>-radiocheck-fase2-implementation.md
     O spec funcional deve ir em: docs/superpowers/specs/<data>-radiocheck-fase2-design.md
     Siga o mesmo padrão do plano da Fase 1 (tasks numeradas, grupos, arquivos, comandos de verificação).

4. superpowers:subagent-driven-development
   → Esta é a skill PRINCIPAL da execução. Só invoque APÓS eu aprovar o plano.
     Workflow por task: Implementer → Spec reviewer → Code quality reviewer → fix loop → commit

═══════════════════════════════════════════════════════════════
CONTEXTO ADICIONAL PARA O PLANEJAMENTO
═══════════════════════════════════════════════════════════════

Ao escrever o spec e o plano, considere:

- Base: o código da Fase 1 já existe — não redesenhe o que funciona, estenda.
- O threshold adaptativo (§9.4) precisa de dados reais das 30 emissoras para calibrar — o plano deve incluir uma task de coleta e calibração inicial com dados reais.
- O CLAP (§10) precisa de um modelo exportado (ONNX ou TorchScript). Decida no brainstorm se ele roda no mesmo container do Match Engine ou num sidecar separado, e por quê.
- A migração paralela com o fornecedor (§17) precisa de um plano de comparação: como vamos medir concordância ≥95%? Que script faz esse comparison?
- Workers para 30 emissoras: o Supervisor da Fase 1 já tem suporte a múltiplos workers? Se não, isso é uma task de refactor antes de escalar.
- Grafana dashboards (§15.4): liste quais painéis são obrigatórios para o critério de saída da Fase 2. Dashboards sem alertas não contam.
- Backup Postgres: a task precisa incluir um teste de restore real (não só configurar — executar restore e verificar integridade).

═══════════════════════════════════════════════════════════════
COMO ESTRUTURAR O PLANO DE FASE 2
═══════════════════════════════════════════════════════════════

Organize as tasks em grupos temáticos, similar à Fase 1. Sugestão de grupos (ajuste conforme o brainstorm):

  Grupo A (Scale Foundation):    Refactor Supervisor para 30 workers, restart escalonado
  Grupo B (Adaptive Threshold):  Calibração por emissora com dados reais
  Grupo C (Neural Layer):        Integração CLAP (export modelo, serviço, integração no Match Engine)
  Grupo D (R2 Storage):          Evidence Service com upload Cloudflare R2, tiering
  Grupo E (API v1):              Endpoints completos + webhooks + autenticação de clientes
  Grupo F (Observability):       Dashboards Grafana, alertas Prometheus, runbooks
  Grupo G (Reliability):         Backup Postgres, restore testado, chaos tests
  Grupo H (Migration):           Script de comparação com fornecedor, coleta de concordância
  Grupo I (Frontend):            Extensões do admin para novas funcionalidades da Fase 2

Para cada task no plano inclua:
- Objetivo claro em 1–2 linhas
- Arquivos a criar/modificar (caminhos exatos)
- Código esqueleto ou pseudocódigo se necessário para clareza
- Comandos de verificação (testes, curl, docker exec, etc.)
- Mensagem de commit sugerida

Crie um TodoWrite com todas as tasks logo após o plano ser aprovado.

═══════════════════════════════════════════════════════════════
CONTEXTO ADICIONAL QUE OS SUBAGENTS PRECISAM
═══════════════════════════════════════════════════════════════

Toda vez que despachar um subagent na fase de execução, inclua:

- Texto integral da task (extraído do plano — NUNCA peça pro subagent abrir o arquivo de plano)
- Caminho do spec: docs/superpowers/specs/<data>-radiocheck-fase2-design.md
- Caminhos dos arquivos relevantes já existentes da Fase 1 que o subagent precisa ler
- Lembrete: NÃO desviar do plano sem justificativa documentada (regra do CLAUDE.md)
- Lembrete: TDD onde a task pedir; `git commit` ao final com a mensagem do plano
- Ambiente: Windows + Docker Desktop, shell bash, branch feature/fase2-hardening no worktree
- Credenciais de infra que você tiver coletado (R2, SMTP, etc.)

═══════════════════════════════════════════════════════════════
QUANDO PARAR E PERGUNTAR
═══════════════════════════════════════════════════════════════

Pare e me consulte se:
- Implementer reportar BLOCKED (skill explica como tratar)
- Spec reviewer ou code reviewer encontrar algo não fixável por retry (ambiguidade real)
- Subagent sugerir mudar o plano (precisa do meu OK — regra do CLAUDE.md)
- Após 3 iterações de review do mesmo aspecto sem convergência
- Task envolver credenciais reais (R2, SMTP, SSH de produção)
- O brainstorm do CLAP abrir dúvida arquitetural que muda tasks subsequentes
- Script de comparação com fornecedor mostrar concordância <90% antes de subir para 30 emissoras

NÃO PARE para:
- Reportar progresso normal (uma linha curta basta)
- Confirmar comandos explicitamente listados no plano

═══════════════════════════════════════════════════════════════
REPORTING DURANTE A EXECUÇÃO
═══════════════════════════════════════════════════════════════

Após cada task concluída (ambas reviews passadas + commit):
  ✅ Task N (nome): impl=ok, spec_review=ok, quality_review=ok, commit=<sha curto>

Se travar:
  ❌ Task N: <descrição do bloqueio> — aguardando

A cada 5 tasks, reporte um smoke test de saúde:
  - Postgres e NATS respondem?
  - Build Go passa? Testes passam?
  - Quantos workers ativos? Algum em crash-loop?
  - Concordância atual com fornecedor (se script de comparação já existir): X%

Marco especial — após Grupo H (comparação com fornecedor) estar completo:
  📊 CONCORDÂNCIA ATUAL: X% em N emissoras — critério de saída é ≥95% em 30 emissoras

═══════════════════════════════════════════════════════════════
PRIMEIRA AÇÃO (faça AGORA, nesta ordem)
═══════════════════════════════════════════════════════════════

1. Leia os arquivos de contexto (seção acima)
2. Confirme com `git log --oneline -5` e `git branch` o estado atual do repo
3. Me responda com:
   - Resumo de 5 linhas do que a Fase 2 entrega e em que difere da Fase 1
   - Lista de dúvidas arquiteturais que o brainstorm precisa resolver antes do plano
   - Lista do que precisa de mim antes de começar (credenciais, decisões, confirmações)
4. AGUARDE meu "pode começar" antes de invocar qualquer skill ou despachar qualquer subagent
5. Quando eu autorizar, invoque na ordem:
   - superpowers:using-git-worktrees (cria worktree feature/fase2-hardening)
   - superpowers:brainstorming (resolve dúvidas arquiteturais)
   - superpowers:writing-plans (escreve spec + plano detalhado)
6. Apresente o plano para minha aprovação ANTES de chamar subagent-driven-development
7. Após aprovação: invoque superpowers:subagent-driven-development e inicie pela Task 1
```

---

## Notas para você (Marke)

**Diferenças em relação ao EXECUTAR.md da Fase 1:**
- A Fase 1 já tinha spec e plano prontos — aqui o agente escreve esses artefatos na mesma sessão, com seu OK antes de executar.
- O brainstorm é obrigatório porque existem decisões arquiteturais abertas na Fase 2 (CLAP in-process vs sidecar, escala de Supervisor, script de comparação com fornecedor).
- O marco de comparação com fornecedor é novo — é o critério de saída da Fase 2 e precisa de atenção especial.

**Antes de iniciar:**
- Tenha em mãos as credenciais de R2, servidor de produção e SMTP.
- O Go/No-Go da Fase 2 (§18.6) exige precision ≥95% no PoC — se ainda não validou isso, faça antes de abrir esta sessão.

**Quanto vai custar/demorar:**
- Planejamento (brainstorm + spec + plano): ~2h wall clock, 1 sessão
- Execução: depende do número de tasks (estimativa: 20–35 tasks × 3 subagents = 60–105 invocações)
- Tempo de execução estimado: 10–20 horas (mais complexo que Fase 1 por CLAP + R2 + observabilidade)
