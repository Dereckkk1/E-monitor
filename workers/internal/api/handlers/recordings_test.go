package handlers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/auth"
	"radiocheck/internal/recording"
)

type fakeRecordings struct {
	createErr error
	cancelErr error
	deleteErr error
	audioErr  error
	gotInput  recording.CreateInput
	audioCtx  context.Context

	calls     int // chamadas ao serviço, de qualquer método
	gotLimit  int
	gotOffset int
	listItems []recording.Recording
}

func (f *fakeRecordings) Create(_ context.Context, in recording.CreateInput) (*recording.Recording, error) {
	f.calls++
	f.gotInput = in
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &recording.Recording{ID: uuid.New(), Status: recording.StatusProgramada, Gaps: []recording.Gap{}}, nil
}
func (f *fakeRecordings) List(_ context.Context, limit, offset int) ([]recording.Recording, int, error) {
	f.calls++
	f.gotLimit, f.gotOffset = limit, offset
	return f.listItems, 0, nil
}
func (f *fakeRecordings) Cancel(context.Context, uuid.UUID) error {
	f.calls++
	return f.cancelErr
}
func (f *fakeRecordings) Delete(context.Context, uuid.UUID) error {
	f.calls++
	return f.deleteErr
}
func (f *fakeRecordings) Audio(ctx context.Context, _ uuid.UUID) (io.ReadCloser, *recording.Recording, int64, error) {
	f.calls++
	f.audioCtx = ctx
	if f.audioErr != nil {
		return nil, nil, 0, f.audioErr
	}
	return io.NopCloser(strings.NewReader("audio")),
		&recording.Recording{}, 5, nil
}

func recReq(method, target, body string, id uuid.UUID) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id.String())
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	return req.WithContext(auth.ContextWithClaims(ctx, &auth.Claims{UserID: uuid.New(), Role: "admin"}))
}

func TestRecordings_Create(t *testing.T) {
	st := uuid.New()
	f := &fakeRecordings{}
	h := &RecordingsHandler{Service: f}
	rec := httptest.NewRecorder()
	req := recReq("POST", "/recordings",
		`{"station_id":"`+st.String()+`","day":"2026-11-05","start":"14:00","end":"15:00"}`, uuid.Nil)
	h.Create(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	require.Equal(t, st, f.gotInput.StationID)
	require.Equal(t, "14:00", f.gotInput.Start)
	claims, ok := auth.ClaimsFromContext(req.Context())
	require.True(t, ok)
	require.NotNil(t, f.gotInput.CreatedBy)
	require.Equal(t, claims.UserID, *f.gotInput.CreatedBy)
}

func TestRecordings_Create_CorpoGrandeDemais(t *testing.T) {
	f := &fakeRecordings{}
	h := &RecordingsHandler{Service: f}
	body := `{"station_id":"` + uuid.NewString() + `","day":"` + strings.Repeat("x", 2<<20) + `"}`
	rec := httptest.NewRecorder()
	h.Create(rec, recReq("POST", "/recordings", body, uuid.Nil))

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Zero(t, f.calls)
}

func TestRecordings_List_Paginacao(t *testing.T) {
	cases := []struct {
		name, query         string
		wantLimit, wantOffs int
	}{
		{"sem parametros", "", 50, 0},
		{"limit zero", "?limit=0", 50, 0},
		{"limit negativo", "?limit=-3", 50, 0},
		{"limit acima do teto vira o teto", "?limit=201", 200, 0},
		{"limit muito acima do teto vira o teto", "?limit=5000", 200, 0},
		{"limit invalido", "?limit=abc", 50, 0},
		{"limit 200 e respeitado", "?limit=200", 200, 0},
		{"limit 1 e respeitado", "?limit=1", 1, 0},
		{"offset negativo", "?offset=-5", 50, 0},
		{"offset invalido", "?offset=abc", 50, 0},
		{"offset valido", "?limit=10&offset=30", 10, 30},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeRecordings{}
			h := &RecordingsHandler{Service: f}
			rec := httptest.NewRecorder()
			h.List(rec, recReq("GET", "/recordings"+c.query, "", uuid.Nil))

			require.Equal(t, http.StatusOK, rec.Code)
			require.Equal(t, c.wantLimit, f.gotLimit)
			require.Equal(t, c.wantOffs, f.gotOffset)
		})
	}
}

