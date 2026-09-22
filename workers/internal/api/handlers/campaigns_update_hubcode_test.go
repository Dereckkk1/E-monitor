package handlers

// campaigns_update_hubcode_test.go — o código do hub no PUT, e o código dentro
// do evento `campanha.upsert` (Task 6 do Plano B; spec do hub 2026-09-18 §4.4 e
// §10).
//
// Os helpers de POST, o hub falso e o seed de cliente vivem em
// `campaigns_hubcode_test.go`, no mesmo pacote.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/catalog"
	"radiocheck/internal/hub"
)

// hubComURL é o hub falso montado à mão, para os testes que precisam controlar
// as DUAS portas (a conferência e a de eventos) de forma diferente.
func hubComURL(url string) *hub.Client { return hub.New(url, "chave") }

func putCampanha(t *testing.T, h *CampaignsHandler, id uuid.UUID, corpo map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(corpo)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPut, "/campaigns/"+id.String(), bytes.NewReader(b))
	req = comParamDeRota(req, "id", id.String())
	rec := httptest.NewRecorder()
	h.Update(rec, req)
	return rec
}

// Cria direto pelo repositório: este arquivo testa o PUT, e passar pelo POST
// faria cada teste depender também da barreira do Create.
func criaCampanhaDireto(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cliente uuid.UUID, codigo string) *catalog.Campaign {
	t.Helper()
	c, err := catalog.NewCampaigns(pool).Create(ctx, catalog.CreateCampaignInput{
		ClientID:  cliente,
		Name:      "Campanha original",
		StartDate: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2027, 2, 28, 0, 0, 0, 0, time.UTC),
		HubCode:   codigo,
	})
	require.NoError(t, err)
	return c
}

// O corpo mínimo que o PUT exige. Sem `hub_code`: quem quer mexer no código
// acrescenta a chave.
func putBasico() map[string]any {
	return map[string]any{
		"name":       "Campanha original",
		"start_date": "2026-12-01T00:00:00Z",
		"end_date":   "2027-02-28T00:00:00Z",
	}
}

func codigoDe(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) string {
	t.Helper()
	var s *string
	require.NoError(t, pool.QueryRow(ctx, `SELECT hub_code FROM campaigns WHERE id = $1`, id).Scan(&s))
	if s == nil {
		return ""
	}
	return *s
}

