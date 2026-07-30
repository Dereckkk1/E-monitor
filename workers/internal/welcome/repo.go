package welcome

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound cobre token inexistente, revogado ou de usuário já excluído.
// Um único erro pros três casos é deliberado: o endpoint é público e não deve
// virar oráculo de "este token existiu um dia".
var ErrNotFound = errors.New("welcome: convite não encontrado")

// Invite é a linha de user_welcome_invites sem o blob cifrado.
type Invite struct {
	ID          uuid.UUID  `json:"id"`
	UserID      uuid.UUID  `json:"user_id"`
	Token       string     `json:"token"`
	EmailStatus string     `json:"email_status"`
	EmailError  *string    `json:"email_error,omitempty"`
	OpenedAt    *time.Time `json:"opened_at,omitempty"`
	OpenCount   int        `json:"open_count"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// Resolved é o que o endpoint público devolve: os dados que a página de
// boas-vindas precisa renderizar. A senha aqui já vem decifrada.
type Resolved struct {
	Name          string `json:"name"`
	Email         string `json:"email"`
	Password      string `json:"password"`
	Role          string `json:"role"` // "admin" | "client"
	ClientName    string `json:"client_name,omitempty"`
	ClientLogoURL string `json:"client_logo_url,omitempty"`
}

// Repo é o acesso a user_welcome_invites.
type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

// CreateInput agrupa os campos de um convite novo. PasswordEnc já deve vir
// cifrado — o repo não conhece a chave.
type CreateInput struct {
	UserID      uuid.UUID
	Token       string
	PasswordEnc []byte
	CreatedBy   *uuid.UUID
}

// Create insere o convite com email_status='pending'. O status definitivo é
// gravado depois pelo MarkEmail, quando o SMTP responde.
func (r *Repo) Create(ctx context.Context, in CreateInput) (*Invite, error) {
	row := r.pool.QueryRow(ctx,
		`INSERT INTO user_welcome_invites (user_id, token, initial_password_enc, created_by)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, user_id, token, email_status, email_error,
		           opened_at, open_count, revoked_at, created_at`,
		in.UserID, in.Token, in.PasswordEnc, in.CreatedBy)
	return scanInvite(row)
}

// MarkEmail grava o desfecho do disparo SMTP. errMsg é ignorado quando vazio.
func (r *Repo) MarkEmail(ctx context.Context, id uuid.UUID, status, errMsg string) error {
	var e *string
	if errMsg != "" {
		// Trunca: mensagem de SMTP pode vir longa e a coluna alimenta a UI.
		if len(errMsg) > 500 {
			errMsg = errMsg[:500]
		}
		e = &errMsg
	}
	_, err := r.pool.Exec(ctx,
		`UPDATE user_welcome_invites SET email_status = $2, email_error = $3 WHERE id = $1`,
		id, status, e)
	return err
}

// Latest devolve o convite mais recente de cada usuário da lista, indexado por
// user_id. Alimenta a coluna "boas-vindas" do /admin/users sem N+1.
func (r *Repo) Latest(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]Invite, error) {
	out := make(map[uuid.UUID]Invite, len(userIDs))
	if len(userIDs) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT DISTINCT ON (user_id)
		        id, user_id, token, email_status, email_error,
		        opened_at, open_count, revoked_at, created_at
		   FROM user_welcome_invites
		  WHERE user_id = ANY($1)
		  ORDER BY user_id, created_at DESC`, userIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		inv, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		out[inv.UserID] = *inv
	}
	return out, rows.Err()
}

