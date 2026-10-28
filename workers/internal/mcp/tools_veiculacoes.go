package mcp

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"radiocheck/internal/catalog"
	"radiocheck/internal/categorizer"
)

type veiculacaoDTO struct {
	ID        string  `json:"id"`
	Quando    string  `json:"quando"`
	Emissora  ref     `json:"emissora"`
	Material  ref     `json:"material"`
	Tipo      *string `json:"tipo"`
	Campanha  ref     `json:"campanha"`
	Cliente   *ref    `json:"cliente"`
	Categoria string  `json:"categoria"`
	Manual    bool    `json:"manual"`
}

func veiculacaoDe(d catalog.DetectionEnriched) veiculacaoDTO {
	v := veiculacaoDTO{
		ID: d.ID.String(), Quando: instante(d.DetectedAt), Emissora: refDe(d.StationID, d.StationName),
		Material: refDe(d.CommercialID, d.CommercialName), Tipo: d.MaterialTypeName,
		Campanha: ref{ID: d.CampaignID.String()}, Categoria: categoriaDe(d.Category), Manual: d.ManualAt != nil,
	}
	if d.CampaignName != nil {
		v.Campanha.Nome = *d.CampaignName
	}
	if d.ClientID != nil {
		r := ref{ID: d.ClientID.String()}
		if d.ClientName != nil {
			r.Nome = *d.ClientName
		}
		v.Cliente = &r
	}
	return v
}

type entradaListarVeiculacoes struct {
	CampanhaIDs         []string `json:"campanha_ids,omitempty" jsonschema:"Até 50 campanhas (ids de listar_campanhas). Passe campanhas ou o período de/ate."`
	EmissoraID          string   `json:"emissora_id,omitempty" jsonschema:"Só desta emissora."`
	MaterialID          string   `json:"material_id,omitempty" jsonschema:"Só deste material."`
	Categorias          []string `json:"categorias,omitempty" jsonschema:"in_slot, bonus, out_slot, out_date. Vazio = todas."`
	De                  string   `json:"de,omitempty" jsonschema:"Primeiro dia (AAAA-MM-DD, America/Sao_Paulo)."`
	Ate                 string   `json:"ate,omitempty" jsonschema:"Último dia (AAAA-MM-DD, America/Sao_Paulo)."`
	Busca               string   `json:"busca,omitempty" jsonschema:"Até 4 palavras; cada uma tem de aparecer em emissora, cidade, UF, banda, dial, material, tipo ou cliente."`
	MaisAntigasPrimeiro bool     `json:"mais_antigas_primeiro,omitempty" jsonschema:"true para ordem cronológica (padrão: mais recentes primeiro)."`
	Paginacao
}

type entradaVeiculacao struct {
	VeiculacaoID string `json:"veiculacao_id" jsonschema:"Id (uuid) da veiculação, vindo de listar_veiculacoes ou plano_do_dia. Nunca invente: pergunte."`
}

type atribuicaoDTO struct {
	Campanha  ref    `json:"campanha"`
	Material  ref    `json:"material"`
	Categoria string `json:"categoria"`
}

type evidenciaDTO struct {
	Status       string `json:"status"`
	TamanhoBytes *int64 `json:"tamanho_bytes"`
}

type manualDTO struct {
	Em   string  `json:"em"`
	Por  *string `json:"por"`
	Nota *string `json:"nota"`
}

type linksVeiculacao struct {
	Ouvir *string `json:"ouvir"`
}

type saidaObterVeiculacao struct {
	ID                string          `json:"id"`
	Quando            string          `json:"quando"`
	Emissora          ref             `json:"emissora"`
	Material          ref             `json:"material"`
	Campanha          ref             `json:"campanha"`
	Categoria         string          `json:"categoria"`
	Aprovada          bool            `json:"aprovada"`
	Atribuicoes       []atribuicaoDTO `json:"atribuicoes"`
	Confianca         float64         `json:"confianca"`
	CoberturaTemporal *float64        `json:"cobertura_temporal"`
	CoberturaAudit    *float64        `json:"cobertura_audit"`
	Hashes            int32           `json:"hashes"`
	Evidencia         evidenciaDTO    `json:"evidencia"`
	Manual            *manualDTO      `json:"manual"`
	IgnoradaEm        *string         `json:"ignorada_em"`
	RetratadaEm       *string         `json:"retratada_em"`
	Roteiro           *string         `json:"roteiro"`
	Links             linksVeiculacao `json:"links"`
	Ressalvas         []string        `json:"ressalvas,omitempty"`
}

type entradaPorMaterial struct {
	CampanhaIDs []string `json:"campanha_ids" jsonschema:"De 1 a 50 campanhas (ids de listar_campanhas). Nunca invente: pergunte."`
	De          string   `json:"de,omitempty" jsonschema:"Primeiro dia (AAAA-MM-DD, America/Sao_Paulo)."`
	Ate         string   `json:"ate,omitempty" jsonschema:"Último dia (AAAA-MM-DD, America/Sao_Paulo)."`
	Busca       string   `json:"busca,omitempty" jsonschema:"Até 4 palavras, como em listar_veiculacoes."`
	Paginacao
}

