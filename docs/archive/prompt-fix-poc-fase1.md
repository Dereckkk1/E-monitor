---
status: legado
ultima-verificacao: 2026-05-15
codigo-relacionado:
  # tipo: prompt operacional de uma sessao concluida (Fase 1 PoC)
  # nota: todos os 10 fix-groups confirmados como mergeados; mantido como artefato historico
---

# Prompt de Execução — Radiocheck PoC: Correções Pós-Implementação

Este arquivo contém o prompt para uma sessão nova do Claude Code corrigir os bugs
identificados via code review do PoC da Fase 1.

**Como usar:** abra uma sessão nova do Claude Code no diretório
`c:\Users\marke\Desktop\Programas\Radiocheck` e cole o bloco abaixo como
primeira mensagem.

---

## Prompt para colar na nova sessão

```
Você vai corrigir bugs e lacunas identificados no Radiocheck PoC (Fase 1), que
já foi implementado (27 tasks concluídas) mas tem problemas críticos encontrados
em review.

Você é o orquestrador. Cada fix-group vai a um subagent, com spec review e code
quality review antes de marcar como concluído — igual ao workflow de
superpowers:subagent-driven-development.

═══════════════════════════════════════════════════════════════
ARQUIVOS DE CONTEXTO (leia nesta ordem, antes de qualquer ação)
═══════════════════════════════════════════════════════════════

1. CLAUDE.md
   → regras críticas e índice do plano arquitetural

2. docs/superpowers/specs/2026-05-05-radiocheck-poc-design.md
   → spec funcional da Fase 1 (fonte de verdade para o spec reviewer)

3. docs/superpowers/plans/2026-05-05-radiocheck-poc-implementation.md
   → plano original com o código de referência de cada task

4. plano_implementacao.md seções relevantes (use o índice do CLAUDE.md):
   § 7.3 Algoritmo de Fingerprint   (linha ~510) — constellation map, hashes
   § 9   Algoritmo de Matching      (linha ~751) — histograma, threshold
   § 9.5 State Machine              (linha ~891) — Idle/Candidate/Cooldown
   § 9.6 Cobertura Temporal         (linha ~928) — CoverageWindow

═══════════════════════════════════════════════════════════════
WORKTREE EXISTENTE
═══════════════════════════════════════════════════════════════

O código fica em:
  .worktrees/poc-fase1/

Branch: poc-fase1

NÃO crie worktree novo — já existe. Confirme com:
  git worktree list

Antes de começar verifique também o estado atual dos arquivos. Alguns fixes já
podem estar aplicados (mas não commitados). Se o fix já estiver correto, pule
para o próximo e marque como concluído sem commitar.

═══════════════════════════════════════════════════════════════
SKILL OBRIGATÓRIA
═══════════════════════════════════════════════════════════════

Invoque antes de começar (Skill tool):

  superpowers:subagent-driven-development

Ela define o workflow: um implementer subagent por fix-group, seguido de um
spec-reviewer subagent e um code-quality-reviewer subagent. Só mark como
completo quando ambos aprovarem.

═══════════════════════════════════════════════════════════════
OS 10 FIX-GROUPS (em ordem de prioridade)
═══════════════════════════════════════════════════════════════

Crie um TodoWrite com os 10 fix-groups logo após ler este prompt.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
FIX-GROUP A — router.go: rotas PUT ausentes (CRÍTICO)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Arquivo: workers/internal/api/router.go

PROBLEMA: os handlers CampaignsHandler.Start e CampaignsHandler.Pause existem
em campaigns.go mas as rotas PUT nunca foram registradas no router. O frontend
chama PUT /campaigns/{id}/start e /pause e recebe 404.

FIX: dentro do bloco `r.Route("/campaigns", ...)`, após `r.Get("/{id}", ...)`:

  r.Put("/{id}/start", d.Campaigns.Start)
  r.Put("/{id}/pause", d.Campaigns.Pause)

VERIFICAÇÃO:
  cd .worktrees/poc-fase1/workers && go build ./...
  (deve compilar sem erros)

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
FIX-GROUP B — hashes.go + peaks.go: constantes erradas (ALTO)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Arquivos:
  workers/pkg/audio/hashes.go
  workers/pkg/audio/peaks.go

PROBLEMA 1 — hashes.go:
  FanOut=15 (deve ser 5) e MaxDelta=200 (deve ser TargetZoneTMax=16).
  Isso gera hashes 3× mais densos que o esperado e pares com deltas grandes
  demais, poluindo o índice e aumentando falsos positivos.
  Também faltam TargetZoneTMin e TargetZoneF (filtros de zona-alvo ausentes).

FIX hashes.go — substituir as constantes e atualizar GenerateHashes:

```go
// Constellation-map pairing constants (§7.3 do plano).
const (
    FanOut         = 5  // max target peaks por anchor
    TargetZoneTMin = 1  // delta mínimo em frames
    TargetZoneTMax = 16 // delta máximo em frames
    TargetZoneF    = 50 // distância máxima em bins de frequência
)
```

Em GenerateHashes, dentro do loop interno:
  - substituir `if dt <= 0` por `if dt < TargetZoneTMin`
  - substituir `if dt > MaxDelta` por `if dt > TargetZoneTMax`
  - ADICIONAR após o check de dt, antes de calcular o hash:
    ```go
    df := targetBin - anchorBin
    if df < 0 { df = -df }
    if df > TargetZoneF { continue }
    ```

PROBLEMA 2 — peaks.go:
  neighborFrames=10, neighborBins=5. O spec (§7.3) define vizinhança de ±15
  frames e ±15 bins (janela 31×31). Com janela menor os picos ficam muito
  densos, degradando a qualidade do fingerprint.

FIX peaks.go:
  neighborFrames = 15
  neighborBins   = 15
  (atualizar o comentário inline também)

VERIFICAÇÃO:
  cd .worktrees/poc-fase1/workers && go test ./pkg/audio/...

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
FIX-GROUP C — store.go + loader.go: VariantID/RateID e filtro ativo (ALTO)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Arquivos:
  workers/internal/index/store.go
  workers/internal/index/loader.go

PROBLEMA 1 — store.go:
  Entry tem apenas CommercialShortID e TimeFrame. A tabela fingerprint_hashes
  tem colunas variant_id e rate_id (broadcast simulation: original + 3 variantes
  de compressão × 3 rates de tempo). Sem essas colunas no Entry o índice trata
  todos os hashes como vindos do mesmo original, anulando a robustez a degradação
  de stream.

FIX store.go:
```go
type Entry struct {
    CommercialShortID int32
    VariantID         uint8 // 0=original, 1=light, 2=medium, 3=heavy
    RateID            uint8 // variante de time-stretch
    TimeFrame         int32
}
```

PROBLEMA 2 — loader.go LoadAll():
  a) SQL não seleciona variant_id nem rate_id
  b) SQL não filtra por campanhas ativas — carrega fingerprints de comerciais
     de campanhas paradas, desperdiçando memória e aumentando falsos positivos

FIX loader.go — LoadAll():

```sql
SELECT fh.hash_value, fh.time_frame, fh.variant_id, fh.rate_id, c.short_id
FROM fingerprint_hashes fh
JOIN commercials c  ON c.id  = fh.commercial_id
JOIN campaigns   ca ON ca.id = c.campaign_id
WHERE c.fingerprint_status = 'ready'
  AND ca.status = 'active'
