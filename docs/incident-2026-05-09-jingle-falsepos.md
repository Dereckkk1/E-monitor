# Incidente AMBIENTAL JINGLE × AMBIENTAL 30 — sting compartilhado + saga F-108

Três episódios sucessivos da mesma família de causa raiz (compartilhamento
de áudio entre comerciais), revelando bugs distintos no algoritmo
`shared-hash` em cada iteração e culminando na versão definitiva
(`fix/sharing-bidirectional-subset`, F-108 v3).

> **Doc relacionado:** [incident-2026-05-12-pgdata-loss.md](incident-2026-05-12-pgdata-loss.md)
> — a saga deste incidente acabou expondo problemas operacionais
> (backup nunca rodou, comando `--force-recreate` destrutivo) que
> levaram à perda total do pgdata no dia 12. Os dois docs se
> referenciam mutuamente.
>
> **Algoritmo final:** [shared-hash-detection.md](shared-hash-detection.md).

## Resumo executivo

Sistema tinha defesa contra **uma** forma de áudio compartilhado (sting
no final de comerciais diferentes), mas existem três formas distintas
e cada episódio expôs uma:

1. **Episódio 1 (2026-05-09, 101 FM Xanxerê):** falso-positivo standalone
   de `AMBIENTAL JINGLE` quando `AMBIENTAL 30` tocou no ar. Defesa
   `is_shared` existia em código mas **não estava ativa** para esse
   comercial (fingerprint gerado antes do wiring do scan automático).
   Fix: `backfill-shared-hashes` no catálogo existente.

2. **Episódio 2 (2026-05-11, Oeste Capital):** `AMBIENTAL JINGLE` real
   foi **retratado** em favor de um falso-positivo de `AMBIENTAL 30`
   pela lógica de desambiguação — short_id menor venceu o empate de
   duração, apagando a detecção correta. Fix: o `is_shared` agora
   ativo (do Episódio 1) elimina o falso-positivo do AMB30 na raiz,
   antes do dedup precisar atuar.

3. **Episódio 3 (madrugada 2026-05-12, 89 FM + 105.7 Jaraguá):** o fix
   do Episódio 1 introduziu **falsos-negativos** em cortes de
   versão (JINGLE ROGGA VERÃO 30 inteiramente contido em VERÃO 60)
   e vinhetas curtas (PULSO SONORO 7s). Saga de 3 iterações no
   algoritmo (F-108 v1 → v2 → v3) até convergir.

## Episódio 1 — 101 FM Xanxerê (2026-05-09)

Falso-positivo de `AMBIENTAL JINGLE` em **101 FM (Xanxerê / SC)**
confirmado pelo matcher em **2026-05-09 18:31:35 -03**
(`detection_id = aa0fbd27-c19f-4cee-9a88-dcb0f4484257`,
`confidence = 0.4615`).

### Causa raiz

`AMBIENTAL 30` e `AMBIENTAL JINGLE` compartilham ~6,25s de áudio
idêntico no final (sting). A defesa `is_shared` (commit `9db258b`)
flageia hashes nessa região para que `UniqueScore` não seja creditado
ao comercial "errado" quando o sting toca.

Mas o fingerprint do JINGLE em prod foi gerado em **2026-05-06**, antes
do commit `3d3bea4` (May 9 11:58 -03) que ligou o NATS subscriber
`fingerprint.shared-scan`. O wiring só pega *novos* uploads — catálogo
pré-existente exigia `backfill-shared-hashes` manual.

Com `is_shared = false` em 100% dos hashes do JINGLE, `UniqueScore == Score`.
Quando AMB30 tocou, o matcher acumulou score do JINGLE só do sting
compartilhado, atingiu `temporal_coverage = 0.462` em ~14s e confirmou.

### Por que aparecia `hash_count=0` na UI

