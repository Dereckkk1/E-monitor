package handlers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"radiocheck/internal/recording"
)

// covBRecSvc é um RecordingService configurável por teste.
type covBRecSvc struct {
	rec      *recording.Recording
	err      error
	peaks    []byte
	items    []recording.Recording
	total    int
	gotLimit int
	gotOff   int
	gotIn    recording.CreateInput
	// writeBeforeErr: o Clip/Clips pede o escritor (inicia o stream) e só depois falha.
	writeBeforeErr bool
	gotClip        recording.ClipSpec
	gotClips       []recording.ClipSpec
	gotFade        bool
	peaksCtxErr    error
	peaksDeadline  bool
}

func (f *covBRecSvc) Create(_ context.Context, in recording.CreateInput) (*recording.Recording, error) {
	f.gotIn = in
	if f.err != nil {
		return nil, f.err
	}
	return &recording.Recording{ID: uuid.New(), Status: recording.StatusProgramada, Gaps: []recording.Gap{}}, nil
}
func (f *covBRecSvc) List(_ context.Context, limit, offset int) ([]recording.Recording, int, error) {
	f.gotLimit, f.gotOff = limit, offset
	return f.items, f.total, f.err
}
func (f *covBRecSvc) Cancel(context.Context, uuid.UUID) error { return f.err }
func (f *covBRecSvc) Delete(context.Context, uuid.UUID) error { return f.err }
func (f *covBRecSvc) Audio(context.Context, uuid.UUID) (io.ReadCloser, *recording.Recording, int64, error) {
	if f.err != nil {
		return nil, nil, 0, f.err
	}
	return io.NopCloser(strings.NewReader("m4a-bytes")), &recording.Recording{}, 9, nil
}
func (f *covBRecSvc) Get(context.Context, uuid.UUID) (*recording.Recording, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.rec, nil
}
func (f *covBRecSvc) Peaks(ctx context.Context, _ uuid.UUID) ([]byte, error) {
	f.peaksCtxErr = ctx.Err()
	_, f.peaksDeadline = ctx.Deadline()
	return f.peaks, f.err
}
func (f *covBRecSvc) Clip(_ context.Context, _ uuid.UUID, c recording.ClipSpec, fade bool, out recording.ClipWriter) error {
	f.gotClip, f.gotFade = c, fade
	if f.err != nil && !f.writeBeforeErr {
		return f.err
	}
	w := out(&recording.Recording{})
	_, _ = w.Write([]byte("MP3DATA"))
	return f.err
}
func (f *covBRecSvc) Clips(_ context.Context, _ uuid.UUID, clips []recording.ClipSpec, fade bool, out recording.ClipWriter) error {
	f.gotClips, f.gotFade = clips, fade
	if f.err != nil && !f.writeBeforeErr {
		return f.err
	}
	w := out(&recording.Recording{})
	_, _ = w.Write([]byte("PKZIP"))
	return f.err
}

func covBRecHandler(svc *covBRecSvc) (*RecordingsHandler, *observer.ObservedLogs) {
	core, logs := observer.New(zap.WarnLevel)
	return &RecordingsHandler{Service: svc, Log: zap.New(core)}, logs
}

func TestCovB_Recordings_FailMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code int
		body string
	}{
		{"validação vira 400 com a mensagem da tela", &recording.ValidationError{Msg: "fim antes do início"}, 400, "fim antes do início"},
		{"sobreposição 409", recording.ErrOverlap, 409, "já existe gravação"},
		{"estado ocupado 409", recording.ErrBusy, 409, "estado que não permite"},
		{"não encontrada 404", recording.ErrNotFound, 404, "gravação não encontrada"},
		{"sem áudio 404", recording.ErrNoAudio, 404, "gravação sem áudio"},
		{"editor ocupado 503", recording.ErrEditorBusy, 503, "editor está ocupado"},
		{"envelopado ainda é reconhecido", errors.Join(errors.New("ctx"), recording.ErrOverlap), 409, ""},
		{"desconhecido 500 sem vazar detalhe", errors.New("conexão com 10.1.2.3 caiu"), 500, "internal error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, logs := covBRecHandler(&covBRecSvc{})
			rec := covBRecorder()
			h.fail(rec, tc.err)
			assert.Equal(t, tc.code, rec.Code)
			assert.Contains(t, rec.Body.String(), tc.body)
			if tc.code == 500 {
				assert.NotContains(t, rec.Body.String(), "10.1.2.3")
				require.Equal(t, 1, logs.Len(), "erro interno é logado, com o detalhe só no log")
				assert.Contains(t, logs.All()[0].Message, "erro interno")
			}
		})
	}
	t.Run("sem logger configurado não entra em pânico", func(t *testing.T) {
		h := &RecordingsHandler{Service: &covBRecSvc{}}
		rec := covBRecorder()
		h.fail(rec, errors.New("x"))
		assert.Equal(t, 500, rec.Code)
	})
}

