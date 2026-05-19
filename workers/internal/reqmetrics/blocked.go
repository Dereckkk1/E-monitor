package reqmetrics

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// BlockList mantém em RAM o conjunto de IPs banidos via /admin/monitoring.
// Refresh: a cada 60s a tabela blocked_ips é re-lida. Inserções imediatas
// (block-ip handler) chamam Add para propagar sem esperar o ciclo.
//
// Por que cache em RAM: o middleware checa em todo request — ler do DB toda
// vez adicionaria 1 round-trip ao caminho quente. 60s de janela é OK para
// fluxo de bloqueio operacional (admin clica → ataque cessa em <1min).
type BlockList struct {
	pool *pgxpool.Pool
	log  *zap.Logger

	mu  sync.RWMutex
	ips map[string]struct{}
}

// NewBlockList constrói e faz a primeira carga sincronamente. Retorna mesmo
// se a carga falhar — set vazio é seguro (apenas não bloqueia ninguém).
func NewBlockList(ctx context.Context, pool *pgxpool.Pool, log *zap.Logger) *BlockList {
	b := &BlockList{pool: pool, log: log, ips: map[string]struct{}{}}
	if err := b.Reload(ctx); err != nil && log != nil {
		log.Warn("reqmetrics: initial block-list load failed", zap.Error(err))
	}
	return b
}

// Reload re-lê a tabela. Substitui o set inteiro de uma vez (não merge) — é
// fonte da verdade.
func (b *BlockList) Reload(ctx context.Context) error {
	rows, err := b.pool.Query(ctx, `SELECT ip FROM blocked_ips`)
	if err != nil {
		return err
	}
	defer rows.Close()

	next := map[string]struct{}{}
	for rows.Next() {
		var ip string
		if err := rows.Scan(&ip); err != nil {
			continue
		}
		next[ip] = struct{}{}
	}
	b.mu.Lock()
	b.ips = next
	b.mu.Unlock()
	return rows.Err()
}

// Add insere um IP no set in-memory imediatamente (chamado pelo block-ip
// handler). NÃO toca o DB — esse é responsabilidade do handler.
func (b *BlockList) Add(ip string) {
	b.mu.Lock()
	b.ips[ip] = struct{}{}
	b.mu.Unlock()
}

// Remove tira do set in-memory.
func (b *BlockList) Remove(ip string) {
	b.mu.Lock()
	delete(b.ips, ip)
	b.mu.Unlock()
}

// Has retorna true se o IP está bloqueado.
func (b *BlockList) Has(ip string) bool {
	b.mu.RLock()
	_, ok := b.ips[ip]
	b.mu.RUnlock()
	return ok
}

// Run mantém o cache fresco — re-lê a cada 60s até ctx cancelar. Roda em
// goroutine, é tolerante a falhas (log + continua).
func (b *BlockList) Run(ctx context.Context) {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := b.Reload(ctx); err != nil && b.log != nil {
				b.log.Warn("reqmetrics: block-list reload failed", zap.Error(err))
			}
		}
	}
}

// BlockMiddleware rejeita com 403 requests vindos de IP banido, EXCETO
// rotas de admin/monitoring (para o admin poder se desbloquear caso
// compartilhe IP com algo banido) e o login (mesma razão — operador precisa
// poder entrar para gerir).
func BlockMiddleware(list *BlockList) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isAdminExempt(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			if list != nil && list.Has(clientIP(r)) {
				http.Error(w, "forbidden: ip blocked", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// isAdminExempt: paths que nunca podem ser bloqueados por IP. /metrics e
// /v1/internal/health são probes internos — devem responder mesmo a IPs
// banidos. /v1/internal/auth/login e /v1/internal/admin/* permitem que o
// operador se desbloqueie caso esteja no mesmo NAT que um IP banido.
func isAdminExempt(path string) bool {
	switch {
	case path == "/metrics":
		return true
	case path == "/v1/internal/health":
		return true
	case path == "/v1/internal/auth/login":
		return true
	case strings.HasPrefix(path, "/v1/internal/admin/"):
		return true
	}
	return false
}
