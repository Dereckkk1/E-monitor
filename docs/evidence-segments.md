# Evidence Segments

## Why this exists

The previous design held the AAC evidence stream in an in-memory ring buffer
(`pkg/ringbuffer.ByteRing`) keyed on `time.Now()` at the moment each chunk
arrived from ffmpeg. That worked for the PoC but had three failure modes
that began surfacing in production:

1. **Stream reconnects lose the pre-buffer.** When ffmpeg dropped and
   reconnected, the chunk timestamps before/after the disconnect formed a
   gap. The 60-second pre-buffer the evidence formula asks for could fall
   entirely inside that gap and silently come back as a 70-second clip
   instead of 150s, with the start of the commercial chopped off.
2. **Worker restarts wipe the pre-buffer entirely.** Memory is volatile.
3. **Wall-clock vs. content-time drift.** The PCM analysis stream and the
   AAC evidence stream go through different ffmpeg internal paths and
   accumulate slightly different latencies. `time.Now()` at chunk arrival
   does not represent the audio's actual content time, and the drift gets
   worse the longer the stream has been running.

Two real false-negative incidents on the same station (UNIUBE +
JINGLE ROGGA VERÃO 30) traced back to this. The full investigation is
captured in the conversation that drove the refactor; see
[diag_falsepos_test.go](../workers/internal/match/diag_falsepos_test.go) for
the diagnostic runner used to confirm the root cause.

## How it works now

ffmpeg writes the ADTS-AAC evidence stream **directly to disk** via the
[segment muxer](https://ffmpeg.org/ffmpeg-formats.html#segment_002c-stream_005fsegment_002c-ssegment).
Each station gets its own subdirectory under `SEGMENTS_PATH` (default
`/data/segments`), populated with rotating 30-second files named with the
strftime pattern `YYYYMMDD-HHMMSS.aac`:

```
/data/segments/
  09065bf7-7473-4634-a334-476ac1c66fd4/
    20260509-130000.aac
    20260509-130030.aac
    20260509-130100.aac
    ...
```

When a detection confirms, `evidence.Service` calls
[`segments.Extract(dir, from, to)`](../workers/internal/segments/segments.go),
which:

1. Lists the segment files whose `[start, start+30s]` interval overlaps the
   requested range.
2. Concatenates them as raw ADTS bytes (legal — ADTS frames are
   self-delimiting).
3. Calls ffmpeg with `-ss / -t -c copy` to trim to the exact requested
   range.
4. Reports `CoveredFraction` and a `Partial` flag so the evidence service
   can mark detections whose underlying audio had gaps.

ffmpeg is invoked once per worker connect attempt with two outputs:

```
ffmpeg ... \
  -map 0:a:0 -c:a copy \
    -f segment -segment_time 30 -segment_format adts \
    -segment_atclocktime 1 -reset_timestamps 1 -strftime 1 \
    /data/segments/<station>/%Y%m%d-%H%M%S.aac \
  -map 0:a:0 -ar 16000 -ac 1 -f f32le pipe:3
```

`-segment_atclocktime 1` aligns rotation with multiples of `segment_time`
from the wall-clock zero. So segments fire at HH:MM:00 and HH:MM:30, every
minute. Boundaries are predictable; lookups on disk are O(1) via name
parsing.

## Why disk and not RAM

| | In-memory ring (old) | Disk segments (new) |
|---|---|---|
| Survives ffmpeg reconnect | ✗ pre-buffer chunks discarded | ✓ files persist |
| Survives worker restart | ✗ buffer empty after restart | ✓ |
| Anchored to content time | ✗ `time.Now()` jitter | ✓ wall-clock-aligned segment boundaries via ffmpeg's PTS |
| Operator-inspectable | ✗ in-process only | ✓ `ls /data/segments/<station>/` and `ffplay <file>` |
| Disk cost | n/a | 256 kbps × 200 stations × 1 hour = 23 GB |
| Memory cost | 5 min × 200 stations × ~15 MB = 3 GB | none |

For a 200-station deployment, the disk approach is cheaper and more
robust. Disk is bounded by the cleanup sidecar (see below); RAM was bounded
only by the ring-size knob and grew with `(stations × buffer-seconds)`.

## Retention and cleanup

A `segments-cleanup` Alpine sidecar (declared in `infra/docker/docker-
compose.yml`) prunes files older than 60 minutes every 5 minutes:

```bash
find /data/segments -type f -name "*.aac" -mmin +60 -delete
find /data/segments -type d -empty -mindepth 1 -delete
```

60 minutes is well beyond the longest evidence window we ever extract
(60s pre-buffer + the commercial duration + 60s post-buffer = at most
3 minutes in practice). Bumping retention is just an env-var change.

## Operator quick reference

Inspect a station's recent segments:
```bash
docker compose exec api ls -la /data/segments/<station-uuid>/
```

Listen to a specific segment without downloading:
```bash
docker compose exec api ffplay -nodisp -autoexit /data/segments/<station-uuid>/20260509-130030.aac
```

Re-extract evidence for a detection by hand (ad-hoc, e.g. when investigating
a complaint):
```bash
# Replace station / from / to with the values from the detections row.
docker compose exec api ffmpeg -i \
  "concat:$(ls /data/segments/<station>/202605091*.aac | head -3 | tr '\n' '|' | sed 's/|$//')" \
  -ss 22 -t 90 -c copy /tmp/evidence.aac
```

Spot-check disk usage:
```bash
docker compose exec api du -sh /data/segments/*
```

## Failure modes still possible (and how they degrade)

- **Disk fills up.** ffmpeg's segment writes fail, ring of complaints in
  the api logs. Cleanup sidecar runs every 5 min; in steady state we use
  ~25 GB / 200 stations. Allocate at least 50 GB to the volume to absorb
  bursts. Future work: emit a Prometheus metric on disk usage so we alert
  before it bites.
- **ffmpeg can't reconnect upstream.** No new segments arrive; the
  existing ones are extracted normally. Detections fired during the outage
  use whatever segments existed; `CoveredFraction` flags partial captures
  on the row.
- **Long worker restart cycle.** Old segments survive; new segments start
  flowing once ffmpeg comes back. No data loss across the restart boundary.

## Migration notes

The previous in-memory `ringbuffer.ByteRing` is no longer wired into the
evidence path. The package itself stays in `pkg/ringbuffer/` because the
PCM analysis path still uses `PCMRing`; only the AAC ring is gone.

The `WorkerConfig.AACBuffer` field was removed in favour of
`SegmentsOutputPattern`. Callers (only `supervisor.startStationWorker`)
pass the strftime path produced by `segments.FFmpegOutputPattern`.
`evidence.Service.Register` now takes a string directory path instead of a
`*ringbuffer.ByteRing` pointer.
