import logging
import os
import subprocess
import tempfile

import numpy as np
import soundfile as sf

log = logging.getLogger(__name__)

SAMPLE_RATE = 16000

VARIANTS = {
    0: {
        "filters": "acompressor=threshold=-20dB:ratio=3:attack=5:release=50",
        "codec_args": ["-c:a", "aac", "-b:a", "96k"],
    },
    1: {
        "filters": "acompressor=threshold=-24dB:ratio=6:attack=2:release=80,alimiter=limit=0.95",
        "codec_args": ["-c:a", "aac", "-b:a", "64k"],
    },
    2: {
        "filters": "acompressor=threshold=-30dB:ratio=10:attack=1:release=100,alimiter=limit=0.98",
        "codec_args": ["-c:a", "libfdk_aac", "-profile:a", "aac_he", "-b:a", "48k"],
    },
    3: {
        # Weak-signal stream: light compression + AAC 64k + white noise -32dB
        "filters": "acompressor=threshold=-22dB:ratio=4:attack=3:release=60",
        "codec_args": ["-c:a", "aac", "-b:a", "64k"],
        "post_python": {"kind": "white", "db": -32},
    },
    4: {
        # Worst case AM-style: lowpass + heavy compression + AAC 48k + white noise -35dB
        "filters": "lowpass=f=3500,acompressor=threshold=-28dB:ratio=10:attack=2:release=70",
        "codec_args": ["-c:a", "aac", "-b:a", "48k"],
        "post_python": {"kind": "white", "db": -35},
    },
}


def _apply_post_python(audio: np.ndarray, post: dict) -> np.ndarray:
    """Apply optional post-processing (noise injection) in Python."""
    kind = post.get("kind")
    db = post.get("db", -30)
    amp = 10 ** (db / 20.0)
    seed = post.get("seed", 31337)
    if kind == "white":
        rng = np.random.default_rng(seed)
        return (audio + rng.normal(0, amp, len(audio)).astype(np.float32)).astype(np.float32)
    if kind == "hum":
        freq = post.get("freq", 60.0)
        t = np.arange(len(audio), dtype=np.float32) / SAMPLE_RATE
        return (audio + (amp * np.sin(2 * np.pi * freq * t)).astype(np.float32)).astype(np.float32)
    return audio


def _has_libfdk() -> bool:
    try:
        out = subprocess.run(["ffmpeg", "-hide_banner", "-encoders"],
                             capture_output=True, text=True, check=True).stdout
        return "libfdk_aac" in out
    except Exception:
        return False


def _ffmpeg_chain(input_path: str, filters: str, codec_args: list[str], encoded_path: str):
    cmd = ["ffmpeg", "-y", "-hide_banner", "-loglevel", "error",
           "-i", input_path,
           "-af", filters,
           *codec_args,
           encoded_path]
    subprocess.run(cmd, check=True)


def _decode_to_pcm(input_path: str) -> np.ndarray:
    """Decode any audio file to 16k mono float32."""
    with tempfile.NamedTemporaryFile(suffix=".wav", delete=False) as tmp:
        wav_path = tmp.name
    try:
        cmd = ["ffmpeg", "-y", "-hide_banner", "-loglevel", "error",
               "-i", input_path,
               "-ac", "1", "-ar", str(SAMPLE_RATE),
               "-f", "wav", "-c:a", "pcm_f32le",
               wav_path]
        subprocess.run(cmd, check=True)
        audio, sr = sf.read(wav_path, dtype="float32", always_2d=False)
        assert sr == SAMPLE_RATE, f"unexpected sr {sr}"
        return audio
    finally:
        try:
            os.unlink(wav_path)
        except OSError:
            pass


def simulate_variants(master_path: str) -> dict[int, np.ndarray]:
    """
    Apply broadcast simulation chains to the master and return decoded PCM
    (16k mono float32) for each variant.
    """
    has_fdk = _has_libfdk()
    out: dict[int, np.ndarray] = {}

    for variant_id, spec in VARIANTS.items():
        codec_args = spec["codec_args"]
        if "libfdk_aac" in codec_args and not has_fdk:
            log.warning("libfdk_aac unavailable; falling back to native aac for variant %d", variant_id)
            codec_args = ["-c:a", "aac", "-b:a", "48k"]

        with tempfile.NamedTemporaryFile(suffix=".m4a", delete=False) as tmp:
            encoded_path = tmp.name
        try:
            _ffmpeg_chain(master_path, spec["filters"], codec_args, encoded_path)
            out[variant_id] = _decode_to_pcm(encoded_path)
            post = spec.get("post_python")
            if post is not None:
                out[variant_id] = _apply_post_python(out[variant_id], post)
        finally:
            try:
                os.unlink(encoded_path)
            except OSError:
                pass

    return out
