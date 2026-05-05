package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadFromEnv(t *testing.T) {
	os.Setenv("DATABASE_URL", "postgres://u:p@h/db")
	os.Setenv("NATS_URL", "nats://n:4222")
	os.Setenv("S3_ENDPOINT", "http://m:9000")
	os.Setenv("S3_BUCKET", "buck")
	os.Setenv("S3_ACCESS_KEY", "ak")
	os.Setenv("S3_SECRET_KEY", "sk")
	os.Setenv("S3_REGION", "us-east-1")
	os.Setenv("MASTERS_PATH", "/data/masters")
	os.Setenv("API_PORT", "8080")
	os.Setenv("REDIS_URL", "redis://r:6379")
	defer func() {
		for _, k := range []string{"DATABASE_URL", "NATS_URL", "S3_ENDPOINT", "S3_BUCKET", "S3_ACCESS_KEY", "S3_SECRET_KEY", "S3_REGION", "MASTERS_PATH", "API_PORT", "REDIS_URL"} {
			os.Unsetenv(k)
		}
	}()

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "postgres://u:p@h/db", cfg.DatabaseURL)
	require.Equal(t, "nats://n:4222", cfg.NATSURL)
	require.Equal(t, "buck", cfg.S3Bucket)
	require.Equal(t, "8080", cfg.APIPort)
}

func TestLoadMissingRequired(t *testing.T) {
	os.Unsetenv("DATABASE_URL")
	_, err := Load()
	require.Error(t, err)
}
