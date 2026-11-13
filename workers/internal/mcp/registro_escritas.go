package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime/debug"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"radiocheck/internal/acoes"
)

// Uma lista só de escritas (spec §5.2; hub registro.ts): a mesma serve ao
// registro das tools e ao executor das pendentes aprovadas — toda `critico`
// tem executor por construção.

type escritaRegistravel interface {
	nomeEscrita() string
	registrar(s *sdk.Server, cfg ConfigServidor, p *Principal)
	// executarAprovada roda a escrita fora do /mcp (aprovação em /pendentes):
	// revalida os args gravados, reroda o Preview, compara o resumo novo com o que a pessoa
	// viu (previaGravada) e chama o Executar.
	//
	// Resumo diferente é uma Recusa preview_desatualizado (*previewDesatualizadoErro, com o
	// resumo novo): o estado mudou depois do pedido, a pessoa aprovaria algo que não viu.
	// Erros da etapa de conferência (decodificar + Preview) que não são
	// *acoes.Recusa saem como *erroConferencia: nada foi tentado, o pedido
	// pode ser aprovado de novo. O que vem do Executar é falha de execução.
	executarAprovada(ctx context.Context, E *Escritas, p *Principal, args json.RawMessage, previaGravada previa) (feito, error)
	// validarArgs só decodifica (schema + defaults + tipos): prova que os args
	// gravados no JSONB voltam a valer na aprovação.
	validarArgs(args json.RawMessage) error
	// argsGravadosDaAuditoria devolve o que o audit_log guarda dos args gravados do pedido
	// (Escrita.ArgsParaAuditoria): a aprovação não tem o In decodificado.
	argsGravadosDaAuditoria(args json.RawMessage) []byte
}

func (d Escrita[In]) nomeEscrita() string { return d.Nome }

func (d Escrita[In]) registrar(s *sdk.Server, cfg ConfigServidor, p *Principal) {
	ferramentaEscrita(s, cfg, p, d)
}

func (d Escrita[In]) validarArgs(args json.RawMessage) error {
	_, err := decodificarArgs[In](args)
	return err
}

const (
	// codigoArgsInvalidos: os args gravados não passam mais no schema (a tool
	// mudou de forma entre o pedido e a aprovação).
	codigoArgsInvalidos    = "args_invalidos"
	codigoToolDesconhecida = "tool_desconhecida"

	// codigoPreviewDesatualizado: o resumo recalculado na aprovação não é o que a pessoa viu
	// ao ser pedida a aprovação (o estado mudou no meio).
	codigoPreviewDesatualizado = "preview_desatualizado"

	msgArgsInvalidos        = "Esta ação mudou desde o pedido e os argumentos gravados não valem mais; nada foi feito. Recuse e peça de novo à IA."
	msgToolDesconhecida     = "Esta ação não existe mais neste servidor; nada foi feito. Recuse e peça de novo à IA."
	msgPreviewDesatualizado = "O estado mudou desde o pedido; o resumo foi atualizado — confira e aprove de novo. Nada foi feito."
	// msgConferenciaFalhou: a conferência do estado (Preview) caiu por erro
	// inesperado ou panic. Nada foi executado; vale tentar de novo.
	msgConferenciaFalhou = "Não foi possível conferir o estado agora; nada foi feito. Tente aprovar de novo em instantes."
)

// erroConferencia: a etapa anterior ao Executar (decodificar os args e rodar o
// Preview) falhou por erro inesperado ou panic — não por recusa de negócio.
// Nada foi tentado, então o pedido volta a `pendente` (não `falhou`).
type erroConferencia struct{ causa error }

func (e *erroConferencia) Error() string {
	return "mcp: conferência da pendente falhou: " + e.causa.Error()
}
func (e *erroConferencia) Unwrap() error { return e.causa }

