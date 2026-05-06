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
// It outputs to two separate pipes using -f tee:
//   - pipe:3 (ExtraFiles[0]): ADTS AAC evidence stream
//   - pipe:4 (ExtraFiles[1]): f32le PCM analysis stream (16kHz mono)
type FFmpegProcess struct {
	cmd     *exec.Cmd
	aacRead *os.File // pipe:3 — ADTS AAC evidence stream (read end)
	pcmRead *os.File // pipe:4 — f32le PCM analysis stream (read end)
	log     *zap.Logger
}

// StartFFmpeg launches ffmpeg for the given stream URL.
// Returns the process with two readable streams:
//   - AACReader(): ADTS AAC evidence stream (pipe:3)
//   - PCMReader(): raw float32 PCM at 16kHz mono (pipe:4)
//
// The context is passed to exec.CommandContext but does NOT automatically
// kill the process on cancellation. Call Stop() to terminate the subprocess.
func StartFFmpeg(ctx context.Context, streamURL string, log *zap.Logger) (*FFmpegProcess, error) {
	// Create pipes for both outputs
	aacRead, aacWrite, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create aac pipe: %w", err)
	}

	pcmRead, pcmWrite, err := os.Pipe()
	if err != nil {
		aacRead.Close()
		aacWrite.Close()
		return nil, fmt.Errorf("create pcm pipe: %w", err)
	}

	// Two separate outputs on pipe:3 and pipe:4:
	// pipe:3 → ADTS AAC passthrough (evidence, no re-encode)
	// pipe:4 → f32le PCM 16kHz mono (analysis)
	args := []string{
		"-y",
		"-reconnect", "1",
		"-reconnect_streamed", "1",
		"-reconnect_delay_max", "5",
		"-reconnect_at_eof", "1",
		"-timeout", "10000000",
		"-user_agent", "VLC/3.0.20 LibVLC/3.0.20",
		"-i", streamURL,
		"-map", "0:a:0", "-c:a", "copy", "-f", "adts", "pipe:3",
		"-map", "0:a:0", "-ar", "16000", "-ac", "1", "-f", "f32le", "pipe:4",
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)

	// ExtraFiles adds file descriptors starting at 3.
	// pipe:3 in ffmpeg maps to ExtraFiles[0] (AAC write end)
	// pipe:4 in ffmpeg maps to ExtraFiles[1] (PCM write end)
	cmd.ExtraFiles = []*os.File{aacWrite, pcmWrite}

	// Start the process
	if err := cmd.Start(); err != nil {
		aacRead.Close()
		aacWrite.Close()
		pcmRead.Close()
		pcmWrite.Close()
		return nil, fmt.Errorf("ffmpeg start: %w", err)
	}

	// Close write ends in parent — ffmpeg owns them now.
	// Closing these allows ffmpeg to detect EOF when it finishes writing.
	aacWrite.Close()
	pcmWrite.Close()

	return &FFmpegProcess{
		cmd:     cmd,
		aacRead: aacRead,
		pcmRead: pcmRead,
		log:     log,
	}, nil
}

// PCMReader returns the pipe reader containing raw float32 PCM at 16kHz mono.
func (p *FFmpegProcess) PCMReader() io.Reader {
	return p.pcmRead
}

// AACReader returns the pipe reader for ADTS AAC evidence stream.
func (p *FFmpegProcess) AACReader() io.Reader {
	return p.aacRead
}

// Stop terminates the ffmpeg process and closes all pipes.
// It calls Process.Kill() to terminate the subprocess, then waits for it to exit.
func (p *FFmpegProcess) Stop() {
	if p.cmd.Process != nil {
		p.cmd.Process.Kill()
	}
	p.aacRead.Close()
	p.pcmRead.Close()
	_ = p.cmd.Wait() // reap the process
}

// Wait waits for the ffmpeg process to exit and returns any error.
func (p *FFmpegProcess) Wait() error {
	return p.cmd.Wait()
}
