package catalog

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// ⚠️ PULAM sem TEST_DATABASE_URL, e um teste pulado sai com exit 0 e parece
// verde — conte os SKIP ao relatar o placar. Mesma nota de `hubclients_test.go`.
func hubCodePool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return ctx, pool
}

// Um cliente descartável, limpo no fim. As campanhas saem junto pela FK.
func seedClienteHubCode(t *testing.T, ctx context.Context, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO clients (name) VALUES ($1) RETURNING id`,
		"Cliente do código do hub "+uuid.NewString()[:8]).Scan(&id))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM campaigns WHERE client_id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, id)
	})
	return id
}

func campanhaCom(t *testing.T, ctx context.Context, repo *Campaigns, cliente uuid.UUID, nome, codigo string) *Campaign {
	t.Helper()
	c, err := repo.Create(ctx, CreateCampaignInput{
		ClientID:  cliente,
		Name:      nome,
		StartDate: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2027, 2, 28, 0, 0, 0, 0, time.UTC),
		HubCode:   codigo,
	})
	require.NoError(t, err)
	return c
}

func TestCreateGuardaHubCode(t *testing.T) {
	ctx, pool := hubCodePool(t)
	repo := NewCampaigns(pool)
	cliente := seedClienteHubCode(t, ctx, pool)

	got := campanhaCom(t, ctx, repo, cliente, "Verão 2026 — SP", "EH-7K4M2X")

	require.NotNil(t, got.HubCode)
	require.Equal(t, "EH-7K4M2X", *got.HubCode)

	/* Recém-criada, o hub ainda não sabe dela: é o que põe a campanha na fila do
	   job de reemissão se o evento se perder.

	   ⚠️ A conferência é pelo `Get`, e não pelo `got` do `Create`. `nil` é o
	   valor ZERO do ponteiro: `require.Nil(t, got.HubNotifiedAt)` passava
	   também quando o `RETURNING` do `Create` perdia a coluna — medido, a suíte
	   ficava verde. Uma asserção que não consegue falhar no caso que promete
	   cobrir não é rede, é decoração. Lendo do banco, ela falsifica. */
	lido, err := repo.Get(ctx, got.ID)
	require.NoError(t, err)
	require.Nil(t, lido.HubNotifiedAt)
	require.NotNil(t, lido.HubCode, "o Create gravou o código mesmo?")
}

// String vazia vira NULL: "" e "não tem" são a mesma coisa aqui, e deixar as
// duas conviverem faria o job achar campanha de código vazio — e mandar ao hub
// um evento que ele recusaria por falta de código, para sempre, a cada 15 min.
func TestCreateSemHubCodeGuardaNull(t *testing.T) {
	ctx, pool := hubCodePool(t)
	repo := NewCampaigns(pool)
	cliente := seedClienteHubCode(t, ctx, pool)

	for _, caso := range []struct{ rotulo, codigo string }{
		{"ausente", ""},
		{"só espaços", "   "},
	} {
		t.Run(caso.rotulo, func(t *testing.T) {
			got := campanhaCom(t, ctx, repo, cliente, "Antiga "+caso.rotulo, caso.codigo)
			require.Nil(t, got.HubCode, "código %q devia virar NULL", caso.codigo)
		})
	}
}

/*
⚠️ O teste que o plano NÃO tem, e é o de maior risco da task.

O plano avisa em prosa que "todo SELECT de campanha deste arquivo precisa das
duas colunas novas na lista e no Scan, ou o Get devolve hub_code vazio e a tela
de edição nasce sem o código preenchido" — e depois não deixa nada guardando
isso. Esquecer um dos leitores compila, passa em todos os outros testes, e o
defeito só aparece na tela: o campo vem vazio, a pessoa salva, e o código some
sem nada ficar vermelho.

Uma tabela por leitor é o que transforma o aviso em rede.
*/
func TestTodoLeitorDeCampanhaTrazAsDuasColunas(t *testing.T) {
	ctx, pool := hubCodePool(t)
	repo := NewCampaigns(pool)
	cliente := seedClienteHubCode(t, ctx, pool)
	criada := campanhaCom(t, ctx, repo, cliente, "Com código", "EH-7K4M2X")
	/* ⚠️ Confirmada de propósito, e é o que faz esta tabela guardar as DUAS
	   colunas em vez de uma. A primeira versão só afirmava o `HubCode`: tirar
	   `hub_notified_at` do SELECT do `ListPaged` — a consulta que serve a tela
	   principal — deixava a suíte inteira verde, e o campo voltaria `null` para
	   toda campanha, para sempre.

	   Nenhum leitor da tabela escreve nesta coluna (o `UpdateFixedCPM` mexe só
	   no CPM), então a ordem dos subtestes continua sem importar. */
	require.NoError(t, repo.MarcarHubNotificada(ctx, criada.ID, "EH-7K4M2X"))

	leitores := []struct {
		nome string
		ler  func(t *testing.T) *Campaign
	}{
		{"Get", func(t *testing.T) *Campaign {
			c, err := repo.Get(ctx, criada.ID)
			require.NoError(t, err)
			return c
		}},
		{"List", func(t *testing.T) *Campaign {
			cs, err := repo.List(ctx)
			require.NoError(t, err)
			return acha(t, cs, criada.ID)
		}},
		{"ListFiltered", func(t *testing.T) *Campaign {
			cs, err := repo.ListFiltered(ctx, nil, []uuid.UUID{cliente})
			require.NoError(t, err)
			return acha(t, cs, criada.ID)
		}},
		{"ListPaged", func(t *testing.T) *Campaign {
			cs, _, err := repo.ListPaged(ctx, "", "", []uuid.UUID{cliente}, nil, 1, 50)
			require.NoError(t, err)
			return acha(t, cs, criada.ID)
		}},
		{"UpdateBasic", func(t *testing.T) *Campaign {
			c, err := repo.UpdateBasic(ctx, criada.ID, UpdateBasicInput{
				Name: "Com código", StartDate: criada.StartDate, EndDate: criada.EndDate,
			})
			require.NoError(t, err)
			return c
		}},
		{"UpdateFixedCPM", func(t *testing.T) *Campaign {
			c, err := repo.UpdateFixedCPM(ctx, criada.ID, nil)
			require.NoError(t, err)
			return c
		}},
	}

	for _, l := range leitores {
		t.Run(l.nome, func(t *testing.T) {
			c := l.ler(t)
			require.NotNil(t, c.HubCode, "%s perdeu o hub_code — o SELECT dele não tem a coluna", l.nome)
			require.Equal(t, "EH-7K4M2X", *c.HubCode)
			require.NotNil(t, c.HubNotifiedAt, "%s perdeu o hub_notified_at — o SELECT dele não tem a coluna", l.nome)
		})
	}
}

func acha(t *testing.T, cs []Campaign, id uuid.UUID) *Campaign {
	t.Helper()
	for i := range cs {
		if cs[i].ID == id {
			return &cs[i]
		}
	}
	t.Fatalf("campanha %v não veio na lista (%d linhas)", id, len(cs))
	return nil
}

/*
⚠️ Trocar o código TEM de zerar a confirmação, e o plano escreve a função com
esse aviso mas sem teste nenhum.

O código novo aponta para OUTRA campanha do hub, que nunca ouviu falar desta.
Sem zerar, o job acharia que já avisou, e a campanha ficaria amarrada ao lugar
antigo para sempre — sem nada em tela denunciando.
*/
func TestAtualizarHubCodeZeraAConfirmacao(t *testing.T) {
	ctx, pool := hubCodePool(t)
	repo := NewCampaigns(pool)
	cliente := seedClienteHubCode(t, ctx, pool)
	c := campanhaCom(t, ctx, repo, cliente, "Vai trocar de código", "EH-AAAAAA")

	require.NoError(t, repo.MarcarHubNotificada(ctx, c.ID, deref(c.HubCode)))
	antes, err := repo.Get(ctx, c.ID)
	require.NoError(t, err)
	require.NotNil(t, antes.HubNotifiedAt, "pré-condição: tinha de estar confirmada")

	require.NoError(t, repo.AtualizarHubCode(ctx, c.ID, "EH-BBBBBB"))

	depois, err := repo.Get(ctx, c.ID)
	require.NoError(t, err)
	require.Equal(t, "EH-BBBBBB", *depois.HubCode)
	require.Nil(t, depois.HubNotifiedAt, "trocar o código tem de devolver a campanha para a fila")
}

/*
Apagar o código também zera: sem código ela não entra na fila (é a regra do §8 —
"só reemite o que tem código"), e deixar a confirmação velha ali seria afirmar
que o hub conhece um vínculo que não existe mais.

⚠️ Os DOIS casos, e o de espaços é o que importa. O `btrim` do `Create` tinha
teste; o do `AtualizarHubCode` não — e medido, tirá-lo só daqui deixava a suíte
verde. É o caminho PIOR dos dois: `AtualizarHubCode` é o que a Task 6 chama
quando alguém reedita o código no wizard, que é exatamente onde o "colei com
espaço em volta" acontece.
*/
func TestApagarOHubCodeDeixaNuloEZeraAConfirmacao(t *testing.T) {
	ctx, pool := hubCodePool(t)
	repo := NewCampaigns(pool)
	cliente := seedClienteHubCode(t, ctx, pool)

	for _, caso := range []struct{ rotulo, novo string }{
		{"string vazia", ""},
		{"só espaços", "   "},
	} {
		t.Run(caso.rotulo, func(t *testing.T) {
			c := campanhaCom(t, ctx, repo, cliente, "Vai perder o código "+caso.rotulo, "EH-AAAAA"+caso.rotulo[:1])
			require.NoError(t, repo.MarcarHubNotificada(ctx, c.ID, deref(c.HubCode)))

			require.NoError(t, repo.AtualizarHubCode(ctx, c.ID, caso.novo))

			depois, err := repo.Get(ctx, c.ID)
			require.NoError(t, err)
			require.Nil(t, depois.HubCode, "%q tinha de virar NULL", caso.novo)
			require.Nil(t, depois.HubNotifiedAt)
		})
	}
}

func TestPendentesDeHubSoTrazQuemTemCodigoESemConfirmacao(t *testing.T) {
	ctx, pool := hubCodePool(t)
	repo := NewCampaigns(pool)
	cliente := seedClienteHubCode(t, ctx, pool)

	pendente := campanhaCom(t, ctx, repo, cliente, "Pendente", "EH-AAAAAA")
	confirmada := campanhaCom(t, ctx, repo, cliente, "Confirmada", "EH-BBBBBB")
	semCodigo := campanhaCom(t, ctx, repo, cliente, "Sem código", "")
	require.NoError(t, repo.MarcarHubNotificada(ctx, confirmada.ID, "EH-BBBBBB"))

	rows, err := repo.PendentesDeHub(ctx, 100)
	require.NoError(t, err)

	ids := map[uuid.UUID]bool{}
	for _, r := range rows {
		ids[r.ID] = true
	}
	require.True(t, ids[pendente.ID], "a pendente tinha de estar na fila")
	require.False(t, ids[confirmada.ID], "a confirmada não volta para a fila")
	// ⚠️ A mais importante das três: campanha sem código NUNCA entra na fila,
	// senão o job vira a carga em lote que esta série acabou de aposentar.
	require.False(t, ids[semCodigo.ID], "campanha sem código não entra na fila")
}

/*
O `limit` existe para o primeiro ciclo depois de uma queda longa do hub não
virar uma rajada de centenas de chamadas.

⚠️ DOIS limites, e não um. Com um só valor, cravar esse número no SQL (`LIMIT 2`
em vez de `LIMIT $1`) passa — medido. Um teste que fixa um único valor autoriza
hardcodar justamente esse valor.

⚠️ E confere QUAIS linhas vieram, não só quantas. `PendentesDeHub` é consulta
GLOBAL — não filtra cliente —, e o `t.Cleanup` da semente engole erro. Sobra de
uma rodada interrompida com `hub_code` preenchido satisfaz um `Len(2)` sem que
nenhuma das campanhas deste teste apareça.
*/
func TestPendentesDeHubRespeitaOLimite(t *testing.T) {
	ctx, pool := hubCodePool(t)
	repo := NewCampaigns(pool)
	cliente := seedClienteHubCode(t, ctx, pool)
	meus := map[uuid.UUID]bool{}
	for i := 0; i < 3; i++ {
		c := campanhaCom(t, ctx, repo, cliente, "Pendente", "EH-AAAAA"+string(rune('A'+i)))
		meus[c.ID] = true
	}

	curto, err := repo.PendentesDeHub(ctx, 2)
	require.NoError(t, err)
	require.Len(t, curto, 2)

	largo, err := repo.PendentesDeHub(ctx, 100)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(largo), 3, "com limite alto, as três têm de caber")

	nossas := 0
	for _, r := range largo {
		if meus[r.ID] {
			nossas++
		}
	}
	require.Equal(t, 3, nossas, "as três campanhas deste teste têm de estar na fila")
}

/*
A fila é FIFO, e isso tem consequência funcional além do índice: com `LIMIT` e
SEM ordenação, o Postgres pode devolver o mesmo subconjunto indefinidamente, e
uma campanha do fim da fila nunca sairia dela. O `ORDER BY created_at` não tinha
nada guardando — removê-lo deixava a suíte verde.
*/
func TestPendentesDeHubEhFilaPorAntiguidade(t *testing.T) {
	ctx, pool := hubCodePool(t)
	repo := NewCampaigns(pool)
	cliente := seedClienteHubCode(t, ctx, pool)

	/* ⚠️ A NOVA é inserida PRIMEIRO, e a velha depois (com o `created_at`
	   empurrado para trás). A ordem importa, e custou uma mutação para
	   descobrir: inserindo a velha primeiro, a ordem FÍSICA da tabela já
	   coincide com a cronológica, e tirar o `ORDER BY` deixava este teste
	   verde — o Postgres devolvia em ordem de heap, que dava o mesmo
	   resultado. Invertendo, as duas ordens DISCORDAM, e só quem ordena de
	   verdade passa. */
	nova := campanhaCom(t, ctx, repo, cliente, "Nova", "EH-NNNNNN")
	velha := campanhaCom(t, ctx, repo, cliente, "Velha", "EH-VVVVVV")
	_, err := pool.Exec(ctx, `UPDATE campaigns SET created_at = now() - interval '2 days' WHERE id = $1`, velha.ID)
	require.NoError(t, err)

	rows, err := repo.PendentesDeHub(ctx, 100)
	require.NoError(t, err)

	posVelha, posNova := -1, -1
	for i, r := range rows {
		if r.ID == velha.ID {
			posVelha = i
		}
		if r.ID == nova.ID {
			posNova = i
		}
	}
	require.NotEqual(t, -1, posVelha)
	require.NotEqual(t, -1, posNova)
	require.Less(t, posVelha, posNova, "a mais antiga sai primeiro da fila")
}

/*
⚠️ `LIMIT` negativo é ERRO do Postgres (SQLSTATE 2201W), não lista vazia: um
`limit` mal calculado derrubaria o ciclo inteiro do job em vez de não fazer
nada. O `ListPaged`, no mesmo arquivo, normaliza `page` e `pageSize` — esta não
normalizava.
*/
func TestPendentesDeHubNormalizaLimiteInvalido(t *testing.T) {
	ctx, pool := hubCodePool(t)
	repo := NewCampaigns(pool)
	cliente := seedClienteHubCode(t, ctx, pool)
	campanhaCom(t, ctx, repo, cliente, "Pendente", "EH-ZZZZZZ")

	for _, limite := range []int{0, -1} {
		rows, err := repo.PendentesDeHub(ctx, limite)
		require.NoError(t, err, "limite %d não pode derrubar o job", limite)
		require.NotEmpty(t, rows, "limite %d devia cair no padrão, não em zero linhas", limite)
	}
}

// A fila traz o CÓDIGO junto: o job precisa dele para remontar o evento, e uma
// segunda consulta por campanha seria N+1 numa varredura de 15 em 15 minutos.
func TestPendentesDeHubTrazOCodigo(t *testing.T) {
	ctx, pool := hubCodePool(t)
	repo := NewCampaigns(pool)
	cliente := seedClienteHubCode(t, ctx, pool)
	c := campanhaCom(t, ctx, repo, cliente, "Pendente", "EH-7K4M2X")

	rows, err := repo.PendentesDeHub(ctx, 100)
	require.NoError(t, err)
	for _, r := range rows {
		if r.ID == c.ID {
			require.NotNil(t, r.HubCode)
			require.Equal(t, "EH-7K4M2X", *r.HubCode)
			return
		}
	}
	t.Fatal("a campanha pendente não veio na fila")
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

/*
⚠️ A marcação só vale se o código gravado ainda for o que foi ENTREGUE.

Sem esta guarda, a goroutine de um evento ANTIGO — ainda em voo quando um PUT já
gravou um código novo — carimba a linha como notificada. Medido em 2026-09-21:
coluna `EH-BBBBBB`, hub recebeu só `EH-AAAAAA`, `hub_notified_at` preenchido, e a
campanha fora da fila de reemissão para sempre. O E-monitor passa a afirmar um
vínculo que o hub nunca recebeu, e nada reconcilia.
*/
func TestMarcarHubNotificadaIgnoraCodigoDesatualizado(t *testing.T) {
	ctx, pool := hubCodePool(t)
	repo := NewCampaigns(pool)
	cliente := seedClienteHubCode(t, ctx, pool)
	c := campanhaCom(t, ctx, repo, cliente, "Trocou de código no meio", "EH-BBBBBB")

	// O evento em voo era do código ANTIGO.
	require.NoError(t, repo.MarcarHubNotificada(ctx, c.ID, "EH-AAAAAA"))

	depois, err := repo.Get(ctx, c.ID)
	require.NoError(t, err)
	require.Nil(t, depois.HubNotifiedAt,
		"carimbou a confirmação de um código que o hub nunca recebeu")

	// E com o código certo ela marca normalmente — o controle positivo.
	require.NoError(t, repo.MarcarHubNotificada(ctx, c.ID, "EH-BBBBBB"))
	depois, err = repo.Get(ctx, c.ID)
	require.NoError(t, err)
	require.NotNil(t, depois.HubNotifiedAt)
}

// O congelamento: código NULL dos dois lados TEM de marcar. `NULL = NULL` é
// NULL, não verdadeiro — por isso o SQL usa `IS NOT DISTINCT FROM`.
func TestMarcarHubNotificadaFuncionaComCodigoNulo(t *testing.T) {
	ctx, pool := hubCodePool(t)
	repo := NewCampaigns(pool)
	cliente := seedClienteHubCode(t, ctx, pool)
	c := campanhaCom(t, ctx, repo, cliente, "Sem código", "")

	require.NoError(t, repo.MarcarHubNotificada(ctx, c.ID, ""))

	depois, err := repo.Get(ctx, c.ID)
	require.NoError(t, err)
	require.NotNil(t, depois.HubNotifiedAt, "o congelamento nunca seria confirmado")
}
