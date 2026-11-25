package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"golang.org/x/crypto/bcrypt"

	"radiocheck/internal/users"
)

// --- JWT --------------------------------------------------------------------

func TestCovZ_IssueTokenForUser_CarriesScope(t *testing.T) {
	t.Setenv("JWT_SECRET", covZSecret)
	cid, other := uuid.New(), uuid.New()
	u := &users.User{ID: uuid.New(), Role: "viewer", ClientID: &cid, ClientIDs: []uuid.UUID{cid, other}}

	tok, err := IssueTokenForUser(u)
	require.NoError(t, err)

	got, err := ParseToken(tok)
	require.NoError(t, err)
	assert.Equal(t, u.ID, got.UserID)
	assert.Equal(t, "viewer", got.Role)
	require.NotNil(t, got.ClientID)
	assert.Equal(t, cid, *got.ClientID)
	assert.Equal(t, []uuid.UUID{cid, other}, got.ClientIDs)
	require.NotNil(t, got.ExpiresAt)
	assert.WithinDuration(t, time.Now().Add(8*time.Hour), got.ExpiresAt.Time, time.Minute, "validade de 8h")
}

func TestCovZ_IssueToken_AdminHasNoClient(t *testing.T) {
	t.Setenv("JWT_SECRET", covZSecret)
	id := uuid.New()
	tok, err := IssueToken(id, "admin")
	require.NoError(t, err)
	got, err := ParseToken(tok)
	require.NoError(t, err)
	assert.Nil(t, got.ClientID)
	assert.Empty(t, got.ClientIDs)
}

func TestCovZ_JWT_SecretTooShort(t *testing.T) {
	t.Setenv("JWT_SECRET", "curto")

	_, err := IssueTokenForUser(&users.User{ID: uuid.New(), Role: "admin"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "32 bytes")

	_, err = IssueToken(uuid.New(), "admin")
	require.Error(t, err)

	_, err = ParseToken("qualquer.coisa.aqui")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "32 bytes")
}

func TestCovZ_ParseToken_Rejections(t *testing.T) {
	t.Setenv("JWT_SECRET", covZSecret)

	valid := covZSign(t, covZSecret, covZClaims("admin"))
	expired := covZClaims("admin")
	expired.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute))
	notYet := covZClaims("admin")
	notYet.NotBefore = jwt.NewNumericDate(time.Now().Add(time.Hour))

	noneTok, err := jwt.NewWithClaims(jwt.SigningMethodNone, covZClaims("admin")).SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)
	hs384, err := jwt.NewWithClaims(jwt.SigningMethodHS384, covZClaims("admin")).SignedString([]byte(covZSecret))
	require.NoError(t, err)

	parts := strings.Split(valid, ".")
	require.Len(t, parts, 3)
	// Troca o claim de papel mantendo a assinatura original: a assinatura deixa de bater.
	forgedClaims := covZSign(t, covZSecret, covZClaims("viewer"))
	forged := strings.Split(forgedClaims, ".")[0] + "." + strings.Split(forgedClaims, ".")[1] + "." + parts[2]

	cases := []struct {
		name  string
		token string
		want  error
	}{
		{"assinado com outra chave", covZSign(t, "outra-chave-de-teste-com-mais-de-32-bytes", covZClaims("admin")), jwt.ErrSignatureInvalid},
		{"expirado", covZSign(t, covZSecret, expired), jwt.ErrTokenExpired},
		{"nbf no futuro", covZSign(t, covZSecret, notYet), jwt.ErrTokenNotValidYet},
		{"claims adulterados com assinatura antiga", forged, jwt.ErrSignatureInvalid},
		{"alg none", noneTok, nil},
		{"hs384 (variante HMAC nao aceita)", hs384, nil},
		{"lixo", "nao-e-um-jwt", jwt.ErrTokenMalformed},
		{"vazio", "", jwt.ErrTokenMalformed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseToken(c.token)
			require.Error(t, err)
			assert.Nil(t, got)
			if c.want != nil {
				assert.ErrorIs(t, err, c.want)
			}
		})
	}

	// Controle: o token valido passa.
	_, err = ParseToken(valid)
	require.NoError(t, err)
}

