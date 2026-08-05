package auth

import (
	"context"
	"slices"

	"github.com/google/uuid"
)

// ContextWithClaims is exposed for tests (and any caller that wants to
// mount a context with pre-defined claims, e.g. integrated handler tests).
// In production, the RequireJWT middleware is what populates this.
func ContextWithClaims(ctx context.Context, c *Claims) context.Context {
	return context.WithValue(ctx, claimsKey, c)
}

// ClientScopesFromContext devolve a carteira de clientes do requester quando ele
// é viewer (cliente), ou nil quando é admin/operator/anônimo. Use em handlers que
// filtram listas por cliente.
//
// Convenção: nil = "sem escopo" = enxerga tudo (só admin/operator).
//
// Defense-in-depth: viewer sem nenhum cliente é token mal-formado (anterior à
// migration 0027 ou bug futuro). Devolve []uuid.UUID{uuid.Nil} pra toda query
// escopada dar resultado vazio, em vez de tratar como unscoped (acesso total).
//
// Compatibilidade: token emitido antes DESTE deploy traz apenas client_id e
// vale por até 8h. Traduzimos pra lista de um elemento — sem isso, todo cliente
// logado seria deslogado no deploy do backend. Remoção rastreada em F-126
// (docs/roadmap/follow-ups-fase2.md): depende de nenhum token pré-deploy estar
// mais vivo, não de uma data no calendário.
//
// A carteira é devolvida como CÓPIA: o slice das claims é dono do request
// inteiro, e a partir da Task 5 ele desce como argumento de query pro pgx em
// ~19 call sites. Um único caller que ordene ou dedupe in-place corromperia as
// claims pro resto do request. Cópia de meia dúzia de UUIDs por request é
// barata; rastrear quem pode mutar o quê, não.
func ClientScopesFromContext(ctx context.Context) []uuid.UUID {
	c, ok := ClaimsFromContext(ctx)
	if !ok {
		return nil
	}
	if c.Role != "viewer" {
		return nil
	}
	if len(c.ClientIDs) > 0 {
		return slices.Clone(c.ClientIDs)
	}
	if c.ClientID != nil {
		return []uuid.UUID{*c.ClientID}
	}
	return []uuid.UUID{uuid.Nil}
}

// ScopeAllows diz se o requester pode enxergar dados do cliente informado.
// Devolve true quando não há escopo (admin/operator). É o helper dos checks
// pontuais que respondem 404 anti-oracle.
//
// uuid.Nil nunca é autorizado, nem pro admin: clients.id é uuid_generate_v4()
// (migration 0001), então nenhum cliente real é zero. Sem esta guarda, o id
// zerado — que `uuid.Parse` aceita de um path param, e que também aparece em
// struct zerada — casaria com a sentinela de falha-fechada [uuid.Nil] que
// ClientScopesFromContext devolve pra token mal-formado. Barrar aqui vale por
// todos os call sites de uma vez.
func ScopeAllows(ctx context.Context, clientID uuid.UUID) bool {
	if clientID == uuid.Nil {
		return false
	}
	scopes := ClientScopesFromContext(ctx)
	if scopes == nil {
		return true
	}
	return slices.Contains(scopes, clientID)
}
