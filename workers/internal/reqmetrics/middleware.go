package reqmetrics

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
)

// userIDHolder é um receptáculo mutável usado para propagar o user_id capturado
// pelo auth.RequireJWT (que roda em sub-Group, depois) de volta ao middleware
// de métricas (que roda no topo, antes). O chi propaga r.Context() para dentro,
// mas mutações no contexto não voltam — então usamos um ponteiro compartilhado:
// o outer cria, escreve no holder, e o inner middleware lê após next.ServeHTTP.
type userIDHolder struct{ UserID *uuid.UUID }

type holderCtxKey struct{}

// SetUserID grava o user_id no holder se ele existir no contexto. Chamado pelo
// auth.RequireJWT após parsear o JWT. Silencioso se não houver holder (request
// não veio através do reqmetrics.Middleware — ex: testes unitários de auth).
func SetUserID(ctx context.Context, uid uuid.UUID) {
	if h, ok := ctx.Value(holderCtxKey{}).(*userIDHolder); ok {
		h.UserID = &uid
	}
}

// Middleware wraps chi handlers, mede a duration, captura status/IP/usuário
// e submete um Sample ao Writer. Roda como middleware externo (antes do
// RequireJWT) para capturar tanto requests anônimos quanto autenticados — o
// user_id é propagado de volta via userIDHolder (ver acima).
//
// O chi resolve o RoutePattern durante o dispatch interno; lemos via
// `chi.RouteContext(r.Context()).RoutePattern()` DEPOIS de `next.ServeHTTP`
// para ter a rota normalizada (ex: /v1/users/{id}, não /v1/users/abc-123).
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

			// Insere holder no contexto antes do next — o auth.RequireJWT do
			// sub-Group escreve nele quando o request tem JWT válido.
			holder := &userIDHolder{}
			ctx := context.WithValue(r.Context(), holderCtxKey{}, holder)
			next.ServeHTTP(ww, r.WithContext(ctx))

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

			// Captura user_id capturado pelo auth.RequireJWT via holder.
			// (Não dá pra ler r.Context() direto — chi propaga contexto pra
			// dentro, não pra fora. Ver comentário em userIDHolder.)
			if holder.UserID != nil {
				sample.UserID = holder.UserID
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
