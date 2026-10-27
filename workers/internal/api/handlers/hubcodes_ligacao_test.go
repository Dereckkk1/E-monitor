package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
	"radiocheck/internal/hub"
)

/*
Hub falso da ligação: responde a conferência (by-code), o ligar e a porta de
eventos. Os campos podem mudar entre dois cliques (teste de idempotência), por
isso o mutex.

⚠️ O id do cliente do hub é NOVO a cada teste ("hub-<uuid>"): uma linha que
vazasse de um teste com id fixo envenenaria o próximo, que acharia um
"detentor" que não criou.

⚠️ Nada de `require` dentro do handler: ele roda em outra goroutine.
*/
type hubLigacaoFalso struct {
	mu             sync.Mutex
	hubID          string
	idNaPlataforma string
	cnpj           string // "" vai como `null`, como o hub manda quando não tem
	statusCodigo   int
	statusLigar    int
	corpoLigar     string
	ligou          int32
	eventos        chan map[string]any
}

func novoHubDaLigacao(t *testing.T) (*hubLigacaoFalso, *hub.Client) {
	t.Helper()
	f := &hubLigacaoFalso{
		hubID:        "hub-" + uuid.NewString(),
		statusCodigo: http.StatusOK,
		statusLigar:  http.StatusOK,
		corpoLigar:   `{"acao":"ligado"}`,
		eventos:      make(chan map[string]any, 8),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		hubID, idNaPlat, stCod, stLig, corpoLig, cnpj := f.hubID, f.idNaPlataforma, f.statusCodigo, f.statusLigar, f.corpoLigar, f.cnpj
		f.mu.Unlock()
		switch {
		case strings.Contains(r.URL.Path, "/api/platform/campaigns/by-code/"):
			if stCod != http.StatusOK {
				w.WriteHeader(stCod)
				_, _ = w.Write([]byte(`{}`))
				return
			}
			cnpjJSON := "null"
			if cnpj != "" {
				b, _ := json.Marshal(cnpj)
				cnpjJSON = string(b)
			}
			_, _ = w.Write([]byte(`{"hubCampaignId":"66f0","nome":"Verão 2026","inicio":"2026-12-01","fim":"2027-02-28",` +
				`"cliente":{"id":"` + hubID + `","nome":"Lojas Renner","idNaPlataforma":"` + idNaPlat + `","cnpj":` + cnpjJSON + `}}`))
		case strings.HasSuffix(r.URL.Path, "/api/platform/clientes/ligar"):
			atomic.AddInt32(&f.ligou, 1)
			w.WriteHeader(stLig)
			_, _ = w.Write([]byte(corpoLig))
		default:
			var env map[string]any
			_ = json.NewDecoder(r.Body).Decode(&env)
			select {
			case f.eventos <- env:
			default:
			}
			_, _ = w.Write([]byte(`{"acao":"atualiza"}`))
		}
	}))
	t.Cleanup(srv.Close)
	return f, hub.New(srv.URL, "chave")
}

func (f *hubLigacaoFalso) mudar(fn func(f *hubLigacaoFalso)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *hubLigacaoFalso) id() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hubID
}

func (f *hubLigacaoFalso) chamadasAoLigar() int32 { return atomic.LoadInt32(&f.ligou) }

