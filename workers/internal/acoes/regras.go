package acoes

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"radiocheck/internal/catalog"
)

// ───────────────────────────── regras de distribuição ─────────────────────────────

// EntradaRegra é o que as rotas e as tools mandam para criar/editar uma regra de
// distribuição (X inserções por dia do tipo T, nas emissoras E, na faixa H).
// A regra substitui tudo na edição (PUT total): quem quer edição parcial lê a
// regra e remonta a entrada (é o que a tool do MCP faz).
type EntradaRegra struct {
	TipoID uuid.UUID
	// EmissoraIDs nil = ausente (recusado). Slice vazio é aceito (regra sem
	// emissora é um no-op silencioso — a rota sempre aceitou).
	EmissoraIDs []uuid.UUID
	// MaterialIDs vazio = regra geral (vale para todos os materiais do tipo).
	MaterialIDs []uuid.UUID
	Nome        string
	Inicio, Fim time.Time // datas de calendário
	// MascaraDias: bit 0 = domingo … bit 6 = sábado (127 = todos os dias).
	MascaraDias     int16
	HoraInicio      string // "HH:MM"
	HoraFim         string
	InsercoesPorDia int16
}

func (e EntradaRegra) paraCatalogo(campanhaID uuid.UUID) catalog.CreateDistributionRuleInput {
	return catalog.CreateDistributionRuleInput{
		CampaignID: campanhaID, TypeID: e.TipoID,
		StationIDs: e.EmissoraIDs, MaterialIDs: e.MaterialIDs,
		Name:      strings.TrimSpace(e.Nome),
		StartDate: e.Inicio, EndDate: e.Fim,
		WeekdayMask: e.MascaraDias,
		TimeStart:   e.HoraInicio, TimeEnd: e.HoraFim,
		PlaysPerDay: e.InsercoesPorDia,
	}
}

func entradaInvalida(msg string) *Recusa {
	return &Recusa{Codigo: CodigoEntradaInvalida, Mensagem: msg}
}

// validarRegra confere o que os CHECK de distribution_rules conferem (e o que
// uma rota que dependia do banco devolvia como 500): máscara 0–127, 1–100
// inserções, data final ≥ inicial e hora final > inicial. Nada além disso — o
// que a rota aceitava continua aceito (a regra sem dia nenhum, máscara 0, é
// aceita pelo banco; a tool do MCP exige pelo menos um dia por conta própria).
func validarRegra(e EntradaRegra) error {
	if e.EmissoraIDs == nil {
		return entradaInvalida("station_ids é obrigatório (use uma lista, mesmo vazia)")
	}
	if e.MascaraDias < 0 || e.MascaraDias > 127 {
		return entradaInvalida("a máscara de dias precisa estar entre 0 e 127")
	}
	if e.InsercoesPorDia < 1 || e.InsercoesPorDia > 100 {
		return entradaInvalida("as inserções por dia precisam estar entre 1 e 100")
	}
	if e.Fim.Before(e.Inicio) {
		return entradaInvalida("a data final não pode ser anterior à inicial")
	}
	// A ordem das horas só é conferida quando as duas se leem: o Postgres aceita
	// formas que HH:MM não cobre (H:MM, HH:MM:SS, 24:00) e quem decide o resto é
	// ele — um horário ilegível volta como entrada inválida por recusaDoBanco.
	ini, okIni := segundosDoHorario(e.HoraInicio)
	fim, okFim := segundosDoHorario(e.HoraFim)
	if okIni && okFim && fim <= ini {
		return entradaInvalida("a hora final precisa ser maior que a inicial")
	}
	return nil
}

// segundosDoHorario lê "H:MM", "HH:MM" ou "HH:MM:SS" (24:00:00 incluído) em
// segundos desde 00:00.
func segundosDoHorario(s string) (int, bool) {
	partes := strings.Split(strings.TrimSpace(s), ":")
	if len(partes) < 2 || len(partes) > 3 {
		return 0, false
	}
	n := [3]int{}
	for i, p := range partes {
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 {
			return 0, false
		}
		n[i] = v
	}
	h, m, seg := n[0], n[1], n[2]
	if h > 24 || m > 59 || seg > 59 || (h == 24 && (m != 0 || seg != 0)) {
		return 0, false
	}
	return h*3600 + m*60 + seg, true
}

