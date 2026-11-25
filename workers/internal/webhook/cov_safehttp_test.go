package webhook

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCovW_IsPrivateIP_NilFailsClosed(t *testing.T) {
	assert.True(t, isPrivateIP(nil), "IP nulo deve ser tratado como privado (fail-closed)")
}

func TestCovW_IsPrivateIP_Table(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", true}, {"10.1.2.3", true}, {"172.16.5.5", true}, {"192.168.0.10", true},
		{"169.254.169.254", true}, {"0.1.2.3", true}, {"224.0.0.251", true},
		{"::1", true}, {"fd12:3456::1", true}, {"fe80::abcd", true}, {"::", true},
		{"::ffff:10.0.0.1", true}, // IPv4 privado mapeado em IPv6
		{"172.15.255.255", false}, {"172.32.0.1", false}, {"11.0.0.1", false},
		{"8.8.4.4", false}, {"2606:4700:4700::1001", false}, {"::ffff:8.8.8.8", false},
	}
	for _, c := range cases {
		t.Run(c.ip, func(t *testing.T) {
			ip := net.ParseIP(c.ip)
			require.NotNil(t, ip)
			assert.Equal(t, c.want, isPrivateIP(ip))
		})
	}
}

func TestCovW_SafeDialer_DialContext(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	addr := ln.Addr().String()
	_, port, _ := net.SplitHostPort(addr)

	noDNS := &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("sem DNS neste teste")
	}}
	ctx := context.Background()

	t.Run("literal privado é bloqueado em produção", func(t *testing.T) {
		d := &safeDialer{timeout: time.Second, resolver: noDNS}
		c, err := d.DialContext(ctx, "tcp", addr)
		if c != nil {
			_ = c.Close()
		}
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrSSRFBlocked)
	})

	t.Run("literal privado é permitido em dev", func(t *testing.T) {
		d := &safeDialer{timeout: time.Second, resolver: noDNS, allowPrivate: true}
		c, err := d.DialContext(ctx, "tcp", addr)
		require.NoError(t, err)
		_ = c.Close()
	})

	t.Run("endereço sem porta", func(t *testing.T) {
		d := &safeDialer{timeout: time.Second, resolver: noDNS}
		_, err := d.DialContext(ctx, "tcp", "sem-porta")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "split host:port")
	})

	t.Run("hostname que resolve pra loopback é bloqueado em produção", func(t *testing.T) {
		d := &safeDialer{timeout: time.Second, resolver: net.DefaultResolver}
		_, err := d.DialContext(ctx, "tcp", net.JoinHostPort("localhost", port))
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrSSRFBlocked, "localhost resolve para 127.0.0.1/::1")
	})

	t.Run("falha de DNS é reportada", func(t *testing.T) {
		d := &safeDialer{timeout: time.Second, resolver: noDNS}
		_, err := d.DialContext(ctx, "tcp", net.JoinHostPort("webhook.example.invalid", "443"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "resolve webhook.example.invalid")
		assert.NotErrorIs(t, err, ErrSSRFBlocked)
	})

	t.Run("hostname em dev não é barrado pelo SSRF", func(t *testing.T) {
		d := &safeDialer{timeout: time.Second, resolver: net.DefaultResolver, allowPrivate: true}
		c, err := d.DialContext(ctx, "tcp", net.JoinHostPort("localhost", port))
		if c != nil {
			_ = c.Close()
		}
		// pode conectar (127.0.0.1) ou falhar a conexão (::1 sem listener), mas nunca por SSRF.
		assert.NotErrorIs(t, err, ErrSSRFBlocked)
	})
}

