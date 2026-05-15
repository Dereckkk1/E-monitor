package ingestor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"go.uber.org/zap"
)

// probeTimeout caps how long ffprobe may spend identifying the audio codec
// of a live stream before StartFFmpeg gives up and falls back to the
// re-encode path. 8s comfortably covers a slow DNS + TLS handshake + ICY
// metadata exchange while still being short enough that 200 simultaneous
// probes during supervisor boot complete inside one stall-watchdog grace.
const probeTimeout = 8 * time.Second

// ProbeAudioCodec returns the codec name (lowercase) of the first audio
// stream at url, using ffprobe. Used by StartFFmpeg to decide whether the
// ADTS segment muxer can accept the input verbatim (AAC only) or whether
// the audio must be re-encoded to AAC first.
//
// Returns "" with no error when ffprobe completes but reports no audio
// streams — callers should treat that as "unknown" and re-encode. Returns
// an error only when the probe itself fails (timeout, network, bad JSON);
// the caller logs and uses the re-encode fallback for safety.
//
// Honours the same -user_agent as StartFFmpeg so a User-Agent gate that
// rejects the live request doesn't surface as a phantom "no audio streams"
// here.
func ProbeAudioCodec(ctx context.Context, url string) (string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	cmd := exec.CommandContext(probeCtx, "ffprobe",
		"-v", "quiet",
		"-user_agent", "VLC/3.0.20 LibVLC/3.0.20",
		"-print_format", "json",
		"-show_streams",
		"-select_streams", "a:0",
		url,
	)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("ffprobe run: %w", err)
	}

	var resp struct {
		Streams []struct {
			CodecName string `json:"codec_name"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return "", fmt.Errorf("ffprobe parse: %w", err)
	}
	if len(resp.Streams) == 0 {
		return "", nil
	}
	return strings.ToLower(resp.Streams[0].CodecName), nil
}

// pickReconnectArgs returns the HTTP reconnect flags for the live input.
// The base trio (-reconnect, -reconnect_streamed, -reconnect_delay_max) is
// always set — they recover from transient TCP/TLS hiccups on any stream
// type. The deciding factor is whether to also set -reconnect_at_eof:
//
//   - Progressive streams (direct .mp3/.aac, Icecast/SHOUTcast, anything
//     that's a single long-running HTTP response) → YES, set it. EOF
//     genuinely means the server closed the connection and we want to
//     re-establish it.
//   - HLS streams (.m3u8) → NO. HLS delivers content as a *sequence of
//     finite segments*; every segment naturally ends in EOF. With the
//     flag on, ffmpeg treats every segment boundary as a disconnect,
//     reconnects immediately, re-pulls the manifest, and never decodes
//     anything. Incident 2026-05-15 third wave (Atlântida Joinville).
//
// Detection is by URL pattern (case-insensitive ".m3u8"). Robust enough
// for the brazilian radio catalog without an extra HEAD request.
func pickReconnectArgs(streamURL string) []string {
	base := []string{
		"-reconnect", "1",
		"-reconnect_streamed", "1",
		"-reconnect_delay_max", "5",
	}
	if isHLS(streamURL) {
		return base
	}
	return append(base, "-reconnect_at_eof", "1")
}

// isHLS reports whether streamURL points to an HLS playlist. Case-insensitive
// substring match against ".m3u8" so query strings and uppercase variants
// both classify correctly.
func isHLS(streamURL string) bool {
	return strings.Contains(strings.ToLower(streamURL), ".m3u8")
}

// pickSegmentAudioArgs decides whether the ADTS segment muxer can copy the
// input audio verbatim or must re-encode. The ADTS muxer accepts ONLY AAC;
// every other codec (mp3, opus, vorbis, flac, …) crashes ffmpeg at startup
// with "Only AAC streams can be muxed by the ADTS muxer" (incident
// 2026-05-15 second wave).
//
// codec is the lowercase codec_name reported by ffprobe (the wrapper does
// the lowercasing). Empty string ("unknown" / probe failed) maps to
// re-encode — losing a few % of CPU on a stream we could have copied is
// far cheaper than another silent worker zombie.
//
// Re-encode target: AAC LC @ 128 kbps. Chosen so MP3 96k inputs come out
// audibly indistinguishable for evidence playback while keeping evidence
// files small; ADTS muxer accepts AAC LC.
func pickSegmentAudioArgs(codec string) []string {
	if strings.EqualFold(codec, "aac") {
		return []string{"-c:a", "copy"}
	}
	return []string{"-c:a", "aac", "-b:a", "128k"}
}

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

	// Pre-flight codec detection. The ADTS segment muxer used below for the
	// evidence stream only accepts AAC; any other codec (mp3, opus, vorbis,
	// flac, …) makes ffmpeg crash at startup with "Only AAC streams can be
	// muxed by the ADTS muxer" — see pickSegmentAudioArgs. ffprobe failure
	// is non-fatal: we fall back to re-encode, which works for any input.
	codec, probeErr := ProbeAudioCodec(ctx, streamURL)
	if probeErr != nil {
		log.Warn("ffmpeg: codec probe failed; falling back to AAC re-encode",
			zap.String("stream_url", streamURL),
			zap.Error(probeErr),
		)
	}
	audioCodecArgs := pickSegmentAudioArgs(codec)
	reconnectArgs := pickReconnectArgs(streamURL)
	log.Info("ffmpeg: starting",
		zap.String("stream_url", streamURL),
		zap.String("input_codec", codec),
		zap.Bool("is_hls", isHLS(streamURL)),
		zap.Strings("segment_audio_args", audioCodecArgs),
		zap.Strings("reconnect_args", reconnectArgs),
	)

	// Two outputs:
	//   1. Segment muxer → disk (evidence). -segment_atclocktime aligns
	//      rotation with multiples of segment_time from wall-clock zero so
	//      file boundaries are predictable. -reset_timestamps starts each
	//      file's PTS at zero, which is what the readers expect when they
	//      `cat`-concat. -strftime expands %Y%m%d-%H%M%S in the filename.
	//   2. f32le PCM @ 16kHz mono → pipe:3 (analysis).
	args := []string{"-y"}
	args = append(args, reconnectArgs...)
	args = append(args,
		"-timeout", "10000000",
		"-user_agent", "VLC/3.0.20 LibVLC/3.0.20",
		"-i", streamURL,

		"-map", "0:a:0",
	)
	args = append(args, audioCodecArgs...)
	args = append(args,
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
	)

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
