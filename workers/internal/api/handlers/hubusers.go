package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// ListUsers atende `GET /v1/internal/hub/users` — a listagem que o hub pagina
// na reconciliação noturna do RFC-001 §9.6.
//
// # Por que existe, se o sync já empurra tudo
//
// Porque empurrar é uma promessa sobre o FUTURO, e a reconciliação é uma
// pergunta sobre o PRESENTE. Um evento que morreu na DLQ há duas semanas, um
// `UPDATE` feito à mão no banco daqui, uma conta desativada localmente pelo
// admin do E-monitor: nada disso passa pelo outbox, e o hub não teria como
// saber. Esta rota é o que permite comparar em vez de confiar.
//
// Mesma autenticação do `POST /hub/sync` — a chave de plataforma. É leitura de
// dado pessoal (nome, e-mail, situação) e por isso não fica atrás de nada mais
// fraco que a porta que já existe.
//
// # O escopo é deliberadamente estreito
//
// Devolve apenas o que o §9.6 compara: identidade e vínculo. NÃO devolve
// `client_id`, permissões nem qualquer coisa de autorização — isso é do
// E-monitor e o hub nunca toca (decisão D9). Uma listagem generosa aqui viraria,
// com o tempo, uma API de exportação de base que ninguém decidiu criar.
func (h *HubSyncHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	if h.hub == nil || !h.hub.ChaveConfere(r.Header.Get("X-Hub-Platform-Key")) {
		http.Error(w, "invalid_platform_key", http.StatusUnauthorized)
		return
	}

	// Cursor por `id`, não por `created_at`: duas contas criadas no mesmo
	// instante fariam a paginação por data pular ou repetir linha — e numa
	// reconciliação, pular linha é exatamente o defeito que ela existe para
	// achar.
	cursor := r.URL.Query().Get("cursor")
	limite := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limite = n
		}
	}

	sql := `SELECT id, email, name, is_active, hub_id, role
	          FROM users
	         WHERE deleted_at IS NULL`
	args := []any{}
	if cursor != "" {
		sql += ` AND id > $1`
		args = append(args, cursor)
	}
	sql += ` ORDER BY id LIMIT ` + strconv.Itoa(limite+1)

	rows, err := h.db.Query(r.Context(), sql, args...)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type linha struct {
		ExternalID string  `json:"externalId"`
		Email      string  `json:"email"`
		Name       string  `json:"name"`
		Active     bool    `json:"active"`
		HubUserID  *string `json:"hubUserId"`
		Role       string  `json:"role"`
	}
	out := make([]linha, 0, limite)
	for rows.Next() {
		var l linha
		if err := rows.Scan(&l.ExternalID, &l.Email, &l.Name, &l.Active, &l.HubUserID, &l.Role); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		out = append(out, l)
	}
	if rows.Err() != nil {
		// Erro no meio da varredura devolve página TRUNCADA se ignorado — e uma
		// página truncada faz a reconciliação concluir que as contas que faltam
		// sumiram da plataforma, marcando gente em erro por engano.
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Pediu-se um a mais só para saber se há próxima página, sem COUNT.
	var proximo *string
	if len(out) > limite {
		out = out[:limite]
		p := out[len(out)-1].ExternalID
		proximo = &p
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"users":      out,
		"nextCursor": proximo,
	})
}
