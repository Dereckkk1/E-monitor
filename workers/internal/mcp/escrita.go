package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"runtime/debug"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"

	"radiocheck/internal/acoes"
	"radiocheck/internal/mcp/auditoria"
)

// Motor de níveis da escrita (spec §5, plano da Fatia 3). Transplante do fluxo
// do hub (backend/src/mcp/write/escrita.ts, aplicarNivel), adaptado a Go:
//
//	livre     executa na primeira chamada;
//	confirma  a primeira chamada devolve preview + token; a segunda, com os
//	          MESMOS argumentos e o token, executa (uso único, 10 min);
//	critico   NUNCA executa pela tool. Grava um pedido pendente (24 h) e
//	          devolve o link /pendentes; quem executa é a aprovação de uma
//	          pessoa logada no portal. O token de confirmação, se
//	          vier, é ignorado (não é consumido).
//
// A escalada (Escalar) consulta o banco e roda em TODA chamada, preview e
// confirmação (F3-2): o token fixa os argumentos, não o estado do mundo.

type Nivel int

const (
	Livre Nivel = iota
	Confirma
	Critico
)

func (n Nivel) String() string {
	if n < Livre || n > Critico {
		return "desconhecido"
	}
	return [...]string{"livre", "confirma", "critico"}[n]
}

// Escalada: o nível que os ARGUMENTOS e o ESTADO pedem, e por quê (o motivo
// vai para o pedido pendente e para a auditoria). Nunca desce do declarado.
type Escalada struct {
	Nivel  Nivel
	Motivo string
}

// previa é o que a pessoa lê antes de aprovar.
type previa struct {
	Resumo    string    `json:"resumo"`
	Mudancas  []mudanca `json:"mudancas,omitempty"`
	Efeitos   []string  `json:"efeitos,omitempty"`
	Ressalvas []string  `json:"ressalvas,omitempty"`
}

type mudanca struct {
	Campo string `json:"campo"`
	De    any    `json:"de"`
	Para  any    `json:"para"`
}

// feito é o que o Executar devolve: o resultado, o alvo da auditoria e as
// ressalvas da resposta (ex.: "a recategorização roda em segundo plano").
type feito struct {
	Resultado any
	Recurso   string // "campaign", "material", …
	RecursoID *uuid.UUID
	Ressalvas []string
}

// Escrita é DADO (spec §5.2): o mesmo valor registra a tool e
// executa a pendente aprovada. Recusa de negócio = *acoes.Recusa (libera o
// token); qualquer outro erro = falha interna (não libera).
type Escrita[In any] struct {
	Nome, Descricao string
	Nivel           Nivel
	Escalar         func(ctx context.Context, E *Escritas, in In) (Escalada, error) // nil = nunca escala
	Preview         func(ctx context.Context, E *Escritas, in In) (previa, error)
	Executar        func(ctx context.Context, E *Escritas, p *Principal, in In) (feito, error)
	Tela            func(E *Escritas, in In) *string // link da tela, na resposta do pedido pendente
	// Motivo é "por que é crítica": usado quando o nível DECLARADO é Critico
	// (a escalada traz o próprio motivo).
	Motivo string
	// Recurso (opcional) diz o alvo da auditoria quando a escrita NÃO chega a
	// produzir um feito (pedido pendente, falha interna): o tipo e o id do
	// registro que a tool mexeria. Sem ele, "mcp_escrita" sem id.
	Recurso func(in In) (string, *uuid.UUID)
	// ArgsParaAuditoria (opcional) devolve o que entra em meta.args do audit_log no lugar dos
	// args canônicos, para a tool cujo argumento carrega um segredo que o log não deve guardar
	// por inteiro (a URL do webhook). Só o audit_log muda: os args gravados no pedido seguem
	// inteiros — são o que o Executar precisa, e só o dono os lê. Vale em toda linha que leva os
	// args, no pedido e na aprovação.
	ArgsParaAuditoria func(in In) any
}

// CampoConfirmacao entra embutido no In de toda tool `confirma` ou escalável.
type CampoConfirmacao struct {
	Confirmacao string `json:"confirmacao,omitempty" jsonschema:"O token devolvido pelo preview. Só vale com os MESMOS argumentos, por 10 min. Nunca o reenvie sem a pessoa aprovar o preview."`
}

func (c CampoConfirmacao) confirmacao() string { return c.Confirmacao }

