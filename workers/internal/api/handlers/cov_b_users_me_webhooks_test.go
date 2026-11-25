package handlers

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"

	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
	"radiocheck/internal/users"
	"radiocheck/internal/webhook"
)

// ── Users ───────────────────────────────────────────────────────────────

func TestCovB_Users_ListWelcomeInvitesAndFilters(t *testing.T) {
	ctx, pool := covBDB(t)
	mail := &noopMailer{}
	svc := newWelcomeSvc(t, pool, mail)
	repo := users.NewRepo(pool)
	h := NewUsersHandler(repo, svc)
	caller := seedAdminCaller(t, ctx, pool)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente Teste")

	create := func(body string) map[string]any {
		rec := covBServe(h.Create, covBWithCtx(covBReq("POST", "/admin/users", body, nil, nil), nil, caller))
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		return covBJSON(t, rec)
	}
	invited := create(`{"role":"client","client_id":"` + cli.String() + `","name":"Ana Teste","email":"ana@example.com","password":"senha-forte-1","send_welcome":true}`)
	plain := create(`{"role":"client","client_id":"` + cli.String() + `","name":"Beto Teste","email":"beto@example.com","password":"senha-forte-2"}`)

	t.Run("send_welcome devolve o convite com link e status do email", func(t *testing.T) {
		w := invited["welcome"].(map[string]any)
		assert.Equal(t, "sent", w["email_status"])
		assert.True(t, strings.HasPrefix(w["link"].(string), "https://e-monitor.online/boasvindas/"))
		assert.NotNil(t, invited["id"])
		assert.Nil(t, plain["welcome"], "sem send_welcome não há bloco de convite")
		assert.Equal(t, 1, mail.sent)
	})
	t.Run("list traz welcome_invites só de quem foi convidado", func(t *testing.T) {
		rec := covBServe(h.List, covBReq("GET", "/admin/users?role=client", "", nil, caller))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		m := covBJSON(t, rec)
		inv := m["welcome_invites"].(map[string]any)
		require.Len(t, inv, 1)
		one := inv[invited["id"].(string)].(map[string]any)
		assert.Equal(t, "sent", one["email_status"])
		assert.NotEmpty(t, one["link"])
		assert.NotContains(t, inv, plain["id"].(string))
	})
	t.Run("list sem serviço de boas-vindas não traz o bloco", func(t *testing.T) {
		h2 := NewUsersHandler(repo, nil)
		rec := covBServe(h2.List, covBReq("GET", "/admin/users", "", nil, caller))
		require.Equal(t, http.StatusOK, rec.Code)
		assert.NotContains(t, covBJSON(t, rec), "welcome_invites")
	})
	t.Run("filtros de papel aceitam o vocabulário do banco", func(t *testing.T) {
		for role, wantMin := range map[string]int{"viewer": 2, "client": 2, "admin": 1, "operator": 0} {
			rec := covBServe(h.List, covBReq("GET", "/admin/users?role="+role, "", nil, caller))
			require.Equal(t, http.StatusOK, rec.Code, role)
			assert.GreaterOrEqual(t, int(covBJSON(t, rec)["total"].(float64)), wantMin, role)
		}
	})
	t.Run("filtro por client_id e por busca", func(t *testing.T) {
		rec := covBServe(h.List, covBReq("GET", "/admin/users?client_id="+cli.String(), "", nil, caller))
		require.Equal(t, http.StatusOK, rec.Code)
		assert.EqualValues(t, 2, covBJSON(t, rec)["total"])
		rec = covBServe(h.List, covBReq("GET", "/admin/users?q=beto", "", nil, caller))
		require.Equal(t, http.StatusOK, rec.Code)
		assert.EqualValues(t, 1, covBJSON(t, rec)["total"])
	})
	t.Run("client_id inválido 400", func(t *testing.T) {
		rec := covBServe(h.List, covBReq("GET", "/admin/users?client_id=x", "", nil, caller))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "invalid_client_id")
	})
	t.Run("lista vazia vira array", func(t *testing.T) {
		rec := covBServe(h.List, covBReq("GET", "/admin/users?q=ninguem-assim", "", nil, caller))
		require.Equal(t, http.StatusOK, rec.Code)
		m := covBJSON(t, rec)
		assert.Equal(t, []any{}, m["data"])
		assert.EqualValues(t, 0, m["total_pages"])
	})
}

