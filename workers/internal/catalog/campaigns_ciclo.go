package catalog

import "time"

// EstadoPeloCiclo diz em que estado o LifecycleScheduler deixa uma campanha: o
// resultado de UMA rodada de PromoteScheduledLifecycle sobre ela, dado o estado
// gravado, as datas e o dia de hoje (America/Sao_Paulo).
//
// É a versão PURA (sem banco) das quatro regras de data do SQL, na MESMA ordem
// em que a transação as aplica:
//
//  1. RECOVERY: concluida com fim ≥ hoje volta a ativa (início ≤ hoje) ou
//     programada;
//  2. DEMOTE: ativa com início > hoje volta a programada;
//  3. programada com início ≤ hoje vira ativa;
//  4. ativa com fim < hoje vira concluida — inclusive a que o passo 3 acabou de
//     ativar (programada com a janela inteira no passado passa por ativa e
//     termina concluida na mesma rodada).
//
// cancelada nunca sai do lugar: só as rotas de ciclo de vida mexem nela.
//
// Quem usa: o MCP, para saber se editar as datas de uma campanha muda o estado
// que o agendador vai dar a ela (então atinge o que o cliente vê). ⚠️ Mexeu no
// SQL de PromoteScheduledLifecycle? Mexa aqui também: o teste
// TestEstadoPeloCiclo_BateComOSQL roda o SQL de verdade sobre todas as
// combinações de estado × datas e compara com esta função.
//
// inicio, fim e hoje são datas de calendário (a hora é ignorada).
func EstadoPeloCiclo(estado string, inicio, fim, hoje time.Time) string {
	inicio, fim, hoje = soData(inicio), soData(fim), soData(hoje)
	switch estado {
	case "concluida": // 1. RECOVERY
		if fim.Before(hoje) {
			return "concluida"
		}
		if inicio.After(hoje) {
			return "programada"
		}
		return "ativa" // fim ≥ hoje: o passo 4 não a alcança
	case "ativa":
		if inicio.After(hoje) { // 2. DEMOTE
			return "programada"
		}
		if fim.Before(hoje) { // 4.
			return "concluida"
		}
		return "ativa"
	case "programada":
		if inicio.After(hoje) {
			return "programada"
		}
		if fim.Before(hoje) { // 3. + 4. na mesma rodada
			return "concluida"
		}
		return "ativa" // 3.
	}
	return estado
}

// soData devolve a data de calendário de t (meia-noite UTC), sem converter fuso:
// os DATE do Postgres já chegam como data de calendário.
func soData(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
