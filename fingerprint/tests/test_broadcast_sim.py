import os
import subprocess
import tempfile

import numpy as np
import pytest
import soundfile as sf

from fingerprint.broadcast_sim import simulate_variants, SAMPLE_RATE


@pytest.fixture
def tone_master():
    """Generate a 3-second 1kHz tone WAV as a master file."""
    sr = 44100
    duration = 3.0
    t = np.linspace(0, duration, int(sr * duration), endpoint=False)
    audio = (0.5 * np.sin(2 * np.pi * 1000 * t)).astype(np.float32)
    with tempfile.NamedTemporaryFile(suffix=".wav", delete=False) as f:
        path = f.name
    sf.write(path, audio, sr)
    yield path
    try:
        os.unlink(path)
    except OSError:
        pass


def _ffmpeg_available():
    try:
        subprocess.run(["ffmpeg", "-version"], capture_output=True, check=True)
        return True
    except Exception:
        return False


@pytest.mark.skipif(not _ffmpeg_available(), reason="ffmpeg not installed")
def test_simulate_variants_returns_three(tone_master):
    out = simulate_variants(tone_master)
    assert set(out.keys()) == {0, 1, 2}
    for v_id, audio in out.items():
        assert isinstance(audio, np.ndarray)
        assert audio.dtype == np.float32
        assert 40000 < len(audio) < 60000, f"variant {v_id} has {len(audio)} samples"
