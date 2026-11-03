package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"radiocheck/internal/acoes"
	"radiocheck/internal/postsale"
)

// Escritas CRÍTICAS de pós-venda (Fatia 4, Task 20): reenviar o email de UM destinatário
// e revogar o link dele. Nenhuma executa na chamada: o motor grava um pedido pendente e
// quem executa é a aprovação do dono em /pendentes, que reroda o Preview (por isso TODA
// recusa que depende do estado mora nele ou no planejar* que ele chama) e só então o
// Executar. Nenhuma recebe token de confirmação.
//
// O token do destinatário é a credencial do link público (/pos-venda/<token>, sem
// expiração): nada do que estas tools devolvem ou gravam o carrega. O destinatário sai
// pelo destinatarioDTO (whitelist, sem token) e todo texto de erro de email passa por
// semSegredoNoTexto (o higienizador também apaga o token de cada destinatário do pós-venda).
//
// F4-10 — o MCP é mais estrito que a rota:
//   - a rota de revogação revoga qualquer destinatário pelo id, sem conferir de qual
//     pós-venda ele é (o id do pós-venda nem entra nela): a tool confere (nao_encontrado,
//     com o mesmo texto de "não existe", para não dizer se o id vive em outro pós-venda);
//   - a rota de reenvio, sem SMTP no servidor, regrava o status do destinatário como
//     'disabled' (apagando o 'sent' de um email que saiu de verdade) e responde 204: a
//     tool recusa com email_desligado antes de escrever.
//
// O que NÃO é recusa: o email que falha no SMTP. O Resend grava email_status=failed (e o
// erro) ANTES de voltar, então a escrita aconteceu — recusa significa "nada foi gravado".
// O aviso do desfecho (o email que não saiu) vai nas ressalvas, que a aprovação devolve e grava.

func escritasCriticasPosVenda(cfg ConfigServidor) []escritaRegistravel {
	return []escritaRegistravel{
		escritaReenviarPosVenda(cfg),
		escritaRevogarDestinatarioPosVenda(cfg),
	}
}

const (
	// Códigos que só o MCP recusa (a rota deixa fazer qualquer um destes).
	codigoPosVendaIndisponivel = "pos_venda_indisponivel"
	codigoPosVendaSemDocumento = "pos_venda_sem_documento"
	codigoEmailDesligado       = "email_desligado"
)

type entradaDestinatarioPosVenda struct {
	PosVendaID     string `json:"pos_venda_id" jsonschema:"Id do pós-venda (de listar_pos_vendas). Nunca invente: pergunte."`
	DestinatarioID string `json:"destinatario_id" jsonschema:"Id do destinatário DESSE pós-venda (o campo id de destinatarios_pos_venda com este pos_venda_id). Nunca invente: pergunte."`
}

// posVendaDoRecurso e linkDoPosVenda são o Recurso da auditoria e a tela dessas tools.
func posVendaDoRecurso(valor string) (string, *uuid.UUID) {
	return campanhaDoRecurso("post_sale_report", valor)
}

func linkDoPosVenda(cfg ConfigServidor, valor string) *string {
	if id, err := uuid.Parse(valor); err == nil {
		return cfg.link("/admin/pos-venda/" + id.String())
	}
	return cfg.link("/admin/pos-venda")
}

// ── Planejamento ────────────────────────────────────────────────────────

// planoDeDestinatario é o pós-venda ENVIADO e o destinatário (do MESMO pós-venda) que a
// escrita mexeria, como estão agora.
type planoDeDestinatario struct {
	rep    *postsale.Report
	rec    postsale.Recipient
	h      *higienizador // limpa título e erro de email: anexo, token de cada destinatário e URLs
	ativos int           // destinatários com link ativo (este incluído, se ele estiver ativo)
}

// titulo é o título do pós-venda como a leitura o mostra (sem URL nem token).
func (pl *planoDeDestinatario) titulo() string { return pl.h.texto(pl.rep.Title, tetoTitulo) }

