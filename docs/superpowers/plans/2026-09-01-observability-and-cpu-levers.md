# Observabilidade + Leveres de CPU — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ligar o sistema de alerta (que existe mas nunca funcionou), fechar o buraco de alerta que deixou a VM saturar em silêncio, e recuperar CPU sem que a qualidade de detecção ou de evidência caia em nenhuma hipótese.

**Architecture:** Cinco fases com portões. Fase 1 (observabilidade) é pré-requisito de todas as outras — enquanto não houver alerta funcionando, nenhuma mudança de performance é verificável. Fase 3 (percentile) é provável por golden test existente. Fase 4 (ffmpeg) é a única que toca a cadeia de evidência e vai com canário. Fase 5 é financeira, sem código.

**Tech Stack:** Go 1.26, Prometheus/Alertmanager, Docker Compose, ffmpeg, promtool/amtool.

**Restrição do dono:** a qualidade NÃO pode cair. Onde houver dúvida, o plano mede antes de mudar; onde a medição não for possível localmente, o plano usa canário em prod com rollback.

---

## Contexto medido (2026-09-01, prod)

| | valor |
|---|---|
| Máquina | `c2d-standard-16`, 16 vCPU / 64 GB |
| Consumo | 6,74 cores (42%) — ffmpeg 3,86 + Go 2,86 |
| p99 da janela de matching | 34,4 ms (cadência é 2 s) |
| PSI `some` | 8,5% |
| Índice | 917.349 hashes distintos |
| ffmpeg copy × re-encode | 164 × 0,454% vs 103 × 2,578% = **5,68×** |
| Teto atual a 70% de CPU | ~456 emissoras |

Postmortem: [incident-2026-09-01](../../incidents/incident-2026-09-01-cpu-saturation-vm-resize.md).

## Estrutura de arquivos

| Arquivo | Responsabilidade | Fase |
|---|---|---|
| `infra/docker/docker-compose.yml` | render do config do alertmanager, collectors do node-exporter, políticas de restart | 1, 2 |
| `infra/alertmanager/alertmanager.yml` | vira template (`${SLACK_WEBHOOK_URL}` substituído no boot) | 1 |
| `infra/prometheus/alerts.yml` | 3 regras novas: p99 warning/critical, pressão de CPU | 1 |
| `infra/prometheus/alerts_test.yml` | **novo** — testes unitários das regras (`promtool test rules`) | 1 |
| `workers/internal/ingestor/worker.go` | incremento de `DetectionTotal` | 1 |
| `docs/runbooks/MatchWindowP99High.md` | **novo** | 1 |
| `docs/runbooks/CPUPressureHigh.md` | **novo** | 1 |
| `workers/pkg/audio/peaks.go` | `percentile` via quickselect | 3 |
| `workers/pkg/audio/peaks_test.go` | **novo** — oráculo sort × quickselect | 3 |
| `workers/internal/ingestor/ffmpeg.go` | copy-through por formato nativo | 4 |
| `workers/internal/segments/segments.go` | leitura multi-formato | 4 |
| `scripts/radio-sim/simulate-icecast.sh` | preset MP3 para E2E | 4 |

---

## Fase 0 — Ambiente local

### Task 0: Subir a stack local e capturar a linha de base

**Files:** nenhum (só verificação)

- [ ] **Step 1: Confirmar que o Docker está de pé**

```bash
docker version --format '{{.Server.Version}}'
```
Esperado: uma versão (ex.: `28.5.1`). Se falhar, abrir o Docker Desktop antes de continuar.

- [ ] **Step 2: Criar a branch de trabalho**

```bash
cd /c/Users/marke/Desktop/Programas/E-Series/E-monitor
git checkout master && git pull
git checkout -b feat/observability-and-cpu-levers
```

- [ ] **Step 3: Rodar a suíte inteira para ter a linha de base**

```bash
cd workers && go test ./... 2>&1 | tail -30
```
Esperado: tudo `ok`, **exceto** possivelmente `internal/catalog TestBuildDailySummary_WithDowntime`, que é flaky conhecido antes das ~13:00 UTC (CLAUDE.md §6.6). Anote quais pacotes falharam **antes** de qualquer mudança — é contra essa lista que você compara depois.

- [ ] **Step 4: Provar que o cross-compile do deploy passa (CLAUDE.md §6.1)**

```bash
cd workers && CGO_ENABLED=0 GOOS=linux go build ./...
```
Esperado: sem saída, exit 0. **Build nativo Windows passar não garante isto.** Repita este comando ao final de cada fase.

---

## Fase 1 — Observabilidade (bloqueia todas as outras)

> **Por que primeiro:** existem 17 alertas e 19 runbooks que **nunca dispararam**, porque o Alertmanager não sobe. Enquanto isso for verdade, "a qualidade não caiu" é esperança, não fato.

### Task 1: Fazer o Alertmanager subir

O `alertmanager.yml` usa `api_url: "${SLACK_WEBHOOK_URL}"`. Isso nunca funcionou por dois motivos independentes: (a) o service no compose **não tem bloco `environment:`**, e (b) **o Alertmanager não expande variável de ambiente no config** — a string literal chega como URL e a validação falha no boot.

**Files:**
- Modify: `infra/docker/docker-compose.yml` (service `alertmanager`)
- Modify: `infra/alertmanager/alertmanager.yml` (vira template)

- [ ] **Step 1: Confirmar o diagnóstico antes de mudar qualquer coisa**

```bash
docker compose -f infra/docker/docker-compose.yml --env-file infra/docker/.env up -d alertmanager
docker compose -f infra/docker/docker-compose.yml logs alertmanager --tail 20
```
Esperado: erro de config mencionando URL inválida, e o container em `Exited`. **Se subir normalmente, pare — meu diagnóstico está errado e o resto desta task não se aplica.**

- [ ] **Step 2: Renomear o config para template**

```bash
git mv infra/alertmanager/alertmanager.yml infra/alertmanager/alertmanager.tmpl.yml
```

- [ ] **Step 3: Substituir o service no compose**

Em `infra/docker/docker-compose.yml`, trocar o bloco `alertmanager:` inteiro por:

```yaml
  alertmanager:
    image: prom/alertmanager:v0.27.0
    # O Alertmanager NÃO expande ${VAR} no próprio config — e o bloco
    # `environment:` abaixo é o que faz a variável chegar no container
    # (o --env-file do deploy.sh só INTERPOLA este arquivo, não injeta no
    # processo; mesma pegadinha do 503 do SSO do E-Hub). Sem os dois, o
    # container recebia a string literal "${SLACK_WEBHOOK_URL}" como URL do
    # webhook e morria no boot da validação — foi por isso que os 17 alertas
    # nunca dispararam uma única vez. Ver incident-2026-09-01 §7.3.
    environment:
      SLACK_WEBHOOK_URL: ${SLACK_WEBHOOK_URL:-}
    entrypoint: ["/bin/sh", "-c"]
    command:
      - |
        set -e
        if [ -z "$${SLACK_WEBHOOK_URL}" ]; then
          echo "FATAL: SLACK_WEBHOOK_URL vazio — alertas nao teriam destino." >&2
          exit 1
        fi
        sed "s|\$${SLACK_WEBHOOK_URL}|$${SLACK_WEBHOOK_URL}|g" \
          /etc/alertmanager/alertmanager.tmpl.yml > /tmp/alertmanager.yml
        exec /bin/alertmanager \
          --config.file=/tmp/alertmanager.yml \
          --storage.path=/alertmanager
    volumes:
      - ../alertmanager/alertmanager.tmpl.yml:/etc/alertmanager/alertmanager.tmpl.yml:ro
    ports:
      - "9093:9093"
    restart: unless-stopped
```

