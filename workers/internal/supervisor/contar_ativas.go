package supervisor

import "github.com/google/uuid"

// ContarAtivas: quantas emissoras de ids têm worker ativo agora — o
// "monitorando agora" da Visão Gerencial. O handler HTTP e o MCP chamam esta
// função; a coluna stations.health_status não é populada, então a fonte é o
// supervisor (mesma da /operations).
func ContarAtivas(statuses []WorkerStatus, ids []uuid.UUID) int {
	ativas := make(map[string]bool, len(statuses))
	for _, ws := range statuses {
		if ws.Active {
			ativas[ws.StationID] = true
		}
	}
	n := 0
	for _, id := range ids {
		if ativas[id.String()] {
			n++
		}
	}
	return n
}
