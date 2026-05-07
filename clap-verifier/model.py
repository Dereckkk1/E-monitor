import os
import threading
import numpy as np
from pathlib import Path

MOCK_MODE = os.getenv("MOCK_MODE", "false").lower() == "true"
MODEL_PATH = Path(os.getenv("MODEL_PATH", "/models/clap_model.onnx"))

_lock = threading.Lock()
_session = None

MAX_PCM_BYTES = 4 * 16000 * 4 * 2  # 4s × 16kHz × float32 × 2× safety = 512 KB


def load_model():
    global _session
    if MOCK_MODE or _session is not None:
        return
    with _lock:
        if _session is not None:  # double-check after acquiring lock
            return
        import onnxruntime as ort
        _session = ort.InferenceSession(str(MODEL_PATH))


def embed(pcm_bytes: bytes, sr: int = 16000) -> list[float]:
    """Returns float[512] embedding from raw float32 PCM at 16kHz mono.

    When MOCK_MODE=true, returns a fixed vector for testing without a model file.
    """
    if MOCK_MODE:
        return [0.1] * 512
    if _session is None:
        raise RuntimeError("Model not loaded — call load_model() first")
    if sr != 16000:
        raise ValueError(f"Only sr=16000 is supported, got {sr}")
    if len(pcm_bytes) > MAX_PCM_BYTES:
        raise ValueError(
            f"PCM payload too large: {len(pcm_bytes)} bytes (max {MAX_PCM_BYTES})"
        )

    audio = np.frombuffer(pcm_bytes, dtype=np.float32)
    audio = audio / (np.abs(audio).max() + 1e-8)
    inp = audio[np.newaxis, :]
    outputs = _session.run(None, {"input": inp})
    return outputs[0][0].tolist()
