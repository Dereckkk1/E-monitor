package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"radiocheck/internal/acoes"
	"radiocheck/internal/catalog"
	"radiocheck/internal/categorizer"
)

// Escritas CRÍTICAS de veiculação (Fatia 4, Task 16): lançar à mão, ignorar e
// restaurar. As três mexem em dado que o cliente já viu e, por causa da cota do
// dia, na categoria das OUTRAS veiculações da mesma célula-dia (campanha × tipo ×
// emissora × dia em America/Sao_Paulo): lançar uma tocada pode rebaixar uma
// in_slot a bonus; ignorar a in_slot libera a vaga para a próxima do dia;
// restaurar a faz retomar o lugar cronológico dela. Nenhuma executa na chamada: o
// motor grava um pedido pendente e quem executa é a aprovação do dono em
// /pendentes, que reroda o Preview (por isso TODA recusa que depende do estado
// mora nele ou no planejar* que ele chama) e só então o Executar. Nenhuma recebe
// token de confirmação. Todas devolvem a veiculação no formato de
// obter_veiculacao (montarVeiculacao).

func escritasCriticasVeiculacoes(cfg ConfigServidor) []escritaRegistravel {
	return []escritaRegistravel{
		escritaCriarVeiculacaoManual(cfg),
		escritaIgnorarVeiculacao(cfg),
		escritaRestaurarVeiculacao(cfg),
	}
}

const (
	// codigoJaIgnorada e codigoNaoIgnorada: só o MCP recusa (a rota deixa ignorar
	// de novo — só carimba outra hora — e restaurar quem nunca foi ignorada).
	codigoJaIgnorada  = "ja_ignorada"
	codigoNaoIgnorada = "nao_ignorada"

	// tetoNotaVeiculacao: a nota livre da veiculação lançada à mão, em caracteres.
	tetoNotaVeiculacao = 500

	// janelaDuplicata é o entorno, para cada lado, em que uma veiculação aprovada
	// do mesmo material na mesma emissora e campanha vira aviso de possível
	// duplicata (F4-13). Só avisa: quem decide é a pessoa que aprova.
	janelaDuplicata = 10 * time.Minute
	// tetoDuplicatasNoAviso: quantas das mais próximas o aviso nomeia.
	tetoDuplicatasNoAviso = 3

	// formatoDiaHora é o dia e a hora que a pessoa lê, no fuso de São Paulo.
	formatoDiaHora = "02/01/2006 15:04"
)

// ── Helpers ─────────────────────────────────────────────────────────────

// horaSP formata um instante em America/Sao_Paulo para a pessoa ler
// ("20/10/2026 08:30"; com segundos só quando existem).
func horaSP(t time.Time) string {
	l := t.In(fusoSP)
	if l.Second() != 0 || l.Nanosecond() != 0 {
		return l.Format(formatoDiaHora + ":05")
	}
	return l.Format(formatoDiaHora)
}

// instanteSP: horaSP com o fuso por extenso.
func instanteSP(t time.Time) string { return horaSP(t) + " (America/Sao_Paulo)" }

// diaSP é o dia (AAAA-MM-DD) em que o instante cai em America/Sao_Paulo — o dia da
// célula-dia.
func diaSP(t time.Time) string { return t.In(fusoSP).Format("2006-01-02") }

// diaPorExtenso: o dia da célula como a pessoa lê (20/10/2026).
func diaPorExtenso(t time.Time) string { return t.In(fusoSP).Format("02/01/2006") }

// instanteDaEntrada lê um instante RFC3339 COM fuso. Sem o fuso a hora seria
// adivinhada (UTC? São Paulo?) e a veiculação cairia no dia errado: recusa.
func instanteDaEntrada(campo, valor string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, aparar(valor))
	if err != nil {
		return time.Time{}, errEntrada(fmt.Sprintf(
			"%s inválido: use RFC3339 COM o fuso, por exemplo 2026-10-20T09:30:00-03:00 (09h30 em São Paulo); sem o fuso o pedido é recusado — nunca presuma o fuso, pergunte à pessoa", campo))
	}
	return t, nil
}