func TestRecordings_List_ItemsVazioSaiComoArray(t *testing.T) {
	// listItems nil: o serviço pode devolver slice nil, e o JSON não pode virar null.
	h := &RecordingsHandler{Service: &fakeRecordings{}}
	rec := httptest.NewRecorder()
	h.List(rec, recReq("GET", "/recordings", "", uuid.Nil))

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"items":[],"total":0}`, rec.Body.String())
}

func TestRecordings_IdInvalido(t *testing.T) {
	f := &fakeRecordings{}
	h := &RecordingsHandler{Service: f}
	cases := map[string]func(http.ResponseWriter, *http.Request){
		"cancel": h.Cancel,
		"delete": h.Delete,
		"audio":  h.Audio,
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/x", nil)
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("id", "nao-e-uuid")
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
			rec := httptest.NewRecorder()
			fn(rec, req)

			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Zero(t, f.calls)
		})
	}
}

func TestRecordings_Create_Erros(t *testing.T) {
	cases := []struct {
		name string
		body string
		err  error
		code int
	}{
		{"json quebrado", `nope`, nil, 400},
		{"emissora invalida", `{"station_id":"x"}`, nil, 400},
		{"validacao vai para a tela", `{"station_id":"` + uuid.NewString() + `"}`, &recording.ValidationError{Msg: "o início já passou"}, 400},
		{"sobreposicao", `{"station_id":"` + uuid.NewString() + `"}`, recording.ErrOverlap, 409},
		{"erro interno nao vaza", `{"station_id":"` + uuid.NewString() + `"}`, errors.New("pg: detalhe interno"), 500},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := &RecordingsHandler{Service: &fakeRecordings{createErr: c.err}}
			rec := httptest.NewRecorder()
			h.Create(rec, recReq("POST", "/recordings", c.body, uuid.Nil))
			require.Equal(t, c.code, rec.Code)
			require.NotContains(t, rec.Body.String(), "pg:")
		})
	}
}

func TestRecordings_CancelEDelete(t *testing.T) {
	h := &RecordingsHandler{Service: &fakeRecordings{cancelErr: recording.ErrBusy, deleteErr: recording.ErrBusy}}
	rec := httptest.NewRecorder()
	h.Cancel(rec, recReq("POST", "/x", "", uuid.New()))
	require.Equal(t, http.StatusConflict, rec.Code)

	rec = httptest.NewRecorder()
	h.Delete(rec, recReq("DELETE", "/x", "", uuid.New()))
	require.Equal(t, http.StatusConflict, rec.Code)

	ok := &RecordingsHandler{Service: &fakeRecordings{}}
	rec = httptest.NewRecorder()
	ok.Delete(rec, recReq("DELETE", "/x", "", uuid.New()))
	require.Equal(t, http.StatusNoContent, rec.Code)
}

func TestRecordings_Audio(t *testing.T) {
	h := &RecordingsHandler{Service: &fakeRecordings{}}
	rec := httptest.NewRecorder()
	id := uuid.New()
	h.Audio(rec, recReq("GET", "/x", "", id))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, `inline; filename="`+id.String()+`.m4a"`, rec.Header().Get("Content-Disposition"))
	require.Equal(t, "audio/mp4", rec.Header().Get("Content-Type"))
	require.Equal(t, "5", rec.Header().Get("Content-Length"))
	require.Equal(t, "audio", rec.Body.String())
}

func TestRecordings_Audio_SemAudio(t *testing.T) {
	h := &RecordingsHandler{Service: &fakeRecordings{audioErr: recording.ErrNoAudio}}
	rec := httptest.NewRecorder()
	h.Audio(rec, recReq("GET", "/x", "", uuid.New()))
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// middleware.Timeout cancela r.Context() em 60s. Com ele ligado ao S3, o download
// de um arquivo grande seria cortado no meio.
func TestRecordings_Audio_ContextoNaoHerdaOTimeout(t *testing.T) {
	f := &fakeRecordings{}
	h := &RecordingsHandler{Service: f}
	req := recReq("GET", "/x", "", uuid.New())
	ctx, cancel := context.WithCancel(req.Context())
	cancel()

	h.Audio(httptest.NewRecorder(), req.WithContext(ctx))

	require.NoError(t, f.audioCtx.Err())
}
