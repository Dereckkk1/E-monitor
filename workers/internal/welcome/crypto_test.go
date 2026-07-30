package welcome

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func testKeyHex(t *testing.T) string {
	t.Helper()
	b := make([]byte, keyLen)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return hex.EncodeToString(b)
}

func TestCipher_RoundTrip(t *testing.T) {
	c, err := NewCipher(testKeyHex(t))
	require.NoError(t, err)

	// Senha real do gerador: 16 chars com símbolos e acentuação improvável,
	// mas testamos UTF-8 mesmo assim porque nome/senha passam pelo mesmo path.
	for _, pwd := range []string{
		"Xk9#mQ2vLp7@Rt4z",
		"senha com espaço e ç",
		strings.Repeat("a", 512),
		"",
	} {
		blob, err := c.Encrypt(pwd)
		require.NoError(t, err)
		got, err := c.Decrypt(blob)
		require.NoError(t, err)
		require.Equal(t, pwd, got)
	}
}

func TestCipher_NonceIsFresh(t *testing.T) {
	c, err := NewCipher(testKeyHex(t))
	require.NoError(t, err)
	a, err := c.Encrypt("mesma-senha")
	require.NoError(t, err)
	b, err := c.Encrypt("mesma-senha")
	require.NoError(t, err)
	// Cifrar a mesma senha duas vezes não pode produzir o mesmo blob, senão
	// dá pra inferir que dois usuários compartilham senha só olhando o banco.
	require.NotEqual(t, a, b)
}

func TestCipher_WrongKeyFails(t *testing.T) {
	c1, err := NewCipher(testKeyHex(t))
	require.NoError(t, err)
	c2, err := NewCipher(testKeyHex(t))
	require.NoError(t, err)

	blob, err := c1.Encrypt("segredo")
	require.NoError(t, err)

	_, err = c2.Decrypt(blob)
	require.Error(t, err, "decifrar com outra chave tem que falhar")
}

func TestCipher_TamperedBlobFails(t *testing.T) {
	c, err := NewCipher(testKeyHex(t))
	require.NoError(t, err)
	blob, err := c.Encrypt("segredo")
	require.NoError(t, err)

	t.Run("bit flip no ciphertext", func(t *testing.T) {
		bad := append([]byte(nil), blob...)
		bad[len(bad)-1] ^= 0x01
		_, err := c.Decrypt(bad)
		require.Error(t, err, "GCM autentica: adulterar tem que ser detectado")
	})

	t.Run("truncado", func(t *testing.T) {
		_, err := c.Decrypt(blob[:4])
		require.Error(t, err)
	})

	t.Run("vazio", func(t *testing.T) {
		_, err := c.Decrypt(nil)
		require.Error(t, err)
	})
}

func TestNewCipher_KeyFormats(t *testing.T) {
	raw := make([]byte, keyLen)
	_, err := rand.Read(raw)
	require.NoError(t, err)

	for name, encoded := range map[string]string{
		"hex":            hex.EncodeToString(raw),
		"base64 std":     base64.StdEncoding.EncodeToString(raw),
		"base64 raw std": base64.RawStdEncoding.EncodeToString(raw),
		"base64 url":     base64.URLEncoding.EncodeToString(raw),
	} {
		t.Run(name, func(t *testing.T) {
			c, err := NewCipher(encoded)
			require.NoError(t, err)
			require.NotNil(t, c)
		})
	}
}

func TestNewCipher_RejectsBadKeys(t *testing.T) {
	// Chave curta demais é o erro provável de config (alguém gera 16 bytes).
	// Tem que falhar alto, não silenciosamente virar AES-128.
	short := hex.EncodeToString(make([]byte, 16))

	for name, key := range map[string]string{
		"vazia":        "",
		"curta":        short,
		"não é hex/b64": "isto não é uma chave!!",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewCipher(key)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrNoKey), "erro deve envolver ErrNoKey, veio %v", err)
		})
	}
}

func TestNewToken(t *testing.T) {
	seen := make(map[string]bool, 200)
	for i := 0; i < 200; i++ {
		tok, err := NewToken()
		require.NoError(t, err)
		// 32 bytes em base64url sem padding = 43 chars.
		require.Len(t, tok, 43)
		require.False(t, strings.ContainsAny(tok, "+/= "), "token tem que ser seguro em path de URL: %q", tok)
		require.False(t, seen[tok], "token repetido em 200 gerações")
		seen[tok] = true
	}
}

func TestFirstName(t *testing.T) {
	for in, want := range map[string]string{
		"Ana Maria Souza": "Ana",
		"Ana":             "Ana",
		"  Ana  Souza  ":  "Ana",
		"":                "",
		"   ":             "",
	} {
		require.Equal(t, want, firstName(in), "firstName(%q)", in)
	}
}
