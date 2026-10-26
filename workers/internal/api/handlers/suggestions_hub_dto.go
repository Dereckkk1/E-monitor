package handlers

import (
	"time"

	"radiocheck/internal/hub"
)

/*
O JSON que a tela de sugestões daqui já consome, montado a partir do que o hub
devolve. Os nomes são os de `catalog.Suggestion` (ref_num, dev_feedback…), para o
front não reescrever leitura nenhuma; os campos novos são aditivos.

⚠️ Não há `dev_notes` aqui, nem como campo. A nota privada é do gestor e mora no
hub; a porta não a manda, e este tipo não teria onde pô-la.
*/

type sugestaoHubDTO struct {
	ID                string     `json:"id"`
	RefNum            int        `json:"ref_num"`
	NumeroOriginal    *int       `json:"numero_original,omitempty"`
	Origin            string     `json:"origin"`
	CreatedBy         string     `json:"created_by"`
	CreatedByName     string     `json:"created_by_name"`
	Title             string     `json:"title"`
	Description       string     `json:"description"`
	Type              string     `json:"type"`
	TargetScreen      *string    `json:"target_screen,omitempty"`
	RequesterPriority string     `json:"requester_priority"`
	Status            string     `json:"status"`
	DevPriority       *string    `json:"dev_priority,omitempty"`
	Effort            *string    `json:"effort,omitempty"`
	DevFeedback       *string    `json:"dev_feedback,omitempty"`
	AwaitingAuthor    bool       `json:"awaiting_author"`
	ResolvedAt        *time.Time `json:"resolved_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	CommentCount      int        `json:"comment_count"`
	Unread            bool       `json:"unread"`
}

type comentarioHubDTO struct {
	ID           string    `json:"id"`
	SuggestionID string    `json:"suggestion_id"`
	AuthorID     string    `json:"author_id"`
	AuthorName   string    `json:"author_name"`
	Body         string    `json:"body"`
	FromDev      bool      `json:"from_dev"`
	CreatedAt    time.Time `json:"created_at"`
}

// anexoHubDTO: `kind` = "arquivo" (imagem ou outro arquivo) ou "link".
type anexoHubDTO struct {
	ID           string    `json:"id"`
	SuggestionID string    `json:"suggestion_id"`
	CommentID    *string   `json:"comment_id,omitempty"`
	Kind         string    `json:"kind"`
	ContentType  string    `json:"content_type,omitempty"`
	SizeBytes    int64     `json:"size_bytes,omitempty"`
	Filename     string    `json:"filename,omitempty"`
	LinkURL      string    `json:"link_url,omitempty"`
	Label        string    `json:"label,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type eventoHubDTO struct {
	ID           string    `json:"id"`
	SuggestionID string    `json:"suggestion_id"`
	EventType    string    `json:"event_type"`
	ActorName    *string   `json:"actor_name,omitempty"`
	FromValue    *string   `json:"from_value,omitempty"`
	ToValue      *string   `json:"to_value,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type detalheHubDTO struct {
	sugestaoHubDTO
	Comments    []comentarioHubDTO `json:"comments"`
	Attachments []anexoHubDTO      `json:"attachments"`
	Events      []eventoHubDTO     `json:"events"`
}

func texto(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func linhaDoHub(l hub.SugestaoLinhaHub) sugestaoHubDTO {
	return sugestaoHubDTO{
		ID: l.ID, RefNum: l.Numero, Origin: l.Origem,
		CreatedBy: l.Autor.ID, CreatedByName: l.Autor.Nome,
		Title: l.Titulo, Description: l.Previa, Type: hub.TipoDoHub(l.Tipo),
		RequesterPriority: l.UrgenciaAutor, Status: l.Status, DevPriority: l.Prioridade,
		AwaitingAuthor: l.SuaVez, CreatedAt: l.CreatedAt, UpdatedAt: l.UltimaAtividadeEm,
		CommentCount: l.Comentarios, Unread: l.NaoLida,
	}
}

func detalheDoHub(s *hub.SugestaoHub) detalheHubDTO {
	d := detalheHubDTO{
		sugestaoHubDTO: sugestaoHubDTO{
			ID: s.ID, RefNum: s.Numero, NumeroOriginal: s.NumeroOriginal, Origin: s.Origem,
			CreatedBy: s.Autor.ID, CreatedByName: s.Autor.Nome,
			Title: s.Titulo, Description: s.Descricao, Type: hub.TipoDoHub(s.Tipo),
			TargetScreen: s.TelaAlvo, RequesterPriority: s.UrgenciaAutor, Status: s.Status,
			DevPriority: s.Prioridade, Effort: s.Esforco, DevFeedback: s.Resposta,
			AwaitingAuthor: s.SuaVez, ResolvedAt: s.ResolvidaEm,
			CreatedAt: s.CreatedAt, UpdatedAt: s.UltimaAtividadeEm,
			CommentCount: len(s.Comentarios),
		},
		Comments:    make([]comentarioHubDTO, 0, len(s.Comentarios)),
		Attachments: make([]anexoHubDTO, 0, len(s.Anexos)),
		Events:      make([]eventoHubDTO, 0, len(s.Eventos)),
	}
	for _, c := range s.Comentarios {
		d.Comments = append(d.Comments, comentarioHubDTO{
			ID: c.ID, SuggestionID: s.ID, AuthorID: c.Autor.ID, AuthorName: c.Autor.Nome,
			Body: c.Corpo, FromDev: c.DoGestor, CreatedAt: c.CreatedAt,
		})
	}
	for _, a := range s.Anexos {
		x := anexoHubDTO{ID: a.ID, SuggestionID: s.ID, CommentID: a.ComentarioID, CreatedAt: a.CreatedAt}
		if a.Tipo == "link" {
			x.Kind, x.LinkURL, x.Label = "link", texto(a.URL), texto(a.Rotulo)
		} else {
			x.Kind, x.ContentType, x.Filename = "arquivo", texto(a.MimeType), texto(a.Nome)
			if a.Tamanho != nil {
				x.SizeBytes = *a.Tamanho
			}
		}
		d.Attachments = append(d.Attachments, x)
	}
	for _, e := range s.Eventos {
		x := eventoHubDTO{ID: e.ID, SuggestionID: s.ID, EventType: hub.EventoDoHub(e.Tipo),
			FromValue: e.De, ToValue: e.Para, CreatedAt: e.CreatedAt}
		if e.Ator != nil {
			nome := e.Ator.Nome
			x.ActorName = &nome
		}
		d.Events = append(d.Events, x)
	}
	return d
}
