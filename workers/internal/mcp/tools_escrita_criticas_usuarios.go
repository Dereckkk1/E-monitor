package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"radiocheck/internal/acoes"
	"radiocheck/internal/catalog"
	"radiocheck/internal/users"
)

// Escritas CRÍTICAS de usuário (Fatia 4, Task 17): criar, editar e desativar.
// Nenhuma executa na chamada: o motor grava um pedido pendente e quem executa é a
// aprovação do dono em /pendentes, que reroda o Preview (por isso TODA recusa que
// depende do estado mora nele ou no planejar* que ele chama) e só então o
// Executar. Nenhuma recebe token de confirmação. Todas devolvem o usuário no
// formato de obter_usuario (montarUsuario): sem link e sem senha.
//
// F4-11 — o segredo não passa pelo chat. criar_usuario não recebe senha: o
// Executar pede uma aleatória ao servidor (acoes.SenhaAleatoria) e o email de
// boas-vindas, obrigatório, é o que a entrega. A senha nasce no Executar, vive só
// na memória até o CriarUsuario cifrá-la no convite, e nunca entra no pedido, no
// preview, no resultado nem no audit_log.
//
// F4-12 — guardas de si mesmo, só no MCP (a rota não as tem). O gatilho da 0070
// revoga as conexões de IA de quem é rebaixado ou desativado: se fosse o próprio
// dono, a credencial dele cairia no meio da aprovação. Nunca a si mesmo, e nunca o
// último administrador ativo. O Preview não recebe o Principal: ele o lê do
// contexto (PrincipalDe), que o motor preenche na chamada e na aprovação; o
// Executar usa o p que recebe.

func escritasCriticasUsuarios(cfg ConfigServidor) []escritaRegistravel {
	return []escritaRegistravel{
		escritaCriarUsuario(cfg),
		escritaEditarUsuario(cfg),
		escritaDesativarUsuario(cfg),
	}
}

const (
	// Códigos que só o MCP recusa (a rota deixa fazer qualquer um destes).
	codigoBoasVindasIndisponivel = "boas_vindas_indisponivel"
	codigoProprioUsuario         = "proprio_usuario"
	codigoUltimoAdmin            = "ultimo_admin"
	codigoUsuarioApagado         = "usuario_apagado"

	tetoNomeUsuario     = 200
	tetoTelefoneUsuario = 40
	// tetoEmailUsuario: o máximo de um endereço de email (RFC 5321).
	tetoEmailUsuario = 254
	// tetoCarteiraPorChamada: quantos clientes uma lista do pedido carrega (uma
	// agência grande tem dezenas).
	tetoCarteiraPorChamada = 200

	// papelAdminNoBanco e papelClienteNoBanco: o vocabulário do banco (o cliente é viewer).
	papelAdminNoBanco   = "admin"
	papelClienteNoBanco = "viewer"
)

// ── Helpers ─────────────────────────────────────────────────────────────

func linkDosUsuarios(cfg ConfigServidor) *string { return cfg.link("/admin/users") }

// usuarioDoRecurso é o Recurso da auditoria das tools que mexem num usuário existente.
func usuarioDoRecurso(valor string) (string, *uuid.UUID) { return campanhaDoRecurso("user", valor) }

// donoDoPedido é quem pede/aprova: o Principal do contexto (o motor o põe na
// chamada e na aprovação). Sem ele a guarda de si mesmo não tem como valer: falha
// fechada (erro interno, não uma Recusa — na aprovação o pedido volta a pendente).
func donoDoPedido(ctx context.Context) (uuid.UUID, error) {
	p, ok := PrincipalDe(ctx)
	if !ok {
		return uuid.Nil, errors.New("mcp: o Preview de usuário rodou sem Principal no contexto")
	}
	return p.UserID, nil
}

// rotuloDoPapel: o papel como a pessoa lê.
func rotuloDoPapel(papelNoBanco string) string {
	switch papelNoBanco {
	case papelAdminNoBanco:
		return "administrador"
	case papelClienteNoBanco:
		return "cliente"
	}
	return "operador"
}

// papelDaTool traduz o vocabulário da tool (admin | cliente) para o do banco.
func papelDaTool(valor string) (string, error) {
	switch aparar(valor) {
	case "admin":
		return papelAdminNoBanco, nil
	case "cliente":
		return papelClienteNoBanco, nil
	}
	return "", errEntrada(fmt.Sprintf("papel inválido (%q): use admin ou cliente", valor))
}

// papelDaAPI é o papel no vocabulário que o acoes e a rota falam (cliente = "client").
func papelDaAPI(papelNoBanco string) string {
	if papelNoBanco == papelClienteNoBanco {
		return "client"
	}
	return papelNoBanco
}

func nomeDaPessoa(valor string) (string, error) {
	n := aparar(valor)
	if n == "" {
		return "", errEntrada("nome: informe o nome da pessoa")
	}
	if err := limiteDeRunas("nome", n, tetoNomeUsuario); err != nil {
		return "", err
	}
	return n, nil
}

func telefoneDaPessoa(valor string) (string, error) {
	t := aparar(valor)
	if err := limiteDeRunas("telefone", t, tetoTelefoneUsuario); err != nil {
		return "", err
	}
	return t, nil
}

