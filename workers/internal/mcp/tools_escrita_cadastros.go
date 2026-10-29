package mcp

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/mail"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"radiocheck/internal/acoes"
	"radiocheck/internal/catalog"
)

// Escritas de cadastro (Lote C, Task 13): cliente, PMM no target e emissora.
// Nenhuma é "só rótulo": o PMM (do cliente e o da emissora) entra nos impactos
// de períodos já fechados, então editar_pmm_target e a troca do PMM da emissora
// escalam para crítico quando atingem veiculação já ocorrida.

func registrarEscritasCadastros(s *sdk.Server, cfg ConfigServidor, p *Principal) {
	ferramentaEscrita(s, cfg, p, escritaCriarCliente(cfg))
	ferramentaEscrita(s, cfg, p, escritaEditarCliente(cfg))
	ferramentaEscrita(s, cfg, p, escritaEditarPMMTarget(cfg))
	ferramentaEscrita(s, cfg, p, escritaEditarEmissora(cfg))
}

// ── Cliente: campos, validação e plano ──────────────────────────────────

const (
	tetoNomeCliente     = 200
	tetoCidadeCliente   = 100
	tetoContatoCliente  = 120
	tetoEmailCliente    = 200
	tetoTelefoneCliente = 40
)

// ufsDoBrasil: as 27 unidades da federação.
var ufsDoBrasil = map[string]bool{
	"AC": true, "AL": true, "AP": true, "AM": true, "BA": true, "CE": true, "DF": true, "ES": true, "GO": true,
	"MA": true, "MT": true, "MS": true, "MG": true, "PA": true, "PB": true, "PR": true, "PE": true, "PI": true,
	"RJ": true, "RN": true, "RS": true, "RO": true, "RR": true, "SC": true, "SP": true, "SE": true, "TO": true,
}

// campoDoCliente descreve um campo editável do cadastro: como validar/normalizar
// o que veio, como ler o valor atual e como gravá-lo no UpdateClientInput. A
// ordem da tabela é a ordem do preview.
type campoDoCliente struct {
	chave  string
	valida func(string) (string, error)
	atual  func(*catalog.Client) string
	grava  func(*catalog.UpdateClientInput, string)
	igual  func(atual, novo string) bool // nil = igualdade de texto
}

func textoDoPtr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func textoCurto(rotulo string, teto int) func(string) (string, error) {
	return func(s string) (string, error) {
		v := aparar(s)
		if err := limiteDeRunas(rotulo, v, teto); err != nil {
			return "", err
		}
		return v, nil
	}
}

// mascararCNPJ valida os 14 dígitos e devolve XX.XXX.XXX/XXXX-XX, como a tela
// grava. "" fica "" (sem CNPJ).
func mascararCNPJ(s string) (string, error) {
	cnpj := aparar(s)
	if cnpj == "" {
		return "", nil
	}
	d := acoes.DigitosDoCNPJ(&cnpj)
	if len(d) != 14 {
		return "", errEntrada("CNPJ inválido: precisa ter 14 dígitos (com ou sem pontuação)")
	}
	return fmt.Sprintf("%s.%s.%s/%s-%s", d[0:2], d[2:5], d[5:8], d[8:12], d[12:14]), nil
}

var camposDoCliente = []campoDoCliente{
	{chave: "nome",
		valida: func(s string) (string, error) {
			v := aparar(s)
			if v == "" {
				return "", errEntrada("o nome do cliente não pode ficar vazio")
			}
			return v, limiteDeRunas("nome", v, tetoNomeCliente)
		},
		atual: func(c *catalog.Client) string { return c.Name },
		grava: func(in *catalog.UpdateClientInput, v string) { in.Name = v }},
	{chave: "cnpj", valida: mascararCNPJ,
		atual: func(c *catalog.Client) string { return textoDoPtr(c.CNPJ) },
		grava: func(in *catalog.UpdateClientInput, v string) { in.CNPJ = ptrTexto(v) },
		// Os mesmos 14 dígitos são o mesmo CNPJ, com ou sem máscara: não é mudança.
		igual: func(a, b string) bool { return acoes.DigitosDoCNPJ(&a) == acoes.DigitosDoCNPJ(&b) }},
	{chave: "cidade", valida: textoCurto("cidade", tetoCidadeCliente),
		atual: func(c *catalog.Client) string { return textoDoPtr(c.City) },
		grava: func(in *catalog.UpdateClientInput, v string) { in.City = ptrTexto(v) }},
	{chave: "uf",
		valida: func(s string) (string, error) {
			uf := strings.ToUpper(aparar(s))
			if uf != "" && !ufsDoBrasil[uf] {
				return "", errEntrada("UF inválida: use a sigla de 2 letras (ex.: MG)")
			}
			return uf, nil
		},
		atual: func(c *catalog.Client) string { return textoDoPtr(c.State) },
		grava: func(in *catalog.UpdateClientInput, v string) { in.State = ptrTexto(v) }},
	{chave: "contato_nome", valida: textoCurto("contato_nome", tetoContatoCliente),
		atual: func(c *catalog.Client) string { return textoDoPtr(c.ContactName) },
		grava: func(in *catalog.UpdateClientInput, v string) { in.ContactName = ptrTexto(v) }},
	{chave: "contato_email",
		valida: func(s string) (string, error) {
			v := aparar(s)
			if v == "" {
				return "", nil
			}
			if err := limiteDeRunas("contato_email", v, tetoEmailCliente); err != nil {
				return "", err
			}
			if a, err := mail.ParseAddress(v); err != nil || a.Address != v {
				return "", errEntrada("e-mail do contato inválido: use só o endereço (ex.: contato@empresa.com.br)")
			}
			return v, nil
		},
		atual: func(c *catalog.Client) string { return textoDoPtr(c.ContactEmail) },
		grava: func(in *catalog.UpdateClientInput, v string) { in.ContactEmail = ptrTexto(v) }},
	{chave: "telefone", valida: textoCurto("telefone", tetoTelefoneCliente),
		atual: func(c *catalog.Client) string { return textoDoPtr(c.Phone) },
		grava: func(in *catalog.UpdateClientInput, v string) { in.Phone = ptrTexto(v) }},
	{chave: "rotulo_target", valida: textoCurto("rotulo_target", acoes.MaxRotuloPublico),
		atual: func(c *catalog.Client) string { return textoDoPtr(c.TargetLabel) },
		grava: func(in *catalog.UpdateClientInput, v string) { in.TargetLabel = ptrTexto(v) }},
}