const (
	msgInvalida = "Confirmação inválida ou expirada, ou os argumentos mudaram. Peça o preview outra vez."
	msgUsada    = "Esta confirmação já foi usada. Peça o preview outra vez se quiser repetir."
	// avisoPendente é o texto do ramo crítico: o pedido foi gravado, nada executou.
	avisoPendente = "Nada aconteceu ainda. Entregue o link aprovar_em à pessoa: só ela, logada no portal, pode aprovar; o pedido vale 24 h."
	// avisoPendenteAprovado: o pedido igual já foi aprovado e está rodando.
	avisoPendenteAprovado = "Este pedido já foi aprovado e está em execução; nada novo foi criado. Use minhas_pendentes para ver o resultado."
	// tetoConfirmacao: comprimento máximo aceito para o campo confirmacao.
	tetoConfirmacao = 200
	// tetoTextoAuditoria: texto longo (roteiro) entra cortado no audit_log (F3-11).
	tetoTextoAuditoria = 500
)

// prazoExecucao limita o Executar (60 s, o mesmo middleware.Timeout do router).
// var e não const só para o teste encurtá-lo.
var prazoExecucao = 60 * time.Second

// anotacoesEscrita: as dicas explícitas — os defaults do protocolo
// (destructive=true, openWorld=true) só estão certos para o primeiro. Uma
// `livre` sem escalada não destrói nada.
func anotacoesEscrita(destrutiva bool) *sdk.ToolAnnotations {
	nao := false
	return &sdk.ToolAnnotations{
		ReadOnlyHint:    false,
		DestructiveHint: &destrutiva,
		OpenWorldHint:   &nao,
	}
}

// ── Formas das respostas (F3-12) ────────────────────────────────────────

type respostaFeita struct {
	Executou  bool     `json:"executou"`
	Resultado any      `json:"resultado"`
	Ressalvas []string `json:"ressalvas,omitempty"`
	Aviso     string   `json:"aviso,omitempty"`
}

type respostaPedido struct {
	Executou    bool   `json:"executou"`
	Preview     previa `json:"preview"`
	Confirmacao string `json:"confirmacao"`
	ExpiraEm    string `json:"expira_em"`
}

type respostaSemEfeito struct {
	Executou bool   `json:"executou"`
	Erro     string `json:"erro"`
}

type linksPendente struct {
	Tela *string `json:"tela"`
}

type respostaPendente struct {
	Executou   bool          `json:"executou"`
	Nivel      string        `json:"nivel"`
	Motivo     string        `json:"motivo"`
	Preview    previa        `json:"preview"`
	PendenteID string        `json:"pendente_id"`
	ExpiraEm   string        `json:"expira_em"`
	AprovarEm  *string       `json:"aprovar_em"`
	JaExistia  bool          `json:"ja_existia"`
	Aviso      string        `json:"aviso"`
	Ressalvas  []string      `json:"ressalvas,omitempty"`
	Links      linksPendente `json:"links"`
}

// criarPendente grava o pedido do ramo crítico (idempotente: o mesmo pedido
// aberto não duplica) e devolve o link de aprovação. Nada é executado.
func criarPendente[In any](ctx context.Context, cfg ConfigServidor, E *Escritas, p *Principal, d Escrita[In],
	in In, args []byte, motivo string, pv previa) (*sdk.CallToolResult, any, error) {
	if E.Pendentes == nil {
		return falhaInterna(cfg.log(), d.Nome, errors.New("mcp: Pendentes não configuradas"))
	}
	pend, jaExistia, err := E.Pendentes.Criar(context.WithoutCancel(ctx), NovaPendente{
		UserID: p.UserID, TokenID: p.TokenID, Tool: d.Nome, Args: args, Motivo: motivo, Preview: pv,
	}, E.agora())
	if err != nil {
		return falhaInterna(cfg.log(), d.Nome, err)
	}
	recurso, recursoID := d.alvo(in)
	if !jaExistia {
		auditar(ctx, cfg, E, p, "mcp.pendente.criada", recurso, recursoID, d.Nome, Critico, d.argsDaAuditoria(in, args),
			map[string]any{"motivo": motivo, chaveMcpPendente: pend.ID.String()})
	}
	// Pedido já existente: a resposta mostra o que ESTÁ gravado (o que a pessoa
	// vai ver em /pendentes), não o recalculado agora.
	aviso := avisoPendente
	var aprovar *string
	if jaExistia {
		motivo, pv = pend.Motivo, pend.Preview
	}
	if jaExistia && pend.Estado == "aprovada" {
		aviso = avisoPendenteAprovado
	} else {
		aprovar = cfg.linkTela("/pendentes", url.Values{"id": {pend.ID.String()}})
	}
	var ressalvas []string
	if aprovar == nil && !(jaExistia && pend.Estado == "aprovada") {
		ressalvas = append(ressalvas, "O endereço do portal não está configurado: abra /pendentes no portal do E-monitor para aprovar.")
	}
	var tela *string
	if d.Tela != nil {
		tela = d.Tela(E, in)
	}
	return resultado(respostaPendente{
		Executou: false, Nivel: Critico.String(), Motivo: motivo, Preview: pv,
		PendenteID: pend.ID.String(), ExpiraEm: instante(pend.ExpiraEm), AprovarEm: aprovar,
		JaExistia: jaExistia, Aviso: aviso, Ressalvas: ressalvas, Links: linksPendente{Tela: tela},
	})
}

