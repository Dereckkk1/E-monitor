package acoes

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

// recarregar manda o supervisor recarregar a campanha (NATS index.reload +
// workers) e só LOGA a falha: o banco já foi mutado, quem chamou precisa receber
// sucesso, e o reconciler de 30 s é a rede de segurança se o reload sofrer. Sem
// supervisor não faz nada. Só tem efeito em campanha `ativa` (o supervisor
// ignora as outras).
//
// Sem este reload, material vinculado a uma campanha em andamento ficaria
// "invisível" para o motor de matching até o próximo tick do reconciler —
// exatamente o sintoma do incidente 2026-05-08 (workers cegos a target_stations
// editado).
func (s *Servico) recarregar(campanhaID uuid.UUID, op string) {
	if s.Supervisor == nil {
		return
	}
	if err := s.Supervisor.Reload(campanhaID); err != nil {
		zap.L().Error("campaign_materials: supervisor reload failed",
			zap.String("op", op),
			zap.String("campaign_id", campanhaID.String()),
			zap.Error(err),
		)
	}
}

// VincularMaterial vincula o material à campanha (ou troca as emissoras do
// vínculo existente: é um upsert) e recarrega a campanha. Lista nula = todas as
// emissoras da campanha. NÃO recategoriza: a atribuição é decidida na entrada da
// tocada (evidence/attribution.go), então o passado não muda.
func (s *Servico) VincularMaterial(ctx context.Context, campanhaID, materialID uuid.UUID, emissoras []uuid.UUID) error {
	if err := s.MateriaisCampanha.Link(ctx, campanhaID, materialID, emissoras); err != nil {
		return recusaDoBanco(err)
	}
	s.recarregar(campanhaID, "link")
	return nil
}

// vinculoInexistente é a recusa do no-op: o vínculo material↔campanha não existe.
func vinculoInexistente() *Recusa {
	return &Recusa{Codigo: CodigoNaoEncontrado, Mensagem: "vínculo do material com a campanha não encontrado", Causa: ErrAlvoInexistente}
}

// TrocarEmissorasMaterial substitui as emissoras do vínculo e recarrega a
// campanha. Sem vínculo (0 linhas tocadas) não há o que trocar: recusa com
// nao_encontrado + ErrAlvoInexistente e NÃO recarrega (reload reinicia os
// workers da campanha à toa). A rota HTTP mapeia a marca para 204, como sempre.
func (s *Servico) TrocarEmissorasMaterial(ctx context.Context, campanhaID, materialID uuid.UUID, emissoras []uuid.UUID) error {
	n, err := s.MateriaisCampanha.UpdateStations(ctx, campanhaID, materialID, emissoras)
	if err != nil {
		return recusaDoBanco(err)
	}
	if n == 0 {
		return vinculoInexistente()
	}
	s.recarregar(campanhaID, "update_stations")
	return nil
}

// DesvincularMaterial remove o vínculo e recarrega a campanha. Sem vínculo (0
// linhas apagadas): recusa com nao_encontrado + ErrAlvoInexistente, sem
// recarregar; a rota HTTP mapeia a marca para 204 (idempotente).
func (s *Servico) DesvincularMaterial(ctx context.Context, campanhaID, materialID uuid.UUID) error {
	n, err := s.MateriaisCampanha.Unlink(ctx, campanhaID, materialID)
	if err != nil {
		return err
	}
	if n == 0 {
		return vinculoInexistente()
	}
	s.recarregar(campanhaID, "unlink")
	return nil
}

// TrocarTipoMaterial troca (ou limpa, com nil) o tipo do material e
// recategoriza em segundo plano TODAS as campanhas em que ele aparece: a
// categoria gravada das veiculações foi computada no insert com o tipo antigo e
// ficou obsoleta — sem isto, veiculações que agora casam uma regra do tipo novo
// continuam 'bonus' ("bonificação sem meta" no resumo diário). Best-effort,
// mesmo padrão das regras.
//
// Material inexistente (0 linhas tocadas): recusa com nao_encontrado +
// ErrAlvoInexistente e NÃO dispara a recategorização (não há o que recategorizar);
// a rota HTTP mapeia a marca para 204, como sempre. Sem Regras no Servico a
// recategorização é pulada (nil-safe, como o handler sempre foi).
//
// LIMITE CONHECIDO (do RecategorizeForMaterial): o escopo resolve o tipo NOVO,
// então as células do tipo ANTIGO ficam desatualizadas até o projrecon.
func (s *Servico) TrocarTipoMaterial(ctx context.Context, materialID uuid.UUID, tipoID *uuid.UUID) error {
	n, err := s.Materiais.UpdateType(ctx, materialID, tipoID)
	if err != nil {
		return recusaDoBanco(err)
	}
	if n == 0 {
		return &Recusa{Codigo: CodigoNaoEncontrado, Mensagem: "material não encontrado", Causa: ErrAlvoInexistente}
	}
	if s.Regras != nil {
		s.recatEmSegundoPlano("material_type_change", func(ctx context.Context) error {
			return s.Regras.RecategorizeForMaterial(ctx, materialID)
		})
	}
	return nil
}

// ApagarMaterial apaga o material. Vinculado a alguma campanha (FK
// campaign_materials_material_id_fkey, RESTRICT): recusa material_vinculado — o
// vínculo precisa sair primeiro. Material que não existe: nao_encontrado +
// ErrAlvoInexistente (a rota HTTP mapeia a marca para 204, idempotente). O
// mapeamento da FK é PRÓPRIO daqui: em recusaDoBanco essa mesma FK significa
// "material não encontrado" (caminho do INSERT do vínculo).
func (s *Servico) ApagarMaterial(ctx context.Context, id uuid.UUID) error {
	n, err := s.Materiais.Delete(ctx, id)
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23503" && pg.ConstraintName == "campaign_materials_material_id_fkey" {
			return &Recusa{Codigo: CodigoMaterialVinculado, Mensagem: "material vinculado a campanha"}
		}
		return err
	}
	if n == 0 {
		return &Recusa{Codigo: CodigoNaoEncontrado, Mensagem: "material não encontrado", Causa: ErrAlvoInexistente}
	}
	return nil
}
