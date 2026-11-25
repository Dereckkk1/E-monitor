package catalog

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func covCSug(t *testing.T, repo *Suggestions, author uuid.UUID, in CreateSuggestionInput) *Suggestion {
	t.Helper()
	in.CreatedBy = author
	s, err := repo.Create(context.Background(), in)
	require.NoError(t, err)
	return s
}

func TestCovC_Suggestions_CreateTargetScreenOpcional(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewSuggestions(pool)
	author := insertSuggestionTestUser(t, ctx, pool)

	// Tela-alvo em branco (ou só espaços) vira NULL.
	semTela := covCSug(t, repo, author, CreateSuggestionInput{Title: "Sem tela", Description: "d", Type: "melhoria",
		TargetScreen: "   ", RequesterPriority: "media"})
	assert.Nil(t, semTela.TargetScreen)
	comTela := covCSug(t, repo, author, CreateSuggestionInput{Title: "Com tela", Description: "d", Type: "bug",
		TargetScreen: "/insights", RequesterPriority: "alta"})
	require.NotNil(t, comTela.TargetScreen)
	assert.Equal(t, "/insights", *comTela.TargetScreen)
	assert.Greater(t, comTela.RefNum, semTela.RefNum, "ref_num cresce")

	// Tipo inválido viola o CHECK e a transação inteira volta (sem evento órfão).
	_, err := repo.Create(ctx, CreateSuggestionInput{CreatedBy: author, Title: "x", Description: "d",
		Type: "tipo-que-nao-existe", RequesterPriority: "media"})
	require.Error(t, err)

	// Autor inexistente viola a FK.
	_, err = repo.Create(ctx, CreateSuggestionInput{CreatedBy: uuid.New(), Title: "x", Description: "d",
		Type: "bug", RequesterPriority: "media"})
	require.Error(t, err)
}

func TestCovC_Suggestions_Get(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewSuggestions(pool)
	author := insertSuggestionTestUser(t, ctx, pool)
	sug := newTestSuggestion(t, ctx, repo, author)

	got, err := repo.Get(ctx, sug.ID)
	require.NoError(t, err)
	assert.Equal(t, sug.ID, got.ID)
	require.NotNil(t, got.CreatedByEmail)
	require.NotNil(t, got.CreatedByName)
	assert.Equal(t, "nova", got.Status)

	_, err = repo.Get(ctx, uuid.New())
	require.ErrorIs(t, err, pgx.ErrNoRows)
}