// pessoa é "Nome (email)" ou só o email, se a pessoa não tem nome.
func (pl *planoDeDestinatario) pessoa() string {
	if pl.rec.Name == "" {
		return pl.rec.Email
	}
	return fmt.Sprintf("%s (%s)", pl.rec.Name, pl.rec.Email)
}

func planejarDestinatario(ctx context.Context, E *Escritas, in entradaDestinatarioPosVenda) (*planoDeDestinatario, error) {
	repID, err := uuidDaEntrada("pos_venda_id", in.PosVendaID)
	if err != nil {
		return nil, err
	}
	recID, err := uuidDaEntrada("destinatario_id", in.DestinatarioID)
	if err != nil {
		return nil, err
	}
	rep, err := E.Leitura.PosVenda.Get(ctx, repID)
	if errors.Is(err, postsale.ErrNotFound) {
		return nil, errNaoEncontrado("pós-venda")
	}
	if err != nil {
		return nil, err
	}
	pl := &planoDeDestinatario{rep: rep, h: novoHigienizador(rep.AttachmentsURL, tokensDe(rep.Recipients))}
	if rep.Status != "sent" {
		return nil, errRecusa(acoes.CodigoNaoEncontrado, fmt.Sprintf(
			"o pós-venda %q ainda é rascunho: nada foi enviado e ele não tem destinatários nem links — só há o que reenviar ou revogar depois do envio", pl.titulo()))
	}
	achou := false
	for _, r := range rep.Recipients {
		if r.RevokedAt == nil {
			pl.ativos++
		}
		if r.ID == recID {
			pl.rec, achou = r, true
		}
	}
	if !achou {
		// O mesmo texto para o id que não existe e para o que é de OUTRO pós-venda: a
		// recusa não diz onde ele vive.
		return nil, errRecusa(acoes.CodigoNaoEncontrado,
			"destinatário: não existe neste pós-venda (use o id que destinatarios_pos_venda devolve para este pos_venda_id)")
	}
	return pl, nil
}

// ── reenviar_pos_venda ──────────────────────────────────────────────────

func planejarReenvio(ctx context.Context, E *Escritas, in entradaDestinatarioPosVenda) (*planoDeDestinatario, error) {
	pl, err := planejarDestinatario(ctx, E, in)
	if err != nil {
		return nil, err
	}
	if pl.rec.RevokedAt != nil {
		// Revogado não reenvia (Resend): seria ressuscitar um link que foi morto.
		return nil, errRecusa(acoes.CodigoNaoEncontrado, fmt.Sprintf(
			"o link de %s foi revogado: reenviar não ressuscita um link que foi morto, e não há como emitir outro (o pós-venda enviado não se republica)", pl.pessoa()))
	}
	if E.PosVenda == nil {
		return nil, errRecusa(codigoPosVendaIndisponivel,
			"o serviço de pós-venda não está disponível neste servidor: o reenvio não pode ser feito por aqui")
	}
	if !E.PosVenda.MailEnabled() {
		return nil, errRecusa(codigoEmailDesligado, fmt.Sprintf(
			"o envio de email está desligado neste servidor (sem SMTP configurado): nenhum email sairia para %s, e a rota ainda regravaria o status dela como disabled. "+
				"O link dela segue válido; sem SMTP o email não sai por nenhum caminho do sistema: configure o SMTP do servidor", pl.pessoa()))
	}
	if _, err := E.Leitura.PosVenda.Payload(ctx, pl.rep.ID); errors.Is(err, postsale.ErrNotFound) {
		return nil, errRecusa(codigoPosVendaSemDocumento, fmt.Sprintf(
			"o pós-venda %q está como enviado mas sem o documento congelado gravado (envio anterior ao congelamento): não há o que reenviar", pl.titulo()))
	} else if err != nil {
		return nil, err
	}
	return pl, nil
}