type linhaPorMaterial struct {
	Material    ref      `json:"material"`
	ShortID     *int32   `json:"short_id"`
	DuracaoS    *float64 `json:"duracao_s"`
	Tipo        *ref     `json:"tipo"`
	Veiculacoes int      `json:"veiculacoes"`
}

type saidaPorMaterial struct {
	lista[linhaPorMaterial]
	TotalVeiculacoes   int `json:"total_veiculacoes"`
	MateriaisDistintos int `json:"materiais_distintos"`
}

const tetoCampanhasPorConsulta = 50

var categoriasValidas = []string{categorizer.CatInSlot, categorizer.CatBonus, categorizer.CatOutSlot, categorizer.CatOutDate}

const ressalvaVeiculacoes = "Só veiculações aprovadas (fora as retratadas, ignoradas e rejeitadas pelo audit). Uma tocada atribuída a N campanhas aparece N vezes, uma por campanha, cada uma com a própria categoria."

// lerCampanhas valida 0..teto ids; vazio devolve nil (sem filtro).
func lerCampanhas(ss []string) ([]uuid.UUID, bool) {
	if len(ss) > tetoCampanhasPorConsulta {
		return nil, false
	}
	var out []uuid.UUID
	for _, s := range ss {
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, false
		}
		out = append(out, id)
	}
	return out, true
}

// lerPeriodoSP converte de/ate (dias de SP) em instantes; vazio = sem limite.
func lerPeriodoSP(de, ate string) (*time.Time, *time.Time, string) {
	var ini, fim *time.Time
	if de != "" {
		d, ok := lerDia(de)
		if !ok {
			return nil, nil, "de"
		}
		i, _ := limitesDoDiaSP(d)
		ini = &i
	}
	if ate != "" {
		d, ok := lerDia(ate)
		if !ok {
			return nil, nil, "ate"
		}
		_, f := limitesDoDiaSP(d)
		fim = &f
	}
	return ini, fim, ""
}

