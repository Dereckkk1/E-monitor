// Package users implementa o repositório da tabela `users`.
//
// Nota de vocabulário: o role 'viewer' do banco corresponde ao "Cliente"
// na UI (vê apenas dados do próprio client_id). 'admin' e 'operator' são
// tratados como sinônimos no middleware de autorização e ambos viram
// "Administrador" na UI. Veja docs/features/user-management.md.
package users

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// User é a projeção completa de uma linha em users.
type User struct {
	ID           uuid.UUID  `json:"id"`
	Email        string     `json:"email"`
	PasswordHash string     `json:"-"`
	Role         string     `json:"role"`
	ClientID     *uuid.UUID `json:"client_id,omitempty"`
	// ClientIDs é a carteira completa (tabela user_clients). Contém sempre o
	// ClientID acima, que é o "principal". Vazio para admin/operator.
	ClientIDs []uuid.UUID `json:"client_ids"`
	Name      string      `json:"name"`
	Phone     *string     `json:"phone,omitempty"`
	IsActive  bool        `json:"is_active"`
	// ReceiveAlertEmails controla se o usuário recebe os disparos diários de
	// email (campanhas + emissoras offline). Default TRUE; só admins/operators
	// são destinatários de qualquer forma (ver ActiveInternal).
	ReceiveAlertEmails bool `json:"receive_alert_emails"`
	// ReceivePostSaleEmails é o opt-in pra receber cópia de TODO pós-venda
	// enviado, de qualquer cliente. Default FALSE (ao contrário do de alerta:
	// aquele preservou comportamento existente, este cria um novo). Quem
	// consome é postsale.Repo.InternalRecipients, que também filtra por role.
	ReceivePostSaleEmails bool       `json:"receive_post_sale_emails"`
	DeletedAt             *time.Time `json:"deleted_at,omitempty"`
	LastLoginAt           *time.Time `json:"last_login_at,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

// Repo wraps a pgxpool.Pool and exposes CRUD operations for the users table.
type Repo struct {
	pool *pgxpool.Pool
}

// NewRepo constructs a Repo backed by the given pool.
func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

// userColumns é a projeção de leitura. O array_agg correlacionado traz a
// carteira sem N+1. Exige que a tabela apareça como `users` (sem alias) na
// query — é o caso de todos os SELECTs deste arquivo.
const userColumns = `id, email, password_hash, role, client_id, name, phone,
                     is_active, receive_alert_emails, receive_post_sale_emails,
                     deleted_at, last_login_at, created_at, updated_at,
                     COALESCE((SELECT array_agg(uc.client_id ORDER BY uc.created_at, uc.client_id)
                               FROM user_clients uc WHERE uc.user_id = users.id), '{}')`

func scanUser(row pgx.Row) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.ClientID,
		&u.Name, &u.Phone, &u.IsActive, &u.ReceiveAlertEmails, &u.ReceivePostSaleEmails,
		&u.DeletedAt, &u.LastLoginAt,
		&u.CreatedAt, &u.UpdatedAt, &u.ClientIDs)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// CreateInput agrupa os campos pra inserir um usuário.
// PasswordHash já deve vir bcrypted; o repo não hashifica.
type CreateInput struct {
	Email        string
	PasswordHash string
	Role         string // 'admin', 'operator' ou 'viewer'
	ClientID     *uuid.UUID
	Name         string
	Phone        *string
	// Preferências de email. nil = deixa o default da coluna decidir (TRUE pro
	// de alerta, FALSE pro de pós-venda) — é o caso do usuário Cliente, que nem
	// vê essas opções no formulário.
	ReceiveAlertEmails    *bool
	ReceivePostSaleEmails *bool
}

// Create insere um novo usuário.
//
// Os COALESCE espelham os DEFAULT das migrations 0037 e 0061 — mexeu num, mexa
// no outro.
func (r *Repo) Create(ctx context.Context, in CreateInput) (*User, error) {
	var id uuid.UUID
	if err := r.pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, client_id, name, phone,
		                    receive_alert_emails, receive_post_sale_emails)
		 VALUES (LOWER($1), $2, $3, $4, $5, $6,
		         COALESCE($7, TRUE), COALESCE($8, FALSE))
		 RETURNING id`,
		in.Email, in.PasswordHash, in.Role, in.ClientID, in.Name, in.Phone,
		in.ReceiveAlertEmails, in.ReceivePostSaleEmails,
	).Scan(&id); err != nil {
		return nil, err
	}
	// Relê pra trazer a carteira já materializada pelo trigger da 0062 — o
	// array_agg num RETURNING roda antes do AFTER trigger e viria vazio.
	return r.Get(ctx, id)
}

