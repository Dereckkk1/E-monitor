import numpy as np
import pytest

from fingerprint.generator import generate_fingerprint, SAMPLE_RATE


def _make_signal(duration_s: float = 3.0, freq: float = 1500.0) -> np.ndarray:
    n = int(duration_s * SAMPLE_RATE)
    t = np.linspace(0, duration_s, n, endpoint=False)
    sig = (
        0.4 * np.sin(2 * np.pi * freq * t)
        + 0.2 * np.sin(2 * np.pi * (freq * 1.5) * t)
        + 0.1 * np.random.RandomState(42).randn(n)
    )
    return sig.astype(np.float32)


def test_generates_hashes_from_signal():
    audio = _make_signal()
    hashes = generate_fingerprint(audio)
    assert len(hashes) > 0
    for h, t in hashes:
        assert 0 <= h < (1 << 32)
        assert t >= 0


def test_same_input_gives_same_hashes():
    audio = _make_signal()
    h1 = generate_fingerprint(audio)
    h2 = generate_fingerprint(audio)
    assert h1 == h2


def test_silence_produces_few_or_no_hashes():
    audio = np.zeros(SAMPLE_RATE * 3, dtype=np.float32)
    hashes = generate_fingerprint(audio)
    assert len(hashes) < 50