O commit `8c87c43` (2026-05-09 21:23 -03) adicionou os campos forenses
(`hash_count`, `match_start_offset_ms`, etc.) no payload `DetectionEvent`
NATS. A detecção é de ~3h antes do commit. O worker daquela versão não
enviava esses campos; o evidence service inseriu a linha com defaults (0).
Não era bug ativo no momento — qualquer detecção anterior a `8c87c43`
tinha o mesmo padrão.

### Fix aplicado

```bash
docker compose exec api sh -c 'backfill-shared-hashes --dsn "$DATABASE_URL"'
docker compose restart api  # reload do índice em memória
```

Após o backfill, JINGLE passou a ter ~36% dos hashes flagged por
variant (a região do sting). Detecção `aa0fbd27` retratada manualmente
com `retraction_reason = "false_positive_shared_sting_with_AMBIENTAL_30"`.

## Episódio 2 — Oeste Capital - FM (93.3) (2026-05-11)

Reportado pelo usuário logo após a aplicação do fix do Episódio 1,
**antes do `docker compose restart api`** reciclar os workers em
memória. A linha do tempo das três detecções na campanha em Oeste
Capital nesse dia:

| Horário (UTC-3) | Comercial | Detection ID | hash_count | confidence | Status |
|-----------------|-----------|--------------|-----------|------------|--------|
| 08:27:59 | AMBIENTAL 30 | (a investigar) | 16 | 0,20 | mantida |
| 08:30:32 | **AMBIENTAL JINGLE** | cd4840ff-… | 98 | 0,21 | **retratada (incorretamente)** |
| 08:30:45 | AMBIENTAL 30 | 272bd95e-… | 21 | 0,20 | mantida (deveria ser retratada) |

### O que rolou

1. Às 08:30:32 o `AMBIENTAL JINGLE` tocou no ar. Matcher confirmou
   corretamente — `hash_count=98` é compatível com playback real.
2. 13s depois (08:30:45), o JINGLE ainda tocando, o matcher acumulou
   21 hashes do `AMBIENTAL 30` — vindos quase todos do sting
   compartilhado de ~6,25s no final do JINGLE. Como o backfill
   `is_shared` tinha sido aplicado no banco mas o worker em memória
   ainda não havia recarregado o índice, `UniqueScore == Score` e o
   AMB30 confirmou falsamente.
3. Os dois detections têm janelas de veiculação sobrepostas.
   Supervisor acionou a desambiguação por sobreposição
   ([version-disambiguation.md](version-disambiguation.md)) e — como
   ambos são 30s — empate de duração resolvido por `short_id`. AMB30
   (`short_id=11`) venceu JINGLE (`short_id=12`).
4. O JINGLE real foi retratado, o falso-positivo AMB30 ficou
   registrado como veiculação válida.

### Validação acústica

`workers/internal/match/diag_falsepos3_test.go` roda as duas capturas
contra ambos os masters reencodados em variant-0:

| Capture | JINGLE master | AMB30 master |
|---------|---------------|--------------|
| `272bd95e` (rotulada como AMB30) | **30 hits** ≥5 | 6 hits ≥5 |
| `cd4840ff` (rotulada como JINGLE) | **29 hits** ≥5 | 9 hits ≥5 |

JINGLE bate ~4× mais que AMB30 em ambas as capturas — consistente com
playback real do JINGLE. Os 6-9 hits do AMB30 são do sting
compartilhado.

### Cleanup aplicado

```sql
-- Reverte a retração indevida
UPDATE detections
SET retracted_at = NULL,
    notes = jsonb_set(notes, '{retraction_undo_reason}',
                      '"jingle_was_real_play_amb30_was_shared_sting_artifact"')
WHERE id = 'cd4840ff-b4d9-44c9-ac9c-e0be675aee5e';

-- Retrata o falso-positivo AMB30 do mesmo evento
UPDATE detections
SET retracted_at = NOW(),
    notes = jsonb_set(COALESCE(notes,'{}'::jsonb), '{retraction_reason}',
                      '"false_positive_shared_sting_with_AMBIENTAL_JINGLE"')
WHERE id = '272bd95e-8669-43c7-9899-33c27029bb9b';
```