func TestCovC_Suggestions_List_FiltrosOrdenacaoEUnread(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewSuggestions(pool)
	autorA := insertSuggestionTestUser(t, ctx, pool)
	autorB := insertSuggestionTestUser(t, ctx, pool)
	dev := insertSuggestionTestUser(t, ctx, pool)

	bug := covCSug(t, repo, autorA, CreateSuggestionInput{Title: "Botão QUEBRADO no mobile", Description: "descrição um",
		Type: "bug", RequesterPriority: "alta"})
	melhoria := covCSug(t, repo, autorA, CreateSuggestionInput{Title: "Exportar relatório", Description: "Precisa de CSV exportável",
		Type: "melhoria", RequesterPriority: "baixa"})
	deB := covCSug(t, repo, autorB, CreateSuggestionInput{Title: "Ideia do outro autor", Description: "d",
		Type: "melhoria", RequesterPriority: "media"})

	ids := func(l []Suggestion) []uuid.UUID {
		out := make([]uuid.UUID, 0, len(l))
		for _, s := range l {
			out = append(out, s.ID)
		}
		return out
	}
	scope := func(f ListSuggestionsFilter) []Suggestion {
		f.ViewerID = dev
		f.ViewerIsDev = true
		l, err := repo.List(ctx, f)
		require.NoError(t, err)
		// isola as linhas deste teste (o banco compartilhado tem outras sugestões)
		var mine []Suggestion
		for _, s := range l {
			if s.ID == bug.ID || s.ID == melhoria.ID || s.ID == deB.ID {
				mine = append(mine, s)
			}
		}
		return mine
	}

	assert.ElementsMatch(t, []uuid.UUID{bug.ID, melhoria.ID, deB.ID}, ids(scope(ListSuggestionsFilter{})))
	assert.ElementsMatch(t, []uuid.UUID{bug.ID}, ids(scope(ListSuggestionsFilter{Type: "bug"})))
	assert.ElementsMatch(t, []uuid.UUID{melhoria.ID, deB.ID}, ids(scope(ListSuggestionsFilter{Type: "melhoria"})))
	// Filtro de autor do dev; OnlyAuthorID (escopo duro do não-dev) tem precedência.
	assert.ElementsMatch(t, []uuid.UUID{deB.ID}, ids(scope(ListSuggestionsFilter{AuthorID: &autorB})))
	assert.ElementsMatch(t, []uuid.UUID{bug.ID, melhoria.ID},
		ids(scope(ListSuggestionsFilter{OnlyAuthorID: &autorA, AuthorID: &autorB})))
	// Busca textual (caixa-insensitiva) em título E descrição.
	assert.ElementsMatch(t, []uuid.UUID{bug.ID}, ids(scope(ListSuggestionsFilter{Query: "quebrado"})))
	assert.ElementsMatch(t, []uuid.UUID{melhoria.ID}, ids(scope(ListSuggestionsFilter{Query: "CSV"})))

	// Prioridade do dev + status.
	alta, andamento := "alta", "em_progresso"
	_, err := repo.Update(ctx, bug.ID, UpdateSuggestionInput{ActorID: dev, DevPriority: &alta, Status: &andamento})
	require.NoError(t, err)
	assert.ElementsMatch(t, []uuid.UUID{bug.ID}, ids(scope(ListSuggestionsFilter{Priority: "alta"})))
	assert.ElementsMatch(t, []uuid.UUID{bug.ID}, ids(scope(ListSuggestionsFilter{Status: "em_progresso"})))
	assert.ElementsMatch(t, []uuid.UUID{melhoria.ID, deB.ID}, ids(scope(ListSuggestionsFilter{Type: "melhoria", Status: "nova"})))

	// Ordenações.
	byRef := scope(ListSuggestionsFilter{Sort: "ref_num"})
	require.Len(t, byRef, 3)
	assert.Equal(t, []uuid.UUID{deB.ID, melhoria.ID, bug.ID}, ids(byRef), "ref_num desc")
	oldest := scope(ListSuggestionsFilter{Sort: "oldest"})
	assert.Equal(t, []uuid.UUID{bug.ID, melhoria.ID, deB.ID}, ids(oldest))
	newest := scope(ListSuggestionsFilter{Sort: "created_at"})
	assert.Equal(t, []uuid.UUID{deB.ID, melhoria.ID, bug.ID}, ids(newest))
	byUpdated := scope(ListSuggestionsFilter{})
	assert.Equal(t, bug.ID, byUpdated[0].ID, "default = mais recentemente atualizada primeiro")

	// Unread: o dev vê atividade nova (qualquer evento/comentário); depois de ler, zera.
	for _, s := range scope(ListSuggestionsFilter{}) {
		assert.True(t, s.Unread, "dev nunca leu: todas têm atividade (evento 'created') não lida")
	}
	require.NoError(t, repo.MarkRead(ctx, dev, bug.ID))
	for _, s := range scope(ListSuggestionsFilter{}) {
		if s.ID == bug.ID {
			assert.False(t, s.Unread)
			assert.GreaterOrEqual(t, s.CommentCount, 0)
		}
	}
	// Autor: só atividade de OUTROS conta. A mudança de status do dev é atividade nova pra ele.
	l, err := repo.List(ctx, ListSuggestionsFilter{OnlyAuthorID: &autorA, ViewerID: autorA})
	require.NoError(t, err)
	var autorSaw bool
	for _, s := range l {
		if s.ID == bug.ID {
			autorSaw = true
			assert.True(t, s.Unread, "o dev mexeu na sugestão do autor")
			require.NotNil(t, s.CreatedByEmail)
		}
		if s.ID == melhoria.ID {
			assert.False(t, s.Unread, "só o evento 'created' do próprio autor")
		}
	}
	assert.True(t, autorSaw)

	// Comentário do dev em ID alheio conta no CommentCount.
	_, err = repo.AddComment(ctx, melhoria.ID, dev, "Vamos priorizar.")
	require.NoError(t, err)
	for _, s := range scope(ListSuggestionsFilter{Type: "melhoria"}) {
		if s.ID == melhoria.ID {
			assert.Equal(t, 1, s.CommentCount)
		}
	}
}

