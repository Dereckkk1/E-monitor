"""
evaluate_detection.py — End-to-end detection evaluation harness.

Generates (or loads) a master audio, applies 20 realistic broadcast degradation
profiles via ffmpeg + numpy, and reports detection success rate against the
master fingerprint as reference.

Usage:
  python evaluate_detection.py                    # synthetic master, 8s
  python evaluate_detection.py --master my.wav    # real master
  python evaluate_detection.py --seconds 12       # longer synthetic
"""

import argparse
import os
import re
import subprocess
import sys
import tempfile
import time
from collections import Counter, defaultdict
from pathlib import Path

import numpy as np
import soundfile as sf

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parent))
from fingerprint.generator import generate_fingerprint, SAMPLE_RATE  # noqa: E402
from fingerprint.broadcast_sim import simulate_variants  # noqa: E402

# Detection params (mirroring workers/internal/match)
DELTA_BIN_SIZE = 2
MATCH_THRESHOLD = 5
MIN_COVERAGE = 0.4

PROFILES = [
    {"name": "AAC 128k (clean ref)",              "filter": None,                                                                            "codec": ["-c:a", "aac", "-b:a", "128k"]},
    {"name": "AAC 96k",                           "filter": None,                                                                            "codec": ["-c:a", "aac", "-b:a", "96k"]},
    {"name": "AAC 64k",                           "filter": None,                                                                            "codec": ["-c:a", "aac", "-b:a", "64k"]},
    {"name": "AAC 48k",                           "filter": None,                                                                            "codec": ["-c:a", "aac", "-b:a", "48k"]},
    {"name": "AAC 32k (very low)",                "filter": None,                                                                            "codec": ["-c:a", "aac", "-b:a", "32k"]},
    {"name": "MP3 64k",                           "filter": None,                                                                            "codec": ["-c:a", "libmp3lame", "-b:a", "64k"]},
    {"name": "AM-style lowpass 4kHz",             "filter": "lowpass=f=4000",                                                                "codec": ["-c:a", "aac", "-b:a", "64k"]},
    {"name": "AM NRSC mask (5kHz lp + comp + hiss)", "filter": "lowpass=f=5000,acompressor=threshold=-26dB:ratio=6:attack=2:release=80",   "codec": ["-c:a", "aac", "-b:a", "64k"], "post": ("white", -33)},
    {"name": "FM pre-emphasis 75us boost",        "filter": "equalizer=f=4000:t=q:w=1:g=6,equalizer=f=8000:t=q:w=1:g=8",                     "codec": ["-c:a", "aac", "-b:a", "96k"]},
    {"name": "Heavy compression",                 "filter": "acompressor=threshold=-30dB:ratio=15:attack=1:release=80,alimiter=limit=0.95",  "codec": ["-c:a", "aac", "-b:a", "96k"]},
    {"name": "Loudness war (limiter)",            "filter": "acompressor=threshold=-25dB:ratio=8:attack=1:release=60,alimiter=limit=0.99",   "codec": ["-c:a", "aac", "-b:a", "96k"]},
    {"name": "AM modulation clipping (heavy clip)", "filter": "acompressor=threshold=-22dB:ratio=8:attack=1:release=60",                     "codec": ["-c:a", "aac", "-b:a", "64k"], "post": ("clipping", 0.85)},
    {"name": "Broadcast EQ (mid boost)",          "filter": "equalizer=f=2000:t=q:w=1:g=4,equalizer=f=4000:t=q:w=1:g=3",                     "codec": ["-c:a", "aac", "-b:a", "96k"]},
    {"name": "Multipath fading 0.3Hz",            "filter": None,                                                                            "codec": ["-c:a", "aac", "-b:a", "96k"], "post": ("fading", 0.3, 0.35)},
    {"name": "Cheap receiver IF (200-3500Hz bandpass)", "filter": "highpass=f=200,lowpass=f=3500",                                         "codec": ["-c:a", "aac", "-b:a", "64k"]},
    {"name": "Resample 8k roundtrip",             "filter": "aresample=8000,aresample=16000",                                                "codec": ["-c:a", "aac", "-b:a", "96k"]},
    {"name": "Stream rebuffering (100ms gap each 5s)", "filter": None,                                                                       "codec": ["-c:a", "aac", "-b:a", "96k"], "post": ("gaps", 100, 5.0)},
    {"name": "Hiss -30dB",                        "filter": None,                                                                            "codec": ["-c:a", "aac", "-b:a", "96k"], "post": ("white", -30)},
    {"name": "Hum 60Hz -25dB",                    "filter": None,                                                                            "codec": ["-c:a", "aac", "-b:a", "96k"], "post": ("hum60", -25)},
    {"name": "AM combo (lp+comp+hiss+48k)",       "filter": "lowpass=f=3500,acompressor=threshold=-28dB:ratio=10:attack=2:release=70",       "codec": ["-c:a", "aac", "-b:a", "48k"], "post": ("white", -35)},
]


