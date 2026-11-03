// Package acoes concentra as escritas de negócio do E-monitor que o portal
// (handlers HTTP) e o MCP compartilham (plano da Fatia 3, F3-7): validação,
// chamada ao repositório e os gatilhos que a escrita dispara (recategorização,
// reload de índice e workers, aviso ao hub). Os handlers e as tools do MCP
// chamam a MESMA função daqui — a regra de ouro da spec (§3): a tool nunca
// reimplementa o que a tela faz.
package acoes

import "errors"

// Códigos de recusa de negócio (plano da Fatia 3, F3-9). Quem os define é a
// camada de ações; o MCP os devolve como "<mensagem> [<código>]" (contrato
// §3.4) e as rotas HTTP os traduzem em status (handlers.escreveRecusa), em geral
// sem expor o código — só onde a rota já respondia JSON com ele.
const (
	CodigoEntradaInvalida   = "entrada_invalida"
	CodigoNaoEncontrado     = "nao_encontrado"
	CodigoCNPJDuplicado     = "cnpj_duplicado"
	CodigoHub               = "codigo_hub"
	CodigoNomeDuplicado     = "nome_duplicado"
	CodigoCampanhaCancelada = "campanha_cancelada"
	// CodigoEstadoTerminal: cancelar campanha que já está concluída/cancelada.
	// Dados["estado_atual"] traz o estado.
	CodigoEstadoTerminal = "estado_terminal"
	// CodigoCampanhaComProjecoes: apagar a campanha destruiria projeções de
	// OUTRAS campanhas. Dados["foreign_projections"] (int) traz quantas, quando
	// a contagem prévia pegou; a defesa em profundidade do banco não traz.
	CodigoCampanhaComProjecoes = "campanha_com_projecoes"
	// CodigoMaterialNaoVinculado: a veiculação manual pede um material que não está
	// vinculado à emissora nesta campanha.
	CodigoMaterialNaoVinculado = "material_nao_vinculado"
	// CodigoClienteComVinculos: apagar o cliente esbarra em campanhas, materiais ou
	// usuários que ainda apontam para ele. Dados traz "campanhas", "materiais" e
	// "usuarios" (int).
	CodigoClienteComVinculos = "cliente_com_vinculos"
	// CodigoMaterialVinculado: apagar o material esbarra num vínculo com campanha.
	CodigoMaterialVinculado = "material_vinculado"
	// CodigoCampanhaComLoteManual: apagar a campanha esbarra nos comprovantes de
	// veiculação manual (manual_proof_batches) que apontam para ela.
	CodigoCampanhaComLoteManual = "campanha_com_lote_manual"
	// CodigoCampanhaEmPosVenda: a campanha faz parte de um relatório de pós-venda.
	CodigoCampanhaEmPosVenda = "campanha_em_pos_venda"
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

// ErroAposEscrita é a falha de uma ação que JÁ gravou algo e não conseguiu terminar: é o oposto
// da Recusa ("nada foi gravado") e nunca é uma. Mensagem diz à pessoa o que ficou feito e o que
// conferir; não pode carregar SQLSTATE, nome de tabela nem segredo — o motor de aprovação do MCP
// a grava como o erro do pedido (que fica `falhou`) e a mostra em /pendentes. Causa é o erro
// técnico, só para o log e para errors.Is. As rotas HTTP a tratam como qualquer erro interno.
type ErroAposEscrita struct {
	Mensagem string
	Causa    error
}

func (e *ErroAposEscrita) Error() string {
	if e.Causa == nil {
		return e.Mensagem
	}
	return e.Mensagem + ": " + e.Causa.Error()
}

func (e *ErroAposEscrita) Unwrap() error { return e.Causa }

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
