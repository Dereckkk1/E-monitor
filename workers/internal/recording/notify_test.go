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

	require.Len(t, sentMails(m, mail), 1)
	got := sentMails(m, mail)[0]
	require.Contains(t, got.html, "&lt;b&gt;Radio&lt;/b&gt;")
	require.NotContains(t, got.html, "<b>Radio")
	require.Contains(t, got.text, "sem áudio de 05/11/2026 20:14:10 até 05/11/2026 20:14:50")
	require.Contains(t, got.text, "05/11/2026, das 20:13 às 21:14", "a janela segue sem segundos")
}

// O pré-roll e o burst do servidor podem dar mais áudio que a janela, mesmo faltando
// áudio dentro dela. "parcial (100%)" com buraco na mesma mensagem é contradição.
func TestNotifyPartial_PercentualNuncaChegaA100(t *testing.T) {
	from := time.Date(2026, 11, 5, 23, 14, 10, 0, time.UTC)
	r := Recording{ID: uuid.New(), StationName: "Radio", CreatorEmail: "a@b.c",
		StartsAt: from.Add(-time.Minute), EndsAt: from.Add(time.Hour)}
	mail := &fakeMailer{}
	m := newTestManager(t, newFakeRepo(), &fakeMedia{}, newFakeObjects(), mail)

	gap := Gap{From: from, To: from.Add(40 * time.Second)}
	m.notifyPartial(r, 63*time.Minute, time.Hour, []Gap{gap})

	got := sentMails(m, mail)[0]
	require.Contains(t, got.subject, "parcial (99%)")
	require.Contains(t, got.text, "cobriu 99% da janela")
}

// Flap numa janela de 6h pode dar centenas de trechos: o e-mail lista os primeiros.
func TestNotifyPartial_ListaLongaResume(t *testing.T) {
	from := time.Date(2026, 11, 5, 23, 14, 10, 0, time.UTC)
	r := Recording{ID: uuid.New(), StationName: "Radio", CreatorEmail: "a@b.c",
		StartsAt: from, EndsAt: from.Add(time.Hour)}
	mail := &fakeMailer{}
	m := newTestManager(t, newFakeRepo(), &fakeMedia{}, newFakeObjects(), mail)
	gaps := make([]Gap, 23)
	for i := range gaps {
		start := from.Add(time.Duration(i) * time.Minute)
		gaps[i] = Gap{From: start, To: start.Add(10 * time.Second)}
	}

	m.notifyPartial(r, 30*time.Minute, time.Hour, gaps)

	text := sentMails(m, mail)[0].text
	require.Equal(t, 20, strings.Count(text, "- sem áudio de "))
	require.Contains(t, text, "- e mais 3 trechos")
}

func TestNotifyPartial_ExatamenteVinteNaoResume(t *testing.T) {
	from := time.Date(2026, 11, 5, 23, 14, 10, 0, time.UTC)
	r := Recording{ID: uuid.New(), StationName: "Radio", CreatorEmail: "a@b.c",
		StartsAt: from, EndsAt: from.Add(time.Hour)}
	mail := &fakeMailer{}
	m := newTestManager(t, newFakeRepo(), &fakeMedia{}, newFakeObjects(), mail)

	m.notifyPartial(r, 30*time.Minute, time.Hour, make([]Gap, 20))

	text := sentMails(m, mail)[0].text
	require.Equal(t, 20, strings.Count(text, "- sem áudio de "))
	require.NotContains(t, text, "e mais")
}

// Muitos furos abaixo de DetailGap: sem lista, mas o percentual e a explicação ficam.
func TestNotifyPartial_SemTrechosListados(t *testing.T) {
	from := time.Date(2026, 11, 5, 23, 14, 10, 0, time.UTC)
	r := Recording{ID: uuid.New(), StationName: "Radio", CreatorEmail: "a@b.c",
		StartsAt: from, EndsAt: from.Add(time.Hour)}
	mail := &fakeMailer{}
	m := newTestManager(t, newFakeRepo(), &fakeMedia{}, newFakeObjects(), mail)

	m.notifyPartial(r, 54*time.Minute, time.Hour, []Gap{})

	got := sentMails(m, mail)[0]
	require.Contains(t, got.subject, "parcial (90%)")
	require.Contains(t, got.text, "cobriu 90% da janela")
	require.Contains(t, got.text, "trechos curtos")
	require.NotContains(t, got.text, "- sem áudio de ")
}

