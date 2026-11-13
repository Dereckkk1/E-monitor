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

type contratoDTO struct {
	Campanhas int     `json:"campanhas"`
	NoAr      bool    `json:"no_ar"`
	ComecaEm  *string `json:"comeca_em"`
}

// emissoraDTO: whitelist — stream_url, saúde do stream e threshold nunca saem.
type emissoraDTO struct {
	ID            string       `json:"id"`
	ShortID       int32        `json:"short_id"`
	Nome          string       `json:"nome"`
	Banda         string       `json:"banda"`
	FrequenciaMHz *float64     `json:"frequencia_mhz"`
	Cidade        *string      `json:"cidade"`
	UF            *string      `json:"uf"`
	PMM           *float64     `json:"pmm"`
	Latitude      *float64     `json:"latitude"`
	Longitude     *float64     `json:"longitude"`
	Contrato      *contratoDTO `json:"contrato"`
}

func emissoraDe(s catalog.Station) emissoraDTO {
	d := emissoraDTO{
		ID: s.ID.String(), ShortID: s.ShortID, Nome: s.Name, Banda: s.Band, FrequenciaMHz: s.FrequencyMHz,
		Cidade: s.City, UF: s.State, PMM: s.PMM, Latitude: s.Latitude, Longitude: s.Longitude,
	}
	if s.Contract != nil {
		d.Contrato = &contratoDTO{Campanhas: s.Contract.Campaigns, NoAr: s.Contract.OnAir, ComecaEm: diaOpc(s.Contract.StartsAt)}
	}
	return d
}

// perfilAudienciaDTO traduz catalog.AudienceProfile (chaves em inglês no
// metadata) para o vocabulário do MCP. Percentuais de 0 a 100, como na fonte;
// bloco que a emissora não tem vem null (não zero).
type perfilAudienciaDTO struct {
	Genero        *generoDTO        `json:"genero"`
	FaixasEtarias *faixasEtariasDTO `json:"faixas_etarias"`
	ClasseSocial  *classeSocialDTO  `json:"classe_social"`
}

type generoDTO struct {
	Masculino float64 `json:"masculino"`
	Feminino  float64 `json:"feminino"`
}

type faixasEtariasDTO struct {
	De18a24  float64 `json:"de_18_a_24"`
	De25a49  float64 `json:"de_25_a_49"`
	De50Mais float64 `json:"de_50_mais"`
}

type classeSocialDTO struct {
	AB float64 `json:"ab"`
	C  float64 `json:"c"`
	DE float64 `json:"de"`
}

func perfilAudienciaDe(p *catalog.AudienceProfile) *perfilAudienciaDTO {
	if p == nil {
		return nil
	}
	d := &perfilAudienciaDTO{}
	if p.Gender != nil {
		d.Genero = &generoDTO{Masculino: p.Gender.Male, Feminino: p.Gender.Female}
	}
	if p.AgeRanges != nil {
		d.FaixasEtarias = &faixasEtariasDTO{De18a24: p.AgeRanges.Range18To24, De25a49: p.AgeRanges.Range25To49, De50Mais: p.AgeRanges.Range50Plus}
	}
	if p.SocialClass != nil {
		d.ClasseSocial = &classeSocialDTO{AB: p.SocialClass.ClasseAB, C: p.SocialClass.ClasseC, DE: p.SocialClass.ClasseDE}
	}
	return d
}

type perfilEmissora struct {
	Categorias      []string            `json:"categorias"`
	PerfilAudiencia *perfilAudienciaDTO `json:"perfil_audiencia"`
	PopulacaoTotal  *int64              `json:"populacao_total"`
	Site            *string             `json:"site"`
	AnoFundacao     *int                `json:"ano_fundacao"`
	RazaoSocial     *string             `json:"razao_social"`
	NomeFantasia    *string             `json:"nome_fantasia"`
	CNPJ            *string             `json:"cnpj"`
	EmailComercial  *string             `json:"email_comercial"`
}

type anatelDTO struct {
	Classe              *string  `json:"classe"`
	ContornoProtegidoKm *float64 `json:"contorno_protegido_km"`
	AlcanceKm           *float64 `json:"alcance_km"`
	Procedencia         *string  `json:"procedencia"`
	CidadeDoPlano       *string  `json:"cidade_do_plano"`
	UFDoPlano           *string  `json:"uf_do_plano"`
	CruzadoEm           *string  `json:"cruzado_em"`
}