### Por que o fix de `is_shared` sozinho não bastou aqui

A defesa `is_shared` flageia hashes **no banco** mas o índice em
memória dos workers não recarrega sozinho. Sem o `docker compose
restart api` (ou um `nats pub index.reload` equivalente), o matcher
continua usando o índice antigo sem flags — mesmo cenário do
Episódio 1.

## Episódio 3 — Falsos-negativos na madrugada (2026-05-12)

Vendor antigo registrou veiculações que o nosso sistema não pegou:

- **JINGLE ROGGA VERÃO 30 em 89 FM Joinville** às 07:02:43 -03
- **PULSO SONORO em 105.7 Jaraguá do Sul** às 06:55:56 -03

Ambos da campanha Rôgga. O sistema estava cego para esses dois
comerciais. Outras detecções (`ROGGA URBAN BAVIERA` em 89 FM às
06:42:43) confirmaram normalmente, então não era falha de stream.

### Diagnóstico

Query do `is_shared` revelou:

| Comercial | duração | shared / total | shared_pct |
|-----------|---------|----------------|------------|
| AMBIENTAL 30 | 30s | 6189 / 17083 | 36,2% (sting com JINGLE — correto) |
| AMBIENTAL JINGLE | 30s | 7133 / 19481 | 36,6% (idem) |
| **JINGLE ROGGA VERÃO 30** | **30,8s** | 18392 / 18392 | **100,0%** ❌ |
| **JINGLE ROGGA VERÃO 60** | **60,2s** | 20162 / 38336 | **52,6%** ❌ |
| **RÔGGA PULSO SONORO** | **7,4s** | 3006 / 4508 | **66,7%** ❌ |

Causa: `MarkSharedHashes` (do Episódio 1 + 2 fix) não distinguia entre
**sting compartilhado** (~6s entre comerciais diferentes) e **subset**
(corte 30s tomado verbatim de um master 60s). Tratava ambos igual e
flageava tudo.

Para VERÃO 30 ⊂ VERÃO 60: 100% dos hashes flagged → `UniqueScore`
sempre zero → state machine nunca avança out of `Idle`. Mesma coisa
pro VERÃO 60 quando o 30 toca (a metade dos hashes que coincide é
toda flagged). Resultado: **nem o 30 nem o 60 confirmam quando tocam
no ar**.

PULSO (7,4s) tinha um problema relacionado mas distinto — ver F-108 v3
abaixo.

### Saga F-108: v1 → v2 → v3

A correção atravessou 3 iterações. Cada uma resolveu um subproblema
e expôs o próximo. Estado final em
[`workers/internal/sharing/sharing.go`](../workers/internal/sharing/sharing.go).

#### v1 — `window-fraction` de um lado (descartada)

Ideia inicial: classificar par `(A, B)` por fração de janelas do scan
de A que bateram em B. Se ≥ 50%, é subset → não flag.

```go
fraction := windowsWithHits / totalWindows
if fraction >= 0.5: skip flagging
```

Funciona para VERÃO 30 scaneando VERÃO 60 (100% das janelas do 30
batem no 60 → subset). **Mas falha para pares assimétricos:** quando
PULSO (7s) é contido em X (30s), o scan de X só vê PULSO em ~15% de
suas 27 janelas (só naquela região que coincide). Classificado como
"sting" → flag normal → PULSO heavily flagged.

#### v2 — frame coverage bidirecional

Mudança: olhar **cobertura de frames dos dois lados** do par.

```go
ownCov   = frames de A cobertos / total de A
otherCov = frames de B cobertos / total de B
score    = max(ownCov, otherCov)
if score >= 0.5: skip flagging
```