// emailDaPessoa valida o endereço e o devolve como será gravado (minúsculas, sem
// espaço nas pontas). A rota não valida o formato; a tool sim, porque o email é o
// canal por onde a senha chega: um endereço torto cria alguém inalcançável.
func emailDaPessoa(valor string) (string, error) {
	e := aparar(valor)
	invalido := func() error {
		return errEntrada(fmt.Sprintf("email inválido (%q): informe um endereço no formato nome@dominio.com.br, sem nome de exibição", valor))
	}
	if e == "" || len(e) > tetoEmailUsuario || strings.ContainsAny(e, " \t\r\n,;<>()") {
		return "", invalido()
	}
	a, err := mail.ParseAddress(e)
	if err != nil || a.Address != e {
		return "", invalido()
	}
	dominio := e[strings.LastIndex(e, "@")+1:]
	if !strings.Contains(dominio, ".") || strings.HasPrefix(dominio, ".") || strings.HasSuffix(dominio, ".") {
		return "", invalido()
	}
	return strings.ToLower(e), nil
}

// carteiraDaEntrada lê uma lista de ids de cliente, sem repetição, na ordem em que
// vieram.
func carteiraDaEntrada(campo string, valores []string) ([]uuid.UUID, error) {
	if len(valores) > tetoCarteiraPorChamada {
		return nil, errEntrada(fmt.Sprintf("%s: no máximo %d clientes por pedido", campo, tetoCarteiraPorChamada))
	}
	return uuidsDaEntrada(campo, valores)
}

// clientesParaUsuario lê todos os clientes (são dezenas) para nomear a carteira e
// conferir que existem.
func clientesParaUsuario(ctx context.Context, L *Leitura) (map[uuid.UUID]catalog.Client, error) {
	cs, err := L.Clientes.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]catalog.Client, len(cs))
	for _, c := range cs {
		out[c.ID] = c
	}
	return out, nil
}

func nomesDeClientes(cs map[uuid.UUID]catalog.Client) map[uuid.UUID]string {
	out := make(map[uuid.UUID]string, len(cs))
	for id, c := range cs {
		out[id] = c.Name
	}
	return out
}

// exigeClientes recusa (client_not_found) o primeiro id que não é de um cliente.
func exigeClientes(ids []uuid.UUID, cs map[uuid.UUID]catalog.Client) error {
	for _, id := range ids {
		if _, ok := cs[id]; !ok {
			return errRecusa(acoes.CodigoClienteInexistente, fmt.Sprintf(
				"o cliente %s não existe no E-monitor: use um id de listar_clientes (nunca invente; pergunte)", id))
		}
	}
	return nil
}

// carteiraPorExtenso: "A, B, C e mais N" ou "nenhum".
func carteiraPorExtenso(ids []uuid.UUID, nomes map[uuid.UUID]string) string {
	if len(ids) == 0 {
		return "nenhum"
	}
	return listaDeNomes(ids, nomes)
}

// todosDesativados: a carteira tem clientes e nenhum está ativo. O login exige
// pelo menos um cliente ativo (docs/features/multi-client-user.md).
func todosDesativados(ids []uuid.UUID, cs map[uuid.UUID]catalog.Client) bool {
	if len(ids) == 0 {
		return false
	}
	for _, id := range ids {
		if cs[id].IsActive {
			return false
		}
	}
	return true
}

// contarAdminsAtivos: role = admin, ativo e não apagado — a definição de "último
// admin" (o Status "active" do List é is_active e deleted_at IS NULL).
func contarAdminsAtivos(ctx context.Context, L *Leitura) (int, error) {
	_, total, err := L.Usuarios.List(ctx, users.ListInput{Role: papelAdminNoBanco, Status: "active", PageSize: 1})
	return total, err
}

// lerUsuarioParaEscrever acha o usuário que a escrita mexeria. Apagado (soft delete)
// não se edita nem se desativa: a tela o trata como inexistente.
func lerUsuarioParaEscrever(ctx context.Context, L *Leitura, valor string) (*users.User, error) {
	id, err := uuidDaEntrada("usuario_id", valor)
	if err != nil {
		return nil, err
	}
	u, err := L.Usuarios.Get(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNaoEncontrado("usuário")
	}
	if err != nil {
		return nil, err
	}
	if u.DeletedAt != nil {
		return nil, errRecusa(codigoUsuarioApagado, fmt.Sprintf(
			"o usuário %q (%s) foi apagado e não pode mais ser editado nem desativado", u.Name, u.Email))
	}
	return u, nil
}

// recusarUltimoAdmin: o alvo é um administrador ativo e o pedido o tiraria dessa
// condição; se ele é o único, o E-monitor ficaria sem ninguém que administre.
func recusarUltimoAdmin(ctx context.Context, L *Leitura, alvo *users.User, tiraria string) error {
	if alvo.Role != papelAdminNoBanco || !alvo.IsActive {
		return nil
	}
	n, err := contarAdminsAtivos(ctx, L)
	if err != nil {
		return err
	}
	if n <= 1 {
		return errRecusa(codigoUltimoAdmin, fmt.Sprintf(
			"%s (%s) é o único administrador ativo: %s deixaria o E-monitor sem administrador. Crie ou ative outro administrador primeiro",
			alvo.Name, alvo.Email, tiraria))
	}
	return nil
}

