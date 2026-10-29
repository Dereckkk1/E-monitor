// Package acoes concentra as escritas de negócio do E-monitor que o portal
// (handlers HTTP) e o MCP compartilham (plano da Fatia 3, F3-7): validação,
// chamada ao repositório e os gatilhos que a escrita dispara (recategorização,
// reload de índice e workers, aviso ao hub). Os handlers e as tools do MCP
// chamam a MESMA função daqui — a regra de ouro da spec (§3): a tool nunca
// reimplementa o que a tela faz.
package acoes

import "errors"

// Códigos de recusa de negócio (plano da Fatia 3, F3-9). As rotas HTTP não têm
// código (respondem texto puro); quem os define é a camada de ações, e o MCP os
// devolve como "<mensagem> [<código>]" (contrato §3.4).
const (
	CodigoEntradaInvalida   = "entrada_invalida"
	CodigoNaoEncontrado     = "nao_encontrado"
	CodigoCNPJDuplicado     = "cnpj_duplicado"
	CodigoHub               = "codigo_hub"
	CodigoNomeDuplicado     = "nome_duplicado"
	CodigoCampanhaCancelada = "campanha_cancelada"
)

// Recusa é a falha de NEGÓCIO de uma ação: a entrada não vale, nada foi
// gravado. É diferente de um erro interno (banco fora do ar, bug): o motor de
// escrita do MCP libera o token de confirmação numa Recusa, e só nela.
type Recusa struct {
	// Mensagem é para a pessoa ler (e para o modelo repetir).
	Mensagem string
	// Codigo é estável e legível por máquina (ver as constantes Codigo*).
	Codigo string
	// Dados é opcional: o que o chamador estruturado (o handler HTTP) precisa
	// para montar a resposta sem reler a Mensagem — por exemplo, `existente`
	// ({id, nome}) do cnpj_duplicado. Nunca entra na Mensagem nem no erro do MCP.
	Dados map[string]any
	// Causa é opcional e serve a quem precisa DISTINGUIR casos de um mesmo
	// código (errors.Is): ErrAlvoInexistente marca o nao_encontrado de "o alvo
	// já não existe" (apagar o que não existe), que a rota HTTP trata como
	// sucesso idempotente (204) e o MCP mostra como nao_encontrado.
	Causa error
}

// ErrAlvoInexistente marca (via Recusa.Causa) o nao_encontrado em que o registro
// pedido simplesmente não existe — diferente de "existe, mas não é desta
// campanha". As rotas HTTP de apagar/desvincular/trocar-tipo respondem 204 nesse
// caso desde sempre (idempotência); as ações o devolvem como Recusa para o MCP
// não dizer "feito" quando nada foi feito.
var ErrAlvoInexistente = errors.New("o registro pedido não existe")

func (r *Recusa) Unwrap() error { return r.Causa }

func (r *Recusa) Error() string {
	if r.Codigo == "" {
		return r.Mensagem
	}
	return r.Mensagem + " [" + r.Codigo + "]"
}
