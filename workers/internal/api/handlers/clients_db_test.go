package handlers

// clients_db_test.go — TRAVA as rotas de criar/editar cliente (status, corpo,
// linha no banco, evento ao hub) ANTES de a lógica de criação sair do handler
// para internal/acoes (plano da Fatia 3, Task 10). Complementa
// `clients_cnpj_test.go` (os 409 de CNPJ). Passam no código antigo e no novo.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/catalog"
)

func putCliente(t *testing.T, h *ClientsHandler, id uuid.UUID, corpo any) *httptest.ResponseRecorder {
	t.Helper()
	var b []byte
	if s, ok := corpo.(string); ok {
		b = []byte(s)
	} else {
		var err error
		b, err = json.Marshal(corpo)
		require.NoError(t, err)
	}
	req := comParamDeRota(httptest.NewRequest(http.MethodPut, "/clients/"+id.String(), bytes.NewReader(b)), "id", id.String())
	rec := httptest.NewRecorder()
	h.Update(rec, req)
	return rec
}

func postClienteBruto(t *testing.T, h *ClientsHandler, corpo string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.Create(rec, httptest.NewRequest(http.MethodPost, "/clients", strings.NewReader(corpo)))
	return rec
}

func linhaDoCliente(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) (nome string, rotulo *string, cnpj *string) {
	t.Helper()
	require.NoError(t, pool.QueryRow(ctx, `SELECT name, target_label, cnpj FROM clients WHERE id = $1`, id).Scan(&nome, &rotulo, &cnpj))
	return nome, rotulo, cnpj
}

func limpaCliente(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) {
	t.Helper()
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, id) })
}

func TestClienteHTTP_Create_201GravaEAvisaOHub(t *testing.T) {
	ctx, pool := poolHubCode(t)
	hb, emitiu := hubFalsoParaCodigo(t, http.StatusOK, `{}`)
	h := &ClientsHandler{Repo: catalog.NewClients(pool), Hub: hb}
	cnpj := cnpjDeTeste()

	rec := postCliente(t, h, map[string]any{
		"name": "Cliente Novo", "cnpj": mascara(cnpj), "city": "Uberaba", "state": "MG",
		"contact_name": "Maria", "contact_email": "maria@x.com", "phone": "3499999",
		"target_label": "  Mulheres 25-45  ",
	})

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var out catalog.Client
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	limpaCliente(t, ctx, pool, out.ID)
	require.Equal(t, "Cliente Novo", out.Name)
	require.NotNil(t, out.TargetLabel)
	require.Equal(t, "Mulheres 25-45", *out.TargetLabel, "o rótulo é aparado")
	nome, rotulo, c := linhaDoCliente(t, ctx, pool, out.ID)
	require.Equal(t, "Cliente Novo", nome)
	require.Equal(t, "Mulheres 25-45", *rotulo)
	require.Equal(t, mascara(cnpj), *c)

	env := esperaEvento(t, emitiu)
	require.Equal(t, "cliente.upsert", env["tipo"])
	dados, _ := env["dados"].(map[string]any)
	require.Equal(t, out.ID.String(), dados["idNaPlataforma"])
	require.Equal(t, "Cliente Novo", dados["nome"])
	require.Equal(t, "Uberaba", dados["cidade"])
	require.Equal(t, "MG", dados["uf"])
}

func TestClienteHTTP_Create_SemHubSoGrava(t *testing.T) {
	ctx, pool := poolHubCode(t)

	rec := postCliente(t, &ClientsHandler{Repo: catalog.NewClients(pool)}, map[string]any{"name": "Sem hub"})

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var out catalog.Client
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	limpaCliente(t, ctx, pool, out.ID)
	require.Nil(t, out.TargetLabel)
}

func TestClienteHTTP_Create_RotuloEmBrancoViraNulo(t *testing.T) {
	ctx, pool := poolHubCode(t)

	rec := postCliente(t, &ClientsHandler{Repo: catalog.NewClients(pool)}, map[string]any{"name": "Rótulo vazio", "target_label": "   "})

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var out catalog.Client
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	limpaCliente(t, ctx, pool, out.ID)
	_, rotulo, _ := linhaDoCliente(t, ctx, pool, out.ID)
	require.Nil(t, rotulo)
}

