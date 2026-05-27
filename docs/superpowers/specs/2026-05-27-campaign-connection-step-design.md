# Etapa "Conexão" no wizard de campanha — Design

**Data:** 2026-05-27
**Autor:** brainstorm Dereck + Claude
**Status:** aprovado para planejamento

---

## 1. Problema

Já temos `stream_url` cadastrada para muitas emissoras, mas parte desses links
veio da fonte antiga e está podre: URLs que venceram, ou IPs que eram só um
conector interno do fornecedor anterior — inúteis pra nós. Hoje o operador só
descobre que uma URL está morta quando a campanha ativa e a emissora aparece
vermelha em `/monitoring` — tarde demais.

Precisamos de uma etapa no cadastro de campanha onde o operador:

1. Veja **todas as emissoras da campanha** numa lista.
2. **Teste a conectividade** de cada uma (a URL alcança? toca áudio? o ingest
   conseguiria puxar PCM?).
3. **Troque a `stream_url` ali mesmo**, em modo tentativa-e-erro: "essa não
   funciona → colo outra → testo → funcionou → salvo essa nova pra essa
   emissora". Emissoras que já funcionam ficam intocadas.
4. Tenha **feedback visual por emissora** pra identificar de relance quem está
   com problema.

## 2. Não-objetivos

- **Não** subir worker de ingestão permanente para campanhas `programada`.
  Worker permanente continua governado pelo lifecycle (só sobe quando a
  campanha vira `ativa`). Toda checagem desta etapa é **sob demanda e
  efêmera** — caso contrário o servidor pesaria com workers de campanhas que
  nem começaram. (Restrição explícita do Dereck no brainstorm.)
- **Não** persistir resultado de teste no banco (v1). Resultado vive só na
  sessão do navegador. O histórico de saúde de emissoras **ativas** já existe
  em `/stream-health`.
- **Não** alterar `stream_url` automaticamente. Testar nunca grava; só o botão
  "Salvar" explícito da linha grava, por emissora, opt-in.
- **Não** reconhecimento de música/transcrição (regra §1.3 do plano).

## 3. Decisões travadas (do brainstorm)

| # | Decisão |
|---|---------|
| Posição | Novo **Step 3** do wizard: Dados → Emissoras → **Conexão** → Materiais → Distribuição → Preços. Wizard vai de 5 → 6 steps. Só dentro do wizard. |
| Worker test | **Híbrido**: se já existe worker vivo pra aquela `station_id` (alguma campanha ativa a usa) **e o teste é da URL salva**, devolve o status ao vivo dele (sem spawnar nada). Senão, probe efêmero ~5s. |
| Persistência | **Efêmero** (sessão). Sem migration. |
| Avançar com falha | **Não bloqueia**. Etapa é diagnóstica; aviso suave ("N emissoras com problema") e deixa avançar. |
| Auto-teste ao abrir | **Só Ping** (barato, ~2s) em todas as emissoras ao montar. Stream e Worker ficam sob clique. |

## 4. A escada de 3 testes

Todos sob demanda, efêmeros, com timeout e cleanup garantido do processo.

| Teste | O que faz | Como | Timeout |
|-------|-----------|------|---------|
| **Ping** | Host do stream está alcançável? | Extrai `host:porta` da URL e faz `net.DialTimeout` TCP. Distingue "host morto" de "host vivo, stream podre". | ~2s |
| **Stream** | Existe áudio tocável na URL? | Reusa `ProbeAudioCodec` (ffprobe) de [`workers/internal/ingestor/ffmpeg.go:36`](../../../workers/internal/ingestor/ffmpeg.go). Retorna `ok` + codec detectado. | ~8s |
| **Worker** | O ingest conseguiria puxar PCM contínuo? | **Híbrido** (ver §6). Efêmero: ffmpeg puxa o stream pra PCM (s16le mono 16kHz) por ~5s, conta bytes; `ok` se recebeu fluxo acima de um piso. Mata o ffmpeg ao fim. | ~6s |

## 5. Arquitetura

### 5.1 Visão de componentes

```
ConnectionStep.jsx  ──POST /v1/internal/stations/{id}/connection-test──▶  StationConnectionTest handler
       │                  { tests:[...], url?:"override" }                        │
       │                                                                          ├─▶ probe.Ping(host)        (net.DialTimeout)
       │                                                                          ├─▶ probe.ProbeStream(url)  (ffprobe — reusa ProbeAudioCodec)
       │                                                                          ├─▶ supervisor.WorkerStatuses()  (caminho "live")
       │                                                                          └─▶ probe.ProbeIngest(url)  (ffmpeg efêmero — caminho "ephemeral")
       │                                                                                    ▲
       │                                                                          semáforo global (N concorrentes p/ probes que spawnam processo)
       │
       └──PUT /v1/internal/stations/{id}──▶  (handler Update existente — salva a URL nova; reconciler troca o worker em ≤30s se ativa)
```

### 5.2 Backend