```

Scan atualizado (dentro do for rows.Next()):
```go
var hashValue uint32
var timeFrame  int32
var variantID  int16
var rateID     int16
var shortID    int32
if err := rows.Scan(&hashValue, &timeFrame, &variantID, &rateID, &shortID); err != nil { ... }
newIndex[hashValue] = append(newIndex[hashValue], Entry{
    CommercialShortID: shortID,
    VariantID:         uint8(variantID),
    RateID:            uint8(rateID),
    TimeFrame:         timeFrame,
})
```

FIX loader.go — Subscribe() (reload incremental por comercial):

a) Lookup do short_id: adicionar filtro de campanha ativa:
```sql
SELECT c.short_id FROM commercials c
JOIN campaigns ca ON ca.id = c.campaign_id
WHERE c.id = $1
  AND c.fingerprint_status = 'ready'
  AND ca.status = 'active'
```

b) Fetch dos hashes: adicionar as colunas:
```sql
SELECT hash_value, time_frame, variant_id, rate_id
FROM fingerprint_hashes
WHERE commercial_id = $1
```

c) Scan e Entry construction: igual ao LoadAll() acima.
   (o tipo local `hashEntry` precisa ganhar variantID e rateID)

VERIFICAÇÃO:
  cd .worktrees/poc-fase1/workers && go build ./...

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
FIX-GROUP D — engine.go: matching sem variant/rate e sem DeltaBin (ALTO)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Arquivo: workers/internal/match/engine.go

PROBLEMA:
  1. O histograma é `map[int32]map[int]int` (commercial → delta → count).
     Ignora VariantID e RateID: pares de hashes de variantes diferentes se
     somam ao mesmo delta, distorcendo o score.
  2. Delta não é quantizado em bins — duas janelas com offset de 1 frame
     nunca acumulam score juntas. O spec define DeltaBinSize=2 (~256ms).
  3. Sem filtro de MinWindowCoverage: um único hash de match isolado pode
     atingir o threshold mínimo em janelas muito esparsas.

FIX — reescrever o núcleo de MatchWindow:

```go
// Constantes do engine (§9.3 do plano).
const (
    DeltaBinSize      = 2   // quantiza delta em bins de 2 frames (~256ms)
    MinWindowCoverage = 0.4 // fração mínima de hashes que precisa ter match
)