// confirmacaoDe lê o token do In; "" se o In não embute CampoConfirmacao.
func confirmacaoDe(in any) string {
	if c, ok := in.(interface{ confirmacao() string }); ok {
		return c.confirmacao()
	}
	return ""
}

// comoFalha traduz o erro de uma etapa: recusa de negócio vira isError com
// [codigo] (contrato §3.4); o resto é falha interna (sem vazar SQLSTATE).
func comoFalha(cfg ConfigServidor, tool string, err error) (*sdk.CallToolResult, any, error) {
	var r *acoes.Recusa
	if errors.As(err, &r) {
		return falha(r.Mensagem, r.Codigo)
	}
	return falhaInterna(cfg.log(), tool, err)
}

// ferramentaEscrita registra uma tool de escrita. Fluxo na mesma ordem do hub
// (aplicarNivel): nível efetivo → livre executa → preview → crítico grava o
// pedido → confirma (sem token: pede; com token: verifica, consome, executa).
func ferramentaEscrita[In any](s *sdk.Server, cfg ConfigServidor, p *Principal, d Escrita[In]) {
	destrutiva := d.Nivel != Livre || d.Escalar != nil
	// Erro de programação, detectado no registro: uma tool que pode pedir
	// confirmação (confirma, ou livre com Escalar) cujo In não embute
	// CampoConfirmacao nunca receberia o token de volta, e o modelo giraria em
	// previews. Não há panic (regra 6.5: panic no boot derruba o deploy): loga
	// e a tool falha fechada.
	semCampoConfirmacao := false
	if d.Nivel == Confirma || (d.Escalar != nil && d.Nivel < Critico) {
		if _, ok := any(*new(In)).(interface{ confirmacao() string }); !ok {
			semCampoConfirmacao = true
			cfg.log().Error("mcp: tool de escrita pode pedir confirmação mas o In não embute CampoConfirmacao",
				zap.String("tool", d.Nome))
		}
	}
	sdk.AddTool(s, &sdk.Tool{
		Name:        d.Nome,
		Description: d.Descricao + " Nível: " + d.Nivel.String() + ".",
		Annotations: anotacoesEscrita(destrutiva),
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in In) (res *sdk.CallToolResult, out any, err error) {
		// O SDK roda cada request numa goroutine sem recover: um panic aqui
		// derrubaria a API inteira. Panic NÃO libera o token: a escrita pode
		// ter gravado algo (F3-5).
		defer func() {
			if r := recover(); r != nil {
				res, out, err = falhaInterna(cfg.log(), d.Nome, fmt.Errorf("panic: %v\n%s", r, debug.Stack()))
			}
		}()
		if semCampoConfirmacao {
			return falhaInterna(cfg.log(), d.Nome, errors.New("mcp: In sem CampoConfirmacao"))
		}
		E := cfg.Escritas
		if E == nil {
			return falhaInterna(cfg.log(), d.Nome, errors.New("mcp: Escritas não configuradas"))
		}
		if p == nil {
			return falha("sem identidade: o pedido não passou pela autenticação do MCP", "sem_identidade")
		}
		// O Preview não recebe o Principal: as recusas que dependem de QUEM pede
		// (editar_usuario/desativar_usuario nunca a si mesmo) o leem do contexto,
		// aqui e na aprovação (Aprovar), para valerem nos dois momentos.
		ctx = ComPrincipal(ctx, p)
		// Token acima do teto nunca é válido (a forma canônica tem ~57
		// caracteres): recusa antes de qualquer trabalho, sem tocar o banco.
		if len(confirmacaoDe(in)) > tetoConfirmacao {
			return resultado(respostaSemEfeito{Executou: false, Erro: msgInvalida})
		}
		args, err := argsCanonicos(in)
		if err != nil {
			return falhaInterna(cfg.log(), d.Nome, err)
		}

		// 1. nível efetivo — roda SEMPRE, preview e confirmação (F3-2).
		nivel, motivo := d.Nivel, ""
		if d.Escalar != nil {
			esc, err := d.Escalar(ctx, E, in)
			if err != nil {
				return comoFalha(cfg, d.Nome, err)
			}
			if esc.Nivel > nivel {
				nivel, motivo = esc.Nivel, esc.Motivo
			}
		}

		// 2. livre: executa.
		if nivel == Livre {
			return executarEAuditar(ctx, cfg, E, p, d, in, args, nivel, "")
		}

		// 3. preview: falha de negócio para tudo antes do token.
		if d.Preview == nil {
			return falhaInterna(cfg.log(), d.Nome, errors.New("mcp: escrita sem Preview"))
		}
		pv, err := d.Preview(ctx, E, in)
		if err != nil {
			return comoFalha(cfg, d.Nome, err)
		}

		// 4. crítico: grava o pedido; quem executa é a aprovação em /pendentes (F4-5).
		if nivel == Critico {
			if motivo == "" {
				motivo = d.Motivo
			}
			return criarPendente(ctx, cfg, E, p, d, in, args, motivo, pv)
		}

		// 5. confirma.
		agora := E.agora()
		tok := confirmacaoDe(in)
		if tok == "" {
			exp := agora.Add(ttlConfirmacao)
			return resultado(respostaPedido{
				Executou: false, Preview: pv,
				Confirmacao: assinarConfirmacao(E.Segredo, d.Nome, args, p.UserID, exp),
				ExpiraEm:    instante(exp),
			})
		}
		if !verificarConfirmacao(E.Segredo, tok, d.Nome, args, p.UserID, agora) {
			return resultado(respostaSemEfeito{Executou: false, Erro: msgInvalida})
		}
		if E.Confirmacoes == nil {
			return falhaInterna(cfg.log(), d.Nome, errors.New("mcp: Confirmacoes não configuradas"))
		}
		// Uso único: consome ANTES de executar (F3-5).
		novo, err := E.Confirmacoes.Consumir(ctx, tok, expiraDoToken(tok))
		if err != nil {
			return falhaInterna(cfg.log(), d.Nome, err)
		}
		if !novo {
			return resultado(respostaSemEfeito{Executou: false, Erro: msgUsada})
		}
		return executarEAuditar(ctx, cfg, E, p, d, in, args, nivel, tok)
	})
}

