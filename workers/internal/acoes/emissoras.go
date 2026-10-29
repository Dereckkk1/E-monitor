package acoes

import (
	"context"
	"errors"
	"math"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"radiocheck/internal/catalog"
)

// CadastroEmissora são os campos do cadastro da emissora que o MCP edita. Cada
// ponteiro nil = "não enviado" (o valor atual fica). É edição PARCIAL de
// propósito (F3-10): a rota é PUT total, e um modelo que esquece um campo
// apagaria o dado.
type CadastroEmissora struct {
	Nome          *string
	FrequenciaMHz *float64
	Cidade        *string
	UF            *string
	PMM           *float64
}

// ResultadoCadastroEmissora é a emissora depois da edição e o aviso que a tool
// precisa dar.
type ResultadoCadastroEmissora struct {
	Emissora *catalog.Station
	// GeocodificacaoFalhou: cidade ou UF foram enviadas mas não há coordenada
	// para o par (cidade/UF desconhecida pelo dataset, ou sem o par completo). A
	// coordenada ANTERIOR (se havia) ficou, e está velha — o mapa e o raio de
	// cobertura seguem nela.
	GeocodificacaoFalhou bool
}

// Teto dos NUMERIC do banco já considerando o arredondamento a 2 casas: um valor
// como 9999,996 arredonda para 10000,00 e estoura NUMERIC(6,2) (22003).
const (
	tetoFrequencia = 9999.995     // NUMERIC(6,2)
	tetoPMM        = 99999999.995 // NUMERIC(10,2)
)

// EditarCadastroEmissora edita nome, frequência, cidade, UF e PMM da emissora,
// gravando só os campos enviados. É NOVA e só do MCP — a rota
// `PUT /stations/{id}` segue como está (a tela manda o cadastro inteiro).
//
// O MCP NUNCA troca stream_url (spec §1, não-objetivos), banda, logo, nem apaga
// metadados: a gravação é catalog.Stations.UpdateCadastro, que só toca as cinco
// colunas do cadastro — parcial NO SQL (COALESCE), sem ler-e-devolver, então uma
// mudança concorrente num campo não enviado (um stream_url trocado pelo wizard,
// um PMM corrigido) não é revertida — e a coordenada derivada da cidade+UF. Por
// que não pelo Update, como o plano descrevia: o Update reserializa o metadata
// pelo struct StationMeta (chave desconhecida se perde).
//
// Classe e raio da Anatel (anatel_*) NÃO são recalculados quando dial ou cidade
// mudam: quem diz isso ao usuário é a tool.
func (s *Servico) EditarCadastroEmissora(ctx context.Context, id uuid.UUID, in CadastroEmissora) (*ResultadoCadastroEmissora, error) {
	var up catalog.UpdateCadastroInput
	if in.Nome != nil {
		nome := strings.TrimSpace(*in.Nome)
		if nome == "" {
			return nil, entradaInvalida("o nome da emissora não pode ficar vazio")
		}
		up.Name = &nome
	}
	if in.FrequenciaMHz != nil {
		// A comparação em negativo também recusa NaN.
		if f := *in.FrequenciaMHz; !(f > 0 && f < tetoFrequencia) || math.IsInf(f, 0) {
			return nil, entradaInvalida("a frequência precisa ser maior que 0 e menor que 9999,995 (MHz para FM, kHz para AM)")
		}
		up.FrequencyMHz = in.FrequenciaMHz
	}
	if in.Cidade != nil {
		cidade := strings.TrimSpace(*in.Cidade)
		if cidade == "" {
			return nil, entradaInvalida("a cidade não pode ficar vazia")
		}
		up.City = &cidade
	}
	if in.UF != nil {
		uf := strings.ToUpper(strings.TrimSpace(*in.UF))
		if len([]rune(uf)) != 2 {
			return nil, entradaInvalida("a UF precisa ter 2 letras (ex.: MG)")
		}
		up.State = &uf
	}
	if in.PMM != nil {
		if p := *in.PMM; !(p >= 0 && p < tetoPMM) || math.IsInf(p, 0) {
			return nil, entradaInvalida("o PMM precisa estar entre 0 e 99.999.999")
		}
		up.PMM = in.PMM
	}

	st, falhou, err := s.Emissoras.UpdateCadastro(ctx, id, up)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, naoEncontrado("emissora não encontrada")
		}
		return nil, recusaDoBanco(err)
	}
	return &ResultadoCadastroEmissora{Emissora: st, GeocodificacaoFalhou: falhou}, nil
}
