package oauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Pedido é o /authorize validado, assinado e entregue ao frontend
// (/autorizar-ia?pedido=…). Sem estado no servidor: o consentimento confia
// nele porque a assinatura é HMAC com o JWT_SECRET.
type Pedido struct {
	ClientID    string `json:"c"`
	RedirectURI string `json:"r"`
	State       string `json:"s,omitempty"`
	Desafio     string `json:"d,omitempty"`
	Exp         int64  `json:"e"`
}

var errPedidoInvalido = errors.New("mcp: pedido de autorização inválido ou vencido")

func macPedido(segredo []byte, carga string) string {
	m := hmac.New(sha256.New, segredo)
	m.Write([]byte("mcp-pedido."))
	m.Write([]byte(carga))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func assinarPedido(segredo []byte, p Pedido) string {
	b, _ := json.Marshal(p)
	carga := base64.RawURLEncoding.EncodeToString(b)
	return carga + "." + macPedido(segredo, carga)
}

func lerPedido(segredo []byte, assinado string, agora time.Time) (*Pedido, error) {
	carga, mac, ok := strings.Cut(assinado, ".")
	if !ok || !hmac.Equal([]byte(mac), []byte(macPedido(segredo, carga))) {
		return nil, errPedidoInvalido
	}
	b, err := base64.RawURLEncoding.DecodeString(carga)
	if err != nil {
		return nil, errPedidoInvalido
	}
	var p Pedido
	if err := json.Unmarshal(b, &p); err != nil || agora.Unix() > p.Exp {
		return nil, errPedidoInvalido
	}
	return &p, nil
}