func TestCovB_Recordings_CreateListCancelDelete(t *testing.T) {
	station := uuid.New()
	admin := covBClaims("admin", uuid.New())

	t.Run("create: json inválido e emissora inválida 400", func(t *testing.T) {
		h, _ := covBRecHandler(&covBRecSvc{})
		assert.Equal(t, 400, covBServe(h.Create, covBReq("POST", "/x", `{"station_id":`, nil, admin)).Code)
		rec := covBServe(h.Create, covBReq("POST", "/x", `{"station_id":"x"}`, nil, admin))
		assert.Equal(t, 400, rec.Code)
		assert.Contains(t, rec.Body.String(), "emissora inválida")
	})
	t.Run("create: repassa os campos e o autor ao serviço", func(t *testing.T) {
		svc := &covBRecSvc{}
		h, _ := covBRecHandler(svc)
		body := `{"station_id":"` + station.String() + `","day":"2026-12-01","start":"08:00","end":"09:30"}`
		rec := covBServe(h.Create, covBReq("POST", "/x", body, nil, admin))
		require.Equal(t, 201, rec.Code, rec.Body.String())
		assert.Equal(t, station, svc.gotIn.StationID)
		assert.Equal(t, "2026-12-01", svc.gotIn.Day)
		assert.Equal(t, "08:00", svc.gotIn.Start)
		assert.Equal(t, "09:30", svc.gotIn.End)
		require.NotNil(t, svc.gotIn.CreatedBy)
		assert.Equal(t, admin.UserID, *svc.gotIn.CreatedBy)
		assert.Equal(t, "programada", covBJSON(t, rec)["status"])
	})
	t.Run("create: sem claims grava sem autor; erro do serviço é traduzido", func(t *testing.T) {
		svc := &covBRecSvc{}
		h, _ := covBRecHandler(svc)
		body := `{"station_id":"` + station.String() + `"}`
		require.Equal(t, 201, covBServe(h.Create, covBReq("POST", "/x", body, nil, nil)).Code)
		assert.Nil(t, svc.gotIn.CreatedBy)
		svc.err = recording.ErrOverlap
		assert.Equal(t, 409, covBServe(h.Create, covBReq("POST", "/x", body, nil, nil)).Code)
	})

	t.Run("list: limites e deslocamento", func(t *testing.T) {
		tests := []struct {
			query         string
			limit, offset int
		}{
			{"", 50, 0},
			{"?limit=10&offset=20", 10, 20},
			{"?limit=0", 50, 0},
			{"?limit=-5", 50, 0},
			{"?limit=999", 200, 0},
			{"?offset=-3", 50, 0},
			{"?limit=abc&offset=xyz", 50, 0},
		}
		for _, tc := range tests {
			svc := &covBRecSvc{}
			h, _ := covBRecHandler(svc)
			require.Equal(t, 200, covBServe(h.List, covBReq("GET", "/x"+tc.query, "", nil, admin)).Code)
			assert.Equal(t, tc.limit, svc.gotLimit, tc.query)
			assert.Equal(t, tc.offset, svc.gotOff, tc.query)
		}
	})
	t.Run("list: nil vira array vazio e o total é repassado", func(t *testing.T) {
		h, _ := covBRecHandler(&covBRecSvc{total: 7})
		rec := covBServe(h.List, covBReq("GET", "/x", "", nil, admin))
		require.Equal(t, 200, rec.Code)
		m := covBJSON(t, rec)
		assert.Equal(t, []any{}, m["items"])
		assert.EqualValues(t, 7, m["total"])
	})
	t.Run("list: erro do serviço 500", func(t *testing.T) {
		h, _ := covBRecHandler(&covBRecSvc{err: errors.New("db fora")})
		assert.Equal(t, 500, covBServe(h.List, covBReq("GET", "/x", "", nil, admin)).Code)
	})

	id := map[string]string{"id": uuid.NewString()}
	for name, call := range map[string]func(*RecordingsHandler) http.HandlerFunc{
		"cancel": func(h *RecordingsHandler) http.HandlerFunc { return h.Cancel },
		"delete": func(h *RecordingsHandler) http.HandlerFunc { return h.Delete },
	} {
		t.Run(name+": id inválido 400, sucesso 204, erros traduzidos", func(t *testing.T) {
			svc := &covBRecSvc{}
			h, _ := covBRecHandler(svc)
			assert.Equal(t, 400, covBServe(call(h), covBReq("POST", "/x", "", map[string]string{"id": "x"}, admin)).Code)
			assert.Equal(t, 204, covBServe(call(h), covBReq("POST", "/x", "", id, admin)).Code)
			svc.err = recording.ErrBusy
			assert.Equal(t, 409, covBServe(call(h), covBReq("POST", "/x", "", id, admin)).Code)
			svc.err = recording.ErrNotFound
			assert.Equal(t, 404, covBServe(call(h), covBReq("POST", "/x", "", id, admin)).Code)
		})
	}
}

