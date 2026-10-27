package oauth

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"radiocheck/internal/mcp/tokens"
)

func formCodigo(clientID, code, verifier string) url.Values {
	return url.Values{
		"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {code},
		"redirect_uri": {redirectClaude}, "code_verifier": {verifier},
	}
}

func TestFluxoPublico_PontaAPonta(t *testing.T) {
	a := novoAmbiente(t)
	clientID, code := a.conectar()
	require.True(t, strings.HasPrefix(code, tokens.PrefixoCodigo))

	st, tok := a.token(formCodigo(clientID, code, verifierRFC))
	require.Equal(t, http.StatusOK, st, tok)
	acesso, refresh := tok["access_token"].(string), tok["refresh_token"].(string)
	require.True(t, strings.HasPrefix(acesso, tokens.PrefixoAcesso))
	require.True(t, strings.HasPrefix(refresh, tokens.PrefixoRefresh))
	require.Equal(t, "Bearer", tok["token_type"])
	require.EqualValues(t, 30*24*3600, tok["expires_in"])

	_, err := a.srv.Tokens.Buscar(a.ctx, acesso, tokens.TipoOAuthAcesso)
	require.NoError(t, err)
	_, err = a.srv.Tokens.Buscar(a.ctx, refresh, tokens.TipoPessoal, tokens.TipoOAuthAcesso)
	require.ErrorIs(t, err, tokens.ErrNaoEncontrado, "refresh nunca autentica o /mcp")

	st, e := a.token(formCodigo(clientID, code, verifierRFC))
	require.Equal(t, http.StatusBadRequest, st)
	require.Equal(t, "invalid_grant", e["error"], "código é de uso único")

	st, tok2 := a.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID}, "refresh_token": {refresh}})
	require.Equal(t, http.StatusOK, st, tok2)
	require.NotEqual(t, refresh, tok2["refresh_token"])

	st, e = a.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID}, "refresh_token": {refresh}})
	require.Equal(t, http.StatusBadRequest, st)
	require.Equal(t, "invalid_grant", e["error"], "refresh rotacionado não serve de novo")

	var primeiro *string
	require.NoError(t, a.pool.QueryRow(a.ctx, `SELECT first_token_at::text FROM mcp_oauth_clients WHERE client_id = $1`, clientID).Scan(&primeiro))
	require.NotNil(t, primeiro)
}

func TestToken_VerifierErradoQueimaOCodigo(t *testing.T) {
	a := novoAmbiente(t)
	clientID, code := a.conectar()
	st, e := a.token(formCodigo(clientID, code, strings.Repeat("a", 43)))
	require.Equal(t, http.StatusBadRequest, st)
	require.Equal(t, "invalid_grant", e["error"])
	st, _ = a.token(formCodigo(clientID, code, verifierRFC))
	require.Equal(t, http.StatusBadRequest, st, "depois de uma tentativa errada o código morre")
}

func TestToken_DonoRebaixadoNaoRecebe(t *testing.T) {
	a := novoAmbiente(t)
	clientID, code := a.conectar()
	_, err := a.pool.Exec(a.ctx, `UPDATE users SET role = 'operator' WHERE id = $1`, a.admin)
	require.NoError(t, err)
	st, e := a.token(formCodigo(clientID, code, verifierRFC))
	require.Equal(t, http.StatusBadRequest, st)
	require.Equal(t, "invalid_grant", e["error"])
}

func TestToken_ClienteConfidencialDoGemini(t *testing.T) {
	a := novoAmbiente(t)
	const redirectGemini = "https://vertexaisearch.cloud.google.com/oauth-redirect"
	_, reg := a.registrar(map[string]any{"client_name": "Gemini Enterprise", "token_endpoint_auth_method": "client_secret_post", "redirect_uris": []string{redirectGemini}})
	clientID, segredo := reg["client_id"].(string), reg["client_secret"].(string)

	// Sem PKCE: o Gemini não faz.
	res := a.autorizar(url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirectGemini}, "state": {"g"}})
	require.Equal(t, http.StatusFound, res.StatusCode)
	loc, _ := url.Parse(res.Header.Get("Location"))
	require.Equal(t, "portal.teste", loc.Host)
	_, out := a.postJSON("/v1/internal/mcp/oauth/grant", a.jwt, map[string]any{"pedido": loc.Query().Get("pedido"), "aprovar": true})
	volta, _ := url.Parse(out["redirect"].(string))
	code := volta.Query().Get("code")
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirectGemini}}

	errado := url.Values{"client_id": {clientID}, "client_secret": {"emonitor_mcs_errado"}}
	for k, v := range form {
		errado[k] = v
	}
	st, e := a.token(errado)
	require.Equal(t, http.StatusUnauthorized, st)
	require.Equal(t, "invalid_client", e["error"])

	// O segredo é conferido ANTES de consumir: o código ainda vale, agora por Basic.
	st, tok := a.token(form, clientID, segredo)
	require.Equal(t, http.StatusOK, st, tok)

	st, _ = a.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID}, "refresh_token": {tok["refresh_token"].(string)}})
	require.Equal(t, http.StatusUnauthorized, st, "refresh de confidencial exige o segredo")
	st, _ = a.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID}, "client_secret": {segredo}, "refresh_token": {tok["refresh_token"].(string)}})
	require.Equal(t, http.StatusOK, st)
}

func TestToken_GrantDesconhecido(t *testing.T) {
	a := novoAmbiente(t)
	_, reg := a.registrar(map[string]any{"client_name": "x", "redirect_uris": []string{redirectClaude}})
	st, e := a.token(url.Values{"grant_type": {"password"}, "client_id": {reg["client_id"].(string)}})
	require.Equal(t, http.StatusBadRequest, st)
	require.Equal(t, "unsupported_grant_type", e["error"])
}