// valoresCliente: chave do campo → o que veio (nil = não enviado).
type valoresCliente map[string]*string

// planoDeCliente é o cadastro como ficará e o que muda.
type planoDeCliente struct {
	novo     catalog.UpdateClientInput
	mudancas []mudanca
	chaves   []string // as chaves que mudam, na ordem da tabela
}

func (p *planoDeCliente) muda(chave string) bool { return slices.Contains(p.chaves, chave) }

// planejarCliente valida e normaliza o que veio. Com `atual` (edição) parte do
// cadastro atual e só toca o que difere; sem ele (criação) grava o que veio. O
// logo e o CEP, que o MCP não expõe, ficam como estão.
func planejarCliente(atual *catalog.Client, enviados valoresCliente) (*planoDeCliente, error) {
	pl := &planoDeCliente{}
	if atual != nil {
		pl.novo = catalog.UpdateClientInput{
			Name: atual.Name, LogoURL: atual.LogoURL, ContactEmail: atual.ContactEmail, ContactName: atual.ContactName,
			Phone: atual.Phone, CNPJ: atual.CNPJ, CEP: atual.CEP, City: atual.City, State: atual.State, TargetLabel: atual.TargetLabel,
		}
	}
	for _, c := range camposDoCliente {
		v := enviados[c.chave]
		if v == nil {
			continue
		}
		norm, err := c.valida(*v)
		if err != nil {
			return nil, err
		}
		cur := ""
		if atual != nil {
			cur = c.atual(atual)
			igual := cur == norm
			if c.igual != nil {
				igual = c.igual(cur, norm)
			}
			if igual {
				continue
			}
		} else if norm == "" {
			continue
		}
		c.grava(&pl.novo, norm)
		pl.chaves = append(pl.chaves, c.chave)
		pl.mudancas = append(pl.mudancas, mudanca{Campo: c.chave, De: textoOuNil(&cur), Para: textoOuNil(&norm)})
	}
	return pl, nil
}

// nomeJaCadastrado diz se já existe cliente com este nome (ignora a caixa): não
// recusa — o nome não é único —, mas é a armadilha clássica do cadastro duplicado.
func nomeJaCadastrado(ctx context.Context, L *Leitura, nome string) (*catalog.Client, error) {
	cs, _, err := L.Clientes.ListPaged(ctx, nome, 1, 20, true)
	if err != nil {
		return nil, err
	}
	for i := range cs {
		if strings.EqualFold(cs[i].Name, nome) {
			return &cs[i], nil
		}
	}
	return nil, nil
}

// algumCampoDoHub: o hub recebe (cliente.upsert) tudo do cadastro menos o rótulo
// do público-alvo; diz se alguma das chaves editadas é um campo que ele recebe.
func algumCampoDoHub(chaves []string) bool {
	for _, k := range chaves {
		if k != "rotulo_target" {
			return true
		}
	}
	return false
}

const ressalvaHubNaoAvisado = "A edição de cliente não avisa o hub: o cadastro dele lá não muda sozinho."

// ── criar_cliente ───────────────────────────────────────────────────────

type entradaCriarCliente struct {
	Nome         string `json:"nome" jsonschema:"Nome do cliente (anunciante), até 200 caracteres. Antes de criar, procure em listar_clientes: o cadastro duplicado é o erro mais comum."`
	CNPJ         string `json:"cnpj,omitempty" jsonschema:"CNPJ com 14 dígitos, com ou sem pontuação. Nunca invente: se a pessoa não informou, omita. Dois clientes não podem ter o mesmo CNPJ."`
	Cidade       string `json:"cidade,omitempty" jsonschema:"Cidade do cliente."`
	UF           string `json:"uf,omitempty" jsonschema:"Sigla do estado (ex.: MG)."`
	ContatoNome  string `json:"contato_nome,omitempty" jsonschema:"Nome da pessoa de contato."`
	ContatoEmail string `json:"contato_email,omitempty" jsonschema:"E-mail do contato (só o endereço)."`
	Telefone     string `json:"telefone,omitempty" jsonschema:"Telefone do contato."`
	RotuloTarget string `json:"rotulo_target,omitempty" jsonschema:"Rótulo do público-alvo (ex.: Mulheres 25-49), até 200 caracteres: vira o sufixo dos números no target."`
	CampoConfirmacao
}

func (in entradaCriarCliente) valores() valoresCliente {
	v := valoresCliente{"nome": &in.Nome}
	for chave, s := range map[string]*string{
		"cnpj": &in.CNPJ, "cidade": &in.Cidade, "uf": &in.UF, "contato_nome": &in.ContatoNome,
		"contato_email": &in.ContatoEmail, "telefone": &in.Telefone, "rotulo_target": &in.RotuloTarget,
	} {
		if aparar(*s) != "" {
			v[chave] = s
		}
	}
	return v
}

