package audit

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os/exec"
)

// DecodeADTSToPCM runs ffmpeg to convert raw ADTS-AAC bytes (the format the
// evidence segment muxer writes to disk) into float32 mono 16kHz PCM samples,
// matching the stream worker's analysis-time format.
//
// Reads stdin → writes stdout, no temp files. The "-f aac" demuxer flag is
// required so ffmpeg doesn't guess the container.
func DecodeADTSToPCM(aacData []byte) ([]float32, error) {
	return decodePCM(aacData, "aac")
}

// DecodeM4AToPCM converts an m4a/MP4-wrapped AAC blob to PCM. Used by the
// offline audit CLI and tests that have m4a files at hand.
func DecodeM4AToPCM(m4aData []byte) ([]float32, error) {
	return decodePCM(m4aData, "mp4")
}

func decodePCM(audioData []byte, inputFormat string) ([]float32, error) {
	cmd := exec.Command("ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-f", inputFormat,
		"-i", "pipe:0",
		"-ac", "1", "-ar", "16000",
		"-f", "f32le", "-c:a", "pcm_f32le",
		"pipe:1",
	)
	cmd.Stdin = bytes.NewReader(audioData)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg decode (%s): %w (stderr: %s)", inputFormat, err, stderr.String())
	}
	if len(out)%4 != 0 {
		return nil, fmt.Errorf("ffmpeg output not aligned to float32: %d bytes", len(out))
	}
	pcm := make([]float32, len(out)/4)
	for i := range pcm {
		bits := binary.LittleEndian.Uint32(out[i*4:])
		pcm[i] = math.Float32frombits(bits)
	}
	return pcm, nil
}