// argsInvalidosErro é a Recusa fixa de args_invalidos mais o detalhe do
// validador, que só vai para o log (nunca para o banco nem para a pessoa).
type argsInvalidosErro struct {
	recusa  *acoes.Recusa
	detalhe error
}

func (e *argsInvalidosErro) Error() string { return e.recusa.Error() }
func (e *argsInvalidosErro) Unwrap() error { return e.recusa }

// previewDesatualizadoErro é a Recusa preview_desatualizado mais o resumo novo, que a aprovação grava
// no lugar do antigo na mesma atualização que devolve o pedido a `pendente`: o que /pendentes
// mostra depois é o que vale agora.
type previewDesatualizadoErro struct {
	recusa *acoes.Recusa
	nova   previa
}

func (e *previewDesatualizadoErro) Error() string { return e.recusa.Error() }
func (e *previewDesatualizadoErro) Unwrap() error { return e.recusa }

// mesmaPrevia compara duas prévias na forma canônica (o JSON decodificado), de modo que a gravada
// no JSONB (números e listas voltam como float64 e []any) e a recém-calculada se igualem quando
// dizem a mesma coisa. Cobre o resumo inteiro: resumo, mudanças, efeitos e ressalvas.
func mesmaPrevia(a, b previa) (bool, error) {
	ca, err := previaCanonica(a)
	if err != nil {
		return false, err
	}
	cb, err := previaCanonica(b)
	if err != nil {
		return false, err
	}
	return reflect.DeepEqual(ca, cb), nil
}

func previaCanonica(p previa) (any, error) {
	bruto, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(bruto, &v); err != nil {
		return nil, err
	}
	return v, nil
}

func (d Escrita[In]) executarAprovada(ctx context.Context, E *Escritas, p *Principal, args json.RawMessage, previaGravada previa) (feito, error) {
	in, err := d.conferirAprovada(ctx, E, args, previaGravada)
	if err != nil {
		return feito{}, err
	}
	// O Executar planeja DE NOVO (o mesmo planejar* que o Preview acabou de chamar), de propósito:
	// ele nunca confia no retrato que o Preview tirou, porque entre a conferência e a escrita o
	// estado pode mudar outra vez, e as recusas que dependem do estado têm de valer na hora de gravar.
	return executarSemPanic(ctx, d, E, p, in)
}

// conferirAprovada decodifica e roda o Preview, com recover: panic aqui ainda
// é "nada foi feito". O resumo recalculado tem de ser o que a pessoa viu
// (previaGravada): se mudou, é a Recusa preview_desatualizado, com o resumo novo.
func (d Escrita[In]) conferirAprovada(ctx context.Context, E *Escritas, args json.RawMessage, previaGravada previa) (in In, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &erroConferencia{causa: fmt.Errorf("panic: %v\n%s", r, debug.Stack())}
		}
	}()
	in, derr := decodificarArgs[In](args)
	if derr != nil {
		return in, &argsInvalidosErro{
			recusa:  &acoes.Recusa{Codigo: codigoArgsInvalidos, Mensagem: msgArgsInvalidos},
			detalhe: derr,
		}
	}
	if d.Preview != nil {
		nova, perr := d.Preview(ctx, E, in)
		if perr != nil {
			var rec *acoes.Recusa
			if errors.As(perr, &rec) {
				return in, perr
			}
			return in, &erroConferencia{causa: perr}
		}
		nova = limparPrevia(nova) // o gravado foi limpo na criação: compara limpo com limpo
		igual, cerr := mesmaPrevia(previaGravada, nova)
		if cerr != nil {
			return in, &erroConferencia{causa: fmt.Errorf("comparar o resumo: %w", cerr)}
		}
		if !igual {
			return in, &previewDesatualizadoErro{
				recusa: &acoes.Recusa{Codigo: codigoPreviewDesatualizado, Mensagem: msgPreviewDesatualizado},
				nova:   nova,
			}
		}
	}
	return in, nil
}

