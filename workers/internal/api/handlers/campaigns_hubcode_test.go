package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/catalog"
	"radiocheck/internal/db"
	"radiocheck/internal/dbtest"
	"radiocheck/internal/hub"
)

// ⚠️ PULAM sem TEST_DATABASE_URL, e um teste pulado sai com exit 0 e parece
// verde. Conte os SKIP ao relatar o placar.
func poolHubCode(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, url, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { pool.Close() })
	dbtest.GuardOrSkip(t, ctx, pool)
	return ctx, pool
}

// Um cliente descartável. As campanhas dele saem junto, pela FK.
func seedClienteLocal(t *testing.T, ctx context.Context, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO clients (name) VALUES ($1) RETURNING id`,
		"Cliente do código "+uuid.NewString()[:8]).Scan(&id))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM campaigns WHERE client_id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, id)
	})
	return id
}

/*
Um hub falso no nível do HTTP, e não do nosso código: assim o `internal/hub`
inteiro — cabeçalho, normalização, tradução de status, parsing — roda de
verdade. É o mesmo padrão do `hubFalso` de `hubsso_test.go`.

`emitiu` recebe os eventos que o `avisarHubDaCampanha` disparar, para os testes
poderem afirmar o que foi (ou não foi) emitido.
*/
func hubFalsoParaCodigo(t *testing.T, status int, corpo string) (*hub.Client, chan map[string]any) {
	t.Helper()
	emitiu := make(chan map[string]any, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/platform/campaigns/by-code/") {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(corpo))
			return
		}
		// O resto é a porta de eventos.
		var env map[string]any
		_ = json.NewDecoder(r.Body).Decode(&env)
		select {
		case emitiu <- env:
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return hub.New(srv.URL, "chave"), emitiu
}

func postCampanha(t *testing.T, h *CampaignsHandler, corpo map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(corpo)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/campaigns", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	return rec
}

func corpoDoHub(idNaPlataforma string) string {
	return `{"hubCampaignId":"66f0","nome":"Verão 2026","inicio":"2026-12-01","fim":"2027-02-28",` +
		`"cliente":{"id":"66e1","nome":"Rôgga","idNaPlataforma":"` + idNaPlataforma + `"}}`
}

func campanhaNova(cliente uuid.UUID, codigo string) map[string]any {
	return map[string]any{
		"client_id": cliente.String(), "name": "Verão 2026",
		"start_date": "2026-12-01T00:00:00Z", "end_date": "2027-02-28T00:00:00Z",
		"hub_code": codigo,
	}
}

func TestCreateRecusaCodigoDeOutroCliente(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)
	outro := uuid.New()
	hb, _ := hubFalsoParaCodigo(t, http.StatusOK, corpoDoHub(outro.String()))

	rec := postCampanha(t, &CampaignsHandler{Repo: catalog.NewCampaigns(pool), Hub: hb},
		campanhaNova(cliente, "EH-7K4M2X"))

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	// A mensagem tem de dizer QUAL é o problema, e de quem é o código: quem
	// cadastrou vai ter de achar o código certo, e "inválido" não ajuda nisso.
	require.Contains(t, rec.Body.String(), "outro cliente")
	require.Contains(t, rec.Body.String(), "Rôgga")
	require.Contains(t, rec.Body.String(), "Verão 2026")
	// E NADA foi gravado.
	require.Zero(t, contaCampanhas(t, ctx, pool, cliente))
}

func TestCreateRecusaCodigoInexistente(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)
	hb, _ := hubFalsoParaCodigo(t, http.StatusNotFound, `{}`)

	rec := postCampanha(t, &CampaignsHandler{Repo: catalog.NewCampaigns(pool), Hub: hb},
		campanhaNova(cliente, "EH-ZZZZZZ"))

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Zero(t, contaCampanhas(t, ctx, pool, cliente))
}

/*
⚠️ 201 e NÃO 422: hub fora do ar não pode parar o cadastro (decisão 2 da spec).
As meninas continuam trabalhando, o `hub_notified_at` fica nulo, e o job de
reemissão leva a campanha quando o hub voltar.
*/
func TestCreateAceitaQuandoOHubNaoResponde(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)
	// 503 é o que o `ConferirCodigo` devolve tanto para hub mudo quanto para
	// 401/429/5xx — todos "problema nosso", nenhum culpa de quem digitou.
	hb, _ := hubFalsoParaCodigo(t, http.StatusServiceUnavailable, `{}`)

	rec := postCampanha(t, &CampaignsHandler{Repo: catalog.NewCampaigns(pool), Hub: hb},
		campanhaNova(cliente, "EH-7K4M2X"))

	require.Equal(t, http.StatusCreated, rec.Code)
	require.Equal(t, 1, contaCampanhas(t, ctx, pool, cliente))
	// E o código foi gravado, senão a campanha nunca entraria na fila do job.
	require.Equal(t, "EH-7K4M2X", codigoGravado(t, ctx, pool, cliente))
}

// Ausência de ponte não é divergência: o cliente do hub ainda não foi ligado a
// este E-monitor, e não há o que comparar. Recusar aqui faria todo cliente
// ainda não ligado ser barrado justo na primeira campanha dele.
func TestCreateAceitaClienteDoHubSemPonte(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)
	hb, _ := hubFalsoParaCodigo(t, http.StatusOK, corpoDoHub(""))

	rec := postCampanha(t, &CampaignsHandler{Repo: catalog.NewCampaigns(pool), Hub: hb},
		campanhaNova(cliente, "EH-7K4M2X"))

	require.Equal(t, http.StatusCreated, rec.Code)
	require.Equal(t, 1, contaCampanhas(t, ctx, pool, cliente))
}

func TestCreateAceitaCodigoDoProprioCliente(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)
	hb, _ := hubFalsoParaCodigo(t, http.StatusOK, corpoDoHub(cliente.String()))

	rec := postCampanha(t, &CampaignsHandler{Repo: catalog.NewCampaigns(pool), Hub: hb},
		campanhaNova(cliente, "EH-7K4M2X"))

	require.Equal(t, http.StatusCreated, rec.Code)
	require.Equal(t, "EH-7K4M2X", codigoGravado(t, ctx, pool, cliente))
}

/*
⚠️ O código é gravado na forma CANÔNICA, venha como vier.

É o que a §10 exige, e sem isto `EH-7K4M2X` e `eh7k4m2x` viram códigos
diferentes na coluna — enquanto para o hub são o mesmo. A decisão 5 diz que dois
PIs compartilham um código de propósito, então agrupar por ele tem significado.
*/
func TestCreateGravaOCodigoNaFormaCanonica(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)
	hb, _ := hubFalsoParaCodigo(t, http.StatusOK, corpoDoHub(cliente.String()))

	rec := postCampanha(t, &CampaignsHandler{Repo: catalog.NewCampaigns(pool), Hub: hb},
		campanhaNova(cliente, "  eh 7k4m2x  "))

	require.Equal(t, http.StatusCreated, rec.Code)
	require.Equal(t, "EH-7K4M2X", codigoGravado(t, ctx, pool, cliente))
}

// Campanha sem código continua sendo criada: o campo é obrigatório só na TELA
// de criação (decisão 6), e campanha antiga continua salvando sem ele.
func TestCreateSemCodigoNaoConsultaOHub(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)
	consultou := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "by-code") {
			consultou = true
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	rec := postCampanha(t, &CampaignsHandler{Repo: catalog.NewCampaigns(pool), Hub: hub.New(srv.URL, "k")},
		campanhaNova(cliente, ""))

	require.Equal(t, http.StatusCreated, rec.Code)
	require.False(t, consultou, "campanha sem código não tem o que conferir")
}

// Instalação sem hub configurado cria campanha igual — é o mesmo portão que o
// SSO já usa, e é o que faz dev e teste não baterem em produção.
func TestCreateSemHubConfiguradoSegue(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)

	rec := postCampanha(t, &CampaignsHandler{Repo: catalog.NewCampaigns(pool), Hub: nil},
		campanhaNova(cliente, "EH-7K4M2X"))

	require.Equal(t, http.StatusCreated, rec.Code)
	require.Equal(t, "EH-7K4M2X", codigoGravado(t, ctx, pool, cliente))
}

func contaCampanhas(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cliente uuid.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM campaigns WHERE client_id = $1`, cliente).Scan(&n))
	return n
}

