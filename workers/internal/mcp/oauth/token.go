package oauth

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"radiocheck/internal/mcp/tokens"
)

type respostaToken struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

// Token — RFC 6749 §3.2, form-urlencoded. authorization_code e refresh_token.
func (s *Servidor) Token(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		erroOAuth(w, http.StatusBadRequest, "invalid_request", "corpo form-urlencoded inválido")
		return
	}
	cli, ok := s.autenticarCliente(w, r)
	if !ok {
		return
	}
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		s.trocarCodigo(w, r, cli)
	case "refresh_token":
		s.renovar(w, r, cli)
	default:
		erroOAuth(w, http.StatusBadRequest, "unsupported_grant_type", "use authorization_code ou refresh_token")
	}
}

// autenticarCliente aceita client_id no corpo ou Basic; confidencial exige o
// segredo em TODA ida ao /token, conferido antes de qualquer consumo.
func (s *Servidor) autenticarCliente(w http.ResponseWriter, r *http.Request) (*Cliente, bool) {
	id, segredo := r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	if bid, bsec, ok := r.BasicAuth(); ok {
		bid, _ = url.QueryUnescape(bid)
		bsec, _ = url.QueryUnescape(bsec)
		if id != "" && id != bid {
			erroOAuth(w, http.StatusBadRequest, "invalid_request", "client_id do corpo difere do Basic")
			return nil, false
		}
		id, segredo = bid, bsec
	}
	cli, err := s.Clientes.Buscar(r.Context(), id)
	if errors.Is(err, ErrClienteDesconhecido) {
		erroOAuth(w, http.StatusUnauthorized, "invalid_client", "cliente desconhecido")
		return nil, false
	}
	if err != nil {
		s.Log.Error("mcp oauth: buscar cliente no token", zapErr(err))
		erroOAuth(w, http.StatusInternalServerError, "server_error", "erro interno")
		return nil, false
	}
	if cli.Confidencial {
		ok, err := s.Clientes.ConferirSegredo(r.Context(), id, segredo)
		if err != nil {
			s.Log.Error("mcp oauth: conferir segredo", zapErr(err))
			erroOAuth(w, http.StatusInternalServerError, "server_error", "erro interno")
			return nil, false
		}
		if !ok {
			erroOAuth(w, http.StatusUnauthorized, "invalid_client", "client_secret ausente ou errado")
			return nil, false
		}
	}
	return cli, true
}

func (s *Servidor) trocarCodigo(w http.ResponseWriter, r *http.Request, cli *Cliente) {
	ctx := r.Context()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		s.falhaInterna(w, "abrir transação", err)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	cod, err := s.Codigos.NaTransacao(tx).Consumir(ctx, r.PostForm.Get("code"))
	if errors.Is(err, ErrCodigoInvalido) {
		erroOAuth(w, http.StatusBadRequest, "invalid_grant", "código inválido, vencido ou já usado")
		return
	}
	if err != nil {
		s.falhaInterna(w, "consumir código", err)
		return
	}
	// Daqui para baixo, toda recusa QUEIMA o código (commit do consumo).
	recusar := func(descricao string) {
		_ = tx.Commit(ctx)
		erroOAuth(w, http.StatusBadRequest, "invalid_grant", descricao)
	}
	if cod.ClientID != cli.ClientID || cod.RedirectURI != r.PostForm.Get("redirect_uri") {
		recusar("o código não pertence a este cliente ou redirect_uri")
		return
	}
	if cod.Desafio != nil {
		if !conferirPKCE(r.PostForm.Get("code_verifier"), *cod.Desafio) {
			recusar("code_verifier não confere")
			return
		}
	} else if !cli.Confidencial {
		recusar("código sem PKCE para cliente público")
		return
	}
	if _, err := s.Tokens.NaTransacao(tx).DonoAdminAtivo(ctx, cod.UserID); err != nil {
		if errors.Is(err, tokens.ErrNaoEncontrado) {
			recusar("o dono já não é administrador ativo")
			return
		}
		s.falhaInterna(w, "reler dono", err)
		return
	}
	s.emitirEResponder(ctx, w, tx, cod.UserID, cli)
}

func (s *Servidor) renovar(w http.ResponseWriter, r *http.Request, cli *Cliente) {
	ctx := r.Context()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		s.falhaInterna(w, "abrir transação", err)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	st := s.Tokens.NaTransacao(tx)
	velho, err := st.ConsumirRefresh(ctx, r.PostForm.Get("refresh_token"))
	if errors.Is(err, tokens.ErrNaoEncontrado) {
		erroOAuth(w, http.StatusBadRequest, "invalid_grant", "refresh inválido, vencido ou já usado")
		return
	}
	if err != nil {
		s.falhaInterna(w, "consumir refresh", err)
		return
	}
	if velho.ClientID == nil || *velho.ClientID != cli.ClientID {
		erroOAuth(w, http.StatusBadRequest, "invalid_grant", "refresh de outro cliente")
		return // rollback: o refresh alheio continua com o dono dele
	}
	if _, err := st.DonoAdminAtivo(ctx, velho.UserID); err != nil {
		if errors.Is(err, tokens.ErrNaoEncontrado) {
			_ = tx.Commit(ctx) // rebaixado/desativado: o refresh morre
			erroOAuth(w, http.StatusBadRequest, "invalid_grant", "o dono já não é administrador ativo")
			return
		}
		s.falhaInterna(w, "reler dono", err)
		return
	}
	s.emitirEResponder(ctx, w, tx, velho.UserID, cli)
}

func (s *Servidor) emitirEResponder(ctx context.Context, w http.ResponseWriter, tx pgx.Tx, userID uuid.UUID, cli *Cliente) {
	st := s.Tokens.NaTransacao(tx)
	cid := cli.ClientID
	acesso, _, err := st.Emitir(ctx, tokens.Novo{UserID: userID, Tipo: tokens.TipoOAuthAcesso, Nome: cli.Nome, ClientID: &cid, Validade: validadeAcesso})
	if err != nil {
		s.falhaInterna(w, "emitir access", err)
		return
	}
	refresh, _, err := st.Emitir(ctx, tokens.Novo{UserID: userID, Tipo: tokens.TipoOAuthRefresh, Nome: cli.Nome, ClientID: &cid, Validade: validadeRefresh})
	if err != nil {
		s.falhaInterna(w, "emitir refresh", err)
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE mcp_oauth_clients SET first_token_at = COALESCE(first_token_at, NOW()) WHERE client_id = $1`, cid); err != nil {
		s.falhaInterna(w, "marcar primeiro token", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.falhaInterna(w, "commit", err)
		return
	}
	w.Header().Set("Pragma", "no-cache")
	escreverJSON(w, http.StatusOK, respostaToken{
		AccessToken: acesso, TokenType: "Bearer", ExpiresIn: int(validadeAcesso.Seconds()),
		RefreshToken: refresh, Scope: Escopo,
	})
}

func (s *Servidor) falhaInterna(w http.ResponseWriter, etapa string, err error) {
	s.Log.Error("mcp oauth: "+etapa, zapErr(err))
	erroOAuth(w, http.StatusInternalServerError, "server_error", "erro interno")
}
