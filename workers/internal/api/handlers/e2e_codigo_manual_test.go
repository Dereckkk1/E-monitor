package handlers

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
	"radiocheck/internal/hub"
)

/*
O código da campanha contra o HUB DE VERDADE — a costura que nenhum hub falso prova.

Todo o resto da série foi medido contra `httptest.Server` escrito por quem
escreveu o parser: ele concorda com o parser por construção. O que ele NÃO pega
é o hub sendo Node/MongoDB do outro lado — um `idNaPlataforma` que chega `null`
em vez de ausente, um envelope que ganhou um nível, um `acao` renomeado, o
`hubCode` normalizado diferente. Esta série já foi mordida por duas dessas.

⚠️ E prova a coisa que mais importa e que o hub falso NUNCA pegaria: que um
`campanha.upsert` aceito VIRA PROPOSTA lá dentro. O hub responde 200 tanto para
"gravei" quanto para "ignorei" (`{"acao":"ignorado"}`), então "o POST deu 201" e
"a campanha chegou" são afirmações diferentes.

Pula por padrão. Para rodar, com o hub local no ar e o Postgres de teste:

	export TEST_DATABASE_URL="postgres://postgres:postgres@127.0.0.1:5433/radiocheck_test?sslmode=disable"
	HUB_E2E_CODIGO_URL=http://localhost:3011 \
	HUB_E2E_CODIGO_KEY=<a chave da plataforma e-monitor> \
	HUB_E2E_CODIGO_OK=EH-7K4M2X \
	HUB_E2E_CODIGO_CLIENTE=<o emonitorClientId do dono daquele código> \
	HUB_E2E_CODIGO_OUTRO=EH-9P3T7W \
	  go test ./internal/api/handlers -run ContraHubReal -v
*/
func e2eCodigoConfig(t *testing.T) (url, key, codigoOK, cliente, codigoOutro string) {
	t.Helper()
	url = os.Getenv("HUB_E2E_CODIGO_URL")
	key = os.Getenv("HUB_E2E_CODIGO_KEY")
	codigoOK = os.Getenv("HUB_E2E_CODIGO_OK")
	cliente = os.Getenv("HUB_E2E_CODIGO_CLIENTE")
	codigoOutro = os.Getenv("HUB_E2E_CODIGO_OUTRO")
	if url == "" || key == "" || codigoOK == "" || cliente == "" {
		t.Skip("HUB_E2E_CODIGO_* não setados")
	}
	return
}