// histKey agrupa por (comercial, variante, rate, bin de delta).
type histKey struct {
    commercialID int32
    variantID    uint8
    rateID       uint8
    deltaBin     int
}
```

MatchResult ganha dois campos novos:
```go
type MatchResult struct {
    CommercialShortID int32
    VariantID         uint8
    RateID            uint8
    Score             int
    OffsetFrames      int
}
```

Dentro de MatchWindow, após gerar hashes:

```go
totalHashes := len(hashes)
if totalHashes == 0 {
    return nil
}

histogram := make(map[histKey]int)
for _, h := range hashes {
    for _, entry := range store.Lookup(h.Value) {
        delta := h.TimeFrame - int(entry.TimeFrame)
        k := histKey{
            commercialID: entry.CommercialShortID,
            variantID:    entry.VariantID,
            rateID:       entry.RateID,
            deltaBin:     delta / DeltaBinSize,
        }
        histogram[k]++
    }
}

// Para cada comercial, encontrar a variante com maior score.
type bestEntry struct {
    count     int
    variantID uint8
    rateID    uint8
    deltaBin  int
}
best := make(map[int32]bestEntry)
for k, count := range histogram {
    if cur, ok := best[k.commercialID]; !ok || count > cur.count {
        best[k.commercialID] = bestEntry{count, k.variantID, k.rateID, k.deltaBin}
    }
}

minHits := int(float64(totalHashes) * MinWindowCoverage)

