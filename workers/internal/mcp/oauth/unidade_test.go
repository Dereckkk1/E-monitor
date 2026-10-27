package oauth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
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

func TestNovoServidor_RedirectsPermitidos(t *testing.T) {
	nop := zap.NewNop()
	t.Setenv("MCP_REDIRECT_URIS", "")
	require.Equal(t, []string{"https://claude.ai/api/mcp/auth_callback", "https://claude.com/api/mcp/auth_callback"},
		NovoServidor(nil, nil, nil, "", "", nop).RedirectsPermitidos, "vazio = padrão")
	t.Setenv("MCP_REDIRECT_URIS", " , ")
	require.Len(t, NovoServidor(nil, nil, nil, "", "", nop).RedirectsPermitidos, 2, "só vírgulas = padrão")
	t.Setenv("MCP_REDIRECT_URIS", " https://a.test/cb , HTTPS://B.TEST/Cb?x=1 ,")
	require.Equal(t, []string{"https://a.test/cb", "https://b.test/Cb?x=1"},
		NovoServidor(nil, nil, nil, "", "", nop).RedirectsPermitidos, "scheme e host em minúsculas; path e query intactos")

	// Entrada que não é URL https válida cai fora, com aviso (falha fechada).
	core, logs := observer.New(zap.WarnLevel)
	t.Setenv("MCP_REDIRECT_URIS", "http://x.test/cb,nao-e-url,https://u@x.test/cb,https://x.test/cb#f,https://ok.test/cb")
	require.Equal(t, []string{"https://ok.test/cb"}, NovoServidor(nil, nil, nil, "", "", zap.New(core)).RedirectsPermitidos)
	require.Equal(t, 4, logs.Len())
	t.Setenv("MCP_REDIRECT_URIS", "http://x.test/cb")
	require.Empty(t, NovoServidor(nil, nil, nil, "", "", nop).RedirectsPermitidos, "tudo inválido = nada permitido, não o padrão")
}

func TestNormalizarOrigem(t *testing.T) {
	casos := map[string]string{
		"HTTPS://CLAUDE.AI/api/MCP/auth_callback?Q=A": "https://claude.ai/api/MCP/auth_callback?Q=A",
		"http://LOCALHOST:8080/Cb":                    "http://localhost:8080/Cb",
		"https://claude.ai":                           "https://claude.ai",
		"https://Claude.AI?x=Y":                       "https://claude.ai?x=Y",
		"https://claude.ai/a%2Fb":                     "https://claude.ai/a%2Fb",
		"sem-esquema":                                 "sem-esquema",
	}
	for entrada, esperado := range casos {
		require.Equal(t, esperado, normalizarOrigem(entrada), entrada)
	}
}

// Caracteres invisíveis montados por código, para o fonte não carregar bidi
// cru (Trojan Source).
var (
	rlo  = string(rune(0x202e)) // RIGHT-TO-LEFT OVERRIDE (Cf)
	zwsp = string(rune(0x200b)) // ZERO WIDTH SPACE (Cf)
)

func TestLimparNome(t *testing.T) {
	require.Equal(t, "Claude", limparNome(rlo+"Cla"+zwsp+"ude\x07"))
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
	// CF-Connecting-IP que não é um IP (lixo, lista) não vira chave: cai no RemoteAddr.
	require.Equal(t, "10.0.0.9", IPDe(req("10.0.0.9:1", map[string]string{"CF-Connecting-IP": "lixo-xxxxxxxx"})))
	require.Equal(t, "10.0.0.9", IPDe(req("10.0.0.9:1", map[string]string{"CF-Connecting-IP": " 1.2.3.4 , 5.6.7.8"})))
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