func TestCovC_Suggestions_Update_EventosETransicoes(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewSuggestions(pool)
	author := insertSuggestionTestUser(t, ctx, pool)
	dev := insertSuggestionTestUser(t, ctx, pool)
	sug := newTestSuggestion(t, ctx, repo, author)
	ptr := func(s string) *string { return &s }
	yes := true

	_, err := repo.Update(ctx, uuid.New(), UpdateSuggestionInput{ActorID: dev, Status: ptr("em_analise")})
	require.ErrorIs(t, err, pgx.ErrNoRows)

	// Campos livres: effort, notas privadas e "aguardando autor".
	got, err := repo.Update(ctx, sug.ID, UpdateSuggestionInput{ActorID: dev,
		Effort: ptr("M"), DevNotes: ptr("nota interna"), AwaitingAuthor: &yes, DevPriority: ptr("alta")})
	require.NoError(t, err)
	require.NotNil(t, got.Effort)
	assert.Equal(t, "M", *got.Effort)
	assert.Equal(t, "nota interna", *got.DevNotes)
	assert.True(t, got.AwaitingAuthor)
	require.NotNil(t, got.DevPriority)

	// Resolver (concluida) carimba resolved_at; recusar também conta como resolvido.
	got, err = repo.Update(ctx, sug.ID, UpdateSuggestionInput{ActorID: dev, Status: ptr("concluida")})
	require.NoError(t, err)
	require.NotNil(t, got.ResolvedAt)
	got, err = repo.Update(ctx, sug.ID, UpdateSuggestionInput{ActorID: dev, Status: ptr("recusada")})
	require.NoError(t, err)
	require.NotNil(t, got.ResolvedAt, "resolvida → resolvida mantém o carimbo")

	// Reabrir limpa resolved_at e gera o evento 'reopened'.
	got, err = repo.Update(ctx, sug.ID, UpdateSuggestionInput{ActorID: dev, Status: ptr("em_analise")})
	require.NoError(t, err)
	assert.Nil(t, got.ResolvedAt)

	// Reenviar o MESMO status/prioridade/feedback não gera evento novo.
	before, err := repo.ListEvents(ctx, sug.ID)
	require.NoError(t, err)
	_, err = repo.Update(ctx, sug.ID, UpdateSuggestionInput{ActorID: dev, Status: ptr("em_analise"), DevPriority: ptr("alta")})
	require.NoError(t, err)
	after, err := repo.ListEvents(ctx, sug.ID)
	require.NoError(t, err)
	assert.Len(t, after, len(before), "mesmo valor não vira evento")

	// Feedback: só vira evento se mudou E não é vazio.
	_, err = repo.Update(ctx, sug.ID, UpdateSuggestionInput{ActorID: dev, DevFeedback: ptr("   ")})
	require.NoError(t, err)
	_, err = repo.Update(ctx, sug.ID, UpdateSuggestionInput{ActorID: dev, DevFeedback: ptr("Resposta ao autor")})
	require.NoError(t, err)
	events, err := repo.ListEvents(ctx, sug.ID)
	require.NoError(t, err)
	kinds := map[string]int{}
	for _, e := range events {
		kinds[e.EventType]++
		if e.EventType != "created" {
			require.NotNil(t, e.ActorName, "o dev que agiu aparece na linha do tempo")
		}
	}
	assert.Equal(t, 1, kinds["created"])
	assert.Equal(t, 1, kinds["reopened"])
	assert.Equal(t, 1, kinds["feedback_given"])
	assert.Equal(t, 1, kinds["priority_changed"])
	assert.GreaterOrEqual(t, kinds["status_changed"], 2)

	// Status inválido viola o CHECK e a transação volta sem efeito colateral.
	bad := "status-invalido"
	_, err = repo.Update(ctx, sug.ID, UpdateSuggestionInput{ActorID: dev, Status: &bad})
	require.Error(t, err)
	cur, err := repo.Get(ctx, sug.ID)
	require.NoError(t, err)
	assert.Equal(t, "em_analise", cur.Status)
}