def synthetic_master(seconds: float = 8.0) -> np.ndarray:
    """Build a structured synthetic audio: tones + impulses + low pink noise."""
    rng = np.random.default_rng(42)
    n = int(seconds * SAMPLE_RATE)
    t = np.arange(n) / SAMPLE_RATE
    audio = np.zeros(n, dtype=np.float32)

    for f, a, mod in [(330, 0.25, 0.7), (820, 0.20, 1.3), (1450, 0.18, 0.5),
                      (2300, 0.14, 1.7), (3500, 0.10, 0.9)]:
        envelope = 0.6 + 0.4 * np.sin(2 * np.pi * mod * t)
        audio += (a * envelope * np.sin(2 * np.pi * f * t)).astype(np.float32)

    for _ in range(int(seconds * 3)):
        idx = int(rng.uniform(0, n - 256))
        impulse = rng.normal(0, 0.4, 256).astype(np.float32)
        impulse *= np.exp(-np.arange(256) / 40.0)
        audio[idx:idx + 256] += impulse

    audio += rng.normal(0, 0.02, n).astype(np.float32)

    peak = float(np.max(np.abs(audio)))
    if peak > 0:
        audio = (audio * (0.7 / peak)).astype(np.float32)
    return audio


def write_wav(path: str, audio: np.ndarray):
    sf.write(path, audio, SAMPLE_RATE, subtype="FLOAT")


def decode_to_pcm(path: str) -> np.ndarray:
    with tempfile.NamedTemporaryFile(suffix=".wav", delete=False) as tmp:
        wav = tmp.name
    try:
        subprocess.run(
            ["ffmpeg", "-y", "-hide_banner", "-loglevel", "error", "-i", path,
             "-ac", "1", "-ar", str(SAMPLE_RATE), "-c:a", "pcm_f32le", "-f", "wav", wav],
            check=True,
        )
        audio, sr = sf.read(wav, dtype="float32", always_2d=False)
        assert sr == SAMPLE_RATE, f"unexpected sr {sr}"
        return audio.astype(np.float32)
    finally:
        try:
            os.unlink(wav)
        except OSError:
            pass


