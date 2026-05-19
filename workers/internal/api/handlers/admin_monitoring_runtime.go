package handlers

import (
	"runtime"
)

// readMemoryMB devolve heapUsedMB / heapTotalMB / rssMB para o overview.
// Não temos métrica nativa de RSS no runtime/Go — usamos a soma Sys como
// aproximação (memória total reservada para o processo, próxima ao RSS para
// workloads steady-state).
func readMemoryMB() (heapUsed, heapTotal, rss int) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	heapUsed = int(m.HeapAlloc / 1024 / 1024)
	heapTotal = int(m.HeapSys / 1024 / 1024)
	rss = int(m.Sys / 1024 / 1024)
	return
}

func goVersion() string {
	return runtime.Version()
}