func escritaCriarCliente(cfg ConfigServidor) Escrita[entradaCriarCliente] {
	return Escrita[entradaCriarCliente]{
		Nome: "criar_cliente",
		Descricao: "Cria um cliente (anunciante): nome e, se a pessoa informou, CNPJ, cidade/UF, contato e rótulo do público-alvo. " +
			"A armadilha é o cadastro duplicado: antes de criar, procure em listar_clientes pelo nome e pelo CNPJ (dois clientes " +
			"não podem ter o mesmo CNPJ; o preview recusa e diz qual já existe). Nunca invente CNPJ nem contato: omita o que a " +
			"pessoa não deu. Se o hub estiver configurado, o cliente é enviado a ele em segundo plano (sem reenvio se falhar). " +
			"Não cria usuário nem campanha. Mostre o preview e espere a pessoa aprovar antes de confirmar.",
		Nivel:   Confirma,
		Recurso: func(entradaCriarCliente) (string, *uuid.UUID) { return "client", nil },
		Tela:    func(*Escritas, entradaCriarCliente) *string { return cfg.link("/clients") },
		Preview: func(ctx context.Context, E *Escritas, in entradaCriarCliente) (previa, error) {
			pl, err := planejarCliente(nil, in.valores())
			if err != nil {
				return previa{}, err
			}
			if err := E.Acoes.ChecarCNPJ(ctx, pl.novo.CNPJ, uuid.Nil); err != nil {
				return previa{}, err
			}
			pv := previa{Resumo: fmt.Sprintf("Criar o cliente %q", pl.novo.Name), Mudancas: pl.mudancas,
				Efeitos: []string{"O cliente passa a existir em listar_clientes; ainda sem campanha, material nem usuário."}}
			if E.Acoes.Hub != nil && E.Acoes.Hub.Configured() {
				pv.Efeitos = append(pv.Efeitos, "O cliente é enviado ao hub (cliente.upsert) em segundo plano.")
			}
			if igual, err := nomeJaCadastrado(ctx, E.Leitura, pl.novo.Name); err != nil {
				return previa{}, err
			} else if igual != nil {
				pv.Ressalvas = append(pv.Ressalvas, fmt.Sprintf(
					"Já existe um cliente com este nome: %q (id %s). Se for o mesmo anunciante, não crie outro: use o existente.", igual.Name, igual.ID))
			}
			return pv, nil
		},
		Executar: func(ctx context.Context, E *Escritas, _ *Principal, in entradaCriarCliente) (feito, error) {
			pl, err := planejarCliente(nil, in.valores())
			if err != nil {
				return feito{}, err
			}
			c, err := E.Acoes.CriarCliente(ctx, catalog.CreateClientInput(pl.novo))
			if err != nil {
				return feito{}, err
			}
			saida, err := lerClienteCompleto(ctx, E.Leitura, c)
			if err != nil {
				return feito{}, err
			}
			f := feito{Resultado: saida, Recurso: "client", RecursoID: &c.ID}
			if E.Acoes.Hub != nil && E.Acoes.Hub.Configured() {
				f.Ressalvas = []string{"O aviso ao hub (cliente.upsert) segue em segundo plano; se o hub estiver fora do ar o cliente fica sem aviso e não há reenvio."}
			}
			return f, nil
		},
	}
}

// ── editar_cliente ──────────────────────────────────────────────────────

type entradaEditarCliente struct {
	ClienteID    string  `json:"cliente_id" jsonschema:"Id (uuid) do cliente, vindo de listar_clientes. Nunca invente: pergunte."`
	Nome         *string `json:"nome,omitempty" jsonschema:"Novo nome (até 200 caracteres, não pode ficar vazio). Omita para manter."`
	CNPJ         *string `json:"cnpj,omitempty" jsonschema:"Novo CNPJ (14 dígitos, com ou sem pontuação); texto vazio apaga o CNPJ. Omita para manter. Dois clientes não podem ter o mesmo CNPJ."`
	Cidade       *string `json:"cidade,omitempty" jsonschema:"Nova cidade; texto vazio apaga. Omita para manter."`
	UF           *string `json:"uf,omitempty" jsonschema:"Nova sigla de estado (ex.: MG); texto vazio apaga. Omita para manter."`
	ContatoNome  *string `json:"contato_nome,omitempty" jsonschema:"Novo contato; texto vazio apaga. Omita para manter."`
	ContatoEmail *string `json:"contato_email,omitempty" jsonschema:"Novo e-mail do contato; texto vazio apaga. Omita para manter."`
	Telefone     *string `json:"telefone,omitempty" jsonschema:"Novo telefone; texto vazio apaga. Omita para manter."`
	RotuloTarget *string `json:"rotulo_target,omitempty" jsonschema:"Novo rótulo do público-alvo (ex.: Mulheres 25-49); texto vazio apaga. Omita para manter."`
	CampoConfirmacao
}

func (in entradaEditarCliente) valores() valoresCliente {
	return valoresCliente{
		"nome": in.Nome, "cnpj": in.CNPJ, "cidade": in.Cidade, "uf": in.UF, "contato_nome": in.ContatoNome,
		"contato_email": in.ContatoEmail, "telefone": in.Telefone, "rotulo_target": in.RotuloTarget,
	}
}

// edicaoDeCliente é o plano de uma edição, com o cliente lido do banco.
type edicaoDeCliente struct {
	id    uuid.UUID
	atual *catalog.Client
	*planoDeCliente
}

