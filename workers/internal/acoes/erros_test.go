package acoes

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

func pgErro(code, constraint string) error {
	return &pgconn.PgError{Code: code, ConstraintName: constraint, Message: "texto cru do Postgres " + constraint}
}

func TestRecusaDoBanco_FKsConhecidasPeloNomeExato(t *testing.T) {
	casos := map[string]struct{ codigo, mensagem string }{
		"distribution_rules_type_id_fkey":         {CodigoEntradaInvalida, "tipo de material inexistente"},
		"distribution_overrides_type_id_fkey":     {CodigoEntradaInvalida, "tipo de material inexistente"},
		"materials_type_id_fkey":                  {CodigoEntradaInvalida, "tipo de material inexistente"},
		"distribution_rules_campaign_id_fkey":     {CodigoNaoEncontrado, "campanha não encontrada"},
		"distribution_overrides_campaign_id_fkey": {CodigoNaoEncontrado, "campanha não encontrada"},
		"campaign_materials_campaign_id_fkey":     {CodigoNaoEncontrado, "campanha não encontrada"},
		"distribution_overrides_station_id_fkey":  {CodigoEntradaInvalida, "emissora inexistente"},
		"campaign_materials_material_id_fkey":     {CodigoNaoEncontrado, "material não encontrado"},
		"campaigns_client_id_fkey":                {CodigoNaoEncontrado, "cliente não encontrado"},
	}
	for nome, esperado := range casos {
		t.Run(nome, func(t *testing.T) {
			r := recusaDe(t, recusaDoBanco(pgErro("23503", nome)))
			require.Equal(t, esperado.codigo, r.Codigo)
			require.Equal(t, esperado.mensagem, r.Mensagem)
		})
	}
}

func TestRecusaDoBanco_ChecksConhecidosGanhamMensagemEmPortugues(t *testing.T) {
	casos := map[string]string{
		"distribution_rules_weekday_mask_check":       "a máscara de dias precisa estar entre 0 e 127",
		"distribution_rules_plays_per_day_check":      "as inserções por dia precisam estar entre 1 e 100",
		"distribution_overrides_plays_expected_check": "as inserções do dia não podem ser negativas",
		"rule_dates_valid":                            "a data final não pode ser anterior à inicial",
		"campaign_dates_valid":                        "a data final não pode ser anterior à inicial",
		"rule_times_valid":                            "a hora final precisa ser maior que a inicial",
		"override_times_valid":                        "a hora final precisa ser maior que a inicial",
	}
	for nome, msg := range casos {
		t.Run(nome, func(t *testing.T) {
			r := recusaDe(t, recusaDoBanco(pgErro("23514", nome)))
			require.Equal(t, CodigoEntradaInvalida, r.Codigo)
			require.Equal(t, msg, r.Mensagem)
		})
	}
}

// FK e CHECK que não estão mapeados viram recusa GENÉRICA — a mensagem nunca
// carrega o nome da constraint, o SQLSTATE nem o texto cru do Postgres.
func TestRecusaDoBanco_NaoMapeadoViraGenericoSemONome(t *testing.T) {
	for _, caso := range []struct{ code, constraint, esperada string }{
		{"23503", "alguma_tabela_nova_type_id_fkey", "uma das referências informadas não existe"},
		{"23503", "", "uma das referências informadas não existe"},
		{"23514", "alguma_tabela_nova_check", "valor fora do permitido"},
		{"23514", "", "valor fora do permitido"},
	} {
		r := recusaDe(t, recusaDoBanco(pgErro(caso.code, caso.constraint)))
		require.Equal(t, CodigoEntradaInvalida, r.Codigo)
		require.Equal(t, caso.esperada, r.Mensagem)
		require.NotContains(t, r.Mensagem, "alguma_tabela")
		require.NotContains(t, r.Mensagem, caso.code)
		require.NotContains(t, strings.ToLower(r.Mensagem), "postgres")
	}
}

// O nome é casado por IGUALDADE: um nome que só CONTÉM "type_id" não vira
// "tipo de material inexistente".
func TestRecusaDoBanco_NomeParecidoNaoCasa(t *testing.T) {
	r := recusaDe(t, recusaDoBanco(pgErro("23503", "outra_tabela_type_id_fkey")))
	require.Equal(t, "uma das referências informadas não existe", r.Mensagem)
}

func TestRecusaDoBanco_NumericoForaDoIntervaloEFormatoDeHora(t *testing.T) {
	r := recusaDe(t, recusaDoBanco(pgErro("22003", "")))
	require.Equal(t, CodigoEntradaInvalida, r.Codigo)
	require.Equal(t, "valor numérico fora do intervalo permitido", r.Mensagem)

	for _, code := range []string{"22007", "22008", "22023", "22P02"} {
		r := recusaDe(t, recusaDoBanco(pgErro(code, "")))
		require.Equal(t, CodigoEntradaInvalida, r.Codigo, code)
		require.Equal(t, "data ou horário inválido", r.Mensagem, code)
	}
}

// Erro do SISTEMA volta como veio: não vira recusa, então o token de confirmação
// do MCP NÃO é liberado (a escrita pode ter acontecido).
func TestRecusaDoBanco_ErroDoSistemaPassaCru(t *testing.T) {
	cru := errors.New("conexão caiu")
	require.Same(t, cru, recusaDoBanco(cru))
	require.NoError(t, recusaDoBanco(nil))

	for _, code := range []string{"40P01", "40001", "57014", "08006", "53300"} {
		e := pgErro(code, "qualquer")
		got := recusaDoBanco(e)
		require.Same(t, e, got, code)
		var r *Recusa
		require.False(t, errors.As(got, &r), "%s não pode virar Recusa", code)
	}
}

// O PgError embrulhado (fmt.Errorf %w) também é reconhecido.
func TestRecusaDoBanco_ReconheceOPgErrorEmbrulhado(t *testing.T) {
	r := recusaDe(t, recusaDoBanco(fmt.Errorf("insert: %w", pgErro("23503", "distribution_rules_type_id_fkey"))))
	require.Equal(t, "tipo de material inexistente", r.Mensagem)
}

// Guarda contra erro de digitação ou renomeação: todo nome mapeado existe de
// verdade no banco migrado (e é do tipo certo).
func TestRecusaDoBanco_OsNomesMapeadosExistemNoBanco(t *testing.T) {
	c := novoCenario(t)
	existe := func(nome, tipo string) bool {
		var n int
		require.NoError(t, c.pool.QueryRow(c.ctx,
			`SELECT count(*) FROM pg_constraint WHERE conname = $1 AND contype = $2`, nome, tipo).Scan(&n))
		return n > 0
	}
	for nome := range fksConhecidas {
		require.True(t, existe(nome, "f"), "FK %s não existe", nome)
	}
	for nome := range checksConhecidos {
		require.True(t, existe(nome, "c"), "CHECK %s não existe", nome)
	}
}
