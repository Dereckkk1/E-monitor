package acoes

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

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
	// Banda ("FM" ou "AM") é a banda da emissora que está sendo editada, não um
	// campo a gravar (o MCP nunca troca a banda). Quando preenchida, a frequência
	// enviada é conferida contra a faixa dela; vazia, vale só o limite do banco.
	Banda string
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

// Tetos de texto do cadastro (as colunas são TEXT sem CHECK: o teto é nosso, para
// um nome ou cidade absurdos não entupirem listas e relatórios).
const (
	tetoNomeEmissora   = 200
	tetoCidadeEmissora = 200
)

// Faixas de frequência por banda. A frequência mora em stations.frequency_mhz na
// unidade da banda: FM em MHz, AM em kHz (ver anatel.Station.FreqMHz).
const (
	fmMinMHz = 76.0
	fmMaxMHz = 108.0
	amMinKHz = 530.0
	amMaxKHz = 1700.0
)

// ufsDoBrasil: as 27 unidades da federação (sigla em maiúsculas).
var ufsDoBrasil = map[string]bool{
	"AC": true, "AL": true, "AP": true, "AM": true, "BA": true, "CE": true, "DF": true, "ES": true, "GO": true,
	"MA": true, "MT": true, "MS": true, "MG": true, "PA": true, "PB": true, "PR": true, "PE": true, "PI": true,
	"RJ": true, "RN": true, "RS": true, "RO": true, "RR": true, "SC": true, "SP": true, "SE": true, "TO": true,
}

// UFValida diz se a sigla (já em maiúsculas) é uma das 27 UFs.
func UFValida(uf string) bool { return ufsDoBrasil[uf] }

// validarFrequenciaDaBanda confere a frequência com a faixa da banda. Banda
// desconhecida ou vazia não confere nada (vale o limite do banco, já checado).
func validarFrequenciaDaBanda(banda string, f float64) error {
	switch strings.ToUpper(strings.TrimSpace(banda)) {
	case "FM":
		if f < fmMinMHz || f > fmMaxMHz {
			return entradaInvalida(fmt.Sprintf("a frequência de uma emissora FM precisa estar entre %.0f e %.0f MHz", fmMinMHz, fmMaxMHz))
		}
	case "AM":
		if f < amMinKHz || f > amMaxKHz {
			return entradaInvalida(fmt.Sprintf("a frequência de uma emissora AM precisa estar entre %.0f e %.0f kHz", amMinKHz, amMaxKHz))
		}
	}
	return nil
}

// ValidarCadastroEmissora confere e normaliza os campos enviados (nome e cidade
// aparados, UF em maiúsculas, limites dos NUMERIC do banco) e devolve o que a
// gravação receberá. Está separada de EditarCadastroEmissora para a tool do MCP
// validar no PREVIEW, sem gravar, exatamente o que a ação validará depois.
// Devolve *Recusa{entrada_invalida}, em português.
func ValidarCadastroEmissora(in CadastroEmissora) (catalog.UpdateCadastroInput, error) {
	var up catalog.UpdateCadastroInput
	if in.Nome != nil {
		nome := strings.TrimSpace(*in.Nome)
		if nome == "" {
			return up, entradaInvalida("o nome da emissora não pode ficar vazio")
		}
		if utf8.RuneCountInString(nome) > tetoNomeEmissora {
			return up, entradaInvalida(fmt.Sprintf("nome: no máximo %d caracteres", tetoNomeEmissora))
		}
		up.Name = &nome
	}
	if in.FrequenciaMHz != nil {
		// A comparação em negativo também recusa NaN.
		if f := *in.FrequenciaMHz; !(f > 0 && f < tetoFrequencia) || math.IsInf(f, 0) {
			return up, entradaInvalida("a frequência precisa ser maior que 0 e menor que 9999,995 (MHz para FM, kHz para AM)")
		}
		if err := validarFrequenciaDaBanda(in.Banda, *in.FrequenciaMHz); err != nil {
			return up, err
		}
		up.FrequencyMHz = in.FrequenciaMHz
	}
	if in.Cidade != nil {
		cidade := strings.TrimSpace(*in.Cidade)
		if cidade == "" {
			return up, entradaInvalida("a cidade não pode ficar vazia")
		}
		if utf8.RuneCountInString(cidade) > tetoCidadeEmissora {
			return up, entradaInvalida(fmt.Sprintf("cidade: no máximo %d caracteres", tetoCidadeEmissora))
		}
		up.City = &cidade
	}
	if in.UF != nil {
		uf := strings.ToUpper(strings.TrimSpace(*in.UF))
		if !UFValida(uf) {
			return up, entradaInvalida("UF inválida: use a sigla de uma das 27 unidades da federação (ex.: MG)")
		}
		up.State = &uf
	}
	if in.PMM != nil {
		if p := *in.PMM; !(p >= 0 && p < tetoPMM) || math.IsInf(p, 0) {
			return up, entradaInvalida("o PMM precisa estar entre 0 e 99.999.999")
		}
		up.PMM = in.PMM
	}
	return up, nil
}

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
	up, err := ValidarCadastroEmissora(in)
	if err != nil {
		return nil, err
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
