package mcp

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"radiocheck/internal/catalog"
)

// Escritas do vínculo material ↔ campanha (Lote C, Task 15): vincular,
// desvincular e trocar as emissoras de um vínculo. NENHUMA escala (F3-4): a
// atribuição é decidida na hora da tocada (evidence/attribution.go) e nada
// recategoriza, então o passado não muda — o teste de prova de F3-4
// (TestVinculos_ProvaF34_PassadoNaoMuda) trava isso: grade, insights e
// veiculações de dias passados saem idênticos antes e depois. Se um dia esse
// teste quebrar, a escalada volta.

func registrarEscritasVinculos(s *sdk.Server, cfg ConfigServidor, p *Principal) {
	ferramentaEscrita(s, cfg, p, escritaVincularMaterial(cfg))
	ferramentaEscrita(s, cfg, p, escritaDesvincularMaterial(cfg))
	ferramentaEscrita(s, cfg, p, escritaEditarEmissorasMaterial(cfg))
}

// ── Contexto e helpers do vínculo ───────────────────────────────────────

// contextoDoVinculo: a campanha (não cancelada), o material e o vínculo atual
// (nil = ainda não vinculado). O cliente do material só é conferido ao CRIAR o
// vínculo (planejarVinculo): a rota deixou vincular material de outro cliente no
// passado, e desvincular/editar esse vínculo tem de continuar possível.
type contextoDoVinculo struct {
	camp    *catalog.Campaign
	mat     *catalog.Material
	vinculo *catalog.CampaignMaterial
}

func lerContextoDoVinculo(ctx context.Context, L *Leitura, campanhaID, materialID string) (*contextoDoVinculo, error) {
	camp, err := lerCampanhaParaEscrever(ctx, L, campanhaID)
	if err != nil {
		return nil, err
	}
	mid, err := uuidDaEntrada("material_id", materialID)
	if err != nil {
		return nil, err
	}
	mat, err := L.Materiais.Get(ctx, mid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNaoEncontrado("material")
	}
	if err != nil {
		return nil, err
	}
	vinculos, err := L.MateriaisCampanha.ListByCampaign(ctx, camp.ID)
	if err != nil {
		return nil, err
	}
	c := &contextoDoVinculo{camp: camp, mat: mat}
	for i := range vinculos {
		if vinculos[i].MaterialID == mat.ID {
			c.vinculo = &vinculos[i]
			break
		}
	}
	return c, nil
}

// exigeVinculo é o nao_encontrado de "esse material não está nessa campanha".
func (c *contextoDoVinculo) exigeVinculo() error {
	if c.vinculo == nil {
		return errNaoEncontrado(fmt.Sprintf("vínculo do material %q com a campanha %q", c.mat.Title, c.camp.Name))
	}
	return nil
}

// efeitoDeRecarga diz, pelo estado da campanha, o que acontece com a detecção
// quando o vínculo muda (a recarga só tem efeito em campanha ativa).
func efeitoDeRecarga(estado, oQueMuda string) string {
	switch estado {
	case "ativa":
		return "A campanha está ativa: o índice de hashes e os workers recarregam agora (index.reload) e " + oQueMuda + "."
	case "programada":
		return "A campanha está programada: o vínculo é gravado e " + oQueMuda + " quando ela ativar (data de início)."
	default:
		return "A campanha está " + estado + ": o vínculo é gravado, mas campanha " + estado + " não recebe veiculações novas."
	}
}

const efeitoNaoRecategoriza = "Não recategoriza nada: a atribuição é decidida na hora da tocada, então o que já foi veiculado não muda."

// saidaVinculo é o resultado de vincular e de trocar as emissoras: o vínculo no
// formato de listar_materiais_campanha.
type saidaVinculo struct {
	Campanha ref                `json:"campanha"`
	Vinculo  materialDaCampanha `json:"vinculo"`
}