func TestCovB_Users_GetCreatePatchResetDelete(t *testing.T) {
	ctx, pool := covBDB(t)
	repo := users.NewRepo(pool)
	h := NewUsersHandler(repo, nil)
	admin := covBClaims("admin", uuid.New())
	cli := mcptest.Cliente(t, ctx, pool, "Cliente Teste")

	t.Run("get: id inválido 400", func(t *testing.T) {
		assert.Equal(t, http.StatusBadRequest, covBServe(h.Get, covBReq("GET", "/x", "", map[string]string{"id": "x"}, admin)).Code)
	})
	t.Run("create: json inválido 400", func(t *testing.T) {
		assert.Equal(t, http.StatusBadRequest, covBServe(h.Create, covBReq("POST", "/x", `{"email":`, nil, admin)).Code)
	})
	t.Run("create: send_welcome sem serviço informa 'unavailable' e ainda cria o usuário", func(t *testing.T) {
		body := `{"role":"admin","name":"Admin Teste","email":"adm@example.com","password":"senha-forte-1","send_welcome":true}`
		rec := covBServe(h.Create, covBReq("POST", "/x", body, nil, admin))
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		m := covBJSON(t, rec)
		assert.Equal(t, "unavailable", m["welcome"].(map[string]any)["email_status"])
		u, err := repo.GetByEmail(ctx, "adm@example.com")
		require.NoError(t, err)
		assert.Equal(t, "admin", u.Role)
	})
	t.Run("create: preferências de e-mail do admin são gravadas", func(t *testing.T) {
		body := `{"role":"admin","name":"Admin Dois","email":"adm2@example.com","password":"senha-forte-1","receive_alert_emails":false,"receive_post_sale_emails":true}`
		rec := covBServe(h.Create, covBReq("POST", "/x", body, nil, admin))
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		m := covBJSON(t, rec)
		assert.Equal(t, false, m["receive_alert_emails"])
		assert.Equal(t, true, m["receive_post_sale_emails"])
	})

	cl, err := repo.Create(ctx, users.CreateInput{Email: "cli@example.com", PasswordHash: "x", Role: "viewer", ClientID: &cli, Name: "Cli Teste"})
	require.NoError(t, err)
	p := map[string]string{"id": cl.ID.String()}

	t.Run("get: devolve o usuário sem o hash da senha", func(t *testing.T) {
		rec := covBServe(h.Get, covBReq("GET", "/x", "", p, admin))
		require.Equal(t, http.StatusOK, rec.Code)
		m := covBJSON(t, rec)
		assert.Equal(t, "cli@example.com", m["email"])
		assert.NotContains(t, m, "password_hash")
		assert.NotContains(t, rec.Body.String(), "PasswordHash")
	})
	t.Run("patch: id inválido, json inválido e e-mail imutável", func(t *testing.T) {
		assert.Equal(t, http.StatusBadRequest, covBServe(h.Patch, covBReq("PATCH", "/x", `{}`, map[string]string{"id": "x"}, admin)).Code)
		assert.Equal(t, http.StatusBadRequest, covBServe(h.Patch, covBReq("PATCH", "/x", `{"name":`, p, admin)).Code)
		rec := covBServe(h.Patch, covBReq("PATCH", "/x", `{"email":"outro@example.com"}`, p, admin))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "email_immutable")
	})
	t.Run("patch: nome, telefone e desativação", func(t *testing.T) {
		rec := covBServe(h.Patch, covBReq("PATCH", "/x", `{"name":"Novo Nome","phone":"+5511999990000","is_active":false}`, p, admin))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		m := covBJSON(t, rec)
		assert.Equal(t, "Novo Nome", m["name"])
		assert.Equal(t, "+5511999990000", m["phone"])
		assert.Equal(t, false, m["is_active"])
		assert.Equal(t, cli.String(), m["client_id"], "a carteira do cliente não é tocada")
	})
	t.Run("reset de senha: validações e sucesso", func(t *testing.T) {
		assert.Equal(t, http.StatusBadRequest,
			covBServe(h.ResetPassword, covBReq("POST", "/x", `{"password":"longa-o-bastante"}`, map[string]string{"id": "x"}, admin)).Code)
		assert.Equal(t, http.StatusBadRequest, covBServe(h.ResetPassword, covBReq("POST", "/x", `{"password":`, p, admin)).Code)
		short := covBServe(h.ResetPassword, covBReq("POST", "/x", `{"password":"curta"}`, p, admin))
		assert.Equal(t, http.StatusBadRequest, short.Code)
		assert.Contains(t, short.Body.String(), "password_too_short")
		assert.Equal(t, http.StatusNotFound,
			covBServe(h.ResetPassword, covBReq("POST", "/x", `{"password":"longa-o-bastante"}`, map[string]string{"id": uuid.NewString()}, admin)).Code)

		rec := covBServe(h.ResetPassword, covBReq("POST", "/x", `{"password":"longa-o-bastante"}`, p, admin))
		require.Equal(t, http.StatusNoContent, rec.Code)
		u, err := repo.Get(ctx, cl.ID)
		require.NoError(t, err)
		assert.NoError(t, bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte("longa-o-bastante")))
	})
	t.Run("delete: id inválido 400, autoexclusão 400, inexistente 204 (idempotente)", func(t *testing.T) {
		assert.Equal(t, http.StatusBadRequest, covBServe(h.Delete, covBReq("DELETE", "/x", "", map[string]string{"id": "x"}, admin)).Code)
		self := covBReq("DELETE", "/x", "", p, covBClaims("admin", cl.ID))
		rec := covBServe(h.Delete, self)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "cannot_delete_self")
		assert.Equal(t, http.StatusNoContent,
			covBServe(h.Delete, covBReq("DELETE", "/x", "", map[string]string{"id": uuid.NewString()}, admin)).Code)
	})
}