func clienteComHubID(t *testing.T, ctx context.Context, pool *pgxpool.Pool, nome string, hubID *string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO clients (name, hub_id) VALUES ($1, $2) RETURNING id`, nome, hubID).Scan(&id))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM audit_log WHERE resource_id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM campaigns WHERE client_id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, id)
	})
	return id
}

func hubIDDe(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) *string {
	t.Helper()
	var h *string
	require.NoError(t, pool.QueryRow(ctx, `SELECT hub_id FROM clients WHERE id = $1`, id).Scan(&h))
	return h
}

func campanhaDe(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cliente uuid.UUID, codigo string) *catalog.Campaign {
	t.Helper()
	c, err := catalog.NewCampaigns(pool).Create(ctx, catalog.CreateCampaignInput{
		ClientID:  cliente,
		Name:      "Campanha " + uuid.NewString()[:8],
		StartDate: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2027, 2, 28, 0, 0, 0, 0, time.UTC),
		HubCode:   codigo,
	})
	require.NoError(t, err)
	return c
}

func getConferencia(t *testing.T, h *HubCodesHandler, clientID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/internal/hub-codes/EH-7K4M2X?client_id="+clientID, nil)
	req = comParamDeRota(req, "code", "EH-7K4M2X")
	rec := httptest.NewRecorder()
	h.Get(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	return rec
}

func postLigarCorpo(t *testing.T, h *HubCodesHandler, corpo []byte, claims *auth.Claims) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/internal/hub-codes/EH-7K4M2X/ligar", bytes.NewReader(corpo))
	if claims != nil {
		req = req.WithContext(auth.ContextWithClaims(req.Context(), claims))
	}
	req = comParamDeRota(req, "code", "EH-7K4M2X")
	rec := httptest.NewRecorder()
	h.Ligar(rec, req)
	return rec
}

func postLigar(t *testing.T, h *HubCodesHandler, cliente uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"client_id": cliente.String()})
	return postLigarCorpo(t, h, b, nil)
}

// A `ligacao` de uma resposta; zero se não veio.
func ligacaoDe(t *testing.T, rec *httptest.ResponseRecorder) catalog.Ligacao {
	t.Helper()
	var out struct {
		Ligacao catalog.Ligacao `json:"ligacao"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	return out.Ligacao
}