func TestClienteHTTP_Create_RotuloContaRunesENaoBytes(t *testing.T) {
	ctx, pool := poolHubCode(t)
	h := &ClientsHandler{Repo: catalog.NewClients(pool)}

	rec := postCliente(t, h, map[string]any{"name": "Rótulo 200", "target_label": strings.Repeat("é", 200)})
	require.Equal(t, http.StatusCreated, rec.Code, "200 runes (400 bytes) cabem: "+rec.Body.String())
	var out catalog.Client
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	limpaCliente(t, ctx, pool, out.ID)

	rec = postCliente(t, h, map[string]any{"name": "Rótulo 201", "target_label": strings.Repeat("é", 201)})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "target_label must be at most 200 characters")
}

func TestClienteHTTP_Create_Recusas400ComOTextoDeSempre(t *testing.T) {
	_, pool := poolHubCode(t)
	h := &ClientsHandler{Repo: catalog.NewClients(pool)}

	rec := postClienteBruto(t, h, `não é json`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid request")

	rec = postClienteBruto(t, h, `{"name":""}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "name is required")
}

func TestClienteHTTP_Create_409NaoGravaNemAvisaOHub(t *testing.T) {
	ctx, pool := poolHubCode(t)
	hb, emitiu := hubFalsoParaCodigo(t, http.StatusOK, `{}`)
	cnpj := cnpjDeTeste()
	clienteComCNPJ(t, ctx, pool, "Dono do CNPJ", cnpj)

	rec := postCliente(t, &ClientsHandler{Repo: catalog.NewClients(pool), Hub: hb},
		map[string]any{"name": "Repetido", "cnpj": cnpj})

	require.Equal(t, http.StatusConflict, rec.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, "cnpj_duplicado", out["error"])
	semEvento(t, emitiu)
	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM clients WHERE name = 'Repetido'`).Scan(&n))
	require.Zero(t, n)
}

func TestClienteHTTP_Update_200SubstituiOsCamposENaoAvisaOHub(t *testing.T) {
	ctx, pool := poolHubCode(t)
	hb, emitiu := hubFalsoParaCodigo(t, http.StatusOK, `{}`)
	id := clienteComCNPJ(t, ctx, pool, "Antes", cnpjDeTeste())
	h := &ClientsHandler{Repo: catalog.NewClients(pool), Hub: hb}

	rec := putCliente(t, h, id, map[string]any{"name": "Depois", "city": "Uberaba", "target_label": "  Jovens  "})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out catalog.Client
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, "Depois", out.Name)
	require.Equal(t, "Jovens", *out.TargetLabel)
	nome, rotulo, cnpj := linhaDoCliente(t, ctx, pool, id)
	require.Equal(t, "Depois", nome)
	require.Equal(t, "Jovens", *rotulo)
	require.Nil(t, cnpj, "PUT substitui os campos: o CNPJ não enviado foi limpo")
	semEvento(t, emitiu) // a edição de cliente NÃO avisa o hub
}

func TestClienteHTTP_Update_404Recusas400(t *testing.T) {
	ctx, pool := poolHubCode(t)
	id := clienteComCNPJ(t, ctx, pool, "Existe", cnpjDeTeste())
	h := &ClientsHandler{Repo: catalog.NewClients(pool)}

	require.Equal(t, http.StatusNotFound, putCliente(t, h, uuid.New(), map[string]any{"name": "x"}).Code)

	rec := putCliente(t, h, id, `{`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid request")
	rec = putCliente(t, h, id, map[string]any{"name": ""})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "name is required")
	rec = putCliente(t, h, id, map[string]any{"name": "x", "target_label": strings.Repeat("a", 201)})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "target_label must be at most 200 characters")

	nome, _, _ := linhaDoCliente(t, ctx, pool, id)
	require.Equal(t, "Existe", nome, "as recusas não gravam")
}

// O CNPJ só é conferido quando os 14 dígitos MUDAM: trocar por um CNPJ livre
// passa (e grava), apagar o CNPJ (nulo/vazio) passa, e nenhum caso avisa o hub.
func TestClienteHTTP_Update_MudarOuApagarOCNPJLivrePassa(t *testing.T) {
	ctx, pool := poolHubCode(t)
	id := clienteComCNPJ(t, ctx, pool, "Troca CNPJ", cnpjDeTeste())
	h := &ClientsHandler{Repo: catalog.NewClients(pool)}

	livre := cnpjDeTeste()
	rec := putCliente(t, h, id, map[string]any{"name": "Troca CNPJ", "cnpj": mascara(livre)})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	_, _, cnpj := linhaDoCliente(t, ctx, pool, id)
	require.Equal(t, mascara(livre), *cnpj)

	rec = putCliente(t, h, id, map[string]any{"name": "Troca CNPJ", "cnpj": ""})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec = putCliente(t, h, id, map[string]any{"name": "Troca CNPJ"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}
