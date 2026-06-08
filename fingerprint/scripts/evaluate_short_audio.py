"""
evaluate_short_audio.py — Baseline de RECALL por DURAÇÃO (Fase 0 do plano de
short-audio). Reusa as peças de evaluate_detection.py (índice multi-variante de
produção + 20 perfis de degradação broadcast) e varre durações 5/10/15/30s para
responder: "quanto do anúncio curto a gente detecta hoje, por perfil de
degradação?".

Gates alinhados ao LIVE (workers/internal/supervisor/supervisor.go):
  - LIVE_THRESHOLD = 5   (MatchThreshold default)
  - LIVE_MIN_COV   = 0.15 (MinTemporalCoverage)
Reporta DOIS recalls:
  - rec_score: best_score >= 5            (gate efetivo do live p/ curto, pois a
                                           cobertura temporal é inerte <=~27s)
  - rec_live:  best_score >= 5 E cov>=0.15 (gate com cobertura, estilo §9.9 audit)

Uso:
  python scripts/evaluate_short_audio.py                          # sintético
  python scripts/evaluate_short_audio.py --master spot.mp3        # real (trimado por duração)
  python scripts/evaluate_short_audio.py --durations 5,10,15,30 --out baseline.md
"""
import argparse
import os
import statistics
import subprocess
import sys
import tempfile
import time
from pathlib import Path

import numpy as np
import soundfile as sf

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import evaluate_detection as ed  # noqa: E402

SAMPLE_RATE = ed.SAMPLE_RATE
LIVE_THRESHOLD = 5     # supervisor.go defaultMatchThreshold
LIVE_MIN_COV = 0.15    # supervisor.go MinTemporalCoverage


def trim_master(master_path: str, seconds: float, td: str):
    out = os.path.join(td, f"master_{seconds}s.wav")
    subprocess.run(
        ["ffmpeg", "-y", "-hide_banner", "-loglevel", "error", "-i", master_path,
         "-t", str(seconds), "-ac", "1", "-ar", str(SAMPLE_RATE),
         "-c:a", "pcm_f32le", out],
        check=True,
    )
    audio, sr = sf.read(out, dtype="float32", always_2d=False)
    assert sr == SAMPLE_RATE
    return audio.astype(np.float32), out


def best_across_variants(q_hashes, refs):
    """Maior score entre TODAS as variantes (o que o matcher Go faz)."""
    best = None
    for vid, (idx, total, _n) in refs.items():
        score, cov, _ok, matched = ed.match_against_reference(q_hashes, idx, total)
        if best is None or score > best["score"]:
            best = {"variant": vid, "score": score, "cov": cov, "matched": matched}
    return best


def run_duration(seconds: float, master_path: str, audio: np.ndarray):
    refs = ed.build_multi_variant_index(master_path, audio)
    n_ref0 = refs[0][2]  # hashes da variante 0 (master cru)
    per = []
    for p in ed.PROFILES:
        try:
            degraded = ed.apply_profile(master_path, p)
            q = ed.generate_fingerprint(degraded)
            b = best_across_variants(q, refs)
            det_score = b["score"] >= LIVE_THRESHOLD
            det_live = det_score and b["cov"] >= LIVE_MIN_COV
            per.append({
                "name": p["name"], "n_hashes": len(q), "score": b["score"],
                "cov": b["cov"], "variant": b["variant"],
                "det_score": det_score, "det_live": det_live,
            })
        except Exception as e:  # noqa: BLE001
            per.append({"name": p["name"], "error": str(e)})
    return n_ref0, per


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--master", help="WAV/MP3 master real (trimado por duração). Default: sintético.")
    ap.add_argument("--durations", default="5,10,15,30")
    ap.add_argument("--out", help="Arquivo .md para salvar o baseline.")
    args = ap.parse_args()
    durations = [float(x) for x in args.durations.split(",")]

    print("=" * 90)
    print("Radiocheck — Baseline de Recall por Duração (short-audio, Fase 0)")
    print(f"gates: score>=%d ; live = score>=%d & cov>=%.2f" % (LIVE_THRESHOLD, LIVE_THRESHOLD, LIVE_MIN_COV))
    print("=" * 90)
    t0 = time.time()

    summary = []
    detail = []
    with tempfile.TemporaryDirectory() as td:
        for sec in durations:
            if args.master:
                audio, mp = trim_master(args.master, sec, td)
                src = Path(args.master).name
            else:
                audio = ed.synthetic_master(sec)
                mp = os.path.join(td, f"syn_{sec}.wav")
                ed.write_wav(mp, audio)
                src = "sintético"
            n_ref0, per = run_duration(sec, mp, audio)
            ok = [r for r in per if "error" not in r]
            if not ok:
                continue
            rec_score = 100 * sum(r["det_score"] for r in ok) / len(ok)
            rec_live = 100 * sum(r["det_live"] for r in ok) / len(ok)
            med_hash = statistics.median([r["n_hashes"] for r in ok])
            med_score = statistics.median([r["score"] for r in ok])
            miss = [r["name"] for r in ok if not r["det_score"]]
            summary.append({
                "sec": sec, "src": src, "ref_hashes": n_ref0,
                "med_query_hashes": med_hash, "med_score": med_score,
                "rec_score": rec_score, "rec_live": rec_live,
                "n": len(ok), "miss": miss,
            })
            detail.append((sec, per))

    # ---- console ----
    print()
    print(f"{'Dur':>5} {'refHash':>8} {'qHash~med':>9} {'score~med':>9} "
          f"{'rec(score)':>11} {'rec(live)':>10}  Misses (score<5)")
    print("-" * 90)
    for s in summary:
        miss_txt = ", ".join(s["miss"]) if s["miss"] else "—"
        print(f"{s['sec']:>4.0f}s {s['ref_hashes']:>8} {s['med_query_hashes']:>9.0f} "
              f"{s['med_score']:>9.0f} {s['rec_score']:>10.0f}% {s['rec_live']:>9.0f}%  {miss_txt}")
    print("-" * 90)
    print(f"total {time.time() - t0:.1f}s")

    # ---- markdown ----
    if args.out:
        lines = []
        lines.append("# Baseline de recall por duração (short-audio · Fase 0)\n")
        lines.append(f"Gates: `score>={LIVE_THRESHOLD}` (gate efetivo do live para curto) e "
                     f"`live = score>={LIVE_THRESHOLD} & cov>={LIVE_MIN_COV}`. "
                     f"Fonte do master: **{summary[0]['src'] if summary else '—'}**. "
                     f"Índice = v0 cru + 5 variantes broadcast_sim. 20 perfis de degradação.\n")
        lines.append("| Duração | hashes ref (v0) | hashes query (med) | score (med) | recall (score≥5) | recall (live) |")
        lines.append("|--------:|----------------:|-------------------:|------------:|-----------------:|--------------:|")
        for s in summary:
            lines.append(f"| {s['sec']:.0f}s | {s['ref_hashes']} | {s['med_query_hashes']:.0f} | "
                         f"{s['med_score']:.0f} | {s['rec_score']:.0f}% | {s['rec_live']:.0f}% |")
        lines.append("\n## Misses por duração (perfis com score < 5)\n")
        for s in summary:
            miss_txt = ", ".join(s["miss"]) if s["miss"] else "_(nenhum)_"
            lines.append(f"- **{s['sec']:.0f}s**: {miss_txt}")
        Path(args.out).write_text("\n".join(lines) + "\n", encoding="utf-8")
        print(f"\nMarkdown salvo em {args.out}")


if __name__ == "__main__":
    main()