// vinculoDepois relê o vínculo gravado e o põe no formato de
// listar_materiais_campanha (emissoras cortadas em 20, com o total real).
func vinculoDepois(ctx context.Context, L *Leitura, camp *catalog.Campaign, mat *catalog.Material) (*saidaVinculo, bool, error) {
	vinculos, err := L.MateriaisCampanha.ListByCampaign(ctx, camp.ID)
	if err != nil {
		return nil, false, err
	}
	i := slices.IndexFunc(vinculos, func(v catalog.CampaignMaterial) bool { return v.MaterialID == mat.ID })
	if i < 0 {
		// A escrita JÁ aconteceu: isto não é recusa de negócio (que liberaria o
		// token), é falha interna — o vínculo sumiu entre a escrita e a releitura.
		return nil, false, errors.New("mcp: o vínculo gravado não foi encontrado na releitura")
	}
	v := vinculos[i]
	tipos, err := nomesTipos(ctx, L)
	if err != nil {
		return nil, false, err
	}
	corte, total := cortarIDs(v.TargetStations, tetoEmissorasPorItem)
	nomes, err := nomesEmissoras(ctx, L, corte)
	if err != nil {
		return nil, false, err
	}
	linha := materialDaCampanha{
		Material: refDe(mat.ID, mat.Title), Emissoras: refs(corte, nomes), EmissorasTotal: total, VinculadoEm: instante(v.AddedAt),
	}
	if mat.TypeID != nil {
		r := refDe(*mat.TypeID, tipos[*mat.TypeID])
		linha.Tipo = &r
	}
	return &saidaVinculo{Campanha: refDe(camp.ID, camp.Name), Vinculo: linha}, total > len(corte), nil
}

// ── vincular_material ───────────────────────────────────────────────────

type entradaVincularMaterial struct {
	CampanhaID  string   `json:"campanha_id" jsonschema:"Id (uuid) da campanha, vindo de listar_campanhas. Nunca invente: pergunte."`
	MaterialID  string   `json:"material_id" jsonschema:"Id (uuid) do material (do MESMO cliente da campanha), vindo de listar_materiais. Nunca invente: pergunte."`
	EmissoraIDs []string `json:"emissora_ids,omitempty" jsonschema:"Emissoras em que o material pode tocar nesta campanha (até 500 ids, todas precisam ser da campanha). Omita para TODAS as emissoras da campanha — é o que a tela faz. Nunca invente ids: pergunte."`
	CampoConfirmacao
}

type planoDeVinculo struct {
	*contextoDoVinculo
	emissoras []uuid.UUID
	nomes     map[uuid.UUID]string
}

func planejarVinculo(ctx context.Context, E *Escritas, in entradaVincularMaterial) (*planoDeVinculo, error) {
	cv, err := lerContextoDoVinculo(ctx, E.Leitura, in.CampanhaID, in.MaterialID)
	if err != nil {
		return nil, err
	}
	// A rota deixa vincular material de qualquer cliente; o MCP não: o material
	// de outro cliente apareceria nas telas e relatórios desta campanha.
	if cv.mat.ClientID != cv.camp.ClientID {
		return nil, errEntrada(fmt.Sprintf(
			"o material %q é de outro cliente: só se vincula a uma campanha material do MESMO cliente dela", cv.mat.Title))
	}
	// Vincular de novo SOBRESCREVERIA a lista de emissoras do vínculo (a rota faz
	// upsert); a própria tela só revincula quando ainda não está na campanha.
	if cv.vinculo != nil {
		return nil, errEntrada(fmt.Sprintf(
			"o material %q já está vinculado a esta campanha (em %d emissora(s)): vincular de novo sobrescreveria a lista de emissoras; use editar_emissoras_material para mudá-la ou desvincular_material para tirá-lo",
			cv.mat.Title, len(cv.vinculo.TargetStations)))
	}
	emissoras, err := listaDeEmissoras("emissora_ids", in.EmissoraIDs)
	if err != nil {
		return nil, err
	}
	if len(emissoras) == 0 {
		// A tela manda todas as emissoras da campanha; lista vazia significaria "nenhuma"
		// (o material nunca seria detectado), que não é o que o modelo quer ao omitir.
		emissoras = slices.Clone(cv.camp.TargetStations)
	}
	if len(emissoras) == 0 {
		return nil, errEntrada("a campanha não tem emissoras-alvo: acrescente-as com editar_emissoras_campanha antes de vincular (um vínculo sem emissora nunca é detectado)")
	}
	var fora []string
	for _, id := range emissoras {
		if !slices.Contains(cv.camp.TargetStations, id) {
			fora = append(fora, id.String())
		}
	}
	if len(fora) > 0 {
		return nil, errEntrada(fmt.Sprintf("emissora(s) fora da campanha: %s (o vínculo só pode usar emissoras da própria campanha; acrescente-as antes com editar_emissoras_campanha)", strings.Join(fora, ", ")))
	}
	nomes, err := nomesEmissoras(ctx, E.Leitura, emissoras)
	if err != nil {
		return nil, err
	}
	return &planoDeVinculo{contextoDoVinculo: cv, emissoras: emissoras, nomes: nomes}, nil
}

