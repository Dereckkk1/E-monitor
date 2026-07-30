# Fix do par curto⊂longo (PULSO MILIUM) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Colocar em prod, com validação, a arbitragem por evidência (§18.2.2-v2, flag `DISAMBIG_BY_COVERAGE`) que resolve o caso "material <10s contido em spot do mesmo cliente" — mais instrumentação (métrica+alerta de supressão suspeita), aviso de UI pra material curto, simulador E2E commitado e diagnóstico do histórico.

**Architecture:** NENHUMA arbitragem nova — a máquina v2 (reattributeByCoverage + co-fire guard + reject-path) já está mergeada e foi validada no E2E (spec §2.2, incident report §4d/§4e). O plano é: instrumentar, avisar, commitar o harness, e dar rollout gated na flag. Contexto obrigatório: [spec de design](../specs/2026-07-30-pulso-subset-dedup-fix-design.md) e [incident report](../../incidents/incident-2026-07-24-pulso-milium-nao-detectado.md).

**Tech Stack:** Go (workers), React/JSX (frontend), Prometheus alert rules, bash (sim/runbook), SQL (diagnóstico).

**Regras do projeto que este plano OBEDECE (CLAUDE.md):** §6.1 cross-compile `CGO_ENABLED=0 GOOS=linux go build ./...` antes de push; §5 NUNCA `npm install` no Windows; §4.1/4.2 deploy com `--force-recreate --no-deps api` após build; prod é executado pelo Dereck, nunca pelo agente (§7).

---

### Task 1: Branch e commit dos artefatos da investigação

O working tree principal está em `feat/pos-venda` com mudanças não relacionadas. Este fix nasce de `master` em worktree isolada.

**Files:**
- Modify (já editado no tree principal, copiar): `infra/docker/docker-compose.yml` (linha ~219: passthrough `DISAMBIG_CONFIDENCE_AWARE` + comentário)
- Create (já existem no tree principal, copiar): `docs/incidents/incident-2026-07-24-pulso-milium-nao-detectado.md`, `docs/superpowers/specs/2026-07-30-pulso-subset-dedup-fix-design.md`, `docs/superpowers/plans/2026-07-30-pulso-subset-dedup-fix.md`

- [ ] **Step 1: Criar worktree a partir do master**

```bash
cd c:/Users/marke/Desktop/Programas/E-Series/E-monitor
git worktree add ../E-monitor-fix-subset -b fix/subset-dedup-pulso master
```

- [ ] **Step 2: Copiar os 4 arquivos do tree principal pra worktree**

```bash
cp docs/incidents/incident-2026-07-24-pulso-milium-nao-detectado.md ../E-monitor-fix-subset/docs/incidents/
mkdir -p ../E-monitor-fix-subset/docs/superpowers/specs ../E-monitor-fix-subset/docs/superpowers/plans
cp docs/superpowers/specs/2026-07-30-pulso-subset-dedup-fix-design.md ../E-monitor-fix-subset/docs/superpowers/specs/
cp docs/superpowers/plans/2026-07-30-pulso-subset-dedup-fix.md ../E-monitor-fix-subset/docs/superpowers/plans/
```

Pro compose, aplicar na worktree o MESMO bloco que está no tree principal (conferir com `git -C . diff infra/docker/docker-compose.yml`): logo após a linha `DISAMBIG_BY_COVERAGE: ${DISAMBIG_BY_COVERAGE:-false}`, inserir:

```yaml
      # §18.2.2 dedup confidence-aware (audit A2): gap de cobertura >= 0.25 decide
      # o dedup (o corte que realmente tocou tem cobertura maior); empate cai pra
      # regra de duração (comportamento antigo). Sem esta linha a env NUNCA chega
      # no container (mesma armadilha do SHARING_MIN_SCORE, incidente 2026-06-15).
      # Caso pulso 5.7s ⊂ spot 30s: incident-2026-07-24-pulso-milium-nao-detectado.md
      DISAMBIG_CONFIDENCE_AWARE: ${DISAMBIG_CONFIDENCE_AWARE:-false}
```

- [ ] **Step 3: Commit**

```bash
cd ../E-monitor-fix-subset
git add docs/incidents/incident-2026-07-24-pulso-milium-nao-detectado.md docs/superpowers/specs/2026-07-30-pulso-subset-dedup-fix-design.md docs/superpowers/plans/2026-07-30-pulso-subset-dedup-fix.md infra/docker/docker-compose.yml
git commit -m "docs(pulso): incident report + spec/plano do fix subset-dedup; passthrough DISAMBIG_CONFIDENCE_AWARE"
```

