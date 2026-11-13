package catalog

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSuggestions_ExportAll_TrazTudoQueAImportacaoPrecisa(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewSuggestions(pool)
	author := insertSuggestionTestUser(t, ctx, pool)
	sug := newTestSuggestion(t, ctx, repo, author)

	notas := "nota privada"
	_, err := repo.Update(ctx, sug.ID, UpdateSuggestionInput{ActorID: author, DevNotes: &notas})
	require.NoError(t, err)
	c, err := repo.AddComment(ctx, sug.ID, author, "primeira fala")
	require.NoError(t, err)
	_, err = repo.AddAttachment(ctx, AddAttachmentInput{SuggestionID: sug.ID, CommentID: &c.ID,
		StorageKey: "suggestions/x/a.png", ContentType: "image/png", SizeBytes: 10, UploadedBy: author})
	require.NoError(t, err)

	all, err := repo.ExportAll(ctx)
	require.NoError(t, err)
	var achada *SuggestionExport
	for i := range all {
		if all[i].ID == sug.ID {
			achada = &all[i]
		}
	}
	require.NotNil(t, achada)
	require.Equal(t, "nota privada", *achada.DevNotes)
	require.NotNil(t, achada.CreatedByEmail)
	require.Len(t, achada.Comments, 1)
	require.Equal(t, "primeira fala", achada.Comments[0].Body)
	require.Equal(t, *achada.CreatedByEmail, *achada.Comments[0].AuthorEmail)
	require.Len(t, achada.Attachments, 1)
	require.Equal(t, c.ID, *achada.Attachments[0].CommentID)
	require.Equal(t, "suggestions/x/a.png", achada.Attachments[0].StorageKey)
	// Quem ENVIOU o anexo: a importação não pode atribuí-lo a outra pessoa.
	require.Equal(t, *achada.CreatedByEmail, *achada.Attachments[0].UploaderEmail)
	require.False(t, achada.Attachments[0].CreatedAt.IsZero())
}
