package similarity

import (
	"os"
	"testing"
)

// TestMain permite que este binário de teste faça o papel de "ffmpeg falso"
// quando re-executado com COV_M_FAKE_FFMPEG=1 (ver cov_similarity_helpers_test.go).
// Fora disso roda a suíte normalmente.
func TestMain(m *testing.M) {
	if os.Getenv(covMEnvFake) == "1" {
		covMRunFakeFFmpeg()
		return
	}
	code := m.Run()
	if covMBinDir != "" {
		_ = os.RemoveAll(covMBinDir)
	}
	os.Exit(code)
}
