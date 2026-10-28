package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"radiocheck/internal/catalog"
	"radiocheck/internal/similarity"
)

type fingerprintDTO struct {
	Status   string  `json:"status"`
	GeradoEm *string `json:"gerado_em"`
	Hashes   *int32  `json:"hashes"`
}

type similaridadeDTO struct {
	Status        string   `json:"status"`
	Score         *float32 `json:"score"`
	ParecidoCom   *ref     `json:"parecido_com"`
	ReconhecidaEm *string  `json:"reconhecida_em"`
}

// materialDTO: whitelist — master_storage_path e master_sha256 nunca saem.
type materialDTO struct {
	ID           string          `json:"id"`
	ShortID      int32           `json:"short_id"`
	Titulo       string          `json:"titulo"`
	Tipo         *ref            `json:"tipo"`
	DuracaoS     float64         `json:"duracao_s"`
	Fingerprint  fingerprintDTO  `json:"fingerprint"`
	Similaridade similaridadeDTO `json:"similaridade"`
	CriadoEm     string          `json:"criado_em"`
}

// materialDe: titulos resolve o nome do material parecido (mesmo cliente).
func materialDe(m catalog.Material, tipos, titulos map[uuid.UUID]string) materialDTO {
	d := materialDTO{
		ID: m.ID.String(), ShortID: m.ShortID, Titulo: m.Title, DuracaoS: m.DurationSeconds,
		Fingerprint: fingerprintDTO{Status: m.FingerprintStatus, GeradoEm: instanteOpc(m.FingerprintGeneratedAt), Hashes: m.FingerprintHashCount},
		Similaridade: similaridadeDTO{Status: m.SimilarityCheckStatus, Score: m.SimilarityScore,
			ReconhecidaEm: instanteOpc(m.SimilarityAckdAt)},
		CriadoEm: instante(m.CreatedAt),
	}
	if m.TypeID != nil {
		r := refDe(*m.TypeID, tipos[*m.TypeID])
		d.Tipo = &r
	}
	if m.MostSimilarMaterialID != nil {
		r := refDe(*m.MostSimilarMaterialID, titulos[*m.MostSimilarMaterialID])
		d.Similaridade.ParecidoCom = &r
	}
	return d
}

type entradaListarMateriais struct {
	ClienteID string `json:"cliente_id" jsonschema:"Id (uuid) do cliente dono dos materiais, vindo de listar_clientes. Nunca invente: pergunte."`
	Busca     string `json:"busca,omitempty" jsonschema:"Trecho do título do material."`
	Paginacao
}

type entradaMaterial struct {
	MaterialID string `json:"material_id" jsonschema:"Id (uuid) do material, vindo de listar_materiais ou listar_materiais_campanha. Nunca invente: pergunte."`
}

type campanhaDoMaterial struct {
	Campanha  ref    `json:"campanha"`
	Estado    string `json:"estado"`
	Emissoras int    `json:"emissoras"`
}

type saidaObterMaterial struct {
	Material  materialDTO          `json:"material"`
	Cliente   ref                  `json:"cliente"`
	Roteiro   *string              `json:"roteiro"`
	Campanhas []campanhaDoMaterial `json:"campanhas"`
	Ressalvas []string             `json:"ressalvas,omitempty"`
}

type tipoMaterialDTO struct {
	ID        string  `json:"id"`
	Nome      string  `json:"nome"`
	Cor       string  `json:"cor"`
	Descricao *string `json:"descricao"`
}

