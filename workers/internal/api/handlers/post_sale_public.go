// post_sale_public.go — as duas rotas que o cliente acessa pelo link do email.
//
// PÚBLICAS, sem JWT. Toda a autorização mora no token: 32 bytes aleatórios,
// único, revogável. Regras não-negociáveis:
//
//   - 404 (nunca 401) para token inválido, revogado ou de rascunho. Um 401
//     faria o interceptor do axios (frontend/src/api/client.js) limpar a
//     sessão e redirecionar pro /login — numa página que, por definição, é
//     aberta sem sessão nenhuma.
//   - Os três casos respondem IGUAL: a rota é enumerável em tese, e não pode
//     virar oráculo de "este pós-venda existiu".
//   - A resposta nunca inclui chave de S3. O download passa pelo Bundle, que
//     revalida o token e só então abre o objeto.
//   - Os BYTES passam pela API (proxy), nunca por redirect pra URL presignada:
//     em prod o host da presigned é o `S3_PUBLIC_ENDPOINT` = localhost:9000,
//     que o navegador do cliente não alcança. Ver a nota em postsale.ObjectStore.
package handlers

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"radiocheck/internal/postsale"
)

type PostSalePublicHandler struct {
	svc *postsale.Service
	log *zap.Logger
}

func NewPostSalePublicHandler(svc *postsale.Service, log *zap.Logger) *PostSalePublicHandler {
	if log == nil {
		log = zap.NewNop()
	}
	return &PostSalePublicHandler{svc: svc, log: log}
}

// Resolve devolve o payload congelado e registra a abertura.
//
// GET /v1/internal/public/post-sale/{token}
func (h *PostSalePublicHandler) Resolve(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if token == "" {
		http.Error(w, "not_found", http.StatusNotFound)
		return
	}
	payload, err := h.svc.Resolve(r.Context(), token)
	if err != nil {
		if errors.Is(err, postsale.ErrNotFound) {
			http.Error(w, "not_found", http.StatusNotFound)
			return
		}
		h.log.Error("postsale: resolve público", zap.Error(err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	// Já é o JSON congelado: escrever direto evita um round-trip de
	// unmarshal/marshal que poderia reordenar campos do documento.
	_, _ = w.Write(payload)
}

// Image serve as imagens que o DOCUMENTO mostra (hoje, o mapa das emissoras).
// Vive fora do zip porque a página precisa exibir, não fazer o cliente baixar.
//
// GET /v1/internal/public/post-sale/{token}/campaigns/{cid}/image/{kind}.png
func (h *PostSalePublicHandler) Image(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	cid, err := uuid.Parse(chi.URLParam(r, "cid"))
	if err != nil || token == "" {
		http.Error(w, "not_found", http.StatusNotFound)
		return
	}
	// Whitelist explícita: o path NUNCA vira nome de arquivo no bucket.
	var kind postsale.AssetKind
	switch chi.URLParam(r, "kind") {
	case "map":
		kind = postsale.AssetMap
	case "insights":
		kind = postsale.AssetInsights
	default:
		http.Error(w, "not_found", http.StatusNotFound)
		return
	}

	asset, err := h.svc.OpenImage(r.Context(), token, cid, kind)
	if err != nil {
		h.failAsset(w, err, "imagem")
		return
	}
	defer asset.Body.Close()
	w.Header().Set("Cache-Control", "private, max-age=300")
	h.stream(w, asset, "inline", "")
}

// Bundle serve o .zip dos relatórios da campanha.
//
// GET /v1/internal/public/post-sale/{token}/campaigns/{cid}/bundle.zip
func (h *PostSalePublicHandler) Bundle(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	cid, err := uuid.Parse(chi.URLParam(r, "cid"))
	if err != nil || token == "" {
		// Mesmo 404 do token inválido: id malformado não merece resposta
		// diferente numa rota pública.
		http.Error(w, "not_found", http.StatusNotFound)
		return
	}
	asset, err := h.svc.OpenBundle(r.Context(), token, cid)
	if err != nil {
		h.failAsset(w, err, "bundle")
		return
	}
	defer asset.Body.Close()
	h.stream(w, asset, "attachment", "relatorios.zip")
}

// failAsset mantém a regra da rota pública: token inválido, revogado, rascunho
// e artefato ausente respondem o MESMO 404.
func (h *PostSalePublicHandler) failAsset(w http.ResponseWriter, err error, op string) {
	if errors.Is(err, postsale.ErrNotFound) {
		http.Error(w, "not_found", http.StatusNotFound)
		return
	}
	h.log.Error("postsale: "+op, zap.Error(err))
	http.Error(w, "internal error", http.StatusInternalServerError)
}

// stream repassa os bytes do storage direto pro cliente, sem io.ReadAll: o
// mesmo processo roda os ffmpeg, e bufferizar o objeto inteiro no heap por
// download é custo que não precisa existir.
func (h *PostSalePublicHandler) stream(w http.ResponseWriter, a *postsale.Asset, disposition, filename string) {
	w.Header().Set("Content-Type", a.ContentType)
	cd := disposition
	if filename != "" {
		cd += `; filename="` + filename + `"`
	}
	w.Header().Set("Content-Disposition", cd)
	if a.Size > 0 {
		// Content-Length explícito pro navegador mostrar progresso do download.
		w.Header().Set("Content-Length", strconv.FormatInt(a.Size, 10))
	}
	if _, err := io.Copy(w, a.Body); err != nil {
		return // cliente desconectou no meio; o header já foi
	}
}
