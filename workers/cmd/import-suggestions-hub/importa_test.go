package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/catalog"
	"radiocheck/internal/hub"
)

type objetosFalsos map[string][]byte

func (o objetosFalsos) Get(_ context.Context, key string) (io.ReadCloser, string, int64, error) {
	b := o[key]
	return io.NopCloser(bytes.NewReader(b)), "image/png", int64(len(b)), nil
}

type subida struct {
	autor hub.Autor
	nome  string
}

type portaFalsa struct {
	subidas   []subida
	importada *hub.SugestaoImportada
	autor     hub.Autor
}

func (p *portaFalsa) SubirAnexoSugestao(_ context.Context, a hub.Autor, nome, _ string, _ []byte) (string, error) {
	p.subidas = append(p.subidas, subida{a, nome})
	return "an" + string(rune('0'+len(p.subidas))), nil
}

func (p *portaFalsa) ImportarSugestao(_ context.Context, a hub.Autor, s hub.SugestaoImportada) (*hub.RespostaImportar, error) {
	p.autor, p.importada = a, &s
	return &hub.RespostaImportar{Acao: "importada", Numero: 99}, nil
}

func s(v string) *string { return &v }

func TestImportaUma(t *testing.T) {
	cID := uuid.New()
	quando := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	sug := catalog.SuggestionExport{
		Suggestion: catalog.Suggestion{
			ID: uuid.New(), RefNum: 17, Title: strings.Repeat("t", 200), Description: "D", Type: "feature",
			RequesterPriority: "alta", Status: "concluida", DevNotes: s("privado"), DevFeedback: s("feito"),
			CreatedAt: quando, CreatedByEmail: s("ana@t.local"), CreatedByName: s("Ana"),
		},
		Comments: []catalog.SuggestionCommentExport{{ID: cID, Body: "fala do dev", CreatedAt: quando,
			AuthorEmail: s("tatico3@hubradios.com"), AuthorName: s("Dereck")}},
		Attachments: []catalog.SuggestionAttachmentExport{
			{ID: uuid.New(), StorageKey: "suggestions/a/raiz.png", ContentType: "image/png", UploaderEmail: s("ANA@t.local")},
			{ID: uuid.New(), CommentID: &cID, StorageKey: "suggestions/a/fala.png", ContentType: "image/png", UploaderEmail: s("tatico3@hubradios.com")},
		},
	}
	p := &portaFalsa{}
	r, err := importaUma(context.Background(), p, objetosFalsos{"suggestions/a/raiz.png": []byte("1"), "suggestions/a/fala.png": []byte("2")}, sug)

	require.NoError(t, err)
	require.Equal(t, 99, r.Numero)
	require.Equal(t, "ana@t.local", p.autor.Email)
	// Cada anexo sobe com os headers de QUEM O ENVIOU (o hub só amarra anexo de
	// quem o enviou, e atribuir a outra pessoa falsificaria o registro).
	require.Equal(t, "ANA@t.local", p.subidas[0].autor.Email)
	require.Equal(t, "raiz.png", p.subidas[0].nome)
	require.Equal(t, "tatico3@hubradios.com", p.subidas[1].autor.Email)
	imp := p.importada
	require.Equal(t, "ideia", imp.Tipo)
	require.Equal(t, 17, imp.NumeroOriginal)
	require.Equal(t, sug.ID.String(), imp.IDOriginal)
	require.Equal(t, "privado", *imp.Notas)
	require.Equal(t, "feito", *imp.Resposta)
	require.Equal(t, []string{"an1"}, imp.AnexoIDs)
	require.Len(t, imp.Comentarios, 1)
	require.Equal(t, []string{"an2"}, imp.Comentarios[0].AnexoIDs)
	require.Equal(t, cID.String(), imp.Comentarios[0].IDOriginal)
	require.Equal(t, "Dereck", imp.Comentarios[0].Autor.Nome)
	require.Len(t, []rune(imp.Titulo), 180)
	require.True(t, strings.HasSuffix(imp.Titulo, "…"))
}

