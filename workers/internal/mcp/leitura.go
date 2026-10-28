package mcp

import (
	"bytes"
	"context"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"radiocheck/internal/catalog"
	"radiocheck/internal/users"
	"radiocheck/internal/welcome"
)

// Leitura reúne os repositórios que as tools de leitura chamam — os MESMOS
// que os handlers HTTP usam (cmd/api/main.go). Nenhuma regra de negócio mora
// aqui nem nas tools: elas traduzem argumentos, chamam o repositório e moldam
// a resposta no contrato.
type Leitura struct {
	Clientes          *catalog.Clients
	PMMTarget         *catalog.ClientStationPMM
	Campanhas         *catalog.Campaigns
	MateriaisCampanha *catalog.CampaignMaterials
	Regras            *catalog.DistributionRules
	Ajustes           *catalog.DistributionOverrides
	Precos            *catalog.Pricing
	Materiais         *catalog.Materials
	TiposMaterial     *catalog.MaterialTypes
	Emissoras         *catalog.Stations
	Veiculacoes       *catalog.Detections
	Grade             *catalog.DailySummaryRepo
	Notificacoes      *catalog.Notifications
	Usuarios          *users.Repo
	BoasVindas        *welcome.Repo
}

func NovaLeitura(pool *pgxpool.Pool) *Leitura {
	return &Leitura{
		Clientes:          catalog.NewClients(pool),
		PMMTarget:         catalog.NewClientStationPMM(pool),
		Campanhas:         catalog.NewCampaigns(pool),
		MateriaisCampanha: catalog.NewCampaignMaterials(pool),
		Regras:            catalog.NewDistributionRules(pool),
		Ajustes:           catalog.NewDistributionOverrides(pool),
		Precos:            catalog.NewPricing(pool),
		Materiais:         catalog.NewMaterials(pool),
		TiposMaterial:     catalog.NewMaterialTypes(pool),
		Emissoras:         catalog.NewStations(pool),
		Veiculacoes:       catalog.NewDetections(pool),
		Grade:             catalog.NewDailySummary(pool),
		Notificacoes:      catalog.NewNotifications(pool),
		Usuarios:          users.NewRepo(pool),
		BoasVindas:        welcome.NewRepo(pool),
	}
}

// nomesClientes: id → nome de todos os clientes (uma consulta; são dezenas).
func nomesClientes(ctx context.Context, L *Leitura) (map[uuid.UUID]string, error) {
	cs, err := L.Clientes.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]string, len(cs))
	for _, c := range cs {
		out[c.ID] = c.Name
	}
	return out, nil
}

// nomesEmissoras: id → nome, pelo mesmo Stations.List da tela (filtro ids,
// no máximo 500 por chamada — o teto do handler).
func nomesEmissoras(ctx context.Context, L *Leitura, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	out := make(map[uuid.UUID]string, len(ids))
	// A grade passa um id por linha: sem dedupe viram dezenas de lotes iguais.
	ids = slices.Compact(slices.SortedFunc(slices.Values(ids), func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) }))
	for len(ids) > 0 {
		n := min(500, len(ids))
		res, err := L.Emissoras.List(ctx, catalog.ListInput{IDs: ids[:n]})
		if err != nil {
			return nil, err
		}
		for _, s := range res.Data {
			out[s.ID] = s.Name
		}
		ids = ids[n:]
	}
	return out, nil
}

// nomesTipos: id → nome de todos os tipos de material.
func nomesTipos(ctx context.Context, L *Leitura) (map[uuid.UUID]string, error) {
	ts, err := L.TiposMaterial.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]string, len(ts))
	for _, t := range ts {
		out[t.ID] = t.Name
	}
	return out, nil
}

// nomesMateriais: id → título dos materiais de um cliente.
func nomesMateriais(ctx context.Context, L *Leitura, cliente uuid.UUID) (map[uuid.UUID]string, error) {
	ms, err := L.Materiais.ListByClient(ctx, cliente, "")
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]string, len(ms))
	for _, m := range ms {
		out[m.ID] = m.Title
	}
	return out, nil
}

// todasCampanhas: id → campanha (todas, inclusive canceladas; são centenas).
func todasCampanhas(ctx context.Context, L *Leitura) (map[uuid.UUID]catalog.Campaign, error) {
	cs, err := L.Campanhas.ListFiltered(ctx, nil, nil)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]catalog.Campaign, len(cs))
	for _, c := range cs {
		out[c.ID] = c
	}
	return out, nil
}
