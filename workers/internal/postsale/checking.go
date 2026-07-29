// Package postsale monta, congela e entrega o relatório de pós-venda: o
// fechamento que o cliente abre por um link pessoal ao fim de uma campanha.
//
// checking.go é a parte que decide o que o cliente lê sobre cada emissora:
// quem entregou a mais, quem ficou devendo, e quem simplesmente cumpriu o
// combinado (e por isso não vira card, vira contagem).
package postsale

import (
	"math"

	"github.com/google/uuid"
)

// RowKind é o balde de uma emissora no Checking.
type RowKind string

const (
	// KindAbove — entregou tudo e ainda sobrou. Vira card "Acima do contratado".
	KindAbove RowKind = "above"
	// KindCompensation — ficou devendo alguma coisa no período. Vira card
	// "Compensações", com ou sem o selo de compensada (ver catalog.IsBonified).
	KindCompensation RowKind = "compensation"
	// KindConforming — entregou exatamente o combinado. NÃO vira card: entra na
	// frase "as outras N entregaram conforme o planejado". Decisão de produto:
	// uma campanha perfeita não pode mostrar lista vazia.
	KindConforming RowKind = "conforming"
)

// Classify aplica a regra da spec §4.5. Déficit manda: uma emissora que ficou
// devendo aparece em Compensações mesmo tendo extras de sobra — o extra vira o
// selo "compensado", não muda a lista.
func Classify(deficit, extras int) RowKind {
	if deficit > 0 {
		return KindCompensation
	}
	if extras > 0 {
		return KindAbove
	}
	return KindConforming
}

// DeliveryPct é a % de entrega exibida no card. nil = indeterminado (nada
// programado e nada tocado) — a UI mostra "—", não "0%".
//
// Programado zero com tocada > 0 é 100%: a emissora não tinha meta no período
// (carve-out, ou plano só em outros dias) e mesmo assim tocou.
func DeliveryPct(programmed, identified int) *int {
	if programmed <= 0 {
		if identified > 0 {
			v := 100
			return &v
		}
		return nil
	}
	v := int(math.Round(float64(identified) / float64(programmed) * 100))
	return &v
}

// StationRow é uma emissora do Checking: agregada no período, classificada, e
// com o metadado que o card mostra (logo, cidade, dial).
//
// Os campos numéricos são congelados no payload junto com o resto — depois do
// envio, recategorização não muda o que o cliente já leu.
type StationRow struct {
	StationID    uuid.UUID `json:"station_id"`
	Name         string    `json:"name"`
	City         *string   `json:"city"`
	State        *string   `json:"state"`
	Band         *string   `json:"band"`
	FrequencyMHz *float64  `json:"frequency_mhz"`
	LogoURL      *string   `json:"logo_url"`

	Programmed  int  `json:"programmed"`
	Identified  int  `json:"identified"`
	Deficit     int  `json:"deficit"`
	Extras      int  `json:"extras"`
	BonusCount  int  `json:"bonus_count"`
	DeliveryPct *int `json:"delivery_pct"`
	// Compensated espelha catalog.IsBonified: tinha déficit e os extras cobrem.
	// Vira o selo "compensado" no card, não muda a lista.
	Compensated bool    `json:"compensated"`
	Kind        RowKind `json:"kind"`
	// Note é escrita pelo admin no passo 3 do wizard (só em KindCompensation).
	Note string `json:"note"`
}
