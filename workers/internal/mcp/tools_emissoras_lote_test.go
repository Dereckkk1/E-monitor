package mcp

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/mcp/mcptest"
)

// emissoraComDial cria uma emissora com dial, cidade e UF (dial nil = sem dial cadastrado).
func emissoraComDial(t *testing.T, ctx context.Context, pool *pgxpool.Pool, nome, banda string, dial *float64, cidade, uf string) uuid.UUID {
	t.Helper()
	id := mcptest.Emissora(t, ctx, pool, nome, 0)
	_, err := pool.Exec(ctx, `UPDATE stations SET band = $2, frequency_mhz = $3, city = $4, state = $5 WHERE id = $1`,
		id, banda, dial, cidade, uf)
	require.NoError(t, err)
	return id
}

func f64(v float64) *float64 { return &v }

func itensLote(t *testing.T, out map[string]any) []map[string]any {
	t.Helper()
	bruto, ok := out["itens"].([]any)
	require.True(t, ok, "sem itens: %v", out)
	res := make([]map[string]any, len(bruto))
	for i, v := range bruto {
		res[i] = v.(map[string]any)
	}
	return res
}

func idsCandidatos(item map[string]any) []string {
	var ids []string
	for _, c := range item["candidatos"].([]any) {
		ids = append(ids, c.(map[string]any)["id"].(string))
	}
	return ids
}

func TestBuscarEmissorasLote_ResolveOLoteNumaChamada(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	vale := emissoraComDial(t, ctx, pool, "Vale FM", "FM", f64(99.9), "Juazeiro do Norte", "CE")
	clubeRP := emissoraComDial(t, ctx, pool, "Clube FM", "FM", f64(100.5), "Ribeirão Preto", "SP")
	clubeFranca := emissoraComDial(t, ctx, pool, "Rádio Clube", "FM", f64(100.5), "Franca", "SP")
	rock := emissoraComDial(t, ctx, pool, "89 - FM (89.1)", "FM", nil, "São Paulo", "SP")
	cem := emissoraComDial(t, ctx, pool, "Cem FM", "FM", f64(100), "Campinas", "SP")
	emissoraComDial(t, ctx, pool, "Vale FM", "FM", f64(99.9), "Crato", "PE") // mesmo dial, outra UF

	out := chamarTool(t, cfgTeste(pool), "buscar_emissoras_lote", map[string]any{"emissoras": []map[string]any{
		{"ref": "Vale | 99,9 | Juazeiro do Norte/CE", "dial": "99,9", "uf": "ce", "cidade": "juazeiro do norte", "nome": "Vale FM"},
		{"ref": "Clube sem cidade", "dial": "100.5", "uf": "SP"},
		{"ref": "Clube RP", "dial": "100,5", "uf": "SP", "cidade": "Ribeirao Preto", "nome": "Clube"},
		{"ref": "Clube em Santos", "dial": "100,5", "uf": "SP", "cidade": "Santos"},
		{"ref": "89 Rock", "dial": "89,1", "uf": "SP", "nome": "89 Rock"},
		{"ref": "9,1 não cola no 89.1", "dial": "9,1", "uf": "SP"},
		{"ref": "100,0 = 100", "dial": "100,0", "uf": "SP"},
		{"ref": "Vale de novo", "dial": "99.90", "uf": "CE"},
		{"ref": "inexistente", "dial": "107,3", "uf": "SP"},
	}})
	it := itensLote(t, out)
	require.Len(t, it, 9)

	require.Equal(t, "unica", it[0]["resultado"])
	require.Equal(t, "Vale | 99,9 | Juazeiro do Norte/CE", it[0]["ref"])
	require.Equal(t, []string{vale.String()}, idsCandidatos(it[0]))
	c0 := it[0]["candidatos"].([]any)[0].(map[string]any)
	require.Equal(t, true, c0["cidade_confere"])
	require.Equal(t, true, c0["nome_confere"])

	require.Equal(t, "varias", it[1]["resultado"])
	require.ElementsMatch(t, []string{clubeRP.String(), clubeFranca.String()}, idsCandidatos(it[1]))
	require.Nil(t, it[1]["candidatos"].([]any)[0].(map[string]any)["cidade_confere"], "sem cidade na linha = null")

	require.Equal(t, "unica", it[2]["resultado"], "cidade sem acento casa com 'Ribeirão Preto'")
	require.Equal(t, []string{clubeRP.String()}, idsCandidatos(it[2]))

	require.Equal(t, "cidade_diferente", it[3]["resultado"])
	require.ElementsMatch(t, []string{clubeRP.String(), clubeFranca.String()}, idsCandidatos(it[3]))

	require.Equal(t, "unica", it[4]["resultado"], "sem dial cadastrado, o dial está no nome")
	require.Equal(t, []string{rock.String()}, idsCandidatos(it[4]))

	require.Equal(t, "nenhuma", it[5]["resultado"])
	require.Empty(t, it[5]["candidatos"])

	require.Equal(t, []string{cem.String()}, idsCandidatos(it[6]))

	require.Equal(t, "unica", it[7]["resultado"])
	require.Equal(t, "nenhuma", it[8]["resultado"])

	resumo := out["resumo"].(map[string]any)
	require.EqualValues(t, 5, resumo["unica"])
	require.EqualValues(t, 1, resumo["varias"])
	require.EqualValues(t, 1, resumo["cidade_diferente"])
	require.EqualValues(t, 2, resumo["nenhuma"])
	require.Contains(t, out["ressalvas"], "emissoras[0] e emissoras[7] caíram na mesma emissora (Vale FM): confira se o PI repete a emissora ou se uma linha está errada")
}

