package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"unicode/utf8"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// minhas_pendentes: os pedidos de escrita crítica que ESTE principal fez e o
// estado de cada um. É como o modelo descobre se uma pessoa já aprovou antes de
// dizer "feito": só `executada` significa feito. O dono é sempre o usuário do
// token (nunca um parâmetro). args, resultado e token_id nunca saem.

const (
	tetoResumoPendente = 250
	tetoErroLista      = 250
	tetoMotivoLista    = 150
	// tetoRessalvasLista é o orçamento (caracteres e bytes) de TODAS as ressalvas de um item, e
	// tetoRessalvaLista o de cada uma: a lista de 50 pedidos tem de caber em ~60 KB. Só o pedido
	// `executada` tem ressalvas (e então não tem erro), então elas ocupam o lugar do erro.
	tetoRessalvasLista = 300
	tetoRessalvaLista  = 250
)

// estadosDoFiltroPendente: filtro da tool → estados do store. nil = todos.
var estadosDoFiltroPendente = map[string][]string{
	"":          {"pendente", "aprovada"},
	"pendente":  {"pendente"},
	"aprovada":  {"aprovada"},
	"executada": {"executada"},
	"falhou":    {"falhou"},
	"recusada":  {"recusada"},
	"expirada":  {"expirada"},
	"todos":     nil,
}

type entradaMinhasPendentes struct {
	Estado string `json:"estado,omitempty" jsonschema:"pendente, aprovada, executada, falhou, recusada, expirada ou todos. Sem filtro: só os que ainda estão abertos (pendente e aprovada)."`
	Paginacao
}

type pedidoListaDTO struct {
	ID          string  `json:"id"`
	Tool        string  `json:"tool"`
	Estado      string  `json:"estado"`
	Motivo      string  `json:"motivo"`
	Resumo      string  `json:"resumo"`
	CriadaEm    string  `json:"criada_em"`
	ExpiraEm    string  `json:"expira_em"`
	AprovadaEm  *string `json:"aprovada_em"`
	ExecutadaEm *string `json:"executada_em"`
	Erro        *string `json:"erro"`
	// Ressalvas são os avisos que a execução gravou ("o email não saiu: a pessoa não tem a senha").
	// Sempre um array; só o pedido `executada` os tem. Cortadas para a lista caber (ressalvasDaLista).
	Ressalvas []string `json:"ressalvas"`
	// AprovarEm só existe enquanto o pedido espera aprovação.
	AprovarEm *string `json:"aprovar_em"`
}

// ressalvasDaLista corta as ressalvas gravadas do pedido para a lista: cada uma em tetoRessalvaLista
// e todas somadas em tetoRessalvasLista; as que não couberem viram "e mais N". Sempre devolve um array.
func ressalvasDaLista(rs []string) []string {
	out := make([]string, 0, len(rs))
	resta := tetoRessalvasLista
	for i, r := range rs {
		if resta <= 0 {
			out = append(out, fmt.Sprintf("… e mais %d ressalva(s): veja o pedido em /pendentes", len(rs)-i))
			break
		}
		c, _ := cortarTexto(r, min(tetoRessalvaLista, resta))
		out = append(out, c)
		resta -= max(utf8.RuneCountInString(c), len(c))
	}
	return out
}

func pendenteDe(cfg ConfigServidor, p Pendente) pedidoListaDTO {
	d := pedidoListaDTO{
		ID: p.ID.String(), Tool: p.Tool, Estado: p.Estado,
		CriadaEm: instante(p.CriadaEm), ExpiraEm: instante(p.ExpiraEm),
		AprovadaEm: instanteOpc(p.AprovadaEm), ExecutadaEm: instanteOpc(p.ExecutadaEm),
		Ressalvas: ressalvasDaLista(p.Ressalvas),
	}
	d.Motivo, _ = cortarTexto(p.Motivo, tetoMotivoLista)
	d.Resumo, _ = cortarTexto(p.Preview.Resumo, tetoResumoPendente)
	if p.Erro != nil {
		e, _ := cortarTexto(*p.Erro, tetoErroLista)
		d.Erro = &e
	}
	if p.Estado == "pendente" {
		d.AprovarEm = cfg.linkTela("/pendentes", url.Values{"id": {p.ID.String()}})
	}
	return d
}

func registrarPendentes(s *sdk.Server, cfg ConfigServidor, p *Principal) {
	ferramentaLeitura(s, cfg, "minhas_pendentes",
		"Lista os pedidos de escrita crítica que você fez e o estado de cada um (pendente, aprovada, executada, falhou, "+
			"recusada, expirada). Use para saber se uma pessoa já aprovou antes de dizer que algo foi feito: só `executada` "+
			"significa feito. Sem filtro, mostra só os abertos (pendente e aprovada); estado=todos traz o histórico. "+
			"Do mais novo para o mais velho; lista paginada (máximo 50 por página).",
		func(ctx context.Context, _ *Leitura, in entradaMinhasPendentes) (*sdk.CallToolResult, error) {
			estados, okE := estadosDoFiltroPendente[in.Estado]
			if !okE {
				return recusa("estado desconhecido: use pendente, aprovada, executada, falhou, recusada, expirada ou todos", "entrada_invalida")
			}
			if cfg.Escritas == nil || cfg.Escritas.Pendentes == nil || p == nil {
				return nil, errors.New("mcp: store de pendentes não configurado")
			}
			limit, offset := in.Paginacao.normalizar()
			limit = min(limit, tetoListaPendente)
			itens, total, err := cfg.Escritas.Pendentes.Listar(ctx, p.UserID, estados, limit, offset)
			if err != nil {
				return nil, err
			}
			dtos := make([]pedidoListaDTO, len(itens))
			for i, it := range itens {
				dtos[i] = pendenteDe(cfg, it)
			}
			return ok(novaLista(dtos, total, limit, offset))
		})
}