func TestCovB_Recordings_GetPeaksAudio(t *testing.T) {
	id := uuid.New()
	p := map[string]string{"id": id.String()}

	t.Run("get: id inválido, sucesso e erros", func(t *testing.T) {
		svc := &covBRecSvc{rec: &recording.Recording{ID: id, Status: recording.StatusProgramada, Gaps: []recording.Gap{}}}
		h, _ := covBRecHandler(svc)
		assert.Equal(t, 400, covBServe(h.Get, covBReq("GET", "/x", "", map[string]string{"id": "x"}, nil)).Code)
		rec := covBServe(h.Get, covBReq("GET", "/x", "", p, nil))
		require.Equal(t, 200, rec.Code)
		assert.Equal(t, id.String(), covBJSON(t, rec)["id"])
		svc.err = recording.ErrNotFound
		assert.Equal(t, 404, covBServe(h.Get, covBReq("GET", "/x", "", p, nil)).Code)
	})
	t.Run("peaks: binário com cabeçalhos e prazo próprio (sem o cancel do router)", func(t *testing.T) {
		svc := &covBRecSvc{peaks: []byte{1, 2, 3, 4}}
		h, _ := covBRecHandler(svc)
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // o router já cancelou o contexto da requisição
		req := covBWithCtx(httptest.NewRequest("GET", "/x", nil).WithContext(ctx), p, nil)
		rec := covBServe(h.Peaks, req)
		require.Equal(t, 200, rec.Code)
		assert.Equal(t, []byte{1, 2, 3, 4}, rec.Body.Bytes())
		assert.Equal(t, "application/octet-stream", rec.Header().Get("Content-Type"))
		assert.Equal(t, strconv.Itoa(recording.PeaksPerSecond), rec.Header().Get("X-Peaks-Per-Second"))
		assert.Equal(t, "4", rec.Header().Get("Content-Length"))
		assert.NoError(t, svc.peaksCtxErr, "o ffmpeg de 6h não pode morrer com o timeout de 60s do router")
		assert.True(t, svc.peaksDeadline, "o corte tem prazo próprio")
	})
	t.Run("peaks: id inválido 400; sem áudio 404; erro 500", func(t *testing.T) {
		svc := &covBRecSvc{}
		h, _ := covBRecHandler(svc)
		assert.Equal(t, 400, covBServe(h.Peaks, covBReq("GET", "/x", "", map[string]string{"id": "x"}, nil)).Code)
		svc.err = recording.ErrNoAudio
		assert.Equal(t, 404, covBServe(h.Peaks, covBReq("GET", "/x", "", p, nil)).Code)
		svc.err = errors.New("ffmpeg falhou")
		assert.Equal(t, 500, covBServe(h.Peaks, covBReq("GET", "/x", "", p, nil)).Code)
	})
	t.Run("audio: id inválido, erro, e bytes com cabeçalhos", func(t *testing.T) {
		svc := &covBRecSvc{}
		h, _ := covBRecHandler(svc)
		assert.Equal(t, 400, covBServe(h.Audio, covBReq("GET", "/x", "", map[string]string{"id": "x"}, nil)).Code)
		rec := covBServe(h.Audio, covBReq("GET", "/x", "", p, nil))
		require.Equal(t, 200, rec.Code)
		assert.Equal(t, "m4a-bytes", rec.Body.String())
		assert.Equal(t, "audio/mp4", rec.Header().Get("Content-Type"))
		assert.Equal(t, "9", rec.Header().Get("Content-Length"))
		assert.Equal(t, `inline; filename="`+id.String()+`.m4a"`, rec.Header().Get("Content-Disposition"))
		svc.err = recording.ErrNoAudio
		assert.Equal(t, 404, covBServe(h.Audio, covBReq("GET", "/x", "", p, nil)).Code)
	})
}