// executarEAuditar roda o Executar e grava a auditoria (F3-11/F3-12). tok != ""
// é a confirmação já consumida: recusa de negócio a libera; erro interno não.
func executarEAuditar[In any](ctx context.Context, cfg ConfigServidor, E *Escritas, p *Principal, d Escrita[In],
	in In, args []byte, nivel Nivel, tok string) (*sdk.CallToolResult, any, error) {

	// Depois do Consumir, o desfecho não pode depender de o cliente continuar
	// conectado: sem o cancelamento do pedido, com prazo próprio (o mesmo
	// middleware.Timeout do router).
	ectx, cancela := context.WithTimeout(context.WithoutCancel(ctx), prazoExecucao)
	defer cancela()
	f, err := executarSemPanic(ectx, d, E, p, in)
	if err != nil {
		var r *acoes.Recusa
		if errors.As(err, &r) {
			// Recusa de negócio: nada foi gravado, o token volta a valer e não
			// há o que auditar.
			if tok != "" {
				if lerr := E.Confirmacoes.Liberar(context.WithoutCancel(ctx), tok); lerr != nil {
					cfg.log().Error("mcp: não consegui liberar a confirmação após recusa de negócio",
						zap.String("tool", d.Nome), zap.Error(lerr))
				}
			}
			return comoFalha(cfg, d.Nome, err)
		}
		// Falha interna ou panic: o token (se havia) NÃO volta — algo pode ter
		// sido gravado — e fica um rastro. O texto do erro Go nunca vai para o
		// audit_log (pode trazer SQLSTATE, nome de tabela): só "interno".
		recurso, recursoID := d.alvo(in)
		auditar(ctx, cfg, E, p, "mcp.escrita.falha", recurso, recursoID, d.Nome, nivel, d.argsDaAuditoria(in, args),
			map[string]any{"erro": "interno"})
		return falhaInterna(cfg.log(), d.Nome, err)
	}

	// A escrita já aconteceu: nada daqui para baixo vira erro (o modelo
	// repetiria). A auditoria roda sem o cancelamento do pedido.
	auditar(ctx, cfg, E, p, "mcp.escrita."+d.Nome, f.Recurso, f.RecursoID, d.Nome, nivel, d.argsDaAuditoria(in, args), nil)

	r, o, e := resultado(respostaFeita{Executou: true, Resultado: f.Resultado, Ressalvas: f.Ressalvas})
	if e == nil {
		return r, o, nil
	}
	cfg.log().Error("mcp: resultado da escrita não serializa", zap.String("tool", d.Nome), zap.Error(e))
	return resultado(respostaFeita{Executou: true, Resultado: nil, Ressalvas: f.Ressalvas,
		Aviso: "A ação foi executada, mas o resultado não pôde ser mostrado."})
}

