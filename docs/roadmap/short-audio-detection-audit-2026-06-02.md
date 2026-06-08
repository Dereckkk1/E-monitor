---
status: planejado
ultima-verificacao: 2026-06-02
codigo-relacionado:
  - workers/pkg/audio/stft.go
  - workers/pkg/audio/peaks.go
  - workers/pkg/audio/hashes.go
  - workers/pkg/audio/preprocess.go
  - workers/internal/match/engine.go
  - workers/internal/match/coverage.go
  - workers/internal/match/statemachine.go
  - workers/internal/ingestor/worker.go
  - workers/internal/calibration/job.go
  - workers/internal/supervisor/supervisor.go
  - workers/internal/supervisor/disambiguation.go
  - workers/internal/audit/auditor.go
  - workers/internal/fingerprint/audio.go
  - workers/internal/neural/client.go
---

# Auditoria do Detector — Recall de Áudios Curtos (5/10/15s)

> Auditoria multi-agente (6 pesquisas web SOTA + 10 dimensões de código auditadas e **verificadas adversarialmente** + crítico de completude). Foco: **aumentar recall/robustez de anúncios de 5/10/15s mantendo falso positivo ~0**. Honestidade brutal: notas são as **ajustadas pelo verificador cético**, não as do agente que propôs.

> ⚠️ **CORREÇÕES PÓS-AUDITORIA (2026-06-03) — a auditoria foi Go-cêntrica e errou 3 pontos.** O pipeline de fingerprint de **produção é Python** (`fingerprint/fingerprint/main.py`, subscriber NATS `fingerprint.generate`); o Go `fingerprint/audio.go` é legado/morto. Consequências:
> - **#5 (variantes broadcast "não implementadas") está ERRADO** — `broadcast_sim.py` já gera **5 variantes** (0-4) e o loader Go (`loader.go`) carrega todas. A "estratégia-mãe" **já existe**. O item vira **calibrar/validar** as variantes, não implementá-las.
> - **#6 (skew de pré-processamento) está ERRADO** — `generator.py` usa `HPF100 + RMS-20dB`, **idêntico ao live**. Não há skew. **Item descartado.**
> - **"Sem harness de recall" (crítico) está ERRADO** — `scripts/evaluate_detection.py` já mede recall sobre **20 perfis de degradação**. A Fase 0 é **estender**, não construir.
> - **Migração de #2/#8/#9** muda constantes em **TRÊS lugares em lockstep**: `generator.py` (Python, fonte do índice) + `pkg/audio/*.go` (Go live) + `evaluate_detection.py`.
>
> Plano executável corrigido: [short-audio-detection-plan.md](short-audio-detection-plan.md).

## TL;DR — a história real

O sistema hoje é **agressivo em recall de curtos por acidente, não por design**, e quase todas as suas defesas anti-FP estão **inertes ou mortas** exatamente na faixa de 5–21s:

1. **A cobertura temporal — descrita no código como "a principal defesa anti-FP" — é inerte para qualquer anúncio até ~27–40s.** O estimador `coverage.go:52` soma um piso fixo de 32 frames (4.096s). Com `MinTemporalCoverage=0.15`, um anúncio de 5s dá cobertura 0.82 em **uma** janela; 10s → 0.41; 15s → 0.27 — todos confirmam em **2 janelas** sem acumular nenhuma evidência temporal real.
2. **A verificação neural (§10) é código morto em produção.** As guardas de `StateUncertain` (`statemachine.go:179-180`) exigem `cov ∈ [0.4, 0.15)` — intervalo **vazio** com `minTemporalCoverage=0.15`. O worker nunca chama `ResolveNeural`. O sidecar CLAP só é health-checkado, nunca usado.
3. **Resultado:** para um curto, **toda** a barreira anti-FP é `UniqueScore ≥ 5` (threshold absoluto) + exclusão de shared-hash. Para segurar FP, a calibração tende a **subir** esse threshold em emissoras ruidosas — e cada +1 no threshold corta desproporcionalmente o recall de 5s. **A inércia da defesa temporal é a causa-raiz de o threshold precisar ser alto, e o threshold alto é o que mata o curto.**
4. **Não existe nenhum harness que meça recall@5/10/15s sobre stream degradado** (AAC 32-64kbps + compressor multibanda + locução). Sem essa curva, **toda** recomendação de tuning abaixo é não-falsificável. Esse é o **primeiro** item que um auditor sério exigiria.

