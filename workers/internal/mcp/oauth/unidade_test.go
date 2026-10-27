package oauth

import (
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Vetor oficial do RFC 7636, apêndice B.
const (
	verifierRFC  = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	challengeRFC = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
)

func TestPKCE_VetorRFC7636(t *testing.T) {
	require.Equal(t, challengeRFC, desafioS256(verifierRFC))
	require.True(t, conferirPKCE(verifierRFC, challengeRFC))
	require.False(t, conferirPKCE(verifierRFC+"x", challengeRFC))
	require.False(t, conferirPKCE("curto", desafioS256("curto")), "verifier < 43 chars é recusado")
	require.True(t, reChallenge.MatchString(challengeRFC))
	require.False(t, reChallenge.MatchString("plain-nao"))
}

func TestRedirectValido(t *testing.T) {
	casos := map[string]bool{
		"https://claude.ai/api/mcp/auth_callback":                true,
		"https://vertexaisearch.cloud.google.com/oauth-redirect": true,
		"http://localhost:33418/callback":                        true,
		"http://127.0.0.1:9000/cb":                               true,
		"http://evil.example/cb":                                 false,
		"https://claude.ai/cb#frag":                              false,
		"javascript:alert(1)":                                    false,
		"https:///sem-host":                                      false,
		"":                                                       false,
	}
	for uri, esperado := range casos {
		require.Equal(t, esperado, redirectValido(uri), uri)
	}
}

func TestComParametros_PreservaQueryEIgnoraVazios(t *testing.T) {
	u, err := url.Parse(comParametros("https://x.test/cb?a=1", map[string]string{"code": "c", "state": ""}))
	require.NoError(t, err)
	require.Equal(t, "1", u.Query().Get("a"))
	require.Equal(t, "c", u.Query().Get("code"))
	require.False(t, u.Query().Has("state"))
}

func TestPedido_AssinaturaEVencimento(t *testing.T) {
	segredo := []byte("segredo-de-teste-do-mcp-com-mais-de-32-bytes")
	agora := time.Unix(1_800_000_000, 0)
	p := Pedido{ClientID: "emc_1", RedirectURI: "https://claude.ai/cb", State: "s", Desafio: challengeRFC, Exp: agora.Add(10 * time.Minute).Unix()}
	assinado := assinarPedido(segredo, p)

	lido, err := lerPedido(segredo, assinado, agora)
	require.NoError(t, err)
	require.Equal(t, p, *lido)

	_, err = lerPedido([]byte("outro-segredo-com-mais-de-32-bytes!!!!"), assinado, agora)
	require.Error(t, err, "outro segredo")
	_, err = lerPedido(segredo, assinado, agora.Add(11*time.Minute))
	require.Error(t, err, "vencido")
	_, err = lerPedido(segredo, "x"+assinado, agora)
	require.Error(t, err, "adulterado")
	_, err = lerPedido(segredo, "", agora)
	require.Error(t, err)
}