**Novo pacote `workers/internal/probe`** (ou helpers no `ingestor`, decidir no plano):

- `Ping(ctx, rawURL) (PingResult, error)` — parseia a URL, deriva `host:porta`
  (porta default por scheme: 80 http, 443 https), `net.DialTimeout` ~2s.
  Mede latência. Erro de parse de URL inválida → resultado `fail` com detalhe,
  não erro HTTP.
- `ProbeStream(ctx, rawURL) (StreamResult, error)` — reusa a lógica de
  `ProbeAudioCodec`; `ok` quando há stream de áudio decodável; inclui `codec`.
- `ProbeIngest(ctx, rawURL, dur) (IngestResult, error)` — spawna
  `ffmpeg -i <url> -t <dur> -f s16le -ar 16000 -ac 1 -`, lê stdout, conta
  bytes. `ok` se `bytesRecebidos >= piso` (ex: ≥ 50% do esperado =
  `16000 * 2 * dur`). Em qualquer saída (sucesso, timeout, erro), o processo é
  morto via context cancel. Reporta `bytes_per_sec` aproximado.
- **Semáforo global** (buffered channel, ex: capacidade 8) que **só** guarda
  `ProbeStream` e `ProbeIngest` (que spawnam ffmpeg/ffprobe). `Ping` é barato e
  não passa pelo semáforo (ou passa por um limite folgado separado). Protege o
  servidor mesmo se o front disparar muitas requests de uma vez — é a barreira
  autoritativa.

**Novo handler `StationConnectionTest`** em
[`workers/internal/api/handlers/stations.go`](../../../workers/internal/api/handlers/stations.go):

- Rota: `POST /v1/internal/stations/{id}/connection-test`, sob o subgrupo
  **admin/operator** do [`router.go`](../../../workers/internal/api/router.go)
  (spawna processos — não é viewer-safe).
- Request body:
  ```json
  { "tests": ["ping", "stream", "worker"], "url": "http://novo-link/stream" }
  ```
  - `tests`: quais rodar (subconjunto). Default = os três.
  - `url` (opcional): **override** — testa essa URL em vez da salva. Permite
    testar antes de salvar. Validar scheme ∈ {http, https} (mitiga SSRF).
- Carrega a station por `id`. URL efetiva = `url` do body se presente, senão
  `station.StreamURL`.
- Response:
  ```json
  {
    "station_id": "uuid",
    "tested_url": "http://...",
    "results": {
      "ping":   { "status": "ok",   "latency_ms": 42 },
      "stream": { "status": "ok",   "codec": "aac" },
      "worker": { "status": "ok",   "source": "live", "last_pcm_at": "RFC3339" }
    }
  }
  ```
  - `status` ∈ `"ok" | "fail" | "skipped"`. `skipped` = não pedido em `tests`.
  - Em `fail`, inclui `detail` (string curta legível: "host inalcançável",
    "timeout", "nenhum stream de áudio", "sem PCM").
  - `worker.source` ∈ `"live" | "ephemeral"` (ver §6).

### 5.3 Frontend

**Novo `frontend/src/pages/CampaignWizardSteps/ConnectionStep.jsx`:**

- Recebe as emissoras da campanha (já disponíveis no
  [`CampaignWizardPage`](../../../frontend/src/pages/CampaignWizardPage.jsx)
  via `targetStationIds` → `allStations`).
- Estado local por emissora (efêmero):
  `{ urlDraft, dirty, results:{ping,stream,worker}, running }`.
  `urlDraft` inicia em `station.stream_url`.
- **Ao montar:** dispara Auto-Ping em todas (com limite de concorrência
  client-side, ex: 6 em paralelo), pra pintar de cara quem está inalcançável.
- **Por linha:** logo/nome/cidade/banda (via `SmartImage`+`getAppSheetImageUrl`
  conforme design system), input de URL editável (default = URL salva), 3
  pílulas de status (Ping / Stream / Worker), botão **Testar** (testa o
  `urlDraft` atual como override — read-only), botão **Salvar** (habilita só
  quando `dirty`).
- **Salvar:** `showConfirm` avisando que muda a `stream_url` da emissora **pra
  todas as campanhas** (não é por-campanha) → `PUT /stations/{id}` via
  `useUpdateStation` → ao sucesso, `urlDraft` vira a nova base, `dirty=false`,
  e re-roda os testes na URL salva.
- **Header:** "Testar todas" (Ping+Stream+Worker, respeitando limite de
  concorrência) + contador de problemas ("3 emissoras com problema").
- **Cores de estado da linha:** verde (todos os testes pedidos ok) / vermelho
  (algum fail) / âmbar (não testado ou parcial) / azul (testando). Semânticos
  atenuados conforme `design.md`; rosa (`--c-action`) só pra ações.
- Empty state estilizado se a campanha não tem emissoras ("volte ao Step 2").

