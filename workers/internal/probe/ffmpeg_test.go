package probe

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPing_Reachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	res := Ping(context.Background(), srv.URL)
	if res.Status != StatusOK {
		t.Fatalf("Ping(%s) = %s (%s), want ok", srv.URL, res.Status, res.Detail)
	}
}

func TestPing_Unreachable(t *testing.T) {
	// Porta fechada: abrimos e fechamos um listener pra pegar uma porta que
	// (quase com certeza) ninguém está escutando.
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()

	res := Ping(context.Background(), "http://"+addr+"/stream")
	if res.Status != StatusFail {
		t.Fatalf("Ping(closed) = %s, want fail", res.Status)
	}
}

func TestPing_BadScheme(t *testing.T) {
	res := Ping(context.Background(), "ftp://host/x")
	if res.Status != StatusFail {
		t.Fatalf("Ping(ftp) = %s, want fail", res.Status)
	}
}

func TestProbeStream_BadScheme(t *testing.T) {
	res := ProbeStream(context.Background(), NewLimiter(1), "file:///etc/passwd")
	if res.Status != StatusFail {
		t.Fatalf("ProbeStream(file) = %s, want fail", res.Status)
	}
}

func TestProbeIngest_BadScheme(t *testing.T) {
	res := ProbeIngest(context.Background(), NewLimiter(1), "ftp://host/x", time.Second)
	if res.Status != StatusFail {
		t.Fatalf("ProbeIngest(ftp) = %s, want fail", res.Status)
	}
}
