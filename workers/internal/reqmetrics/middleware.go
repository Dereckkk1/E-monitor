package reqmetrics

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"radiocheck/internal/auth"
)

// Middleware wraps chi handlers, mede a duration, captura status/IP/usuário
// e submete um Sample ao Writer. Tem que rodar APÓS chi resolver a rota (para
// `RoutePattern()` estar disponível) e APÓS RequireJWT (para os claims).
// Como dentro do mesmo Group (`r.Use(...)`) o chi acumula middlewares de fora
// para dentro, o pattern é resolvido pelo dispatcher antes do handler — então
// basta encadear esse middleware no nível certo e ler o pattern depois do
// `next.ServeHTTP`.
//
// Skip routes: /metrics (Prometheus) e /v1/internal/health são chamadas
// internas de probe que não devem poluir o painel.
func Middleware(w *Writer, slowMs int) func(http.Handler) http.Handler {
	if slowMs <= 0 {
		slowMs = 2000
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			// Pula caminhos de instrumentação para não auto-poluir o painel.
			if shouldSkip(r.URL.Path) {
				next.ServeHTTP(rw, r)
				return
			}

			start := time.Now()
			ww := middleware.NewWrapResponseWriter(rw, r.ProtoMajor)
			next.ServeHTTP(ww, r)

			// Resolve route pattern DEPOIS do handler — só agora chi populou
			// o RouteContext. Fallback no path cru se o pattern não foi
			// resolvido (404, OPTIONS pré-middleware, etc.).
			route := r.URL.Path
			if rctx := chi.RouteContext(r.Context()); rctx != nil {
				if p := rctx.RoutePattern(); p != "" {
					route = p
				}
			}

			dur := time.Since(start)
			ms := int(dur.Milliseconds())

			status := ww.Status()
			if status == 0 {
				status = http.StatusOK
			}

			sample := Sample{
				TS:         time.Now().UTC(),
				Route:      route,
				Method:     r.Method,
				StatusCode: status,
				DurationMs: ms,
				IP:         clientIP(r),
				IsError:    status >= 500,
				IsSlow:     ms > slowMs,
			}

			// Captura claims se o request passou por RequireJWT.
			if claims, ok := auth.ClaimsFromContext(r.Context()); ok && claims != nil {
				uid := claims.UserID
				sample.UserID = &uid
				// O JWT atual não carrega email no claim — preencher o email
				// exigiria lookup por request (overhead) ou cache. Deixamos
				// vazio aqui e o handler /top-actors faz o JOIN com users.
			}

			w.Submit(sample)
		})
	}
}

// shouldSkip cobre paths que não pertencem ao painel de monitoramento — eles
// poluiriam timeline e contagens com ruído mecânico.
func shouldSkip(path string) bool {
	switch {
	case path == "/metrics":
		return true
	case path == "/v1/internal/health":
		return true
	case strings.HasPrefix(path, "/debug/pprof"):
		return true
	}
	return false
}

// clientIP extrai o melhor candidato a IP de cliente: usa X-Forwarded-For
// quando atrás de proxy (Cloudflare Tunnel/Nginx), senão RemoteAddr cru.
// chi.middleware.RealIP já reescreve r.RemoteAddr quando o header está
// presente, então aqui basta pegar e tirar a porta.
func clientIP(r *http.Request) string {
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 && i < len(host)-1 {
		// Tira ":port" mas preserva IPv6 envolvido em [].
		if strings.HasPrefix(host, "[") {
			if j := strings.Index(host, "]"); j > 0 {
				host = host[1:j]
			}
		} else {
			host = host[:i]
		}
	}
	return host
}