E há um **bug de correção, não só de recall**: a desambiguação de versões escolhe a **maior duração nominal**. Como a cobertura virou inerte, um corte de 30s **confirma a partir de ~4s** de conteúdo compartilhado com o corte de 15s; quando só o 15s vai ao ar, **os dois confirmam** e o sistema **retrai o 15s legítimo** e publica um 30s que não tocou (falso-positivo **e** falso-negativo simultâneos).

---

## Tabela consolidada (ordenada por recomendação verificada)

| # | Como tá | Resultado (limitação p/ curto) | Melhoria | Resultado da melhoria | Rec. 0-10 | Esforço | Risco FP | Veredito |
|---|---------|--------------------------------|----------|------------------------|:---------:|---------|----------|----------|
| 1 | `engine.go:80,90-94`: pico = bin de delta de **maior contagem** sobre bins **disjuntos** de 256ms (`count > cur.count`), sem merge de vizinhos. | Match real cujo alinhamento cai na **fronteira** entre 2 bins reparte os votos e **corta o Score pela metade** — fatal no curto, onde já há poucos hashes. Nenhuma das 10 dimensões nomeou; achado do crítico. | Somar `count[bin-1]+count[bin]+count[bin+1]` (ou janela deslizante de 3 bins, estilo Shazam/audfprint `match-win`) ao escolher o pico. | Recupera matches de 5s perdidos por vote-splitting. **Zero** mudança de DSP, threshold ou re-fingerprint. Provável **maior ganho de recall por real gasto** depois do hop. | **8** | muito baixo | baixo | confirmado (crítico) |
| 2 | `peaks.go:11-13`: max-filter 17×17 → supressão temporal de **±1,024s** por pico. | O raio temporal largo (não o percentil-80) é o **gargalo dominante de densidade**. Medido no master real: variar percentil 80→50 dá só +12%; encolher o raio dá **2,3×–8,4×** mais hashes. | `neighborFrames 8→3` (±0,384s), manter `neighborBins`; acoplar cap top-K (8-10) por frame. | **Medido empiricamente: 2,5–4× hashes** num clipe de 5s (≈370→900-1400) sem tocar STFT/ffmpeg — eleva a margem sobre o threshold onde o p² do stream degradado morde. | **7** | baixo (2 constantes) + reindex | **médio** (recalibrar threshold obrigatório) | confirmado c/ ressalvas |
| 3 | `disambiguation.go:118-134`: conflito resolvido por **maior `DurationSeconds` nominal**; cobertura inerte deixou o 30s confirmar com o trecho compartilhado. | Corte 30s (head = 15s) **retrai o 15s legítimo** quando só o 15s tocou → FP (30s fantasma) **+** FN (15s perdido). | Desambiguar por **cobertura medida / overlap de hash** (TODO R-B `disambiguation.go:223`), não duração nominal; usar cobertura do audit como tie-breaker enquanto a do live for inerte. | Win-win: para de publicar o 30s que não tocou **e** para de retrair o 15s real. Recupera recall de curto silenciosamente convertido. | **7** | médio | baixo (reduz FP e FN) | confirmado c/ ressalvas |
| 4 | `coverage.go:9,52` piso de 32 frames + `MinTemporalCoverage=0.15` → cobertura inerte ≤~27s; `StateUncertain`/neural inalcançáveis. | Defesa anti-FP de curtos é **só** `UniqueScore≥5`. Sem 2º estágio nem evidência temporal → threshold forçado alto → mata 5s. | Remover o piso; trocar `cov>=0.15` por **acumulação sequencial** (N≥2-3 janelas com offset avançando ~linearmente, estilo SPRT/Panako). **Habilitador** para baixar o threshold com segurança. | Restaura uma defesa anti-FP **independente do score** → permite afrouxar o threshold p/ ganhar recall de 5s sem subir FP. Re-fortalece a desambiguação (#3). | **7** | médio (reescreve state machine) | baixo (endurece) | parcial (enabler) |
| 5 | `fingerprint/audio.go:74-88`: variantes broadcast (light/medium/heavy, §7) retornam `ErrVariantNotImplemented`; só `clean` é indexado. | A "estratégia-mãe" de robustez do plano está **ausente**. 1 fingerprint limpo precisa casar stream AAC + compressor multibanda; pelo p² de Wang, 1-2% dos hashes sobrevivem → num 5s pode ser <5 hashes = miss silencioso. | Implementar `medium` (AAC ~64k + acompressor+alimiter) e `heavy`; indexar clean+medium(+heavy). Infra de matching **já existe** (VariantID em Entry/histKey/loader) — falta só a geração. | Multiplica a prob. de sobrevivência de hash por âncora em stream degradado. Literatura: augmentation casada quase **dobrou** recall (.10→.22). | **7** | médio | baixo | parcial (ataca *degradado*, não "curto" puro) |
| 6 | `engine.go:107-108` (live) usa `HPF 100Hz 1ª ordem + RMS -20dB`; `audio.go:78` (offline) usa `loudnorm EBU-R128 + HPF 80 + LPF 7500`. | **Train/serve skew**: gate de pico (percentil global) cai em ponto diferente entre ref e query → seleciona picos diferentes → menos hashes sobrevivem ao casamento. Desproporcional no curto. | Alinhar a cadeia DSP dos dois lados (mesma normalização e mesmo HPF). | Recupera hashes que morrem **só** por divergência de pré-proc. Risco baixíssimo, **só ajuda**, FP-safe. **Fazer primeiro.** | **6** | baixo | baixo | parcial |
| 7 | `worker.go:333-334`: hop ao vivo = 2s, janela = 4s. | Anúncio de 5s só cabe inteiro na janela durante ~1s de fases → tipicamente **1 janela "limpa"**; no pior caso de fase nunca junta as 2 janelas exigidas → FN geométrico. | `tickEvery 16000` (hop 1s, 2 Hz→4 Hz). Aplicar **seletivamente** a campanhas com material ≤10s. | Dobra janelas-de-oportunidade do 5s (pior caso 1→2-3 janelas) e corta ~1s de latência. | **6** | baixo (1 const + recalibrar contadores) | **médio**: só com trava de offset cross-janela (#4); senão aumenta overlap e enfraquece a única defesa | parcial |
| 8 | `hashes.go:61-63`: `f1,f2 = bin & 0x1FF` (9 bits) sobre `numBins=999` → bins 512-998 **aliasam** sobre 0-486; `dt` usa 14 bits p/ valor ≤24 (9 bits mortos). | ~97,5% dos bins colidem com um parceiro físico distante → perda de entropia/especificidade do hash. Em curto, cada hash conta mais. | `&0x3FF` (10 bits) cobre 0-998 sem alias; `dt` em 5 bits; opcional `df` (delta-F, já calculado) em 7 bits (estilo audfprint, parcial. invariante a EQ). | Recupera os ~10 bits reais de Wang, limpa o histograma. **FP-neutro a reduz.** Mas recall de curto é **indireto** (não adiciona hashes). | **6** | baixo no código, **alto operacional** (re-fingerprint atômico de toda a base + bump de versão) | baixo (reduz colisão) | parcial (higiene/fundação) |
| 9 | `stft.go:12-13`: FFT 4096 (256ms) / hop 2048 (128ms) → **7,81 fps**, o **piso** entre Shazam/audfprint/dejavu/Olaf. | Clipe de 5s = só **39 frames**; densidade de hashes marginal sobre o threshold. Maior alavanca **teórica** de recall, mas a mais cara. | `HopSize 2048→512/1024`; reescalar `TargetZoneTMax`, `DeltaBinSize`. | 5s passa de 39→156 frames (4×), hashes >4× → margem confortável. | **6** | **alto** (4× índice/CPU, re-fingerprint, **reverte o commit 790dd34 de -48% CPU**) | médio (recalibração mandatória) | parcial — #2 dá ganho parecido mais barato; medir custo p/ 200 streams antes |
| 10 | `auditor.go` re-fingerprinta o PCM **inteiro** da janela de evidência (~125s p/ um 5s) com a mesma DSP fraca. | Gasta ~95% de CPU em áudio irrelevante e pode contaminar o histograma com material adjacente. Cobertura do master (sem piso) vira a barreira **mais apertada** do sistema p/ curtos. | (A) Recortar o PCM p/ a janela do match (`MatchStart/EndMs` já existem). (B) Gate por contagem absoluta de frames p/ masters <10s, instrumentado por telemetria antes. | (A): ~20-25× menos CPU, **mais preciso**, FP-safe. Para de descartar 5s legítimo no limiar. | **6** | baixo | baixo | parcial — (A) já; (B) gated por métrica |
| 11 | `calibration/job.go:50-51`: `min_hashes = max(noise_p99*1.5, 5)`, amostrado sobre **Score total** (`worker.go:491-498`, inclui shared). | Mas o gate aplica em **UniqueScore** (sempre ≤ Score). Em emissora com jingle compartilhado, o p99 infla → `min_hashes` vira 10-15 → curto nunca cruza. **Falso-negativo induzido pela própria calibração.** | Amostrar o ruído em `UniqueScore` (já existe `uniqueByKey`): 1 campo + 1 linha. Casar a unidade de calibração com a unidade do gate. | `min_hashes` cai p/ a escala correta (~5-7) **exatamente nas emissoras ruidosas que a calibração queria proteger** mas penaliza. | **6** | baixo | baixo (gate continua UniqueScore) | parcial (exige recalibração) |
| 12 | `engine.go:167-171`: threshold absoluto (5) + `MinScoreCoverage=0.02` (quase inerte); sem `input_confidence`. | Nenhum threshold único serve p/ 5s e 60s. (Ressalva do verificador: `min_hashes` **já** é noise-derived; metade da ideia já existe.) | Adicionar `input_confidence = score_alinhado/totalHashes` normalizado pela **query** (estilo Dejavu); margem de ruído **aditiva** (`p99+k`) em vez de multiplicativa (1.5×). | Em estação ruidosa o threshold sobe sozinho; em estação limpa cai p/ o piso — recall de curto sem número mágico. | **5-6** | médio | baixo-médio | parcial (parte já implementada) |
| 13 | `statemachine.go` 2º estágio (`StateUncertain`→`ResolveNeural`) é **código morto**. | Sem rede de segurança p/ candidatos borderline — impede a estratégia "baixar 1º estágio + verificar". | Tornar `StateUncertain` alcançável + plugar **GCC-PHAT** (cross-correlação, sem GPU, determinístico) como gate; alternativa: religar o neural. Roda só nos poucos borderline. | Destrava "recall alto + FP~0". Mas é **greenfield** e depende de #4. | **5** | **alto** (verificador novo) | baixo (2º estágio só reduz FP) | parcial (enabler, condicional) |
| 14 | `peaks.go:44-53`: gate = percentil-80 **global** da janela. | Em janela com locutor alto + jingle baixo, o trecho baixo zera picos → cobertura não-uniforme (anti-padrão de Wang). | Gate **por banda** (4-6 bandas log) + cap top-K por frame; alinhar pré-proc. | Recupera picos do segmento quieto do anúncio em stream real. | **4** | médio | baixo | parcial (2ª ordem, não medível no repo) |
| 15 | `worker.go:394`: só analisa os últimos 4s alinhados à fase do tick. | Se nenhum tick acertar a janela-limpa do 5s, perde o anúncio. | Varredura condicional de sub-janela (offsets -0,5/-1,0/-1,5s) quando rawScore está em `[0.6·thr, thr)`, com trava de offset. | Recupera o caso de fase desfavorável; mais barato que hop menor (só em borderline). | **4** | médio | médio (pega-o-máximo = viés otimista) | parcial (sobrepõe #7) |
| 16 | `engine.go:10`: `DeltaBinSize=2` (256ms), sem sub-bin. | Bin grosso infla coincidência **e** tolera drift. Mas no hop atual afinar quase não move (resolução limitada pelo próprio hop). | Voto em bin vizinho (barato); reduzir p/ 1 frame **só** acoplado a hop menor. | Ganho pequeno isolado; grande só com hop reduzido. | **4** | baixo | baixo | parcial (menor ROI do scoring) |
| 17 | Multi-rate (§9.7) NÃO implementado; `RateID` sempre 0. | Zero tolerância a time-stretch. **Mas:** o matcher opera em janelas de 4s — o drift intra-janela a 1-3% é <1 bin **em qualquer duração**. ROI baixo p/ curto. | Indexar rates 0.97/1.0/1.03 (`atempo`). Infra de consumo pronta. | Recupera FN de emissoras que esticam tempo — concentrado em peças longas. Medir prevalência de stretch antes. | **3-4** | médio (×3 índice) | médio | parcial (mecanismo quantitativo do achado original estava errado) |
| 18 | `stft.go:15`: `FreqMaxBin=1024` corta em ~4kHz; descarta 4-8kHz. | Menos picos candidatos. | Estender p/ ~7kHz (só após corrigir a máscara de bits). | Densidade incremental. | **3-4** | médio | médio | parcial — **crítico: superestimado** (banda de baixa SNR pós-codec AAC) |
| 19 | `stft.go:95` emite log-mag cru; sem whitening. | Picos vão p/ bandas de energia média (música de fundo, hum), não p/ transientes do comercial. | HPF de 1 polo (pole~0.98) na log-mag por bin ao longo do tempo, **simétrico** offline/online (audfprint). | Picos migram p/ onsets robustos a EQ — sobe p² em degradado. | **3** | médio | baixo | parcial (2ª ordem, não medível, exige rebuild) |

### Rejeitados (honestidade — ideias populares que NÃO ajudam aqui)

| Ideia | Por que rejeitada | Rec. |
|-------|-------------------|:----:|
| **Exigir offset constante entre janelas** (SMC-3) | **Refutada**: a janela é sempre "últimos 4s", então o offset **deriva ~8 deltaBins/janela** num anúncio real. Exigir constância rejeitaria os anúncios reais. A versão válida modela deriva **linear**, não constância. | 3 |
| **Quad-hash / Panako / Constant-Q** (H3) | **Negativo p/ curto**: benchmark Jamendo mostra Panako/Olaf 0% @1-2s; a razão-de-tempos é ruidosa com poucos picos. Custa R-Tree/CPU num orçamento já apertado. | 2 |
| **Janela de análise adaptativa por duração** (LW-2) | Janela é por-**estação** (1 worker × todos os comerciais). Encolher p/ casar 5s **reduz** hashes e quebra os 30s/60s da mesma estação. | 2 |
| **Fan-out 8→10** (H4) | Fan-out já é alto; o gargalo é densidade de picos, não o cap de pareamento — raramente satura. | 2 |

### Buracos que o crítico de completude achou (não estavam nas 10 dimensões)

1. **Cobertura usa relógio de PAREDE, não de áudio** (`coverage.go:51`, `last.Sub(first)`). Stall do worker / GC / reconexão / restart preventivo (§8.7) infla `elapsed` e pode **confirmar ou super-cobrir sem o áudio ter tocado** → vetor real de FP e de janela de evidência errada. **Não auditado por nenhuma dimensão.**
2. **O offset é descartado na estrutura de dados** (`coverage.go:37` `Add(_ int, ...)`), não só "inerte". Reativar a defesa (#4) exige **re-cablear** a `CoverageWindow`, não só ajustar threshold.
3. **Não-determinismo no desempate do histograma** (`engine.go:90-94` itera `map` Go; empate → vencedor aleatório). `OffsetFrames` não-determinístico afeta reprodutibilidade de evidência — relevante p/ defender números perante cliente.
4. **Auto-similaridade intra-comercial**: jingles repetem motivos; com offset descartado e sem verificação linear, hashes de uma repetição alinham no mesmo bin de outra → cobertura espúria.
5. **Linhas estacionárias de AM/FM** (hum 60/120Hz, piloto) geram picos persistentes idênticos em **todas** as estações → colisão de índice cross-station que o whitening atacaria.
6. **GCC-PHAT** como verificador de 2º estágio: mais barato que neural, determinístico, sem GPU, roda no clipe que já é salvo — melhor caminho que o CLAP morto.
7. **Sub-fingerprint binário (Haitsma-Kalker)** por sub-banda como 2º descritor robusto ao **limiter** broadcast (que esmaga picos esparsos) — barato (1 bit/banda/frame).

---

## Modelo de miss-rate (estimativa, NÃO medida)

Orçamento de hash de um 5s: ~39 frames STFT; confirmação exige `UniqueScore≥5` em **2 das ~2-3 janelas**. Em condição limpa o `UniqueScore`/janela fica em ~8-25 (margem ~2-5× sobre o 5). Sob degradação moderada (AAC 64k + compressor), a sobrevivência de pico `p` cai e o **par** sobrevive ~`p²`: se `p` cai 0.9→0.6, hashes alinhados caem ~56% → `UniqueScore` ~12→5, **no limiar**. Basta **uma** das 2 janelas cair abaixo de 5 p/ perder o anúncio inteiro.

| Duração | Miss-rate estimado (degradação moderada) | Em estação ruidosa (threshold≥7) |
|---------|:----------------------------------------:|:--------------------------------:|
| 5s | ~25-40% | ~40-55% |
| 10s | ~10-20% | — |
| 15s | ~5-12% | — |

> **Incerteza ±15pp.** Números ancorados em Wang (cada degrau de duração ≈ 3dB de robustez), **não** em medição E2E. O 10s/15s salvam-se pela redundância (4-6 janelas, basta 2 cruzarem).

---

## Roadmap recomendado (sequenciado por dependência e custo)

**Fase 0 — Instrumentar antes de tunar (pré-requisito inegociável)**
- Construir o **harness de recall@5/10/15s × {AAC 32/64/96k, compressor multibanda, locução sobreposta, time-stretch 0-3%}**. Sem essa curva, todo o resto é não-falsificável. Reusar `audio-refs/` + os perfis de `diag_falsepos*_test.go`.
- Instrumentar `radiocheck_audit_coverage` e recall segmentados **por faixa de duração**.

**Fase 1 — Wins baratos, FP-safe, sem migração (semanas)**
- **#1 merge de bins adjacentes** (maior ROI, zero migração).
- **#6 alinhar pré-processamento** ref/live.
- **#11 calibrar ruído em UniqueScore** (unidade correta).
- **#3 desambiguação por cobertura/overlap** + **#10(A) recortar PCM do audit**.
- **#4 reativar cobertura como defesa real** (re-cablear offset; corrigir relógio-de-parede → relógio-de-amostras, buraco #1 do crítico).

**Fase 2 — Densidade + qualidade do hash (exige re-fingerprint atômico — fazer junto)**
- **#2 encolher raio temporal do max-filter** (medido, melhor ROI de densidade) **+ #8 corrigir aliasing/bits** no mesmo rebuild de índice. Recalibrar threshold pela PDF de ruído **antes** de prod.
- Decidir **#9 hop STFT** só **depois** de medir o custo real em 200 streams (já houve pressão de CPU — commit -48%).

**Fase 3 — Robustez e 2º estágio (maior esforço)**
- **#5 variantes broadcast** (medium primeiro, calibrar contra clipes reais).
- **#13 GCC-PHAT** como verificador de 2º estágio → então **#7 hop ao vivo 1s** e baixar o threshold com a rede de segurança no lugar.
- **#17 multi-rate** só se a Fase 0 mostrar prevalência real de time-stretch.

---

## Caveats honestos

- **Nada disto foi medido E2E.** Os ganhos (`2,5-4×` hashes, miss-rate, `min_hashes 10-15→5-7`) são estimativas de modelo / medições em **master limpo**, não recall em stream degradado real. Fase 0 existe por isso.
- **Várias mudanças exigem re-fingerprint atômico de toda a base** (#2, #8, #9, #14, #19). Rollout parcial **quebra silenciosamente 100% do match** (query nova não casa base antiga). Tratar como migração de primeira classe — ver CLAUDE.md §4 (incidente de perda de dado em prod).
- **A correção do aliasing é overrated como anti-FP** (crítico): é auto-consistente, então é perda de **entropia/especificidade**, não FP direto. Vale fazer, mas como higiene, não como driver de recall.
- **Variantes broadcast e multi-rate atacam DEGRADAÇÃO (transversal à duração), não "curto" em si.** Para curto-e-limpo, **#1 + #2 + #6** resolvem mais.
- **Abaixo de ~2-3s nenhum método landmark é confiável** — fora do envelope.