// planejarEdicaoDeCliente lê o cliente, aplica SÓ os campos enviados que de fato
// diferem (edição parcial, F3-10) e confere o CNPJ quando os 14 dígitos mudam.
func planejarEdicaoDeCliente(ctx context.Context, E *Escritas, in entradaEditarCliente) (*edicaoDeCliente, error) {
	id, err := uuidDaEntrada("cliente_id", in.ClienteID)
	if err != nil {
		return nil, err
	}
	enviados := in.valores()
	enviouAlgo := false
	for _, v := range enviados {
		if v != nil {
			enviouAlgo = true
		}
	}
	if !enviouAlgo {
		return nil, errEntrada("informe ao menos um campo a alterar (nome, cnpj, cidade, uf, contato_nome, contato_email, telefone ou rotulo_target)")
	}
	atual, err := E.Leitura.Clientes.Get(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNaoEncontrado("cliente")
	}
	if err != nil {
		return nil, err
	}
	pl, err := planejarCliente(atual, enviados)
	if err != nil {
		return nil, err
	}
	if len(pl.mudancas) == 0 {
		return nil, errEntrada("nada a alterar: os valores enviados já são os atuais do cliente")
	}
	if pl.muda("cnpj") {
		if err := E.Acoes.ChecarCNPJ(ctx, pl.novo.CNPJ, id); err != nil {
			return nil, err
		}
	}
	return &edicaoDeCliente{id: id, atual: atual, planoDeCliente: pl}, nil
}

func escritaEditarCliente(cfg ConfigServidor) Escrita[entradaEditarCliente] {
	return Escrita[entradaEditarCliente]{
		Nome: "editar_cliente",
		Descricao: "Edita o cadastro de um cliente: só os campos enviados mudam (nome, cnpj, cidade, uf, contato_nome, contato_email, " +
			"telefone, rotulo_target); texto vazio apaga um campo opcional. A armadilha: a edição NÃO avisa o hub — o cadastro dele lá " +
			"não muda sozinho — e o CNPJ não pode repetir o de outro cliente (o preview recusa e diz qual). Não mexe em usuários, " +
			"campanhas, materiais, logo nem na ponte com o hub. Nunca invente o cliente_id nem dados do cliente: pergunte. " +
			"Mostre o preview e espere a pessoa aprovar antes de confirmar.",
		Nivel: Confirma,
		Recurso: func(in entradaEditarCliente) (string, *uuid.UUID) {
			if id, err := uuid.Parse(in.ClienteID); err == nil {
				return "client", &id
			}
			return "client", nil
		},
		Tela: func(*Escritas, entradaEditarCliente) *string { return cfg.link("/clients") },
		Preview: func(ctx context.Context, E *Escritas, in entradaEditarCliente) (previa, error) {
			pl, err := planejarEdicaoDeCliente(ctx, E, in)
			if err != nil {
				return previa{}, err
			}
			pv := previa{Resumo: fmt.Sprintf("Editar o cadastro do cliente %q", pl.atual.Name), Mudancas: pl.mudancas,
				Efeitos: []string{"Só o cadastro muda: campanhas, materiais e usuários do cliente seguem como estão."}}
			if algumCampoDoHub(pl.chaves) {
				pv.Efeitos = append(pv.Efeitos, "O hub não é avisado da edição: o cadastro do cliente lá não muda sozinho.")
			}
			return pv, nil
		},
		Executar: func(ctx context.Context, E *Escritas, _ *Principal, in entradaEditarCliente) (feito, error) {
			pl, err := planejarEdicaoDeCliente(ctx, E, in)
			if err != nil {
				return feito{}, err
			}
			c, err := E.Acoes.EditarCliente(ctx, pl.id, pl.novo)
			if err != nil {
				return feito{}, err
			}
			saida, err := lerClienteCompleto(ctx, E.Leitura, c)
			if err != nil {
				return feito{}, err
			}
			f := feito{Resultado: saida, Recurso: "client", RecursoID: &c.ID}
			if algumCampoDoHub(pl.chaves) {
				f.Ressalvas = []string{ressalvaHubNaoAvisado}
			}
			return f, nil
		},
	}
}

// ── editar_pmm_target ───────────────────────────────────────────────────

const (
	tetoEntradasPMM = 500
	// tetoLinhasPMMNaResposta: quantas linhas voltam no resultado (o total vem sempre).
	tetoLinhasPMMNaResposta = 100
	tetoPMMTarget           = math.MaxInt32 // a coluna é INTEGER
)

type linhaEntradaPMM struct {
	EmissoraID string `json:"emissora_id" jsonschema:"Id (uuid) da emissora, vindo de buscar_emissoras ou pmm_target_cliente. Nunca invente: pergunte."`
	PMMTarget  *int   `json:"pmm_target" jsonschema:"PMM no target do cliente nessa emissora: inteiro de 0 em diante (0 conta e soma nada) ou null para REMOVER a linha (a emissora sai do total de impactos no target; não vira zero). Obrigatório, mesmo quando null."`
}

type entradaEditarPMMTarget struct {
	ClienteID string            `json:"cliente_id" jsonschema:"Id (uuid) do cliente, vindo de listar_clientes. Nunca invente: pergunte."`
	Entradas  []linhaEntradaPMM `json:"entradas" jsonschema:"Até 500 pares emissora/valor. Só as emissoras listadas mudam; as outras ficam como estão."`
	CampoConfirmacao
}

// emissorasPorID lê as emissoras de uma lista de ids (em lotes de 500, o teto da
// tela); as que não existem ficam fora do mapa.
func emissorasPorID(ctx context.Context, L *Leitura, ids []uuid.UUID) (map[uuid.UUID]catalog.Station, error) {
	out := make(map[uuid.UUID]catalog.Station, len(ids))
	for len(ids) > 0 {
		n := min(500, len(ids))
		res, err := L.Emissoras.List(ctx, catalog.ListInput{IDs: ids[:n]})
		if err != nil {
			return nil, err
		}
		for _, s := range res.Data {
			out[s.ID] = s
		}
		ids = ids[n:]
	}
	return out, nil
}

