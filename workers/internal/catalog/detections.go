package catalog

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"radiocheck/internal/categorizer"
)

type Detection struct {
	ID                 uuid.UUID `json:"id"`
	StationID          uuid.UUID `json:"station_id"`
	StationName        string    `json:"station_name"`
	CommercialID       uuid.UUID `json:"commercial_id"`
	CommercialName     string    `json:"commercial_name"`
	CampaignID         uuid.UUID `json:"campaign_id"`
	DetectedAt         time.Time `json:"detected_at"`
	MatchStartOffsetMs int32     `json:"match_start_offset_ms"`
	MatchEndOffsetMs   int32     `json:"match_end_offset_ms"`
	Confidence         float64   `json:"confidence"`
	HashCount          int32     `json:"hash_count"`
	TemporalCoverage   *float64  `json:"temporal_coverage,omitempty"`
	// AuditCoverage is the §9.9 audit coverage of the saved clip against the
	// attributed master (frames matched / total). Nil until the evidence audit
	// runs. After §18.2.2-v2 reattribution it reflects the WINNING cut's
	// coverage. Surfaced on /detections/:id so an operator sees how much of the
	// master the clip actually contained.
	AuditCoverage     *float64 `json:"audit_coverage,omitempty"`
	VariantUsed       *int16   `json:"variant_used,omitempty"`
	RateUsed          *int16   `json:"rate_used,omitempty"`
	EvidenceStatus    string   `json:"evidence_status"`
	EvidenceKey       *string  `json:"evidence_key,omitempty"`
	EvidenceSizeBytes *int64   `json:"evidence_size_bytes,omitempty"`
	// Category is one of in_slot|out_slot|out_date|bonus (migrations 0018 e
	// 0063). 'orphan' é o nome antigo de 'bonus' e ainda pode chegar de linha
	// gravada pelo binário anterior — ver categorizer.CatOrphan.
	// Consumed by the DayDetailModal to group detections under their category
	// section; without it, the modal renders an empty list even when filtered
	// detections exist.
	Category string `json:"category"`
	// TypeID is the type_id of the detected material, resolved via JOIN
	// materials (migration 0019 made distribution rules type-keyed; the
	// frontend filters/groups detections by type using this field).
	// Nil when the material has no type assigned (legacy).
	TypeID *uuid.UUID `json:"type_id,omitempty"`
	// RetractedAt is set when §18.2.2 disambiguation overruled this row in
	// favour of a longer cut from the same client; nil otherwise.
	RetractedAt *time.Time `json:"retracted_at,omitempty"`
	// IgnoredAt / IgnoredBy are set when an admin manually disregards this
	// veiculação via the "Desconsiderar" action on the detection detail page.
	// The daily_play_summary view skips ignored rows, so bonus/deficit
	// recompute automatically. Reversible: clearing IgnoredAt reactivates.
	IgnoredAt *time.Time `json:"ignored_at,omitempty"`
	IgnoredBy *uuid.UUID `json:"ignored_by,omitempty"`
	// ManualAt / ManualBy / ManualNote populate quando um admin sobe a
	// veiculação retroativamente via "Adicionar veiculação manualmente" na
	// modal de /detections. A linha conta normalmente em agregados (o
	// categorizer roda igual a uma detection real); a tripla é só pra
	// auditoria + badge + nota na detail page.
	ManualAt   *time.Time `json:"manual_at,omitempty"`
	ManualBy   *uuid.UUID `json:"manual_by,omitempty"`
	ManualNote *string    `json:"manual_note,omitempty"`
	// ProofBatchID aponta pro lote de comprovante (manual_proof_batches) quando
	// a veiculação foi criada via "comprovante PDF" em lote. Nil para detecções
	// automáticas e manuais sem comprovante. /detections/:id usa pra mostrar o
	// card "Comprovante (PDF)".
	ProofBatchID *uuid.UUID `json:"proof_batch_id,omitempty"`
	// CommercialScript mirrors materials.script for the detected material.
	// Populated by the Get handler (single-detection detail page); the bulk
	// list endpoints leave it nil to keep the payload tight.
	CommercialScript *string   `json:"commercial_script,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

type Detections struct {
	pool *pgxpool.Pool
}

func NewDetections(pool *pgxpool.Pool) *Detections {
	return &Detections{pool: pool}
}

type CreateDetectionInput struct {
	StationID          uuid.UUID
	CommercialID       uuid.UUID
	CampaignID         uuid.UUID
	DetectedAt         time.Time
	MatchStartOffsetMs int32
	MatchEndOffsetMs   int32
	Confidence         float64
	HashCount          int32
	TemporalCoverage   float64
	VariantUsed        int16
	RateUsed           int16
}

func (d *Detections) Create(ctx context.Context, in CreateDetectionInput) (*Detection, error) {
	// Transação: fecha a célula-dia, insere a detecção física E sua projeção
	// canônica (1:1) atomicamente. F-119: a grade (daily_play_summary) e as
	// leituras por-campanha lêem detection_campaigns, então TODA detecção precisa
	// ao menos da projeção canônica — independente do caminho (evidence service,
	// manual, backfill). O fan-out multi-atribuição (projeções extras) é feito
	// pelo evidence.Service quando MULTI_ATTRIBUTION está ON; aqui é só a canônica.
	//
	// O fechamento roda DENTRO da tx (spec 2026-08-14): ele reescreve a categoria
	// das outras tocadas do dia, e essas reescritas só podem valer se a tocada que
	// as motivou for de fato gravada.
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	category, err := d.settleCellDay(ctx, tx, in, nil, true)
	if err != nil {
		return nil, err
	}

	var det Detection
	err = tx.QueryRow(ctx, `
		INSERT INTO detections (station_id, commercial_id, campaign_id, detected_at,
		                        match_start_offset_ms, match_end_offset_ms, confidence,
		                        hash_count, temporal_coverage, variant_used, rate_used,
		                        category)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id, station_id, commercial_id, campaign_id, detected_at,
		          match_start_offset_ms, match_end_offset_ms, confidence, hash_count,
		          temporal_coverage, variant_used, rate_used,
		          evidence_status, evidence_key, evidence_size_bytes, category, retracted_at, created_at`,
		in.StationID, in.CommercialID, in.CampaignID, in.DetectedAt,
		in.MatchStartOffsetMs, in.MatchEndOffsetMs, in.Confidence, in.HashCount,
		in.TemporalCoverage, in.VariantUsed, in.RateUsed, category,
	).Scan(&det.ID, &det.StationID, &det.CommercialID, &det.CampaignID, &det.DetectedAt,
		&det.MatchStartOffsetMs, &det.MatchEndOffsetMs, &det.Confidence, &det.HashCount,
		&det.TemporalCoverage, &det.VariantUsed, &det.RateUsed,
		&det.EvidenceStatus, &det.EvidenceKey, &det.EvidenceSizeBytes, &det.Category, &det.RetractedAt, &det.CreatedAt)
	if err != nil {
		return nil, err
	}

	if _, err = tx.Exec(ctx, `
		INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (detection_id, detected_at, campaign_id) DO NOTHING`,
		det.ID, det.DetectedAt, det.CampaignID, det.CommercialID, det.Category); err != nil {
		return nil, err
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &det, nil
}

// CategorizeFor expõe o fechamento por-campanha pra fora do pacote. F-119: o
// evidence.Service calcula a categoria de cada projeção fan-out com as regras da
// campanha respectiva. Mesmo settleCellDay usado no Create — logo, além de
// devolver a categoria da projeção nova, reassenta as demais tocadas do dia
// NAQUELA campanha (só as projeções dela; a tocada-base fica com a canônica).
//
// Abre transação própria: o advisory lock que serializa fechamentos da mesma
// célula-dia é xact-scoped e não protege nada quando cada statement é sua
// própria transação implícita.
//
// ATENÇÃO: a projeção pela qual este fechamento foi calculado só é gravada
// DEPOIS, pelo InsertProjections do chamador — se ela não entrar, as reescritas
// aqui já commitaram e sobra tocada rebaixada por uma projeção inexistente. O
// chamador conserta com ResettleCellDay (ver evidence/service.go).
func (d *Detections) CategorizeFor(ctx context.Context, campaignID, commercialID, stationID uuid.UUID, detectedAt time.Time) (string, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return categorizer.CatBonus, err
	}
	defer tx.Rollback(ctx)

	cat, err := d.settleCellDay(ctx, tx, CreateDetectionInput{
		CampaignID:   campaignID,
		CommercialID: commercialID,
		StationID:    stationID,
		DetectedAt:   detectedAt,
	}, nil, true)
	if err != nil {
		return categorizer.CatBonus, err
	}
	if err := tx.Commit(ctx); err != nil {
		return categorizer.CatBonus, err
	}
	return cat, nil
}

// pgxQuerier é o subconjunto de pgxpool.Pool / pgx.Tx que o settleCellDay usa.
// Existe pra ele poder rodar DENTRO de uma transação: quando o caller já segura
// uma conexão via tx, usar d.pool aqui exigiria uma SEGUNDA conexão simultânea —
// com o pool no teto, N batches concorrentes deadlockam.
//
// Exec entrou junto com o fechamento por célula-dia (spec 2026-08-14): o
// settleCellDay não só LÊ pra decidir a categoria da tocada nova, ele REESCREVE
// a das tocadas já gravadas do dia. Essa escrita tem que sair no MESMO querier
// da leitura, senão o caller que está numa tx veria (e reescreveria) um estado
// que a própria tx dele ainda não commitou.
type pgxQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// spLoc é o fuso das datas de negócio. O "dia" de uma célula (campanha, tipo,
// emissora, dia) é sempre o dia calendário local em São Paulo — é assim que o
// categorizer, a view daily_play_summary e o recat SQL enxergam a grade.
var spLoc, _ = time.LoadLocation("America/Sao_Paulo")

// settleCellDay FECHA a célula-dia (campanha, tipo de material, emissora, dia
// local SP) da tocada descrita por `in`: carrega TODAS as tocadas aprovadas
// daquele dia naquela célula, roda categorizer.Settle sobre o conjunto inteiro,
// regrava a categoria das que mudaram e devolve a categoria da tocada NOVA.
//
// Substitui a categorização stateless (uma tocada por vez) — spec 2026-08-14: a
// categoria depende da COTA do dia, então uma tocada que chega mais tarde pode
// mudar o veredito de uma que já estava gravada (a das 03:00 vira excedente
// quando a meta fecha dentro da faixa).
//
// Migration 0019: rules e overrides são chaveados pelo TIPO do material.
// Migration 0031: o override carrega faixa própria e supersede as rules do dia.
//
// q é o querier a usar (d.pool fora de transação, ou a tx do caller quando já
// segurando uma — ver pgxQuerier acima). Callers dentro de uma tx DEVEM passar
// essa tx: usar d.pool ali exigiria uma segunda conexão simultânea do pool, e
// as reescritas ficariam fora do átomo do caller.
//
// replacingID != nil identifica uma tocada que JÁ EXISTE no banco e cujo lugar
// no dia está sendo recomputado (reatribuição): ela é excluída do conjunto
// carregado e entra só como a tocada "nova", com o material/campanha novos.
// Sem isso ela contaria duas vezes contra a cota.
//
// Multi-atribuição (F-119): o fechamento é POR CAMPANHA — o conjunto é escopado
// por dc.campaign_id e a reescrita só toca a projeção DESTA campanha. A
// tocada-base (detections.category) só é espelhada quando a projeção é a
// canônica (d.campaign_id = in.CampaignID), mesma guarda do recatApplySQL.
//
// includeNewPlay=false refecha a célula-dia SEM tocada nova (só o que já está
// gravado) e devolve string vazia — é o caminho de convergência quando a tocada
// que motivou um fechamento anterior acabou não sendo persistida (ver
// ResettleCellDay).
func (d *Detections) settleCellDay(ctx context.Context, q pgxQuerier, in CreateDetectionInput,
	replacingID *uuid.UUID, includeNewPlay bool) (string, error) {

	local := in.DetectedAt.In(spLoc)
	dayLocal := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, spLoc)
	dayEnd := dayLocal.AddDate(0, 0, 1)

	// PRIMEIRO STATEMENT, antes de QUALQUER leitura — serializa os fechamentos
	// concorrentes da mesma célula-dia. Sem isso, duas transações lêem o mesmo
	// conjunto (READ COMMITTED dá um snapshot por statement), ambas concluem que
	// ainda há vaga na cota e ambas gravam in_slot: a célula fecha com
	// in_slot > N, silenciosamente. Quando nada precisa ser reescrito elas não
	// compartilham NENHUMA linha, então não existe lock de linha que as serialize
	// — o advisory lock é o único ponto de encontro. Tomá-lo depois de ler já não
	// adianta: a leitura teria saído do snapshot velho.
	//
	// A chave é (campanha, emissora, dia) e NÃO inclui o tipo do material de
	// propósito: o type_id exige um SELECT em materials, e aí o lock deixaria de
	// ser o primeiro statement. Chave mais grossa é conservadora — serializa
	// também células de tipos diferentes da mesma campanha+emissora+dia, o que é
	// contenção desprezível (a mesma emissora não recebe duas tocadas no mesmo
	// instante) e nunca incorreta.
	//
	// _xact_ = escopo de transação: solta sozinho no commit/rollback. Fora de uma
	// tx (caminho d.pool) cada statement é sua própria transação implícita, o
	// lock nasce e morre nesse statement e não protege nada — por isso os callers
	// que precisam da garantia passam uma tx (ver CategorizeFor).
	if err := lockCellDayKeys(ctx, q,
		cellDayLockKey(in.CampaignID, in.StationID, dayLocal)); err != nil {
		return categorizer.CatBonus, err
	}

	// typeID pode ser NULL (material legado sem tipo) e a linha pode nem estar em
	// materials (commercial legado) — nos dois casos nenhuma regra/override casa.
	// Por isso o subselect, e não um JOIN: um JOIN devolveria zero linhas e o
	// insert falharia com ErrNoRows.
	var cmpStart, cmpEnd time.Time
	var typeID *uuid.UUID
	err := q.QueryRow(ctx, `
		SELECT c.start_date, c.end_date, (SELECT m.type_id FROM materials m WHERE m.id = $2)
		FROM campaigns c WHERE c.id = $1`, in.CampaignID, in.CommercialID,
	).Scan(&cmpStart, &cmpEnd, &typeID)
	if err != nil {
		return categorizer.CatBonus, err
	}

	cmp := categorizer.Campaign{StartDate: cmpStart, EndDate: cmpEnd}
	newPlay := categorizer.Play{DetectedAt: in.DetectedAt, MaterialID: in.CommercialID}

	if typeID == nil {
		// Sem tipo não há célula: nenhuma regra e nenhum override podem casar, e
		// as outras tocadas do dia pertencem a outras células. Fecha só ela —
		// out_date fora do período da campanha, senão bonus (meta 0).
		if !includeNewPlay {
			return "", nil
		}
		return categorizer.Settle(dayLocal, []categorizer.Play{newPlay}, cmp, nil, nil)[0], nil
	}

	rules, err := d.loadRulesForCell(ctx, q, in.CampaignID, *typeID, in.StationID)
	if err != nil {
		return categorizer.CatBonus, err
	}
	ov, err := d.loadOverrideForCell(ctx, q, in.CampaignID, *typeID, in.StationID, dayLocal)
	if err != nil {
		return categorizer.CatBonus, err
	}

	existing, err := d.loadCellDayPlays(ctx, q, in, *typeID, dayLocal, dayEnd, replacingID)
	if err != nil {
		return categorizer.CatBonus, err
	}

	// Pré-condição 2 de Settle (ordem por detected_at, id): as gravadas vêm
	// ordenadas do SQL e a nova é apenas ANEXADA — a ordenação estável do Settle
	// a coloca na posição certa por detected_at. Num empate EXATO de segundo com
	// uma já gravada, a nova fica por último e perde a vaga da cota; o id dela
	// ainda não existe (e será um UUID v4), então o desempate por id do recat SQL
	// é efetivamente aleatório: os dois motores podem discordar do empate em
	// QUALQUER direção, não só nessa. Inalcançável na prática (o cooldown do
	// matcher impede duas tocadas no mesmo segundo) e o recat converge.
	plays := make([]categorizer.Play, 0, len(existing)+1)
	for _, r := range existing {
		plays = append(plays, categorizer.Play{DetectedAt: r.detectedAt, MaterialID: r.materialID})
	}
	if includeNewPlay {
		plays = append(plays, newPlay)
	}

	cats := categorizer.Settle(dayLocal, plays, cmp, rules, ov)

	if err := d.rewriteCategories(ctx, q, in.CampaignID, existing, cats[:len(existing)],
		dayLocal, dayEnd); err != nil {
		return categorizer.CatBonus, err
	}
	if !includeNewPlay {
		return "", nil
	}
	return cats[len(cats)-1], nil
}

// ResettleCellDay refecha a célula-dia SEM nenhuma tocada nova: reassenta só o
// que já está gravado. É o caminho de convergência do fan-out (F-119) — o
// CategorizeFor fecha a célula-dia da campanha secundária contando com uma
// projeção que só é gravada DEPOIS, por InsertProjections; se essa gravação
// falhar, as tocadas que já existiam ficam rebaixadas por uma tocada que não
// existe. Chamar isto restaura a célula imediatamente, em vez de esperar o
// projrecon. Roda na própria transação (o advisory lock do fechamento precisa
// de uma pra valer).
func (d *Detections) ResettleCellDay(ctx context.Context,
	campaignID, commercialID, stationID uuid.UUID, at time.Time) error {

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := d.settleCellDay(ctx, tx, CreateDetectionInput{
		CampaignID:   campaignID,
		CommercialID: commercialID,
		StationID:    stationID,
		DetectedAt:   at,
	}, nil, false); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// dayStartSP devolve a meia-noite local SP do dia de `at` — a coordenada "dia"
// da célula-dia. Toda derivação de dia no fechamento passa por aqui.
func dayStartSP(at time.Time) time.Time {
	l := at.In(spLoc)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, spLoc)
}

// cellDayLockKey monta a chave do advisory lock da célula-dia. NÃO inclui o tipo
// do material de propósito — ver o comentário longo em settleCellDay: resolver o
// type_id exige um SELECT em materials, e o lock tem que ser o PRIMEIRO statement
// da transação. Chave mais grossa é conservadora: serializa também células de
// outros tipos da mesma campanha+emissora+dia (contenção desprezível), mas nunca
// deixa passar um par que conflita — duas células com o mesmo (campanha,
// emissora, dia) SEMPRE colidem na mesma chave, tenham o mesmo tipo ou não.
func cellDayLockKey(campaignID, stationID uuid.UUID, dayLocal time.Time) string {
	return campaignID.String() + "|" + stationID.String() + "|" + dayLocal.Format("2006-01-02")
}

// lockCellDayKeys toma os advisory locks xact-scoped das células-dia informadas,
// em ORDEM LEXICOGRÁFICA e sem repetição.
//
// A ordenação é o que impede deadlock entre duas transações que travam mais de
// uma célula (reatribuição = origem + destino; tocada com fan-out = uma célula
// por campanha projetada): sem uma ordem global, uma pega A→B e a outra B→A.
// Re-tomar a mesma chave dentro da mesma transação é no-op (advisory locks são
// reentrantes), então settleCellDay pode pedir de novo a chave que o caller já
// segura.
//
// INVARIANTE que sustenta a ausência de deadlock com os locks de LINHA: toda
// transação toma TODOS os seus advisory locks ANTES de escrever qualquer linha.
// Assim nenhuma transação fica esperando um advisory lock segurando um lock de
// linha, e o ciclo advisory↔linha não pode se formar.
func lockCellDayKeys(ctx context.Context, q pgxQuerier, keys ...string) error {
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	prev := ""
	for i, k := range sorted {
		if i > 0 && k == prev {
			continue
		}
		prev = k
		if _, err := q.Exec(ctx,
			`SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, k); err != nil {
			return err
		}
	}
	return nil
}

