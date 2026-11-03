package mcp

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"radiocheck/internal/acoes"
	"radiocheck/internal/catalog"
)

// Escritas CRÍTICAS de cliente e de material (Fatia 4, Task 18): desativar e
// ativar cliente, apagar cliente e apagar material. Nenhuma executa na chamada: o
// motor grava um pedido pendente e quem executa é a aprovação do dono em
// /pendentes, que reroda o Preview (por isso TODA recusa que depende do estado
// mora nele ou no planejar* que ele chama) e só então o Executar. Nenhuma recebe
// token de confirmação. Desativar e ativar devolvem o cliente no formato de
// obter_cliente; as duas de apagar devolvem {apagado:{id,nome}}.
//
// F4-10 — o MCP é mais estrito que a rota onde a rota destrói em silêncio:
//   - apagar_material só sem vínculo com campanha (a rota também recusa, por FK) e
//     só sem NENHUMA veiculação no histórico (a rota apaga: detections.commercial_id
//     não tem FK e as tocadas ficam apontando para um id que não existe mais);
//   - desativar/ativar_cliente que não muda nada é sem_mudanca, em vez de um pedido
//     de aprovação vazio.

func escritasCriticasClientes(cfg ConfigServidor) []escritaRegistravel {
	return []escritaRegistravel{
		escritaDesativarCliente(cfg),
		escritaAtivarCliente(cfg),
		escritaApagarCliente(cfg),
		escritaApagarMaterial(cfg),
	}
}

// codigoMaterialComHistorico: só o MCP recusa (a rota apagaria o material
// deixando as veiculações órfãs).
const codigoMaterialComHistorico = "material_com_historico"

// ── Helpers ─────────────────────────────────────────────────────────────

func linkDosClientes(cfg ConfigServidor) *string { return cfg.link("/clients") }

// clienteDoRecurso e materialDoRecurso são o Recurso da auditoria dessas tools.
func clienteDoRecurso(valor string) (string, *uuid.UUID) { return campanhaDoRecurso("client", valor) }
func materialDoRecurso(valor string) (string, *uuid.UUID) {
	return campanhaDoRecurso("material", valor)
}

// quantos: "1 campanha" / "2 campanhas".
func quantos(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", singular)
	}
	return fmt.Sprintf("%d %s", n, plural)
}

// juntarComE: "A", "A e B", "A, B e C".
func juntarComE(partes []string) string {
	switch len(partes) {
	case 0:
		return ""
	case 1:
		return partes[0]
	}
	return strings.Join(partes[:len(partes)-1], ", ") + " e " + partes[len(partes)-1]
}

// lerClienteParaEscrever valida o id e carrega o cliente (ativo ou não: é o que as
// quatro tools precisam).
func lerClienteParaEscrever(ctx context.Context, L *Leitura, valor string) (*catalog.Client, error) {
	id, err := uuidDaEntrada("cliente_id", valor)
	if err != nil {
		return nil, err
	}
	c, err := L.Clientes.Get(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNaoEncontrado("cliente")
	}
	if err != nil {
		return nil, err
	}
	return c, nil
}

// hubDoCliente: o hub_id do cliente ("" = sem ponte com o hub).
func hubDoCliente(ctx context.Context, L *Leitura, id uuid.UUID) (string, error) {
	m, err := L.Clientes.HubIDs(ctx, []uuid.UUID{id})
	if err != nil {
		return "", err
	}
	return m[id], nil
}

// nomesDeUsuarios: "A, B, C e mais N" (corta em tetoNomesNoPreview).
func nomesDeUsuarios(us []catalog.ClientUserAccess) string {
	partes := make([]string, 0, min(len(us), tetoNomesNoPreview))
	for _, u := range us[:min(len(us), tetoNomesNoPreview)] {
		partes = append(partes, u.Name)
	}
	s := strings.Join(partes, ", ")
	if len(us) > tetoNomesNoPreview {
		s += fmt.Sprintf(" e mais %d", len(us)-tetoNomesNoPreview)
	}
	return s
}

// separarPorAcesso divide os usuários ativos do cliente entre os que só têm ELE
// (o login depende dele) e os que também têm outro cliente ativo (o login
// independe dele): auth.Login exige pelo menos um cliente ativo na carteira.
func separarPorAcesso(us []catalog.ClientUserAccess) (soEste, comOutro []catalog.ClientUserAccess) {
	for _, u := range us {
		if u.OtherActiveClient {
			comOutro = append(comOutro, u)
		} else {
			soEste = append(soEste, u)
		}
	}
	return soEste, comOutro
}

