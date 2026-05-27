package probe

import (
	"context"
	"testing"
	"time"
)

func TestHostPort(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		want    string
		wantErr bool
	}{
		{"http default port", "http://stream.example.com/live", "stream.example.com:80", false},
		{"https default port", "https://stream.example.com/live", "stream.example.com:443", false},
		{"explicit port", "http://1.2.3.4:8000/stream", "1.2.3.4:8000", false},
		{"https explicit port", "https://host:9443/x", "host:9443", false},
		{"empty", "", "", true},
		{"no scheme", "stream.example.com/live", "", true},
		{"bad scheme ftp", "ftp://host/file", "", true},
		{"bad scheme file", "file:///etc/passwd", "", true},
		{"no host", "http:///path", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := hostPort(c.url)
			if c.wantErr {
				if err == nil {
					t.Fatalf("hostPort(%q) = %q, want error", c.url, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("hostPort(%q) unexpected error: %v", c.url, err)
			}
			if got != c.want {
				t.Fatalf("hostPort(%q) = %q, want %q", c.url, got, c.want)
			}
		})
	}
}

func TestIngestOK(t *testing.T) {
	// 16kHz mono s16le = 32000 bytes/sec. Floor is 50% of expected.
	cases := []struct {
		name  string
		bytes int64
		dur   time.Duration
		want  bool
	}{
		{"full 5s", 160000, 5 * time.Second, true},
		{"exactly half 5s", 80000, 5 * time.Second, true},
		{"just under half", 79999, 5 * time.Second, false},
		{"zero bytes", 0, 5 * time.Second, false},
		{"zero dur guards", 100, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ingestOK(c.bytes, c.dur); got != c.want {
				t.Fatalf("ingestOK(%d, %v) = %v, want %v", c.bytes, c.dur, got, c.want)
			}
		})
	}
}

func TestPickWorkerSource(t *testing.T) {
	recent := time.Now().Add(-5 * time.Second)
	stale := time.Now().Add(-90 * time.Second)
	cases := []struct {
		name        string
		overrideURL string
		savedURL    string
		live        *LiveWorker
		wantSource  string
		wantUseLive bool
	}{
		{"no override, live recent", "", "http://a/s", &LiveWorker{Active: true, LastPCMAt: recent}, "live", true},
		{"no override, live stale", "", "http://a/s", &LiveWorker{Active: true, LastPCMAt: stale}, "ephemeral", false},
		{"no override, no worker", "", "http://a/s", nil, "ephemeral", false},
		{"override == saved, live recent", "http://a/s", "http://a/s", &LiveWorker{Active: true, LastPCMAt: recent}, "live", true},
		{"override != saved forces ephemeral", "http://b/s", "http://a/s", &LiveWorker{Active: true, LastPCMAt: recent}, "ephemeral", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src, useLive := PickWorkerSource(c.overrideURL, c.savedURL, c.live)
			if src != c.wantSource || useLive != c.wantUseLive {
				t.Fatalf("PickWorkerSource(%q,%q,%v) = (%q,%v), want (%q,%v)",
					c.overrideURL, c.savedURL, c.live, src, useLive, c.wantSource, c.wantUseLive)
			}
		})
	}
}

func TestLimiter_Capacity(t *testing.T) {
	l := NewLimiter(2)
	if !l.TryAcquire() {
		t.Fatal("1st TryAcquire should succeed")
	}
	if !l.TryAcquire() {
		t.Fatal("2nd TryAcquire should succeed")
	}
	if l.TryAcquire() {
		t.Fatal("3rd TryAcquire should fail at capacity 2")
	}
	l.Release()
	if !l.TryAcquire() {
		t.Fatal("TryAcquire after Release should succeed")
	}
}

func TestLimiter_AcquireRespectsContext(t *testing.T) {
	l := NewLimiter(1)
	if err := l.Acquire(context.Background()); err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := l.Acquire(ctx); err == nil {
		t.Fatal("Acquire on full limiter should return ctx error")
	}
}