// escritasDe é a lista única das escritas, na ordem de registro.
func escritasDe(cfg ConfigServidor) []escritaRegistravel {
	var todas []escritaRegistravel
	for _, familia := range [][]escritaRegistravel{
		escritasBasicas(cfg), escritasCadastros(cfg), escritasCampanhas(cfg), escritasProgramacao(cfg),
		escritasVinculos(cfg), escritasRegras(cfg), escritasAjustes(cfg),
		escritasCriticasCampanhas(cfg), escritasCriticasVeiculacoes(cfg), escritasCriticasUsuarios(cfg),
		escritasCriticasClientes(cfg), escritasCriticasWebhooks(cfg), escritasCriticasPosVenda(cfg),
	} {
		todas = append(todas, familia...)
	}
	return todas
}

func registrarEscritas(s *sdk.Server, cfg ConfigServidor, p *Principal) {
	for _, e := range escritasDe(cfg) {
		e.registrar(s, cfg, p)
	}
}

func indexarEscritas(lista []escritaRegistravel) (map[string]escritaRegistravel, error) {
	m := make(map[string]escritaRegistravel, len(lista))
	for _, e := range lista {
		if _, dup := m[e.nomeEscrita()]; dup {
			return nil, fmt.Errorf("mcp: escrita com nome repetido: %s", e.nomeEscrita())
		}
		m[e.nomeEscrita()] = e
	}
	return m, nil
}

// schemasDeEntrada guarda o schema resolvido de cada tipo de entrada.
var schemasDeEntrada sync.Map // reflect.Type -> *jsonschema.Resolved

// schemaResolvido espelha mcp.setSchema (go-sdk v1.8.0, server.go): o mesmo
// jsonschema.ForType com ForOptions vazio e o mesmo Resolve com
// ValidateDefaults, para a entrada valer exatamente o que a tool valia.
func schemaResolvido(rt reflect.Type) (*jsonschema.Resolved, error) {
	if rt.Kind() == reflect.Pointer {
		rt = rt.Elem()
	}
	if v, ok := schemasDeEntrada.Load(rt); ok {
		return v.(*jsonschema.Resolved), nil
	}
	sch, err := jsonschema.ForType(rt, &jsonschema.ForOptions{})
	if err != nil {
		return nil, err
	}
	if sch == nil {
		return nil, fmt.Errorf("schema nulo para %v", rt)
	}
	res, err := sch.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		return nil, err
	}
	schemasDeEntrada.Store(rt, res)
	return res, nil
}

// decodificarArgs revalida os args gravados como o SDK os valida numa chamada
// de tool: espelha o th de mcp.toolForErr junto com mcp.applySchema (go-sdk
// v1.8.0, server.go e tool.go): objeto → ApplyDefaults → Validate → decodifica
// em In. Diferença deliberada: o SDK decodifica com segmentio (sem casar
// maiúsculas/minúsculas); aqui é encoding/json com DisallowUnknownFields — o
// schema de struct já veta campo extra (additionalProperties false), então a
// checagem só endurece e nunca recusa o que o SDK aceita.
func decodificarArgs[In any](args json.RawMessage) (In, error) {
	var in In
	resolvido, err := schemaResolvido(reflect.TypeFor[In]())
	if err != nil {
		return in, fmt.Errorf("schema da entrada: %w", err)
	}
	obj := make(map[string]any)
	if len(bytes.TrimSpace(args)) > 0 {
		if err := json.Unmarshal(args, &obj); err != nil {
			return in, fmt.Errorf("argumentos: %w", err)
		}
	}
	var valor any = obj
	if err := resolvido.ApplyDefaults(&valor); err != nil {
		return in, fmt.Errorf("aplicando defaults: %w", err)
	}
	if err := resolvido.Validate(&valor); err != nil {
		return in, err
	}
	final, err := json.Marshal(valor)
	if err != nil {
		return in, err
	}
	dec := json.NewDecoder(bytes.NewReader(final))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return in, err
	}
	return in, nil
}
