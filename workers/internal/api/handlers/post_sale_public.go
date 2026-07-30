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
//     revalida o token e só então presigna.
package handlers

import (
	"errors"
	"net/http"
	"time"

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

// bundleTTL é curto de propósito: a URL presignada não carrega autenticação
// nenhuma depois de emitida, então vale só o tempo do download começar.
const bundleTTL = 15 * time.Minute

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

	url, err := h.svc.ImageURL(r.Context(), token, cid, kind, bundleTTL)
	if err != nil {
		if errors.Is(err, postsale.ErrNotFound) {
			http.Error(w, "not_found", http.StatusNotFound)
			return
		}
		h.log.Error("postsale: imagem", zap.Error(err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, url, http.StatusFound)
}

// Bundle redireciona pro .zip no S3 com URL presignada de vida curta.
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
	url, err := h.svc.BundleURL(r.Context(), token, cid, bundleTTL)
	if err != nil {
		if errors.Is(err, postsale.ErrNotFound) {
			http.Error(w, "not_found", http.StatusNotFound)
			return
		}
		h.log.Error("postsale: bundle", zap.Error(err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, url, http.StatusFound)
}
