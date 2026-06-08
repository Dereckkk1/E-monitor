# Baseline de recall por duração (short-audio · Fase 0)

Gates: `score>=5` (gate efetivo do live para curto) e `live = score>=5 & cov>=0.15`. Fonte do master: **sintético**. Índice = v0 cru + 5 variantes broadcast_sim. 20 perfis de degradação.

| Duração | hashes ref (v0) | hashes query (med) | score (med) | recall (score≥5) | recall (live) |
|--------:|----------------:|-------------------:|------------:|-----------------:|--------------:|
| 5s | 627 | 668 | 308 | 95% | 90% |
| 10s | 1407 | 1326 | 614 | 100% | 100% |
| 15s | 2391 | 2329 | 1037 | 100% | 100% |
| 30s | 4891 | 4903 | 2310 | 100% | 100% |

## Misses por duração (perfis com score < 5)

- **5s**: AM combo (lp+comp+hiss+48k)
- **10s**: _(nenhum)_
- **15s**: _(nenhum)_
- **30s**: _(nenhum)_

---

## Interpretação e ressalvas (NÃO leia a tabela isolada)

**Boa notícia:** o núcleo do matcher + o índice de 5 variantes broadcast já entrega **95-100% mesmo em 5s** sobre conteúdo rico. A densidade de hash não está catastroficamente quebrada — só o **pior perfil** (AM lowpass 3500 + compressor pesado + hiss + AAC 48k) escapa em 5s, e mesmo assim só ali.

**3 ressalvas que impedem concluir "está resolvido":**

1. **Sintético é OTIMISTA (~2× mais denso que anúncio real).** Este master sintético (5 tons fortes + impulsos + ruído rosa) gera **668 hashes** num clipe de 5s. O master REAL medido na auditoria (PEAK-1, `UNIFIQUE-SPOT-30`) rende ~**2,64 picos/frame ≈ 370 hashes** num 5s — quase **metade**. Locução calma de anúncio real tem trechos esparsos que degradam pior. Logo o recall real de 5s deve ser **menor** que estes 95% — provavelmente é onde os levers de densidade (#2 raio do max-filter) realmente pagam, junto com o pior caso (AM combo).

2. **Este harness mede o TETO do matcher, não o recall do LIVE.** Ele roda uma passada única contra o índice. Ele **NÃO modela** o que o live faz: confirmação por **2 janelas** da state machine, o **threshold de calibração** que SOBE em emissora ruidosa (`min_hashes = max(noise_p99*1.5, 5)`, e medido sobre `Score` total e não `UniqueScore` — bug CT-1), a **cobertura inerte**, nem o **vote-splitting** do histograma (#1). Esses problemas da camada live são **invisíveis aqui** e podem ser a fonte real de miss em produção. Eles precisam de validação própria (testes Go de integração / harness live-fiel).

3. **Score mediano de 308 em 5s é enorme (>>5), mas é contra o mesmo conteúdo.** Em produção há ruído cruzado da emissora + o threshold calibrado pode subir bem acima de 5 — o que aperta justamente o curto.

**O que isto muda no plano:**
- Os levers de **densidade (#2)** se justificam pelo **pior caso de degradação** (AM combo) + **conteúdo real esparso**, não pelo caso típico (já 100%).
- Os fixes de **camada live (#1 vote-splitting, #4 cobertura, #11 calibração)** **não aparecem neste harness** — validar via testes Go / harness live-fiel.

**Próximos passos da Fase 0:**
- Rodar com **masters REAIS** (na VM, `/mnt/data/audio-refs`): `python scripts/evaluate_short_audio.py --master /mnt/data/audio-refs/<spot>.mp3 --out baseline-real.md`.
- `pprof` de 60s na VM para o split ffmpeg/matcher.
- Construir a validação live-fiel (Go) para #1/#4/#11.
