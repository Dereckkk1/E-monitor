/*
Package hubnotify conserta a única falha silenciosa do caminho novo.

O emissor de `campanha.upsert` é dispara-e-esquece, sem retentativa. Até
2026-09-18 isso era tolerável porque a carga em lote do hub varria o Postgres e
trazia o que faltasse. Com a carga aposentada (§7.2 da spec), **evento perdido
vira campanha que nunca chega, e não há nada em tela nenhuma dizendo que falta
alguma**.

Este pacote fecha esse buraco com a coluna `hub_notified_at` e uma pergunta a
cada 15 minutos. Em regime ele devolve ZERO linhas — só cai na fila quem perdeu
o evento.
*/
package hubnotify

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"radiocheck/internal/catalog"
	"radiocheck/internal/hub"
)

// Intervalo entre ciclos (§8 da spec).
const Intervalo = 15 * time.Minute

/*
PorCiclo é o teto de campanhas por ciclo.

⚠️ Existe para o primeiro ciclo depois de uma queda LONGA do hub não virar uma
rajada de centenas de chamadas contra ele — exatamente quando ele acabou de
voltar e está mais frágil. O que sobrar fica para o ciclo seguinte; a fila não
tem pressa, e o limitador do hub é por IP (esta plataforma inteira divide um
balde só).
*/
const PorCiclo = 50

/*
Repo é o que este pacote precisa de `catalog.Campaigns`, e nada mais.

Interface e não o tipo concreto porque é o que deixa o teste rodar sem banco: os
casos que importam aqui — sucesso, recusa do hub, uma falha no meio de várias,
entregou mas não marcou — são sobre a ORDEM das chamadas, não sobre SQL. O SQL
tem os testes dele, no `catalog`.
*/
type Repo interface {
	PendentesDeHub(ctx context.Context, limite int) ([]catalog.Campaign, error)
	MarcarHubNotificada(ctx context.Context, id uuid.UUID) error
}

// Emissor manda o `campanha.upsert`. `EmissorHTTP` é a implementação de
// produção, com o `hub.Client` dentro.
type Emissor interface {
	Emitir(ctx context.Context, c catalog.Campaign) error
}

type EmissorHTTP struct{ Hub *hub.Client }

func (e EmissorHTTP) Emitir(ctx context.Context, c catalog.Campaign) error {
	/* ⚠️ `Hub.Emitir(ctx, tipo, dados)` é o MESMO método que o
	   `avisarHubDaCampanha` dos handlers usa. Montar um segundo caminho de
	   emissão aqui faria os dois divergirem no dia em que o envelope mudasse —
	   e o que divergiria é justamente a rede de segurança, que é a que ninguém
	   está olhando. */
	return e.Hub.Emitir(ctx, "campanha.upsert", hub.CampanhaUpsert{
		IDNaPlataforma:        c.ID.String(),
		IDClienteNaPlataforma: c.ClientID.String(),
		HubCode:               deref(c.HubCode),
	})
}

// RodarUmCiclo pergunta quem está pendente e tenta entregar cada um. Devolve
// quantas foram CONFIRMADAS (entregues e marcadas).
//
// O erro de retorno é só o do repositório: falha de UMA campanha não é falha do
// ciclo — ver o laço.
func RodarUmCiclo(ctx context.Context, repo Repo, emissor Emissor, limite int) (int, error) {
	pendentes, err := repo.PendentesDeHub(ctx, limite)
	if err != nil {
		return 0, err
	}

	entregues := 0
	for _, c := range pendentes {
		/* ⚠️ Campanha SEM código não sai daqui, e o motivo é pior que "não
		   adianta": um `campanha.upsert` com `hubCode` vazio NÃO é ignorado
		   pelo hub quando já existe proposta com aquela fonte — ele é lido como
		   "o código foi apagado" e CONGELA a coleta da proposta (§6.5, desfecho
		   `congela` do `campanhaDaPlataforma.ts`). Um job que reemitisse
		   campanha sem código desligaria a coleta de clientes que estão
		   funcionando.

		   O `PendentesDeHub` já filtra por `hub_code IS NOT NULL`, então isto é
		   defesa em profundidade — e vale o custo, porque o modo de falha é
		   silencioso e acontece do lado de fora. */
		if strings.TrimSpace(deref(c.HubCode)) == "" {
			zap.L().Warn("hubnotify: campanha sem codigo na fila — ignorada",
				zap.String("campaign_id", c.ID.String()))
			continue
		}

		if err := emissor.Emitir(ctx, c); err != nil {
			/* Segue para a próxima: uma campanha cujo código não existe mais no
			   hub falha para SEMPRE, e parar o laço nela deixaria todas as
			   outras presas atrás dela. */
			zap.L().Warn("hubnotify: reemissao falhou",
				zap.String("campaign_id", c.ID.String()), zap.Error(err))
			continue
		}

		if err := repo.MarcarHubNotificada(ctx, c.ID); err != nil {
			/* Entregou e não marcou: o `hub_notified_at` fica nulo e o próximo
			   ciclo reemite esta campanha. É seguro — o `campanha.upsert` é
			   idempotente por cliente do outro lado (§6.3) —, e por isso ela
			   NÃO entra no placar: contá-la faria o log dizer "entreguei 3" num
			   ciclo que vai repetir uma delas. */
			zap.L().Warn("hubnotify: entregou mas nao marcou hub_notified_at",
				zap.String("campaign_id", c.ID.String()), zap.Error(err))
			continue
		}
		entregues++
	}
	return entregues, nil
}

/*
Iniciar sobe o laço em segundo plano. Morre com o contexto.

⚠️ O primeiro ciclo é DEPOIS do primeiro intervalo, não no boot. O `cmd/api`
sobe junto com o resto do sistema, e um ciclo no instante do boot chegaria ao
hub junto com todo o resto do deploy. Quem acabou de criar campanha já foi
avisado pelo emissor; esta fila é para o que se PERDEU, e quinze minutos de
atraso nela não custam nada.
*/
func Iniciar(ctx context.Context, repo Repo, emissor Emissor) {
	go func() {
		t := time.NewTicker(Intervalo)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				n, err := RodarUmCiclo(ctx, repo, emissor, PorCiclo)
				if err != nil {
					zap.L().Warn("hubnotify: ciclo falhou", zap.Error(err))
					continue
				}
				// Só fala quando fez alguma coisa: em regime este job é mudo, e
				// um "0 entregues" a cada 15 minutos afogaria o log em ruído
				// que ninguém lê — até o dia em que houvesse algo para ler.
				if n > 0 {
					zap.L().Info("hubnotify: campanhas reentregues ao hub", zap.Int("quantas", n))
				}
			}
		}
	}()
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