// usuarioEmPortugues traduz a Recusa que o acoes devolve com o código no lugar da
// mensagem (a rota devolve o código como corpo). O código fica; o texto é para a
// pessoa. Qualquer outro erro passa intacto.
func usuarioEmPortugues(err error) error {
	var r *acoes.Recusa
	if !errors.As(err, &r) || r.Mensagem != r.Codigo {
		return err
	}
	texto, ok := map[string]string{
		acoes.CodigoEmailEmUso:            "esse e-mail já está em uso por outro usuário",
		acoes.CodigoClienteInexistente:    "algum cliente da carteira não existe mais no E-monitor",
		acoes.CodigoClienteObrigatorio:    "um usuário cliente precisa de ao menos um cliente na carteira",
		acoes.CodigoClienteProibidoAdmin:  "administrador não tem carteira de clientes",
		acoes.CodigoUsuarioNaoEncontrado:  "o usuário não existe mais no E-monitor",
		acoes.CodigoNaoReativarApagado:    "usuário apagado não pode ser reativado",
		acoes.CodigoPapelClienteInconsist: "o papel e a carteira do usuário não combinam (cliente exige um cliente principal)",
		acoes.CodigoPapelInvalido:         "papel inválido: use admin ou cliente",
		acoes.CodigoCamposObrigatorios:    "faltou nome ou e-mail",
	}[r.Codigo]
	if !ok {
		texto = "o E-monitor recusou o pedido (" + r.Codigo + ")"
	}
	return &acoes.Recusa{Codigo: r.Codigo, Mensagem: texto}
}

// usuarioFeito é o feito de uma escrita de usuário JÁ gravada: o resultado é o
// usuário como ficou, no formato de obter_usuario. Se só a releitura falhar, a
// escrita NÃO vira erro (o modelo a repetiria): devolve o id e manda conferir.
func usuarioFeito(ctx context.Context, cfg ConfigServidor, E *Escritas, id uuid.UUID, ressalvas ...string) feito {
	f := feito{Recurso: "user", RecursoID: &id, Ressalvas: ressalvas}
	saida, err := montarUsuario(ctx, E.Leitura, id)
	if err != nil {
		cfg.log().Error("mcp: releitura do usuário após a escrita falhou", zap.Error(err))
		f.Resultado = struct {
			Usuario struct {
				ID string `json:"id"`
			} `json:"usuario"`
		}{Usuario: struct {
			ID string `json:"id"`
		}{ID: id.String()}}
		f.Ressalvas = append(f.Ressalvas,
			"A escrita foi gravada, mas não consegui reler o usuário agora: confira o estado com obter_usuario (id "+id.String()+").")
		return f
	}
	f.Resultado = saida
	return f
}

// ── criar_usuario ───────────────────────────────────────────────────────

type entradaCriarUsuario struct {
	Nome           string   `json:"nome" jsonschema:"Nome completo da pessoa (até 200 caracteres)."`
	Email          string   `json:"email" jsonschema:"E-mail da pessoa, no formato nome@dominio.com.br: é por ele que ela recebe a senha inicial. Vira o login e não pode ser trocado depois."`
	Papel          string   `json:"papel" jsonschema:"admin (vê todos os clientes e as telas de operação) ou cliente (vê só a carteira de cliente_ids)."`
	ClienteIDs     []string `json:"cliente_ids,omitempty" jsonschema:"A carteira de clientes (ids de listar_clientes, ao menos um) quando o papel é cliente; uma agência pode ter vários. Omita para admin. Nunca invente ids: pergunte."`
	Telefone       *string  `json:"telefone,omitempty" jsonschema:"Telefone de contato (até 40 caracteres). Opcional."`
	RecebeAlertas  *bool    `json:"recebe_alertas,omitempty" jsonschema:"Se recebe os emails diários de alerta (só vale para admin; o padrão é sim)."`
	RecebePosVenda *bool    `json:"recebe_pos_venda,omitempty" jsonschema:"Se recebe cópia de todo pós-venda enviado (só vale para admin; o padrão é não)."`
}

// planoDeNovoUsuario é o que o pedido criaria, já validado contra o estado.
type planoDeNovoUsuario struct {
	nome, email string
	telefone    *string
	papel       string // o do banco (admin | viewer)
	carteira    []uuid.UUID
	clientes    map[uuid.UUID]catalog.Client
}

func planejarNovoUsuario(ctx context.Context, E *Escritas, in entradaCriarUsuario) (*planoDeNovoUsuario, error) {
	nome, err := nomeDaPessoa(in.Nome)
	if err != nil {
		return nil, err
	}
	email, err := emailDaPessoa(in.Email)
	if err != nil {
		return nil, err
	}
	papel, err := papelDaTool(in.Papel)
	if err != nil {
		return nil, err
	}
	pl := &planoDeNovoUsuario{nome: nome, email: email, papel: papel}
	if in.Telefone != nil {
		t, err := telefoneDaPessoa(*in.Telefone)
		if err != nil {
			return nil, err
		}
		pl.telefone = ptrTexto(t)
	}
	if pl.carteira, err = carteiraDaEntrada("cliente_ids", in.ClienteIDs); err != nil {
		return nil, err
	}
	switch {
	case papel == papelAdminNoBanco && len(pl.carteira) > 0:
		return nil, errRecusa(acoes.CodigoClienteProibidoAdmin,
			"administrador vê todos os clientes e não tem carteira: omita cliente_ids (para limitar o acesso a clientes, crie um usuário com papel cliente)")
	case papel == papelClienteNoBanco && len(pl.carteira) == 0:
		return nil, errRecusa(acoes.CodigoClienteObrigatorio,
			"usuário cliente precisa de ao menos um cliente em cliente_ids (ids de listar_clientes; nunca invente, pergunte)")
	}
	// A senha chega pelo email de boas-vindas (F4-11): sem ele, ninguém saberia a
	// senha de quem for criado.
	if !E.Acoes.BoasVindasDisponiveis() {
		return nil, errRecusa(codigoBoasVindasIndisponivel,
			"o email de boas-vindas não está disponível neste servidor (falta a chave WELCOME_ENC_KEY): é ele que entrega a senha à pessoa, "+
				"então o usuário não pode ser criado por aqui. Crie pela tela /admin/users")
	}
	if pl.clientes, err = clientesParaUsuario(ctx, E.Leitura); err != nil {
		return nil, err
	}
	if err := exigeClientes(pl.carteira, pl.clientes); err != nil {
		return nil, err
	}
	existente, err := E.Leitura.Usuarios.GetByEmail(ctx, email)
	switch {
	case err == nil:
		return nil, errRecusa(acoes.CodigoEmailEmUso, fmt.Sprintf(
			"já existe um usuário com o e-mail %s (%s): use outro e-mail, ou edite o usuário que já existe", email, existente.Name))
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, err
	}
	return pl, nil
}