// edicaoDePMM é o lote validado: só as entradas que de fato mudam o que está gravado.
type edicaoDePMM struct {
	cliente   *catalog.Client
	emissoras map[uuid.UUID]catalog.Station
	atuais    map[uuid.UUID]int
	efetivas  []catalog.TargetPMMEntry
	semEfeito int // entradas que já estavam como pedido
}

func (pl *edicaoDePMM) ids() []uuid.UUID {
	ids := make([]uuid.UUID, len(pl.efetivas))
	for i, e := range pl.efetivas {
		ids[i] = e.StationID
	}
	return ids
}

func planejarEdicaoDePMM(ctx context.Context, E *Escritas, in entradaEditarPMMTarget) (*edicaoDePMM, error) {
	cid, err := uuidDaEntrada("cliente_id", in.ClienteID)
	if err != nil {
		return nil, err
	}
	if len(in.Entradas) == 0 {
		return nil, errEntrada("entradas vazia: passe ao menos um par emissora_id / pmm_target")
	}
	if len(in.Entradas) > tetoEntradasPMM {
		return nil, errEntrada(fmt.Sprintf("no máximo %d entradas por chamada", tetoEntradasPMM))
	}
	entradas := make([]catalog.TargetPMMEntry, 0, len(in.Entradas))
	vistas := make(map[uuid.UUID]bool, len(in.Entradas))
	for _, l := range in.Entradas {
		id, err := uuidDaEntrada("emissora_id", l.EmissoraID)
		if err != nil {
			return nil, err
		}
		if vistas[id] {
			return nil, errEntrada(fmt.Sprintf("a emissora %s aparece mais de uma vez em entradas: mande um valor só por emissora", id))
		}
		vistas[id] = true
		if l.PMMTarget != nil && (*l.PMMTarget < 0 || *l.PMMTarget > tetoPMMTarget) {
			return nil, errEntrada(fmt.Sprintf("pmm_target da emissora %s precisa ser um inteiro entre 0 e %d (ou null para remover)", id, tetoPMMTarget))
		}
		entradas = append(entradas, catalog.TargetPMMEntry{StationID: id, PMMTarget: l.PMMTarget})
	}
	cliente, err := E.Leitura.Clientes.Get(ctx, cid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNaoEncontrado("cliente")
	}
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, len(entradas))
	for i, e := range entradas {
		ids[i] = e.StationID
	}
	emissoras, err := emissorasPorID(ctx, E.Leitura, ids)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, ok := emissoras[id]; !ok {
			return nil, errNaoEncontrado(fmt.Sprintf("emissora %s", id))
		}
	}
	atuais, err := E.Leitura.PMMTarget.ForStations(ctx, cid, ids)
	if err != nil {
		return nil, err
	}
	pl := &edicaoDePMM{cliente: cliente, emissoras: emissoras, atuais: atuais}
	for _, e := range entradas {
		atual, tem := atuais[e.StationID]
		switch {
		case e.PMMTarget == nil && !tem: // remover o que não existe
			pl.semEfeito++
		case e.PMMTarget != nil && tem && *e.PMMTarget == atual:
			pl.semEfeito++
		default:
			pl.efetivas = append(pl.efetivas, e)
		}
	}
	if len(pl.efetivas) == 0 {
		return nil, errEntrada("nada a alterar: todas as entradas já estão como pedido")
	}
	return pl, nil
}

// escalaPMM diz se o lote atinge número que o cliente já viu (F3-4): o cliente
// tem veiculação aprovada nas emissoras do lote OU tem campanha (não cancelada)
// com alguma delas que começou antes de hoje (SP) — o total de impactos no target
// aparece mesmo sem tocada, e o PMM é retroativo (sem versionamento, migration 0054).
func escalaPMM(ctx context.Context, E *Escritas, pl *edicaoDePMM) (Escalada, error) {
	ids := pl.ids()
	cid := pl.cliente.ID
	ha, err := E.Leitura.Veiculacoes.HaVeiculacao(ctx, catalog.EscopoVeiculacao{ClienteID: &cid, EmissoraIDs: ids})
	if err != nil {
		return Escalada{}, err
	}
	if ha {
		return Escalada{Nivel: Critico, Motivo: "O cliente já tem veiculação aprovada em emissoras deste lote: o PMM no target é retroativo " +
			"(não há versionamento) e muda os impactos no target de períodos passados, que o cliente já viu."}, nil
	}
	campanhas, err := E.Leitura.Campanhas.ListFiltered(ctx, nil, []uuid.UUID{cid})
	if err != nil {
		return Escalada{}, err
	}
	hoje := hojeSP(E.agora())
	for _, c := range campanhas {
		if c.Status == "cancelada" || !c.StartDate.Before(hoje) {
			continue
		}
		for _, id := range c.TargetStations {
			if slices.Contains(ids, id) {
				return Escalada{Nivel: Critico, Motivo: fmt.Sprintf(
					"A campanha %q do cliente já começou (%s) e usa emissoras deste lote: o total de impactos no target aparece mesmo antes "+
						"da primeira veiculação, e o PMM no target é retroativo (não há versionamento).", c.Name, dia(c.StartDate))}, nil
			}
		}
	}
	return Escalada{}, nil
}

type saidaEditarPMMTarget struct {
	Cliente   ref              `json:"cliente"`
	Gravadas  int              `json:"gravadas"`
	Removidas int              `json:"removidas"`
	Emissoras []linhaPMMTarget `json:"emissoras"`
}