func escritaVincularMaterial(cfg ConfigServidor) Escrita[entradaVincularMaterial] {
	return Escrita[entradaVincularMaterial]{
		Nome: "vincular_material",
		Descricao: "Vincula um material (áudio) a uma campanha, nas emissoras em que ele pode tocar (omita emissora_ids para todas as da " +
			"campanha, como a tela faz). O material precisa ser do mesmo cliente da campanha e ainda não estar vinculado (para mudar as " +
			"emissoras de um vínculo existente use editar_emissoras_material). Numa campanha ativa o índice e os workers recarregam agora. " +
			"As armadilhas: material sem tipo não é coberto por nenhuma regra de distribuição, e material sem fingerprint pronto não é " +
			"detectado. Não recategoriza nada e não sobe de nível: o que já foi veiculado não muda. Campanha cancelada é recusada. " +
			"Nunca invente ids: pergunte. Mostre o preview e espere a pessoa aprovar antes de confirmar.",
		Nivel: Confirma,
		Recurso: func(in entradaVincularMaterial) (string, *uuid.UUID) {
			return campanhaDoRecurso("campaign_material", in.CampanhaID)
		},
		Tela: func(_ *Escritas, in entradaVincularMaterial) *string { return linkDaCampanha(cfg, in.CampanhaID) },
		Preview: func(ctx context.Context, E *Escritas, in entradaVincularMaterial) (previa, error) {
			pl, err := planejarVinculo(ctx, E, in)
			if err != nil {
				return previa{}, err
			}
			pv := previa{
				Resumo: fmt.Sprintf("Vincular o material %q à campanha %q", pl.mat.Title, pl.camp.Name),
				Mudancas: []mudanca{
					{Campo: "vínculo", Para: fmt.Sprintf("%s → %s", pl.mat.Title, pl.camp.Name)},
					{Campo: "emissoras do vínculo", Para: resumoDeEmissoras(pl.emissoras, pl.nomes)},
				},
				Efeitos: []string{efeitoDeRecarga(pl.camp.Status, "o material passa a ser detectado nas emissoras do vínculo"), efeitoNaoRecategoriza},
			}
			if pl.mat.TypeID == nil {
				pv.Ressalvas = append(pv.Ressalvas, "O material não tem tipo: nenhuma regra de distribuição o cobre (a distribuição é por tipo). Defina o tipo com editar_material.")
			}
			if pl.mat.FingerprintStatus != "ready" {
				pv.Ressalvas = append(pv.Ressalvas, fmt.Sprintf("O fingerprint do material não está pronto (%s): ele só será detectado depois que ficar pronto.", pl.mat.FingerprintStatus))
			}
			return pv, nil
		},
		Executar: func(ctx context.Context, E *Escritas, _ *Principal, in entradaVincularMaterial) (feito, error) {
			pl, err := planejarVinculo(ctx, E, in)
			if err != nil {
				return feito{}, err
			}
			if err := E.Acoes.VincularMaterial(ctx, pl.camp.ID, pl.mat.ID, pl.emissoras); err != nil {
				return feito{}, err
			}
			saida, cortada, err := vinculoDepois(ctx, E.Leitura, pl.camp, pl.mat)
			if err != nil {
				return feito{}, err
			}
			f := feito{Resultado: saida, Recurso: "campaign_material", RecursoID: &pl.camp.ID}
			if cortada {
				f.Ressalvas = append(f.Ressalvas, ressalvaEmissorasCortadas)
			}
			return f, nil
		},
	}
}

// ── desvincular_material ────────────────────────────────────────────────

type entradaDesvincularMaterial struct {
	CampanhaID string `json:"campanha_id" jsonschema:"Id (uuid) da campanha, vindo de listar_campanhas. Nunca invente: pergunte."`
	MaterialID string `json:"material_id" jsonschema:"Id (uuid) do material vinculado, vindo de listar_materiais_campanha. Nunca invente: pergunte."`
	CampoConfirmacao
}

type saidaDesvinculo struct {
	Campanha           ref  `json:"campanha"`
	Material           ref  `json:"material"`
	Desvinculado       bool `json:"desvinculado"`
	MateriaisRestantes int  `json:"materiais_restantes"`
}

