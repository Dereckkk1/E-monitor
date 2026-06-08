---
status: planejado
ultima-verificacao: 2026-06-03
codigo-relacionado:
  - fingerprint/fingerprint/generator.py
  - fingerprint/fingerprint/broadcast_sim.py
  - fingerprint/scripts/evaluate_detection.py
  - workers/pkg/audio/peaks.go
  - workers/pkg/audio/hashes.go
  - workers/internal/match/engine.go
  - workers/internal/match/coverage.go
  - workers/internal/match/statemachine.go
  - workers/internal/calibration/job.go
  - workers/internal/supervisor/disambiguation.go
  - workers/internal/audit/auditor.go
  - workers/internal/ingestor/worker.go
---

# Plano de Implantação — Recall de Áudios Curtos (5/10/15s)

Plano executável derivado da [auditoria](short-audio-detection-audit-2026-06-02.md), **corrigido** após verificação de coerência contra o pipeline real. Objetivo: subir recall de 5/10/15s mantendo FP ~0.

> 📌 **Atualização 2026-06-08 — o que de fato foi feito.** O diagnóstico com áudio real (air-checks perdidos do Asaas) mostrou que o problema era **recall de áudio curto** (Spot 5/15s sub-detectados), não atribuição/EAD-3. O fix **#2 (peak-picking ~4× mais denso)** foi implementado, validado e está em produção (commit `3bd7178`). O hardware foi migrado para **`c3-highcpu-8` (8 vCPU, 16 GB)** — então as análises de CPU abaixo (feitas no `c3-standard-4` / 4 vCPU @ 68%) ficam como **registro histórico do porquê #2 venceu #9**; com 8 vCPU o #2 (e até o nível agressivo) cabem folgado (~40%). Migração de re-fingerprint: [refingerprint-density-migration.md](../operations/refingerprint-density-migration.md).

## Correções de coerência (o que a auditoria errou e o que sai do escopo)

A auditoria foi Go-cêntrica. O pipeline de fingerprint de **produção é Python** (`fingerprint/fingerprint/main.py`). Correções:

| Item da auditoria | Status real | Ação no plano |
|---|---|---|
| **#5 variantes broadcast "ausentes"** | **JÁ EXISTEM** — `broadcast_sim.py` gera 5 variantes (0: comp leve+AAC96k; 1: AAC64k+limiter; 2: HE-AAC48k; 3: +ruído; 4: AM lowpass3500+comp pesado+ruído); `loader.go` carrega todas. | Vira **calibrar/validar** as variantes (Fase 4), não construir. |
| **#6 skew de pré-processamento** | **NÃO EXISTE** — `generator.py` usa `HPF100+RMS-20dB` = idêntico ao live. | **Descartado.** |
| **"Sem harness de recall" (crítico)** | **JÁ EXISTE** — `evaluate_detection.py` mede recall sobre 20 perfis de degradação. | Fase 0 = **estender**, não construir. |
| #18 banda 4→7 kHz | crítico: overrated (banda de baixa SNR pós-codec) | **Cortado.** |
| #19 whitening, #14 gate por banda, #15 sub-window, #16 DeltaBin 2→1 | 2ª ordem / dependem de hop que não faremos | **Diferidos** (backlog, não no plano principal). |
| #12 score normalizado | metade já existe (`min_hashes` já é noise-derived) | só o `input_confidence` query-normalized entra, como melhoria menor na Fase 3. |