// ressalvaDoHub: o hub manda no ativo/inativo e na existência do cliente de que
// tem ponte — o evento client.upsert regrava `is_active` (hubsync.go) e recria o
// cliente que não achar. Nenhuma ação do E-monitor avisa o hub.
func ressalvaDoHub(hubID, naoAvisado, consequencia string) string {
	return fmt.Sprintf("O cliente tem ponte com o hub (hub_id %s) e o hub não é avisado %s: %s", hubID, naoAvisado, consequencia)
}

const consequenciaDoAtivoNoHub = "se o cadastro dele for editado lá, o evento client.upsert regrava o ativo/inativo do hub por cima do que for feito aqui, e %s pode ser desfeit%s sozinh%s."

// clienteFeito é o feito de uma escrita de cliente JÁ gravada: o resultado é o
// cliente como ficou, no formato de obter_cliente. Se só a releitura falhar, a
// escrita NÃO vira erro (o modelo a repetiria): devolve o id e manda conferir.
func clienteFeito(ctx context.Context, cfg ConfigServidor, E *Escritas, c *catalog.Client, ressalvas ...string) feito {
	f := feito{Recurso: "client", RecursoID: &c.ID, Ressalvas: ressalvas}
	saida, err := lerClienteCompleto(ctx, E.Leitura, c)
	if err != nil {
		cfg.log().Error("mcp: releitura do cliente após a escrita falhou", zap.Error(err))
		f.Resultado = struct {
			Cliente struct {
				ID string `json:"id"`
			} `json:"cliente"`
		}{Cliente: struct {
			ID string `json:"id"`
		}{ID: c.ID.String()}}
		f.Ressalvas = append(f.Ressalvas,
			"A escrita foi gravada, mas não consegui reler o cliente agora: confira o estado com obter_cliente (id "+c.ID.String()+").")
		return f
	}
	f.Resultado = saida
	return f
}

// saidaApagado é o resultado das duas tools de apagar: quem foi apagado.
type saidaApagado struct {
	Apagado ref `json:"apagado"`
}

// ── desativar_cliente e ativar_cliente ──────────────────────────────────

// planoDeAtivacao é o cliente como está e quem entra ou deixa de entrar.
type planoDeAtivacao struct {
	cliente  *catalog.Client
	usuarios []catalog.ClientUserAccess
	hubID    string
}

