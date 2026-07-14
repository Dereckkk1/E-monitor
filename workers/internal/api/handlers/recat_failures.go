package handlers

import (
	"go.uber.org/zap"

	"radiocheck/internal/metrics"
)

// recordRecatFailure torna visível a falha de uma recategorização best-effort
// (goroutine disparada pelos handlers de rule/override/material). Antes o erro
// era engolido (`_ =`) e a categoria ficava velha em silêncio — spec 2026-07-14
// §3-T2. O reconciler (projrecon) cura o dado; isto aqui denuncia a causa.
// Usa zap.L() (global) porque os handlers não carregam logger próprio.
func recordRecatFailure(origin string, err error) {
	if err == nil {
		return
	}
	metrics.RecategorizeFailures.WithLabelValues(origin).Inc()
	zap.L().Error("recategorização best-effort falhou",
		zap.String("origin", origin), zap.Error(err))
}
