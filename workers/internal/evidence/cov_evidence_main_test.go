package evidence

import (
	"os"
	"testing"
)

// TestMain permite que este binário de teste faça o papel de "ffmpeg falso"
// quando re-executado com COV_E_FAKE_FFMPEG=1 (ver cov_evidence_helpers_test.go).
// Os testes de cobertura nunca invocam o ffmpeg de verdade. Fora disso roda a
// suíte normalmente.
func TestMain(m *testing.M) {
	if os.Getenv(covEEnvFake) == "1" {
		covERunFakeFFmpeg()
		return
	}
	code := m.Run()
	if covEBinDir != "" {
		_ = os.RemoveAll(covEBinDir)
	}
	os.Exit(code)
}
