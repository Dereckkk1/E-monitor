package postsale

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CreateDraft abre um pós-venda novo. Nasce sempre draft: o link só existe
// depois do publish.
func (r *Repo) CreateDraft(ctx context.Context, in CreateDraftInput) (*Report, error) {
	var rep Report
	err := r.pool.QueryRow(ctx,
		`INSERT INTO post_sale_reports (client_id, title, intro_message, created_by)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, client_id, title, intro_message, status, sent_at, created_at`,
		in.ClientID, strings.TrimSpace(in.Title), in.IntroMessage, in.CreatedBy,
	).Scan(&rep.ID, &rep.ClientID, &rep.Title, &rep.IntroMessage,
		&rep.Status, &rep.SentAt, &rep.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("postsale: criar draft: %w", err)
	}
	return &rep, nil
}

// UpdateContent grava o que o admin escreveu (passo 3). Só em draft: depois de
// enviado, o texto que o cliente leu não muda.
func (r *Repo) UpdateContent(ctx context.Context, id uuid.UUID, title, intro string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE post_sale_reports
		    SET title = $2, intro_message = $3
		  WHERE id = $1 AND status = 'draft'`,
		id, strings.TrimSpace(title), intro)
	if err != nil {
		return fmt.Errorf("postsale: atualizar conteúdo: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ReplaceBlocks troca o conjunto inteiro de campanhas do relatório. Substitui
// em vez de fazer merge porque o passo 2 do wizard é uma seleção completa —
// desmarcar uma campanha tem que removê-la.
func (r *Repo) ReplaceBlocks(ctx context.Context, reportID uuid.UUID, blocks []BlockRow) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`DELETE FROM post_sale_report_campaigns WHERE report_id = $1`, reportID); err != nil {
		return fmt.Errorf("postsale: limpar blocos: %w", err)
	}
	for i, b := range blocks {
		rowsJSON, err := json.Marshal(orEmptyRows(b.CheckingRows))
		if err != nil {
			return err
		}
		pos := b.Position
		if pos == 0 {
			pos = i
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO post_sale_report_campaigns
			   (report_id, campaign_id, period_from, period_to, position, checking_text, checking_rows)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			reportID, b.CampaignID, b.From, b.To, pos, b.CheckingText, rowsJSON); err != nil {
			return fmt.Errorf("postsale: gravar bloco: %w", err)
		}
	}
	return tx.Commit(ctx)
}

func orEmptyRows(rows []StationRow) []StationRow {
	if rows == nil {
		return []StationRow{}
	}
	return rows
}

// Get carrega o relatório com blocos e destinatários.
func (r *Repo) Get(ctx context.Context, id uuid.UUID) (*Report, error) {
	var rep Report
	err := r.pool.QueryRow(ctx,
		`SELECT r.id, r.client_id, c.name, r.title, r.intro_message,
		        r.status, r.sent_at, r.created_at
		   FROM post_sale_reports r
		   JOIN clients c ON c.id = r.client_id
		  WHERE r.id = $1`, id,
	).Scan(&rep.ID, &rep.ClientID, &rep.ClientName, &rep.Title, &rep.IntroMessage,
		&rep.Status, &rep.SentAt, &rep.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("postsale: get: %w", err)
	}

	blocks, err := r.blocks(ctx, id)
	if err != nil {
		return nil, err
	}
	rep.Blocks = blocks

	recs, err := r.Recipients(ctx, id)
	if err != nil {
		return nil, err
	}
	rep.Recipients = recs
	return &rep, nil
}

func (r *Repo) blocks(ctx context.Context, reportID uuid.UUID) ([]BlockRow, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT b.id, b.campaign_id, cmp.name, b.period_from, b.period_to,
		        b.position, b.checking_text, b.checking_rows, b.assets
		   FROM post_sale_report_campaigns b
		   JOIN campaigns cmp ON cmp.id = b.campaign_id
		  WHERE b.report_id = $1
		  ORDER BY b.position, cmp.name`, reportID)
	if err != nil {
		return nil, fmt.Errorf("postsale: blocos: %w", err)
	}
	defer rows.Close()

	var out []BlockRow
	for rows.Next() {
		var b BlockRow
		var rawRows, rawAssets []byte
		if err := rows.Scan(&b.ID, &b.CampaignID, &b.CampaignName, &b.From, &b.To,
			&b.Position, &b.CheckingText, &rawRows, &rawAssets); err != nil {
			return nil, err
		}
		if len(rawRows) > 0 {
			if err := json.Unmarshal(rawRows, &b.CheckingRows); err != nil {
				return nil, fmt.Errorf("postsale: checking_rows inválido no bloco %s: %w", b.ID, err)
			}
		}
		if len(rawAssets) > 0 {
			if err := json.Unmarshal(rawAssets, &b.Assets); err != nil {
				return nil, fmt.Errorf("postsale: assets inválido no bloco %s: %w", b.ID, err)
			}
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Recipients lista os destinatários de um relatório (inclusive revogados — o
// admin precisa ver que revogou).
func (r *Repo) Recipients(ctx context.Context, reportID uuid.UUID) ([]Recipient, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, email, name, token, email_status, email_error,
		        opened_at, open_count, revoked_at
		   FROM post_sale_report_recipients
		  WHERE report_id = $1
		  ORDER BY email`, reportID)
	if err != nil {
		return nil, fmt.Errorf("postsale: destinatários: %w", err)
	}
	defer rows.Close()

	var out []Recipient
	for rows.Next() {
		var rc Recipient
		if err := rows.Scan(&rc.ID, &rc.Email, &rc.Name, &rc.Token, &rc.EmailStatus,
			&rc.EmailError, &rc.OpenedAt, &rc.OpenCount, &rc.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, rc)
	}
	return out, rows.Err()
}

// List alimenta /admin/pos-venda. Traz as contagens agregadas para a linha
// "5 de 7 abriram" sem N+1.
func (r *Repo) List(ctx context.Context) ([]ListItem, error) {
	rows, err := r.pool.Query(ctx, `
SELECT r.id, r.client_id, c.name, c.logo_url, r.title, r.status, r.sent_at, r.created_at,
       (SELECT COUNT(*) FROM post_sale_report_campaigns b WHERE b.report_id = r.id)::int,
       (SELECT COUNT(*) FROM post_sale_report_recipients p WHERE p.report_id = r.id)::int,
       (SELECT COUNT(*) FROM post_sale_report_recipients p
         WHERE p.report_id = r.id AND p.opened_at IS NOT NULL)::int
  FROM post_sale_reports r
  JOIN clients c ON c.id = r.client_id
 ORDER BY COALESCE(r.sent_at, r.created_at) DESC`)
	if err != nil {
		return nil, fmt.Errorf("postsale: listar: %w", err)
	}
	defer rows.Close()

	out := []ListItem{}
	for rows.Next() {
		var it ListItem
		if err := rows.Scan(&it.ID, &it.ClientID, &it.ClientName, &it.ClientLogo,
			&it.Title, &it.Status, &it.SentAt, &it.CreatedAt,
			&it.Campaigns, &it.Recipients, &it.Opened); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// SetAssets grava as chaves S3 de um bloco.
func (r *Repo) SetAssets(ctx context.Context, blockID uuid.UUID, a Assets) error {
	raw, err := json.Marshal(a)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx,
		`UPDATE post_sale_report_campaigns SET assets = $2 WHERE id = $1`, blockID, raw)
	return err
}

// MarkSent congela o payload e fecha o relatório. É a fronteira entre editável
// e imutável: daqui pra frente nada recalcula.
func (r *Repo) MarkSent(ctx context.Context, id uuid.UUID, payload []byte) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE post_sale_reports
		    SET status = 'sent', payload_json = $2, sent_at = COALESCE(sent_at, NOW())
		  WHERE id = $1 AND status = 'draft'`, id, payload)
	if err != nil {
		return fmt.Errorf("postsale: marcar enviado: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrAlreadySent
	}
	return nil
}

// CreateRecipients grava um destinatário por pessoa, cada um com seu token.
func (r *Repo) CreateRecipients(ctx context.Context, reportID uuid.UUID, people []RecipientInput) ([]Recipient, error) {
	out := make([]Recipient, 0, len(people))
	for _, p := range people {
		var rc Recipient
		err := r.pool.QueryRow(ctx,
			`INSERT INTO post_sale_report_recipients (report_id, user_id, email, name, token)
			 VALUES ($1, $2, $3, $4, $5)
			 RETURNING id, email, name, token, email_status, email_error,
			           opened_at, open_count, revoked_at`,
			reportID, p.UserID, p.Email, p.Name, p.Token,
		).Scan(&rc.ID, &rc.Email, &rc.Name, &rc.Token, &rc.EmailStatus, &rc.EmailError,
			&rc.OpenedAt, &rc.OpenCount, &rc.RevokedAt)
		if err != nil {
			return nil, fmt.Errorf("postsale: criar destinatário %s: %w", p.Email, err)
		}
		out = append(out, rc)
	}
	return out, nil
}

// MarkEmail grava o desfecho do disparo SMTP.
func (r *Repo) MarkEmail(ctx context.Context, recipientID uuid.UUID, status, errMsg string) error {
	var e *string
	if errMsg != "" {
		// Trunca: mensagem de SMTP vem longa e a coluna alimenta a UI.
		if len(errMsg) > 500 {
			errMsg = errMsg[:500]
		}
		e = &errMsg
	}
	_, err := r.pool.Exec(ctx,
		`UPDATE post_sale_report_recipients SET email_status = $2, email_error = $3 WHERE id = $1`,
		recipientID, status, e)
	return err
}

// ResolveToken é o coração do endpoint público. Só resolve token não revogado
// de relatório JÁ ENVIADO — draft nunca vaza.
func (r *Repo) ResolveToken(ctx context.Context, token string) (*ResolvedReport, error) {
	var out ResolvedReport
	err := r.pool.QueryRow(ctx,
		`SELECT rc.id, r.id, r.payload_json, rc.open_count
		   FROM post_sale_report_recipients rc
		   JOIN post_sale_reports r ON r.id = rc.report_id
		  WHERE rc.token = $1
		    AND rc.revoked_at IS NULL
		    AND r.status = 'sent'
		    AND r.payload_json IS NOT NULL`, token,
	).Scan(&out.RecipientID, &out.ReportID, &out.Payload, &out.OpenCount)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("postsale: resolve token: %w", err)
	}
	return &out, nil
}

// TouchOpen registra a abertura. Falha aqui nunca pode derrubar a resposta:
// telemetria não é o produto.
func (r *Repo) TouchOpen(ctx context.Context, recipientID uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE post_sale_report_recipients
		    SET opened_at = COALESCE(opened_at, NOW()),
		        open_count = open_count + 1
		  WHERE id = $1`, recipientID)
	return err
}

// RevokeRecipient mata um link. Idempotente. Depois disso o token responde 404
// pra sempre — é o único jeito de cortar um link vazado, já que ele não expira.
//
// `by` UUID zero vira NULL: revoked_by tem FK pra users e gravar o zero
// levantaria foreign_key_violation, fazendo uma ação de SEGURANÇA falhar por
// causa da auditoria. A revogação sempre vence.
func (r *Repo) RevokeRecipient(ctx context.Context, recipientID, by uuid.UUID) error {
	var actor *uuid.UUID
	if by != uuid.Nil {
		actor = &by
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE post_sale_report_recipients
		    SET revoked_at = COALESCE(revoked_at, NOW()),
		        revoked_by = COALESCE(revoked_by, $2)
		  WHERE id = $1`, recipientID, actor)
	if err != nil {
		return fmt.Errorf("postsale: revogar: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// BundleKey devolve a chave S3 do .zip de uma campanha do relatório.
func (r *Repo) BundleKey(ctx context.Context, reportID, campaignID uuid.UUID) (string, error) {
	var raw []byte
	err := r.pool.QueryRow(ctx,
		`SELECT assets FROM post_sale_report_campaigns
		  WHERE report_id = $1 AND campaign_id = $2`, reportID, campaignID).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("postsale: bundle key: %w", err)
	}
	var a Assets
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", err
		}
	}
	if a.BundleZIP == "" {
		return "", ErrNotFound
	}
	return a.BundleZIP, nil
}

// ClientBrief carrega o cabeçalho do cliente pro documento.
func (r *Repo) ClientBrief(ctx context.Context, id uuid.UUID) (*ClientBrief, error) {
	var c ClientBrief
	err := r.pool.QueryRow(ctx,
		`SELECT name, logo_url FROM clients WHERE id = $1`, id).Scan(&c.Name, &c.LogoURL)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("postsale: cliente: %w", err)
	}
	return &c, nil
}