def apply_profile(master_path: str, profile: dict) -> np.ndarray:
    suffix = ".m4a" if "aac" in " ".join(profile["codec"]) else ".mp3"
    with tempfile.NamedTemporaryFile(suffix=suffix, delete=False) as tmp:
        out_path = tmp.name
    try:
        cmd = ["ffmpeg", "-y", "-hide_banner", "-loglevel", "error", "-i", master_path]
        if profile.get("filter"):
            cmd += ["-af", profile["filter"]]
        cmd += profile["codec"]
        cmd += [out_path]
        subprocess.run(cmd, check=True)
        audio = decode_to_pcm(out_path)
    finally:
        try:
            os.unlink(out_path)
        except OSError:
            pass

    post = profile.get("post")
    if post is not None:
        kind = post[0]
        if kind == "white":
            amp = 10 ** (post[1] / 20.0)
            n_rng = np.random.default_rng(13)
            audio = audio + n_rng.normal(0, amp, len(audio)).astype(np.float32)
        elif kind == "hum60":
            amp = 10 ** (post[1] / 20.0)
            t = np.arange(len(audio)) / SAMPLE_RATE
            audio = audio + (amp * np.sin(2 * np.pi * 60 * t)).astype(np.float32)
        elif kind == "fading":
            rate, depth = post[1], post[2]
            t = np.arange(len(audio)) / SAMPLE_RATE
            audio = (audio * (1.0 - depth + depth * np.sin(2 * np.pi * rate * t).astype(np.float32))).astype(np.float32)
        elif kind == "gaps":
            gap_ms, period_s = post[1], post[2]
            gap_samples = int(gap_ms * SAMPLE_RATE / 1000)
            period_samples = int(period_s * SAMPLE_RATE)
            a = audio.copy()
            for start in range(period_samples, len(a), period_samples):
                a[start:start + gap_samples] = 0
            audio = a
        elif kind == "clipping":
            level = post[1]
            audio = np.clip(audio, -level, level).astype(np.float32)
    return audio.astype(np.float32)


def match_against_reference(query_hashes, ref_index, total_ref_frames):
    """
    ref_index: dict[hash] -> list[ref_frame]
    Returns (best_score, coverage, detected, n_matched_hashes).
    """
    if not query_hashes:
        return 0, 0.0, False, 0

    bin_counts: Counter = Counter()
    bin_ref_offsets: dict = defaultdict(set)
    matched = 0
    for h, t_q in query_hashes:
        ref_list = ref_index.get(h)
        if not ref_list:
            continue
        matched += 1
        for t_r in ref_list:
            delta = t_r - t_q
            bin_idx = delta // DELTA_BIN_SIZE
            bin_counts[bin_idx] += 1
            bin_ref_offsets[bin_idx].add(t_r)

    if not bin_counts:
        return 0, 0.0, False, 0

    best_bin, best_score = bin_counts.most_common(1)[0]
    distinct_ref = len(bin_ref_offsets[best_bin])
    coverage = distinct_ref / max(total_ref_frames, 1)
    detected = best_score >= MATCH_THRESHOLD and coverage >= MIN_COVERAGE
    return best_score, coverage, detected, matched