> Todas as tasks seguintes rodam DENTRO de `../E-monitor-fix-subset`.

---

### Task 2: Métrica de supressão suspeita (`suppressed_suspect`)

Supressão que provavelmente matou tocada real = `suppressed_confidence >= kept_confidence + 0.25` (mesma margem `confidenceMargin` já definida em disambiguation.go:143; mesmo predicado do índice parcial `idx_dedup_suppressions_suspect` da migration 0049).

**Files:**
- Modify: `workers/internal/supervisor/disambiguation.go` (função nova + 1 chamada no branch Suppress, ~linha 250)
- Modify: `workers/internal/metrics/metrics.go:227` (comentário dos valores do label)
- Test: `workers/internal/supervisor/disambiguation_test.go`

- [ ] **Step 1: Escrever o teste que falha**

Adicionar ao final de `disambiguation_test.go`:

```go
// isSuspectSuppression — sinal de "provável tocada REAL morta pelo dedup".
// Números do E2E do incidente 2026-07-24 (§4d): pulso real conf 1.0 vs
// false-confirm do spot conf 0.167.
func TestIsSuspectSuppression(t *testing.T) {
	cases := []struct {
		name      string
		sup, kept float64
		want      bool
	}{
		{"pulso real suprimido por false-confirm do spot", 1.0, 0.167, true},
		{"30s⊂60s legitimo (quase-empate de confianca)", 0.95, 0.90, false},
		{"exatamente na margem conta como suspeito", 0.45, 0.20, true},
		{"logo abaixo da margem nao conta", 0.44, 0.20, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isSuspectSuppression(c.sup, c.kept); got != c.want {
				t.Errorf("isSuspectSuppression(%v, %v) = %v, want %v", c.sup, c.kept, got, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

```bash
cd workers && go test ./internal/supervisor -run TestIsSuspectSuppression -v
```
Esperado: FAIL — `undefined: isSuspectSuppression`.

- [ ] **Step 3: Implementar**

Em `disambiguation.go`, logo após a const `confidenceMargin` (linha ~143):

```go
// isSuspectSuppression marca uma supressão §18.2.2 que provavelmente matou uma
// veiculação REAL: o corte suprimido estava materialmente mais confiante que o
// mantido (o mantido só false-confirmou a região compartilhada). Espelha o
// predicado do índice parcial idx_dedup_suppressions_suspect (migration 0049)
// e usa a mesma margem do confidence-aware.
func isSuspectSuppression(suppressedConf, keptConf float64) bool {
	return suppressedConf >= keptConf+confidenceMargin
}
```

No branch `case DedupActionSuppress:` de `SubmitDetection` (~linha 250), logo após `metrics.MatchDisambiguation.WithLabelValues("suppressed").Inc()`:

```go
		if isSuspectSuppression(det.Confidence, conflict.Detection.Confidence) {
			metrics.MatchDisambiguation.WithLabelValues("suppressed_suspect").Inc()
		}
```

Em `metrics.go:227`, atualizar o comentário do label:

```go
	}, []string{"action"}) // suppressed | suppressed_suspect | retracted | reattributed_by_coverage | restored_on_reject | reattributed_on_reject | duplicate_cofire_retracted
```

- [ ] **Step 4: Rodar e ver passar + cross-compile**

```bash
cd workers && go test ./internal/supervisor -run TestIsSuspectSuppression -v
CGO_ENABLED=0 GOOS=linux go build ./...
```
Esperado: PASS e build limpo.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/supervisor/disambiguation.go workers/internal/supervisor/disambiguation_test.go workers/internal/metrics/metrics.go
git commit -m "feat(dedup): métrica suppressed_suspect quando a supressão mata tocada mais confiante"
```

---

### Task 3: Alerta Prometheus + runbook

**Files:**
- Modify: `infra/prometheus/alerts.yml`
- Create: `docs/runbooks/dedup-suppressed-suspect.md`
- Modify: `docs/runbooks/README.md` (linha no índice)

- [ ] **Step 1: Ler `infra/prometheus/alerts.yml` e identificar o grupo de regras existente** (nome do `- name:` e indentação). Adicionar NO MESMO formato do arquivo:

```yaml
      - alert: DedupSuppressedSuspect
        expr: increase(radiocheck_match_disambiguation_total{action="suppressed_suspect"}[1h]) > 0
        for: 0m
        labels:
          severity: warning
        annotations:
          summary: "Dedup §18.2.2 suprimiu tocada provavelmente REAL"
          description: "{{ $value }} supressões na última hora com confiança do suprimido >= mantido+0.25 (false-confirm vencendo tocada real — caso PULSO MILIUM). Rodar a query B5 do incident-2026-07-24 e conferir DISAMBIG_BY_COVERAGE."
```

