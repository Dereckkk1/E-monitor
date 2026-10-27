package oauth

import (
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
)

// redirectValido: https, ou http só em loopback (Claude Code), sem fragmento e
// sem userinfo (https://evil@claude.ai engana quem lê a URL na tela).
func redirectValido(bruto string) bool {
	if strings.Contains(bruto, "#") {
		return false
	}
	u, err := url.Parse(bruto)
	if err != nil || u.Host == "" || u.User != nil {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		return ehLoopback(u.Hostname())
	}
	return false
}

func ehLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// redirectRegistrado diz se o redirect_uri pedido no /authorize casa com um
// registrado: igual, ou — RFC 8252 §7.3 / OAuth 2.1 §8.4.2 — os dois http em
// loopback com o mesmo host, path e query, em qualquer porta (o Claude Code
// abre uma porta efêmera a cada login). Fora do loopback a comparação é exata.
func redirectRegistrado(registrados []string, pedido string) bool {
	if slices.Contains(registrados, pedido) {
		return true
	}
	if !redirectValido(pedido) {
		return false
	}
	p, err := url.Parse(pedido)
	if err != nil || p.Scheme != "http" {
		return false
	}
	for _, bruto := range registrados {
		r, err := url.Parse(bruto)
		if err != nil || r.Scheme != "http" || !ehLoopback(r.Hostname()) || r.User != nil {
			continue
		}
		if r.Hostname() == p.Hostname() && r.EscapedPath() == p.EscapedPath() && r.RawQuery == p.RawQuery {
			return true
		}
	}
	return false
}

// comParametros acrescenta params à query de base, pulando os vazios. A query
// registrada fica como veio, byte a byte (OAuth 2.1 §4.1.2: "MUST be
// retained"); a base só é interpretada para conferir que é uma URL válida.
func comParametros(base string, params map[string]string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := url.Values{}
	for k, v := range params {
		if v != "" {
			q.Set(k, v)
		}
	}
	extra := q.Encode()
	switch {
	case extra == "":
	case u.RawQuery == "":
		u.RawQuery = extra
	default:
		u.RawQuery += "&" + extra
	}
	return u.String()
}

// IPDe devolve a chave de rate limit do cliente.
//
// Prefere o CF-Connecting-IP: a borda do Cloudflare sempre o sobrescreve com o
// IP que abriu a conexão, então o cliente não consegue forjá-lo. Não dá para
// confiar no RemoteAddr: o middleware.RealIP do chi (router.go) o troca pelo
// True-Client-IP/X-Real-IP/X-Forwarded-For, que o cliente escreve à vontade e
// o Cloudflare repassa — com um X-Real-IP novo a cada pedido, o limite de
// registros por IP não valeria nada. Sem o header (dev, teste, chamada direta
// ao túnel) cai no host de RemoteAddr.
//
// IPv6 vira o prefixo /64: um cliente comum recebe um /64 inteiro e pode girar
// os 64 bits de baixo sem custo nenhum.
func IPDe(r *http.Request) string {
	bruto := strings.TrimSpace(r.Header.Get("CF-Connecting-IP"))
	if bruto == "" {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		bruto = host
	}
	addr, err := netip.ParseAddr(bruto)
	if err != nil {
		return bruto
	}
	addr = addr.Unmap().WithZone("")
	if addr.Is6() {
		return netip.PrefixFrom(addr, 64).Masked().String()
	}
	return addr.String()
}
