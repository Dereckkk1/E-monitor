package acoes

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"radiocheck/internal/catalog"
	"radiocheck/internal/supervisor"
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
// atribuição é decidida na entrada da tocada. Nome, datas e código vão num
// UPDATE só (UpdateBasic com HubCode): ou grava tudo ou nada.
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

	entrada := catalog.UpdateBasicInput{
		Name:      strings.TrimSpace(in.Nome),
		StartDate: in.Inicio,
		EndDate:   in.Fim,
	}
	if codigoMudou {
		// Trocar o código zera o hub_notified_at no mesmo UPDATE (o RETURNING
		// já traz hub_code e hub_notified_at atualizados).
		entrada.HubCode = &canonico
	}
	out, err := s.Campanhas.UpdateBasic(ctx, id, entrada)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, naoEncontrado("campanha não encontrada")
		}
		return nil, recusaDoBanco(err)
	}

	if codigoMudou {
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
			if errors.Is(err, supervisor.ErrCampaignNotFound) {
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

// CancelarCampanha passa a campanha de programada/ativa para cancelada (§18.2.1)
// e derruba os workers das emissoras que ficaram sem campanha ativa. Devolve a
// campanha já cancelada.
//
//   - id inexistente: nao_encontrado.
//   - campanha já concluída/cancelada: estado_terminal, com Dados["estado_atual"].
//   - devolve nil (sem erro) se só a releitura final falhar: o cancelamento já
//     está gravado.
//
// Os workers só caem DEPOIS de a transição gravar (o CancelCampaign já virou o
// estado, então vai direto ao stop, sem o round-trip extra do Pause).
func (s *Servico) CancelarCampanha(ctx context.Context, id uuid.UUID) (*catalog.Campaign, error) {
	mudou, anterior, err := s.Campanhas.CancelCampaign(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, naoEncontrado("campanha não encontrada")
		}
		return nil, err
	}
	if !mudou {
		return nil, &Recusa{
			Codigo:   CodigoEstadoTerminal,
			Mensagem: "a campanha já está em estado final (" + anterior + ") e não pode ser cancelada",
			Dados:    map[string]any{"estado_atual": anterior},
		}
	}
	if s.Supervisor != nil {
		s.Supervisor.StopWorkersForCampaign(id)
	}
	// A transição já está gravada: a releitura é só para devolver a campanha, e
	// falhar nela NÃO pode virar erro (diria "não cancelou" de uma campanha
	// cancelada). Quem chama tolera a campanha nil: a rota ignora o valor e a
	// tool do MCP monta a resposta sem ela.
	out, err := s.Campanhas.Get(ctx, id)
	if err != nil {
		zap.L().Warn("CancelarCampanha: campanha cancelada, mas a releitura falhou",
			zap.String("campaign_id", id.String()), zap.Error(err))
		return nil, nil
	}
	return out, nil
}

// ApagarCampanha remove a campanha e tudo que é dela (tocadas, hashes, materiais
// legados).
//
// ⚠️ Contrato Recusa (o MCP depende dele): uma *Recusa significa "NADA foi
// gravado"; qualquer outro erro, "pode ter gravado". Por isso toda checagem de
// negócio vem ANTES da primeira escrita, e depois dela nenhum caminho devolve
// Recusa:
//
//  1. a campanha existe? Senão nao_encontrado + ErrAlvoInexistente (a rota
//     responde 204, apagar o que não existe é idempotente);
//  2. alguma tocada dela carrega projeção de OUTRA campanha? campanha_com_projecoes
//     (o CASCADE varreria o histórico alheio — audit C1). Erro da contagem é
//     erro interno: nada foi gravado, e não dá para afirmar que é seguro seguir;
//  3. tem lote de comprovante manual? campanha_com_lote_manual (a FK
//     manual_proof_batches→campaigns não tem ON DELETE e abortaria o delete);
//  4. está num pós-venda? campanha_em_pos_venda (FK RESTRICT; o pós-venda é
//     documento enviado ao cliente).
//
// Só então CANCELA (CancelCampaign grava `cancelled_at`, que o Pause antigo não
// gravava) e derruba os workers, e depois apaga. Do cancelamento em diante
// qualquer falha é erro INTERNO, inclusive ErrCampaignHasForeignProjections (uma
// projeção que apareceu entre a checagem e o delete): a campanha já foi
// cancelada, então devolver Recusa mentiria "nada aconteceu". Cancelar uma
// campanha já concluída/cancelada não muda nada (changed=false) e segue.
func (s *Servico) ApagarCampanha(ctx context.Context, id uuid.UUID) error {
	if _, err := s.Campanhas.Get(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &Recusa{Codigo: CodigoNaoEncontrado, Mensagem: "not found", Causa: ErrAlvoInexistente}
		}
		return err
	}
	n, err := s.Campanhas.CountForeignProjections(ctx, id)
	if err != nil {
		return err
	}
	if n > 0 {
		return &Recusa{
			Codigo:   CodigoCampanhaComProjecoes,
			Mensagem: "a campanha tem tocadas que carregam veiculações de outras campanhas; apagá-la destruiria esse histórico",
			Dados:    map[string]any{"foreign_projections": n},
		}
	}
	temLote, emPosVenda, err := s.Campanhas.DeleteBlockers(ctx, id)
	if err != nil {
		return err
	}
	if temLote {
		return &Recusa{
			Codigo:   CodigoCampanhaComLoteManual,
			Mensagem: "a campanha tem comprovantes de veiculação manual; apagá-la perderia esses comprovantes",
		}
	}
	if emPosVenda {
		return &Recusa{
			Codigo:   CodigoCampanhaEmPosVenda,
			Mensagem: "a campanha faz parte de um relatório de pós-venda e não pode ser apagada",
		}
	}

	// A partir daqui nada é Recusa.
	if _, _, err := s.Campanhas.CancelCampaign(ctx, id); err != nil {
		return err
	}
	if s.Supervisor != nil {
		s.Supervisor.StopWorkersForCampaign(id)
	}
	if err := s.Campanhas.Delete(ctx, id); err != nil {
		return fmt.Errorf("apagar campanha depois de cancelar: %w", err)
	}
	return nil
}

// limiteCPMFixo: a coluna é NUMERIC(12,2), que não comporta 1e10 ou mais.
const limiteCPMFixo = 1e10

// DefinirCPMFixo grava (ou, com nil, limpa) o CPM fixo da campanha: quando
// preenchido, sobrescreve o CPM derivado em /campaigns, /insights e no dashboard.
func (s *Servico) DefinirCPMFixo(ctx context.Context, id uuid.UUID, cpm *float64) (*catalog.Campaign, error) {
	if cpm != nil && *cpm < 0 {
		return nil, entradaInvalida("fixed_cpm must be >= 0")
	}
	if cpm != nil && (math.IsNaN(*cpm) || *cpm >= limiteCPMFixo) {
		return nil, entradaInvalida("fixed_cpm must be less than 10000000000")
	}
	out, err := s.Campanhas.UpdateFixedCPM(ctx, id, cpm)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, naoEncontrado("campanha não encontrada")
		}
		return nil, recusaDoBanco(err)
	}
	return out, nil
}
