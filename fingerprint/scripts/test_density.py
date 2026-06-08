"""
test_density.py — Valida o fix de DENSIDADE (#2 da auditoria) ANTES da migração.

Hipotese: o #77 (15s) some ao vivo porque a margem de match (UniqueScore pos-
shared + degradacao) cai abaixo do threshold em veiculacao marginal. O fix #2
encolhe o raio TEMPORAL do max-filter no peak-picking -> mais picos -> mais
hashes -> margem maior por janela.

Este script roda o match das censuras contra o #77 com o peak-picking ATUAL
(footprint 17x17) vs DENSO (#2: tempo 7, freq 13) — via monkeypatch das
constantes do generator (sem tocar no arquivo de producao) — e compara o pico
de score por janela de 4s. Mais score = mais margem pra sobreviver ao vivo.

Uso: python scripts/test_density.py
"""
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import evaluate_detection as ed  # noqa: E402
import fingerprint.generator as gen  # noqa: E402

SR = ed.SAMPLE_RATE
WIN = 64000
HOP = 32000
REFS_DIR = HERE.parent.parent / "audio-refs"
MASTER = REFS_DIR / "ASAAS - PLATAFORMA FINANCEIRA SPOT 15.mp3"

SETTINGS = [
    ("ATUAL  (17x17)", 17, 17),
    ("#2 DENSO (F13,T7)", 13, 7),
    ("#2 AGRESSIVO (F13,T5)", 13, 5),
]


def best_window_scores(censura_audio, refs):
    peak = 0
    n_ge7 = 0
    hashes_med = []
    for start in range(0, max(1, len(censura_audio) - WIN), HOP):
        q = gen.generate_fingerprint(censura_audio[start:start + WIN])
        hashes_med.append(len(q))
        best = 0
        for _vid, (idx, total, _n) in refs.items():
            score, _c, _o, _m = ed.match_against_reference(q, idx, total)
            best = max(best, score)
        if best >= 7:
            n_ge7 += 1
        peak = max(peak, best)
    med_h = sorted(hashes_med)[len(hashes_med) // 2] if hashes_med else 0
    return peak, n_ge7, med_h


def main():
    master_audio = ed.decode_to_pcm(str(MASTER))
    censuras = sorted([p for p in REFS_DIR.glob("*.mp3") if p.name[:1].isdigit()])

    print("=" * 96)
    print("TESTE DE DENSIDADE (#2) — pico de score do #77 por janela nas censuras")
    print("(mais score/hashes = mais margem pra UniqueScore sobreviver ao vivo)")
    print("=" * 96)

    for label, f, t in SETTINGS:
        gen.PEAK_NEIGHBORHOOD_F = f
        gen.PEAK_NEIGHBORHOOD_T = t
        refs = ed.build_multi_variant_index(str(MASTER), master_audio)
        ref0_hashes = refs[0][2]
        print(f"\n### {label}  | hashes do master(v0): {ref0_hashes}")
        for c in censuras:
            audio = ed.decode_to_pcm(str(c))
            peak, n_ge7, med_h = best_window_scores(audio, refs)
            nome = c.name.split(" - ")[2] if " - " in c.name else c.name[:20]
            print(f"  {nome:<14} pico_score={peak:>4}  janelas>=7={n_ge7:>2}  hashes/janela~{med_h}")

    print("\n" + "=" * 96)
    print("Leitura: se #2 sobe MUITO o pico_score do #77, a densidade da a margem")
    print("que faltava ao vivo -> fix = densidade (migracao Python+Go + re-fingerprint).")


if __name__ == "__main__":
    main()
