"""
audit_detection.py — Forense de uma detecção real.

Replay offline do algoritmo de matching contra:
  - master audio (a censura cadastrada)
  - evidence audio (o clipe salvo quando a produção confirmou a detecção)

Constrói o mesmo índice multi-variante (v0 raw + v1..v5 broadcast_sim) e
reporta tanto matching do clipe inteiro quanto janela deslizante 4s @ 0.5s,
imitando o que o worker faz em produção.

Uso:
  python audit_detection.py \
    --master "../audio-refs/UNIFIQUE - 7X UNIFIQUE - SPOT 30.mp3" \
    --evidence "../audio-refs/veiculacao-5328606c-3d17-4a6c-b2aa-6ce786ee482b.m4a"
"""

import argparse
import sys
from collections import Counter, defaultdict
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parent))
sys.path.insert(0, str(HERE))

from fingerprint.generator import generate_fingerprint, SAMPLE_RATE  # noqa: E402
from evaluate_detection import (  # noqa: E402
    decode_to_pcm,
    build_multi_variant_index,
    match_against_reference,
    DELTA_BIN_SIZE,
    MATCH_THRESHOLD,
    MIN_COVERAGE,
)

WINDOW_S = 4.0
STEP_S = 0.5


def diagnose_full_clip(query_hashes, refs):
    print(f"\n=== Full-clip match ({len(query_hashes)} query hashes) ===")
    print(f"{'var':>3} {'matched':>8} {'best_score':>10} {'cov':>6} {'detect':>7}  top_deltas (d=frames : count)")
    print("-" * 100)
    best_overall = None
    for vid in sorted(refs.keys()):
        idx, total, _n = refs[vid]
        score, cov, ok, matched = match_against_reference(query_hashes, idx, total)
        bin_counts: Counter = Counter()
        for h, t_q in query_hashes:
            for t_r in idx.get(h, []):
                bin_counts[(t_r - t_q) // DELTA_BIN_SIZE] += 1
        top = bin_counts.most_common(5)
        deltas_str = ", ".join(f"d={b*DELTA_BIN_SIZE:+d}:{c}" for b, c in top)
        mark = "YES" if ok else "no"
        print(f"{vid:>3} {matched:>8} {score:>10} {cov:>6.2f} {mark:>7}  {deltas_str}")
        if best_overall is None or score > best_overall[1]:
            best_overall = (vid, score, cov, ok, matched, top)
    return best_overall


def diagnose_sliding(audio, refs):
    win = int(WINDOW_S * SAMPLE_RATE)
    step = int(STEP_S * SAMPLE_RATE)
    n_windows = max(0, (len(audio) - win) // step + 1)
    print(f"\n=== Sliding {WINDOW_S}s window @ {STEP_S}s step ({n_windows} windows) ===")
    print(f"{'t(s)':>7} {'best_var':>8} {'score':>6} {'cov':>6} {'matched':>7}  detect")
    print("-" * 60)
    detected_windows = 0
    over_threshold = 0
    for off in range(0, len(audio) - win + 1, step):
        seg = audio[off:off + win]
        q = generate_fingerprint(seg)
        best = None
        for vid, (idx, total, _) in refs.items():
            score, cov, ok, matched = match_against_reference(q, idx, total)
            if best is None or score > best[1]:
                best = (vid, score, cov, ok, matched)
        vid, score, cov, ok, matched = best
        if score >= 3:
            mark = "YES" if ok else ""
            print(f"{off/SAMPLE_RATE:>7.2f} {vid:>8} {score:>6} {cov:>6.2f} {matched:>7}  {mark}")
            over_threshold += 1
        if ok:
            detected_windows += 1
    print(f"\n  {detected_windows}/{n_windows} windows passou MATCH_THRESHOLD+MIN_COVERAGE ({MATCH_THRESHOLD}/{MIN_COVERAGE})")
    print(f"  {over_threshold}/{n_windows} windows com score>=3 (mostrado acima)")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--master", required=True)
    ap.add_argument("--evidence", required=True)
    ap.add_argument("--no-sliding", action="store_true", help="Skip sliding-window pass")
    args = ap.parse_args()

    print(f"Master:   {args.master}")
    print(f"Evidence: {args.evidence}")

    master_pcm = decode_to_pcm(args.master)
    evid_pcm = decode_to_pcm(args.evidence)
    print(f"\nMaster decoded:   {len(master_pcm)/SAMPLE_RATE:.2f}s ({len(master_pcm)} samples @ {SAMPLE_RATE}Hz mono)")
    print(f"Evidence decoded: {len(evid_pcm)/SAMPLE_RATE:.2f}s ({len(evid_pcm)} samples @ {SAMPLE_RATE}Hz mono)")

    print(f"\nThresholds production: MATCH_THRESHOLD={MATCH_THRESHOLD}  MIN_COVERAGE={MIN_COVERAGE}  DELTA_BIN_SIZE={DELTA_BIN_SIZE}")

    print("\nBuilding multi-variant index from master (v0 raw + v1..v5 broadcast_sim)...")
    refs = build_multi_variant_index(args.master, master_pcm)
    for vid, (_idx, total, n_h) in sorted(refs.items()):
        print(f"  v{vid}: {n_h} hashes / {total} frames")

    full_hashes = generate_fingerprint(evid_pcm)
    best = diagnose_full_clip(full_hashes, refs)
    vid, score, cov, ok, matched, top = best
    print(f"\nFull-clip vencedor: v{vid} score={score} cov={cov:.2f} matched={matched} detect={'YES' if ok else 'no'}")

    if not args.no_sliding:
        diagnose_sliding(evid_pcm, refs)


if __name__ == "__main__":
    main()
