package acoes

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"radiocheck/internal/catalog"
)

// ProgramarCampanhaEntrada é a campanha nova inteira: os dados dela, as regras
// de distribuição e os preços das emissoras.
type ProgramarCampanhaEntrada struct {
	Campanha CriarCampanhaEntrada
	Regras   []EntradaRegra
	Precos   []catalog.EntradaPreco
}

// ProgramacaoFeita é o que ficou gravado.
type ProgramacaoFeita struct {
	Campanha *catalog.Campaign
	Regras   []catalog.DistributionRule
	// Precos é o número de emissoras com preço gravado.
	Precos int
}

// ProgramarCampanha cria a campanha, as regras e os preços numa transação só
// (catalog.ProgramacaoDeCampanha): ou grava tudo ou nada. Tudo é validado ANTES
// da transação — as mesmas validações de CriarCampanha, CriarRegra e
// DefinirPrecos —, e o código do hub é conferido antes também.
//
// Não recategoriza nada: a campanha acabou de nascer e não tem veiculação. O
// aviso ao hub (campanha.upsert) sai depois do commit, como em CriarCampanha.
func (s *Servico) ProgramarCampanha(ctx context.Context, in ProgramarCampanhaEntrada) (*ProgramacaoFeita, error) {
	if s.Programacao == nil {
		return nil, errors.New("acoes: Programacao não configurada")
	}
	c := in.Campanha
	if c.Nome == "" || c.ClienteID == uuid.Nil {
		return nil, entradaInvalida("name and client_id are required")
	}
	if c.Fim.Before(c.Inicio) {
		return nil, entradaInvalida("a data final da campanha não pode ser anterior à inicial")
	}
	if len(c.EmissoraIDs) == 0 {
		return nil, entradaInvalida("a programação precisa de ao menos uma emissora-alvo")
	}
	// station_ids da regra é array sem FK, e o preço só tem FK para stations: sem esta conferência,
	// uma regra ou um preço de emissora FORA da campanha seria gravado sem erro.
	alvo := make(map[uuid.UUID]bool, len(c.EmissoraIDs))
	for _, id := range c.EmissoraIDs {
		alvo[id] = true
	}
	for _, r := range in.Regras {
		if err := validarRegra(r); err != nil {
			return nil, err
		}
		if r.Inicio.Before(c.Inicio) || r.Fim.After(c.Fim) {
			return nil, entradaInvalida("a vigência da regra sai do período da campanha")
		}
		for _, id := range r.EmissoraIDs {
			if !alvo[id] {
				return nil, entradaInvalida("a regra tem emissora fora das emissoras-alvo da campanha")
			}
		}
	}
	if err := validarLoteDePrecos(in.Precos, uuid.Nil); err != nil {
		return nil, err
	}
	for _, p := range in.Precos {
		if !alvo[p.StationID] {
			return nil, entradaInvalida("preço de emissora fora das emissoras-alvo da campanha")
		}
	}
	canonico, err := s.ConferirCodigoHub(ctx, c.ClienteID, c.CodigoHub)
	if err != nil {
		return nil, err
	}

	regras := make([]catalog.CreateDistributionRuleInput, len(in.Regras))
	for i, r := range in.Regras {
		regras[i] = r.paraCatalogo(uuid.Nil) // o id da campanha entra dentro da transação
	}
	gravada, err := s.Programacao.Gravar(ctx, catalog.ProgramacaoInput{
		Campanha: catalog.CreateCampaignInput{
			ClientID: c.ClienteID, Name: c.Nome,
			StartDate: c.Inicio, EndDate: c.Fim,
			TargetStations: c.EmissoraIDs,
			HubCode:        canonico,
		},
		Regras: regras,
		Precos: in.Precos,
	})
	if err != nil {
		return nil, recusaDoBanco(err)
	}
	s.avisarHubDaCampanha(gravada.Campanha)
	return &ProgramacaoFeita{Campanha: gravada.Campanha, Regras: gravada.Regras, Precos: len(in.Precos)}, nil
}

// validarLoteDePrecos é a validação de DefinirPrecos sem a exigência de lote
// não vazio (a programação pode nascer sem preço): emissora repetida, tipo
// repetido dentro da emissora e cada entrada pela ValidatePricingInput.
func validarLoteDePrecos(entradas []catalog.EntradaPreco, campanha uuid.UUID) error {
	vistas := make(map[uuid.UUID]struct{}, len(entradas))
	for _, e := range entradas {
		if _, repetida := vistas[e.StationID]; repetida {
			return entradaInvalida("emissora repetida no lote de preços")
		}
		vistas[e.StationID] = struct{}{}
		tipos := make(map[uuid.UUID]struct{}, len(e.PerType))
		for _, t := range e.PerType {
			if _, repetido := tipos[t.TypeID]; repetido {
				return entradaInvalida("tipo de material repetido no preço da emissora")
			}
			tipos[t.TypeID] = struct{}{}
		}
	}
	for _, e := range entradas {
		if err := catalog.ValidatePricingInput(catalog.UpsertInput{
			CampaignID: campanha, StationID: e.StationID, Mode: e.Mode,
			ConsolidatedValue: e.ConsolidatedValue, PerType: e.PerType,
		}); err != nil {
			return &Recusa{Codigo: CodigoEntradaInvalida, Mensagem: err.Error(), Causa: err}
		}
	}
	return nil
}