// distanciaPorExtenso: "5 min antes" / "5 min depois" / "no mesmo instante", de
// uma tocada (outra) em relação ao instante pedido (quando).
func distanciaPorExtenso(outra, quando time.Time) string {
	d := quando.Sub(outra)
	if d == 0 {
		return "no mesmo instante"
	}
	lado := "antes"
	if d < 0 {
		lado, d = "depois", -d
	}
	if m := int(d.Round(time.Minute) / time.Minute); m > 0 {
		return fmt.Sprintf("%d min %s", m, lado)
	}
	return "menos de 1 min " + lado
}

// nomeDoTipoOuSemTipo: o nome do tipo do material ("sem tipo" quando o material
// não tem — a célula-dia dele fica no balde sem tipo).
func nomeDoTipoOuSemTipo(ctx context.Context, L *Leitura, tipo *uuid.UUID) (string, error) {
	if tipo == nil {
		return "sem tipo", nil
	}
	t, err := L.TiposMaterial.Get(ctx, *tipo)
	if errors.Is(err, pgx.ErrNoRows) {
		return tipo.String(), nil
	}
	if err != nil {
		return "", err
	}
	return t.Name, nil
}

// linkDasVeiculacoes é a lista de veiculações da campanha: o que a tela de
// /detections aceita como deep link é só a campanha, e é lá que a pessoa lança o
// comprovante (PDF) e a veiculação à mão.
func linkDasVeiculacoes(cfg ConfigServidor, campanhaID string) *string {
	if id, err := uuid.Parse(campanhaID); err == nil {
		return cfg.linkTela("/detections", url.Values{"campaign_id": {id.String()}})
	}
	return cfg.link("/detections")
}

// linkDaVeiculacao é a página da veiculação, onde a tela ignora e restaura.
func linkDaVeiculacao(cfg ConfigServidor, valor string) *string {
	if id, err := uuid.Parse(valor); err == nil {
		return cfg.link("/detections/" + id.String())
	}
	return cfg.link("/detections")
}

// veiculacaoDoRecurso é o Recurso da auditoria das tools que mexem numa
// veiculação existente.
func veiculacaoDoRecurso(valor string) (string, *uuid.UUID) {
	return recursoDe("detection", valor)
}

// veiculacaoFeita é o feito de uma escrita de veiculação JÁ gravada: o resultado
// é a veiculação como ficou, no formato de obter_veiculacao (feitoRelido: se só a
// releitura falhar, a escrita NÃO vira erro — o modelo a repetiria e lançaria outra
// tocada —, devolve o id e manda conferir).
func veiculacaoFeita(ctx context.Context, cfg ConfigServidor, E *Escritas, id uuid.UUID, ressalvas ...string) feito {
	return feitoRelido(cfg, alvoGravado{Recurso: "detection", ID: id, Artigo: "a veiculação", Conferir: "obter_veiculacao (id " + id.String() + ")"},
		func() (saidaObterVeiculacao, error) { return montarVeiculacao(ctx, cfg, E.Leitura, id) },
		saidaSoID{ID: id.String()}, ressalvas...)
}

// ── criar_veiculacao_manual ─────────────────────────────────────────────

type entradaCriarVeiculacaoManual struct {
	CampanhaID string `json:"campanha_id" jsonschema:"Id (uuid) da campanha, vindo de listar_campanhas. Nunca invente: pergunte."`
	EmissoraID string `json:"emissora_id" jsonschema:"Id (uuid) da emissora, vindo de buscar_emissoras ou de obter_campanha. Nunca invente: pergunte."`
	MaterialID string `json:"material_id" jsonschema:"Id (uuid) do material que foi ao ar, vindo de listar_materiais_campanha. Nunca invente: pergunte."`
	Quando     string `json:"quando" jsonschema:"Instante em que foi ao ar, em RFC3339 COM o fuso, por exemplo 2026-10-20T09:30:00-03:00 (09h30 em São Paulo). Sem o fuso o pedido é recusado: nunca presuma, pergunte. Não pode estar no futuro."`
	Nota       string `json:"nota,omitempty" jsonschema:"Observação livre (até 500 caracteres) gravada na veiculação, por exemplo de onde veio a informação."`
}