// O dev anexou um print direto na sugestão de outra pessoa: NÃO vai para o topo
// em nome do autor. Vira uma fala sintética, sem texto, de quem enviou.
func TestImportaUma_AnexoDeOutraPessoaNaRaizViraFalaSintetica(t *testing.T) {
	aID := uuid.New()
	quando := time.Date(2026, 7, 21, 9, 0, 0, 0, time.UTC)
	p := &portaFalsa{}
	_, err := importaUma(context.Background(), p, objetosFalsos{"k": []byte("1")}, catalog.SuggestionExport{
		Suggestion: catalog.Suggestion{ID: uuid.New(), Type: "bug", Status: "nova", CreatedAt: quando.Add(-time.Hour),
			CreatedByEmail: s("ana@t.local"), CreatedByName: s("Ana")},
		Attachments: []catalog.SuggestionAttachmentExport{{ID: aID, StorageKey: "k", ContentType: "image/png",
			CreatedAt: quando, UploaderEmail: s("tatico3@hubradios.com"), UploaderName: s("Dereck")}},
	})
	require.NoError(t, err)
	require.Equal(t, "tatico3@hubradios.com", p.subidas[0].autor.Email)
	require.Empty(t, p.importada.AnexoIDs)
	require.Len(t, p.importada.Comentarios, 1)
	f := p.importada.Comentarios[0]
	require.Equal(t, "anexo-"+aID.String(), f.IDOriginal)
	require.Equal(t, "tatico3@hubradios.com", f.Autor.Email)
	require.Equal(t, "", f.Corpo)
	require.Equal(t, quando, f.CreatedAt)
	require.Equal(t, []string{"an1"}, f.AnexoIDs)
}

func TestImportaUma_FalasSaemEmOrdemDeData(t *testing.T) {
	cID := uuid.New()
	t0 := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	p := &portaFalsa{}
	_, err := importaUma(context.Background(), p, objetosFalsos{"k": []byte("1")}, catalog.SuggestionExport{
		Suggestion: catalog.Suggestion{ID: uuid.New(), Type: "bug", Status: "nova", CreatedByEmail: s("ana@t.local")},
		Comments:   []catalog.SuggestionCommentExport{{ID: cID, Body: "depois", CreatedAt: t0.Add(2 * time.Hour), AuthorEmail: s("ana@t.local")}},
		Attachments: []catalog.SuggestionAttachmentExport{{ID: uuid.New(), StorageKey: "k", ContentType: "image/png",
			CreatedAt: t0.Add(time.Hour), UploaderEmail: s("tatico3@hubradios.com")}},
	})
	require.NoError(t, err)
	require.Len(t, p.importada.Comentarios, 2)
	require.Equal(t, "", p.importada.Comentarios[0].Corpo) // a sintética, 1h
	require.Equal(t, "depois", p.importada.Comentarios[1].Corpo)
}

func TestImportaUma_AutorApagadoViraMarcador(t *testing.T) {
	p := &portaFalsa{}
	_, err := importaUma(context.Background(), p, objetosFalsos{}, catalog.SuggestionExport{
		Suggestion: catalog.Suggestion{ID: uuid.New(), Type: "bug", Status: "nova"},
	})
	require.NoError(t, err)
	require.Equal(t, "autor-removido@e-monitor.invalid", p.autor.Email)
	require.Equal(t, "Pessoa removida", p.autor.Nome)
}

func TestImportaUma_AnexoDeFalaInexistenteVaiParaARaiz(t *testing.T) {
	fantasma := uuid.New()
	p := &portaFalsa{}
	_, err := importaUma(context.Background(), p, objetosFalsos{"k": []byte("1")}, catalog.SuggestionExport{
		Suggestion: catalog.Suggestion{ID: uuid.New(), Type: "bug", Status: "nova", CreatedByEmail: s("ana@t.local")},
		Attachments: []catalog.SuggestionAttachmentExport{{ID: uuid.New(), CommentID: &fantasma, StorageKey: "k",
			ContentType: "image/png", UploaderEmail: s("ana@t.local")}},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"an1"}, p.importada.AnexoIDs)
}
