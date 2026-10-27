package mcp

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"radiocheck/internal/auth"
	"radiocheck/internal/mcp/auditoria"
	"radiocheck/internal/mcp/tokens"
)

// TokensHTTP serve a seção "Integração com IA" de /account
// (/v1/internal/mcp/tokens, sob JWT + RequireRole admin).
type TokensHTTP struct {
	Tokens *tokens.Store
	DB     tokens.DB
	Log    *zap.Logger
}

func escreverJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func erroJSON(w http.ResponseWriter, status int, msg string) {
	escreverJSON(w, status, map[string]string{"error": msg})
}

func (h *TokensHTTP) Listar(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	itens, err := h.Tokens.ListarConexoes(r.Context(), claims.UserID)
	if err != nil {
		h.Log.Error("mcp: listar conexões", zap.Error(err))
		erroJSON(w, http.StatusInternalServerError, "erro interno")
		return
	}
	escreverJSON(w, http.StatusOK, map[string]any{"items": itens})
}

type corpoCriarToken struct {
	Nome         string `json:"nome"`
	ValidadeDias int    `json:"validade_dias"`
}

func (h *TokensHTTP) Criar(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	var c corpoCriarToken
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&c); err != nil {
		erroJSON(w, http.StatusBadRequest, "corpo inválido")
		return
	}
	c.Nome = strings.TrimSpace(c.Nome)
	if c.Nome == "" || len([]rune(c.Nome)) > 80 {
		erroJSON(w, http.StatusBadRequest, "dê um nome de 1 a 80 caracteres (ex.: Claude Code no notebook)")
		return
	}
	if c.ValidadeDias == 0 {
		c.ValidadeDias = 90
	}
	if c.ValidadeDias < 1 || c.ValidadeDias > 365 {
		erroJSON(w, http.StatusBadRequest, "validade de 1 a 365 dias")
		return
	}
	// O JWT do portal carrega o role por até 8h, então o RequireRole("admin")
	// deixa passar quem foi rebaixado depois de logar. Emitir um token pessoal
	// para essa pessoa o deixaria dormente no banco e ele ressuscitaria se ela
	// voltasse a ser admin: confere o dono no banco, como o oauth/conceder.go.
	if _, err := h.Tokens.DonoAdminAtivo(r.Context(), claims.UserID); err != nil {
		if errors.Is(err, tokens.ErrNaoEncontrado) {
			erroJSON(w, http.StatusForbidden, "só administrador ativo pode criar credencial do MCP")
			return
		}
		h.Log.Error("mcp: conferir admin ativo", zap.Error(err))
		erroJSON(w, http.StatusInternalServerError, "erro interno")
		return
	}
	claro, tok, err := h.Tokens.Emitir(r.Context(), tokens.Novo{
		UserID: claims.UserID, Tipo: tokens.TipoPessoal, Nome: c.Nome,
		Validade: time.Duration(c.ValidadeDias) * 24 * time.Hour,
	})
	if err != nil {
		h.Log.Error("mcp: emitir token pessoal", zap.Error(err))
		erroJSON(w, http.StatusInternalServerError, "erro interno")
		return
	}
	if err := auditoria.Registrar(r.Context(), h.DB, claims.UserID, "mcp.token.criado", "mcp_token", &tok.ID,
		map[string]any{"nome": tok.Nome, "validade_dias": c.ValidadeDias}); err != nil {
		h.Log.Warn("mcp: auditoria token criado", zap.Error(err))
	}
	escreverJSON(w, http.StatusCreated, map[string]any{"token": claro, "item": tok})
}

func (h *TokensHTTP) Revogar(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		erroJSON(w, http.StatusNotFound, "credencial não encontrada")
		return
	}
	ok, err := h.Tokens.Revogar(r.Context(), id, claims.UserID)
	if err != nil {
		h.Log.Error("mcp: revogar", zap.Error(err))
		erroJSON(w, http.StatusInternalServerError, "erro interno")
		return
	}
	if !ok {
		erroJSON(w, http.StatusNotFound, "credencial não encontrada")
		return
	}
	if err := auditoria.Registrar(r.Context(), h.DB, claims.UserID, "mcp.token.revogado", "mcp_token", &id, nil); err != nil {
		h.Log.Warn("mcp: auditoria token revogado", zap.Error(err))
	}
	w.WriteHeader(http.StatusNoContent)
}