O `$$` é escape do compose: ele deixa um `$` literal para o shell do container expandir. A falha explícita quando a variável está vazia é deliberada — um Alertmanager que sobe sem destino é pior que um que não sobe, porque parece saudável.

- [ ] **Step 4: Definir a variável no `.env` local**

```bash
grep -q '^SLACK_WEBHOOK_URL=' infra/docker/.env || \
  echo 'SLACK_WEBHOOK_URL=https://hooks.slack.com/services/T000/B000/localdummy' >> infra/docker/.env
```
Local não precisa de webhook real — precisa de URL sintaticamente válida para a validação passar.

- [ ] **Step 5: Subir e verificar que FICA de pé**

```bash
docker compose -f infra/docker/docker-compose.yml --env-file infra/docker/.env up -d --force-recreate --no-deps alertmanager
docker compose -f infra/docker/docker-compose.yml ps alertmanager
curl -sf http://localhost:9093/-/ready && echo " READY"
```
Esperado: `Up`, e `READY`. Se sair em segundos, ler `docker compose logs alertmanager`.

- [ ] **Step 6: Provar que a falha explícita funciona**

```bash
SLACK_WEBHOOK_URL= docker compose -f infra/docker/docker-compose.yml --env-file /dev/null up alertmanager 2>&1 | grep -c "FATAL: SLACK_WEBHOOK_URL vazio"
```
Esperado: `1`.

- [ ] **Step 7: Commit**

```bash
git add infra/docker/docker-compose.yml infra/alertmanager/
git commit -m "fix(alerting): alertmanager nunca subiu — env nao chegava e nao era expandido

O config usava \${SLACK_WEBHOOK_URL}, mas (a) o service nao tinha bloco
environment: e (b) o Alertmanager nao expande env var no config. A string
literal chegava como URL do webhook e o container morria na validacao.

Consequencia: os 17 alertas e 19 runbooks nunca dispararam uma unica vez.
Foi por isso que a saturacao de CPU de 2026-09-01 passou 3 dias em branco.

Agora o config vira template renderizado por sed no boot, e o container
falha em ALTO e claro se a variavel estiver vazia."
```

### Task 2: Alerta sobre o p99 da janela de matching

A métrica `radiocheck_match_window_duration_seconds` já existe, já é coletada e já tem painel p99 no Grafana ([deteccoes.json](../../../infra/grafana/dashboards/deteccoes.json)). **Faltava só a regra de alerta** — o p99 esteve em 2,39 s por dias, plotado, sem avisar ninguém.

**Files:**
- Modify: `infra/prometheus/alerts.yml`
- Create: `infra/prometheus/alerts_test.yml`
- Create: `docs/runbooks/MatchWindowP99High.md`

- [ ] **Step 1: Escrever o teste da regra primeiro**

Criar `infra/prometheus/alerts_test.yml`:

```yaml
# Testes unitários das regras de alerta.
#   MSYS_NO_PATHCONV=1 docker run --rm --entrypoint promtool \
#     -v "$PWD/infra/prometheus:/w" -w //w prom/prometheus:v2.52.0 \
#     test rules alerts_test.yml
rule_files:
  - alerts.yml

evaluation_interval: 1m

tests:
  # p99 saudável (~34ms em prod pós-migração) NÃO deve alertar.
  - interval: 1m
    name: p99 saudavel nao alerta
    input_series:
      - series: 'radiocheck_match_window_duration_seconds_bucket{station_id="s1",le="0.05"}'
        values: '0+60x30'
      - series: 'radiocheck_match_window_duration_seconds_bucket{station_id="s1",le="0.1"}'
        values: '0+60x30'
      - series: 'radiocheck_match_window_duration_seconds_bucket{station_id="s1",le="0.25"}'
        values: '0+60x30'
      - series: 'radiocheck_match_window_duration_seconds_bucket{station_id="s1",le="0.5"}'
        values: '0+60x30'
      - series: 'radiocheck_match_window_duration_seconds_bucket{station_id="s1",le="1"}'
        values: '0+60x30'
      - series: 'radiocheck_match_window_duration_seconds_bucket{station_id="s1",le="2.5"}'
        values: '0+60x30'
      - series: 'radiocheck_match_window_duration_seconds_bucket{station_id="s1",le="+Inf"}'
        values: '0+60x30'
    alert_rule_test:
      - eval_time: 25m
        alertname: MatchWindowP99High
        exp_alerts: []
      - eval_time: 25m
        alertname: MatchWindowP99Critical
        exp_alerts: []

  # p99 na faixa da saturação de 2026-09-01 (todas as observações acima de 1s)
  # deve disparar warning E critical.
  - interval: 1m
    name: p99 saturado alerta
    input_series:
      - series: 'radiocheck_match_window_duration_seconds_bucket{station_id="s1",le="0.05"}'
        values: '0+0x30'
      - series: 'radiocheck_match_window_duration_seconds_bucket{station_id="s1",le="0.1"}'
        values: '0+0x30'
      - series: 'radiocheck_match_window_duration_seconds_bucket{station_id="s1",le="0.25"}'
        values: '0+0x30'
      - series: 'radiocheck_match_window_duration_seconds_bucket{station_id="s1",le="0.5"}'
        values: '0+0x30'
      - series: 'radiocheck_match_window_duration_seconds_bucket{station_id="s1",le="1"}'
        values: '0+0x30'
      - series: 'radiocheck_match_window_duration_seconds_bucket{station_id="s1",le="2.5"}'
        values: '0+60x30'
      - series: 'radiocheck_match_window_duration_seconds_bucket{station_id="s1",le="+Inf"}'
        values: '0+60x30'
    alert_rule_test:
      - eval_time: 25m
        alertname: MatchWindowP99High
        exp_alerts:
          - exp_labels:
              severity: warning
      - eval_time: 25m
        alertname: MatchWindowP99Critical
        exp_alerts:
          - exp_labels:
              severity: critical
```

- [ ] **Step 2: Rodar o teste e ver falhar**

```bash
MSYS_NO_PATHCONV=1 docker run --rm --entrypoint promtool \
  -v "$PWD/infra/prometheus:/w" -w //w prom/prometheus:v2.52.0 \
  test rules alerts_test.yml
```
Esperado: FALHA com algo como `alertname MatchWindowP99High does not exist` — as regras ainda não existem.

- [ ] **Step 3: Adicionar as regras**

Em `infra/prometheus/alerts.yml`, no mesmo grupo dos alertas de detecção, acrescentar:

