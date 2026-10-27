package handlers

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"radiocheck/internal/hub"
)

func ptr[T any](v T) *T { return &v }

func TestDetalheDoHub_TraduzParaOVocabularioDaTela(t *testing.T) {
	quando := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	s := &hub.SugestaoHub{
		ID: "s1", Numero: 42, NumeroOriginal: ptr(17), Origem: "e-monitor",
		Tipo: "ideia", Titulo: "T", Descricao: "D", UrgenciaAutor: "alta", Status: "retirada",
		Prioridade: ptr("urgente"), Esforco: ptr("M"), Resposta: ptr("feito"), SuaVez: true,
		Autor: hub.PessoaHub{ID: "u1", Nome: "Ana"}, CreatedAt: quando, UltimaAtividadeEm: quando,
		Comentarios: []hub.ComentarioHub{{ID: "c1", Autor: hub.PessoaHub{ID: "g1", Nome: "Dereck"}, Corpo: "oi", DoGestor: true, CreatedAt: quando}},
		Anexos: []hub.AnexoHub{
			{ID: "a1", Tipo: "arquivo", ComentarioID: ptr("c1"), Nome: ptr("p.png"), MimeType: ptr("image/png"), Tamanho: ptr(int64(10))},
			{ID: "a2", Tipo: "link", URL: ptr("https://x.y/z"), Rotulo: ptr("doc")},
		},
		Eventos: []hub.EventoHub{{ID: "e1", Tipo: "criada", Ator: &hub.PessoaHub{Nome: "Ana"}}, {ID: "e2", Tipo: "status", Para: ptr("retirada")}},
	}

	d := detalheDoHub(s)
	b, _ := json.Marshal(d)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))

	require.Equal(t, float64(42), m["ref_num"])
	require.Equal(t, float64(17), m["numero_original"])
	require.Equal(t, "feature", m["type"])
	require.Equal(t, "retirada", m["status"])
	require.Equal(t, "alta", m["requester_priority"])
	require.Equal(t, "urgente", m["dev_priority"])
	require.Equal(t, "feito", m["dev_feedback"])
	require.Equal(t, true, m["awaiting_author"])
	require.Equal(t, "u1", m["created_by"])
	require.Equal(t, "Ana", m["created_by_name"])
	require.Equal(t, "e-monitor", m["origin"])
	require.Equal(t, float64(1), m["comment_count"])
	require.NotContains(t, string(b), "dev_notes")

	c := m["comments"].([]any)[0].(map[string]any)
	require.Equal(t, "g1", c["author_id"])
	require.Equal(t, "Dereck", c["author_name"])
	require.Equal(t, true, c["from_dev"])

	at := m["attachments"].([]any)
	require.Equal(t, "arquivo", at[0].(map[string]any)["kind"])
	require.Equal(t, "c1", at[0].(map[string]any)["comment_id"])
	require.Equal(t, "image/png", at[0].(map[string]any)["content_type"])
	require.Equal(t, "link", at[1].(map[string]any)["kind"])
	require.Equal(t, "https://x.y/z", at[1].(map[string]any)["link_url"])

	ev := m["events"].([]any)
	require.Equal(t, "created", ev[0].(map[string]any)["event_type"])
	require.Equal(t, "status_changed", ev[1].(map[string]any)["event_type"])
	require.Equal(t, "retirada", ev[1].(map[string]any)["to_value"])
}

func TestLinhaDoHub(t *testing.T) {
	l := linhaDoHub(hub.SugestaoLinhaHub{ID: "s1", Numero: 3, Origem: "hub", Tipo: "bug", Titulo: "T",
		Previa: "prévia…", Status: "nova", UrgenciaAutor: "baixa", Comentarios: 2, NaoLida: true,
		Autor: hub.PessoaHub{ID: "u1", Nome: "Ana"}})
	require.Equal(t, 3, l.RefNum)
	require.Equal(t, "prévia…", l.Description)
	require.Equal(t, 2, l.CommentCount)
	require.True(t, l.Unread)
	require.Equal(t, "hub", l.Origin)
}

func TestDetalheDoHub_ListasVaziasSaemComoArray(t *testing.T) {
	b, _ := json.Marshal(detalheDoHub(&hub.SugestaoHub{ID: "s1"}))
	require.Contains(t, string(b), `"comments":[]`)
	require.Contains(t, string(b), `"attachments":[]`)
	require.Contains(t, string(b), `"events":[]`)
}
