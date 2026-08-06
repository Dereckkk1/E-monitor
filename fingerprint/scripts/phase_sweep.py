"""
phase_sweep.py — em que fracao dos alinhamentos possiveis a tocada do pulso
sobrevive, com a regra atual (2 janelas) vs a regra de 1 janela forte.

A grade de janelas (4s, hop 2s) cai em fase aleatoria em relacao a tocada.
Varremos todas as fases em passos de 0.25s e contamos os desfechos.

Gates de prod: UniqueScore >= min_hashes (19) E score/hashes_da_janela >= 2%.
"""
import sys
from collections import Counter
from pathlib import Path

import numpy as np

REPO = Path("c:/Users/marke/Desktop/Programas/E-Series/E-monitor")
FP = REPO / "fingerprint"
sys.path.insert(0, str(FP))
sys.path.insert(0, str(FP / "scripts"))

from fingerprint.generator import generate_fingerprint, SAMPLE_RATE  # noqa: E402
import evaluate_detection as ed  # noqa: E402

REFS = REPO / "audio-refs"
PULSO = REFS / "PULSO SONORO MILIUM - [Ao Vivo].mp3"
MIN_HASHES = 19      # calibrado em prod nas 7 emissoras
RATIO_GATE = 0.02    # MinScoreCoverage
WIN_S, HOP_S = 4.0, 2.0
DB = ed.DELTA_BIN_SIZE

CASOS = [
    (REFS / "Censura - Milium 24-07 15h45.mp3.mpeg", "censura 24/07 (aircheck 40kbps)", 70.0, 88.0),
    (REFS / "WhatsApp Audio 2026-07-29 at 15.34.52 (1).mpeg", "censura WhatsApp (320kbps)", 0.0, 16.0),
]


def build():
    pcm = ed.decode_to_pcm(str(PULSO))
    return ed.build_multi_variant_index(str(PULSO), pcm)


def win_score(qh, refs):
    best = 0
    for _v, (idx, _t, _n) in refs.items():
        bins = Counter()
        for h, tq in qh:
            for tr in idx.get(h, ()):
                bins[(tr - tq) // DB] += 1
        if bins:
            best = max(best, max(bins.values()))
    return best


def main():
    refs = build()
    win, hop = int(WIN_S * SAMPLE_RATE), int(HOP_S * SAMPLE_RATE)
    step = int(0.25 * SAMPLE_RATE)

    for path, rotulo, t0, t1 in CASOS:
        audio = ed.decode_to_pcm(str(path))
        seg = audio[int(t0 * SAMPLE_RATE):int(t1 * SAMPLE_RATE)]
        print("=" * 72)
        print(rotulo)
        print("=" * 72)
        print(f"  {'fase':>6} {'janelas qualificadas':>22} {'melhor score':>14}   desfecho")
        ok2 = ok1 = total = 0
        for ph in range(0, hop, step):
            scores = []
            for s in range(ph, len(seg) - win, hop):
                qh = generate_fingerprint(seg[s:s + win])
                if not qh:
                    continue
                sc = win_score(qh, refs)
                ratio = sc / len(qh)
                scores.append((sc, sc >= MIN_HASHES and ratio >= RATIO_GATE))
            qual = [s for s, ok in scores if ok]
            best = max([s for s, _ in scores], default=0)
            conf2 = len(qual) >= 2            # regra atual
            conf1 = best >= 3 * MIN_HASHES    # regra nova (1 janela forte, K=3)
            total += 1
            ok2 += conf2
            ok1 += (conf2 or conf1)
            desf = "CONFIRMA (2 janelas)" if conf2 else (
                   "salva pela regra de 1 janela" if conf1 else "PERDIDA")
            print(f"  {ph/SAMPLE_RATE:>5.2f}s {len(qual):>22} {best:>14}   {desf}")
        print(f"\n  regra ATUAL (2 janelas):        {ok2}/{total} fases = {100*ok2//total}%")
        print(f"  regra NOVA (+1 janela forte):   {ok1}/{total} fases = {100*ok1//total}%")
        print(f"  limiar da regra nova: score >= {3*MIN_HASHES} (3x min_hashes)\n")


if __name__ == "__main__":
    main()
