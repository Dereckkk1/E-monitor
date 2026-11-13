// Package auditoria grava no audit_log (migration 0009) as ações feitas pelo
// MCP ou sobre ele, marcadas com origem "mcp" —
// é assim que uma escrita da IA se distingue de uma feita à mão (contrato §5) —
// e as lê de volta (Listar, para a tool auditoria_mcp).
package auditoria

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Execer é o mínimo que Registrar precisa: *pgxpool.Pool e pgx.Tx servem.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func Registrar(ctx context.Context, db Execer, ator uuid.UUID, acao, recurso string, recursoID *uuid.UUID, meta map[string]any) error {
	m := copiar(meta)
	m["origem"] = "mcp" // depois da cópia: quem chama não sobrescreve
	_, err := db.Exec(ctx,
		`INSERT INTO audit_log (actor_id, actor_type, action, resource, resource_id, metadata)
		 VALUES ($1, 'user', $2, $3, $4, $5)`,
		ator, acao, recurso, recursoID, m)
	return err
}

// RegistrarSistema grava uma ação feita por um CLI na VM, sem usuário do
// portal: actor_id NULL, actor_type 'cli', e metadata.pelo diz qual comando
// foi. pelo e origem são gravados depois da cópia: o meta não os sobrescreve.
func RegistrarSistema(ctx context.Context, db Execer, pelo, acao, recurso string, recursoID *uuid.UUID, meta map[string]any) error {
	m := copiar(meta)
	m["pelo"] = pelo
	m["origem"] = "mcp"
	_, err := db.Exec(ctx,
		`INSERT INTO audit_log (actor_id, actor_type, action, resource, resource_id, metadata)
		 VALUES (NULL, 'cli', $1, $2, $3, $4)`,
		acao, recurso, recursoID, m)
	return err
}

func copiar(meta map[string]any) map[string]any {
	m := make(map[string]any, len(meta)+2)
	for k, v := range meta {
		m[k] = v
	}
	return m
}

// ── Leitura (auditoria_mcp) ─────────────────────────────────────────────

// Consultor é o que Listar precisa: *pgxpool.Pool e pgx.Tx servem.
type Consultor interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// ErrCursorInvalido: o cursor não veio de um Listar anterior.
var ErrCursorInvalido = errors.New("auditoria: cursor inválido")

type Filtro struct {
	AcaoPrefixo string     // "" = todas; "mcp.token" casa mcp.token.* (literal: % e _ não são curinga)
	AtorID      *uuid.UUID // nil = todos
	Cursor      string     // "" = do mais recente
	Limite      int        // > 0 (quem chama aplica o teto)
}

type Evento struct {
	ID        uuid.UUID
	Quando    time.Time
	AtorID    *uuid.UUID
	AtorNome  *string
	AtorEmail *string
	TipoAtor  string
	Acao      string
	Recurso   string
	RecursoID *uuid.UUID
	Detalhes  map[string]any // metadata sem a chave "origem"
}

// Repo amarra Listar a uma conexão (a Leitura do MCP guarda um).
type Repo struct{ db Consultor }

func NovoRepo(db Consultor) *Repo { return &Repo{db: db} }

func (r *Repo) Listar(ctx context.Context, f Filtro) ([]Evento, string, error) {
	return Listar(ctx, r.db, f)
}

// escaparLike torna o prefixo literal no LIKE (com ESCAPE '\' explícito no SQL).
var escaparLike = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// chaveSecreta: chave de metadata cujo valor nunca sai de Listar, em qualquer
// nível (hoje nenhum escritor grava segredo; é a defesa para o próximo). O
// casamento é por substring e de propósito largo: chaves inofensivas como
// token_id, chave_pix ou session_count também saem omitidas — perder um id é
// barato, vazar uma credencial não.
var chaveSecreta = regexp.MustCompile(`(?i)secret|segredo|token|senha|password|chave|credencial|credential|` +
	`api[_-]?key|apikey|authorization|bearer|cookie|jwt|private|verifier|session`)

// OmitirSegredos aplica a máscara de chaveSecreta a v (in-place) e devolve o
// próprio v. Exportada para quem GRAVA: a escrita do MCP mascara os args no
// momento da gravação, para o audit_log nunca guardar o valor (a máscara da
// leitura, em Listar, continua valendo por cima).
func OmitirSegredos(v any) any {
	omitirSegredos(v)
	return v
}

