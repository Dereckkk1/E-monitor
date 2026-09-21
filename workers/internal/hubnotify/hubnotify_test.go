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
	tentadas     []uuid.UUID
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

func (r *repoFalso) MarcarHubNotificada(_ context.Context, id uuid.UUID, _ string) error {
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

func (r *repoFalso) MarcarTentativaDeHub(_ context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tentadas = append(r.tentadas, id)
	return nil
}

func (r *repoFalso) foiTentada(id uuid.UUID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range r.tentadas {
		if t == id {
			return true
		}
	}
	return false
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

/*
Cancelar o contexto encerra o laço — senão o job sobreviveria ao shutdown e
seguiria batendo no banco depois do `Close()` do pool.

⚠️ Este teste JÁ FOI uma asserção que não conseguia falhar: com o `Intervalo` de
15 minutos fixo, ele dormia 200ms e passava com o `case <-ctx.Done()` APAGADO —
medido, 13 de 13 verdes. Ele só prova alguma coisa porque o intervalo é
injetável e porque ele espera o laço RODAR antes de cancelar: sem ver ciclo
nenhum primeiro, "parou" e "nunca começou" são indistinguíveis.
*/
func TestIniciarParaQuandoOContextoMorre(t *testing.T) {
	repo := &repoFalso{}
	ctx, cancel := context.WithCancel(context.Background())

	IniciarCom(ctx, repo, &emissorFalso{}, 10*time.Millisecond)

	// Primeiro: provar que ele ESTÁ rodando.
	require.Eventually(t, func() bool { return repo.quantosCiclos() > 0 },
		2*time.Second, 5*time.Millisecond, "o laço nem chegou a rodar")

	cancel()
	time.Sleep(50 * time.Millisecond) // deixa o cancelamento ser visto
	parou := repo.quantosCiclos()
	time.Sleep(200 * time.Millisecond) // ~20 ticks, se ainda estivesse vivo

	require.Equal(t, parou, repo.quantosCiclos(), "seguiu rodando depois do cancelamento")
}

func TestOsNumerosDoJob(t *testing.T) {
	// ⚠️ Números conferidos contra a spec (§8) em vez de assumidos: 15 minutos é
	// o intervalo que o desenho pede, e o teto por ciclo existe para o primeiro
	// ciclo depois de uma queda longa do hub não virar uma rajada contra ele
	// exatamente quando ele acabou de voltar.
	require.Equal(t, 15*time.Minute, Intervalo)
	require.Equal(t, 50, PorCiclo)
}

// ─────────── C1: 200 não é entrega ───────────

/*
⚠️ O DEFEITO CRÍTICO QUE ESTE TESTE FECHA (medido em 2026-09-21).

O hub responde **HTTP 200** com `{"acao":"ignorado","motivo":"codigo-inexistente"}`
quando DESCARTA o evento pela regra dele (§6.1) — e também para
`cliente-divergente`, `codigo-malformado`, `sem-codigo` e `cliente-sem-par`.

Lendo só o status, o job contava isso como entrega e marcava `hub_notified_at`.
A campanha saía da fila PARA SEMPRE sem nunca ter chegado ao hub, e o log dizia
"reentregues: 1" quando foi descartada. A rede de segurança do §8 fechando, ela
mesma, o buraco que existe para vigiar.

E o caminho não é hipotético: o `Create` aceita código não conferido exatamente
quando o hub está mudo (decisão 2), que é o mesmo instante que põe a campanha
nesta fila.
*/
func TestHubQueResponde200IgnoradoNaoContaComoEntrega(t *testing.T) {
	for _, motivo := range []string{"codigo-inexistente", "cliente-divergente", "codigo-malformado"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"acao":"ignorado","motivo":"` + motivo + `"}`))
		}))

		c := campanhaPendente("EH-AAAAAA")
		repo := &repoFalso{pendentes: []catalog.Campaign{c}}
		n, err := RodarUmCiclo(context.Background(), repo,
			EmissorHTTP{Hub: hub.New(srv.URL, "chave")}, 100)

		require.NoError(t, err)
		require.Zerof(t, n, "motivo %q: contou um evento IGNORADO como entregue", motivo)
		require.Falsef(t, repo.foiMarcada(c.ID),
			"motivo %q: marcou hub_notified_at de uma campanha que o hub descartou", motivo)
		// ⚠️ Mas a TENTATIVA tem de ficar registrada, senão esta campanha —
		// que vai falhar para sempre — trava a cabeça da fila.
		require.Truef(t, repo.foiTentada(c.ID), "motivo %q: não carimbou a tentativa", motivo)
		srv.Close()
	}
}

// O espelho do teste acima: o desfecho de SUCESSO continua contando. Sem este
// controle positivo, bastaria o emissor passar a recusar tudo para o teste de
// cima ficar permanentemente verde.
func TestHubQueAceitaContaComoEntrega(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"acao":"atualiza","campanhaId":"66f0"}`))
	}))
	defer srv.Close()

	c := campanhaPendente("EH-AAAAAA")
	repo := &repoFalso{pendentes: []catalog.Campaign{c}}
	n, err := RodarUmCiclo(context.Background(), repo,
		EmissorHTTP{Hub: hub.New(srv.URL, "chave")}, 100)

	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.True(t, repo.foiMarcada(c.ID))
}