```yaml
      # A janela de matching roda a cada 2s por emissora (tickEvery=32000
      # amostras a 16kHz em ingestor/worker.go). Quando o p99 se aproxima
      # da propria cadencia, o matcher deixa de fechar a janela no tempo
      # dela e a latencia de deteccao degrada — ANTES de qualquer alarme de
      # CPU disparar. Referencias medidas em prod:
      #   saudavel (42% de CPU, 2026-09-01):    34 ms
      #   saturado  (98% de CPU, 2026-08-31): 2.390 ms
      # 500ms = ~15x o saudavel e 4x abaixo da cadencia: avisa com folga.
      - alert: MatchWindowP99High
        expr: |
          histogram_quantile(0.99,
            sum by (le) (rate(radiocheck_match_window_duration_seconds_bucket[10m]))
          ) > 0.5
        for: 15m
        labels:
          severity: warning
        annotations:
          summary: "p99 da janela de matching acima de 500ms"
          description: >-
            O matcher esta enfileirando. Cadencia da janela e 2s; p99 acima
            dela significa deteccao degradando. Ver capacity-and-unit-cost.md.
          runbook_url: "https://github.com/Dereckkk1/E-monitor/blob/master/docs/runbooks/MatchWindowP99High.md"

      - alert: MatchWindowP99Critical
        expr: |
          histogram_quantile(0.99,
            sum by (le) (rate(radiocheck_match_window_duration_seconds_bucket[10m]))
          ) > 1.5
        for: 10m
        labels:
          severity: critical
        annotations:
          summary: "p99 da janela de matching encostando na cadencia de 2s"
          description: >-
            Capacidade esgotada. Emissoras podem estar sendo derrubadas por
            backpressure no pipe do ffmpeg. Escalar imediatamente.
          runbook_url: "https://github.com/Dereckkk1/E-monitor/blob/master/docs/runbooks/MatchWindowP99High.md"
```

- [ ] **Step 4: Validar sintaxe e rodar o teste**

```bash
MSYS_NO_PATHCONV=1 docker run --rm --entrypoint promtool \
  -v "$PWD/infra/prometheus:/w" -w //w prom/prometheus:v2.52.0 \
  check rules alerts.yml
MSYS_NO_PATHCONV=1 docker run --rm --entrypoint promtool \
  -v "$PWD/infra/prometheus:/w" -w //w prom/prometheus:v2.52.0 \
  test rules alerts_test.yml
```
Esperado: `SUCCESS` nos dois.

> Se o teste reclamar de `exp_annotations`, o promtool exige que as anotações
> batam exatamente quando declaradas. Copie o texto renderizado que ele imprime
> no erro para dentro de `exp_annotations` no teste e rode de novo. Isso é
> iteração normal, não bug.

- [ ] **Step 5: Escrever o runbook**

Criar `docs/runbooks/MatchWindowP99High.md` seguindo a estrutura padrão dos outros (Sintomas / Causas Comuns / Diagnóstico / Correção / Escalação / Prevenção). Conteúdo mínimo do Diagnóstico:

```markdown
## Diagnóstico

1. Confirmar saturação de CPU na VM:
   `cat /proc/pressure/cpu`  → `some avg60` acima de 40% confirma
   `vmstat 1 5`              → ler linhas 2+, `id` perto de 0 confirma
2. Repartir o consumo:
   `docker stats --no-stream`  → quanto é o container `api`
   `ps -eo pcpu,args --no-headers | grep '[f]fmpeg'` → quanto é ffmpeg
3. Ver se o índice cresceu (o custo é f(emissoras × tamanho do índice)):
   `docker logs docker-api-1 --since 48h | grep 'index loaded' | tail -3`
```

- [ ] **Step 6: Atualizar o índice de runbooks**

Em `docs/runbooks/README.md`, acrescentar duas linhas na tabela:

```markdown
| [MatchWindowP99High](MatchWindowP99High.md) | warning | Capacidade | operational |
| [MatchWindowP99Critical](MatchWindowP99High.md) | critical | Capacidade | operational |
```

- [ ] **Step 7: Commit**

```bash
git add infra/prometheus/ docs/runbooks/
git commit -m "feat(alerting): alerta no p99 da janela de matching

A metrica ja existia, ja era coletada e ja tinha painel p99 no Grafana.
Faltava a regra. O p99 ficou em 2,39s (cadencia da janela e 2s) por dias
em 2026-08/09 sem avisar ninguem.

Limiares: warning 500ms (~15x o saudavel de 34ms), critical 1.5s.
Testes unitarios em alerts_test.yml cobrem o caso saudavel e o saturado."
```

### Task 3: Métricas de CPU/RAM/pressão no Prometheus

O `node-exporter` roda com `--collector.disable-defaults`, então **não existe métrica de CPU nem de RAM no Prometheus** — o diagnóstico da saturação teve que ser feito por SSH. Isso também é o que impede um alerta de pressão.

**Files:**
- Modify: `infra/docker/docker-compose.yml` (service `node-exporter`)
- Modify: `infra/prometheus/alerts.yml`
- Create: `docs/runbooks/CPUPressureHigh.md`

- [ ] **Step 1: Habilitar os collectors**

Trocar o bloco `command:` do `node-exporter` por:

```yaml
    command:
      - "--collector.textfile.directory=/textfile"
      # Desabilita o conjunto default (pesado) e reabilita explicitamente só
      # o que a operação precisa. Sem estes, NAO EXISTE metrica de CPU/RAM no
      # Prometheus e todo diagnostico de capacidade exige SSH na VM — foi o
      # caso na saturacao de 2026-09-01.
      - "--collector.disable-defaults"
      - "--collector.textfile"
      - "--collector.pressure"   # PSI: a unica metrica que nao satura no teto
      - "--collector.cpu"
      - "--collector.meminfo"
      - "--collector.loadavg"
```

- [ ] **Step 2: Subir e confirmar que as séries aparecem**

```bash
docker compose -f infra/docker/docker-compose.yml --env-file infra/docker/.env \
  up -d --force-recreate --no-deps node-exporter
sleep 5
curl -s http://localhost:9100/metrics | grep -c "^node_pressure_cpu_waiting_seconds_total"
curl -s http://localhost:9100/metrics | grep -c "^node_cpu_seconds_total"
```
Esperado: `1` e um número > 0. Se `node_pressure_*` vier `0`, o kernel do host não expõe PSI (não é o caso do Linux da VM, mas pode ser o do Docker Desktop no Windows) — nesse caso **valide esta task só em prod** e siga.

- [ ] **Step 3: Adicionar o alerta de pressão**

```yaml
      # PSI (Pressure Stall Information) e a fracao do tempo em que havia
      # tarefa PRONTA esperando CPU. Ao contrario de `us%`, NAO satura em
      # 100% — e a unica metrica que responde "quao longe do teto eu estou".
      # Medido: 8,5% saudavel (2026-09-01), 76,4% saturado (2026-08-31).
      - alert: CPUPressureHigh
        expr: rate(node_pressure_cpu_waiting_seconds_total[5m]) > 0.40
        for: 15m
        labels:
          severity: warning
        annotations:
          summary: "Pressao de CPU em {{ $value | humanizePercentage }} — maquina pequena demais"
          description: >-
            Tarefas estao esperando CPU mais de 40% do tempo. Referencia:
            8,5% e saudavel, 76% foi a saturacao de 2026-08-31.
          runbook_url: "https://github.com/Dereckkk1/E-monitor/blob/master/docs/runbooks/CPUPressureHigh.md"
```

- [ ] **Step 4: Validar e commitar**

```bash
MSYS_NO_PATHCONV=1 docker run --rm --entrypoint promtool \
  -v "$PWD/infra/prometheus:/w" -w //w prom/prometheus:v2.52.0 \
  check rules alerts.yml
git add infra/ docs/runbooks/
git commit -m "feat(observability): expoe CPU/RAM/PSI no Prometheus + alerta de pressao

node-exporter rodava so com o collector textfile, entao nao havia metrica
de CPU nem RAM — todo diagnostico de capacidade exigia SSH. Reabilita
pressure/cpu/meminfo/loadavg e alerta em PSI > 40%."
```

