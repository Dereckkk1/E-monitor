package catalog

import "time"

// TodaySaoPaulo devolve o dia corrente em America/Sao_Paulo como date-only
// (meia-noite UTC daquele dia de calendário). É o "hoje" do consolidado
// mensal em /insights e /campaigns/financials — e do MCP, que tem de dar o
// mesmo número que as telas.
func TodaySaoPaulo() time.Time {
	n := time.Now().In(spLoc)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
}
