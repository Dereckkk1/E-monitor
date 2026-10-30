package acoes

import (
	"context"

	"github.com/google/uuid"

	"radiocheck/internal/catalog"
)

// DefinirPrecos grava o preço de uma ou mais emissoras da campanha, TODAS numa
// transação (ou grava tudo ou nada). Cada entrada substitui por inteiro o preço
// anterior daquela emissora.
//
// Toda entrada é validada antes de qualquer escrita. A recusa é entrada_invalida,
// com a mensagem original do catálogo (a rota responde 422 com esse texto) e a
// causa (catalog.ErrPricing*) acessível por errors.Is. Referência inexistente
// (campanha, emissora, tipo de material) volta como recusa do banco, não como
// erro interno.
//
// O lote não pode ser vazio, repetir emissora nem repetir type_id dentro de um
// PerType (o PK estouraria no meio da transação): entrada_invalida, antes de abrir
// a transação.
func (s *Servico) DefinirPrecos(ctx context.Context, campanha uuid.UUID, entradas []catalog.EntradaPreco) ([]catalog.StationPricing, error) {
	if len(entradas) == 0 {
		return nil, entradaInvalida("pelo menos uma emissora é obrigatória")
	}
	vistas := make(map[uuid.UUID]struct{}, len(entradas))
	for _, e := range entradas {
		if _, repetida := vistas[e.StationID]; repetida {
			return nil, entradaInvalida("emissora repetida no lote de preços")
		}
		vistas[e.StationID] = struct{}{}
		tipos := make(map[uuid.UUID]struct{}, len(e.PerType))
		for _, t := range e.PerType {
			if _, repetido := tipos[t.TypeID]; repetido {
				return nil, entradaInvalida("tipo de material repetido no preço da emissora")
			}
			tipos[t.TypeID] = struct{}{}
		}
	}
	for _, e := range entradas {
		if err := catalog.ValidatePricingInput(catalog.UpsertInput{
			CampaignID: campanha, StationID: e.StationID, Mode: e.Mode,
			ConsolidatedValue: e.ConsolidatedValue, PerType: e.PerType,
		}); err != nil {
			return nil, &Recusa{Codigo: CodigoEntradaInvalida, Mensagem: err.Error(), Causa: err}
		}
	}
	out, err := s.Precos.UpsertLote(ctx, campanha, entradas)
	if err != nil {
		return nil, recusaDoBanco(err)
	}
	return out, nil
}