func TestCovB_Users_ParseHelpers(t *testing.T) {
	for in, want := range map[string]string{"admin": "admin", "client": "viewer", "operator": "operator", "viewer": "viewer"} {
		got, ok := parseRoleFilter(in)
		assert.True(t, ok, in)
		assert.Equal(t, want, got, in)
	}
	for _, bad := range []string{"", "root", "ADMIN", "cliente"} {
		_, ok := parseRoleFilter(bad)
		assert.False(t, ok, bad)
	}
	assert.Equal(t, 7, atoiOr("", 7))
	assert.Equal(t, 3, atoiOr("3", 7))
	assert.Equal(t, 7, atoiOr("x", 7))
	assert.Equal(t, -2, atoiOr("-2", 7))
}

// ── Me ──────────────────────────────────────────────────────────────────

func TestCovB_Me_ErrorBranches(t *testing.T) {
	ctx, pool := covBDB(t)
	repo := users.NewRepo(pool)
	h := NewMeHandler(repo)
	hash, err := bcrypt.GenerateFromPassword([]byte("senha-atual-1"), 10)
	require.NoError(t, err)
	u, err := repo.Create(ctx, users.CreateInput{Email: "eu@example.com", PasswordHash: string(hash), Role: "admin", Name: "Eu"})
	require.NoError(t, err)
	me := &auth.Claims{UserID: u.ID, Role: "admin"}
	ghost := &auth.Claims{UserID: uuid.New(), Role: "admin"}

	t.Run("sem claims 401 nas três rotas", func(t *testing.T) {
		for name, hf := range map[string]http.HandlerFunc{"get": h.Get, "patch": h.Patch, "password": h.ChangePassword} {
			rec := covBServe(hf, covBReq("POST", "/me", `{}`, nil, nil))
			assert.Equal(t, http.StatusUnauthorized, rec.Code, name)
		}
	})
	t.Run("get: usuário que não existe mais 404", func(t *testing.T) {
		assert.Equal(t, http.StatusNotFound, covBServe(h.Get, covBReq("GET", "/me", "", nil, ghost)).Code)
	})
	t.Run("get: nunca devolve o hash", func(t *testing.T) {
		rec := covBServe(h.Get, covBReq("GET", "/me", "", nil, me))
		require.Equal(t, http.StatusOK, rec.Code)
		assert.NotContains(t, rec.Body.String(), string(hash))
	})
	t.Run("patch: json inválido 400; usuário inexistente 404", func(t *testing.T) {
		assert.Equal(t, http.StatusBadRequest, covBServe(h.Patch, covBReq("PATCH", "/me", `{"name":`, nil, me)).Code)
		assert.Equal(t, http.StatusNotFound, covBServe(h.Patch, covBReq("PATCH", "/me", `{"name":"x"}`, nil, ghost)).Code)
	})
	t.Run("patch: só nome e telefone mudam; papel e is_active no corpo são ignorados", func(t *testing.T) {
		rec := covBServe(h.Patch, covBReq("PATCH", "/me", `{"name":"Meu Nome","phone":"11 4000-0000","role":"viewer","is_active":false}`, nil, me))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		m := covBJSON(t, rec)
		assert.Equal(t, "Meu Nome", m["name"])
		assert.Equal(t, "11 4000-0000", m["phone"])
		assert.Equal(t, "admin", m["role"])
		assert.Equal(t, true, m["is_active"])
	})
	t.Run("senha: json inválido, campos faltando e nova curta", func(t *testing.T) {
		assert.Equal(t, http.StatusBadRequest, covBServe(h.ChangePassword, covBReq("POST", "/me/password", `{"current":`, nil, me)).Code)
		for _, body := range []string{`{}`, `{"current_password":"senha-atual-1"}`, `{"new_password":"nova-senha-1"}`} {
			rec := covBServe(h.ChangePassword, covBReq("POST", "/me/password", body, nil, me))
			assert.Equal(t, http.StatusBadRequest, rec.Code, body)
			assert.Contains(t, rec.Body.String(), "missing_fields")
		}
		rec := covBServe(h.ChangePassword, covBReq("POST", "/me/password", `{"current_password":"senha-atual-1","new_password":"curta"}`, nil, me))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "password_too_short")
	})
	t.Run("senha: atual errada 401 e a senha não muda", func(t *testing.T) {
		rec := covBServe(h.ChangePassword, covBReq("POST", "/me/password", `{"current_password":"errada-errada","new_password":"nova-senha-1"}`, nil, me))
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.Contains(t, rec.Body.String(), "invalid_current_password")
		cur, err := repo.Get(ctx, u.ID)
		require.NoError(t, err)
		assert.NoError(t, bcrypt.CompareHashAndPassword([]byte(cur.PasswordHash), []byte("senha-atual-1")))
	})
	t.Run("senha: usuário inexistente falha sem 204", func(t *testing.T) {
		rec := covBServe(h.ChangePassword, covBReq("POST", "/me/password", `{"current_password":"senha-atual-1","new_password":"nova-senha-1"}`, nil, ghost))
		assert.GreaterOrEqual(t, rec.Code, 400)
	})
	t.Run("senha: sucesso troca o hash", func(t *testing.T) {
		rec := covBServe(h.ChangePassword, covBReq("POST", "/me/password", `{"current_password":"senha-atual-1","new_password":"nova-senha-1"}`, nil, me))
		require.Equal(t, http.StatusNoContent, rec.Code)
		cur, err := repo.Get(ctx, u.ID)
		require.NoError(t, err)
		assert.NoError(t, bcrypt.CompareHashAndPassword([]byte(cur.PasswordHash), []byte("nova-senha-1")))
		assert.Error(t, bcrypt.CompareHashAndPassword([]byte(cur.PasswordHash), []byte("senha-atual-1")))
	})
}

