// Package welcome implementa o convite de boas-vindas: o link público que o
// usuário recém-criado abre a partir do email pra ver as credenciais iniciais
// e o tutorial da plataforma. Ver docs/features/welcome-onboarding.md.
package welcome

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// ErrNoKey é devolvido quando a chave de cifra não foi configurada. O chamador
// deve tratar como "não dá pra enviar boas-vindas", nunca como "grava em texto
// claro": a ausência de chave é falha de configuração, não modo degradado.
var ErrNoKey = errors.New("welcome: WELCOME_ENC_KEY ausente ou inválida")

// keyLen é o tamanho exigido: AES-256.
const keyLen = 32

// Cipher cifra e decifra a senha inicial do convite com AES-256-GCM.
// O formato do blob é nonce(12B) || ciphertext || tag(16B).
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher constrói o Cipher a partir do valor bruto de WELCOME_ENC_KEY.
// Aceita a chave em hex (64 chars) ou base64 (std ou url, com ou sem padding);
// qualquer outra coisa é erro. Chave vazia devolve (nil, ErrNoKey) pra que o
// chamador consiga subir a API sem a feature e recusar o envio caso a caso.
func NewCipher(raw string) (*Cipher, error) {
	if raw == "" {
		return nil, ErrNoKey
	}
	key, err := decodeKey(raw)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("welcome: aes.NewCipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("welcome: cipher.NewGCM: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

func decodeKey(raw string) ([]byte, error) {
	if b, err := hex.DecodeString(raw); err == nil && len(b) == keyLen {
		return b, nil
	}
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(raw); err == nil && len(b) == keyLen {
			return b, nil
		}
	}
	return nil, fmt.Errorf("%w: esperado 32 bytes em hex ou base64", ErrNoKey)
}

// Encrypt cifra a senha. Cada chamada gera um nonce novo, então cifrar a mesma
// senha duas vezes produz blobs diferentes.
func (c *Cipher) Encrypt(plaintext string) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("welcome: nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

// Decrypt recupera a senha. Falha se o blob foi truncado, adulterado ou foi
// cifrado com outra chave — GCM autentica, não só cifra.
func (c *Cipher) Decrypt(blob []byte) (string, error) {
	ns := c.aead.NonceSize()
	if len(blob) < ns {
		return "", errors.New("welcome: blob cifrado truncado")
	}
	plain, err := c.aead.Open(nil, blob[:ns], blob[ns:], nil)
	if err != nil {
		return "", fmt.Errorf("welcome: decrypt: %w", err)
	}
	return string(plain), nil
}

// NewToken gera o token opaco da URL: 32 bytes de aleatoriedade cripto-segura
// em base64url sem padding (43 chars, seguro em path de URL).
func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", fmt.Errorf("welcome: token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
