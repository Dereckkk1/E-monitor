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
func (s *Servico) DefinirPrecos(ctx context.Context, campanha uuid.UUID, entradas []catalog.EntradaPreco) ([]catalog.StationPricing, error) {
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
