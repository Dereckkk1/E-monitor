# CI/CD do backend — design

**Data:** 2026-08-25
**Autor:** Dereck + Claude (brainstorming)
**Status:** aprovado, aguardando plano de implementação

---

## 1. Problema

O backend não tem CI. Não existe `.github/workflows/`. O único portão automatizado antes
de produção é o `scripts/deploy.sh`, que roda `shadow_migration_test` e um health check —
mas **nunca executa a suíte de testes**. O `workers.Dockerfile` também não: ele faz apenas
`go build` explícito por binário, sem `go test` e sem `go vet`.

Três achados medidos neste levantamento (2026-08-25):

| Achado | Medição |
|---|---|
| Testes que dependem de Postgres e **pulam silenciosamente** sem `TEST_DATABASE_URL` | **306 de 934** funções `Test*` (33%), em 62 arquivos |
| Binário em `workers/cmd/` ausente do `workers.Dockerfile` (drift da regra 6.7) | 1 caso real hoje: `measure-quota` |
| Services buildados sem `image:` no compose (`api`) | rollback para versão anterior é **impossível** — cada build sobrescreve a mesma tag |

O `go test ./...` que se roda manualmente hoje dá verde executando dois terços da suíte.
Baseline verificado: exit 0, e `CGO_ENABLED=0 GOOS=linux go build ./...` também passa.

## 2. Objetivo

Um pipeline que:

1. Executa a suíte **inteira** (incluindo os 306 testes hoje cegos) a cada push e PR.
2. Verifica automaticamente as guardas do `CLAUDE.md` que já causaram quebra de deploy
   (regras 5, 6.1, 6.2, 6.7).
3. Automatiza o deploy de ponta a ponta, com **promoção para produção via aprovação
   humana** (padrão *continuous delivery*), rollback de imagem e trilha de auditoria.
4. Mede e publica cobertura de testes.

### Não objetivos

- Escrever testes novos para cobrir buracos. Esta entrega **mede** a cobertura e expõe os
  buracos; preenchê-los é spec separada, priorizada pelo relatório.
- Fechar a janela de ordenação frontend x backend (ver §8).
- Reescrever o `scripts/deploy.sh`. O CD **chama** o script existente.
- Rodar `.down.sql` automaticamente em qualquer circunstância.

## 3. Decisões

| Decisão | Escolha | Motivo |
|---|---|---|
| Deploy automático? | CD automatizado **com gate de aprovação** (GitHub Environment + required reviewer) | Atende a recomendação do tutor de TCC (pipeline completo automatizado) sem remover o ponto de decisão humano antes de prod, que o histórico de incidentes do projeto justifica |
| Testes de DB no CI? | Sim — Postgres service container + migrations | Sem isso 33% da suíte é verde falso, justamente na área (categorização, atribuição, handlers) dos incidentes recentes |
| Testes `//go:build integration` (áudio)? | Sim, mas **só no runner da VM** | `audio-refs/` é gitignored (18 MB, 0 arquivos versionados); runner hospedado não tem os fixtures nem ffmpeg |
| Acesso à VM | Runner **self-hosted** na própria VM | SSH externo é restrito por IP (regra 7); evita abrir firewall e evita guardar chave privada no GitHub |
| Testes vermelhos no primeiro run | **Bloquear desde o dia um** | Decisão do dono. Implica que a branch só está pronta com a suíte de DB verde — o shakedown é parte da implementação, não um "depois" |
| Teste flaky conhecido | Corrigir agora (injeção de tempo) | `internal/catalog/health_events_test.go:62` usa `time.Now().Add(-13*time.Hour)` e falha antes das ~13h UTC (regra 6.6). CI vermelho por motivo falso destrói a credibilidade da suíte |
| Cobertura | Relatório + badge agora; catraca depois | Definir mínimo antes de conhecer a linha de base engessa ou vira enfeite |
| Rollback | Sim, de imagem — **nunca** de migration | Ver §6 |

## 4. Arquitetura — `ci.yml` (runner hospedado)

