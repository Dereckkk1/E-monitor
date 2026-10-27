package oauth

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

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

// Rebaixa ANTES de o código existir (o trigger da 0070 não tem o que apagar),
// para exercitar o DonoAdminAtivo do trocarCodigo.
func TestToken_TrocarCodigoReleODono(t *testing.T) {
	a := novoAmbiente(t)
	_, reg := a.registrar(map[string]any{"client_name": "Claude", "redirect_uris": []string{redirectClaude}})
	clientID := reg["client_id"].(string)
	_, err := a.pool.Exec(a.ctx, `UPDATE users SET role = 'operator' WHERE id = $1`, a.admin)
	require.NoError(t, err)
	ch := challengeRFC
	code, err := a.srv.Codigos.Criar(a.ctx, Codigo{ClientID: clientID, UserID: a.admin, RedirectURI: redirectClaude, Desafio: &ch})
	require.NoError(t, err)

	st, e := a.token(formCodigo(clientID, code, verifierRFC))
	require.Equal(t, http.StatusBadRequest, st)
	require.Equal(t, "invalid_grant", e["error"])
	require.Contains(t, e["error_description"], "administrador ativo")
	require.Zero(t, a.contar(`SELECT COUNT(*) FROM mcp_oauth_codes`), "código queimado")
	require.Zero(t, a.contar(`SELECT COUNT(*) FROM mcp_tokens`), "nada emitido")
}

func TestToken_RenovarReleODono(t *testing.T) {
	a := novoAmbiente(t)
	_, reg := a.registrar(map[string]any{"client_name": "Claude", "redirect_uris": []string{redirectClaude}})
	clientID := reg["client_id"].(string)
	_, err := a.pool.Exec(a.ctx, `UPDATE users SET is_active = false WHERE id = $1`, a.admin)
	require.NoError(t, err)
	cid := clientID
	refresh, tk, err := a.srv.Tokens.Emitir(a.ctx, tokens.Novo{UserID: a.admin, Tipo: tokens.TipoOAuthRefresh, Nome: "x", ClientID: &cid, Validade: time.Hour})
	require.NoError(t, err)

	st, e := a.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID}, "refresh_token": {refresh}})
	require.Equal(t, http.StatusBadRequest, st)
	require.Equal(t, "invalid_grant", e["error"])
	require.Contains(t, e["error_description"], "administrador ativo")
	require.Equal(t, 1, a.contar(`SELECT COUNT(*) FROM mcp_tokens WHERE id = $1 AND revoked_at IS NOT NULL`, tk.ID), "o refresh morre")
	require.Zero(t, a.contar(`SELECT COUNT(*) FROM mcp_tokens WHERE tipo = 'oauth_access'`), "nada emitido")
}

func TestToken_CodigoDeOutroClienteQueima(t *testing.T) {
	a := novoAmbiente(t)
	clientID, code := a.conectar()
	_, reg2 := a.registrar(map[string]any{"client_name": "Outro", "redirect_uris": []string{redirectClaude}})
	st, e := a.token(formCodigo(reg2["client_id"].(string), code, verifierRFC))
	require.Equal(t, http.StatusBadRequest, st)
	require.Equal(t, "invalid_grant", e["error"])
	st, _ = a.token(formCodigo(clientID, code, verifierRFC))
	require.Equal(t, http.StatusBadRequest, st, "queimado pela tentativa cruzada")
}

func TestToken_RefreshDeOutroClienteContinuaDoDono(t *testing.T) {
	a := novoAmbiente(t)
	clientID, code := a.conectar()
	_, tok := a.token(formCodigo(clientID, code, verifierRFC))
	refresh := tok["refresh_token"].(string)
	_, reg2 := a.registrar(map[string]any{"client_name": "Outro", "redirect_uris": []string{redirectClaude}})
	st, e := a.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {reg2["client_id"].(string)}, "refresh_token": {refresh}})
	require.Equal(t, http.StatusBadRequest, st)
	require.Equal(t, "invalid_grant", e["error"])
	st, _ = a.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID}, "refresh_token": {refresh}})
	require.Equal(t, http.StatusOK, st, "o dono ainda consegue renovar")
}

const redirectGeminiTeste = "https://vertexaisearch.cloud.google.com/oauth-redirect"

// codigoConfidencial registra um confidencial (Basic) e devolve id, segredo e
// uma função que faz authorize+grant com params extras e devolve o código.
func (a *ambiente) codigoConfidencial() (string, string, func(url.Values) string) {
	_, reg := a.registrar(map[string]any{"client_name": "G", "token_endpoint_auth_method": "client_secret_basic", "redirect_uris": []string{redirectGeminiTeste}})
	id, sec := reg["client_id"].(string), reg["client_secret"].(string)
	return id, sec, func(extra url.Values) string {
		p := url.Values{"response_type": {"code"}, "client_id": {id}, "redirect_uri": {redirectGeminiTeste}}
		for k, v := range extra {
			p[k] = v
		}
		res := a.autorizar(p)
		require.Equal(a.t, http.StatusFound, res.StatusCode)
		loc, _ := url.Parse(res.Header.Get("Location"))
		require.Equal(a.t, "portal.teste", loc.Host, loc.String())
		_, out := a.postJSON("/v1/internal/mcp/oauth/grant", a.jwt, map[string]any{"pedido": loc.Query().Get("pedido"), "aprovar": true})
		v, _ := url.Parse(out["redirect"].(string))
		return v.Query().Get("code")
	}
}

