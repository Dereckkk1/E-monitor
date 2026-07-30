"""
verify_censura_coverage.py — valida a DECISÃO do fix (§18.2.2-v2 chooseByCoverage)
usando as CENSURAS REAIS de ar, não o simulador.

Para cada censura (áudio gravado do ar onde o PULSO realmente tocou):
  1. extrai a janela de evidência em torno da tocada (como o §9.9 faria),
  2. mede a cobertura do clipe contra o master do PULSO e contra o master do SPOT,
  3. aplica a regra do chooseByCoverage (margem 1.5, empate → duração)
  4. reporta quem o fix escolheria.

Se o fix escolher o PULSO nas duas censuras, a arbitragem por cobertura está
validada em áudio de ar real (e não só no E2E com Icecast).
"""
import sys
from collections import Counter, defaultdict
from pathlib import Path

import numpy as np

HERE = Path(__file__).resolve().parent
REPO = Path("c:/Users/marke/Desktop/Programas/E-Series/E-monitor")
FP = REPO / "fingerprint"
sys.path.insert(0, str(FP))
sys.path.insert(0, str(FP / "scripts"))

from fingerprint.generator import generate_fingerprint, SAMPLE_RATE  # noqa: E402
import evaluate_detection as ed  # noqa: E402

REFS = REPO / "audio-refs"
PULSO = REFS / "PULSO SONORO MILIUM - [Ao Vivo].mp3"
SPOT = REFS / "MILIUM - DEMAIS RADIOS DO PLANO -20 a 26.07.mp3"

# (arquivo, rótulo, janela de evidência em segundos) — janelas conforme o
# incident report §1.2 (audit sim: clipe 74-86s e 2-14s).
CENSURAS = [
    (REFS / "Censura - Milium 24-07 15h45.mp3.mpeg", "censura 24/07 15h45", 74.0, 86.0),
    (REFS / "WhatsApp Audio 2026-07-29 at 15.34.52 (1).mpeg", "censura WhatsApp 29/07", 2.0, 14.0),
]

COVERAGE_MARGIN = 1.5  # evidence/disambig_coverage.go


def best_coverage(clip_hashes, refs):
    """Maior cobertura do clipe entre as variantes do master (o audit usa o melhor match)."""
    best = {"cov": 0.0, "score": 0, "variant": None}
    for vid, (idx, total, _n) in refs.items():
        score, cov, _ok, _m = ed.match_against_reference(clip_hashes, idx, total)
        if score > best["score"]:
            best = {"cov": cov, "score": score, "variant": vid}
    return best


def choose_by_coverage(a, b):
    """Porta fiel de chooseByCoverage (evidence/disambig_coverage.go)."""
    hi, lo = (a, b) if a["cov"] >= b["cov"] else (b, a)
    if lo["cov"] <= 0 or hi["cov"] >= lo["cov"] * COVERAGE_MARGIN:
        return hi, "cobertura"
    # quase-empate → duração
    if a["dur"] != b["dur"]:
        return (a, "duração") if a["dur"] > b["dur"] else (b, "duração")
    return (a if a["short_id"] < b["short_id"] else b), "short_id"


def main():
    print("=" * 78)
    print("Construindo índices multi-variante (master direto + broadcast_sim)")
    print("=" * 78)
    pulso_pcm = ed.decode_to_pcm(str(PULSO))
    spot_pcm = ed.decode_to_pcm(str(SPOT))
    print(f"  master PULSO: {len(pulso_pcm)/SAMPLE_RATE:.2f}s")
    print(f"  master SPOT : {len(spot_pcm)/SAMPLE_RATE:.2f}s")
    refs_pulso = ed.build_multi_variant_index(str(PULSO), pulso_pcm)
    refs_spot = ed.build_multi_variant_index(str(SPOT), spot_pcm)
    print(f"  variantes: pulso={len(refs_pulso)} spot={len(refs_spot)}")

    for path, label, t0, t1 in CENSURAS:
        print()
        print("=" * 78)
        print(f"{label}   (clipe de evidência {t0:.0f}-{t1:.0f}s)")
        print("=" * 78)
        pcm = ed.decode_to_pcm(str(path))
        clip = pcm[int(t0 * SAMPLE_RATE): int(t1 * SAMPLE_RATE)]
        print(f"  clipe: {len(clip)/SAMPLE_RATE:.2f}s de {len(pcm)/SAMPLE_RATE:.1f}s totais")
        clip_hashes = generate_fingerprint(clip)
        print(f"  hashes do clipe: {len(clip_hashes)}")

        bp = best_coverage(clip_hashes, refs_pulso)
        bs = best_coverage(clip_hashes, refs_spot)
        print()
        print(f"  {'master':<8} {'score':>7} {'cobertura':>11}   variante")
        print(f"  {'-'*8} {'-'*7} {'-'*11}   {'-'*8}")
        print(f"  {'PULSO':<8} {bp['score']:>7} {bp['cov']:>11.3f}   v{bp['variant']}")
        print(f"  {'SPOT':<8} {bs['score']:>7} {bs['cov']:>11.3f}   v{bs['variant']}")

        a = {**bp, "name": "PULSO", "dur": 6, "short_id": 211}
        b = {**bs, "name": "SPOT", "dur": 31, "short_id": 213}
        winner, why = choose_by_coverage(a, b)
        ratio = (max(a["cov"], b["cov"]) / min(a["cov"], b["cov"])) if min(a["cov"], b["cov"]) > 0 else float("inf")
        print()
        print(f"  razão de cobertura: {ratio:.2f}x  (margem exigida: {COVERAGE_MARGIN}x)")
        print(f"  >>> chooseByCoverage elege: {winner['name']} (por {why})")
        print(f"  >>> regra v1 (duração) elegeria: SPOT")
        veredito = "CORRETO (o pulso é quem tocou)" if winner["name"] == "PULSO" else "ERRADO"
        print(f"  >>> veredito: {veredito}")


if __name__ == "__main__":
    main()
