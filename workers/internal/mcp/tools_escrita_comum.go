package mcp

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"radiocheck/internal/acoes"
)

// Helpers das tools de escrita (Lote C). As recusas de negócio são
// *acoes.Recusa: o motor as devolve como "<mensagem> [<código>]" e, no nível
// confirma, libera o token (F3-5). Validar a entrada aqui, em português e antes
// de chamar o acoes, é regra: algumas mensagens movidas para o acoes ainda são
// inglesas por paridade com a rota HTTP.

// errEntrada é a recusa entrada_invalida.
func errEntrada(mensagem string) error {
	return &acoes.Recusa{Codigo: acoes.CodigoEntradaInvalida, Mensagem: mensagem}
}

// errNaoEncontrado é a recusa nao_encontrado ("<o quê>: não existe no E-monitor").
func errNaoEncontrado(oQue string) error {
	return &acoes.Recusa{Codigo: acoes.CodigoNaoEncontrado, Mensagem: fmt.Sprintf("%s: não existe no E-monitor", oQue)}
}

// codigoSemMudanca: o pedido não mudaria nada (o valor pedido já é o gravado).
// As tools críticas recusam em vez de abrir um pedido de aprovação vazio.
const codigoSemMudanca = "sem_mudanca"

// errRecusa é uma recusa com código próprio (nome_duplicado, cnpj_duplicado…).
func errRecusa(codigo, mensagem string) error {
	return &acoes.Recusa{Codigo: codigo, Mensagem: mensagem}
}

// uuidDaEntrada lê um id da entrada; vazio ou fora do formato é entrada_invalida,
// com a mesma mensagem das tools de leitura.
func uuidDaEntrada(campo, valor string) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(valor))
	if err != nil {
		return uuid.Nil, errEntrada(fmt.Sprintf("%s inválido: passe o valor exato que outra tool devolveu (nunca invente; pergunte ao usuário)", campo))
	}
	return id, nil
}

// uuidsDaEntrada lê uma lista de ids, sem repetição, na ordem em que vieram.
func uuidsDaEntrada(campo string, valores []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(valores))
	vistos := make(map[uuid.UUID]bool, len(valores))
	for _, v := range valores {
		id, err := uuidDaEntrada(campo, v)
		if err != nil {
			return nil, err
		}
		if !vistos[id] {
			vistos[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

// aparar tira os espaços das pontas.
func aparar(s string) string { return strings.TrimSpace(s) }

// limiteDeRunas recusa texto acima de n runas (contado em runas, não em bytes:
// acento não pode contar dobrado).
func limiteDeRunas(campo, s string, n int) error {
	if utf8.RuneCountInString(s) > n {
		return errEntrada(fmt.Sprintf("%s: no máximo %d caracteres", campo, n))
	}
	return nil
}

// recorte corta um texto para o preview (o roteiro inteiro não vai para a resposta).
func recorte(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

// violacaoDeUnicidade diz se o erro é de chave única (SQLSTATE 23505): a corrida
// entre a checagem do preview e o INSERT.
func violacaoDeUnicidade(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

// ptrTexto devolve nil para "" (o NULL do banco).
func ptrTexto(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// igualTexto compara dois textos opcionais tratando nil e "" como o mesmo vazio.
func igualTexto(a *string, b string) bool {
	if a == nil {
		return b == ""
	}
	return *a == b
}

// textoOuNil: o valor de um texto opcional para mostrar no preview (nil = vazio).
func textoOuNil(p *string) any {
	if p == nil || *p == "" {
		return nil
	}
	return *p
}
