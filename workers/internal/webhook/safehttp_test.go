package webhook

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestIsPrivateIP_PrivateRanges(t *testing.T) {
	private := []string{
		"127.0.0.1",
		"127.0.0.53",
		"10.0.0.1",
		"10.255.255.255",
		"172.16.0.1",
		"172.31.255.255",
		"192.168.1.1",
		"169.254.169.254", // canonical cloud metadata
		"169.254.0.1",
		"0.0.0.0",
		"224.0.0.1",       // multicast
		"::1",
		"fc00::1",         // unique-local
		"fe80::1",         // link-local
		"ff02::1",         // multicast
	}
	for _, s := range private {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("parse %s", s)
		}
		if !isPrivateIP(ip) {
			t.Errorf("isPrivateIP(%s) = false, want true", s)
		}
	}
}

func TestIsPrivateIP_PublicRanges(t *testing.T) {
	public := []string{
		"8.8.8.8",
		"1.1.1.1",
		"93.184.216.34",         // example.com
		"2606:4700:4700::1111",  // cloudflare
		"2001:4860:4860::8888",  // google
	}
	for _, s := range public {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("parse %s", s)
		}
		if isPrivateIP(ip) {
			t.Errorf("isPrivateIP(%s) = true, want false", s)
		}
	}
}

// TestSafeClient_BlocksLoopbackIP verifies the SSRF guard refuses to dial a
// raw private IP literal. We use 127.0.0.1 against an httptest server (which
// the client should NEVER reach) — if the request succeeds, the guard is
// broken.
func TestSafeClient_BlocksLoopbackIP(t *testing.T) {
	t.Setenv("RADIOCHECK_ENV", "production") // explicit: not dev

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := BuildSafeHTTPClient(2 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := client.Do(req)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatalf("expected SSRF error against loopback %s, got success", srv.URL)
	}
	if !errors.Is(err, ErrSSRFBlocked) && !strings.Contains(err.Error(), "ssrf") {
		t.Fatalf("expected SSRF error, got %v", err)
	}
}

// TestSafeClient_BlocksRawPrivateIP verifies that pointing at a raw RFC1918
// IP literal also fails. We don't actually need to reach the IP; the dialer
// should refuse before opening a socket.
func TestSafeClient_BlocksRawPrivateIP(t *testing.T) {
	t.Setenv("RADIOCHECK_ENV", "production")

	client := BuildSafeHTTPClient(1 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// 10.255.255.255:1 — guaranteed-private, won't accept connections.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://10.255.255.255:1/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	_, err = client.Do(req)
	if err == nil {
		t.Fatalf("expected SSRF error for 10.255.255.255, got nil")
	}
	if !strings.Contains(err.Error(), "ssrf") && !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("expected SSRF error, got %v", err)
	}
}

// TestSafeClient_AllowsPrivateInDevMode confirms the dev-mode escape hatch
// works so local integration tests can keep hitting http://127.0.0.1:N.
func TestSafeClient_AllowsPrivateInDevMode(t *testing.T) {
	t.Setenv("RADIOCHECK_ENV", "development")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := BuildSafeHTTPClient(2 * time.Second)
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("dev-mode loopback request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
}

func TestValidateURLForSSRF_RejectsPrivate(t *testing.T) {
	t.Setenv("RADIOCHECK_ENV", "production")
	cases := []string{
		"http://127.0.0.1/x",
		"http://10.0.0.1/x",
		"http://169.254.169.254/latest/meta-data/",
		"http://[::1]/x",
		"http://[fc00::1]/x",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			err := ValidateURLForSSRF(context.Background(), raw)
			if err == nil {
				t.Fatalf("expected error for %s, got nil", raw)
			}
			if !errors.Is(err, ErrSSRFBlocked) {
				t.Fatalf("expected ErrSSRFBlocked for %s, got %v", raw, err)
			}
		})
	}
}

func TestValidateURLForSSRF_AllowsPublicLiteral(t *testing.T) {
	t.Setenv("RADIOCHECK_ENV", "production")
	// 8.8.8.8 is public; using literal avoids depending on DNS in tests.
	if err := ValidateURLForSSRF(context.Background(), "https://8.8.8.8/"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAllowInsecureURL(t *testing.T) {
	t.Run("https always allowed", func(t *testing.T) {
		t.Setenv("RADIOCHECK_ENV", "production")
		u := mustParseURL(t, "https://api.example.com/hook")
		if !AllowInsecureURL(u) {
			t.Fatal("https should be allowed in prod")
		}
	})

	t.Run("http blocked in prod", func(t *testing.T) {
		t.Setenv("RADIOCHECK_ENV", "production")
		u := mustParseURL(t, "http://api.example.com/hook")
		if AllowInsecureURL(u) {
			t.Fatal("http should be blocked in prod")
		}
	})

	t.Run("http blocked in prod for localhost too", func(t *testing.T) {
		t.Setenv("RADIOCHECK_ENV", "")
		u := mustParseURL(t, "http://localhost:9000/hook")
		if AllowInsecureURL(u) {
			t.Fatal("http://localhost should be blocked when env is not dev")
		}
	})

	t.Run("http allowed for localhost in dev", func(t *testing.T) {
		t.Setenv("RADIOCHECK_ENV", "development")
		for _, host := range []string{"localhost", "127.0.0.1", "::1"} {
			u := mustParseURL(t, "http://"+bracketIfV6(host)+":9000/hook")
			if !AllowInsecureURL(u) {
				t.Errorf("http://%s should be allowed in dev", host)
			}
		}
	})

	t.Run("http blocked for non-loopback even in dev", func(t *testing.T) {
		t.Setenv("RADIOCHECK_ENV", "development")
		u := mustParseURL(t, "http://example.com/hook")
		if AllowInsecureURL(u) {
			t.Fatal("http to remote host should be blocked even in dev")
		}
	})
}

func bracketIfV6(host string) string {
	if strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return host
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", raw, err)
	}
	return u
}
