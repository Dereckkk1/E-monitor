package recording

import (
	"context"
	"fmt"
	"html"
	"strings"
	"time"

	"go.uber.org/zap"
)

// maxReasonChars limita o motivo técnico no e-mail: stderr de ffmpeg não tem teto.
const maxReasonChars = 300

// fmtSP tem segundos porque um buraco de 40s pode caber dentro do mesmo minuto.
func fmtSP(t time.Time) string { return t.In(saoPaulo).Format("02/01/2006 15:04:05") }

// clipReason corta por caractere: cortar por byte partiria um acento.
func clipReason(s string) string {
	r := []rune(s)
	if len(r) > maxReasonChars {
		return string(r[:maxReasonChars])
	}
	return s
}

func windowLabel(r Recording) string {
	return fmt.Sprintf("%s, das %s às %s", r.StartsAt.In(saoPaulo).Format("02/01/2006"),
		r.StartsAt.In(saoPaulo).Format("15:04"), r.EndsAt.In(saoPaulo).Format("15:04"))
}

// notify nunca falha para quem chamou: o e-mail é aviso, e o estado real está
// na tela. Sem criador (usuário excluído), não há para quem mandar.
func (m *Manager) notify(rec Recording, subject, body string) {
	if rec.CreatorEmail == "" {
		return
	}
	text := fmt.Sprintf("%s\n\nEmissora: %s\nGravação: %s\n\n%s/admin/gravacoes\n",
		body, rec.StationName, windowLabel(rec), strings.TrimRight(m.cfg.BaseURL, "/"))
	htmlBody := "<p>" + strings.ReplaceAll(html.EscapeString(text), "\n", "<br>") + "</p>"

	// Contexto próprio: o aviso de queda não pode morrer junto com a tentativa.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	subject = fmt.Sprintf("Gravação de %s: %s", rec.StationName, subject)
	if err := m.mail.Send(ctx, []string{rec.CreatorEmail}, subject, htmlBody, text); err != nil {
		m.log.Warn("recording: e-mail falhou", zap.Stringer("id", rec.ID), zap.Error(err))
	}
}

func (m *Manager) notifyNoConnection(rec Recording) {
	m.notify(rec, "não conectou",
		"O stream não respondeu no início da gravação. O sistema segue tentando até o fim da janela. "+
			"Se a URL mudou, corrija na emissora: a nova vale na próxima tentativa.")
}

func (m *Manager) notifyDropped(rec Recording) {
	m.notify(rec, "stream caiu",
		"O stream parou no meio da gravação. O sistema segue tentando reconectar até o fim da janela. "+
			"Se a URL mudou, corrija na emissora: a nova vale na próxima tentativa.")
}

func (m *Manager) notifyMissed(rec Recording) {
	m.notify(rec, "não gravou", "A plataforma ficou fora do ar durante toda a janela e nada foi gravado.")
}

func (m *Manager) notifyFailed(rec Recording) {
	body := "Nenhum áudio foi capturado nesta gravação."
	if rec.LastError != nil && *rec.LastError != "" {
		body += "\nMotivo: " + clipReason(*rec.LastError)
	}
	m.notify(rec, "falhou", body)
}

func (m *Manager) notifyPartial(rec Recording, covered, window time.Duration, gaps []Gap) {
	lines := make([]string, 0, len(gaps))
	for _, g := range gaps {
		lines = append(lines, fmt.Sprintf("- sem áudio de %s até %s", fmtSP(g.From), fmtSP(g.To)))
	}
	pct := 0
	if window > 0 {
		pct = int(covered * 100 / window)
	}
	m.notify(rec, fmt.Sprintf("parcial (%d%%)", pct),
		fmt.Sprintf("A gravação terminou com buracos e cobriu %d%% da janela.\n%s", pct, strings.Join(lines, "\n")))
}

func (m *Manager) notifyFinalizeStuck(rec Recording, cause error) {
	m.notify(rec, "arquivo não fechou",
		"O áudio está preservado, mas não foi possível fechar o arquivo. O sistema segue tentando.\n"+
			"Motivo: "+clipReason(cause.Error()))
}