// Revoke apaga a senha cifrada e carimba revoked_at. Idempotente: revogar duas
// vezes não é erro. Depois disso o link responde 404 pra sempre — é o único
// jeito de cortar um convite, já que ele não expira por tempo.
//
// `by` é quem revogou. UUID zero vira NULL: revoked_by tem FK pra users, então
// gravar o zero levantaria foreign_key_violation e a revogação — que é uma ação
// de segurança — falharia por causa da auditoria. A revogação sempre vence.
func (r *Repo) Revoke(ctx context.Context, id uuid.UUID, by uuid.UUID) error {
	var actor *uuid.UUID
	if by != uuid.Nil {
		actor = &by
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE user_welcome_invites
		    SET revoked_at = COALESCE(revoked_at, NOW()),
		        revoked_by = COALESCE(revoked_by, $2),
		        initial_password_enc = NULL
		  WHERE id = $1`, id, actor)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ResolvedRow é a linha crua do Resolve: os dados do usuário mais o blob ainda
// cifrado. Quem decifra é o Service, que é dono da chave — o repo não a conhece.
type ResolvedRow struct {
	InviteID      uuid.UUID
	PasswordEnc   []byte
	Name          string
	Email         string
	Role          string // vocabulário do banco ('viewer' | 'admin' | 'operator')
	ClientName    string
	ClientLogoURL string
}

// Resolve carrega o convite pelo token da URL, junto com os dados do usuário e
// o nome do cliente vinculado. Recusa convite revogado, sem senha guardada, ou
// de usuário excluído — todos como ErrNotFound.
//
// Usuário DESATIVADO ainda resolve: o admin pode criar a conta desativada e só
// liberar depois; a página fala do acesso, não garante que ele está aberto.
func (r *Repo) Resolve(ctx context.Context, token string) (*ResolvedRow, error) {
	var row ResolvedRow
	var clientName, clientLogo *string
	err := r.pool.QueryRow(ctx,
		`SELECT i.id, i.initial_password_enc, u.name, u.email, u.role, c.name, c.logo_url
		   FROM user_welcome_invites i
		   JOIN users u   ON u.id = i.user_id
		   LEFT JOIN clients c ON c.id = u.client_id
		  WHERE i.token = $1
		    AND i.revoked_at IS NULL
		    AND i.initial_password_enc IS NOT NULL
		    AND u.deleted_at IS NULL`, token,
	).Scan(&row.InviteID, &row.PasswordEnc, &row.Name, &row.Email, &row.Role,
		&clientName, &clientLogo)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("welcome: resolve: %w", err)
	}
	if clientName != nil {
		row.ClientName = *clientName
	}
	if clientLogo != nil {
		row.ClientLogoURL = *clientLogo
	}
	return &row, nil
}

// TouchOpen registra a abertura. Falha aqui nunca deve derrubar a resposta:
// telemetria não é o produto. O chamador loga e segue.
func (r *Repo) TouchOpen(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE user_welcome_invites
		    SET opened_at = COALESCE(opened_at, NOW()),
		        open_count = open_count + 1
		  WHERE id = $1`, id)
	return err
}

// ClientName resolve o nome da empresa cliente pra saudação do email
// ("vinculada a Sofá & Cia"). Cliente inexistente devolve string vazia sem
// erro: o email só perde uma frase, não deixa de sair.
func (r *Repo) ClientName(ctx context.Context, id uuid.UUID) string {
	var name string
	if err := r.pool.QueryRow(ctx, `SELECT name FROM clients WHERE id = $1`, id).Scan(&name); err != nil {
		return ""
	}
	return name
}

// GetByID carrega um convite pelo id (usado pelo revoke do admin).
func (r *Repo) GetByID(ctx context.Context, id uuid.UUID) (*Invite, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT id, user_id, token, email_status, email_error,
		        opened_at, open_count, revoked_at, created_at
		   FROM user_welcome_invites WHERE id = $1`, id)
	inv, err := scanInvite(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return inv, nil
}

func scanInvite(row pgx.Row) (*Invite, error) {
	var i Invite
	err := row.Scan(&i.ID, &i.UserID, &i.Token, &i.EmailStatus, &i.EmailError,
		&i.OpenedAt, &i.OpenCount, &i.RevokedAt, &i.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &i, nil
}

// apiRole traduz o vocabulário do banco pro da API (users.go faz o mesmo):
// 'viewer' é o "Cliente" da UI; 'admin' e 'operator' são "Administrador".
func apiRole(dbRole string) string {
	if dbRole == "viewer" {
		return "client"
	}
	return "admin"
}
