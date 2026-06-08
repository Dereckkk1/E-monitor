"""
replay_live.py — Replay LIVE-FIEL: roda a censura pelo MESMO caminho do worker
(janela 4s, hop 2s, MatchWindowDetailed por janela) + a state machine real
(Idle->Detecting->Confirmed em 2 janelas com cobertura), pra responder POR QUE
um spot curto some ao vivo mesmo casando forte offline.

Compara hop=2s (atual) vs hop=1s (fix LW-1 candidato).

State machine espelhada de workers/internal/match/statemachine.go + coverage.go:
  - Idle->Detecting quando score_da_janela >= min_score
  - Detecting: proxima janela com score>=min_score; cobertura =
    (elapsed*fps + 32)/total_frames (cap 1.0); confirma se >= min_cov
  - confirmTimeout 30s -> reset; cooldown = dur_master + 5s

Uso: python scripts/replay_live.py
"""
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import evaluate_detection as ed  # noqa: E402

SR = ed.SAMPLE_RATE
WIN = 64000          # 4s
FPS = SR / 2048.0    # 7.8125
FRAMES_PER_WINDOW = 32
MIN_SCORE = 7        # threshold calibrado dessas emissoras (station_thresholds)
MIN_COV = 0.15       # MinTemporalCoverage
CONFIRM_TIMEOUT = 30.0
REFS_DIR = HERE.parent.parent / "audio-refs"


def is_censura(p):
    return p.name[:1].isdigit()


def best_score_vs(q_hashes, refs):
    best = 0
    for _vid, (idx, total, _n) in refs.items():
        score, _cov, _ok, _m = ed.match_against_reference(q_hashes, idx, total)
        if score > best:
            best = score
    return best


def state_machine(timeline, total_frames):
    """timeline: lista (t_seg, score). Retorna lista de confirmacoes (t_confirm)."""
    state = "idle"
    first_t = 0.0
    cooldown_until = -1.0
    confirms = []
    n_qual = 0  # janelas com score>=min_score na fase detecting
    for t, score in timeline:
        if t < cooldown_until:
            continue
        if state == "idle":
            if score >= MIN_SCORE:
                state, first_t, n_qual = "detecting", t, 1
        elif state == "detecting":
            if t - first_t > CONFIRM_TIMEOUT:
                state = "idle"
                continue
            if score >= MIN_SCORE:
                n_qual += 1
                elapsed = t - first_t
                cov = min(1.0, (elapsed * FPS + FRAMES_PER_WINDOW) / total_frames)
                if cov >= MIN_COV:
                    confirms.append(t)
                    dur = total_frames / FPS
                    state, cooldown_until = "idle", t + dur + 5.0
    return confirms


def run(censura_audio, refs, total_frames, hop_samples):
    timeline = []
    for start in range(0, max(1, len(censura_audio) - WIN), hop_samples):
        q = ed.generate_fingerprint(censura_audio[start:start + WIN])
        timeline.append((start / SR, best_score_vs(q, refs)))
    qual = [(t, s) for t, s in timeline if s >= MIN_SCORE]
    confirms = state_machine(timeline, total_frames)
    return confirms, qual, timeline


def main():
    files = sorted(REFS_DIR.glob("*.mp3"))
    masters = [p for p in files if not is_censura(p)]
    censuras = [p for p in files if is_censura(p)]

    print("=" * 100)
    print(f"REPLAY LIVE-FIEL — state machine real (min_score={MIN_SCORE}, min_cov={MIN_COV}, 2 janelas+cobertura)")
    print("=" * 100)

    mrefs = {}
    for m in masters:
        a = ed.decode_to_pcm(str(m))
        refs = ed.build_multi_variant_index(str(m), a)
        total_frames = max(1, (len(a) - 4096) // 2048 + 1)
        mrefs[m.name] = (refs, total_frames)
        print(f"master: {m.name}  ({len(a)/SR:.1f}s, {total_frames} frames)")

    for c in censuras:
        print("\n" + "-" * 100)
        print(f"CENSURA: {c.name}")
        audio = ed.decode_to_pcm(str(c))
        for m in masters:
            refs, total_frames = mrefs[m.name]
            short = m.name.replace("ASAAS", "").replace(".mp3", "").strip(" -")[:34]
            for hop_s, hop_samples in (("2s", 32000), ("1s", 16000)):
                confirms, qual, _ = run(audio, refs, total_frames, hop_samples)
                qstr = ", ".join(f"{t:.0f}s:{s}" for t, s in qual[:8])
                verdict = f"CONFIRMA x{len(confirms)} @ {[f'{t:.0f}s' for t in confirms]}" if confirms else "NAO confirma"
                print(f"  [{short:<22}] hop {hop_s}: janelas>={MIN_SCORE}: {len(qual):>2}  ({qstr})")
                print(f"  {'':<24} -> {verdict}")
    print("\n" + "=" * 100)


if __name__ == "__main__":
    main()