- [ ] **Step 2: Criar `docs/runbooks/dedup-suppressed-suspect.md`**

```markdown
---
status: implementado
ultima-verificacao: 2026-07-30
codigo-relacionado:
  - workers/internal/supervisor/disambiguation.go
  - migrations/0049_dedup_suppressions.up.sql
---

# Alerta: DedupSuppressedSuspect

**O que significa:** o dedup §18.2.2 suprimiu uma detecção MAIS confiante do que a que
manteve — assinatura de veiculação real morta por um false-confirm de trecho compartilhado
(caso canônico: material <10s contido em spot do mesmo cliente —
[incident-2026-07-24-pulso-milium-nao-detectado.md](../incidents/incident-2026-07-24-pulso-milium-nao-detectado.md)).

**Triagem (VM):**
```bash
DC="docker compose -f infra/docker/docker-compose.yml -f infra/docker/docker-compose.override.yml --env-file infra/docker/.env"
$DC exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB"' <<'SQL'
SELECT to_char(detected_at AT TIME ZONE 'America/Sao_Paulo','DD/MM HH24:MI:SS') AS quando,
       s.name AS emissora, suppressed_short_id, kept_short_id,
       round(suppressed_confidence::numeric,3) AS conf_supr,
       round(kept_confidence::numeric,3) AS conf_kept, reason
FROM dedup_suppressions ds JOIN stations s ON s.id=ds.station_id
WHERE suppressed_confidence >= kept_confidence + 0.25
  AND detected_at > now() - interval '24 hours'
ORDER BY detected_at DESC;
SQL
```

**Ação:**
1. `DISAMBIG_BY_COVERAGE=true` está ativo? (`grep DISAMBIG infra/docker/.env`) — com ele ON,
   a v2 resgata a tocada via a row do corte mantido (reatribui/retrata); confira nos logs
   `reattributed by coverage` / `co-fire` pro mesmo horário.
2. Se a v2 NÃO resgatou (sem row do mantido, ou audit em erro), registrar veiculação manual
   e abrir follow-up — é o caso F-125 (publicar-provisório no supervisor).
```

- [ ] **Step 3: Adicionar linha no índice `docs/runbooks/README.md`** (mesmo formato das existentes): `DedupSuppressedSuspect → dedup-suppressed-suspect.md`.

- [ ] **Step 4: Commit**

```bash
git add infra/prometheus/alerts.yml docs/runbooks/dedup-suppressed-suspect.md docs/runbooks/README.md
git commit -m "feat(alertas): DedupSuppressedSuspect + runbook (supressão de tocada real não é mais silenciosa)"
```

---

### Task 4: Travar em teste os números medidos do chooseByCoverage

Lock do comportamento validado no E2E: par subset real decide certo nas DUAS direções.

**Files:**
- Test: `workers/internal/evidence/disambig_coverage_test.go` (adicionar casos)

- [ ] **Step 1: Adicionar o teste**

```go
// Números MEDIDOS no E2E do incidente 2026-07-24 (§4d/§4e): pulso 5.7s ⊂ spot 30.8s.
func TestChooseByCoverage_SubsetPairPulsoMilium(t *testing.T) {
	pulso := CutCoverage{ShortID: 211, DurationSeconds: 6, Coverage: 0.90}
	spotFalse := CutCoverage{ShortID: 213, DurationSeconds: 31, Coverage: 0.183}
	// Pulso tocou sozinho: clipe cobre 0.90 do pulso e 0.183 do spot → pulso vence
	// (0.90 >= 0.183*1.5).
	if got := chooseByCoverage(spotFalse, pulso); got != 211 {
		t.Errorf("pulso standalone: chooseByCoverage = %d, want 211 (pulso)", got)
	}
	// Spot tocou: clipe cobre 0.90 do spot e ~1.0 do pulso (a cauda ESTÁ no clipe).
	// 1.0 < 0.90*1.5 → quase-empate → duração → spot vence (fail-safe correto).
	pulsoTail := CutCoverage{ShortID: 211, DurationSeconds: 6, Coverage: 1.0}
	spotReal := CutCoverage{ShortID: 213, DurationSeconds: 31, Coverage: 0.90}
	if got := chooseByCoverage(spotReal, pulsoTail); got != 213 {
		t.Errorf("spot tocando: chooseByCoverage = %d, want 213 (spot)", got)
	}
}
```

- [ ] **Step 2: Rodar (deve passar de primeira — lock, não bug-fix)**