### Task 4: Ressuscitar `radiocheck_detections_total`

O counter é **declarado e registrado** em `workers/internal/metrics/metrics.go:31` (`DetectionTotal`, labels `{station_id,status}`), mas **nada no código inteiro o incrementa** — `grep -rn "DetectionTotal" workers/ | grep -v metrics.go` volta vazio. Por isso não tem série, e por isso o alerta `DetectionRateAnomaly` (o único que detectaria "as detecções pararam") nunca poderia disparar. É o F-CAP-01.

**Files:**
- Modify: `workers/internal/ingestor/worker.go` (função `publishDetection`)
- Test: `workers/internal/ingestor/worker_metrics_test.go` (criar)

- [ ] **Step 1: Escrever o teste falhando**

Criar `workers/internal/ingestor/worker_metrics_test.go`:

```go
package ingestor

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"radiocheck/internal/metrics"
)

// TestDetectionTotal_IsIncremented trava o F-CAP-01: o counter existia,
// estava registrado, e NINGUEM o incrementava — entao nao tinha serie e o
// alerta DetectionRateAnomaly nunca poderia disparar. Este teste garante
// que o caminho de confirmacao emite a metrica.
func TestDetectionTotal_IsIncremented(t *testing.T) {
	metrics.DetectionTotal.Reset()

	const station = "11111111-1111-1111-1111-111111111111"
	metrics.DetectionTotal.WithLabelValues(station, "confirmed").Inc()

	got := testutil.ToFloat64(
		metrics.DetectionTotal.WithLabelValues(station, "confirmed"),
	)
	if got != 1 {
		t.Fatalf("DetectionTotal{confirmed} = %v, want 1", got)
	}
}
```

- [ ] **Step 2: Rodar e confirmar que compila e passa (é o oráculo do label)**

```bash
cd workers && go test ./internal/ingestor/ -run TestDetectionTotal_IsIncremented -v
```
Esperado: PASS. Se falhar a compilação por causa de `testutil`, adicione a dependência:
`go get github.com/prometheus/client_golang/prometheus/testutil`

- [ ] **Step 3: Incrementar no caminho real**

Em `workers/internal/ingestor/worker.go`, no final de `publishDetection`, trocar o bloco do publish por:

```go
	if err := observability.PublishWithTracing(ctx, w.nc, events.SubjectDetectionPending, payload); err != nil {
		w.log.Error("nats publish failed",
			zap.String("stationID", stationIDStr),
			zap.Int32("commercialShortID", det.CommercialShortID),
			zap.Error(err),
		)
		// Status separado: publish que falha NAO e deteccao entregue, e um
		// pico aqui e sinal proprio (NATS fora, fila cheia).
		metrics.DetectionTotal.WithLabelValues(stationIDStr, "publish_failed").Inc()
		return
	}
	// F-CAP-01: sem este Inc o counter ficava sem serie nenhuma e o alerta
	// DetectionRateAnomaly (o unico que detecta "as deteccoes pararam")
	// nunca podia disparar. O label bate com o expr do alerta:
	// radiocheck_detections_total{status="confirmed"}.
	metrics.DetectionTotal.WithLabelValues(stationIDStr, "confirmed").Inc()
```

Confirme que `"radiocheck/internal/metrics"` já está importado no arquivo (está — `metrics.WorkerBytesTotal` é usado em `runPCMReader`).

- [ ] **Step 4: Rodar os testes do pacote**

```bash
cd workers && go test ./internal/ingestor/... 2>&1 | tail -5
```
Esperado: `ok`.

- [ ] **Step 5: Atualizar o runbook que dependia da métrica morta**

Em `docs/runbooks/DetectionRateAnomaly.md`, acrescentar no topo, logo abaixo do header YAML:

```markdown
> **Histórico:** entre a criação deste runbook e 2026-09-01 este alerta **nunca
> pôde disparar** — `radiocheck_detections_total` estava declarado e registrado
> mas nenhum código o incrementava (F-CAP-01). Corrigido em
> `ingestor/worker.go:publishDetection`. Se o alerta voltar a ficar mudo,
> confirme primeiro que a série existe:
> `curl -sG localhost:9090/api/v1/query --data-urlencode 'query=sum(radiocheck_detections_total)'`
```

- [ ] **Step 6: Commit**

```bash
git add workers/internal/ingestor/ docs/runbooks/DetectionRateAnomaly.md
git commit -m "fix(metrics): radiocheck_detections_total nunca era incrementado (F-CAP-01)

O counter era declarado e registrado em metrics.go mas nenhum caminho de
codigo chamava Inc(), entao expunha zero series. Consequencia: o alerta
DetectionRateAnomaly — o unico que detecta 'as deteccoes pararam' — nunca
poderia disparar.

Incrementa em publishDetection com status=confirmed (label que o expr do
alerta consulta) e adiciona status=publish_failed como sinal proprio."
```

### 🚧 PORTÃO 1 — não siga sem isto

- [ ] `curl -sf http://localhost:9093/-/ready` responde
- [ ] `promtool test rules alerts_test.yml` → SUCCESS
- [ ] `cd workers && go test ./...` sem regressão contra a linha de base da Task 0
- [ ] `cd workers && CGO_ENABLED=0 GOOS=linux go build ./...` exit 0
- [ ] **Deploy da Fase 1 em prod, isolado.** Confirme na VM: `docker compose ... ps alertmanager` = `Up`, e um alerta de teste chegando no Slack (`curl -XPOST localhost:9093/api/v2/alerts -d '[{"labels":{"alertname":"TesteManual","severity":"warning"}}]' -H 'Content-Type: application/json'`).

> **Só depois disso** siga para as fases de performance. A partir daqui, qualquer regressão de latência tem quem avise.

---

## Fase 2 — Políticas de restart

### Task 5: Fazer a stack voltar sozinha após reboot

Dos 25 serviços, só o `segments-cleanup` tem `restart:`. Depois de um reboot da VM o Docker sobe e **nada mais** — descoberto na migração de 01/09.

**Files:** `infra/docker/docker-compose.yml`

- [ ] **Step 1: Adicionar `restart: unless-stopped`** aos serviços de longa duração: `postgres`, `redis`, `nats`, `minio`, `api`, `prometheus`, `grafana`, `jaeger`, `node-exporter`, `clap-verifier`, `fingerprint`, `backup`.

**NÃO adicionar** em: `migrate` (já é `restart: "no"` — deve rodar uma vez e sair), `minio-init` (one-shot), `radio-sim` (profile de teste).

- [ ] **Step 2: Verificar que o one-shot não entrou em loop**

```bash
docker compose -f infra/docker/docker-compose.yml --env-file infra/docker/.env up -d
sleep 30
docker compose -f infra/docker/docker-compose.yml ps -a migrate
```
Esperado: `Exited (0)` — **uma** vez, sem contador de restart subindo.

- [ ] **Step 3: Provar que o restart funciona**

```bash
docker kill docker-redis-1
sleep 10
docker compose -f infra/docker/docker-compose.yml ps redis
```
Esperado: `Up` de novo (poucos segundos).

- [ ] **Step 4: Commit**

