package catalog

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// queryRower é o que os INSERTs compartilhados precisam: um pool ou uma
// transação (os dois têm QueryRow com a mesma assinatura).
type queryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var (
	_ queryRower = (*pgxpool.Pool)(nil)
	_ queryRower = (pgx.Tx)(nil)
)

// ProgramacaoDeCampanha grava uma campanha nova, as regras de distribuição dela
// e os preços das emissoras numa transação só: ou fica tudo gravado, ou nada.
// É o caminho da tool programar_campanha do MCP — sem ela, uma campanha
// programada por IA levava uma chamada por regra, e uma queda no meio deixava a
// campanha pela metade (campanha sem parte das regras, sem preço).
type ProgramacaoDeCampanha struct {
	pool *pgxpool.Pool
}

func NewProgramacaoDeCampanha(pool *pgxpool.Pool) *ProgramacaoDeCampanha {
	return &ProgramacaoDeCampanha{pool: pool}
}

// ProgramacaoInput é a campanha inteira. As regras chegam sem CampaignID (o id
// só existe depois do INSERT da campanha, dentro da mesma transação).
type ProgramacaoInput struct {
	Campanha CreateCampaignInput
	Regras   []CreateDistributionRuleInput
	Precos   []EntradaPreco
}

// ProgramacaoGravada é o que ficou gravado.
type ProgramacaoGravada struct {
	Campanha *Campaign
	Regras   []DistributionRule
}

// Gravar roda os INSERTs na ordem campanha → regras → preços. Não valida: quem
// chama (acoes.ProgramarCampanha) já conferiu cada regra e passou cada preço por
// ValidatePricingInput. O erro do banco volta como veio (pgconn.PgError), para
// quem chama traduzir.
func (p *ProgramacaoDeCampanha) Gravar(ctx context.Context, in ProgramacaoInput) (*ProgramacaoGravada, error) {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("programacao: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	camp, err := insertCampaign(ctx, tx, in.Campanha)
	if err != nil {
		return nil, err
	}
	regras := make([]DistributionRule, 0, len(in.Regras))
	for _, r := range in.Regras {
		r.CampaignID = camp.ID
		gravada, err := insertRule(ctx, tx, r)
		if err != nil {
			return nil, err
		}
		regras = append(regras, *gravada)
	}
	if len(in.Precos) > 0 {
		if err := upsertPricingTx(ctx, tx, camp.ID, in.Precos); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("programacao: commit: %w", err)
	}
	return &ProgramacaoGravada{Campanha: camp, Regras: regras}, nil
}