Gatilho: `push` em qualquer branch e `pull_request` para `master`.
Runner: `ubuntu-latest`. Nenhum segredo de produção é exposto a este workflow.

### Job `guards` (~40 s, falha cedo)

Cada guarda é um script executável em `scripts/ci/`, **não** YAML inline, para poder ser
rodado localmente antes do push com resultado idêntico ao do CI.

| Script | Regra | O que faz |
|---|---|---|
| `check-cross-compile.sh` | 6.1 | `CGO_ENABLED=0 GOOS=linux go build ./...` — reproduz o Dockerfile |
| `check-dockerfile-drift.sh` | 6.7 | Para cada `workers/cmd/<x>`, exige `RUN ... go build -o /out/<x>` **e** `COPY --from=builder /out/<x>` no `workers.Dockerfile`. Allowlist explícita para binários com Dockerfile próprio (`fingerprint`) |
| `check-go-version.sh` | 6.2 | Diretiva `go` do `go.mod` (1.26.2) <= major.minor da imagem do Dockerfile (`golang:1.26-alpine`) |
| `go vet ./...` | — | O Dockerfile não roda vet hoje |

`check-dockerfile-drift.sh` **falha no estado atual** por causa de `measure-quota`.
Resolver esse caso (adicionar as duas linhas ao Dockerfile, ou incluí-lo na allowlist com
justificativa) é parte da entrega.

### Job `test`

- Service container `postgres:16-alpine` (mesma imagem do compose).
- `migrate/migrate:v4.17.1` aplica as 64 migrations de `migrations/`.
- Exporta `TEST_DATABASE_URL` apontando para esse Postgres descartável.
- `go test ./... -p 1 -coverprofile=coverage.out`.

**`-p 1` é obrigatório.** Rodar pacotes em paralelo contra o mesmo banco gera deadlock e
contagem errada que se parece com regressão (lição registrada em 2026-08-03). O custo é
tempo de parede estimado em 4–8 min. Paralelizar exigiria um banco por pacote — otimização
deliberadamente adiada.

**Sem `-race` nesta entrega.** O detector de corrida vale para `supervisor`, `index` e
`ringbuffer`, mas combinado com `-p 1` empurraria o run para além de 15 min. Fica como job
separado e agendado (nightly) em spec futura, não no caminho de PR.

Tags `integration` **não** rodam aqui (fixtures de áudio ausentes).

### Job `frontend`

Regra 5 — a que já quebrou o Cloudflare Pages mais de uma vez:

- `npm ci` (o comando exato que o CF Pages roda; lockfile podado falha aqui, não lá).
- Compara `grep -c emnapi frontend/package-lock.json` contra o valor em `master`; falha se
  **caiu**.
- `npm run lint` e `npm run build`.

### Job `coverage` (depende de `test`)

Resumo por pacote, relatório HTML como artefato do run, badge no README. Sem mínimo
bloqueante nesta entrega.

## 5. Arquitetura — `cd.yml` (runner self-hosted na VM)

Gatilho: `workflow_run` no evento `completed` do `ci.yml`, filtrado por
`conclusion == 'success'` **e** branch `master`; mais `workflow_dispatch` para acionamento
manual. O encadeamento é por `workflow_run`, não por `needs` (que só funciona dentro de um
mesmo workflow) nem por confiança em branch protection.

Complementarmente, `master` recebe branch protection com os jobs do `ci.yml` como required
status checks — isso impede que código vermelho **entre** em master; o `workflow_run`
impede que ele **saia** para produção. São duas defesas distintas.

**Regra fixa: `cd.yml` nunca dispara em `pull_request`.** Runner self-hosted não executa
código de PR, mesmo em repositório privado (confirmado privado em 2026-08-25). O runner
roda como usuário de serviço dedicado, não root, com acesso limitado ao que o `deploy.sh`
precisa.

### Job `audio-tests`

`go test -tags=integration ./internal/match/... ./internal/segments/...`, usando os
fixtures de `audio-refs/` presentes na VM e o ffmpeg/ffprobe locais.