// ressalvasDoDesvinculo: o que a retirada deixa para trás — regras escopadas a
// este material e tipo sem nenhum outro material vinculado (as regras seguem
// cobrando inserções que nada vai cumprir).
func ressalvasDoDesvinculo(ctx context.Context, E *Escritas, cv *contextoDoVinculo) ([]string, error) {
	regras, err := E.Leitura.Regras.ListByCampaign(ctx, cv.camp.ID)
	if err != nil {
		return nil, err
	}
	// Regra que já terminou não cobra mais nada: só as vigentes ou futuras importam.
	hoje := hojeSP(E.agora())
	regras = slices.DeleteFunc(regras, func(r catalog.DistributionRule) bool { return r.EndDate.Before(hoje) })
	var out []string
	for _, r := range regras {
		if slices.Contains(r.MaterialIDs, cv.mat.ID) {
			nome := r.Name
			if nome == "" {
				nome = r.ID.String()
			}
			out = append(out, fmt.Sprintf("A regra de distribuição %q é escopada a este material e continua existindo: sem o vínculo ela fica sem material para tocar.", nome))
		}
	}
	if cv.mat.TypeID == nil {
		return out, nil
	}
	vinculos, err := E.Leitura.MateriaisCampanha.ListByCampaign(ctx, cv.camp.ID)
	if err != nil {
		return nil, err
	}
	doCliente, err := E.Leitura.Materiais.ListByClient(ctx, cv.camp.ClientID, "")
	if err != nil {
		return nil, err
	}
	tipoDe := make(map[uuid.UUID]*uuid.UUID, len(doCliente))
	for _, m := range doCliente {
		tipoDe[m.ID] = m.TypeID
	}
	for _, v := range vinculos {
		if v.MaterialID == cv.mat.ID {
			continue
		}
		if t := tipoDe[v.MaterialID]; t != nil && *t == *cv.mat.TypeID {
			return out, nil // há outro material do mesmo tipo: as regras do tipo seguem cumpríveis
		}
	}
	var daTipo int
	for _, r := range regras {
		if r.TypeID == *cv.mat.TypeID {
			daTipo++
		}
	}
	if daTipo > 0 {
		out = append(out, fmt.Sprintf("Este é o único material do seu tipo vinculado à campanha: as %d regra(s) de distribuição desse tipo seguem cobrando inserções que nada vai cumprir (o déficit dos próximos dias cresce) até outro material do tipo ser vinculado.", daTipo))
	}
	return out, nil
}

func escritaDesvincularMaterial(cfg ConfigServidor) Escrita[entradaDesvincularMaterial] {
	return Escrita[entradaDesvincularMaterial]{
		Nome: "desvincular_material",
		Descricao: "Tira um material de uma campanha (remove o vínculo). Numa campanha ativa o índice e os workers recarregam agora e o material " +
			"deixa de ser detectado nela. As veiculações já registradas continuam atribuídas à campanha — não recategoriza nada e não sobe de " +
			"nível. As armadilhas: regras de distribuição escopadas a este material, ou do tipo dele quando era o único do tipo, ficam cobrando " +
			"inserções que nada cumpre; e lançar veiculação manual deste material na campanha deixa de ser possível. Vínculo que não existe é " +
			"recusado. Campanha cancelada é recusada. Nunca invente ids: pergunte. Mostre o preview e espere a pessoa aprovar antes de confirmar.",
		Nivel: Confirma,
		Recurso: func(in entradaDesvincularMaterial) (string, *uuid.UUID) {
			return campanhaDoRecurso("campaign_material", in.CampanhaID)
		},
		Tela: func(_ *Escritas, in entradaDesvincularMaterial) *string { return linkDaCampanha(cfg, in.CampanhaID) },
		Preview: func(ctx context.Context, E *Escritas, in entradaDesvincularMaterial) (previa, error) {
			cv, err := lerContextoDoVinculo(ctx, E.Leitura, in.CampanhaID, in.MaterialID)
			if err != nil {
				return previa{}, err
			}
			if err := cv.exigeVinculo(); err != nil {
				return previa{}, err
			}
			pv := previa{
				Resumo:   fmt.Sprintf("Tirar o material %q da campanha %q", cv.mat.Title, cv.camp.Name),
				Mudancas: []mudanca{{Campo: "vínculo", De: fmt.Sprintf("%s → %s (%d emissora(s))", cv.mat.Title, cv.camp.Name, len(cv.vinculo.TargetStations))}},
				Efeitos: []string{
					efeitoDeRecarga(cv.camp.Status, "o material deixa de ser detectado nesta campanha"),
					"As veiculações já registradas continuam atribuídas à campanha: o passado não muda.",
					"Veiculação manual deste material nesta campanha deixa de ser possível (ela exige o vínculo).",
				},
			}
			pv.Ressalvas, err = ressalvasDoDesvinculo(ctx, E, cv)
			if err != nil {
				return previa{}, err
			}
			return pv, nil
		},
		Executar: func(ctx context.Context, E *Escritas, _ *Principal, in entradaDesvincularMaterial) (feito, error) {
			cv, err := lerContextoDoVinculo(ctx, E.Leitura, in.CampanhaID, in.MaterialID)
			if err != nil {
				return feito{}, err
			}
			if err := cv.exigeVinculo(); err != nil {
				return feito{}, err
			}
			if err := E.Acoes.DesvincularMaterial(ctx, cv.camp.ID, cv.mat.ID); err != nil {
				return feito{}, err
			}
			restantes, err := E.Leitura.MateriaisCampanha.ListByCampaign(ctx, cv.camp.ID)
			if err != nil {
				return feito{}, err
			}
			return feito{
				Resultado: saidaDesvinculo{Campanha: refDe(cv.camp.ID, cv.camp.Name), Material: refDe(cv.mat.ID, cv.mat.Title),
					Desvinculado: true, MateriaisRestantes: len(restantes)},
				Recurso: "campaign_material", RecursoID: &cv.camp.ID,
			}, nil
		},
	}
}

