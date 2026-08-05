package welcome

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
	// Clients é a carteira inteira (agências). client_name/client_logo_url
	// acima seguem sendo o principal, pra uma página em cache no navegador do
	// visitante não quebrar quando o backend novo sobe.
	Clients []ResolvedClient `json:"clients,omitempty"`
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
	// Clients é a carteira inteira, na ordem canônica (user_clients.created_at,
	// client_id). ClientName/ClientLogoURL acima continuam sendo o principal —
	// mantidos porque o payload público é consumido por uma página que pode
	// estar em cache no navegador do visitante.
	Clients []ResolvedClient
}

// ResolvedClient é um cliente da carteira, do jeito que a página de boas-vindas
// precisa: nome pra escrever e logo pra estampar.
type ResolvedClient struct {
	Name    string `json:"name"`
	LogoURL string `json:"logo_url,omitempty"`
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
	var walletJSON []byte
	err := r.pool.QueryRow(ctx,
		`SELECT i.id, i.initial_password_enc, u.name, u.email, u.role, c.name, c.logo_url,
		        COALESCE((
		          SELECT json_agg(json_build_object('name', wc.name, 'logo_url', wc.logo_url)
		                          ORDER BY uc.created_at, uc.client_id)
		            FROM user_clients uc
		            JOIN clients wc ON wc.id = uc.client_id
		           WHERE uc.user_id = u.id
		        ), '[]'::json)
		   FROM user_welcome_invites i
		   JOIN users u   ON u.id = i.user_id
		   LEFT JOIN clients c ON c.id = u.client_id
		  WHERE i.token = $1
		    AND i.revoked_at IS NULL
		    AND i.initial_password_enc IS NOT NULL
		    AND u.deleted_at IS NULL`, token,
	).Scan(&row.InviteID, &row.PasswordEnc, &row.Name, &row.Email, &row.Role,
		&clientName, &clientLogo, &walletJSON)
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
	// Carteira ilegível não derruba a página: ela perde os logos extras, não o
	// acesso. O visitante está aqui pra pegar a senha.
	if err := json.Unmarshal(walletJSON, &row.Clients); err != nil {
		row.Clients = nil
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

// WalletNames devolve os nomes da carteira do usuário já prontos pra frase do
// email: "Sofá & Cia", "Sofá & Cia e Milium", "Sofá & Cia, Milium e Uniube".
//
// Existe porque o email dizia "vinculada a {cliente}" resolvendo só o
// principal — com carteira de agência isso afirma um vínculo e esconde os
// outros. Falha devolve string vazia: o email perde a frase, não deixa de sair.
func (r *Repo) WalletNames(ctx context.Context, userID uuid.UUID) string {
	rows, err := r.pool.Query(ctx,
		`SELECT c.name
		   FROM user_clients uc
		   JOIN clients c ON c.id = uc.client_id
		  WHERE uc.user_id = $1
		  ORDER BY uc.created_at, uc.client_id`, userID)
	if err != nil {
		return ""
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return ""
		}
		names = append(names, n)
	}
	if rows.Err() != nil {
		return ""
	}

	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	default:
		// "A, B e C" — o "e" antes do último, que é como se lê em português.
		return strings.Join(names[:len(names)-1], ", ") + " e " + names[len(names)-1]
	}
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
