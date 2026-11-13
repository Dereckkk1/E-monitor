package handlers

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"radiocheck/internal/catalog"
)

func TestCreateComLigacaoFaltandoDa422ComAMensagem(t *testing.T) {
	ctx, pool := poolHubCode(t)
	c := clienteComHubID(t, ctx, pool, "LOJAS RENNER", nil)
	fake, hb := novoHubDaLigacao(t)
	h := &CampaignsHandler{Repo: catalog.NewCampaigns(pool), Hub: hb, Ligacoes: catalog.NewLigacoes(pool)}
	_ = fake

	rec := postCampanha(t, h, campanhaNova(c, "EH-7K4M2X"))

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "ainda não está ligado")
	require.Contains(t, rec.Body.String(), "Lojas Renner")
	require.Equal(t, 0, contaCampanhas(t, ctx, pool, c))
}

func TestCreateComLigacaoCompletaPassa(t *testing.T) {
	ctx, pool := poolHubCode(t)
	fake, hb := novoHubDaLigacao(t)
	hid := fake.id()
	c := clienteComHubID(t, ctx, pool, "LOJAS RENNER", &hid)
	fake.mudar(func(f *hubLigacaoFalso) { f.idNaPlataforma = c.String() })
	h := &CampaignsHandler{Repo: catalog.NewCampaigns(pool), Hub: hb, Ligacoes: catalog.NewLigacoes(pool)}

	rec := postCampanha(t, h, campanhaNova(c, "EH-7K4M2X"))

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
}

// Decisão 2 da spec do código, mantida: hub fora não trava.
func TestCreateComHubForaPassaMesmoSemLigacao(t *testing.T) {
	ctx, pool := poolHubCode(t)
	c := clienteComHubID(t, ctx, pool, "LOJAS RENNER", nil)
	hb, _ := hubFalsoParaCodigo(t, http.StatusServiceUnavailable, `{}`)
	h := &CampaignsHandler{Repo: catalog.NewCampaigns(pool), Hub: hb, Ligacoes: catalog.NewLigacoes(pool)}

	rec := postCampanha(t, h, campanhaNova(c, "EH-7K4M2X"))

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
}

func TestUpdateQueMudaOCodigoTambemPassaPelaLigacao(t *testing.T) {
	ctx, pool := poolHubCode(t)
	c := clienteComHubID(t, ctx, pool, "LOJAS RENNER", nil)
	_, hb := novoHubDaLigacao(t)
	h := &CampaignsHandler{Repo: catalog.NewCampaigns(pool), Hub: hb, Ligacoes: catalog.NewLigacoes(pool)}
	id := criaCampanhaDireto(t, ctx, pool, c, "").ID

	rec := putCampanha(t, h, id, map[string]any{
		"name": "Verão 2026", "start_date": "2026-12-01T00:00:00Z", "end_date": "2027-02-28T00:00:00Z",
		"hub_code": "EH-7K4M2X",
	})

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

// CNPJ do cliente escolhido ≠ CNPJ do dono do código no hub: o salvar trava.
func TestCreateComCNPJDivergenteDa422(t *testing.T) {
	ctx, pool := poolHubCode(t)
	c := clienteComHubID(t, ctx, pool, "LOJAS RENNER", nil)
	_, err := pool.Exec(ctx, `UPDATE clients SET cnpj = '07714104000107' WHERE id = $1`, c)
	require.NoError(t, err)
	fake, hb := novoHubDaLigacao(t)
	fake.mudar(func(f *hubLigacaoFalso) { f.cnpj = "03.984.660/0001-81" })
	h := &CampaignsHandler{Repo: catalog.NewCampaigns(pool), Hub: hb, Ligacoes: catalog.NewLigacoes(pool)}

	rec := postCampanha(t, h, campanhaNova(c, "EH-7K4M2X"))

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "CNPJ")
	require.Equal(t, 0, contaCampanhas(t, ctx, pool, c))
}