// reler devolve o pós-venda e o destinatário DEPOIS da escrita (o status do email e o
// revogado_em de agora).
func reler(ctx context.Context, E *Escritas, pl *planoDeDestinatario) (*postsale.Report, *postsale.Recipient, error) {
	rep, err := E.Leitura.PosVenda.Get(ctx, pl.rep.ID)
	if err != nil {
		return nil, nil, err
	}
	for i := range rep.Recipients {
		if rep.Recipients[i].ID == pl.rec.ID {
			return rep, &rep.Recipients[i], nil
		}
	}
	return nil, nil, postsale.ErrNotFound
}

// saidaDestinatarioPosVenda é o resultado das duas tools: o destinatário no formato de
// destinatarios_pos_venda (sem token) e de qual pós-venda ele é.
type saidaDestinatarioPosVenda struct {
	PosVenda     ref             `json:"pos_venda"`
	Destinatario destinatarioDTO `json:"destinatario"`
}

// saidaDestinatarioSemLeitura é o resultado quando a escrita foi gravada mas a releitura
// falhou: quem é o destinatário e o que conferir.
type saidaDestinatarioSemLeitura struct {
	PosVenda       ref    `json:"pos_venda"`
	DestinatarioID string `json:"destinatario_id"`
}

// destinatarioFeito é o feito de uma escrita de destinatário JÁ gravada. Se só a
// releitura falhar, a escrita NÃO vira erro (o modelo a repetiria): devolve quem é e manda
// conferir. avisar monta o aviso do desfecho (vazio = nada a avisar) a partir do
// destinatário relido e do pós-venda relido.
func destinatarioFeito(ctx context.Context, cfg ConfigServidor, E *Escritas, pl *planoDeDestinatario,
	avisar func(dto destinatarioDTO, rep *postsale.Report) string) feito {
	f := feito{Recurso: "post_sale_report", RecursoID: &pl.rep.ID}
	rep, rec, err := reler(ctx, E, pl)
	if err != nil {
		cfg.log().Error("mcp: releitura do destinatário do pós-venda após a escrita falhou", zap.Error(err))
		aviso := "A escrita foi gravada, mas não consegui reler o destinatário agora: confira o estado com destinatarios_pos_venda (pos_venda_id " + pl.rep.ID.String() + ")."
		f.Resultado = saidaDestinatarioSemLeitura{PosVenda: refDe(pl.rep.ID, pl.titulo()), DestinatarioID: pl.rec.ID.String()}
		f.Ressalvas = []string{aviso}
		return f
	}
	h := novoHigienizador(rep.AttachmentsURL, tokensDe(rep.Recipients))
	dto := destinatarioDe(h)(*rec)
	saida := saidaDestinatarioPosVenda{PosVenda: refDe(rep.ID, h.texto(rep.Title, tetoTitulo)), Destinatario: dto}
	if aviso := avisar(dto, rep); aviso != "" {
		f.Ressalvas = append(f.Ressalvas, aviso)
	}
	if h.cortou {
		f.Ressalvas = append(f.Ressalvas, ressalvaTextoCortado)
	}
	f.Resultado = saida
	return f
}

// avisoDoReenvio diz o desfecho do email quando ele não foi um 'sent' limpo. O erro vem
// do destinatarioDTO: já passou por semSegredoNoTexto (sem link nem token) e pelo teto.
func avisoDoReenvio(dto destinatarioDTO) string {
	switch dto.StatusEmail {
	case "sent":
		return ""
	case "failed":
		erro := "o servidor não deu detalhe"
		if dto.ErroEmail != nil && *dto.ErroEmail != "" {
			erro = *dto.ErroEmail
		}
		return fmt.Sprintf("O email NÃO saiu: o envio para %s falhou (status failed) — %s. Nada chegou à pessoa; o link dela segue valendo. "+
			"Confira o endereço e o SMTP e tente reenviar de novo.", dto.Email, erro)
	case "disabled":
		return fmt.Sprintf("Nenhum email saiu: o envio de email está desligado neste servidor (status disabled). O link de %s segue valendo.", dto.Email)
	}
	return fmt.Sprintf("O status do email de %s ficou em %q: confira com destinatarios_pos_venda.", dto.Email, dto.StatusEmail)
}

