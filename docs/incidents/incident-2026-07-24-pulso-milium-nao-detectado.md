---
status: em-investigacao
severidade: ALTA
ultima-verificacao: 2026-07-29
codigo-relacionado:
  - workers/internal/match/statemachine.go
  - workers/internal/match/engine.go
  - workers/internal/match/coverage.go
  - workers/internal/supervisor/disambiguation.go
  - workers/internal/supervisor/dedup_buffer.go
  - workers/internal/sharing/sharing.go
  - workers/internal/evidence/service.go
  - workers/internal/audit/auditor.go
  - workers/internal/calibration/job.go
---

# INCIDENTE 2026-07-24 — PULSO SONORO MILIUM (5.7s) não detectado em 2 emissoras

> **Status:** investigação local COMPLETA (áudio + código + reprodução E2E). Causa raiz em prod
> tem 2 candidatas finais; o runbook da §5 discrimina entre elas com queries read-only.
> **O problema NÃO é falta de hashes e NÃO é o audit §9.9** — os dois foram descartados com medição.

## 0. TL;DR

- O material **PULSO SONORO MILIUM tem 5.71s** — áudio ultra-curto, a classe com recall mais
  frágil do sistema ([plano de áudio curto](../roadmap/short-audio-detection-plan.md), Fases 1-2
  **não implementadas**).
- **O fingerprint do master é saudável**: ~1.8-2.6k hashes por variante (10.925 no total),
  self-match score 1753. *Não é falta de hashes.*
- **As duas censuras contêm o pulso e ele casa** no matcher offline que replica prod:
  pico score 57 (censura 24/07) e 76 (censura WhatsApp), cobertura 0.35-0.37 — acima dos
  gates (score≥5, cov≥0.15). E isso em aircheck degradado (22kHz mono 40kbps); no stream real
  os scores seriam maiores.
- **O audit §9.9 PASSARIA** nos dois casos (score 39-42 ≥ bypass 30; cov 0.33-0.42 ≥ 0.15).
  Além disso, rejeição de audit **deixa row** (`evidence_status='audit_rejected'`) — se fosse
  o audit, haveria registro no banco.
- Logo, a morte está em um destes pontos (ranqueados na §3):
  **(1)** dedup §18.2.2 v1 por duração — um spot ≥10s da Milium que contenha o pulso confirma
  junto (co-firing) e o pulso **sempre perde** (suprimido sem row, ou retratado);
  **(2)** exigência estrutural de **2 janelas qualificadas** na state machine — um 5.7s tem
  no máximo 1 janela 100% interna, a segunda é parcial e pode falhar os gates por estação;
  **(3)** vínculo/índice (target_stations sem as emissoras, campanha fora de data, etc.).
- **Atualização 29-30/07 — quadro fechado:** o pulso detectou em 5 emissoras e falhou em 2
  (`stream6.loopert.com/303`, `painel.sintonizar.tv.br/stream/meninablu`). As hipóteses por
  emissora foram **eliminadas uma a uma**: as 2 falhas detectam corretamente **outro material
  de 30s do plano** (captura viva → ban de IP morto; stream carrega os comerciais → stream≠
  antena improvável); `meninacam`, do MESMO painel sintonizar, detecta ok (classe de painel
  não separa); codec HE-AAC e threshold foram descartados por medição (§4b). E **Dereck
  confirmou que o spot de 30s detectado em todas CONTÉM o pulso** (§4c) — ou seja, todas as 8
  emissoras rodam a configuração exata do E2E da §4. O false-confirm do spot na tocada
  standalone do pulso é *borderline* (score 43-127) → co-confirma em algumas emissoras e não
  em outras → **onde co-confirma, o dedup por duração zera o pulso**. É a causa 1, modulada
  por emissora.
- A §5 tem o runbook completo (bash+SQL read-only) pra cravar qual delas foi — em particular
  `dedup_suppressions` e as detections de QUALQUER material Milium na janela 24/07 15:30-16:05.

## 1. O que foi verificado localmente (evidência)

### 1.1 Os arquivos

| Arquivo (audio-refs/) | Duração real | Formato |
|---|---|---|
| `PULSO SONORO MILIUM - [Ao Vivo].mp3` (master) | **5.71s** | mp3 48kHz stereo |
| `Censura - Milium 24-07 15h45.mp3.mpeg` | 123.3s | mp3 **22.05kHz MONO 40kbps** (aircheck degradado) |
| `WhatsApp Audio 2026-07-29 at 15.34.52 (1).mpeg` | 27.4s | mp3 44.1kHz stereo 320kbps |

⚠️ O nome "[Ao Vivo]" levanta uma dúvida que só prod resolve: **a versão cadastrada na
plataforma é esta mesma gravação?** Se o material cadastrado for outra versão do pulso
(estúdio vs. ao vivo), o master de prod pode não casar com o que foi ao ar. O runbook checa
`duration_seconds`/`master_sha256` do material cadastrado (B1).

### 1.2 Diagnóstico offline (replica o matcher de prod: índice v0+5 variantes broadcast_sim, janela 4s/hop 2s)

```
Master: 5.71s @16kHz — 43 frames
  v0: 1753 hashes … v5: 2661 hashes   SELF-MATCH: score=1753 cov=0.86  ← fingerprint OK

Censura 24/07 15h45 (123s):
  pico score=57 @ t=78s (var 5), cov±2=0.37 — janelas t=76 (33) e t=78 (57) passam os gates
  → 2 janelas consecutivas qualificadas  → o live CONFIRMARIA (por margem mínima)
  AUDIT sim (clipe 74-86s): score=39 cov=0.42 → PASSA

Censura WhatsApp (27s):
  pico score=76 @ t=6s (var 0), cov±2=0.35 — mas vizinhas t=4 (5) e t=8 (6) FALHAM cov/2%
  → APENAS 1 janela qualificada → o live NÃO confirmaria (state machine exige 2)
  AUDIT sim (clipe 2-14s): score=42 cov=0.35 → PASSA
```

Leitura: **o áudio casa**. Nos gates de janela, a tocada de 24/07 passaria por margem mínima
(2 janelas exatas) e a do WhatsApp não passaria — *neste aircheck*; a qualidade do stream real
é melhor que 40kbps mono, então em prod o cenário era mais favorável que isso.

## 2. O ciclo completo de detecção em prod (mapeado no código)

Ordem do pipeline com os gates que podem matar uma veiculação real. Constantes verificadas no
código atual (base do master que estava em prod em 24/07).

