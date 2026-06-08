import numpy as np
from scipy.signal import stft
from scipy.ndimage import maximum_filter

SAMPLE_RATE = 16000
WINDOW_SIZE = 4096
HOP_SIZE = 2048

# Peak max-filter footprint (freq, time).
# #2 short-audio recall: shrunk from 17x17 to 13x7. A smaller TEMPORAL footprint
# emits ~4x more peaks/hashes, giving 5-15s spots the match margin to survive
# broadcast degradation (validated on real lost air-checks).
# LOCKSTEP: must match workers/pkg/audio/peaks.go
#   neighborFrames = 3 (= (PEAK_NEIGHBORHOOD_T-1)/2)
#   neighborBins   = 6 (= (PEAK_NEIGHBORHOOD_F-1)/2)
# Changing these changes the hash MATH — the whole catalog must be
# re-fingerprinted atomically on deploy.
PEAK_NEIGHBORHOOD_F = 13
PEAK_NEIGHBORHOOD_T = 7
PEAK_AMPLITUDE_PERCENTILE = 80

TARGET_ZONE_T_MIN = 1
TARGET_ZONE_T_MAX = 24
TARGET_ZONE_F = 50
FAN_OUT = 8

FREQ_MIN_BIN = 25
FREQ_MAX_BIN = 1024


def _preprocess(audio: np.ndarray) -> np.ndarray:
    """High-pass at 100Hz + RMS normalize to -20 dBFS."""
    rc = 1.0 / (2.0 * np.pi * 100.0)
    dt = 1.0 / SAMPLE_RATE
    alpha = rc / (rc + dt)
    out = np.empty_like(audio)
    out[0] = audio[0]
    for i in range(1, len(audio)):
        out[i] = alpha * (out[i - 1] + audio[i] - audio[i - 1])

    rms = float(np.sqrt(np.mean(out * out)))
    if rms > 1e-6:
        target = 10 ** (-20.0 / 20.0)
        out = out * (target / rms)
    return out.astype(np.float32)


def generate_fingerprint(audio: np.ndarray, sample_rate: int = SAMPLE_RATE) -> list[tuple[int, int]]:
    """
    Generate (hash, time_frame) pairs from an audio segment.
    Hash layout (32-bit):
      bits 23-31: f1 (9 bits)
      bits 14-22: f2 (9 bits)
      bits  0-13: dt (14 bits)
    """
    assert sample_rate == SAMPLE_RATE, f"expected {SAMPLE_RATE}, got {sample_rate}"

    audio = _preprocess(audio)

    f, t, Zxx = stft(
        audio,
        fs=sample_rate,
        nperseg=WINDOW_SIZE,
        noverlap=WINDOW_SIZE - HOP_SIZE,
        return_onesided=True,
        boundary=None,
        padded=False,
    )
    magnitude = np.abs(Zxx)
    magnitude = magnitude[FREQ_MIN_BIN:FREQ_MAX_BIN, :]

    log_mag = np.log1p(magnitude)
    neighborhood = np.ones((PEAK_NEIGHBORHOOD_F, PEAK_NEIGHBORHOOD_T))
    local_max = maximum_filter(log_mag, footprint=neighborhood) == log_mag
    threshold = np.percentile(log_mag, PEAK_AMPLITUDE_PERCENTILE)
    peaks_mask = local_max & (log_mag > threshold)

    freq_bins, time_frames = np.where(peaks_mask)
    peaks = sorted(zip(time_frames.tolist(), freq_bins.tolist()))

    hashes: list[tuple[int, int]] = []
    for i, (t1, f1) in enumerate(peaks):
        produced = 0
        for j in range(i + 1, len(peaks)):
            if produced >= FAN_OUT:
                break
            t2, f2 = peaks[j]
            dt = t2 - t1
            if dt < TARGET_ZONE_T_MIN:
                continue
            if dt > TARGET_ZONE_T_MAX:
                break
            if abs(f2 - f1) > TARGET_ZONE_F:
                continue
            f1_bits = f1 & 0x1FF
            f2_bits = f2 & 0x1FF
            dt_bits = dt & 0x3FFF
            hash_value = (f1_bits << 23) | (f2_bits << 14) | dt_bits
            hashes.append((int(hash_value), int(t1)))
            produced += 1

    return hashes