// planoDeVeiculacaoManual é o que o pedido lançaria, já validado contra o estado.
type planoDeVeiculacaoManual struct {
	camp     *catalog.Campaign
	est      *catalog.Station
	mat      *catalog.Material
	tipo     string
	quando   time.Time
	nota     string
	proximas []time.Time // aprovadas do mesmo material no entorno (possível duplicata)
}

func planejarVeiculacaoManual(ctx context.Context, E *Escritas, in entradaCriarVeiculacaoManual) (*planoDeVeiculacaoManual, error) {
	camp, err := lerCampanhaParaEscrever(ctx, E.Leitura, in.CampanhaID)
	if err != nil {
		return nil, err
	}
	estID, err := uuidDaEntrada("emissora_id", in.EmissoraID)
	if err != nil {
		return nil, err
	}
	matID, err := uuidDaEntrada("material_id", in.MaterialID)
	if err != nil {
		return nil, err
	}
	quando, err := instanteDaEntrada("quando", in.Quando)
	if err != nil {
		return nil, err
	}
	nota := aparar(in.Nota)
	if err := limiteDeRunas("nota", nota, tetoNotaVeiculacao); err != nil {
		return nil, err
	}

	est, err := E.Leitura.Emissoras.Get(ctx, estID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNaoEncontrado("emissora")
	}
	if err != nil {
		return nil, err
	}
	mat, err := E.Leitura.Materiais.Get(ctx, matID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNaoEncontrado("material")
	}
	if err != nil {
		return nil, err
	}

	// O vínculo material × emissora × campanha é a condição da própria escrita
	// (catalog.CreateManual): checá-la AQUI recusa no pedido, não só depois que a
	// pessoa aprovou.
	vinculos, err := E.Leitura.MateriaisCampanha.ListByCampaign(ctx, camp.ID)
	if err != nil {
		return nil, err
	}
	var vinculo *catalog.CampaignMaterial
	for i := range vinculos {
		if vinculos[i].MaterialID == mat.ID {
			vinculo = &vinculos[i]
			break
		}
	}
	switch {
	case vinculo == nil:
		return nil, &acoes.Recusa{Codigo: acoes.CodigoMaterialNaoVinculado, Mensagem: fmt.Sprintf(
			"o material %q não está vinculado à campanha %q: vincule-o primeiro (vincular_material) e só então lance a veiculação", mat.Title, camp.Name)}
	case !slices.Contains(vinculo.TargetStations, est.ID):
		return nil, &acoes.Recusa{Codigo: acoes.CodigoMaterialNaoVinculado, Mensagem: fmt.Sprintf(
			"o material %q está na campanha %q mas não está vinculado à emissora %q (toca em %d emissora(s)): inclua a emissora no vínculo (editar_emissoras_material) e só então lance a veiculação",
			mat.Title, camp.Name, est.Name, len(vinculo.TargetStations))}
	}

	// Mesma régua da escrita (acoes.CriarVeiculacaoManual): o relógio e a
	// tolerância são os dela.
	if E.Acoes.NoFuturo(quando) {
		return nil, errEntrada(fmt.Sprintf(
			"quando (%s) está no futuro: só se lança veiculação que já foi ao ar (a tolerância é de 5 minutos de diferença de relógio)", instanteSP(quando)))
	}
	temParticao, err := E.Leitura.Veiculacoes.HasPartitionFor(ctx, quando)
	if err != nil {
		return nil, err
	}
	if !temParticao {
		return nil, errEntrada(fmt.Sprintf(
			"quando (%s) está fora do período que o sistema armazena (não há partição de veiculações para esse mês): confira o ano e o mês", instanteSP(quando)))
	}

	tipo, err := nomeDoTipoOuSemTipo(ctx, E.Leitura, mat.TypeID)
	if err != nil {
		return nil, err
	}
	proximas, err := E.Leitura.Veiculacoes.ApprovedNear(ctx, camp.ID, est.ID, mat.ID, quando, janelaDuplicata, tetoDuplicatasNoAviso)
	if err != nil {
		return nil, err
	}
	return &planoDeVeiculacaoManual{camp: camp, est: est, mat: mat, tipo: tipo, quando: quando, nota: nota, proximas: proximas}, nil
}

