package recording

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestNotifyPartial_EscapaHTMLEMostraHorarioDeSaoPaulo(t *testing.T) {
	from := time.Date(2026, 11, 5, 23, 14, 10, 0, time.UTC)
	r := Recording{ID: uuid.New(), StationName: "<b>Radio</b>", CreatorEmail: "a@b.c",
		StartsAt: from.Add(-time.Minute), EndsAt: from.Add(time.Hour)}
	mail := &fakeMailer{}
	m := newTestManager(t, newFakeRepo(), &fakeMedia{}, newFakeObjects(), mail)

	// Buraco de 40s dentro do mesmo minuto: sem segundos, os dois lados saíam iguais.
	gap := Gap{From: from, To: from.Add(40 * time.Second)}
	m.notifyPartial(r, 30*time.Minute, time.Hour, []Gap{gap})

	require.Len(t, mail.all(), 1)
	got := mail.all()[0]
	require.Contains(t, got.html, "&lt;b&gt;Radio&lt;/b&gt;")
	require.NotContains(t, got.html, "<b>Radio")
	require.Contains(t, got.text, "sem áudio de 05/11/2026 20:14:10 até 05/11/2026 20:14:50")
	require.Contains(t, got.text, "05/11/2026, das 20:13 às 21:14", "a janela segue sem segundos")
}

func TestNotifyFailed_IncluiMotivoCortado(t *testing.T) {
	r := Recording{ID: uuid.New(), StationName: "Radio", CreatorEmail: "a@b.c"}
	mail := &fakeMailer{}
	m := newTestManager(t, newFakeRepo(), &fakeMedia{}, newFakeObjects(), mail)

	m.notifyFailed(r)
	require.NotContains(t, mail.all()[0].text, "Motivo")

	// Corta por caractere: cortar por byte partiria o "é" e geraria UTF-8 inválido.
	motivo := strings.Repeat("é", 400)
	r.LastError = &motivo
	m.notifyFailed(r)

	text := mail.all()[1].text
	require.Contains(t, text, strings.Repeat("é", 300))
	require.NotContains(t, text, strings.Repeat("é", 301))
	require.Equal(t, strings.ToValidUTF8(text, ""), text)
}

func TestNotifyFinalizeStuck_SemCriadorNaoManda(t *testing.T) {
	mail := &fakeMailer{}
	m := newTestManager(t, newFakeRepo(), &fakeMedia{}, newFakeObjects(), mail)

	m.notifyFinalizeStuck(Recording{ID: uuid.New()}, context.DeadlineExceeded)

	require.Empty(t, mail.all())
}
