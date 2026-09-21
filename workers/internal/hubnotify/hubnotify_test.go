package hubnotify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/catalog"
	"radiocheck/internal/hub"
)

// ───────────────────────────── os dublês ─────────────────────────────

type repoFalso struct {
	mu           sync.Mutex
	pendentes    []catalog.Campaign
	erro         error
	erroMarcar   error
	limitePedido int
	ciclos       int
	marcadas     map[uuid.UUID]bool
}

func (r *repoFalso) PendentesDeHub(_ context.Context, limite int) ([]catalog.Campaign, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.limitePedido = limite
	r.ciclos++
	if r.erro != nil {
		return nil, r.erro
	}
	return r.pendentes, nil
}

func (r *repoFalso) MarcarHubNotificada(_ context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.erroMarcar != nil {
		return r.erroMarcar
	}
	if r.marcadas == nil {
		r.marcadas = map[uuid.UUID]bool{}
	}
	r.marcadas[id] = true
	return nil
}

func (r *repoFalso) foiMarcada(id uuid.UUID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.marcadas[id]
}

func (r *repoFalso) quantosCiclos() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ciclos
}

type emissorFalso struct {
	mu             sync.Mutex
	emitidas       []catalog.Campaign
	erro           error
	erroNaPrimeira bool
}

func (e *emissorFalso) Emitir(_ context.Context, c catalog.Campaign) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.erroNaPrimeira && len(e.emitidas) == 0 {
		e.emitidas = append(e.emitidas, c) // registra a tentativa
		return errors.New("o hub recusou a primeira")
	}
	if e.erro != nil {
		return e.erro
	}
	e.emitidas = append(e.emitidas, c)
	return nil
}

func (e *emissorFalso) quantas() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.emitidas)
}

func ptr(s string) *string { return &s }

func campanhaPendente(codigo string) catalog.Campaign {
	return catalog.Campaign{ID: uuid.New(), ClientID: uuid.New(), HubCode: ptr(codigo)}
}

// ───────────────────────────── o ciclo ─────────────────────────────

func TestReemiteEMarcaOQueOHubAceitou(t *testing.T) {
	c := campanhaPendente("EH-AAAAAA")
	repo := &repoFalso{pendentes: []catalog.Campaign{c}}
	emissor := &emissorFalso{}

	n, err := RodarUmCiclo(context.Background(), repo, emissor, 100)

	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, 1, emissor.quantas())
	require.Equal(t, "EH-AAAAAA", deref(emissor.emitidas[0].HubCode))
	require.True(t, repo.foiMarcada(c.ID), "não marcou hub_notified_at depois do sucesso")
}

/*
⚠️ Marcar mesmo com falha transformaria "tentou e não deu" em "já avisou", e a
campanha NUNCA MAIS entraria na fila. Fila que esquece é pior que fila que
repete: a campanha ficaria invisível no hub para sempre, sem nada em tela
nenhuma denunciando — que é exatamente o buraco que este pacote existe para
fechar.
*/
func TestNaoMarcaQuandoOHubRecusa(t *testing.T) {
	c := campanhaPendente("EH-AAAAAA")
	repo := &repoFalso{pendentes: []catalog.Campaign{c}}
	emissor := &emissorFalso{erro: errors.New("502 do hub")}

	n, err := RodarUmCiclo(context.Background(), repo, emissor, 100)

	require.NoError(t, err, "a falha de UMA campanha não é erro do ciclo")
	require.Zero(t, n)
	require.False(t, repo.foiMarcada(c.ID), "marcou apesar do erro")
}

