package handlers

import (
	"errors"
	"net/http"

	"radiocheck/internal/acoes"
)

// escreveRecusa traduz uma recusa de negócio de internal/acoes na resposta HTTP
// que a rota sempre deu: o status de cada código e, onde a rota já tinha texto,
// o MESMO texto (as ações guardam em Recusa.Mensagem o texto antigo das
// validações que saíram do handler). Devolve false quando o erro não é uma
// Recusa — quem chama então responde 500, como sempre.
func escreveRecusa(w http.ResponseWriter, err error) bool {
	var r *acoes.Recusa
	if !errors.As(err, &r) {
		return false
	}
	switch r.Codigo {
	case acoes.CodigoNaoEncontrado:
		http.Error(w, "not found", http.StatusNotFound)
	case acoes.CodigoCNPJDuplicado:
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":     acoes.CodigoCNPJDuplicado,
			"existente": r.Dados["existente"],
		})
	case acoes.CodigoEstadoTerminal:
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":          "campaign already in terminal state",
			"current_status": r.Dados["estado_atual"],
		})
	case acoes.CodigoCampanhaComProjecoes:
		corpo := map[string]any{"error": "campaign_has_foreign_projections"}
		if n, ok := r.Dados["foreign_projections"]; ok {
			corpo["foreign_projections"] = n
		}
		writeJSON(w, http.StatusConflict, corpo)
	case acoes.CodigoClienteComVinculos:
		// Corpo genérico: a rota de apagar cliente monta o dela, com as contagens.
		writeJSON(w, http.StatusConflict, map[string]any{"error": "client_has_dependents"})
	case acoes.CodigoMaterialVinculado,
		acoes.CodigoCampanhaComLoteManual,
		acoes.CodigoCampanhaEmPosVenda:
		writeJSON(w, http.StatusConflict, map[string]any{"error": r.Codigo})
	case acoes.CodigoMaterialNaoVinculado:
		http.Error(w, r.Mensagem, http.StatusUnprocessableEntity)
	case acoes.CodigoHub:
		http.Error(w, r.Mensagem, http.StatusUnprocessableEntity)
	default: // entrada_invalida e o que vier a ser acrescentado: o pedido é que está errado.
		http.Error(w, r.Mensagem, http.StatusBadRequest)
	}
	return true
}
