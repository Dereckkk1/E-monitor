# Relatório de Avaliação de Detecção — Baseline e Recomendações

**Data:** 2026-05-05
**Script:** `fingerprint/scripts/evaluate_detection.py`
**Master:** sintético (8s, tons + impulsos + ruído rosa baixo)
**Threshold de match:** score ≥ 5 **AND** coverage ≥ 0.4

---

## 1. Resumo Executivo

Foi criado um harness end-to-end que aplica 15 perfis realistas de degradação broadcast a um master e tenta detectá-lo contra um índice multi-variante (variant 0 = raw + variants 1..3 = `broadcast_sim`).

| Cenário | Detectados | Taxa |
|---------|------------|------|
| Match só contra variante 0 (master limpo) | 11/15 | 73% |
| Match contra todas as variantes (produção) | **13/15** | **87%** |

**Conclusão:** o multi-variant indexing entrega um ganho líquido de **+14 pontos percentuais** em robustez. Os dois casos remanescentes têm uma raiz comum: **ruído aditivo (hiss, noise floor)** que nenhuma das 3 variantes do `broadcast_sim` atual modela.

---

## 2. Resultados Detalhados

| # | Perfil | Variant | Score | Cov | Resultado | Observação |
|---|--------|---------|-------|-----|-----------|------------|
| 1 | AAC 128k (clean ref) | 0 | 737 | 0.49 | OK | baseline ótimo |
| 2 | AAC 96k | 0 | 737 | 0.49 | OK | – |
| 3 | AAC 64k | 0 | 466 | 0.48 | OK | – |
| 4 | AAC 48k | 0 | 207 | 0.44 | OK | encoder começa a degradar |
| 5 | AAC 32k (very low) | 0 | 203 | 0.49 | OK | encoder agressivo, ainda passa |
| 6 | MP3 64k | 0 | 796 | 0.52 | OK | – |
| 7 | AM-style lowpass 4kHz | 0 | 680 | 0.52 | OK | banda larga preservada |
| 8 | Heavy compression | **2** | 367 | 0.52 | OK | **só passa com multi-variant** |
| 9 | Loudness war | **2** | 337 | 0.54 | OK | **só passa com multi-variant** |
| 10 | Broadcast EQ (mid boost) | 0 | 745 | 0.54 | OK | EQ não move picos |
| 11 | Light echo / phase | 0 | 186 | 0.46 | OK | eco curto tolerado |
| 12 | Resample 8k roundtrip | 0 | 976 | 0.54 | OK | sample rate aliasing inofensivo |
| 13 | Hiss -30dB | 0 | 26 | 0.26 | **MISS** | ruído branco polui constellation |
| 14 | Hum 60Hz -25dB | 0 | 737 | 0.49 | OK | high-pass remove hum no preprocess |
| 15 | AM combo (lp+comp+hiss+48k) | 3 | 9 | 0.15 | **MISS** | combo destrutivo |

**Insight:** os perfis 8 e 9 (compressão pesada / loudness war) **só são detectados quando o índice tem variantes simuladas com compressão correspondente** (variant 2 do `broadcast_sim`). Sem multi-variant, eles falhariam por coverage.

---

## 3. Análise dos Misses

### 3.1 Hiss -30dB (perfil 13)

**Fingerprint do degradado tem 1346 hashes** (mais que o master limpo, 1211) — mas **só 36 deles casam com a referência**. O hiss adiciona picos espúrios distribuídos uniformemente no espectro, e o `maximum_filter` do `peaks.py` os escolhe junto com os picos legítimos. Resultado: o constellation map fica diluído com pares aleatórios que não casam com nada.

**Por que -30dB já quebra:** o ruído de -30dBFS está apenas 30dB abaixo do sinal normalizado a -20dBFS. Após pre-emphasis (high-pass 100Hz) e RMS normalize, a relação sinal-ruído cai mais ainda. O percentile=75 do peak picker acaba selecionando muito ruído.

### 3.2 AM combo (perfil 15)

Este é o "pior caso realista" combinando 4 degradações típicas de AM com receptor ruim:
- Lowpass 3.5kHz (corta toda informação aguda)
- Compressor pesado (achata transientes)
- AAC 48k (artefatos de encoder)
- Hiss -35dB (poluição de picos)

Casa com variant 3 (compressão pesada) com score 9 — abaixo do threshold de 5? Não, passa, mas coverage 0.15 derruba. **A coverage é matada pelos artefatos do encoder de baixo bitrate combinados com o achatamento da compressão.**

---

## 4. Recomendações de Melhoria

Ordenadas por **custo de implementação × impacto esperado**.

### 4.1 ⭐ Alta prioridade — adicionar variantes com hiss ao `broadcast_sim`

**Custo:** ~30 minutos. **Impacto esperado:** resolver perfis 13 e 15 (+13 pp).

A causa-raiz dos misses é que **nenhuma das 3 variantes atuais injeta ruído branco**. A produção indexa um comercial limpo + 3 versões com compressão progressivamente pesada — mas streams reais com chuva, antena ruim ou conexão fraca **têm ruído de fundo** que não é modelado.