Roda **antes** do gate de aprovação: se o matcher regrediu, o botão de aprovar nem chega a
aparecer.

### Job `deploy`

`environment: production` com required reviewer. O job fica **pendente** até aprovação
manual; depois executa `./scripts/deploy.sh` integralmente. O `shadow_migration_test` e o
health check que o script já tem continuam sendo a defesa primária — não são substituídos
nem duplicados.

## 6. Rollback

### Pré-requisito: tag de imagem

Hoje `api` usa `build:` sem `image:` no `docker-compose.yml`. Sem tag, cada build
sobrescreve o mesmo nome e a imagem anterior vira `<none>` pendurada — não há alvo de
rollback. A entrega adiciona ao service:

```yaml
image: radiocheck/api:${IMAGE_TAG:-latest}
```

com `deploy.sh` setando `IMAGE_TAG=<sha-curto-do-commit>` e gravando na VM o SHA que
estava rodando antes de subir.

### Mecânica

Health check falhou após o deploy → `docker compose up -d --no-deps api` com a tag
anterior. O `--no-deps` é a regra 4.1 e não é opcional.

### Migrations: limite explícito

**Rollback de imagem não desfaz migration.** Se o deploy que quebrou aplicou schema novo,
voltar o binário deixa código antigo contra banco novo.

Comportamento definido:

- O rollback da imagem **acontece sempre** — é o que restaura o serviço.
- `.down.sql` **nunca** roda automaticamente. Down automático em produção é a vizinhança
  onde o incidente 2026-05-12 nasceu.
- Se o run aplicou migration nova, o job termina em **falha ruidosa**, declarando que o
  schema está à frente do binário e exige verificação humana.

Na prática as migrations do projeto são majoritariamente aditivas, o que torna esse estado
tolerável — mas o pipeline não pode presumir isso em silêncio.

## 7. Testabilidade do próprio pipeline

- Guardas são scripts com testes próprios, executáveis no Windows do dev antes do push.
- `ci.yml` é validado em branch descartável antes de virar required check.
- `cd.yml` só é exercitado via `workflow_dispatch` até o comportamento estar confirmado.

## 8. Riscos e limites conhecidos

| Risco | Tratamento |
|---|---|
| Front novo contra API velha: CF Pages publica o frontend a cada push em master, backend espera aprovação | O gate encurta a janela, não a fecha. Fora de escopo; spec separada |
| Testes de áudio consomem CPU da VM que monitora 200 emissoras ao vivo | Rodam antes do gate, em horário de aprovação escolhido. Se incomodar, viram `workflow_dispatch` separado |
| Suíte de DB pode revelar falhas nunca vistas | É o objetivo. Bloqueia desde o dia um por decisão do dono; corrigir faz parte da implementação |
| Runner self-hosted executando código não confiável | `cd.yml` sem gatilho de `pull_request`; repo privado; usuário de serviço dedicado |

## 9. Critérios de aceitação

1. `ci.yml` verde em `master`, com os 306 testes de DB **executando**, não pulando. Como o
   `go test` só imprime `--- SKIP` com `-v`, o job roda com `-v` e falha explicitamente se
   encontrar qualquer skip com a mensagem `TEST_DATABASE_URL not set` — um skip desses
   significa que o service container não subiu e o verde seria falso.
2. `scripts/ci/check-dockerfile-drift.sh` passa, com `measure-quota` resolvido.
3. `scripts/ci/*` produzem o mesmo resultado rodados localmente e no CI.
4. Teste flaky de `internal/catalog/health_events_test.go` passa em qualquer hora do dia.
5. Lockfile podado é rejeitado pelo job `frontend` (verificável com uma poda proposital em
   branch descartável).
6. `cd.yml` fica pendente aguardando aprovação e não avança sozinho.
7. Deploy via `cd.yml` produz imagem taggeada por SHA e registra o SHA anterior.
8. Rollback restaura a imagem anterior; quando houve migration no run, falha com a mensagem
   de schema à frente.
9. Relatório de cobertura publicado como artefato, com badge no README.