// ── editar_emissoras_material ───────────────────────────────────────────

type entradaEditarEmissorasMaterial struct {
	CampanhaID string   `json:"campanha_id" jsonschema:"Id (uuid) da campanha, vindo de listar_campanhas. Nunca invente: pergunte."`
	MaterialID string   `json:"material_id" jsonschema:"Id (uuid) do material já vinculado, vindo de listar_materiais_campanha. Nunca invente: pergunte."`
	Adicionar  []string `json:"adicionar,omitempty" jsonschema:"Emissoras a ACRESCENTAR ao vínculo (até 500 ids; precisam ser da campanha). Nunca invente ids: pergunte."`
	Remover    []string `json:"remover,omitempty" jsonschema:"Emissoras a TIRAR do vínculo (até 500 ids de listar_materiais_campanha). Só as listadas saem."`
	CampoConfirmacao
}

type planoDeEmissorasDoVinculo struct {
	*contextoDoVinculo
	adicionar, remover []uuid.UUID
	nova               []uuid.UUID
	nomes              map[uuid.UUID]string
}

func planejarEmissorasDoVinculo(ctx context.Context, E *Escritas, in entradaEditarEmissorasMaterial) (*planoDeEmissorasDoVinculo, error) {
	if len(in.Adicionar) == 0 && len(in.Remover) == 0 {
		return nil, errEntrada("informe ao menos uma emissora em adicionar ou remover")
	}
	cv, err := lerContextoDoVinculo(ctx, E.Leitura, in.CampanhaID, in.MaterialID)
	if err != nil {
		return nil, err
	}
	if err := cv.exigeVinculo(); err != nil {
		return nil, err
	}
	add, err := listaDeEmissoras("adicionar", in.Adicionar)
	if err != nil {
		return nil, err
	}
	rem, err := listaDeEmissoras("remover", in.Remover)
	if err != nil {
		return nil, err
	}
	atuais := cv.vinculo.TargetStations
	for _, id := range add {
		switch {
		case slices.Contains(rem, id):
			return nil, errEntrada(fmt.Sprintf("a emissora %s está em adicionar e em remover ao mesmo tempo", id))
		case slices.Contains(atuais, id):
			return nil, errEntrada(fmt.Sprintf("a emissora %s já está no vínculo", id))
		case !slices.Contains(cv.camp.TargetStations, id):
			return nil, errEntrada(fmt.Sprintf("a emissora %s não é da campanha: acrescente-a antes com editar_emissoras_campanha", id))
		}
	}
	for _, id := range rem {
		if !slices.Contains(atuais, id) {
			return nil, errEntrada(fmt.Sprintf("a emissora %s não está no vínculo, então não há o que remover (confira com listar_materiais_campanha)", id))
		}
	}
	nova := make([]uuid.UUID, 0, len(atuais)+len(add))
	for _, id := range atuais {
		if !slices.Contains(rem, id) {
			nova = append(nova, id)
		}
	}
	nova = append(nova, add...)
	if len(nova) == 0 {
		return nil, errEntrada("o material ficaria sem nenhuma emissora no vínculo (nunca seria detectado); para tirá-lo da campanha use desvincular_material")
	}
	nomes, err := nomesEmissoras(ctx, E.Leitura, slices.Concat(add, rem))
	if err != nil {
		return nil, err
	}
	return &planoDeEmissorasDoVinculo{contextoDoVinculo: cv, adicionar: add, remover: rem, nova: nova, nomes: nomes}, nil
}

