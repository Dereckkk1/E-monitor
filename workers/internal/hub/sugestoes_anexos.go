package hub

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

/*
Os anexos de sugestão no hub (spec do hub §4.3): o mesmo fluxo de três passos da
tela do hub — URL assinada → PUT no GCS → confirm —, só que feito AQUI, no
servidor. O arquivo sobe "sem dono"; quem o amarra é a criação ou o comentário,
com `anexoIds`.
*/

// transferencia fala com o BUCKET, nunca com o hub: 60 s porque 10 MB numa rede
// ruim passam dos 10 s do cliente da porta. Sem headers nossos, nunca.
var transferencia = &http.Client{Timeout: 60 * time.Second}

type pedidoDeUpload struct {
	AnexoID   string `json:"anexoId"`
	UploadURL string `json:"uploadUrl"`
}

// SubirAnexoSugestao devolve o id do anexo no hub, pronto para `anexoIds`.
func (c *Client) SubirAnexoSugestao(ctx context.Context, a Autor, nome, mime string, dados []byte) (string, error) {
	var p pedidoDeUpload
	if err := c.porta(ctx, http.MethodPost, "/anexos/upload-url", a,
		map[string]any{"nome": nome, "mimeType": mime, "tamanho": len(dados)}, &p); err != nil {
		return "", err
	}
	if p.AnexoID == "" || p.UploadURL == "" {
		return "", &Error{Status: http.StatusBadGateway, Message: "resposta ilegível da Central de Clientes"}
	}
	if err := putNoBucket(ctx, p.UploadURL, mime, dados); err != nil {
		return "", err
	}
	if err := c.porta(ctx, http.MethodPost, "/anexos/"+url.PathEscape(p.AnexoID)+"/confirm", a, nil, nil); err != nil {
		return "", err
	}
	return p.AnexoID, nil
}

// putNoBucket manda os bytes para a URL assinada.
//
// ⚠️ SÓ o Content-Type, e tem de ser o MESMO que o hub assinou
// (`signedUploadUrl(objectKey, mimeType)`); outro dá 403 SignatureDoesNotMatch.
// A URL assinada já é a credencial: chave da plataforma aqui iria parar no log
// do Google.
func putNoBucket(ctx context.Context, destino, mime string, dados []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, destino, bytes.NewReader(dados))
	if err != nil {
		return &Error{Status: http.StatusInternalServerError, Message: "erro interno"}
	}
	req.Header.Set("Content-Type", mime)
	resp, err := transferencia.Do(req)
	if err != nil {
		return &Error{Status: http.StatusServiceUnavailable, Message: "não consegui enviar o arquivo, tente de novo"}
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &Error{Status: http.StatusBadGateway, Message: fmt.Sprintf("o armazenamento recusou o arquivo (%d)", resp.StatusCode)}
	}
	return nil
}

/*
AbrirAnexoSugestao devolve os BYTES do anexo. Quem chama fecha o corpo.

O hub responde 302 para uma URL assinada de 5 min. Ela NÃO vai para o navegador:
é o incidente de 2026-07-13 deste repo (URL pré-assinada com host inalcançável,
imagem quebrada). A tela continua recebendo bytes pelo proxy da API.

⚠️ O redirect é seguido À MÃO. O `http.Client` padrão copia num redirect os
headers que não considera sensíveis — `X-Hub-Platform-Key` e `X-Hub-Autor-Email`
iriam para o GCS. Por isso o primeiro pedido usa `ErrUseLastResponse` e o
segundo sai limpo, pelo cliente de transferência.
*/
func (c *Client) AbrirAnexoSugestao(ctx context.Context, a Autor, id string) (io.ReadCloser, string, error) {
	if !c.Configured() {
		return nil, "", ErrNotConfigured
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+prefixoSugestoes+"/anexos/"+url.PathEscape(id)+"/arquivo?inline=1", nil)
	if err != nil {
		return nil, "", &Error{Status: http.StatusInternalServerError, Message: "erro interno"}
	}
	c.autorNosHeaders(req, a)
	semSeguir := &http.Client{
		Timeout:       c.http.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := semSeguir.Do(req)
	if err != nil {
		return nil, "", &Error{Status: http.StatusServiceUnavailable, Message: "não consegui falar com a Central de Clientes, tente de novo"}
	}
	var destino *url.URL
	if resp.StatusCode >= 300 && resp.StatusCode <= 399 {
		destino, err = resp.Request.URL.Parse(resp.Header.Get("Location"))
	}
	var falha error
	if resp.StatusCode < 300 || resp.StatusCode > 399 {
		falha = erroDaPorta(resp)
	} else if err != nil || destino == nil || resp.Header.Get("Location") == "" {
		falha = &Error{Status: http.StatusBadGateway, Message: "resposta ilegível da Central de Clientes"}
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
	_ = resp.Body.Close()
	if falha != nil {
		return nil, "", falha
	}

	req2, err := http.NewRequestWithContext(ctx, http.MethodGet, destino.String(), nil)
	if err != nil {
		return nil, "", &Error{Status: http.StatusInternalServerError, Message: "erro interno"}
	}
	resp2, err := transferencia.Do(req2)
	if err != nil {
		return nil, "", &Error{Status: http.StatusServiceUnavailable, Message: "não consegui buscar o arquivo, tente de novo"}
	}
	if resp2.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp2.Body, maxBody))
		_ = resp2.Body.Close()
		return nil, "", &Error{Status: http.StatusBadGateway, Message: fmt.Sprintf("o armazenamento não entregou o arquivo (%d)", resp2.StatusCode)}
	}
	return resp2.Body, resp2.Header.Get("Content-Type"), nil
}
