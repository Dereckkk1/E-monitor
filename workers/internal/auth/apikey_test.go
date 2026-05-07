package auth_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"radiocheck/internal/auth"
)

func TestGenerateAPIKey_HashConsistency(t *testing.T) {
	raw, hash := auth.GenerateAPIKey()
	assert.Len(t, raw, 64)  // 32 bytes as hex
	assert.Len(t, hash, 64) // SHA-256 as hex
	// Verify hash matches raw
	sum := sha256.Sum256([]byte(raw))
	assert.Equal(t, hash, hex.EncodeToString(sum[:]))
}

func TestGenerateAPIKey_Unique(t *testing.T) {
	_, h1 := auth.GenerateAPIKey()
	_, h2 := auth.GenerateAPIKey()
	assert.NotEqual(t, h1, h2)
}