// chaveMcpPendente: chave especial do extra de auditar, que vira meta.mcp.pendente_id
// (e não um campo solto do meta).
const chaveMcpPendente = "mcp_pendente_id"

// auditar grava a linha do audit_log (spec §4.6). Falha é logada e engolida:
// a escrita já aconteceu e o modelo repetiria.
func auditar(ctx context.Context, cfg ConfigServidor, E *Escritas, p *Principal, acao, recurso string,
	recursoID *uuid.UUID, tool string, nivel Nivel, args []byte, extra map[string]any) {

	meta := map[string]any{
		"tool":  tool,
		"nivel": nivel.String(),
		// Só os args são mascarados na gravação; a chave "mcp" (token_id) não.
		"args": auditoria.OmitirSegredos(argsParaAuditoria(args)),
		"mcp":  map[string]any{"token_id": p.TokenID.String()},
	}
	for k, v := range extra {
		if k == chaveMcpPendente {
			// Execução vinda de uma aprovação: o id do pedido vai junto do
			// token_id, dentro de meta.mcp.
			meta["mcp"].(map[string]any)["pendente_id"] = v
			continue
		}
		meta[k] = v
	}
	if E.DB == nil {
		cfg.log().Error("mcp: auditoria da escrita sem banco configurado", zap.String("acao", acao))
		return
	}
	if err := auditoria.Registrar(context.WithoutCancel(ctx), E.DB, p.UserID, acao, recurso, recursoID, meta); err != nil {
		cfg.log().Error("mcp: auditoria da escrita falhou", zap.String("acao", acao), zap.Error(err))
	}
}

// argsParaAuditoria devolve os args canônicos com todo texto cortado em
// tetoTextoAuditoria runas (um roteiro de 5 mil caracteres não vai para o log).
func argsParaAuditoria(args []byte) any {
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return map[string]any{}
	}
	return cortarTextos(v)
}

func cortarTextos(v any) any {
	switch x := v.(type) {
	case string:
		if utf8.RuneCountInString(x) <= tetoTextoAuditoria {
			return x
		}
		return string([]rune(x)[:tetoTextoAuditoria]) + "…[cortado]"
	case map[string]any:
		for k, val := range x {
			x[k] = cortarTextos(val)
		}
	case []any:
		for i, val := range x {
			x[i] = cortarTextos(val)
		}
	}
	return v
}

// argsDaAuditoria são os args que vão para o audit_log: os canônicos, ou o que a tool declara em
// ArgsParaAuditoria. O hook que falha ou entra em panic grava {} — nunca os args sem redação.
func (d Escrita[In]) argsDaAuditoria(in In, args []byte) (saida []byte) {
	if d.ArgsParaAuditoria == nil {
		return args
	}
	defer func() {
		if recover() != nil {
			saida = []byte("{}")
		}
	}()
	b, err := json.Marshal(d.ArgsParaAuditoria(in))
	if err != nil {
		return []byte("{}")
	}
	return b
}

// argsGravadosDaAuditoria é o argsDaAuditoria para os args JÁ gravados no pedido (a aprovação
// não tem o In). Args que não decodificam não dá para redigir: saem {} no log.
func (d Escrita[In]) argsGravadosDaAuditoria(args json.RawMessage) []byte {
	if d.ArgsParaAuditoria == nil {
		return args
	}
	in, err := decodificarArgs[In](args)
	if err != nil {
		return []byte("{}")
	}
	return d.argsDaAuditoria(in, args)
}

// alvo é o recurso da auditoria de uma escrita sem feito (pendente ou falha):
// o que a tool declara em Recurso, ou o genérico "mcp_escrita" sem id.
func (d Escrita[In]) alvo(in In) (string, *uuid.UUID) {
	if d.Recurso != nil {
		if r, id := d.Recurso(in); r != "" {
			return r, id
		}
	}
	return "mcp_escrita", nil
}

// executarSemPanic roda o Executar convertendo panic em erro comum: ele tem
// de seguir o mesmo caminho da falha interna (rastro em mcp.escrita.falha, o
// token não volta). O panic nunca é uma Recusa.
func executarSemPanic[In any](ctx context.Context, d Escrita[In], E *Escritas, p *Principal, in In) (f feito, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v\n%s", r, debug.Stack())
		}
	}()
	return d.Executar(ctx, E, p, in)
}
