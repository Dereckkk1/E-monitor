package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
)

/*
ExportAll lê o módulo de sugestões INTEIRO para o `import-suggestions-hub`
(spec do hub 2026-10-26 §5): toda sugestão, com a nota privada, o e-mail e o nome
de quem escreveu cada coisa, e onde cada anexo mora no bucket.

Só a importação chama isto. As telas leem do hub desde 2026-10-26.
*/

type SuggestionExport struct {
	Suggestion
	Comments    []SuggestionCommentExport
	Attachments []SuggestionAttachmentExport
}

type SuggestionCommentExport struct {
	ID          uuid.UUID
	Body        string
	CreatedAt   time.Time
	AuthorEmail *string
	AuthorName  *string
}

// UploaderEmail/UploaderName: quem ENVIOU (nil se a pessoa foi apagada). A
// importação usa isso para nunca atribuir o anexo a outra pessoa.
type SuggestionAttachmentExport struct {
	ID            uuid.UUID
	CommentID     *uuid.UUID
	StorageKey    string
	ContentType   string
	SizeBytes     int64
	CreatedAt     time.Time
	UploaderEmail *string
	UploaderName  *string
}

func (s *Suggestions) ExportAll(ctx context.Context) ([]SuggestionExport, error) {
	// u.name ANTES de u.email: é a ordem do scanSuggestionNamed.
	rows, err := s.pool.Query(ctx, `
		SELECT `+suggestionSelectCols+`, u.name, u.email
		FROM suggestions s
		LEFT JOIN users u ON u.id = s.created_by
		ORDER BY s.ref_num ASC`)
	if err != nil {
		return nil, err
	}
	var out []SuggestionExport
	pos := map[uuid.UUID]int{}
	for rows.Next() {
		var e SuggestionExport
		if err := scanSuggestionNamed(rows, &e.Suggestion); err != nil {
			rows.Close()
			return nil, err
		}
		pos[e.ID] = len(out)
		out = append(out, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	crows, err := s.pool.Query(ctx, `
		SELECT c.id, c.suggestion_id, c.body, c.created_at, u.email, u.name
		FROM suggestion_comments c
		LEFT JOIN users u ON u.id = c.author_id
		ORDER BY c.created_at ASC, c.id ASC`)
	if err != nil {
		return nil, err
	}
	for crows.Next() {
		var c SuggestionCommentExport
		var sid uuid.UUID
		if err := crows.Scan(&c.ID, &sid, &c.Body, &c.CreatedAt, &c.AuthorEmail, &c.AuthorName); err != nil {
			crows.Close()
			return nil, err
		}
		if i, ok := pos[sid]; ok {
			out[i].Comments = append(out[i].Comments, c)
		}
	}
	crows.Close()
	if err := crows.Err(); err != nil {
		return nil, err
	}

	arows, err := s.pool.Query(ctx, `
		SELECT a.id, a.suggestion_id, a.comment_id, a.storage_key, a.content_type, a.size_bytes,
		       a.created_at, u.email, u.name
		FROM suggestion_attachments a
		LEFT JOIN users u ON u.id = a.uploaded_by
		ORDER BY a.created_at ASC, a.id ASC`)
	if err != nil {
		return nil, err
	}
	defer arows.Close()
	for arows.Next() {
		var a SuggestionAttachmentExport
		var sid uuid.UUID
		if err := arows.Scan(&a.ID, &sid, &a.CommentID, &a.StorageKey, &a.ContentType, &a.SizeBytes,
			&a.CreatedAt, &a.UploaderEmail, &a.UploaderName); err != nil {
			return nil, err
		}
		if i, ok := pos[sid]; ok {
			out[i].Attachments = append(out[i].Attachments, a)
		}
	}
	return out, arows.Err()
}