func escritaCriarUsuario(cfg ConfigServidor) Escrita[entradaCriarUsuario] {
	return Escrita[entradaCriarUsuario]{
		Nome: "criar_usuario",
		Descricao: "Cria um usuário do E-monitor (a tela /admin/users): administrador (vê todos os clientes e as telas de operação) ou cliente (vê só a carteira " +
			"de cliente_ids; uma agência pode ter vários clientes). A senha NÃO passa por aqui: o sistema gera uma aleatória e a entrega à pessoa pelo email de " +
			"boas-vindas (com o link da página de acesso), que é obrigatório — nunca peça nem informe senha. Sem a chave de boas-vindas no servidor a tool recusa " +
			"(boas_vindas_indisponivel) e a criação é pela tela. O resultado é o usuário no formato de obter_usuario, sem link nem senha; se o email não sair " +
			"(boas_vindas.status_email diferente de sent) a pessoa não tem a senha e o resultado avisa: redefina pela tela. Recusas: e-mail já em uso (email_taken), " +
			"e-mail inválido, cliente sem cliente_ids (client_id_required), admin com cliente_ids (client_id_not_allowed_for_admin), cliente inexistente " +
			"(client_not_found). Um pedido por pessoa. " + fraseCritica,
		Nivel:   Critico,
		Motivo:  "manda email para fora e dá acesso",
		Recurso: func(entradaCriarUsuario) (string, *uuid.UUID) { return "user", nil }, // o usuário ainda não existe
		Tela:    func(*Escritas, entradaCriarUsuario) *string { return linkDosUsuarios(cfg) },
		Preview: func(ctx context.Context, E *Escritas, in entradaCriarUsuario) (previa, error) {
			pl, err := planejarNovoUsuario(ctx, E, in)
			if err != nil {
				return previa{}, err
			}
			nomes := nomesDeClientes(pl.clientes)
			pv := previa{
				Resumo: fmt.Sprintf("Criar o usuário %q (%s) como %s", pl.nome, pl.email, rotuloDoPapel(pl.papel)),
				Mudancas: []mudanca{
					{Campo: "nome", Para: pl.nome},
					{Campo: "email", Para: pl.email},
					{Campo: "papel", Para: rotuloDoPapel(pl.papel)},
				},
			}
			if pl.papel == papelClienteNoBanco {
				pv.Resumo += " da carteira " + carteiraPorExtenso(pl.carteira, nomes)
				pv.Mudancas = append(pv.Mudancas, mudanca{Campo: "clientes", Para: carteiraPorExtenso(pl.carteira, nomes)})
			}
			if pl.telefone != nil {
				pv.Mudancas = append(pv.Mudancas, mudanca{Campo: "telefone", Para: *pl.telefone})
			}
			if in.RecebeAlertas != nil {
				pv.Mudancas = append(pv.Mudancas, mudanca{Campo: "recebe_alertas", Para: *in.RecebeAlertas})
			}
			if in.RecebePosVenda != nil {
				pv.Mudancas = append(pv.Mudancas, mudanca{Campo: "recebe_pos_venda", Para: *in.RecebePosVenda})
			}
			pv.Efeitos = append(pv.Efeitos,
				fmt.Sprintf("Envia email de boas-vindas para %s com o link e a senha inicial. A senha é gerada pelo sistema e só chega à pessoa por esse email; "+
					"ela não passa por esta conversa nem aparece no resultado.", pl.email))
			if pl.papel == papelAdminNoBanco {
				pv.Efeitos = append(pv.Efeitos,
					"Cria uma conta de administrador: a pessoa vê todos os clientes e todas as telas de operação, e pode criar conexões de IA (MCP) com esse mesmo alcance.")
			} else {
				pv.Efeitos = append(pv.Efeitos, fmt.Sprintf(
					"Cria uma conta de cliente: a pessoa vê só os dados de %s (campanhas, veiculações, relatórios).", carteiraPorExtenso(pl.carteira, nomes)))
			}
			pv.Ressalvas = append(pv.Ressalvas,
				"O link de boas-vindas não expira e quem o abre vê a senha inicial: se o email for para o endereço errado, revogue o convite pela tela /admin/users.")
			if pl.papel == papelClienteNoBanco && (in.RecebeAlertas != nil || in.RecebePosVenda != nil) {
				pv.Ressalvas = append(pv.Ressalvas, "recebe_alertas e recebe_pos_venda só têm efeito para administradores: para cliente ficam gravados sem enviar nada.")
			}
			if todosDesativados(pl.carteira, pl.clientes) {
				pv.Ressalvas = append(pv.Ressalvas,
					"Todos os clientes da carteira estão desativados: a pessoa só consegue entrar quando algum deles for reativado.")
			}
			return pv, nil
		},
		Executar: func(ctx context.Context, E *Escritas, p *Principal, in entradaCriarUsuario) (feito, error) {
			pl, err := planejarNovoUsuario(ctx, E, in)
			if err != nil {
				return feito{}, err
			}
			// A senha nasce aqui e só aqui: não está no pedido, no preview nem no
			// resultado. Quem a recebe é o CriarUsuario, que a cifra no convite e a
			// manda por email.
			senha, err := acoes.SenhaAleatoria()
			if err != nil {
				return feito{}, err
			}
			ator := p.UserID
			u, bv, err := E.Acoes.CriarUsuario(ctx, acoes.EntradaUsuario{
				Email: pl.email, Senha: senha, Nome: pl.nome, Telefone: pl.telefone, Papel: papelDaAPI(pl.papel),
				ClienteIDs: pl.carteira, EnviarBoasVindas: true, Ator: &ator,
				RecebeAlertas: in.RecebeAlertas, RecebePosVenda: in.RecebePosVenda,
			})
			if err != nil {
				return feito{}, usuarioEmPortugues(err)
			}
			f := usuarioFeito(ctx, cfg, E, u.ID)
			if bv != nil && bv.EmailStatus == "sent" {
				f.Ressalvas = append(f.Ressalvas, "A senha inicial foi gerada pelo sistema e enviada à pessoa por email; ela não aparece aqui.")
				return f, nil
			}
			status := "unavailable"
			if bv != nil {
				status = bv.EmailStatus
			}
			aviso := fmt.Sprintf("o email não saiu (boas_vindas.status_email: %s): a pessoa não tem a senha — redefina pela tela /admin/users.", status)
			f.Ressalvas = append(f.Ressalvas, aviso)
			if saida, ok := f.Resultado.(saidaObterUsuario); ok {
				f.Resultado = saidaCriarUsuario{saidaObterUsuario: saida, Aviso: aviso}
			}
			return f, nil
		},
	}
}

