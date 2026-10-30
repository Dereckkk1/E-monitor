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
	// revalida os args gravados, reroda o Preview e chama o Executar.
	//
	// Erros da etapa de conferência (decodificar + Preview) que não são
	// *acoes.Recusa saem como *erroConferencia: nada foi tentado, o pedido
	// pode ser aprovado de novo. O que vem do Executar é falha de execução.
	executarAprovada(ctx context.Context, E *Escritas, p *Principal, args json.RawMessage) (feito, error)
	// validarArgs só decodifica (schema + defaults + tipos): prova que os args
	// gravados no JSONB voltam a valer na aprovação.
	validarArgs(args json.RawMessage) error
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

	msgArgsInvalidos    = "Esta ação mudou desde o pedido e os argumentos gravados não valem mais; nada foi feito. Recuse e peça de novo à IA."
	msgToolDesconhecida = "Esta ação não existe mais neste servidor; nada foi feito. Recuse e peça de novo à IA."
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

func (d Escrita[In]) executarAprovada(ctx context.Context, E *Escritas, p *Principal, args json.RawMessage) (feito, error) {
	in, err := d.conferirAprovada(ctx, E, args)
	if err != nil {
		return feito{}, err
	}
	return executarSemPanic(ctx, d, E, p, in)
}

// conferirAprovada decodifica e roda o Preview, com recover: panic aqui ainda
// é "nada foi feito".
func (d Escrita[In]) conferirAprovada(ctx context.Context, E *Escritas, args json.RawMessage) (in In, err error) {
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
		if _, perr := d.Preview(ctx, E, in); perr != nil {
			var rec *acoes.Recusa
			if errors.As(perr, &rec) {
				return in, perr
			}
			return in, &erroConferencia{causa: perr}
		}
	}
	return in, nil
}

// escritasDe é a lista única das escritas, na ordem de registro.
func escritasDe(cfg ConfigServidor) []escritaRegistravel {
	var todas []escritaRegistravel
	for _, familia := range [][]escritaRegistravel{
		escritasBasicas(cfg), escritasCadastros(cfg), escritasCampanhas(cfg),
		escritasVinculos(cfg), escritasRegras(cfg), escritasAjustes(cfg),
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
