package mcp

import "radiocheck/internal/users"

// papelDe traduz o papel do banco para o vocabulário da tela: viewer é "cliente".
func papelDe(role string) string {
	if role == "viewer" {
		return "cliente"
	}
	return role
}

// situacaoDe: apagado (soft delete) > inativo > ativo.
func situacaoDe(u users.User) string {
	switch {
	case u.DeletedAt != nil:
		return "apagado"
	case !u.IsActive:
		return "inativo"
	default:
		return "ativo"
	}
}