var results []MatchResult
for id, b := range best {
    if b.count >= threshold && b.count >= minHits {
        results = append(results, MatchResult{
            CommercialShortID: id,
            VariantID:         b.variantID,
            RateID:            b.rateID,
            Score:             b.count,
            OffsetFrames:      b.deltaBin * DeltaBinSize,
        })
    }
}
return results
```

ATENÇÃO — engine_test.go:
  buildIndexFromHashes cria Entry{} sem VariantID/RateID. Após o fix de store.go,
  esses campos terão zero-value (uint8=0) — correto, não precisa mudar o test.
  MAS o TestMatchWindow_SelfMatch usa threshold=5.
  Com FanOut=5 e FiltroF=50 a densidade de hashes cai. Rode o test para confirmar
  que o self-match ainda passa. Se o score mínimo garantido for muito próximo de 5,
  ajuste o threshold no test para 3.

VERIFICAÇÃO:
  cd .worktrees/poc-fase1/workers && go test ./internal/match/...

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
FIX-GROUP E — statemachine.go + worker.go: sem estado Cooldown (ALTO)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Arquivos:
  workers/internal/match/statemachine.go
  workers/internal/ingestor/worker.go
  workers/internal/match/statemachine_test.go

PROBLEMA:
  Após confirmar uma detecção, a state machine reseta imediatamente para Idle.
  O mesmo comercial pode ser detectado múltiplas vezes dentro dos 30s seguintes,
  gerando duplicatas no relatório. O spec (§9.5) exige StateCooldown com duração
  = duração do comercial + 5s.

FIX — statemachine.go:

1. Adicionar estado:
```go
const (
    StateIdle      State = iota
    StateDetecting
    StateCooldown
)
```

2. StateMachine struct ganha dois campos:
```go
cooldownDuration time.Duration
cooldownUntil    time.Time
```

3. NewStateMachine ganha parâmetro `cooldownDuration time.Duration` (após confirmTimeout):
```go
func NewStateMachine(
    stationID string,
    commercialShortID int32,
    totalFrames int,
    minScore int,
    minCoverage float64,
    confirmTimeout time.Duration,
    cooldownDuration time.Duration,
    log *zap.Logger,
) *StateMachine {
    return &StateMachine{
        ...
        cooldownDuration: cooldownDuration,
    }
}
```

4. Em Update(), case StateDetecting — após emitir ConfirmedDetection, trocar
   o reset para:
```go
sm.coverage.Reset()
sm.state = StateCooldown
sm.cooldownUntil = now.Add(sm.cooldownDuration)
return detection
```
   (remover `sm.state = StateIdle` que estava aí)

5. Tick() — adicionar case para StateCooldown:
```go
case StateCooldown:
    if now.After(sm.cooldownUntil) {
        sm.log.Info("cooldown expired, back to idle",
            zap.String("stationID", sm.stationID),
            zap.Int32("commercialShortID", sm.commercialShortID),
        )
        sm.state = StateIdle
    }
```

FIX — worker.go (runPCMReader ou loop de criação de machines):

Antes de chamar NewStateMachine, calcular cooldownDuration a partir dos frames:
```go
// hopSize=2048, sampleRate=16000 — mesmo valor do supervisor.go
frameDur := time.Duration(float64(time.Second) * float64(totalFrames) * 2048 / 16000)
cooldown := frameDur + 5*time.Second
machines[id] = match.NewStateMachine(
    stationIDStr, id, totalFrames,
    w.cfg.MatchThreshold, w.cfg.MinCoverage,
    w.cfg.ConfirmTimeout, cooldown,
    w.log,
)
```
(o loop já tem `totalFrames := w.cfg.CommercialFrames[id]`)

FIX — statemachine_test.go:

a) newTestStateMachine precisa do parâmetro cooldownDuration:
```go
func newTestStateMachine(totalFrames, minScore int, minCoverage float64) *StateMachine {
    log, _ := zap.NewDevelopment()
    return NewStateMachine(
        "station-test", int32(42), totalFrames,
        minScore, minCoverage,
        5*time.Second,   // confirmTimeout
        100*time.Millisecond, // cooldownDuration curto para testes
        log,
    )
}
```

b) TestStateMachine_ConfirmsAfterCoverage: após receber confirmed, estado deve
   ser StateCooldown (não StateIdle). Adicionar após o require.NotNil:
```go
assert.Equal(t, StateCooldown, sm.State(), "após confirmação deve entrar em Cooldown")

