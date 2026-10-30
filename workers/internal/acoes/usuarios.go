package acoes

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"

	"radiocheck/internal/users"
	"radiocheck/internal/welcome"
)

// Códigos de recusa da criação e edição de usuário. São as MESMAS strings que
// a rota /admin/users sempre devolveu no corpo do erro.
const (
	CodigoCamposObrigatorios    = "missing_fields"
	CodigoSenhaCurta            = "password_too_short"
	CodigoPapelInvalido         = "invalid_role"
	CodigoClienteObrigatorio    = "client_id_required"
	CodigoClienteProibidoAdmin  = "client_id_not_allowed_for_admin"
	CodigoEmailEmUso            = "email_taken"
	CodigoPapelClienteInconsist = "role_client_inconsistent"
	CodigoClienteInexistente    = "client_not_found"
	CodigoUsuarioNaoEncontrado  = "not_found"
	CodigoNaoReativarApagado    = "cannot_reactivate_deleted"
)

const (
	// tamanhoMinimoSenhaUsuario espelha minPasswordLen do handler (me.go).
	tamanhoMinimoSenhaUsuario = 12
	custoBcryptUsuario        = 10
)

// EmissorBoasVindas emite o convite de boas-vindas do usuário recém-criado
// (*welcome.Service, via BoasVindasDoServico). Entra por interface para os
// testes trocarem o envio de email.
type EmissorBoasVindas interface {
	// Emitir cria o convite e tenta enviar o email. criadoPor pode ser nil.
	// Um erro aqui nunca falha a criação do usuário (ver CriarUsuario).
	Emitir(ctx context.Context, u *users.User, senhaClara string, criadoPor *uuid.UUID) (*BoasVindas, error)
}

// BoasVindas é o desfecho do convite. O Link é segredo (quem tem o link vê a
// senha): a rota o devolve ao admin, como sempre; o MCP nunca o expõe.
type BoasVindas struct {
	ConviteID   *uuid.UUID
	Link        string
	EmailStatus string // sent | failed | disabled | unavailable
	EmailErro   string
}

type emissorDoServico struct{ svc *welcome.Service }

// BoasVindasDoServico adapta o welcome.Service. svc nil ou desabilitado (sem
// WELCOME_ENC_KEY) devolve email_status "unavailable", como a rota sempre fez.
func BoasVindasDoServico(svc *welcome.Service) EmissorBoasVindas {
	return emissorDoServico{svc: svc}
}

func (e emissorDoServico) Emitir(ctx context.Context, u *users.User, senha string, criadoPor *uuid.UUID) (*BoasVindas, error) {
	if e.svc == nil || !e.svc.Enabled() {
		return &BoasVindas{EmailStatus: "unavailable"}, nil
	}
	in := welcome.SendInput{
		UserID:    u.ID,
		Name:      u.Name,
		Email:     u.Email,
		Password:  senha,
		Role:      u.Role,
		CreatedBy: criadoPor,
	}
	if u.ClientID != nil {
		// Carteira inteira, não só o principal: "vinculada a A, B e C".
		in.ClientName = e.svc.Repo().WalletNames(ctx, u.ID)
		if in.ClientName == "" {
			in.ClientName = e.svc.Repo().ClientName(ctx, *u.ClientID)
		}
	}
	res, err := e.svc.Issue(ctx, in)
	if err != nil {
		return nil, err
	}
	return &BoasVindas{ConviteID: &res.InviteID, Link: res.Link, EmailStatus: res.EmailStatus, EmailErro: res.EmailError}, nil
}

// EntradaUsuario é o que a rota e a tool mandam para criar o usuário.
type EntradaUsuario struct {
	Email, Senha, Nome string
	Telefone           *string
	// Papel no vocabulário da API: "admin" | "client" (client vira viewer no banco).
	Papel string
	// ClienteID é o formato legado; ClienteIDs (a carteira) vence quando vêm os dois.
	ClienteID  *uuid.UUID
	ClienteIDs []uuid.UUID
	// EnviarBoasVindas emite o convite (nunca falha a criação).
	EnviarBoasVindas bool
	// Ator é quem criou (created_by do convite). Pode ser nil.
	Ator *uuid.UUID
	// Preferências de email; nil = default da coluna.
	RecebeAlertas, RecebePosVenda *bool
}

func papelDoBanco(api string) (string, bool) {
	switch api {
	case "admin":
		return "admin", true
	case "client":
		return "viewer", true
	}
	return "", false
}