```bash
cd workers && go test ./internal/evidence -run TestChooseByCoverage_SubsetPairPulsoMilium -v
```
Esperado: PASS. Se FALHAR, PARE — o comportamento validado mudou; reabra o incident report.

- [ ] **Step 3: Commit**

```bash
git add workers/internal/evidence/disambig_coverage_test.go
git commit -m "test(disambig): trava os números medidos do par subset pulso⊂spot (incidente 2026-07-24)"
```

---

### Task 5: Aviso de UI pra material <10s no upload

**Files:**
- Modify: `frontend/src/pages/CampaignWizardSteps/MaterialsStep.jsx` (fluxo `submitUploads`, ~linha 1213-1226, e o render da fila de upload)

- [ ] **Step 1: Guardar o aviso no entry após o fingerprint ficar pronto**

Em `submitUploads`, após o bloco que obtém `fp` (poll do fingerprint, linha ~1217-1226), adicionar:

```jsx
        // Material curto (<10s): fica fora da defesa shared-hash e, se existir
        // spot do MESMO cliente contendo este áudio, as tocadas são disputadas
        // entre os dois (incident-2026-07-24-pulso-milium). Aviso não-bloqueante.
        const durS = Number(fp.duration_seconds)
        if (Number.isFinite(durS) && durS > 0 && durS < 10) {
          setEntryStage(entry.key, 'verifying', {
            shortWarning: `Material curto (${durS.toFixed(1)}s): detecção menos robusta e, se houver um spot deste cliente que contenha este áudio, as veiculações podem ser atribuídas ao spot. Confirme com o suporte antes de faturar por este material.`,
          })
        }
```

(`setEntryStage` já mescla extras no entry — mesmo padrão de `{ materialId: mat.id }` na linha 1214.)

- [ ] **Step 2: Renderizar o aviso na fila**

Localizar onde a fila renderiza `errorMsg` (`grep -n "errorMsg" frontend/src/pages/CampaignWizardSteps/MaterialsStep.jsx`, pegar a ocorrência no JSX da lista de uploads) e, logo abaixo desse bloco, adicionar:

```jsx
                  {entry.shortWarning && (
                    <div className="upload-warning" role="status">
                      ⚠️ {entry.shortWarning}
                    </div>
                  )}
```

Estilo: reutilizar a classe de aviso existente do heads-up de similaridade se houver (grep `headsup` no arquivo/CSS do wizard); senão adicionar em `frontend/src/pages/CampaignWizardPage.css`:

```css
.upload-warning {
  margin-top: 4px;
  font-size: 12.5px;
  color: var(--warning-strong, #b45309);
}
```

- [ ] **Step 3: Validar visualmente** — stack local de pé, subir um arquivo <10s pelo wizard (o pulso serve) e conferir o aviso na fila. NÃO rodar `npm install` (CLAUDE.md §5).

- [ ] **Step 4: Commit**

```bash
git add frontend/src/pages/CampaignWizardSteps/MaterialsStep.jsx frontend/src/pages/CampaignWizardPage.css
git commit -m "feat(wizard): aviso não-bloqueante ao subir material <10s (caso pulso⊂spot)"
```

---

### Task 6: Commitar o simulador Icecast + atualizar o doc de simulação

O `simulate.sh` original (ffmpeg `-listen 1`) é INCOMPATÍVEL com o worker atual: o ffprobe de pré-flight ([ingestor/ffmpeg.go:168](../../../workers/internal/ingestor/ffmpeg.go)) consome a única conexão e o ffmpeg principal cai em porta morta. A variante Icecast (validada em todo o E2E deste incidente) resolve.

**Files:**
- Create: `scripts/radio-sim/simulate-icecast.sh` (conteúdo COMPLETO abaixo)
- Modify: `infra/docker/docker-compose.yml` (service `radio-sim`)
- Modify: `docs/operations/simulacao-radio.md`

- [ ] **Step 1: Criar `scripts/radio-sim/simulate-icecast.sh`** com exatamente este conteúdo (line endings LF):

