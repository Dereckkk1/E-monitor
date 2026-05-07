package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

// BootstrapConfig holds the parameters for the idempotent admin user
// bootstrap. Email and Password come from environment variables and may be
// empty (in which case EnsureAdmin is a no-op). Role defaults to "admin"
// when empty.
type BootstrapConfig struct {
	Email    string
	Password string
	Role     string
}

// Minimum password length for the bootstrap admin. The CHECK constraint on
// the users table only constrains the role; the length floor is enforced
// here so a misconfigured env var does not silently create a weak account.
const bootstrapMinPasswordLen = 12

// EnsureAdmin guarantees that the user identified by cfg.Email exists in
// the users table.
//
// Behaviour:
//   - When Email or Password is empty, returns nil (silent no-op).
//   - When the email already exists, logs "bootstrap admin already exists"
//     and returns nil. The password is NOT updated; rotation is an explicit
//     SQL operation (see docs/auth-bootstrap.md).
//   - When the email does not exist, validates the password length, hashes
//     it with bcrypt cost 10, and inserts the row. Logs the event with a
//     masked email (first character + domain).
//
// EnsureAdmin is intended to run once on every API startup so a fresh
// install can be unblocked without manual SQL. Operators should remove
// RADIOCHECK_BOOTSTRAP_ADMIN_PASSWORD from the environment after the first
// successful boot.
func EnsureAdmin(ctx context.Context, pool *pgxpool.Pool, cfg BootstrapConfig, log *zap.Logger) error {
	if cfg.Email == "" || cfg.Password == "" {
		return nil
	}
	if !strings.Contains(cfg.Email, "@") {
		return errors.New("bootstrap admin email must contain '@'")
	}
	role := cfg.Role
	if role == "" {
		role = "admin"
	}

	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM users WHERE email = $1)`, cfg.Email,
	).Scan(&exists); err != nil {
		return fmt.Errorf("check existing user: %w", err)
	}
	if exists {
		if log != nil {
			log.Info("bootstrap admin already exists",
				zap.String("email", maskEmail(cfg.Email)))
		}
		return nil
	}

	if len(cfg.Password) < bootstrapMinPasswordLen {
		return fmt.Errorf("password too short: must be >= %d chars", bootstrapMinPasswordLen)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(cfg.Password), 10)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (email, password_hash, role) VALUES ($1, $2, $3)`,
		cfg.Email, string(hash), role,
	); err != nil {
		return fmt.Errorf("insert user: %w", err)
	}
	if log != nil {
		log.Info("bootstrap admin created",
			zap.String("email", maskEmail(cfg.Email)),
			zap.String("role", role))
	}
	return nil
}

// maskEmail returns "<first-char>***@<domain>". For "alice@example.com" it
// produces "a***@example.com". When the email lacks an "@" or has an empty
// local part, the input is returned unchanged for diagnostics.
func maskEmail(email string) string {
	at := strings.IndexByte(email, '@')
	if at <= 0 {
		return email
	}
	return email[:1] + "***" + email[at:]
}