Calibração:
- VERÃO 30 ⊂ VERÃO 60: own=100%, other=50% → max=100% → subset ✓
- PULSO (7s) ⊂ X (30s): own=100%, other=23% → max=100% → subset ✓
- AMB30/JINGLE sting: own=20%, other=20% → max=20% → sting ✓

Funcionou para VERÃO 30/60. **Mas PULSO continuou 66,7% flagged.**

Investigação revelou: PULSO compartilhava pedaços com **múltiplos**
comerciais Rôgga (a campanha inteira tem assinatura sonora parecida).
Cada par individual ficava abaixo do limiar (sting legítimo), mas
matches caíam em fatias finas diferentes de PULSO. A **união cumulativa
across pairs** cobria 66,7% de PULSO.

Pior: matches do scan de X (30s) em PULSO produziam `xRange` calculado
com OffsetFrames que clampava em `[0, 7]` frames quando o match caía
perto da borda de PULSO. `otherCov_PULSO = 7/57 = 12%` (baixo) e
`ownCov_X` também baixo → par classificado como sting → flag. Várias
fatias finas de várias X's somavam até 67% de PULSO.

Análise: comerciais < 10s não têm resolução de janela suficiente. UMA
janela de 4s já cobre > 50% de um comercial de 7s. A defesa
`shared-hash` simplesmente não consegue distinguir subset de sting
nesse regime.

#### v3 — skip de comerciais curtos (versão final)

Adicionado ao v2: **pular shared-hash flagging completamente quando
qualquer lado do par tem duração < 10s.**

```go
const MinShareableDurationSeconds = 10.0  // ≈ 78 frames

if report.ownTotalFrames < MinShareableDurationFrames:
    return out  // scan inteiro pulado

for otherID, scan := range perOther:
    if scan.otherTotalFrames < MinShareableDurationFrames:
        continue  // pula par
    // ... bidirectional check do v2 ...
```

Trade-off: comerciais curtos perdem a defesa `shared-hash`. Mitigado
pela camada de [version-disambiguation](version-disambiguation.md) no
supervisor — se PULSO false-confirmar enquanto X toca, supervisor
retrata PULSO pela regra de maior duração. Quando PULSO toca sozinho,
confirma normalmente.

**Validação:** 8 testes unitários em
[`sharing_test.go`](../workers/internal/sharing/sharing_test.go),
incluindo `TestClassifyAndFilter_AsymmetricSubset_15sInside30s`
(bidirectional) e `TestClassifyAndFilter_ShortCommercialOwnScanSkipped`
(skip de curto).

### Branch + deploy