func TestBuscarEmissorasLote_NomeQueConfereVemPrimeiroEBandaFiltra(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	emissoraComDial(t, ctx, pool, "Alfa", "FM", f64(98.1), "Goiânia", "GO")
	positiva := emissoraComDial(t, ctx, pool, "Positiva FM", "FM", f64(98.1), "Goiânia", "GO")
	am := emissoraComDial(t, ctx, pool, "Rádio Positiva", "AM", f64(98.1), "Goiânia", "GO")

	out := chamarTool(t, cfgTeste(pool), "buscar_emissoras_lote", map[string]any{"emissoras": []map[string]any{
		{"dial": "98,1", "uf": "GO", "cidade": "Goiania", "nome": "Rádio Positiva FM"},
		{"dial": "98,1", "uf": "GO", "banda": "am"},
	}})
	it := itensLote(t, out)
	require.Equal(t, "varias", it[0]["resultado"])
	ids := idsCandidatos(it[0])
	require.Len(t, ids, 3)
	require.ElementsMatch(t, []string{positiva.String(), am.String()}, ids[:2], "as que têm 'positiva' no nome vêm antes")
	require.Equal(t, false, it[0]["candidatos"].([]any)[2].(map[string]any)["nome_confere"])

	require.Equal(t, "unica", it[1]["resultado"])
	require.Equal(t, []string{am.String()}, idsCandidatos(it[1]))
}

func TestBuscarEmissorasLote_Recusas(t *testing.T) {
	_, pool := mcptest.DBCatalogo(t)
	cfg := cfgTeste(pool)
	casos := map[string]struct {
		emissoras []map[string]any
		trecho    string
	}{
		"lista vazia": {[]map[string]any{}, "de 1 a 100"},
		"dial texto":  {[]map[string]any{{"dial": "99,9", "uf": "SP"}, {"dial": "noventa", "uf": "SP"}}, "emissoras[1]: dial"},
		"uf por nome": {[]map[string]any{{"dial": "99,9", "uf": "São Paulo"}}, "emissoras[0]: uf"},
		"banda":       {[]map[string]any{{"dial": "99,9", "uf": "SP", "banda": "OM"}}, "emissoras[0]: banda"},
	}
	for nome, c := range casos {
		texto := chamarToolErro(t, cfg, "buscar_emissoras_lote", map[string]any{"emissoras": c.emissoras})
		require.Contains(t, texto, c.trecho, nome)
	}
	muitas := make([]map[string]any, tetoLinhasLote+1)
	for i := range muitas {
		muitas[i] = map[string]any{"dial": "99,9", "uf": "SP"}
	}
	require.Contains(t, chamarToolErro(t, cfg, "buscar_emissoras_lote", map[string]any{"emissoras": muitas}), "de 1 a 100")
}

func TestDialDe_EspelhaONumericDoBanco(t *testing.T) {
	for f, want := range map[float64]string{99.9: "99.9", 100: "100", 100.5: "100.5", 1340: "1340", 87.95: "87.95", 90.1: "90.1"} {
		require.Equal(t, want, dialDe(f), f)
	}
}
