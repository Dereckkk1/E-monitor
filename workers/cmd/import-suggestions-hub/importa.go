package main

import (
	"context"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/google/uuid"

	"radiocheck/internal/catalog"
	"radiocheck/internal/hub"
)

// Quem escreveu e foi apagado daqui (`created_by` NULL) entra no hub com um
// autor marcador: a porta exige um e-mail, e "ninguém" não é e-mail.
//
// ⚠️ `.invalid`, nunca um domínio real: o hub suprime todo aviso para e-mail
// `.invalid` (hub 21713a74). Com `@e-monitor.online`, a resposta do gestor
// sairia por e-mail para uma caixa que alguém pode ter.
const (
	autorRemovidoEmail = "autor-removido@e-monitor.invalid"
	autorRemovidoNome  = "Pessoa removida"
)

// Os tetos do hub (`LIMITES` do vocabulário de lá). Mais longo que isso o hub
// recusaria a sugestão inteira; cortar e marcar com "…" perde menos.
const (
	limiteTitulo = 180
	limiteTexto  = 8000
	// O maior anexo que o hub aceita (arquivo, 25 MB). As imagens daqui têm 10.
	limiteAnexo = 25 << 20
)

type leitorDeObjetos interface {
	Get(ctx context.Context, key string) (io.ReadCloser, string, int64, error)
}

type portaDoHub interface {
	SubirAnexoSugestao(ctx context.Context, a hub.Autor, nome, mime string, dados []byte) (string, error)
	ImportarSugestao(ctx context.Context, a hub.Autor, s hub.SugestaoImportada) (*hub.RespostaImportar, error)
}

func autorDe(email, nome *string) hub.Autor {
	if email == nil || strings.TrimSpace(*email) == "" {
		return hub.Autor{Email: autorRemovidoEmail, Nome: autorRemovidoNome}
	}
	n := ""
	if nome != nil {
		n = strings.TrimSpace(*nome)
	}
	if n == "" {
		n = *email
	}
	return hub.Autor{Email: *email, Nome: n}
}

func corta(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

func lerObjeto(ctx context.Context, o leitorDeObjetos, key string) ([]byte, error) {
	corpo, _, _, err := o.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer corpo.Close()
	dados, err := io.ReadAll(io.LimitReader(corpo, limiteAnexo+1))
	if err != nil {
		return nil, err
	}
	if len(dados) > limiteAnexo {
		return nil, fmt.Errorf("maior que %d MB", limiteAnexo>>20)
	}
	return dados, nil
}

func mesmaPessoa(a, b hub.Autor) bool {
	return strings.EqualFold(strings.TrimSpace(a.Email), strings.TrimSpace(b.Email))
}

/*
importaUma sobe os anexos e manda a sugestão.

⚠️ Todo anexo sobe com os headers de QUEM O ENVIOU (`uploaded_by`), nunca de
outra pessoa: atribuir o print do dev ao autor falsificaria o registro. O
anexo vai para onde aquela pessoa é dona — o topo, se foi o autor da sugestão;
a fala, se foi quem falou. Quando não é dono de nada ali (o dev anexou direto na
sugestão de alguém), o anexo vira uma FALA SINTÉTICA sem texto, daquela pessoa,
na data do anexo, com `idOriginal` estável `anexo-<id>` (o hub aceita fala vazia
com anexo, e a reimportação não a duplica).

Idempotente do lado do hub. Os anexos de uma reimportação que dá `ja-importada`
ficam "sem dono" lá até o `limpaUploadsOrfaos` (24 h); o arquivo de estado do
`main.go` evita subi-los à toa.
*/
func importaUma(ctx context.Context, porta portaDoHub, objetos leitorDeObjetos, s catalog.SuggestionExport) (*hub.RespostaImportar, error) {
	autor := autorDe(s.CreatedByEmail, s.CreatedByName)
	autorDaFala := map[uuid.UUID]hub.Autor{}
	for _, c := range s.Comments {
		autorDaFala[c.ID] = autorDe(c.AuthorEmail, c.AuthorName)
	}

	raiz := []string{}
	porFala := map[uuid.UUID][]string{}
	var sinteticas []hub.ComentarioImportado
	for _, a := range s.Attachments {
		quemEnviou := autorDe(a.UploaderEmail, a.UploaderName)
		dados, err := lerObjeto(ctx, objetos, a.StorageKey)
		if err != nil {
			return nil, fmt.Errorf("anexo %s (%s): %w", a.ID, a.StorageKey, err)
		}
		id, err := porta.SubirAnexoSugestao(ctx, quemEnviou, path.Base(a.StorageKey), a.ContentType, dados)
		if err != nil {
			return nil, fmt.Errorf("anexo %s: %w", a.ID, err)
		}
		if a.CommentID != nil {
			if dono, ok := autorDaFala[*a.CommentID]; ok && mesmaPessoa(dono, quemEnviou) {
				porFala[*a.CommentID] = append(porFala[*a.CommentID], id)
				continue
			}
		} else if mesmaPessoa(autor, quemEnviou) {
			raiz = append(raiz, id)
			continue
		}
		// Fala inexistente, ou anexo de quem não é dono do lugar: fala sintética.
		// Um anexo cuja fala sumiu mas que é do autor vai para o topo.
		if a.CommentID != nil && mesmaPessoa(autor, quemEnviou) {
			if _, existe := autorDaFala[*a.CommentID]; !existe {
				raiz = append(raiz, id)
				continue
			}
		}
		sinteticas = append(sinteticas, hub.ComentarioImportado{
			IDOriginal: "anexo-" + a.ID.String(),
			Autor:      hub.AutorJSON{Email: quemEnviou.Email, Nome: quemEnviou.Nome},
			Corpo:      "", CreatedAt: a.CreatedAt, AnexoIDs: []string{id},
		})
	}

	falas := make([]hub.ComentarioImportado, 0, len(s.Comments)+len(sinteticas))
	for _, c := range s.Comments {
		q := autorDaFala[c.ID]
		ids := porFala[c.ID]
		if ids == nil {
			ids = []string{}
		}
		falas = append(falas, hub.ComentarioImportado{
			IDOriginal: c.ID.String(), Autor: hub.AutorJSON{Email: q.Email, Nome: q.Nome},
			Corpo: corta(c.Body, limiteTexto), CreatedAt: c.CreatedAt, AnexoIDs: ids,
		})
	}
	falas = append(falas, sinteticas...)
	// A conversa do hub é lida em ordem de data: a sintética entra onde o anexo
	// foi feito, não no fim.
	sort.SliceStable(falas, func(i, j int) bool { return falas[i].CreatedAt.Before(falas[j].CreatedAt) })

	return porta.ImportarSugestao(ctx, autor, hub.SugestaoImportada{
		IDOriginal: s.ID.String(), NumeroOriginal: int(s.RefNum),
		Tipo: hub.TipoParaHub(s.Type), Titulo: corta(s.Title, limiteTitulo), Descricao: corta(s.Description, limiteTexto),
		TelaAlvo: s.TargetScreen, UrgenciaAutor: s.RequesterPriority, Status: s.Status,
		Prioridade: s.DevPriority, Esforco: s.Effort, Resposta: s.DevFeedback, Notas: s.DevNotes,
		SuaVez: s.AwaitingAuthor, ResolvidaEm: s.ResolvedAt, CreatedAt: s.CreatedAt,
		AnexoIDs: raiz, Comentarios: falas,
	})
}