// celulaDia descreve a célula-dia em que o instante cai.
func celulaDia(campanha, emissora, tipo string, quando time.Time) string {
	return fmt.Sprintf("campanha %q × emissora %q × tipo %q × dia %s", campanha, emissora, tipo, diaPorExtenso(quando))
}

func escritaCriarVeiculacaoManual(cfg ConfigServidor) Escrita[entradaCriarVeiculacaoManual] {
	return Escrita[entradaCriarVeiculacaoManual]{
		Nome: "criar_veiculacao_manual",
		Descricao: "Lança à mão UMA veiculação que o sistema não detectou: campanha, emissora, material e o instante em que foi ao ar (quando, em RFC3339 COM o " +
			"fuso, ex.: 2026-10-20T09:30:00-03:00; nunca presuma o fuso: pergunte). Ela entra na cobrança como as detectadas e disputa a cota do dia: pode mudar " +
			"a categoria das outras veiculações da mesma célula-dia (campanha × tipo × emissora × dia em America/Sao_Paulo), por exemplo rebaixar uma in_slot a bonus. " +
			"ANTES de chamar, PERGUNTE à pessoa se existe o comprovante (PDF) da emissora: o PDF só entra na criação, pela tela de /detections, e esta tool " +
			"lança SEM comprovante — havendo, não chame: mande a pessoa fazer pela tela. O áudio da censura pode ser anexado depois pela tela. Recusas: " +
			"material não vinculado àquela emissora na campanha (material_nao_vinculado), instante no futuro, campanha cancelada. Uma veiculação aprovada do mesmo " +
			"material, emissora e campanha a até 10 min NÃO recusa, mas o preview avisa (pode ser duplicata). " + fraseCritica,
		Nivel:  Critico,
		Motivo: "entra na cobrança e pode mudar a categoria das outras veiculações do mesmo dia",
		// A veiculação ainda não existe: o alvo do pedido é o tipo de recurso, sem id.
		Recurso: func(entradaCriarVeiculacaoManual) (string, *uuid.UUID) { return "detection", nil },
		Tela: func(_ *Escritas, in entradaCriarVeiculacaoManual) *string {
			return linkDasVeiculacoes(cfg, in.CampanhaID)
		},
		Preview: func(ctx context.Context, E *Escritas, in entradaCriarVeiculacaoManual) (previa, error) {
			pl, err := planejarVeiculacaoManual(ctx, E, in)
			if err != nil {
				return previa{}, err
			}
			pv := previa{
				Resumo: fmt.Sprintf("Lançar à mão 1 veiculação de %q na emissora %q (campanha %q) em %s",
					pl.mat.Title, pl.est.Name, pl.camp.Name, instanteSP(pl.quando)),
				Mudancas: []mudanca{
					{Campo: "campanha", Para: pl.camp.Name},
					{Campo: "emissora", Para: pl.est.Name},
					{Campo: "material", Para: pl.mat.Title},
					{Campo: "tipo", Para: pl.tipo},
					{Campo: "quando", Para: instanteSP(pl.quando)},
				},
			}
			if pl.nota != "" {
				pv.Mudancas = append(pv.Mudancas, mudanca{Campo: "nota", Para: pl.nota})
			}
			pv.Efeitos = append(pv.Efeitos,
				fmt.Sprintf("Entra como veiculação lançada à mão (confiança 100%%, sem áudio) e disputa a cota do dia com as automáticas, na célula-dia %s: "+
					"a categoria dela é decidida na hora e pode rebaixar ou promover outras veiculações do mesmo dia.",
					celulaDia(pl.camp.Name, pl.est.Name, pl.tipo, pl.quando)),
				"Passa a contar em /detections, /insights, /campaigns e nos relatórios (só in_slot e bonus entram nos impactos).")
			if d := diaSP(pl.quando); d < dia(pl.camp.StartDate) || d > dia(pl.camp.EndDate) {
				pv.Efeitos = append(pv.Efeitos, fmt.Sprintf(
					"O dia %s está fora do período da campanha (%s a %s): a veiculação entra como out_date (fora do prazo), não consome a cota do dia e não entra nos impactos.",
					diaPorExtenso(pl.quando), dia(pl.camp.StartDate), dia(pl.camp.EndDate)))
			}
			if pl.camp.Status == "concluida" {
				pv.Ressalvas = append(pv.Ressalvas,
					"A campanha está concluída: a veiculação entra mesmo assim e muda os números que o cliente já viu.")
			}
			tela := ""
			if l := linkDasVeiculacoes(cfg, in.CampanhaID); l != nil {
				tela = " (" + *l + ")"
			}
			pv.Ressalvas = append(pv.Ressalvas,
				"Lançada SEM comprovante: o comprovante (PDF) da emissora só entra na criação, pela tela de /detections"+tela+" — não há como anexá-lo depois. "+
					"Se a pessoa tem o comprovante, não aprove este pedido e faça pela tela. O áudio da censura pode ser anexado depois, na página da veiculação.")
			if len(pl.proximas) > 0 {
				partes := make([]string, len(pl.proximas))
				for i, p := range pl.proximas {
					partes[i] = fmt.Sprintf("%s (%s)", horaSP(p), distanciaPorExtenso(p, pl.quando))
				}
				pv.Ressalvas = append(pv.Ressalvas, fmt.Sprintf(
					"Já existe veiculação aprovada deste material nesta emissora e campanha a até %d min deste instante: %s. Pode ser a mesma tocada lançada duas vezes — se for, não aprove este pedido.",
					int(janelaDuplicata/time.Minute), strings.Join(partes, "; ")))
			}
			return pv, nil
		},
		Executar: func(ctx context.Context, E *Escritas, p *Principal, in entradaCriarVeiculacaoManual) (feito, error) {
			pl, err := planejarVeiculacaoManual(ctx, E, in)
			if err != nil {
				return feito{}, err
			}
			det, err := E.Acoes.CriarVeiculacaoManual(ctx, acoes.EntradaManual{
				Campanha: pl.camp.ID, Emissora: pl.est.ID, Material: pl.mat.ID, Quando: pl.quando, Nota: pl.nota, Ator: p.UserID,
			})
			if err != nil {
				return feito{}, err
			}
			return veiculacaoFeita(ctx, cfg, E, det.ID,
				"A categoria dela e a das outras veiculações do mesmo dia já foram recalculadas (veja categoria). Foi lançada sem comprovante; "+
					"o áudio da censura pode ser anexado depois, na página da veiculação."), nil
		},
	}
}

