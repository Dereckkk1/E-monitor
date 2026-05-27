---
status: implementado
ultima-verificacao: 2026-05-27
codigo-relacionado:
  - frontend/src/pages/CampaignWizardSteps/ConnectionStep.jsx
  - frontend/src/pages/CampaignWizardPage.jsx
  - frontend/src/components/WizardStepper.jsx
  - frontend/src/components/WizardLayout.jsx
  - frontend/src/api/hooks.js
  - workers/internal/probe/probe.go
  - workers/internal/probe/ffmpeg.go
  - workers/internal/api/handlers/stations.go
  - workers/internal/catalog/stations.go
  - workers/internal/api/router.go
---

# Etapa "Conexão" do wizard de campanha

Step 3 do wizard (Dados → Emissoras → **Conexão** → Materiais → Distribuição →
Preços). Lista as emissoras da campanha e deixa o operador diagnosticar e trocar
a `stream_url` de cada uma — útil porque muitos links vieram da fonte antiga e
estão podres (URLs vencidas, IPs de conector interno do fornecedor anterior).

> Spec: [`docs/superpowers/specs/2026-05-27-campaign-connection-step-design.md`](../superpowers/specs/2026-05-27-campaign-connection-step-design.md)
> Plano: [`docs/superpowers/plans/2026-05-27-campaign-connection-step.md`](../superpowers/plans/2026-05-27-campaign-connection-step.md)

## Escada de 3 testes (todos efêmeros, sob demanda)

| Teste | Função interna | O que faz | Timeout |
|-------|----------------|-----------|---------|
| Ping | `probe.Ping` | TCP dial no host:porta da URL. Host alcançável? Distingue "host morto" de "host vivo, stream podre". | ~2s |
| Stream | `probe.ProbeStream` → `ingestor.ProbeAudioCodec` | ffprobe acha áudio decodável? Retorna codec. | ~8s |
| Worker | híbrido (ver abaixo) | O ingest puxaria PCM contínuo? | ~6s |

Ao **abrir** a etapa, só o Ping roda automaticamente (barato), em todas as
emissoras, com concorrência limitada (6 no front). Stream e Worker ficam sob
clique ("Testar" por linha ou "Testar todas").

## Caminho híbrido do teste "worker"

`probe.PickWorkerSource` decide:

1. Se a URL testada é um **override diferente** da salva → sempre **efêmero**
   (o worker vivo, se existe, está na URL antiga — irrelevante).
2. Senão, se há **worker vivo** pra aquela `station_id` (alguma campanha ativa a
   usa) com `last_pcm_at` < 30s → devolve o status **ao vivo**, sem spawnar nada.
3. Caso contrário → `probe.ProbeIngest`: ffmpeg puxa ~5s de PCM (s16le 16kHz
   mono), conta bytes, ok se ≥ 50% do esperado. Mata o ffmpeg ao fim.

**Nunca** sobe worker permanente — campanhas `programada` não pesam o servidor.
O worker permanente continua governado pelo lifecycle (só sobe quando a campanha
vira `ativa`).

## Trocar a URL

Editar a URL na linha e clicar "Testar" roda os probes na URL digitada
(override) **sem gravar**. "Salvar" pede confirmação (`useConfirm`, avisando que
muda pra **todas** as campanhas da emissora) e chama
`PATCH /v1/internal/stations/{id}/stream-url`, que atualiza **só** a `stream_url`
(não usa o `PUT /stations/{id}` cru, que reescreveria todas as colunas e zeraria
city/state/frequency_mhz/logo_url/pmm/metadata). O reconciler recria o worker em
≤30s se a campanha estiver ativa
([worker-commercial-reconciler](../operations/worker-commercial-reconciler.md)).
Emissoras com link funcionando ficam intocadas.

## Contrato da API

`POST /v1/internal/stations/{id}/connection-test` (admin/operator)
- body: `{ "tests": ["ping","stream","worker"], "url": "<override opcional>" }`
  (`tests` vazio = roda os três)
- resposta:
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
  Cada result tem `status` (`ok|fail|skipped`), `detail` (em fail), e campos
  extras por tipo (`latency_ms`, `codec`, `source` `live|ephemeral`,
  `last_pcm_at`, `bytes_per_sec`).

`PATCH /v1/internal/stations/{id}/stream-url` (admin/operator)
- body: `{ "url": "..." }` — atualiza só a `stream_url`. Retorna a station.

## Concorrência e segurança

- `probe.Limiter` (capacidade 8) limita probes que spawnam ffmpeg/ffprobe — a
  barreira autoritativa anti-sobrecarga. O front também limita a 6 em paralelo no
  "Testar todas". Ping não conta (é barato).
- Override de URL e o PATCH são restritos a `http`/`https` (mitiga SSRF).
  Endpoints são admin/operator-only. Sem allowlist de host no v1 (risco residual
  aceito e documentado).

## Não-objetivos

- Não persiste resultado (efêmero, vive na sessão do navegador). Histórico de
  saúde de emissoras ativas continua em `/stream-health`.
- Não sobe worker permanente pra campanha `programada`.
- Não bloqueia avançar no wizard mesmo com emissora vermelha (etapa diagnóstica).

## Como verificar (smoke manual)

Precisa da stack rodando + JWT admin/operator + um stream local
([simulacao-radio](../operations/simulacao-radio.md)).

```bash
# station_id real:
curl -s http://localhost:8080/v1/internal/stations?limit=1 | jq '.data[0].id'

# três testes na URL salva:
curl -s -X POST http://localhost:8080/v1/internal/stations/<ID>/connection-test \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"tests":["ping","stream","worker"]}' | jq

# override (URL ainda não salva):
curl -s -X POST http://localhost:8080/v1/internal/stations/<ID>/connection-test \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"tests":["ping","stream"],"url":"http://localhost:8000/stream"}' | jq

# salvar URL nova (cirúrgico) — city/state/frequency NÃO podem sumir:
curl -s -X PATCH http://localhost:8080/v1/internal/stations/<ID>/stream-url \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"url":"http://localhost:8000/stream"}' | jq '{stream_url, city, state, frequency_mhz}'
```

No frontend: `/campaigns/new` → selecionar emissoras → Step 3 "Conexão". Auto-ping
pinta as pílulas ao abrir; "Testar todas" colore as linhas; editar URL habilita
"Salvar"; avançar não é bloqueado mesmo com falha; stepper mostra 6 passos.