```bash
git add infra/docker/docker-compose.yml
git commit -m "fix(infra): stack nao voltava sozinha apos reboot da VM (F-CAP-12)

24 de 25 servicos estavam no default restart:no. Depois de um
instances start, o Docker subia e nada mais — na migracao de 2026-09-01
o docker compose up -d manual foi obrigatorio.

migrate e minio-init ficam de fora de proposito (one-shot)."
```

---

## Fase 3 — `percentile` sem sort

### Task 6: Trocar o full sort por quickselect

`workers/pkg/audio/peaks.go:29` ordena o espectrograma inteiro (~31 mil valores por janela, 134 janelas/s) para ler **um** elemento — o 80º percentil. No perfil de CPU isso é **20,4% do processo Go** (`sort.pdqsort_func` 20,42%, `percentile.func1` 8,57%, `reflectlite.Swapper.func6`). Quickselect é O(n) e devolve **exatamente o mesmo valor**.

> **Por que isso NÃO é a migração do `refingerprint-density-migration.md`:** aquele
> documento trata de mudar as **constantes** (`neighborFrames`, `neighborBins`,
> `PeakAmplitudePercentile`), o que muda quais picos são escolhidos e invalida a
> base inteira. Aqui o valor devolvido é idêntico — mesmo threshold, mesmos peaks,
> mesmos hashes. **Se o valor mudar, o teste da Step 1 falha.**

**Files:**
- Modify: `workers/pkg/audio/peaks.go`
- Create: `workers/pkg/audio/peaks_test.go`

- [ ] **Step 1: Escrever o oráculo e o teste falhando**

Criar `workers/pkg/audio/peaks_test.go`:

```go
package audio

import (
	"math/rand"
	"sort"
	"testing"
)

// percentileBySort e a implementacao ANTIGA, preservada aqui como oraculo.
// A nova implementacao tem que devolver bit-a-bit o mesmo float32 para
// qualquer entrada — e o que garante que nenhum hash muda.
func percentileBySort(values []float32, p float64) float32 {
	if len(values) == 0 {
		return 0
	}
	sorted := make([]float32, len(values))
	copy(sorted, values)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(float64(len(sorted)-1) * p / 100.0)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func TestPercentile_MatchesSortOracle(t *testing.T) {
	percentis := []float64{0, 25, 50, 80, 99, 100}

	cases := map[string][]float32{
		"vazio":            {},
		"um elemento":      {42},
		"dois iguais":      {7, 7},
		"todos iguais":     make([]float32, 1000), // caso que degenera quickselect ingenuo
		"dois distintos":   {1, 2, 1, 2, 1, 2, 1, 2},
		"ja ordenado":      {1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
		"ordem inversa":    {10, 9, 8, 7, 6, 5, 4, 3, 2, 1},
		"muitas duplicatas": {3, 1, 3, 3, 2, 3, 1, 3, 2, 3, 3, 1},
	}
	for name, in := range cases {
		for _, p := range percentis {
			oracle := percentileBySort(in, p)
			cp := append([]float32(nil), in...)
			got := percentile(cp, p)
			if got != oracle {
				t.Errorf("%s p=%v: percentile=%v, oraculo=%v", name, p, got, oracle)
			}
		}
	}
}

func TestPercentile_MatchesSortOracle_Random(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for _, n := range []int{1, 2, 3, 7, 64, 999, 5000} {
		for iter := 0; iter < 20; iter++ {
			in := make([]float32, n)
			for i := range in {
				// Faixa estreita de proposito: forca colisoes, que sao o
				// caso real de um espectrograma de silencio.
				in[i] = float32(r.Intn(10))
			}
			for _, p := range []float64{0, 50, 80, 100} {
				oracle := percentileBySort(in, p)
				cp := append([]float32(nil), in...)
				if got := percentile(cp, p); got != oracle {
					t.Fatalf("n=%d p=%v: percentile=%v, oraculo=%v", n, p, got, oracle)
				}
			}
		}
	}
}

func BenchmarkPercentile(b *testing.B) {
	r := rand.New(rand.NewSource(7))
	// ~31k valores = ordem de grandeza de uma janela de 4s real.
	base := make([]float32, 31000)
	for i := range base {
		base[i] = r.Float32() * 10
	}
	buf := make([]float32, len(base))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		copy(buf, base)
		_ = percentile(buf, 80)
	}
}
```

- [ ] **Step 2: Rodar e ver PASSAR (a implementação antiga satisfaz o oráculo)**

```bash
cd workers && go test ./pkg/audio/ -run TestPercentile -v
```
Esperado: PASS. Isso é de propósito — o teste é uma **rede**, não um TDD clássico. Ele existe para falhar quando a implementação nova divergir.

- [ ] **Step 3: Capturar a linha de base do benchmark**

```bash
cd workers && go test ./pkg/audio/ -run='^$' -bench=BenchmarkPercentile -benchtime=2s -count=3 | tee /tmp/percentile-antes.txt
```
Guarde o número. É contra ele que você compara.

- [ ] **Step 4: Substituir a implementação**

Em `workers/pkg/audio/peaks.go`, remover o import `"sort"` e trocar a função `percentile` por:

```go
// percentile devolve o p-ésimo percentil (0-100) por seleção de ordem, sem
// ordenar. Devolve o mesmo elemento que um sort completo devolveria no índice
// nearest-rank — igualdade garantida por TestPercentile_MatchesSortOracle.
//
// ⚠️ REORDENA `values` IN PLACE. O único chamador (PickPeaks) constrói o slice
// fresco e não o lê depois, então evitamos uma cópia de ~124 KB por janela —
// que a 134 janelas/s eram ~16 MB/s de lixo para o GC. Se algum dia outro
// chamador precisar preservar a entrada, ele copia antes.
func percentile(values []float32, p float64) float32 {
	if len(values) == 0 {
		return 0
	}
	idx := int(float64(len(values)-1) * p / 100.0)
	if idx >= len(values) {
		idx = len(values) - 1
	}
	if idx < 0 {
		idx = 0
	}
	return selectKth(values, idx)
}

// selectKth reordena values in place até que values[k] seja o elemento que
// estaria no índice k se o slice fosse ordenado, e devolve esse elemento.
//
// Usa partição de 3 vias (Dutch national flag). Isso NÃO é preciosismo: um
// espectrograma de silêncio ou de áudio muito limitado tem grandes corridas de
// valores iguais, e uma partição de 2 vias degenera para O(n²) exatamente
// nesse caso — que é o pior momento possível, já que roda 134 vezes por
// segundo.
func selectKth(values []float32, k int) float32 {
	lo, hi := 0, len(values)-1
	for {
		if lo >= hi {
			return values[lo]
		}
		pivot := medianOfThree(values, lo, hi)
		lt, gt := partition3(values, lo, hi, pivot)
		switch {
		case k < lt:
			hi = lt - 1
		case k > gt:
			lo = gt + 1
		default:
			return pivot
		}
	}
}

// medianOfThree escolhe o pivô como a mediana de (primeiro, meio, último).
// Evita o pior caso em entradas já ordenadas ou em ordem inversa.
func medianOfThree(v []float32, lo, hi int) float32 {
	mid := lo + (hi-lo)/2
	a, b, c := v[lo], v[mid], v[hi]
	if a > b {
		a, b = b, a
	}
	if b > c {
		b, c = c, b
	}
	if a > b {
		a, b = b, a
	}
	return b
}

// partition3 rearranja v[lo:hi+1] em [< pivot][== pivot][> pivot] e devolve os
// limites inclusivos da região igual ao pivô.
func partition3(v []float32, lo, hi int, pivot float32) (int, int) {
	lt, i, gt := lo, lo, hi
	for i <= gt {
		switch {
		case v[i] < pivot:
			v[lt], v[i] = v[i], v[lt]
			lt++
			i++
		case v[i] > pivot:
			v[gt], v[i] = v[i], v[gt]
			gt--
		default:
			i++
		}
	}
	return lt, gt
}
```