```sh
#!/bin/sh
# Simulador de radio (variante Icecast) — multi-cliente, sobrevive ao ffprobe
# de pre-flight do worker (2 conexoes). ffmpeg encoda a playlist e empurra pro
# Icecast local; o worker consome http://radio-sim:8000/stream como radio real.
# Vars: QUALITY, PORT, GAP_SECONDS, MASTERS_DIR (iguais ao simulate.sh original).
set -eu

QUALITY="${QUALITY:-fm-standard}"
PORT="${PORT:-8000}"
GAP_SECONDS="${GAP_SECONDS:-15}"
MASTERS_DIR="${MASTERS_DIR:-/masters}"

echo "==> generating $GAP_SECONDS s of background"
ffmpeg -y -f lavfi -i "anoisesrc=color=pink:amplitude=0.08:duration=$GAP_SECONDS" \
       -ar 44100 -ac 2 -c:a pcm_s16le /tmp/gap.wav 2>/dev/null

echo "==> normalizing masters"
mkdir -p /tmp/norm
PLAYLIST=/tmp/playlist.txt
: > "$PLAYLIST"
COUNT=0
for f in "$MASTERS_DIR"/*; do
    [ -f "$f" ] || continue
    case "$f" in
        *.wav|*.mp3|*.m4a|*.aac|*.mpeg|*.WAV|*.MP3|*.M4A|*.AAC|*.MPEG)
            BASE=$(basename "$f")
            OUT="/tmp/norm/${BASE%.*}.wav"
            ffmpeg -y -i "$f" -ar 44100 -ac 2 -c:a pcm_s16le "$OUT" 2>/dev/null \
                || { echo "  [skip] $BASE (decode failed)"; continue; }
            printf "file '%s'\nfile '/tmp/gap.wav'\n" "$OUT" >> "$PLAYLIST"
            COUNT=$((COUNT + 1))
            echo "  [ok] $BASE"
            ;;
    esac
done
[ "$COUNT" -eq 0 ] && { echo "ERROR: no masters in $MASTERS_DIR"; exit 1; }
echo "==> playlist: $COUNT masters + gaps"

case "$QUALITY" in
    fm-hifi)       AFILTER="loudnorm=I=-14:LRA=7:TP=-1"; BITRATE="128k"; SR="44100"; CH="2" ;;
    fm-standard)   AFILTER="acompressor=threshold=-18dB:ratio=3:attack=10:release=100,equalizer=f=8000:t=h:width=2000:g=2,loudnorm=I=-12:LRA=5:TP=-1"; BITRATE="96k"; SR="44100"; CH="2" ;;
    fm-compressed) AFILTER="acompressor=threshold=-24dB:ratio=6:attack=5:release=50,acompressor=threshold=-12dB:ratio=4:attack=2:release=30,alimiter=limit=0.97,loudnorm=I=-9:LRA=3:TP=-0.5"; BITRATE="64k"; SR="44100"; CH="2" ;;
    am)            AFILTER="highpass=f=300,lowpass=f=5000,acompressor=threshold=-24dB:ratio=8:attack=3:release=40,alimiter=limit=0.95,loudnorm=I=-7:LRA=2:TP=-0.5"; BITRATE="48k"; SR="22050"; CH="1" ;;
    bad-stream)    AFILTER="acompressor=threshold=-18dB:ratio=4,loudnorm=I=-12:LRA=4:TP=-1"; BITRATE="32k"; SR="22050"; CH="1" ;;
    *) echo "ERROR: quality desconhecida: $QUALITY"; exit 1 ;;
esac

echo "==> preset=$QUALITY bitrate=$BITRATE sr=$SR ch=$CH"

mkdir -p /tmp/ice/web /tmp/ice/admin /tmp/ice/log
cat > /tmp/ice/icecast.xml <<EOF
<icecast>
  <location>sim</location>
  <admin>sim@localhost</admin>
  <limits><clients>16</clients><sources>2</sources></limits>
  <authentication>
    <source-password>simpass</source-password>
    <admin-user>admin</admin-user>
    <admin-password>simpass</admin-password>
  </authentication>
  <hostname>radio-sim</hostname>
  <listen-socket><port>$PORT</port></listen-socket>
  <fileserve>0</fileserve>
  <paths>
    <basedir>/tmp/ice</basedir>
    <logdir>/tmp/ice/log</logdir>
    <webroot>/tmp/ice/web</webroot>
    <adminroot>/tmp/ice/admin</adminroot>
  </paths>
  <logging>
    <accesslog>access.log</accesslog>
    <errorlog>error.log</errorlog>
    <loglevel>3</loglevel>
  </logging>
  <security>
    <chroot>0</chroot>
    <changeowner><user>icecast</user><group>icecast</group></changeowner>
  </security>
</icecast>
EOF
chown -R icecast:icecast /tmp/ice 2>/dev/null || true
icecast -c /tmp/ice/icecast.xml &
sleep 2
echo "==> icecast up; servindo em http://0.0.0.0:$PORT/stream"

while true; do
    ffmpeg -hide_banner -loglevel warning \
        -re -stream_loop -1 \
        -f concat -safe 0 -i "$PLAYLIST" \
        -af "$AFILTER" \
        -c:a aac -b:a "$BITRATE" -ar "$SR" -ac "$CH" \
        -f adts -content_type audio/aac \
        "icecast://source:simpass@127.0.0.1:$PORT/stream" || true
    echo "==> source caiu, reiniciando push..."
    sleep 1
done
```

