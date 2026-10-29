package acoes

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

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

// RepositorioAjustes é o que DefinirAjuste/ApagarAjuste pedem ao banco
// (*catalog.DistributionOverrides satisfaz). Entra por interface porque o handler
// HTTP dos ajustes sempre aceitou um repositório substituível nos testes.
type RepositorioAjustes interface {
	Upsert(ctx context.Context, in catalog.UpsertOverrideInput) error
	Delete(ctx context.Context, campaignID, typeID, stationID uuid.UUID, forDate time.Time) error
}

// RecategorizadorDeCelula refaz a categoria de UMA célula-dia depois de mexer
// num ajuste (*catalog.DistributionRules satisfaz).
type RecategorizadorDeCelula interface {
	RecategorizeForOverride(ctx context.Context, campaignID, typeID, stationID uuid.UUID, forDate time.Time) error
}

var (
	_ RepositorioAjustes      = (*catalog.DistributionOverrides)(nil)
	_ RecategorizadorDeCelula = (*catalog.DistributionRules)(nil)
)

// Servico reúne os repositórios e os colaboradores das ações (F3-7).
type Servico struct {
	Campanhas         *catalog.Campaigns
	MateriaisCampanha *catalog.CampaignMaterials
	Materiais         *catalog.Materials
	Regras            *catalog.DistributionRules
	Ajustes           RepositorioAjustes
	// RecatCelula recategoriza a célula-dia dos ajustes. nil = não recategoriza
	// (o ajuste é gravado igual — a rota sempre foi nil-safe aqui).
	RecatCelula RecategorizadorDeCelula
	Clientes    *catalog.Clients
	Emissoras   *catalog.Stations

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
	regras := catalog.NewDistributionRules(pool)
	return &Servico{
		Campanhas:         catalog.NewCampaigns(pool),
		MateriaisCampanha: catalog.NewCampaignMaterials(pool),
		Materiais:         catalog.NewMaterials(pool),
		Regras:            regras,
		Ajustes:           catalog.NewDistributionOverrides(pool),
		RecatCelula:       regras,
		Clientes:          catalog.NewClients(pool),
		Emissoras:         catalog.NewStations(pool),
		Supervisor:        sup,
		Hub:               h,
		Ligacoes:          lig,
	}
}

// emSegundoPlano roda f fora do caminho da resposta. Em produção
// (EmSegundoPlano nil) é uma goroutine que RECUPERA de pânico: um gatilho que
// quebra (recategorização, aviso ao hub) é logado com a pilha e não derruba o
// processo da API. Não há limite de concorrência — um pico de edições dispara
// uma goroutine por gatilho; limitá-las é follow-up.
func (s *Servico) emSegundoPlano(f func()) {
	if s.EmSegundoPlano != nil {
		s.EmSegundoPlano(f)
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				zap.L().Error("acoes: gatilho em segundo plano entrou em pânico",
					zap.Any("panic", r), zap.Stack("stack"))
			}
		}()
		f()
	}()
}