func TestCodigoDaCampanha_ContraHubReal(t *testing.T) {
	url, key, codigoOK, clienteDoHub, _ := e2eCodigoConfig(t)
	ctx, pool := poolHubCode(t)

	local := uuid.MustParse(clienteDoHub)
	/* Cria o cliente local com o MESMO id que o hub tem na ponte, E com o
	   `hub_id` apontando de volta.

	   ⚠️ O `hub_id` não é detalhe: é o que o `RequireHubKeyScoped` usa para
	   traduzir o cliente do hub neste aqui quando o hub CHAMA DE VOLTA a porta
	   de leitura no meio do `campanha.upsert`. Sem ele a chamada leva 403
	   `client_not_linked`, o hub devolve 502 `plataforma_indisponivel`, e a
	   campanha nunca é confirmada — e o teste falha dizendo "não foi marcado em
	   5s", que não aponta para lugar nenhum.

	   A primeira versão deste teste omitia o `hub_id` e passava só porque o
	   cliente já estava semeado à mão no banco; o `t.Cleanup` o apagava, e a
	   rodada seguinte falhava. */
	_, err := pool.Exec(ctx, `INSERT INTO clients (id, name, hub_id) VALUES ($1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, hub_id = EXCLUDED.hub_id`,
		local, "Cliente E2E", os.Getenv("HUB_E2E_CODIGO_HUBCLIENTE"))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM campaigns WHERE client_id = $1`, local)
		_, _ = pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, local)
	})

	/* ⚠️ O `campanha.upsert` NÃO é dispara-e-esquece do lado de lá: ao tratá-lo,
	   o hub CHAMA DE VOLTA a porta de leitura deste repositório
	   (`GET /v1/internal/hub/campaigns/{id}`) para buscar nome, datas e status.
	   Sem alguém atendendo, ele devolve 502 `plataforma_indisponivel` e a
	   campanha nunca é confirmada.

	   Medido em 2026-09-22: com o `baseUrl` do produto apontando para a
	   PRODUÇÃO, o hub recebia o HTML da SPA e reclamava de JSON inválido — o
	   "200 mentiroso" de sempre. Por isso o teste sobe a porta de leitura DE
	   VERDADE (router, middleware e repositório reais) numa porta fixa, e o
	   `baseUrl` do produto no Mongo aponta para ela. */
	pararPorta := subirPortaDeLeitura(t, pool, key)
	defer pararPorta()

	repo := catalog.NewCampaigns(pool)
	h := &CampaignsHandler{Repo: repo, Hub: hub.New(url, key)}

	rec := postCampanha(t, h, campanhaNova(local, codigoOK))
	require.Equalf(t, http.StatusCreated, rec.Code, "corpo: %s", rec.Body.String())

	var criada catalog.Campaign
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &criada))
	require.NotNil(t, criada.HubCode)
	require.Equal(t, codigoOK, *criada.HubCode, "não gravou a forma canônica")

	/* ⚠️ A asserção que dá sentido ao teste: a confirmação só é carimbada
	   quando o hub respondeu algo que NÃO é `{"acao":"ignorado"}`. Se o evento
	   tivesse sido descartado lá (código inexistente, cliente divergente), o
	   `hub_notified_at` ficaria nulo — e é exatamente esse o caso que o hub
	   falso não sabe encenar. */
	esperaMarcacao(t, ctx, pool, criada.ID)
	t.Logf("campanha %s entregue e confirmada pelo hub real", criada.ID)
}

// O 422 vindo do hub DE VERDADE: o código existe lá, mas é de outro cliente.
func TestCodigoDeOutroCliente_ContraHubReal(t *testing.T) {
	url, key, _, clienteDoHub, codigoOutro := e2eCodigoConfig(t)
	if codigoOutro == "" {
		t.Skip("HUB_E2E_CODIGO_OUTRO não setado")
	}
	ctx, pool := poolHubCode(t)

	local := uuid.MustParse(clienteDoHub)
	_, err := pool.Exec(ctx, `INSERT INTO clients (id, name, hub_id) VALUES ($1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, hub_id = EXCLUDED.hub_id`,
		local, "Cliente E2E", os.Getenv("HUB_E2E_CODIGO_HUBCLIENTE"))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM campaigns WHERE client_id = $1`, local)
		_, _ = pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, local)
	})

	h := &CampaignsHandler{Repo: catalog.NewCampaigns(pool), Hub: hub.New(url, key)}
	rec := postCampanha(t, h, campanhaNova(local, codigoOutro))

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Contains(t, rec.Body.String(), "outro cliente")
	t.Logf("recusa do hub real: %s", rec.Body.String())
}