func TestCovC_Suggestions_AnexosEComentarios(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewSuggestions(pool)
	author := insertSuggestionTestUser(t, ctx, pool)
	sug := newTestSuggestion(t, ctx, repo, author)

	_, err := repo.GetAttachment(ctx, uuid.New())
	require.ErrorIs(t, err, pgx.ErrNoRows)
	empty, err := repo.ListAttachments(ctx, sug.ID)
	require.NoError(t, err)
	assert.Empty(t, empty)

	root, err := repo.AddAttachment(ctx, AddAttachmentInput{SuggestionID: sug.ID,
		StorageKey: "suggestions/" + sug.ID.String() + "/raiz.png", ContentType: "image/png", SizeBytes: 2048, UploadedBy: author})
	require.NoError(t, err)
	assert.Nil(t, root.CommentID)
	cmt, err := repo.AddComment(ctx, sug.ID, author, "veja o print")
	require.NoError(t, err)
	onComment, err := repo.AddAttachment(ctx, AddAttachmentInput{SuggestionID: sug.ID, CommentID: &cmt.ID,
		StorageKey: "suggestions/" + sug.ID.String() + "/comentario.jpg", ContentType: "image/jpeg", SizeBytes: 512, UploadedBy: author})
	require.NoError(t, err)
	require.NotNil(t, onComment.CommentID)
	assert.Equal(t, cmt.ID, *onComment.CommentID)

	list, err := repo.ListAttachments(ctx, sug.ID)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, root.ID, list[0].ID, "mais antigo primeiro")
	assert.EqualValues(t, 2048, list[0].SizeBytes)

	got, err := repo.GetAttachment(ctx, onComment.ID)
	require.NoError(t, err)
	assert.Equal(t, "image/jpeg", got.ContentType)

	// O anexo gera o evento attachment_added na linha do tempo.
	events, err := repo.ListEvents(ctx, sug.ID)
	require.NoError(t, err)
	n := 0
	for _, e := range events {
		if e.EventType == "attachment_added" {
			n++
		}
	}
	assert.Equal(t, 2, n)

	// Sugestão inexistente: FK impede o anexo e não deixa evento órfão.
	_, err = repo.AddAttachment(ctx, AddAttachmentInput{SuggestionID: uuid.New(), StorageKey: "k", ContentType: "image/png", SizeBytes: 1, UploadedBy: author})
	require.Error(t, err)

	// Thread: o nome do autor vem do JOIN.
	comments, err := repo.ListComments(ctx, sug.ID)
	require.NoError(t, err)
	require.Len(t, comments, 1)
	require.NotNil(t, comments[0].AuthorName)
}