// 200 com corpo ilegível (envelope novo do hub, proxy respondendo por ele) NÃO
// é falha de entrega: o hub disse que aceitou. Devolver erro aqui trocaria um
// defeito silencioso por outro — reemitir para sempre uma campanha que chegou.
func TestHubQueResponde200ComCorpoIlegivelContaComoEntrega(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<html>proxy no meio do caminho</html>`))
	}))
	defer srv.Close()

	c := campanhaPendente("EH-AAAAAA")
	repo := &repoFalso{pendentes: []catalog.Campaign{c}}
	n, err := RodarUmCiclo(context.Background(), repo,
		EmissorHTTP{Hub: hub.New(srv.URL, "chave")}, 100)

	require.NoError(t, err)
	require.Equal(t, 1, n)
}

// ─────────── C2: a fila roda, não morre na cabeça ───────────

/*
⚠️ A TENTATIVA É CARIMBADA MESMO QUANDO A ENTREGA FALHA.

É o que impede a fome permanente. O `PendentesDeHub` ordena por
`hub_notify_tentado_em NULLS FIRST` com `LIMIT 50`; uma campanha que falha
sempre (código que sumiu do hub, `clients.hub_id` vazio, chave rodada) nunca é
marcada como notificada, e sem o carimbo de tentativa ela continuaria sendo a
mais antiga não-tentada — as MESMAS 50 linhas todo ciclo, para sempre. Com 500
pendentes e 50 envenenadas, as outras 450 nunca seriam tentadas uma única vez.
*/
func TestCarimbaATentativaAindaQueAEntregaFalhe(t *testing.T) {
	c1, c2 := campanhaPendente("EH-AAAAAA"), campanhaPendente("EH-BBBBBB")
	repo := &repoFalso{pendentes: []catalog.Campaign{c1, c2}}
	emissor := &emissorFalso{erro: errors.New("502 permanente")}

	_, err := RodarUmCiclo(context.Background(), repo, emissor, 100)

	require.NoError(t, err)
	require.True(t, repo.foiTentada(c1.ID), "a que falhou não foi carimbada — ela trava a fila")
	require.True(t, repo.foiTentada(c2.ID))
	require.False(t, repo.foiMarcada(c1.ID), "tentativa não pode virar confirmação")
	require.False(t, repo.foiMarcada(c2.ID))
}

// Campanha sem código não é sequer tentada: ela não vai à rede, então não há
// tentativa que carimbar.
func TestCampanhaSemCodigoNaoEhTentada(t *testing.T) {
	semCodigo := catalog.Campaign{ID: uuid.New(), ClientID: uuid.New(), HubCode: nil}
	repo := &repoFalso{pendentes: []catalog.Campaign{semCodigo}}

	_, err := RodarUmCiclo(context.Background(), repo, &emissorFalso{}, 100)

	require.NoError(t, err)
	require.False(t, repo.foiTentada(semCodigo.ID))
}