type cidadeNoAlcance struct {
	Cidade      string  `json:"cidade"`
	UF          string  `json:"uf"`
	IBGE        int     `json:"ibge"`
	DistanciaKm float64 `json:"distancia_km"`
	Sede        bool    `json:"sede"`
}

type cidadesNoAlcance struct {
	Total int               `json:"total"`
	Itens []cidadeNoAlcance `json:"itens"`
}

type campanhaNaEmissora struct {
	Campanha ref    `json:"campanha"`
	Cliente  ref    `json:"cliente"`
	Estado   string `json:"estado"`
	Inicio   string `json:"inicio"`
	Fim      string `json:"fim"`
}

type saidaObterEmissora struct {
	Emissora         emissoraDTO          `json:"emissora"`
	Perfil           *perfilEmissora      `json:"perfil"`
	Anatel           anatelDTO            `json:"anatel"`
	CidadesNoAlcance cidadesNoAlcance     `json:"cidades_no_alcance"`
	Campanhas        []campanhaNaEmissora `json:"campanhas"`
	Ressalvas        []string             `json:"ressalvas"`
}

type entradaBuscarEmissoras struct {
	Busca         string `json:"busca,omitempty" jsonschema:"Palavras (todas precisam casar): nome, cidade, UF, banda ou dial (ex.: 'jovem pan 100.9 sp')."`
	Banda         string `json:"banda,omitempty" jsonschema:"FM ou AM."`
	UF            string `json:"uf,omitempty" jsonschema:"Sigla do estado (ex.: SP)."`
	Cidade        string `json:"cidade,omitempty" jsonschema:"Nome exato da cidade (ignora acento)."`
	ContratadaPor string `json:"contratada_por,omitempty" jsonschema:"Id de cliente (de listar_clientes): só as emissoras de campanhas ativas ou programadas dele, com o resumo do contrato. Nunca invente: use o id de outra tool."`
	Paginacao
}

type entradaEmissora struct {
	EmissoraID string `json:"emissora_id" jsonschema:"Id (uuid) da emissora, vindo de buscar_emissoras. Nunca invente: pergunte."`
}

// tetoCidades: cidades no alcance devolvidas por obter_emissora (o total vem sempre).
const tetoCidades = 100

const ressalvaAnatel = "Anatel: contorno_protegido_km é o raio normativo da classe; alcance_km = contorno × 1,5 (transbordo) e é o raio que gera as cidades no alcance. null = indeterminado, nunca zero (AM não tem raio por classe). Classe C de FM ≠ classe C de AM."