// linhaDePMM é a linha no formato de pmm_target_cliente, com o valor dado.
func linhaDePMM(st catalog.Station, alvo *int) linhaPMMTarget {
	banda := st.Band
	return linhaPMMTarget{
		Emissora: refDe(st.ID, st.Name), Banda: &banda, FrequenciaMHz: st.FrequencyMHz, Cidade: st.City, UF: st.State,
		PMM: st.PMM, PMMTarget: alvo,
	}
}

func escritaEditarPMMTarget(cfg ConfigServidor) Escrita[entradaEditarPMMTarget] {
	return Escrita[entradaEditarPMMTarget]{
		Nome: "editar_pmm_target",
		Descricao: "Cadastra, altera ou remove o PMM no target de um cliente, emissora por emissora (a base dos impactos no target). " +
			"Só as emissoras listadas mudam. pmm_target é inteiro de 0 em diante ou null para REMOVER a linha — remover não é zerar: " +
			"sem linha a emissora fica FORA do total de impactos no target; com 0 ela conta e soma nada. A armadilha: o valor é " +
			"retroativo (não há versionamento) e vale para todas as campanhas do cliente. Sobe para crítico quando o cliente já tem " +
			"veiculação nessas emissoras ou tem campanha em andamento (já iniciada) com elas; nesse caso nada é feito e a resposta " +
			"traz o link da tela. Nunca invente ids nem valores: pergunte. Mostre o preview e espere a pessoa aprovar antes de confirmar.",
		Nivel: Confirma,
		Escalar: func(ctx context.Context, E *Escritas, in entradaEditarPMMTarget) (Escalada, error) {
			pl, err := planejarEdicaoDePMM(ctx, E, in)
			if err != nil {
				return Escalada{}, err
			}
			return escalaPMM(ctx, E, pl)
		},
		Recurso: func(in entradaEditarPMMTarget) (string, *uuid.UUID) {
			if id, err := uuid.Parse(in.ClienteID); err == nil {
				return "client_station_pmm", &id
			}
			return "client_station_pmm", nil
		},
		Tela: func(_ *Escritas, in entradaEditarPMMTarget) *string {
			if id, err := uuid.Parse(in.ClienteID); err == nil {
				return cfg.link("/clients/" + id.String() + "/target-pmm")
			}
			return cfg.link("/clients")
		},
		Preview: func(ctx context.Context, E *Escritas, in entradaEditarPMMTarget) (previa, error) {
			pl, err := planejarEdicaoDePMM(ctx, E, in)
			if err != nil {
				return previa{}, err
			}
			mudancas := make([]mudanca, 0, len(pl.efetivas))
			var grava, remove int
			for _, e := range pl.efetivas {
				st := pl.emissoras[e.StationID]
				var de, para any
				if v, tem := pl.atuais[e.StationID]; tem {
					de = v
				}
				if e.PMMTarget != nil {
					para = *e.PMMTarget
					grava++
				} else {
					remove++
				}
				mudancas = append(mudancas, mudanca{Campo: fmt.Sprintf("%s (%s)", st.Name, st.ID), De: de, Para: para})
			}
			pv := previa{
				Resumo: fmt.Sprintf("PMM no target de %q: gravar %d, remover %d", pl.cliente.Name, grava, remove),
				Efeitos: []string{
					"O PMM no target é retroativo (sem versionamento) e vale para todas as campanhas do cliente: relatórios, /insights e CPM " +
						"passam a usar o novo valor, inclusive em períodos já fechados.",
					"null remove a linha: a emissora sai do total de impactos no target e do contador de emissoras com target (não vira zero).",
				},
			}
			if len(mudancas) > tetoLinhasPMMNaResposta {
				pv.Ressalvas = append(pv.Ressalvas, fmt.Sprintf("mostrando %d de %d mudanças", tetoLinhasPMMNaResposta, len(mudancas)))
				mudancas = mudancas[:tetoLinhasPMMNaResposta]
			}
			pv.Mudancas = mudancas
			if pl.semEfeito > 0 {
				pv.Ressalvas = append(pv.Ressalvas, fmt.Sprintf("%d entrada(s) já estavam como pedido e ficam de fora.", pl.semEfeito))
			}
			// Emissoras que nenhuma campanha do cliente usa: o valor fica gravado, mas só aparece quando uma campanha a incluir.
			campanhas, err := E.Leitura.Campanhas.ListFiltered(ctx, nil, []uuid.UUID{pl.cliente.ID})
			if err != nil {
				return previa{}, err
			}
			usadas := map[uuid.UUID]bool{}
			for _, c := range campanhas {
				for _, id := range c.TargetStations {
					usadas[id] = true
				}
			}
			var fora []string
			for _, e := range pl.efetivas {
				if !usadas[e.StationID] {
					fora = append(fora, pl.emissoras[e.StationID].Name)
				}
			}
			if len(fora) > 0 {
				pv.Ressalvas = append(pv.Ressalvas, fmt.Sprintf(
					"%d emissora(s) não estão em nenhuma campanha do cliente (%s): o valor fica gravado, mas só aparece quando uma campanha a incluir.",
					len(fora), strings.Join(fora[:min(len(fora), 5)], ", ")))
			}
			return pv, nil
		},
		Executar: func(ctx context.Context, E *Escritas, _ *Principal, in entradaEditarPMMTarget) (feito, error) {
			pl, err := planejarEdicaoDePMM(ctx, E, in)
			if err != nil {
				return feito{}, err
			}
			gravadas, removidas, err := E.Leitura.PMMTarget.BulkUpsert(ctx, pl.cliente.ID, pl.efetivas)
			if err != nil {
				return feito{}, err
			}
			depois, err := E.Leitura.PMMTarget.ForStations(ctx, pl.cliente.ID, pl.ids())
			if err != nil {
				return feito{}, err
			}
			linhas := make([]linhaPMMTarget, 0, len(pl.efetivas))
			for _, e := range pl.efetivas {
				var alvo *int
				if v, tem := depois[e.StationID]; tem {
					alvo = &v
				}
				linhas = append(linhas, linhaDePMM(pl.emissoras[e.StationID], alvo))
			}
			f := feito{Recurso: "client_station_pmm", RecursoID: &pl.cliente.ID, Ressalvas: []string{ressalvaPMMTarget}}
			if len(linhas) > tetoLinhasPMMNaResposta {
				f.Ressalvas = append(f.Ressalvas, fmt.Sprintf("mostrando %d de %d emissoras alteradas", tetoLinhasPMMNaResposta, len(linhas)))
				linhas = linhas[:tetoLinhasPMMNaResposta]
			}
			f.Resultado = saidaEditarPMMTarget{Cliente: refDe(pl.cliente.ID, pl.cliente.Name), Gravadas: gravadas, Removidas: removidas, Emissoras: linhas}
			return f, nil
		},
	}
}

