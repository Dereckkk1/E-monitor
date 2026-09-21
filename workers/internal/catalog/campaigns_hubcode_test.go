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
	// Recém-criada, o hub ainda não sabe dela: é o que põe a campanha na fila
	// do job de reemissão se o evento se perder.
	require.Nil(t, got.HubNotifiedAt)
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
func TestTodoLeitorDeCampanhaTrazOHubCode(t *testing.T) {
	ctx, pool := hubCodePool(t)
	repo := NewCampaigns(pool)
	cliente := seedClienteHubCode(t, ctx, pool)
	criada := campanhaCom(t, ctx, repo, cliente, "Com código", "EH-7K4M2X")

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

	require.NoError(t, repo.MarcarHubNotificada(ctx, c.ID))
	antes, err := repo.Get(ctx, c.ID)
	require.NoError(t, err)
	require.NotNil(t, antes.HubNotifiedAt, "pré-condição: tinha de estar confirmada")

	require.NoError(t, repo.AtualizarHubCode(ctx, c.ID, "EH-BBBBBB"))

	depois, err := repo.Get(ctx, c.ID)
	require.NoError(t, err)
	require.Equal(t, "EH-BBBBBB", *depois.HubCode)
	require.Nil(t, depois.HubNotifiedAt, "trocar o código tem de devolver a campanha para a fila")
}

// Apagar o código também zera: sem código ela não entra na fila (é a regra do
// §8 — "só reemite o que tem código"), e deixar a confirmação velha ali seria
// afirmar que o hub conhece um vínculo que não existe mais.
func TestApagarOHubCodeDeixaNuloEZeraAConfirmacao(t *testing.T) {
	ctx, pool := hubCodePool(t)
	repo := NewCampaigns(pool)
	cliente := seedClienteHubCode(t, ctx, pool)
	c := campanhaCom(t, ctx, repo, cliente, "Vai perder o código", "EH-AAAAAA")
	require.NoError(t, repo.MarcarHubNotificada(ctx, c.ID))

	require.NoError(t, repo.AtualizarHubCode(ctx, c.ID, ""))

	depois, err := repo.Get(ctx, c.ID)
	require.NoError(t, err)
	require.Nil(t, depois.HubCode)
	require.Nil(t, depois.HubNotifiedAt)
}

func TestPendentesDeHubSoTrazQuemTemCodigoESemConfirmacao(t *testing.T) {
	ctx, pool := hubCodePool(t)
	repo := NewCampaigns(pool)
	cliente := seedClienteHubCode(t, ctx, pool)

	pendente := campanhaCom(t, ctx, repo, cliente, "Pendente", "EH-AAAAAA")
	confirmada := campanhaCom(t, ctx, repo, cliente, "Confirmada", "EH-BBBBBB")
	semCodigo := campanhaCom(t, ctx, repo, cliente, "Sem código", "")
	require.NoError(t, repo.MarcarHubNotificada(ctx, confirmada.ID))

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

// O `limit` existe para o primeiro ciclo depois de uma queda longa do hub não
// virar uma rajada de centenas de chamadas.
func TestPendentesDeHubRespeitaOLimite(t *testing.T) {
	ctx, pool := hubCodePool(t)
	repo := NewCampaigns(pool)
	cliente := seedClienteHubCode(t, ctx, pool)
	for i := 0; i < 3; i++ {
		campanhaCom(t, ctx, repo, cliente, "Pendente", "EH-AAAAA"+string(rune('A'+i)))
	}

	rows, err := repo.PendentesDeHub(ctx, 2)
	require.NoError(t, err)
	require.Len(t, rows, 2)
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
