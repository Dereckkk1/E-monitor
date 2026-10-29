package acoes

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// recusaDoBanco traduz o erro do Postgres que é de ENTRADA (e não do sistema)
// numa *Recusa. Existe porque as rotas antigas devolviam 500 para um tipo de
// material inexistente ou uma faixa horária invertida — o CHECK/FK do banco
// disparava e o handler só sabia dizer "internal error". Aqui a pessoa (ou o
// modelo) recebe o motivo. Qualquer outro erro volta como veio (falha interna):
// não-PgError, deadlock (40P01), falha de serialização (40001), cancelamento
// (57014) e o que mais for do sistema NÃO viram recusa — e portanto não liberam
// o token de confirmação do MCP.
//
// A validação em Go vem ANTES e dá a mensagem boa; isto é a rede para o que ela
// não prevê (e para a corrida entre a checagem e o INSERT).
//
// ⚠️ A Mensagem NUNCA carrega nome de constraint nem SQLSTATE: ela vai para o
// usuário e para o modelo. FK e CHECK são casados pelo nome EXATO da constraint
// (os nomes vêm das migrations); o que não está mapeado vira uma recusa
// genérica, sem o nome.
func recusaDoBanco(err error) error {
	var pg *pgconn.PgError
	if !errors.As(err, &pg) {
		return err
	}
	switch pg.Code {
	case "23503": // foreign_key_violation
		if r, ok := fksConhecidas[pg.ConstraintName]; ok {
			return &Recusa{Codigo: r.codigo, Mensagem: r.mensagem}
		}
		return entradaInvalida("uma das referências informadas não existe")
	case "23514": // check_violation
		if msg, ok := checksConhecidos[pg.ConstraintName]; ok {
			return entradaInvalida(msg)
		}
		return entradaInvalida("valor fora do permitido")
	case "22003": // numeric_value_out_of_range
		return entradaInvalida("valor numérico fora do intervalo permitido")
	case "22007", "22008", "22023", "22P02": // formato/estouro de data ou hora, parâmetro inválido (ex.: "meio-dia"::time)
		return entradaInvalida("data ou horário inválido")
	}
	return err
}

type recusaConhecida struct{ codigo, mensagem string }

// fksConhecidas: constraint FK → o que dizer. Os nomes são os que o Postgres
// gerou nas migrations (conferidos em pg_constraint).
var fksConhecidas = map[string]recusaConhecida{
	"distribution_rules_type_id_fkey":     {CodigoEntradaInvalida, "tipo de material inexistente"},
	"distribution_overrides_type_id_fkey": {CodigoEntradaInvalida, "tipo de material inexistente"},
	"materials_type_id_fkey":              {CodigoEntradaInvalida, "tipo de material inexistente"},

	"distribution_rules_campaign_id_fkey":     {CodigoNaoEncontrado, "campanha não encontrada"},
	"distribution_overrides_campaign_id_fkey": {CodigoNaoEncontrado, "campanha não encontrada"},
	"campaign_materials_campaign_id_fkey":     {CodigoNaoEncontrado, "campanha não encontrada"},

	"distribution_overrides_station_id_fkey": {CodigoEntradaInvalida, "emissora inexistente"},

	"campaign_materials_material_id_fkey": {CodigoNaoEncontrado, "material não encontrado"},
	"campaigns_client_id_fkey":            {CodigoNaoEncontrado, "cliente não encontrado"},
}

// checksConhecidos: constraint CHECK → mensagem em português.
var checksConhecidos = map[string]string{
	"distribution_rules_weekday_mask_check":       "a máscara de dias precisa estar entre 0 e 127",
	"distribution_rules_plays_per_day_check":      "as inserções por dia precisam estar entre 1 e 100",
	"distribution_overrides_plays_expected_check": "as inserções do dia não podem ser negativas",
	"rule_dates_valid":                            "a data final não pode ser anterior à inicial",
	"campaign_dates_valid":                        "a data final não pode ser anterior à inicial",
	"rule_times_valid":                            "a hora final precisa ser maior que a inicial",
	"override_times_valid":                        "a hora final precisa ser maior que a inicial",
}
