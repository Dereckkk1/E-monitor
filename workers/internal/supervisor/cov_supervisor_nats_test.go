package supervisor

import (
	"bufio"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// ── fake NATS ──────────────────────────────────────────────────────────────
//
// covSNATS é um servidor NATS mínimo (protocolo core: INFO/CONNECT/PING/SUB/
// UNSUB/PUB/HPUB → MSG/HMSG) que roda dentro do processo de teste. O cliente
// real (nats.go) conversa com ele por TCP em 127.0.0.1, então o código sob
// teste usa exatamente o mesmo caminho de produção (Subscribe/Publish) sem
// precisar de um nats-server instalado nem de rede externa.

type covSNATSMsg struct {
	Subject string
	Data    []byte
}

type covSNATSConn struct {
	c    net.Conn
	mu   sync.Mutex // protege subs e escrita
	subs map[string]string
}

func (c *covSNATSConn) write(s string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, _ = io.WriteString(c.c, s)
}

type covSNATS struct {
	ln    net.Listener
	mu    sync.Mutex
	conns map[*covSNATSConn]struct{}
	msgs  []covSNATSMsg
}

func covSStartNATS(t *testing.T) *covSNATS {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	s := &covSNATS{ln: ln, conns: map[*covSNATSConn]struct{}{}}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		s.mu.Lock()
		for c := range s.conns {
			_ = c.c.Close()
		}
		s.mu.Unlock()
	})
	return s
}

func (s *covSNATS) URL() string { return "nats://" + s.ln.Addr().String() }

func covSSubjectMatches(pattern, subject string) bool {
	pt := strings.Split(pattern, ".")
	st := strings.Split(subject, ".")
	for i, p := range pt {
		if p == ">" {
			return len(st) > i
		}
		if i >= len(st) {
			return false
		}
		if p != "*" && p != st[i] {
			return false
		}
	}
	return len(pt) == len(st)
}

func (s *covSNATS) route(subject, reply string, hdr, payload []byte) {
	s.mu.Lock()
	s.msgs = append(s.msgs, covSNATSMsg{Subject: subject, Data: append([]byte(nil), payload...)})
	conns := make([]*covSNATSConn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()

	for _, c := range conns {
		c.mu.Lock()
		var sids []string
		for sid, pat := range c.subs {
			if covSSubjectMatches(pat, subject) {
				sids = append(sids, sid)
			}
		}
		c.mu.Unlock()
		for _, sid := range sids {
			replyPart := ""
			if reply != "" {
				replyPart = reply + " "
			}
			if hdr != nil {
				total := len(hdr) + len(payload)
				c.write("HMSG " + subject + " " + sid + " " + replyPart + strconv.Itoa(len(hdr)) + " " + strconv.Itoa(total) + "\r\n" + string(hdr) + string(payload) + "\r\n")
			} else {
				c.write("MSG " + subject + " " + sid + " " + replyPart + strconv.Itoa(len(payload)) + "\r\n" + string(payload) + "\r\n")
			}
		}
	}
}

func (s *covSNATS) serve(raw net.Conn) {
	c := &covSNATSConn{c: raw, subs: map[string]string{}}
	s.mu.Lock()
	s.conns[c] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
		_ = raw.Close()
	}()
	c.write(`INFO {"server_id":"cov","server_name":"cov","version":"2.10.0","proto":1,"headers":true,"max_payload":1048576}` + "\r\n")
	r := bufio.NewReader(raw)
	readN := func(n int) ([]byte, bool) {
		buf := make([]byte, n+2) // +CRLF
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, false
		}
		return buf[:n], true
	}
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		op, rest, _ := strings.Cut(line, " ")
		f := strings.Fields(rest)
		switch strings.ToUpper(op) {
		case "PING":
			c.write("PONG\r\n")
		case "SUB":
			if len(f) >= 2 {
				c.mu.Lock()
				c.subs[f[len(f)-1]] = f[0]
				c.mu.Unlock()
			}
		case "UNSUB":
			if len(f) >= 1 {
				c.mu.Lock()
				delete(c.subs, f[0])
				c.mu.Unlock()
			}
		case "PUB":
			if len(f) < 2 {
				return
			}
			n, _ := strconv.Atoi(f[len(f)-1])
			payload, ok := readN(n)
			if !ok {
				return
			}
			reply := ""
			if len(f) == 3 {
				reply = f[1]
			}
			s.route(f[0], reply, nil, payload)
		case "HPUB":
			if len(f) < 3 {
				return
			}
			hn, _ := strconv.Atoi(f[len(f)-2])
			tn, _ := strconv.Atoi(f[len(f)-1])
			body, ok := readN(tn)
			if !ok {
				return
			}
			reply := ""
			if len(f) == 4 {
				reply = f[1]
			}
			s.route(f[0], reply, body[:hn], body[hn:])
		}
	}
}

// Messages devolve os payloads publicados (PUB/HPUB) no subject, em ordem.
func (s *covSNATS) Messages(subject string) []covSNATSMsg {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []covSNATSMsg
	for _, m := range s.msgs {
		if m.Subject == subject {
			out = append(out, m)
		}
	}
	return out
}

// covSConnect abre uma conexão nats.go real contra o servidor falso.
func covSConnect(t *testing.T, s *covSNATS) *nats.Conn {
	t.Helper()
	nc, err := nats.Connect(s.URL(), nats.Timeout(5*time.Second))
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	return nc
}

// covSObservedLogger devolve um zap.Logger que grava tudo em memória, pra os
// testes afirmarem sobre os logs (warn/error/info) emitidos pelo loader.
func covSObservedLogger() (*zap.Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zap.DebugLevel)
	return zap.New(core), logs
}

// covSWaitLog bloqueia (até 10s) até aparecer ao menos `n` logs com a mensagem.
func covSWaitLog(t *testing.T, logs *observer.ObservedLogs, msg string, n int) {
	t.Helper()
	require.Eventually(t, func() bool {
		return logs.FilterMessage(msg).Len() >= n
	}, 10*time.Second, 10*time.Millisecond, "log %q não apareceu %d vez(es)", msg, n)
}
