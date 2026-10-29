package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
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
//	critico   NUNCA executa pela tool. Até a Fatia 4 trazer /pendentes, a
//	          resposta é barrada com o motivo e o link da tela (F3-1).
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
// vai para a resposta barrada e para a auditoria). Nunca desce do declarado.
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

// Escrita é DADO (spec §5.2): o mesmo valor registra a tool e, na Fatia 4,
// executará a pendente aprovada. Recusa de negócio = *acoes.Recusa (libera o
// token); qualquer outro erro = falha interna (não libera).
type Escrita[In any] struct {
	Nome, Descricao string
	Nivel           Nivel
	Escalar         func(ctx context.Context, E *Escritas, in In) (Escalada, error) // nil = nunca escala
	Preview         func(ctx context.Context, E *Escritas, in In) (previa, error)
	Executar        func(ctx context.Context, E *Escritas, p *Principal, in In) (feito, error)
	Tela            func(E *Escritas, in In) *string // link da tela para o ramo crítico barrado
}

// CampoConfirmacao entra embutido no In de toda tool `confirma` ou escalável.
type CampoConfirmacao struct {
	Confirmacao string `json:"confirmacao,omitempty" jsonschema:"O token devolvido pelo preview. Só vale com os MESMOS argumentos, por 10 min. Nunca o reenvie sem a pessoa aprovar o preview."`
}

func (c CampoConfirmacao) confirmacao() string { return c.Confirmacao }

const (
	msgInvalida = "Confirmação inválida ou expirada, ou os argumentos mudaram. Peça o preview outra vez."
	msgUsada    = "Esta confirmação já foi usada. Peça o preview outra vez se quiser repetir."
	// avisoBarrado é o texto do ramo crítico (F3-1): a Fatia 4 troca esse ramo
	// por criarPendente.
	avisoBarrado = "Nada aconteceu. Esta alteração atinge veiculação já ocorrida e precisa de aprovação humana; a aprovação pelo MCP ainda não existe — faça pela tela, logado."
	// tetoTextoAuditoria: texto longo (roteiro) entra cortado no audit_log (F3-11).
	tetoTextoAuditoria = 500
)

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

type linksBarrado struct {
	Tela *string `json:"tela"`
}

type respostaBarrada struct {
	Executou bool         `json:"executou"`
	Nivel    string       `json:"nivel"`
	Motivo   string       `json:"motivo"`
	Preview  previa       `json:"preview"`
	Aviso    string       `json:"aviso"`
	Links    linksBarrado `json:"links"`
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
// (aplicarNivel): nível efetivo → livre executa → preview → crítico barra →
// confirma (sem token: pede; com token: verifica, consome, executa).
func ferramentaEscrita[In any](s *sdk.Server, cfg ConfigServidor, p *Principal, d Escrita[In]) {
	destrutiva := d.Nivel != Livre || d.Escalar != nil
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
		E := cfg.Escritas
		if E == nil {
			return falhaInterna(cfg.log(), d.Nome, errors.New("mcp: Escritas não configuradas"))
		}
		if p == nil {
			return falha("sem identidade: o pedido não passou pela autenticação do MCP", "sem_identidade")
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

		// 4. crítico: barrado até a Fatia 4 (F3-1). Nada é escrito nem consumido.
		if nivel == Critico {
			auditar(ctx, cfg, E, p, "mcp.escrita.requer_aprovacao", "mcp_escrita", nil, d.Nome, nivel, args, motivo)
			var tela *string
			if d.Tela != nil {
				tela = d.Tela(E, in)
			}
			return resultado(respostaBarrada{
				Executou: false, Nivel: nivel.String(), Motivo: motivo, Preview: pv,
				Aviso: avisoBarrado, Links: linksBarrado{Tela: tela},
			})
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

	f, err := d.Executar(ctx, E, p, in)
	if err != nil {
		var r *acoes.Recusa
		if tok != "" && errors.As(err, &r) {
			if lerr := E.Confirmacoes.Liberar(context.WithoutCancel(ctx), tok); lerr != nil {
				cfg.log().Error("mcp: não consegui liberar a confirmação após recusa de negócio",
					zap.String("tool", d.Nome), zap.Error(lerr))
			}
		}
		return comoFalha(cfg, d.Nome, err)
	}

	// A escrita já aconteceu: nada daqui para baixo vira erro (o modelo
	// repetiria). A auditoria roda sem o cancelamento do pedido.
	auditar(ctx, cfg, E, p, "mcp.escrita."+d.Nome, f.Recurso, f.RecursoID, d.Nome, nivel, args, "")

	r, o, e := resultado(respostaFeita{Executou: true, Resultado: f.Resultado, Ressalvas: f.Ressalvas})
	if e == nil {
		return r, o, nil
	}
	cfg.log().Error("mcp: resultado da escrita não serializa", zap.String("tool", d.Nome), zap.Error(e))
	return resultado(respostaFeita{Executou: true, Resultado: nil, Ressalvas: f.Ressalvas,
		Aviso: "A ação foi executada, mas o resultado não pôde ser mostrado."})
}

// auditar grava a linha do audit_log (spec §4.6). Falha é logada e engolida:
// a escrita já aconteceu e o modelo repetiria.
func auditar(ctx context.Context, cfg ConfigServidor, E *Escritas, p *Principal, acao, recurso string,
	recursoID *uuid.UUID, tool string, nivel Nivel, args []byte, motivo string) {

	meta := map[string]any{
		"tool":  tool,
		"nivel": nivel.String(),
		"args":  argsParaAuditoria(args),
		"mcp":   map[string]any{"token_id": p.TokenID.String()},
	}
	if motivo != "" {
		meta["motivo"] = motivo
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
