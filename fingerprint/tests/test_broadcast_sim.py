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
def test_simulate_variants_returns_five(tone_master):
    out = simulate_variants(tone_master)
    assert set(out.keys()) == {0, 1, 2, 3, 4}
    for v_id, audio in out.items():
        assert isinstance(audio, np.ndarray)
        assert audio.dtype == np.float32
        assert 40000 < len(audio) < 60000, f"variant {v_id} has {len(audio)} samples"


@pytest.fixture
def video_master():
    """A 'master' that carries an h264 video stream — e.g. an .mp4 uploaded as a
    material's audio, or an audio file with h264 cover art. broadcast_sim must
    strip the video (-vn) instead of choking on it. Regression: prod 2026-06-08,
    ffmpeg exit 234 'Could not find tag for codec h264' (the .m4a output uses the
    `ipod` muxer, which has no h264 video tag) on a /data/masters file."""
    with tempfile.NamedTemporaryFile(suffix=".mp4", delete=False) as f:
        path = f.name
    try:
        subprocess.run(
            ["ffmpeg", "-y", "-hide_banner", "-loglevel", "error",
             "-f", "lavfi", "-i", "sine=frequency=1000:duration=3",
             "-f", "lavfi", "-i", "testsrc=size=64x64:rate=10:duration=3",
             "-c:a", "aac", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-shortest",
             path],
            check=True, capture_output=True)
    except Exception:
        try:
            os.unlink(path)
        except OSError:
            pass
        pytest.skip("ffmpeg cannot build an h264 fixture (libx264 unavailable)")
    yield path
    try:
        os.unlink(path)
    except OSError:
        pass


@pytest.mark.skipif(not _ffmpeg_available(), reason="ffmpeg not installed")
def test_simulate_variants_strips_video_stream(video_master):
    """A master carrying a video stream must still produce all 5 audio variants
    (broadcast_sim must drop the video, not fail). Regression: prod 2026-06-08.

    NOTE: passes even on the buggy code with a lenient ffmpeg build (Windows
    re-encodes/drops the video). The deterministic guard is
    test_ffmpeg_chain_drops_video below — strict builds (alpine) DO break."""
    out = simulate_variants(video_master)
    assert set(out.keys()) == {0, 1, 2, 3, 4}
    for v_id, audio in out.items():
        assert isinstance(audio, np.ndarray)
        assert len(audio) > 0, f"variant {v_id} is empty"


def test_ffmpeg_chain_drops_video(monkeypatch):
    """Deterministic regression (prod 2026-06-08): the variant encoder MUST pass
    -vn. Without it, a master carrying a video stream (h264 cover art, or an .mp4
    uploaded as audio) makes ffmpeg map the video into the .m4a output, whose
    `ipod` muxer has no h264 tag -> exit 234, fingerprint fails. Asserts the
    command rather than the behaviour because the failure is specific to strict
    ffmpeg builds (alpine) and is not reproduced by lenient local builds."""
    captured = {}

    def fake_run(cmd, **kwargs):
        captured["cmd"] = cmd
        return subprocess.CompletedProcess(cmd, 0)

    monkeypatch.setattr("fingerprint.broadcast_sim.subprocess.run", fake_run)
    from fingerprint import broadcast_sim
    broadcast_sim._ffmpeg_chain("in.mp3", "acompressor=x", ["-c:a", "aac"], "out.m4a")
    assert "-vn" in captured["cmd"], f"video stream not disabled: {captured['cmd']}"