**Acoplamento crítico de migração:** as constantes de DSP/hash vivem em **3 lugares que mudam juntos**: `generator.py` (fonte do índice) + `pkg/audio/*.go` (live) + `evaluate_detection.py`. Qualquer mudança em peaks/hash (#2, #8) exige editar os três + re-fingerprint de todo o catálogo (re-publicar `fingerprint.generate`) + regenerar o golden de `stft_regression_test.go`, em **deploy atômico** (índice versionado).

---

## Decisão: hop STFT (#9) — MANTER a otimização, NÃO reverter

> **Veredito: mantém o commit `790dd34` (passada única, −48% CPU). NÃO fazer o #9 (hop 2048→512).**

Justificativa, com o que sabemos do sistema:
1. **São levers ortogonais, não o mesmo.** `790dd34` = "não rodar o pass 2-3× por janela" (pura eficiência, **zero custo de recall**). #9 = "fazer cada pass 4× mais pesado". Manter `790dd34` é gratuito e ainda **reduz** o custo do #9 caso ele fosse feito.
2. **#9 é o pior ROI da auditoria.** 4× CPU para densidade que o **#2 (encolher o raio temporal do max-filter) entrega medido a ~1,4-1,6×** — porque #2 deixa o STFT (38% do pass) intacto e ainda barateia o peak-picking por célula.
3. **Não cabe no 4 vCPU.** Medido: matcher ≈ 14-18% do box; #9 (×4) → +45-55 pts → **~110-125%**. Estouraria. #2 → ~78-80%, cabe.
4. **Mesma migração, economia muito pior.** #9 exige o mesmo re-fingerprint Python+Go que o #2, mas com CPU 3× pior.

**Regra:** densidade vem do **#2**. Só reconsiderar `hop=1024` (2×, meio-termo) se a Fase 0 + harness provarem que #2 saturou e ainda falta recall — e nesse caso já num c3-standard-8.

---

## Fases

### Fase 0 — Medir (torna tudo falsificável) · ~1 semana · 70% já existe
**Objetivo:** baseline e harness antes de tunar nada.
- [x] **Harness de varredura por duração** criado: `fingerprint/scripts/evaluate_short_audio.py` (5/10/15/30s × 20 perfis, índice real de 5 variantes, gates do live `score>=5` e `cov>=0.15`). Baseline sintético em [short-audio-baseline-2026-06-03.md](short-audio-baseline-2026-06-03.md): **5s=95%/90%, 10-30s=100%** — só o perfil "AM combo" escapa em 5s. ⚠️ Sintético é ~2× mais denso que anúncio real → recall real de 5s deve ser menor.
- [ ] Rodar o harness com **masters REAIS** na VM (`--master /mnt/data/audio-refs/<spot>.mp3`) — o número que importa.
- [ ] **Validação live-fiel (Go)** para #1/#4/#11: o harness Python mede o TETO do matcher (passada única), **não** modela a state machine de 2 janelas, calibração nem vote-splitting. Construir testes Go de integração que exercitem a confirmação live.
- [ ] `pprof` de 60s na VM → split real **ffmpeg vs matcher vs Go** (valida os ~14-18% do matcher).
- [ ] **Congelar baseline:** recall por duração (real) + FP atual + CPU% atual.

**Go/No-Go:** harness reproduz ≥1 miss conhecido de 5s ✅; baseline real registrado. Sem isso, nenhuma fase seguinte é mensurável.

### Fase 1 — Ganhos grátis, sem migração, FP-safe · 1-2 semanas
**Objetivo:** máximo recall/correção a ~0 CPU e zero re-fingerprint.
- [ ] **#1 Merge de bins adjacentes** no histograma (`engine.go`): somar `count[bin-1..+1]` ao escolher o pico. + **tie-break determinístico** (eliminar dependência da ordem de iteração do `map`). *Maior ganho de recall do plano sem migração.*
- [ ] **#11 Calibrar ruído em `UniqueScore`** (`worker.go`/`job.go`): amostrar `topUniqueScore` em vez de `Score` total. Destrava curto em emissora com jingle compartilhado.
- [ ] **#3 Desambiguação por cobertura medida/overlap** (`disambiguation.go`, TODO R-B): corrige a **misatribuição** 30s-retrai-15s. *Correção, não só recall.*
- [ ] **#10A Recortar PCM do audit** para a janela do match (`auditor.go`/`service.go`): −20-25× CPU do audit, mais preciso, para de descartar 5s legítimo.

**Go/No-Go:** cada item validado no harness (recall ↑ ou neutro, **FP=0**); `pprof` confirma CPU estável.

### Fase 2 — Reativar as defesas mortas (correção arquitetural) · 2-3 semanas
**Objetivo:** tornar a cobertura uma defesa real → pré-requisito para baixar threshold com segurança.
- [ ] **#4 Consertar `coverage.go`:** remover o piso de 32 frames; trocar **wall-clock por tempo de áudio observado** (fecha o vetor de FP por stall/GC/reconexão); **re-cablear o offset** hoje descartado em `Add(_ int)`; confirmar por **progressão de offset linear** (SPRT/Panako) — **NÃO** por offset constante (refutado: o offset deriva ~8 bins/janela).
- [ ] Tornar `StateUncertain` alcançável (corrigir os intervalos vazios em `statemachine.go:179-180`).

**Go/No-Go:** cobertura volta a discriminar (anúncio real confirma; ruído sustentado não); FP=0; latência de confirmação < 10s.

### Fase 3 — Densidade + qualidade do hash (migração atômica Python+Go) · 3-4 semanas
**Objetivo:** atacar a matemática do orçamento de hash do curto. **Uma** migração coordenada.
- [ ] **#2 Encolher raio temporal do max-filter** (`neighborFrames 8→3`) — melhor ROI medido (2,5-4× hashes a ~1,4-1,6× CPU). Editar **`generator.py` E `pkg/audio/peaks.go` em lockstep**.
- [ ] **#8 Corrigir aliasing** (`&0x1FF → &0x3FF`) + rebalancear bits — no **mesmo** patch, em `generator.py` E `pkg/audio/hashes.go`.
- [ ] Adicionar **versão de formato ao índice** (pré-requisito de rollback seguro).
- [ ] **Re-fingerprint de todo o catálogo** (re-publicar `fingerprint.generate`) + cutover atômico + **recalibrar threshold** pela PDF de ruído + regenerar golden de `stft_regression_test.go`.
- [ ] **NÃO #9 (hop).** Manter `790dd34`.
- [ ] Só **depois**, com o harness mostrando FP=0: **baixar `MatchThreshold` 5→4** (o ganho de recall do curto). Opcional: `input_confidence` query-normalized.

**Go/No-Go:** recall de curto ↑ no harness; FP=0; CPU no envelope (`pprof`, alvo < 82%); rollback testado (índice versionado). **Rollout parcial = match quebrado** — tratar como migração de 1ª classe (CLAUDE.md §4).

### Fase 4 — 2º estágio + calibração de variantes (condicional) · 4+ semanas
**Objetivo:** recall de curto **degradado** + rede de segurança para baixar threshold.
- [ ] **#13 GCC-PHAT** como verificador de 2º estágio no `StateUncertain` (determinístico, sem GPU) — ou religar o **CLAP** que já existe em `clap-verifier/`. Roda só nos borderline.
- [ ] **Calibrar as 5 variantes broadcast existentes** contra processamento real de emissora (via `evaluate_detection.py`) — **validar/ajustar as cadeias**, não implementar (já existem).
- [ ] **#7 Hop live 1s SELETIVO** (campanhas com material ≤10s), com a trava de offset da Fase 2.
- [ ] Condicional, medir antes: **#17 multi-rate** (só se a Fase 0 mostrar prevalência real de time-stretch).

**Go/No-Go:** cada item gated por harness + `pprof`.

---

## Gatilho de hardware — ✅ FEITO (c3-standard-4 → c3-highcpu-8, 2026-06-08)

Migrado para **`c3-highcpu-8` (8 vCPU, 16 GB)** ao subir o #2 em prod (o #2 dobra o matcher por janela). Escolha por `highcpu` (16 GB) em vez de `standard` (32 GB): a carga é **CPU-bound** e o índice de fingerprint é **leve** (~40 MB mesmo com a densidade). Com 8 vCPU, a saturação de CPU sobe pra ~290 emissoras — então a **meta de 200 cabe** (o ffmpeg, não o algoritmo, é a parede).

**Próximo gatilho (futuro):** se ao escalar pra ~200 emissoras a **RAM** apertar (os ~200 FFmpeg + PostgreSQL, **não** o índice), migrar pra `c3-standard-8` (32 GB). Custo do C3-highcpu-8 ≈ ~$305/mês on-demand (verificar no billing).

## Itens rejeitados (não fazer)
Offset constante entre janelas (refutado), quad-hash/Panako (0% @1-2s), janela adaptativa por duração (quebra os longos), fan-out 8→10 (não é o gargalo), #9 hop STFT (pior ROI), #18 banda 4→7 kHz (overrated). Detalhes na [auditoria](short-audio-detection-audit-2026-06-02.md).

## Resumo de custo por fase

| Fase | CPU adicional | Migração? | RAM | Cabe no 4 vCPU @ 68%? |
|---|---|---|---|:---:|
| 0 Medir | ~0 | não | — | ✅ |
| 1 Grátis | ~0 (#10A reduz) | não | — | ✅ (~67%) |
| 2 Defesas | ~0 | não | — | ✅ |
| 3 Densidade | +6-12 pts | **sim (Python+Go)** | +trivial | ✅ (~78-80%) |
| 4 2º estágio | +2-5 pts | parcial | +trivial | ⚠️ ~82% (ok) |