func escritaReenviarPosVenda(cfg ConfigServidor) Escrita[entradaDestinatarioPosVenda] {
	return Escrita[entradaDestinatarioPosVenda]{
		Nome: "reenviar_pos_venda",
		Descricao: "Reenvia o email do pós-venda a UM destinatário que já recebeu (o botão Reenviar de /admin/pos-venda/<id>): o email sai agora, de verdade, para o endereço " +
			"dessa pessoa. Use quando ela diz que o email não chegou ou o status do email dela é failed em destinatarios_pos_venda. As armadilhas: (1) é o MESMO link — o token não " +
			"muda, então o link que ela já tem segue valendo; reenviar não corta nada, e para cortar um link que vazou é revogar_destinatario_pos_venda, que é outra coisa; " +
			"(2) manda email para FORA do E-monitor, a uma pessoa por vez, e cada aprovação manda mais um; (3) se o SMTP falhar o reenvio NÃO vira erro: o status dela passa a " +
			"failed, o erro fica gravado e a aprovação traz o aviso nas ressalvas — diga que o email não saiu; e quando sai, o servidor entregou ao SMTP, não é garantia de que ela o leu. " +
			"Pós-venda em rascunho, destinatário que não é desse pós-venda e link revogado dão nao_encontrado; sem SMTP no servidor, email_desligado. Os ids vêm de " +
			"listar_pos_vendas (o pós-venda) e de destinatarios_pos_venda (o destinatário). O resultado é {pos_venda, destinatario}: o destinatário no formato de " +
			"destinatarios_pos_venda (sem o link nem o token). " + fraseCritica,
		Nivel:   Critico,
		Motivo:  "manda email para fora",
		Recurso: func(in entradaDestinatarioPosVenda) (string, *uuid.UUID) { return posVendaDoRecurso(in.PosVendaID) },
		Tela:    func(_ *Escritas, in entradaDestinatarioPosVenda) *string { return linkDoPosVenda(cfg, in.PosVendaID) },
		Preview: func(ctx context.Context, E *Escritas, in entradaDestinatarioPosVenda) (previa, error) {
			pl, err := planejarReenvio(ctx, E, in)
			if err != nil {
				return previa{}, err
			}
			dto := destinatarioDe(pl.h)(pl.rec)
			pv := previa{
				Resumo: fmt.Sprintf("Reenviar o pós-venda %q para %s", pl.titulo(), pl.pessoa()),
				Mudancas: []mudanca{{Campo: "status_email", De: pl.rec.EmailStatus,
					Para: "o desfecho do novo envio (sent ou failed)"}},
				Efeitos: []string{
					fmt.Sprintf("O email do pós-venda sai AGORA do servidor para %s, com o MESMO link pessoal dela: o token não muda, e o link que ela já tem segue valendo.", pl.rec.Email),
					"O email vai para fora do E-monitor, para o endereço do destinatário; cada aprovação manda mais um.",
				},
			}
			if pl.rec.OpenedAt != nil {
				pv.Ressalvas = append(pv.Ressalvas, fmt.Sprintf(
					"Essa pessoa já abriu o documento %d vez(es): o reenvio só repete o email, não muda o que ela vê (o pós-venda enviado é congelado).", pl.rec.OpenCount))
			}
			if pl.rec.EmailStatus == "failed" && dto.ErroEmail != nil {
				pv.Ressalvas = append(pv.Ressalvas, "O último envio para ela falhou: "+*dto.ErroEmail)
			}
			pv.Ressalvas = append(pv.Ressalvas,
				"Se o SMTP recusar o email, o reenvio NÃO vira erro: o status dela passa a failed, o erro fica gravado e a aprovação traz o aviso nas ressalvas — o pedido consta como executado.",
				"Sucesso aqui quer dizer que o servidor entregou o email ao SMTP, não que a pessoa o recebeu ou leu.",
				"Reenviar não corta link nenhum. Para matar um link que vazou ou foi para a pessoa errada use revogar_destinatario_pos_venda.")
			if pl.h.cortou {
				pv.Ressalvas = append(pv.Ressalvas, ressalvaTextoCortado)
			}
			return pv, nil
		},
		Executar: func(ctx context.Context, E *Escritas, _ *Principal, in entradaDestinatarioPosVenda) (feito, error) {
			pl, err := planejarReenvio(ctx, E, in)
			if err != nil {
				return feito{}, err
			}
			if err := E.PosVenda.Resend(ctx, pl.rep.ID, pl.rec.ID); err != nil {
				if errors.Is(err, postsale.ErrNotFound) {
					// O Resend confere tudo ANTES de mandar: nada saiu nem foi gravado. O
					// pós-venda, o destinatário ou o documento mudaram entre a conferência e o envio.
					return feito{}, errRecusa(acoes.CodigoNaoEncontrado,
						"o pós-venda ou o destinatário mudou enquanto o pedido esperava (link revogado, destinatário apagado ou pós-venda de volta a rascunho): "+
							"nada foi enviado. Confira com destinatarios_pos_venda")
				}
				return feito{}, err
			}
			// O Resend devolve só o erro da gravação do status: o desfecho do email (sent,
			// failed, disabled) está no banco, e é de lá que o resultado o lê.
			return destinatarioFeito(ctx, cfg, E, pl, func(dto destinatarioDTO, _ *postsale.Report) string { return avisoDoReenvio(dto) }), nil
		},
	}
}

