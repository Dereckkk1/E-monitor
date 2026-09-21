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
	"fmt"
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
	MarcarHubNotificada(ctx context.Context, id uuid.UUID, codigoEntregue string) error
	// MarcarTentativaDeHub carimba a tentativa — é o que faz a fila rodar em
	// vez de morrer na cabeça. Ver o comentário do laço.
	MarcarTentativaDeHub(ctx context.Context, id uuid.UUID) error
}

// ⚠️ A asserção que faz o contrato falhar NO PACOTE QUE O DECLARA. Sem ela,
// renomear um método no `catalog` quebra o `cmd/api` e deixa `go build
// ./internal/...` verde — a falha cai em quem CONSOME o contrato, não em quem o
// define.
var _ Repo = (*catalog.Campaigns)(nil)

// Emissor manda o `campanha.upsert`. `EmissorHTTP` é a implementação de
// produção, com o `hub.Client` dentro.
type Emissor interface {
	Emitir(ctx context.Context, c catalog.Campaign) error
}

type EmissorHTTP struct{ Hub *hub.Client }

func (e EmissorHTTP) Emitir(ctx context.Context, c catalog.Campaign) error {
	/* ⚠️ O MESMO método que o `avisarHubDaCampanha` dos handlers usa. Montar um
	   segundo caminho de emissão faria os dois divergirem no dia em que o
	   envelope mudasse — e o que divergiria é justamente a rede de segurança,
	   que é a que ninguém está olhando. */
	resposta, err := e.Hub.EmitirComResposta(ctx, "campanha.upsert", hub.CampanhaUpsert{
		IDNaPlataforma:        c.ID.String(),
		IDClienteNaPlataforma: c.ClientID.String(),
		HubCode:               deref(c.HubCode),
	})
	if err != nil {
		return err
	}
	/* ⚠️ 200 NÃO É ENTREGA, e ler só o status era um defeito crítico medido em
	   2026-09-21. O hub responde 200 com `{"acao":"ignorado","motivo":"..."}`
	   quando DESCARTA o evento pela regra dele — código inexistente, cliente
	   divergente, código malformado. Tratando isso como sucesso, o job marcava
	   `hub_notified_at` e a campanha saía da fila PARA SEMPRE sem nunca ter
	   chegado ao hub: a rede de segurança do §8 fechando o próprio buraco que
	   existe para vigiar, e o log dizendo "3 reentregues" quando foram 3
	   descartadas. */
	if resposta.Ignorado() {
		return fmt.Errorf("o hub ignorou o evento (motivo: %s)", resposta.Motivo)
	}
	return nil
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

		/* ⚠️ A tentativa é carimbada ANTES do resultado, e de propósito. É ela
		   que tira esta campanha da cabeça da fila no próximo ciclo, e é
		   justamente quando a entrega FALHA que isso precisa acontecer —
		   carimbar só no sucesso deixaria as que falham sempre presas no topo,
		   impedindo todas as outras de serem tentadas uma única vez.

		   Se o carimbo falhar, seguimos assim mesmo: perder o rodízio de um
		   ciclo é muito menos grave que não tentar entregar. */
		if err := repo.MarcarTentativaDeHub(ctx, c.ID); err != nil {
			zap.L().Warn("hubnotify: nao deu para carimbar a tentativa",
				zap.String("campaign_id", c.ID.String()), zap.Error(err))
		}

		if err := emissor.Emitir(ctx, c); err != nil {
			/* Segue para a próxima: uma campanha cujo código não existe mais no
			   hub falha para SEMPRE, e parar o laço nela deixaria todas as
			   outras presas atrás dela. */
			zap.L().Warn("hubnotify: reemissao falhou",
				zap.String("campaign_id", c.ID.String()), zap.Error(err))
			continue
		}

		if err := repo.MarcarHubNotificada(ctx, c.ID, deref(c.HubCode)); err != nil {
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
	IniciarCom(ctx, repo, emissor, Intervalo)
}

// IniciarCom é o `Iniciar` com o intervalo aberto. Existe para o TESTE: com o
// intervalo fixo em 15 minutos, um teste de "o laço para quando o contexto
// morre" dorme 200ms e passa com o cancelamento REMOVIDO — medido, 13 de 13
// verdes com o `case <-ctx.Done()` apagado. Asserção que não consegue falhar
// não prova nada, e sem intervalo injetável não existe teste honesto do laço.
func IniciarCom(ctx context.Context, repo Repo, emissor Emissor, intervalo time.Duration) {
	go func() {
		t := time.NewTicker(intervalo)
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