- [ ] **Step 2: Trocar o service `radio-sim` do compose pra variante Icecast**

Em `infra/docker/docker-compose.yml`, no service `radio-sim` (linha ~304): trocar o `command` de `"apk add --no-cache ffmpeg >/dev/null && exec /sim/simulate.sh"` para `"apk add --no-cache ffmpeg icecast >/dev/null && exec /sim/simulate-icecast.sh"`, e a env `MASTERS_DIR: /masters` para `MASTERS_DIR: ${SIM_MASTERS_DIR:-/masters}` (permite apontar pra subpasta e controlar a playlist).

- [ ] **Step 3: Testar** — `cd infra/docker && docker compose --profile sim up -d radio-sim`, esperar `docker logs` mostrar `icecast up`, e de dentro do api: `docker exec docker-api-1 sh -c "ffprobe -v error -show_entries stream=codec_name -of compact http://radio-sim:8000/stream"` → deve responder `codec_name=aac` em <5s (o probe de pré-flight funciona).

- [ ] **Step 4: Atualizar `docs/operations/simulacao-radio.md`** — adicionar no topo, abaixo do header YAML:

```markdown
> **2026-07-30 — variante Icecast é a oficial.** O worker atual faz um ffprobe de
> pré-flight ANTES do ffmpeg principal (2 conexões em sequência); o servidor
> `ffmpeg -listen 1` original atende 1 conexão por vez e entra em loop de falha.
> O compose agora usa `simulate-icecast.sh` (Icecast local multi-cliente — o
> mesmo protocolo das rádios reais). Use `SIM_MASTERS_DIR` pra tocar só uma
> subpasta de `/masters` (controle de playlist). Procedimento E2E completo do
> caso subset curto⊂longo: incident-2026-07-24-pulso-milium §4/§4d/§4e.
```

E atualizar o header: `ultima-verificacao: 2026-07-30`, `codigo-relacionado` + `scripts/radio-sim/simulate-icecast.sh`.

- [ ] **Step 5: Commit**

```bash
git add scripts/radio-sim/simulate-icecast.sh infra/docker/docker-compose.yml docs/operations/simulacao-radio.md
git commit -m "feat(radio-sim): variante Icecast (compatível com o probe de pré-flight do worker)"
```

---

### Task 7: Runbook de rollout da flag em prod

**Files:**
- Create: `docs/operations/disambig-by-coverage-rollout.md`

- [ ] **Step 1: Criar o doc com este conteúdo:**

```markdown
---
status: planejado
ultima-verificacao: 2026-07-30
codigo-relacionado:
  - workers/internal/evidence/service.go
  - workers/internal/evidence/cofire_guard.go
  - infra/docker/docker-compose.yml
---

# Rollout: DISAMBIG_BY_COVERAGE=true em prod

Contexto: [incident-2026-07-24-pulso-milium-nao-detectado.md](../incidents/incident-2026-07-24-pulso-milium-nao-detectado.md)
(§4d/§4e = validação E2E). A flag liga a arbitragem §18.2.2-v2 por cobertura de evidência
(pass-path + co-fire guard + reject-path). Em 30/06 ela rodou SEM o co-fire guard e causou
duplicatas — o guard (merge 02/07) corrige exatamente aquilo, e o E2E re-exercitou o cenário.

## Pré-condições
- [ ] Deploy atual do api inclui o co-fire guard (commit `4b2e112`+ de 02/07 — conferir `git log`)
- [ ] Métrica `suppressed_suspect` + alerta deployados (Tasks 2-3 do plano)
- [ ] Janela de observação de 48h combinada (dias úteis)

## Ligar (VM — executa o Dereck)
```bash
cd ~/radiocheck && git pull
# .env: adicionar/editar
#   DISAMBIG_BY_COVERAGE=true
DC="docker compose -f infra/docker/docker-compose.yml -f infra/docker/docker-compose.override.yml --env-file infra/docker/.env"
$DC build api && $DC up -d --force-recreate --no-deps api    # --no-deps OBRIGATÓRIO (CLAUDE.md 4.1)
docker logs docker-api-1 2>&1 | grep "coverage-based disambiguation ENABLED"   # tem que aparecer
```

## Sombra 48h — o que olhar (1x/dia)
```bash
curl -s localhost:8080/metrics | grep radiocheck_match_disambiguation_total
# esperado: reattributed_by_coverage / duplicate_cofire_retracted / restored_on_reject > 0
# em dias com tocada standalone de material curto; suppressed_suspect estável ou caindo.
```
```sql
-- rows re-arbitradas nas últimas 24h (deve conter os resgates do pulso):
SELECT to_char(d.detected_at AT TIME ZONE 'America/Sao_Paulo','DD/MM HH24:MI') AS quando,
       s.name, m.short_id, m.title, d.evidence_status,
       (d.retracted_at IS NOT NULL) AS retratada, d.audit_coverage
