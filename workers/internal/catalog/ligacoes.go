package catalog

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

/*
Ligacoes lê e grava a ponte `clients.hub_id` para o botão "Ligar os dois
clientes" (spec do hub 2026-10-22 §4.1).
*/
type Ligacoes struct {
	pool *pgxpool.Pool
}

func NewLigacoes(pool *pgxpool.Pool) *Ligacoes { return &Ligacoes{pool: pool} }

var (
	ErrDetentorEmUso    = errors.New("o cliente que tem a ligação tem cadastros pendurados")
	ErrEscolhidoDeOutro = errors.New("o cliente escolhido já está ligado a outro cliente do hub")
)

// consulta é o que o pool e a transação têm em comum: `ClienteVazio` roda nos dois.
type consulta interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

/*
ClienteVazio diz se NENHUMA linha de NENHUMA tabela aponta para o cliente.

⚠️ As tabelas vêm do `pg_constraint`, não de uma lista escrita aqui. Uma lista
fica velha na próxima migration e passa a dizer "vazio" para um cliente que não
está — e "vazio" é o que autoriza tirar a ligação dele. É a mesma consulta que
foi rodada à mão na VM em 2026-10-22.
*/
func (l *Ligacoes) ClienteVazio(ctx context.Context, id uuid.UUID) (bool, error) {
	return clienteVazio(ctx, l.pool, id)
}

func clienteVazio(ctx context.Context, q consulta, id uuid.UUID) (bool, error) {
	rows, err := q.Query(ctx, `
		SELECT c.conrelid::regclass::text, a.attname
		  FROM pg_constraint c
		  JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)
		 WHERE c.contype = 'f' AND c.confrelid = 'clients'::regclass`)
	if err != nil {
		return false, err
	}
	type fk struct{ tabela, coluna string }
	var fks []fk
	for rows.Next() {
		var f fk
		if err := rows.Scan(&f.tabela, &f.coluna); err != nil {
			rows.Close()
			return false, err
		}
		fks = append(fks, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}
	// Lidas TODAS antes de consultar de novo: numa transação a conexão é uma
	// só, e o pgx não aceita outra consulta com linhas abertas.
	for _, f := range fks {
		var existe bool
		sql := fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s WHERE %s = $1)`,
			f.tabela, pgx.Identifier{f.coluna}.Sanitize())
		if err := q.QueryRow(ctx, sql, id).Scan(&existe); err != nil {
			return false, err
		}
		if existe {
			return false, nil
		}
	}
	return true, nil
}

func resumoDoCliente(ctx context.Context, q consulta, where string, args ...any) (*ClienteResumo, error) {
	var r ClienteResumo
	err := q.QueryRow(ctx,
		`SELECT id, name, COALESCE(hub_id, '') FROM clients WHERE `+where+` LIMIT 1`, args...).
		Scan(&r.ID, &r.Nome, &r.HubID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

/*
Fatos junta o que o banco daqui sabe com o que o hub respondeu na conferência:
`hubClientID` é o dono do código, `idNaPlataforma` é o id daqui que o hub tem
gravado (texto livre do lado de lá — pode ser vazio ou lixo).

Devolve `pgx.ErrNoRows` se o escolhido não existe.
*/
func (l *Ligacoes) Fatos(ctx context.Context, escolhido uuid.UUID, hubClientID, idNaPlataforma string) (*FatosDaLigacao, error) {
	c, err := resumoDoCliente(ctx, l.pool, `id = $1`, escolhido)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, pgx.ErrNoRows
	}
	f := &FatosDaLigacao{Escolhido: *c}

	if hubClientID != "" {
		d, err := resumoDoCliente(ctx, l.pool, `hub_id = $1 AND id <> $2`, hubClientID, escolhido)
		if err != nil {
			return nil, err
		}
		if d != nil {
			f.Detentor = d
			if f.DetentorVazio, err = clienteVazio(ctx, l.pool, d.ID); err != nil {
				return nil, err
			}
		}
	}

	doHub, perr := uuid.Parse(strings.TrimSpace(idNaPlataforma))
	switch {
	case perr != nil:
		f.HubSemPonte = true
	case doHub == escolhido:
		f.HubApontaEscolhido = true
	default:
		x, err := resumoDoCliente(ctx, l.pool, `id = $1`, doHub)
		if err != nil {
			return nil, err
		}
		if x == nil {
			f.HubSemPonte = true
		} else {
			f.ApontadoPeloHub = x
			if f.ApontadoVazio, err = clienteVazio(ctx, l.pool, x.ID); err != nil {
				return nil, err
			}
		}
	}
	return f, nil
}
