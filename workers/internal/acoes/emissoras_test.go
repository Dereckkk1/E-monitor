package acoes

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func fl(f float64) *float64 { return &f }
func sp(s string) *string   { return &s }

// semeiaEmissora cria o estado que o MCP não pode estragar: stream_url, logo e
// um metadata com chave que o struct StationMeta NÃO conhece.
func (c *cenario) emissoraComMetadados(t *testing.T) uuid.UUID {
	t.Helper()
	_, err := c.pool.Exec(c.ctx, `
		UPDATE stations SET logo_url = 'http://logo/x.png', city = 'Cidade Inexistente XYZ', state = 'SP',
		       frequency_mhz = 99.9, pmm = 100,
		       metadata = '{"categories":["jovem"],"chave_desconhecida":{"a":1}}'::jsonb
		WHERE id = $1`, c.emissora)
	require.NoError(t, err)
	return c.emissora
}

func (c *cenario) streamEMeta(t *testing.T, id uuid.UUID) (stream, band, logo, meta string) {
	t.Helper()
	require.NoError(t, c.pool.QueryRow(c.ctx,
		`SELECT stream_url, band, logo_url, metadata::text FROM stations WHERE id = $1`, id).Scan(&stream, &band, &logo, &meta))
	return stream, band, logo, meta
}

func TestEditarCadastroEmissora_ParcialMantemOResto(t *testing.T) {
	c := novoCenario(t)
	id := c.emissoraComMetadados(t)
	streamAntes, _, _, _ := c.streamEMeta(t, id)

	st, err := c.srv.EditarCadastroEmissora(c.ctx, id, CadastroEmissora{Nome: sp("  Nome Novo FM  ")})

	require.NoError(t, err)
	require.Equal(t, "Nome Novo FM", st.Name)
	require.InDelta(t, 99.9, *st.FrequencyMHz, 0.001, "frequência não enviada: mantida")
	require.Equal(t, "Cidade Inexistente XYZ", *st.City)
	require.Equal(t, "SP", *st.State)
	require.InDelta(t, 100, *st.PMM, 0.001)
	stream, band, logo, meta := c.streamEMeta(t, id)
	require.Equal(t, streamAntes, stream, "o MCP nunca troca stream_url")
	require.Equal(t, "FM", band)
	require.Equal(t, "http://logo/x.png", logo)
	require.JSONEq(t, `{"categories":["jovem"],"chave_desconhecida":{"a":1}}`, meta,
		"o metadata inteiro sobrevive, inclusive a chave que o struct não conhece")
}

func TestEditarCadastroEmissora_MudarACidadeGeocodificaComAUFAtual(t *testing.T) {
	c := novoCenario(t)
	id := c.emissoraComMetadados(t)
	_, err := c.pool.Exec(c.ctx, `UPDATE stations SET state = 'SP', latitude = NULL, longitude = NULL WHERE id = $1`, id)
	require.NoError(t, err)

	// Só a cidade vem no pedido; a UF é a que já estava (SP).
	st, err := c.srv.EditarCadastroEmissora(c.ctx, id, CadastroEmissora{Cidade: sp("São Paulo")})

	require.NoError(t, err)
	require.Equal(t, "São Paulo", *st.City)
	require.NotNil(t, st.Latitude, "a geocodificação roda quando a cidade muda")
	require.InDelta(t, -23.55, *st.Latitude, 0.6)
	require.InDelta(t, -46.63, *st.Longitude, 0.6)
	// A emissora segue com stream e metadados.
	_, _, _, meta := c.streamEMeta(t, id)
	require.JSONEq(t, `{"categories":["jovem"],"chave_desconhecida":{"a":1}}`, meta)
}

func TestEditarCadastroEmissora_AplicaOsCincoCamposENormalizaAUF(t *testing.T) {
	c := novoCenario(t)
	id := c.emissoraComMetadados(t)

	st, err := c.srv.EditarCadastroEmissora(c.ctx, id, CadastroEmissora{
		Nome: sp("Outra FM"), FrequenciaMHz: fl(104.5), Cidade: sp("Uberaba"), UF: sp(" mg "), PMM: fl(54321),
	})

	require.NoError(t, err)
	require.Equal(t, "Outra FM", st.Name)
	require.InDelta(t, 104.5, *st.FrequencyMHz, 0.001)
	require.Equal(t, "Uberaba", *st.City)
	require.Equal(t, "MG", *st.State)
	require.InDelta(t, 54321, *st.PMM, 0.001)
}

func TestEditarCadastroEmissora_CamposVaziosNaoApagam(t *testing.T) {
	c := novoCenario(t)
	id := c.emissoraComMetadados(t)

	// Um pedido sem nenhum campo não muda nada (e não é erro).
	st, err := c.srv.EditarCadastroEmissora(c.ctx, id, CadastroEmissora{})

	require.NoError(t, err)
	require.Equal(t, "Rádio Ação", st.Name)
	require.InDelta(t, 99.9, *st.FrequencyMHz, 0.001)
}

func TestEditarCadastroEmissora_Recusas(t *testing.T) {
	c := novoCenario(t)
	id := c.emissoraComMetadados(t)
	for nome, in := range map[string]CadastroEmissora{
		"nome vazio":           {Nome: sp("   ")},
		"cidade vazia":         {Cidade: sp(" ")},
		"UF com 3 letras":      {UF: sp("MGX")},
		"UF com 1 letra":       {UF: sp("M")},
		"frequência zero":      {FrequenciaMHz: fl(0)},
		"frequência negativa":  {FrequenciaMHz: fl(-1)},
		"frequência absurda":   {FrequenciaMHz: fl(10000)},
		"PMM negativo":         {PMM: fl(-1)},
		"PMM acima do numeric": {PMM: fl(1e8)},
	} {
		t.Run(nome, func(t *testing.T) {
			_, err := c.srv.EditarCadastroEmissora(c.ctx, id, in)
			require.Equal(t, CodigoEntradaInvalida, recusaDe(t, err).Codigo)
		})
	}
	// Nada foi gravado pelas recusas.
	var nome string
	require.NoError(t, c.pool.QueryRow(c.ctx, `SELECT name FROM stations WHERE id = $1`, id).Scan(&nome))
	require.Equal(t, "Rádio Ação", nome)
}

func TestEditarCadastroEmissora_Inexistente(t *testing.T) {
	c := novoCenario(t)
	_, err := c.srv.EditarCadastroEmissora(c.ctx, uuid.New(), CadastroEmissora{Nome: sp("x")})
	require.Equal(t, CodigoNaoEncontrado, recusaDe(t, err).Codigo)
}