// ── revogar_destinatario_pos_venda ──────────────────────────────────────

func planejarRevogacao(ctx context.Context, E *Escritas, in entradaDestinatarioPosVenda) (*planoDeDestinatario, error) {
	pl, err := planejarDestinatario(ctx, E, in)
	if err != nil {
		return nil, err
	}
	if pl.rec.RevokedAt != nil {
		return nil, errRecusa(codigoSemMudanca, fmt.Sprintf(
			"o link de %s já está revogado (desde %s): nada a fazer", pl.pessoa(), instante(*pl.rec.RevokedAt)))
	}
	return pl, nil
}

const avisoUltimoLinkRevogado = "Este era o último link ativo do pós-venda: ninguém do cliente consegue mais abrir o documento, e não há como emitir outro."

func escritaRevogarDestinatarioPosVenda(cfg ConfigServidor) Escrita[entradaDestinatarioPosVenda] {
	return Escrita[entradaDestinatarioPosVenda]{
		Nome: "revogar_destinatario_pos_venda",
		Descricao: "Revoga o link pessoal de UM destinatário de um pós-venda enviado (o botão Revogar de /admin/pos-venda/<id>): o link dele para de abrir na hora, para sempre. " +
			"Use para cortar um link vazado ou que foi para a pessoa errada; os links dos outros destinatários não mudam. A armadilha: é IRREVERSÍVEL e sem substituto — " +
			"não há como reativar o link nem emitir outro para esta pessoa, porque o pós-venda enviado é um documento congelado que não se republica (nem a tela, nem outra tool); " +
			"se ela ainda precisa ver o documento, não revogue. O email que ela já recebeu continua na caixa dela, só que o link dentro dele passa a dar erro. Para repetir o " +
			"email com o MESMO link, sem cortar nada, é reenviar_pos_venda. Link já revogado dá sem_mudanca; pós-venda em rascunho e destinatário que não é desse pós-venda " +
			"dão nao_encontrado. Os ids vêm de listar_pos_vendas (o pós-venda) e de destinatarios_pos_venda (o destinatário). O resultado é {pos_venda, destinatario}: " +
			"o destinatário no formato de destinatarios_pos_venda, já com revogado_em. " + fraseCritica,
		Nivel:   Critico,
		Motivo:  "irreversível: o link morre e não há como emitir outro para esta pessoa (o documento não se republica)",
		Recurso: func(in entradaDestinatarioPosVenda) (string, *uuid.UUID) { return posVendaDoRecurso(in.PosVendaID) },
		Tela:    func(_ *Escritas, in entradaDestinatarioPosVenda) *string { return linkDoPosVenda(cfg, in.PosVendaID) },
		Preview: func(ctx context.Context, E *Escritas, in entradaDestinatarioPosVenda) (previa, error) {
			pl, err := planejarRevogacao(ctx, E, in)
			if err != nil {
				return previa{}, err
			}
			pv := previa{
				Resumo:   fmt.Sprintf("Revogar o link de %s no pós-venda %q", pl.pessoa(), pl.titulo()),
				Mudancas: []mudanca{{Campo: "link", De: "ativo", Para: "revogado"}},
				Efeitos: []string{
					fmt.Sprintf("O link pessoal de %s para de abrir na hora (a página responde \"não encontrado\") e nunca mais volta: a revogação é IRREVERSÍVEL.", pl.pessoa()),
					"Não há como emitir outro link para esta pessoa: o pós-venda enviado é um documento congelado e não se republica.",
					"O email que ela já recebeu continua na caixa dela, mas o link dentro dele passa a dar erro.",
				},
			}
			if pl.rec.OpenedAt != nil {
				pv.Efeitos = append(pv.Efeitos, fmt.Sprintf("Ela já abriu o documento %d vez(es): o histórico de aberturas fica gravado.", pl.rec.OpenCount))
			}
			if restam := pl.ativos - 1; restam > 0 {
				pv.Efeitos = append(pv.Efeitos, fmt.Sprintf("Os links dos outros destinatários não mudam. Restam %d link(s) ativo(s) neste pós-venda.", restam))
			} else {
				pv.Ressalvas = append(pv.Ressalvas,
					"É o ÚLTIMO link ativo deste pós-venda: depois disso ninguém do cliente consegue abrir o documento.")
			}
			if pl.h.cortou {
				pv.Ressalvas = append(pv.Ressalvas, ressalvaTextoCortado)
			}
			return pv, nil
		},
		Executar: func(ctx context.Context, E *Escritas, p *Principal, in entradaDestinatarioPosVenda) (feito, error) {
			pl, err := planejarRevogacao(ctx, E, in)
			if err != nil {
				return feito{}, err
			}
			// `by` é o usuário que revoga (revoked_by, FK para users), como a rota passa o
			// admin logado: aqui, quem executa — o dono do pedido, logado na aprovação. Não
			// existe "dono do relatório" nessa chamada. O UUID zero vira NULL no repositório.
			by := uuid.Nil
			if p != nil {
				by = p.UserID
			}
			if err := E.Leitura.PosVenda.RevokeRecipient(ctx, pl.rec.ID, by); err != nil {
				if errors.Is(err, postsale.ErrNotFound) {
					return feito{}, errRecusa(acoes.CodigoNaoEncontrado,
						"o destinatário sumiu enquanto o pedido esperava: nada foi revogado. Confira com destinatarios_pos_venda")
				}
				return feito{}, err
			}
			return destinatarioFeito(ctx, cfg, E, pl, func(_ destinatarioDTO, rep *postsale.Report) string {
				for _, r := range rep.Recipients {
					if r.RevokedAt == nil {
						return ""
					}
				}
				return avisoUltimoLinkRevogado
			}), nil
		},
	}
}
