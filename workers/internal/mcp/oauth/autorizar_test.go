package oauth

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"radiocheck/internal/auth"
	"radiocheck/internal/mcp/mcptest"
)

func paramsAutorizar(clientID string) url.Values {
	return url.Values{
		"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirectClaude},
		"code_challenge": {challengeRFC}, "code_challenge_method": {"S256"}, "state": {"st-1"},
	}
}

func TestAutorizar_MandaParaOPortalComCOOP(t *testing.T) {
	a := novoAmbiente(t)
	_, reg := a.registrar(map[string]any{"client_name": "Claude", "redirect_uris": []string{redirectClaude}})
	res := a.autorizar(paramsAutorizar(reg["client_id"].(string)))
	require.Equal(t, http.StatusFound, res.StatusCode)
	require.Equal(t, "unsafe-none", res.Header.Get("Cross-Origin-Opener-Policy"), "lição do hub b77b344e: pop-up do Gemini")
	loc := res.Header.Get("Location")
	require.True(t, strings.HasPrefix(loc, "https://portal.teste/autorizar-ia?pedido="), loc)

	u, _ := url.Parse(loc)
	st, ped := a.get("/v1/internal/mcp/oauth/pedido?p="+url.QueryEscape(u.Query().Get("pedido")), a.jwt)
	require.Equal(t, http.StatusOK, st)
	require.Equal(t, "Claude", ped["cliente"])
	require.Equal(t, "claude.ai", ped["destino"])
}

func TestAutorizar_RecusasSemRedirect(t *testing.T) {
	a := novoAmbiente(t)
	res := a.autorizar(paramsAutorizar("emc_nao_existe"))
	require.Equal(t, http.StatusBadRequest, res.StatusCode, "cliente desconhecido: nunca redireciona")

	_, reg := a.registrar(map[string]any{"client_name": "Claude", "redirect_uris": []string{redirectClaude}})
	p := paramsAutorizar(reg["client_id"].(string))
	p.Set("redirect_uri", "https://evil.example/cb")
	res = a.autorizar(p)
	require.Equal(t, http.StatusBadRequest, res.StatusCode, "redirect não registrado: nunca redireciona (open redirect)")
}

func TestAutorizar_RecusasComRedirect(t *testing.T) {
	a := novoAmbiente(t)
	_, reg := a.registrar(map[string]any{"client_name": "Claude", "redirect_uris": []string{redirectClaude}})
	id := reg["client_id"].(string)
	a.ativar(id) // cliente novo não recebe erro por redirect (ver TestAutorizar_ClienteNovoNaoRecebeErroPorRedirect)
	casos := map[string]func(url.Values){
		"invalid_request/sem PKCE":         func(p url.Values) { p.Del("code_challenge"); p.Del("code_challenge_method") },
		"invalid_request/PKCE pela metade": func(p url.Values) { p.Del("code_challenge_method") },
		"invalid_request/plain":            func(p url.Values) { p.Set("code_challenge_method", "plain") },
		"unsupported_response_type/token":  func(p url.Values) { p.Set("response_type", "token") },
		"invalid_target/resource alheio":   func(p url.Values) { p.Set("resource", "https://outro.test/mcp") },
	}
	for nome, mexer := range casos {
		p := paramsAutorizar(id)
		mexer(p)
		res := a.autorizar(p)
		require.Equal(t, http.StatusFound, res.StatusCode, nome)
		loc, _ := url.Parse(res.Header.Get("Location"))
		require.Equal(t, "claude.ai", loc.Host, nome)
		require.Equal(t, strings.Split(nome, "/")[0], loc.Query().Get("error"), nome)
		require.Equal(t, "st-1", loc.Query().Get("state"), nome)
		require.Equal(t, "https://api.teste", loc.Query().Get("iss"), nome)
	}
}

func TestConceder_RecusaEAdminRebaixado(t *testing.T) {
	a := novoAmbiente(t)
	_, reg := a.registrar(map[string]any{"client_name": "Claude", "redirect_uris": []string{redirectClaude}})
	res := a.autorizar(paramsAutorizar(reg["client_id"].(string)))
	loc, _ := url.Parse(res.Header.Get("Location"))
	pedido := loc.Query().Get("pedido")

	st, out := a.postJSON("/v1/internal/mcp/oauth/grant", a.jwt, map[string]any{"pedido": pedido, "aprovar": false})
	require.Equal(t, http.StatusOK, st)
	volta, _ := url.Parse(out["redirect"].(string))
	require.Equal(t, "access_denied", volta.Query().Get("error"))
	require.Empty(t, volta.Query().Get("code"))

	// JWT de admin ainda válido (8h), mas a pessoa foi rebaixada: o grant relê.
	rebaixado := mcptest.Usuario(t, a.ctx, a.pool, "admin", true)
	jwt, err := auth.IssueToken(rebaixado, "admin")
	require.NoError(t, err)
	_, err = a.pool.Exec(a.ctx, `UPDATE users SET role = 'operator' WHERE id = $1`, rebaixado)
	require.NoError(t, err)
	st, _ = a.postJSON("/v1/internal/mcp/oauth/grant", jwt, map[string]any{"pedido": pedido, "aprovar": true})
	require.Equal(t, http.StatusForbidden, st)

	st, _ = a.postJSON("/v1/internal/mcp/oauth/grant", a.jwt, map[string]any{"pedido": pedido + "x", "aprovar": true})
	require.Equal(t, http.StatusBadRequest, st, "pedido adulterado")
}