// Avançar além do cooldown (100ms).
sm.Tick(time.Now().Add(200 * time.Millisecond))
assert.Equal(t, StateIdle, sm.State(), "após cooldown expirar deve voltar a Idle")
```
   E remover/substituir a assertion antiga que verificava StateIdle imediatamente.

c) TestStateMachine_TimeoutResetsToIdle: neste test o timeout acontece em
   StateDetecting (sem confirmar). O resultado correto ainda é StateIdle — sem
   mudança na lógica, mas o test deve passar sem tocar porque Tick() em
   StateDetecting → StateIdle permanece igual.

VERIFICAÇÃO:
  cd .worktrees/poc-fase1/workers && go test ./internal/match/... ./internal/ingestor/...

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
FIX-GROUP F — supervisor: RestoreActive ao reiniciar (MÉDIO)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Arquivos:
  workers/internal/supervisor/supervisor.go
  workers/cmd/api/main.go

PROBLEMA:
  Se o processo reiniciar (crash, deploy), campanhas ativas no banco ficam
  com workers parados. O spec (§8.7 / Apêndice C) exige RestoreActive() logo
  após a inicialização.

FIX — supervisor.go — adicionar método:
```go
// RestoreActive re-lança workers para todas as campanhas com status 'active'.
// Chamado uma vez no startup após o loader.LoadAll().
func (s *Supervisor) RestoreActive(ctx context.Context) error {
    rows, err := s.db.Query(ctx, `
        SELECT id FROM campaigns WHERE status = 'active'
    `)
    if err != nil {
        return fmt.Errorf("supervisor.RestoreActive: query: %w", err)
    }
    defer rows.Close()

    var ids []uuid.UUID
    for rows.Next() {
        var id uuid.UUID
        if err := rows.Scan(&id); err != nil {
            return fmt.Errorf("supervisor.RestoreActive: scan: %w", err)
        }
        ids = append(ids, id)
    }
    if err := rows.Err(); err != nil {
        return fmt.Errorf("supervisor.RestoreActive: iterate: %w", err)
    }

    for _, id := range ids {
        if err := s.Start(id); err != nil {
            s.log.Error("supervisor.RestoreActive: failed to start campaign",
                zap.String("campaign_id", id.String()),
                zap.Error(err),
            )
        }
    }

    s.log.Info("supervisor.RestoreActive: done", zap.Int("campaigns", len(ids)))
    return nil
}
```

FIX — main.go — chamar RestoreActive() após loader.LoadAll(), antes de iniciar o servidor:
```go
// Restaurar workers de campanhas ativas (caso o processo tenha reiniciado).
if err := sup.RestoreActive(ctx); err != nil {
    logger.Warn("supervisor restore active failed", zap.Error(err))
}
```
Adicionar logo após a linha `sup := supervisor.New(...)`.

VERIFICAÇÃO:
  cd .worktrees/poc-fase1/workers && go build ./cmd/api/...

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
FIX-GROUP G — CampaignsPage.jsx + hooks.js: upload de comercial ausente (CRÍTICO)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Arquivos:
  frontend/src/api/hooks.js
  frontend/src/pages/CampaignsPage.jsx

PROBLEMA:
  O spec (Task 27 do plano) descreve um formulário de upload de comercial
  dentro da página de campanhas. Sem ele é impossível cadastrar materiais —
  fluxo principal do produto quebrado.

  O handler de upload já existe:
    POST /v1/internal/commercials
    Content-Type: multipart/form-data
    campos: campaign_id, title, cut_label (opcional), file (binário)
  
  O handler responde com o comercial criado (JSON) e dispara fingerprint via NATS.

FIX — hooks.js — adicionar hook:
```js
// Upload de comercial (multipart/form-data)
export function useUploadCommercial() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (formData) =>
      api.post('/commercials', formData, {
        headers: { 'Content-Type': 'multipart/form-data' },
      }).then(r => r.data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['campaigns'] }),
  })
}
```

FIX — CampaignsPage.jsx — adicionar estado e UI de upload:

1. Importar o hook:
```js
import { ..., useUploadCommercial } from '../api/hooks'
```

2. Adicionar estado no componente:
```js
const uploadCommercial = useUploadCommercial()
const [uploadFor, setUploadFor] = useState(null)   // campaign ID ou null
const [uploadTitle, setUploadTitle] = useState('')
const [uploadCut, setUploadCut] = useState('')
const [uploadFile, setUploadFile] = useState(null)
```

3. Adicionar handler:
```js
function handleUpload(e) {
  e.preventDefault()
  const fd = new FormData()
  fd.append('campaign_id', uploadFor)
  fd.append('title', uploadTitle)
  if (uploadCut) fd.append('cut_label', uploadCut)
  fd.append('audio', uploadFile)  // handler espera FormFile("audio")
  uploadCommercial.mutate(fd, {
    onSuccess: () => {
      setUploadFor(null)
      setUploadTitle('')
      setUploadCut('')
      setUploadFile(null)
    },
  })
}
```

4. Na tabela de campanhas, adicionar botão "Upload" na coluna Ações:
```jsx
<button onClick={() => setUploadFor(c.id)}>Upload</button>
```

5. Abaixo da tabela (ou como modal inline), renderizar o formulário quando
   uploadFor !== null:
```jsx
{uploadFor && (
  <div style={{ marginTop: 16, padding: 12, border: '1px solid #ccc', maxWidth: 400 }}>
    <h3>Upload de Comercial</h3>
    <p style={{ fontSize: 12, color: '#666' }}>
      Campanha: {campaigns.find(c => c.id === uploadFor)?.name}
    </p>
    <form onSubmit={handleUpload} style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      <input
        placeholder="Título do comercial"
        value={uploadTitle}
        onChange={e => setUploadTitle(e.target.value)}
        required
      />
      <input
        placeholder="Cut label (ex: versão 30s)"
        value={uploadCut}
        onChange={e => setUploadCut(e.target.value)}
      />
      <input
        type="file"
        accept="audio/*"
        onChange={e => setUploadFile(e.target.files[0])}
        required
      />
      <div style={{ display: 'flex', gap: 8 }}>
        <button type="submit" disabled={uploadCommercial.isPending}>
          {uploadCommercial.isPending ? 'Enviando...' : 'Enviar'}
        </button>
        <button type="button" onClick={() => setUploadFor(null)}>Cancelar</button>
      </div>
      {uploadCommercial.isError && (
        <p style={{ color: 'red' }}>Erro no upload. Tente novamente.</p>
      )}
    </form>
  </div>
)}
```

VERIFICAÇÃO:
  cd .worktrees/poc-fase1/frontend && npm run build
  (deve compilar sem erros; teste manual: subir docker-compose e usar a UI)

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
FIX-GROUP H — DetectionsPage.jsx: player de evidência (MÉDIO)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Arquivo: frontend/src/pages/DetectionsPage.jsx

PROBLEMA:
  A evidência abre em nova aba (`window.open`). Isso quebra quando o backend
  exige autenticação ou quando o usuário está numa rede sem exposição direta
  da porta. O spec pede player inline.

FIX — remover a função playEvidence e o botão, substituir pela coluna de
evidência com um player inline:

```jsx
<td>
  {d.evidence_status === 'available' ? (
    <audio
      controls
      preload="none"
      src={`/v1/internal/detections/${d.id}/evidence`}
      style={{ height: 28 }}
    />
  ) : (
    <span style={{ color: '#aaa' }}>{d.evidence_status}</span>
  )}
