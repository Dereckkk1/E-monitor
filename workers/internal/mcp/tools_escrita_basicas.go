package mcp

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Escritas básicas (Lote C, Tasks 11 e 12): as que mexem num registro só e,
// salvo a troca de tipo de material com veiculação, não atingem o passado.

// registrarEscritasBasicas registra as tools da Task 11 (livres) e, a seguir, as
// da Task 12.
func registrarEscritasBasicas(s *sdk.Server, cfg ConfigServidor, p *Principal) {
	ferramentaEscrita(s, cfg, p, escritaMarcarNotificacoesLidas())
	ferramentaEscrita(s, cfg, p, escritaReconhecerSimilaridade())
}

// ── marcar_notificacoes_lidas ───────────────────────────────────────────

const tetoNotificacoesMarcadas = 200

// formaDaChaveDeNotificacao: a chave do sininho é 'campaign_failure:<uuid>:<dia>'
// (catalog.Notifications.List). Chave inventada não corresponde a nenhum item e
// só entupiria notification_reads.
var formaDaChaveDeNotificacao = regexp.MustCompile(
	`^campaign_failure:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}:[0-9]{4}-[0-9]{2}-[0-9]{2}$`)

type entradaMarcarNotificacoes struct {
	Notificacoes []string `json:"notificacoes,omitempty" jsonschema:"As chaves (campo chave de cada item de minhas_notificacoes) a marcar como lidas, até 200. Passe isto OU todas, nunca os dois. Nunca invente chaves: copie as que minhas_notificacoes devolveu."`
	Todas        bool     `json:"todas,omitempty" jsonschema:"true para marcar como lidas todas as notificações da janela atual (últimos 7 dias fechados, as que têm déficit), mesmo as que passam do teto de 50 do sininho. Exclusivo com notificacoes."`
}

type saidaMarcadas struct {
	Marcadas int `json:"marcadas"`
}

// chavesDasNotificacoes valida a entrada: exatamente uma das duas formas.
func chavesDasNotificacoes(in entradaMarcarNotificacoes) ([]string, error) {
	if in.Todas && len(in.Notificacoes) > 0 {
		return nil, errEntrada("passe notificacoes OU todas, não os dois")
	}
	if in.Todas {
		return nil, nil
	}
	if len(in.Notificacoes) == 0 {
		return nil, errEntrada("passe a lista notificacoes (as chaves que minhas_notificacoes devolveu) ou todas: true")
	}
	if len(in.Notificacoes) > tetoNotificacoesMarcadas {
		return nil, errEntrada(fmt.Sprintf("no máximo %d notificações por chamada; para marcar tudo use todas: true", tetoNotificacoesMarcadas))
	}
	vistas := make(map[string]bool, len(in.Notificacoes))
	out := make([]string, 0, len(in.Notificacoes))
	for _, k := range in.Notificacoes {
		if !formaDaChaveDeNotificacao.MatchString(k) {
			return nil, errEntrada(fmt.Sprintf("chave de notificação inválida (%q): use exatamente o campo chave que minhas_notificacoes devolveu; nunca invente", recorte(k, 80)))
		}
		if !vistas[k] {
			vistas[k] = true
			out = append(out, k)
		}
	}
	return out, nil
}

func escritaMarcarNotificacoesLidas() Escrita[entradaMarcarNotificacoes] {
	return Escrita[entradaMarcarNotificacoes]{
		Nome: "marcar_notificacoes_lidas",
		Descricao: "Marca como lidas as notificações do sininho de quem está conectado (só a leitura dele muda; as de outros " +
			"usuários não). Use depois de minhas_notificacoes: passe as chaves em notificacoes (até 200) ou todas: true para " +
			"marcar a janela inteira. Não apaga nada e repetir é inofensivo. Nunca invente chaves: use as de minhas_notificacoes.",
		Nivel: Livre,
		Recurso: func(entradaMarcarNotificacoes) (string, *uuid.UUID) {
			return "notification", nil
		},
		Executar: func(ctx context.Context, E *Escritas, p *Principal, in entradaMarcarNotificacoes) (feito, error) {
			chaves, err := chavesDasNotificacoes(in)
			if err != nil {
				return feito{}, err
			}
			var n int
			if in.Todas {
				n, err = E.Leitura.Notificacoes.MarkAllReadInWindow(ctx, p.UserID)
			} else {
				n, err = E.Leitura.Notificacoes.MarkRead(ctx, p.UserID, chaves)
			}
			if err != nil {
				return feito{}, err
			}
			f := feito{Resultado: saidaMarcadas{Marcadas: n}, Recurso: "notification"}
			if !in.Todas && n < len(chaves) {
				f.Ressalvas = []string{fmt.Sprintf("%d das %d notificações já estavam marcadas como lidas.", len(chaves)-n, len(chaves))}
			}
			return f, nil
		},
	}
}

// ── reconhecer_similaridade_material ────────────────────────────────────

func escritaReconhecerSimilaridade() Escrita[entradaMaterial] {
	return Escrita[entradaMaterial]{
		Nome: "reconhecer_similaridade_material",
		Descricao: "Reconhece o aviso de duplicata de um material (o mesmo do botão \"Manter assim mesmo\" do wizard): registra que a " +
			"pessoa viu que ele é muito parecido com outro material do mesmo cliente e decidiu manter os dois. A armadilha: reconhecer " +
			"NÃO resolve a ambiguidade — dois materiais quase iguais na mesma emissora, em campanhas diferentes, continuam fazendo a " +
			"tocada cair na campanha errada. Só use depois que a pessoa decidiu manter os dois. Só vale para material com aviso " +
			"pendente (obter_material mostra a ressalva). Nunca invente o material_id: pergunte.",
		Nivel: Livre,
		Recurso: func(in entradaMaterial) (string, *uuid.UUID) {
			if id, err := uuid.Parse(in.MaterialID); err == nil {
				return "material", &id
			}
			return "material", nil
		},
		Executar: func(ctx context.Context, E *Escritas, _ *Principal, in entradaMaterial) (feito, error) {
			id, err := uuidDaEntrada("material_id", in.MaterialID)
			if err != nil {
				return feito{}, err
			}
			m, err := E.Leitura.Materiais.Get(ctx, id)
			if errors.Is(err, pgx.ErrNoRows) {
				// A rota devolve 204 para um id que não existe; a tool não finge que fez.
				return feito{}, errNaoEncontrado("material")
			}
			if err != nil {
				return feito{}, err
			}
			var ressalvas []string
			switch {
			case m.SimilarityAckdAt != nil:
				ressalvas = []string{"O aviso de similaridade já estava reconhecido; nada mudou (o instante do reconhecimento original foi mantido)."}
			case !avisoDeSimilaridadePendente(*m):
				// Reconhecer de antemão esconderia, sem ninguém ver, o aviso que a
				// checagem (ainda em andamento) pode gerar.
				return feito{}, errEntrada("este material não tem aviso de similaridade pendente (a checagem precisa estar pronta e o score em 50% ou mais); não há o que reconhecer")
			default:
				if err := E.Acoes.Materiais.Acknowledge(ctx, id); err != nil {
					return feito{}, err
				}
			}
			saida, err := lerMaterialCompleto(ctx, E.Leitura, id)
			if err != nil {
				return feito{}, err
			}
			return feito{Resultado: saida, Recurso: "material", RecursoID: &id, Ressalvas: ressalvas}, nil
		},
	}
}
