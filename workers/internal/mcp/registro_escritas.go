package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
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
	executarAprovada(ctx context.Context, E *Escritas, p *Principal, args json.RawMessage) (feito, error)
}

func (d Escrita[In]) nomeEscrita() string { return d.Nome }

func (d Escrita[In]) registrar(s *sdk.Server, cfg ConfigServidor, p *Principal) {
	ferramentaEscrita(s, cfg, p, d)
}

// codigoArgsInvalidos: os args gravados não passam mais no schema (a tool mudou
// de forma entre o pedido e a aprovação).
const codigoArgsInvalidos = "args_invalidos"

func (d Escrita[In]) executarAprovada(ctx context.Context, E *Escritas, p *Principal, args json.RawMessage) (feito, error) {
	in, err := decodificarArgs[In](args)
	if err != nil {
		return feito{}, &acoes.Recusa{Codigo: codigoArgsInvalidos,
			Mensagem: "Os argumentos gravados não passam mais na validação desta ação: " + err.Error()}
	}
	if d.Preview != nil {
		if _, err := d.Preview(ctx, E, in); err != nil {
			return feito{}, err
		}
	}
	return executarSemPanic(ctx, d, E, p, in)
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