func codigoGravado(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cliente uuid.UUID) string {
	t.Helper()
	var c *string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT hub_code FROM campaigns WHERE client_id = $1`, cliente).Scan(&c))
	if c == nil {
		return ""
	}
	return *c
}

/*
⚠️ Texto que não é código é recusado AQUI, sem ir ao hub.

Sem esta recusa, "abc" cairia no `NULLIF(btrim(...))` do repositório, que só
tira espaço: a coluna guardaria "abc", a campanha entraria na fila de reemissão,
e o job mandaria ao hub, de 15 em 15 minutos e para sempre, um código que ele
recusa. Nada em tela nenhuma denunciaria.
*/
func TestCreateRecusaTextoQueNaoEhCodigo(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)
	consultou := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "by-code") {
			consultou = true
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	for _, ruim := range []string{"abc", "EH-7K4M2XY", "EH-7K4M2O"} {
		t.Run(ruim, func(t *testing.T) {
			rec := postCampanha(t, &CampaignsHandler{Repo: catalog.NewCampaigns(pool), Hub: hub.New(srv.URL, "k")},
				campanhaNova(cliente, ruim))
			require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
		})
	}
	require.False(t, consultou, "código que nem é código não vai à rede")
	require.Zero(t, contaCampanhas(t, ctx, pool, cliente))
}

/*
⚠️ Recusado o código, NENHUM evento sai.

O `avisarHubDaCampanha` roda depois do `Repo.Create`, então um `return` que
esquecesse de sair antes dele emitiria `campanha.upsert` para uma campanha que
não existe. O hub trataria como `codigo-inexistente` e ignoraria — ou seja, o
estrago ficaria invisível dos dois lados.
*/
func TestCreateRecusadoNaoEmiteEvento(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)
	hb, emitiu := hubFalsoParaCodigo(t, http.StatusNotFound, `{}`)

	rec := postCampanha(t, &CampaignsHandler{Repo: catalog.NewCampaigns(pool), Hub: hb},
		campanhaNova(cliente, "EH-ZZZZZZ"))

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	select {
	case env := <-emitiu:
		t.Fatalf("saiu evento para uma campanha que não foi criada: %v", env)
	default:
	}
}
