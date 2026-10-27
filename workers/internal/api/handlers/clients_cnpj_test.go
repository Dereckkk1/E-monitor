package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/catalog"
)

// CNPJ inventado por teste, para não colidir com dado de outro teste.
func cnpjDeTeste() string {
	d := []rune(uuid.New().String())
	out := make([]rune, 0, 14)
	for _, r := range d {
		if r >= '0' && r <= '9' {
			out = append(out, r)
		}
		if len(out) == 14 {
			break
		}
	}
	for len(out) < 14 {
		out = append(out, '7')
	}
	return string(out)
}

func mascara(c string) string {
	return c[0:2] + "." + c[2:5] + "." + c[5:8] + "/" + c[8:12] + "-" + c[12:14]
}

func clienteComCNPJ(t *testing.T, ctx context.Context, pool *pgxpool.Pool, nome, cnpj string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO clients (name, cnpj) VALUES ($1, $2) RETURNING id`, nome, cnpj).Scan(&id))
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, id) })
	return id
}

func postCliente(t *testing.T, h *ClientsHandler, corpo map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(corpo)
	rec := httptest.NewRecorder()
	h.Create(rec, httptest.NewRequest(http.MethodPost, "/clients", bytes.NewReader(b)))
	return rec
}

func TestCreateClienteComCNPJMascaradoDeOutroDa409(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cnpj := cnpjDeTeste()
	existente := clienteComCNPJ(t, ctx, pool, "Giga + Internet", cnpj)

	rec := postCliente(t, &ClientsHandler{Repo: catalog.NewClients(pool)},
		map[string]any{"name": "GIGA MAIS FIBRA", "cnpj": mascara(cnpj)})

	require.Equal(t, http.StatusConflict, rec.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	ex, _ := out["existente"].(map[string]any)
	require.Equal(t, existente.String(), ex["id"])
	require.Equal(t, "Giga + Internet", ex["nome"])
}

func TestCreateClienteFilialNaoColide(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cnpj := cnpjDeTeste()
	clienteComCNPJ(t, ctx, pool, "Matriz", cnpj)
	filial := cnpj[:8] + "0002" + cnpj[12:]
	if filial == cnpj {
		filial = cnpj[:8] + "0003" + cnpj[12:]
	}

	rec := postCliente(t, &ClientsHandler{Repo: catalog.NewClients(pool)},
		map[string]any{"name": "Filial", "cnpj": filial})

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, out["id"]) })
}

func TestUpdateClienteParaCNPJDeOutroDa409MasOProprioPassa(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cnpjA, cnpjB := cnpjDeTeste(), cnpjDeTeste()
	clienteComCNPJ(t, ctx, pool, "A", cnpjA)
	b := clienteComCNPJ(t, ctx, pool, "B", cnpjB)
	h := &ClientsHandler{Repo: catalog.NewClients(pool)}

	put := func(cnpj string) int {
		body, _ := json.Marshal(map[string]any{"name": "B", "cnpj": cnpj})
		req := comParamDeRota(httptest.NewRequest(http.MethodPut, "/clients/"+b.String(), bytes.NewReader(body)), "id", b.String())
		rec := httptest.NewRecorder()
		h.Update(rec, req)
		return rec.Code
	}
	require.Equal(t, http.StatusConflict, put(mascara(cnpjA)))
	require.Equal(t, http.StatusOK, put(mascara(cnpjB)), "salvar o próprio CNPJ com máscara não é repetição")
}

// Duplicata que já existe (A e B com os mesmos 14 dígitos) não pode travar a
// edição de B: o CNPJ só é conferido quando os dígitos MUDAM.
func TestUpdateClienteJaDuplicadoEditaSemMudarOCNPJ(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cnpj, terceiro := cnpjDeTeste(), cnpjDeTeste()
	clienteComCNPJ(t, ctx, pool, "A", cnpj)
	b := clienteComCNPJ(t, ctx, pool, "B", cnpj)
	clienteComCNPJ(t, ctx, pool, "C", terceiro)
	h := &ClientsHandler{Repo: catalog.NewClients(pool)}

	put := func(nome, c string) int {
		body, _ := json.Marshal(map[string]any{"name": nome, "cnpj": c})
		req := comParamDeRota(httptest.NewRequest(http.MethodPut, "/clients/"+b.String(), bytes.NewReader(body)), "id", b.String())
		rec := httptest.NewRecorder()
		h.Update(rec, req)
		return rec.Code
	}
	require.Equal(t, http.StatusOK, put("B renomeado", mascara(cnpj)), "mesmos dígitos: não confere")
	require.Equal(t, http.StatusConflict, put("B renomeado", mascara(terceiro)), "mudar para o CNPJ de um terceiro continua 409")
}