func TestCovC_Suggestions_Summary(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewSuggestions(pool)
	author := insertSuggestionTestUser(t, ctx, pool)
	dev := insertSuggestionTestUser(t, ctx, pool)
	ptr := func(s string) *string { return &s }

	before, err := repo.Summary(ctx)
	require.NoError(t, err)

	a := newTestSuggestion(t, ctx, repo, author)
	b := newTestSuggestion(t, ctx, repo, author)
	c := newTestSuggestion(t, ctx, repo, author)
	_, err = repo.Update(ctx, b.ID, UpdateSuggestionInput{ActorID: dev, Status: ptr("concluida")})
	require.NoError(t, err)
	_, err = repo.Update(ctx, c.ID, UpdateSuggestionInput{ActorID: dev, Status: ptr("recusada")})
	require.NoError(t, err)
	_ = a

	after, err := repo.Summary(ctx)
	require.NoError(t, err)
	assert.Equal(t, before.Total+3, after.Total)
	assert.Equal(t, before.ByStatus["nova"]+1, after.ByStatus["nova"])
	assert.Equal(t, before.ByStatus["concluida"]+1, after.ByStatus["concluida"])
	assert.Equal(t, before.ByStatus["recusada"]+1, after.ByStatus["recusada"])
	assert.Equal(t, before.ResolvedThisMonth+2, after.ResolvedThisMonth, "concluída e recusada resolvidas neste mês")
	require.NotNil(t, after.OldestOpen, "há ao menos uma aberta")
	assert.False(t, after.OldestOpen.After(a.CreatedAt), "a mais antiga em aberto não é mais nova que a minha aberta")
}

func TestCovC_Suggestions_UnreadCount_Dev(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewSuggestions(pool)
	author := insertSuggestionTestUser(t, ctx, pool)
	dev := insertSuggestionTestUser(t, ctx, pool)

	before, err := repo.UnreadCount(ctx, dev, true)
	require.NoError(t, err)
	s1 := newTestSuggestion(t, ctx, repo, author)
	s2 := newTestSuggestion(t, ctx, repo, author)
	after, err := repo.UnreadCount(ctx, dev, true)
	require.NoError(t, err)
	assert.Equal(t, before+2, after, "para o dev, nunca lida = não lida (qualquer atividade)")

	require.NoError(t, repo.MarkRead(ctx, dev, s1.ID))
	require.NoError(t, repo.MarkRead(ctx, dev, s1.ID), "MarkRead é upsert idempotente")
	afterRead, err := repo.UnreadCount(ctx, dev, true)
	require.NoError(t, err)
	assert.Equal(t, after-1, afterRead)

	// Atividade nova em s1 depois da leitura volta a contar.
	_, err = repo.AddComment(ctx, s1.ID, author, "novidade")
	require.NoError(t, err)
	again, err := repo.UnreadCount(ctx, dev, true)
	require.NoError(t, err)
	assert.Equal(t, after, again)
	_ = s2
}

func TestCovC_Suggestions_ErroDeBancoPropaga(t *testing.T) {
	_, pool := newTestDB(t)
	repo := NewSuggestions(pool)
	ctx := covCCancelled()
	id := uuid.New()
	_, err := repo.Create(ctx, CreateSuggestionInput{CreatedBy: id})
	require.Error(t, err)
	_, err = repo.List(ctx, ListSuggestionsFilter{})
	require.Error(t, err)
	_, err = repo.Get(ctx, id)
	require.Error(t, err)
	_, err = repo.Update(ctx, id, UpdateSuggestionInput{})
	require.Error(t, err)
	_, err = repo.ListComments(ctx, id)
	require.Error(t, err)
	_, err = repo.AddComment(ctx, id, id, "x")
	require.Error(t, err)
	_, err = repo.ListAttachments(ctx, id)
	require.Error(t, err)
	_, err = repo.AddAttachment(ctx, AddAttachmentInput{SuggestionID: id})
	require.Error(t, err)
	_, err = repo.GetAttachment(ctx, id)
	require.Error(t, err)
	_, err = repo.ListEvents(ctx, id)
	require.Error(t, err)
	require.Error(t, repo.MarkRead(ctx, id, id))
	_, err = repo.Summary(ctx)
	require.Error(t, err)
	_, err = repo.UnreadCount(ctx, id, true)
	require.Error(t, err)
	_, err = repo.UnreadCount(ctx, id, false)
	require.Error(t, err)
	_, err = repo.ExportAll(ctx)
	require.Error(t, err)
}