// saidaCriarUsuario é o obter_usuario mais, só quando o email de boas-vindas não
// saiu, o aviso de que a pessoa ainda não tem a senha. Com o email enviado o JSON é
// exatamente o do obter_usuario (aviso é omitempty).
type saidaCriarUsuario struct {
	saidaObterUsuario
	Aviso string `json:"aviso,omitempty"`
}

// ── editar_usuario ──────────────────────────────────────────────────────

type entradaEditarUsuario struct {
	UsuarioID         string   `json:"usuario_id" jsonschema:"Id (uuid) do usuário, vindo de listar_usuarios. Nunca invente: pergunte."`
	Nome              *string  `json:"nome,omitempty" jsonschema:"Novo nome (até 200 caracteres, não pode ficar vazio). Omita para manter."`
	Telefone          *string  `json:"telefone,omitempty" jsonschema:"Novo telefone (até 40 caracteres); texto vazio apaga. Omita para manter."`
	Papel             *string  `json:"papel,omitempty" jsonschema:"Novo papel: admin ou cliente. Virar cliente exige carteira (adicionar_clientes); virar admin esvazia a carteira. Omita para manter."`
	AdicionarClientes []string `json:"adicionar_clientes,omitempty" jsonschema:"Clientes a ACRESCENTAR à carteira (ids de listar_clientes). Só para quem é (ou está virando) cliente. Nunca invente ids: pergunte."`
	RemoverClientes   []string `json:"remover_clientes,omitempty" jsonschema:"Clientes a TIRAR da carteira (ids de obter_usuario). A carteira não pode ficar vazia: para cortar o acesso inteiro use desativar_usuario."`
	RecebeAlertas     *bool    `json:"recebe_alertas,omitempty" jsonschema:"Se recebe os emails diários de alerta (só vale para admin). Omita para manter."`
	RecebePosVenda    *bool    `json:"recebe_pos_venda,omitempty" jsonschema:"Se recebe cópia de todo pós-venda enviado (só vale para admin). Omita para manter."`
}

func (in entradaEditarUsuario) pediuAlgo() bool {
	return in.Nome != nil || in.Telefone != nil || in.Papel != nil || len(in.AdicionarClientes) > 0 || len(in.RemoverClientes) > 0 ||
		in.RecebeAlertas != nil || in.RecebePosVenda != nil
}

// planoDeEdicaoUsuario é a edição já validada contra o estado de AGORA. Só leva o
// que muda de fato (valor igual ao gravado não é mudança).
type planoDeEdicaoUsuario struct {
	alvo     *users.User
	clientes map[uuid.UUID]catalog.Client

	papelDepois string // o do banco
	papelMuda   bool

	nome, telefone *string

	carteiraAntes, carteiraDepois []uuid.UUID
	carteiraMuda                  bool
	entram, saem                  []uuid.UUID // o que muda de fato (já descontado o que já estava / não estava)

	alertas, posVenda *bool
}