func contarHubLink(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cliente uuid.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = 'client.hub_link' AND resource_id = $1`, cliente).Scan(&n))
	return n
}

// ---------- GET /hub-codes/{code} ----------

func TestConferencia_SemClientIDNaoTrazLigacao(t *testing.T) {
	_, pool := poolHubCode(t)
	_, hb := novoHubDaLigacao(t)
	rec := getHubCode(t, &HubCodesHandler{Hub: hb, Ligacoes: catalog.NewLigacoes(pool)}, "EH-7K4M2X")
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, rec.Body.String(), "ligacao")
}

func TestConferencia_ClientIDInvalidoOuDesconhecidoNaoTrazLigacao(t *testing.T) {
	_, pool := poolHubCode(t)
	_, hb := novoHubDaLigacao(t)
	h := &HubCodesHandler{Hub: hb, Ligacoes: catalog.NewLigacoes(pool)}
	for _, id := range []string{"banana", uuid.NewString()} {
		rec := getConferencia(t, h, id)
		require.NotContains(t, rec.Body.String(), "ligacao", "client_id=%s", id)
	}
}

func TestConferencia_FaltaLigar(t *testing.T) {
	ctx, pool := poolHubCode(t)
	c := clienteComHubID(t, ctx, pool, "LOJAS RENNER", nil)
	f, hb := novoHubDaLigacao(t)
	rec := getConferencia(t, &HubCodesHandler{Hub: hb, Ligacoes: catalog.NewLigacoes(pool)}, c.String())
	require.Equal(t, catalog.EstadoFaltaLigar, ligacaoDe(t, rec).Estado)
	require.NotContains(t, rec.Body.String(), f.id(), "o id do hub não desce para o navegador")
}

func TestConferencia_Ligado(t *testing.T) {
	ctx, pool := poolHubCode(t)
	f, hb := novoHubDaLigacao(t)
	h := f.id()
	c := clienteComHubID(t, ctx, pool, "LOJAS RENNER", &h)
	f.mudar(func(f *hubLigacaoFalso) { f.idNaPlataforma = c.String() })
	rec := getConferencia(t, &HubCodesHandler{Hub: hb, Ligacoes: catalog.NewLigacoes(pool)}, c.String())
	require.Equal(t, catalog.EstadoLigado, ligacaoDe(t, rec).Estado)
}

// ---------- POST /hub-codes/{code}/ligar ----------

func TestLigar_LigaOsDoisLados(t *testing.T) {
	ctx, pool := poolHubCode(t)
	c := clienteComHubID(t, ctx, pool, "LOJAS RENNER", nil)
	f, hb := novoHubDaLigacao(t)

	rec := postLigar(t, &HubCodesHandler{Hub: hb, Ligacoes: catalog.NewLigacoes(pool)}, c)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, catalog.EstadoLigado, ligacaoDe(t, rec).Estado)
	require.Equal(t, int32(1), f.chamadasAoLigar())
	require.Equal(t, f.id(), *hubIDDe(t, ctx, pool, c))
	require.NotContains(t, rec.Body.String(), f.id(), "o id do hub não desce para o navegador")
}

// Depois de ligar, as campanhas do cliente que esperavam o job de 15 minutos
// saem já — fora do caminho da resposta.
func TestLigar_ReemiteAsPendentesDoCliente(t *testing.T) {
	ctx, pool := poolHubCode(t)
	c := clienteComHubID(t, ctx, pool, "LOJAS RENNER", nil)
	camp := campanhaDe(t, ctx, pool, c, "EH-7K4M2X")
	f, hb := novoHubDaLigacao(t)
	h := &HubCodesHandler{Hub: hb, Ligacoes: catalog.NewLigacoes(pool), Campanhas: catalog.NewCampaigns(pool)}

	rec := postLigar(t, h, c)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	select {
	case env := <-f.eventos:
		require.Equal(t, "campanha.upsert", env["tipo"])
		dados, _ := env["dados"].(map[string]any)
		require.Equal(t, camp.ID.String(), dados["idNaPlataforma"])
		require.Equal(t, "EH-7K4M2X", dados["hubCode"])
	case <-time.After(5 * time.Second):
		t.Fatal("o hub não recebeu o campanha.upsert da pendente")
	}
	// Espera a goroutine terminar de marcar, para ela não sobreviver ao teste.
	require.Eventually(t, func() bool {
		var marcada bool
		_ = pool.QueryRow(ctx, `SELECT hub_notified_at IS NOT NULL FROM campaigns WHERE id = $1`, camp.ID).Scan(&marcada)
		return marcada
	}, 5*time.Second, 20*time.Millisecond)
}

func TestLigar_HubForaDeixaOLadoLocalGravadoE503(t *testing.T) {
	ctx, pool := poolHubCode(t)
	c := clienteComHubID(t, ctx, pool, "LOJAS RENNER", nil)
	f, hb := novoHubDaLigacao(t)
	f.mudar(func(f *hubLigacaoFalso) { f.statusLigar = 500; f.corpoLigar = `{}` })

	rec := postLigar(t, &HubCodesHandler{Hub: hb, Ligacoes: catalog.NewLigacoes(pool)}, c)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Equal(t, catalog.EstadoFaltaLigar, ligacaoDe(t, rec).Estado)
	require.Equal(t, f.id(), *hubIDDe(t, ctx, pool, c))
}

// 404 no ligar é hub sem a rota: apertar de novo não resolve, a tela tem de dizer.
func TestLigar_Hub404NoLigarPedeParaAvisarOSuporte(t *testing.T) {
	ctx, pool := poolHubCode(t)
	c := clienteComHubID(t, ctx, pool, "LOJAS RENNER", nil)
	f, hb := novoHubDaLigacao(t)
	f.mudar(func(f *hubLigacaoFalso) { f.statusLigar = 404; f.corpoLigar = `{}` })

	rec := postLigar(t, &HubCodesHandler{Hub: hb, Ligacoes: catalog.NewLigacoes(pool)}, c)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	l := ligacaoDe(t, rec)
	require.Equal(t, catalog.EstadoFaltaLigar, l.Estado)
	require.Contains(t, l.Mensagem, "avise o suporte")
}

func TestLigar_Hub409ViraBloqueado(t *testing.T) {
	ctx, pool := poolHubCode(t)
	c := clienteComHubID(t, ctx, pool, "LOJAS RENNER", nil)
	f, hb := novoHubDaLigacao(t)
	f.mudar(func(f *hubLigacaoFalso) {
		f.statusLigar = 409
		f.corpoLigar = `{"error":{"code":"id_em_uso"},"cliente":"LOJAS RENNER"}`
	})

	rec := postLigar(t, &HubCodesHandler{Hub: hb, Ligacoes: catalog.NewLigacoes(pool)}, c)

	require.Equal(t, http.StatusConflict, rec.Code)
	l := ligacaoDe(t, rec)
	require.Equal(t, catalog.EstadoBloqueado, l.Estado)
	require.Contains(t, l.Mensagem, "LOJAS RENNER")
	// O lado local já estava gravado antes da chamada, e continua.
	require.Equal(t, f.id(), *hubIDDe(t, ctx, pool, c))
}

func TestLigar_BloqueadoNaoChamaOHubNemGrava(t *testing.T) {
	ctx, pool := poolHubCode(t)
	outro := "hub-" + uuid.NewString()
	c := clienteComHubID(t, ctx, pool, "LOJAS RENNER", &outro)
	f, hb := novoHubDaLigacao(t)

	rec := postLigar(t, &HubCodesHandler{Hub: hb, Ligacoes: catalog.NewLigacoes(pool)}, c)

	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, catalog.EstadoBloqueado, ligacaoDe(t, rec).Estado)
	require.Equal(t, int32(0), f.chamadasAoLigar())
	require.Equal(t, outro, *hubIDDe(t, ctx, pool, c))
}

func TestLigar_JaLigadoNaoChamaOHub(t *testing.T) {
	ctx, pool := poolHubCode(t)
	f, hb := novoHubDaLigacao(t)
	h := f.id()
	c := clienteComHubID(t, ctx, pool, "LOJAS RENNER", &h)
	f.mudar(func(f *hubLigacaoFalso) { f.idNaPlataforma = c.String() })

	rec := postLigar(t, &HubCodesHandler{Hub: hb, Ligacoes: catalog.NewLigacoes(pool)}, c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, catalog.EstadoLigado, ligacaoDe(t, rec).Estado)
	require.Equal(t, int32(0), f.chamadasAoLigar())
}

// A ligação está num duplicado SEM nada pendurado: passa para o escolhido.
func TestLigar_NoDuplicadoVazioPassaALigacao(t *testing.T) {
	ctx, pool := poolHubCode(t)
	f, hb := novoHubDaLigacao(t)
	h := f.id()
	duplicado := clienteComHubID(t, ctx, pool, "LOJAS RENNER (duplicado)", &h)
	c := clienteComHubID(t, ctx, pool, "LOJAS RENNER", nil)
	f.mudar(func(f *hubLigacaoFalso) { f.idNaPlataforma = duplicado.String() })

	rec := postLigar(t, &HubCodesHandler{Hub: hb, Ligacoes: catalog.NewLigacoes(pool)}, c)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, catalog.EstadoLigado, ligacaoDe(t, rec).Estado)
	require.Nil(t, hubIDDe(t, ctx, pool, duplicado))
	require.Equal(t, h, *hubIDDe(t, ctx, pool, c))
	require.Equal(t, int32(1), f.chamadasAoLigar())
}

// O duplicado TEM cadastro: bloqueia e nada muda, nem aqui nem no hub.
func TestLigar_DetentorEmUsoBloqueiaENadaMuda(t *testing.T) {
	ctx, pool := poolHubCode(t)
	f, hb := novoHubDaLigacao(t)
	h := f.id()
	detentor := clienteComHubID(t, ctx, pool, "LOJAS RENNER (antigo)", &h)
	campanhaDe(t, ctx, pool, detentor, "")
	c := clienteComHubID(t, ctx, pool, "LOJAS RENNER", nil)

	rec := postLigar(t, &HubCodesHandler{Hub: hb, Ligacoes: catalog.NewLigacoes(pool)}, c)

	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, catalog.EstadoBloqueado, ligacaoDe(t, rec).Estado)
	require.Equal(t, h, *hubIDDe(t, ctx, pool, detentor))
	require.Nil(t, hubIDDe(t, ctx, pool, c))
	require.Equal(t, int32(0), f.chamadasAoLigar())
}

// O hub aponta para OUTRO cliente daqui, vazio: segue e o hub é chamado.
func TestLigar_ApontadoPeloHubVazioChamaOHub(t *testing.T) {
	ctx, pool := poolHubCode(t)
	f, hb := novoHubDaLigacao(t)
	apontado := clienteComHubID(t, ctx, pool, "RENNER", nil)
	c := clienteComHubID(t, ctx, pool, "LOJAS RENNER", nil)
	f.mudar(func(f *hubLigacaoFalso) { f.idNaPlataforma = apontado.String() })

	rec := postLigar(t, &HubCodesHandler{Hub: hb, Ligacoes: catalog.NewLigacoes(pool)}, c)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, int32(1), f.chamadasAoLigar())
	require.Equal(t, f.id(), *hubIDDe(t, ctx, pool, c))
}

// Hub fora no primeiro clique, de volta no segundo: o segundo refaz SÓ a
// chamada — a transação local não roda de novo (uma linha de auditoria só).
func TestLigar_ApertarDeNovoRefazSoAChamada(t *testing.T) {
	ctx, pool := poolHubCode(t)
	c := clienteComHubID(t, ctx, pool, "LOJAS RENNER", nil)
	f, hb := novoHubDaLigacao(t)
	h := &HubCodesHandler{Hub: hb, Ligacoes: catalog.NewLigacoes(pool)}

	f.mudar(func(f *hubLigacaoFalso) { f.statusLigar = 500; f.corpoLigar = `{}` })
	require.Equal(t, http.StatusServiceUnavailable, postLigar(t, h, c).Code)

	f.mudar(func(f *hubLigacaoFalso) { f.statusLigar = 200; f.corpoLigar = `{"acao":"ligado"}` })
	rec := postLigar(t, h, c)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, catalog.EstadoLigado, ligacaoDe(t, rec).Estado)

	require.Equal(t, int32(2), f.chamadasAoLigar())
	require.Equal(t, 1, contarHubLink(t, ctx, pool, c))
}

// O hub respondeu a conferência sem o id do dono: não dá para ligar a nada.
func TestLigar_HubSemIDDoClienteDa503SemGravar(t *testing.T) {
	ctx, pool := poolHubCode(t)
	c := clienteComHubID(t, ctx, pool, "LOJAS RENNER", nil)
	f, hb := novoHubDaLigacao(t)
	f.mudar(func(f *hubLigacaoFalso) { f.hubID = "" })

	rec := postLigar(t, &HubCodesHandler{Hub: hb, Ligacoes: catalog.NewLigacoes(pool)}, c)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Equal(t, catalog.Ligacao{
		Estado:   catalog.EstadoFaltaLigar,
		Mensagem: "A Central não informou o cliente dono deste código. Tente de novo em instantes.",
	}, ligacaoDe(t, rec))
	require.Nil(t, hubIDDe(t, ctx, pool, c))
	require.Equal(t, int32(0), f.chamadasAoLigar())
}

func TestLigar_ClientIDInvalido400(t *testing.T) {
	_, pool := poolHubCode(t)
	_, hb := novoHubDaLigacao(t)
	h := &HubCodesHandler{Hub: hb, Ligacoes: catalog.NewLigacoes(pool)}
	for _, corpo := range []string{`{}`, `{"client_id":"banana"}`, `não é json`, `{"client_id":"00000000-0000-0000-0000-000000000000"}`} {
		require.Equal(t, http.StatusBadRequest, postLigarCorpo(t, h, []byte(corpo), nil).Code, corpo)
	}
}

func TestLigar_ClienteDesconhecido404(t *testing.T) {
	_, pool := poolHubCode(t)
	f, hb := novoHubDaLigacao(t)
	rec := postLigar(t, &HubCodesHandler{Hub: hb, Ligacoes: catalog.NewLigacoes(pool)}, uuid.New())
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, int32(0), f.chamadasAoLigar())
}

func TestLigar_CodigoInexistenteNoHub404(t *testing.T) {
	ctx, pool := poolHubCode(t)
	c := clienteComHubID(t, ctx, pool, "LOJAS RENNER", nil)
	f, hb := novoHubDaLigacao(t)
	f.mudar(func(f *hubLigacaoFalso) { f.statusCodigo = 404 })

	rec := postLigar(t, &HubCodesHandler{Hub: hb, Ligacoes: catalog.NewLigacoes(pool)}, c)

	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Nil(t, hubIDDe(t, ctx, pool, c))
}

func TestLigar_SemLigacoes503(t *testing.T) {
	_, hb := novoHubDaLigacao(t)
	rec := postLigar(t, &HubCodesHandler{Hub: hb}, uuid.New())
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// Viewer de outro cliente: 404 (anti-oráculo), sem chamar o hub nem gravar.
func TestLigar_ForaDoEscopo404(t *testing.T) {
	ctx, pool := poolHubCode(t)
	c := clienteComHubID(t, ctx, pool, "LOJAS RENNER", nil)
	f, hb := novoHubDaLigacao(t)
	b, _ := json.Marshal(map[string]string{"client_id": c.String()})
	viewer := &auth.Claims{UserID: uuid.New(), Role: "viewer", ClientIDs: []uuid.UUID{uuid.New()}}

	rec := postLigarCorpo(t, &HubCodesHandler{Hub: hb, Ligacoes: catalog.NewLigacoes(pool)}, b, viewer)

	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, int32(0), f.chamadasAoLigar())
	require.Nil(t, hubIDDe(t, ctx, pool, c))
}

// O hub já aponta para o escolhido mas o lado local está solto (faltaLigar
// vindo daqui): o ligar do hub é idempotente e zera o backoff do coletor lá,
// então É chamado.
func TestLigar_HubJaApontaMasLocalSoltoChamaOHub(t *testing.T) {
	ctx, pool := poolHubCode(t)
	f, hb := novoHubDaLigacao(t)
	c := clienteComHubID(t, ctx, pool, "LOJAS RENNER", nil)
	f.mudar(func(f *hubLigacaoFalso) { f.idNaPlataforma = c.String() })

	rec := postLigar(t, &HubCodesHandler{Hub: hb, Ligacoes: catalog.NewLigacoes(pool)}, c)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, catalog.EstadoLigado, ligacaoDe(t, rec).Estado)
	require.Equal(t, int32(1), f.chamadasAoLigar())
	require.Equal(t, f.id(), *hubIDDe(t, ctx, pool, c))
}

// ---------- CNPJ divergente entre os dois lados (decisão de 2026-10-22) ----------

func comCNPJNoBanco(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, cnpj string) {
	t.Helper()
	_, err := pool.Exec(ctx, `UPDATE clients SET cnpj = $2 WHERE id = $1`, id, cnpj)
	require.NoError(t, err)
}

func TestLigar_CNPJDivergenteBloqueiaSemChamarOHubNemGravar(t *testing.T) {
	ctx, pool := poolHubCode(t)
	c := clienteComHubID(t, ctx, pool, "LOJAS RENNER", nil)
	comCNPJNoBanco(t, ctx, pool, c, "07714104000107")
	f, hb := novoHubDaLigacao(t)
	f.mudar(func(f *hubLigacaoFalso) { f.cnpj = "03.984.660/0001-81" })

	rec := postLigar(t, &HubCodesHandler{Hub: hb, Ligacoes: catalog.NewLigacoes(pool)}, c)

	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	l := ligacaoDe(t, rec)
	require.Equal(t, catalog.EstadoBloqueado, l.Estado)
	require.Contains(t, l.Mensagem, "CNPJ")
	require.Equal(t, int32(0), f.chamadasAoLigar())
	require.Nil(t, hubIDDe(t, ctx, pool, c))
}

func TestConferencia_CNPJDivergenteEhBloqueadoEOCNPJDoHubNaoVaiNoCliente(t *testing.T) {
	ctx, pool := poolHubCode(t)
	c := clienteComHubID(t, ctx, pool, "LOJAS RENNER", nil)
	comCNPJNoBanco(t, ctx, pool, c, "07714104000107")
	f, hb := novoHubDaLigacao(t)
	f.mudar(func(f *hubLigacaoFalso) { f.cnpj = "03.984.660/0001-81" })

	rec := getConferencia(t, &HubCodesHandler{Hub: hb, Ligacoes: catalog.NewLigacoes(pool)}, c.String())

	require.Equal(t, catalog.EstadoBloqueado, ligacaoDe(t, rec).Estado)
	var out struct {
		Cliente map[string]any `json:"cliente"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	require.NotNil(t, out.Cliente)
	require.NotContains(t, out.Cliente, "cnpj", "o CNPJ do hub não desce no `cliente`")
}

// Ligação completa (Renner, consertada à mão) não se desfaz por CNPJ diferente.
func TestLigar_CNPJDivergenteJaLigadoContinuaLigado(t *testing.T) {
	ctx, pool := poolHubCode(t)
	f, hb := novoHubDaLigacao(t)
	h := f.id()
	c := clienteComHubID(t, ctx, pool, "LOJAS RENNER", &h)
	comCNPJNoBanco(t, ctx, pool, c, "07714104000107")
	f.mudar(func(f *hubLigacaoFalso) { f.idNaPlataforma = c.String(); f.cnpj = "03.984.660/0001-81" })

	rec := postLigar(t, &HubCodesHandler{Hub: hb, Ligacoes: catalog.NewLigacoes(pool)}, c)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, catalog.EstadoLigado, ligacaoDe(t, rec).Estado)
}
