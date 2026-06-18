package catalog

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Notification é um item do sininho. Por enquanto só o kind
// "campaign_failure" existe — schema é genérico pra crescimento futuro.
type Notification struct {
	Key           string     `json:"key"`
	Kind          string     `json:"kind"`
	CampaignID    uuid.UUID  `json:"campaign_id"`
	CampaignName  string     `json:"campaign_name"`
	ClientID      uuid.UUID  `json:"client_id"`
	ClientName    string     `json:"client_name"`
	ClientLogoURL string     `json:"client_logo_url"`
	OccurredOn    string     `json:"occurred_on"` // YYYY-MM-DD
	ReadAt        *time.Time `json:"read_at"`
}

type NotificationsResult struct {
	Items       []Notification `json:"items"`
	UnreadCount int            `json:"unread_count"`
}

type Notifications struct {
	pool *pgxpool.Pool
}

func NewNotifications(pool *pgxpool.Pool) *Notifications {
	return &Notifications{pool: pool}
}

// List retorna notificações dos últimos 7 dias FECHADOS, com read_at
// preenchido pra cada item que o user já leu. "Fechado" = exclui o dia
// corrente — uma campanha não pode ser considerada "falha" no meio do
// próprio dia (o dia ainda não acabou). Janela efetiva: [hoje-7d, ontem].
// Ordem: data mais recente primeiro, alfabético por campanha como
// tiebreaker. Limite hard de 50 itens — improvável estourar em 7 dias.
//
// Source: daily_play_summary.deficit > 0, agrupado por (campaign, day).
// Campanhas com status='cancelada' são excluídas. Campanhas bonificadas
// continuam aparecendo (não filtramos — operador decide).
func (n *Notifications) List(ctx context.Context, userID uuid.UUID) (*NotificationsResult, error) {
	rows, err := n.pool.Query(ctx, `
SELECT
    'campaign_failure:' || c.id::text || ':' || dps.for_date::text AS key,
    c.id, c.name,
    cl.id, COALESCE(cl.name, '—'), COALESCE(cl.logo_url, ''),
    dps.for_date,
    nr.read_at
FROM daily_play_summary dps
JOIN campaigns c ON c.id = dps.campaign_id
LEFT JOIN clients cl ON cl.id = c.client_id
LEFT JOIN notification_reads nr
    ON nr.user_id = $1
   AND nr.notification_key =
       'campaign_failure:' || c.id::text || ':' || dps.for_date::text
WHERE dps.for_date >= (CURRENT_DATE - INTERVAL '7 days')
  AND dps.for_date <  CURRENT_DATE
  AND dps.deficit > 0
  AND c.status != 'cancelada'
GROUP BY c.id, c.name, cl.id, cl.name, cl.logo_url, dps.for_date, nr.read_at
ORDER BY dps.for_date DESC, c.name ASC
LIMIT 50`, userID)
	if err != nil {
		return nil, fmt.Errorf("notifications.List query: %w", err)
	}
	defer rows.Close()

	result := &NotificationsResult{Items: []Notification{}}
	for rows.Next() {
		var item Notification
		var occurred time.Time
		var readAt *time.Time
		if err := rows.Scan(
			&item.Key, &item.CampaignID, &item.CampaignName,
			&item.ClientID, &item.ClientName, &item.ClientLogoURL,
			&occurred, &readAt,
		); err != nil {
			return nil, fmt.Errorf("notifications.List scan: %w", err)
		}
		item.Kind = "campaign_failure"
		item.OccurredOn = occurred.Format("2006-01-02")
		item.ReadAt = readAt
		if readAt == nil {
			result.UnreadCount++
		}
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("notifications.List rows: %w", err)
	}
	return result, nil
}

// MarkRead faz upsert de (user_id, key, NOW()) pra cada key. Idempotente
// graças ao ON CONFLICT DO NOTHING. Retorna quantos foram efetivamente
// inseridos (zero quando todos já estavam marcados).
func (n *Notifications) MarkRead(ctx context.Context, userID uuid.UUID, keys []string) (int, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	tag, err := n.pool.Exec(ctx, `
INSERT INTO notification_reads (user_id, notification_key)
SELECT $1, k
FROM UNNEST($2::text[]) AS k
ON CONFLICT (user_id, notification_key) DO NOTHING`, userID, keys)
	if err != nil {
		return 0, fmt.Errorf("notifications.MarkRead: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// MarkAllReadInWindow deriva os keys da janela atual (mesma da List) e
// faz upsert pra todos. Não confia em lista vinda do cliente — evita o
// case de "marquei tudo via um endpoint cego" deixando reads órfãos pro
// resto da eternidade.
func (n *Notifications) MarkAllReadInWindow(ctx context.Context, userID uuid.UUID) (int, error) {
	tag, err := n.pool.Exec(ctx, `
INSERT INTO notification_reads (user_id, notification_key)
SELECT $1, 'campaign_failure:' || c.id::text || ':' || dps.for_date::text
FROM daily_play_summary dps
JOIN campaigns c ON c.id = dps.campaign_id
WHERE dps.for_date >= (CURRENT_DATE - INTERVAL '7 days')
  AND dps.for_date <  CURRENT_DATE
  AND dps.deficit > 0
  AND c.status != 'cancelada'
GROUP BY c.id, dps.for_date
ON CONFLICT (user_id, notification_key) DO NOTHING`, userID)
	if err != nil {
		return 0, fmt.Errorf("notifications.MarkAllReadInWindow: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// HasRead reporta se o usuário já marcou aquela notification_key como lida.
// Usado pelo digest diário de falhas pra decidir se a modal já foi vista
// hoje. Chave esperada: "daily_failures_digest:YYYY-MM-DD".
func (n *Notifications) HasRead(ctx context.Context, userID uuid.UUID, key string) (bool, error) {
	var exists bool
	err := n.pool.QueryRow(ctx, `
SELECT EXISTS(
  SELECT 1 FROM notification_reads
  WHERE user_id = $1 AND notification_key = $2
)`, userID, key).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("notifications.HasRead: %w", err)
	}
	return exists, nil
}