func planejarEdicaoUsuario(ctx context.Context, E *Escritas, eu uuid.UUID, in entradaEditarUsuario) (*planoDeEdicaoUsuario, error) {
	if _, err := uuidDaEntrada("usuario_id", in.UsuarioID); err != nil {
		return nil, err
	}
	if !in.pediuAlgo() {
		return nil, errEntrada("informe ao menos uma mudança: nome, telefone, papel, adicionar_clientes, remover_clientes, recebe_alertas ou recebe_pos_venda")
	}
	var (
		err                    error
		novoNome, novoTelefone string
		papelPedido            string
		adicionar, remover     []uuid.UUID
	)
	if in.Nome != nil {
		if novoNome, err = nomeDaPessoa(*in.Nome); err != nil {
			return nil, err
		}
	}
	if in.Telefone != nil {
		if novoTelefone, err = telefoneDaPessoa(*in.Telefone); err != nil {
			return nil, err
		}
	}
	if in.Papel != nil {
		if papelPedido, err = papelDaTool(*in.Papel); err != nil {
			return nil, err
		}
	}
	if adicionar, err = carteiraDaEntrada("adicionar_clientes", in.AdicionarClientes); err != nil {
		return nil, err
	}
	if remover, err = carteiraDaEntrada("remover_clientes", in.RemoverClientes); err != nil {
		return nil, err
	}
	for _, id := range adicionar {
		if slices.Contains(remover, id) {
			return nil, errEntrada(fmt.Sprintf("o cliente %s está em adicionar_clientes e em remover_clientes: escolha um dos dois", id))
		}
	}

	alvo, err := lerUsuarioParaEscrever(ctx, E.Leitura, in.UsuarioID)
	if err != nil {
		return nil, err
	}
	pl := &planoDeEdicaoUsuario{alvo: alvo, papelDepois: alvo.Role}
	if papelPedido != "" && papelPedido != alvo.Role {
		pl.papelDepois, pl.papelMuda = papelPedido, true
	}

	// Guardas de si mesmo (F4-12): mudar o papel de quem pede, ou rebaixar o último
	// administrador ativo.
	if pl.papelMuda && alvo.ID == eu {
		return nil, errRecusa(codigoProprioUsuario,
			"você não pode mudar o seu próprio papel por aqui: a revogação das suas conexões de IA derrubaria este pedido no meio da aprovação. "+
				"Peça a outro administrador (ou faça pela tela /admin/users)")
	}
	if pl.papelMuda && pl.papelDepois != papelAdminNoBanco {
		if err := recusarUltimoAdmin(ctx, E.Leitura, alvo, "rebaixá-lo"); err != nil {
			return nil, err
		}
	}

	// A carteira: a de agora + adicionar − remover, calculada aqui e de novo na
	// aprovação (alguém pode ter mexido nela no meio).
	if pl.clientes, err = clientesParaUsuario(ctx, E.Leitura); err != nil {
		return nil, err
	}
	mexeNaCarteira := len(adicionar) > 0 || len(remover) > 0
	if pl.papelDepois != papelClienteNoBanco {
		if mexeNaCarteira {
			return nil, errRecusa(acoes.CodigoClienteProibidoAdmin,
				"administrador não tem carteira de clientes (vê todos os clientes): retire adicionar_clientes e remover_clientes deste pedido")
		}
	} else {
		if alvo.Role == papelClienteNoBanco {
			pl.carteiraAntes = slices.Clone(alvo.ClientIDs)
		}
		if err := exigeClientes(adicionar, pl.clientes); err != nil {
			return nil, err
		}
		depois := make([]uuid.UUID, 0, len(pl.carteiraAntes)+len(adicionar))
		for _, id := range pl.carteiraAntes {
			if !slices.Contains(remover, id) {
				depois = append(depois, id)
			}
		}
		for _, id := range adicionar {
			if !slices.Contains(depois, id) {
				depois = append(depois, id)
			}
		}
		if len(depois) == 0 {
			return nil, errRecusa(acoes.CodigoClienteObrigatorio, fmt.Sprintf(
				"a carteira de %s ficaria vazia: usuário cliente precisa de ao menos um cliente. Informe adicionar_clientes (ids de listar_clientes) "+
					"ou, para cortar o acesso inteiro, use desativar_usuario", alvo.Name))
		}
		pl.carteiraDepois = depois
		for _, id := range depois {
			if !slices.Contains(pl.carteiraAntes, id) {
				pl.entram = append(pl.entram, id)
			}
		}
		for _, id := range pl.carteiraAntes {
			if !slices.Contains(depois, id) {
				pl.saem = append(pl.saem, id)
			}
		}
		pl.carteiraMuda = pl.papelMuda || len(pl.entram) > 0 || len(pl.saem) > 0
	}

	if in.Nome != nil && novoNome != alvo.Name {
		pl.nome = &novoNome
	}
	if in.Telefone != nil && !igualTexto(alvo.Phone, novoTelefone) {
		pl.telefone = &novoTelefone
	}
	if in.RecebeAlertas != nil && *in.RecebeAlertas != alvo.ReceiveAlertEmails {
		pl.alertas = in.RecebeAlertas
	}
	if in.RecebePosVenda != nil && *in.RecebePosVenda != alvo.ReceivePostSaleEmails {
		pl.posVenda = in.RecebePosVenda
	}
	if pl.nome == nil && pl.telefone == nil && !pl.papelMuda && !pl.carteiraMuda && pl.alertas == nil && pl.posVenda == nil {
		return nil, errRecusa(codigoSemMudanca, fmt.Sprintf(
			"o usuário %q (%s) já está assim: nada muda com este pedido (confira com obter_usuario)", alvo.Name, alvo.Email))
	}
	return pl, nil
}

