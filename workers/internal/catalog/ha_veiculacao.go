package catalog

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// EscopoVeiculacao é o recorte de uma escrita do MCP para a escalada a crítico
// (plano da Fatia 3, F3-3). Campos nil/vazios = sem filtro. Pelo menos um de
// CampanhaIDs, MaterialID ou ClienteID é obrigatório (sem âncora, "existe
// veiculação?" varreria a base inteira e a resposta quase sempre seria sim).
type EscopoVeiculacao struct {
	CampanhaIDs []uuid.UUID
	ClienteID   *uuid.UUID
	MaterialID  *uuid.UUID
	TipoIDs     []uuid.UUID
	EmissoraIDs []uuid.UUID
	// DiaDe/DiaAte: datas de calendário (meia-noite UTC, como o DATE do pgx),
	// lidas como dias em America/Sao_Paulo, limites incluídos.
	DiaDe  *time.Time
	DiaAte *time.Time
}

// haVeiculacaoSQL é o recorte do recat (recatScopeByCampaignSQL): a PROJEÇÃO
// (detection_campaigns) × a tocada × o material, o dia em America/Sao_Paulo —
// com o filtro de conjunto aprovado que o recat aplica às tocadas que fecha
// (recatClassifiedCTE.plays).
//
// Por que o filtro de aprovadas entra aqui: no recat, o scope só APONTA células
// e a CTE `plays` recarrega delas só as aprovadas — uma tocada retratada,
// ignorada, audit_rejected ou ambígua não consome vaga na cota nem aparece em
// tela nenhuma. Então "o recat vai mexer em número que o cliente já viu" é
// exatamente "existe tocada APROVADA no recorte": uma célula só com retratadas
// é refechada sem nenhuma linha para reclassificar.
//
// O dia entra como intervalo [00:00 de DiaDe, 00:00 de DiaAte+1) em SP — a
// mesma conta de `plays` — e não como date_trunc(...)::date, para a poda de
// partição de detections/detection_campaigns (particionadas por detected_at).
const haVeiculacaoSQL = `
SELECT EXISTS (
    SELECT 1
    FROM detection_campaigns dc
    JOIN detections d ON d.id = dc.detection_id AND d.detected_at = dc.detected_at
    JOIN materials m  ON m.id = dc.commercial_id
    JOIN campaigns c  ON c.id = dc.campaign_id
    WHERE (cardinality($1::uuid[]) = 0 OR dc.campaign_id = ANY($1))
      AND ($2::uuid IS NULL OR c.client_id = $2)
      AND ($3::uuid IS NULL OR dc.commercial_id = $3)
      AND (cardinality($4::uuid[]) = 0 OR m.type_id = ANY($4))
      AND (cardinality($5::uuid[]) = 0 OR d.station_id = ANY($5))
      AND ($6::date IS NULL OR dc.detected_at >= ($6::date::timestamp AT TIME ZONE 'America/Sao_Paulo'))
      AND ($7::date IS NULL OR dc.detected_at <  (($7::date + 1)::timestamp AT TIME ZONE 'America/Sao_Paulo'))
      AND ($6::date IS NULL OR d.detected_at >= ($6::date::timestamp AT TIME ZONE 'America/Sao_Paulo'))
      AND ($7::date IS NULL OR d.detected_at <  (($7::date + 1)::timestamp AT TIME ZONE 'America/Sao_Paulo'))
      AND ` + ApprovedDetectionsFilter + `
)`

// HaVeiculacao diz se existe veiculação (aprovada, ver haVeiculacaoSQL) no
// recorte — o MESMO de recatScopeByCampaignSQL: se esta função diz false, o
// recat desse recorte não tem nenhuma tocada aprovada para reclassificar.
func (d *Detections) HaVeiculacao(ctx context.Context, e EscopoVeiculacao) (bool, error) {
	if len(e.CampanhaIDs) == 0 && e.MaterialID == nil && e.ClienteID == nil {
		return false, errors.New("catalog: HaVeiculacao exige CampanhaIDs, MaterialID ou ClienteID")
	}
	campanhas, tipos, emissoras := e.CampanhaIDs, e.TipoIDs, e.EmissoraIDs
	// cardinality() de NULL é NULL (e `= 0` descartaria a linha): slices nil
	// viram arrays vazios.
	if campanhas == nil {
		campanhas = []uuid.UUID{}
	}
	if tipos == nil {
		tipos = []uuid.UUID{}
	}
	if emissoras == nil {
		emissoras = []uuid.UUID{}
	}
	var ha bool
	err := d.pool.QueryRow(ctx, haVeiculacaoSQL,
		campanhas, e.ClienteID, e.MaterialID, tipos, emissoras, e.DiaDe, e.DiaAte).Scan(&ha)
	return ha, err
}
