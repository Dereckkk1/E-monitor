package ingestor

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"

	"go.uber.org/zap"
)

// FFmpegProcess wraps a running ffmpeg subprocess.
//
// ffmpeg is invoked with two outputs:
//   - The ADTS-AAC evidence stream is written DIRECTLY to disk via the
//     segment muxer (one rotating file every SegmentDuration seconds, named
//     by wall-clock strftime). The Go side never reads these bytes through a
//     pipe — see internal/segments for the read path.
//   - The f32le PCM analysis stream is read from pipe:3 by the matcher.
//
// We dropped pipe:3 for AAC because keeping the evidence in an in-memory
// ring buffer keyed on time.Now() lost audio whenever ffmpeg reconnected,
// the worker restarted, or wall-clock drift accumulated against the AAC
// arrival timestamps. Anchoring evidence to disk and to ffmpeg's own
// stream-time PTS makes the capture survive process restarts and
// reconnects, and produces files an operator can `ffplay` directly.
type FFmpegProcess struct {
	cmd     *exec.Cmd
	pcmRead *os.File // pipe:3 — f32le PCM analysis stream (read end)
	log     *zap.Logger
}

// StartFFmpeg launches ffmpeg for the given stream URL.
//
// segmentsDir must already exist; ffmpeg will write rotating ADTS files
// named with strftime there (see internal/segments.FFmpegOutputPattern for
// the path used by callers).
//
// The context is passed to exec.CommandContext but does NOT automatically
// kill the process on cancellation. Call Stop() to terminate the subprocess.
func StartFFmpeg(ctx context.Context, streamURL, segmentsOutputPattern string, log *zap.Logger) (*FFmpegProcess, error) {
	if segmentsOutputPattern == "" {
		return nil, fmt.Errorf("ffmpeg: segmentsOutputPattern is required")
	}

	pcmRead, pcmWrite, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create pcm pipe: %w", err)
	}

	// Two outputs:
	//   1. Segment muxer → disk (evidence). -segment_atclocktime aligns
	//      rotation with multiples of segment_time from wall-clock zero so
	//      file boundaries are predictable. -reset_timestamps starts each
	//      file's PTS at zero, which is what the readers expect when they
	//      `cat`-concat. -strftime expands %Y%m%d-%H%M%S in the filename.
	//   2. f32le PCM @ 16kHz mono → pipe:3 (analysis).
	args := []string{
		"-y",
		"-reconnect", "1",
		"-reconnect_streamed", "1",
		"-reconnect_delay_max", "5",
		"-reconnect_at_eof", "1",
		"-timeout", "10000000",
		"-user_agent", "VLC/3.0.20 LibVLC/3.0.20",
		"-i", streamURL,

		"-map", "0:a:0",
		"-c:a", "copy",
		"-f", "segment",
		"-segment_time", "30",
		"-segment_format", "adts",
		"-segment_atclocktime", "1",
		"-reset_timestamps", "1",
		"-strftime", "1",
		segmentsOutputPattern,

		"-map", "0:a:0",
		"-ar", "16000",
		"-ac", "1",
		"-f", "f32le",
		"pipe:3",
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)

	// ExtraFiles[0] becomes pipe:3 in ffmpeg-land — the PCM write end.
	cmd.ExtraFiles = []*os.File{pcmWrite}

	if err := cmd.Start(); err != nil {
		pcmRead.Close()
		pcmWrite.Close()
		return nil, fmt.Errorf("ffmpeg start: %w", err)
	}

	// ffmpeg owns the write end now; closing here lets it observe EOF when
	// it shuts down.
	pcmWrite.Close()

	return &FFmpegProcess{
		cmd:     cmd,
		pcmRead: pcmRead,
		log:     log,
	}, nil
}

// PCMReader returns the pipe reader containing raw float32 PCM at 16kHz mono.
func (p *FFmpegProcess) PCMReader() io.Reader {
	return p.pcmRead
}

// Stop terminates the ffmpeg process and closes all pipes.
// It calls Process.Kill() to terminate the subprocess, then waits for it to exit.
func (p *FFmpegProcess) Stop() {
	if p.cmd.Process != nil {
		p.cmd.Process.Kill()
	}
	p.pcmRead.Close()
	_ = p.cmd.Wait() // reap the process
}

// Wait waits for the ffmpeg process to exit and returns any error.
func (p *FFmpegProcess) Wait() error {
	return p.cmd.Wait()
}
