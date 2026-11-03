package acoes

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"radiocheck/internal/catalog"
	"radiocheck/internal/hub"
	"radiocheck/internal/users"
	"radiocheck/internal/webhook"
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
	// StopWorkersForCampaign derruba os workers das emissoras que ficaram sem
	// campanha ativa (depois de cancelar). Não devolve erro.
	StopWorkersForCampaign(campaignID uuid.UUID)
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
	// Delete devolve as linhas apagadas: 0 = a chave já não existia (ApagarAjuste a trata como
	// nao_encontrado + ErrAlvoInexistente).
	Delete(ctx context.Context, campaignID, typeID, stationID uuid.UUID, forDate time.Time) (int64, error)
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
	// Precos é o preço por emissora × campanha (DefinirPrecos).
	Precos *catalog.Pricing

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

	// Veiculacoes é o repositório das tocadas (veiculação manual, ignorar,
	// restaurar).
	Veiculacoes *catalog.Detections

	// Usuarios é o repositório dos usuários (CriarUsuario, EditarUsuario).
	Usuarios *users.Repo
	// BoasVindas emite o convite de boas-vindas. nil = indisponível (a criação
	// segue e devolve email_status "unavailable").
	BoasVindas EmissorBoasVindas

	// Webhooks é a outbox dos webhooks (TestarWebhook). nil = indisponível
	// (ErrWebhooksIndisponivel). Passe nil de verdade, não um *webhook.Outbox nil
	// dentro da interface.
	Webhooks webhook.OutboxEnqueuer

	// Agora é o relógio da validação "não está no futuro". nil = time.Now.
	Agora func() time.Time

	// releituraUsuario substitui a releitura do usuário que o CriarUsuario faz depois de gravar a
	// carteira. Só os testes a preenchem (para a releitura falhar com a gravação já certa).
	releituraUsuario func(ctx context.Context, id uuid.UUID) (*users.User, error)
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
		Precos:            catalog.NewPricing(pool),
		Veiculacoes:       catalog.NewDetections(pool),
		Usuarios:          users.NewRepo(pool),
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
