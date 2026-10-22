package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
	// ErrLigacaoConcorrente: outra ligação para o mesmo código do hub venceu a corrida.
	ErrLigacaoConcorrente = errors.New("outra ligação para o mesmo código do hub foi gravada ao mesmo tempo")
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
		 WHERE c.contype = 'f' AND c.confrelid = 'clients'::regclass
		   AND c.conparentid = 0 AND array_length(c.conkey, 1) = 1`)
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
		`SELECT id, name, COALESCE(hub_id, ''), COALESCE(cnpj, '') FROM clients WHERE `+where+` LIMIT 1`, args...).
		Scan(&r.ID, &r.Nome, &r.HubID, &r.CNPJ)
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
gravado (texto livre do lado de lá — pode ser vazio ou lixo), `cnpjNoHub` é o
CNPJ do dono do código como o hub o tem (com ou sem máscara, ou vazio).

Devolve `pgx.ErrNoRows` se o escolhido não existe.

Lê SEM snapshot compartilhado de propósito: é consultivo, e `LigarLocal`
reconfere o duplicado dentro da transação.
*/
func (l *Ligacoes) Fatos(ctx context.Context, escolhido uuid.UUID, hubClientID, idNaPlataforma, cnpjNoHub string) (*FatosDaLigacao, error) {
	c, err := resumoDoCliente(ctx, l.pool, `id = $1`, escolhido)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, pgx.ErrNoRows
	}
	f := &FatosDaLigacao{Escolhido: *c, CNPJNoHub: cnpjNoHub}

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

/*
LigarLocal grava `hub_id` = dono do código no cliente escolhido, tirando-o
antes de um duplicado VAZIO que o tenha. Devolve `mudou=false` quando já estava
ligado (idempotente: o botão pode ser apertado de novo depois de uma queda do
hub no meio).

⚠️ A ORDEM dos dois UPDATEs é obrigatória: `clients_hub_id_key` é único, então
gravar no escolhido antes de soltar o duplicado estoura o índice.

⚠️ `FOR UPDATE` nas duas linhas: dois cliques simultâneos se enfileiram, e o
segundo encontra o escolhido já ligado.

⚠️ É `FOR UPDATE` (e NÃO `FOR NO KEY UPDATE`) no duplicado de propósito: só ele
conflita com o `FOR KEY SHARE` que um INSERT com FK para o cliente toma, então
ninguém pendura cadastro no detentor entre a checagem de "vazio" e o UPDATE.
Não "otimize".
*/
func (l *Ligacoes) LigarLocal(ctx context.Context, escolhido uuid.UUID, hubClientID string, ator *uuid.UUID) (bool, error) {
	if hubClientID == "" {
		return false, errors.New("hubClientID vazio")
	}
	tx, err := l.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var atual *string
	if err := tx.QueryRow(ctx, `SELECT hub_id FROM clients WHERE id = $1 FOR UPDATE`, escolhido).Scan(&atual); err != nil {
		return false, err
	}
	if atual != nil && *atual == hubClientID {
		return false, nil
	}
	if atual != nil && *atual != "" {
		return false, ErrEscolhidoDeOutro
	}

	var detentor *uuid.UUID
	var d uuid.UUID
	err = tx.QueryRow(ctx,
		`SELECT id FROM clients WHERE hub_id = $1 AND id <> $2 FOR UPDATE`, hubClientID, escolhido).Scan(&d)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return false, err
	default:
		vazio, err := clienteVazio(ctx, tx, d)
		if err != nil {
			return false, err
		}
		if !vazio {
			return false, ErrDetentorEmUso
		}
		if _, err := tx.Exec(ctx, `UPDATE clients SET hub_id = NULL, updated_at = NOW() WHERE id = $1`, d); err != nil {
			return false, err
		}
		detentor = &d
	}

	if _, err := tx.Exec(ctx, `UPDATE clients SET hub_id = $1, updated_at = NOW() WHERE id = $2`, hubClientID, escolhido); err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" && pe.ConstraintName == "clients_hub_id_key" {
			return false, ErrLigacaoConcorrente
		}
		return false, err
	}

	meta, _ := json.Marshal(map[string]any{"hub_id": hubClientID, "de": detentor})
	tipo := "user"
	if ator == nil {
		tipo = "system"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_log (actor_id, actor_type, action, resource, resource_id, metadata)
		VALUES ($1, $2, 'client.hub_link', 'client', $3, $4)`, ator, tipo, escolhido, meta); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