func planejarAtivacao(ctx context.Context, E *Escritas, in entradaCliente, ativar bool) (*planoDeAtivacao, error) {
	c, err := lerClienteParaEscrever(ctx, E.Leitura, in.ClienteID)
	if err != nil {
		return nil, err
	}
	if c.IsActive == ativar {
		estado := "inativo"
		if ativar {
			estado = "ativo"
		}
		return nil, errRecusa(codigoSemMudanca, fmt.Sprintf("o cliente %q já está %s: nada a fazer", c.Name, estado))
	}
	us, err := E.Leitura.Clientes.ActiveUsers(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	hub, err := hubDoCliente(ctx, E.Leitura, c.ID)
	if err != nil {
		return nil, err
	}
	return &planoDeAtivacao{cliente: c, usuarios: us, hubID: hub}, nil
}

func escritaDesativarCliente(cfg ConfigServidor) Escrita[entradaCliente] {
	return Escrita[entradaCliente]{
		Nome: "desativar_cliente",
		Descricao: "Desativa um cliente (reversível): os usuários dele deixam de conseguir entrar no portal e o cliente sai da lista de gestão de /clients. " +
			"Campanhas, materiais e usuários ficam intactos e ativar_cliente desfaz. Use quando a empresa foi suspensa; é a alternativa a apagar_cliente. " +
			"As armadilhas: (1) vale a partir do PRÓXIMO login — a sessão que a pessoa já tem aberta dura até 8 h; (2) só bloqueia o login: chaves de API, " +
			"webhooks e o monitoramento das campanhas do cliente continuam funcionando; (3) quem tem outro cliente ativo na carteira segue entrando por ele; " +
			"(4) cliente com ponte no hub pode ser reativado pelo hub. Cliente já inativo é recusado (sem_mudanca). O resultado é o cliente no formato de " +
			"obter_cliente. " + fraseCritica,
		Nivel:   Critico,
		Motivo:  "bloqueia o login dos usuários do cliente",
		Recurso: func(in entradaCliente) (string, *uuid.UUID) { return clienteDoRecurso(in.ClienteID) },
		Tela:    func(*Escritas, entradaCliente) *string { return linkDosClientes(cfg) },
		Preview: func(ctx context.Context, E *Escritas, in entradaCliente) (previa, error) {
			pl, err := planejarAtivacao(ctx, E, in, false)
			if err != nil {
				return previa{}, err
			}
			perdem, seguem := separarPorAcesso(pl.usuarios)
			pv := previa{
				Resumo:   fmt.Sprintf("Desativar o cliente %q", pl.cliente.Name),
				Mudancas: []mudanca{{Campo: "situação", De: "ativo", Para: "inativo"}},
			}
			if len(perdem) > 0 {
				pv.Efeitos = append(pv.Efeitos, fmt.Sprintf(
					"Bloqueia o login dos usuários do cliente: %d usuário(s) ativo(s) deixam de entrar a partir do próximo login (%s).", len(perdem), nomesDeUsuarios(perdem)))
			}
			if len(seguem) > 0 {
				pv.Efeitos = append(pv.Efeitos, fmt.Sprintf(
					"%d usuário(s) ativo(s) segue(m) entrando por outro cliente ativo da carteira (%s); o cliente desativado deixa de aparecer para eles.", len(seguem), nomesDeUsuarios(seguem)))
			}
			if len(pl.usuarios) == 0 {
				pv.Efeitos = append(pv.Efeitos, "O cliente não tem usuário ativo: ninguém perde o acesso agora (o bloqueio vale para quem for criado ou reativado depois).")
			}
			pv.Efeitos = append(pv.Efeitos,
				"O cliente sai da lista de gestão de /clients (aparece em \"Mostrar inativos\"). É reversível: campanhas, materiais e usuários ficam intactos e ativar_cliente desfaz.")
			pv.Ressalvas = append(pv.Ressalvas,
				"Quem já está logado continua: a sessão aberta dura até 8 h, porque a desativação vale a partir do próximo login.",
				"Desativar só bloqueia o login: as chaves de API, os webhooks e o monitoramento das campanhas do cliente continuam funcionando.")
			if pl.hubID != "" {
				pv.Ressalvas = append(pv.Ressalvas, ressalvaDoHub(pl.hubID, "da desativação", fmt.Sprintf(consequenciaDoAtivoNoHub, "a desativação", "a", "a")))
			}
			return pv, nil
		},
		Executar: func(ctx context.Context, E *Escritas, _ *Principal, in entradaCliente) (feito, error) {
			pl, err := planejarAtivacao(ctx, E, in, false)
			if err != nil {
				return feito{}, err
			}
			c, err := E.Acoes.DefinirAtivoCliente(ctx, pl.cliente.ID, false)
			if err != nil {
				return feito{}, clienteEmPortugues(err)
			}
			return clienteFeito(ctx, cfg, E, c,
				"Login dos usuários do cliente bloqueado a partir do próximo login (as sessões abertas duram até 8 h). Chaves de API, webhooks e monitoramento seguem ativos. Para reativar, ativar_cliente."), nil
		},
	}
}

func escritaAtivarCliente(cfg ConfigServidor) Escrita[entradaCliente] {
	return Escrita[entradaCliente]{
		Nome: "ativar_cliente",
		Descricao: "Reativa um cliente desativado: o cliente volta a entrar — os usuários dele voltam a poder fazer login e ele volta à lista de gestão de /clients. " +
			"Use depois de desativar_cliente (ou quando a tela mostra o cliente como inativo). Não recria nem muda nada além de ativo/inativo. A armadilha: " +
			"cliente com ponte no hub pode ter o ativo regravado pelo hub na próxima edição dele lá. Cliente já ativo é recusado (sem_mudanca). O resultado é " +
			"o cliente no formato de obter_cliente. " + fraseCritica,
		Nivel:   Critico,
		Motivo:  "o cliente volta a entrar",
		Recurso: func(in entradaCliente) (string, *uuid.UUID) { return clienteDoRecurso(in.ClienteID) },
		Tela:    func(*Escritas, entradaCliente) *string { return linkDosClientes(cfg) },
		Preview: func(ctx context.Context, E *Escritas, in entradaCliente) (previa, error) {
			pl, err := planejarAtivacao(ctx, E, in, true)
			if err != nil {
				return previa{}, err
			}
			voltam, jaEntravam := separarPorAcesso(pl.usuarios)
			pv := previa{
				Resumo:   fmt.Sprintf("Reativar o cliente %q", pl.cliente.Name),
				Mudancas: []mudanca{{Campo: "situação", De: "inativo", Para: "ativo"}},
			}
			if len(voltam) > 0 {
				pv.Efeitos = append(pv.Efeitos, fmt.Sprintf(
					"O cliente volta a entrar: %d usuário(s) ativo(s) voltam a poder entrar (%s).", len(voltam), nomesDeUsuarios(voltam)))
			}
			if len(jaEntravam) > 0 {
				pv.Efeitos = append(pv.Efeitos, fmt.Sprintf(
					"%d usuário(s) ativo(s) já entrava(m) por outro cliente ativo da carteira (%s); o cliente reativado volta a aparecer para eles.", len(jaEntravam), nomesDeUsuarios(jaEntravam)))
			}
			if len(pl.usuarios) == 0 {
				pv.Efeitos = append(pv.Efeitos, "O cliente não tem usuário ativo: ninguém ganha acesso agora.")
			}
			pv.Efeitos = append(pv.Efeitos, "O cliente volta à lista de gestão de /clients. Campanhas, materiais e usuários não mudam.")
			if pl.hubID != "" {
				pv.Ressalvas = append(pv.Ressalvas, ressalvaDoHub(pl.hubID, "da reativação", fmt.Sprintf(consequenciaDoAtivoNoHub, "a reativação", "a", "a")))
			}
			return pv, nil
		},
		Executar: func(ctx context.Context, E *Escritas, _ *Principal, in entradaCliente) (feito, error) {
			pl, err := planejarAtivacao(ctx, E, in, true)
			if err != nil {
				return feito{}, err
			}
			c, err := E.Acoes.DefinirAtivoCliente(ctx, pl.cliente.ID, true)
			if err != nil {
				return feito{}, clienteEmPortugues(err)
			}
			return clienteFeito(ctx, cfg, E, c, "Os usuários ativos do cliente voltam a poder entrar no próximo login."), nil
		},
	}
}

// clienteEmPortugues troca a mensagem inglesa do nao_encontrado do acoes ("not
// found") pela do MCP; qualquer outro erro passa intacto.
func clienteEmPortugues(err error) error {
	var r *acoes.Recusa
	if errors.As(err, &r) && r.Codigo == acoes.CodigoNaoEncontrado {
		return errNaoEncontrado("cliente")
	}
	return err
}

// ── apagar_cliente ──────────────────────────────────────────────────────

// planoDeExclusaoCliente é o cliente como está e o que o DELETE levaria junto.
type planoDeExclusaoCliente struct {
	cliente *catalog.Client
	pegada  catalog.ClientFootprint
	hubID   string
}

func planejarExclusaoCliente(ctx context.Context, E *Escritas, in entradaCliente) (*planoDeExclusaoCliente, error) {
	c, err := lerClienteParaEscrever(ctx, E.Leitura, in.ClienteID)
	if err != nil {
		return nil, err
	}
	pegada, err := E.Leitura.Clientes.Footprint(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	// Mesmas FKs que a rota (campanhas, materiais e usuários seguram o cliente), mas
	// a recusa chega antes do DELETE e com as contagens na mensagem.
	if d := pegada.Dependents; d.Total() > 0 {
		var partes []string
		if d.Campaigns > 0 {
			partes = append(partes, quantos(d.Campaigns, "campanha", "campanhas"))
		}
		if d.Materials > 0 {
			partes = append(partes, quantos(d.Materials, "material", "materiais"))
		}
		if d.Users > 0 {
			partes = append(partes, quantos(d.Users, "usuário", "usuários"))
		}
		msg := fmt.Sprintf("o cliente %q não pode ser apagado: ainda tem vínculos (%s). Só se apaga cliente sem nenhum vínculo; "+
			"para tirá-lo de circulação sem perder nada, use desativar_cliente", c.Name, juntarComE(partes))
		if d.Users > 0 {
			msg += ". A conta de usuários inclui inativos e apagados, que ainda seguram o vínculo"
		}
		return nil, errRecusa(acoes.CodigoClienteComVinculos, msg)
	}
	hub, err := hubDoCliente(ctx, E.Leitura, c.ID)
	if err != nil {
		return nil, err
	}
	return &planoDeExclusaoCliente{cliente: c, pegada: pegada, hubID: hub}, nil
}

func escritaApagarCliente(cfg ConfigServidor) Escrita[entradaCliente] {
	return Escrita[entradaCliente]{
		Nome: "apagar_cliente",
		Descricao: "Apaga um cliente de vez, sem volta (irreversível) — SÓ se ele não tem campanha, material nem usuário (usuário desativado ou apagado ainda conta). " +
			"Com qualquer vínculo o pedido é recusado (cliente_com_vinculos, com as contagens) e o caminho é desativar_cliente. Serve para cliente criado por engano. " +
			"Vai junto o que depende só do cliente: as chaves de API (as ativas deixam de autenticar), as entregas e falhas de webhook e a configuração do webhook, " +
			"o PMM no target e os pós-vendas (o que já foi enviado deixa de abrir pelo link). A armadilha é a irreversibilidade: não há tela nem tool que recupere, " +
			"e o hub não é avisado — se o cliente tem ponte com o hub (hub_id), o hub pode recriá-lo na próxima edição dele lá. O resultado é {apagado:{id,nome}}. " + fraseCritica,
		Nivel:   Critico,
		Motivo:  "irreversível: apaga o cliente e o que depende só dele (chaves de API, webhooks, PMM no target e pós-vendas)",
		Recurso: func(in entradaCliente) (string, *uuid.UUID) { return clienteDoRecurso(in.ClienteID) },
		Tela:    func(*Escritas, entradaCliente) *string { return linkDosClientes(cfg) },
		Preview: func(ctx context.Context, E *Escritas, in entradaCliente) (previa, error) {
			pl, err := planejarExclusaoCliente(ctx, E, in)
			if err != nil {
				return previa{}, err
			}
			p := pl.pegada
			chaves := fmt.Sprintf("chaves de API: %d", p.APIKeys)
			if p.APIKeys > 0 {
				chaves += fmt.Sprintf(" (%s)", quantos(p.APIKeysActive, "ativa", "ativas"))
			}
			posVenda := fmt.Sprintf("pós-vendas: %d", p.PostSaleReports)
			if p.PostSaleReports > 0 {
				posVenda += fmt.Sprintf(" (%s)", quantos(p.PostSaleSent, "enviado", "enviados"))
			}
			pv := previa{
				Resumo:   fmt.Sprintf("Apagar o cliente %q", pl.cliente.Name),
				Mudancas: []mudanca{{Campo: "cliente", De: pl.cliente.Name, Para: "apagado"}},
				Efeitos: []string{
					"O cliente é apagado de vez, sem volta: nem a tela nem o MCP o recuperam.",
					fmt.Sprintf("Vão junto: %s; entregas de webhook: %d; falhas de webhook: %d; PMM no target: %s; %s.",
						chaves, p.WebhookDeliveries, p.WebhookFailures, quantos(p.PMMRows, "emissora", "emissoras"), posVenda),
				},
			}
			if p.WebhookConfigured {
				pv.Efeitos = append(pv.Efeitos, "A configuração de webhook do cliente (URL e segredo) mora na linha dele e some junto.")
			}
			if p.APIKeysActive > 0 {
				pv.Efeitos = append(pv.Efeitos, fmt.Sprintf(
					"%s deixam de funcionar na hora: quem integra com elas passa a receber erro de autenticação.", quantos(p.APIKeysActive, "chave de API ativa", "chaves de API ativas")))
			}
			if p.PostSaleSent > 0 {
				pv.Efeitos = append(pv.Efeitos, fmt.Sprintf(
					"%s já enviado(s) some(m): o documento congelado que o cliente recebeu deixa de existir e o link dele (/pos-venda/<token>) passa a dar 404.", quantos(p.PostSaleSent, "pós-venda", "pós-vendas")))
			}
			pv.Ressalvas = append(pv.Ressalvas,
				"O cliente não tem campanha, material nem usuário; é só por isso que pode ser apagado (com qualquer vínculo o MCP recusa e manda desativar).")
			if pl.hubID != "" {
				pv.Ressalvas = append(pv.Ressalvas, ressalvaDoHub(pl.hubID, "de que ele foi apagado", "o evento client.upsert dele pode recriá-lo (vazio, só com o cadastro mínimo) na próxima edição lá."))
			}
			return pv, nil
		},
		Executar: func(ctx context.Context, E *Escritas, _ *Principal, in entradaCliente) (feito, error) {
			pl, err := planejarExclusaoCliente(ctx, E, in)
			if err != nil {
				return feito{}, err
			}
			if err := E.Acoes.ApagarCliente(ctx, pl.cliente.ID); err != nil {
				return feito{}, clienteEmPortugues(err)
			}
			return feito{
				Resultado: saidaApagado{Apagado: refDe(pl.cliente.ID, pl.cliente.Name)},
				Recurso:   "client", RecursoID: &pl.cliente.ID,
				Ressalvas: []string{"Não há como desfazer: o cliente e o que dependia só dele não existem mais."},
			}, nil
		},
	}
}

// ── apagar_material ─────────────────────────────────────────────────────

type planoDeExclusaoMaterial struct {
	mat     *catalog.Material
	cliente string
	tipo    string
	pegada  catalog.MaterialFootprint
}

func planejarExclusaoMaterial(ctx context.Context, E *Escritas, in entradaMaterial) (*planoDeExclusaoMaterial, error) {
	id, err := uuidDaEntrada("material_id", in.MaterialID)
	if err != nil {
		return nil, err
	}
	m, err := E.Leitura.Materiais.Get(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNaoEncontrado("material")
	}
	if err != nil {
		return nil, err
	}
	pegada, err := E.Leitura.Materiais.Footprint(ctx, m.ID)
	if err != nil {
		return nil, err
	}
	vinculos, err := E.Leitura.MateriaisCampanha.ListByMaterial(ctx, m.ID)
	if err != nil {
		return nil, err
	}
	// O histórico vem primeiro: é o impedimento que desvincular não resolve. Sem
	// filtro de aprovação — a tocada retratada ou ignorada também é histórico.
	if pegada.HasDetections || pegada.HasProjections {
		msg := fmt.Sprintf("o material %q tem veiculações no histórico (inclusive retratadas ou ignoradas) e apagá-lo as deixaria apontando para um material "+
			"que não existe mais: o MCP não faz isso. Se ele não deve mais veicular, desvincule-o das campanhas (desvincular_material): ele continua na biblioteca", m.Title)
		if len(vinculos) > 0 {
			msg += fmt.Sprintf(". Ele também está vinculado a %s, o que já impediria apagar", quantos(len(vinculos), "campanha", "campanhas"))
		}
		return nil, errRecusa(codigoMaterialComHistorico, msg)
	}
	if len(vinculos) > 0 {
		camps, err := E.Leitura.Campanhas.ListFiltered(ctx, nil, []uuid.UUID{m.ClientID})
		if err != nil {
			return nil, err
		}
		porID := make(map[uuid.UUID]catalog.Campaign, len(camps))
		for _, c := range camps {
			porID[c.ID] = c
		}
		partes := make([]string, 0, min(len(vinculos), tetoNomesNoPreview))
		for _, v := range vinculos[:min(len(vinculos), tetoNomesNoPreview)] {
			if c, ok := porID[v.CampaignID]; ok {
				partes = append(partes, fmt.Sprintf("%s (%s)", c.Name, c.Status))
			} else {
				partes = append(partes, v.CampaignID.String())
			}
		}
		lista := strings.Join(partes, ", ")
		if len(vinculos) > tetoNomesNoPreview {
			lista += fmt.Sprintf(" e mais %d", len(vinculos)-tetoNomesNoPreview)
		}
		return nil, errRecusa(acoes.CodigoMaterialVinculado, fmt.Sprintf(
			"o material %q está vinculado a %s: %s. Desvincule-o de cada uma (desvincular_material) antes de pedir para apagar",
			m.Title, quantos(len(vinculos), "campanha", "campanhas"), lista))
	}
	cliente, err := nomeDoCliente(ctx, E, m.ClientID)
	if err != nil {
		return nil, err
	}
	pl := &planoDeExclusaoMaterial{mat: m, cliente: cliente, pegada: pegada}
	if m.TypeID != nil {
		tipos, err := nomesTipos(ctx, E.Leitura)
		if err != nil {
			return nil, err
		}
		pl.tipo = tipos[*m.TypeID]
	}
	return pl, nil
}

func escritaApagarMaterial(cfg ConfigServidor) Escrita[entradaMaterial] {
	return Escrita[entradaMaterial]{
		Nome: "apagar_material",
		Descricao: "Apaga um material (áudio) da biblioteca do cliente, sem volta (irreversível). Serve para material subido por engano: ele precisa estar SEM vínculo " +
			"com campanha (material_vinculado, que lista as campanhas — desvincular_material primeiro) e SEM nenhuma veiculação no histórico, nem retratada nem " +
			"ignorada (material_com_historico: a rota apagaria e deixaria as veiculações apontando para um material que não existe mais; o MCP não). A armadilha: " +
			"o arquivo master (o áudio) e os hashes do fingerprint NÃO são removidos — ficam no servidor, sem dono. Os outros materiais do cliente que apontavam este como " +
			"\"parecido\" perdem o aviso de similaridade. O resultado é {apagado:{id,nome}}. " + fraseCritica,
		Nivel:   Critico,
		Motivo:  "irreversível: apaga o material da biblioteca (o arquivo master e os hashes ficam no servidor)",
		Recurso: func(in entradaMaterial) (string, *uuid.UUID) { return materialDoRecurso(in.MaterialID) },
		Tela:    func(*Escritas, entradaMaterial) *string { return cfg.link("/materials") },
		Preview: func(ctx context.Context, E *Escritas, in entradaMaterial) (previa, error) {
			pl, err := planejarExclusaoMaterial(ctx, E, in)
			if err != nil {
				return previa{}, err
			}
			duracao := strconv.FormatFloat(pl.mat.DurationSeconds, 'f', -1, 64) + "s"
			quem := duracao
			if pl.tipo != "" {
				quem = pl.tipo + ", " + duracao
			}
			pv := previa{
				Resumo:   fmt.Sprintf("Apagar o material %q (%s) do cliente %q", pl.mat.Title, quem, pl.cliente),
				Mudancas: []mudanca{{Campo: "material", De: pl.mat.Title, Para: "apagado"}},
				Efeitos:  []string{"O material é apagado da biblioteca do cliente, sem volta: nem a tela nem o MCP o recuperam."},
			}
			if pl.pegada.SimilarRefs > 0 {
				pv.Efeitos = append(pv.Efeitos, fmt.Sprintf(
					"%d outro(s) material(is) do cliente apontava(m) para este como o mais parecido: o aviso de similaridade deles some.", pl.pegada.SimilarRefs))
			}
			if pl.pegada.TwinRows > 0 {
				pv.Efeitos = append(pv.Efeitos, fmt.Sprintf(
					"Os %d registro(s) de desambiguação de gêmeos acústicos deste material são apagados junto.", pl.pegada.TwinRows))
			}
			pv.Ressalvas = append(pv.Ressalvas,
				"O arquivo master (o áudio) e os hashes do fingerprint NÃO são apagados: ficam no servidor, sem dono.",
				"Nenhuma campanha usa o material e ele não tem veiculação no histórico; é só por isso que pode ser apagado (com qualquer um dos dois o MCP recusa).")
			if pl.mat.FingerprintStatus == "pending" || pl.mat.FingerprintStatus == "generating" {
				pv.Ressalvas = append(pv.Ressalvas, fmt.Sprintf(
					"O fingerprint do material ainda não terminou (%s): se o worker o estiver gerando agora, os hashes que ele gravar também ficam no servidor, sem dono.", pl.mat.FingerprintStatus))
			}
			return pv, nil
		},
		Executar: func(ctx context.Context, E *Escritas, _ *Principal, in entradaMaterial) (feito, error) {
			pl, err := planejarExclusaoMaterial(ctx, E, in)
			if err != nil {
				return feito{}, err
			}
			if err := E.Acoes.ApagarMaterial(ctx, pl.mat.ID); err != nil {
				if errors.Is(err, acoes.ErrAlvoInexistente) {
					return feito{}, errNaoEncontrado("material")
				}
				return feito{}, err
			}
			return feito{
				Resultado: saidaApagado{Apagado: refDe(pl.mat.ID, pl.mat.Title)},
				Recurso:   "material", RecursoID: &pl.mat.ID,
				Ressalvas: []string{"Não há como desfazer: o material saiu da biblioteca. O arquivo master e os hashes do fingerprint continuam no servidor."},
			}, nil
		},
	}
}