// ── editar_emissora ─────────────────────────────────────────────────────

type entradaEditarEmissora struct {
	EmissoraID    string   `json:"emissora_id" jsonschema:"Id (uuid) da emissora, vindo de buscar_emissoras. Nunca invente: pergunte."`
	Nome          *string  `json:"nome,omitempty" jsonschema:"Novo nome da emissora (não pode ficar vazio). Omita para manter."`
	FrequenciaMHz *float64 `json:"frequencia_mhz,omitempty" jsonschema:"Nova frequência: MHz para FM (ex.: 99.9), kHz para AM (ex.: 1180). Omita para manter."`
	Cidade        *string  `json:"cidade,omitempty" jsonschema:"Nova cidade (não pode ficar vazia); a coordenada do mapa é refeita pela cidade+UF. Omita para manter."`
	UF            *string  `json:"uf,omitempty" jsonschema:"Nova sigla de estado (2 letras). Omita para manter."`
	PMM           *float64 `json:"pmm,omitempty" jsonschema:"Novo PMM geral da emissora (audiência média por inserção, de 0 em diante). Omita para manter; não há como apagar."`
	CampoConfirmacao
}

// edicaoDeEmissora é o plano de uma edição: só o que de fato muda.
type edicaoDeEmissora struct {
	id       uuid.UUID
	atual    *catalog.Station
	enviar   acoes.CadastroEmissora
	mudancas []mudanca

	mudaNome, mudaDial, mudaLocal, mudaPMM bool
}

func arredonda2(f float64) float64 { return math.Round(f*100) / 100 }

func iguais2(a *float64, b float64) bool { return a != nil && arredonda2(*a) == arredonda2(b) }

// planejarEdicaoDeEmissora valida (com a MESMA validação da ação), lê a emissora
// e separa o que muda. Recusa a edição que não muda nada.
func planejarEdicaoDeEmissora(ctx context.Context, L *Leitura, in entradaEditarEmissora) (*edicaoDeEmissora, error) {
	id, err := uuidDaEntrada("emissora_id", in.EmissoraID)
	if err != nil {
		return nil, err
	}
	if in.Nome == nil && in.FrequenciaMHz == nil && in.Cidade == nil && in.UF == nil && in.PMM == nil {
		return nil, errEntrada("informe ao menos um campo a alterar (nome, frequencia_mhz, cidade, uf ou pmm)")
	}
	val, err := acoes.ValidarCadastroEmissora(acoes.CadastroEmissora{
		Nome: in.Nome, FrequenciaMHz: in.FrequenciaMHz, Cidade: in.Cidade, UF: in.UF, PMM: in.PMM})
	if err != nil {
		return nil, err
	}
	st, err := L.Emissoras.Get(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNaoEncontrado("emissora")
	}
	if err != nil {
		return nil, err
	}
	pl := &edicaoDeEmissora{id: id, atual: st}
	if val.Name != nil && *val.Name != st.Name {
		pl.mudaNome, pl.enviar.Nome = true, val.Name
		pl.mudancas = append(pl.mudancas, mudanca{Campo: "nome", De: st.Name, Para: *val.Name})
	}
	if val.FrequencyMHz != nil && !iguais2(st.FrequencyMHz, *val.FrequencyMHz) {
		pl.mudaDial, pl.enviar.FrequenciaMHz = true, val.FrequencyMHz
		var de any
		if st.FrequencyMHz != nil {
			de = *st.FrequencyMHz
		}
		pl.mudancas = append(pl.mudancas, mudanca{Campo: "frequencia_mhz", De: de, Para: *val.FrequencyMHz})
	}
	if val.City != nil && *val.City != textoDoPtr(st.City) {
		pl.mudaLocal, pl.enviar.Cidade = true, val.City
		pl.mudancas = append(pl.mudancas, mudanca{Campo: "cidade", De: textoOuNil(st.City), Para: *val.City})
	}
	if val.State != nil && *val.State != textoDoPtr(st.State) {
		pl.mudaLocal, pl.enviar.UF = true, val.State
		pl.mudancas = append(pl.mudancas, mudanca{Campo: "uf", De: textoOuNil(st.State), Para: *val.State})
	}
	if val.PMM != nil && !iguais2(st.PMM, *val.PMM) {
		pl.mudaPMM, pl.enviar.PMM = true, val.PMM
		var de any
		if st.PMM != nil {
			de = *st.PMM
		}
		pl.mudancas = append(pl.mudancas, mudanca{Campo: "pmm", De: de, Para: *val.PMM})
	}
	if len(pl.mudancas) == 0 {
		return nil, errEntrada("nada a alterar: os valores enviados já são os atuais da emissora")
	}
	return pl, nil
}

