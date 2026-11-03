package acoes

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"radiocheck/internal/catalog"
)

// toleranciaFuturo é quanto uma veiculação manual pode estar à frente do relógio
// (diferença de relógio entre quem declara e o servidor) sem ser "futuro".
const toleranciaFuturo = 5 * time.Minute

// agora devolve o relógio do serviço (Agora nil = time.Now).
func (s *Servico) agora() time.Time {
	if s.Agora != nil {
		return s.Agora()
	}
	return time.Now()
}

// NoFuturo diz se o instante passa da tolerância de relógio (toleranciaFuturo)
// à frente do relógio do serviço: é a regra de CriarVeiculacaoManual, exposta
// para quem confere o pedido antes (o MCP recusa na pré-visualização, com o
// mesmo relógio e a mesma tolerância da escrita).
func (s *Servico) NoFuturo(quando time.Time) bool {
	return quando.After(s.agora().Add(toleranciaFuturo))
}

// EntradaManual é o que a rota e a tool mandam para criar a veiculação manual.
type EntradaManual struct {
	Campanha, Emissora, Material uuid.UUID
	// Quando é o instante declarado da veiculação.
	Quando time.Time
	// Nota é livre e opcional (vazia vira NULL no banco).
	Nota string
	// Ator é quem declarou (gravado em manual_by).
	Ator uuid.UUID
}

// CriarVeiculacaoManual insere a veiculação declarada à mão. Valida a entrada,
// confere o vínculo material × emissora × campanha (no repositório) e deixa o
// fechamento da célula-dia decidir a categoria: a manual disputa a cota do dia
// com as automáticas e pode rebaixar outras tocadas da mesma célula. O upload do
// áudio (opcional) NÃO mora aqui: é depois do insert, com o id gerado.
func (s *Servico) CriarVeiculacaoManual(ctx context.Context, in EntradaManual) (*catalog.Detection, error) {
	if in.Campanha == uuid.Nil || in.Emissora == uuid.Nil || in.Material == uuid.Nil {
		return nil, entradaInvalida("campaign_id, station_id and commercial_id are required")
	}
	if in.Quando.IsZero() {
		return nil, entradaInvalida("detected_at is required")
	}
	if s.NoFuturo(in.Quando) {
		return nil, entradaInvalida("detected_at cannot be in the future")
	}

	det, err := s.Veiculacoes.CreateManual(ctx, catalog.CreateManualInput{
		StationID:    in.Emissora,
		CommercialID: in.Material,
		CampaignID:   in.Campanha,
		DetectedAt:   in.Quando,
		ManualBy:     in.Ator,
		ManualNote:   in.Nota,
	})
	if err != nil {
		if errors.Is(err, catalog.ErrMaterialNotLinkedToStation) {
			return nil, &Recusa{Codigo: CodigoMaterialNaoVinculado, Mensagem: catalog.ErrMaterialNotLinkedToStation.Error()}
		}
		// detections é particionada por mês: uma data sem partição estoura 23514
		// ("no partition of relation ... found for row") — é entrada, não bug.
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23514" && strings.Contains(pg.Message, "no partition of relation") {
			return nil, entradaInvalida("detected_at fora do período suportado")
		}
		return nil, err
	}
	return det, nil
}

// IgnorarVeiculacao desconsidera a tocada (ignored_at/ignored_by): ela sai da
// conta e a vaga que ocupava na cota do dia passa à próxima excedente.
// Reversível por RestaurarVeiculacao.
//
// Devolve nil (sem erro) se só a releitura final falhar: a escrita já está
// gravada e nenhum erro dela pode vir depois (diria "não ignorou" de uma tocada
// ignorada). Quem chama tolera o nil: a tool do MCP monta a resposta de novo e a
// rota responde só o id.
func (s *Servico) IgnorarVeiculacao(ctx context.Context, id, ator uuid.UUID) (*catalog.Detection, error) {
	det, err := s.veiculacaoExistente(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.Veiculacoes.Ignore(ctx, det.ID, ator); err != nil {
		return nil, err
	}
	return s.releituraDaVeiculacao(ctx, id), nil
}

// RestaurarVeiculacao desfaz IgnorarVeiculacao: a tocada volta à conta e retoma
// o lugar cronológico dela na cota do dia (rebaixando quem ocupou a vaga). O
// mesmo contrato do nil de IgnorarVeiculacao.
func (s *Servico) RestaurarVeiculacao(ctx context.Context, id uuid.UUID) (*catalog.Detection, error) {
	det, err := s.veiculacaoExistente(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.Veiculacoes.Restore(ctx, det.ID); err != nil {
		return nil, err
	}
	return s.releituraDaVeiculacao(ctx, id), nil
}

// releituraDaVeiculacao relê a tocada DEPOIS de ela ter sido gravada. Falhar aqui
// (inclusive a tocada ter sumido no meio) não é erro nem recusa — a escrita
// aconteceu —: loga e devolve nil, o mesmo padrão do CancelarCampanha.
func (s *Servico) releituraDaVeiculacao(ctx context.Context, id uuid.UUID) *catalog.Detection {
	det, err := s.Veiculacoes.Get(ctx, id)
	if err != nil {
		zap.L().Warn("veiculação gravada, mas a releitura falhou",
			zap.String("detection_id", id.String()), zap.Error(err))
		return nil
	}
	return det
}

// veiculacaoExistente: sem a checagem o UPDATE não acharia linha e a ação
// "funcionaria" em silêncio. Só vale ANTES de gravar: a tocada que não existe é
// nao_encontrado + ErrAlvoInexistente, não erro interno. O texto é o da rota.
func (s *Servico) veiculacaoExistente(ctx context.Context, id uuid.UUID) (*catalog.Detection, error) {
	det, err := s.Veiculacoes.Get(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, &Recusa{Codigo: CodigoNaoEncontrado, Mensagem: "not found", Causa: ErrAlvoInexistente}
		}
		return nil, err
	}
	return det, nil
}