</td>
```

Remover a função `playEvidence` e o import de `useState` se não for mais usado
por outros campos (verificar antes de remover).

VERIFICAÇÃO:
  cd .worktrees/poc-fase1/frontend && npm run build

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
FIX-GROUP I — Verificação de build e testes completos (SEMPRE ÚLTIMO)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Após todos os fix-groups anteriores:

1. Build Go completo:
   cd .worktrees/poc-fase1/workers && go build ./...

2. Todos os testes Go:
   cd .worktrees/poc-fase1/workers && go test ./...

3. Build frontend:
   cd .worktrees/poc-fase1/frontend && npm run build

4. Testes de lint (se configurado):
   cd .worktrees/poc-fase1/workers && go vet ./...

Se algum test falhar, NÃO marque como completo. Corrija e re-rode.

COMMIT FINAL (somente se tudo passar):
  git add -A
  git commit -m "fix: corrige bugs críticos do PoC pós-review

  - router: adiciona PUT /campaigns/{id}/start|pause
  - fingerprint: FanOut=5, TargetZoneTMax=16, vizinhança 31×31
  - index: Entry com VariantID/RateID; loader filtra só campanhas ativas
  - engine: histograma variant/rate/DeltaBin + MinWindowCoverage
  - statemachine: StateCooldown evita detecção duplicada
  - supervisor: RestoreActive() no startup
  - frontend: upload de comercial; player de evidência inline"

═══════════════════════════════════════════════════════════════
CONTEXTO PARA SUBAGENTS IMPLEMENTERS
═══════════════════════════════════════════════════════════════

Ao despachar cada implementer subagent, inclua sempre:

- Texto literal do fix-group acima (completo, não resumido)
- Instrução: leia o arquivo ANTES de editar — alguns fixes já podem estar
  aplicados; se estiver correto, não edite e reporte DONE sem commit
- Instrução: go build ./... deve passar após cada grupo Go
- Instrução: npm run build deve passar após cada grupo frontend
- Instrução: commitar por fix-group (não agrupe tudo num commit só), exceto
  o FIX-GROUP I que faz o commit final agregado se a opção acima não tiver
  sido usada
- Lembrete: não desviar do spec sem consultar o orquestrador (regra CLAUDE.md)
- Ambiente: Windows 11, shell bash, Go 1.22+, Node 18+, Docker Desktop

═══════════════════════════════════════════════════════════════
QUANDO PARAR E CONSULTAR
═══════════════════════════════════════════════════════════════

Pare e me consulte se:
- Implementer reportar BLOCKED (bloqueio real, não "preciso de mais contexto")
- Spec reviewer encontrar ambiguidade não resolvível relendo o plano
- Go build falhar por razão não coberta pelos fix-groups (ex: dependência nova)
- npm run build falhar por incompatibilidade de versão de pacote
- Algum fix-group sugerir mudar o plano arquitetural (precisa do meu OK)
- Após 3 iterações de review sem convergência no mesmo ponto

NÃO PARE para:
- Progresso normal entre fix-groups
- Fixes triviais de compilação (ex: import não usado) — corrija inline
- Dúvidas sobre qual constante usar: o plano_implementacao.md seções §7.3 e
  §9.3 são a fonte de verdade

═══════════════════════════════════════════════════════════════
REPORTING
═══════════════════════════════════════════════════════════════

Por fix-group concluído (ambas reviews passadas + commit feito):
  ✅ Fix-Group X (arquivo.go): impl=ok, spec_review=ok, quality_review=ok, commit=<sha>

Se travar:
  ❌ Fix-Group X: <motivo específico> — aguardando

Ao final do Fix-Group I (build completo):
  reporte quantos tests passaram, resultado do go vet e do npm run build

═══════════════════════════════════════════════════════════════
PRIMEIRA AÇÃO (faça AGORA, nesta ordem)
═══════════════════════════════════════════════════════════════

1. Leia os 4 arquivos da seção "ARQUIVOS DE CONTEXTO"
2. Confirme o worktree: git worktree list
3. Para cada arquivo dos fix-groups, leia o estado atual e anote quais
   fixes já estão aplicados vs. quais precisam de trabalho
4. Me responda com:
   - Lista dos 10 fix-groups com status atual: JÁ FEITO / PENDENTE
   - Qualquer dúvida antes de começar
5. AGUARDE meu "pode começar" antes de invocar qualquer skill ou subagent
6. Quando eu autorizar, invoque superpowers:subagent-driven-development e
   crie o TodoWrite com os 10 fix-groups
7. Execute na ordem A → B → C → D → E → F → G → H → I
   (A e G são os mais críticos — priorize se algo der errado)
```