// Get busca por ID (inclui deletados).
func (r *Repo) Get(ctx context.Context, id uuid.UUID) (*User, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = $1`, id)
	return scanUser(row)
}

// GetByEmail busca por email entre não-deletados.
func (r *Repo) GetByEmail(ctx context.Context, email string) (*User, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users
		 WHERE LOWER(email) = LOWER($1) AND deleted_at IS NULL`, email)
	return scanUser(row)
}

// UpdateInput permite mudar campos opcionais. Pointer == nil → não muda.
// Email é imutável (decisão da P6 do spec).
//
// Prioridade entre client_id: se ClearClient=true, ClientID é IGNORADO
// (a coluna vira NULL). Pra atribuir um cliente novo, deixe ClearClient=false
// e passe ClientID != nil.
type UpdateInput struct {
	Name        *string
	Phone       *string
	Role        *string
	ClientID    *uuid.UUID // nil = não mudar
	ClearClient bool       // true → set client_id = NULL (admin/operator)
	IsActive    *bool
	// ReceiveAlertEmails: opt-in/out dos emails diários de alerta.
	ReceiveAlertEmails *bool
	// ReceivePostSaleEmails: opt-in/out da cópia de todo pós-venda.
	ReceivePostSaleEmails *bool
}

// Update aplica as alterações de UpdateInput e retorna o usuário atualizado.
func (r *Repo) Update(ctx context.Context, id uuid.UUID, in UpdateInput) (*User, error) {
	sets := []string{"updated_at = NOW()"}
	args := []any{}
	idx := 1
	push := func(col string, val any) {
		sets = append(sets, col+" = $"+strconv.Itoa(idx))
		args = append(args, val)
		idx++
	}
	if in.Name != nil {
		push("name", *in.Name)
	}
	if in.Phone != nil {
		push("phone", *in.Phone)
	}
	if in.Role != nil {
		push("role", *in.Role)
	}
	if in.ClearClient {
		sets = append(sets, "client_id = NULL")
	} else if in.ClientID != nil {
		push("client_id", *in.ClientID)
	}
	if in.IsActive != nil {
		push("is_active", *in.IsActive)
	}
	if in.ReceiveAlertEmails != nil {
		push("receive_alert_emails", *in.ReceiveAlertEmails)
	}
	if in.ReceivePostSaleEmails != nil {
		push("receive_post_sale_emails", *in.ReceivePostSaleEmails)
	}

	args = append(args, id)
	q := `UPDATE users SET ` + strings.Join(sets, ", ") +
		` WHERE id = $` + strconv.Itoa(idx) +
		` RETURNING id`
	var updatedID uuid.UUID
	if err := r.pool.QueryRow(ctx, q, args...).Scan(&updatedID); err != nil {
		return nil, err
	}
	// Relê pelo mesmo motivo do Create: array_agg num RETURNING não veria
	// mudanças feitas por triggers AFTER UPDATE OF client_id da 0062.
	return r.Get(ctx, updatedID)
}

// SetClients redefine a carteira de clientes do usuário numa transação:
// insere os que faltam, remove os que saíram e reposiciona o cliente principal
// (users.client_id).
//
// O principal é MANTIDO se continuar na lista; senão vira o primeiro id
// recebido — regra determinística pra não embaralhar o cabeçalho da página de
// boas-vindas a cada edição.
//
// Lista vazia é rejeitada: quem não tem cliente é admin/operator, e esse
// caminho é o ClearClient do UpdateInput.
func (r *Repo) SetClients(ctx context.Context, userID uuid.UUID, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return errors.New("client list must not be empty")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var current *uuid.UUID
	if err := tx.QueryRow(ctx,
		`SELECT client_id FROM users WHERE id = $1 FOR UPDATE`, userID,
	).Scan(&current); err != nil {
		return err
	}

	primary := ids[0]
	if current != nil {
		for _, id := range ids {
			if id == *current {
				primary = *current
				break
			}
		}
	}

	// Ordem: UPDATE primeiro (o trigger da 0062 insere o principal em
	// user_clients), depois INSERT do resto, e o DELETE por último — assim o
	// DELETE nunca apaga uma linha recém-criada. primary ∈ ids, então ele
	// sobrevive ao <> ALL.
	if _, err := tx.Exec(ctx,
		`UPDATE users SET client_id = $2, updated_at = NOW() WHERE id = $1`,
		userID, primary); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO user_clients (user_id, client_id)
		 SELECT $1, unnest($2::uuid[])
		 ON CONFLICT DO NOTHING`, userID, ids); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM user_clients
		 WHERE user_id = $1 AND client_id <> ALL($2::uuid[])`,
		userID, ids); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SetPassword troca o hash de senha.