FROM detections d JOIN stations s ON s.id=d.station_id
LEFT JOIN materials m ON m.id=d.commercial_id
WHERE d.detected_at > now() - interval '24 hours'
  AND m.short_id IN (<short_id do PULSO>, <short_id do SPOT DEMAIS RADIOS>)
ORDER BY d.detected_at DESC;
```
Critérios de aceite da sombra: (1) pulso passa a ter rows contando nas 2 emissoras
(meninablu, loopert/303) nos dias com tocada; (2) contagem do SPOT não cai nos dias/emissoras
em que ele tocou de verdade (comparar com semana anterior); (3) zero duplicata
pulso+spot no mesmo minuto/emissora contando juntas.

## Rollback (instantâneo)
```bash
# .env: DISAMBIG_BY_COVERAGE=false
$DC up -d --force-recreate --no-deps api
```

## Depois do aceite
- Mover este doc pra `status: implementado` com a data.
- Avaliar o backfill histórico (scripts/sql/diagnose-pulso-subset-rows.sql) com o dono.
```

- [ ] **Step 2: Commit**

```bash
git add docs/operations/disambig-by-coverage-rollout.md
git commit -m "docs(ops): runbook de rollout do DISAMBIG_BY_COVERAGE com sombra e rollback"
```

---

### Task 8: SQL de diagnóstico do histórico (reparo é decisão do dono)

**Files:**
- Create: `scripts/sql/diagnose-pulso-subset-rows.sql`

- [ ] **Step 1: Criar o arquivo:**

```sql
-- diagnose-pulso-subset-rows.sql — READ-ONLY.
-- Candidatas a "tocada do pulso contada como spot" no histórico: rows do spot
-- com audit_coverage baixo (assinatura de false-confirm, medido 0.183 no E2E)
-- + supressões do pulso registradas. Rodar na VM via:
--   $DC exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB"' < este_arquivo
-- Reparo (reatribuir/retratar/manual) é decisão do dono a partir deste relatório.

\echo '=== 1. Rows do catálogo Milium com audit_coverage < 0.30 (últimos 30 dias) ==='
SELECT to_char(d.detected_at AT TIME ZONE 'America/Sao_Paulo','DD/MM HH24:MI:SS') AS quando_brt,
       s.name AS emissora, m.short_id, left(m.title,40) AS material,
       d.audit_coverage, d.evidence_status, (d.retracted_at IS NOT NULL) AS retratada
FROM detections d
JOIN stations s ON s.id=d.station_id
JOIN materials m ON m.id=d.commercial_id
JOIN clients c ON c.id=m.client_id
WHERE c.name ILIKE '%MILIUM%'
  AND d.audit_coverage IS NOT NULL AND d.audit_coverage < 0.30
  AND d.detected_at > now() - interval '30 days'
ORDER BY d.detected_at;

\echo '=== 2. Supressões do pulso (tocadas reais mortas sem row) ==='
SELECT to_char(ds.detected_at AT TIME ZONE 'America/Sao_Paulo','DD/MM HH24:MI:SS') AS quando_brt,
       s.name AS emissora, ds.suppressed_short_id, ds.kept_short_id,
       round(ds.suppressed_confidence::numeric,3) AS conf_supr,
       round(ds.kept_confidence::numeric,3) AS conf_kept
FROM dedup_suppressions ds JOIN stations s ON s.id=ds.station_id
WHERE ds.detected_at > now() - interval '30 days'
  AND ds.suppressed_short_id IN (SELECT short_id FROM materials m
                                 JOIN clients c ON c.id=m.client_id
                                 WHERE c.name ILIKE '%MILIUM%' AND m.duration_seconds < 12)
ORDER BY ds.detected_at;

\echo '=== 3. Cruzamento: dias/emissoras com item-1 E item-2 = tocadas recuperáveis por reatribuição ==='
SELECT date(d.detected_at AT TIME ZONE 'America/Sao_Paulo') AS dia, s.name AS emissora,
       count(*) FILTER (WHERE d.audit_coverage < 0.30) AS rows_spot_suspeitas
FROM detections d
JOIN stations s ON s.id=d.station_id
JOIN materials m ON m.id=d.commercial_id
JOIN clients c ON c.id=m.client_id
WHERE c.name ILIKE '%MILIUM%' AND d.audit_coverage < 0.30
  AND d.detected_at > now() - interval '30 days'
GROUP BY 1, 2 ORDER BY 1, 2;
```