func notificadaEm(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) *time.Time {
	t.Helper()
	var at *time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT hub_notified_at FROM campaigns WHERE id = $1`, id).Scan(&at))
	return at
}

// esperaMarcacao espera a goroutine do emissor gravar o `hub_notified_at`.
//
// ⚠️ A marcação é ASSÍNCRONA — ela roda na mesma goroutine que emite, depois de
// o handler já ter respondido. Ler o banco na hora dá nulo e o teste acusaria um
// defeito que não existe.
func esperaMarcacao(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) {
	t.Helper()
	prazo := time.Now().Add(5 * time.Second)
	for time.Now().Before(prazo) {
		if notificadaEm(t, ctx, pool, id) != nil {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("o hub_notified_at não foi marcado em 5s")
}

func semEvento(t *testing.T, emitiu chan map[string]any) {
	t.Helper()
	// ⚠️ `time.After` e NÃO `default:`: o emissor dispara uma goroutine, e um
	// `select` com `default` roda antes de o evento chegar. Com `default`, esta
	// asserção não consegue falhar — medido nesta casa em 2026-09-21.
	select {
	case env := <-emitiu:
		t.Fatalf("emitiu um evento que não devia: %v", env)
	case <-time.After(300 * time.Millisecond):
	}
}

func esperaEvento(t *testing.T, emitiu chan map[string]any) map[string]any {
	t.Helper()
	select {
	case env := <-emitiu:
		return env
	case <-time.After(5 * time.Second):
		t.Fatal("nenhum evento em 5s")
		return nil
	}
}

func hubCodeDoEvento(t *testing.T, env map[string]any) any {
	t.Helper()
	d, ok := env["dados"].(map[string]any)
	require.True(t, ok, "envelope sem `dados`: %v", env)
	return d["hubCode"]
}

// ───────────────────────── o código dentro do evento ─────────────────────────

/*
⚠️ O TESTE QUE DESTRAVA A SÉRIE INTEIRA.

Sem o `hubCode` no evento, o hub recebe um `campanha.upsert` sem código e o
IGNORA (regra do corte, §6.1 da spec), registrando `platform.campanha.ignorada`
motivo `sem-codigo`. Sem erro, sem retentativa, sem nada em tela nenhuma: a
campanha nasce certa aqui e nunca chega lá.
*/
func TestEmissaoLevaOHubCode(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)
	hb, emitiu := hubFalsoParaCodigo(t, http.StatusOK, corpoDoHub(cliente.String()))

	rec := postCampanha(t, &CampaignsHandler{Repo: catalog.NewCampaigns(pool), Hub: hb},
		campanhaNova(cliente, "EH-7K4M2X"))
	require.Equal(t, http.StatusCreated, rec.Code)

	require.Equal(t, "EH-7K4M2X", hubCodeDoEvento(t, esperaEvento(t, emitiu)))
}

// O evento leva a forma CANÔNICA, não o que foi digitado: os dois lados se
// encontram no fio, e o hub re-normaliza o que chega.
func TestEmissaoLevaOCodigoCanonicoENaoODigitado(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)
	hb, emitiu := hubFalsoParaCodigo(t, http.StatusOK, corpoDoHub(cliente.String()))

	postCampanha(t, &CampaignsHandler{Repo: catalog.NewCampaigns(pool), Hub: hb},
		campanhaNova(cliente, "  eh7k4m2x "))

	require.Equal(t, "EH-7K4M2X", hubCodeDoEvento(t, esperaEvento(t, emitiu)))
}

/*
A confirmação é o que tira a campanha da fila do job de reemissão. Sem ela, uma
campanha entregue com sucesso continuaria sendo reenviada de 15 em 15 minutos
para sempre.
*/
func TestCreateMarcaAConfirmacaoQuandoOHubAceita(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)
	hb, _ := hubFalsoParaCodigo(t, http.StatusOK, corpoDoHub(cliente.String()))
	repo := catalog.NewCampaigns(pool)

	rec := postCampanha(t, &CampaignsHandler{Repo: repo, Hub: hb}, campanhaNova(cliente, "EH-7K4M2X"))
	require.Equal(t, http.StatusCreated, rec.Code)

	var criada catalog.Campaign
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &criada))
	esperaMarcacao(t, ctx, pool, criada.ID)
}

/*
⚠️ E NÃO marca quando a entrega falhou. Marcar um envio que não chegou
transformaria "tentou e não deu" em "já avisou", e a campanha sairia da fila sem
nunca ter chegado ao hub — fila que esquece é pior que fila que repete.
*/
func TestNaoMarcaAConfirmacaoQuandoAEntregaFalha(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)
	// O hub responde 200 na conferência e 500 na porta de eventos.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if bytes.Contains([]byte(r.URL.Path), []byte("/by-code/")) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(corpoDoHub(cliente.String())))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	repo := catalog.NewCampaigns(pool)
	rec := postCampanha(t, &CampaignsHandler{Repo: repo, Hub: hubComURL(srv.URL)},
		campanhaNova(cliente, "EH-7K4M2X"))
	require.Equal(t, http.StatusCreated, rec.Code)

	var criada catalog.Campaign
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &criada))
	time.Sleep(700 * time.Millisecond) // tempo de sobra para a goroutine tentar e falhar
	require.Nil(t, notificadaEm(t, ctx, pool, criada.ID),
		"marcou a confirmação de um evento que o hub recusou")
}

// ───────────────────────────── o PUT ─────────────────────────────

func TestUpdateEmiteQuandoOCodigoMuda(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)
	camp := criaCampanhaDireto(t, ctx, pool, cliente, "EH-AAAAAA")
	hb, emitiu := hubFalsoParaCodigo(t, http.StatusOK, corpoDoHub(cliente.String()))

	corpo := putBasico()
	corpo["hub_code"] = "EH-BBBBBB"
	rec := putCampanha(t, &CampaignsHandler{Repo: catalog.NewCampaigns(pool), Hub: hb}, camp.ID, corpo)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "EH-BBBBBB", hubCodeDoEvento(t, esperaEvento(t, emitiu)))
	require.Equal(t, "EH-BBBBBB", codigoDe(t, ctx, pool, camp.ID))
}

/*
⚠️ O nome da campanha daqui vira o nome da PROPOSTA no hub, e ele é atualizado
na varredura de métricas. Emitir a cada renomeação seria uma chamada de rede por
tecla salva, sem nada de novo do outro lado.
*/
func TestUpdateNaoEmiteQuandoMudaSoONome(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)
	camp := criaCampanhaDireto(t, ctx, pool, cliente, "EH-AAAAAA")
	hb, emitiu := hubFalsoParaCodigo(t, http.StatusOK, corpoDoHub(cliente.String()))

	corpo := putBasico()
	corpo["name"] = "Outro nome"
	corpo["hub_code"] = "EH-AAAAAA" // o MESMO código
	rec := putCampanha(t, &CampaignsHandler{Repo: catalog.NewCampaigns(pool), Hub: hb}, camp.ID, corpo)

	require.Equal(t, http.StatusOK, rec.Code)
	semEvento(t, emitiu)
}

/*
⚠️ Apagar o código TEM de avisar: é o que faz o hub congelar a coleta da
proposta (§6.5 da spec). Sem o aviso, a proposta continuaria coletando para
sempre, amarrada a uma campanha que não aponta mais para ela.
*/
func TestUpdateEmiteQuandoOCodigoEApagado(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)
	camp := criaCampanhaDireto(t, ctx, pool, cliente, "EH-AAAAAA")
	hb, emitiu := hubFalsoParaCodigo(t, http.StatusOK, corpoDoHub(cliente.String()))

	corpo := putBasico()
	corpo["hub_code"] = ""
	rec := putCampanha(t, &CampaignsHandler{Repo: catalog.NewCampaigns(pool), Hub: hb}, camp.ID, corpo)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "", hubCodeDoEvento(t, esperaEvento(t, emitiu)))
	require.Equal(t, "", codigoDe(t, ctx, pool, camp.ID))
}

/*
⚠️ O ponteiro do `hub_code` existe para ESTE caso: corpo que não fala do código
não mexe nele. Sem o ponteiro, todo PUT do wizard que editasse só a data
apagaria o código — e apagar o código congela a coleta no hub.
*/
func TestUpdateSemOCampoNaoMexeNoCodigo(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)
	camp := criaCampanhaDireto(t, ctx, pool, cliente, "EH-AAAAAA")
	hb, emitiu := hubFalsoParaCodigo(t, http.StatusOK, corpoDoHub(cliente.String()))

	corpo := putBasico() // sem a chave `hub_code`
	corpo["name"] = "Outro nome"
	rec := putCampanha(t, &CampaignsHandler{Repo: catalog.NewCampaigns(pool), Hub: hb}, camp.ID, corpo)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "EH-AAAAAA", codigoDe(t, ctx, pool, camp.ID))
	semEvento(t, emitiu)
}

func TestUpdateGravaOCodigoNaFormaCanonica(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)
	camp := criaCampanhaDireto(t, ctx, pool, cliente, "")
	hb, _ := hubFalsoParaCodigo(t, http.StatusOK, corpoDoHub(cliente.String()))

	corpo := putBasico()
	corpo["hub_code"] = " eh7k4m2x "
	rec := putCampanha(t, &CampaignsHandler{Repo: catalog.NewCampaigns(pool), Hub: hb}, camp.ID, corpo)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "EH-7K4M2X", codigoDe(t, ctx, pool, camp.ID))
}

// ──────────────── a barreira do §4.4 vale no PUT também ────────────────

/*
⚠️ O DESVIO QUE ESTE TESTE FECHA (achado da revisão da Task 4, §4.0a do handoff
de 2026-09-21).

Sem a conferência aqui, a barreira do §4.4 vira opcional em duas requisições:
cria-se a campanha sem código (a decisão 6 permite) e edita-se pondo o código de
outro cliente. Pior: o `AtualizarHubCode` ZERA o `hub_notified_at`, então a
campanha entra na fila do job e passa a bater no hub de 15 em 15 minutos com o
código do cliente errado, para sempre.
*/
func TestUpdateRecusaCodigoDeOutroCliente(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)
	camp := criaCampanhaDireto(t, ctx, pool, cliente, "")
	outro := uuid.New()
	hb, emitiu := hubFalsoParaCodigo(t, http.StatusOK, corpoDoHub(outro.String()))

	corpo := putBasico()
	corpo["hub_code"] = "EH-7K4M2X"
	rec := putCampanha(t, &CampaignsHandler{Repo: catalog.NewCampaigns(pool), Hub: hb}, camp.ID, corpo)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Contains(t, rec.Body.String(), "outro cliente")
	require.Equal(t, "", codigoDe(t, ctx, pool, camp.ID), "gravou o código recusado")
	semEvento(t, emitiu)
}

func TestUpdateRecusaCodigoInexistente(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)
	camp := criaCampanhaDireto(t, ctx, pool, cliente, "")
	hb, _ := hubFalsoParaCodigo(t, http.StatusNotFound, `{}`)

	corpo := putBasico()
	corpo["hub_code"] = "EH-ZZZZZZ"
	rec := putCampanha(t, &CampaignsHandler{Repo: catalog.NewCampaigns(pool), Hub: hb}, camp.ID, corpo)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Equal(t, "", codigoDe(t, ctx, pool, camp.ID))
}

// Texto que não é código: o mesmo 422 do Create. Sem isto, lixo entra na coluna
// que o job lê de 15 em 15 minutos.
func TestUpdateRecusaTextoQueNaoEhCodigo(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)
	camp := criaCampanhaDireto(t, ctx, pool, cliente, "EH-AAAAAA")
	hb, _ := hubFalsoParaCodigo(t, http.StatusOK, corpoDoHub(cliente.String()))

	corpo := putBasico()
	corpo["hub_code"] = "isto nao e um codigo"
	rec := putCampanha(t, &CampaignsHandler{Repo: catalog.NewCampaigns(pool), Hub: hb}, camp.ID, corpo)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Equal(t, "EH-AAAAAA", codigoDe(t, ctx, pool, camp.ID), "apagou o código bom")
}

/*
⚠️ A RECUSA NÃO PODE TER GRAVADO O NOME.

Se a conferência rodasse DEPOIS do `UpdateBasic` — que é como o plano a escreveu
—, um PUT que muda o nome e traz um código de outro cliente responderia 422 com
o nome JÁ GRAVADO. Resposta de erro com escrita parcial é o tipo de coisa que só
aparece meses depois, quando alguém repara que o nome mudou numa edição que "deu
erro".
*/
func TestUpdateRecusadoNaoGravaNemONome(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)
	camp := criaCampanhaDireto(t, ctx, pool, cliente, "")
	outro := uuid.New()
	hb, _ := hubFalsoParaCodigo(t, http.StatusOK, corpoDoHub(outro.String()))

	corpo := putBasico()
	corpo["name"] = "Nome que NAO pode ficar gravado"
	corpo["hub_code"] = "EH-7K4M2X"
	rec := putCampanha(t, &CampaignsHandler{Repo: catalog.NewCampaigns(pool), Hub: hb}, camp.ID, corpo)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	var nome string
	require.NoError(t, pool.QueryRow(ctx, `SELECT name FROM campaigns WHERE id = $1`, camp.ID).Scan(&nome))
	require.Equal(t, "Campanha original", nome)
}

/*
⚠️ Hub fora do ar NÃO trava a edição (decisão 2). O código é gravado, a
confirmação fica nula, e o job leva a campanha quando o hub voltar.
*/
func TestUpdateAceitaQuandoOHubNaoResponde(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)
	camp := criaCampanhaDireto(t, ctx, pool, cliente, "")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // conexão recusada

	corpo := putBasico()
	corpo["hub_code"] = "EH-7K4M2X"
	rec := putCampanha(t, &CampaignsHandler{Repo: catalog.NewCampaigns(pool), Hub: hubComURL(srv.URL)}, camp.ID, corpo)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "EH-7K4M2X", codigoDe(t, ctx, pool, camp.ID))
	require.Nil(t, notificadaEm(t, ctx, pool, camp.ID))
}

// ──────────── §4.0c: a ponte é comparada como UUID, não como texto ────────────

/*
⚠️ O hub guarda `emonitorClientId` como texto livre — `String` com `trim` e mais
nada, digitado no formulário de `/admin/clientes`. Deste lado o valor é sempre
`uuid.String()`, minúsculo. Um UUID colado em MAIÚSCULAS, que é exatamente como
o Compass o mostra, fazia TODA campanha daquele cliente levar 422 dizendo "é de
outro cliente (Y)" — onde Y é o nome do PRÓPRIO cliente. Falha fechada com a
mensagem mais confusa possível.
*/
func TestPonteComparaUUIDENaoTexto(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)
	maiusculo := strings.ToUpper(cliente.String())
	hb, _ := hubFalsoParaCodigo(t, http.StatusOK, corpoDoHub(maiusculo))

	rec := postCampanha(t, &CampaignsHandler{Repo: catalog.NewCampaigns(pool), Hub: hb},
		campanhaNova(cliente, "EH-7K4M2X"))

	require.Equal(t, http.StatusCreated, rec.Code,
		"o mesmo cliente em maiúsculas foi lido como outro cliente: %s", rec.Body.String())
}

// Espaço em volta idem — o hub faz `trim`, mas um `trim` que alguém remova do
// lado de lá não pode derrubar esta ponte.
func TestPonteToleraEspacoEmVolta(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)
	hb, _ := hubFalsoParaCodigo(t, http.StatusOK, corpoDoHub(" "+cliente.String()+" "))

	rec := postCampanha(t, &CampaignsHandler{Repo: catalog.NewCampaigns(pool), Hub: hb},
		campanhaNova(cliente, "EH-7K4M2X"))

	require.Equal(t, http.StatusCreated, rec.Code)
}

/*
⚠️ Texto que NÃO é UUID é ponte AUSENTE, não divergência.

Lixo no campo do hub não é afirmação de que a campanha é de outro cliente — é
afirmação de que ninguém ligou os dois cadastros direito. Recusar aqui barraria
o cliente inteiro por um erro de digitação do outro lado, e a §4.4 já trata
ponte vazia como "aceita".
*/
func TestPonteIlegivelEhTratadaComoAusente(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)
	hb, _ := hubFalsoParaCodigo(t, http.StatusOK, corpoDoHub("nao-e-um-uuid"))

	rec := postCampanha(t, &CampaignsHandler{Repo: catalog.NewCampaigns(pool), Hub: hb},
		campanhaNova(cliente, "EH-7K4M2X"))

	require.Equal(t, http.StatusCreated, rec.Code)
}

// ──────────── §4.0b: a conferência não prende o POST por 10 segundos ────────────

/*
⚠️ Medido em 2026-09-21: um hub que aceita a conexão e não responde fazia o POST
levar 10,0 s e devolver o MESMO 201 que teria devolvido em 1 s. São dez segundos
de tela travada no fim de um wizard inteiro preenchido, comprando nada — o
desfecho é fail-open dos dois jeitos.

Não dá para consertar mexendo no `hub.New`: aquele `http.Client{Timeout: 10s}` é
o mesmo do `Exchange`, que roda dentro de um login.
*/
func TestCreateNaoPrendePeloTimeoutDoCliente(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)
	/* Aceita a conexão e NUNCA responde — é exatamente isso que o teste mede.

	   ⚠️ O `solta` não é enfeite: `httptest.Server.Close()` ESPERA os handlers
	   em voo terminarem, então um handler parado aqui faz o teste custar o
	   tempo inteiro da espera. E esperar só por `r.Context().Done()` NÃO basta
	   — medido nesta máquina em 2026-09-21: a requisição que o emissor dispara
	   em goroutine não teve o contexto cancelado quando o cliente desistiu, e o
	   pacote passou de 284s para estourar o timeout de 2 min.

	   Os defers são LIFO: o `close(solta)` roda ANTES do `Close()`. */
	solta := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-solta:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(solta)

	comecou := time.Now()
	rec := postCampanha(t, &CampaignsHandler{Repo: catalog.NewCampaigns(pool), Hub: hubComURL(srv.URL)},
		campanhaNova(cliente, "EH-7K4M2X"))
	demorou := time.Since(comecou)

	require.Equal(t, http.StatusCreated, rec.Code)
	require.Lessf(t, demorou, 5*time.Second,
		"o POST esperou %v — o deadline de %v não está valendo", demorou, prazoDeConferencia)
	require.Equal(t, "EH-7K4M2X", codigoDe(t, ctx, pool, uuidDoCorpo(t, rec)))
}

func uuidDoCorpo(t *testing.T, rec *httptest.ResponseRecorder) uuid.UUID {
	t.Helper()
	var c catalog.Campaign
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &c))
	return c.ID
}
