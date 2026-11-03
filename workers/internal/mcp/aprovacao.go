package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"time"

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

// execucaoInterrompidaErro é o ErrExecucaoInterrompida com o texto que a pessoa lê: o genérico
// (msgInterrompida) ou o da ação, quando ela falhou DEPOIS de gravar por um motivo que a pessoa
// precisa conhecer (*acoes.ErroAposEscrita). errors.Is(err, ErrExecucaoInterrompida) segue valendo.
type execucaoInterrompidaErro struct{ mensagem string }

func (e *execucaoInterrompidaErro) Error() string      { return ErrExecucaoInterrompida.Error() }
func (e *execucaoInterrompidaErro) Is(alvo error) bool { return alvo == ErrExecucaoInterrompida }

// mensagemDaInterrupcao é o texto de uma execução interrompida: o da ação, se ela deu um, ou o genérico.
func mensagemDaInterrupcao(err error) string {
	var e *execucaoInterrompidaErro
	if errors.As(err, &e) && e.mensagem != "" {
		return e.mensagem
	}
	return msgInterrompida
}

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
	Pendente Pendente
	// ResultadoJSON é o resultado do Executar já serializado (nil = null): a
	// rota HTTP o escreve como está, então um resultado que não serializa nunca
	// vira um 200 vazio.
	ResultadoJSON json.RawMessage
	// Ressalvas são os avisos da execução (feito.Ressalvas), o que a pessoa lê
	// depois da aprovação: "o email não saiu", "gravei mas não consegui reler…".
	// Nunca nil.
	Ressalvas []string
}

// ErrConferenciaFalhou: a conferência do estado antes de executar caiu por erro
// inesperado ou panic. Nada foi executado e o pedido voltou a `pendente`.
var ErrConferenciaFalhou = errors.New("mcp: conferência do pedido falhou")

// prazoGravarAprovacao limita a gravação da aprovação (o UPDATE), que roda sem
// o cancelamento do pedido HTTP.
const prazoGravarAprovacao = 10 * time.Second

// auditarPendente grava as linhas mcp.pendente.* (recurso "mcp_pendente"). Se o
// pedido perdeu o token (FK SET NULL), token_id sai como o UUID zero.
func (a *Aprovacoes) auditarPendente(ctx context.Context, p *Principal, acao string, pend Pendente, extra map[string]any) {
	e := map[string]any{chaveMcpPendente: pend.ID.String()}
	for k, v := range extra {
		e[k] = v
	}
	id := pend.ID
	auditar(ctx, a.cfg, a.cfg.Escritas, p, acao, "mcp_pendente", &id, pend.Tool, Critico, a.argsDaAuditoria(pend), e)
}

// argsDaAuditoria são os args do pedido como o audit_log os guarda (a tool pode resumir um
// argumento sensível); tool que não existe mais grava os args como estão.
func (a *Aprovacoes) argsDaAuditoria(pend Pendente) []byte {
	if e, ok := a.indice[pend.Tool]; ok {
		return e.argsGravadosDaAuditoria(pend.Args)
	}
	return pend.Args
}