func escritaEditarUsuario(cfg ConfigServidor) Escrita[entradaEditarUsuario] {
	return Escrita[entradaEditarUsuario]{
		Nome: "editar_usuario",
		Descricao: "Edita um usuário: nome, telefone, papel (admin ou cliente), carteira de clientes (adicionar_clientes e remover_clientes: a carteira que fica é a " +
			"de AGORA mais os adicionados menos os removidos, calculada na hora da aprovação) e as preferências de email (recebe_alertas, recebe_pos_venda; só valem " +
			"para admin). Informe ao menos uma mudança. O e-mail não muda e a senha não se troca por aqui. Armadilhas: virar cliente exige carteira (adicionar_clientes); " +
			"virar admin esvazia a carteira; rebaixar um admin REVOGA na hora as conexões de IA dele. O portal lê o papel e a carteira do login da pessoa: a sessão " +
			"aberta mantém o acesso antigo até o próximo login (até 8 h) — para cortar já, desativar_usuario. Recusas: mudar o próprio papel (proprio_usuario), " +
			"rebaixar o último admin ativo (ultimo_admin), carteira vazia para cliente (client_id_required), carteira para admin (client_id_not_allowed_for_admin), " +
			"cliente inexistente (client_not_found), usuário apagado (usuario_apagado), pedido que não muda nada (sem_mudanca). O resultado é o usuário no formato " +
			"de obter_usuario. Um pedido por usuário. " + fraseCritica,
		Nivel:   Critico,
		Motivo:  "muda acesso a dados de cliente",
		Recurso: func(in entradaEditarUsuario) (string, *uuid.UUID) { return usuarioDoRecurso(in.UsuarioID) },
		Tela:    func(*Escritas, entradaEditarUsuario) *string { return linkDosUsuarios(cfg) },
		Preview: func(ctx context.Context, E *Escritas, in entradaEditarUsuario) (previa, error) {
			eu, err := donoDoPedido(ctx)
			if err != nil {
				return previa{}, err
			}
			pl, err := planejarEdicaoUsuario(ctx, E, eu, in)
			if err != nil {
				return previa{}, err
			}
			return previaDeEdicaoUsuario(pl), nil
		},
		Executar: func(ctx context.Context, E *Escritas, p *Principal, in entradaEditarUsuario) (feito, error) {
			pl, err := planejarEdicaoUsuario(ctx, E, p.UserID, in)
			if err != nil {
				return feito{}, err
			}
			ed := acoes.EdicaoUsuario{Nome: pl.nome, Telefone: pl.telefone, RecebeAlertas: pl.alertas, RecebePosVenda: pl.posVenda}
			if pl.papelMuda {
				papel := papelDaAPI(pl.papelDepois)
				ed.Papel = &papel
			}
			if pl.carteiraMuda {
				ids := pl.carteiraDepois
				ed.ClienteIDs = &ids
			}
			if _, err := E.Acoes.EditarUsuario(ctx, pl.alvo.ID, ed); err != nil {
				return feito{}, usuarioEmPortugues(err)
			}
			return usuarioFeito(ctx, cfg, E, pl.alvo.ID), nil
		},
	}
}

func previaDeEdicaoUsuario(pl *planoDeEdicaoUsuario) previa {
	alvo := pl.alvo
	nomes := nomesDeClientes(pl.clientes)
	pv := previa{Resumo: fmt.Sprintf("Editar o usuário %q (%s)", alvo.Name, alvo.Email)}
	if pl.nome != nil {
		pv.Mudancas = append(pv.Mudancas, mudanca{Campo: "nome", De: alvo.Name, Para: *pl.nome})
	}
	if pl.telefone != nil {
		pv.Mudancas = append(pv.Mudancas, mudanca{Campo: "telefone", De: textoOuNil(alvo.Phone), Para: textoOuNil(pl.telefone)})
	}
	if pl.papelMuda {
		pv.Mudancas = append(pv.Mudancas, mudanca{Campo: "papel", De: rotuloDoPapel(alvo.Role), Para: rotuloDoPapel(pl.papelDepois)})
	}
	if pl.carteiraMuda {
		pv.Mudancas = append(pv.Mudancas, mudanca{Campo: "clientes", De: carteiraPorExtenso(alvo.ClientIDs, nomes), Para: carteiraPorExtenso(pl.carteiraDepois, nomes)})
	} else if pl.papelMuda && pl.papelDepois != papelClienteNoBanco && len(alvo.ClientIDs) > 0 {
		pv.Mudancas = append(pv.Mudancas, mudanca{Campo: "clientes", De: carteiraPorExtenso(alvo.ClientIDs, nomes), Para: "nenhum (administrador vê todos)"})
	}
	if pl.alertas != nil {
		pv.Mudancas = append(pv.Mudancas, mudanca{Campo: "recebe_alertas", De: alvo.ReceiveAlertEmails, Para: *pl.alertas})
	}
	if pl.posVenda != nil {
		pv.Mudancas = append(pv.Mudancas, mudanca{Campo: "recebe_pos_venda", De: alvo.ReceivePostSaleEmails, Para: *pl.posVenda})
	}

	switch {
	case pl.papelMuda && alvo.Role == papelAdminNoBanco:
		pv.Efeitos = append(pv.Efeitos, fmt.Sprintf(
			"Rebaixa: a pessoa deixa de ser administrador e fica só com a carteira %s; o sistema revoga as conexões de IA dessa pessoa na hora "+
				"(as credenciais do MCP e os códigos de autorização pendentes).", carteiraPorExtenso(pl.carteiraDepois, nomes)))
	case pl.papelMuda && pl.papelDepois == papelAdminNoBanco:
		pv.Efeitos = append(pv.Efeitos, "Promove: a pessoa passa a ser administrador, vê todos os clientes e todas as telas de operação e pode criar conexões de IA; "+
			"a carteira atual é esvaziada (administrador não tem carteira).")
	case pl.papelMuda:
		pv.Efeitos = append(pv.Efeitos, fmt.Sprintf("Passa a ser cliente da carteira %s.", carteiraPorExtenso(pl.carteiraDepois, nomes)))
	}
	if len(pl.saem) > 0 && !(pl.papelMuda && pl.papelDepois != papelClienteNoBanco) {
		pv.Efeitos = append(pv.Efeitos, fmt.Sprintf(
			"Sai da carteira: %s. A pessoa deixa de ver as campanhas, veiculações e relatórios desse(s) cliente(s).", carteiraPorExtenso(pl.saem, nomes)))
	}
	if len(pl.entram) > 0 && !pl.papelMuda {
		pv.Efeitos = append(pv.Efeitos, fmt.Sprintf(
			"Entra na carteira: %s. A pessoa passa a ver as campanhas, veiculações e relatórios desse(s) cliente(s).", carteiraPorExtenso(pl.entram, nomes)))
	}
	if (pl.alertas != nil || pl.posVenda != nil) && alvo.Role == papelClienteNoBanco && !pl.papelMuda {
		pv.Ressalvas = append(pv.Ressalvas, "recebe_alertas e recebe_pos_venda só têm efeito para administradores: para cliente ficam gravados sem enviar nada.")
	}
	if pl.papelMuda || len(pl.saem) > 0 {
		pv.Ressalvas = append(pv.Ressalvas,
			"O portal lê o papel e a carteira do login da pessoa: quem já está logado mantém o acesso antigo até o próximo login (até 8 h). "+
				"Para cortar o acesso agora, desativar_usuario (vale em até 60 s).")
	}
	if !alvo.IsActive {
		pv.Ressalvas = append(pv.Ressalvas, "O usuário está inativo: a edição vale, mas ele continua sem poder entrar.")
	}
	if pl.carteiraMuda && todosDesativados(pl.carteiraDepois, pl.clientes) {
		pv.Ressalvas = append(pv.Ressalvas, "Todos os clientes da carteira estão desativados: a pessoa só consegue entrar quando algum deles for reativado.")
	}
	return pv
}

