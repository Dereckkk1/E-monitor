package mcp

import (
	"context"
	"errors"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestPaginacao_Normalizar(t *testing.T) {
	cases := []struct {
		in            Paginacao
		limit, offset int
	}{
		{Paginacao{}, 25, 0},
		{Paginacao{Limit: 500, Offset: -3}, 100, 0},
		{Paginacao{Limit: 10, Offset: 999_999_999}, 10, offsetTeto},
	}
	for _, c := range cases {
		l, o := c.in.normalizar()
		require.Equal(t, c.limit, l)
		require.Equal(t, c.offset, o)
	}
}

func TestPaginar_TotalRealEHasMore(t *testing.T) {
	todos := []int{1, 2, 3, 4, 5}
	l := paginar(todos, Paginacao{Limit: 2, Offset: 2})
	require.Equal(t, []int{3, 4}, l.Items)
	require.Equal(t, pagina{Total: 5, Limit: 2, Offset: 2, Returned: 2, HasMore: true}, l.Page)

	fim := paginar(todos, Paginacao{Limit: 2, Offset: 9})
	require.Empty(t, fim.Items)
	require.NotNil(t, fim.Items, "items vazio é [], nunca null")
	require.False(t, fim.Page.HasMore)
	require.Equal(t, 5, fim.Page.Total)
}

// fonte paginada que imita os repositórios com COUNT(*) OVER (): total 0
// numa página além do fim.
func fontePaginada(todos []int) func(page, size int) ([]int, int, error) {
	return func(page, size int) ([]int, int, error) {
		ini := (page - 1) * size
		if ini >= len(todos) {
			return nil, 0, nil
		}
		return todos[ini:min(ini+size, len(todos))], len(todos), nil
	}
}

func TestBuscarJanela_OffsetForaDoMultiplo(t *testing.T) {
	todos := []int{1, 2, 3, 4, 5, 6, 7}
	l, err := buscarJanela(Paginacao{Limit: 3, Offset: 2}, fontePaginada(todos))
	require.NoError(t, err)
	require.Equal(t, []int{3, 4, 5}, l.Items)
	require.Equal(t, 7, l.Page.Total)
	require.True(t, l.Page.HasMore)
}

func TestBuscarJanela_AlemDoFimTrazTotalReal(t *testing.T) {
	l, err := buscarJanela(Paginacao{Limit: 3, Offset: 30}, fontePaginada([]int{1, 2, 3, 4}))
	require.NoError(t, err)
	require.Empty(t, l.Items)
	require.Equal(t, 4, l.Page.Total, "total real mesmo com a página vazia")
}

func TestBuscarJanela_PropagaErro(t *testing.T) {
	_, err := buscarJanela(Paginacao{}, func(int, int) ([]int, int, error) { return nil, 0, errors.New("x") })
	require.Error(t, err)
}

// O erro Go de uma tool nunca chega ao modelo: vira "[interno]" e vai pro log.
func TestFerramentaLeitura_ErroInternoNaoVaza(t *testing.T) {
	s := sdk.NewServer(&sdk.Implementation{Name: "t", Version: "1"}, nil)
	ferramentaLeitura(s, ConfigServidor{Leitura: &Leitura{}}, "quebra", "teste",
		func(context.Context, *Leitura, struct{}) (*sdk.CallToolResult, error) {
			return nil, errors.New(`ERROR: relation "x" does not exist (SQLSTATE 42P01)`)
		})
	texto, erro := chamarNoServidor(t, s, "quebra", map[string]any{})
	require.True(t, erro)
	require.Contains(t, texto, "[interno]")
	require.NotContains(t, texto, "SQLSTATE")
}

func TestFerramentaLeitura_SemLeituraFalhaFechado(t *testing.T) {
	s := sdk.NewServer(&sdk.Implementation{Name: "t", Version: "1"}, nil)
	ferramentaLeitura(s, ConfigServidor{}, "sem", "teste",
		func(context.Context, *Leitura, struct{}) (*sdk.CallToolResult, error) {
			t.Fatal("não pode chegar aqui sem Leitura")
			return nil, nil
		})
	texto, erro := chamarNoServidor(t, s, "sem", map[string]any{})
	require.True(t, erro)
	require.Contains(t, texto, "[interno]")
}

// O SDK roda cada request numa goroutine sem recover: panic numa tool
// derrubaria a API inteira. O funil converte em "[interno]".
func TestFerramentaLeitura_PanicVira_Interno(t *testing.T) {
	s := sdk.NewServer(&sdk.Implementation{Name: "t", Version: "1"}, nil)
	ferramentaLeitura(s, ConfigServidor{Leitura: &Leitura{}}, "panica", "teste",
		func(context.Context, *Leitura, struct{}) (*sdk.CallToolResult, error) {
			var m map[string]int
			m["x"] = 1
			return nil, nil
		})
	texto, erro := chamarNoServidor(t, s, "panica", map[string]any{})
	require.True(t, erro)
	require.Contains(t, texto, "[interno]")
	require.NotContains(t, texto, "nil map")
}

func TestFerramentaLeitura_ResultadoNilVira_Interno(t *testing.T) {
	s := sdk.NewServer(&sdk.Implementation{Name: "t", Version: "1"}, nil)
	ferramentaLeitura(s, ConfigServidor{Leitura: &Leitura{}}, "nada", "teste",
		func(context.Context, *Leitura, struct{}) (*sdk.CallToolResult, error) {
			return nil, nil
		})
	texto, erro := chamarNoServidor(t, s, "nada", map[string]any{})
	require.True(t, erro)
	require.Contains(t, texto, "[interno]")
}
