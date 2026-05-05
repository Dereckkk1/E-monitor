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
type FFmpegProcess struct {
	cmd      *exec.Cmd
	stdout   io.ReadCloser // PCM f32le 16kHz mono
	aacRead  *os.File      // AAC fMP4 stream (read end)
	aacWrite *os.File      // AAC fMP4 stream (write end, held for lifecycle)
	log      *zap.Logger
}

// StartFFmpeg launches ffmpeg for the given stream URL.
// Returns the process with two readable streams:
//   - PCMReader(): raw float32 PCM at 16kHz mono (stdout)
//   - AACReader(): AAC fMP4 stream (pipe:3 / ExtraFiles[0])
//
// The context is passed to exec.CommandContext but does NOT automatically
// kill the process on cancellation. Call Stop() to terminate the subprocess.
func StartFFmpeg(ctx context.Context, streamURL string, log *zap.Logger) (*FFmpegProcess, error) {
	// Create a pipe for AAC output (pipe:3)
	aacRead, aacWrite, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create aac pipe: %w", err)
	}

	// Build ffmpeg command arguments per §8.2
	args := []string{
		"-reconnect", "1",
		"-reconnect_streamed", "1",
		"-reconnect_delay_max", "10",
		"-i", streamURL,
		"-filter_complex", "[0:a]asplit=2[pcm_out][aac_out]",
		"-map", "[pcm_out]", "-ar", "16000", "-ac", "1", "-f", "f32le", "pipe:1",
		"-map", "[aac_out]", "-c:a", "aac", "-b:a", "128k", "-f", "mp4",
		"-movflags", "frag_keyframe+empty_moov", "pipe:3",
		"-loglevel", "error",
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)

	// ExtraFiles adds file descriptors starting at 3.
	// pipe:3 in ffmpeg maps to ExtraFiles[0]
	cmd.ExtraFiles = []*os.File{aacWrite}

	// Get stdout pipe for PCM output
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		aacRead.Close()
		aacWrite.Close()
		return nil, fmt.Errorf("get stdout pipe: %w", err)
	}

	// Start the process
	if err := cmd.Start(); err != nil {
		aacRead.Close()
		aacWrite.Close()
		stdout.Close()
		return nil, fmt.Errorf("ffmpeg start: %w", err)
	}

	// Close write end in parent — ffmpeg owns the write end now.
	// Closing this allows ffmpeg to detect EOF when it finishes writing.
	aacWrite.Close()

	return &FFmpegProcess{
		cmd:      cmd,
		stdout:   stdout,
		aacRead:  aacRead,
		aacWrite: aacWrite,
		log:      log,
	}, nil
}

// PCMReader returns the stdout reader containing raw float32 PCM at 16kHz mono.
func (p *FFmpegProcess) PCMReader() io.Reader {
	return p.stdout
}

// AACReader returns the pipe reader for AAC fMP4 stream.
func (p *FFmpegProcess) AACReader() io.Reader {
	return p.aacRead
}

// Stop terminates the ffmpeg process and closes all pipes.
// It calls Process.Kill() to terminate the subprocess, then waits for it to exit.
func (p *FFmpegProcess) Stop() {
	if p.cmd.Process != nil {
		p.cmd.Process.Kill()
	}
	p.stdout.Close()
	p.aacRead.Close()
	_ = p.cmd.Wait() // reap the process
}

// Wait waits for the ffmpeg process to exit and returns any error.
func (p *FFmpegProcess) Wait() error {
	return p.cmd.Wait()
}