/*
⚠️ Uma campanha cujo código não existe mais no hub falha para SEMPRE. Parar o
laço nela deixaria todas as outras presas atrás dela, indefinidamente — uma
campanha quebrada congelaria a fila inteira.
*/
func TestUmaFalhaNaoParaAsOutras(t *testing.T) {
	c1, c2 := campanhaPendente("EH-AAAAAA"), campanhaPendente("EH-BBBBBB")
	repo := &repoFalso{pendentes: []catalog.Campaign{c1, c2}}
	emissor := &emissorFalso{erroNaPrimeira: true}

	n, err := RodarUmCiclo(context.Background(), repo, emissor, 100)

	require.NoError(t, err)
	require.Equal(t, 1, n, "a segunda campanha não foi entregue depois de a primeira falhar")
	require.False(t, repo.foiMarcada(c1.ID))
	require.True(t, repo.foiMarcada(c2.ID))
}

/*
⚠️ Entregou mas não conseguiu marcar NÃO conta como entregue no placar, e é de
propósito: o `hub_notified_at` continua nulo, então o próximo ciclo vai reemitir
esta campanha. Contá-la aqui faria o número do log dizer "entreguei 3" num ciclo
que vai repetir uma delas — e o `campanha.upsert` é idempotente do outro lado,
então repetir é seguro; mentir no placar, não.
*/
func TestEntregouENaoMarcouNaoContaComoEntregue(t *testing.T) {
	c := campanhaPendente("EH-AAAAAA")
	repo := &repoFalso{pendentes: []catalog.Campaign{c}, erroMarcar: errors.New("banco fora")}
	emissor := &emissorFalso{}

	n, err := RodarUmCiclo(context.Background(), repo, emissor, 100)

	require.NoError(t, err)
	require.Zero(t, n)
	require.Equal(t, 1, emissor.quantas(), "nem tentou emitir")
}

func TestErroDoRepoDerrubaOCicloEntaoNaoEmiteNada(t *testing.T) {
	repo := &repoFalso{erro: errors.New("banco fora")}
	emissor := &emissorFalso{}

	n, err := RodarUmCiclo(context.Background(), repo, emissor, 100)

	require.Error(t, err)
	require.Zero(t, n)
	require.Zero(t, emissor.quantas())
}

func TestFilaVaziaEhOCasoNORMAL(t *testing.T) {
	repo := &repoFalso{}
	emissor := &emissorFalso{}

	n, err := RodarUmCiclo(context.Background(), repo, emissor, 100)

	// Em regime isto é o que acontece SEMPRE: só cai na fila quem perdeu o
	// evento. Zero linhas não é erro nem aviso.
	require.NoError(t, err)
	require.Zero(t, n)
	require.Zero(t, emissor.quantas())
}

/*
⚠️ O TESTE QUE IMPEDE O JOB DE VIRAR A CARGA EM LOTE QUE ESTA SÉRIE APOSENTOU.

Campanha sem código não pode sair daqui, e a razão é pior que "não adianta": um
`campanha.upsert` com `hubCode` vazio não é ignorado pelo hub quando já existe
proposta com aquela fonte — ele é lido como "o código foi APAGADO" e CONGELA a
coleta da proposta (`campanhaDaPlataforma.ts`, desfecho `congela`). Ou seja, um
job que reemitisse campanha sem código desligaria a coleta de clientes que estão
funcionando.

O `PendentesDeHub` já filtra por `hub_code IS NOT NULL`, então isto é defesa em
profundidade — e vale o custo, porque o modo de falha é silencioso e do lado de
fora.
*/
func TestNuncaEmiteCampanhaSemCodigo(t *testing.T) {
	semCodigo := catalog.Campaign{ID: uuid.New(), ClientID: uuid.New(), HubCode: nil}
	soEspaco := catalog.Campaign{ID: uuid.New(), ClientID: uuid.New(), HubCode: ptr("   ")}
	boa := campanhaPendente("EH-AAAAAA")
	repo := &repoFalso{pendentes: []catalog.Campaign{semCodigo, soEspaco, boa}}
	emissor := &emissorFalso{}

	n, err := RodarUmCiclo(context.Background(), repo, emissor, 100)

	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, 1, emissor.quantas(), "emitiu campanha sem código — isso CONGELA coleta no hub")
	require.Equal(t, boa.ID, emissor.emitidas[0].ID)
	require.False(t, repo.foiMarcada(semCodigo.ID), "marcou como notificada sem ter notificado")
	require.False(t, repo.foiMarcada(soEspaco.ID))
}

