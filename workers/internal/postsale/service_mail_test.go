package postsale

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// MailEnabled diz o que sendOne decide: só há email de verdade com a flag ligada E um
// mailer. É o que a escrita do MCP consulta antes de reenviar (sem SMTP o Resend
// regrava o status como disabled e nada sai).
func TestService_MailEnabled(t *testing.T) {
	require.True(t, New(Config{Mailer: &recordingMailer{}, MailEnabled: true}).MailEnabled())
	require.False(t, New(Config{Mailer: &recordingMailer{}, MailEnabled: false}).MailEnabled(), "flag desligada (sem credencial de SMTP)")
	require.False(t, New(Config{MailEnabled: true}).MailEnabled(), "flag ligada mas sem mailer")
	require.False(t, New(Config{}).MailEnabled())
}