func TestToken_ConfidencialComDesafioExigeVerifier(t *testing.T) {
	a := novoAmbiente(t)
	id, sec, codigo := a.codigoConfidencial()
	c := codigo(url.Values{"code_challenge": {challengeRFC}, "code_challenge_method": {"S256"}})
	st, e := a.token(url.Values{"grant_type": {"authorization_code"}, "code": {c}, "redirect_uri": {redirectGeminiTeste}}, id, sec)
	require.Equal(t, http.StatusBadRequest, st)
	require.Equal(t, "invalid_grant", e["error"])
}

// RFC 9700 §2.1.1: verifier sem challenge é sinal de downgrade de PKCE.
func TestToken_VerifierSemDesafioRecusado(t *testing.T) {
	a := novoAmbiente(t)
	id, sec, codigo := a.codigoConfidencial()
	c := codigo(nil)
	st, e := a.token(url.Values{"grant_type": {"authorization_code"}, "code": {c},
		"redirect_uri": {redirectGeminiTeste}, "code_verifier": {verifierRFC}}, id, sec)
	require.Equal(t, http.StatusBadRequest, st)
	require.Equal(t, "invalid_grant", e["error"])
	require.Contains(t, e["error_description"], "code_verifier sem code_challenge")
}

func TestToken_PublicoSemDesafioRecusado(t *testing.T) {
	a := novoAmbiente(t)
	_, reg := a.registrar(map[string]any{"client_name": "Claude", "redirect_uris": []string{redirectClaude}})
	clientID := reg["client_id"].(string)
	code, err := a.srv.Codigos.Criar(a.ctx, Codigo{ClientID: clientID, UserID: a.admin, RedirectURI: redirectClaude})
	require.NoError(t, err)
	st, e := a.token(url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {code}, "redirect_uri": {redirectClaude}})
	require.Equal(t, http.StatusBadRequest, st)
	require.Equal(t, "invalid_grant", e["error"])
	require.Contains(t, e["error_description"], "código sem PKCE para cliente público")
}

func TestToken_RedirectDiferenteQueimaOCodigo(t *testing.T) {
	a := novoAmbiente(t)
	clientID, code := a.conectar()
	form := formCodigo(clientID, code, verifierRFC)
	form.Set("redirect_uri", redirectClaude+"?outro=1")
	st, e := a.token(form)
	require.Equal(t, http.StatusBadRequest, st)
	require.Equal(t, "invalid_grant", e["error"])
	st, _ = a.token(formCodigo(clientID, code, verifierRFC))
	require.Equal(t, http.StatusBadRequest, st, "queimado")
}

func TestToken_CodigoVencido(t *testing.T) {
	a := novoAmbiente(t)
	clientID, code := a.conectar()
	_, err := a.pool.Exec(a.ctx, `UPDATE mcp_oauth_codes SET expires_at = NOW() - INTERVAL '1 second'`)
	require.NoError(t, err)
	st, e := a.token(formCodigo(clientID, code, verifierRFC))
	require.Equal(t, http.StatusBadRequest, st)
	require.Equal(t, "invalid_grant", e["error"])
}

func TestToken_AcessoNaoServeComoRefresh(t *testing.T) {
	a := novoAmbiente(t)
	clientID, code := a.conectar()
	_, tok := a.token(formCodigo(clientID, code, verifierRFC))
	st, e := a.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID}, "refresh_token": {tok["access_token"].(string)}})
	require.Equal(t, http.StatusBadRequest, st)
	require.Equal(t, "invalid_grant", e["error"])
}

func TestToken_BasicEClientIDDoCorpoDiferentes(t *testing.T) {
	a := novoAmbiente(t)
	id, sec, codigo := a.codigoConfidencial()
	c := codigo(nil)
	st, e := a.token(url.Values{"grant_type": {"authorization_code"}, "client_id": {"emc_outro"}, "code": {c},
		"redirect_uri": {redirectGeminiTeste}}, id, sec)
	require.Equal(t, http.StatusBadRequest, st)
	require.Equal(t, "invalid_request", e["error"])
}

// RFC 6749 §2.3: um método de autenticação por pedido.
func TestToken_BasicESegredoNoCorpoRecusado(t *testing.T) {
	a := novoAmbiente(t)
	id, sec, codigo := a.codigoConfidencial()
	c := codigo(nil)
	st, e := a.token(url.Values{"grant_type": {"authorization_code"}, "code": {c}, "client_secret": {sec},
		"redirect_uri": {redirectGeminiTeste}}, id, sec)
	require.Equal(t, http.StatusBadRequest, st)
	require.Equal(t, "invalid_request", e["error"])
}

// RFC 6749 §5.2: 401 invalid_client com Basic leva WWW-Authenticate.
func TestToken_BasicErradoTemWWWAuthenticate(t *testing.T) {
	a := novoAmbiente(t)
	id, _, _ := a.codigoConfidencial()
	res, err := http.DefaultClient.Do(a.pedidoToken(url.Values{"grant_type": {"authorization_code"}, "code": {"x"}}, id, "errado"))
	require.NoError(t, err)
	res.Body.Close()
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	require.Equal(t, `Basic realm="emonitor-mcp"`, res.Header.Get("WWW-Authenticate"))

	res, err = http.DefaultClient.Do(a.pedidoToken(url.Values{"grant_type": {"authorization_code"}, "code": {"x"}, "client_id": {id}, "client_secret": {"errado"}}))
	require.NoError(t, err)
	res.Body.Close()
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	require.Empty(t, res.Header.Get("WWW-Authenticate"), "sem Basic, sem desafio Basic")
}