func TestCovW_ValidateURLForSSRF(t *testing.T) {
	ctx := context.Background()

	t.Run("URL malformada", func(t *testing.T) {
		t.Setenv("RADIOCHECK_ENV", "production")
		err := ValidateURLForSSRF(ctx, "http://[::1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "parse url")
	})

	t.Run("host vazio", func(t *testing.T) {
		t.Setenv("RADIOCHECK_ENV", "production")
		err := ValidateURLForSSRF(ctx, "https:///caminho")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty host")
	})

	t.Run("em dev qualquer host com nome é aceito sem resolver", func(t *testing.T) {
		t.Setenv("RADIOCHECK_ENV", "dev")
		assert.NoError(t, ValidateURLForSSRF(ctx, "http://127.0.0.1:9000/hook"))
		assert.NoError(t, ValidateURLForSSRF(ctx, "http://10.0.0.5/hook"))
	})

	t.Run("hostname que resolve pra loopback é bloqueado", func(t *testing.T) {
		t.Setenv("RADIOCHECK_ENV", "production")
		err := ValidateURLForSSRF(ctx, "http://localhost:8080/hook")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrSSRFBlocked)
		assert.Contains(t, err.Error(), "localhost")
	})

	t.Run("IPv6 público literal é aceito", func(t *testing.T) {
		t.Setenv("RADIOCHECK_ENV", "production")
		assert.NoError(t, ValidateURLForSSRF(ctx, "https://[2606:4700:4700::1111]/hook"))
	})
}

func TestCovW_AllowInsecureURL_EdgeCases(t *testing.T) {
	t.Setenv("RADIOCHECK_ENV", "development")
	assert.False(t, AllowInsecureURL(nil), "URL nula nunca é permitida")
	for _, raw := range []string{"ftp://localhost/x", "ws://localhost/x", "file:///etc/passwd"} {
		u, err := url.Parse(raw)
		require.NoError(t, err)
		assert.False(t, AllowInsecureURL(u), "esquema %q não é http(s)", u.Scheme)
	}
	httpsRemote, _ := url.Parse("https://example.com/h")
	assert.True(t, AllowInsecureURL(httpsRemote))
	upper, _ := url.Parse("http://LOCALHOST:9000/h")
	assert.True(t, AllowInsecureURL(upper), "comparação do host é case-insensitive")
}

func TestCovW_DevModeAllowsPrivate(t *testing.T) {
	cases := map[string]bool{
		"development": true, "dev": true, " DEV ": true, "Development": true,
		"production": false, "prod": false, "": false, "devel": false,
	}
	for env, want := range cases {
		t.Run("env="+env, func(t *testing.T) {
			t.Setenv("RADIOCHECK_ENV", env)
			assert.Equal(t, want, devModeAllowsPrivate())
		})
	}
}

func TestCovW_BuildSafeHTTPClient_CheckRedirect(t *testing.T) {
	t.Setenv("RADIOCHECK_ENV", "production")
	client := BuildSafeHTTPClient(3 * time.Second)
	require.NotNil(t, client.CheckRedirect)
	assert.Equal(t, 3*time.Second, client.Timeout)

	mkReq := func(raw string) *http.Request {
		r, err := http.NewRequest(http.MethodGet, raw, nil)
		require.NoError(t, err)
		return r
	}

	t.Run("redireciona pra IP privado é bloqueado", func(t *testing.T) {
		err := client.CheckRedirect(mkReq("http://169.254.169.254/latest/meta-data/"), []*http.Request{mkReq("https://8.8.8.8/")})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrSSRFBlocked)
		assert.Contains(t, err.Error(), "redirect blocked")
	})

	t.Run("corrente de 3 redirecionamentos é cortada", func(t *testing.T) {
		via := []*http.Request{mkReq("https://8.8.8.8/1"), mkReq("https://8.8.8.8/2"), mkReq("https://8.8.8.8/3")}
		err := client.CheckRedirect(mkReq("https://8.8.4.4/4"), via)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "too many redirects")
	})

	t.Run("redirecionamento público dentro do limite passa", func(t *testing.T) {
		assert.NoError(t, client.CheckRedirect(mkReq("https://8.8.4.4/ok"), []*http.Request{mkReq("https://8.8.8.8/")}))
	})
}

// Em dev, o cliente segue um redirecionamento real entre dois servidores locais.
func TestCovW_BuildSafeHTTPClient_FollowsRedirectInDevMode(t *testing.T) {
	t.Setenv("RADIOCHECK_ENV", "development")
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("destino"))
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	resp, err := BuildSafeHTTPClient(3 * time.Second).Get(redirector.URL)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	tu, _ := url.Parse(target.URL)
	assert.Equal(t, tu.Host, resp.Request.URL.Host, "seguiu o redirecionamento até o destino")
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, "destino", string(body))
}