def build_reference(audio: np.ndarray):
    hashes = generate_fingerprint(audio)
    idx: dict = defaultdict(list)
    for h, t in hashes:
        idx[h].append(t)
    total_frames = max(0, (len(audio) - 4096) // 2048 + 1)
    return idx, total_frames, len(hashes)


def build_multi_variant_index(master_path: str, master_audio: np.ndarray):
    """
    Build the same multi-variant reference the production index has:
    - variant 0: master direct (no broadcast simulation)
    - variants 1, 2, 3: broadcast_sim profiles applied to master
    Returns dict[variant_id] -> (idx, total_frames, n_hashes).
    """
    refs = {}
    # variant 0: raw master (what production does — original is always indexed)
    refs[0] = build_reference(master_audio)
    # variants 1..3: broadcast_sim outputs (production has these too)
    sim_pcms = simulate_variants(master_path)
    for vid, pcm in sim_pcms.items():
        refs[vid + 1] = build_reference(pcm)
    return refs


def match_multi_variant(query_hashes, refs):
    """
    Run match against each variant; return (best_result, detected_anywhere).
    best_result: dict with variant, score, cov, matched, ok
    """
    results = []
    for vid, (idx, total, _n) in refs.items():
        score, cov, ok, matched = match_against_reference(query_hashes, idx, total)
        results.append({
            "variant": vid, "score": score, "cov": cov,
            "matched": matched, "ok": ok,
        })
    detected = [r for r in results if r["ok"]]
    if detected:
        return max(detected, key=lambda r: r["score"]), True
    # nothing detected — return whichever had the highest score (for diagnostics)
    return max(results, key=lambda r: r["score"]), False


def _slug(name: str) -> str:
    s = re.sub(r"[^a-z0-9]+", "_", name.lower()).strip("_")
    return s or "profile"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--master", help="WAV/MP3 file as master (default: synthetic)")
    ap.add_argument("--seconds", type=float, default=8.0, help="Synthetic audio duration")
    ap.add_argument("--export-audio", help="Directory to save degraded WAVs (one per profile)")
    args = ap.parse_args()

    export_dir = None
    if args.export_audio:
        export_dir = Path(args.export_audio)
        export_dir.mkdir(parents=True, exist_ok=True)
        print(f"Exporting degraded audios to {export_dir}")

    print("=" * 82)
    print("Radiocheck — Detection Evaluation Harness")
    print("=" * 82)
    t0 = time.time()

    with tempfile.TemporaryDirectory() as td:
        if args.master:
            audio = decode_to_pcm(args.master)
            master_path = args.master
            print(f"Master: {master_path}  ({len(audio) / SAMPLE_RATE:.1f}s)")
        else:
            audio = synthetic_master(args.seconds)
            master_path = os.path.join(td, "master.wav")
            write_wav(master_path, audio)
            print(f"Master: synthetic  ({len(audio) / SAMPLE_RATE:.1f}s)")

        print("Building multi-variant reference index "
              "(production has v0=raw + v1..v3=broadcast_sim)...")
        t_ref = time.time()
        refs = build_multi_variant_index(master_path, audio)
        for vid, (_idx, total, n_h) in sorted(refs.items()):
            print(f"  variant {vid}: {n_h} hashes / {total} frames")
        print(f"  built in {time.time() - t_ref:.1f}s")
        print()
        print(f"{'#':>2}  {'Profile':<40} {'Hashes':>7} {'Var':>3} "
              f"{'Score':>6} {'Cov':>6}  Result")
        print("-" * 82)

        if export_dir:
            ref_path = export_dir / "00_master_as_seen_by_system.wav"
            sf.write(str(ref_path), audio, SAMPLE_RATE, subtype="PCM_16")

        detected = 0
        rows = []  # for the markdown report
        for i, p in enumerate(PROFILES, 1):
            try:
                degraded = apply_profile(master_path, p)
                if export_dir:
                    out_wav = export_dir / f"{i:02d}_{_slug(p['name'])}.wav"
                    sf.write(str(out_wav), degraded, SAMPLE_RATE, subtype="PCM_16")
                q_hashes = generate_fingerprint(degraded)
                best, ok = match_multi_variant(q_hashes, refs)
                mark = "OK" if ok else "MISS"
                if ok:
                    detected += 1
                print(f"{i:>2}  {p['name']:<40} {len(q_hashes):>7} "
                      f"{best['variant']:>3} {best['score']:>6} {best['cov']:>6.2f}  {mark}")
                rows.append({
                    "n": i, "name": p["name"], "n_hashes": len(q_hashes),
                    "variant": best["variant"], "score": best["score"],
                    "coverage": best["cov"], "detected": ok,
                })
            except subprocess.CalledProcessError:
                print(f"{i:>2}  {p['name']:<40} {'-':>7} {'-':>3} {'-':>6} {'-':>6}  FFMPEG ERR")
                rows.append({"n": i, "name": p["name"], "error": "ffmpeg"})
            except Exception as e:
                print(f"{i:>2}  {p['name']:<40} {'-':>7} {'-':>3} {'-':>6} {'-':>6}  ERROR: {e}")
                rows.append({"n": i, "name": p["name"], "error": str(e)})

        print("-" * 82)
        rate = 100 * detected / len(PROFILES)
        print(f"Detected: {detected}/{len(PROFILES)} ({rate:.0f}%)   "
              f"total {time.time() - t0:.1f}s")


if __name__ == "__main__":
    main()
