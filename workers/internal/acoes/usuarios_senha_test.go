package acoes

import (
	"context"
	"strings"
	"testing"
	"unicode"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/welcome"
)

// SenhaAleatoria é a senha inicial que o MCP gera para criar_usuario (F4-11): a
// senha nunca passa pelo chat, então quem a inventa é o servidor, com crypto/rand.

func TestSenhaAleatoria_TamanhoEAlfabeto(t *testing.T) {
	for i := 0; i < 200; i++ {
		s, err := SenhaAleatoria()
		require.NoError(t, err)
		require.Len(t, s, TamanhoSenhaGerada)
		require.GreaterOrEqual(t, len(s), TamanhoMinimoSenha, "tem de passar no piso que a criação exige")
		for _, r := range s {
			require.True(t, strings.ContainsRune(alfabetoSenha, r), "caractere fora do alfabeto: %q", r)
		}
	}
}

func TestSenhaAleatoria_AlfabetoSemAmbiguosESemRepeticao(t *testing.T) {
	for _, ambiguo := range "0O1lI" {
		require.NotContains(t, alfabetoSenha, string(ambiguo), "a pessoa copia a senha de uma página: sem 0/O/1/l/I")
	}
	vistos := map[rune]bool{}
	for _, r := range alfabetoSenha {
		require.False(t, vistos[r], "símbolo repetido no alfabeto: %q (viesaria o sorteio)", r)
		vistos[r] = true
	}
}

// Toda senha tem maiúscula, minúscula e dígito: o piso do sistema é só o
// tamanho, mas quem a lê na página de boas-vindas espera uma senha "de verdade".
func TestSenhaAleatoria_SempreTemMaiusculaMinusculaEDigito(t *testing.T) {
	for i := 0; i < 2000; i++ {
		s, err := SenhaAleatoria()
		require.NoError(t, err)
		var up, low, dig bool
		for _, r := range s {
			switch {
			case unicode.IsUpper(r):
				up = true
			case unicode.IsLower(r):
				low = true
			case unicode.IsDigit(r):
				dig = true
			}
		}
		require.True(t, up && low && dig, "faltou uma classe em %q", s)
	}
}

func TestSenhaAleatoria_NaoRepeteEm1000Chamadas(t *testing.T) {
	vistas := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		s, err := SenhaAleatoria()
		require.NoError(t, err)
		require.False(t, vistas[s], "senha repetida: %q", s)
		vistas[s] = true
	}
}

// Cada posição sorteia o alfabeto inteiro (um viés ou uma posição fixa
// apareceriam como símbolo que nunca sai): em 4 000 senhas todo símbolo do
// alfabeto aparece em todas as posições.
func TestSenhaAleatoria_TodaPosicaoUsaOAlfabetoInteiro(t *testing.T) {
	porPosicao := make([]map[byte]bool, TamanhoSenhaGerada)
	for i := range porPosicao {
		porPosicao[i] = map[byte]bool{}
	}
	for i := 0; i < 4000; i++ {
		s, err := SenhaAleatoria()
		require.NoError(t, err)
		for pos := 0; pos < len(s); pos++ {
			porPosicao[pos][s[pos]] = true
		}
	}
	for pos, vistos := range porPosicao {
		require.Len(t, vistos, len(alfabetoSenha), "posição %d não sorteou o alfabeto inteiro", pos)
	}
}

// ── BoasVindasDisponiveis ───────────────────────────────────────────────

// A criação de usuário pelo MCP só existe com o convite disponível (é ele que
// entrega a senha). Sem emissor, ou com o serviço sem WELCOME_ENC_KEY, não há
// como a pessoa receber a senha.
func TestBoasVindasDisponiveis(t *testing.T) {
	t.Run("sem emissor", func(t *testing.T) {
		require.False(t, (&Servico{}).BoasVindasDisponiveis())
	})
	t.Run("serviço nil dentro do adaptador", func(t *testing.T) {
		require.False(t, (&Servico{BoasVindas: BoasVindasDoServico(nil)}).BoasVindasDisponiveis())
	})
	t.Run("serviço sem chave de cifra", func(t *testing.T) {
		svc := welcome.New(welcome.Config{Log: zap.NewNop()})
		require.False(t, (&Servico{BoasVindas: BoasVindasDoServico(svc)}).BoasVindasDisponiveis())
	})
	t.Run("serviço com chave de cifra", func(t *testing.T) {
		c, err := welcome.NewCipher(strings.Repeat("ab", 32))
		require.NoError(t, err)
		svc := welcome.New(welcome.Config{Cipher: c, Log: zap.NewNop()})
		require.True(t, (&Servico{BoasVindas: BoasVindasDoServico(svc)}).BoasVindasDisponiveis())
	})
	t.Run("emissor que não diz (um falso de teste) vale como disponível", func(t *testing.T) {
		require.True(t, (&Servico{BoasVindas: &emissorFalso{}}).BoasVindasDisponiveis())
	})
	t.Run("emissor que diz que não", func(t *testing.T) {
		require.False(t, (&Servico{BoasVindas: &emissorIndisponivel{}}).BoasVindasDisponiveis())
	})
}

// ── BoasVindasEmailLigado ───────────────────────────────────────────────

// Convite emitido sem SMTP fica com email_status disabled: a pessoa criada pelo MCP não
// receberia a senha. O emissor real diz se o serviço manda email de verdade.
func TestBoasVindasEmailLigado(t *testing.T) {
	cifra, err := welcome.NewCipher(strings.Repeat("ab", 32))
	require.NoError(t, err)
	t.Run("sem emissor", func(t *testing.T) {
		require.False(t, (&Servico{}).BoasVindasEmailLigado())
	})
	t.Run("serviço nil dentro do adaptador", func(t *testing.T) {
		require.False(t, (&Servico{BoasVindas: BoasVindasDoServico(nil)}).BoasVindasEmailLigado())
	})
	t.Run("serviço com chave mas sem SMTP", func(t *testing.T) {
		svc := welcome.New(welcome.Config{Cipher: cifra, Log: zap.NewNop()})
		require.True(t, (&Servico{BoasVindas: BoasVindasDoServico(svc)}).BoasVindasDisponiveis())
		require.False(t, (&Servico{BoasVindas: BoasVindasDoServico(svc)}).BoasVindasEmailLigado())
	})
	t.Run("serviço com chave e SMTP", func(t *testing.T) {
		svc := welcome.New(welcome.Config{Cipher: cifra, Mailer: mailerNulo{}, MailEnabled: true, Log: zap.NewNop()})
		require.True(t, (&Servico{BoasVindas: BoasVindasDoServico(svc)}).BoasVindasEmailLigado())
	})
	t.Run("emissor que não diz (um falso de teste) vale como ligado", func(t *testing.T) {
		require.True(t, (&Servico{BoasVindas: &emissorFalso{}}).BoasVindasEmailLigado())
	})
	t.Run("emissor que diz que não", func(t *testing.T) {
		require.False(t, (&Servico{BoasVindas: &emissorSemEmail{}}).BoasVindasEmailLigado())
	})
}

// mailerNulo satisfaz mailer.Mailer sem enviar nada.
type mailerNulo struct{}

func (mailerNulo) Send(context.Context, []string, string, string, string) error { return nil }

// emissorSemEmail implementa o opcional EmailLigado() devolvendo falso.
type emissorSemEmail struct{ emissorFalso }

func (*emissorSemEmail) EmailLigado() bool { return false }

// emissorIndisponivel implementa o opcional Disponivel() devolvendo falso.
type emissorIndisponivel struct{ emissorFalso }

func (*emissorIndisponivel) Disponivel() bool { return false }
