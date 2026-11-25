package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/catalog"
	"radiocheck/internal/storage"
	"radiocheck/internal/users"
)

type covBSug struct {
	ctx    context.Context
	pool   *pgxpool.Pool
	h      *SuggestionsHandler
	s3     *covBS3
	dev    uuid.UUID
	author uuid.UUID
	other  uuid.UUID
}

// covBSugSetup monta o handler com S3 falso e três usuários (dev, autor, outro).
func covBSugSetup(t *testing.T) *covBSug {
	t.Helper()
	ctx, pool := covBDB(t)
	cli, fake := covBNewS3(t)
	s := &covBSug{ctx: ctx, pool: pool, s3: fake}
	s.h = &SuggestionsHandler{
		Repo:     catalog.NewSuggestions(pool),
		Users:    users.NewRepo(pool),
		Storage:  cli,
		DevEmail: suggestionsTestDevEmail,
	}
	s.dev = mkUser(t, ctx, pool, suggestionsTestDevEmail)
	s.author = mkUser(t, ctx, pool, "autor@example.com")
	s.other = mkUser(t, ctx, pool, "outro@example.com")
	return s
}

func (s *covBSug) newSug(t *testing.T, by uuid.UUID, title, typ, prio string) *catalog.Suggestion {
	t.Helper()
	sug, err := s.h.Repo.Create(s.ctx, catalog.CreateSuggestionInput{
		CreatedBy: by, Title: title, Description: "descrição " + title, Type: typ, RequesterPriority: prio,
	})
	require.NoError(t, err)
	return sug
}