// ── ignorar_veiculacao e restaurar_veiculacao ───────────────────────────

// contextoDaVeiculacao é a tocada como ela está agora e a célula-dia que o
// ignorar/restaurar refecharia.
type contextoDaVeiculacao struct {
	det      *catalog.Detection
	campanha string
	tipo     string
	outras   []string // nomes das outras campanhas a que a tocada conta
	aprovada bool
	// foraPorOutroMotivo: por que a tocada está (ou continuaria) fora do conjunto
	// aprovado além de ignorada ("" = nenhum).
	foraPorOutroMotivo string
}

func lerContextoDaVeiculacao(ctx context.Context, E *Escritas, valor string) (*contextoDaVeiculacao, error) {
	id, err := uuidDaEntrada("veiculacao_id", valor)
	if err != nil {
		return nil, err
	}
	det, err := E.Leitura.Veiculacoes.Get(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNaoEncontrado("veiculação")
	}
	if err != nil {
		return nil, err
	}
	atribs, err := E.Leitura.Veiculacoes.Attributions(ctx, id, det.DetectedAt) // detected_at poda as partições
	if err != nil {
		return nil, err
	}
	aprovada, err := E.Leitura.Veiculacoes.Approved(ctx, id, det.DetectedAt)
	if err != nil {
		return nil, err
	}
	tipo, err := nomeDoTipoOuSemTipo(ctx, E.Leitura, det.TypeID)
	if err != nil {
		return nil, err
	}
	cx := &contextoDaVeiculacao{det: det, tipo: tipo, aprovada: aprovada}
	for _, a := range atribs {
		if a.CampaignID == det.CampaignID {
			cx.campanha = a.CampaignName
		} else {
			cx.outras = append(cx.outras, a.CampaignName)
		}
	}
	if cx.campanha == "" {
		cx.campanha = det.CampaignID.String()
		camp, err := E.Leitura.Campanhas.Get(ctx, det.CampaignID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if camp != nil {
			cx.campanha = camp.Name
		}
	}
	switch {
	case det.EvidenceStatus == "ambiguous":
		cx.foraPorOutroMotivo = "ambígua: gêmeo acústico sem atribuição"
	case det.RetractedAt != nil:
		cx.foraPorOutroMotivo = "retratada pela desambiguação de versões"
	case det.EvidenceStatus == "audit_rejected":
		cx.foraPorOutroMotivo = "rejeitada pelo audit de evidência"
	}
	return cx, nil
}

func (cx *contextoDaVeiculacao) celula() string {
	return celulaDia(cx.campanha, cx.det.StationName, cx.tipo, cx.det.DetectedAt)
}

// quem: "de "Material" na emissora "Rádio 1" em 20/10/2026 08:10 (America/Sao_Paulo)".
func (cx *contextoDaVeiculacao) quem() string {
	return fmt.Sprintf("de %q na emissora %q em %s", cx.det.CommercialName, cx.det.StationName, instanteSP(cx.det.DetectedAt))
}

// efeitosComuns: a célula refechada e, se a tocada conta para mais de uma
// campanha, a célula de cada uma.
func (cx *contextoDaVeiculacao) efeitosComuns() []string {
	ef := []string{fmt.Sprintf("Célula-dia afetada: %s. Ela é refechada na mesma operação.", cx.celula())}
	if len(cx.outras) > 0 {
		ef = append(ef, fmt.Sprintf(
			"Esta tocada também conta para %d outra(s) campanha(s) (%s): a célula-dia de cada uma é refechada junto.",
			len(cx.outras), listaCortada(cx.outras)))
	}
	return ef
}

func efeitoDeSair(categoria string) string {
	switch categoriaDe(categoria) {
	case categorizer.CatInSlot:
		return "Hoje ela ocupa uma vaga da cota do dia (in_slot). Ao sair, a vaga é liberada na mesma operação: a próxima veiculação elegível do dia " +
			"(dentro da faixa, hoje bonus) passa a in_slot; se não houver, o déficit do dia sobe em 1 e os impactos e o valor entregue caem."
	case categorizer.CatBonus:
		return "Hoje ela é bonus (além da cota): não ocupa vaga na cota, então o in_slot e o déficit do dia seguem como estão; a bonificação do dia diminui em 1."
	case categorizer.CatOutSlot:
		return "Hoje ela é out_slot (fora da faixa, não vale nada): não ocupa vaga na cota, então as outras veiculações do dia não mudam de categoria."
	case categorizer.CatOutDate:
		return "Hoje ela é out_date (fora do período da campanha): não entra na cota do dia, então as outras veiculações não mudam de categoria."
	}
	return "As outras veiculações do dia podem mudar de categoria."
}

const efeitoDeVoltar = "Ao voltar, ela retoma o lugar cronológico dela na cota do dia: se for uma das primeiras dentro da faixa, vira in_slot e rebaixa a bonus " +
	"quem ocupou a vaga dela; se for fora da faixa, vira out_slot ou bonus conforme a cota. A categoria gravada hoje é de antes de ela sair e pode mudar."

const ressalvaPosVendaCongelado = "O pós-venda já enviado não muda (o documento é congelado)."

// ── ignorar_veiculacao ──────────────────────────────────────────────────

func planejarIgnorar(ctx context.Context, E *Escritas, in entradaVeiculacao) (*contextoDaVeiculacao, error) {
	cx, err := lerContextoDaVeiculacao(ctx, E, in.VeiculacaoID)
	if err != nil {
		return nil, err
	}
	if cx.det.IgnoredAt != nil {
		return nil, errRecusa(codigoJaIgnorada, fmt.Sprintf(
			"a veiculação %s já está ignorada desde %s: nada a fazer; para fazê-la contar de novo, restaurar_veiculacao",
			cx.quem(), instanteSP(*cx.det.IgnoredAt)))
	}
	return cx, nil
}

func escritaIgnorarVeiculacao(cfg ConfigServidor) Escrita[entradaVeiculacao] {
	return Escrita[entradaVeiculacao]{
		Nome: "ignorar_veiculacao",
		Descricao: "Ignora (desconsidera) uma veiculação: ela sai do conjunto aprovado e deixa de contar em telas, relatórios e cobrança; o registro e o áudio ficam, e " +
			"restaurar_veiculacao a traz de volta. Use quando a tocada não deveria contar (duplicada, de outro comercial, que não foi ao ar de fato). A armadilha: " +
			"a categoria das OUTRAS veiculações da mesma célula-dia (campanha × tipo × emissora × dia em America/Sao_Paulo) pode mudar — se ela ocupava uma vaga " +
			"in_slot da cota, a próxima do dia assume a vaga (bonus vira in_slot) ou o déficit sobe. Veiculação já ignorada é recusada (ja_ignorada). " +
			"Um pedido por veiculação. " + fraseCritica,
		Nivel:  Critico,
		Motivo: "a vaga liberada pode promover outra veiculação do mesmo dia",
		Recurso: func(in entradaVeiculacao) (string, *uuid.UUID) {
			return veiculacaoDoRecurso(in.VeiculacaoID)
		},
		Tela: func(_ *Escritas, in entradaVeiculacao) *string { return linkDaVeiculacao(cfg, in.VeiculacaoID) },
		Preview: func(ctx context.Context, E *Escritas, in entradaVeiculacao) (previa, error) {
			cx, err := planejarIgnorar(ctx, E, in)
			if err != nil {
				return previa{}, err
			}
			cat := categoriaDe(cx.det.Category)
			pv := previa{
				Resumo:   fmt.Sprintf("Ignorar a veiculação %s (campanha %q, categoria %s)", cx.quem(), cx.campanha, cat),
				Mudancas: []mudanca{{Campo: "veiculação", De: fmt.Sprintf("conta (%s)", cat), Para: "ignorada"}},
			}
			if !cx.aprovada {
				pv.Ressalvas = append(pv.Ressalvas, fmt.Sprintf(
					"Esta veiculação já está fora do conjunto aprovado (%s): ignorar não muda nenhum número agora; fica registrado que foi ignorada (e, se um dia ela voltar ao conjunto, não contará). Célula-dia: %s.",
					cx.foraPorOutroMotivo, cx.celula()))
				return pv, nil
			}
			pv.Efeitos = append(pv.Efeitos,
				"Sai do conjunto aprovado: deixa de contar em /detections, /insights, /campaigns, nos relatórios e no que o cliente vê. O registro e o áudio ficam; é reversível por restaurar_veiculacao.")
			pv.Efeitos = append(pv.Efeitos, cx.efeitosComuns()...)
			pv.Efeitos = append(pv.Efeitos, efeitoDeSair(cx.det.Category))
			pv.Ressalvas = append(pv.Ressalvas, ressalvaPosVendaCongelado)
			return pv, nil
		},
		Executar: func(ctx context.Context, E *Escritas, p *Principal, in entradaVeiculacao) (feito, error) {
			cx, err := planejarIgnorar(ctx, E, in)
			if err != nil {
				return feito{}, err
			}
			if _, err := E.Acoes.IgnorarVeiculacao(ctx, cx.det.ID, p.UserID); err != nil {
				if errors.Is(err, acoes.ErrAlvoInexistente) {
					return feito{}, errNaoEncontrado("veiculação")
				}
				return feito{}, err
			}
			return veiculacaoFeita(ctx, cfg, E, cx.det.ID,
				"A célula-dia foi refechada: as outras veiculações do dia já têm a categoria nova. Reversível por restaurar_veiculacao."), nil
		},
	}
}

// ── restaurar_veiculacao ────────────────────────────────────────────────

func planejarRestaurar(ctx context.Context, E *Escritas, in entradaVeiculacao) (*contextoDaVeiculacao, error) {
	cx, err := lerContextoDaVeiculacao(ctx, E, in.VeiculacaoID)
	if err != nil {
		return nil, err
	}
	if cx.det.IgnoredAt == nil {
		return nil, errRecusa(codigoNaoIgnorada, fmt.Sprintf(
			"a veiculação %s não está ignorada: não há o que restaurar; para tirá-la da conta, ignorar_veiculacao", cx.quem()))
	}
	return cx, nil
}

func escritaRestaurarVeiculacao(cfg ConfigServidor) Escrita[entradaVeiculacao] {
	return Escrita[entradaVeiculacao]{
		Nome: "restaurar_veiculacao",
		Descricao: "Desfaz o ignorar: a veiculação volta ao conjunto aprovado e a contar em telas, relatórios e cobrança. A armadilha: ela volta a disputar a cota do " +
			"dia pelo horário em que tocou e pode REBAIXAR outra veiculação (uma in_slot que vira bonus) — o inverso do ignorar. Veiculação que não está ignorada " +
			"é recusada (nao_ignorada). Restaurar não desfaz retratação nem rejeição do audit: se a tocada está fora do conjunto por esse motivo, continua fora. " +
			"Um pedido por veiculação. " + fraseCritica,
		Nivel:  Critico,
		Motivo: "a veiculação volta a disputar a cota do dia e pode rebaixar outra",
		Recurso: func(in entradaVeiculacao) (string, *uuid.UUID) {
			return veiculacaoDoRecurso(in.VeiculacaoID)
		},
		Tela: func(_ *Escritas, in entradaVeiculacao) *string { return linkDaVeiculacao(cfg, in.VeiculacaoID) },
		Preview: func(ctx context.Context, E *Escritas, in entradaVeiculacao) (previa, error) {
			cx, err := planejarRestaurar(ctx, E, in)
			if err != nil {
				return previa{}, err
			}
			pv := previa{
				Resumo: fmt.Sprintf("Restaurar a veiculação %s (campanha %q)", cx.quem(), cx.campanha),
				Mudancas: []mudanca{{
					Campo: "veiculação", De: "ignorada desde " + instanteSP(*cx.det.IgnoredAt), Para: "conta de novo",
				}},
			}
			if cx.foraPorOutroMotivo != "" {
				pv.Ressalvas = append(pv.Ressalvas, fmt.Sprintf(
					"Esta veiculação continua fora do conjunto aprovado (%s): restaurar só tira a marca de ignorada, ela segue sem contar nem ocupar vaga.",
					cx.foraPorOutroMotivo))
				return pv, nil
			}
			pv.Efeitos = append(pv.Efeitos,
				"Volta ao conjunto aprovado: passa a contar em /detections, /insights, /campaigns, nos relatórios e no que o cliente vê.")
			pv.Efeitos = append(pv.Efeitos, cx.efeitosComuns()...)
			pv.Efeitos = append(pv.Efeitos, efeitoDeVoltar)
			pv.Ressalvas = append(pv.Ressalvas, ressalvaPosVendaCongelado)
			return pv, nil
		},
		Executar: func(ctx context.Context, E *Escritas, _ *Principal, in entradaVeiculacao) (feito, error) {
			cx, err := planejarRestaurar(ctx, E, in)
			if err != nil {
				return feito{}, err
			}
			if _, err := E.Acoes.RestaurarVeiculacao(ctx, cx.det.ID); err != nil {
				if errors.Is(err, acoes.ErrAlvoInexistente) {
					return feito{}, errNaoEncontrado("veiculação")
				}
				return feito{}, err
			}
			return veiculacaoFeita(ctx, cfg, E, cx.det.ID,
				"A célula-dia foi refechada: a veiculação retomou o lugar dela na cota e as outras do dia já têm a categoria nova. Reversível por ignorar_veiculacao."), nil
		},
	}
}