// omitirSegredos troca por "[omitido]" o valor de toda chave que parece segredo,
// descendo em objetos e listas aninhados.
func omitirSegredos(v any) {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if chaveSecreta.MatchString(k) {
				x[k] = "[omitido]"
				continue
			}
			omitirSegredos(val)
		}
	case []any:
		for _, val := range x {
			omitirSegredos(val)
		}
	}
}

// Listar devolve os eventos gravados pelo MCP (metadata.origem = 'mcp'), do
// mais recente para o mais antigo, por cursor (created_at, id). proximo vazio
// = fim. O id (uuid) só desempata instantes iguais; o cursor guarda os dois,
// então um empate nunca repete nem pula linha entre páginas. Os detalhes saem
// sem a chave "origem" e com os valores de chave de segredo omitidos.
//
// Risco conhecido (M-6), não tratado: created_at é o now() do INÍCIO da
// transação que gravou. Uma transação longa que confirme depois de alguém ter
// paginado além do instante dela fica com created_at anterior ao cursor, e
// esse leitor não a vê. Hoje toda gravação no audit_log é um INSERT curto.
func Listar(ctx context.Context, db Consultor, f Filtro) (eventos []Evento, proximo string, err error) {
	if f.Limite <= 0 {
		return nil, "", errors.New("auditoria: Listar exige Limite > 0")
	}
	var antesDe *time.Time
	var antesID *uuid.UUID
	if f.Cursor != "" {
		t, id, ok := lerCursor(f.Cursor)
		if !ok {
			return nil, "", ErrCursorInvalido
		}
		antesDe, antesID = &t, &id
	}
	rows, err := db.Query(ctx, `
		SELECT a.id, a.created_at, a.actor_id, u.name, u.email, a.actor_type, a.action, a.resource, a.resource_id, a.metadata
		FROM audit_log a
		LEFT JOIN users u ON u.id = a.actor_id
		WHERE a.metadata->>'origem' = 'mcp'
		  AND ($1::text = '' OR a.action LIKE $1::text || '%' ESCAPE '\')
		  AND ($2::uuid IS NULL OR a.actor_id = $2::uuid)
		  AND ($3::timestamptz IS NULL OR (a.created_at, a.id) < ($3::timestamptz, $4::uuid))
		ORDER BY a.created_at DESC, a.id DESC
		LIMIT $5`, escaparLike.Replace(f.AcaoPrefixo), f.AtorID, antesDe, antesID, f.Limite+1)
	if err != nil {
		return nil, "", fmt.Errorf("auditoria: listar: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var e Evento
		var meta []byte
		if err := rows.Scan(&e.ID, &e.Quando, &e.AtorID, &e.AtorNome, &e.AtorEmail, &e.TipoAtor, &e.Acao, &e.Recurso, &e.RecursoID, &meta); err != nil {
			return nil, "", err
		}
		// metadata aqui é sempre objeto: o filtro metadata->>'origem' devolve
		// NULL para array/escalar/null, que nem é selecionado (teste
		// TestListar_MetadataNaoObjetoNaoDerrubaAPagina).
		if len(meta) > 0 {
			if err := json.Unmarshal(meta, &e.Detalhes); err != nil {
				return nil, "", fmt.Errorf("auditoria: metadata do evento %s: %w", e.ID, err)
			}
			delete(e.Detalhes, "origem")
			omitirSegredos(e.Detalhes)
		}
		eventos = append(eventos, e)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	if len(eventos) > f.Limite {
		eventos = eventos[:f.Limite]
		u := eventos[len(eventos)-1]
		proximo = base64.RawURLEncoding.EncodeToString([]byte(u.Quando.UTC().Format(time.RFC3339Nano) + "|" + u.ID.String()))
	}
	return eventos, proximo, nil
}

// lerCursor desfaz o cursor opaco de Listar. O Postgres guarda microssegundos
// e o RFC3339Nano os preserva: o < da tupla compara exato.
func lerCursor(s string) (time.Time, uuid.UUID, bool) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, uuid.Nil, false
	}
	partes := strings.SplitN(string(b), "|", 2)
	if len(partes) != 2 {
		return time.Time{}, uuid.Nil, false
	}
	t, err1 := time.Parse(time.RFC3339Nano, partes[0])
	id, err2 := uuid.Parse(partes[1])
	return t, id, err1 == nil && err2 == nil
}
