package acoes

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"radiocheck/internal/catalog"
)

// DefinirAtivoCliente desativa (ativo=false, reversível) ou reativa o cliente. O
// cliente fica no banco com campanhas, materiais e usuários intactos; desativado
// some da lista de gestão e seus usuários não entram mais. Cliente inexistente:
// nao_encontrado.
func (s *Servico) DefinirAtivoCliente(ctx context.Context, id uuid.UUID, ativo bool) (*catalog.Client, error) {
	out, err := s.Clientes.SetActive(ctx, id, ativo)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, naoEncontrado("not found")
		}
		return nil, err
	}
	return out, nil
}

// ApagarCliente apaga o cliente de vez. Cliente inexistente: nao_encontrado.
// Com campanhas, materiais ou usuários apontando para ele (FK): recusa
// cliente_com_vinculos, com a contagem de cada um em Dados ("campanhas",
// "materiais", "usuarios") para quem monta a resposta oferecer o caminho
// reversível (desativar). A contagem é best-effort: se falhar, a recusa sai com
// zeros — o conflito é real de qualquer forma.
func (s *Servico) ApagarCliente(ctx context.Context, id uuid.UUID) error {
	err := s.Clientes.Delete(ctx, id)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pgx.ErrNoRows):
		return naoEncontrado("not found")
	case errors.Is(err, catalog.ErrClientHasDependents):
		cont, cerr := s.Clientes.CountDependents(ctx, id)
		if cerr != nil {
			cont = catalog.DependentCounts{}
		}
		return &Recusa{
			Codigo:   CodigoClienteComVinculos,
			Mensagem: "o cliente ainda tem campanhas, materiais ou usuários vinculados; desative-o em vez de apagar",
			Dados: map[string]any{
				"campanhas": cont.Campaigns,
				"materiais": cont.Materials,
				"usuarios":  cont.Users,
			},
		}
	default:
		return err
	}
}
