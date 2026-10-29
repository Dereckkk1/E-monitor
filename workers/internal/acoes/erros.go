package acoes

import (
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// recusaDoBanco traduz o erro do Postgres que é de ENTRADA (e não do sistema)
// numa *Recusa. Existe porque as rotas antigas devolviam 500 para um tipo de
// material inexistente ou uma faixa horária invertida — o CHECK/FK do banco
// disparava e o handler só sabia dizer "internal error". Aqui a pessoa (ou o
// modelo) recebe o motivo. Qualquer outro erro volta como veio (falha interna).
//
// A validação em Go vem ANTES e dá a mensagem boa; isto é a rede para o que ela
// não prevê (e para a corrida entre a checagem e o INSERT).
func recusaDoBanco(err error) error {
	var pg *pgconn.PgError
	if !errors.As(err, &pg) {
		return err
	}
	switch pg.Code {
	case "23503": // foreign_key_violation
		c := pg.ConstraintName
		switch {
		case strings.Contains(c, "type_id"):
			return &Recusa{Codigo: CodigoEntradaInvalida, Mensagem: "tipo de material inexistente"}
		case strings.Contains(c, "campaign_id"):
			return &Recusa{Codigo: CodigoNaoEncontrado, Mensagem: "campanha não encontrada"}
		case strings.Contains(c, "station_id"):
			return &Recusa{Codigo: CodigoEntradaInvalida, Mensagem: "emissora inexistente"}
		}
	case "23514": // check_violation
		return &Recusa{Codigo: CodigoEntradaInvalida, Mensagem: "valor fora do permitido (" + pg.ConstraintName + ")"}
	case "22007", "22008", "22023", "22P02": // formato/estouro de data ou hora, parâmetro inválido (ex.: "meio-dia"::time)
		return &Recusa{Codigo: CodigoEntradaInvalida, Mensagem: "data ou horário inválido"}
	}
	return err
}