func TestNotifyFailed_IncluiMotivoCortado(t *testing.T) {
	r := Recording{ID: uuid.New(), StationName: "Radio", CreatorEmail: "a@b.c"}
	mail := &fakeMailer{}
	m := newTestManager(t, newFakeRepo(), &fakeMedia{}, newFakeObjects(), mail)

	m.notifyFailed(r)
	require.NotContains(t, sentMails(m, mail)[0].text, "Motivo")

	// Corta por caractere: cortar por byte partiria o "é" e geraria UTF-8 inválido.
	motivo := strings.Repeat("é", 400)
	r.LastError = &motivo
	m.notifyFailed(r)

	text := sentMails(m, mail)[1].text
	require.Contains(t, text, strings.Repeat("é", 300))
	require.NotContains(t, text, strings.Repeat("é", 301))
	require.Equal(t, strings.ToValidUTF8(text, ""), text)
}

func TestNotifyFinalizeStuck_SemCriadorNaoManda(t *testing.T) {
	mail := &fakeMailer{}
	m := newTestManager(t, newFakeRepo(), &fakeMedia{}, newFakeObjects(), mail)

	m.notifyFinalizeStuck(Recording{ID: uuid.New()}, context.DeadlineExceeded)

	require.Empty(t, sentMails(m, mail))
}

// SMTP mudo não pode parar quem avisa: o agendador manda notifyMissed de dentro do tick.
func TestNotify_SMTPMudoNaoPrendeQuemChamou(t *testing.T) {
	r := Recording{ID: uuid.New(), StationName: "Radio", CreatorEmail: "a@b.c"}
	muted := newMutedMailer(t)
	m := newTestManager(t, newFakeRepo(), &fakeMedia{}, newFakeObjects(), &fakeMailer{})
	m.mail = muted

	returned := make(chan struct{})
	go func() {
		defer close(returned)
		m.notifyMissed(r)
	}()

	select {
	case <-returned:
	case <-time.After(10 * time.Second):
		t.Fatal("notify ficou preso no envio")
	}
	require.Eventually(t, func() bool { return muted.calls.Load() == 1 }, 2*time.Second, time.Millisecond)
}

// A captura pode não ter começado por causa do disco: o texto não culpa o stream.
func TestNotifyNoConnection_NaoAfirmaQueFoiOStream(t *testing.T) {
	r := Recording{ID: uuid.New(), StationName: "Radio", CreatorEmail: "a@b.c"}
	mail := &fakeMailer{}
	m := newTestManager(t, newFakeRepo(), &fakeMedia{}, newFakeObjects(), mail)

	m.notifyNoConnection(r)

	got := sentMails(m, mail)[0]
	require.Contains(t, got.subject, "não conectou")
	require.Contains(t, got.text, "A captura não começou. O sistema segue tentando até o fim da janela. "+
		"Se a URL do stream mudou, corrija na emissora: a nova vale na próxima tentativa.")
	require.NotContains(t, got.text, "não respondeu")
}

// A captura pode ter parado por disco cheio ou emissora excluída: nem o assunto nem
// o texto culpam o stream.
func TestNotifyDropped_NaoAfirmaQueFoiOStream(t *testing.T) {
	r := Recording{ID: uuid.New(), StationName: "Radio", CreatorEmail: "a@b.c"}
	mail := &fakeMailer{}
	m := newTestManager(t, newFakeRepo(), &fakeMedia{}, newFakeObjects(), mail)

	m.notifyDropped(r)

	got := sentMails(m, mail)[0]
	require.Contains(t, got.subject, "captura parou")
	require.NotContains(t, got.subject, "caiu")
	require.Contains(t, got.text, "A captura parou no meio da gravação. O sistema segue tentando até o fim da janela. "+
		"Se a URL do stream mudou, corrija na emissora: a nova vale na próxima tentativa.")
	require.NotContains(t, got.text, "O stream parou")
}