// lerEmissoraCompleta monta o que obter_emissora devolve para uma emissora já
// lida; editar_emissora devolve o MESMO formato depois de gravar. Nunca devolve
// stream_url (emissoraDTO é whitelist).
func lerEmissoraCompleta(ctx context.Context, L *Leitura, st *catalog.Station) (*saidaObterEmissora, error) {
	id := st.ID
	an, err := L.Emissoras.Anatel(ctx, id)
	if err != nil {
		return nil, err
	}
	cidades, err := L.Emissoras.CoverageCities(ctx, id)
	if err != nil {
		return nil, err
	}
	ativas, err := L.Campanhas.ListFiltered(ctx, []string{"ativa", "programada"}, nil)
	if err != nil {
		return nil, err
	}
	clientes, err := nomesClientes(ctx, L)
	if err != nil {
		return nil, err
	}
	out := &saidaObterEmissora{
		Emissora: emissoraDe(*st),
		Anatel: anatelDTO{
			Classe: an.Class, ContornoProtegidoKm: an.CoverageKm, AlcanceKm: an.ReachKm, Procedencia: an.MatchTier,
			CidadeDoPlano: an.PlanCity, UFDoPlano: an.PlanState, CruzadoEm: instanteOpc(an.MatchedAt),
		},
		CidadesNoAlcance: cidadesNoAlcance{Total: len(cidades), Itens: []cidadeNoAlcance{}},
		Campanhas:        []campanhaNaEmissora{},
		Ressalvas:        []string{ressalvaAnatel},
	}
	if mt := st.Meta; mt != nil {
		// website está NULL em quase toda emissora; o endereço real ficou
		// em audiency_site (ver catalog.StationMeta).
		site := mt.Website
		if site == nil || *site == "" {
			site = mt.AudiencySite
		}
		out.Perfil = &perfilEmissora{
			Categorias: mt.Categories, PerfilAudiencia: perfilAudienciaDe(mt.AudienceProfile), PopulacaoTotal: mt.TotalPopulation,
			Site: site, AnoFundacao: mt.FoundationYear, RazaoSocial: mt.CompanyName,
			NomeFantasia: mt.FantasyName, CNPJ: mt.CNPJ, EmailComercial: mt.CommercialEmail,
		}
	}
	for _, c := range cidades[:min(len(cidades), tetoCidades)] {
		out.CidadesNoAlcance.Itens = append(out.CidadesNoAlcance.Itens, cidadeNoAlcance{
			Cidade: c.City, UF: c.State, IBGE: c.IBGECode, DistanciaKm: c.DistanceKm, Sede: c.IsHome,
		})
	}
	if len(cidades) > tetoCidades {
		out.Ressalvas = append(out.Ressalvas, fmt.Sprintf(
			"mostrando as %d cidades mais próximas de %d no alcance", tetoCidades, len(cidades)))
	}
	for _, c := range ativas {
		if slices.Contains(c.TargetStations, id) {
			out.Campanhas = append(out.Campanhas, campanhaNaEmissora{
				Campanha: refDe(c.ID, c.Name), Cliente: refDe(c.ClientID, clientes[c.ClientID]),
				Estado: c.Status, Inicio: dia(c.StartDate), Fim: dia(c.EndDate),
			})
		}
	}
	return out, nil
}

func registrarEmissoras(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "buscar_emissoras",
		"Busca emissoras como a tela /stations: palavras que todas precisam casar (nome, cidade, UF, banda ou dial), mais filtros "+
			"de banda, UF e cidade. contratada_por (id de cliente) restringe às emissoras das campanhas ativas/programadas dele e "+
			"traz o resumo do contrato. Devolve id, nome, banda, dial, cidade/UF e PMM. Lista paginada.",
		func(ctx context.Context, L *Leitura, in entradaBuscarEmissoras) (*sdk.CallToolResult, error) {
			var contratadas []uuid.UUID
			if in.ContratadaPor != "" {
				c, rec, err := lerCliente(ctx, L, in.ContratadaPor)
				if rec != nil || err != nil {
					return rec, err
				}
				contratadas = []uuid.UUID{c.ID}
			}
			pg, err := buscarJanela(in.Paginacao, func(page, size int) ([]catalog.Station, int, error) {
				res, err := L.Emissoras.List(ctx, catalog.ListInput{
					Q: in.Busca, Band: strings.ToUpper(strings.TrimSpace(in.Banda)), State: strings.ToUpper(strings.TrimSpace(in.UF)), City: in.Cidade, ContractedBy: contratadas,
					Page: page, Limit: size,
				})
				return res.Data, int(res.Total), err
			})
			if err != nil {
				return nil, err
			}
			return ok(mapear(pg, emissoraDe))
		})

	ferramentaLeitura(s, cfg, "obter_emissora",
		"Devolve uma emissora: cadastro, perfil (categorias, audiência, site, razão social), a classe Anatel com o contorno "+
			"protegido e o alcance (transbordo), as cidades no alcance (as 100 mais próximas + o total) e as campanhas ativas "+
			"ou programadas nela. A armadilha: raio null é indeterminado, não zero. Nunca invente o emissora_id.",
		func(ctx context.Context, L *Leitura, in entradaEmissora) (*sdk.CallToolResult, error) {
			id, err := uuid.Parse(in.EmissoraID)
			if err != nil {
				return recusaEntrada("emissora_id")
			}
			st, err := L.Emissoras.Get(ctx, id)
			if errors.Is(err, pgx.ErrNoRows) {
				return naoEncontrado("emissora")
			}
			if err != nil {
				return nil, err
			}
			out, err := lerEmissoraCompleta(ctx, L, st)
			if err != nil {
				return nil, err
			}
			return ok(out)
		})
}