// ── desativar_usuario ───────────────────────────────────────────────────

func planejarDesativacao(ctx context.Context, E *Escritas, eu uuid.UUID, in entradaUsuario) (*users.User, error) {
	alvo, err := lerUsuarioParaEscrever(ctx, E.Leitura, in.UsuarioID)
	if err != nil {
		return nil, err
	}
	if alvo.ID == eu {
		return nil, errRecusa(codigoProprioUsuario,
			"você não pode desativar a si mesmo por aqui: a revogação das suas conexões de IA derrubaria este pedido no meio da aprovação. "+
				"Peça a outro administrador (ou faça pela tela /admin/users)")
	}
	if !alvo.IsActive {
		return nil, errRecusa(codigoSemMudanca, fmt.Sprintf("o usuário %q (%s) já está inativo: nada a fazer", alvo.Name, alvo.Email))
	}
	if err := recusarUltimoAdmin(ctx, E.Leitura, alvo, "desativá-lo"); err != nil {
		return nil, err
	}
	return alvo, nil
}

func escritaDesativarUsuario(cfg ConfigServidor) Escrita[entradaUsuario] {
	return Escrita[entradaUsuario]{
		Nome: "desativar_usuario",
		Descricao: "Desativa um usuário: ele deixa de entrar no portal (a sessão aberta também cai em até 60 s) e as conexões de IA dele são revogadas na hora. " +
			"Não apaga nada. NÃO há tool para reativar: a reativação é pela tela /admin/users, e as conexões de IA revogadas NÃO voltam (a pessoa cria outras). " +
			"Recusas: você mesmo (proprio_usuario), o último admin ativo (ultimo_admin), usuário já inativo (sem_mudanca), usuário apagado (usuario_apagado). " +
			"O resultado é o usuário no formato de obter_usuario. Um pedido por usuário. " + fraseCritica,
		Nivel:   Critico,
		Motivo:  "corta o acesso",
		Recurso: func(in entradaUsuario) (string, *uuid.UUID) { return usuarioDoRecurso(in.UsuarioID) },
		Tela:    func(*Escritas, entradaUsuario) *string { return linkDosUsuarios(cfg) },
		Preview: func(ctx context.Context, E *Escritas, in entradaUsuario) (previa, error) {
			eu, err := donoDoPedido(ctx)
			if err != nil {
				return previa{}, err
			}
			alvo, err := planejarDesativacao(ctx, E, eu, in)
			if err != nil {
				return previa{}, err
			}
			pv := previa{
				Resumo:   fmt.Sprintf("Desativar o usuário %q (%s, %s)", alvo.Name, alvo.Email, rotuloDoPapel(alvo.Role)),
				Mudancas: []mudanca{{Campo: "situação", De: "ativo", Para: "inativo"}},
				Efeitos: []string{
					"Corta o acesso: login cortado em até 60 s (a sessão aberta da pessoa deixa de valer) e ela não consegue entrar de novo enquanto estiver inativa.",
					"As conexões de IA (MCP) dela são revogadas na hora e NÃO voltam se ela for reativada: a pessoa precisa criar outras.",
					"Nada é apagado: o usuário, a carteira e o histórico ficam, e ele pode ser reativado pela tela /admin/users.",
				},
			}
			if alvo.Role == papelAdminNoBanco {
				pv.Ressalvas = append(pv.Ressalvas,
					"É um administrador: ele deixa de contar entre os administradores ativos (e de receber os emails de alerta e de pós-venda).")
			}
			return pv, nil
		},
		Executar: func(ctx context.Context, E *Escritas, p *Principal, in entradaUsuario) (feito, error) {
			alvo, err := planejarDesativacao(ctx, E, p.UserID, in)
			if err != nil {
				return feito{}, err
			}
			inativo := false
			if _, err := E.Acoes.EditarUsuario(ctx, alvo.ID, acoes.EdicaoUsuario{Ativo: &inativo}); err != nil {
				return feito{}, usuarioEmPortugues(err)
			}
			return usuarioFeito(ctx, cfg, E, alvo.ID,
				"Login cortado em até 60 s e conexões de IA revogadas. Para reativar, a tela /admin/users."), nil
		},
	}
}
