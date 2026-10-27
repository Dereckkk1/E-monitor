// Package oauth é o authorization server OAuth 2.1 do MCP (spec §4.2):
// descoberta, registro dinâmico, authorize, consentimento e token.
package oauth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"regexp"
)

var (
	reChallenge = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	reVerifier  = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)
)

func desafioS256(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func conferirPKCE(verifier, challenge string) bool {
	if !reVerifier.MatchString(verifier) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(desafioS256(verifier)), []byte(challenge)) == 1
}
