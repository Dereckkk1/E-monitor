package mcp

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"radiocheck/internal/acoes"
)

// Aprovar → executar → fechar (spec §5.2; hub executarPendente.ts), com a
// sessão de quem aprova — que é o dono: o store filtra por user_id.

// ErrExecucaoInterrompida: a execução caiu por erro inesperado; o pedido fica
// `falhou` e o texto do erro Go nunca sai do servidor.
var ErrExecucaoInterrompida = errors.New("mcp: execução interrompida")

const msgInterrompida = "A execução caiu no meio por um erro inesperado; confira se a ação aconteceu antes de pedir de novo."

type Aprovacoes struct {
	cfg    ConfigServidor
	indice map[string]escritaRegistravel
}

func NovasAprovacoes(cfg ConfigServidor) (*Aprovacoes, error) {
	return novasAprovacoesCom(cfg, escritasDe(cfg))
}

func novasAprovacoesCom(cfg ConfigServidor, lista []escritaRegistravel) (*Aprovacoes, error) {
	idx, err := indexarEscritas(lista)
	if err != nil {
		return nil, err
	}
	return &Aprovacoes{cfg: cfg, indice: idx}, nil
}

type Aprovado struct {
	Pendente  Pendente
	Resultado any
}

// auditarPendente grava as linhas mcp.pendente.* (recurso "mcp_pendente"). Se o
// pedido perdeu o token (FK SET NULL), token_id sai como o UUID zero.
func (a *Aprovacoes) auditarPendente(ctx context.Context, p *Principal, acao string, pend Pendente, extra map[string]any) {
	e := map[string]any{chaveMcpPendente: pend.ID.String()}
	for k, v := range extra {
		e[k] = v
	}
	id := pend.ID
	auditar(ctx, a.cfg, a.cfg.Escritas, p, acao, "mcp_pendente", &id, pend.Tool, Critico, pend.Args, e)
}

func (a *Aprovacoes) Aprovar(ctx context.Context, id uuid.UUID, ator *Principal) (Aprovado, error) {
	E := a.cfg.Escritas
	pend, err := E.Pendentes.Aprovar(ctx, id, ator.UserID, E.agora())
	if err != nil {
		return Aprovado{}, err
	}
	p := &Principal{UserID: ator.UserID, Email: ator.Email, Nome: ator.Nome}
	if pend.TokenID != nil {
		p.TokenID = *pend.TokenID
	}
	a.auditarPendente(ctx, p, "mcp.pendente.aprovada", pend, nil)

	ectx, cancela := context.WithTimeout(context.WithoutCancel(ctx), prazoExecucao)
	defer cancela()
	fim := context.WithoutCancel(ctx)

	var f feito
	if e, ok := a.indice[pend.Tool]; !ok {
		err = &acoes.Recusa{Codigo: "tool_desconhecida", Mensagem: "A ação " + pend.Tool + " não existe mais neste servidor."}
	} else {
		f, err = e.executarAprovada(ectx, E, p, pend.Args)
	}
	if err != nil {
		var r *acoes.Recusa
		if errors.As(err, &r) {
			if ok, merr := E.Pendentes.MarcarFalhaDeNegocio(fim, pend.ID, pend.Tentativa, r.Mensagem, E.agora()); merr != nil || !ok {
				a.cfg.log().Error("mcp: não consegui devolver o pedido a pendente", zap.String("pendente", pend.ID.String()), zap.Bool("ok", ok), zap.Error(merr))
			}
			a.auditarPendente(fim, p, "mcp.pendente.falhou", pend, map[string]any{"codigo": r.Codigo})
			return Aprovado{}, r
		}
		a.cfg.log().Error("mcp: execução da pendente aprovada falhou", zap.String("pendente", pend.ID.String()), zap.String("tool", pend.Tool), zap.Error(err))
		if ok, merr := E.Pendentes.MarcarInterrompida(fim, pend.ID, pend.Tentativa, msgInterrompida); merr != nil || !ok {
			a.cfg.log().Error("mcp: não consegui marcar o pedido como falhou", zap.String("pendente", pend.ID.String()), zap.Bool("ok", ok), zap.Error(merr))
		}
		a.auditarPendente(fim, p, "mcp.pendente.falhou", pend, map[string]any{"codigo": "interno"})
		return Aprovado{}, ErrExecucaoInterrompida
	}

	// Escrita feita: daqui para baixo nada vira erro.
	auditar(fim, a.cfg, E, p, "mcp.escrita."+pend.Tool, f.Recurso, f.RecursoID, pend.Tool, Critico, pend.Args,
		map[string]any{chaveMcpPendente: pend.ID.String()})
	var bruto json.RawMessage
	if b, merr := json.Marshal(f.Resultado); merr != nil {
		a.cfg.log().Error("mcp: resultado da pendente não serializa", zap.String("pendente", pend.ID.String()), zap.Error(merr))
	} else {
		bruto = b
	}
	if ok, merr := E.Pendentes.MarcarExecutada(fim, pend.ID, pend.Tentativa, bruto, E.agora()); merr != nil || !ok {
		a.cfg.log().Error("mcp: ação executada mas o pedido não fechou", zap.String("pendente", pend.ID.String()), zap.Bool("ok", ok), zap.Error(merr))
	}
	a.auditarPendente(fim, p, "mcp.pendente.executada", pend, nil)
	pend.Estado = "executada"
	return Aprovado{Pendente: pend, Resultado: f.Resultado}, nil
}

// Recusar fecha o pedido a pedido do dono.
func (a *Aprovacoes) Recusar(ctx context.Context, id uuid.UUID, ator *Principal) (Pendente, error) {
	E := a.cfg.Escritas
	pend, err := E.Pendentes.Recusar(ctx, id, ator.UserID, E.agora())
	if err != nil {
		return Pendente{}, err
	}
	p := &Principal{UserID: ator.UserID, Email: ator.Email, Nome: ator.Nome}
	if pend.TokenID != nil {
		p.TokenID = *pend.TokenID
	}
	a.auditarPendente(ctx, p, "mcp.pendente.recusada", pend, nil)
	return pend, nil
}