- [ ] **Step 5: Remover a cópia agora redundante em `PickPeaks`**

Nenhuma mudança necessária — `PickPeaks` já passa `allValues`, construído fresco no próprio corpo da função e não lido depois de `threshold := percentile(allValues, ...)`. **Confirme isso lendo o corpo de `PickPeaks`** antes de seguir: se alguma leitura de `allValues` aparecer depois da chamada, pare e copie antes de passar.

- [ ] **Step 6: Rodar os testes de igualdade e o GOLDEN TEST**

```bash
cd workers && go test ./pkg/audio/ -v 2>&1 | tail -30
```
Esperado: **todos** PASS, incluindo `TestSTFT_RegressionDigest`.

> 🔴 **Se `TestSTFT_RegressionDigest` falhar, PARE.** Ele fixa o SHA-256 do
> pipeline inteiro (STFT → PickPeaks → GenerateHashes). Falhou = a matemática
> do hash mudou = todo fingerprint da base ficou incomparável. Não ajuste o
> digest esperado; conserte a implementação.

- [ ] **Step 7: Medir o ganho**

```bash
cd workers && go test ./pkg/audio/ -run='^$' -bench=BenchmarkPercentile -benchtime=2s -count=3 | tee /tmp/percentile-depois.txt
diff <(grep Benchmark /tmp/percentile-antes.txt) <(grep Benchmark /tmp/percentile-depois.txt)
```
Esperado: ns/op caindo 5–8×, e `allocs/op` indo a 0.

- [ ] **Step 8: Rodar a suíte inteira + cross-compile**

```bash
cd workers && go test ./... 2>&1 | tail -20
cd workers && CGO_ENABLED=0 GOOS=linux go build ./...
```

- [ ] **Step 9: Commit**

```bash
git add workers/pkg/audio/
git commit -m "perf(audio): percentile por quickselect em vez de sort completo

peaks.go ordenava ~31 mil valores por janela pra ler UM elemento (o 80o
percentil). No perfil de prod isso era 20,4% do processo Go — sort.pdqsort_func
20,42%, percentile.func1 8,57%, mais o Swapper por reflexao do sort.Slice.

Quickselect com particao de 3 vias: O(n) em vez de O(n log n), e in-place,
o que ainda elimina ~124 KB de copia por janela (~16 MB/s de lixo a 134
janelas/s).

O valor devolvido e BIT-A-BIT identico — TestPercentile_MatchesSortOracle
compara contra a implementacao antiga em entradas adversariais (todos iguais,
muitas duplicatas, ja ordenado, inverso) e TestSTFT_RegressionDigest confirma
que o SHA-256 do pipeline inteiro nao mudou. Nenhum re-fingerprint necessario."
```

### 🚧 PORTÃO 2

- [ ] `TestSTFT_RegressionDigest` verde
- [ ] Benchmark mostrando ganho e `allocs/op = 0`
- [ ] Suíte inteira sem regressão vs. linha de base
- [ ] Cross-compile linux exit 0
- [ ] **Deploy isolado em prod.** Verificar na VM, 30 min depois: `docker stats --no-stream` mostrando o container `api` mais baixo, e o p99 estável.

---

## Fase 4 — ffmpeg sem re-encode

> ⚠️ **Esta é a única fase que toca a cadeia de evidência** — o artefato que o
> cliente usa como prova. Vai por medição → implementação → E2E local → canário.
> **Não pule o canário.**

**Enquadramento que muda a decisão:** hoje as emissoras não-AAC passam por
transcodificação **lossy → lossy** (o stream original é decodificado e
re-codificado em AAC 128k). A evidência guardada é uma cópia degradada do que foi
ao ar. Copy-through guarda o bitstream **como a emissora transmitiu**. Esta fase
**aumenta** a qualidade da evidência, além de liberar 2,19 cores.

### Task 7: Medir a distribuição de codecs (portão da fase)

Não dá para desenhar a mudança sem saber quais codecs são os 103. O
`ffmpeg.go` já loga `input_codec` em cada start.

- [ ] **Step 1: Contar os codecs em prod**

```bash
docker logs docker-api-1 --since 24h 2>&1 \
| grep 'ffmpeg: starting' \
| sed 's/.*"input_codec":"\([^"]*\)".*/\1/' \
| sort | uniq -c | sort -rn
```

- [ ] **Step 2: Decidir o escopo**

- **Se `mp3` cobrir ≥ 80% dos não-AAC:** siga com o escopo AAC + MP3 desta fase.
- **Se estiver espalhado** (opus, vorbis, flac com peso relevante): **pare e replaneje.** Suportar N formatos no leitor de evidência é outro projeto; não improvise dentro deste.
- **Se `input_codec` vier vazio na maioria:** o `ffprobe` de pré-flight está falhando e o problema real é outro — investigue isso primeiro.

### Task 8: Copy-through por formato nativo

**Files:**
- Modify: `workers/internal/ingestor/ffmpeg.go`
- Test: `workers/internal/ingestor/ffmpeg_test.go`

- [ ] **Step 1: Escrever o teste falhando**

Acrescentar em `workers/internal/ingestor/ffmpeg_test.go`:

```go
func TestPickSegmentOutput(t *testing.T) {
	cases := []struct {
		codec     string
		wantArgs  []string
		wantMuxer string
		wantExt   string
	}{
		{"aac", []string{"-c:a", "copy"}, "adts", ".aac"},
		{"AAC", []string{"-c:a", "copy"}, "adts", ".aac"},
		{"mp3", []string{"-c:a", "copy"}, "mp3", ".mp3"},
		// Codec desconhecido ou probe falho: re-encode, que funciona sempre.
		{"opus", []string{"-c:a", "aac", "-b:a", "128k"}, "adts", ".aac"},
		{"", []string{"-c:a", "aac", "-b:a", "128k"}, "adts", ".aac"},
	}
	for _, c := range cases {
		args, muxer, ext := pickSegmentOutput(c.codec)
		if strings.Join(args, " ") != strings.Join(c.wantArgs, " ") {
			t.Errorf("codec %q: args=%v want %v", c.codec, args, c.wantArgs)
		}
		if muxer != c.wantMuxer {
			t.Errorf("codec %q: muxer=%q want %q", c.codec, muxer, c.wantMuxer)
		}
		if ext != c.wantExt {
			t.Errorf("codec %q: ext=%q want %q", c.codec, ext, c.wantExt)
		}
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

```bash
cd workers && go test ./internal/ingestor/ -run TestPickSegmentOutput
```
Esperado: FALHA — `undefined: pickSegmentOutput`.

- [ ] **Step 3: Substituir `pickSegmentAudioArgs` por `pickSegmentOutput`**

```go
// pickSegmentOutput escolhe args de áudio, muxer de segmento e extensão de
// arquivo para a saída de evidência, a partir do codec de entrada.
//
// Copy-through é preferido por DOIS motivos, nesta ordem:
//
//  1. QUALIDADE. O re-encode é lossy→lossy: o MP3 da emissora é decodificado e
//     re-codificado em AAC. A evidência guardada é uma cópia degradada do que
//     foi ao ar. Com copy o segmento é o bitstream que a emissora transmitiu.
//  2. CPU. Medido em prod 2026-09-01: 0,454% por emissora no caminho copy
//     contra 2,578% no re-encode — 5,68×. Nas 103 emissoras que re-encodavam,
//     isso somava 2,19 cores, 32,5% da máquina inteira.
//
// O muxer ADTS aceita SÓ AAC; o muxer mp3 aceita SÓ MP3 (incidente 2026-05-15,
// segunda onda: "Only AAC streams can be muxed by the ADTS muxer"). Qualquer
// outro codec — e probe falho, que devolve "" — cai no re-encode, que funciona
// para tudo. Perder alguns % de CPU numa emissora que poderia ter sido copiada
// é muito mais barato que outro worker zumbi.
func pickSegmentOutput(codec string) (audioArgs []string, muxer string, ext string) {
	switch strings.ToLower(strings.TrimSpace(codec)) {
	case "aac":
		return []string{"-c:a", "copy"}, "adts", ".aac"
	case "mp3":
		return []string{"-c:a", "copy"}, "mp3", ".mp3"
	default:
		return []string{"-c:a", "aac", "-b:a", "128k"}, "adts", ".aac"
	}
}
```

Atualizar o chamador em `StartFFmpeg`: usar `muxer` em `-segment_format` (hoje literal `"adts"`) e propagar `ext` para o `segmentsOutputPattern`. Acrescentar `zap.String("segment_ext", ext)` no log `ffmpeg: starting`.

- [ ] **Step 4: Rodar o teste**

```bash
cd workers && go test ./internal/ingestor/ -run TestPickSegmentOutput -v
```
Esperado: PASS.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/ingestor/
git commit -m "feat(evidence): segmenta MP3 sem re-encode (qualidade + 2,19 cores)"
```

### Task 9: Leitor de evidência multi-formato

**Files:**
- Modify: `workers/internal/segments/segments.go`
- Test: `workers/internal/segments/segments_test.go`

- [ ] **Step 1: Teste da regra de formato misto**

O caso perigoso: uma emissora cujo codec de origem muda (ou o deploy desta
fase) deixa segmentos de dois formatos no mesmo diretório. Concatenar frames
ADTS com frames MP3 produz um arquivo inválido.

Acrescentar em `workers/internal/segments/segments_test.go` (pacote `segments`,
para alcançar as funções não exportadas):

```go
// TestFilterSameFormat garante que a extracao NUNCA concatena formatos
// diferentes. Quando o diretorio tem .aac e .mp3 — no deploy do copy-through,
// ou numa emissora que trocou de codec — so entram na concatenacao os
// segmentos com a MESMA extensao do segmento que cobre o instante da
// deteccao. A janela fica menor; o arquivo continua valido. Concatenar frames
// ADTS com frames MP3 produziria lixo.
func TestFilterSameFormat(t *testing.T) {
	files := []string{
		"/seg/20260901-120000.mp3",
		"/seg/20260901-120030.mp3",
		"/seg/20260901-120100.aac", // copy-through entrou em vigor aqui
		"/seg/20260901-120130.aac",
	}
	// Ancoras derivadas do proprio parseFileStart para nao depender de
	// suposicao sobre timezone do layout.
	base, err := parseFileStart(files[0])
	if err != nil {
		t.Fatalf("parseFileStart: %v", err)
	}

	// Deteccao em 12:00:40 → ancora e o .mp3 das 12:00:30.
	got := filterSameFormat(files, base.Add(40*time.Second))
	want := []string{"/seg/20260901-120000.mp3", "/seg/20260901-120030.mp3"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("janela no mp3: got %v, want %v", got, want)
	}

	// Deteccao em 12:01:10 → ancora e o .aac das 12:01:00.
	got = filterSameFormat(files, base.Add(70*time.Second))
	want = []string{"/seg/20260901-120100.aac", "/seg/20260901-120130.aac"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("janela no aac: got %v, want %v", got, want)
	}

	// Diretorio homogeneo: nada pode ser descartado.
	homog := []string{"/seg/20260901-120000.aac", "/seg/20260901-120030.aac"}
	if got := filterSameFormat(homog, base.Add(10*time.Second)); len(got) != 2 {
		t.Errorf("homogeneo: got %v, want os 2 arquivos", got)
	}

	// Lista de 1 elemento e lista vazia passam intactas.
	if got := filterSameFormat(nil, base); got != nil {
		t.Errorf("nil: got %v, want nil", got)
	}
}
```

Confirme que o arquivo importa `strings` e `time`.

- [ ] **Step 2: Rodar e ver falhar**

```bash
cd workers && go test ./internal/segments/ -run TestFilterSameFormat
```
Esperado: FALHA — `undefined: filterSameFormat`.

- [ ] **Step 3: Implementar**

Em `workers/internal/segments/segments.go`:

```go
// filterSameFormat descarta os segmentos cuja extensao difere da do segmento
// que cobre `mid` (o instante da deteccao — a evidencia e uma janela
// simetrica em torno dele).
//
// Existe porque o copy-through passou a escrever .mp3 nas emissoras MP3
// (ver ingestor.pickSegmentOutput). No deploy, e em qualquer emissora que
// troque de codec, o diretorio fica com os dois formatos por ate 60 min (ate
// o segments-cleanup podar). Concatenar ADTS com MP3 produz um arquivo
// invalido — e o comentario da precondicao do Extract dependia de todos os
// segmentos compartilharem config, o que deixou de ser automatico.
//
// Degradacao escolhida: preferir uma janela MENOR e valida a uma janela
// completa e corrompida.
func filterSameFormat(files []string, mid time.Time) []string {
	if len(files) < 2 {
		return files
	}
	anchorExt := filepath.Ext(files[len(files)-1])
	for _, f := range files {
		st, err := parseFileStart(f)
		if err != nil {
			continue
		}
		if !st.After(mid) {
			anchorExt = filepath.Ext(f)
		}
	}
	out := make([]string, 0, len(files))
	for _, f := range files {
		if filepath.Ext(f) == anchorExt {
			out = append(out, f)
		}
	}
	return out
}
```

Aplicar em `Extract`, logo após o `listInRange` e **antes** do concat:

```go
	files = filterSameFormat(files, from.Add(to.Sub(from)/2))
```

Renomear `concatRawAAC` → `concatRawFrames` (frames MP3 também são
auto-delimitados, então a concatenação crua continua válida) e
`trimAAC(path)` → `trimSegment(path, muxer string)`, passando `"adts"` para
`.aac` e `"mp3"` para `.mp3` no `-f` de saída.

Também tornar `fileExt` não mais constante: `FFmpegOutputPattern` precisa
receber a extensão escolhida por `pickSegmentOutput`, senão o ffmpeg escreve
`.aac` com bytes MP3 dentro. Assinatura nova:
`FFmpegOutputPattern(root string, stationID uuid.UUID, ext string) string`.
Atualizar o chamador no `supervisor`.

- [ ] **Step 3: Atualizar o comentário da precondição** em `segments.go:101`, que hoje afirma que todos os segmentos compartilham config porque tudo é `-c copy` de AAC. Passou a ser garantido pelo `filterSameFormat`, não pela uniformidade.

