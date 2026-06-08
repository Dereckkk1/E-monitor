"""
diagnose_censura.py — Diagnóstico de falha REAL: qual CORTE do material tocou em
cada censura (air-check) e se o sistema casaria.

Replica produção: fingerprinta CADA master (ASAAS *.mp3) com o índice
multi-variante (v0 cru + 5 variantes broadcast_sim) e roda cada censura contra
TODOS os masters. A cobertura sobre o master revela QUAL corte realmente tocou
(o de maior cobertura), resolvendo o caso 15s SPOT vs 30s.

Convenção de nomes na pasta audio-refs/:
  - MASTER  : nome NÃO começa com dígito (ex.: "ASAAS - ... SPOT 15.mp3")
  - CENSURA : nome começa com a data (ex.: "05_06_2026 - ... Ouro Verde ....mp3")

Uso:
  python scripts/diagnose_censura.py
"""
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import evaluate_detection as ed  # noqa: E402

SAMPLE_RATE = ed.SAMPLE_RATE
WIN = 64000   # 4s
HOP = 32000   # 2s
THRESHOLD = 5
REFS_DIR = HERE.parent.parent / "audio-refs"


def match_full(q_hashes, refs):
    """Melhor (variante, score, cobertura, matched) entre todas as variantes."""
    best = None
    for vid, (idx, total, _n) in refs.items():
        score, cov, _ok, matched = ed.match_against_reference(q_hashes, idx, total)
        cand = {"variant": vid, "score": score, "cov": cov, "matched": matched, "total_ref": total}
        if best is None or score > best["score"]:
            best = cand
    return best


def sliding_peak(audio, refs):
    """Pico de score por janela de 4s (hop 2s) contra um master — replica o live."""
    peak = {"score": 0, "t": 0.0, "variant": None, "cov": 0.0}
    for start in range(0, max(1, len(audio) - WIN), HOP):
        b = match_full(ed.generate_fingerprint(audio[start:start + WIN]), refs)
        if b["score"] > peak["score"]:
            peak = {"score": b["score"], "t": start / SAMPLE_RATE,
                    "variant": b["variant"], "cov": b["cov"]}
    return peak


def is_censura(p: Path) -> bool:
    return p.name[:1].isdigit()


def main():
    print("=" * 100)
    print("DIAGNÓSTICO — qual corte do Asaas tocou em cada censura")
    print("=" * 100)

    files = sorted(REFS_DIR.glob("*.mp3"))
    masters = [p for p in files if not is_censura(p)]
    censuras = [p for p in files if is_censura(p)]

    if not masters:
        print("Nenhum MASTER encontrado (arquivo cujo nome NÃO começa com dígito).")
        return
    print(f"\nMasters ({len(masters)}):")
    master_refs = {}
    for m in masters:
        audio = ed.decode_to_pcm(str(m))
        dur = len(audio) / SAMPLE_RATE
        refs = ed.build_multi_variant_index(str(m), audio)
        master_refs[m.name] = refs
        print(f"  • {m.name}  ({dur:.1f}s, {refs[0][2]} hashes v0)")

    print(f"\nCensuras ({len(censuras)}):")
    for c in censuras:
        print("\n" + "-" * 100)
        print(f"CENSURA: {c.name}")
        audio = ed.decode_to_pcm(str(c))
        print(f"  duração: {len(audio) / SAMPLE_RATE:.1f}s")
        q_full = ed.generate_fingerprint(audio)

        # match completo contra cada master
        rows = []
        for mname, refs in master_refs.items():
            full = match_full(q_full, refs)
            rows.append((mname, full))
        rows.sort(key=lambda r: r[1]["cov"], reverse=True)

        print(f"  {'master':<42} {'var':>3} {'score':>6} {'cobertura':>10} {'frames':>10}")
        for mname, f in rows:
            short = mname.replace("ASAAS", "").replace(".mp3", "").strip(" -")[:40]
            print(f"  {short:<42} {f['variant']:>3} {f['score']:>6} {f['cov']:>9.2f} "
                  f"{int(f['cov']*f['total_ref'])}/{f['total_ref']:>3}")

        winner, wf = rows[0]
        wshort = winner.replace("ASAAS", "").replace(".mp3", "").strip(" -")
        peak = sliding_peak(audio, master_refs[winner])
        live = "DETECTARIA" if peak["score"] >= THRESHOLD else "NÃO detectaria"
        print(f"  >> TOCOU (maior cobertura): {wshort}  | cobertura {wf['cov']:.2f}")
        print(f"  >> ao vivo (janela 4s): pico {peak['score']} @ {peak['t']:.0f}s -> {live}")

    print("\n" + "=" * 100)


if __name__ == "__main__":
    main()