func TestCovB_Recordings_ClipAndClips(t *testing.T) {
	id := uuid.New()
	p := map[string]string{"id": id.String()}

	t.Run("clip: id inválido e from/to inválidos 400", func(t *testing.T) {
		h, _ := covBRecHandler(&covBRecSvc{})
		assert.Equal(t, 400, covBServe(h.Clip, covBReq("GET", "/x?from=1&to=2", "", map[string]string{"id": "x"}, nil)).Code)
		for _, q := range []string{"", "?from=1", "?to=2", "?from=a&to=2", "?from=1&to=b"} {
			rec := covBServe(h.Clip, covBReq("GET", "/x"+q, "", p, nil))
			assert.Equal(t, 400, rec.Code, q)
			assert.Contains(t, rec.Body.String(), "informe início e fim", q)
		}
	})
	t.Run("clip: repassa from/to/nome/fade e responde mp3 com nome seguro", func(t *testing.T) {
		svc := &covBRecSvc{}
		h, _ := covBRecHandler(svc)
		q := "?from=1.5&to=9&fade=1&name=" + url.QueryEscape(`Spot "Ação" 30s`)
		rec := covBServe(h.Clip, covBReq("GET", "/x"+q, "", p, nil))
		require.Equal(t, 200, rec.Code, rec.Body.String())
		assert.Equal(t, "MP3DATA", rec.Body.String())
		assert.Equal(t, "audio/mpeg", rec.Header().Get("Content-Type"))
		assert.Equal(t, 1.5, svc.gotClip.From)
		assert.Equal(t, 9.0, svc.gotClip.To)
		assert.Equal(t, `Spot "Ação" 30s`, svc.gotClip.Name)
		assert.True(t, svc.gotFade)
		assert.Equal(t, `attachment; filename="Spot -A__o- 30s.mp3"; filename*=UTF-8''Spot%20-A%C3%A7%C3%A3o-%2030s.mp3`,
			rec.Header().Get("Content-Disposition"), "aspas viram '-', não-ASCII vira '_' no reserva e %XX no UTF-8")
	})
	t.Run("clip: fade só liga com fade=1", func(t *testing.T) {
		svc := &covBRecSvc{}
		h, _ := covBRecHandler(svc)
		covBServe(h.Clip, covBReq("GET", "/x?from=0&to=1&fade=true", "", p, nil))
		assert.False(t, svc.gotFade)
	})
	t.Run("clip: erro antes do primeiro byte vira status HTTP", func(t *testing.T) {
		svc := &covBRecSvc{err: recording.ErrNoAudio}
		h, _ := covBRecHandler(svc)
		assert.Equal(t, 404, covBServe(h.Clip, covBReq("GET", "/x?from=0&to=1", "", p, nil)).Code)
		svc.err = recording.ErrEditorBusy
		assert.Equal(t, 503, covBServe(h.Clip, covBReq("GET", "/x?from=0&to=1", "", p, nil)).Code)
	})
	t.Run("clip: erro depois do primeiro byte só gera log (download truncado, status 200)", func(t *testing.T) {
		svc := &covBRecSvc{err: errors.New("ffmpeg morreu"), writeBeforeErr: true}
		h, logs := covBRecHandler(svc)
		rec := covBServe(h.Clip, covBReq("GET", "/x?from=0&to=1", "", p, nil))
		assert.Equal(t, 200, rec.Code)
		assert.Equal(t, "MP3DATA", rec.Body.String())
		require.Equal(t, 1, logs.Len())
		assert.Contains(t, logs.All()[0].Message, "corte interrompido")
	})

	t.Run("clips: id inválido, json inválido e corpo gigante 400", func(t *testing.T) {
		h, _ := covBRecHandler(&covBRecSvc{})
		assert.Equal(t, 400, covBServe(h.Clips, covBReq("POST", "/x", `{}`, map[string]string{"id": "x"}, nil)).Code)
		assert.Equal(t, 400, covBServe(h.Clips, covBReq("POST", "/x", `{"clips":`, p, nil)).Code)
		huge := `{"clips":[{"name":"` + strings.Repeat("a", 2<<20) + `"}]}`
		assert.Equal(t, 400, covBServe(h.Clips, covBReq("POST", "/x", huge, p, nil)).Code, "limite de 1MB no corpo")
	})
	t.Run("clips: zip com lista, fade e nome do pacote", func(t *testing.T) {
		svc := &covBRecSvc{}
		h, _ := covBRecHandler(svc)
		body := `{"clips":[{"name":"A","from":1,"to":2},{"name":"B","from":3,"to":4}],"fade":true,"name":"Meu pacote"}`
		rec := covBServe(h.Clips, covBReq("POST", "/x", body, p, nil))
		require.Equal(t, 200, rec.Code, rec.Body.String())
		assert.Equal(t, "application/zip", rec.Header().Get("Content-Type"))
		assert.Equal(t, "PKZIP", rec.Body.String())
		require.Len(t, svc.gotClips, 2)
		assert.Equal(t, "B", svc.gotClips[1].Name)
		assert.True(t, svc.gotFade)
		assert.Contains(t, rec.Header().Get("Content-Disposition"), `filename="Meu pacote.zip"`)
	})
	t.Run("clips: nome ausente usa 'trechos'", func(t *testing.T) {
		h, _ := covBRecHandler(&covBRecSvc{})
		rec := covBServe(h.Clips, covBReq("POST", "/x", `{"clips":[]}`, p, nil))
		require.Equal(t, 200, rec.Code)
		assert.Contains(t, rec.Header().Get("Content-Disposition"), `filename="trechos.zip"`)
	})
	t.Run("clips: erro de validação antes do stream 400", func(t *testing.T) {
		h, _ := covBRecHandler(&covBRecSvc{err: &recording.ValidationError{Msg: "trecho inválido"}})
		rec := covBServe(h.Clips, covBReq("POST", "/x", `{"clips":[{"from":5,"to":1}]}`, p, nil))
		assert.Equal(t, 400, rec.Code)
		assert.Contains(t, rec.Body.String(), "trecho inválido")
	})
}

