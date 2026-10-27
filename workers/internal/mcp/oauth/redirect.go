package oauth

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// redirectValido: https, ou http só em loopback (Claude Code), sem fragmento.
func redirectValido(bruto string) bool {
	if strings.Contains(bruto, "#") {
		return false
	}
	u, err := url.Parse(bruto)
	if err != nil || u.Host == "" {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		h := u.Hostname()
		return h == "localhost" || h == "127.0.0.1" || h == "::1"
	}
	return false
}

// comParametros acrescenta params à query de base, pulando os vazios.
func comParametros(base string, params map[string]string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	for k, v := range params {
		if v != "" {
			q.Set(k, v)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// IPDe devolve o host de RemoteAddr (que o middleware.RealIP do chi já
// trocou pelo IP do cliente atrás do túnel).
func IPDe(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