**Proposta:** adicionar 2 novas variantes ao `broadcast_sim.py`:

```python
VARIANTS = {
    0: { "filters": "acompressor=...:ratio=3...",  "codec_args": [...96k...] },
    1: { "filters": "acompressor=...:ratio=6...",  "codec_args": [...64k...] },
    2: { "filters": "acompressor=...:ratio=10...", "codec_args": [...48k HE...] },
    # NOVAS:
    3: { "filters": "acompressor=...:ratio=4..., anoisesrc=color=white:amp=0.005[n];[a][n]amix",
         "codec_args": [...64k...], "name": "noisy_clean" },
    4: { "filters": "lowpass=f=3500, acompressor=...:ratio=8...",
         "codec_args": [...48k...], "post_python": ("white", -32), "name": "am_combo" },
}
```

Isso cobre o gap diretamente. Custo de armazenamento adicional: +66% no índice (5 variantes × 3 rates = 15 entries por comercial em vez de 9). Aceitável dado que o índice cabe inteiro em RAM.

### 4.2 ⭐ Alta prioridade — coverage adaptativo por estação

**Custo:** ~1h (já há fundação na Fase 2 com `station_thresholds`). **Impacto:** ganho marginal mas reduz falsos negativos.

O `MIN_COVERAGE = 0.4` é um valor universal que penaliza estações ruidosas. Já existe a tabela `station_thresholds` com `min_hashes` calibrado por estação — **estender para também armazenar `min_coverage`** calibrado a partir do mesmo histórico de noise samples.

**Implementação:** durante a calibração de 7 dias, registrar não só hash counts mas também coverage observada. Setar `min_coverage = max(0.25, p10_coverage_observada)`.

### 4.3 Média — denoise leve no preprocessamento

**Custo:** ~2h + benchmark. **Impacto:** melhora hiss MAS pode prejudicar áudio limpo.

Adicionar um spectral subtraction leve antes do STFT: estimar piso de ruído nos primeiros 200ms e subtrair do magnitude spectrogram. Isso atenuaria picos espúrios do hiss sem mexer nos picos legítimos.

**Risco:** pode cortar harmônicos baixos legítimos em áudio limpo. **Recomendação:** A/B testar antes de promover.

### 4.4 Média — peak picker mais conservador

**Custo:** ~30min + benchmark. **Impacto:** resolve hiss parcialmente, custo é menos picos no master limpo.

Subir `PEAK_AMPLITUDE_PERCENTILE` de 75 → 85 e/ou aumentar `PEAK_NEIGHBORHOOD` de 15×15 → 21×21. Reduz a quantidade de picos em áudios ruidosos (rejeita os de baixa magnitude). Trade-off: também rejeita picos legítimos em áudios limpos.

**Recomendação:** se for fazer, fazer junto com (4.1) e medir delta.

### 4.5 Média — neural verification (CLAP) já implementada

**Custo:** zero — código já existe na Fase 2 (E2). **Impacto:** resolve casos `uncertain`.

A state machine em `workers/internal/match/statemachine.go` já tem o estado `StateUncertain` que ativa quando `coverage ≥ 0.4 mas < min_coverage` após 3 janelas. Para os perfis 13 e 15, a coverage está abaixo de 0.4 — **não chegam ao estado uncertain**, são descartados como Idle.

**Proposta:** baixar o gate de entrada em `StateUncertain` de `coverage ≥ 0.4` para `coverage ≥ 0.2 AND score ≥ 3 × MATCH_THRESHOLD`. Isso pega os casos como o perfil 13 (score 26 = 5× threshold) e os encaminha para verificação neural via CLAP. Sem multiplicar falsos positivos pois CLAP rejeita os negativos.

### 4.6 Baixa prioridade — usar log-mel + neural fingerprint

**Custo:** semanas. **Impacto:** alto em qualidade, mas é reescrita do core.

A literatura recente (Dejavu, Panako, NeuralFP) mostra que features log-mel + auto-encoder convolucional são significativamente mais robustas a ruído que constellation maps puros. Isso já está parcialmente coberto pela Fase 2 com CLAP — mas como **camada de verificação**, não como núcleo do match.

Migrar pra um sistema que **ranqueia candidatos por embedding similarity** em vez de histogram delta seria uma mudança de arquitetura grande. **Não recomendo** sem evidência de problema em produção que justifique.

### 4.7 Baixa prioridade — fan-out maior + target zone mais generoso

**Custo:** ~1h. **Impacto:** mais hashes redundantes, índice infla.

Atualmente `FAN_OUT=5`, `TARGET_ZONE_T_MAX=16`. Subir para `FAN_OUT=8`, `TARGET_ZONE_T_MAX=24` produziria ~60% mais hashes e potencialmente mais robustez a perda. **Custo:** índice 60% maior em RAM.

**Recomendação:** só se (4.1) não for suficiente.

---

## 5. Plano Sugerido

Em ordem de execução:

1. **Implementar 4.1** (variantes com hiss) — 30min — esperado: 15/15 detectados
2. **Re-rodar este harness** — confirmar ganho — 5min
3. **Implementar 4.5** (gate uncertain mais permissivo) — 1h — proteção adicional para edge cases
4. **Implementar 4.2** (coverage adaptativo) — 1h — generalização para estações ruidosas reais
5. **Avaliar em produção** com 2 semanas de coexistência — Fase 2 §17

Total de implementação: ~2h30. Esperado: **detecção robusta a 95%+ dos perfis broadcast realistas**, com fallback CLAP para casos extremos.

---

## 6. Caveats Importantes

- **Master sintético ≠ comercial real.** A estrutura espectral do master sintético (5 tons + impulsos) é menos rica que um comercial musical/falado. Resultados em produção tendem a ser **melhores** porque há mais picos distintos pra escolher.
- **8 segundos é curto.** Comerciais reais são 15-30s. Mais áudio = mais hashes = mais robustez. O "score 26" do hiss em 8s pode virar "score 100" em 30s, passando o threshold.
- **A coverage está sendo medida contra o áudio inteiro**, não janela de 4s como o worker em produção. Em produção, a janela deslizante atual cobre 4s e exige cobertura dentro desse window. Os números absolutos diferem.
- **Não foi testado:** time-stretching (multi-rate já implementado mas não exercitado aqui), codec mudanças (Opus, FLAC), bitrate ultrabaixo (16k), ou jitter de rede simulado.

---

## 7. Reproduzir

```bash
cd fingerprint
python scripts/evaluate_detection.py                 # synthetic, 8s
python scripts/evaluate_detection.py --seconds 20    # synthetic, 20s
python scripts/evaluate_detection.py --master master.wav  # comercial real
python scripts/evaluate_detection.py --master master.wav --export-audio dir/  # exporta WAVs
```

---

## 8. Follow-up — implementação das recomendações

As recomendações 4.1, 4.4, 4.5 e 4.7 foram implementadas. O harness foi expandido de 15 para 20 perfis, removendo o `Light echo` (pouco realista em FM moderno) e adicionando 5 perfis baseados em condições reais de antena.

### Mudanças aplicadas

| Recomendação | O que mudou | Onde |
|--------------|-------------|------|
| **4.1** | +2 variantes no `broadcast_sim` (white noise -32dB e AM combo com noise -35dB) | `fingerprint/fingerprint/broadcast_sim.py` |
| **4.4** | `PEAK_AMPLITUDE_PERCENTILE` 75→80, `PEAK_NEIGHBORHOOD` 15×15→17×17 | Python + Go (revelou que Go **não tinha** percentile filtering — gap fechado) |
| **4.5** | Gate de `StateUncertain` baixou de `cov ≥ 0.4` para também aceitar `score ≥ 3× threshold AND cov ≥ 0.2` | `workers/internal/match/statemachine.go` (apenas no branch fase2) |
| **4.7** | `FAN_OUT` 5→8, `TARGET_ZONE_T_MAX` 16→24 | Python + Go sincronizados |

### Novos perfis adicionados ao harness

1. **AM NRSC mask** — lowpass 5kHz + compressão + hiss -33dB (padrão AM brasileiro)
2. **FM pre-emphasis 75µs** — EQ boost em 4kHz/8kHz (típico de processador FM)
3. **AM modulation clipping** — saturação em 0.85 (over-modulação comum)
4. **Multipath fading 0.3Hz** — modulação cíclica de amplitude (recepção com reflexão)
5. **Cheap receiver IF** — bandpass 200-3500Hz (receptor barato com IF estreito)
6. **Stream rebuffering** — gaps de 100ms a cada 5s (jitter de rede)

### Resultados finais

| Cenário | Antes (15 perfis, 4 variantes) | Depois (20 perfis, 6 variantes) |
|---------|------------------------------|-------------------------------|
| Jingle musical (Rogga Verão 30s) | 15/15 (100%) | **20/20 (100%)** |
| Comercial falado (Amanay 30s) | 14/15 (93%) | **20/20 (100%)** |

### Observações dos novos resultados

- **Variant 5 do `broadcast_sim`** (AM combo com hiss) salvou os perfis #20 em ambos os áudios — sem ela, score cairia abaixo do limiar
- **Variant 1** (compressão média + AAC 64k) ficou sendo a melhor referência pra comercial falado em vários perfis com compressão
- **Multipath fading no falado:** coverage 0.47, perto do limiar — segundo ponto frágil em voz, depois do AM combo (cov 0.49)
- **Margem em comercial falado é menor** que em musical, como esperado — voz tem menos densidade espectral

### Caveat persistente

Os números acima são **pior caso teórico** porque o harness não usa CLAP neural verification (recomendação 4.5 — implementada no Go mas o harness é Python puro). Em produção, casos com score 500-800 e coverage 0.45-0.49 (como AM combo no comercial falado) seriam encaminhados ao `StateUncertain` → CLAP, dando uma camada extra de robustez.

### Custos

- **Índice +50% em RAM** (6 variantes × 3 rates = 18 entries por comercial vs 12 antes). Aceitável.
- **Fingerprints atuais no DB ficam inválidos** após mudanças 4.4 + 4.7 — re-fingerprintar todos os comerciais antes de promover pra produção. Migração necessária.