**Novo hook** `useStationConnectionTest()` em
[`frontend/src/api/hooks.js`](../../../frontend/src/api/hooks.js) — mutation que
chama o endpoint. Reusa `useUpdateStation` existente pro salvar.

### 5.4 Renumeração do wizard (5 → 6 steps)

Inserir "Conexão" como Step 3 desloca Materiais→4, Distribuição→5, Preços→6.
Touchpoints:

- [`CampaignWizardPage.jsx`](../../../frontend/src/pages/CampaignWizardPage.jsx):
  índices dos steps, `Math.min(5,...)` → `6`, `completedSteps([1,2,3,4])` →
  `[1,2,3,4,5]`, lógica de `nextLabel`/`onNext` (finish passa pro step 6),
  `handleStepClick`.
- [`WizardLayout.jsx`](../../../frontend/src/components/WizardLayout.jsx):
  `STEP_META` ganha entrada 6 e os eyebrows viram "Passo X de 6";
  `isLast = currentStep === 6`; **corrige o bug pré-existente** do footer que
  mostra `{currentStep} / 4` (hardcoded errado — já estava errado com 5 steps).
- [`WizardStepper.jsx`](../../../frontend/src/components/WizardStepper.jsx):
  adicionar o label/ícone do novo step (verificar estrutura no plano).
- `nextDisabled` da Conexão = `false` (não bloqueia — §3).

## 6. Caminho híbrido do teste "worker" (detalhe)

```
teste "worker" pedido
        │
        ├─ url override presente E != station.stream_url ?
        │        └─ SIM → sempre EFÊMERO (o worker vivo, se existe, está na URL antiga — irrelevante)
        │
        └─ NÃO (testando a URL salva)
                 │
                 ├─ supervisor.WorkerStatuses()[station_id].Active
                 │   && time.Since(last_pcm_at) < 30s ?
                 │        └─ SIM → status "ok", source "live", devolve last_pcm_at (zero spawn)
                 │
                 └─ NÃO → ProbeIngest efêmero (source "ephemeral")
```

`WorkerStatuses()` vive em
[`workers/internal/supervisor/supervisor.go`](../../../workers/internal/supervisor/supervisor.go).
O handler precisa de acesso ao `Supervisor` (já injetado em outros handlers de
health).

## 7. Tratamento de erro & segurança

- **Independência:** cada teste é isolado; um falhar não aborta os outros. O
  handler roda os pedidos e agrega.
- **Timeout & cleanup:** cada probe roda com `context.WithTimeout`. Ao expirar,
  o ffmpeg/ffprobe é morto (kill via context). Sem processos órfãos.
- **SSRF:** o override de URL deixa um admin/operator fazer o servidor conectar
  numa URL arbitrária. Mitigação v1: endpoint é admin/operator-only (interno) +
  restringe scheme a http/https. Risco residual aceito e **documentado** aqui;
  não fazemos allowlist de host no v1.
- **URL inválida / vazia:** resultado `fail` com `detail`, nunca 500.
- **ffmpeg/ffprobe ausente:** 500 com erro claro (mesma imagem do supervisor já
  tem os binários — caso não-esperado em prod).
- **Sobrecarga:** semáforo global é a defesa real; o limite client-side é só
  pra UX. "Testar todas" em 200 emissoras nunca passa de N processos
  simultâneos no servidor.

## 8. Testes

- **Backend (unit):** parse de `host:porta` por scheme; validação de scheme
  (rejeita ftp/file/gopher); mapeamento de resultado (ok/fail/detail);
  comportamento do semáforo (não excede capacidade); decisão híbrida do worker
  (live vs ephemeral vs override).
- **Backend (integração):** usar o simulador de stream local
  ([`docs/operations/simulacao-radio.md`](../../operations/simulacao-radio.md))
  pra um caso ok e um caso de URL morta.
- **Frontend:** smoke manual (padrão atual do projeto — sem teste automatizado
  de front).

## 9. Documentação a criar (na implementação)

- `docs/features/campaign-connection-step.md` (header YAML obrigatório) — fluxo
  da etapa, contrato do endpoint, semântica híbrida do worker, nota de SSRF.
- Atualizar [`docs/features/campaign-wizard.md`](../../features/campaign-wizard.md)
  pra refletir os 6 steps.
- Linha no [`docs/README.md`](../../README.md) e no mapa de consulta do
  `CLAUDE.md`.

## 10. Resumo do que muda

**Novo:**
- `workers/internal/probe/` (Ping / ProbeStream / ProbeIngest + semáforo)
- handler `StationConnectionTest` + rota `POST /stations/{id}/connection-test`
- `frontend/.../ConnectionStep.jsx` + hook `useStationConnectionTest`

**Alterado:**
- `CampaignWizardPage.jsx`, `WizardLayout.jsx`, `WizardStepper.jsx`
  (renumeração 5→6 + correção do "/4")

**Reusado sem mudança:**
- `PUT /stations/{id}` (salvar URL), `ProbeAudioCodec`, `WorkerStatuses()`,
  reconciler de `stream_url`.