func registrarMateriais(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "listar_materiais",
		"Lista os materiais (áudios de comercial) de um cliente, do mais novo para o mais velho: título, tipo, duração, estado "+
			"do fingerprint (pending, generating, ready, failed — só ready é detectado) e o aviso de similaridade com outro "+
			"material do mesmo cliente. Nunca invente o cliente_id. Lista paginada.",
		func(ctx context.Context, L *Leitura, in entradaListarMateriais) (*sdk.CallToolResult, error) {
			c, rec, err := lerCliente(ctx, L, in.ClienteID)
			if rec != nil || err != nil {
				return rec, err
			}
			mats, err := L.Materiais.ListByClient(ctx, c.ID, in.Busca)
			if err != nil {
				return nil, err
			}
			titulos, err := nomesMateriais(ctx, L, c.ID)
			if err != nil {
				return nil, err
			}
			tipos, err := nomesTipos(ctx, L)
			if err != nil {
				return nil, err
			}
			return ok(mapear(paginar(mats, in.Paginacao), func(m catalog.Material) materialDTO {
				return materialDe(m, tipos, titulos)
			}))
		})

	ferramentaLeitura(s, cfg, "obter_material",
		"Devolve um material: título, tipo, duração, roteiro, estado do fingerprint, similaridade (com qual material e quanto) "+
			"e as campanhas em que está vinculado. A armadilha: dois materiais quase iguais na mesma emissora fazem a tocada "+
			"cair na campanha errada — quando a similaridade passa de 50% sem reconhecimento, vem ressalva. Nunca invente o material_id.",
		func(ctx context.Context, L *Leitura, in entradaMaterial) (*sdk.CallToolResult, error) {
			id, err := uuid.Parse(in.MaterialID)
			if err != nil {
				return recusaEntrada("material_id")
			}
			m, err := L.Materiais.Get(ctx, id)
			if errors.Is(err, pgx.ErrNoRows) {
				return naoEncontrado("material")
			}
			if err != nil {
				return nil, err
			}
			titulos, err := nomesMateriais(ctx, L, m.ClientID)
			if err != nil {
				return nil, err
			}
			tipos, err := nomesTipos(ctx, L)
			if err != nil {
				return nil, err
			}
			clientes, err := nomesClientes(ctx, L)
			if err != nil {
				return nil, err
			}
			vinculos, err := L.MateriaisCampanha.ListByMaterial(ctx, m.ID)
			if err != nil {
				return nil, err
			}
			camps, err := todasCampanhas(ctx, L)
			if err != nil {
				return nil, err
			}
			out := saidaObterMaterial{
				Material: materialDe(*m, tipos, titulos), Cliente: refDe(m.ClientID, clientes[m.ClientID]),
				Roteiro: m.Script, Campanhas: make([]campanhaDoMaterial, 0, len(vinculos)),
			}
			for _, v := range vinculos {
				c := camps[v.CampaignID]
				out.Campanhas = append(out.Campanhas, campanhaDoMaterial{
					Campanha: refDe(v.CampaignID, c.Name), Estado: c.Status, Emissoras: len(v.TargetStations),
				})
			}
			if m.SimilarityScore != nil && *m.SimilarityScore >= similarity.WarnThreshold && m.SimilarityAckdAt == nil {
				parecido := ""
				if m.MostSimilarMaterialID != nil {
					parecido = titulos[*m.MostSimilarMaterialID]
				}
				out.Ressalvas = append(out.Ressalvas, fmt.Sprintf(
					"Aviso de duplicata não reconhecido: este material é %.0f%% parecido com %q. Os dois na mesma emissora em campanhas diferentes fazem a tocada ser atribuída à campanha errada.",
					*m.SimilarityScore*100, parecido))
			}
			return ok(out)
		})

	ferramentaLeitura(s, cfg, "listar_tipos_material",
		"Lista os tipos de material (spot 30s, spot 15s, vinheta, citação…), com id e cor. A grade e as regras de distribuição são "+
			"por TIPO: use o id daqui para ler grade_campanha e plano_do_dia. Lista paginada.",
		func(ctx context.Context, L *Leitura, in Paginacao) (*sdk.CallToolResult, error) {
			ts, err := L.TiposMaterial.List(ctx)
			if err != nil {
				return nil, err
			}
			return ok(mapear(paginar(ts, in), func(t catalog.MaterialType) tipoMaterialDTO {
				return tipoMaterialDTO{ID: t.ID.String(), Nome: t.Name, Cor: t.Color, Descricao: t.Description}
			}))
		})
}