func recusaUsuario(codigo string) *Recusa {
	return &Recusa{Codigo: codigo, Mensagem: codigo}
}

// traduzErroUsuario converte os SQLSTATEs que o /admin/users sabe explicar em
// Recusa; devolve o erro intacto caso contrário. Três escritas (INSERT, UPDATE,
// SetClients) podem estourar os mesmos códigos.
func traduzErroUsuario(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.Code {
	case "23505":
		return recusaUsuario(CodigoEmailEmUso)
	case "23514":
		return recusaUsuario(CodigoPapelClienteInconsist)
	case "23503":
		return recusaUsuario(CodigoClienteInexistente)
	}
	return err
}

func clientesDoPrincipal(p *uuid.UUID) []uuid.UUID {
	if p == nil {
		return nil
	}
	return []uuid.UUID{*p}
}

// conferirClientesDaCarteira recusa (client_not_found) se algum cliente não
// existe. Roda ANTES de qualquer escrita: o que o banco só pegaria no meio da
// criação/edição deixaria o usuário gravado e a rota respondendo erro.
func (s *Servico) conferirClientesDaCarteira(ctx context.Context, ids []uuid.UUID) error {
	ok, err := s.Usuarios.ClientsExist(ctx, ids)
	if err != nil {
		return err
	}
	if !ok {
		return recusaUsuario(CodigoClienteInexistente)
	}
	return nil
}

// CriarUsuario valida, grava o usuário (e a carteira) e, se pedido, emite o
// convite. Todo cliente da carteira é conferido ANTES de gravar (client_not_found
// = nada criado); se mesmo assim o SetClients falhar com a carteira secundária
// (corrida), o usuário JÁ está gravado e o erro volta como INTERNO, nunca como
// Recusa. O convite nunca falha a criação: o desfecho vai em BoasVindas.
func (s *Servico) CriarUsuario(ctx context.Context, in EntradaUsuario) (*users.User, *BoasVindas, error) {
	if in.Email == "" || in.Senha == "" || in.Nome == "" {
		return nil, nil, recusaUsuario(CodigoCamposObrigatorios)
	}
	if len(in.Senha) < tamanhoMinimoSenhaUsuario {
		return nil, nil, recusaUsuario(CodigoSenhaCurta)
	}
	dbRole, ok := papelDoBanco(in.Papel)
	if !ok {
		return nil, nil, recusaUsuario(CodigoPapelInvalido)
	}
	// client_ids vence, client_id é o fallback legado.
	carteira := in.ClienteIDs
	if len(carteira) == 0 && in.ClienteID != nil {
		carteira = []uuid.UUID{*in.ClienteID}
	}
	if dbRole == "viewer" && len(carteira) == 0 {
		return nil, nil, recusaUsuario(CodigoClienteObrigatorio)
	}
	if dbRole != "viewer" && len(carteira) > 0 {
		return nil, nil, recusaUsuario(CodigoClienteProibidoAdmin)
	}
	// O principal SEMPRE sai da carteira normalizada (admin: lista vazia e o
	// CHECK users_client_role_consistency exige client_id NULL).
	var principal *uuid.UUID
	if len(carteira) > 0 {
		principal = &carteira[0]
	}
	if err := s.conferirClientesDaCarteira(ctx, carteira); err != nil {
		return nil, nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Senha), custoBcryptUsuario)
	if err != nil {
		return nil, nil, err
	}
	u, err := s.Usuarios.Create(ctx, users.CreateInput{
		Email:                 in.Email,
		PasswordHash:          string(hash),
		Role:                  dbRole,
		ClientID:              principal,
		Name:                  in.Nome,
		Phone:                 in.Telefone,
		ReceiveAlertEmails:    in.RecebeAlertas,
		ReceivePostSaleEmails: in.RecebePosVenda,
	})
	if err != nil {
		return nil, nil, traduzErroUsuario(err)
	}

	// O INSERT + trigger da 0062 já deixou o principal na carteira; só os
	// secundários exigem uma segunda escrita.
	if len(carteira) > 1 {
		// Daqui em diante o usuário existe: nada de Recusa.
		if err := s.Usuarios.SetClients(ctx, u.ID, carteira); err != nil {
			return nil, nil, fmt.Errorf("gravar a carteira do usuário já criado: %w", err)
		}
		if u, err = s.Usuarios.Get(ctx, u.ID); err != nil {
			return nil, nil, err
		}
	}

	if !in.EnviarBoasVindas {
		return u, nil, nil
	}
	return u, s.emitirBoasVindas(ctx, u, in.Senha, in.Ator), nil
}

