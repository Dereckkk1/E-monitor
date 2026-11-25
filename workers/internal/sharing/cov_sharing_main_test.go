package sharing

import (
	"os"
	"testing"
)

// TestMain permite que este binário de teste faça o papel de "ffmpeg falso"
// quando re-executado com COV_H_FAKE_FFMPEG=1 (ver cov_sharing_helpers_test.go).
// Fora disso roda a suíte normalmente.
func TestMain(m *testing.M) {
	if os.Getenv(covHEnvFake) == "1" {
		covHRunFakeFFmpeg()
		return
	}
	code := m.Run()
	if covHBinDir != "" {
		_ = os.RemoveAll(covHBinDir)
	}
	os.Exit(code)
}