// affectedCell é uma célula-dia que uma tocada alimenta. Uma tocada alimenta
// UMA célula por projeção em detection_campaigns: a canônica mais, quando o
// fan-out multi-atribuição (F-119) está ligado, uma por campanha secundária.
type affectedCell struct {
	campaignID   uuid.UUID
	commercialID uuid.UUID
	stationID    uuid.UUID
	detectedAt   time.Time
}

// affectedCells lista as células-dia que a detecção alimenta. Sai da PROJEÇÃO
// (detection_campaigns), porque o fechamento é por campanha; o LEFT JOIN +
// COALESCE cobre a tocada que ainda não tem projeção nenhuma (cai na campanha da
// própria linha base). Zero linhas = a detecção não existe.
//
// detectedAt != nil poda partição nas DUAS tabelas particionadas. Ignore/Restore
// não recebem detected_at do chamador (a rota é só /detections/:id) e passam nil.
func (d *Detections) affectedCells(ctx context.Context, q pgxQuerier,
	id uuid.UUID, detectedAt *time.Time) ([]affectedCell, error) {

	rows, err := q.Query(ctx, `
		SELECT COALESCE(dc.campaign_id, d.campaign_id),
		       COALESCE(dc.commercial_id, d.commercial_id),
		       d.station_id, d.detected_at
		FROM detections d
		LEFT JOIN detection_campaigns dc
		       ON dc.detection_id = d.id
		      AND dc.detected_at = d.detected_at
		      AND ($2::timestamptz IS NULL OR dc.detected_at = $2)
		WHERE d.id = $1
		  AND ($2::timestamptz IS NULL OR d.detected_at = $2)
		ORDER BY 1`, id, detectedAt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []affectedCell
	for rows.Next() {
		var c affectedCell
		if err := rows.Scan(&c.campaignID, &c.commercialID, &c.stationID, &c.detectedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// resettleCells refecha cada célula-dia SEM tocada nova (includeNewPlay=false):
// o conjunto é relido do banco, então quem acabou de sair do conjunto aprovado
// já não conta na cota e quem voltou já conta.
func (d *Detections) resettleCells(ctx context.Context, q pgxQuerier, cells []affectedCell) error {
	for _, c := range cells {
		if _, err := d.settleCellDay(ctx, q, CreateDetectionInput{
			CampaignID:   c.campaignID,
			CommercialID: c.commercialID,
			StationID:    c.stationID,
			DetectedAt:   c.detectedAt,
		}, nil, false); err != nil {
			return err
		}
	}
	return nil
}

// mutateApprovedSet aplica uma mudança de estado que TIRA (retratar, ignorar,
// marcar ambígua, rejeitar no audit) ou DEVOLVE (des-retratar, restaurar) a
// tocada do conjunto aprovado, e refecha as células-dia afetadas na MESMA
// transação.
//
// Existe porque com cota a categoria de uma tocada depende das OUTRAS: liberar
// uma vaga promove a excedente seguinte a in_slot, e reabrir a meta faz a tocada
// fora da faixa voltar a segurar o déficit. No modelo antigo (classificação
// isolada por tocada) nada disso acontecia e bastava o UPDATE. Sem o
// re-fechamento, se nenhuma tocada nova cair naquela célula-dia as categorias
// ficam erradas PRA SEMPRE — in_slot subnotificado, déficit superestimado.
//
// ORDEM, que é o ponto todo:
//  1. lê as células afetadas (identidade da linha — não o conjunto da célula);
//  2. trava TODAS elas (antes de escrever qualquer linha — ver lockCellDayKeys);
//  3. aplica a mudança de estado;
//  4. refecha, relendo o conjunto — o ApprovedDetectionsFilter compartilhado
//     exclui (ou reinclui) a linha naturalmente. NUNCA filtre a linha que está
//     saindo na mão: o filtro canônico é a única definição do conjunto.
//
// Linha inexistente = no-op silencioso, igual ao UPDATE cru que estes caminhos
// eram antes.
func (d *Detections) mutateApprovedSet(ctx context.Context, id uuid.UUID, detectedAt *time.Time,
	apply func(context.Context, pgx.Tx) error) error {

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	cells, err := d.affectedCells(ctx, tx, id, detectedAt)
	if err != nil {
		return err
	}
	if len(cells) == 0 {
		return tx.Commit(ctx)
	}

	keys := make([]string, 0, len(cells))
	for _, c := range cells {
		keys = append(keys, cellDayLockKey(c.campaignID, c.stationID, dayStartSP(c.detectedAt)))
	}
	if err := lockCellDayKeys(ctx, tx, keys...); err != nil {
		return err
	}
	if apply != nil {
		if err := apply(ctx, tx); err != nil {
			return err
		}
	}
	if err := d.resettleCells(ctx, tx, cells); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ResettleDetectionCells refecha as células-dia de uma tocada cuja mudança de
// estado JÁ foi commitada por fora. Existe pros caminhos em lote, onde a escrita
// é um UPDATE de N linhas de uma vez e não dá pra envolver cada uma na transação
// do próprio fechamento (ver cmd/backfill-unretract-displaced). NÃO é atômico
// com a escrita: um crash entre as duas deixa a célula desatualizada até o
// próximo fechamento ou recat. Prefira mutateApprovedSet sempre que a escrita
// couber na mesma transação.
//
// Nasceu servindo o RestoreDisplacedShorterCut, que desde então passou a fazer
// a escrita DENTRO do mutateApprovedSet (atômico) — por isso este helper é
// exportado e hoje tem um único chamador, o CLI de backfill. Se você está
// pensando em usá-lo num caminho de uma linha só, quase certamente quer o
// mutateApprovedSet.
func (d *Detections) ResettleDetectionCells(ctx context.Context, id uuid.UUID, detectedAt *time.Time) error {
	return d.mutateApprovedSet(ctx, id, detectedAt, nil)
}

// loadRulesForCell carrega TODAS as regras da célula (campanha, tipo, emissora),
// sem filtrar por dia: o Settle precisa das regras de outros dias pra decidir o
// out_date do carve-out (material fora do período das regras que o nomeiam).
func (d *Detections) loadRulesForCell(ctx context.Context, q pgxQuerier,
	campaignID, typeID, stationID uuid.UUID) ([]categorizer.Rule, error) {

	rows, err := q.Query(ctx, `
		SELECT r.start_date, r.end_date, r.weekday_mask,
		       r.time_start::text, r.time_end::text, r.plays_per_day, r.material_ids
		FROM distribution_rules r
		WHERE r.campaign_id = $1
		  AND r.type_id = $2
		  AND $3 = ANY(r.station_ids)`,
		campaignID, typeID, stationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []categorizer.Rule
	for rows.Next() {
		var r categorizer.Rule
		var tsStr, teStr string
		var plays int16
		if err := rows.Scan(&r.StartDate, &r.EndDate, &r.WeekdayMask,
			&tsStr, &teStr, &plays, &r.MaterialIDs); err != nil {
			return nil, err
		}
		r.TimeStart, _ = time.Parse("15:04:05", tsStr)
		r.TimeEnd, _ = time.Parse("15:04:05", teStr)
		r.PlaysPerDay = plays
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

// loadOverrideForCell devolve o override da célula+dia, ou nil. (campaign, type,
// station, for_date) é PK em distribution_overrides; for_date é a data local SP.
// Quando há override, ele supersede as rules pra essa célula+dia (D1/D7 do spec).
func (d *Detections) loadOverrideForCell(ctx context.Context, q pgxQuerier,
	campaignID, typeID, stationID uuid.UUID, dayLocal time.Time) (*categorizer.Override, error) {

	var (
		plays  int16
		tsStr  string
		teStr  string
		forDay = dayLocal.Format("2006-01-02")
	)
	err := q.QueryRow(ctx, `
		SELECT plays_expected, time_start::text, time_end::text
		FROM distribution_overrides
		WHERE campaign_id = $1 AND type_id = $2 AND station_id = $3 AND for_date = $4::date`,
		campaignID, typeID, stationID, forDay,
	).Scan(&plays, &tsStr, &teStr)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ts, _ := time.Parse("15:04:05", tsStr)
	te, _ := time.Parse("15:04:05", teStr)
	return &categorizer.Override{PlaysExpected: plays, TimeStart: ts, TimeEnd: te}, nil
}

// cellDayPlay é uma tocada já gravada da célula-dia, com as categorias atuais
// (projeção + base) pra decidir o que precisa ser reescrito.
type cellDayPlay struct {
	id           uuid.UUID
	detectedAt   time.Time
	materialID   uuid.UUID
	projCategory string
	baseCategory string
	baseCampaign uuid.UUID
}

// loadCellDayPlays lê as tocadas APROVADAS já gravadas na célula-dia, ordenadas
// por (detected_at, id) — a mesma ordem do ROW_NUMBER do recat SQL, que é a
// pré-condição 2 do Settle. Escopa pela PROJEÇÃO (detection_campaigns) porque o
// fechamento é por campanha: uma projeção fan-out F-119 pertence a esta campanha
// mesmo quando a tocada-base é de outra. As duas tabelas são particionadas por
// detected_at, então AMBAS levam o recorte do dia pra podar partição.
func (d *Detections) loadCellDayPlays(ctx context.Context, q pgxQuerier, in CreateDetectionInput,
	typeID uuid.UUID, dayLocal, dayEnd time.Time, replacingID *uuid.UUID) ([]cellDayPlay, error) {

	rows, err := q.Query(ctx, `
		SELECT d.id, d.detected_at, dc.commercial_id, dc.category, d.category, d.campaign_id
		FROM detection_campaigns dc
		JOIN detections d ON d.id = dc.detection_id AND d.detected_at = dc.detected_at
		JOIN materials m ON m.id = dc.commercial_id
		WHERE dc.campaign_id = $1
		  AND m.type_id = $2
		  AND d.station_id = $3
		  AND dc.detected_at >= $4 AND dc.detected_at < $5
		  AND d.detected_at  >= $4 AND d.detected_at  < $5
		  AND ($6::uuid IS NULL OR d.id <> $6)
		  AND `+ApprovedDetectionsFilter+`
		ORDER BY d.detected_at, d.id`,
		in.CampaignID, typeID, in.StationID, dayLocal, dayEnd, replacingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []cellDayPlay
	for rows.Next() {
		var p cellDayPlay
		if err := rows.Scan(&p.id, &p.detectedAt, &p.materialID,
			&p.projCategory, &p.baseCategory, &p.baseCampaign); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// rewriteCategories grava o veredito do fechamento nas tocadas JÁ existentes que
// mudaram de categoria — projeção desta campanha sempre, tocada-base só quando a
// projeção é a canônica (mesma guarda do recatApplySQL: sem ela o fechamento de
// uma campanha SECUNDÁRIA do fan-out sobrescreveria a base com o veredito de
// outra campanha). Uma única ida ao banco com os arrays; nenhuma quando nada
// mudou, que é o caso comum no caminho quente de escrita.
//
// ORDEM DE LOCK — detections PRIMEIRO, detection_campaigns depois. O Postgres
// executa o ModifyTable do statement PRINCIPAL antes do da CTE data-modifying,
// então quem trava primeiro é a tabela do UPDATE de baixo. Essa ordem TEM que
// bater com a dos outros caminhos que escrevem nas duas tabelas na mesma tx
// (ReattributeDetection / ReattributeRejectedDetection → UPDATE detections e
// depois syncCanonicalProjection). Com as ordens invertidas, um Create fechando
// a célula-dia e um reattribute de outra tocada da MESMA célula deadlockam — o
// Postgres mata um dos dois, e se a vítima for o Create a veiculação é perdida
// (evidence.Service.handle só loga o erro, não tem retry). Reproduzido em duas
// sessões psql antes da correção. Se mexer aqui, confirme com EXPLAIN que o
// "Update on detections" continua sendo o nó de cima.
func (d *Detections) rewriteCategories(ctx context.Context, q pgxQuerier, campaignID uuid.UUID,
	plays []cellDayPlay, cats []string, dayLocal, dayEnd time.Time) error {

	ids := make([]uuid.UUID, 0, len(plays))
	ats := make([]time.Time, 0, len(plays))
	newCats := make([]string, 0, len(plays))
	for i, p := range plays {
		projStale := p.projCategory != cats[i]
		baseStale := p.baseCampaign == campaignID && p.baseCategory != cats[i]
		if !projStale && !baseStale {
			continue
		}
		ids = append(ids, p.id)
		ats = append(ats, p.detectedAt)
		newCats = append(newCats, cats[i])
	}
	if len(ids) == 0 {
		return nil
	}

	// `d.id = ANY($1)` / `dc.detection_id = ANY($1)` são redundantes com o JOIN em
	// v, mas dão ao planner um predicado de igualdade sobre a PK — sem eles ele
	// chuta a cardinalidade do unnest e varre a partição do mês inteira.
	_, err := q.Exec(ctx, `
		WITH v AS (
		    SELECT * FROM unnest($1::uuid[], $2::timestamptz[], $3::text[])
		           AS t(id, detected_at, category)
		),
		upd_proj AS (
		    UPDATE detection_campaigns dc
		    SET category = v.category
		    FROM v
		    WHERE dc.detection_id = v.id AND dc.detected_at = v.detected_at
		      AND dc.detection_id = ANY($1)
		      AND dc.detected_at >= $5 AND dc.detected_at < $6
		      AND dc.campaign_id = $4
		      AND dc.category IS DISTINCT FROM v.category
		    RETURNING 1
		)
		UPDATE detections d
		SET category = v.category
		FROM v
		WHERE d.id = v.id AND d.detected_at = v.detected_at
		  AND d.id = ANY($1)
		  AND d.detected_at >= $5 AND d.detected_at < $6
		  AND d.campaign_id = $4
		  AND d.category IS DISTINCT FROM v.category`,
		ids, ats, newCats, campaignID, dayLocal, dayEnd)
	return err
}

// CreateManualInput é o payload da inserção retroativa "Adicionar veiculação
// manualmente" que aparece na DayDetailModal. Os campos espelham a entrada
// real (campaign / commercial / station / detected_at) mais a tripla de
// auditoria que vai pra detections.manual_*. O fechamento da célula-dia roda
// igual à engine — então in_slot / out_slot / out_date / bonus funcionam
// exatamente como numa veiculação real.
type CreateManualInput struct {
	StationID    uuid.UUID
	CommercialID uuid.UUID
	CampaignID   uuid.UUID
	DetectedAt   time.Time
	ManualBy     uuid.UUID
	ManualNote   string // pode ser vazio → vai como NULL
}

// CreateManual valida o vínculo material × emissora × campanha (rejeita se a
// emissora não estiver em campaign_materials.target_stations pro material), e
// insere a detection com:
//   - confidence = 1.0 (declarado, ground-truth)
//   - hash_count = 0, *_offset_ms = 0
//   - evidence_status = 'missing' (sem áudio)
//   - manual_at = now(), manual_by, manual_note
//
// A categorização sai do mesmo settleCellDay() que a engine real usa: a
// veiculação manual disputa a cota do dia junto com as automáticas e pode
// reclassificar as que já estavam gravadas.
func (d *Detections) CreateManual(ctx context.Context, in CreateManualInput) (*Detection, error) {
	// Validação do vínculo: a emissora precisa estar no target_stations
	// do material dentro daquela campanha. Sem isso o operador podia subir
	// veiculação de um material que nem está atribuído à emissora — gerando
	// dado contraditório com o restante do sistema.
	var linked bool
	err := d.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM campaign_materials
			WHERE campaign_id = $1
			  AND material_id = $2
			  AND $3 = ANY(target_stations)
		)`, in.CampaignID, in.CommercialID, in.StationID,
	).Scan(&linked)
	if err != nil {
		return nil, err
	}
	if !linked {
		return nil, ErrMaterialNotLinkedToStation
	}

	var note *string
	if trimmed := strings.TrimSpace(in.ManualNote); trimmed != "" {
		note = &trimmed
	}

	// Transação: fechamento da célula-dia + detecção manual + projeção canônica
	// (1:1), igual ao Create. F-119: sem a projeção a veiculação manual sumiria da
	// grade (que lê detection_campaigns).
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Fecha a célula-dia igual à engine real — a veiculação manual disputa a
	// mesma cota que as automáticas.
	cat, err := d.settleCellDay(ctx, tx, CreateDetectionInput{
		StationID:    in.StationID,
		CommercialID: in.CommercialID,
		CampaignID:   in.CampaignID,
		DetectedAt:   in.DetectedAt,
	}, nil, true)
	if err != nil {
		return nil, err
	}

	var id uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO detections (
		    station_id, commercial_id, campaign_id, detected_at,
		    match_start_offset_ms, match_end_offset_ms,
		    confidence, hash_count, category,
		    evidence_status,
		    manual_at, manual_by, manual_note
		) VALUES (
		    $1, $2, $3, $4,
		    0, 0,
		    1.0, 0, $5,
		    'missing',
		    now(), $6, $7
		)
		RETURNING id`,
		in.StationID, in.CommercialID, in.CampaignID, in.DetectedAt,
		cat, in.ManualBy, note,
	).Scan(&id); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (detection_id, detected_at, campaign_id) DO NOTHING`,
		id, in.DetectedAt, in.CampaignID, in.CommercialID, cat); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return d.Get(ctx, id)
}

// ErrMaterialNotLinkedToStation sinaliza tentativa de inserir veiculação
// manual de material que não está atribuído à emissora alvo na campanha.
var ErrMaterialNotLinkedToStation = errors.New("material is not linked to this station in this campaign")

// UpdateEvidence grava o resultado da persistência do clipe.
//
// 'audit_rejected' é o ÚNICO status que muda o conjunto aprovado
// (ApprovedDetectionsFilter): a tocada sai da conta e libera a vaga que ocupava
// na cota do dia, então esse caso roda transacional e refecha a célula-dia
// (mutateApprovedSet). Os demais status ('available', 'missing', 'failed',
// 'expired') não mexem no conjunto e seguem no UPDATE cru — é o caminho quente
// de escrita e não vale pagar transação + refechamento por nada.
//
// A direção inversa (sair de 'audit_rejected') não passa por aqui: quem
// des-rejeita é ReattributeRejectedDetection, que já fecha as duas células.
func (d *Detections) UpdateEvidence(ctx context.Context, id uuid.UUID, detectedAt time.Time,
	status, key string, sizeBytes int64) error {

	const q = `
		UPDATE detections
		SET evidence_status = $3, evidence_key = $4, evidence_size_bytes = $5
		WHERE id = $1 AND detected_at = $2`

	if status != "audit_rejected" {
		_, err := d.pool.Exec(ctx, q, id, detectedAt, status, key, sizeBytes)
		return err
	}
	return d.mutateApprovedSet(ctx, id, &detectedAt, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, q, id, detectedAt, status, key, sizeBytes)
		return err
	})
}

// MarkEvidenceExpired flags a detection whose audio clip was reclaimed by the
// local retention prune (§11.4 prod variant, incidente 2026-07-02): the clip is
// gone from storage but the detection still counts as an airing. Sets
// evidence_status='expired' and clears the key/size so the UI and the presign
// endpoint stop pointing at a deleted object. Guarded by evidence_status =
// 'available' so it is idempotent and never clobbers a row that changed status
// (retracted/ambiguous) between candidate selection and the update. detected_at
// is in the WHERE for partition pruning (detections is partitioned by it).
func (d *Detections) MarkEvidenceExpired(ctx context.Context, id uuid.UUID, detectedAt time.Time) error {
	_, err := d.pool.Exec(ctx, `
		UPDATE detections
		SET evidence_status = 'expired', evidence_key = NULL, evidence_size_bytes = 0
		WHERE id = $1 AND detected_at = $2 AND evidence_status = 'available'`,
		id, detectedAt)
	return err
}

// SetAuditCoverage records the §9.9 audit coverage (master frames matched in the
// clip / total) for a detection that passed the audit. The coverage-based version
// disambiguation (§18.2.2 v2) reads it on a sibling cut to decide which cut
// actually aired (the most-covered master wins, not the longest); /detections/:id
// shows it. detected_at is in the WHERE for partition pruning (detections is
// partitioned by detected_at), mirroring UpdateEvidence.
func (d *Detections) SetAuditCoverage(ctx context.Context, id uuid.UUID, detectedAt time.Time, coverage float64) error {
	_, err := d.pool.Exec(ctx,
		`UPDATE detections SET audit_coverage = $3 WHERE id = $1 AND detected_at = $2`,
		id, detectedAt, coverage)
	return err
}

// SiblingCut is another cut (master) of the same client — the unit the
// coverage-based version disambiguation (§18.2.2 v2) re-audits the evidence clip
// against to decide which cut actually aired.
type SiblingCut struct {
	ID              uuid.UUID
	ShortID         int32
	DurationSeconds int
}

// FindCutWithSiblings, given an attributed master UUID, returns that master's own
// (short_id, duration) plus the OTHER ready material masters of the same client.
// The siblings come from the catalog (materials), NOT from detection rows — so a
// cut that was suppressed/retracted and has no row is still found. This is what
// lets the audit re-fingerprint the clip against every cut of the client and pick
// the one it really matches.
//
// When the master UUID is not a material (legacy commercial), self is zero and
// siblings is empty — the caller then leaves attribution unchanged (the material
// library is where the 15s/30s confusion lives).
func (d *Detections) FindCutWithSiblings(ctx context.Context, masterID uuid.UUID) (self SiblingCut, siblings []SiblingCut, err error) {
	var clientID uuid.UUID
	var dur float64
	err = d.pool.QueryRow(ctx,
		`SELECT short_id, duration_seconds, client_id FROM materials WHERE id = $1`, masterID,
	).Scan(&self.ShortID, &dur, &clientID)
	if errors.Is(err, pgx.ErrNoRows) {
		return SiblingCut{}, nil, nil // not a material master → no siblings
	}
	if err != nil {
		return SiblingCut{}, nil, err
	}
	self.ID = masterID
	self.DurationSeconds = int(dur + 0.5)

	rows, err := d.pool.Query(ctx, `
		SELECT id, short_id, duration_seconds
		FROM materials
		WHERE client_id = $1 AND id <> $2 AND fingerprint_status = 'ready'`,
		clientID, masterID)
	if err != nil {
		return self, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var s SiblingCut
		var d2 float64
		if err := rows.Scan(&s.ID, &s.ShortID, &d2); err != nil {
			return self, nil, err
		}
		s.DurationSeconds = int(d2 + 0.5)
		siblings = append(siblings, s)
	}
	return self, siblings, rows.Err()
}

// ReattributeDetection re-points a detection at a different cut (§18.2.2 v2): the
// coverage-based audit found the evidence clip matches newCommercialID better
// than the cut it was first attributed to. Sets commercial_id + campaign_id and
// re-runs the categorizer for the new (campaign, cut, station, day) so in_slot /
// out_slot / out_date / bonus stays consistent. detected_at is in the WHERE for
// partition pruning.
//
// F-119: a tocada base e sua projeção canônica em detection_campaigns DEVEM
// andar juntas. Atualizar só detections deixa a projeção órfã no material/
// campanha antigos (incidente 2026-06-30: tocada-fantasma do material errado na
// grade/relatórios, que lêem detection_campaigns). As duas escritas correm na
// MESMA transação via syncCanonicalProjection.
//
// DUAS células-dia mudam aqui: a de DESTINO ganha a tocada e a de ORIGEM a
// PERDE. A de origem também precisa ser refechada — a vaga que a tocada ocupava
// na cota de lá fica livre e tem que passar pra próxima excedente. Sem isso o
// dia de origem fica com in_slot subnotificado e déficit inflado pra sempre.
//
// Os DOIS advisory locks são tomados juntos, no começo e em ordem determinística
// (lockCellDayKeys ordena), antes de qualquer escrita: é o único par de células
// travado pela mesma transação neste arquivo, exatamente o caso que produziria
// deadlock se cada lado pegasse na ordem que lhe convém.
func (d *Detections) ReattributeDetection(ctx context.Context, detectionID uuid.UUID, detectedAt time.Time,
	newCommercialID, newCampaignID, stationID uuid.UUID) error {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Identidade da linha (não o conjunto da célula) — pode ser lida antes do
	// lock, mesma justificativa do affectedCells em mutateApprovedSet. O
	// commercial antigo é o que resolve o TIPO da célula de origem.
	var oldCampaignID, oldCommercialID uuid.UUID
	if err := tx.QueryRow(ctx,
		`SELECT campaign_id, commercial_id FROM detections WHERE id = $1 AND detected_at = $2`,
		detectionID, detectedAt).Scan(&oldCampaignID, &oldCommercialID); err != nil {
		return err
	}

	day := dayStartSP(detectedAt)
	if err := lockCellDayKeys(ctx, tx,
		cellDayLockKey(oldCampaignID, stationID, day),
		cellDayLockKey(newCampaignID, stationID, day)); err != nil {
		return err
	}

	// Fecha a célula-dia de DESTINO com esta tocada no lugar novo. replacingID
	// exclui a linha do conjunto carregado: ela já existe no banco (com o corte
	// antigo) e contaria duas vezes contra a cota do dia.
	cat, err := d.settleCellDay(ctx, tx, CreateDetectionInput{
		StationID:    stationID,
		CommercialID: newCommercialID,
		CampaignID:   newCampaignID,
		DetectedAt:   detectedAt,
	}, &detectionID, true)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE detections
		SET commercial_id = $3, campaign_id = $4, category = $5
		WHERE id = $1 AND detected_at = $2`,
		detectionID, detectedAt, newCommercialID, newCampaignID, cat); err != nil {
		return err
	}
	if err := syncCanonicalProjection(ctx, tx, detectionID, detectedAt,
		oldCampaignID, newCampaignID, newCommercialID, cat); err != nil {
		return err
	}

	// Célula-dia de ORIGEM, DEPOIS das escritas: só agora a projeção antiga já
	// não existe (ou aponta pro material novo), então a releitura do conjunto
	// exclui a tocada que saiu — que é o ponto todo. Sem replacingID: aqui não
	// há tocada nova, só o que sobrou. Quando origem e destino são a MESMA
	// célula (mesma campanha e mesmo tipo), isto reassenta o conjunto já
	// correto e converge pro mesmo resultado.
	if _, err := d.settleCellDay(ctx, tx, CreateDetectionInput{
		StationID:    stationID,
		CommercialID: oldCommercialID,
		CampaignID:   oldCampaignID,
		DetectedAt:   detectedAt,
	}, nil, false); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// syncCanonicalProjection re-aponta a projeção canônica (detection_campaigns) pra
// acompanhar uma reatribuição da tocada base, na MESMA tx do UPDATE detections.
// Remove a projeção da campanha antiga e grava a da campanha nova (idempotente:
// se a campanha nova já tinha projeção — multi-atribuição — atualiza material +
// categoria). Só mexe na projeção da campanha-base; projeções de OUTRAS campanhas
// (fan-out multi-atribuição) ficam intactas. Ver incidente 2026-06-30.
func syncCanonicalProjection(ctx context.Context, tx pgx.Tx,
	detectionID uuid.UUID, detectedAt time.Time,
	oldCampaignID, newCampaignID, newCommercialID uuid.UUID, category string) error {
	if _, err := tx.Exec(ctx, `
		DELETE FROM detection_campaigns
		WHERE detection_id = $1 AND detected_at = $2 AND campaign_id = $3`,
		detectionID, detectedAt, oldCampaignID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (detection_id, detected_at, campaign_id)
		DO UPDATE SET commercial_id = EXCLUDED.commercial_id, category = EXCLUDED.category`,
		detectionID, detectedAt, newCampaignID, newCommercialID, category)
	return err
}

// RestoreDisplacedShorterCut implements the reject-path recovery of §18.2.2 v2.
//
// When `rejectedID` (a longer cut, e.g. the 30s) is audit-rejected, the live
// matcher almost always counted a real SHORTER airing as the longer cut: v1
// retracted the shorter cut (the 15s) in favour of the longer one by duration,
// and now the longer one fails the §9.9 audit because the clip is actually the
// shorter cut's audio. Without this, the real airing vanishes (the shorter cut
// stays retracted, the longer cut is rejected — counted nowhere). Measured at
// 104/110 such losses being recoverable across clients (2026-06-19 audit).
//
// We find the shorter sibling cut of the SAME client that v1 retracted within
// the broadcast window (±windowSeconds) on the same station AND that already
// passed its own §9.9 audit (evidence_status='available' — i.e. it is a proven
// match of the shorter master), and clear its retracted_at so the genuine airing
// counts again. No new row, no re-upload, no double-count (the rejected longer
// cut stays audit_rejected). Returns the restored id+short_id, or (nil,0,nil)
// when there is nothing to restore — the shorter cut was also rejected, or the
// retraction was a legitimate same-cut duplicate (its displacer is not a longer
// rejected cut, so it is not matched here).
//
// detected_at is bounded on both the rejected row and the target row for
// partition pruning (detections is partitioned by detected_at).
//
// A linha restaurada VOLTA pro conjunto aprovado, então a vaga dela na cota do
// dia tem que ser retomada — a célula-dia é refechada junto (mutateApprovedSet).
// Por isso a escolha do candidato virou um SELECT separado do UPDATE: o
// refechamento precisa do id da linha ANTES de escrever (o advisory lock da
// célula tem que ser tomado antes de qualquer lock de linha — ver
// lockCellDayKeys), e com um UPDATE ... RETURNING o id só apareceria depois.
//
// A janela entre o SELECT e o UPDATE é a única coisa que este caminho perdeu ao
// deixar de ser um statement só, e é fechada aqui: o guard `retracted_at IS NOT
// NULL` no UPDATE detecta a corrida (outro caminho des-retratou a linha no
// meio), e ZERO linhas casadas devolve (nil, 0, nil) — exatamente o que o
// ErrNoRows do statement único devolvia. Isso importa porque o chamador
// (evidence/service.go) usa o nil pra cair no recoverRejectedByCoverage; se
// respondêssemos "restaurei" numa restauração que não foi nossa, o fallthrough
// sumiria e o restored_on_reject contaria uma recuperação inexistente.
// A transação inteira sofre rollback nesse caso: não há nada pra refechar.
func (d *Detections) RestoreDisplacedShorterCut(ctx context.Context,
	rejectedID uuid.UUID, detectedAt time.Time, stationID uuid.UUID,
) (restoredID *uuid.UUID, restoredShortID int32, err error) {
	const windowSeconds = 60
	var (
		id      uuid.UUID
		short   int32
		restoAt time.Time
	)
	err = d.pool.QueryRow(ctx, `
		WITH rej AS (
		    SELECT m.client_id, m.duration_seconds
		    FROM detections d
		    JOIN materials m ON m.id = d.commercial_id
		    WHERE d.id = $1 AND d.detected_at = $2
		)
		SELECT t.id, t.detected_at, tm.short_id
		FROM detections t
		JOIN materials tm ON tm.id = t.commercial_id
		CROSS JOIN rej
		WHERE t.station_id = $3
		  AND t.retracted_at IS NOT NULL
		  AND t.evidence_status = 'available'
		  AND tm.client_id = rej.client_id
		  AND tm.duration_seconds < rej.duration_seconds
		  AND t.detected_at BETWEEN $2 - ($4 * interval '1 second')
		                        AND $2 + ($4 * interval '1 second')
		ORDER BY abs(extract(epoch FROM t.detected_at - $2)) ASC,
		         t.audit_coverage DESC NULLS LAST
		LIMIT 1`,
		rejectedID, detectedAt, stationID, windowSeconds,
	).Scan(&id, &restoAt, &short)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}

	if err := d.mutateApprovedSet(ctx, id, &restoAt, func(ctx context.Context, tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE detections SET retracted_at = NULL
			WHERE id = $1 AND detected_at = $2 AND retracted_at IS NOT NULL`, id, restoAt)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return errRestoreLostRace
		}
		return nil
	}); err != nil {
		if errors.Is(err, errRestoreLostRace) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	return &id, short, nil
}

// errRestoreLostRace sai do apply do RestoreDisplacedShorterCut quando o UPDATE
// guardado não casa nenhuma linha — a linha escolhida pelo SELECT deixou de
// estar retratada antes da escrita. Serve só pra abortar a transação (não há o
// que refechar) e sinalizar "não restaurei nada" pro topo da função, que o
// traduz em (nil, 0, nil). NUNCA escapa pro chamador: é detalhe interno do
// protocolo com mutateApprovedSet, não um erro de verdade.
var errRestoreLostRace = errors.New("catalog: restore candidate left the retracted set before the update")

type ListFilter struct {
	CampaignID *uuid.UUID
	StationID  *uuid.UUID
	StartDate  *time.Time
	EndDate    *time.Time
	Limit      int
	Offset     int
	// ClientIDs, quando não-nil, restringe às detecções cujas campanhas
	// pertencem a esses clientes (carteira do viewer no JWT). nil = admin.
	ClientIDs []uuid.UUID
}

// ListPagedFilter mirrors ListFilter but with page-based pagination and an
// optional case/accent-insensitive search over station/material/type/client
// text fields. Separate from ListFilter because the paginated path returns a
// different shape (ListPagedResult); keeping the types distinct avoids
// breaking the unpaginated consumers (DayDetailModal).
type ListPagedFilter struct {
	// CampaignIDs restringe às detecções dessas campanhas. nil = sem recorte
	// por campanha (a lista sem filtro do admin). Slice VAZIO ≠ nil: encodado
	// como '{}', casa zero linhas — é a leitura correta de "seleção vazia".
	CampaignIDs []uuid.UUID
	StartDate   *time.Time
	EndDate     *time.Time
	Q           string // free text; empty disables the filter
	Sort        string // "detected_at_desc" (default) | "detected_at_asc"
	Page        int    // 1-based
	PageSize    int    // 1..200
	// ClientIDs, quando não-nil, restringe às detecções cujas campanhas
	// pertencem a esses clientes (carteira do viewer no JWT). nil = admin.
	ClientIDs []uuid.UUID
}

// ListPagedResult is the wire format returned to the frontend. Total is a
// separate count(*) so the paginator can render "X of N" + last-page jump.
type ListPagedResult struct {
	Data       []DetectionEnriched `json:"data"`
	Page       int                 `json:"page"`
	PageSize   int                 `json:"page_size"`
	Total      int                 `json:"total"`
	TotalPages int                 `json:"total_pages"`
}

// DetectionEnriched extends Detection with the joined columns the airtime
// report card needs in one round-trip. Adding new fields is safe: JSON
// decoders ignore unknown keys, and the airtime-report card consumes a
// dedicated hook (useDetectionsPaged) that knows the shape.
type DetectionEnriched struct {
	Detection
	StationFrequencyMHz *float64 `json:"station_frequency_mhz,omitempty"`
	StationBand         *string  `json:"station_band,omitempty"`
	StationCity         *string  `json:"station_city,omitempty"`
	StationState        *string  `json:"station_state,omitempty"`
	StationLogoURL      *string  `json:"station_logo_url,omitempty"`
	StationPMM          *float64 `json:"station_pmm,omitempty"`
	// StationPMMTarget é o PMM no target do CLIENTE DONO da campanha desta
	// atribuição, resolvido por (cmp.client_id × station_id). nil = sem cadastro.
	StationPMMTarget    *int       `json:"station_pmm_target"`
	MaterialDurationSec *float64   `json:"material_duration_sec,omitempty"`
	MaterialTypeName    *string    `json:"material_type_name,omitempty"`
	MaterialTypeColor   *string    `json:"material_type_color,omitempty"`
	ClientID            *uuid.UUID `json:"client_id,omitempty"`
	ClientName          *string    `json:"client_name,omitempty"`
	// CampaignName é o nome da campanha desta atribuição. Existe porque
	// /reports/airtime aceita várias campanhas de uma vez e a linha precisa
	// dizer de qual delas a veiculação veio — o mesmo que o feed do
	// /management faz.
	CampaignName *string `json:"campaign_name,omitempty"`
	// StationShortID é o identificador curto e estável da emissora
	// (stations.short_id). Vira a coluna "Identificador" do CSV detalhado, que
	// espelha o layout do relatório do fornecedor.
	StationShortID *int32 `json:"station_short_id,omitempty"`
	// UnitPrice é o valor unitário contratado pra (campanha, emissora, tipo de
	// material). Só existe quando o pricing da emissora está em modo
	// `per_insertion` — em `consolidated` não há valor por inserção, e o campo
	// fica nil.
	UnitPrice *float64 `json:"unit_price,omitempty"`
}

// ListPaged is the cronological detection list backing /reports/airtime.
// Performs a single query with COUNT(*) OVER () for total. Excludes ignored
// and retracted rows so the airtime report matches what daily_play_summary
// counts.
func (d *Detections) ListPaged(ctx context.Context, f ListPagedFilter) (*ListPagedResult, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 || f.PageSize > 200 {
		f.PageSize = 10
	}
	order := "DESC"
	if f.Sort == "detected_at_asc" {
		order = "ASC"
	}

	var qTokens any = nil
	if q := strings.TrimSpace(f.Q); q != "" {
		toks := strings.Fields(q)
		if len(toks) > 4 {
			toks = toks[:4]
		}
		qTokens = toks
	}

	sql := `
		SELECT d.id, d.station_id, COALESCE(s.name, ''), d.commercial_id, COALESCE(m.title, c.title, ''),
		       d.campaign_id, d.detected_at,
		       d.match_start_offset_ms, d.match_end_offset_ms, d.confidence, d.hash_count,
		       d.temporal_coverage, d.variant_used, d.rate_used,
		       d.evidence_status, d.evidence_key, d.evidence_size_bytes, d.category,
		       m.type_id, d.retracted_at, d.ignored_at, d.ignored_by,
		       d.manual_at, d.manual_by, d.manual_note, d.created_at,
		       s.frequency_mhz, s.band, s.city, s.state, s.logo_url, s.pmm, cst.pmm_target,
		       m.duration_seconds, mt.name, mt.color,
		       cmp.client_id, cli.name, cmp.name,
		       COUNT(*) OVER () AS total
		FROM detection_attributions d
		LEFT JOIN stations s        ON s.id = d.station_id
		LEFT JOIN commercials c     ON c.id = d.commercial_id
		LEFT JOIN materials m       ON m.id = d.commercial_id
		LEFT JOIN material_types mt ON mt.id = m.type_id
		LEFT JOIN campaigns cmp     ON cmp.id = d.campaign_id
		LEFT JOIN clients cli       ON cli.id = cmp.client_id
		LEFT JOIN client_station_pmm cst
		       ON cst.client_id = cmp.client_id AND cst.station_id = d.station_id
		WHERE ($1::uuid[] IS NULL OR d.campaign_id = ANY($1))
		  AND ($2::timestamptz IS NULL OR d.detected_at >= $2)
		  AND ($3::timestamptz IS NULL OR d.detected_at <= $3)
		  AND ($7::uuid[] IS NULL OR cmp.client_id = ANY($7))
		  AND d.ignored_at IS NULL
		  AND d.retracted_at IS NULL
		  AND d.evidence_status <> 'audit_rejected'
		  AND ($4::text[] IS NULL OR (
		      SELECT bool_and(
		          unaccent(lower(
		              COALESCE(s.name,'') || ' ' || COALESCE(s.city,'') || ' ' ||
		              COALESCE(s.state,'') || ' ' || COALESCE(s.band,'') || ' ' ||
		              COALESCE(s.frequency_mhz::text,'') || ' ' ||
		              COALESCE(m.title, c.title, '') || ' ' || COALESCE(mt.name,'') || ' ' ||
		              COALESCE(cli.name,'')
		          )) LIKE '%' || unaccent(lower(tok)) || '%'
		      )
		      FROM unnest($4::text[]) AS tok
		  ))
		ORDER BY d.detected_at ` + order + `
		LIMIT $5 OFFSET $6`

	offset := (f.Page - 1) * f.PageSize
	rows, err := d.pool.Query(ctx, sql,
		f.CampaignIDs, f.StartDate, f.EndDate, qTokens, f.PageSize, offset, f.ClientIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var (
		out   []DetectionEnriched
		total int
	)
	for rows.Next() {
		var det DetectionEnriched
		if err := rows.Scan(&det.ID, &det.StationID, &det.StationName, &det.CommercialID, &det.CommercialName,
			&det.CampaignID, &det.DetectedAt, &det.MatchStartOffsetMs, &det.MatchEndOffsetMs,
			&det.Confidence, &det.HashCount, &det.TemporalCoverage, &det.VariantUsed,
			&det.RateUsed, &det.EvidenceStatus, &det.EvidenceKey,
			&det.EvidenceSizeBytes, &det.Category, &det.TypeID, &det.RetractedAt,
			&det.IgnoredAt, &det.IgnoredBy,
			&det.ManualAt, &det.ManualBy, &det.ManualNote, &det.CreatedAt,
			&det.StationFrequencyMHz, &det.StationBand, &det.StationCity, &det.StationState,
			&det.StationLogoURL, &det.StationPMM, &det.StationPMMTarget,
			&det.MaterialDurationSec, &det.MaterialTypeName, &det.MaterialTypeColor,
			&det.ClientID, &det.ClientName, &det.CampaignName,
			&total); err != nil {
			return nil, err
		}
		out = append(out, det)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	totalPages := (total + f.PageSize - 1) / f.PageSize
	if totalPages < 1 {
		totalPages = 1
	}
	if out == nil {
		out = []DetectionEnriched{}
	}
	return &ListPagedResult{
		Data:       out,
		Page:       f.Page,
		PageSize:   f.PageSize,
		Total:      total,
		TotalPages: totalPages,
	}, nil
}

func (d *Detections) List(ctx context.Context, f ListFilter) ([]Detection, error) {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 100
	}
	rows, err := d.pool.Query(ctx, `
		SELECT d.id, d.station_id, COALESCE(s.name, ''), d.commercial_id, COALESCE(m.title, c.title, ''),
		       d.campaign_id, d.detected_at,
		       d.match_start_offset_ms, d.match_end_offset_ms, d.confidence, d.hash_count,
		       d.temporal_coverage, d.variant_used, d.rate_used,
		       d.evidence_status, d.evidence_key, d.evidence_size_bytes, d.category,
		       m.type_id, d.retracted_at, d.ignored_at, d.ignored_by,
		       d.manual_at, d.manual_by, d.manual_note, d.created_at
		FROM detection_attributions d
		LEFT JOIN stations s ON s.id = d.station_id
		LEFT JOIN commercials c ON c.id = d.commercial_id
		LEFT JOIN materials m ON m.id = d.commercial_id
		LEFT JOIN campaigns cmp ON cmp.id = d.campaign_id
		WHERE ($1::uuid IS NULL OR d.campaign_id = $1)
		  AND ($2::uuid IS NULL OR d.station_id = $2)
		  AND ($3::timestamptz IS NULL OR d.detected_at >= $3)
		  AND ($4::timestamptz IS NULL OR d.detected_at <= $4)
		  AND ($7::uuid[] IS NULL OR cmp.client_id = ANY($7))
		  AND `+ApprovedDetectionsFilter+`
		ORDER BY d.detected_at DESC
		LIMIT $5 OFFSET $6`,
		f.CampaignID, f.StationID, f.StartDate, f.EndDate, f.Limit, f.Offset, f.ClientIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Detection
	for rows.Next() {
		var det Detection
		if err := rows.Scan(&det.ID, &det.StationID, &det.StationName, &det.CommercialID, &det.CommercialName,
			&det.CampaignID, &det.DetectedAt, &det.MatchStartOffsetMs, &det.MatchEndOffsetMs,
			&det.Confidence, &det.HashCount, &det.TemporalCoverage, &det.VariantUsed,
			&det.RateUsed, &det.EvidenceStatus, &det.EvidenceKey,
			&det.EvidenceSizeBytes, &det.Category, &det.TypeID, &det.RetractedAt,
			&det.IgnoredAt, &det.IgnoredBy,
			&det.ManualAt, &det.ManualBy, &det.ManualNote, &det.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, det)
	}
	return out, rows.Err()
}

// IterateForExport streams enriched detections without paging, invoking the
// callback once per row. Stops if cb returns an error. Uses the same WHERE
// clause as ListPaged so filters/q behave identically.
//
// ATENÇÃO — esta é a única leitura de detections SEM recorte por cliente: ela
// ignora f.ClientIDs de propósito, porque só roda em rotas admin-only
// (/detections/export e o bundle do pós-venda, ver router.go). Mover esta
// função pro subgrupo viewer-friendly do router serviria TODOS os clientes pra
// qualquer um — se for preciso expô-la ao cliente, aplique o mesmo
// `($N::uuid[] IS NULL OR cmp.client_id = ANY($N))` que ListPaged usa.
func (d *Detections) IterateForExport(ctx context.Context, f ListPagedFilter,
	cb func(DetectionEnriched) error) error {
	var qTokens any = nil
	if q := strings.TrimSpace(f.Q); q != "" {
		toks := strings.Fields(q)
		if len(toks) > 4 {
			toks = toks[:4]
		}
		qTokens = toks
	}
	order := "DESC"
	if f.Sort == "detected_at_asc" {
		order = "ASC"
	}

	rows, err := d.pool.Query(ctx, `
		SELECT d.id, d.station_id, COALESCE(s.name, ''), d.commercial_id, COALESCE(m.title, c.title, ''),
		       d.campaign_id, d.detected_at,
		       d.match_start_offset_ms, d.match_end_offset_ms, d.confidence, d.hash_count,
		       d.temporal_coverage, d.variant_used, d.rate_used,
		       d.evidence_status, d.evidence_key, d.evidence_size_bytes, d.category,
		       m.type_id, d.retracted_at, d.ignored_at, d.ignored_by,
		       d.manual_at, d.manual_by, d.manual_note, d.created_at,
		       s.frequency_mhz, s.band, s.city, s.state, s.logo_url, s.pmm, cst.pmm_target,
		       m.duration_seconds, mt.name, mt.color,
		       cmp.client_id, cli.name,
		       s.short_id,
		       CASE WHEN csp.mode = 'per_insertion' THEN cstp.unit_value END
		FROM detection_attributions d
		LEFT JOIN stations s        ON s.id = d.station_id
		LEFT JOIN commercials c     ON c.id = d.commercial_id
		LEFT JOIN materials m       ON m.id = d.commercial_id
		LEFT JOIN material_types mt ON mt.id = m.type_id
		LEFT JOIN campaigns cmp     ON cmp.id = d.campaign_id
		LEFT JOIN clients cli       ON cli.id = cmp.client_id
		LEFT JOIN client_station_pmm cst
		       ON cst.client_id = cmp.client_id AND cst.station_id = d.station_id
		-- Preço unitário da coluna "Preço" do CSV detalhado. O CASE pelo modo é
		-- o guard que a 0022_pricing delega à app: não há FK cruzando
		-- campaign_station_pricing e campaign_station_type_pricing, então uma
		-- linha órfã de type_pricing não pode virar cobrança no relatório.
		LEFT JOIN campaign_station_pricing csp
		       ON csp.campaign_id = d.campaign_id AND csp.station_id = d.station_id
		LEFT JOIN campaign_station_type_pricing cstp
		       ON cstp.campaign_id = d.campaign_id
		      AND cstp.station_id  = d.station_id
		      AND cstp.type_id     = m.type_id
		WHERE ($1::uuid[] IS NULL OR d.campaign_id = ANY($1))
		  AND ($2::timestamptz IS NULL OR d.detected_at >= $2)
		  AND ($3::timestamptz IS NULL OR d.detected_at <= $3)
		  AND d.ignored_at IS NULL
		  AND d.retracted_at IS NULL
		  AND d.evidence_status <> 'audit_rejected'
		  AND ($4::text[] IS NULL OR (
		      SELECT bool_and(
		          unaccent(lower(
		              COALESCE(s.name,'') || ' ' || COALESCE(s.city,'') || ' ' ||
		              COALESCE(s.state,'') || ' ' || COALESCE(s.band,'') || ' ' ||
		              COALESCE(s.frequency_mhz::text,'') || ' ' ||
		              COALESCE(m.title, c.title, '') || ' ' || COALESCE(mt.name,'') || ' ' ||
		              COALESCE(cli.name,'')
		          )) LIKE '%' || unaccent(lower(tok)) || '%'
		      )
		      FROM unnest($4::text[]) AS tok
		  ))
		ORDER BY d.detected_at `+order,
		f.CampaignIDs, f.StartDate, f.EndDate, qTokens)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var det DetectionEnriched
		if err := rows.Scan(&det.ID, &det.StationID, &det.StationName, &det.CommercialID, &det.CommercialName,
			&det.CampaignID, &det.DetectedAt, &det.MatchStartOffsetMs, &det.MatchEndOffsetMs,
			&det.Confidence, &det.HashCount, &det.TemporalCoverage, &det.VariantUsed,
			&det.RateUsed, &det.EvidenceStatus, &det.EvidenceKey,
			&det.EvidenceSizeBytes, &det.Category, &det.TypeID, &det.RetractedAt,
			&det.IgnoredAt, &det.IgnoredBy,
			&det.ManualAt, &det.ManualBy, &det.ManualNote, &det.CreatedAt,
			&det.StationFrequencyMHz, &det.StationBand, &det.StationCity, &det.StationState,
			&det.StationLogoURL, &det.StationPMM, &det.StationPMMTarget,
			&det.MaterialDurationSec, &det.MaterialTypeName, &det.MaterialTypeColor,
			&det.ClientID, &det.ClientName,
			&det.StationShortID, &det.UnitPrice); err != nil {
			return err
		}
		if err := cb(det); err != nil {
			return err
		}
	}
	return rows.Err()
}

// MaterialAggregateRow is one entry of the airtime-report sidebar panel.
type MaterialAggregateRow struct {
	MaterialID          uuid.UUID  `json:"material_id"`
	MaterialShortID     *int32     `json:"material_short_id,omitempty"`
	MaterialTitle       string     `json:"material_title"`
	MaterialDurationSec *float64   `json:"material_duration_sec,omitempty"`
	MaterialTypeID      *uuid.UUID `json:"material_type_id,omitempty"`
	MaterialTypeName    *string    `json:"material_type_name,omitempty"`
	MaterialTypeColor   *string    `json:"material_type_color,omitempty"`
	Count               int        `json:"count"`
}

// MaterialAggregateResult is the wire format of /aggregate-by-material.
type MaterialAggregateResult struct {
	Data              []MaterialAggregateRow `json:"data"`
	TotalDetections   int                    `json:"total_detections"`
	DistinctMaterials int                    `json:"distinct_materials"`
}

// AggregateFilter is the query input — at least one campaign is required, the
// rest mirror ListPagedFilter so the panel stays consistent with the list.
type AggregateFilter struct {
	// CampaignIDs é obrigatório (>= 1). O painel de /reports/airtime manda a
	// seleção inteira; o pós-venda manda uma campanha só.
	CampaignIDs []uuid.UUID
	StartDate   *time.Time
	EndDate     *time.Time
	Q           string
	// ClientIDs, quando não-nil, restringe às detecções cujas campanhas
	// pertencem a esses clientes (carteira do viewer no JWT). nil = admin.
	// Vira filtro SQL em AggregateByMaterial: com N campanhas na seleção,
	// checar posse uma a uma no handler custaria N round-trips, e o filtro no
	// WHERE é o mesmo que ListPaged já aplica — campanha fora da carteira não
	// soma nada.
	ClientIDs []uuid.UUID
}

// AggregateByMaterial counts non-ignored, non-retracted detections grouped by
// material for the airtime-report sidebar panel. Same WHERE clause as
// ListPaged so the panel matches the list under any filter combination.
func (d *Detections) AggregateByMaterial(ctx context.Context, f AggregateFilter) (*MaterialAggregateResult, error) {
	var qTokens any = nil
	if q := strings.TrimSpace(f.Q); q != "" {
		toks := strings.Fields(q)
		if len(toks) > 4 {
			toks = toks[:4]
		}
		qTokens = toks
	}

	rows, err := d.pool.Query(ctx, `
		SELECT d.commercial_id, m.short_id, COALESCE(m.title, c.title, ''),
		       m.duration_seconds, m.type_id, mt.name, mt.color,
		       COUNT(*) AS cnt
		FROM detection_attributions d
		LEFT JOIN commercials c     ON c.id = d.commercial_id
		LEFT JOIN materials m       ON m.id = d.commercial_id
		LEFT JOIN material_types mt ON mt.id = m.type_id
		LEFT JOIN stations s        ON s.id = d.station_id
		LEFT JOIN campaigns cmp     ON cmp.id = d.campaign_id
		LEFT JOIN clients cli       ON cli.id = cmp.client_id
		WHERE d.campaign_id = ANY($1)
		  AND ($2::timestamptz IS NULL OR d.detected_at >= $2)
		  AND ($3::timestamptz IS NULL OR d.detected_at <= $3)
		  AND ($5::uuid[] IS NULL OR cmp.client_id = ANY($5))
		  AND d.ignored_at IS NULL
		  AND d.retracted_at IS NULL
		  AND d.evidence_status <> 'audit_rejected'
		  AND ($4::text[] IS NULL OR (
		      SELECT bool_and(
		          unaccent(lower(
		              COALESCE(s.name,'') || ' ' || COALESCE(s.city,'') || ' ' ||
		              COALESCE(s.state,'') || ' ' || COALESCE(s.band,'') || ' ' ||
		              COALESCE(s.frequency_mhz::text,'') || ' ' ||
		              COALESCE(m.title, c.title, '') || ' ' || COALESCE(mt.name,'') || ' ' ||
		              COALESCE(cli.name,'')
		          )) LIKE '%' || unaccent(lower(tok)) || '%'
		      )
		      FROM unnest($4::text[]) AS tok
		  ))
		GROUP BY d.commercial_id, m.short_id, m.title, c.title, m.duration_seconds, m.type_id, mt.name, mt.color
		ORDER BY cnt DESC, c.title ASC`,
		f.CampaignIDs, f.StartDate, f.EndDate, qTokens, f.ClientIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var (
		out   []MaterialAggregateRow
		total int
	)
	for rows.Next() {
		var r MaterialAggregateRow
		if err := rows.Scan(&r.MaterialID, &r.MaterialShortID, &r.MaterialTitle,
			&r.MaterialDurationSec, &r.MaterialTypeID, &r.MaterialTypeName, &r.MaterialTypeColor,
			&r.Count); err != nil {
			return nil, err
		}
		out = append(out, r)
		total += r.Count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []MaterialAggregateRow{}
	}
	return &MaterialAggregateResult{
		Data:              out,
		TotalDetections:   total,
		DistinctMaterials: len(out),
	}, nil
}

// Get returns a single detection by id for the /detections/:id detail page.
//
// EXCEÇÃO DELIBERADA ao ApprovedDetectionsFilter: NÃO filtra retracted/ignored/
// audit_rejected — o operador precisa abrir uma veiculação retratada/ignorada/
// rejeitada pra inspecioná-la (a página renderiza os badges de estado). Isto é
// detalhe forense de UMA linha, não um agregado contável.
func (d *Detections) Get(ctx context.Context, id uuid.UUID) (*Detection, error) {
	var det Detection
	err := d.pool.QueryRow(ctx, `
		SELECT d.id, d.station_id, COALESCE(s.name, ''), d.commercial_id, COALESCE(m.title, c.title, ''),
		       d.campaign_id, d.detected_at,
		       d.match_start_offset_ms, d.match_end_offset_ms, d.confidence, d.hash_count,
		       d.temporal_coverage, d.audit_coverage, d.variant_used, d.rate_used,
		       d.evidence_status, d.evidence_key, d.evidence_size_bytes, d.category,
		       m.type_id, d.retracted_at, d.ignored_at, d.ignored_by,
		       d.manual_at, d.manual_by, d.manual_note, m.script, d.created_at,
		       d.proof_batch_id
		FROM detections d
		LEFT JOIN stations s ON s.id = d.station_id
		LEFT JOIN commercials c ON c.id = d.commercial_id
		LEFT JOIN materials m ON m.id = d.commercial_id
		WHERE d.id = $1`, id,
	).Scan(&det.ID, &det.StationID, &det.StationName, &det.CommercialID, &det.CommercialName,
		&det.CampaignID, &det.DetectedAt,
		&det.MatchStartOffsetMs, &det.MatchEndOffsetMs, &det.Confidence, &det.HashCount,
		&det.TemporalCoverage, &det.AuditCoverage, &det.VariantUsed, &det.RateUsed,
		&det.EvidenceStatus, &det.EvidenceKey, &det.EvidenceSizeBytes, &det.Category, &det.TypeID,
		&det.RetractedAt, &det.IgnoredAt, &det.IgnoredBy,
		&det.ManualAt, &det.ManualBy, &det.ManualNote, &det.CommercialScript, &det.CreatedAt,
		&det.ProofBatchID)
	if err != nil {
		return nil, err
	}
	return &det, nil
}

// Ignore stamps ignored_at = now() and ignored_by = userID on the detection
// so daily_play_summary excludes it from aggregates. Idempotent — a second
// call updates the timestamp but keeps the row in the ignored state.
//
// A tocada SAI do conjunto aprovado, então a célula-dia é refechada na mesma
// transação: a vaga que ela ocupava na cota passa pra próxima excedente
// (mutateApprovedSet).
func (d *Detections) Ignore(ctx context.Context, id, userID uuid.UUID) error {
	return d.mutateApprovedSet(ctx, id, nil, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE detections SET ignored_at = now(), ignored_by = $2 WHERE id = $1`,
			id, userID)
		return err
	})
}

// Restore clears ignored_at / ignored_by so the detection counts again. No-op
// when the row was never ignored.
//
// A tocada VOLTA pro conjunto aprovado: refecha a célula-dia pra ela retomar o
// lugar cronológico dela na cota (e rebaixar quem tinha ocupado a vaga).
func (d *Detections) Restore(ctx context.Context, id uuid.UUID) error {
	return d.mutateApprovedSet(ctx, id, nil, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE detections SET ignored_at = NULL, ignored_by = NULL WHERE id = $1`,
			id)
		return err
	})
}

// ──────────────────────────────────────────────────────────────────────────
// Campaign reports (CSV consolidated + PDF summary)
// ──────────────────────────────────────────────────────────────────────────

// MaterialStationRow é a granularidade do relatório consolidado: uma linha
// por (material × emissora) com o total de veiculações no período. Inclui
// metadata leve da emissora pra o CSV ficar legível sem JOIN no front, e
// um breakdown por categoria (in_slot/out_slot/out_date/bonus) pra
// fechamento comercial saber quantas veiculações foram bonificação,
// fora-faixa etc. dentro de cada combinação material × emissora.
type MaterialStationRow struct {
	MaterialID          uuid.UUID `json:"material_id"`
	MaterialShortID     *int32    `json:"material_short_id,omitempty"`
	MaterialTitle       string    `json:"material_title"`
	MaterialDurationSec *float64  `json:"material_duration_sec,omitempty"`
	MaterialTypeName    *string   `json:"material_type_name,omitempty"`
	StationID           uuid.UUID `json:"station_id"`
	StationName         string    `json:"station_name"`
	StationBand         *string   `json:"station_band,omitempty"`
	StationFrequencyMHz *float64  `json:"station_frequency_mhz,omitempty"`
	StationCity         *string   `json:"station_city,omitempty"`
	StationState        *string   `json:"station_state,omitempty"`
	// PMM total da emissora e PMM no target do cliente dono da campanha.
	// StationPMMTarget nil = par (cliente, emissora) não cadastrado — a
	// célula do relatório fica vazia, que é diferente de zero.
	StationPMM       *float64 `json:"station_pmm,omitempty"`
	StationPMMTarget *int     `json:"station_pmm_target"`
	Count            int      `json:"count"`
	// Breakdown por status — soma sempre == Count.
	InSlotCount  int `json:"in_slot_count"`
	OutSlotCount int `json:"out_slot_count"`
	OutDateCount int `json:"out_date_count"`
	// BonusCount é a bonificação (categoria `bonus`, ex-`orphan`). O nome do
	// campo JSON continua `orphan_count` de propósito: frontend/src/utils/
	// pdfReport.js lê essa chave, e renomeá-la aqui zeraria a coluna "Bônus"
	// do PDF sem erro nenhum. Mesmo trato do `extras_orphan` em insights.go.
	BonusCount int `json:"orphan_count"`
	// ImpactCount é a BASE CANÔNICA DE IMPACTOS: in_slot + bonus. É ela que
	// multiplica o PMM em todo relatório — nunca Count (que inclui out_slot e
	// out_date). Ver docs/features/client-target-pmm.md: out_slot não vale nada
	// comercialmente (D3 da categorização por cota) e out_date está fora do
	// período contratado, então nenhum dos dois é impacto entregue ao cliente.
	// Vem do SQL (não é InSlotCount+BonusCount somado em Go) pra que a base
	// exista até para consumidores que ignoram o breakdown.
	ImpactCount     int       `json:"impact_count"`
	FirstDetectedAt time.Time `json:"first_detected_at"`
	LastDetectedAt  time.Time `json:"last_detected_at"`
}

// AggregateByMaterialStation agrupa as veiculações da campanha por
// (material × emissora). Usa o mesmo WHERE da lista para que o relatório
// consolidado bata exatamente com o que o usuário vê em /reports/airtime
// e /detections sob os mesmos filtros.
func (d *Detections) AggregateByMaterialStation(ctx context.Context, f AggregateFilter) ([]MaterialStationRow, error) {
	rows, err := d.pool.Query(ctx, `
		SELECT d.commercial_id, m.short_id, COALESCE(m.title, c.title, ''),
		       m.duration_seconds, mt.name,
		       d.station_id, COALESCE(s.name, ''),
		       s.band, s.frequency_mhz, s.city, s.state,
		       s.pmm, cst.pmm_target,
		       COUNT(*) AS cnt,
		       COUNT(*) FILTER (WHERE d.category = 'in_slot')  AS in_slot_count,
		       COUNT(*) FILTER (WHERE d.category = 'out_slot') AS out_slot_count,
		       COUNT(*) FILTER (WHERE d.category = 'out_date') AS out_date_count,
		       -- Bonificação aceita o sinônimo legado 'orphan' (BonusCategoriesSQL):
		       -- este breakdown promete "soma == Count", e uma linha gravada pelo
		       -- binário antigo na janela de deploy sumiria das quatro colunas,
		       -- quebrando a soma justo no relatório que o cliente recebe.
		       COUNT(*) FILTER (WHERE d.category IN `+categorizer.BonusCategoriesSQL+`) AS bonus_count,
		       -- Base canônica de impactos (in_slot + bonus) — ver ImpactCount.
		       COUNT(*) FILTER (WHERE d.category = 'in_slot'
		                           OR d.category IN `+categorizer.BonusCategoriesSQL+`) AS impact_count,
		       MIN(d.detected_at), MAX(d.detected_at)
		FROM detection_attributions d
		LEFT JOIN commercials c     ON c.id = d.commercial_id
		LEFT JOIN materials m       ON m.id = d.commercial_id
		LEFT JOIN material_types mt ON mt.id = m.type_id
		LEFT JOIN stations s        ON s.id = d.station_id
		LEFT JOIN campaigns cmp     ON cmp.id = d.campaign_id
		LEFT JOIN client_station_pmm cst
		       ON cst.client_id = cmp.client_id AND cst.station_id = d.station_id
		WHERE d.campaign_id = ANY($1)
		  AND ($2::timestamptz IS NULL OR d.detected_at >= $2)
		  AND ($3::timestamptz IS NULL OR d.detected_at <= $3)
		  AND d.ignored_at IS NULL
		  AND d.retracted_at IS NULL
		  AND d.evidence_status <> 'audit_rejected'
		GROUP BY d.commercial_id, m.short_id, m.title, c.title, m.duration_seconds, mt.name,
		         d.station_id, s.name, s.band, s.frequency_mhz, s.city, s.state,
		         s.pmm, cst.pmm_target
		ORDER BY COALESCE(m.title, c.title, '') ASC, s.name ASC`,
		f.CampaignIDs, f.StartDate, f.EndDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []MaterialStationRow{}
	for rows.Next() {
		var r MaterialStationRow
		if err := rows.Scan(
			&r.MaterialID, &r.MaterialShortID, &r.MaterialTitle,
			&r.MaterialDurationSec, &r.MaterialTypeName,
			&r.StationID, &r.StationName,
			&r.StationBand, &r.StationFrequencyMHz, &r.StationCity, &r.StationState,
			&r.StationPMM, &r.StationPMMTarget,
			&r.Count,
			&r.InSlotCount, &r.OutSlotCount, &r.OutDateCount, &r.BonusCount,
			&r.ImpactCount,
			&r.FirstDetectedAt, &r.LastDetectedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// StationAggregateRow alimenta a seção "por emissora" do PDF.
type StationAggregateRow struct {
	StationID           uuid.UUID `json:"station_id"`
	StationName         string    `json:"station_name"`
	StationBand         *string   `json:"station_band,omitempty"`
	StationFrequencyMHz *float64  `json:"station_frequency_mhz,omitempty"`
	StationCity         *string   `json:"station_city,omitempty"`
	StationState        *string   `json:"station_state,omitempty"`
	// StationPMMTarget nil = par (cliente, emissora) não cadastrado.
	StationPMM       *float64 `json:"station_pmm,omitempty"`
	StationPMMTarget *int     `json:"station_pmm_target"`
	Count            int      `json:"count"`
	// ImpactCount é a BASE CANÔNICA DE IMPACTOS: in_slot + bonus. Count é o
	// total de veiculações (as quatro categorias) e continua alimentando a
	// coluna "Total" do relatório; quem multiplica PMM usa ImpactCount.
	// Ver MaterialStationRow.ImpactCount e docs/features/client-target-pmm.md.
	ImpactCount int `json:"impact_count"`
}

// AggregateByStation devolve total de veiculações por emissora — usado tanto
// pelo PDF quanto pela seção sumária do relatório consolidado.
func (d *Detections) AggregateByStation(ctx context.Context, f AggregateFilter) ([]StationAggregateRow, error) {
	rows, err := d.pool.Query(ctx, `
		SELECT d.station_id, COALESCE(s.name, ''), s.band, s.frequency_mhz, s.city, s.state,
		       s.pmm, cst.pmm_target,
		       COUNT(*) AS cnt,
		       -- Base canônica de impactos (in_slot + bonus) — ver ImpactCount.
		       COUNT(*) FILTER (WHERE d.category = 'in_slot'
		                           OR d.category IN `+categorizer.BonusCategoriesSQL+`) AS impact_count
		FROM detection_attributions d
		LEFT JOIN stations s ON s.id = d.station_id
		LEFT JOIN campaigns cmp ON cmp.id = d.campaign_id
		LEFT JOIN client_station_pmm cst
		       ON cst.client_id = cmp.client_id AND cst.station_id = d.station_id
		WHERE d.campaign_id = ANY($1)
		  AND ($2::timestamptz IS NULL OR d.detected_at >= $2)
		  AND ($3::timestamptz IS NULL OR d.detected_at <= $3)
		  AND d.ignored_at IS NULL
		  AND d.retracted_at IS NULL
		  AND d.evidence_status <> 'audit_rejected'
		GROUP BY d.station_id, s.name, s.band, s.frequency_mhz, s.city, s.state,
		         s.pmm, cst.pmm_target
		ORDER BY cnt DESC, s.name ASC`,
		f.CampaignIDs, f.StartDate, f.EndDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []StationAggregateRow{}
	for rows.Next() {
		var r StationAggregateRow
		if err := rows.Scan(
			&r.StationID, &r.StationName, &r.StationBand, &r.StationFrequencyMHz,
			&r.StationCity, &r.StationState,
			&r.StationPMM, &r.StationPMMTarget,
			&r.Count, &r.ImpactCount,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetClientID returns the client_id of the campaign that owns the detection.
// Used by handlers to verify viewer scope without modifying the Get signature.
// Returns pgx.ErrNoRows when the detection does not exist.
func (d *Detections) GetClientID(ctx context.Context, detectionID uuid.UUID) (*uuid.UUID, error) {
	var clientID uuid.UUID
	err := d.pool.QueryRow(ctx, `
		SELECT cmp.client_id
		FROM detections det
		JOIN campaigns cmp ON cmp.id = det.campaign_id
		WHERE det.id = $1`, detectionID,
	).Scan(&clientID)
	if err != nil {
		return nil, err
	}
	return &clientID, nil
}

// SiblingDetectionRow é uma row de detection de um corte irmão encontrada na
// janela da veiculação — usada pelo reject-path (§18.2.2 v2c) pra decidir entre
// restaurar uma row retraída, pular (já contada) ou reatribuir a row rejeitada.
type SiblingDetectionRow struct {
	ID             uuid.UUID
	DetectedAt     time.Time
	Retracted      bool
	EvidenceStatus string
}

// FindSiblingDetectionInWindow devolve a row de detection MAIS PRÓXIMA do corte
// `shortID` na mesma emissora dentro de ±windowSeconds de `detectedAt`, ou nil se
// não houver. Resolução de short_id é POLIMÓRFICA (commercials ∪ materials) — sem
// isso um corte do material library casaria zero rows (regressão do incidente
// 2026-06-09). detected_at fica no WHERE pra partition pruning.
func (d *Detections) FindSiblingDetectionInWindow(ctx context.Context,
	shortID int32, stationID uuid.UUID, detectedAt time.Time, windowSeconds int,
) (*SiblingDetectionRow, error) {
	var row SiblingDetectionRow
	err := d.pool.QueryRow(ctx, `
		SELECT d.id, d.detected_at, (d.retracted_at IS NOT NULL), d.evidence_status
		FROM detections d
		WHERE d.station_id = $2
		  AND d.commercial_id IN (
		      SELECT id FROM commercials WHERE short_id = $1
		      UNION
		      SELECT id FROM materials   WHERE short_id = $1
		  )
		  AND d.detected_at >= $3::timestamptz - ($4::int * interval '1 second')
		  AND d.detected_at <= $3::timestamptz + ($4::int * interval '1 second')
		ORDER BY abs(extract(epoch FROM d.detected_at - $3::timestamptz)) ASC
		LIMIT 1`,
		shortID, stationID, detectedAt, int32(windowSeconds),
	).Scan(&row.ID, &row.DetectedAt, &row.Retracted, &row.EvidenceStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// ClearRetraction des-retrata uma detection (retracted_at = NULL). Usada pelo
// reject-path v2c pra restaurar o corte irmão deslocado de QUALQUER duração
// (o RestoreDisplacedShorterCut só cobre o estritamente mais curto). detected_at
// no WHERE pra partition pruning.
//
// A tocada VOLTA pro conjunto aprovado: refecha a célula-dia na mesma transação
// pra ela retomar seu lugar na cota do dia (mutateApprovedSet).
func (d *Detections) ClearRetraction(ctx context.Context, id uuid.UUID, detectedAt time.Time) error {
	return d.mutateApprovedSet(ctx, id, &detectedAt, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE detections SET retracted_at = NULL WHERE id = $1 AND detected_at = $2`,
			id, detectedAt)
		return err
	})
}

// RetractByID retrata uma detection (retracted_at = $3) por id+detected_at, só se
// ainda não estava retraída (idempotente — a 2a chamada é no-op). Simétrico ao
// ClearRetraction. Usado pelo co-fire guard do §18.2.2-v2 pass-path pra descartar
// a row irmã que é a MESMA veiculação do cut vencedor. NÃO mexe em
// detection_campaigns: a projeção é gateada pelo retracted_at da row base (view
// daily_play_summary CTE `actual`), então a retração in-place basta. detected_at
// no WHERE pra partition pruning.
//
// A tocada SAI do conjunto aprovado e LIBERA a vaga que ocupava na cota do dia —
// a célula-dia é refechada na mesma transação (mutateApprovedSet). É o caso do
// dedup de co-fire (evidence/service.go), que retrata uma duplicata na mesma
// célula-dia: sem o re-fechamento a vaga liberada não é reaproveitada por
// ninguém e o déficit do dia fica inflado pra sempre.
func (d *Detections) RetractByID(ctx context.Context, id uuid.UUID, detectedAt, at time.Time) error {
	return d.mutateApprovedSet(ctx, id, &detectedAt, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE detections SET retracted_at = $3
			 WHERE id = $1 AND detected_at = $2 AND retracted_at IS NULL`,
			id, detectedAt, at)
		return err
	})
}

// MarkAmbiguous estampa uma detecção como tocada de gêmeo acústico NÃO resolvida:
// o clipe casou um de dois gêmeos quase idênticos mas o trecho discriminante não
// disse qual (spec 2026-07-01, desambiguação de gêmeos). Seta
// evidence_status='ambiguous' E retrata a linha (retracted_at) — a invariante
// `ambiguous ⟺ retracted` faz a detecção não contar em LUGAR nenhum: o
// ApprovedDetectionsFilter e todas as views (daily_play_summary, projeção
// detection_campaigns) gateiam no retracted_at da row base ao vivo, então NÃO é
// preciso sincronizar detection_campaigns (mesma razão do RetractByID). COALESCE
// mantém idempotente (re-marcar não move o timestamp). A linha aguarda resolução
// manual (uma fila de revisão futura reatribui + limpa a retração). detected_at
// no WHERE pra partition pruning.
//
// A tocada SAI do conjunto aprovado (por retracted_at E por evidence_status), o
// que libera a vaga dela na cota do dia — a célula-dia é refechada na mesma
// transação (mutateApprovedSet).
func (d *Detections) MarkAmbiguous(ctx context.Context, id uuid.UUID, detectedAt time.Time) error {
	return d.mutateApprovedSet(ctx, id, &detectedAt, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE detections
			 SET evidence_status = 'ambiguous',
			     retracted_at = COALESCE(retracted_at, now())
			 WHERE id = $1 AND detected_at = $2`,
			id, detectedAt)
		return err
	})
}

// ErrReattributeNoRow sinaliza que a reatribuição não tocou nenhuma row — a
// detection alvo não estava (mais) audit_rejected. O caller trata como no-op
// seguro: a row permanece como estava (guard G3 do incidente 2026-05-17).
var ErrReattributeNoRow = errors.New("reattribute: no audit_rejected row matched")

// ReattributeRejectedDetection re-aponta uma row AUDIT_REJECTED pro corte que o
// clipe realmente cobre (§18.2.2 v2c, caso suprimido sem row irmã). Diferente do
// ReattributeDetection (pass-path), aqui a row vem do reject-path: não teve clipe
// subido (evidence_key vazio), então o status vai pra 'missing' (veiculação
// válida, sem áudio — mesma semântica de detecção manual; CONTA nos agregados).
// Tudo num UPDATE atômico guardado por evidence_status='audit_rejected':
//   - re-categoriza pro novo (campaign, corte, station, dia);
//   - seta commercial_id, campaign_id, category, evidence_status='missing', audit_coverage;
//   - se 0 rows tocadas (já não estava rejeitada), devolve ErrReattributeNoRow e
//     NÃO altera nada (G3 — falha mantém o estado, nunca orfana).
//
// Só UMA célula-dia muda aqui, ao contrário do ReattributeDetection: a row
// estava 'audit_rejected', logo FORA do conjunto aprovado
// (ApprovedDetectionsFilter) — nunca ocupou vaga na cota da célula de origem, e
// tirá-la de lá não libera nada. Ela só ENTRA na cota da célula de destino, que
// o settleCellDay abaixo fecha. Por isso um advisory lock só.
func (d *Detections) ReattributeRejectedDetection(ctx context.Context, detectionID uuid.UUID, detectedAt time.Time,
	newCommercialID, newCampaignID, stationID uuid.UUID, coverage float64) error {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Lê a campanha antiga sob o guard audit_rejected: se a row não estiver
	// rejeitada (0 rows), devolve ErrReattributeNoRow sem alterar nada (G3).
	var oldCampaignID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT campaign_id FROM detections
		WHERE id = $1 AND detected_at = $2 AND evidence_status = 'audit_rejected'`,
		detectionID, detectedAt).Scan(&oldCampaignID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrReattributeNoRow
	}
	if err != nil {
		return err
	}

	// Fecha a célula-dia de destino só DEPOIS da guarda: se a row não estava
	// rejeitada, nada foi reassentado (G3 — falha não altera estado). replacingID
	// tira a própria row do conjunto (ela vai entrar como a tocada nova).
	cat, err := d.settleCellDay(ctx, tx, CreateDetectionInput{
		StationID:    stationID,
		CommercialID: newCommercialID,
		CampaignID:   newCampaignID,
		DetectedAt:   detectedAt,
	}, &detectionID, true)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE detections
		SET commercial_id = $3, campaign_id = $4, category = $5,
		    evidence_status = 'missing', audit_coverage = $6
		WHERE id = $1 AND detected_at = $2 AND evidence_status = 'audit_rejected'`,
		detectionID, detectedAt, newCommercialID, newCampaignID, cat, coverage); err != nil {
		return err
	}
	// F-119: leva a projeção canônica junto (mesmo motivo do ReattributeDetection).
	if err := syncCanonicalProjection(ctx, tx, detectionID, detectedAt,
		oldCampaignID, newCampaignID, newCommercialID, cat); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