func TestOTetoPorCicloChegaAoRepo(t *testing.T) {
	repo := &repoFalso{}
	_, err := RodarUmCiclo(context.Background(), repo, &emissorFalso{}, 7)
	require.NoError(t, err)
	require.Equal(t, 7, repo.limitePedido)
}

// ───────────────────────────── o EmissorHTTP ─────────────────────────────

/*
⚠️ O payload do job tem de ser IGUAL ao do `avisarHubDaCampanha`. Se divergirem,
a campanha entregue pelo caminho normal e a entregue pela rede de segurança
chegam diferentes ao hub — e a segunda é justamente a que ninguém está olhando.
*/
func TestEmissorHTTPMandaOsTresCamposNoEnvelopeCerto(t *testing.T) {
	recebido := make(chan map[string]any, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var env map[string]any
		_ = json.Unmarshal(b, &env)
		recebido <- env
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := campanhaPendente("EH-7K4M2X")
	err := EmissorHTTP{Hub: hub.New(srv.URL, "chave")}.Emitir(context.Background(), c)
	require.NoError(t, err)

	select {
	case env := <-recebido:
		require.Equal(t, "campanha.upsert", env["tipo"])
		d, _ := env["dados"].(map[string]any)
		require.Equal(t, c.ID.String(), d["idNaPlataforma"])
		require.Equal(t, c.ClientID.String(), d["idClienteNaPlataforma"])
		require.Equal(t, "EH-7K4M2X", d["hubCode"])
	case <-time.After(5 * time.Second):
		t.Fatal("o hub não recebeu nada em 5s")
	}
}

func TestEmissorHTTPDevolveErroQuandoOHubRecusa(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := EmissorHTTP{Hub: hub.New(srv.URL, "chave")}.Emitir(
		context.Background(), campanhaPendente("EH-AAAAAA"))

	// Sem este erro o ciclo marcaria como entregue o que o hub recusou.
	require.Error(t, err)
}

// ───────────────────────────── o Iniciar ─────────────────────────────

/*
⚠️ O job NÃO roda no boot, e isso é escolha. O `cmd/api` sobe junto com o resto
do sistema; um ciclo no instante do boot disputaria conexão com a subida e
chegaria ao hub junto com todo o resto do deploy. Quem acabou de criar campanha
já foi avisado pelo emissor; a fila é para o que se perdeu, e quinze minutos de
atraso nela não custam nada.
*/
func TestIniciarNaoRodaNoBoot(t *testing.T) {
	repo := &repoFalso{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	Iniciar(ctx, repo, &emissorFalso{})
	time.Sleep(300 * time.Millisecond)

	require.Zero(t, repo.quantosCiclos(), "rodou um ciclo no boot")
}

// Cancelar o contexto encerra o laço — senão o job sobreviveria ao shutdown e
// seguiria batendo no banco depois do `Close()` do pool.
func TestIniciarParaQuandoOContextoMorre(t *testing.T) {
	repo := &repoFalso{}
	ctx, cancel := context.WithCancel(context.Background())

	Iniciar(ctx, repo, &emissorFalso{})
	cancel()
	time.Sleep(200 * time.Millisecond)

	require.Zero(t, repo.quantosCiclos())
}

func TestOsNumerosDoJob(t *testing.T) {
	// ⚠️ Números conferidos contra a spec (§8) em vez de assumidos: 15 minutos é
	// o intervalo que o desenho pede, e o teto por ciclo existe para o primeiro
	// ciclo depois de uma queda longa do hub não virar uma rajada contra ele
	// exatamente quando ele acabou de voltar.
	require.Equal(t, 15*time.Minute, Intervalo)
	require.Equal(t, 50, PorCiclo)
}