func escritaEditarEmissorasMaterial(cfg ConfigServidor) Escrita[entradaEditarEmissorasMaterial] {
	return Escrita[entradaEditarEmissorasMaterial]{
		Nome: "editar_emissoras_material",
		Descricao: "Acrescenta e/ou tira emissoras do vínculo de um material já vinculado a uma campanha: adicionar e remover são listas de " +
			"ids, e só as listadas mudam (a tela troca a lista inteira; aqui nunca). É a lista que decide em que emissoras uma tocada do " +
			"material é atribuída à campanha — emissora acrescentada à campanha NÃO entra nos vínculos sozinha, e é aqui que se inclui. " +
			"Numa campanha ativa o índice e os workers recarregam agora. Não recategoriza nada e não sobe de nível: veiculações já registradas " +
			"não mudam; as futuras nas emissoras removidas deixam de ser atribuídas à campanha. Não deixa o vínculo sem emissora (para tirar " +
			"o material use desvincular_material). Campanha cancelada é recusada. Nunca invente ids: pergunte. Mostre o preview e espere a " +
			"pessoa aprovar antes de confirmar.",
		Nivel: Confirma,
		Recurso: func(in entradaEditarEmissorasMaterial) (string, *uuid.UUID) {
			return campanhaDoRecurso("campaign_material", in.CampanhaID)
		},
		Tela: func(_ *Escritas, in entradaEditarEmissorasMaterial) *string {
			return linkDaCampanha(cfg, in.CampanhaID)
		},
		Preview: func(ctx context.Context, E *Escritas, in entradaEditarEmissorasMaterial) (previa, error) {
			pl, err := planejarEmissorasDoVinculo(ctx, E, in)
			if err != nil {
				return previa{}, err
			}
			var mud []mudanca
			if len(pl.adicionar) > 0 {
				mud = append(mud, mudanca{Campo: "emissoras acrescentadas ao vínculo", Para: resumoDeEmissoras(pl.adicionar, pl.nomes)})
			}
			if len(pl.remover) > 0 {
				mud = append(mud, mudanca{Campo: "emissoras tiradas do vínculo", De: resumoDeEmissoras(pl.remover, pl.nomes)})
			}
			mud = append(mud, mudanca{Campo: "emissoras do vínculo (total)", De: len(pl.vinculo.TargetStations), Para: len(pl.nova)})
			pv := previa{
				Resumo:   fmt.Sprintf("Material %q na campanha %q: acrescentar %d e tirar %d emissora(s) do vínculo", pl.mat.Title, pl.camp.Name, len(pl.adicionar), len(pl.remover)),
				Mudancas: mud,
				Efeitos:  []string{efeitoDeRecarga(pl.camp.Status, "a nova lista de emissoras passa a valer na detecção"), efeitoNaoRecategoriza},
			}
			if len(pl.remover) > 0 {
				pv.Efeitos = append(pv.Efeitos,
					"Tocadas futuras deste material nas emissoras tiradas deixam de ser atribuídas a esta campanha; veiculações já registradas continuam como estão.",
					"Veiculação manual nessas emissoras deixa de ser possível (ela exige a emissora no vínculo).")
			}
			return pv, nil
		},
		Executar: func(ctx context.Context, E *Escritas, _ *Principal, in entradaEditarEmissorasMaterial) (feito, error) {
			pl, err := planejarEmissorasDoVinculo(ctx, E, in)
			if err != nil {
				return feito{}, err
			}
			if err := E.Acoes.TrocarEmissorasMaterial(ctx, pl.camp.ID, pl.mat.ID, pl.nova); err != nil {
				return feito{}, err
			}
			saida, cortada, err := vinculoDepois(ctx, E.Leitura, pl.camp, pl.mat)
			if err != nil {
				return feito{}, err
			}
			f := feito{Resultado: saida, Recurso: "campaign_material", RecursoID: &pl.camp.ID}
			if cortada {
				f.Ressalvas = append(f.Ressalvas, ressalvaEmissorasCortadas)
			}
			return f, nil
		},
	}
}