// CriarRegra grava a regra e recategoriza em segundo plano as veiculações da
// campanha que ela pode afetar (tipo da regra, período inteiro da campanha).
func (s *Servico) CriarRegra(ctx context.Context, campanhaID uuid.UUID, in EntradaRegra) (*catalog.DistributionRule, error) {
	if err := validarRegra(in); err != nil {
		return nil, err
	}
	regra, err := s.Regras.Create(ctx, in.paraCatalogo(campanhaID))
	if err != nil {
		return nil, recusaDoBanco(err)
	}
	s.recatEmSegundoPlano("rule_create", func(ctx context.Context) error {
		return s.Regras.RecategorizeForRule(ctx, regra.ID)
	})
	return regra, nil
}

// regraDaCampanha lê a regra e confere que ela é DA campanha informada. Regra
// inexistente e regra de outra campanha são nao_encontrado para quem chama (a
// rota antiga editava/apagava a regra de outra campanha e recategorizava a
// errada — F3-8a). Só a INEXISTENTE leva a marca ErrAlvoInexistente: o DELETE
// HTTP a trata como idempotente (204), a de outra campanha segue 404.
func (s *Servico) regraDaCampanha(ctx context.Context, campanhaID, regraID uuid.UUID) (*catalog.DistributionRule, error) {
	regra, err := s.Regras.Get(ctx, regraID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, &Recusa{Codigo: CodigoNaoEncontrado, Mensagem: "regra de distribuição não encontrada nesta campanha", Causa: ErrAlvoInexistente}
	}
	if err != nil {
		return nil, err
	}
	if regra.CampaignID != campanhaID {
		return nil, &Recusa{Codigo: CodigoNaoEncontrado, Mensagem: "regra de distribuição não encontrada nesta campanha"}
	}
	return regra, nil
}

// EditarRegra substitui a regra DA campanha e recategoriza em segundo plano: o
// tipo novo (período inteiro da campanha) e, quando a edição TROCA o tipo, também
// o tipo antigo — senão as veiculações do tipo de onde a regra saiu ficam com a
// categoria velha (F3-8b). nao_encontrado se a regra não é desta campanha.
func (s *Servico) EditarRegra(ctx context.Context, campanhaID, regraID uuid.UUID, in EntradaRegra) error {
	if err := validarRegra(in); err != nil {
		return err
	}
	anterior, err := s.regraDaCampanha(ctx, campanhaID, regraID)
	if err != nil {
		return err
	}
	if err := s.Regras.Update(ctx, regraID, in.paraCatalogo(campanhaID)); err != nil {
		return recusaDoBanco(err)
	}
	passos := []func(context.Context) error{
		func(ctx context.Context) error { return s.Regras.RecategorizeForRule(ctx, regraID) },
	}
	if anterior.TypeID != in.TipoID {
		passos = append(passos, func(ctx context.Context) error {
			return s.Regras.RecategorizeForCampaignType(ctx, campanhaID, anterior.TypeID)
		})
	}
	s.recatEmSegundoPlano("rule_update", passos...)
	return nil
}

// ApagarRegra apaga a regra DA campanha e recategoriza a campanha inteira em
// segundo plano (depois do delete o escopo da regra se perdeu). nao_encontrado
// se a regra não é desta campanha.
func (s *Servico) ApagarRegra(ctx context.Context, campanhaID, regraID uuid.UUID) error {
	if _, err := s.regraDaCampanha(ctx, campanhaID, regraID); err != nil {
		return err
	}
	if err := s.Regras.Delete(ctx, regraID); err != nil {
		return err
	}
	s.recatEmSegundoPlano("rule_delete", func(ctx context.Context) error {
		return s.Regras.RecategorizeForCampaign(ctx, campanhaID)
	})
	return nil
}

// ───────────────────────────── ajustes do dia (overrides) ─────────────────────────────

// hhmm aceita 00:00 até 23:59 — mais estrito que a regra de propósito: é o que a
// rota de override sempre exigiu, com o 400 amigável antes do Postgres.
var hhmm = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// EntradaAjuste é a célula-dia (campanha × tipo × emissora × dia) com meta e
// faixa próprias. Um ajuste SUPERSEDE as regras do dia na categorização.
type EntradaAjuste struct {
	CampanhaID, TipoID, EmissoraID uuid.UUID
	Dia                            time.Time // data de calendário
	Insercoes                      int16     // a meta N do dia; 0 = toda tocada é bonificação
	HoraInicio, HoraFim            string    // "HH:MM"
	Motivo                         *string
	CriadoPor                      *uuid.UUID
}

