package users

import (
	"context"
	"testing"
)

// TestActiveInternal_Signature fixa a API de ActiveInternal em tempo de
// compilação (method expression). O teste de dados real depende de
// TEST_DATABASE_URL e roda no fluxo de integração do scheduler.
func TestActiveInternal_Signature(t *testing.T) {
	var _ func(*Repo, context.Context) ([]User, error) = (*Repo).ActiveInternal
}