func TestCovB_Suggestions_Create(t *testing.T) {
	s := covBSugSetup(t)

	t.Run("sem claims 401", func(t *testing.T) {
		rec := covBServe(s.h.Create, covBReq("POST", "/suggestions", `{}`, nil, nil))
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})
	t.Run("json inválido 400", func(t *testing.T) {
		rec := covBServe(s.h.Create, asCaller(covBReq("POST", "/suggestions", `{"title":`, nil, nil), s.author))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
	t.Run("campos faltando 422 lista cada erro", func(t *testing.T) {
		rec := covBServe(s.h.Create, asCaller(covBReq("POST", "/suggestions",
			`{"title":"  ","description":"","type":"xx","requester_priority":"urgentissima"}`, nil, nil), s.author))
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
		errs, ok := covBJSON(t, rec)["errors"].(map[string]any)
		require.True(t, ok)
		for _, k := range []string{"title", "description", "type", "requester_priority"} {
			assert.Contains(t, errs, k)
		}
	})
	t.Run("sucesso apara espaços e fixa status nova", func(t *testing.T) {
		rec := covBServe(s.h.Create, asCaller(covBReq("POST", "/suggestions",
			`{"title":"  Botão quebrado ","description":" ao clicar ","type":"bug","target_screen":" /insights ","requester_priority":"alta"}`,
			nil, nil), s.author))
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		m := covBJSON(t, rec)
		assert.Equal(t, "Botão quebrado", m["title"])
		assert.Equal(t, "ao clicar", m["description"])
		assert.Equal(t, "/insights", m["target_screen"])
		assert.Equal(t, "nova", m["status"])
		assert.Equal(t, s.author.String(), m["created_by"])
	})
}

func TestCovB_Suggestions_List(t *testing.T) {
	s := covBSugSetup(t)
	a1 := s.newSug(t, s.author, "alfa", "bug", "alta")
	a2 := s.newSug(t, s.author, "beta", "feature", "baixa")
	o1 := s.newSug(t, s.other, "gama", "bug", "media")

	// dev define prioridade e anotação privada em a1.
	prio, notes, st := "urgente", "segredo do dev", "em_analise"
	_, err := s.h.Repo.Update(s.ctx, a1.ID, catalog.UpdateSuggestionInput{
		ActorID: s.dev, DevPriority: &prio, DevNotes: &notes, Status: &st,
	})
	require.NoError(t, err)

	ids := func(t *testing.T, rec *httptest.ResponseRecorder) map[string]map[string]any {
		t.Helper()
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		data, ok := covBJSON(t, rec)["data"].([]any)
		require.True(t, ok)
		out := map[string]map[string]any{}
		for _, it := range data {
			m := it.(map[string]any)
			out[m["id"].(string)] = m
		}
		return out
	}

	tests := []struct {
		name   string
		caller uuid.UUID
		query  string
		want   []uuid.UUID
	}{
		{"dev vê tudo", s.dev, "", []uuid.UUID{a1.ID, a2.ID, o1.ID}},
		{"dev filtra por tipo", s.dev, "?type=feature", []uuid.UUID{a2.ID}},
		{"dev filtra por status", s.dev, "?status=em_analise", []uuid.UUID{a1.ID}},
		{"dev filtra por prioridade do dev", s.dev, "?priority=urgente", []uuid.UUID{a1.ID}},
		{"dev filtra por autor", s.dev, "?author_id=" + s.other.String(), []uuid.UUID{o1.ID}},
		{"dev com author_id inválido ignora o filtro", s.dev, "?author_id=nao-uuid", []uuid.UUID{a1.ID, a2.ID, o1.ID}},
		{"dev busca por texto", s.dev, "?q=GAMA", []uuid.UUID{o1.ID}},
		{"autor só enxerga as próprias", s.author, "", []uuid.UUID{a1.ID, a2.ID}},
		{"autor não amplia escopo com author_id", s.author, "?author_id=" + s.other.String(), []uuid.UUID{a1.ID, a2.ID}},
		{"autor não usa filtro de status", s.author, "?status=nova", []uuid.UUID{a1.ID, a2.ID}},
		{"autor com busca", s.author, "?q=beta", []uuid.UUID{a2.ID}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := covBServe(s.h.List, asCaller(covBReq("GET", "/suggestions"+tc.query, "", nil, nil), tc.caller))
			got := ids(t, rec)
			want := map[string]bool{}
			for _, id := range tc.want {
				want[id.String()] = true
			}
			require.Len(t, got, len(want))
			for id := range want {
				assert.Contains(t, got, id)
			}
		})
	}

	t.Run("dev_notes visível ao dev e removido do autor", func(t *testing.T) {
		devView := ids(t, covBServe(s.h.List, asCaller(covBReq("GET", "/suggestions", "", nil, nil), s.dev)))
		assert.Equal(t, notes, devView[a1.ID.String()]["dev_notes"])
		authorView := ids(t, covBServe(s.h.List, asCaller(covBReq("GET", "/suggestions", "", nil, nil), s.author)))
		_, leaked := authorView[a1.ID.String()]["dev_notes"]
		assert.False(t, leaked, "nota privada do dev vazou para o autor")
	})
	t.Run("lista vazia vira array e não null", func(t *testing.T) {
		rec := covBServe(s.h.List, asCaller(covBReq("GET", "/suggestions?q=zzzz-inexistente", "", nil, nil), s.dev))
		require.Equal(t, http.StatusOK, rec.Code)
		assert.JSONEq(t, `{"data":[]}`, rec.Body.String())
	})
	t.Run("ordenação por ref_num decrescente", func(t *testing.T) {
		rec := covBServe(s.h.List, asCaller(covBReq("GET", "/suggestions?sort=ref_num", "", nil, nil), s.dev))
		require.Equal(t, http.StatusOK, rec.Code)
		data := covBJSON(t, rec)["data"].([]any)
		require.Len(t, data, 3)
		assert.EqualValues(t, o1.RefNum, data[0].(map[string]any)["ref_num"])
	})
	t.Run("usuário inexistente no banco 500", func(t *testing.T) {
		rec := covBServe(s.h.List, asCaller(covBReq("GET", "/suggestions", "", nil, nil), uuid.New()))
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
	t.Run("sem claims 500", func(t *testing.T) {
		rec := covBServe(s.h.List, covBReq("GET", "/suggestions", "", nil, nil))
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}

func TestCovB_Suggestions_Get(t *testing.T) {
	s := covBSugSetup(t)
	sug := s.newSug(t, s.author, "detalhe", "bug", "alta")

	t.Run("id inválido 400", func(t *testing.T) {
		rec := covBServe(s.h.Get, asCaller(covBReq("GET", "/s/x", "", map[string]string{"id": "xx"}, nil), s.dev))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
	t.Run("inexistente 404", func(t *testing.T) {
		rec := covBServe(s.h.Get, asCaller(covBReq("GET", "/s/x", "", map[string]string{"id": uuid.NewString()}, nil), s.dev))
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
	t.Run("usuário desconhecido 500", func(t *testing.T) {
		rec := covBServe(s.h.Get, asCaller(covBReq("GET", "/s/x", "", map[string]string{"id": sug.ID.String()}, nil), uuid.New()))
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
	t.Run("autor lê a sua; comentários e anexos vazios viram array e a timeline traz só o created", func(t *testing.T) {
		rec := covBServe(s.h.Get, asCaller(covBReq("GET", "/s/x", "", map[string]string{"id": sug.ID.String()}, nil), s.author))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		m := covBJSON(t, rec)
		assert.Equal(t, "detalhe", m["title"])
		assert.Equal(t, []any{}, m["comments"])
		assert.Equal(t, []any{}, m["attachments"])
		evs := m["events"].([]any)
		require.Len(t, evs, 1)
		assert.Equal(t, "created", evs[0].(map[string]any)["event_type"])
	})
	t.Run("traz comentários, eventos e anexo com URL presignada", func(t *testing.T) {
		_, err := s.h.Repo.AddComment(s.ctx, sug.ID, s.dev, "resposta do dev")
		require.NoError(t, err)
		st := "aceita"
		_, err = s.h.Repo.Update(s.ctx, sug.ID, catalog.UpdateSuggestionInput{ActorID: s.dev, Status: &st})
		require.NoError(t, err)
		_, err = s.h.Repo.AddAttachment(s.ctx, catalog.AddAttachmentInput{
			SuggestionID: sug.ID, StorageKey: "suggestions/" + sug.ID.String() + "/a.png",
			ContentType: "image/png", SizeBytes: 3, UploadedBy: s.author,
		})
		require.NoError(t, err)

		rec := covBServe(s.h.Get, asCaller(covBReq("GET", "/s/x", "", map[string]string{"id": sug.ID.String()}, nil), s.dev))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		m := covBJSON(t, rec)
		assert.Equal(t, "aceita", m["status"])
		require.Len(t, m["comments"], 1)
		assert.Equal(t, "resposta do dev", m["comments"].([]any)[0].(map[string]any)["body"])
		evTypes := []string{}
		for _, e := range m["events"].([]any) {
			evTypes = append(evTypes, e.(map[string]any)["event_type"].(string))
		}
		assert.Equal(t, []string{"created", "status_changed", "attachment_added"}, evTypes)
		att := m["attachments"].([]any)
		require.Len(t, att, 1)
		url, _ := att[0].(map[string]any)["url"].(string)
		assert.Contains(t, url, "covb-bucket", "o anexo deve vir com URL presignada do bucket")
	})
	t.Run("Get marca a sugestão como lida em segundo plano", func(t *testing.T) {
		unreadForDev := func() int {
			n, err := s.h.Repo.UnreadCount(s.ctx, s.dev, true)
			require.NoError(t, err)
			return n
		}
		// Os Get anteriores deste teste já disparam MarkRead assíncrono: espera assentar
		// antes de criar atividade nova, senão a leitura atrasada engoliria a novidade.
		require.Eventually(t, func() bool { return unreadForDev() == 0 }, 5*time.Second, 25*time.Millisecond)

		_, err := s.h.Repo.AddComment(s.ctx, sug.ID, s.author, "novidade depois da última leitura")
		require.NoError(t, err)
		require.Equal(t, 1, unreadForDev(), "comentário novo do autor deixa a sugestão não lida para o dev")

		rec := covBServe(s.h.Get, asCaller(covBReq("GET", "/s/x", "", map[string]string{"id": sug.ID.String()}, nil), s.dev))
		require.Equal(t, http.StatusOK, rec.Code)
		require.Eventually(t, func() bool { return unreadForDev() == 0 }, 5*time.Second, 25*time.Millisecond)
	})
}

func TestCovB_Suggestions_Patch(t *testing.T) {
	s := covBSugSetup(t)
	sug := s.newSug(t, s.author, "alvo", "melhoria", "media")
	params := map[string]string{"id": sug.ID.String()}

	t.Run("id inválido 400", func(t *testing.T) {
		rec := covBServe(s.h.Patch, asCaller(covBReq("PATCH", "/s/x", `{}`, map[string]string{"id": "x"}, nil), s.dev))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
	t.Run("usuário desconhecido 500", func(t *testing.T) {
		rec := covBServe(s.h.Patch, asCaller(covBReq("PATCH", "/s/x", `{}`, params, nil), uuid.New()))
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
	t.Run("json inválido 400", func(t *testing.T) {
		rec := covBServe(s.h.Patch, asCaller(covBReq("PATCH", "/s/x", `{"status":`, params, nil), s.dev))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
	t.Run("valores inválidos 422 por campo", func(t *testing.T) {
		rec := covBServe(s.h.Patch, asCaller(covBReq("PATCH", "/s/x",
			`{"status":"nope","dev_priority":"nope","effort":"XG"}`, params, nil), s.dev))
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
		errs := covBJSON(t, rec)["errors"].(map[string]any)
		assert.Contains(t, errs, "status")
		assert.Contains(t, errs, "dev_priority")
		assert.Contains(t, errs, "effort")
	})
	t.Run("inexistente 404", func(t *testing.T) {
		rec := covBServe(s.h.Patch, asCaller(covBReq("PATCH", "/s/x", `{"status":"aceita"}`,
			map[string]string{"id": uuid.NewString()}, nil), s.dev))
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
	t.Run("dev atualiza e concluir grava resolved_at", func(t *testing.T) {
		rec := covBServe(s.h.Patch, asCaller(covBReq("PATCH", "/s/x",
			`{"status":"concluida","dev_priority":"alta","effort":"M","dev_feedback":"feito","dev_notes":"nota","awaiting_author":true}`,
			params, nil), s.dev))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		m := covBJSON(t, rec)
		assert.Equal(t, "concluida", m["status"])
		assert.Equal(t, "alta", m["dev_priority"])
		assert.Equal(t, "M", m["effort"])
		assert.Equal(t, "feito", m["dev_feedback"])
		assert.Equal(t, true, m["awaiting_author"])
		assert.NotNil(t, m["resolved_at"])

		evs, err := s.h.Repo.ListEvents(s.ctx, sug.ID)
		require.NoError(t, err)
		types := []string{}
		for _, e := range evs {
			types = append(types, e.EventType)
		}
		assert.ElementsMatch(t, []string{"created", "status_changed", "priority_changed", "feedback_given"}, types)
	})
	t.Run("reabrir limpa resolved_at e registra reopened", func(t *testing.T) {
		rec := covBServe(s.h.Patch, asCaller(covBReq("PATCH", "/s/x", `{"status":"em_progresso"}`, params, nil), s.dev))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		m := covBJSON(t, rec)
		assert.Equal(t, "em_progresso", m["status"])
		assert.Nil(t, m["resolved_at"])
		evs, err := s.h.Repo.ListEvents(s.ctx, sug.ID)
		require.NoError(t, err)
		found := false
		for _, e := range evs {
			found = found || e.EventType == "reopened"
		}
		assert.True(t, found, "reabertura deve gerar o evento reopened")
	})
	t.Run("autor não pode editar 403", func(t *testing.T) {
		rec := covBServe(s.h.Patch, asCaller(covBReq("PATCH", "/s/x", `{"status":"aceita"}`, params, nil), s.author))
		assert.Equal(t, http.StatusForbidden, rec.Code)
	})
}

func TestCovB_Suggestions_AddComment(t *testing.T) {
	s := covBSugSetup(t)
	sug := s.newSug(t, s.author, "thread", "duvida", "baixa")
	params := map[string]string{"id": sug.ID.String()}

	t.Run("id inválido 400", func(t *testing.T) {
		rec := covBServe(s.h.AddComment, asCaller(covBReq("POST", "/s/x/c", `{"body":"oi"}`, map[string]string{"id": "x"}, nil), s.author))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
	t.Run("usuário desconhecido 500", func(t *testing.T) {
		rec := covBServe(s.h.AddComment, asCaller(covBReq("POST", "/s/x/c", `{"body":"oi"}`, params, nil), uuid.New()))
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
	t.Run("sugestão inexistente 404", func(t *testing.T) {
		rec := covBServe(s.h.AddComment, asCaller(covBReq("POST", "/s/x/c", `{"body":"oi"}`,
			map[string]string{"id": uuid.NewString()}, nil), s.author))
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
	t.Run("terceiro 403", func(t *testing.T) {
		rec := covBServe(s.h.AddComment, asCaller(covBReq("POST", "/s/x/c", `{"body":"intruso"}`, params, nil), s.other))
		assert.Equal(t, http.StatusForbidden, rec.Code)
		cs, err := s.h.Repo.ListComments(s.ctx, sug.ID)
		require.NoError(t, err)
		assert.Empty(t, cs, "comentário de terceiro não pode ser gravado")
	})
	t.Run("json inválido 400", func(t *testing.T) {
		rec := covBServe(s.h.AddComment, asCaller(covBReq("POST", "/s/x/c", `{"body":`, params, nil), s.author))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
	t.Run("mensagem vazia 422", func(t *testing.T) {
		rec := covBServe(s.h.AddComment, asCaller(covBReq("POST", "/s/x/c", `{"body":"   "}`, params, nil), s.author))
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	})
	t.Run("dev comenta", func(t *testing.T) {
		rec := covBServe(s.h.AddComment, asCaller(covBReq("POST", "/s/x/c", `{"body":"  precisamos de print "}`, params, nil), s.dev))
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		assert.Equal(t, "precisamos de print", covBJSON(t, rec)["body"])
	})
	t.Run("resposta do autor limpa awaiting_author", func(t *testing.T) {
		flag := true
		_, err := s.h.Repo.Update(s.ctx, sug.ID, catalog.UpdateSuggestionInput{ActorID: s.dev, AwaitingAuthor: &flag})
		require.NoError(t, err)

		rec := covBServe(s.h.AddComment, asCaller(covBReq("POST", "/s/x/c", `{"body":"segue o print"}`, params, nil), s.author))
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		cur, err := s.h.Repo.Get(s.ctx, sug.ID)
		require.NoError(t, err)
		assert.False(t, cur.AwaitingAuthor, "a resposta do autor devolve a bola ao dev")
	})
	t.Run("comentário do dev não limpa awaiting_author", func(t *testing.T) {
		flag := true
		_, err := s.h.Repo.Update(s.ctx, sug.ID, catalog.UpdateSuggestionInput{ActorID: s.dev, AwaitingAuthor: &flag})
		require.NoError(t, err)
		rec := covBServe(s.h.AddComment, asCaller(covBReq("POST", "/s/x/c", `{"body":"mais uma"}`, params, nil), s.dev))
		require.Equal(t, http.StatusCreated, rec.Code)
		cur, err := s.h.Repo.Get(s.ctx, sug.ID)
		require.NoError(t, err)
		assert.True(t, cur.AwaitingAuthor)
	})
}

func TestCovB_Suggestions_UploadAttachment(t *testing.T) {
	s := covBSugSetup(t)
	sug := s.newSug(t, s.author, "anexos", "bug", "alta")
	params := map[string]string{"id": sug.ID.String()}
	target := "/s/" + sug.ID.String() + "/attachments"
	png := covBFile{Field: "file", Filename: "tela.png", ContentType: "image/png", Data: covBPNG}

	t.Run("id inválido 400", func(t *testing.T) {
		req := covBMultipartReq(t, target, nil, map[string]string{"id": "x"}, covBClaims("admin", s.author), png)
		assert.Equal(t, http.StatusBadRequest, covBServe(s.h.UploadAttachment, req).Code)
	})
	t.Run("usuário desconhecido 500", func(t *testing.T) {
		req := covBMultipartReq(t, target, nil, params, covBClaims("admin", uuid.New()), png)
		assert.Equal(t, http.StatusInternalServerError, covBServe(s.h.UploadAttachment, req).Code)
	})
	t.Run("sugestão inexistente 404", func(t *testing.T) {
		req := covBMultipartReq(t, target, nil, map[string]string{"id": uuid.NewString()}, covBClaims("admin", s.author), png)
		assert.Equal(t, http.StatusNotFound, covBServe(s.h.UploadAttachment, req).Code)
	})
	t.Run("terceiro 403 e nada gravado no bucket", func(t *testing.T) {
		req := covBMultipartReq(t, target, nil, params, covBClaims("admin", s.other), png)
		assert.Equal(t, http.StatusForbidden, covBServe(s.h.UploadAttachment, req).Code)
		assert.Equal(t, 0, s.s3.count())
	})
	t.Run("corpo não multipart 400", func(t *testing.T) {
		req := covBWithCtx(httptest.NewRequest("POST", target, nil), params, covBClaims("admin", s.author))
		assert.Equal(t, http.StatusBadRequest, covBServe(s.h.UploadAttachment, req).Code)
	})
	t.Run("sem campo file 400", func(t *testing.T) {
		req := covBMultipartReq(t, target, map[string]string{"x": "y"}, params, covBClaims("admin", s.author))
		assert.Equal(t, http.StatusBadRequest, covBServe(s.h.UploadAttachment, req).Code)
	})
	t.Run("formato não suportado 415", func(t *testing.T) {
		bad := covBFile{Field: "file", Filename: "x.svg", ContentType: "image/svg+xml", Data: []byte("<svg/>")}
		req := covBMultipartReq(t, target, nil, params, covBClaims("admin", s.author), bad)
		assert.Equal(t, http.StatusUnsupportedMediaType, covBServe(s.h.UploadAttachment, req).Code)
	})
	t.Run("comment_id inválido 400", func(t *testing.T) {
		req := covBMultipartReq(t, target, map[string]string{"comment_id": "nao-uuid"}, params, covBClaims("admin", s.author), png)
		assert.Equal(t, http.StatusBadRequest, covBServe(s.h.UploadAttachment, req).Code)
	})
	t.Run("falha no bucket 500", func(t *testing.T) {
		s.s3.failPut = true
		defer func() { s.s3.failPut = false }()
		req := covBMultipartReq(t, target, nil, params, covBClaims("admin", s.author), png)
		assert.Equal(t, http.StatusInternalServerError, covBServe(s.h.UploadAttachment, req).Code)
	})
	t.Run("sem storage configurado 500", func(t *testing.T) {
		h := *s.h
		h.Storage = nil
		req := covBMultipartReq(t, target, nil, params, covBClaims("admin", s.author), png)
		assert.Equal(t, http.StatusInternalServerError, covBServe(h.UploadAttachment, req).Code)
	})
	t.Run("sucesso grava objeto, linha e devolve URL", func(t *testing.T) {
		cm, err := s.h.Repo.AddComment(s.ctx, sug.ID, s.author, "com anexo")
		require.NoError(t, err)
		req := covBMultipartReq(t, target, map[string]string{"comment_id": cm.ID.String()}, params,
			covBClaims("admin", s.author), png)
		rec := covBServe(s.h.UploadAttachment, req)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		m := covBJSON(t, rec)
		assert.Equal(t, "image/png", m["content_type"])
		assert.Equal(t, cm.ID.String(), m["comment_id"])
		assert.NotEmpty(t, m["url"])
		key, _ := m["storage_key"].(string)
		assert.Contains(t, key, "suggestions/"+sug.ID.String()+"/")
		assert.Contains(t, key, ".png")
		assert.Contains(t, s.s3.keys(), key)

		atts, err := s.h.Repo.ListAttachments(s.ctx, sug.ID)
		require.NoError(t, err)
		require.Len(t, atts, 1)
		assert.EqualValues(t, len(covBPNG), atts[0].SizeBytes)
	})
	t.Run("dev também pode anexar (jpeg com parâmetro de charset em caixa alta)", func(t *testing.T) {
		jpg := covBFile{Field: "file", Filename: "f.jpg", ContentType: "IMAGE/JPEG", Data: []byte("jpegdata")}
		req := covBMultipartReq(t, target, nil, params, covBClaims("admin", s.dev), jpg)
		rec := covBServe(s.h.UploadAttachment, req)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		assert.Contains(t, covBJSON(t, rec)["storage_key"], ".jpg")
	})
}

func TestCovB_Suggestions_AttachmentProxyAndURL(t *testing.T) {
	s := covBSugSetup(t)
	sug := s.newSug(t, s.author, "blob", "bug", "alta")
	key := "suggestions/" + sug.ID.String() + "/x.png"
	att, err := s.h.Repo.AddAttachment(s.ctx, catalog.AddAttachmentInput{
		SuggestionID: sug.ID, StorageKey: key, ContentType: "image/png", SizeBytes: int64(len(covBPNG)), UploadedBy: s.author,
	})
	require.NoError(t, err)
	s.s3.put(key, covBPNG, "image/png")
	aid := map[string]string{"aid": att.ID.String()}

	t.Run("proxy: id inválido 400", func(t *testing.T) {
		rec := covBServe(s.h.ProxyAttachment, asCaller(covBReq("GET", "/a", "", map[string]string{"aid": "x"}, nil), s.author))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
	t.Run("proxy: anexo inexistente 404", func(t *testing.T) {
		rec := covBServe(s.h.ProxyAttachment, asCaller(covBReq("GET", "/a", "", map[string]string{"aid": uuid.NewString()}, nil), s.author))
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
	t.Run("proxy: usuário desconhecido 500", func(t *testing.T) {
		rec := covBServe(s.h.ProxyAttachment, asCaller(covBReq("GET", "/a", "", aid, nil), uuid.New()))
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
	t.Run("proxy: outro autor 403", func(t *testing.T) {
		rec := covBServe(s.h.ProxyAttachment, asCaller(covBReq("GET", "/a", "", aid, nil), s.other))
		assert.Equal(t, http.StatusForbidden, rec.Code)
	})
	t.Run("proxy: dono recebe os bytes com cabeçalhos corretos", func(t *testing.T) {
		rec := covBServe(s.h.ProxyAttachment, asCaller(covBReq("GET", "/a", "", aid, nil), s.author))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, covBPNG, rec.Body.Bytes())
		assert.Equal(t, "image/png", rec.Header().Get("Content-Type"))
		assert.Equal(t, `inline; filename="`+att.ID.String()+`"`, rec.Header().Get("Content-Disposition"))
		assert.Equal(t, "private, max-age=300", rec.Header().Get("Cache-Control"))
	})
	t.Run("proxy: dev lê anexo de qualquer autor", func(t *testing.T) {
		rec := covBServe(s.h.ProxyAttachment, asCaller(covBReq("GET", "/a", "", aid, nil), s.dev))
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, covBPNG, rec.Body.Bytes())
	})
	t.Run("proxy: objeto sumiu do bucket 500", func(t *testing.T) {
		gone, err := s.h.Repo.AddAttachment(s.ctx, catalog.AddAttachmentInput{
			SuggestionID: sug.ID, StorageKey: "suggestions/" + sug.ID.String() + "/gone.png",
			ContentType: "image/png", SizeBytes: 1, UploadedBy: s.author,
		})
		require.NoError(t, err)
		rec := covBServe(s.h.ProxyAttachment, asCaller(covBReq("GET", "/a", "", map[string]string{"aid": gone.ID.String()}, nil), s.author))
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})

	t.Run("url: id inválido 400", func(t *testing.T) {
		rec := covBServe(s.h.AttachmentURL, asCaller(covBReq("GET", "/a/url", "", map[string]string{"aid": "x"}, nil), s.author))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
	t.Run("url: inexistente 404", func(t *testing.T) {
		rec := covBServe(s.h.AttachmentURL, asCaller(covBReq("GET", "/a/url", "", map[string]string{"aid": uuid.NewString()}, nil), s.author))
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
	t.Run("url: usuário desconhecido 500", func(t *testing.T) {
		rec := covBServe(s.h.AttachmentURL, asCaller(covBReq("GET", "/a/url", "", aid, nil), uuid.New()))
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
	t.Run("url: outro autor 403", func(t *testing.T) {
		rec := covBServe(s.h.AttachmentURL, asCaller(covBReq("GET", "/a/url", "", aid, nil), s.other))
		assert.Equal(t, http.StatusForbidden, rec.Code)
	})
	t.Run("url: sem storage 500", func(t *testing.T) {
		h := *s.h
		h.Storage = nil
		rec := covBServe(h.AttachmentURL, asCaller(covBReq("GET", "/a/url", "", aid, nil), s.author))
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
	t.Run("url: dono recebe URL e expiração futura em RFC3339", func(t *testing.T) {
		rec := covBServe(s.h.AttachmentURL, asCaller(covBReq("GET", "/a/url", "", aid, nil), s.author))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		m := covBJSON(t, rec)
		assert.Contains(t, m["url"], key)
		exp, err := time.Parse(time.RFC3339, m["expires_at"].(string))
		require.NoError(t, err)
		assert.True(t, exp.After(time.Now()), "a URL presignada precisa expirar no futuro")
	})
	t.Run("proxy sem storage 500", func(t *testing.T) {
		h := *s.h
		h.Storage = (*storage.Client)(nil)
		rec := covBServe(h.ProxyAttachment, asCaller(covBReq("GET", "/a", "", aid, nil), s.author))
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}

func TestCovB_Suggestions_MarkReadSummaryUnread(t *testing.T) {
	s := covBSugSetup(t)
	sug := s.newSug(t, s.author, "leitura", "bug", "alta")
	st := "aceita"
	_, err := s.h.Repo.Update(s.ctx, sug.ID, catalog.UpdateSuggestionInput{ActorID: s.dev, Status: &st})
	require.NoError(t, err)
	params := map[string]string{"id": sug.ID.String()}

	t.Run("markread: id inválido 400", func(t *testing.T) {
		rec := covBServe(s.h.MarkRead, covBReq("POST", "/s/x/read", "", map[string]string{"id": "x"}, covBClaims("admin", s.author)))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
	t.Run("markread: sem claims 401", func(t *testing.T) {
		rec := covBServe(s.h.MarkRead, covBReq("POST", "/s/x/read", "", params, nil))
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})
	t.Run("unread-count do autor reflete atividade do dev e zera após MarkRead", func(t *testing.T) {
		rec := covBServe(s.h.UnreadCount, asCaller(covBReq("GET", "/s/unread-count", "", nil, nil), s.author))
		require.Equal(t, http.StatusOK, rec.Code)
		assert.EqualValues(t, 1, covBJSON(t, rec)["count"])

		rec = covBServe(s.h.MarkRead, covBReq("POST", "/s/x/read", "", params, covBClaims("admin", s.author)))
		require.Equal(t, http.StatusNoContent, rec.Code)

		rec = covBServe(s.h.UnreadCount, asCaller(covBReq("GET", "/s/unread-count", "", nil, nil), s.author))
		require.Equal(t, http.StatusOK, rec.Code)
		assert.EqualValues(t, 0, covBJSON(t, rec)["count"])
	})
	t.Run("unread-count do dev conta a atividade ainda não lida", func(t *testing.T) {
		rec := covBServe(s.h.UnreadCount, asCaller(covBReq("GET", "/s/unread-count", "", nil, nil), s.dev))
		require.Equal(t, http.StatusOK, rec.Code)
		assert.EqualValues(t, 1, covBJSON(t, rec)["count"])
	})
	t.Run("unread-count com usuário desconhecido 500", func(t *testing.T) {
		rec := covBServe(s.h.UnreadCount, asCaller(covBReq("GET", "/s/unread-count", "", nil, nil), uuid.New()))
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
	t.Run("summary: autor 403", func(t *testing.T) {
		rec := covBServe(s.h.Summary, asCaller(covBReq("GET", "/s/summary", "", nil, nil), s.author))
		assert.Equal(t, http.StatusForbidden, rec.Code)
	})
	t.Run("summary: usuário desconhecido 500", func(t *testing.T) {
		rec := covBServe(s.h.Summary, asCaller(covBReq("GET", "/s/summary", "", nil, nil), uuid.New()))
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
	t.Run("summary: dev vê contagem por status", func(t *testing.T) {
		s.newSug(t, s.other, "segunda", "bug", "baixa")
		rec := covBServe(s.h.Summary, asCaller(covBReq("GET", "/s/summary", "", nil, nil), s.dev))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		m := covBJSON(t, rec)
		assert.EqualValues(t, 2, m["total"])
		by := m["by_status"].(map[string]any)
		assert.EqualValues(t, 1, by["aceita"])
		assert.EqualValues(t, 1, by["nova"])
		assert.NotNil(t, m["oldest_open"])
	})
}