func registrarVeiculacoes(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "listar_veiculacoes",
		"Lista veiculações (tocadas) como o relatório Data e Hora: quando, emissora, material, tipo, campanha, cliente e "+
			"categoria (in_slot, bonus, out_slot, out_date). Filtros: campanha_ids, emissora_id, material_id, categorias, "+
			"de/ate (dias de America/Sao_Paulo), busca. Pede campanhas ou período. A armadilha: só conta o conjunto aprovado, "+
			"e uma tocada com várias campanhas aparece uma vez por campanha. Lista paginada com total real.",
		func(ctx context.Context, L *Leitura, in entradaListarVeiculacoes) (*sdk.CallToolResult, error) {
			camps, okC := lerCampanhas(in.CampanhaIDs)
			if !okC {
				return recusa("campanha_ids: até 50 ids válidos", "entrada_invalida")
			}
			ini, fim, campoRuim := lerPeriodoSP(in.De, in.Ate)
			if campoRuim != "" {
				return recusaEntrada(campoRuim)
			}
			if len(camps) == 0 && (ini == nil || fim == nil) {
				return recusa("passe campanha_ids ou o período completo (de e ate)", "entrada_invalida")
			}
			f := catalog.ListPagedFilter{CampaignIDs: camps, StartDate: ini, EndDate: fim, Q: in.Busca}
			if in.MaisAntigasPrimeiro {
				f.Sort = "detected_at_asc"
			}
			if in.EmissoraID != "" {
				id, err := uuid.Parse(in.EmissoraID)
				if err != nil {
					return recusaEntrada("emissora_id")
				}
				f.StationID = &id
			}
			if in.MaterialID != "" {
				id, err := uuid.Parse(in.MaterialID)
				if err != nil {
					return recusaEntrada("material_id")
				}
				f.MaterialID = &id
			}
			for _, c := range in.Categorias {
				if !slices.Contains(categoriasValidas, c) {
					return recusa(fmt.Sprintf("categoria desconhecida %q: use in_slot, bonus, out_slot ou out_date", c), "entrada_invalida")
				}
				f.Categories = append(f.Categories, c)
				if c == categorizer.CatBonus {
					f.Categories = append(f.Categories, categorizer.CatOrphan) // nome antigo de bonus
				}
			}
			pg, err := buscarJanela(in.Paginacao, func(page, size int) ([]catalog.DetectionEnriched, int, error) {
				f.Page, f.PageSize = page, size
				res, err := L.Veiculacoes.ListPaged(ctx, f)
				if err != nil {
					return nil, 0, err
				}
				return res.Data, res.Total, nil
			})
			if err != nil {
				return nil, err
			}
			out := mapear(pg, veiculacaoDe)
			out.Ressalvas = []string{ressalvaVeiculacoes}
			return ok(out)
		})

	ferramentaLeitura(s, cfg, "obter_veiculacao",
		"Devolve uma veiculação: quando, emissora, material, a campanha e a categoria canônicas, TODAS as campanhas a que ela "+
			"conta (atribuicoes, cada uma com a própria categoria), se está no conjunto aprovado, confiança e cobertura da "+
			"detecção, estado da evidência, se foi lançada à mão, e o link para ouvir no portal (o áudio nunca vem aqui). "+
			"Nunca invente o veiculacao_id.",
		func(ctx context.Context, L *Leitura, in entradaVeiculacao) (*sdk.CallToolResult, error) {
			id, err := uuid.Parse(in.VeiculacaoID)
			if err != nil {
				return recusaEntrada("veiculacao_id")
			}
			d, err := L.Veiculacoes.Get(ctx, id)
			if errors.Is(err, pgx.ErrNoRows) {
				return naoEncontrado("veiculação")
			}
			if err != nil {
				return nil, err
			}
			atribs, err := L.Veiculacoes.Attributions(ctx, id, d.DetectedAt) // detected_at poda as partições
			if err != nil {
				return nil, err
			}
			aprovada, err := L.Veiculacoes.Approved(ctx, id)
			if err != nil {
				return nil, err
			}
			out := saidaObterVeiculacao{
				ID: d.ID.String(), Quando: instante(d.DetectedAt), Emissora: refDe(d.StationID, d.StationName),
				Material: refDe(d.CommercialID, d.CommercialName), Campanha: ref{ID: d.CampaignID.String()},
				Categoria: categoriaDe(d.Category), Aprovada: aprovada, Atribuicoes: make([]atribuicaoDTO, 0, len(atribs)),
				Confianca: d.Confidence, CoberturaTemporal: d.TemporalCoverage, CoberturaAudit: d.AuditCoverage,
				Hashes: d.HashCount, Evidencia: evidenciaDTO{Status: d.EvidenceStatus, TamanhoBytes: d.EvidenceSizeBytes},
				IgnoradaEm: instanteOpc(d.IgnoredAt), RetratadaEm: instanteOpc(d.RetractedAt), Roteiro: d.CommercialScript,
				Links: linksVeiculacao{Ouvir: cfg.link("/detections/" + d.ID.String())},
			}
			for _, a := range atribs {
				if a.CampaignID == d.CampaignID {
					out.Campanha.Nome = a.CampaignName
				}
				out.Atribuicoes = append(out.Atribuicoes, atribuicaoDTO{
					Campanha: refDe(a.CampaignID, a.CampaignName), Material: refDe(a.CommercialID, a.CommercialName),
					Categoria: categoriaDe(a.Category),
				})
			}
			if d.ManualAt != nil {
				m := &manualDTO{Em: instante(*d.ManualAt), Nota: d.ManualNote}
				if d.ManualBy != nil {
					s := d.ManualBy.String()
					m.Por = &s
				}
				out.Manual = m
			}
			if !aprovada {
				out.Ressalvas = append(out.Ressalvas,
					"Esta veiculação está FORA do conjunto aprovado (ignorada, retratada ou rejeitada pelo audit): não conta em nenhuma tela nem relatório.")
			}
			return ok(out)
		})

	ferramentaLeitura(s, cfg, "veiculacoes_por_material",
		"Conta as veiculações por material (as mesmas do relatório Data e Hora, conjunto aprovado) em 1 a 50 campanhas, com "+
			"filtro opcional de período (dias de America/Sao_Paulo) e busca. Devolve cada material com a contagem, mais o total "+
			"e quantos materiais distintos tocaram. Nunca some você: o total vem pronto. Lista paginada.",
		func(ctx context.Context, L *Leitura, in entradaPorMaterial) (*sdk.CallToolResult, error) {
			camps, okC := lerCampanhas(in.CampanhaIDs)
			if !okC || len(camps) == 0 {
				return recusa("campanha_ids: de 1 a 50 ids válidos", "entrada_invalida")
			}
			ini, fim, campoRuim := lerPeriodoSP(in.De, in.Ate)
			if campoRuim != "" {
				return recusaEntrada(campoRuim)
			}
			res, err := L.Veiculacoes.AggregateByMaterial(ctx, catalog.AggregateFilter{
				CampaignIDs: camps, StartDate: ini, EndDate: fim, Q: in.Busca,
			})
			if err != nil {
				return nil, err
			}
			linhas := make([]linhaPorMaterial, len(res.Data))
			for i, r := range res.Data {
				linhas[i] = linhaPorMaterial{
					Material: refDe(r.MaterialID, r.MaterialTitle), ShortID: r.MaterialShortID,
					DuracaoS: r.MaterialDurationSec, Veiculacoes: r.Count,
				}
				if r.MaterialTypeID != nil {
					t := ref{ID: r.MaterialTypeID.String()}
					if r.MaterialTypeName != nil {
						t.Nome = *r.MaterialTypeName
					}
					linhas[i].Tipo = &t
				}
			}
			out := saidaPorMaterial{lista: paginar(linhas, in.Paginacao),
				TotalVeiculacoes: res.TotalDetections, MateriaisDistintos: res.DistinctMaterials}
			out.Ressalvas = []string{ressalvaVeiculacoes}
			return ok(out)
		})
}