// --- Middlewares ------------------------------------------------------------

func TestCovZ_RequireJWT(t *testing.T) {
	t.Setenv("JWT_SECRET", covZSecret)
	var seen *Claims
	h := RequireJWT(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = ClaimsFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))

	expired := covZClaims("admin")
	expired.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Hour))
	good := covZClaims("operator")

	cases := []struct {
		name   string
		header string
		want   int
	}{
		{"sem header", "", http.StatusUnauthorized},
		{"esquema errado", "Basic dXNlcjpwYXNz", http.StatusUnauthorized},
		{"bearer vazio", "Bearer ", http.StatusUnauthorized},
		{"token lixo", "Bearer xxx.yyy.zzz", http.StatusUnauthorized},
		{"expirado", "Bearer " + covZSign(t, covZSecret, expired), http.StatusUnauthorized},
		{"chave errada", "Bearer " + covZSign(t, "outra-chave-de-teste-com-mais-de-32-bytes", good), http.StatusUnauthorized},
		{"valido", "Bearer " + covZSign(t, covZSecret, good), http.StatusNoContent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			seen = nil
			req := httptest.NewRequest(http.MethodGet, "/x", nil)
			if c.header != "" {
				req.Header.Set("Authorization", c.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			assert.Equal(t, c.want, rec.Code)
			if c.want == http.StatusNoContent {
				require.NotNil(t, seen, "claims devem chegar ao handler")
				assert.Equal(t, good.UserID, seen.UserID)
				assert.Equal(t, "operator", seen.Role)
			} else {
				assert.Nil(t, seen, "handler nao deve rodar sem token valido")
			}
		})
	}
}

