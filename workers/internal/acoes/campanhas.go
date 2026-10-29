package acoes

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"radiocheck/internal/catalog"
)

func naoEncontrado(msg string) *Recusa {
	return &Recusa{Codigo: CodigoNaoEncontrado, Mensagem: msg}
}

// CriarCampanhaEntrada é o que a rota e a tool mandam para criar a campanha.
type CriarCampanhaEntrada struct {
	ClienteID   uuid.UUID
	Nome        string
	Inicio, Fim time.Time
	EmissoraIDs []uuid.UUID
	// CodigoHub: vazio = sem código (a decisão 6 permite). Passa pela barreira
	// do hub e é gravado na forma canônica.
	CodigoHub string
}

// CriarCampanha confere o código do hub, grava a campanha e avisa o hub
// (`campanha.upsert`, fora do caminho da resposta).
//
// ⚠️ O estado NÃO é decidido aqui: a campanha nasce `programada` pelo DEFAULT do
// banco, e o LifecycleScheduler (60 s) a promove a `ativa` quando
// `start_date ≤ hoje (SP)` — o que sobe os workers e faz o cliente passar a ver.
// Não há validação de datas aqui: a rota sempre as aceitou como vieram (quem
// valida as datas da campanha, no MCP, é a tool).
func (s *Servico) CriarCampanha(ctx context.Context, in CriarCampanhaEntrada) (*catalog.Campaign, error) {
	if in.Nome == "" || in.ClienteID == uuid.Nil {
		return nil, entradaInvalida("name and client_id are required")
	}
	canonico, err := s.ConferirCodigoHub(ctx, in.ClienteID, in.CodigoHub)
	if err != nil {
		return nil, err
	}
	out, err := s.Campanhas.Create(ctx, catalog.CreateCampaignInput{
		ClientID: in.ClienteID, Name: in.Nome,
		StartDate: in.Inicio, EndDate: in.Fim,
		TargetStations: in.EmissoraIDs,
		HubCode:        canonico,
	})
	if err != nil {
		return nil, recusaDoBanco(err)
	}
	s.avisarHubDaCampanha(out)
	return out, nil
}

// EditarCampanhaEntrada substitui nome e datas (PUT total: quem quer edição
// parcial lê a campanha e remonta a entrada). O cliente não muda: a Etapa 3 do
// wizard é hidratada contra a biblioteca de materiais dele.
type EditarCampanhaEntrada struct {
	Nome        string
	Inicio, Fim time.Time
	// CodigoHub é PONTEIRO, e o ponteiro é o desenho: nil = o pedido não falou do
	// código (não mexe), "" = mandou vazio (apaga). Sem ele, todo PUT do wizard
	// que editasse só a data apagaria o código — e apagar o código congela a
	// coleta da proposta no hub (§6.5). Um campo ausente e um campo vazio querem
	// dizer coisas opostas aqui.
	CodigoHub *string
}

// EditarCampanha edita nome, datas e (se vier) o código do hub.
//
// ⚠️ A conferência do código vem ANTES de qualquer escrita, e é só por isso que
// o Get existe: conferindo depois do UpdateBasic, um pedido que muda o nome E
// traz um código de outro cliente responderia recusa com o nome JÁ GRAVADO.
// Resposta de erro com escrita parcial é defeito que só aparece meses depois,
// quando alguém repara que o nome mudou numa edição que "deu erro".
//
// ⚠️ Só avisa o hub quando o código MUDOU. O preço: renomear a campanha ou mudar
// o estado dela NÃO chega ao hub depois da criação (decisão de produto, registrada
// no handoff do código do hub). E apagar o código TEM de emitir: é o evento sem
// código que faz o hub pôr `coletaAtiva = false` na proposta (§6.5).
//
// Nenhuma recategorização: o estado segue as datas pelo scheduler, e a
// atribuição é decidida na entrada da tocada. Não é transacional (nome e datas
// são gravados antes de AtualizarHubCode), como a rota sempre foi.
func (s *Servico) EditarCampanha(ctx context.Context, id uuid.UUID, in EditarCampanhaEntrada) (*catalog.Campaign, error) {
	if strings.TrimSpace(in.Nome) == "" {
		return nil, entradaInvalida("name is required")
	}
	if in.Inicio.IsZero() || in.Fim.IsZero() {
		return nil, entradaInvalida("start_date and end_date are required")
	}
	if in.Fim.Before(in.Inicio) {
		return nil, entradaInvalida("end_date must be on or after start_date")
	}

	var canonico string
	var codigoMudou bool
	if in.CodigoHub != nil {
		atual, err := s.Campanhas.Get(ctx, id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, naoEncontrado("campanha não encontrada")
			}
			return nil, err
		}
		canonico, err = s.ConferirCodigoHub(ctx, atual.ClientID, *in.CodigoHub)
		if err != nil {
			return nil, err
		}
		codigoMudou = deref(atual.HubCode) != canonico
	}

	out, err := s.Campanhas.UpdateBasic(ctx, id, catalog.UpdateBasicInput{
		Name:      strings.TrimSpace(in.Nome),
		StartDate: in.Inicio,
		EndDate:   in.Fim,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, naoEncontrado("campanha não encontrada")
		}
		return nil, recusaDoBanco(err)
	}

	if codigoMudou {
		// O `AtualizarHubCode` já zera o `hub_notified_at` sozinho, e tem teste
		// para isso — não refaça essa parte aqui.
		if err := s.Campanhas.AtualizarHubCode(ctx, id, canonico); err != nil {
			return nil, recusaDoBanco(err)
		}
		out.HubCode = ptrOuNil(canonico)
		out.HubNotifiedAt = nil
		s.avisarHubDaCampanha(out)
	}
	return out, nil
}

// TrocarEmissorasCampanha SUBSTITUI target_stations (lista omitida apaga todas).
//
// Com supervisor, a gravação e a reconciliação dos workers acontecem num caminho
// só (Supervisor.UpdateStations): se a campanha está `ativa`, para/sobe os
// workers da diferença — sem passar por `cancelada`, que era o que a dança
// Pause+Start fazia. Sem supervisor (teste, dev) é só o banco.
func (s *Servico) TrocarEmissorasCampanha(ctx context.Context, id uuid.UUID, emissoras []uuid.UUID) error {
	if emissoras == nil {
		emissoras = []uuid.UUID{}
	}
	if s.Supervisor != nil {
		if err := s.Supervisor.UpdateStations(id, emissoras); err != nil {
			zap.L().Error("campaigns.UpdateStations: supervisor failed",
				zap.String("campaign_id", id.String()), zap.Error(err))
			if strings.Contains(err.Error(), "campaign not found") {
				return naoEncontrado("campanha não encontrada")
			}
			return err
		}
		return nil
	}
	if _, err := s.Campanhas.Get(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return naoEncontrado("campanha não encontrada")
		}
		return err
	}
	return s.Campanhas.UpdateTargetStations(ctx, id, emissoras)
}