- [ ] **Step 2: Commit**

```bash
git add scripts/sql/diagnose-pulso-subset-rows.sql
git commit -m "chore(sql): diagnóstico read-only do histórico pulso-contado-como-spot"
```

---

### Task 9: Atualizar docs de arquitetura + follow-up F-125

**Files:**
- Modify: `docs/architecture/version-disambiguation.md`
- Modify: `docs/roadmap/follow-ups-fase2.md`

- [ ] **Step 1:** Em `version-disambiguation.md`, adicionar seção ao final (e `ultima-verificacao: 2026-07-30` no header):

```markdown
## Caso subset <10s (pulso ⊂ spot) — 2026-07-30

Material <10s é PULADO pelo shared-hash (dos dois lados, `MinShareableDurationSeconds`), então
quando ele é subset de um spot do mesmo cliente os dois confirmam juntos (co-fire) e a regra
v1 por duração mata o curto — inclusive quando o curto é a tocada REAL (false-confirm do
longo a conf ~0.2). Demonstrado e validado no
[incident-2026-07-24-pulso-milium-nao-detectado.md](../incidents/incident-2026-07-24-pulso-milium-nao-detectado.md):
- `DISAMBIG_CONFIDENCE_AWARE` NÃO resolve (conf da state machine é estruturalmente enviesada
  — curto ~1.0 pelo piso de 32 frames, longo ~0.2 por confirmar na 2ª janela; §4d).
- A resolução é a v2 (`DISAMBIG_BY_COVERAGE`): arbitragem pós-audit por cobertura do clipe +
  co-fire guard. Rollout: [disambig-by-coverage-rollout.md](../operations/disambig-by-coverage-rollout.md).
- Limite residual: se o false-confirm do longo nem gerar row (não observado no E2E), a
  supressão v1 fica sem resgate → F-125.
```

- [ ] **Step 2:** Em `follow-ups-fase2.md`, adicionar (seguindo o formato/numeração do arquivo):

```markdown
### F-125 — Dedup v1 não pode suprimir par-containment sem registro recuperável
Origem: incident-2026-07-24-pulso-milium. A v2 resgata a tocada do curto ATRAVÉS da row do
longo; se o longo não deixar row (não confirmou ou NATS caiu), a supressão v1 é perda seca.
Fix definitivo: (a) shared-scan registra a relação de containment mesmo pra <10s (hoje pula
sem registrar), (b) supervisor publica-provisório em conflito com containment conhecido em
vez de suprimir. Prioridade: baixa enquanto a sombra do rollout não mostrar caso real.
```

- [ ] **Step 3: Commit + push da branch**

```bash
git add docs/architecture/version-disambiguation.md docs/roadmap/follow-ups-fase2.md
git commit -m "docs: caso subset<10s na arquitetura de desambiguação + follow-up F-125"
git push -u origin fix/subset-dedup-pulso
```

---

## Gate final antes do merge (CLAUDE.md §6)

- [ ] `cd workers && CGO_ENABLED=0 GOOS=linux go build ./...` — limpo
- [ ] `cd workers && go test ./...` — falhas só nas flaky conhecidas (§6.6) em pacotes não tocados
- [ ] `git show master:frontend/package-lock.json | grep -c emnapi` == `grep -c emnapi frontend/package-lock.json` (lockfile NÃO foi tocado — este plano não mexe em deps)
- [ ] Nenhuma migration nova (nada de shadow test necessário)
- [ ] E2E local (opcional, recomendado): repetir o procedimento do incident report §4d/§4e com a branch buildada — mesmos desfechos

## Self-review (feito na escrita, 2026-07-30)

- Cobertura da spec: §3.1 rollout→Task 7; §3.2 alerta→Tasks 2-3; §3.3 UI→Task 5; §3.4
  histórico→Task 8; §3.5 higiene→Tasks 4/6/9. Flag confidence-aware permanece OFF (spec §4).
- Sem placeholders; tipos/nomes conferidos contra o código lido em 30/07 (disambiguation.go,
  metrics.go:224-227, disambig_coverage.go, MaterialsStep.jsx:1180-1249, compose:304-318).
- Dependências entre tasks: 3 depende da métrica da 2; demais são independentes.
