package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/google/uuid"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"radiocheck/internal/postsale"
)

var estadosPosVenda = map[string]string{"enviado": "sent", "rascunho": "draft"}
var nomeEstadoPosVenda = map[string]string{"sent": "enviado", "draft": "rascunho"}

// competenciaRe: a mesma validação do handler (monthRe), que ignora a
// competência malformada em silêncio; aqui ela é recusada.
var competenciaRe = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)

const (
	// tetoBlocosPosVenda: as campanhas de um pós-venda saem paginadas, e cada
	// uma leva até tetoPorItem linhas de checking com a nota livre do admin.
	// Com 10 por página o pior caso passava de 95 KB; medido em
	// TestObterPosVenda_PiorCasoCabe.
	tetoBlocosPosVenda = 5
	// tetoPrevia: cada grupo da prévia de destinatários (são dois).
	tetoPrevia = 100
)

const ressalvaPosVenda = "O pós-venda enviado é um documento CONGELADO: os números são os do dia do envio e não mudam se a campanha for recategorizada depois — divergir de insights_campanha é esperado. O link que o cliente abre e o token dele nunca saem daqui; o anexo sai só como domínio."

const ressalvaRascunho = "Rascunho: nada está congelado ainda, então numeros, checking e emissoras_conformes vêm null (a prévia da tela recalcula tudo ao vivo; os números só existem a partir do envio)."

type entradaListarPosVendas struct {
	Busca       string `json:"busca,omitempty" jsonschema:"Casa no título ou no nome do cliente."`
	ClienteID   string `json:"cliente_id,omitempty" jsonschema:"Só deste cliente (de listar_clientes)."`
	Competencia string `json:"competencia,omitempty" jsonschema:"AAAA-MM: pós-vendas cujo período cruza o mês."`
	Estado      string `json:"estado,omitempty" jsonschema:"enviado ou rascunho. Vazio = todos."`
	Paginacao
}

type posVendaItem struct {
	ID            string  `json:"id"`
	Titulo        string  `json:"titulo"`
	Cliente       ref     `json:"cliente"`
	Estado        string  `json:"estado"`
	EnviadoEm     *string `json:"enviado_em"`
	CriadoEm      string  `json:"criado_em"`
	Campanhas     int     `json:"campanhas"`
	Destinatarios int     `json:"destinatarios"`
	Abriram       int     `json:"abriram"`
	PeriodoDe     *string `json:"periodo_de"`
	PeriodoAte    *string `json:"periodo_ate"`
	Tela          *string `json:"tela"`
}

type contagensPosVenda struct {
	Todos     int `json:"todos"`
	Enviados  int `json:"enviados"`
	Rascunhos int `json:"rascunhos"`
}

type listaPosVendasDTO struct {
	lista[posVendaItem]
	// Contagens ignoram o filtro de estado (como os chips da tela).
	Contagens contagensPosVenda `json:"contagens"`
	Links     links             `json:"links"`
}

type entradaPosVenda struct {
	PosVendaID string `json:"pos_venda_id" jsonschema:"Id do pós-venda (de listar_pos_vendas). Nunca invente: pergunte."`
	Paginacao
}

type numerosPosVenda struct {
	ValorEntregue        float64 `json:"valor_entregue"`
	Impactos             int64   `json:"impactos"`
	CPM                  float64 `json:"cpm"`
	Bonificacao          float64 `json:"bonificacao"`
	BonificacaoInsercoes int64   `json:"bonificacao_insercoes"`
	Emissoras            int     `json:"emissoras"`
	// Target: null quando nenhuma emissora tem PMM no target (ausência ≠ zero,
	// docs/features/client-target-pmm.md).
	ImpactosTarget     *int64   `json:"impactos_target"`
	CPMTarget          *float64 `json:"cpm_target"`
	EmissorasComTarget int      `json:"emissoras_com_target"`
	RotuloTarget       *string  `json:"rotulo_target"`
	Consolidado        bool     `json:"consolidado"`
	AjustadoAMao       bool     `json:"ajustado_a_mao"`
}

type checkingDTO struct {
	Emissora     ref    `json:"emissora"`
	Programado   int    `json:"programado"`
	Identificado int    `json:"identificado"`
	Deficit      int    `json:"deficit"`
	Extras       int    `json:"extras"`
	Situacao     string `json:"situacao"`
	Compensada   bool   `json:"compensada"`
	Nota         string `json:"nota,omitempty"`
}

