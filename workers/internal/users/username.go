package users

import (
	"errors"
	"regexp"
	"strings"
)

// ErrInvalidUsername é devolvido por NormalizeUsername quando o valor não cabe
// no formato. O handler traduz para 400 invalid_username.
var ErrInvalidUsername = errors.New("invalid username")

// usernamePattern espelha o CHECK users_username_format da migration 0070 —
// mexeu num, mexa no outro. Sem `@` de propósito: é o que separa, no login, um
// username de um e-mail.
var usernamePattern = regexp.MustCompile(`^[a-z0-9._-]{3,32}$`)

// NormalizeUsername tira espaços das pontas, passa pra minúsculas e valida o
// formato (3–32 caracteres: a-z, 0-9, ponto, hífen, sublinhado). Devolve "" sem
// erro para entrada vazia — quem chama decide se vazio significa "sem username"
// (criação) ou "remover" (edição).
func NormalizeUsername(raw string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return "", nil
	}
	if !usernamePattern.MatchString(s) {
		return "", ErrInvalidUsername
	}
	return s, nil
}