// ── Webhooks ────────────────────────────────────────────────────────────

func TestCovB_Webhooks_ConfigDeliveriesAndTest(t *testing.T) {
	ctx, pool := covBDB(t)
	// http://localhost só é aceito em desenvolvimento; também desliga a checagem de DNS/SSRF.
	t.Setenv("RADIOCHECK_ENV", "development")
	clients := catalog.NewClients(pool)
	outbox := webhook.NewOutbox(pool, clients, zap.NewNop())
	h := NewWebhooksHandler(pool, clients, outbox)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente Teste")
	p := map[string]string{"id": cli.String()}
	ghost := map[string]string{"id": uuid.NewString()}

	t.Run("get: cliente sem webhook devolve a configuração padrão", func(t *testing.T) {
		rec := covBServe(h.GetConfig, covBReq("GET", "/x", "", p, nil))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		m := covBJSON(t, rec)
		assert.Equal(t, "", m["webhook_url"])
		assert.Equal(t, false, m["webhook_enabled"])
		assert.Equal(t, false, m["has_secret"])
		assert.Equal(t, []any{"detection.confirmed"}, m["webhook_events"])
	})
	t.Run("get e patch: cliente inexistente 404", func(t *testing.T) {
		assert.Equal(t, http.StatusNotFound, covBServe(h.GetConfig, covBReq("GET", "/x", "", ghost, nil)).Code)
		assert.Equal(t, http.StatusNotFound,
			covBServe(h.PatchConfig, covBReq("PATCH", "/x", `{"webhook_enabled":true}`, ghost, nil)).Code)
	})
	t.Run("send-test: cliente sem webhook responde 202 mas não enfileira nada", func(t *testing.T) {
		rec := covBServe(h.SendTest, covBReq("POST", "/x", "", p, nil))
		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
		assert.JSONEq(t, `{"queued":true}`, strings.TrimSpace(rec.Body.String()))
		var n int
		require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM webhook_deliveries WHERE client_id = $1`, cli).Scan(&n))
		assert.Equal(t, 0, n)
	})
	t.Run("send-test: cliente inexistente 404 e id inválido 400", func(t *testing.T) {
		assert.Equal(t, http.StatusNotFound, covBServe(h.SendTest, covBReq("POST", "/x", "", ghost, nil)).Code)
		assert.Equal(t, http.StatusBadRequest, covBServe(h.SendTest, covBReq("POST", "/x", "", map[string]string{"id": "x"}, nil)).Code)
	})
	t.Run("patch: configura, mascara o segredo e persiste", func(t *testing.T) {
		body := `{"webhook_url":"http://localhost:9/hook","webhook_secret":"segredo-com-mais-de-16","webhook_enabled":true,"webhook_events":["detection.confirmed","webhook.test"]}`
		rec := covBServe(h.PatchConfig, covBReq("PATCH", "/x", body, p, nil))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		m := covBJSON(t, rec)
		assert.Equal(t, "http://localhost:9/hook", m["webhook_url"])
		assert.Equal(t, true, m["webhook_enabled"])
		assert.Equal(t, true, m["has_secret"])
		assert.Equal(t, "segr...", m["webhook_secret_masked"])
		assert.NotContains(t, rec.Body.String(), "segredo-com-mais-de-16", "o segredo nunca volta em claro")

		got := covBJSON(t, covBServe(h.GetConfig, covBReq("GET", "/x", "", p, nil)))
		assert.Equal(t, "http://localhost:9/hook", got["webhook_url"])
		assert.Equal(t, []any{"detection.confirmed", "webhook.test"}, got["webhook_events"])
	})
	t.Run("send-test: com webhook ativo enfileira uma entrega webhook.test", func(t *testing.T) {
		rec := covBServe(h.SendTest, covBReq("POST", "/x", "", p, nil))
		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
		var typ, status string
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT event_type, status FROM webhook_deliveries WHERE client_id = $1`, cli).Scan(&typ, &status))
		assert.Equal(t, "webhook.test", typ)
		assert.Equal(t, "pending", status)
	})
	t.Run("deliveries: lista, limita e filtra por status", func(t *testing.T) {
		_, err := pool.Exec(ctx,
			`INSERT INTO webhook_deliveries (client_id, event_type, payload, status)
			 VALUES ($1, 'detection.confirmed', '{}', 'failed'), ($1, 'detection.confirmed', '{}', 'delivered')`, cli)
		require.NoError(t, err)

		count := func(q string) int {
			rec := covBServe(h.ListDeliveries, covBReq("GET", "/x"+q, "", p, nil))
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			return len(covBJSON(t, rec)["data"].([]any))
		}
		assert.Equal(t, 3, count(""))
		assert.Equal(t, 1, count("?limit=1"))
		assert.Equal(t, 3, count("?limit=0"), "limit inválido volta ao padrão")
		assert.Equal(t, 3, count("?limit=999"), "acima de 200 também")
		assert.Equal(t, 3, count("?limit=abc"))
		assert.Equal(t, 1, count("?status=failed"))
		assert.Equal(t, 1, count("?status=%20pending%20"), "status é aparado")
		assert.Equal(t, 0, count("?status=dead"))
	})
	t.Run("deliveries: cliente sem entregas devolve data vazio", func(t *testing.T) {
		other := mcptest.Cliente(t, ctx, pool, "Outro Cliente Teste")
		rec := covBServe(h.ListDeliveries, covBReq("GET", "/x", "", map[string]string{"id": other.String()}, nil))
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Empty(t, covBJSON(t, rec)["data"])
	})
}