// ResultadoAjuste é o que DefinirAjusteComResultado/ApagarAjusteComResultado
// contam além do erro: a recategorização da célula-dia é best-effort (a ação já
// gravou), então o chamador que PRECISA saber se ela falhou — o MCP, que devolve a
// célula recalculada — lê RecatFalhou. As rotas HTTP seguem ignorando.
type ResultadoAjuste struct {
	// RecatFalhou: a recategorização síncrona da célula-dia deu erro (já registrado
	// em log e métrica); o reconciler de projeções corrige depois.
	RecatFalhou bool
}

// DefinirAjuste grava (ou troca) o ajuste e recategoriza a célula-dia de forma
// SÍNCRONA: a categoria já tem de estar certa quando a resposta volta (o
// frontend refaz a leitura no onSuccess; em segundo plano ele pintaria a
// categoria velha). O escopo é UMA célula-dia, com o advisory lock dela — a
// latência é desprezível. A recategorização é best-effort: o ajuste já foi
// gravado, então o erro dela é registrado e NÃO falha a ação.
func (s *Servico) DefinirAjuste(ctx context.Context, e EntradaAjuste) error {
	_, err := s.DefinirAjusteComResultado(ctx, e)
	return err
}

// DefinirAjusteComResultado é o DefinirAjuste que também diz se a recategorização falhou.
func (s *Servico) DefinirAjusteComResultado(ctx context.Context, e EntradaAjuste) (ResultadoAjuste, error) {
	if !hhmm.MatchString(e.HoraInicio) {
		return ResultadoAjuste{}, entradaInvalida("time_start must be HH:MM (00:00-23:59)")
	}
	if !hhmm.MatchString(e.HoraFim) {
		return ResultadoAjuste{}, entradaInvalida("time_end must be HH:MM (00:00-23:59)")
	}
	// Comparação de texto é segura porque o regex força HH:MM com zero à esquerda.
	if e.HoraFim <= e.HoraInicio {
		return ResultadoAjuste{}, entradaInvalida("time_end must be greater than time_start")
	}
	if e.Insercoes < 0 {
		return ResultadoAjuste{}, entradaInvalida("as inserções do dia não podem ser negativas")
	}
	if err := s.Ajustes.Upsert(ctx, catalog.UpsertOverrideInput{
		CampaignID: e.CampanhaID, TypeID: e.TipoID, StationID: e.EmissoraID,
		ForDate: e.Dia, PlaysExpected: e.Insercoes,
		TimeStart: e.HoraInicio, TimeEnd: e.HoraFim,
		Reason: e.Motivo, CreatedBy: e.CriadoPor,
	}); err != nil {
		return ResultadoAjuste{}, recusaDoBanco(err)
	}
	return ResultadoAjuste{RecatFalhou: s.recatDaCelula("override_upsert", e.CampanhaID, e.TipoID, e.EmissoraID, e.Dia)}, nil
}

// ApagarAjuste apaga o ajuste (a célula volta a ser julgada pelas regras) e
// recategoriza a célula-dia de forma síncrona, como DefinirAjuste. Apagar um
// ajuste que não existe não é erro (a rota sempre respondeu 204).
func (s *Servico) ApagarAjuste(ctx context.Context, campanhaID, tipoID, emissoraID uuid.UUID, dia time.Time) error {
	_, err := s.ApagarAjusteComResultado(ctx, campanhaID, tipoID, emissoraID, dia)
	return err
}

// ApagarAjusteComResultado é o ApagarAjuste que também diz se a recategorização falhou.
func (s *Servico) ApagarAjusteComResultado(ctx context.Context, campanhaID, tipoID, emissoraID uuid.UUID, dia time.Time) (ResultadoAjuste, error) {
	if err := s.Ajustes.Delete(ctx, campanhaID, tipoID, emissoraID, dia); err != nil {
		return ResultadoAjuste{}, err
	}
	return ResultadoAjuste{RecatFalhou: s.recatDaCelula("override_delete", campanhaID, tipoID, emissoraID, dia)}, nil
}

// recatDaCelula recategoriza a célula-dia e devolve true se falhou. Sem
// recategorizador configurado não há o que falhar (false).
func (s *Servico) recatDaCelula(origem string, campanhaID, tipoID, emissoraID uuid.UUID, dia time.Time) bool {
	if s.RecatCelula == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), PrazoRecat)
	defer cancel()
	err := s.RecatCelula.RecategorizeForOverride(ctx, campanhaID, tipoID, emissoraID, dia)
	RegistrarFalhaRecat(origem, err)
	return err != nil
}
