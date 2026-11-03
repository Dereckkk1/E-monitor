package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Leituras que só o MCP usa hoje (spec 2026-10-27 §6 Fatia 1). São leituras
// de coluna, sem regra de negócio: a tela não as tem porque não mostra o dado
// (ou o mostra por outro caminho).

// HubIDs devolve o hub_id (ponte com a Central de Clientes, migration 0068)
// dos clientes pedidos. Cliente sem ponte não aparece no mapa.
func (c *Clients) HubIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	out := make(map[uuid.UUID]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := c.pool.Query(ctx,
		`SELECT id, hub_id FROM clients WHERE id = ANY($1) AND hub_id IS NOT NULL`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var hub string
		if err := rows.Scan(&id, &hub); err != nil {
			return nil, err
		}
		out[id] = hub
	}
	return out, rows.Err()
}

// ListByHubCode devolve as campanhas com este código do hub, da mais nova
// para a mais velha. O código NÃO é único (dois PIs podem dividi-lo — decisão
// 5 da spec do hub 2026-09-18), por isso é lista. code tem de vir canônico
// (hub.NormalizaCodigo), como é gravado.
func (c *Campaigns) ListByHubCode(ctx context.Context, code string) ([]Campaign, error) {
	rows, err := c.pool.Query(ctx, `
		SELECT id, client_id, name, start_date, end_date, status, target_stations,
		       fixed_cpm, hub_code, hub_notified_at, created_at, updated_at
		FROM campaigns WHERE hub_code = $1
		ORDER BY start_date DESC, id`, code)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Campaign{}
	for rows.Next() {
		var camp Campaign
		if err := rows.Scan(&camp.ID, &camp.ClientID, &camp.Name, &camp.StartDate, &camp.EndDate,
			&camp.Status, &camp.TargetStations, &camp.FixedCPM, &camp.HubCode, &camp.HubNotifiedAt,
			&camp.CreatedAt, &camp.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, camp)
	}
	return out, rows.Err()
}