/*
subirPortaDeLeitura sobe o `/v1/internal/hub/campaigns/{id}` DE VERDADE — router,
middleware `RequireHubKeyScoped` e `catalog.Campaigns` reais — na porta que o
`baseUrl` do produto e-monitor aponta no Mongo do hub.

Porta FIXA e não `httptest.NewServer`: quem decide a URL é um documento no Mongo
do outro processo, e não dá para ensiná-lo a uma porta sorteada sem escrever no
banco dele no meio do teste.
*/
func subirPortaDeLeitura(t *testing.T, pool *pgxpool.Pool, chave string) func() {
	t.Helper()
	porta := os.Getenv("HUB_E2E_CODIGO_PORTA_LEITURA")
	if porta == "" {
		porta = "38080"
	}
	/* A rota é montada à mão porque `handlers` não pode importar `api` (ciclo:
	   o `router.go` importa `handlers`). O que importa aqui continua REAL — o
	   `RequireHubKeyScoped`, o `CampaignsHandler.Get` e o `catalog.Campaigns`
	   contra o Postgres de teste. A montagem no router de verdade é assunto dos
	   testes de `internal/api`. */
	interno := &CampaignsHandler{Repo: catalog.NewCampaigns(pool)}
	r := chi.NewRouter()
	r.Route("/v1/internal/hub", func(r chi.Router) {
		r.Use(auth.RequireHubKeyScoped(hub.New("http://nao-usado", chave), catalog.NewHubClients(pool)))
		r.Get("/campaigns/{id}", interno.Get)
	})
	srv := &http.Server{Addr: "127.0.0.1:" + porta, Handler: r}
	ln, err := net.Listen("tcp", srv.Addr)
	require.NoErrorf(t, err, "porta %s ocupada — feche o que estiver nela", porta)
	go func() { _ = srv.Serve(ln) }()
	return func() { _ = srv.Close() }
}

/*
Mover e congelar, contra o hub real (§6.5 da spec).

⚠️ É o teste que prova as duas afirmações mais fáceis de escrever e mais difíceis
de verificar: trocar o código MOVE a proposta e continua sendo UMA; apagar o
código CONGELA a coleta e não apaga nada. Um hub falso não tem proposta para
mover nem coleta para congelar — ele só diria 200.

Precisa de `HUB_E2E_CODIGO_OK` e `HUB_E2E_CODIGO_SEGUNDO`, dois códigos de
campanhas DO MESMO cliente.
*/
func TestMoverECongelar_ContraHubReal(t *testing.T) {
	url, key, codigoOK, clienteDoHub, _ := e2eCodigoConfig(t)
	segundo := os.Getenv("HUB_E2E_CODIGO_SEGUNDO")
	if segundo == "" {
		t.Skip("HUB_E2E_CODIGO_SEGUNDO não setado")
	}
	ctx, pool := poolHubCode(t)

	local := uuid.MustParse(clienteDoHub)
	/* O `hub_id` é o que o `RequireHubKeyScoped` da porta de leitura usa para
	   traduzir o cliente do hub neste aqui — sem ele a chamada de volta leva
	   403 `client_not_linked` e o evento vira 502 `plataforma_indisponivel`. */
	_, err := pool.Exec(ctx, `INSERT INTO clients (id, name, hub_id) VALUES ($1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET hub_id = EXCLUDED.hub_id`,
		local, "Cliente E2E", os.Getenv("HUB_E2E_CODIGO_HUBCLIENTE"))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM campaigns WHERE client_id = $1`, local)
		_, _ = pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, local)
	})

	pararPorta := subirPortaDeLeitura(t, pool, key)
	defer pararPorta()

	h := &CampaignsHandler{Repo: catalog.NewCampaigns(pool), Hub: hub.New(url, key)}

	rec := postCampanha(t, h, campanhaNova(local, codigoOK))
	require.Equalf(t, http.StatusCreated, rec.Code, "corpo: %s", rec.Body.String())
	var c catalog.Campaign
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &c))
	esperaMarcacao(t, ctx, pool, c.ID)

	// ── mover ──
	corpo := putBasico()
	corpo["hub_code"] = segundo
	rec = putCampanha(t, h, c.ID, corpo)
	require.Equalf(t, http.StatusOK, rec.Code, "corpo: %s", rec.Body.String())
	esperaMarcacao(t, ctx, pool, c.ID)
	t.Logf("movida de %s para %s", codigoOK, segundo)

	// ── congelar ──
	corpo = putBasico()
	corpo["hub_code"] = ""
	rec = putCampanha(t, h, c.ID, corpo)
	require.Equalf(t, http.StatusOK, rec.Code, "corpo: %s", rec.Body.String())
	require.Equal(t, "", codigoDe(t, ctx, pool, c.ID))
	esperaMarcacao(t, ctx, pool, c.ID)
	t.Logf("congelada — o hub confirmou o evento sem código")
	t.Logf("campanha local: %s", c.ID)
}
