package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/google/uuid"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"radiocheck/internal/catalog"
)

type entradaMapa struct {
	CampanhaIDs []string `json:"campanha_ids" jsonschema:"De 1 a 200 campanhas (de listar_campanhas). Canceladas não entram: o mapa é tela ao vivo. Nunca invente: pergunte."`
	Paginacao
}

// tetoPaginaMapa: cada emissora leva até tetoPorItem cidades; com o teto
// global (100) uma página passava de 150 KB, acima do que o cliente MCP aceita
// (~25k tokens). Medido em TestMapaCoberturaCampanha_PiorCasoCabe.
const tetoPaginaMapa = 25

type cidadeDTO struct {
	Nome        string  `json:"nome"`
	UF          string  `json:"uf"`
	DistanciaKm float64 `json:"distancia_km"`
	Sede        bool    `json:"sede"`
}

type emissoraMapa struct {
	Emissora            ref         `json:"emissora"`
	Cidade              *string     `json:"cidade"`
	UF                  *string     `json:"uf"`
	Banda               string      `json:"banda"`
	Dial                *float64    `json:"dial"`
	ClasseAnatel        *string     `json:"classe_anatel"`
	ContornoProtegidoKm *float64    `json:"contorno_protegido_km"`
	AlcanceKm           *float64    `json:"alcance_km"`
	UltimaVeiculacao    *string     `json:"ultima_veiculacao"`
	Cidades             []cidadeDTO `json:"cidades"`
	CidadesTotal        int         `json:"cidades_total"`
}

type mapaDTO struct {
	CidadesDistintas    int `json:"cidades_distintas"`
	ParesEmissoraCidade int `json:"pares_emissora_cidade"`
	lista[emissoraMapa]
	Links links `json:"links"`
}

var ressalvaMapa = ressalvaAnatel + fmt.Sprintf(" cidades de cada emissora: as %d mais próximas (cidades_total traz o número real; obter_emissora lista até %d). "+
	"cidades_distintas conta cada município uma vez, mesmo alcançado por várias emissoras; pares_emissora_cidade conta um por emissora. "+
	"Emissora sem coordenada não aparece na lista, mas as cidades dela entram nos totais.", tetoPorItem, tetoCidades)

func registrarMapa(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "mapa_cobertura_campanha",
		"Devolve a cobertura das campanhas como o Mapa ao Vivo (/live-map): por emissora, a classe Anatel, o contorno "+
			"protegido e o alcance (transbordo) em km e as cidades no alcance; no topo, quantos municípios distintos a "+
			"campanha alcança; e o link da tela. Pede 1 a 200 campanha_ids. Lista paginada de emissoras (até 25 por página).",
		func(ctx context.Context, L *Leitura, in entradaMapa) (*sdk.CallToolResult, error) {
			camps, rec := lerIDsObrigatorios("campanha_ids", in.CampanhaIDs, 200)
			if rec != nil {
				return rec, nil
			}
			// As duas chamadas que a tela /live-map faz, com o padrão dela (sem canceladas).
			mapa, err := L.MapaAoVivo.Get(ctx, camps, nil, catalog.LiveMapOpts{})
			if errors.Is(err, catalog.ErrCampaignNotFound) {
				return naoEncontrado("campanha (inexistente ou cancelada — o mapa ao vivo não mostra canceladas)")
			}
			if err != nil {
				return nil, err
			}
			cob, err := L.MapaAoVivo.Coverage(ctx, camps, nil, catalog.LiveMapOpts{})
			if err != nil {
				return nil, err
			}
			porEmissora := map[uuid.UUID][]catalog.CoverageCity{}
			for _, c := range cob.Cities { // já vem por emissora e distância
				porEmissora[c.StationID] = append(porEmissora[c.StationID], c)
			}
			tot := cob.Totals()
			o := mapaDTO{CidadesDistintas: tot.DistinctCities, ParesEmissoraCidade: tot.StationCityPairs}
			o.lista = mapear(paginar(mapa.Stations, in.Paginacao.comTeto(tetoPaginaMapa)), func(s catalog.LiveStation) emissoraMapa {
				cs, n := cortar(porEmissora[s.ID], tetoPorItem)
				e := emissoraMapa{Emissora: refDe(s.ID, s.Name), Cidade: s.City, UF: s.State, Banda: s.Band, Dial: s.FrequencyMHz,
					ClasseAnatel: s.AnatelClass, ContornoProtegidoKm: s.AnatelCoverageKm, AlcanceKm: s.AnatelReachKm,
					UltimaVeiculacao: instanteOpc(s.LastDetectionAt), CidadesTotal: n, Cidades: make([]cidadeDTO, len(cs))}
				for i, c := range cs {
					e.Cidades[i] = cidadeDTO{Nome: c.City, UF: c.State, DistanciaKm: c.DistanceKm, Sede: c.IsHome}
				}
				return e
			})
			q, err := queryMapa(ctx, L, camps)
			if err != nil {
				return nil, err
			}
			o.Ressalvas = []string{ressalvaMapa}
			o.Links = links{Tela: cfg.linkTela("/live-map", q)}
			return ok(o)
		})
}

// queryMapa: a tela pede o cliente antes das campanhas; o link só leva
// client_id quando todas as campanhas são do mesmo cliente.
func queryMapa(ctx context.Context, L *Leitura, camps []uuid.UUID) (url.Values, error) {
	q := url.Values{}
	q.Set("campaigns", csvIDs(camps))
	todas, err := todasCampanhas(ctx, L)
	if err != nil {
		return nil, err
	}
	var cliente uuid.UUID
	for i, id := range camps {
		c, achou := todas[id]
		if !achou || (i > 0 && c.ClientID != cliente) {
			return q, nil
		}
		cliente = c.ClientID
	}
	q.Set("client_id", cliente.String())
	return q, nil
}
