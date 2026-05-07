package webhook

// safehttp.go — SSRF protection for outbound webhook delivery.
//
// The webhook worker POSTs to URLs configured by operators. Without a guard,
// an attacker (or a malicious operator) could point `webhook_url` at private
// network resources:
//
//   - http://169.254.169.254/...   (AWS/GCP/Azure cloud metadata)
//   - http://127.0.0.1:5432/...    (loopback / colocated services)
//   - http://10.0.0.x/...          (RFC1918 private networks)
//
// Each detection then becomes an authenticated POST inside our perimeter, and
// the response body (capped at 4KB) is persisted into `webhook_deliveries`
// where it is exfiltratable via `GET /v1/internal/clients/{id}/webhook-deliveries`.
//
// Mitigation:
//
//  1. Custom DialContext resolves the host, blocks any answer that maps to a
//     private/loopback/link-local address, and dials the IP directly (so a
//     DNS rebinding attack cannot swap the address between resolution and
//     dial).
//  2. CheckRedirect re-validates redirects against the same rule so a public
//     302 → private cannot bypass the guard.
//  3. PATCH /webhook validation (handlers/webhooks.go) refuses URLs that
//     resolve to private space at config time.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// ErrSSRFBlocked is returned when a host resolves to a private/loopback/
// link-local address (or when a redirect points at one). Wrapped with %w by
// the dialer so callers can errors.Is against it.
var ErrSSRFBlocked = errors.New("ssrf: target resolves to a private/loopback/link-local address")

// isPrivateIP reports whether ip should be treated as non-public for the
// purposes of SSRF protection.
//
// Covers:
//   - IPv4 loopback (127.0.0.0/8)
//   - IPv4 "this network" (0.0.0.0/8)
//   - IPv4 link-local (169.254.0.0/16, including AWS/GCP/Azure metadata
//     169.254.169.254)
//   - IPv4 private (10/8, 172.16/12, 192.168/16)
//   - IPv4 multicast (224.0.0.0/4) — never a legitimate webhook target
//   - IPv6 loopback (::1)
//   - IPv6 unique-local (fc00::/7)
//   - IPv6 link-local unicast (fe80::/10)
//   - IPv6 multicast (ff00::/8) including interface-local multicast
//
// Public addresses (8.8.8.8, 1.1.1.1, 2606:4700:4700::1111, ...) return false.
func isPrivateIP(ip net.IP) bool {
	if ip == nil {
		return true // fail-closed
	}
	if ip.IsLoopback() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() ||
		ip.IsPrivate() {
		return true
	}
	// Defense-in-depth: explicitly check IPv4 ranges that older Go versions
	// may not cover via IsPrivate (mostly stdlib parity). Cheap.
	if v4 := ip.To4(); v4 != nil {
		// 0.0.0.0/8 — "this network"
		if v4[0] == 0 {
			return true
		}
		// 169.254/16 — link-local (already covered by IsLinkLocalUnicast,
		// kept for clarity since 169.254.169.254 is the canonical metadata
		// IP we want to block).
		if v4[0] == 169 && v4[1] == 254 {
			return true
		}
	}
	return false
}

// safeDialer wraps the underlying TCP dialer with SSRF resolution checks.
type safeDialer struct {
	timeout  time.Duration
	resolver *net.Resolver
	// allowPrivate is true when RADIOCHECK_ENV=development; lets local devs
	// run integration tests against http://localhost:N. Production must keep
	// this false.
	allowPrivate bool
}

