package auth_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"radiocheck/internal/auth"
)

// TestEnsureAdmin_NoOp_EmptyCreds: when either env var is unset, EnsureAdmin
// must be a silent no-op so missing config never blocks API startup.
// pool=nil proves the function returns BEFORE touching the database.
func TestEnsureAdmin_NoOp_EmptyCreds(t *testing.T) {
	ctx := context.Background()
	cases := []auth.BootstrapConfig{
		{Email: "", Password: ""},
		{Email: "admin@example.com", Password: ""},
		{Email: "", Password: "longenoughpassword"},
	}
	for _, c := range cases {
		err := auth.EnsureAdmin(ctx, nil, c, nil)
		assert.NoError(t, err, "config %+v should be no-op", c)
	}
}

// TestEnsureAdmin_RejectsBadEmail: malformed email is caught before any DB
// access, again exercised with pool=nil.
func TestEnsureAdmin_RejectsBadEmail(t *testing.T) {
	ctx := context.Background()
	err := auth.EnsureAdmin(ctx, nil, auth.BootstrapConfig{
		Email:    "not-an-email",
		Password: "somethinglongenough",
	}, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "@")
}