func TestCovB_Recordings_AttachmentAndParseSeconds(t *testing.T) {
	t.Run("attachment: nome vazio usa o reserva e extensão", func(t *testing.T) {
		assert.Equal(t, `attachment; filename="trecho.mp3"; filename*=UTF-8''trecho.mp3`, attachment("", "trecho", ".mp3"))
	})
	t.Run("attachment: UTF-8 vai codificado e o ASCII de reserva troca por _", func(t *testing.T) {
		got := attachment("Promoção", "trecho", ".zip")
		assert.Contains(t, got, `filename="Promo__o.zip"`)
		assert.Contains(t, got, "filename*=UTF-8''Promo%C3%A7%C3%A3o.zip")
	})
	t.Run("attachment: aspas e barras não quebram o cabeçalho", func(t *testing.T) {
		got := attachment(`a"b\c`, "x", ".mp3")
		inner := strings.Split(strings.Split(got, `filename="`)[1], `"`)[0]
		assert.NotContains(t, inner, `"`)
		assert.NotContains(t, inner, `\`)
	})
	t.Run("parseSeconds", func(t *testing.T) {
		v, ok := parseSeconds("12.5")
		assert.True(t, ok)
		assert.Equal(t, 12.5, v)
		_, ok = parseSeconds("")
		assert.False(t, ok)
		_, ok = parseSeconds("1,5")
		assert.False(t, ok)
	})
}