type blocoPosVenda struct {
	Campanha           ref              `json:"campanha"`
	Estado             string           `json:"estado"`
	PeriodoDe          string           `json:"periodo_de"`
	PeriodoAte         string           `json:"periodo_ate"`
	Numeros            *numerosPosVenda `json:"numeros"`
	Checking           []checkingDTO    `json:"checking"`
	CheckingTotal      *int             `json:"checking_total"`
	EmissorasConformes *int             `json:"emissoras_conformes"`
}

type resumoDestinatarios struct {
	Total     int `json:"total"`
	Abriram   int `json:"abriram"`
	Falharam  int `json:"falharam"`
	Revogados int `json:"revogados"`
}

type posVendaDTO struct {
	ID            string              `json:"id"`
	Titulo        string              `json:"titulo"`
	Cliente       ref                 `json:"cliente"`
	Estado        string              `json:"estado"`
	EnviadoEm     *string             `json:"enviado_em"`
	CriadoEm      string              `json:"criado_em"`
	Mensagem      string              `json:"mensagem"`
	Anexo         *string             `json:"anexo"`
	Destinatarios resumoDestinatarios `json:"destinatarios"`
	// items: as campanhas do pós-venda (blocos), paginadas.
	lista[blocoPosVenda]
	Links links `json:"links"`
}

type destinatarioDTO struct {
	Nome        string  `json:"nome"`
	Email       string  `json:"email"`
	StatusEmail string  `json:"status_email"`
	ErroEmail   *string `json:"erro_email"`
	AbertoEm    *string `json:"aberto_em"`
	Aberturas   int     `json:"aberturas"`
	RevogadoEm  *string `json:"revogado_em"`
}

// destinatarioDe é whitelist: o Token (a credencial do link público
// /pos-venda/:token, sem expiração) NUNCA entra.
func destinatarioDe(r postsale.Recipient) destinatarioDTO {
	return destinatarioDTO{Nome: r.Name, Email: r.Email, StatusEmail: r.EmailStatus, ErroEmail: semCredencialDeURL(r.EmailError),
		AbertoEm: instanteOpc(r.OpenedAt), Aberturas: r.OpenCount, RevogadoEm: instanteOpc(r.RevokedAt)}
}

func checkingDe(rows []postsale.StationRow) ([]checkingDTO, int) {
	rs, n := cortar(rows, tetoPorItem)
	out := make([]checkingDTO, len(rs))
	for i, r := range rs {
		out[i] = checkingDTO{Emissora: refDe(r.StationID, r.Name), Programado: r.Programmed, Identificado: r.Identified,
			Deficit: r.Deficit, Extras: r.Extras, Situacao: string(r.Kind), Compensada: r.Compensated, Nota: r.Note}
	}
	return out, n
}

func numerosDe(k postsale.BlockKPIs) *numerosPosVenda {
	n := &numerosPosVenda{ValorEntregue: k.ValorEntregue, Impactos: k.Impactos, CPM: k.CPM, Bonificacao: k.Bonificacao,
		BonificacaoInsercoes: k.BonificacaoCount, Emissoras: k.StationsCount, EmissorasComTarget: k.StationsWithTarget,
		RotuloTarget: k.TargetLabel, Consolidado: k.Consolidated, AjustadoAMao: k.Overridden}
	if k.StationsWithTarget > 0 {
		it, ct := k.ImpactosTarget, k.CPMTarget
		n.ImpactosTarget, n.CPMTarget = &it, &ct
	}
	return n
}

// congeladosDe lê o payload de um pós-venda enviado: campanha → bloco congelado.
func congeladosDe(ctx context.Context, L *Leitura, id uuid.UUID) (map[uuid.UUID]postsale.CampaignBlock, error) {
	raw, err := L.PosVenda.Payload(ctx, id)
	if err != nil {
		return nil, err
	}
	var p postsale.Payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("mcp: payload do pós-venda %s: %w", id, err)
	}
	out := make(map[uuid.UUID]postsale.CampaignBlock, len(p.Campaigns))
	for _, b := range p.Campaigns {
		out[b.CampaignID] = b
	}
	return out, nil
}

func pessoasDe(xs []postsale.RecipientInput) ([]map[string]string, int) {
	cs, n := cortar(xs, tetoPrevia)
	out := make([]map[string]string, len(cs))
	for i, x := range cs {
		out[i] = map[string]string{"nome": x.Name, "email": x.Email}
	}
	return out, n
}