| # | Estágio | Gate / valor | O que acontece quando falha |
|---|---|---|---|
| 0a | Índice em memória | `fingerprint_status='ready'` + link com campanha `programada/ativa` ([loader.go:53](../../workers/internal/index/loader.go)) | material invisível; auto-cura em ≤30min (`RunReconcileLoop`) |
| 0b | Lista do worker | campanha **`ativa`** + `$station = ANY(cm.target_stations)` ([materials.go:257](../../workers/internal/catalog/materials.go)) | sem state machine → **silêncio total** (resultados descartados em [worker.go:479](../../workers/internal/ingestor/worker.go)) |
| 1a | Engine, por janela 4s/2s | `count ≥ min_hashes` (default 5; calibrado `max(noise_p99×1.5, 5)` [job.go:51](../../workers/internal/calibration/job.go)) | não entra em results; só `window scan` logado |
| 1b | Engine | `count ≥ 2% dos hashes da janela` (`MinScoreCoverage=0.02` [supervisor.go:524](../../workers/internal/supervisor/supervisor.go)) — piso real de **~16-40 hits** em janela densa | idem; **gate mais alto que o min_hashes na prática** |
| 1c | Engine | pico por **bin isolado** de delta (sem merge de vizinhos — **#1 do plano NÃO implementado**) | vote-splitting derruba o pico; fatal pra contagens pequenas de material curto |
| 2a | State machine | Idle→Detecting na 1ª janela com `UniqueScore ≥ minScore` ([statemachine.go:143](../../workers/internal/match/statemachine.go)) | — |
| 2b | State machine | **confirmação SÓ na 2ª janela qualificada** ([statemachine.go:161-168](../../workers/internal/match/statemachine.go)) | 1 janela boa = Detecting → `confirmTimeout` 30s → Idle. **Sem log, sem row.** Um 5.7s tem NO MÁXIMO 1 janela 100% interna (hop 2s); a 2ª é sempre parcial |
| 2c | State machine | cobertura ≥0.15 — **no-op pra ≤27.3s** (piso de 32 frames em [coverage.go:9](../../workers/internal/match/coverage.go) já dá 32/44=0.73) | não é o culpado pra material curto |
| 2d | Cooldown | `duração+5s` ≈ **10.6s** pro pulso | só engole re-tocada em <10.6s; não explica zero |
| 3 | NATS core | `detections.pending`→supervisor→`detections.confirmed` fire-and-forget | restart do api no instante = confirmação evapora sem log |
| 4 | **Dedup §18.2.2 v1** (supervisor, ANTES da row) | conflito = mesma emissora + **mesmo cliente** + overlap de janela ([dedup_buffer.go:77](../../workers/internal/supervisor/dedup_buffer.go)); vence o de **maior duração** ([disambiguation.go:119-135](../../workers/internal/supervisor/disambiguation.go)) | curto chega 2º → **Suppress: NENHUMA row**, forense só em `dedup_suppressions` + métrica; curto chega 1º → row criada e **retratada** (se a row ainda não existia, a retração é perdida com warn) |
| 5 | Atribuição (evidence) | link + `target_stations` + campanha `programada/ativa` + **`detected_at` dentro de start/end** ([attribution.go:29-50](../../workers/internal/evidence/attribution.go)) | **descarte SEM row** — log `evidence: failed to resolve short_id` |
| 6 | Audit §9.9 | score≥5 E (cov≥0.15 OU score≥30 sem shared hash) ([auditor.go:278](../../workers/internal/audit/auditor.go)); roda contra o **clipe inteiro ~126s** (#10A não implementado) | row fica com `audit_rejected` — **invisível em todas as telas**, mas existe no banco |
| 7 | Pós-audit (flags) | `DISAMBIG_BY_COVERAGE` / `TWIN_*` / `CONFIDENCE_AWARE` — **todas default OFF** no compose | co-fire guard/recuperações só rodam com flag ON |

**Peça-chave do caso:** `sharing.go` **pula o shared-hash flagging pra material <10s dos dois
lados** (`MinShareableDurationSeconds=10`, [sharing.go:92](../../workers/internal/sharing/sharing.go)) — e o
comentário do próprio código ([sharing.go:86-91](../../workers/internal/sharing/sharing.go)) declara o design:

> *"O caso de conflito real (X de 30s toca, e contém o áudio de PULSO de 7s) cai pra defesa de
> version-disambiguation — supervisor retrata PULSO em favor de X pela regra de maior duração."*

Consequência matemática verificada: com `is_shared=false` nos dois lados, quando o pulso toca
**sozinho**, um spot de 30s da Milium que contenha o pulso também acumula score cheio na região
compartilhada e **também confirma** (2 janelas; cobertura (15+32)/234 ≈ 0.20 ≥ 0.15). Aí o dedup
v1 mata o pulso — **sempre** — e a tocada ou vira detection do spot errado, ou é rejeitada no
audit do spot, ou é suprimida sem rastro visível. Esse é exatamente o precedente RÔGGA
(mat34 5.4s ⊂ mat33 30s, incidente PILECCO 2026-06-30) e o achado G2 do
[AUDIT-2026-07-02](../AUDIT-2026-07-02.md).

## 3. Causas candidatas, ranqueadas — e o teste que crava cada uma

| # | Hipótese | Consistência com "2 emissoras, zero registro" | Teste discriminante (runbook §5) |
|---|---|---|---|
| 1 | **Dedup v1 por duração** (pulso ⊂ spot Milium ≥10s co-confirmando) | ALTA — sistêmico por material, vale pra N emissoras | B5 (`dedup_suppressions`), B6 (detections de outros materiais Milium 15:30-16:05), B4 (rows retraídas) |
| 2 | **2ª janela qualificada falha** (geometria 4s/2s × 5.7s + min_hashes calibrado + gate 2%) | ALTA se as emissoras têm threshold calibrado alto | B3 (`station_thresholds`), B8 (logs `window scan` com score alto sem confirmação) |
| 3 | **Vínculo/índice**: `target_stations` sem as 2 emissoras, campanha fora do range de datas, fingerprint não-ready | MÉDIA — binário, explica tudo de uma vez | B1/B2 (material+link+datas) |
| 4 | Audit §9.9 rejeitou | BAIXA (audit passaria na simulação; e deixaria row) | B4 (`evidence_status='audit_rejected'`) |
| 5 | Versão cadastrada ≠ versão que foi ao ar ("[Ao Vivo]") | BAIXA-MÉDIA | B1 (`duration_seconds`/sha256 vs arquivo local) |
| 6 | Stream ≠ antena / worker down na hora | BAIXA (2 emissoras ao mesmo tempo é improvável) | B7 (`stream_health_events`) |

## 3b. Re-rank com o fato "5 emissoras detectam, 2 não" (29/07)

> **SUPERSEDIDO em 30/07 pela §4c** — mantido como registro do processo de eliminação. As
> hipóteses 1 (ban de IP) e 2 (stream≠antena) morreram quando Dereck confirmou que as 2
> emissoras detectam corretamente outro material de 30s do plano; 3 (threshold) e 4 (codec)
> morreram por medição (§4b). Sobrou a 5 (dedup), agora confirmada como borderline por
> emissora (§4c).

O material funciona no resto da rede → material/fingerprint/índice global estão OK. O que
diferencia exatamente essas 2:

| # | Hipótese (por emissora) | Por quê encaixa | Teste |
|---|---|---|---|
| 1 | **Captura morta/instável: painel de streaming bloqueando o IP da VM** | As duas URLs são de painéis de hosting (loopert, painel.sintonizar.tv.br) — mesma classe que **já baniu o IP fixo da VM (34.39.163.110) em jun/2026** (livespanel/streamingdevideo, timeout TCP silencioso; breaker implementado mas não deployado à época). "Streaming no ar" no navegador do Dereck ≠ no ar pro IP da VM | B7 (`stream_health_events`), **B10** (bytes/reconnects do worker), `/admin/overview` |
| 2 | **Stream ≠ antena** (meninablu = rede Menina; afiliadas inserem comercial local na antena que não vai pro stream — ou o stream carrega feed de outra praça) | Censura é gravação de ar/aircheck, não do stream; a 90fm/ASAAS já ensinou a checar isso | B11 (gravar o stream num horário com veiculação programada e conferir se o pulso está NO STREAM) |
| 3 | **Threshold calibrado alto nessas 2** (`min_hashes = noise_p99×1.5`, sem teto; #11 não implementado) | Mata a 2ª janela do 5.7s só onde o ruído é alto | B3 |
| 4 | **Codec/qualidade do stream** (HE-AAC/SBR, mono 22k, bitrate baixo — riscos R6/R7) | Plausível degradar recall; **difícil produzir zero absoluto** (a censura de 40kbps mono ainda casa offline com score 57) | §4b (análise direta das URLs) + B10 |
| 5 | Dedup §18.2.2 (§3 hipótese 1) | Só se o spot-que-contém-o-pulso estiver vinculado **só** a essas 2 emissoras | B5, B6, B2 |

Resposta direta à pergunta "não pode ser só qualidade de stream?": pode contribuir, mas
dificilmente explica **zero absoluto** sozinha — a censura de 22kHz mono 40kbps (pior que
qualquer stream razoável) ainda casa com score 57 no matcher offline. Qualidade ruim derruba
recall pra "às vezes perde"; **zero em todas as tocadas cheira a captura morta (worker sem
áudio útil), stream sem o comercial, ou supressão sistemática**. A §4b abaixo mede os dois
streams reais.

## 4. Reprodução E2E local (fluxo completo de prod) — 2026-07-29

Ambiente: stack docker local completa (api+supervisor+workers, fingerprint Python, postgres,
NATS, MinIO), imagem `docker-api` buildada do master atual. Material subido pelo endpoint real
(`POST /v1/internal/materials` → NATS `fingerprint.generate` → Python → `index.reload`),
campanha criada e ativada pela API, emissora "SIM PULSO FM" apontada pra um simulador de rádio
(Icecast + ffmpeg, preset `fm-standard` 96k, o pulso tocando em loop com gaps de 12s).

### Fase A — só o pulso no catálogo: DETECTA PERFEITAMENTE

O master de 5.7s (short_id local 211, 10.925 hashes) foi confirmado em **TODAS as tocadas**:

```
window match score=115..372 (unique_score=score, ratio 0.07-0.31)
→ detecting started (1ª janela) → detection confirmed (2ª janela, confidence 1.0)
→ evidence persisted → audit passed (score 556, coverage 0.897, extent 1.0) → evidence ready
10 rows 'available' em 3 minutos (1 por tocada do loop)
```

**Conclusão fase A: o pipeline completo de prod detecta um 5.7s standalone com folga** em
qualidade de stream normal. As hipóteses "falta de hashes", "state machine não confirma curto"
e "audit rejeita curto" caem — *quando o pulso é o único material com aquele áudio no catálogo*.

### Fase B — entra um spot de 30s do mesmo cliente que CONTÉM o pulso: o caso de prod se reproduz na hora

Subido um segundo material (short_id 212, 30s, sintético com o pulso nos últimos 5.7s —
réplica do padrão "spot com pulso sonoro no final"). O sharing **pulou o par** (pulso <10s →
`MinShareableDuration`, dos dois lados) → `is_shared=false` em ambos → co-firing armado.
O simulador continuou tocando **apenas o pulso standalone**. Resultado imediato, capturado ao vivo:

```
TOCADA 1 (19:37:57)
  window match: 211 score=322 E 212 score=308 (mesma janela — co-firing)
  detection confirmed: 211 conf=0.886  E  212 conf=0.167  ← false-confirm do spot a 17%
  supervisor: "retract — no rows updated (evidence write may be in flight)"   ← RACE
  supervisor: "detection retracted by version disambiguation, retracted=211,
               replacement=212, reason=longer_cut_detected"
  → no banco: AS DUAS rows ficaram vivas (retração perdida) = DUPLA CONTAGEM
    211 conf 0.886 + 212 conf 0.167, ambas sem retracted_at

TOCADA 2 (19:38:11)
  211 confirma (conf 1.0, tocada real)
  supervisor: "detection suppressed by version disambiguation,
               suppressed=211 (6s), kept=212 (30s)"
  → NENHUMA row criada pro pulso; forense só em dedup_suppressions:
    suppressed_conf=1.000 vs kept_conf=0.167, reason=shorter_cut

TOCADAS SEGUINTES: alternância dos dois desfechos acima, dependendo do timing
  (o 212 fica ~30-60s "vivo" no dedup buffer e absorve as tocadas seguintes do pulso)
```

`dedup_suppressions` local após o teste:

```
 quando   | suppressed | kept | sup_dur | kept_dur | conf_supr | conf_kept | reason
 19:38:11 |    211     | 212  |    6    |    30    |   1.000   |   0.167   | shorter_cut
```

**Conclusão fase B (a causa raiz demonstrada):** com um material ≥10s do mesmo cliente contendo
o áudio do pulso no catálogo da emissora, a tocada standalone do pulso:
1. co-dispara o spot (false-confirm a ~0.17-0.21 de cobertura);
2. **perde SEMPRE a desambiguação §18.2.2 v1 por duração** (`DISAMBIG_CONFIDENCE_AWARE` OFF);
3. morre por **suppress sem row** ou vira row **retratada** — e no race "evidence write in
   flight" a retração se perde e sobra **dupla contagem** (row do pulso + row falsa do spot).
4. As rows falsas do spot (conf 0.17-0.21) seguem pro audit e **PASSAM** — medido ao vivo:
   `audit passed, score=777, coverage=0.183` (0.183 ≥ 0.15; e mesmo que a cobertura falhasse,
   o `coverageBypassScore=30` se aplicaria, já que o par <10s pulado pelo sharing deixa o spot
   sem hash compartilhado) → **o spot que nunca tocou vira detecção `available`** (misatribuição
   visível nas telas como veiculação do spot).

Pré-condição do mecanismo em prod: o spot-que-contém-o-pulso precisa estar **vinculado às
mesmas emissoras** (worker precisa ter os dois no índice). Se em alguma das 2 emissoras nenhum
material Milium ≥10s contiver o pulso, a causa lá é outra (checar B1/B2/B3 do runbook).

É o mesmo mecanismo já visto em prod no caso RÔGGA (mat34 5.4s ⊂ mat33 30s, 2026-06-30) e o
comportamento **documentado como design** em [sharing.go:86-91](../../workers/internal/sharing/sharing.go).

Placar final do teste (banco local, ~16 tocadas do pulso após o spot entrar):

| material | rows | available | retratadas | conf média |
|---|---|---|---|---|
| 211 pulso (tocou de verdade) | 16 | 14 | 1 | 0.993 |
| 212 spot (NUNCA tocou) | 5 | 2+ | 0 | 0.275 |

No teste local o race fez o pulso *manter* a maioria das rows (retração chega antes da row
existir e se perde) — em prod, com latência real de evidence write e o spot ficando "vivo" no
dedup buffer, o balanço pende pro suppress/retract (foi o que os logs mostraram alternando).
O ponto não é o placar exato: é que **os 3 desfechos ruins (suprimir tocada real, dupla
contagem, detecção available de material que não tocou) acontecem por corrida de timing** —
não-determinismo puro.

## 4b. Análise direta dos 2 streams (loopert /303 e sintonizar /meninablu) — MEDIDO 29/07

**Codec (ffprobe do dev, ambos responderam):**

| stream | codec | profile | sr | canais | bitrate |
|---|---|---|---|---|---|
| stream6.loopert.com/303 | aac | **HE-AACv2** (SBR+PS) | 44.1k | 2 | 64k |
| painel.sintonizar.tv.br/stream/meninablu | aac | **HE-AAC** (SBR) | 44.1k | 2 | 64k |

As duas emissoras que falham são exatamente as duas em **HE-AAC/SBR** (risco R7). Achado
lateral confirmado: o `fingerprint.Dockerfile` instala ffmpeg Debian **sem libfdk_aac** → em
prod a variante 2 do broadcast_sim ("HE-AAC 48k") **cai pra AAC-LC nativo** — o índice NÃO tem
variante SBR real (log: `libfdk_aac unavailable; falling back to native aac for variant 2`).

**Mas a medição diz que isso NÃO explica o zero:**

1. A banda do fingerprint é **97Hz–4kHz** (`FREQ_MAX_BIN=1024` × 3.9Hz) e o crossover SBR fica
   acima de ~5.5kHz — a banda usada vem do **core AAC real**, não da reconstrução SBR.
2. Teste empírico — pulso re-encodado pela cadeia in-band equivalente (compressão de emissora
   + core a 22.05kHz + AAC 40-64k mono/estéreo, incluindo hipercompressão estilo meninablu,
   que mediu mean_volume -5.4dB):

   | cadeia | score janela 4s | cov clipe |
   |---|---|---|
   | HE-AACv1 64k emu | 384 | 0.86 |
   | HE-AACv2 64k emu (mono, core 40k) | 422 | 0.86 |
   | pior caso hipercomp + mono 22k 40k | 348 | 0.86 |

   Score de tocada real nessas cadeias: **300-400+** — gates são 5 e ~2% (~25-30 hits). Sobra
   margem de 10x.
3. Áudio das capturas de 60s é saudável (conteúdo forte em 1-4kHz nos dois; streams vivos e
   estáveis a partir do IP do dev).
4. **Noise floor da programação real** dos 2 streams contra o índice do pulso: pico **6-7** em
   60s → `min_hashes` calibrado ficaria ~9-10 → não mata tocada de score 300+. (Hipótese 3 do
   §3b — threshold — fica improvável; B3 confirma o valor real.)

**Conclusão §4b:** codec/qualidade de stream **descartado como causa do zero** (pode custar
alguns % de recall, não 100%). Threshold calibrado improvável.

**30/07 — sonda das 8 emissoras enterra a correlação de codec de vez:**

| stream (mount) | codec medido | pulso detecta? |
|---|---|---|
| wz7.servidoresbrasil.com:8070/stream | HE-AAC 48k 44.1kHz | ✅ |
| painel.sintonizar.tv.br/stream/**meninablu** | **HE-AAC 64k 44.1kHz** | ❌ |
| painel.sintonizar.tv.br/stream/meninacam | HE-AAC 64k 48kHz | ✅ |
| stream6.loopert.com/**303** | **HE-AACv2 64k 44.1kHz** | ❌ |
| cast.youngtech.radio.br/radio/8370/radio | MP3 256k 44.1kHz | ✅ |
| ice.fabricahost.com.br/fmverdevale | HE-AAC 96k 48kHz | ✅ |
| cast2.youngtech.radio.br:8130/radio | HE-AACv2 64k 44.1kHz | ✅ |
| ice-br.fabricahost.com.br/play/amandafm | HE-AACv2 64k 44.1kHz | ✅ |

5 das 6 que detectam também são HE-AAC — duas com perfil+bitrate **idênticos** à loopert que
falha. Codec não correlaciona. O que separa os grupos é pra que lado o false-confirm
*borderline* do spot (§4c, score 43-127) cai em cada praça — processamento local de áudio /
timing, não parâmetro observável do stream.

## 4c. O par REAL (30/07): PULSO × "MILIUM - DEMAIS RADIOS DO PLANO 20 a 26.07" (30.8s)

Dereck confirmou que o spot de 30s **que detecta corretamente em todas as 8 emissoras contém
o pulso** — e subiu o master. Medições com o par real:

1. **Containment parcial, não cópia**: o final do spot (t≈26-30.8s) casa com o master do pulso
   a score **45** / cov 0.33 (cópia idêntica daria 300+). O pulso standalone ("[Ao Vivo]") é
   uma versão *parecida* do encerramento do spot — mesmo jingle, gravação diferente.
2. **Reverso (o que importa): quando o pulso toca sozinho, o índice do SPOT vê score 127**
   (cov single 0.08; com o piso de 32 frames, 2 janelas dão cov (15.6+32)/240 ≈ **0.198 ≥
   0.15**) → o spot **pode** false-confirmar a conf ~0.2 — mas é **borderline** (score 43-127
   conforme janela/variante), não garantido como no sintético.
3. **Nas duas censuras o pulso tocou STANDALONE** — timeline janela-a-janela contra os dois
   índices mostra o pulso em t=76-80s (censura 24/07) e t=6-10s (WhatsApp) **sem nenhuma
   tocada do spot adjacente** na gravação.

**Isso fecha a explicação unificada do "5 ok / 2 zeradas":** o false-confirm do spot na tocada
standalone do pulso é *borderline* — pequenas diferenças por emissora (cadeia de processamento,
codec, loudness) decidem se o spot co-confirma ou não. Onde não co-confirma → pulso detecta
normal (as 5). Onde co-confirma → dedup §18.2.2 v1 mata o pulso por duração (as 2). Nenhuma
causa "da emissora" além de empurrar um caso borderline pra um lado ou pro outro — o defeito
é a **arbitragem por duração diante de um co-fire fraco** (conf 1.0 vs ~0.2).

**Corolário pra conferir em prod (B4/B6):** nas 5 emissoras "ok", parte das rows do pulso e do
spot pode estar trocada/duplicada nos horários em que o SPOT tocou (co-fire reverso, score 45 —
mais fraco, menos provável, mas existe; e o race do retract perde retrações, como medido).

### Mina de deploy descoberta: `DISAMBIG_CONFIDENCE_AWARE` não tinha passthrough no compose

O `cmd/api/main.go` lê a env, mas o `docker-compose.yml` **não a repassava** pro container —
setar no `.env` da VM silenciosamente não faria NADA (mesma armadilha do `SHARING_MIN_SCORE`,
incidente 2026-06-15). **Corrigido neste working tree** (linha nova ao lado de
`DISAMBIG_BY_COVERAGE`); precisa ir junto no commit do fix.

## 4d. VALIDAÇÃO DO FIX — E2E com o par real e `DISAMBIG_CONFIDENCE_AWARE=true` (30/07)

Mesmo ambiente E2E da §4, agora com o **par real** (pulso 211 + spot "DEMAIS RADIOS" 213,
ambos vinculados na mesma emissora) e a flag ligada (após corrigir o passthrough do compose).
Boot loga `dedup confidence-aware ENABLED`.

**C1 — pulso tocando standalone (o caso que falhava em prod):**

```
14 tocadas em ~4.5min → 211 confirmado em TODAS (conf 1.0)
co-fire do spot real: 1 vez em 14 (conf 0.154 — borderline, como previsto)
e nessa vez: "suppressed_short_id: 213, kept_short_id: 211,
              suppressed_duration: 31, kept_duration: 6"
→ a flag INVERTEU a regra de duração: o false-confirm do spot foi suprimido,
  a tocada real do pulso ficou. gap de confiança 0.84 vs 0.15 >> margem 0.25.
```

**C2 — spot tocando (teste de regressão): ⚠️ REGRIDE — a flag sozinha NÃO é o fix**

```
6 tocadas do spot em ~3.5min:
  spot confirma com conf 0.20 (ESTRUTURAL: 30s confirma na 2ª janela →
    cobertura (15.6+32)/240 ≈ 0.198 — nunca chega perto de 1.0 na confirmação)
  pulso co-dispara na CAUDA do spot com conf 1.0 (ESTRUTURAL: piso de 32
    frames ≥ os 44 frames do pulso → 1-2 janelas = cobertura 0.73-1.0)
  gap +0.8 → "retracted_short_id: 213, replacement_short_id: 211" em 4 das 6
  → O PULSO ROUBA A TOCADA REAL DO SPOT (misatribuição invertida + spot some)
```

**Insight central da validação:** no momento do dedup do supervisor, os dois cenários são
**indistinguíveis** — nos DOIS o supervisor vê "curto conf 1.0 vs longo conf ~0.2", porque a
"confiança" da state machine é cobertura-na-confirmação, estruturalmente enviesada (curto
sempre ~1.0 pelo piso de 32 frames; longo sempre ~0.2 por confirmar na 2ª janela). Nenhuma
regra no supervisor decide certo com esses números. Quem discrimina é a **evidência**:

| cenário | audit_coverage do SPOT sobre o clipe | do PULSO |
|---|---|---|
| pulso standalone | **0.183** (medido, §4) | 0.90 |
| spot tocando | **0.90+** | ~1.0 (cauda está no clipe) |

O árbitro correto é a cobertura do **material LONGO** medida na evidência (`audit_coverage`,
já computada e persistida): alta → spot tocou; baixa → pulso standalone. A arbitragem tem que
ser **pós-audit** (ou usar a âncora de offset/extent do match no supervisor — achado G3 do
AUDIT-2026-07-02, aberto).

Matriz demonstrada (par real, E2E):

| cenário | flag OFF (prod hoje) | flag ON |
|---|---|---|
| pulso standalone | pulso suprimido/retratado (quando o spot co-confirma — borderline, ~1/14 aqui, mais frequente nas 2 emissoras de prod) ❌ | pulso mantido, false-spot suprimido ✅ |
| spot tocando | spot mantido ✅ | spot retratado, pulso fica (4/6) ❌ |

## 4e. VALIDAÇÃO FINAL — `DISAMBIG_BY_COVERAGE=true` resolve as DUAS direções (E2E fase D, 30/07)

A arbitragem correta **já existe mergeada**: §18.2.2-v2 (`reattributeByCoverage` + co-fire
guard + reject-path), gated por `DISAMBIG_BY_COVERAGE` (passthrough OK no compose). Validada
com o par real + o spot sintético (co-fire determinístico), flag ON, v1 intacta:

**D1/D1b — pulso tocando standalone (o caso que zerava):**

| material | rows | contando | retratadas |
|---|---|---|---|
| 211 pulso (tocou ~15x) | 15 | **14** | 1 |
| 212 spot sintético (nunca tocou) | 13 | **0** | 13 |

A v1 continuou matando o pulso em toda tocada (suppress/retract) — e a v2 desfez TUDO no
pós-audit: `co-fire — restored winner, retracted duplicate (winner 211)` (des-retrata o pulso
e retrata o falso), `winner already counted, retracted duplicate` (limpa duplicata do race), e
`reattributed by coverage (from 212, cov 0.183 → vencedor)` (o caso suppress-sem-row: a row
falsa do spot vira a row da tocada). **De zero pra 14/15 contando.**

**D2 — spot real tocando (regressão que a confidence-aware reprovou):**

| material | rows | contando | retratadas |
|---|---|---|---|
| 213 spot real (tocou 6x) | 6 | **6** | 0 |
| 211 pulso | 0 | 0 | — |

Zero roubo (vs 4/6 roubadas pela confidence-aware, §4d). Os co-fires de cauda do pulso foram
suprimidos pela v1 (correto — eram o spot) e as duplicatas de race limpas pelo guard.

**Edge conhecido (artefato de loop, documentar):** com o pulso repetindo a cada 18s no sim,
uma janela de evidência de ~126s contém ~7 tocadas → a cobertura AGREGADA do spot no clipe
sobe (medido 0.79) → quase-empate → duração vence e o spot fica com rows (4 no D1b). Em rádio
real (1 pulso por break) a cobertura do spot é ~0.18 e o pulso vence com folga; com 2 tocadas
na mesma janela ainda vence (0.9 ≥ 0.36×1.5). Só rotação absurda (>3 tocadas/2min) degrada —
monitorar na sombra do rollout.

**Conclusão:** o fix é **rollout de `DISAMBIG_BY_COVERAGE=true`** (com sombra e critérios de
aceite) + instrumentação. Spec e plano: ver o quadro no topo da §6.

## 5. Runbook de diagnóstico em prod (Dereck cola na VM; tudo read-only)

Preparação (uma vez por sessão SSH):

```bash
cd ~/radiocheck
DC="docker compose -f infra/docker/docker-compose.yml -f infra/docker/docker-compose.override.yml --env-file infra/docker/.env"
PSQL() { $DC exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB"'; }
```

### B0 — Estado do deploy e flags (contexto)

```bash
git log -1 --format='deployado: %h %cd %s' --date=format:'%d/%m %H:%M'
docker image inspect docker-api --format 'imagem api buildada: {{.Created}}'
grep -E 'DISAMBIG|AUDIT_ENABLED|MULTI_ATTRIBUTION|SHARING_MIN_SCORE' infra/docker/.env || echo "(nenhuma flag no .env = todas default OFF/ON de código)"
```

### B1 — O material do pulso (existe? ready? qual duração? shared residual?)

```bash
PSQL <<'SQL'
SELECT m.short_id, m.title, m.duration_seconds, m.fingerprint_status,
       m.fingerprint_hash_count, to_char(m.created_at,'DD/MM HH24:MI') AS criado,
       left(m.master_sha256,12) AS sha12,
       (SELECT count(*) FROM fingerprint_hashes fh WHERE fh.commercial_id=m.id AND fh.is_shared) AS hashes_shared
FROM materials m
JOIN clients c ON c.id=m.client_id
WHERE c.name ILIKE '%MILIUM%'
ORDER BY m.created_at DESC;
SQL
```

Interpretação: `fingerprint_status != 'ready'` ou `hash_count=0` → causa 3 (fecha o caso).
`duration_seconds` MUITO diferente de **5.74** → causa 5 (versão errada cadastrada).
`hashes_shared > 0` → anomalia grave (não deveria: <10s é pulado pelo sharing).

### B2 — Vínculo do pulso: campanha, datas, emissoras

```bash
PSQL <<'SQL'
SELECT ca.name AS campanha, ca.status, ca.start_date, ca.end_date,
       cardinality(cm.target_stations) AS n_emissoras,
       (SELECT string_agg(s.name, ' | ') FROM stations s WHERE s.id = ANY(cm.target_stations)) AS emissoras
FROM campaign_materials cm
JOIN campaigns ca ON ca.id=cm.campaign_id
JOIN materials m  ON m.id=cm.material_id
JOIN clients  c   ON c.id=m.client_id
WHERE c.name ILIKE '%MILIUM%' AND m.duration_seconds < 12
ORDER BY ca.start_date DESC;
SQL
```

Interpretação: as 2 emissoras das censuras TÊM que aparecer em `emissoras`, a campanha TEM que
estar `ativa` e 24/07 dentro de `start_date..end_date`. Qualquer falha aqui → causa 3.

### B3 — Threshold calibrado das emissoras (mata a 2ª janela?)

```bash
PSQL <<'SQL'
SELECT s.name, st.min_hashes, round(st.noise_p99::numeric,1) AS noise_p99,
       st.calibration_mode, to_char(st.updated_at,'DD/MM') AS atualizado
FROM station_thresholds st JOIN stations s ON s.id=st.station_id
WHERE st.station_id IN (
  SELECT unnest(cm.target_stations)
  FROM campaign_materials cm JOIN materials m ON m.id=cm.material_id
  JOIN clients c ON c.id=m.client_id
  WHERE c.name ILIKE '%MILIUM%' AND m.duration_seconds < 12);
SQL
```

Interpretação: `min_hashes ≥ 15-20` nessas emissoras torna a 2ª janela (parcial) do 5.7s muito
difícil → reforça causa 2.

### B4 — Rows do pulso em QUALQUER estado (inclusive invisíveis)

```bash
PSQL <<'SQL'
SELECT to_char(d.detected_at AT TIME ZONE 'America/Sao_Paulo','DD/MM HH24:MI:SS') AS quando_brt,
       s.name AS emissora, d.evidence_status,
       (d.retracted_at IS NOT NULL) AS retratada,
       round(d.confidence,3) AS conf, d.hash_count, d.audit_coverage
FROM detections d
JOIN stations s ON s.id=d.station_id
JOIN materials m ON m.id=d.commercial_id
JOIN clients c ON c.id=m.client_id
WHERE c.name ILIKE '%MILIUM%' AND m.duration_seconds < 12
  AND d.detected_at > now() - interval '15 days'
ORDER BY d.detected_at DESC LIMIT 60;
SQL
```

Interpretação: rows `audit_rejected` → causa 4. Rows `retracted` → dedup/desambiguação matou
DEPOIS de confirmar (variação da causa 1). **Zero rows** → morreu antes da row (causas 1-suppress,
2 ou 3).

### B5 — Forense do dedup v1 (a prova da causa 1)

```bash
PSQL <<'SQL'
SELECT to_char(ds.detected_at AT TIME ZONE 'America/Sao_Paulo','DD/MM HH24:MI:SS') AS quando_brt,
       s.name AS emissora, ds.suppressed_short_id, ds.kept_short_id,
       ds.suppressed_duration, ds.kept_duration,
       round(ds.suppressed_confidence::numeric,3) AS conf_suprimida,
       round(ds.kept_confidence::numeric,3) AS conf_mantida, ds.reason
FROM dedup_suppressions ds
JOIN stations s ON s.id=ds.station_id
WHERE ds.detected_at > now() - interval '15 days'
  AND (ds.suppressed_short_id IN (SELECT short_id FROM materials m JOIN clients c ON c.id=m.client_id
                                  WHERE c.name ILIKE '%MILIUM%')
    OR ds.kept_short_id      IN (SELECT short_id FROM materials m JOIN clients c ON c.id=m.client_id
                                  WHERE c.name ILIKE '%MILIUM%'))
ORDER BY ds.detected_at DESC LIMIT 60;
SQL
```

Interpretação: linha com `suppressed_short_id = <pulso>` no dia/hora das censuras = **causa 1
CONFIRMADA**. `conf_suprimida > conf_mantida` = a veiculação real foi morta em favor de um
false-confirm do spot longo. (Tabela existe desde o deploy do commit `21989c1`, 02/07 — se a
tabela não existir, o deploy é anterior e a causa 1 fica sem forense direto; use B6+B8.)

### B6 — A janela da censura: o que o sistema registrou de QUALQUER material Milium

```bash
PSQL <<'SQL'
SELECT to_char(d.detected_at AT TIME ZONE 'America/Sao_Paulo','DD/MM HH24:MI:SS') AS quando_brt,
       s.name AS emissora, m.short_id, left(m.title,40) AS material,
       m.duration_seconds AS dur, d.evidence_status,
       (d.retracted_at IS NOT NULL) AS retratada, round(d.confidence,3) AS conf
FROM detections d
JOIN stations s ON s.id=d.station_id
JOIN materials m ON m.id=d.commercial_id
JOIN clients c ON c.id=m.client_id
WHERE c.name ILIKE '%MILIUM%'
  AND d.detected_at BETWEEN '2026-07-24 15:30:00-03' AND '2026-07-24 16:05:00-03'
ORDER BY d.detected_at;
SQL
```

**Repita com a janela da 2ª censura** (Dereck: ajustar a data/hora — o arquivo WhatsApp é de
29/07 15:34, mas a tocada pode ser anterior; se souber o horário real, use ±20min).

Interpretação: se aparecer um **spot de 30s da Milium** às ~15:45 de 24/07 (qualquer
`evidence_status`) → a tocada do pulso foi **capturada e atribuída ao material errado** —
causa 1 confirmada com a variante "spot venceu e ficou".

### B7 — A emissora estava sendo capturada na hora?

```bash
PSQL <<'SQL'
SELECT s.name, e.event_type,
       to_char(e.event_at AT TIME ZONE 'America/Sao_Paulo','DD/MM HH24:MI:SS') AS quando_brt,
       e.duration_seconds
FROM stream_health_events e JOIN stations s ON s.id=e.station_id
WHERE e.station_id IN (
  SELECT unnest(cm.target_stations)
  FROM campaign_materials cm JOIN materials m ON m.id=cm.material_id
  JOIN clients c ON c.id=m.client_id
  WHERE c.name ILIKE '%MILIUM%' AND m.duration_seconds < 12)
  AND e.event_at BETWEEN '2026-07-24 12:00:00-03' AND '2026-07-24 20:00:00-03'
ORDER BY e.event_at;
SQL
```

### B8 — Logs do worker (se a retenção do docker ainda cobrir 24/07)

```bash
# score do pulso nas janelas (troque 000 pelo short_id do B1):
$DC logs --since 240h api 2>&1 | grep '"top_commercial_short_id":000' | grep -vE '"top_score":[0-9]($|,)' | tail -30
# supressões do dedup e rejeições de audit:
$DC logs --since 240h api 2>&1 | grep -E 'suppressed by version disambiguation|audit REJECTED|failed to resolve short_id' | tail -30
```

### B9 — Métricas cumulativas (Grafana/Prometheus pega a série do dia 24)

```bash
curl -s localhost:8080/metrics | grep -E 'radiocheck_match_disambiguation_total|radiocheck_audit_attempts_total|radiocheck_cooldown_possible_reair_total'
```

No Grafana: `increase(radiocheck_match_disambiguation_total{action="suppressed"}[1h])` em
24/07 14:00-17:00.

### B10 — A VM está de fato recebendo áudio dessas 2 emissoras? (hipótese nº1 do §3b)

Na UI: `/admin/overview` ou `/operations` → workers das 2 emissoras → **bytes recebidos,
reconnects, stall restarts**. Bytes ~0 ou reconnects altos = captura morta (provável bloqueio
do IP da VM pelo painel, precedente jun/2026). Via métricas:

```bash
curl -s localhost:8080/metrics | grep -E 'radiocheck_worker_(reconnects|bytes)' | grep -iE '<station_id_1>|<station_id_2>'
# e teste manual DA VM (o ban é por IP — testar do seu PC não vale):
ffprobe -v error -show_entries stream=codec_name,sample_rate,channels -user_agent "VLC/3.0.20 LibVLC/3.0.20" "https://stream6.loopert.com/303"
ffprobe -v error -show_entries stream=codec_name,sample_rate,channels -user_agent "VLC/3.0.20 LibVLC/3.0.20" "http://painel.sintonizar.tv.br/stream/meninablu"
```

Se o ffprobe da VM pendurar/timeout e do seu PC funcionar → **é bloqueio de IP**; a solução é
a do plano de junho (breaker + allowlist→proxy).

### B11 — O comercial passa NO STREAM? (stream ≠ antena, hipótese nº2 — crítica pra afiliada de rede)

Num horário em que a emissora confirme que o pulso vai ao ar, gravar o stream na VM e ouvir/
casar offline:

```bash
ffmpeg -y -user_agent "VLC/3.0.20 LibVLC/3.0.20" -i "<stream_url>" -t 900 -ac 1 -ar 16000 /tmp/capture-15min.wav
# levar o wav pro dev e rodar o harness offline (fingerprint/scripts) contra o master do pulso
```

Se o pulso NÃO estiver no áudio do stream enquanto a antena o toca → stream≠antena (inserção
local da afiliada não vai pro stream) — nenhum fix de matcher resolve; é caso de trocar a
fonte de captura dessa emissora.

## 6. O que precisa pra resolver DE VEZ

> **30/07 — spec e plano de implementação prontos e VALIDADOS por E2E (fase D, §4e):**
> [spec de design](../superpowers/specs/2026-07-30-pulso-subset-dedup-fix-design.md) ·
> [plano em 9 tasks](../superpowers/plans/2026-07-30-pulso-subset-dedup-fix.md).
> Resumo executivo: a arbitragem correta **já existe mergeada** (§18.2.2-v2 +
> co-fire guard, flag `DISAMBIG_BY_COVERAGE`) e foi validada com o par real nas duas
> direções; o plano instrumenta (métrica+alerta de supressão suspeita), avisa (UI <10s),
> commita o simulador Icecast e dá o rollout gated da flag com sombra de 48h. As seções
> A/B/C abaixo ficam como análise de fundo; o plano supersede a ordem sugerida.

Duas frentes: **(A) parar de perder a tocada do curto pro spot longo** (causa 1) e **(B) dar ao
curto uma chance justa de confirmar** (causa 2). Ambas já têm caminho mapeado no
[plano de áudio curto](../roadmap/short-audio-detection-plan.md) e nos achados do
[AUDIT-2026-07-02](../AUDIT-2026-07-02.md) — nada aqui é novo escopo, é executar o que está
especificado.

### A. Dedup/desambiguação não pode matar o curto às cegas (causa 1)

> ⚠️ **REVISADO em 30/07 após a validação §4d.** A recomendação original ("ligar
> `DISAMBIG_CONFIDENCE_AWARE=true`") foi **testada e reprovada**: conserta o caso pulso-
> standalone mas ROUBA as tocadas reais do spot (§4d, matriz). **NÃO ligar em prod.** O
> passthrough adicionado no compose fica (default false) pra quando a arbitragem for confiável.

1. **O fix definitivo: arbitragem do par curto⊂longo decidida pela EVIDÊNCIA, não no
   supervisor.** Design (aproveita máquina existente):
   - No dedup v1, quando o conflito é `duração_menor < duração_maior` **e** o par tem relação
     de containment (persistir a relação no shared-scan — hoje o par <10s é pulado sem
     registrar nada), **não suprimir sem row**: publicar os dois provisórios (ou marcar a
     supressão como "pendente de arbitragem").
   - Pós-audit, decidir pelo **`audit_coverage` do material LONGO** sobre o clipe (já
     computado): `≥ ~0.5` → o spot tocou → mantém spot, retrata pulso; `< ~0.3` → pulso
     standalone → mantém pulso, retrata/rejeita a row do spot. Faixa intermediária → fila de
     revisão/`ambiguous`. É a materialização do TODO R-B + achado G3 (âncora `MatchExtent`)
     com dado que o sistema já produz — a tabela da §4d mostra a separação: 0.183 vs 0.90+.
   - Alternativa no supervisor (mais cedo, menos robusta): usar o **offset/extent do match do
     longo** na confirmação (match só na cauda = false-confirm; match progredindo da cabeça =
     tocada real). Vale como heurística de desempate, não como árbitro único.
2. **Consertar o race da retração** (`supervisor: retract — no rows updated`): a retração que
   chega antes da row existir é perdida → dupla contagem (medido: 14/16 na §4). Guardar a
   retração pendente por (station, short_id, detected_at±tol) e aplicá-la quando o evidence
   service inserir a row (ou checar `dedup_suppressions`/verdict no INSERT).
3. **Sombra permanente:** alerta Prometheus quando
   `increase(radiocheck_match_disambiguation_total{action="suppressed"}[24h])` > N com
   `suppressed_confidence > kept_confidence` (a query B5 já detecta; virar alerta) — supressão
   de veiculação real não pode ser silenciosa.
4. Enquanto o item 1 não estiver em prod, **assumir e comunicar a limitação**: pulso sonoro
   <10s cadastrado junto com spot do mesmo cliente que o contém terá subcontagem nas emissoras
   onde o false-confirm do spot cruza o gate (borderline por emissora, §4c). A contagem manual
   (veiculações manuais em lote) é o paliativo operacional pro faturamento do pulso.

### B. Curto precisa de uma 2ª janela alcançável (causa 2)

4. **#1 Merge de bins adjacentes no histograma** (`engine.go`) — item de "maior ganho de recall
   sem migração" do plano, ainda `- [ ]`. Junta o pico dividido entre bins vizinhos; é o que
   faz a 2ª janela (parcial) do 5.7s passar o gate.
5. **Hop 1s seletivo pra material ≤10s** (**#7 da Fase 4** do plano): dobra o número de janelas
   candidatas do pulso de 2-3 pra 5-6. Custo ~+12pts CPU já dimensionado pro c3-highcpu-8.
6. **Revisar o piso de 2% (`MinScoreCoverage`)** pra janelas em que o material candidato é
   curto: 2% de uma janela densa (~16-40 hits) é MAIOR que o min_hashes e desproporcional
   quando o material só ocupa metade da janela. Proposta: aplicar o 2% sobre
   `min(hashes_janela, hashes_do_material_na_janela_teórica)` ou capar em min_hashes×2.
7. **#11 Calibração amostrar UniqueScore** (não Score total) — evita `min_hashes` inflado por
   catálogo denso numa emissora, que hoje pune exatamente o curto.

### C. Higiene que evita o próximo caso

8. **Aviso de material curto no upload** (UI + API): <10s não é proibido, mas o operador
   precisa saber que (a) fica fora da defesa shared-hash, (b) tem recall estruturalmente menor,
   (c) conflita com spots do mesmo cliente que o contenham. Hoje não há NENHUMA validação de
   duração ([materials.go:85](../../workers/internal/api/handlers/materials.go)).
9. **Validação §7.5 pós-geração no caminho de prod** (achado G9): `total_hashes==0` → status
   `failed`, não `ready`.
10. **Fix do simulador local** pro fluxo com ffprobe de pré-flight (2 conexões): a variante
    Icecast usada nesta investigação (§4) resolve; commitar em `scripts/radio-sim/`.
11. **Documentar a política**: "pulso sonoro / vinheta <10s do mesmo cliente de spots que a
    contêm" é o pior caso do sistema — enquanto A+B não estiverem em prod, cadastrar o pulso
    como material separado do mesmo cliente **produz supressão sistemática** (design atual).

### Ordem sugerida

| Passo | Esforço | Dependência |
|---|---|---|
| Rodar runbook §5 (Dereck) — confirma em prod o que o E2E provou (B5/B6/B4) | 15min | — |
| A2 fix do race da retração (para a dupla contagem já existente) | ~1 dia | — |
| A3 alerta de supressão suspeita | horas | — |
| **A1 arbitragem pós-audit por `audit_coverage` do longo** (o fix de verdade) | 3-5 dias | design §6.A.1 |
| A4 paliativo: veiculação manual pro pulso onde subcontar | operacional | — |
| C8/C9 avisos de material curto + validação §7.5 | horas | — |
| B4 #1 merge de bins (recall do curto em geral) | 1-2 dias | — |
| B5 hop seletivo + B6 gate 2% | 2-4 dias | B4 medido |

**NÃO fazer:** ligar `DISAMBIG_CONFIDENCE_AWARE` em prod (reprovado na validação §4d).

## 7. Referências

- [docs/roadmap/short-audio-detection-plan.md](../roadmap/short-audio-detection-plan.md) — o plano de recall de áudio curto (Fases 1-2 pendentes)
- [docs/AUDIT-2026-07-02.md](../AUDIT-2026-07-02.md) — achados G2 (dedup por duração), G9 (validação §7.5), E3/E4 (index reload)
- [docs/architecture/shared-hash-detection.md](../architecture/shared-hash-detection.md) + [sharing.go](../../workers/internal/sharing/sharing.go) — skip <10s e o design "curto perde por duração"
- [docs/architecture/evidence-audit.md](../architecture/evidence-audit.md) — §9.9, thresholds e o precedente de rejeição em massa (2026-05-17)
- [docs/architecture/version-disambiguation.md](../architecture/version-disambiguation.md) — §18.2.2
- Incidente irmão: [incident-2026-06-12-detection-recall-gaps.md](incident-2026-06-12-detection-recall-gaps.md) e [incident-2026-06-15-shared-hash-density-regression.md](incident-2026-06-15-shared-hash-density-regression.md)
- Harness offline: `fingerprint/scripts/{evaluate_detection,diagnose_censura,evaluate_short_audio}.py`
