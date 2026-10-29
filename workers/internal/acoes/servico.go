package acoes

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"radiocheck/internal/catalog"
	"radiocheck/internal/hub"
)

// SupervisorDeCampanha é o que as ações pedem ao supervisor de workers
// (*supervisor.Supervisor satisfaz). Entra por interface para os testes
// usarem um falso que registra as chamadas.
type SupervisorDeCampanha interface {
	// UpdateStations troca target_stations da campanha e reconcilia os workers
	// em andamento (para/sobe os da diferença se a campanha está ativa).
	UpdateStations(campaignID uuid.UUID, stations []uuid.UUID) error
	// Reload manda o índice de hashes e os workers da campanha se
	// reconfigurarem (NATS index.reload), só faz efeito se estiver ativa.
	Reload(campaignID uuid.UUID) error
}

// AvisoHub é o subconjunto de *hub.Client que as ações de campanha e de
// cliente usam: o aviso fire-and-forget (campanha.upsert, cliente.upsert) e a
// conferência do código EH-XXXXXX.
type AvisoHub interface {
	Configured() bool
	Emitir(ctx context.Context, tipo string, dados any) error
	EmitirComResposta(ctx context.Context, tipo string, dados any) (*hub.RespostaEvento, error)
	ConferirCodigo(ctx context.Context, code string) (*hub.CampanhaDoHub, error)
}

var _ AvisoHub = (*hub.Client)(nil)

// Servico reúne os repositórios e os colaboradores das ações (F3-7).
type Servico struct {
	Campanhas         *catalog.Campaigns
	MateriaisCampanha *catalog.CampaignMaterials
	Materiais         *catalog.Materials
	Regras            *catalog.DistributionRules
	Ajustes           *catalog.DistributionOverrides
	Clientes          *catalog.Clients
	Emissoras         *catalog.Stations

	// Supervisor pode ser nil: mesma regra dos handlers — sem supervisor, a
	// ação só grava no banco.
	Supervisor SupervisorDeCampanha
	// Hub pode ser nil ou não configurado: a ação segue sem avisar o hub.
	Hub AvisoHub
	// Ligacoes liga a barreira da ligação com o hub no código da campanha.
	// nil = só a conferência antiga de "outro cliente".
	Ligacoes *catalog.Ligacoes

	// EmSegundoPlano roda o gatilho que não pode atrasar a resposta (a
	// recategorização de regra, por exemplo). nil = `go f()`. Os testes
	// injetam um que roda na hora (ou que espera) para observar o efeito.
	EmSegundoPlano func(func())
}

// NovoServico monta o Servico sobre o pool, com os repositórios que as telas usam.
func NovoServico(pool *pgxpool.Pool, sup SupervisorDeCampanha, h AvisoHub, lig *catalog.Ligacoes) *Servico {
	return &Servico{
		Campanhas:         catalog.NewCampaigns(pool),
		MateriaisCampanha: catalog.NewCampaignMaterials(pool),
		Materiais:         catalog.NewMaterials(pool),
		Regras:            catalog.NewDistributionRules(pool),
		Ajustes:           catalog.NewDistributionOverrides(pool),
		Clientes:          catalog.NewClients(pool),
		Emissoras:         catalog.NewStations(pool),
		Supervisor:        sup,
		Hub:               h,
		Ligacoes:          lig,
	}
}

// emSegundoPlano roda f fora do caminho da resposta (padrão `go f()`).
func (s *Servico) emSegundoPlano(f func()) {
	if s.EmSegundoPlano != nil {
		s.EmSegundoPlano(f)
		return
	}
	go f()
}