// CancelledAtFor devolve cancelled_at (migration 0044) das campanhas
// canceladas entre ids. Campaign não carrega a coluna; quem precisa dizer
// "déficit congelado em" pergunta aqui.
func (c *Campaigns) CancelledAtFor(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]time.Time, error) {
	out := map[uuid.UUID]time.Time{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := c.pool.Query(ctx, `
		SELECT id, cancelled_at FROM campaigns
		WHERE id = ANY($1) AND status = 'cancelada' AND cancelled_at IS NOT NULL`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var em time.Time
		if err := rows.Scan(&id, &em); err != nil {
			return nil, err
		}
		out[id] = em
	}
	return out, rows.Err()
}

// StationAnatel é o cruzamento da emissora com os planos da Anatel
// (migration 0066, docs/features/anatel-station-class-coverage.md).
// Ponteiro nil = indeterminado, NUNCA zero (AM não tem raio por classe).
type StationAnatel struct {
	Class      *string
	CoverageKm *float64 // contorno protegido (normativo)
	ReachKm    *float64 // transbordo = contorno × 1,5 (gera as cidades no alcance)
	MatchTier  *string  // exact | geo | radcom
	PlanCity   *string
	PlanState  *string
	MatchedAt  *time.Time
}

// Anatel lê o cruzamento Anatel de uma emissora. pgx.ErrNoRows se a emissora
// não existe.
func (s *Stations) Anatel(ctx context.Context, id uuid.UUID) (*StationAnatel, error) {
	var a StationAnatel
	err := s.pool.QueryRow(ctx, `
		SELECT anatel_class, anatel_coverage_km::float8, anatel_reach_km::float8,
		       anatel_match_tier, anatel_plan_city, anatel_plan_state, anatel_matched_at
		FROM stations WHERE id = $1`, id).
		Scan(&a.Class, &a.CoverageKm, &a.ReachKm, &a.MatchTier, &a.PlanCity, &a.PlanState, &a.MatchedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// CoverageCities: as cidades no alcance (transbordo) de UMA emissora, da mais
// perto para a mais longe. A mesma tabela que o /live-map lê por campanha
// (LiveMap.Coverage).
func (s *Stations) CoverageCities(ctx context.Context, id uuid.UUID) ([]CoverageCity, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT station_id, ibge_code, city, state, distance_km::float8, is_home,
		       latitude::float8, longitude::float8
		FROM station_coverage_cities WHERE station_id = $1
		ORDER BY distance_km, city`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CoverageCity{}
	for rows.Next() {
		var c CoverageCity
		if err := rows.Scan(&c.StationID, &c.IBGECode, &c.City, &c.State, &c.DistanceKm,
			&c.IsHome, &c.Latitude, &c.Longitude); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DetectionAttribution é uma projeção da tocada (detection_campaigns, F-119):
// a tocada conta para cada campanha com a própria categoria.
type DetectionAttribution struct {
	CampaignID     uuid.UUID
	CampaignName   string
	CommercialID   uuid.UUID
	CommercialName string
	Category       string
}

// Attributions lista as projeções de uma tocada, por nome de campanha.
// Get devolve só a canônica (detections.campaign_id).
//
// detectedAt é o detected_at da tocada: detection_campaigns é particionada por
// ele, e passá-lo deixa o Postgres podar as partições em vez de varrer todas.
func (d *Detections) Attributions(ctx context.Context, id uuid.UUID, detectedAt time.Time) ([]DetectionAttribution, error) {
	rows, err := d.pool.Query(ctx, `
		SELECT dc.campaign_id, COALESCE(cmp.name, ''), dc.commercial_id,
		       COALESCE(m.title, c.title, ''), dc.category
		FROM detection_campaigns dc
		LEFT JOIN campaigns cmp ON cmp.id = dc.campaign_id
		LEFT JOIN materials m   ON m.id = dc.commercial_id
		LEFT JOIN commercials c ON c.id = dc.commercial_id
		WHERE dc.detection_id = $1 AND dc.detected_at = $2
		ORDER BY cmp.name, dc.campaign_id`, id, detectedAt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DetectionAttribution{}
	for rows.Next() {
		var a DetectionAttribution
		if err := rows.Scan(&a.CampaignID, &a.CampaignName, &a.CommercialID, &a.CommercialName, &a.Category); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Approved diz se a tocada está no conjunto aprovado — o que conta em toda
// tela (ApprovedDetectionsFilter). Get devolve a tocada mesmo fora dele.
//
// detectedAt é o detected_at da tocada: detections é particionada por ele, e
// passá-lo deixa o Postgres podar as partições (como em Attributions).
func (d *Detections) Approved(ctx context.Context, id uuid.UUID, detectedAt time.Time) (bool, error) {
	var ok bool
	err := d.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM detections d WHERE d.id = $1 AND d.detected_at = $2 AND `+ApprovedDetectionsFilter+`)`,
		id, detectedAt).Scan(&ok)
	return ok, err
}

// ApprovedNear lista os instantes das veiculações APROVADAS (a mesma definição
// de ApprovedDetectionsFilter) da mesma campanha × emissora × material em torno
// de `at`, até `window` para cada lado, da mais próxima para a mais distante e
// no máximo `limit`. Serve ao aviso de possível duplicata de quem vai lançar
// uma veiculação à mão.
//
// A campanha e o material saem da PROJEÇÃO (detection_campaigns): uma tocada
// atribuída a várias campanhas conta para cada uma. O COALESCE cobre a tocada
// que ainda não tem projeção (cai na campanha e no material da linha base, como
// em affectedCells). A janela em detected_at poda as partições das duas tabelas.
func (d *Detections) ApprovedNear(ctx context.Context, campaignID, stationID, commercialID uuid.UUID,
	at time.Time, window time.Duration, limit int) ([]time.Time, error) {

	rows, err := d.pool.Query(ctx, `
		SELECT d.detected_at
		FROM detections d
		LEFT JOIN detection_campaigns dc
		       ON dc.detection_id = d.id
		      AND dc.detected_at = d.detected_at
		WHERE d.station_id = $2
		  AND d.detected_at >= $4::timestamptz - make_interval(secs => $5::float8)
		  AND d.detected_at <= $4::timestamptz + make_interval(secs => $5::float8)
		  AND COALESCE(dc.campaign_id, d.campaign_id) = $1
		  AND COALESCE(dc.commercial_id, d.commercial_id) = $3
		  AND `+ApprovedDetectionsFilter+`
		ORDER BY abs(extract(epoch FROM d.detected_at - $4::timestamptz)), d.detected_at, d.id
		LIMIT $6`,
		campaignID, stationID, commercialID, at, window.Seconds(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []time.Time{}
	for rows.Next() {
		var t time.Time
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// HasPartitionFor diz se detections tem partição que comporte o instante. A
// tabela é particionada por mês e as partições só existem de quando o banco foi
// criado até alguns meses à frente: INSERT fora disso estoura 23514 ("no
// partition of relation"). Lê os limites do catálogo do Postgres, sem escrever.
func (d *Detections) HasPartitionFor(ctx context.Context, at time.Time) (bool, error) {
	var ok bool
	err := d.pool.QueryRow(ctx, `
		SELECT EXISTS (
		    SELECT 1
		    FROM pg_inherits i
		    JOIN pg_class c ON c.oid = i.inhrelid
		    CROSS JOIN LATERAL (SELECT pg_get_expr(c.relpartbound, c.oid) AS limites) l
		    CROSS JOIN LATERAL (SELECT regexp_match(l.limites, 'FROM \(''([^'']+)''\) TO \(''([^'']+)''\)') AS m) r
		    WHERE i.inhparent = 'detections'::regclass
		      AND (l.limites = 'DEFAULT'
		           OR (r.m IS NOT NULL AND r.m[1]::timestamptz <= $1 AND $1 < r.m[2]::timestamptz))
		)`, at).Scan(&ok)
	return ok, err
}

// ClientFootprint é o que está pendurado num cliente: o que BLOQUEIA apagá-lo
// (Dependents: campanhas e materiais pelas FKs da própria tabela, usuários pela
// carteira) e o que um DELETE levaria junto (ON DELETE CASCADE: chaves de API,
// entregas e falhas de webhook, PMM no target e pós-vendas — mais a configuração
// de webhook, que mora nas colunas da própria linha do cliente). Só leitura:
// serve ao preview de quem vai apagar, que precisa dizer o que vai embora ANTES.
type ClientFootprint struct {
	Dependents DependentCounts

	APIKeys           int  // api_keys (todas, revogadas inclusive)
	APIKeysActive     int  // api_keys sem revoked_at
	WebhookDeliveries int  // webhook_deliveries
	WebhookFailures   int  // webhook_failures
	WebhookConfigured bool // clients.webhook_url preenchida
	PMMRows           int  // client_station_pmm
	PostSaleReports   int  // post_sale_reports (rascunhos e enviados)
	PostSaleSent      int  // post_sale_reports já enviados (o documento congelado)
}

// Footprint lê a ClientFootprint de um cliente. Cliente inexistente devolve tudo
// zerado (quem chama já conferiu que ele existe).
func (c *Clients) Footprint(ctx context.Context, id uuid.UUID) (ClientFootprint, error) {
	dep, err := c.CountDependents(ctx, id)
	if err != nil {
		return ClientFootprint{}, err
	}
	f := ClientFootprint{Dependents: dep}
	err = c.pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM api_keys WHERE client_id = $1),
		       (SELECT count(*) FROM api_keys WHERE client_id = $1 AND revoked_at IS NULL),
		       (SELECT count(*) FROM webhook_deliveries WHERE client_id = $1),
		       (SELECT count(*) FROM webhook_failures WHERE client_id = $1),
		       COALESCE((SELECT webhook_url <> '' FROM clients WHERE id = $1), false),
		       (SELECT count(*) FROM client_station_pmm WHERE client_id = $1),
		       (SELECT count(*) FROM post_sale_reports WHERE client_id = $1),
		       (SELECT count(*) FROM post_sale_reports WHERE client_id = $1 AND status = 'sent')`, id,
	).Scan(&f.APIKeys, &f.APIKeysActive, &f.WebhookDeliveries, &f.WebhookFailures, &f.WebhookConfigured,
		&f.PMMRows, &f.PostSaleReports, &f.PostSaleSent)
	return f, err
}

// ClientUserAccess é um usuário ATIVO (não desativado, não apagado) da carteira de
// um cliente. OtherActiveClient diz se ele também tem outro cliente ATIVO na
// carteira: o login exige pelo menos um cliente ativo (auth.Login), então é isso
// que separa quem perde o acesso quando o cliente é desativado de quem segue
// entrando pelo outro.
type ClientUserAccess struct {
	ID                uuid.UUID
	Name              string
	Email             string
	OtherActiveClient bool
}

// ActiveUsers lista os usuários ativos da carteira do cliente, por nome. Conta
// pela carteira (user_clients), como CountDependents: o principal (users.client_id)
// já está nela pelo gatilho da 0062.
func (c *Clients) ActiveUsers(ctx context.Context, id uuid.UUID) ([]ClientUserAccess, error) {
	rows, err := c.pool.Query(ctx, `
		SELECT u.id, u.name, u.email,
		       EXISTS (SELECT 1 FROM user_clients o JOIN clients oc ON oc.id = o.client_id
		                WHERE o.user_id = u.id AND o.client_id <> $1 AND oc.is_active)
		  FROM user_clients uc
		  JOIN users u ON u.id = uc.user_id
		 WHERE uc.client_id = $1 AND u.is_active AND u.deleted_at IS NULL
		 ORDER BY u.name, u.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ClientUserAccess{}
	for rows.Next() {
		var u ClientUserAccess
		if err := rows.Scan(&u.ID, &u.Name, &u.Email, &u.OtherActiveClient); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// MaterialFootprint é o histórico de um material, que impediria apagá-lo sem
// deixar rastro órfão, e as linhas que um DELETE mexeria. detections.commercial_id
// e detection_campaigns.commercial_id NÃO têm FK (a coluna é polimórfica: material
// OU comercial legado), então o banco apaga o material e deixa as tocadas apontando
// para um id que não existe mais.
type MaterialFootprint struct {
	// Histórico (EXISTS: para no primeiro achado). Sem filtro de aprovação: a
	// tocada retratada ou ignorada também é histórico.
	HasDetections  bool // detections.commercial_id
	HasProjections bool // detection_campaigns.commercial_id (a tocada base pode ser de outro material)

	// O que um DELETE mexe: ponteiros de similaridade de outros materiais (SET NULL)
	// e o cache de desambiguação de gêmeos acústicos (CASCADE).
	SimilarRefs int // materials.most_similar_material_id = este
	TwinRows    int // material_twin_discriminative (como material ou como gêmeo)
}

// Footprint lê a MaterialFootprint de um material numa consulta só.
func (m *Materials) Footprint(ctx context.Context, id uuid.UUID) (MaterialFootprint, error) {
	var f MaterialFootprint
	err := m.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM detections WHERE commercial_id = $1),
		       EXISTS(SELECT 1 FROM detection_campaigns WHERE commercial_id = $1),
		       (SELECT count(*) FROM materials WHERE most_similar_material_id = $1),
		       (SELECT count(*) FROM material_twin_discriminative WHERE material_id = $1 OR twin_id = $1)`, id,
	).Scan(&f.HasDetections, &f.HasProjections, &f.SimilarRefs, &f.TwinRows)
	return f, err
}
