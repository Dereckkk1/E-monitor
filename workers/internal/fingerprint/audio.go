// Package fingerprint orchestrates the offline pipeline that turns a master
// audio file into a list of fingerprint hashes ready to be persisted in the
// fingerprint_hashes table. It is a thin orchestration layer on top of the
// reusable building blocks in pkg/audio (STFT, peak picking, hash generation).
//
// See §7 of plano_implementacao.md for the algorithmic specification.
package fingerprint

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os/exec"
)

// SampleRate is the analysis sample rate in Hz (§25 Apêndice B).
const SampleRate = 16000

// VariantID values match the variant_id column in fingerprint_hashes
// (SMALLINT). The plan describes three broadcast-simulation variants
// (clean/light/medium/heavy); for the PoC we ship only `clean`.
type VariantID uint8

const (
	VariantClean  VariantID = 0
	VariantLight  VariantID = 1 // TODO §7.2 — broadcast-simulation light not implemented
	VariantMedium VariantID = 2 // TODO §7.2 — broadcast-simulation medium not implemented
	VariantHeavy  VariantID = 3 // TODO §7.2 — broadcast-simulation heavy not implemented
)

// String returns a stable lower-case label for the variant (used by the CLI
// flag and by docs).
func (v VariantID) String() string {
	switch v {
	case VariantClean:
		return "clean"
	case VariantLight:
		return "light"
	case VariantMedium:
		return "medium"
	case VariantHeavy:
		return "heavy"
	default:
		return fmt.Sprintf("variant_%d", uint8(v))
	}
}

// ParseVariant parses a CLI-friendly label.
func ParseVariant(s string) (VariantID, error) {
	switch s {
	case "clean":
		return VariantClean, nil
	case "light":
		return VariantLight, nil
	case "medium":
		return VariantMedium, nil
	case "heavy":
		return VariantHeavy, nil
	default:
		return 0, fmt.Errorf("fingerprint: unknown variant %q (clean|light|medium|heavy)", s)
	}
}

// FFmpegFilter returns the -af filter chain used to derive `variant` from the
// master. Today only `clean` is implemented.
//
// TODO(§7.2): the medium/heavy variants need additional acompressor / alimiter
// stages plus AAC bitrate downsampling to match the plan. The infrastructure
// here keeps the call site stable so adding them is a matter of returning the
// proper filter chain plus an extra ffmpeg encode/decode pass.
func filterChain(variant VariantID) (string, error) {
	switch variant {
	case VariantClean:
		// Light analysis-only chain: loudness normalize, HPF/LPF as per §7.2 step 3.
		return "loudnorm=I=-16:LRA=11:TP=-1.5,highpass=f=80,lowpass=f=7500", nil
	case VariantLight, VariantMedium, VariantHeavy:
		return "", ErrVariantNotImplemented
	default:
		return "", fmt.Errorf("fingerprint: unknown variant %d", variant)
	}
}

// ErrVariantNotImplemented is returned by DecodePCM when called with a
// broadcast-simulation variant that has not been wired up yet.
var ErrVariantNotImplemented = fmt.Errorf("fingerprint: broadcast-simulation variants (light/medium/heavy) not implemented yet — see TODO §7.2")

// DecodePCM decodes the file at `path` to mono float32 PCM at SampleRate using
// ffmpeg as a subprocess. The filter chain depends on the requested variant.
//
// The returned slice contains [-1, +1] normalized samples. `ctx` controls the
// lifetime of the ffmpeg process.
func DecodePCM(ctx context.Context, path string, variant VariantID) ([]float32, error) {
	chain, err := filterChain(variant)
	if err != nil {
		return nil, err
	}

	args := []string{
		"-hide_banner", "-loglevel", "error",
		"-i", path,
		"-ac", "1",
		"-ar", fmt.Sprintf("%d", SampleRate),
		"-af", chain,
		"-f", "f32le",
		"-c:a", "pcm_f32le",
		"-",
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("fingerprint: ffmpeg decode %s: %w (stderr: %s)", path, err, stderr.String())
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("fingerprint: ffmpeg produced empty PCM for %s (stderr: %s)", path, stderr.String())
	}
	if len(out)%4 != 0 {
		return nil, fmt.Errorf("fingerprint: ffmpeg output not aligned to float32 (%d bytes)", len(out))
	}

	pcm := make([]float32, len(out)/4)
	for i := range pcm {
		bits := binary.LittleEndian.Uint32(out[i*4:])
		pcm[i] = math.Float32frombits(bits)
	}
	return pcm, nil
}
