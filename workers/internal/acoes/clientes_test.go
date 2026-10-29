package acoes

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/catalog"
	"radiocheck/internal/hub"
	"radiocheck/internal/mcp/mcptest"
)

func TestNormalizarRotuloPublico(t *testing.T) {
	var nulo *string
	require.True(t, NormalizarRotuloPublico(&nulo))
	require.Nil(t, nulo)

	vazio := "   "
	p := &vazio
	require.True(t, NormalizarRotuloPublico(&p))
	require.Nil(t, p, "só espaço vira nil")

	s := "  Jovens  "
	p = &s
	require.True(t, NormalizarRotuloPublico(&p))
	require.Equal(t, "Jovens", *p)

	acentuado := strings.Repeat("é", MaxRotuloPublico) // 200 runes, 400 bytes
	p = &acentuado
	require.True(t, NormalizarRotuloPublico(&p), "conta runes, não bytes")

	longo := strings.Repeat("é", MaxRotuloPublico+1)
	p = &longo
	require.False(t, NormalizarRotuloPublico(&p))
}

func TestDigitosDoCNPJ(t *testing.T) {
	require.Equal(t, "", DigitosDoCNPJ(nil))
	m := "07.714.104/0001-07"
	require.Equal(t, "07714104000107", DigitosDoCNPJ(&m))
}

func cnpjUnico() string {
	var b strings.Builder
	for _, r := range uuid.NewString() {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
		if b.Len() == 14 {
			break
		}
	}
	for b.Len() < 14 {
		b.WriteRune('7')
	}
	return b.String()
}

func mascarado(c string) string {
	return c[0:2] + "." + c[2:5] + "." + c[5:8] + "/" + c[8:12] + "-" + c[12:14]
}

func TestChecarCNPJ_DuplicadoTrazOExistenteEPodeExcluirOProprio(t *testing.T) {
	c := novoCenario(t)
	cnpj := cnpjUnico()
	dono, err := catalog.NewClients(c.pool).Create(c.ctx, catalog.CreateClientInput{Name: "Dono", CNPJ: &cnpj})
	require.NoError(t, err)
	mascara := mascarado(cnpj)

	r := recusaDe(t, c.srv.ChecarCNPJ(c.ctx, &mascara, uuid.Nil))

	require.Equal(t, CodigoCNPJDuplicado, r.Codigo)
	require.Contains(t, r.Mensagem, "Dono")
	require.Equal(t, map[string]any{"id": dono.ID.String(), "nome": "Dono"}, r.Dados["existente"])
	// O próprio cliente não conta como duplicata de si mesmo (salvar com máscara).
	require.NoError(t, c.srv.ChecarCNPJ(c.ctx, &mascara, dono.ID))
	// Sem CNPJ nunca há duplicata.
	require.NoError(t, c.srv.ChecarCNPJ(c.ctx, nil, uuid.Nil))
}

func TestCriarCliente_GravaApagaORotuloEAvisaOHub(t *testing.T) {
	c := novoCenario(t)
	hf := &mcptest.HubFalso{}
	c.srv.Hub = hf
	rotulo := "  Mulheres 25-45  "
	cnpj := cnpjUnico()
	cidade, uf := "Uberaba", "MG"

	out, err := c.srv.CriarCliente(c.ctx, catalog.CreateClientInput{
		Name: "Cliente Novo", CNPJ: &cnpj, City: &cidade, State: &uf, TargetLabel: &rotulo,
	})

	require.NoError(t, err)
	require.Equal(t, "Mulheres 25-45", *out.TargetLabel)
	evs := hf.EventosDoTipo("cliente.upsert")
	require.Len(t, evs, 1)
	dados := evs[0].Dados.(hub.ClienteUpsert)
	require.Equal(t, out.ID.String(), dados.IDNaPlataforma)
	require.Equal(t, "Cliente Novo", dados.Nome)
	require.Equal(t, "Uberaba", *dados.Cidade)
}

func TestCriarCliente_SemHubSoGrava(t *testing.T) {
	c := novoCenario(t) // Hub nil
	out, err := c.srv.CriarCliente(c.ctx, catalog.CreateClientInput{Name: "Sem hub"})
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, out.ID)

	c.srv.Hub = &mcptest.HubFalso{SemConfiguracao: true}
	_, err = c.srv.CriarCliente(c.ctx, catalog.CreateClientInput{Name: "Hub desligado"})
	require.NoError(t, err)
}

func TestCriarCliente_RecusasComOsTextosDaRota(t *testing.T) {
	c := novoCenario(t)

	_, err := c.srv.CriarCliente(c.ctx, catalog.CreateClientInput{})
	r := recusaDe(t, err)
	require.Equal(t, CodigoEntradaInvalida, r.Codigo)
	require.Equal(t, "name is required", r.Mensagem)

	longo := strings.Repeat("a", MaxRotuloPublico+1)
	_, err = c.srv.CriarCliente(c.ctx, catalog.CreateClientInput{Name: "x", TargetLabel: &longo})
	r = recusaDe(t, err)
	require.Equal(t, CodigoEntradaInvalida, r.Codigo)
	require.Equal(t, "target_label must be at most 200 characters", r.Mensagem)
}

func TestCriarCliente_CNPJRepetidoNaoGravaNemAvisaOHub(t *testing.T) {
	c := novoCenario(t)
	hf := &mcptest.HubFalso{}
	c.srv.Hub = hf
	cnpj := cnpjUnico()
	_, err := catalog.NewClients(c.pool).Create(c.ctx, catalog.CreateClientInput{Name: "Dono", CNPJ: &cnpj})
	require.NoError(t, err)
	mascara := mascarado(cnpj)

	_, err = c.srv.CriarCliente(c.ctx, catalog.CreateClientInput{Name: "Repetido", CNPJ: &mascara})

	require.Equal(t, CodigoCNPJDuplicado, recusaDe(t, err).Codigo)
	require.Empty(t, hf.Eventos())
	var n int
	require.NoError(t, c.pool.QueryRow(c.ctx, `SELECT count(*) FROM clients WHERE name = 'Repetido'`).Scan(&n))
	require.Zero(t, n)
}