// CampaignBrief carrega o mínimo da campanha pro bloco.
func (r *Repo) CampaignBrief(ctx context.Context, id uuid.UUID) (*CampaignMeta, error) {
	var m CampaignMeta
	err := r.pool.QueryRow(ctx,
		`SELECT id, client_id, name, status, start_date, end_date
		   FROM campaigns WHERE id = $1`, id,
	).Scan(&m.ID, &m.ClientID, &m.Name, &m.Status, &m.StartDate, &m.EndDate)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("postsale: campanha: %w", err)
	}
	return &m, nil
}

// ActiveClientUsers lista quem recebe o email: usuários do cliente, ativos e
// não excluídos. "Só ativos" é decisão de produto — mandar pós-venda pra conta
// desativada é ruído.
func (r *Repo) ActiveClientUsers(ctx context.Context, clientID uuid.UUID) ([]RecipientInput, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, email, COALESCE(name, '')
		   FROM users
		  WHERE client_id = $1 AND is_active = TRUE AND deleted_at IS NULL
		  ORDER BY email`, clientID)
	if err != nil {
		return nil, fmt.Errorf("postsale: destinatários ativos: %w", err)
	}
	defer rows.Close()

	var out []RecipientInput
	for rows.Next() {
		var in RecipientInput
		var uid uuid.UUID
		if err := rows.Scan(&uid, &in.Email, &in.Name); err != nil {
			return nil, err
		}
		id := uid
		in.UserID = &id
		out = append(out, in)
	}
	return out, rows.Err()
}

// TargetSuffix é o rótulo de público-alvo do cliente dono da campanha, no
// formato que entra no cabeçalho das colunas "no target" do CSV.
func (r *Repo) TargetSuffix(ctx context.Context, campaignID uuid.UUID) (string, error) {
	var label *string
	err := r.pool.QueryRow(ctx,
		`SELECT cl.target_label
		   FROM campaigns cmp
		   JOIN clients cl ON cl.id = cmp.client_id
		  WHERE cmp.id = $1`, campaignID).Scan(&label)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("postsale: target label: %w", err)
	}
	if label == nil || strings.TrimSpace(*label) == "" {
		return "", nil
	}
	return " (" + strings.TrimSpace(*label) + ")", nil
}
