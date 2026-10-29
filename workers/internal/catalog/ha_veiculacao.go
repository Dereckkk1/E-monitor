package catalog

import (
	"context"
	"errors"
	"fmt"
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

// haVeiculacaoSelectSQL é o recorte do recat (recatScopeByCampaignSQL): a
// PROJEÇÃO (detection_campaigns) × a tocada × o material, o dia em
// America/Sao_Paulo. Os predicados de dia ($6/$7) e o filtro de conjunto
// aprovado são anexados por montarHaVeiculacaoSQL.
//
// Por que o filtro de aprovadas: no recat, o scope só APONTA células e a CTE
// `plays` recarrega delas só as aprovadas — uma tocada retratada, ignorada,
// audit_rejected ou ambígua não consome vaga na cota nem aparece em tela
// nenhuma. Então, numa chamada ESCOPADA POR CAMPANHA, "existe tocada aprovada
// no recorte" é exatamente "o recat desse recorte tem alguma linha aprovada
// para reclassificar": uma célula só com não-aprovadas é refechada sem
// nenhuma linha a mudar (provado por TestHaVeiculacao_ParidadeComORecatReal).
const haVeiculacaoSelectSQL = `
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
      AND (cardinality($5::uuid[]) = 0 OR d.station_id = ANY($5))`

// montarHaVeiculacaoSQL anexa o predicado de dia SÓ quando o dia foi pedido.
// Um `$6::date IS NULL OR dc.detected_at >= ...` fixo impediria, num plano
// genérico, a poda das partições de detections/detection_campaigns
// (particionadas por detected_at). O dia entra como intervalo
// [00:00 de DiaDe, 00:00 de DiaAte+1) em SP — a mesma conta da CTE `plays` do
// recat —, nas duas tabelas. Os parâmetros do dia são numerados na ordem em
// que aparecem: DiaDe, quando há, é o $6 e DiaAte vira o próximo.
func montarHaVeiculacaoSQL(temDe, temAte bool) string {
	sql := haVeiculacaoSelectSQL
	n := 5
	if temDe {
		n++
		sql += fmt.Sprintf(`
      AND dc.detected_at >= ($%[1]d::date::timestamp AT TIME ZONE 'America/Sao_Paulo')
      AND d.detected_at >= ($%[1]d::date::timestamp AT TIME ZONE 'America/Sao_Paulo')`, n)
	}
	if temAte {
		n++
		sql += fmt.Sprintf(`
      AND dc.detected_at <  (($%[1]d::date + 1)::timestamp AT TIME ZONE 'America/Sao_Paulo')
      AND d.detected_at <  (($%[1]d::date + 1)::timestamp AT TIME ZONE 'America/Sao_Paulo')`, n)
	}
	return sql + `
      AND ` + ApprovedDetectionsFilter + `
)`
}

// HaVeiculacao diz se existe veiculação APROVADA (ver haVeiculacaoSelectSQL)
// no recorte.
//
// Equivalência com o recat: vale para chamadas escopadas por campanha
// (CampanhaIDs preenchido, com ou sem TipoIDs/EmissoraIDs/dias) — false
// significa que o recat desse recorte não tem nenhuma tocada aprovada para
// reclassificar. Para escopos só de MaterialID ou só de ClienteID (sem
// CampanhaIDs) o significado é mais estreito: "não há tocada aprovada DESSE
// material/cliente no recorte". Não é uma afirmação sobre tudo que o recat
// desse escopo poderia refechar (ex.: RecategorizeForMaterial refecha a célula
// inteira, com tocadas de outros materiais do mesmo tipo); a escalada que
// precisar dessa garantia mais larga deve passar CampanhaIDs.
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
	args := []any{campanhas, e.ClienteID, e.MaterialID, tipos, emissoras}
	if e.DiaDe != nil {
		args = append(args, *e.DiaDe)
	}
	if e.DiaAte != nil {
		args = append(args, *e.DiaAte)
	}
	var ha bool
	err := d.pool.QueryRow(ctx, montarHaVeiculacaoSQL(e.DiaDe != nil, e.DiaAte != nil), args...).Scan(&ha)
	return ha, err
}