func registrarPosVenda(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "listar_pos_vendas",
		"Lista os pós-vendas (/admin/pos-venda): título, cliente, estado (enviado/rascunho), datas, nº de campanhas, de "+
			"destinatários e de quem abriu, período e o link da tela de cada um; mais as contagens por estado. Filtros: busca, "+
			"cliente_id, competencia (AAAA-MM), estado. Lista paginada.",
		func(ctx context.Context, L *Leitura, in entradaListarPosVendas) (*sdk.CallToolResult, error) {
			f := postsale.ListFilter{Q: in.Busca}
			if in.Competencia != "" {
				if !competenciaRe.MatchString(in.Competencia) {
					return recusa("competencia tem de ser AAAA-MM", "entrada_invalida")
				}
				f.Month = in.Competencia
			}
			if in.ClienteID != "" {
				id, err := uuid.Parse(in.ClienteID)
				if err != nil {
					return recusaEntrada("cliente_id")
				}
				f.ClientID = &id
			}
			if in.Estado != "" {
				st, achou := estadosPosVenda[in.Estado]
				if !achou {
					return recusa(fmt.Sprintf("estado desconhecido %q: use enviado ou rascunho", in.Estado), "entrada_invalida")
				}
				f.Status = st
			}
			var contagens postsale.ListCounts
			pg, err := buscarJanela(in.Paginacao, func(page, size int) ([]postsale.ListItem, int, error) {
				f.Page, f.PerPage = page, size
				res, err := L.PosVenda.List(ctx, f)
				if err != nil {
					return nil, 0, err
				}
				contagens = res.Counts
				return res.Items, res.Total, nil
			})
			if err != nil {
				return nil, err
			}
			o := listaPosVendasDTO{Contagens: contagensPosVenda{Todos: contagens.All, Enviados: contagens.Sent, Rascunhos: contagens.Draft},
				Links: links{Tela: cfg.link("/admin/pos-venda")}}
			o.lista = mapear(pg, func(it postsale.ListItem) posVendaItem {
				return posVendaItem{ID: it.ID.String(), Titulo: it.Title, Cliente: refDe(it.ClientID, it.ClientName),
					Estado: nomeEstadoPosVenda[it.Status], EnviadoEm: instanteOpc(it.SentAt), CriadoEm: instante(it.CreatedAt),
					Campanhas: it.Campaigns, Destinatarios: it.Recipients, Abriram: it.Opened,
					PeriodoDe: diaOpc(it.PeriodFrom), PeriodoAte: diaOpc(it.PeriodTo), Tela: cfg.link("/admin/pos-venda/" + it.ID.String())}
			})
			return ok(o)
		})

	ferramentaLeitura(s, cfg, "obter_pos_venda",
		"Devolve um pós-venda: cliente, estado, datas, o resumo dos destinatários e, por campanha, o período, os números "+
			"congelados no envio (valor entregue, impactos, CPM, bonificação, target) e o checking das emissoras fora do "+
			"plano; mais o link da tela. Rascunho vem sem números. As campanhas vêm paginadas em items (até 5 por página). "+
			"Para a lista de destinatários, destinatarios_pos_venda.",
		func(ctx context.Context, L *Leitura, in entradaPosVenda) (*sdk.CallToolResult, error) {
			id, err := uuid.Parse(in.PosVendaID)
			if err != nil {
				return recusaEntrada("pos_venda_id")
			}
			rep, err := L.PosVenda.Get(ctx, id)
			if errors.Is(err, postsale.ErrNotFound) {
				return naoEncontrado("pós-venda")
			}
			if err != nil {
				return nil, err
			}
			enviado := rep.Status == "sent"
			var congelados map[uuid.UUID]postsale.CampaignBlock
			if enviado {
				if congelados, err = congeladosDe(ctx, L, id); err != nil {
					return nil, err
				}
			}
			blocos := paginar(rep.Blocks, in.Paginacao.comTeto(tetoBlocosPosVenda))
			cancelada := false
			l, err := mapearErr(blocos, func(b postsale.BlockRow) (blocoPosVenda, error) {
				c, err := L.Campanhas.Get(ctx, b.CampaignID) // o estado de hoje (a cancelada sai da cobrança)
				if err != nil {
					return blocoPosVenda{}, err
				}
				cancelada = cancelada || c.Status == "cancelada"
				bl := blocoPosVenda{Campanha: refDe(b.CampaignID, b.CampaignName), Estado: c.Status,
					PeriodoDe: dia(b.From), PeriodoAte: dia(b.To)}
				// Rascunho: nada congelado (numeros/checking null). Enviado sem o bloco
				// no payload não acontece pelo publish; se acontecer, também null.
				if cb, achou := congelados[b.CampaignID]; achou {
					bl.Numeros = numerosDe(cb.KPIs)
					var n int
					bl.Checking, n = checkingDe(cb.CheckingRows)
					conformes := cb.ConformingCount
					bl.CheckingTotal, bl.EmissorasConformes = &n, &conformes
				}
				return bl, nil
			})
			if err != nil {
				return nil, err
			}
			dest := resumoDestinatarios{Total: len(rep.Recipients)}
			for _, r := range rep.Recipients {
				if r.OpenedAt != nil {
					dest.Abriram++
				}
				if r.EmailStatus == "failed" {
					dest.Falharam++
				}
				if r.RevokedAt != nil {
					dest.Revogados++
				}
			}
			var anexo *string
			if rep.AttachmentsURL != "" {
				a := soEsquemaEHost(rep.AttachmentsURL)
				anexo = &a
			}
			ress := []string{ressalvaPosVenda, fmt.Sprintf("checking de cada campanha: as %d primeiras linhas (checking_total traz o real).", tetoPorItem)}
			if !enviado {
				ress = append(ress, ressalvaRascunho)
			}
			if cancelada {
				ress = append(ress, ressalvaCanceladaRelatorio)
			}
			l.Ressalvas = ress
			return ok(posVendaDTO{ID: rep.ID.String(), Titulo: rep.Title, Cliente: refDe(rep.ClientID, rep.ClientName),
				Estado: nomeEstadoPosVenda[rep.Status], EnviadoEm: instanteOpc(rep.SentAt), CriadoEm: instante(rep.CreatedAt),
				Mensagem: rep.IntroMessage, Anexo: anexo, Destinatarios: dest, lista: l,
				Links: links{Tela: cfg.link("/admin/pos-venda/" + rep.ID.String())}})
		})

	ferramentaLeitura(s, cfg, "destinatarios_pos_venda",
		"Com pos_venda_id: lista quem recebeu aquele pós-venda (nome, email, status do email, quando abriu, quantas vezes, "+
			"se o link foi revogado). Com cliente_id: quem RECEBERIA um pós-venda novo desse cliente hoje (usuários ativos do "+
			"cliente + admins internos que pediram cópia). Passe exatamente um dos dois.",
		func(ctx context.Context, L *Leitura, in struct {
			PosVendaID string `json:"pos_venda_id,omitempty" jsonschema:"Id do pós-venda (de listar_pos_vendas). Nunca invente: pergunte."`
			ClienteID  string `json:"cliente_id,omitempty" jsonschema:"Id do cliente (de listar_clientes), para a prévia de quem receberia. Nunca invente: pergunte."`
			Paginacao
		}) (*sdk.CallToolResult, error) {
			if (in.PosVendaID == "") == (in.ClienteID == "") {
				return recusa("passe pos_venda_id OU cliente_id (exatamente um dos dois)", "entrada_invalida")
			}
			if in.PosVendaID != "" {
				id, err := uuid.Parse(in.PosVendaID)
				if err != nil {
					return recusaEntrada("pos_venda_id")
				}
				rep, err := L.PosVenda.Get(ctx, id) // traz os destinatários (ORDER BY email, id)
				if errors.Is(err, postsale.ErrNotFound) {
					return naoEncontrado("pós-venda")
				}
				if err != nil {
					return nil, err
				}
				return ok(mapear(paginar(rep.Recipients, in.Paginacao), destinatarioDe))
			}
			if _, rec, err := lerCliente(ctx, L, in.ClienteID); rec != nil || err != nil {
				return rec, err
			}
			cid, _ := uuid.Parse(in.ClienteID) // lerCliente já validou
			doCliente, err := L.PosVenda.ActiveClientUsers(ctx, cid)
			if err != nil {
				return nil, err
			}
			internos, err := L.PosVenda.InternalRecipients(ctx)
			if err != nil {
				return nil, err
			}
			cs, nc := pessoasDe(doCliente)
			is, ni := pessoasDe(internos)
			return ok(map[string]any{"cliente": cs, "cliente_total": nc, "internos": is, "internos_total": ni,
				"ressalvas": []string{"É uma prévia: quem receberia se o pós-venda fosse enviado hoje. internos = admins com cópia de todos os pós-vendas ligada. " +
					fmt.Sprintf("Cada grupo traz até %d pessoas (*_total traz o real).", tetoPrevia)}})
		})
}

// mapearErr: mapear com uma função que pode falhar (a primeira falha aborta).
func mapearErr[A, B any](l lista[A], f func(A) (B, error)) (lista[B], error) {
	out := make([]B, len(l.Items))
	for i, a := range l.Items {
		b, err := f(a)
		if err != nil {
			return lista[B]{}, err
		}
		out[i] = b
	}
	return lista[B]{Items: out, Page: l.Page, Ressalvas: l.Ressalvas}, nil
}