func (a *Aprovacoes) Aprovar(ctx context.Context, id uuid.UUID, ator *Principal) (Aprovado, error) {
	E := a.cfg.Escritas
	// A aprovação commita sem depender de o cliente continuar conectado: com o
	// UPDATE feito e o pedido cancelado, a linha ficaria `aprovada` sem ninguém
	// para executá-la.
	actx, cancelaAprovar := context.WithTimeout(context.WithoutCancel(ctx), prazoGravarAprovacao)
	pend, err := E.Pendentes.Aprovar(actx, id, ator.UserID, E.agora())
	cancelaAprovar()
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
	// O Preview rerodado na conferência vê o dono e o token do pedido (PrincipalDe):
	// é assim que a recusa "nunca a si mesmo" vale também na aprovação.
	ectx = ComPrincipal(ectx, p)
	fim := context.WithoutCancel(ctx)

	var f feito
	if e, ok := a.indice[pend.Tool]; !ok {
		a.cfg.log().Error("mcp: ação da pendente não existe mais neste servidor", zap.String("pendente", pend.ID.String()), zap.String("tool", pend.Tool))
		err = &acoes.Recusa{Codigo: codigoToolDesconhecida, Mensagem: msgToolDesconhecida}
	} else {
		f, err = e.executarAprovada(ectx, E, p, pend.Args, pend.Preview)
	}
	if err != nil {
		var conf *erroConferencia
		if errors.As(err, &conf) {
			// Nada foi tentado: volta a pendente, para aprovar de novo.
			a.cfg.log().Error("mcp: conferência da pendente falhou", zap.String("pendente", pend.ID.String()), zap.String("tool", pend.Tool), zap.Error(conf.causa))
			if ok, merr := E.Pendentes.MarcarFalhaDeNegocio(fim, pend.ID, pend.Tentativa, msgConferenciaFalhou, E.agora(), true, nil); merr != nil || !ok {
				// Não deu para devolver o pedido a pendente: dizer "tente de novo
				// em instantes" mentiria (a linha segue `aprovada`, sem executor).
				// É o mesmo desfecho de uma execução interrompida: ninguém tenta
				// de novo sozinho, a pessoa confere.
				a.cfg.log().Error("mcp: não consegui devolver o pedido a pendente", zap.String("pendente", pend.ID.String()), zap.Bool("ok", ok), zap.Error(merr))
				a.auditarPendente(fim, p, "mcp.pendente.falhou", pend, map[string]any{"codigo": "interno"})
				return Aprovado{}, ErrExecucaoInterrompida
			}
			a.auditarPendente(fim, p, "mcp.pendente.falhou", pend, map[string]any{"codigo": "conferencia_falhou"})
			return Aprovado{}, ErrConferenciaFalhou
		}
		var r *acoes.Recusa
		if errors.As(err, &r) {
			// args_invalidos e tool_desconhecida não se resolvem esperando: não
			// ganham folga de prazo.
			estender := r.Codigo != codigoArgsInvalidos && r.Codigo != codigoToolDesconhecida
			var inv *argsInvalidosErro
			if errors.As(err, &inv) {
				a.cfg.log().Warn("mcp: args gravados não passam mais no schema", zap.String("pendente", pend.ID.String()), zap.String("tool", pend.Tool), zap.Error(inv.detalhe))
			}
			// preview_desatualizado: o resumo novo entra no lugar do antigo na mesma atualização
			// que devolve o pedido a pendente, para /pendentes mostrar o que vale agora.
			var novaPrevia *previa
			var desat *previewDesatualizadoErro
			if errors.As(err, &desat) {
				novaPrevia = &desat.nova
			}
			if ok, merr := E.Pendentes.MarcarFalhaDeNegocio(fim, pend.ID, pend.Tentativa, r.Mensagem, E.agora(), estender, novaPrevia); merr != nil || !ok {
				// Sem devolver a pendente, a Recusa ("nada aconteceu, aprove de novo")
				// seria mentira: mesmo desfecho da conferência.
				a.cfg.log().Error("mcp: não consegui devolver o pedido a pendente", zap.String("pendente", pend.ID.String()), zap.Bool("ok", ok), zap.Error(merr))
				a.auditarPendente(fim, p, "mcp.pendente.falhou", pend, map[string]any{"codigo": "interno"})
				return Aprovado{}, ErrExecucaoInterrompida
			}
			a.auditarPendente(fim, p, "mcp.pendente.falhou", pend, map[string]any{"codigo": r.Codigo})
			return Aprovado{}, r
		}
		a.cfg.log().Error("mcp: execução da pendente aprovada falhou", zap.String("pendente", pend.ID.String()), zap.String("tool", pend.Tool), zap.Error(err))
		// A ação gravou algo e falhou por um motivo que a pessoa precisa ler ("a campanha foi
		// cancelada mas não apagada", "o usuário foi criado mas…"): o texto dela vai no lugar do
		// genérico. O erro Go (a Causa) fica só no log.
		msg, codigo := msgInterrompida, "interno"
		var pos *acoes.ErroAposEscrita
		if errors.As(err, &pos) && pos.Mensagem != "" {
			msg, codigo = pos.Mensagem, "falha_apos_escrita"
		}
		if ok, merr := E.Pendentes.MarcarInterrompida(fim, pend.ID, pend.Tentativa, msg); merr != nil || !ok {
			a.cfg.log().Error("mcp: não consegui marcar o pedido como falhou", zap.String("pendente", pend.ID.String()), zap.Bool("ok", ok), zap.Error(merr))
		}
		a.auditarPendente(fim, p, "mcp.pendente.falhou", pend, map[string]any{"codigo": codigo})
		return Aprovado{}, &execucaoInterrompidaErro{mensagem: msg}
	}

	// Escrita feita: daqui para baixo nada vira erro.
	auditar(fim, a.cfg, E, p, "mcp.escrita."+pend.Tool, f.Recurso, f.RecursoID, pend.Tool, Critico, a.argsDaAuditoria(pend),
		map[string]any{chaveMcpPendente: pend.ID.String()})
	var bruto json.RawMessage
	if b, merr := json.Marshal(f.Resultado); merr != nil {
		a.cfg.log().Error("mcp: resultado da pendente não serializa", zap.String("pendente", pend.ID.String()), zap.Error(merr))
	} else {
		bruto = b
	}
	ressalvas := f.Ressalvas
	if ressalvas == nil {
		ressalvas = []string{}
	}
	fechada, ok, merr := E.Pendentes.MarcarExecutada(fim, pend.ID, pend.Tentativa, bruto, ressalvas, E.agora())
	if merr != nil || !ok {
		a.cfg.log().Error("mcp: ação executada mas o pedido não fechou", zap.String("pendente", pend.ID.String()), zap.Bool("ok", ok), zap.Error(merr))
		fechada = pend
		fechada.Estado = "executada"
		fechada.Ressalvas = ressalvas
	}
	a.auditarPendente(fim, p, "mcp.pendente.executada", pend, nil)
	return Aprovado{Pendente: fechada, ResultadoJSON: bruto, Ressalvas: ressalvas}, nil
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