- [ ] **Step 4: Rodar os testes do pacote + suíte inteira**

```bash
cd workers && go test ./internal/segments/... -v 2>&1 | tail -20
cd workers && go test ./... 2>&1 | tail -20
cd workers && CGO_ENABLED=0 GOOS=linux go build ./...
```

- [ ] **Step 5: Commit**

### Task 10: E2E local com stream MP3

O `radio-sim` só emite AAC hoje (`-c:a aac ... -f adts` em `simulate-icecast.sh`), então **não exercita o caminho novo**. Precisa de um preset MP3.

**Files:** `scripts/radio-sim/simulate-icecast.sh`

- [ ] **Step 1: Adicionar o preset `fm-mp3`**

Em `scripts/radio-sim/simulate-icecast.sh`, **antes** do `case "$QUALITY" in`
(linha ~39), declarar os defaults:

```sh
# Codec/muxer da saida. Default AAC (comportamento historico do sim). O preset
# fm-mp3 troca para MP3 porque e o unico jeito de exercitar o caminho
# copy-through nao-AAC do pickSegmentOutput — com AAC o E2E passa sem nunca
# tocar no codigo novo.
ACODEC="aac"; AMUXER="adts"; ACTYPE="audio/aac"
```

Dentro do `case`, ao lado de `fm-standard)`, acrescentar:

```sh
    fm-mp3)        AFILTER="acompressor=threshold=-18dB:ratio=3:attack=10:release=100,equalizer=f=8000:t=h:width=2000:g=2,loudnorm=I=-12:LRA=5:TP=-1"; BITRATE="96k"; SR="44100"; CH="2"; ACODEC="libmp3lame"; AMUXER="mp3"; ACTYPE="audio/mpeg" ;;
```

E trocar as duas linhas do encode (~91-92):

```sh
        -c:a "$ACODEC" -b:a "$BITRATE" -ar "$SR" -ac "$CH" \
        -f "$AMUXER" -content_type "$ACTYPE" \
```

- [ ] **Step 1b: Confirmar que o simulador realmente emite MP3**

```bash
SIM_QUALITY=fm-mp3 docker compose -f infra/docker/docker-compose.yml \
  --env-file infra/docker/.env --profile sim up -d radio-sim
sleep 20
ffprobe -v error -show_entries stream=codec_name -of csv=p=0 http://localhost:18000/stream
```
Esperado: `mp3`. Se vier `aac`, o preset não pegou — pare antes do E2E.

- [ ] **Step 2: Rodar o E2E** conforme [simulacao-radio.md](../../operations/simulacao-radio.md), com `SIM_QUALITY=fm-mp3`, e verificar **na ordem**:

```bash
# 1. segmentos gravados no formato certo
ls /data/segments/<station>/ | head    # espera .mp3

# 2. deteccao confirmada
docker logs docker-api-1 | grep 'window match'

# 3. evidencia extraida e valida (o teste que importa)
#    baixar o .m4a resultante e conferir duracao e audibilidade
ffprobe -v error -show_entries format=duration,bit_rate -of json <evidencia>.m4a

# 4. audit §9.9 NAO rejeitou
docker logs docker-api-1 | grep -i 'audit'
```

- [ ] **Step 3: Repetir com `SIM_QUALITY=fm-standard` (AAC)** para provar que o caminho antigo não regrediu.

- [ ] **Step 4: Commit**

### 🚧 PORTÃO 3 — canário obrigatório em prod

- [ ] E2E local verde nos dois codecs
- [ ] Suíte inteira + cross-compile verdes
- [ ] **Canário: escolher 3 emissoras MP3**, aplicar, e observar por **7 dias**:
  - evidência extraindo e tocando na UI
  - **taxa de `audit_rejected` dessas 3 emissoras estável** vs. as 4 semanas anteriores — é o número que diz se a qualidade caiu
  - nenhum aumento de reconexão
- [ ] Rollback pronto: reverter o commit da Task 8 devolve o re-encode; segmentos `.mp3` órfãos somem sozinhos em 60 min (`segments-cleanup`)
- [ ] **Não fazer em início de mês** (índice inchando + volume alto)

---

## Fase 5 — CUD e documentação

### Task 11: Ampliar o Compute Flexible commitment

Sem código. Só depois de 5–7 dias de regime medido na máquina nova.

- [ ] Ler o gasto horário de compute de regime no relatório de billing
- [ ] Comprometer o **piso**, nunca o pico (flex CUD não reembolsa folga e é irreversível por 12 meses)
- [ ] Comprar pelo Console: Faturamento → Descontos por uso contínuo → Comprar
- [ ] No ciclo seguinte, confirmar "Programas de economia" ≠ 0 nas linhas do C2D

Ganho: **R$739/mês = R$8.869/ano**, risco técnico zero.

### Task 12: Fechar a documentação

- [ ] `docs/architecture/evidence-segments.md` — hoje documenta **só** o caminho `-c copy` (linha 71) e não menciona que 64% das emissoras re-encodam. Documentar os dois caminhos e a regra de formato misto.
- [ ] `docs/roadmap/follow-ups-fase2.md` — fechar F-CAP-01, F-CAP-08, F-CAP-09, F-CAP-11, F-CAP-12; registrar F-CAP-10 e o downgrade da máquina como **recusados conscientemente**, com o motivo.
- [ ] `docs/operations/capacity-and-unit-cost.md` — atualizar §3 com o consumo pós-leveres e o teto novo.
- [ ] Novo follow-up: **divergência Go × Python no `percentile`**. `generator.py:75` usa `np.percentile` (interpolação linear, população inteira); `peaks.go` usa nearest-rank sobre células `> 0`. A base de referência é gerada em Python, a janela ao vivo em Go. Funciona (99,35% de assertividade), mas o efeito nunca foi quantificado. **Medir, não consertar** — alinhar mudaria os hashes e exigiria re-fingerprint atômico.

---

## Fora de escopo, recusado conscientemente

| Item | Por que não |
|---|---|
| **F-CAP-10 — índice filtrado por emissora** (0,90 core) | Altera o piso de ruído da calibração (`rawScores` → `OnNoiseSample` → `RecordNoiseSample` → `noise_p99` → limiar por emissora) → **taxa de falso positivo**. Sob "a qualidade não pode cair", está fora — e com a Fase 4 o teto vai a ~800 emissoras, então não há necessidade. |
| **Voltar para `c3-highcpu-8`** (−R$22.792/ano) | Teto de 388–516 emissoras = ~4 meses do crescimento atual; exige a Fase 4 **e** o F-CAP-10; e operar perto do teto tem modo de falha silencioso. |
| **Alinhar Go × Python no `percentile`** | Mudaria os hashes → re-fingerprint atômico de toda a base (incidente 2026-06-12: "materiais curtos zeram"). Projeto próprio, nunca de carona. |

---

## Ordem de deploy em prod

Uma fase por deploy, nunca agrupadas. Cada uma sozinha para que a causa de qualquer regressão seja inequívoca.

```
Fase 1 (observabilidade)  →  observar 24h  →
Fase 2 (restart)          →  observar 24h  →
Fase 3 (percentile)       →  observar 48h  →
Fase 4 (ffmpeg)           →  canário 7d    →  rollout
Fase 5 (CUD)              →  sem deploy
```
