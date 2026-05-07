package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// GenerateAPIKey returns (rawKey, keyHash). Raw is shown once; hash is stored.
func GenerateAPIKey() (string, string) {
	raw := make([]byte, 32)
	rand.Read(raw) //nolint:errcheck
	rawHex := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(rawHex))
	return rawHex, hex.EncodeToString(sum[:])
}

type rateLimitEntry struct {
	count       int
	windowStart time.Time
}

type APIKeyMiddleware struct {
	db         *pgxpool.Pool
	mu         sync.Mutex
	limits     map[string]*rateLimitEntry
	sweepCount int
}

func NewAPIKeyMiddleware(db *pgxpool.Pool) *APIKeyMiddleware {
	return &APIKeyMiddleware{db: db, limits: make(map[string]*rateLimitEntry)}
}

type ctxClientKey string

const clientIDKey ctxClientKey = "client_id"

// ClientIDFromContext extracts the client ID from the request context.
func ClientIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(clientIDKey).(string)
	return id, ok
}

func (m *APIKeyMiddleware) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.Header.Get("X-Api-Key")
		if raw == "" {
			if bearer := r.Header.Get("Authorization"); strings.HasPrefix(bearer, "Bearer ") {
				raw = strings.TrimPrefix(bearer, "Bearer ")
			}
		}
		if raw == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		sum := sha256.Sum256([]byte(raw))
		hash := hex.EncodeToString(sum[:])

		var clientID string
		var rateLimit int
		err := m.db.QueryRow(r.Context(), `
			SELECT client_id::text, rate_limit FROM api_keys
			WHERE key_hash = $1 AND revoked_at IS NULL
		`, hash).Scan(&clientID, &rateLimit)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if !m.checkRate(hash, rateLimit) {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		_, _ = m.db.Exec(r.Context(), `UPDATE api_keys SET last_used_at = NOW() WHERE key_hash = $1`, hash)
		ctx := context.WithValue(r.Context(), clientIDKey, clientID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (m *APIKeyMiddleware) checkRate(hash string, limitPerMin int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Sweep stale entries every 1000 requests.
	m.sweepCount++
	if m.sweepCount%1000 == 0 {
		for k, v := range m.limits {
			if time.Since(v.windowStart) > 2*time.Minute {
				delete(m.limits, k)
			}
		}
	}

	e, ok := m.limits[hash]
	if !ok || time.Since(e.windowStart) > time.Minute {
		m.limits[hash] = &rateLimitEntry{count: 1, windowStart: time.Now()}
		return true
	}
	e.count++
	return e.count <= limitPerMin
}
