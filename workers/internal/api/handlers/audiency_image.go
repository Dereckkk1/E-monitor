package handlers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// AudiencyImageHandler proxies image fetches to api.audiency.io. The
// browser can't talk to Audiency directly without exposing the apiKey,
// and going through this proxy also sidesteps the cross-origin / CSP
// dance that some browsers do for third-party images.
//
// URL: GET /v1/internal/audiency-image?token=<file-token>
//
// Token is the opaque hex string Audiency returns as `file` (clients) or
// `logo` (stations). We don't store or validate it beyond a basic charset
// check — Audiency returns 404 for anything bogus, we surface the same.
type AudiencyImageHandler struct {
	APIKey string // env AUDIENCY_API_KEY; falls back to hardcoded dev key
	Base   string // env AUDIENCY_BASE; falls back to https://api.audiency.io
}

// NewAudiencyImageHandler reads env config; safe to construct at boot.
func NewAudiencyImageHandler() *AudiencyImageHandler {
	key := os.Getenv("AUDIENCY_API_KEY")
	if key == "" {
		// Mesma chave usada pelos scripts de fetch — Audiency considera
		// "apiKey por usuário", e essa é a do operador padrão.
		key = "9620cf74-856d-40c2-a091-248e4f322caa"
	}
	base := os.Getenv("AUDIENCY_BASE")
	if base == "" {
		base = "https://api.audiency.io"
	}
	return &AudiencyImageHandler{APIKey: key, Base: strings.TrimRight(base, "/")}
}

// ServeHTTP fetches the image from Audiency and streams it back. The
// response uses the upstream Content-Type and Content-Length; we also set
// a permissive Cache-Control because the tokens are immutable per-file.
func (h *AudiencyImageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}
	// Tokens são hex/alfanum até ~128 chars; descarta input absurdo cedo.
	if len(token) > 256 || !isSafeToken(token) {
		http.Error(w, "invalid token", http.StatusBadRequest)
		return
	}

	upstream := fmt.Sprintf("%s/advertiser-rest/files/image?token=%s", h.Base, url.QueryEscape(token))

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstream, nil)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	req.Header.Set("apiKey", h.APIKey)
	req.Header.Set("Accept", "image/*")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, "upstream unreachable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		http.Error(w, "image not found", http.StatusNotFound)
		return
	}
	if resp.StatusCode >= 400 {
		http.Error(w, fmt.Sprintf("upstream status %d", resp.StatusCode), http.StatusBadGateway)
		return
	}

	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	} else {
		w.Header().Set("Content-Type", "image/png")
	}
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		w.Header().Set("Content-Length", cl)
	}
	// Tokens são únicos por arquivo (upload reescreve o token), então
	// podemos cachear agressivamente. Tunável via flag se precisar.
	w.Header().Set("Cache-Control", "public, max-age=86400, immutable")

	_, _ = io.Copy(w, resp.Body)
}

// isSafeToken aceita só alfanum (Audiency emite hex/base64-url).
func isSafeToken(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		default:
			return false
		}
	}
	return true
}
