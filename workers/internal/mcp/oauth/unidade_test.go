package oauth

import (
	"net/http"
	"net/http/httptest"
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
		"https://evil@claude.ai":                                 false, // userinfo engana quem lê a URL
		"https://evil@claude.ai/cb":                              false,
		"http://localhost.evil.com/cb":                           false,
		"http://[::1]:9000/cb":                                   true,
	}
	for uri, esperado := range casos {
		require.Equal(t, esperado, redirectValido(uri), uri)
	}
	// Homógrafos: host com qualquer byte não-ASCII é recusado, cru ou em %.
	require.False(t, redirectValido("https://сlaude.ai/cb"), "c cirílico")
	require.False(t, redirectValido("https://%D1%81laude.ai/cb"), "c cirílico em %")
	require.True(t, redirectValido("https://xn--laude-3ve.ai/cb"), "punycode é ASCII: quem barra é a lista de hosts")
}

func TestNovoServidor_HostsPermitidos(t *testing.T) {
	t.Setenv("MCP_REDIRECT_HOSTS", "")
	require.Equal(t, []string{"claude.ai", "claude.com", "vertexaisearch.cloud.google.com"},
		NovoServidor(nil, nil, nil, "", "", nil).HostsPermitidos, "vazio = padrão")
	t.Setenv("MCP_REDIRECT_HOSTS", " , ")
	require.Len(t, NovoServidor(nil, nil, nil, "", "", nil).HostsPermitidos, 3, "só vírgulas = padrão")
	t.Setenv("MCP_REDIRECT_HOSTS", "a.test, B.TEST ,")
	require.Equal(t, []string{"a.test", "b.test"}, NovoServidor(nil, nil, nil, "", "", nil).HostsPermitidos)
}

func TestLimparNome(t *testing.T) {
	require.Equal(t, "Claude", limparNome("‮Cla​ude\x07"))
	require.Equal(t, "Gemini Enterprise", limparNome("Gemini Enterprise"))
	require.Equal(t, "Ação", limparNome("Ação"))
}

func TestComParametros_PreservaQueryEIgnoraVazios(t *testing.T) {
	u, err := url.Parse(comParametros("https://x.test/cb?a=1", map[string]string{"code": "c", "state": ""}))
	require.NoError(t, err)
	require.Equal(t, "1", u.Query().Get("a"))
	require.Equal(t, "c", u.Query().Get("code"))
	require.False(t, u.Query().Has("state"))
}

// OAuth 2.1 §4.1.2: a query registrada é mantida como veio; só acrescentamos.
func TestComParametros_QueryRegistradaIntacta(t *testing.T) {
	out := comParametros("https://x.test/cb?a=b%7e1", map[string]string{"code": "C", "state": "s t"})
	require.Equal(t, "https://x.test/cb?a=b%7e1&code=C&state=s+t", out)
	require.Equal(t, "https://x.test/cb?code=C", comParametros("https://x.test/cb", map[string]string{"code": "C"}))
	require.Equal(t, "https://x.test/cb?z=1", comParametros("https://x.test/cb?z=1", map[string]string{"code": ""}))
}

func TestRedirectRegistrado(t *testing.T) {
	registrados := []string{redirectClaude, "http://localhost:33418/callback?x=1", "http://127.0.0.1/cb", "http://[::1]:9000/cb"}
	casos := map[string]bool{
		redirectClaude:                                true,
		"http://localhost:33418/callback?x=1":         true,
		"http://localhost:5555/callback?x=1":          true, // RFC 8252 §7.3: porta livre em loopback
		"http://localhost/callback?x=1":               true,
		"http://127.0.0.1:61000/cb":                   true,
		"http://[::1]:1234/cb":                        true,
		"http://localhost:5555/callback":              false, // query diferente
		"http://localhost:5555/callback?x=1#f":        false,
		"http://localhost:5555/outro?x=1":             false,
		"http://127.0.0.1:5555/callback?x=1":          false, // host diferente do registrado
		"http://evil@localhost:5555/callback?x=1":     false,
		"https://localhost:5555/callback?x=1":         false, // só http loopback ganha a porta livre
		"https://claude.ai:444/api/mcp/auth_callback": false, // https: comparação exata
		"https://claude.ai/api/mcp/auth_callback/":    false,
		"": false,
	}
	for pedido, esperado := range casos {
		require.Equal(t, esperado, redirectRegistrado(registrados, pedido), pedido)
	}
}

func TestIPDe(t *testing.T) {
	req := func(remote string, cabecalhos map[string]string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.RemoteAddr = remote
		for k, v := range cabecalhos {
			r.Header.Set(k, v)
		}
		return r
	}
	// CF-Connecting-IP vence o que veio em RemoteAddr (que o RealIP pode ter
	// trocado por um X-Real-IP forjado).
	require.Equal(t, "203.0.113.7", IPDe(req("10.0.0.9:1234", map[string]string{"CF-Connecting-IP": "203.0.113.7", "X-Real-IP": "10.0.0.9"})))
	require.Equal(t, "10.0.0.9", IPDe(req("10.0.0.9:1234", nil)), "sem Cloudflare: host de RemoteAddr")
	require.Equal(t, "10.0.0.9", IPDe(req("10.0.0.9", nil)), "RemoteAddr sem porta")
	// IPv6: a chave é o /64.
	a := IPDe(req("[2001:db8:1:2:aaaa::1]:443", nil))
	b := IPDe(req("[2001:db8:1:2:bbbb::9]:443", nil))
	c := IPDe(req("[2001:db8:1:3::1]:443", nil))
	require.Equal(t, a, b, "mesmo /64, mesma chave")
	require.NotEqual(t, a, c, "outro /64, outra chave")
	require.Equal(t, "2001:db8:1:2::/64", IPDe(req("1.1.1.1:1", map[string]string{"CF-Connecting-IP": "2001:db8:1:2::ffff"})))
	require.Equal(t, "192.0.2.1", IPDe(req("[::ffff:192.0.2.1]:80", nil)), "IPv4 mapeado continua IPv4")
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
