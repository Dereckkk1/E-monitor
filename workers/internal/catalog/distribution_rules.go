package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DistributionRule é uma regra de "X tocadas por dia do tipo Y nas emissoras Z"
// — distribuição por TIPO (migration 0019). Quem efetivamente toca é qualquer
// material com type_id = Y que esteja vinculado ao campaign via campaign_materials.
type DistributionRule struct {
	ID          uuid.UUID   `json:"id"`
	CampaignID  uuid.UUID   `json:"campaign_id"`
	TypeID      uuid.UUID   `json:"type_id"`
	StationIDs  []uuid.UUID `json:"station_ids"`
	MaterialIDs []uuid.UUID `json:"material_ids"`
	// Name é o rótulo opcional do "conjunto" (ex.: "Rede Nova Brasil"). Vazio
	// = sem nome → a UI deriva a assinatura. Migration 0045.
	Name        string    `json:"name"`
	StartDate   time.Time `json:"start_date"`
	EndDate     time.Time `json:"end_date"`
	WeekdayMask int16     `json:"weekday_mask"`
	TimeStart   string    `json:"time_start"` // HH:MM
	TimeEnd     string    `json:"time_end"`   // HH:MM
	PlaysPerDay int16     `json:"plays_per_day"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type DistributionRules struct {
	pool *pgxpool.Pool
}

func NewDistributionRules(pool *pgxpool.Pool) *DistributionRules {
	return &DistributionRules{pool: pool}
}

type CreateDistributionRuleInput struct {
	CampaignID  uuid.UUID
	TypeID      uuid.UUID
	StationIDs  []uuid.UUID
	MaterialIDs []uuid.UUID
	Name        string
	StartDate   time.Time
	EndDate     time.Time
	WeekdayMask int16
	TimeStart   string // "HH:MM"
	TimeEnd     string // "HH:MM"
	PlaysPerDay int16
}

// normalizeMaterialIDs converte um slice nil em `{}`.
//
// distribution_rules.material_ids é `uuid[] NOT NULL DEFAULT '{}'` (migration
// 0043), mas o DEFAULT só vale quando a coluna é OMITIDA do INSERT — passar um
// slice nil como bind param manda NULL explícito e viola o NOT NULL. Como o zero
// value de []uuid.UUID é nil, qualquer caller que simplesmente não preencha
// MaterialIDs (o caso "regra geral, vale pra todos os materiais do tipo")
// quebrava o INSERT em QUALQUER banco — não era falha de ambiente.
//
// `{}` é exatamente a representação canônica da regra geral: ver
// cardinality(material_ids) = 0 no recatClassifiedCTE e len(Rule.MaterialIDs) == 0
// no categorizer.
//
// station_ids NÃO recebe o mesmo tratamento de propósito: lá `{}` significaria
// "regra que não vale pra emissora nenhuma", um no-op silencioso. Melhor
// estourar o NOT NULL e o caller descobrir.
func normalizeMaterialIDs(ids []uuid.UUID) []uuid.UUID {
	if ids == nil {
		return []uuid.UUID{}
	}
	return ids
}

// ruleColumns uses to_char to normalize TIME to HH:MM string in SELECTs.
const ruleColumns = `id, campaign_id, type_id, station_ids, material_ids, name,
       start_date, end_date, weekday_mask,
       to_char(time_start, 'HH24:MI') AS time_start,
       to_char(time_end,   'HH24:MI') AS time_end,
       plays_per_day, created_at, updated_at`

func (dr *DistributionRules) Create(ctx context.Context, in CreateDistributionRuleInput) (*DistributionRule, error) {
	var r DistributionRule
	err := dr.pool.QueryRow(ctx, `
		INSERT INTO distribution_rules
		  (campaign_id, type_id, station_ids, material_ids, name, start_date, end_date,
		   weekday_mask, time_start, time_end, plays_per_day)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::time, $10::time, $11)
		RETURNING `+ruleColumns,
		in.CampaignID, in.TypeID, in.StationIDs, normalizeMaterialIDs(in.MaterialIDs), in.Name,
		in.StartDate, in.EndDate, in.WeekdayMask,
		in.TimeStart, in.TimeEnd, in.PlaysPerDay,
	).Scan(&r.ID, &r.CampaignID, &r.TypeID, &r.StationIDs, &r.MaterialIDs, &r.Name,
		&r.StartDate, &r.EndDate, &r.WeekdayMask,
		&r.TimeStart, &r.TimeEnd, &r.PlaysPerDay,
		&r.CreatedAt, &r.UpdatedAt)
	return &r, err
}

func (dr *DistributionRules) Get(ctx context.Context, id uuid.UUID) (*DistributionRule, error) {
	var r DistributionRule
	err := dr.pool.QueryRow(ctx,
		`SELECT `+ruleColumns+` FROM distribution_rules WHERE id = $1`, id,
	).Scan(&r.ID, &r.CampaignID, &r.TypeID, &r.StationIDs, &r.MaterialIDs, &r.Name,
		&r.StartDate, &r.EndDate, &r.WeekdayMask,
		&r.TimeStart, &r.TimeEnd, &r.PlaysPerDay,
		&r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (dr *DistributionRules) ListByCampaign(ctx context.Context, campaignID uuid.UUID) ([]DistributionRule, error) {
	rows, err := dr.pool.Query(ctx,
		`SELECT `+ruleColumns+` FROM distribution_rules
		 WHERE campaign_id = $1
		 ORDER BY start_date ASC, time_start ASC`, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DistributionRule
	for rows.Next() {
		var r DistributionRule
		if err := rows.Scan(&r.ID, &r.CampaignID, &r.TypeID, &r.StationIDs, &r.MaterialIDs, &r.Name,
			&r.StartDate, &r.EndDate, &r.WeekdayMask,
			&r.TimeStart, &r.TimeEnd, &r.PlaysPerDay,
			&r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListApplicable returns rules where:
//   - the material's type_id matches the rule's type_id
//   - station_id is in station_ids array
//   - date is in [start_date, end_date]
//   - weekday of date matches weekday_mask
//
// SEM CALLER desde o fechamento por célula-dia (spec 2026-08-14): o motor Go é
// categorizer.Settle, alimentado por Detections.loadRulesForCell, que carrega as
// regras da célula SEM filtrar por dia (o Settle precisa das de outros dias pro
// out_date do carve-out) — este filtro data+dia-da-semana é justamente o que ele
// não pode usar. Mantida só como leitura auxiliar.
//
// Resolves the material's tipo via JOIN materials — when the material has no
// type_id (legacy), zero rules are returned.
//
// IMPORTANT TZ contract: `date` MUST already be normalized to local date
// at midnight in America/Sao_Paulo. The SQL casts `$4::date` which uses
// the session timezone — passing a UTC time can resolve to the wrong
// weekday for events near midnight in SP TZ.
//
// Time-of-day matching (slot inclusion) is intentionally NOT done here —
// the categorizer evaluates that in Go after this method returns the
// applicable rules. Filter is date+weekday only.
func (dr *DistributionRules) ListApplicable(ctx context.Context,
	campaignID, materialID, stationID uuid.UUID, date time.Time) ([]DistributionRule, error) {

	rows, err := dr.pool.Query(ctx,
		`SELECT `+ruleColumns+` FROM distribution_rules r
		 WHERE r.campaign_id = $1
		   AND r.type_id = (SELECT type_id FROM materials WHERE id = $2)
		   AND $3 = ANY(r.station_ids)
		   AND $4::date BETWEEN r.start_date AND r.end_date
		   AND ((1 << EXTRACT(DOW FROM $4::date)::int) & r.weekday_mask) != 0`,
		campaignID, materialID, stationID, date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DistributionRule
	for rows.Next() {
		var r DistributionRule
		if err := rows.Scan(&r.ID, &r.CampaignID, &r.TypeID, &r.StationIDs, &r.MaterialIDs, &r.Name,
			&r.StartDate, &r.EndDate, &r.WeekdayMask,
			&r.TimeStart, &r.TimeEnd, &r.PlaysPerDay,
			&r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (dr *DistributionRules) Update(ctx context.Context, id uuid.UUID, in CreateDistributionRuleInput) error {
	_, err := dr.pool.Exec(ctx, `
		UPDATE distribution_rules
		SET type_id = $2, station_ids = $3, material_ids = $4, start_date = $5,
		    end_date = $6, weekday_mask = $7, time_start = $8::time,
		    time_end = $9::time, plays_per_day = $10, name = $11, updated_at = now()
		WHERE id = $1`,
		id, in.TypeID, in.StationIDs, normalizeMaterialIDs(in.MaterialIDs), in.StartDate, in.EndDate,
		in.WeekdayMask, in.TimeStart, in.TimeEnd, in.PlaysPerDay, in.Name)
	return err
}

func (dr *DistributionRules) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := dr.pool.Exec(ctx, `DELETE FROM distribution_rules WHERE id = $1`, id)
	return err
}

// RecategorizeForRule re-classifica todas as detections potencialmente
// afetadas pela criação/edição/exclusão da regra dada. O escopo é ampliado
// para o período inteiro da campanha (todas as estações, tipo da regra) para
// que o carve-out possa reclassificar detections do material fora do
// período/estação da própria regra (ex.: out_date pra semana 3 quando a regra
// específica só cobre a semana 1).
func (dr *DistributionRules) RecategorizeForRule(ctx context.Context, ruleID uuid.UUID) error {
	r, err := dr.Get(ctx, ruleID)
	if err != nil {
		return err
	}
	var cs, ce time.Time
	if err := dr.pool.QueryRow(ctx,
		`SELECT start_date, end_date FROM campaigns WHERE id = $1`, r.CampaignID,
	).Scan(&cs, &ce); err != nil {
		return err
	}
	// Escopo amplo (tipo inteiro, todas as estações, período da campanha) pra
	// pegar detections do material fora do período/estação da regra — que o
	// carve-out pode reclassificar (ex.: out_date fora da 1ª semana).
	return dr.recategorizeScope(ctx, r.CampaignID, &r.TypeID, nil, cs, ce)
}

// RecategorizeForCampaign re-classifica todas as detections de uma campanha.
// Útil ao deletar uma regra (não sabemos mais o scope dela) ou pra backfill manual.
func (dr *DistributionRules) RecategorizeForCampaign(ctx context.Context, campaignID uuid.UUID) error {
	var start, end time.Time
	err := dr.pool.QueryRow(ctx,
		`SELECT start_date, end_date FROM campaigns WHERE id = $1`, campaignID,
	).Scan(&start, &end)
	if err != nil {
		return err
	}
	return dr.recategorizeScope(ctx, campaignID, nil, nil, start, end)
}

// recatClassifiedCTE é a CTE `classified` — o espelho SQL de
// categorizer.Settle (spec 2026-08-14). Separada de recatApplySQL para o
// reconciler (projection_reconcile.go) poder CONTAR divergências (SELECT) sem
// aplicá-las. Espera uma CTE `scope(id, detected_at, campaign_id, material_id,
// type_id, station_id)` definida antes dela e devolve `classified(id,
// detected_at, campaign_id, new_category)`.
//
// DIFERENÇA ESTRUTURAL PRO MODELO ANTIGO — é o ponto todo desta CTE. Antes cada
// linha do `scope` era classificada ISOLADAMENTE. Com cota o veredito depende de
// TODAS as tocadas da célula-dia (campanha, tipo, emissora, dia local SP), então
// a primeira coisa que a CTE faz é EXPANDIR o escopo recebido pras células-dia
// completas (`cell`) e RECARREGAR todas as tocadas aprovadas delas (`plays`) —
// inclusive as que o scope não mencionou. Classificar um escopo parcial linha a
// linha produziria cota errada (uma tocada de fora do escopo que já ocupa vaga
// ficaria invisível, e a de dentro viraria in_slot indevidamente).
//
// Consequência deliberada: `classified` devolve MAIS linhas do que o `scope`
// recebeu, e o apply reescreve todas elas. É o mesmo contrato do insert-path
// (settleCellDay regrava a célula-dia inteira, não só a tocada nova).
//
// A regra, por célula-dia (idêntica ao godoc de categorizer.Settle):
//
//	passo 0: out_date — fora do período da campanha, ou (carve-out) fora do
//	               período das regras que nomeiam o material. Não consome cota.
//	1. N         — override.plays_expected, senão Σ plays_per_day das regras do dia.
//	2. "dentro da faixa" — casa ALGUMA faixa válida hoje (±900s). Sem cota por faixa.
//	3. dentro da faixa, em ordem cronológica: as N primeiras → in_slot, resto → bonus.
//	4. fora da faixa: in_slot < N → out_slot, senão → bonus.
//
// CONJUNTO APROVADO: `plays` (e `nulltype`) filtram por ApprovedDetectionsFilter,
// a MESMA definição que o motor Go usa em loadCellDayPlays. Sem isso uma tocada
// retratada/ignorada/audit_rejected/ambígua consumiria vaga na cota aqui e não
// no Go, e os dois motores divergiriam em toda célula-dia que tivesse uma.
// Efeito colateral aceito e deliberado: a linha NÃO-aprovada também sai do
// `classified`, ou seja, o recat deixa a category dela como está (o Go faz o
// mesmo — ela nem entra no Settle). Categoria de linha fora do conjunto aprovado
// não é lida por nenhuma tela; quando a linha volta pro conjunto
// (des-retratação), mutateApprovedSet refecha a célula e a regrava.
//
// IMPORTANTE: a tolerância de 900s (15 min) DEVE bater com
// categorizer.SlotToleranceSeconds. Sem ela, recategorizações disparadas
// por create/edit de rule reclassificavam como out_slot detections que o motor
// Go (no insert) tinha marcado in_slot — divergência silenciosa.
//
// PARIDADE: esta CTE e categorizer.Settle DEVEM concordar bit a bit; o teste de
// paridade (Task 5) prova isso sobre a tabela-verdade da spec.
const recatClassifiedCTE = `,
-- Expansão do escopo: cada linha do scope vira a COORDENADA da célula-dia dela.
-- type_id NULL não tem célula (nenhuma regra/override pode casar) e é tratado
-- à parte, na CTE nulltype.
cell AS (
    SELECT DISTINCT s.campaign_id, s.type_id, s.station_id,
           date_trunc('day', s.detected_at AT TIME ZONE 'America/Sao_Paulo')::date AS for_date
    FROM scope s
    WHERE s.type_id IS NOT NULL
),
-- Meta do dia (N). Override supersede as regras (D1/D7 do spec 2026-05-19),
-- inclusive com plays_expected = 0 → N = 0 → tudo excedente. A soma NÃO filtra
-- por material: N é propriedade da célula-dia, não do material (espelha
-- categorizer.Settle, que soma toda regra com ruleCoversDay).
meta AS (
    SELECT c.campaign_id, c.type_id, c.station_id, c.for_date,
           COALESCE(o.plays_expected, rq.rule_expected, 0)::int AS n
    FROM cell c
    LEFT JOIN distribution_overrides o
           ON o.campaign_id = c.campaign_id AND o.type_id = c.type_id
          AND o.station_id  = c.station_id  AND o.for_date = c.for_date
    LEFT JOIN LATERAL (
        SELECT SUM(r.plays_per_day)::int AS rule_expected
        FROM distribution_rules r
        WHERE r.campaign_id = c.campaign_id
          AND r.type_id     = c.type_id
          AND c.station_id  = ANY(r.station_ids)
          AND c.for_date BETWEEN r.start_date AND r.end_date
          AND ((1 << EXTRACT(DOW FROM c.for_date)::int) & r.weekday_mask) != 0
    ) rq ON TRUE
),
-- Recarrega o conjunto COMPLETO de tocadas aprovadas de cada célula-dia.
-- Escopa pela PROJEÇÃO (detection_campaigns) porque o fechamento é por campanha:
-- uma projeção fan-out F-119 pertence a esta campanha mesmo quando a tocada-base
-- é de outra. As duas tabelas são particionadas por detected_at, então AMBAS
-- levam o recorte do dia (poda de partição) — igual a loadCellDayPlays.
plays AS (
    SELECT dc.detection_id AS id, dc.detected_at, dc.campaign_id,
           dc.commercial_id AS material_id,
           c.type_id, c.station_id, c.for_date,
           (dc.detected_at AT TIME ZONE 'America/Sao_Paulo')::time AS tod,
           cmp.start_date AS cmp_start, cmp.end_date AS cmp_end
    FROM cell c
    JOIN campaigns cmp ON cmp.id = c.campaign_id
    JOIN detection_campaigns dc
          ON dc.campaign_id = c.campaign_id
         AND dc.detected_at >= (c.for_date::timestamp AT TIME ZONE 'America/Sao_Paulo')
         AND dc.detected_at <  ((c.for_date + 1)::timestamp AT TIME ZONE 'America/Sao_Paulo')
    JOIN detections d
          ON d.id = dc.detection_id AND d.detected_at = dc.detected_at
         AND d.detected_at >= (c.for_date::timestamp AT TIME ZONE 'America/Sao_Paulo')
         AND d.detected_at <  ((c.for_date + 1)::timestamp AT TIME ZONE 'America/Sao_Paulo')
         AND d.station_id = c.station_id
    JOIN materials m ON m.id = dc.commercial_id AND m.type_id = c.type_id
    WHERE ` + ApprovedDetectionsFilter + `
),
flagged AS (
    SELECT p.*,
        -- passo 0 — out_date: fora do período da campanha OU carve-out fora do
        -- período das regras que nomeiam o material. Vale mesmo com override (D6):
        -- célula zerada não pode mascarar material fora do período dele.
        (p.for_date NOT BETWEEN p.cmp_start AND p.cmp_end
         OR (EXISTS (
                SELECT 1 FROM distribution_rules r
                WHERE r.campaign_id = p.campaign_id AND r.type_id = p.type_id
                  AND p.station_id = ANY(r.station_ids)
                  AND cardinality(r.material_ids) > 0
                  AND p.material_id = ANY(r.material_ids))
             AND NOT EXISTS (
                SELECT 1 FROM distribution_rules r
                WHERE r.campaign_id = p.campaign_id AND r.type_id = p.type_id
                  AND p.station_id = ANY(r.station_ids)
                  AND p.material_id = ANY(r.material_ids)
                  AND p.for_date BETWEEN r.start_date AND r.end_date))
        ) AS is_out_date,
        -- passo 2 — "dentro da faixa": override manda (a faixa dele é a ÚNICA
        -- considerada); senão material carve-out usa só as regras que o nomeiam e
        -- material comum só as gerais. 900s = categorizer.SlotToleranceSeconds.
        CASE
            WHEN EXISTS (
                SELECT 1 FROM distribution_overrides o
                WHERE o.campaign_id = p.campaign_id AND o.type_id = p.type_id
                  AND o.station_id = p.station_id AND o.for_date = p.for_date)
            THEN EXISTS (
                SELECT 1 FROM distribution_overrides o
                WHERE o.campaign_id = p.campaign_id AND o.type_id = p.type_id
                  AND o.station_id = p.station_id AND o.for_date = p.for_date
                  AND EXTRACT(EPOCH FROM p.tod)
                      BETWEEN EXTRACT(EPOCH FROM o.time_start) - 900
                          AND EXTRACT(EPOCH FROM o.time_end)   + 900)
            ELSE EXISTS (
                SELECT 1 FROM distribution_rules r
                WHERE r.campaign_id = p.campaign_id AND r.type_id = p.type_id
                  AND p.station_id = ANY(r.station_ids)
                  AND (CASE
                         WHEN EXISTS (
                            SELECT 1 FROM distribution_rules r2
                            WHERE r2.campaign_id = p.campaign_id AND r2.type_id = p.type_id
                              AND p.station_id = ANY(r2.station_ids)
                              AND cardinality(r2.material_ids) > 0
                              AND p.material_id = ANY(r2.material_ids))
                         THEN cardinality(r.material_ids) > 0
                              AND p.material_id = ANY(r.material_ids)
                         ELSE cardinality(r.material_ids) = 0
                       END)
                  AND p.for_date BETWEEN r.start_date AND r.end_date
                  AND ((1 << EXTRACT(DOW FROM p.for_date)::int) & r.weekday_mask) != 0
                  AND EXTRACT(EPOCH FROM p.tod)
                      BETWEEN EXTRACT(EPOCH FROM r.time_start) - 900
                          AND EXTRACT(EPOCH FROM r.time_end)   + 900)
        END AS in_window
    FROM plays p
),
ranked AS (
    -- WHERE NOT is_out_date roda ANTES das window functions, então as out_date
    -- ficam fora das duas contagens — igual ao Settle, que as tira do conjunto
    -- antes de distribuir a cota.
    --
    -- rn_in numera dentro de cada (célula-dia, in_window). ROW_NUMBER() é window
    -- function pura e NÃO aceita FILTER — por isso in_window entra na PARTITION e
    -- o valor só é lido no ramo in_window do CASE. Já COUNT(*) aceita FILTER com
    -- OVER (é agregado usado como window function).
    --
    -- ORDER BY (detected_at, id) é a MESMA ordem do loadCellDayPlays; o desempate
    -- por id é o que faz os dois motores escolherem o mesmo vencedor num empate
    -- de segundo (pré-condição 2 de categorizer.Settle).
    SELECT f.*, m.n,
        ROW_NUMBER() OVER (
            PARTITION BY f.campaign_id, f.type_id, f.station_id, f.for_date, f.in_window
            ORDER BY f.detected_at, f.id
        ) AS rn_in,
        -- in_slot fechado da célula = LEAST(N, nº de tocadas dentro da faixa).
        -- É exatamente o inSlot do Settle ao terminar o passo 3.
        LEAST(m.n, COUNT(*) FILTER (WHERE f.in_window) OVER (
            PARTITION BY f.campaign_id, f.type_id, f.station_id, f.for_date
        )) AS in_slot_total
    FROM flagged f
    JOIN meta m ON m.campaign_id = f.campaign_id AND m.type_id = f.type_id
               AND m.station_id  = f.station_id  AND m.for_date = f.for_date
    WHERE NOT f.is_out_date
),
-- Material sem type_id (legado): nenhuma regra/override pode casar, N = 0 e
-- nenhuma faixa vale → out_date fora do período da campanha, senão bonus.
-- Espelha o ramo "typeID == nil" de settleCellDay, que fecha a tocada sozinha.
nulltype AS (
    SELECT s.id, s.detected_at, s.campaign_id,
           (CASE WHEN date_trunc('day', s.detected_at AT TIME ZONE 'America/Sao_Paulo')::date
                      NOT BETWEEN c.start_date AND c.end_date
                 THEN 'out_date' ELSE 'bonus' END)::text AS new_category
    FROM scope s
    JOIN campaigns c ON c.id = s.campaign_id
    JOIN detections d ON d.id = s.id AND d.detected_at = s.detected_at
    WHERE s.type_id IS NULL
      AND ` + ApprovedDetectionsFilter + `
),
classified AS (
    SELECT id, detected_at, campaign_id, new_category FROM nulltype
    UNION ALL
    SELECT id, detected_at, campaign_id, 'out_date'::text
    FROM flagged WHERE is_out_date
    UNION ALL
    SELECT id, detected_at, campaign_id,
        (CASE
            WHEN in_window AND rn_in <= n THEN 'in_slot'   -- passo 3
            WHEN in_window                THEN 'bonus'     -- passo 3 (excedente)
            WHEN in_slot_total < n        THEN 'out_slot'  -- passo 4 (meta aberta)
            ELSE 'bonus'                                   -- passo 4 (excedente)
        END)::text
    FROM ranked
)`

// recatSelectTailSQL fecha o pipeline de classificação como SELECT puro. É o que
// materializa o veredito na temp table do applyRecat — e o mesmo formato que o
// CountProjectionDrift consome pra CONTAR divergências sem escrever nada.
const recatSelectTailSQL = recatClassifiedCTE + `
SELECT id, detected_at, campaign_id, new_category FROM classified`

// recatApplyBaseSQL / recatApplyProjSQL aplicam o veredito já materializado em
// recat_verdict (ver applyRecat). São DOIS statements, e a ORDEM entre eles é o
// ponto: detections PRIMEIRO, detection_campaigns depois.
//
// ORDEM DE LOCK. Tem que bater com a dos outros caminhos que escrevem nas duas
// tabelas na mesma transação — Detections.rewriteCategories (fechamento da
// célula-dia no insert) e ReattributeDetection → syncCanonicalProjection. Com as
// ordens invertidas, um Create fechando a célula-dia e um recat da MESMA célula
// deadlockam; o Postgres mata um dos dois, e se a vítima for o Create a
// veiculação é PERDIDA (evidence.Service.handle só loga o erro, não tem retry).
// A versão anterior deste arquivo escrevia detection_campaigns primeiro.
//
// POR QUE DOIS STATEMENTS e não um só com CTE data-modifying (o padrão do
// rewriteCategories): naquele padrão o command tag vem do UPDATE principal, então
// pôr detections como principal — o que a ordem de lock exige — faria o
// RowsAffected passar a contar tocadas-base em vez de projeções, e
// HealProjectionDrift* devolveria 0 justamente nos casos que existem pra provar
// alcance (projeção secundária de fan-out, projeção fora do período: a base não
// muda). Um reconciler que reporta 0 curas enquanto cura é pior que um quebrado
// barulhento — o ProjectionDriftLastRun mentiria. Com dois statements a ordem é a
// do código (não depende do ExecPostprocessPlan) E cada UPDATE devolve o próprio
// tag, então a projeção volta a ser contada exatamente.
//
// O QUE ISTO **NÃO** FECHA: a ordem padronizada é a das TABELAS. Dentro do
// `UPDATE detections ... FROM recat_verdict` as linhas são varridas na ordem
// FÍSICA da temp table, que vem de um UNION ALL sem ORDER BY — enquanto o
// rewriteCategories varre um unnest ordenado por (detected_at, id). Duas
// transações que compartilhem 2+ linhas ainda podem deadlockar nos locks de
// LINHA, em ordens opostas. É pré-existente (a CTE `classified` também não
// tinha ordem) e o advisory lock cobre os caminhos estreitos, mas não assuma
// que o problema está resolvido de ponta a ponta.
//
// A guarda `d.campaign_id = v.campaign_id` no UPDATE da base é o que impede o
// recat de uma campanha SECUNDÁRIA (fan-out F-119) de sobrescrever a categoria da
// tocada-base com o veredito de outra campanha. Ver spec 2026-07-14 §3-T1.
const recatApplyBaseSQL = `
UPDATE detections d
SET category = v.new_category
FROM recat_verdict v
WHERE d.id = v.id AND d.detected_at = v.detected_at
  AND d.campaign_id = v.campaign_id
  AND d.category IS DISTINCT FROM v.new_category`

const recatApplyProjSQL = `
UPDATE detection_campaigns dc
SET category = v.new_category
FROM recat_verdict v
WHERE dc.detection_id = v.id AND dc.detected_at = v.detected_at
  AND dc.campaign_id = v.campaign_id
  AND dc.category IS DISTINCT FROM v.new_category`

// applyRecat roda o pipeline inteiro dentro da transação `tx`: materializa o
// veredito de `scopeSQL` numa temp table e aplica nas duas tabelas na ordem de
// lock. Devolve o nº de PROJEÇÕES corrigidas (a métrica que o reconciler reporta).
//
// A temp table existe por dois motivos, ambos necessários:
//  1. os dois UPDATEs precisam ver EXATAMENTE o mesmo veredito — recalcular a CTE
//     duas vezes também custaria o dobro do trabalho, que no reconciler é a janela
//     inteira;
//  2. separar classificação de escrita é o que permite os dois statements (e
//     portanto a ordem de lock explícita) sem pagar a classificação duas vezes.
//
// ON COMMIT DROP: some no commit E no rollback, então a conexão volta limpa pro
// pool. O nome é fixo de propósito — nome único por chamada geraria um SQL
// diferente a cada vez e detonaria o cache de statements do pgx.
func (dr *DistributionRules) applyRecat(ctx context.Context, tx pgx.Tx,
	scopeSQL string, args ...any) (int64, error) {

	if _, err := tx.Exec(ctx, `
		CREATE TEMP TABLE recat_verdict (
		    id           uuid        NOT NULL,
		    detected_at  timestamptz NOT NULL,
		    campaign_id  uuid        NOT NULL,
		    new_category text        NOT NULL
		) ON COMMIT DROP`); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO recat_verdict `+scopeSQL+recatSelectTailSQL, args...); err != nil {
		return 0, err
	}
	// A temp table nasce com reltuples = -1; o planner ESTIMA pelo tamanho real
	// da relação, então nem de longe erra por ordem de grandeza. O ANALYZE serve
	// pro resto: reltuples exato, largura real das colunas e — o que importa —
	// n_distinct das chaves do join contra as partições de detections.
	if _, err := tx.Exec(ctx, `ANALYZE recat_verdict`); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, recatApplyBaseSQL); err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, recatApplyProjSQL)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// runRecat é o applyRecat com transação própria, pros caminhos que não precisam
// segurar nada além dela.
func (dr *DistributionRules) runRecat(ctx context.Context, scopeSQL string, args ...any) (int64, error) {
	tx, err := dr.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	n, err := dr.applyRecat(ctx, tx, scopeSQL, args...)
	if err != nil {
		return 0, err
	}
	return n, tx.Commit(ctx)
}

// recategorizeScope é o motor SQL pra escopos rule/campaign. Escopa por
// PROJEÇÃO (detection_campaigns), não pela tocada-base: uma projeção fan-out
// F-119 pertence à campanha $1 mesmo quando a base (d.campaign_id) é outra —
// era o ponto cego do caso COPA 10/07 (spec 2026-07-14 §1.1).
//
// O escopo aqui só escolhe QUAIS células-dia serão refechadas; a CTE `cell` do
// recatClassifiedCTE expande cada linha pra célula-dia completa e recarrega o
// conjunto aprovado dela. Por isso o scope NÃO filtra por ApprovedDetectionsFilter:
// uma linha retratada continua servindo pra apontar a célula (que será fechada
// corretamente sem ela), e filtrar aqui só perderia células.
//
// ADVISORY LOCK — deliberadamente AUSENTE neste caminho. O insert-path
// (settleCellDay) toma pg_advisory_xact_lock por (campanha, emissora, dia) antes
// de ler, pra serializar dois fechamentos da mesma célula. Aqui um único
// statement pode tocar MILHARES de células-dia (RecategorizeForCampaign = período
// inteiro × todas as emissoras), e travar todas custaria um lock por célula na
// mesma transação — o lock table do Postgres é dimensionado por
// max_locks_per_transaction (64 por padrão) e estouraria com "out of shared
// memory".
//
// RISCO RESIDUAL: um recat amplo concorrente com um fechamento da mesma
// célula-dia pode gravar um veredito calculado a partir de um snapshot sem a
// tocada nova. Isso NÃO é só "in_slot a menos" — pode QUEBRAR A INVARIANTE
// in_slot <= N, e o gatilho é uma tocada que chega com timestamp ANTERIOR a
// outra já assentada (veiculação manual, lote retroativo, evidência atrasada):
//
//	N = 1; P1 20:00 dentro da faixa → in_slot.
//	P0 06:00 chega (mais CEDO) e o insert-path refecha: P0=in_slot, P1=bonus.
//	o recat velho aplica o veredito computado sem P0: P0=in_slot, P1=in_slot
//	→ in_slot = 2 com N = 1.
//
// Ou seja, o desvio é de SUPER-atribuição (entrega inflada, deficit = N − in_slot
// zerado), não só de sub-atribuição. Continua convergente — o próximo fechamento
// da célula ou a passagem do projrecon restauram — e o UPDATE de linha serializa
// a escrita, então não há perda de dado; mas quem for julgar se o risco é
// aceitável tem que saber que ele erra pro lado que fatura a mais. O caminho
// estreito — RecategorizeForOverride, uma única célula-dia, disparado pela UI
// enquanto o rádio está no ar — TOMA o lock; ver lá.
func (dr *DistributionRules) recategorizeScope(ctx context.Context,
	campaignID uuid.UUID, typeID *uuid.UUID, stationIDs []uuid.UUID,
	from, to time.Time) error {

	_, err := dr.runRecat(ctx, recatScopeByCampaignSQL,
		campaignID, typeID, stationIDs, from, to)
	return err
}

// recatScopeByCampaignSQL é a CTE `scope` dos escopos rule/campaign/override.
const recatScopeByCampaignSQL = `
WITH scope AS (
    SELECT dc.detection_id AS id, dc.detected_at, dc.campaign_id,
           dc.commercial_id AS material_id, m.type_id, d.station_id
    FROM detection_campaigns dc
    JOIN detections d ON d.id = dc.detection_id AND d.detected_at = dc.detected_at
    JOIN materials m ON m.id = dc.commercial_id
    WHERE dc.campaign_id = $1
      AND ($2::uuid IS NULL OR m.type_id = $2)
      AND ($3::uuid[] IS NULL OR d.station_id = ANY($3))
      AND (date_trunc('day', dc.detected_at AT TIME ZONE 'America/Sao_Paulo')::date
           BETWEEN $4::date AND $5::date)
)`

// RecategorizeForMaterial re-classifica TODAS as projeções que carregam um
// material, em todas as campanhas onde ele aparece. Usado quando o type_id do
// material muda: a categoria gravada foi computada no insert com o tipo antigo
// e fica obsoleta — uma projeção que casava uma regra do tipo novo continua
// marcada 'bonus' (indistinguível de excedente no resumo diário). O escopo vem de
// detection_campaigns (dc.commercial_id = $1), não da tocada-base: uma projeção
// fan-out F-119 carrega o material numa campanha SECUNDÁRIA mesmo quando a base
// (d.commercial_id) é outro material — reclassificá-la exige escopar pela
// projeção. A tocada-base (detections.category) é espelhada só quando a projeção
// é a canônica, via guarda do recatApplyBaseSQL (d.campaign_id = v.campaign_id).
// Como o scope resolve m.type_id ao vivo (JOIN materials), rodar isto APÓS o
// UPDATE do type_id reclassifica corretamente contra as regras do tipo atual.
//
// LIMITE CONHECIDO (herdado, e que a cota tornou visível): o scope resolve o tipo
// NOVO, então as células-dia refechadas são as do tipo novo. As células do tipo
// ANTIGO — de onde a tocada acabou de SAIR — perdem uma tocada da cota e ficam
// desatualizadas (uma excedente delas deveria ser promovida a in_slot). Não dá
// pra identificá-las daqui: o tipo antigo já não existe em lugar nenhum quando
// este método roda. Convergem pelo projrecon (janela móvel) enquanto estiverem
// dentro dela, ou pelo próximo fechamento da célula. Resolver de vez exige o
// caller (handlers/materials.go) passar o type_id antigo — fora do escopo aqui.
func (dr *DistributionRules) RecategorizeForMaterial(ctx context.Context, materialID uuid.UUID) error {
	_, err := dr.runRecat(ctx, `
WITH scope AS (
    SELECT dc.detection_id AS id, dc.detected_at, dc.campaign_id,
           dc.commercial_id AS material_id, m.type_id, d.station_id
    FROM detection_campaigns dc
    JOIN detections d ON d.id = dc.detection_id AND d.detected_at = dc.detected_at
    JOIN materials m ON m.id = dc.commercial_id
    WHERE dc.commercial_id = $1
)`, materialID)
	return err
}

// RecategorizeForOverride re-classifica as detections de UMA célula (campaign,
// type, station, dia) após criar/editar/apagar um override. Escopo preciso: só
// aquele dia/tipo/estação. recategorizeScope lê rules+overrides ao vivo, então
// serve tanto p/ Upsert (aplica o override) quanto p/ Delete (célula reverte pra
// regra). O tail atualiza detections.category E detection_campaigns.category.
//
// Único caminho de recat que TOMA o advisory lock da célula-dia — o mesmo que o
// settleCellDay toma no insert. Aqui é barato e vale a pena: o escopo é UMA
// célula-dia (uma chave, cf. cellDayLockKey, que colapsa o tipo de propósito), e
// é o recat mais provável de interleavar com uma tocada chegando, porque o
// operador salva o override pela UI com o rádio no ar. Sem o lock, os dois lêem
// o mesmo conjunto e o que commitar por último grava um veredito calculado sem a
// tocada do outro. Os escopos amplos não tomam — ver recategorizeScope.
//
// forDate é a data local SP da célula (o for_date de distribution_overrides).
// Vai pra chave do lock formatada como YYYY-MM-DD no fuso do próprio valor:
// NÃO passe por dayStartSP, que deslocaria um DATE lido como 00:00Z pro dia
// anterior em SP.
func (dr *DistributionRules) RecategorizeForOverride(ctx context.Context,
	campaignID, typeID, stationID uuid.UUID, forDate time.Time) error {

	tx, err := dr.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// PRIMEIRO statement da transação, antes de qualquer leitura — mesma razão
	// documentada em settleCellDay.
	if err := lockCellDayKeys(ctx, tx, cellDayLockKey(campaignID, stationID, forDate)); err != nil {
		return err
	}
	if _, err := dr.applyRecat(ctx, tx, recatScopeByCampaignSQL,
		campaignID, &typeID, []uuid.UUID{stationID}, forDate, forDate); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
