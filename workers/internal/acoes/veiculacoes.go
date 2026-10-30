package acoes

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

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
	if in.Quando.After(s.agora().Add(toleranciaFuturo)) {
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
func (s *Servico) IgnorarVeiculacao(ctx context.Context, id, ator uuid.UUID) (*catalog.Detection, error) {
	det, err := s.veiculacaoExistente(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.Veiculacoes.Ignore(ctx, det.ID, ator); err != nil {
		return nil, err
	}
	return s.veiculacaoExistente(ctx, id)
}

// RestaurarVeiculacao desfaz IgnorarVeiculacao: a tocada volta à conta e retoma
// o lugar cronológico dela na cota do dia (rebaixando quem ocupou a vaga).
func (s *Servico) RestaurarVeiculacao(ctx context.Context, id uuid.UUID) (*catalog.Detection, error) {
	det, err := s.veiculacaoExistente(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.Veiculacoes.Restore(ctx, det.ID); err != nil {
		return nil, err
	}
	return s.veiculacaoExistente(ctx, id)
}

// veiculacaoExistente: sem a checagem o UPDATE não acharia linha e a ação
// "funcionaria" em silêncio. Também serve à releitura final: se a tocada sumiu
// no meio da operação, é nao_encontrado + ErrAlvoInexistente, não erro interno.
// O texto é o da rota.
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