func (d *safeDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("ssrf dial: split host:port: %w", err)
	}

	// If host is already an IP literal, validate directly.
	if ip := net.ParseIP(host); ip != nil {
		if !d.allowPrivate && isPrivateIP(ip) {
			return nil, fmt.Errorf("%w: %s", ErrSSRFBlocked, ip.String())
		}
		dialer := &net.Dialer{Timeout: d.timeout}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
	}

	// Resolve the hostname and dial the first acceptable IP. We DO NOT pass
	// the hostname to the underlying dialer — passing the IP literal closes
	// the DNS-rebinding window between LookupIPAddr and DialContext.
	addrs, err := d.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("ssrf dial: resolve %s: %w", host, err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("ssrf dial: no addresses for %s", host)
	}
	for _, ipa := range addrs {
		if !d.allowPrivate && isPrivateIP(ipa.IP) {
			// Fail-closed: any single private answer aborts the dial. A
			// rebound DNS server could otherwise put a private IP in slot 2
			// and rely on us trying it.
			return nil, fmt.Errorf("%w: host=%s ip=%s", ErrSSRFBlocked, host, ipa.IP.String())
		}
	}
	dialer := &net.Dialer{Timeout: d.timeout}
	// Use the first resolved address. (Workers don't need Happy Eyeballs.)
	return dialer.DialContext(ctx, network, net.JoinHostPort(addrs[0].IP.String(), port))
}

// ValidateURLForSSRF is the exported wrapper used by API handlers to
// fail-fast on configuration. See validateURLForSSRF for behaviour.
func ValidateURLForSSRF(ctx context.Context, raw string) error {
	return validateURLForSSRF(ctx, raw)
}

// AllowInsecureURL reports whether http:// (non-TLS) is permitted for the
// given URL. The contract:
//   - https:// is always fine and isn't routed through here.
//   - In production, http:// is REJECTED unconditionally.
//   - In development (RADIOCHECK_ENV=development), http:// is allowed only
//     for loopback hostnames (localhost, 127.0.0.1, ::1) — there is no
//     legitimate reason to test the integration over plaintext against a
//     remote box.
func AllowInsecureURL(u *url.URL) bool {
	if u == nil {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	if u.Scheme != "http" {
		return false
	}
	if !devModeAllowsPrivate() {
		return false
	}
	host := strings.ToLower(u.Hostname())
	switch host {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// validateURLForSSRF parses raw, resolves its host, and rejects any answer in
// private space. Used by the API PATCH handler to fail fast at config time
// instead of at delivery time.
//
// In development (RADIOCHECK_ENV=development) private targets are allowed so
// local integration tests can hit http://localhost:N.
func validateURLForSSRF(ctx context.Context, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse url: %w", err)
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("empty host")
	}
	if devModeAllowsPrivate() {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		if isPrivateIP(ip) {
			return fmt.Errorf("%w: %s", ErrSSRFBlocked, ip.String())
		}
		return nil
	}
	resolveCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(resolveCtx, host)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", host, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("no addresses for %s", host)
	}
	for _, a := range addrs {
		if isPrivateIP(a.IP) {
			return fmt.Errorf("%w: host=%s ip=%s", ErrSSRFBlocked, host, a.IP.String())
		}
	}
	return nil
}

// devModeAllowsPrivate returns true when RADIOCHECK_ENV is "development" or
// "dev". Used both by validateURLForSSRF and BuildSafeHTTPClient.
func devModeAllowsPrivate() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("RADIOCHECK_ENV")))
	return v == "development" || v == "dev"
}

// BuildSafeHTTPClient constructs an *http.Client with:
//   - SSRF-aware DialContext (above)
//   - CheckRedirect that re-validates the redirect target against the same
//     rule (so a public 302 → 169.254.169.254 fails closed) and caps the
//     redirect chain at 3
//   - the caller-supplied overall request Timeout
func BuildSafeHTTPClient(timeout time.Duration) *http.Client {
	dialer := &safeDialer{
		timeout:      5 * time.Second,
		resolver:     net.DefaultResolver,
		allowPrivate: devModeAllowsPrivate(),
	}
	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("ssrf: too many redirects")
			}
			// Re-resolve the redirect target. The DialContext will also
			// catch private targets, but failing here gives a clearer error
			// in the delivery log and avoids opening a TCP socket.
			if err := validateURLForSSRF(req.Context(), req.URL.String()); err != nil {
				return fmt.Errorf("ssrf: redirect blocked: %w", err)
			}
			return nil
		},
	}
}
