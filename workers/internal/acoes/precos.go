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
	if err := validarLoteDePrecos(entradas, campanha); err != nil {
		return nil, err
	}
	out, err := s.Precos.UpsertLote(ctx, campanha, entradas)
	if err != nil {
		return nil, recusaDoBanco(err)
	}
	return out, nil
}