func (r *Repo) SetPassword(ctx context.Context, id uuid.UUID, newHash string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE users SET password_hash = $1, updated_at = NOW() WHERE id = $2`,
		newHash, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// SoftDelete marca deleted_at = NOW() e força is_active = false.
func (r *Repo) SoftDelete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE users SET deleted_at = NOW(), is_active = false, updated_at = NOW()
		 WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// TouchLastLogin atualiza last_login_at = NOW().
//
// Não checa RowsAffected porque é chamado apenas pelo handler de login
// imediatamente após GetByEmail bem-sucedido — a linha é garantidamente
// existente. Erros de pool ainda são propagados.
func (r *Repo) TouchLastLogin(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE users SET last_login_at = NOW() WHERE id = $1`, id)
	return err
}

// ListInput aceita filtros do endpoint GET /admin/users.
type ListInput struct {
	Role     string
	ClientID *uuid.UUID
	Status   string // "" (= active) | "active" | "inactive" | "deleted" | "all"
	Q        string
	Page     int
	PageSize int
}

// List retorna a página filtrada e o total que casa o filtro.
func (r *Repo) List(ctx context.Context, in ListInput) ([]User, int, error) {
	if in.PageSize <= 0 {
		in.PageSize = 20
	}
	if in.PageSize > 100 {
		in.PageSize = 100
	}
	if in.Page <= 0 {
		in.Page = 1
	}

	where := []string{"1=1"}
	args := []any{}
	idx := 1

	switch in.Status {
	case "", "active":
		where = append(where, "deleted_at IS NULL", "is_active = TRUE")
	case "inactive":
		where = append(where, "deleted_at IS NULL", "is_active = FALSE")
	case "deleted":
		where = append(where, "deleted_at IS NOT NULL")
	case "all":
		// sem filtro de status
	default:
		return nil, 0, errors.New("invalid status filter")
	}
	if in.Role != "" {
		where = append(where, "role = $"+strconv.Itoa(idx))
		args = append(args, in.Role)
		idx++
	}
	if in.ClientID != nil {
		// Casa por VÍNCULO (carteira), não só pelo principal: um usuário de
		// agência filtrado por um cliente secundário precisa aparecer.
		where = append(where,
			"EXISTS (SELECT 1 FROM user_clients uc WHERE uc.user_id = users.id"+
				" AND uc.client_id = $"+strconv.Itoa(idx)+")")
		args = append(args, *in.ClientID)
		idx++
	}
	if in.Q != "" {
		like := "%" + strings.ToLower(in.Q) + "%"
		where = append(where,
			"(LOWER(name) LIKE $"+strconv.Itoa(idx)+
				" OR LOWER(email) LIKE $"+strconv.Itoa(idx+1)+")")
		args = append(args, like, like)
		idx += 2
	}

	whereSQL := strings.Join(where, " AND ")

	var total int
	if err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM users WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, in.PageSize, (in.Page-1)*in.PageSize)
	q := `SELECT ` + userColumns + ` FROM users WHERE ` + whereSQL +
		` ORDER BY created_at DESC LIMIT $` + strconv.Itoa(idx) +
		` OFFSET $` + strconv.Itoa(idx+1)
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *u)
	}
	return out, total, rows.Err()
}

// ActiveInternal retorna os usuários internos ativos (role 'admin' ou
// 'operator', não deletados, is_active=true) que optaram por receber os
// emails diários de alerta (receive_alert_emails). É o público-alvo dos
// disparos — o conjunto que a UI chama de "Administrador". Sem paginação:
// o volume de internos é pequeno.
func (r *Repo) ActiveInternal(ctx context.Context) ([]User, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+userColumns+` FROM users
		 WHERE deleted_at IS NULL AND is_active = TRUE
		   AND receive_alert_emails = TRUE
		   AND role IN ('admin','operator')
		 ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}