func (s *Servico) emitirBoasVindas(ctx context.Context, u *users.User, senha string, ator *uuid.UUID) *BoasVindas {
	if s.BoasVindas == nil {
		return &BoasVindas{EmailStatus: "unavailable"}
	}
	bv, err := s.BoasVindas.Emitir(ctx, u, senha, ator)
	if err != nil {
		return &BoasVindas{EmailStatus: "unavailable", EmailErro: err.Error()}
	}
	return bv
}

// EdicaoUsuario é o que a rota e a tool mandam para editar. nil = não mexe.
type EdicaoUsuario struct {
	Nome, Telefone *string
	Papel          *string // vocabulário da API: "admin" | "client"
	ClienteID      *uuid.UUID
	// ClienteIDs SUBSTITUI a carteira (vazia/nil = não mexe); vence ClienteID.
	ClienteIDs                    *[]uuid.UUID
	Ativo                         *bool
	RecebeAlertas, RecebePosVenda *bool
}

// EditarUsuario aplica a edição. O email é imutável (quem recusa é a rota, que
// vê o corpo do pedido).
func (s *Servico) EditarUsuario(ctx context.Context, id uuid.UUID, in EdicaoUsuario) (*users.User, error) {
	atual, err := s.Usuarios.Get(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, recusaUsuario(CodigoUsuarioNaoEncontrado)
		}
		return nil, err
	}
	// Reativar deletado é proibido.
	if in.Ativo != nil && *in.Ativo && atual.DeletedAt != nil {
		return nil, recusaUsuario(CodigoNaoReativarApagado)
	}

	var carteira []uuid.UUID
	if in.ClienteIDs != nil {
		carteira = *in.ClienteIDs
	}

	up := users.UpdateInput{
		Name:                  in.Nome,
		Phone:                 in.Telefone,
		IsActive:              in.Ativo,
		ReceiveAlertEmails:    in.RecebeAlertas,
		ReceivePostSaleEmails: in.RecebePosVenda,
	}
	if in.Papel != nil {
		dbRole, ok := papelDoBanco(*in.Papel)
		if !ok {
			return nil, recusaUsuario(CodigoPapelInvalido)
		}
		up.Role = &dbRole
		// Virando cliente exige client_id/client_ids OU já ter um principal.
		if dbRole == "viewer" && in.ClienteID == nil && len(carteira) == 0 && atual.ClientID == nil {
			return nil, recusaUsuario(CodigoClienteObrigatorio)
		}
		if dbRole != "viewer" {
			up.ClearClient = true
		}
	}
	if in.ClienteID != nil && len(carteira) == 0 {
		// client_id só vale se o usuário é (ou está virando) viewer; se virou
		// admin, ClearClient já está true e ignoramos. Com client_ids junto ele
		// vence e este ramo é pulado (senão o Update podaria a carteira antes
		// do SetClients reconstruí-la).
		if !up.ClearClient {
			up.ClientID = in.ClienteID
		}
	} else if len(carteira) > 0 && !up.ClearClient && atual.ClientID == nil {
		// SÓ na promoção admin → cliente: o Update precisa gravar um principal
		// na mesma transação em que grava role='viewer' (CHECK
		// users_client_role_consistency). Para quem já é cliente não tocamos
		// client_id: o Update poda a carteira e um SetClients que falhasse
		// depois a deixaria truncada.
		up.ClientID = &carteira[0]
	}

	if err := s.conferirClientesDaCarteira(ctx, append(append([]uuid.UUID{}, carteira...), clientesDoPrincipal(up.ClientID)...)); err != nil {
		return nil, err
	}

	u, err := s.Usuarios.Update(ctx, id, up)
	if err != nil {
		return nil, traduzErroUsuario(err)
	}

	// client_ids explícito: SetClients (ClearClient já esvaziou a carteira no
	// Update, e chamar devolveria o acesso que a promoção tirou).
	if len(carteira) > 0 && !up.ClearClient {
		// O Update já gravou: daqui em diante nada de Recusa.
		if err := s.Usuarios.SetClients(ctx, id, carteira); err != nil {
			return nil, fmt.Errorf("gravar a carteira depois de editar o usuário: %w", err)
		}
		if u, err = s.Usuarios.Get(ctx, id); err != nil {
			return nil, err
		}
	}
	return u, nil
}