func TestCovZ_RequireJWTAtivo_NilVerifierIsPlainJWT(t *testing.T) {
	t.Setenv("JWT_SECRET", covZSecret)
	h := RequireJWTAtivo(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "sem token continua barrando")

	req = httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer "+covZSign(t, covZSecret, covZClaims("admin")))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestCovZ_RequireJWTAtivo_BlocksDeactivatedUser(t *testing.T) {
	t.Setenv("JWT_SECRET", covZSecret)
	ctx, pool := covZPool(t)

	active := covZUser(t, ctx, pool, "ativo-covz@example.com", true, false)
	inactive := covZUser(t, ctx, pool, "inativo-covz@example.com", false, false)
	deleted := covZUser(t, ctx, pool, "apagado-covz@example.com", true, true)

	v := NovoVerificadorAtivo(pool, time.Minute)
	h := RequireJWTAtivo(v)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	call := func(id uuid.UUID) int {
		c := covZClaims("admin")
		c.UserID = id
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("Authorization", "Bearer "+covZSign(t, covZSecret, c))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	assert.Equal(t, http.StatusNoContent, call(active))
	assert.Equal(t, http.StatusUnauthorized, call(inactive), "is_active=false vira 401 (sessao deixou de valer)")
	assert.Equal(t, http.StatusUnauthorized, call(deleted), "usuario apagado e tao inativo quanto o desativado")
}

func TestCovZ_RequireRole(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := RequireRole("admin", "operator")(ok)

	run := func(claims *Claims) int {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		if claims != nil {
			req = req.WithContext(WithClaims(req.Context(), claims))
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	assert.Equal(t, http.StatusUnauthorized, run(nil), "sem claims no contexto")
	assert.Equal(t, http.StatusForbidden, run(&Claims{Role: "viewer"}))
	assert.Equal(t, http.StatusForbidden, run(&Claims{Role: ""}))
	assert.Equal(t, http.StatusNoContent, run(&Claims{Role: "admin"}))
	assert.Equal(t, http.StatusNoContent, run(&Claims{Role: "operator"}), "qualquer papel da lista serve")
}

func TestCovZ_RequireRole_NoRolesDeniesEveryone(t *testing.T) {
	h := RequireRole()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req = req.WithContext(WithClaims(req.Context(), &Claims{Role: "admin"}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestCovZ_WithClaims_RoundTrip(t *testing.T) {
	_, ok := ClaimsFromContext(context.Background())
	assert.False(t, ok)

	c := &Claims{UserID: uuid.New(), Role: "viewer"}
	got, ok := ClaimsFromContext(WithClaims(context.Background(), c))
	require.True(t, ok)
	assert.Same(t, c, got)
}

// --- API key ----------------------------------------------------------------

func TestCovZ_GenerateAPIKey_HashMatches(t *testing.T) {
	raw, hash := GenerateAPIKey()
	raw2, hash2 := GenerateAPIKey()
	assert.Len(t, raw, 64)
	assert.Len(t, hash, 64)
	assert.NotEqual(t, raw, raw2)
	assert.NotEqual(t, hash, hash2)
	assert.NotEqual(t, raw, hash)
}

func TestCovZ_APIKeyMiddleware_CheckRate(t *testing.T) {
	m := NewAPIKeyMiddleware(nil)

	t.Run("limite por janela", func(t *testing.T) {
		assert.True(t, m.checkRate("h1", 2))
		assert.True(t, m.checkRate("h1", 2))
		assert.False(t, m.checkRate("h1", 2), "terceira na mesma janela excede")
		assert.True(t, m.checkRate("h2", 2), "chaves independentes")
	})

	t.Run("janela vencida reinicia a contagem", func(t *testing.T) {
		m.limits["h3"] = &rateLimitEntry{count: 99, windowStart: time.Now().Add(-2 * time.Minute)}
		assert.True(t, m.checkRate("h3", 1))
		assert.Equal(t, 1, m.limits["h3"].count)
	})

	t.Run("varredura periodica remove entradas velhas", func(t *testing.T) {
		m.limits["velha"] = &rateLimitEntry{count: 1, windowStart: time.Now().Add(-10 * time.Minute)}
		m.limits["recente"] = &rateLimitEntry{count: 1, windowStart: time.Now()}
		m.sweepCount = 999 // a proxima chamada e a de numero 1000
		assert.True(t, m.checkRate("qualquer", 10))
		assert.NotContains(t, m.limits, "velha")
		assert.Contains(t, m.limits, "recente")
	})
}

func TestCovZ_APIKeyMiddleware_Lookup(t *testing.T) {
	ctx, pool := covZPool(t)

	var clientID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO clients (name) VALUES ('Cliente Teste ApiKey') RETURNING id`).Scan(&clientID))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM api_keys WHERE client_id=$1`, clientID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM clients WHERE id=$1`, clientID)
	})

	rawOK, hashOK := GenerateAPIKey()
	rawRevoked, hashRevoked := GenerateAPIKey()
	rawLimited, hashLimited := GenerateAPIKey()
	_, err := pool.Exec(ctx, `INSERT INTO api_keys (client_id, key_hash, rate_limit) VALUES ($1,$2,60)`, clientID, hashOK)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO api_keys (client_id, key_hash, rate_limit, revoked_at) VALUES ($1,$2,60,now())`, clientID, hashRevoked)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO api_keys (client_id, key_hash, rate_limit) VALUES ($1,$2,2)`, clientID, hashLimited)
	require.NoError(t, err)

	var gotClient string
	var gotOK bool
	mw := NewAPIKeyMiddleware(pool).Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotClient, gotOK = ClientIDFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	do := func(setup func(*http.Request)) int {
		gotClient, gotOK = "", false
		req := httptest.NewRequest(http.MethodGet, "/v1/detections", nil)
		setup(req)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		return rec.Code
	}

	t.Run("sem chave", func(t *testing.T) {
		assert.Equal(t, http.StatusUnauthorized, do(func(r *http.Request) {}))
		assert.False(t, gotOK)
	})
	t.Run("header Authorization sem Bearer e ignorado", func(t *testing.T) {
		assert.Equal(t, http.StatusUnauthorized, do(func(r *http.Request) { r.Header.Set("Authorization", "Basic "+rawOK) }))
	})
	t.Run("chave desconhecida", func(t *testing.T) {
		assert.Equal(t, http.StatusUnauthorized, do(func(r *http.Request) { r.Header.Set("X-Api-Key", "nao-existe") }))
	})
	t.Run("chave revogada", func(t *testing.T) {
		assert.Equal(t, http.StatusUnauthorized, do(func(r *http.Request) { r.Header.Set("X-Api-Key", rawRevoked) }))
	})
	t.Run("chave valida via X-Api-Key injeta o client_id e marca last_used_at", func(t *testing.T) {
		assert.Equal(t, http.StatusNoContent, do(func(r *http.Request) { r.Header.Set("X-Api-Key", rawOK) }))
		assert.True(t, gotOK)
		assert.Equal(t, clientID.String(), gotClient)
		var used *time.Time
		require.NoError(t, pool.QueryRow(ctx, `SELECT last_used_at FROM api_keys WHERE key_hash=$1`, hashOK).Scan(&used))
		require.NotNil(t, used)
		assert.WithinDuration(t, time.Now(), *used, time.Minute)
	})
	t.Run("chave valida via Bearer", func(t *testing.T) {
		assert.Equal(t, http.StatusNoContent, do(func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+rawOK) }))
		assert.Equal(t, clientID.String(), gotClient)
	})
	t.Run("excesso de requisicoes devolve 429", func(t *testing.T) {
		withKey := func(r *http.Request) { r.Header.Set("X-Api-Key", rawLimited) }
		assert.Equal(t, http.StatusNoContent, do(withKey))
		assert.Equal(t, http.StatusNoContent, do(withKey))
		assert.Equal(t, http.StatusTooManyRequests, do(withKey))
		assert.False(t, gotOK, "handler nao roda quando limitado")
	})
}

func TestCovZ_APIKeyViewerScope_RejectsInvalidClientID(t *testing.T) {
	reached := false
	h := APIKeyViewerScope(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))

	req := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
	req = req.WithContext(context.WithValue(req.Context(), clientIDKey, "isto-nao-e-uuid"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.False(t, reached)
}

// --- Rate limiter de login --------------------------------------------------

func TestCovZ_LoginRateLimiter_DisabledWhenMaxNonPositive(t *testing.T) {
	for _, max := range []int{0, -1} {
		l := NewLoginRateLimiter(max, time.Minute)
		for i := 0; i < 50; i++ {
			require.True(t, l.Allow("k"))
		}
	}
}

func TestCovZ_LoginRateLimiter_SweepDropsExpiredWindows(t *testing.T) {
	l := NewLoginRateLimiter(5, time.Minute)
	l.windows["velha"] = &loginWindow{count: 3, windowStart: time.Now().Add(-10 * time.Minute)}
	l.windows["recente"] = &loginWindow{count: 3, windowStart: time.Now()}
	l.sweepCount = 999
	assert.True(t, l.Allow("nova"))
	assert.NotContains(t, l.windows, "velha")
	assert.Contains(t, l.windows, "recente")
	assert.Contains(t, l.windows, "nova")
}

func TestCovZ_ClientIPKey(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.RemoteAddr = "203.0.113.7:5555"
	assert.Equal(t, "203.0.113.7", clientIPKey(r))
	r.RemoteAddr = "[2001:db8::1]:443"
	assert.Equal(t, "2001:db8::1", clientIPKey(r))
	r.RemoteAddr = "sem-porta"
	assert.Equal(t, "sem-porta", clientIPKey(r), "sem porta devolve o valor cru")
}

func TestCovZ_LoginRateLimiter_Middleware_SetsRetryAfter(t *testing.T) {
	l := NewLoginRateLimiter(1, time.Minute)
	h := l.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	hit := func(addr string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/login", nil)
		req.RemoteAddr = addr
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	assert.Equal(t, http.StatusNoContent, hit("198.51.100.1:1000").Code)
	blocked := hit("198.51.100.1:2000") // outra porta, mesmo IP => mesma chave
	assert.Equal(t, http.StatusTooManyRequests, blocked.Code)
	assert.Equal(t, "60", blocked.Header().Get("Retry-After"))
	assert.Equal(t, http.StatusNoContent, hit("198.51.100.2:1000").Code, "outro IP nao e afetado")
}

// --- VerificadorAtivo com banco ---------------------------------------------

func TestCovZ_VerificadorAtivo_Database(t *testing.T) {
	ctx, pool := covZPool(t)
	v := NovoVerificadorAtivo(pool, time.Minute)

	active := covZUser(t, ctx, pool, "v-ativo-covz@example.com", true, false)
	inactive := covZUser(t, ctx, pool, "v-inativo-covz@example.com", false, false)
	deleted := covZUser(t, ctx, pool, "v-apagado-covz@example.com", true, true)

	assert.True(t, v.Ativo(ctx, active))
	assert.False(t, v.Ativo(ctx, inactive))
	assert.False(t, v.Ativo(ctx, deleted))

	// O resultado fica em cache: reativar no banco nao muda a resposta ate expirar/esquecer.
	_, err := pool.Exec(ctx, `UPDATE users SET is_active=true WHERE id=$1`, inactive)
	require.NoError(t, err)
	assert.False(t, v.Ativo(ctx, inactive), "ainda em cache dentro do TTL")
	v.Esquece(inactive)
	assert.True(t, v.Ativo(ctx, inactive), "depois de Esquece a consulta volta ao banco")

	// Desativar e esquecer tem efeito imediato.
	_, err = pool.Exec(ctx, `UPDATE users SET is_active=false WHERE id=$1`, active)
	require.NoError(t, err)
	assert.True(t, v.Ativo(ctx, active), "cache protege o caminho quente")
	v.Esquece(active)
	assert.False(t, v.Ativo(ctx, active))

	// Usuario inexistente libera e NAO polui o cache.
	ghost := uuid.New()
	assert.True(t, v.Ativo(ctx, ghost))
	_, cached := v.doCache(ghost.String(), time.Now())
	assert.False(t, cached)
}

func TestCovZ_VerificadorAtivo_DBFailureReleasesWithoutCaching(t *testing.T) {
	_, pool := covZPool(t)
	pool.Close() // consultas passam a falhar
	v := NovoVerificadorAtivo(pool, time.Minute)
	id := uuid.New()
	assert.True(t, v.Ativo(context.Background(), id), "falha do banco libera")
	_, cached := v.doCache(id.String(), time.Now())
	assert.False(t, cached, "falha nao pode ficar em cache")
}

func TestCovZ_VerificadorAtivo_GuardaEvictsExpiredAtCap(t *testing.T) {
	v := NovoVerificadorAtivo(nil, time.Minute)
	agora := time.Now()
	for i := 0; i < maxEntradasAtivo; i++ {
		v.cache[uuid.NewString()] = entradaAtivo{ativo: true, ate: agora.Add(-time.Hour)} // todas vencidas
	}
	vivo := uuid.NewString()
	v.cache[vivo] = entradaAtivo{ativo: false, ate: agora.Add(time.Hour)}

	v.guarda("nova", true, agora)

	assert.Contains(t, v.cache, "nova")
	assert.Contains(t, v.cache, vivo, "entrada valida nao e removida")
	assert.Len(t, v.cache, 2, "so sobram a valida e a nova")
}

func TestCovZ_VerificadorAtivo_EsqueceNilSafe(t *testing.T) {
	var v *VerificadorAtivo
	assert.NotPanics(t, func() { v.Esquece(uuid.New()) })
}

// --- EnsureAdmin ------------------------------------------------------------

func TestCovZ_MaskEmail(t *testing.T) {
	cases := []struct{ in, want string }{
		{"alice@example.com", "a***@example.com"},
		{"x@y.z", "x***@y.z"},
		{"sem-arroba", "sem-arroba"},
		{"@dominio.com", "@dominio.com"},
		{"", ""},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) { assert.Equal(t, c.want, maskEmail(c.in)) })
	}
}

func TestCovZ_EnsureAdmin_CreatesIdempotently(t *testing.T) {
	ctx, pool := covZPool(t)
	const email = "bootstrap-covz@example.com"
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE email=$1`, email) })

	core, logs := observer.New(zap.InfoLevel)
	log := zap.New(core)

	cfg := BootstrapConfig{Email: email, Password: "senha-de-teste-longa-1"}
	require.NoError(t, EnsureAdmin(ctx, pool, cfg, log))

	var role, hash string
	require.NoError(t, pool.QueryRow(ctx, `SELECT role, password_hash FROM users WHERE email=$1`, email).Scan(&role, &hash))
	assert.Equal(t, "admin", role, "papel padrao e admin")
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(hash), []byte(cfg.Password)), "senha guardada como bcrypt")
	require.Equal(t, 1, logs.FilterMessage("bootstrap admin created").Len())
	created := logs.FilterMessage("bootstrap admin created").All()[0]
	assert.Equal(t, "b***@example.com", created.ContextMap()["email"], "email mascarado no log")

	// Segunda execucao: nao recria nem troca a senha.
	cfg.Password = "outra-senha-de-teste-2"
	require.NoError(t, EnsureAdmin(ctx, pool, cfg, log))
	var hash2 string
	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT password_hash, (SELECT COUNT(*) FROM users WHERE email=$1) FROM users WHERE email=$1`, email).Scan(&hash2, &n))
	assert.Equal(t, hash, hash2, "senha nao e atualizada")
	assert.Equal(t, 1, n)
	assert.Equal(t, 1, logs.FilterMessage("bootstrap admin already exists").Len())
}

func TestCovZ_EnsureAdmin_CustomRoleAndNilLogger(t *testing.T) {
	ctx, pool := covZPool(t)
	const email = "bootstrap-op-covz@example.com"
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE email=$1`, email) })

	require.NoError(t, EnsureAdmin(ctx, pool, BootstrapConfig{Email: email, Password: "senha-de-teste-longa-1", Role: "operator"}, nil))
	var role string
	require.NoError(t, pool.QueryRow(ctx, `SELECT role FROM users WHERE email=$1`, email).Scan(&role))
	assert.Equal(t, "operator", role)

	// Ja existe + logger nulo: nao panica.
	assert.NoError(t, EnsureAdmin(ctx, pool, BootstrapConfig{Email: email, Password: "x"}, nil))
}

func TestCovZ_EnsureAdmin_Errors(t *testing.T) {
	ctx, pool := covZPool(t)
	const email = "bootstrap-erro-covz@example.com"
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE email=$1`, email) })

	t.Run("senha curta e recusada antes de gravar", func(t *testing.T) {
		err := EnsureAdmin(ctx, pool, BootstrapConfig{Email: email, Password: "curta"}, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "password too short")
		var n int
		require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE email=$1`, email).Scan(&n))
		assert.Zero(t, n)
	})

	t.Run("papel invalido viola o CHECK e o erro sobe", func(t *testing.T) {
		err := EnsureAdmin(ctx, pool, BootstrapConfig{Email: email, Password: "senha-de-teste-longa-1", Role: "superuser"}, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "insert user")
	})

	t.Run("banco indisponivel", func(t *testing.T) {
		_, closed := covZPool(t)
		closed.Close()
		err := EnsureAdmin(ctx, closed, BootstrapConfig{Email: email, Password: "senha-de-teste-longa-1"}, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "check existing user")
	})
}