const ressalvaAnatelNaoRecalculada = "A classe, o raio de cobertura e as cidades no alcance (Anatel) NÃO são recalculados: seguem os do cruzamento anterior " +
	"(feito por dial e geografia) até ele ser refeito pela operação."

func escritaEditarEmissora(cfg ConfigServidor) Escrita[entradaEditarEmissora] {
	return Escrita[entradaEditarEmissora]{
		Nome: "editar_emissora",
		Descricao: "Edita o cadastro de uma emissora: só os campos enviados mudam (nome, frequencia_mhz, cidade, uf, pmm). Nunca troca " +
			"a stream_url, a banda, o logo nem os metadados. As armadilhas: (1) o PMM geral entra nos impactos de TODAS as campanhas que " +
			"usam a emissora, inclusive dias já veiculados (não há versionamento) — sobe para crítico quando o pmm muda e a emissora já " +
			"tem veiculação aprovada; nesse caso nada é feito e a resposta traz o link da tela; (2) mudar dial ou cidade não recalcula a " +
			"classe/raio da Anatel. Nunca invente o emissora_id nem os valores: pergunte. " +
			"Mostre o preview e espere a pessoa aprovar antes de confirmar.",
		Nivel: Confirma,
		Escalar: func(ctx context.Context, E *Escritas, in entradaEditarEmissora) (Escalada, error) {
			if in.PMM == nil {
				return Escalada{}, nil
			}
			pl, err := planejarEdicaoDeEmissora(ctx, E.Leitura, in)
			if err != nil {
				return Escalada{}, err
			}
			if !pl.mudaPMM {
				return Escalada{}, nil
			}
			ha, err := E.Leitura.Veiculacoes.HaVeiculacaoNaEmissora(ctx, []uuid.UUID{pl.id})
			if err != nil {
				return Escalada{}, err
			}
			if !ha {
				return Escalada{}, nil
			}
			return Escalada{Nivel: Critico, Motivo: "A emissora já tem veiculação aprovada: o PMM geral entra no cálculo dos impactos de todas as " +
				"campanhas que a usam, inclusive de dias já veiculados (não há versionamento), então a mudança reescreve números que o cliente já viu."}, nil
		},
		Recurso: func(in entradaEditarEmissora) (string, *uuid.UUID) {
			if id, err := uuid.Parse(in.EmissoraID); err == nil {
				return "station", &id
			}
			return "station", nil
		},
		Tela: func(_ *Escritas, in entradaEditarEmissora) *string {
			if id, err := uuid.Parse(in.EmissoraID); err == nil {
				return cfg.link("/stations/" + id.String() + "/edit")
			}
			return cfg.link("/stations")
		},
		Preview: func(ctx context.Context, E *Escritas, in entradaEditarEmissora) (previa, error) {
			pl, err := planejarEdicaoDeEmissora(ctx, E.Leitura, in)
			if err != nil {
				return previa{}, err
			}
			pv := previa{Resumo: fmt.Sprintf("Editar o cadastro da emissora %q", pl.atual.Name), Mudancas: pl.mudancas,
				Efeitos: []string{"A stream_url, a banda, o logo e os metadados não mudam."}}
			if pl.mudaLocal {
				pv.Efeitos = append(pv.Efeitos, "A coordenada do mapa é refeita pela cidade e UF (se o par não for conhecido, a coordenada anterior fica).")
			}
			if pl.mudaDial || pl.mudaLocal {
				pv.Ressalvas = append(pv.Ressalvas, ressalvaAnatelNaoRecalculada)
			}
			if pl.mudaPMM {
				ha, err := E.Leitura.Veiculacoes.HaVeiculacaoNaEmissora(ctx, []uuid.UUID{pl.id})
				if err != nil {
					return previa{}, err
				}
				efeito := "O PMM geral entra nos impactos de todas as campanhas que usam a emissora (/campaigns, /insights, relatórios), sem versionamento. "
				if ha {
					efeito += "A emissora já tem veiculação aprovada: os impactos de dias passados mudam."
				} else {
					efeito += "A emissora não tem veiculação aprovada, então hoje não há número passado a mudar."
				}
				pv.Efeitos = append(pv.Efeitos, efeito)
			}
			return pv, nil
		},
		Executar: func(ctx context.Context, E *Escritas, _ *Principal, in entradaEditarEmissora) (feito, error) {
			pl, err := planejarEdicaoDeEmissora(ctx, E.Leitura, in)
			if err != nil {
				return feito{}, err
			}
			res, err := E.Acoes.EditarCadastroEmissora(ctx, pl.id, pl.enviar)
			if err != nil {
				return feito{}, err
			}
			saida, err := lerEmissoraCompleta(ctx, E.Leitura, res.Emissora)
			if err != nil {
				return feito{}, err
			}
			f := feito{Resultado: saida, Recurso: "station", RecursoID: &pl.id}
			if res.GeocodificacaoFalhou {
				f.Ressalvas = append(f.Ressalvas, "A cidade/UF não têm coordenada conhecida: o mapa e o raio de cobertura seguem na coordenada anterior (que pode estar velha).")
			}
			if pl.mudaDial || pl.mudaLocal {
				f.Ressalvas = append(f.Ressalvas, ressalvaAnatelNaoRecalculada)
			}
			return f, nil
		},
	}
}
