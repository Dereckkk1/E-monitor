package acoes

import (
	"context"
	"errors"
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

// EditarCadastroEmissora edita nome, frequência, cidade, UF e PMM da emissora,
// relendo-a e aplicando só os campos enviados. É NOVA e só do MCP — a rota
// `PUT /stations/{id}` segue como está (a tela manda o cadastro inteiro).
//
// O MCP NUNCA troca stream_url (spec §1, não-objetivos), banda, logo, nem apaga
// metadados: a gravação é catalog.Stations.UpdateCadastro, que só toca as cinco
// colunas do cadastro e a coordenada derivada da cidade+UF (a geocodificação
// roda quando cidade ou UF mudam, e um lookup que falha não destrói a
// coordenada). Por que não ler tudo e devolver pelo Update, como o plano
// descrevia: o Update reserializa o metadata pelo struct StationMeta (chave
// desconhecida se perde) e a leitura-escrita abre uma corrida com a troca de
// stream_url feita pelo wizard — ver o comentário de UpdateCadastro.
//
// Classe e raio da Anatel (anatel_*) NÃO são recalculados quando dial ou cidade
// mudam: quem diz isso ao usuário é a tool.
func (s *Servico) EditarCadastroEmissora(ctx context.Context, id uuid.UUID, in CadastroEmissora) (*catalog.Station, error) {
	atual, err := s.Emissoras.Get(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, naoEncontrado("emissora não encontrada")
		}
		return nil, err
	}

	merged := catalog.UpdateCadastroInput{
		Name: atual.Name, FrequencyMHz: atual.FrequencyMHz,
		City: atual.City, State: atual.State, PMM: atual.PMM,
	}
	if in.Nome != nil {
		nome := strings.TrimSpace(*in.Nome)
		if nome == "" {
			return nil, entradaInvalida("o nome da emissora não pode ficar vazio")
		}
		merged.Name = nome
	}
	if in.FrequenciaMHz != nil {
		// NUMERIC(6,2): até 9999,99.
		if *in.FrequenciaMHz <= 0 || *in.FrequenciaMHz >= 10000 {
			return nil, entradaInvalida("a frequência precisa estar entre 0 e 10000 (MHz para FM, kHz para AM)")
		}
		merged.FrequencyMHz = in.FrequenciaMHz
	}
	if in.Cidade != nil {
		cidade := strings.TrimSpace(*in.Cidade)
		if cidade == "" {
			return nil, entradaInvalida("a cidade não pode ficar vazia")
		}
		merged.City = &cidade
	}
	if in.UF != nil {
		uf := strings.ToUpper(strings.TrimSpace(*in.UF))
		if len([]rune(uf)) != 2 {
			return nil, entradaInvalida("a UF precisa ter 2 letras (ex.: MG)")
		}
		merged.State = &uf
	}
	if in.PMM != nil {
		// NUMERIC(10,2): até 99.999.999,99.
		if *in.PMM < 0 || *in.PMM >= 1e8 {
			return nil, entradaInvalida("o PMM precisa estar entre 0 e 99.999.999")
		}
		merged.PMM = in.PMM
	}

	st, err := s.Emissoras.UpdateCadastro(ctx, id, merged)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, naoEncontrado("emissora não encontrada")
		}
		return nil, recusaDoBanco(err)
	}
	return st, nil
}