Branch [`fix/sharing-bidirectional-subset`](https://github.com/Dereckkk1/Radiocheck/tree/fix/sharing-bidirectional-subset)
(commits `290aea6` → `f7e289d`). Procedimento de deploy depois do
recadastro do catálogo:

```bash
# Reset is_shared globalmente (estado anterior veio dos algoritmos v1/v2)
docker compose -f infra/docker/docker-compose.yml exec -T postgres \
  psql -U radiocheck -d radiocheck \
  -c "UPDATE fingerprint_hashes SET is_shared = false WHERE is_shared = true;"

# Build + recreate da api com algoritmo v3 (--no-deps mandatório!)
docker compose -f infra/docker/docker-compose.yml build api
docker compose -f infra/docker/docker-compose.yml up -d --force-recreate --no-deps api

# Re-roda backfill (idempotente, agora com algoritmo correto)
docker compose -f infra/docker/docker-compose.yml exec api \
  sh -c 'backfill-shared-hashes --dsn "$DATABASE_URL"'

# Verificação
docker compose -f infra/docker/docker-compose.yml exec -T postgres \
  psql -U radiocheck -d radiocheck -c "
    SELECT c.short_id, c.title, c.duration_seconds,
           ROUND(100.0 * COUNT(*) FILTER (WHERE fh.is_shared) / NULLIF(COUNT(*),0), 1) shared_pct
    FROM commercials c JOIN fingerprint_hashes fh ON fh.commercial_id = c.id
    GROUP BY c.id, c.short_id, c.title, c.duration_seconds
    ORDER BY c.title;
  "
```

Esperado:
- AMBIENTAL 30/JINGLE: ~36% (sting, mantém)
- JINGLE ROGGA VERÃO 30/60: **0%** (subset, pulado)
- RÔGGA PULSO SONORO: **0%** (< 10s, pulado)

## Lições

1. **Defesas no código são insuficientes sem operação de catálogo.**
   Mudanças estruturais em fingerprint (como o `is_shared` em
   `9db258b`) exigem **backfill explícito** no plano de deploy. Sem
   isso, código novo opera sobre dado velho silenciosamente.

2. **Backfill no banco ≠ defesa ativa no matcher.** Os workers só
   usam o flag após recarregar o índice em memória. Janela entre
   backfill SQL e index reload é vulnerável. F-105 (publicar
   `index.reload` automaticamente no fim do backfill) pendente.

3. **Desempate por `short_id` é frágil.** A regra foi desenhada para
   versões do mesmo conceito (corte 30s/60s) onde o ranking faz
   sentido acústico. Para comerciais distintos com mesma duração,
   ela vira loteria ordenada — pode escolher o errado em 100% das
   vezes (Episódio 2).

4. **Algoritmo de "subset detection" precisa olhar dos dois lados
   E descartar comerciais muito curtos.** Cobertura de janelas de um
   lado só (v1) ignora a relação assimétrica. Cobertura bidirecional
   (v2) ignora o caso fragmentado. Skip por duração mínima (v3)
   abandona casos onde a resolução não é suficiente para classificar.

5. **A defesa de dedup do supervisor cobre o gap quando o
   `shared-hash` é insuficiente** — pra PULSO em particular, se
   tocar simultâneo com a campanha Rôgga inteira, a regra de maior
   duração retrata corretamente.

## Follow-ups derivados

- **F-100..F-103** — anotados em revisões anteriores deste doc,
  pendentes.
- **F-104 — *RESOLVIDO (na prática)* via F-108 v3.** A regra de
  disambiguação por overlap de hashes não foi implementada
  separadamente, mas o efeito do skip de comerciais curtos +
  bidirectional check cobre os casos que motivaram o follow-up.
- **F-105 — Trigger `index.reload` automático ao final do
  `backfill-shared-hashes`.** Pendente. Hoje o CLI termina sem
  avisar workers; exige `docker compose restart api` manual. Foi
  exatamente a janela que permitiu o Episódio 2 acontecer.
- **F-108 — *RESOLVIDO* — Detecção bidirecional de subset + skip de
  curtos em `sharing.MarkSharedHashes`.** Versão final em
  [`sharing.go`](../workers/internal/sharing/sharing.go) commits
  `290aea6` (v2) + `f7e289d` (v3). Documentação em
  [shared-hash-detection.md](shared-hash-detection.md).

## Doc relacionado

- [shared-hash-detection.md](shared-hash-detection.md) — algoritmo
  em sua forma final pós-incidente, com casos de teste.
- [version-disambiguation.md](version-disambiguation.md) — camada de
  defesa complementar (dedup por duração no supervisor).
- [incident-2026-05-12-pgdata-loss.md](incident-2026-05-12-pgdata-loss.md)
  — incidente do dia seguinte: a deploy do fix do Episódio 1
  (`backfill-shared-hashes` + restart api) acabou se desdobrando
  num `--force-recreate` mal-feito que destruiu o pgdata. O backup
  que deveria nos salvar nunca tinha rodado.
- `CLAUDE.md` §4.6 — regra crítica derivada deste incidente sobre o
  conflito `initdb.d` + migrate service.