func TestConceder_GravaAuditoria(t *testing.T) {
	a := novoAmbiente(t)
	a.conectar()
	var n int
	require.NoError(t, a.pool.QueryRow(a.ctx,
		`SELECT COUNT(*) FROM audit_log WHERE actor_id = $1 AND action = 'mcp.oauth.autorizado' AND metadata->>'origem' = 'mcp'`,
		a.admin).Scan(&n))
	require.Equal(t, 1, n)
}

// RFC 9700 §4.11.2: o DCR é aberto, então um cliente que nunca chegou ao
// /token pode ser de qualquer um — redirecionar o erro dele faria do
// /authorize um open redirect com a nossa origem na frente.
func TestAutorizar_ClienteNovoNaoRecebeErroPorRedirect(t *testing.T) {
	a := novoAmbiente(t)
	for _, corpo := range []map[string]any{
		{"client_name": "pub", "redirect_uris": []string{redirectClaude}},
		{"client_name": "conf", "token_endpoint_auth_method": "client_secret_post", "redirect_uris": []string{redirectClaude}},
	} {
		st, reg := a.registrar(corpo)
		require.Equal(t, http.StatusCreated, st)
		p := paramsAutorizar(reg["client_id"].(string))
		p.Set("response_type", "x")
		res := a.autorizar(p)
		require.Equal(t, http.StatusBadRequest, res.StatusCode, corpo["client_name"])
		require.Empty(t, res.Header.Get("Location"), corpo["client_name"])

		a.ativar(reg["client_id"].(string))
		res = a.autorizar(p)
		require.Equal(t, http.StatusFound, res.StatusCode, "depois do primeiro token o erro volta por redirect")
	}
}

func TestAutorizar_LoopbackComOutraPorta(t *testing.T) {
	a := novoAmbiente(t)
	_, reg := a.registrar(map[string]any{"client_name": "Claude Code", "redirect_uris": []string{"http://localhost:33418/callback"}})
	id := reg["client_id"].(string)
	p := paramsAutorizar(id)
	p.Set("redirect_uri", "http://localhost:5555/callback")
	res := a.autorizar(p)
	require.Equal(t, http.StatusFound, res.StatusCode)
	loc, _ := url.Parse(res.Header.Get("Location"))
	require.Equal(t, "portal.teste", loc.Host)

	st, out := a.postJSON("/v1/internal/mcp/oauth/grant", a.jwt, map[string]any{"pedido": loc.Query().Get("pedido"), "aprovar": true})
	require.Equal(t, http.StatusOK, st)
	volta, _ := url.Parse(out["redirect"].(string))
	require.Equal(t, "localhost:5555", volta.Host, "o pedido leva o redirect que foi pedido")
	code := volta.Query().Get("code")

	// O /token compara exato com o redirect do código, não com o registrado.
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {id}, "code": {code},
		"redirect_uri": {"http://localhost:5555/callback"}, "code_verifier": {verifierRFC}}
	st, tok := a.token(form)
	require.Equal(t, http.StatusOK, st, tok)

	p.Set("redirect_uri", "http://127.0.0.1:5555/callback")
	res = a.autorizar(p)
	require.Equal(t, http.StatusBadRequest, res.StatusCode, "outro host de loopback não casa")
}

func TestAutorizar_ResourceNormalizado(t *testing.T) {
	a := novoAmbiente(t)
	_, reg := a.registrar(map[string]any{"client_name": "Claude", "redirect_uris": []string{redirectClaude}})
	id := reg["client_id"].(string)
	a.ativar(id)
	for rec, ok := range map[string]bool{
		"https://api.teste/mcp":   true,
		"HTTPS://API.TESTE/mcp/":  true,
		"https://api.teste/mcp//": false,
		"https://api.teste/MCP":   false,
		"https://api.teste":       false,
	} {
		p := paramsAutorizar(id)
		p.Set("resource", rec)
		res := a.autorizar(p)
		require.Equal(t, http.StatusFound, res.StatusCode, rec)
		loc, _ := url.Parse(res.Header.Get("Location"))
		if ok {
			require.Equal(t, "portal.teste", loc.Host, rec)
		} else {
			require.Equal(t, "invalid_target", loc.Query().Get("error"), rec)
		}
	}
}

func TestConceder_SucessoLevaIssEState(t *testing.T) {
	a := novoAmbiente(t)
	_, reg := a.registrar(map[string]any{"client_name": "Claude", "redirect_uris": []string{redirectClaude}})
	res := a.autorizar(paramsAutorizar(reg["client_id"].(string)))
	loc, _ := url.Parse(res.Header.Get("Location"))
	st, out := a.postJSON("/v1/internal/mcp/oauth/grant", a.jwt, map[string]any{"pedido": loc.Query().Get("pedido"), "aprovar": true})
	require.Equal(t, http.StatusOK, st)
	volta, _ := url.Parse(out["redirect"].(string))
	require.Equal(t, "https://api.teste", volta.Query().Get("iss"), "RFC 9207")
	require.Equal(t, "st-1", volta.Query().Get("state"))
	require.NotEmpty(t, volta.Query().Get("code"))
}
