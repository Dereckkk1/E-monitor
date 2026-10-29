package mcp

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"radiocheck/internal/acoes"
	"radiocheck/internal/catalog"
)

// Escritas da Task 12: tipos de material (globais, só rótulo) e editar_material
// (título, roteiro e tipo — só o tipo atinge o passado).

// ── Tipos de material ───────────────────────────────────────────────────

const (
	// corPadraoTipo é a cor que o repositório dá quando nenhuma vem (slate-400).
	corPadraoTipo = "#94a3b8"
	// Tetos do cadastro de tipo: a tabela é TEXT sem CHECK, o teto é do MCP (um
	// nome de 5 mil caracteres entupiria seletores e relatórios).
	tetoNomeTipo      = 80
	tetoDescricaoTipo = 500
)

var formaDaCor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

type saidaTipoMaterial struct {
	Tipo tipoMaterialDTO `json:"tipo"`
}

func validarNomeDeTipo(s string) (string, error) {
	nome := aparar(s)
	if nome == "" {
		return "", errEntrada("o nome do tipo de material não pode ficar vazio")
	}
	if err := limiteDeRunas("nome", nome, tetoNomeTipo); err != nil {
		return "", err
	}
	return nome, nil
}

// validarCorDeTipo devolve a cor em minúsculas; "" fica "" (sem cor enviada).
func validarCorDeTipo(s string) (string, error) {
	cor := aparar(s)
	if cor == "" {
		return "", nil
	}
	if !formaDaCor.MatchString(cor) {
		return "", errEntrada("cor inválida: use hexadecimal no formato #rrggbb (ex.: #10b981)")
	}
	return strings.ToLower(cor), nil
}

func validarDescricaoDeTipo(s string) (string, error) {
	d := aparar(s)
	if err := limiteDeRunas("descricao", d, tetoDescricaoTipo); err != nil {
		return "", err
	}
	return d, nil
}

// nomeDeTipoEmUso recusa com nome_duplicado se OUTRO tipo (exceto `exceto`) já
// tem o nome. A comparação ignora a caixa: o índice único do banco só pega a
// igualdade exata, e "Spot 30S" ao lado de "Spot 30s" confunde quem escolhe.
func nomeDeTipoEmUso(ctx context.Context, L *Leitura, nome string, exceto uuid.UUID) error {
	tipos, err := L.TiposMaterial.List(ctx)
	if err != nil {
		return err
	}
	for _, t := range tipos {
		if t.ID != exceto && strings.EqualFold(t.Name, nome) {
			return errRecusa(acoes.CodigoNomeDuplicado,
				fmt.Sprintf("já existe um tipo de material chamado %q (id %s); use editar_tipo_material nele ou escolha outro nome", t.Name, t.ID))
		}
	}
	return nil
}

// ── criar_tipo_material ─────────────────────────────────────────────────

type entradaCriarTipoMaterial struct {
	Nome      string `json:"nome" jsonschema:"Nome do tipo (ex.: Vinheta 5s), único e com no máximo 80 caracteres. Maiúsculas e minúsculas contam como o mesmo nome."`
	Cor       string `json:"cor,omitempty" jsonschema:"Cor em hexadecimal #rrggbb (ex.: #10b981). Sem cor, o tipo nasce cinza (#94a3b8)."`
	Descricao string `json:"descricao,omitempty" jsonschema:"Texto livre de até 500 caracteres explicando para que serve o tipo."`
	CampoConfirmacao
}

// tipoParaCriar valida a entrada e devolve o que será gravado.
func tipoParaCriar(in entradaCriarTipoMaterial) (nome, cor, descricao string, err error) {
	if nome, err = validarNomeDeTipo(in.Nome); err != nil {
		return
	}
	if cor, err = validarCorDeTipo(in.Cor); err != nil {
		return
	}
	descricao, err = validarDescricaoDeTipo(in.Descricao)
	return
}

func escritaCriarTipoMaterial(cfg ConfigServidor) Escrita[entradaCriarTipoMaterial] {
	return Escrita[entradaCriarTipoMaterial]{
		Nome: "criar_tipo_material",
		Descricao: "Cria um tipo de material (spot 30s, vinheta, citação…). Os tipos são globais: valem para todos os clientes, e as regras " +
			"de distribuição e a grade são por tipo — antes de criar, confira em listar_tipos_material se já não existe um equivalente " +
			"(o nome não pode repetir, nem trocando maiúsculas por minúsculas). Criar não altera nada que já existe: o tipo só passa a poder " +
			"ser usado. Mostre o preview e espere a pessoa aprovar antes de confirmar.",
		Nivel:   Confirma,
		Recurso: func(entradaCriarTipoMaterial) (string, *uuid.UUID) { return "material_type", nil },
		Tela:    func(*Escritas, entradaCriarTipoMaterial) *string { return cfg.link("/material-types") },
		Preview: func(ctx context.Context, E *Escritas, in entradaCriarTipoMaterial) (previa, error) {
			nome, cor, descricao, err := tipoParaCriar(in)
			if err != nil {
				return previa{}, err
			}
			if err := nomeDeTipoEmUso(ctx, E.Leitura, nome, uuid.Nil); err != nil {
				return previa{}, err
			}
			corMostrada := cor
			if corMostrada == "" {
				corMostrada = corPadraoTipo
			}
			return previa{
				Resumo: fmt.Sprintf("Criar o tipo de material %q", nome),
				Mudancas: []mudanca{
					{Campo: "nome", De: nil, Para: nome},
					{Campo: "cor", De: nil, Para: corMostrada},
					{Campo: "descricao", De: nil, Para: textoOuNil(&descricao)},
				},
				Efeitos: []string{"O tipo passa a aparecer em listar_tipos_material e pode ser usado em materiais e regras de distribuição. Nada que já existe muda."},
			}, nil
		},
		Executar: func(ctx context.Context, E *Escritas, _ *Principal, in entradaCriarTipoMaterial) (feito, error) {
			nome, cor, descricao, err := tipoParaCriar(in)
			if err != nil {
				return feito{}, err
			}
			mt, err := E.Leitura.TiposMaterial.Create(ctx, catalog.CreateMaterialTypeInput{Name: nome, Color: cor, Description: ptrTexto(descricao)})
			if err != nil {
				if violacaoDeUnicidade(err) {
					return feito{}, errRecusa(acoes.CodigoNomeDuplicado, fmt.Sprintf("já existe um tipo de material chamado %q", nome))
				}
				return feito{}, err
			}
			return feito{Resultado: saidaTipoMaterial{Tipo: tipoMaterialDe(*mt)}, Recurso: "material_type", RecursoID: &mt.ID}, nil
		},
	}
}

// ── editar_tipo_material ────────────────────────────────────────────────

type entradaEditarTipoMaterial struct {
	TipoID    string  `json:"tipo_id" jsonschema:"Id (uuid) do tipo, vindo de listar_tipos_material. Nunca invente: pergunte."`
	Nome      string  `json:"nome,omitempty" jsonschema:"Novo nome (único, até 80 caracteres). Omita para manter."`
	Cor       string  `json:"cor,omitempty" jsonschema:"Nova cor em hexadecimal #rrggbb. Omita para manter."`
	Descricao *string `json:"descricao,omitempty" jsonschema:"Nova descrição (até 500 caracteres); texto vazio limpa a descrição; omita para manter."`
	CampoConfirmacao
}

// edicaoDeTipo é o tipo como está e como ficará, com o que muda.
type edicaoDeTipo struct {
	id       uuid.UUID
	atual    *catalog.MaterialType
	novo     catalog.CreateMaterialTypeInput
	mudancas []mudanca
}

// planejarEdicaoDeTipo lê o tipo, aplica SÓ os campos enviados (edição parcial,
// F3-10) e diz o que muda. Recusa a edição que não muda nada.
func planejarEdicaoDeTipo(ctx context.Context, L *Leitura, in entradaEditarTipoMaterial) (*edicaoDeTipo, error) {
	id, err := uuidDaEntrada("tipo_id", in.TipoID)
	if err != nil {
		return nil, err
	}
	if in.Nome != "" && aparar(in.Nome) == "" {
		return nil, errEntrada("o nome do tipo de material não pode ficar vazio")
	}
	if in.Nome == "" && aparar(in.Cor) == "" && in.Descricao == nil {
		return nil, errEntrada("informe ao menos um campo a alterar (nome, cor ou descricao)")
	}
	var nome, cor, descricao string
	if in.Nome != "" {
		if nome, err = validarNomeDeTipo(in.Nome); err != nil {
			return nil, err
		}
	}
	if cor, err = validarCorDeTipo(in.Cor); err != nil {
		return nil, err
	}
	if in.Descricao != nil {
		if descricao, err = validarDescricaoDeTipo(*in.Descricao); err != nil {
			return nil, err
		}
	}
	atual, err := L.TiposMaterial.Get(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNaoEncontrado("tipo de material")
	}
	if err != nil {
		return nil, err
	}
	pl := &edicaoDeTipo{id: id, atual: atual, novo: catalog.CreateMaterialTypeInput{Name: atual.Name, Color: atual.Color, Description: atual.Description}}
	if nome != "" && nome != atual.Name {
		pl.novo.Name = nome
		pl.mudancas = append(pl.mudancas, mudanca{Campo: "nome", De: atual.Name, Para: nome})
	}
	if cor != "" && cor != strings.ToLower(atual.Color) {
		pl.novo.Color = cor
		pl.mudancas = append(pl.mudancas, mudanca{Campo: "cor", De: atual.Color, Para: cor})
	}
	if in.Descricao != nil && !igualTexto(atual.Description, descricao) {
		pl.novo.Description = ptrTexto(descricao)
		pl.mudancas = append(pl.mudancas, mudanca{Campo: "descricao", De: textoOuNil(atual.Description), Para: textoOuNil(&descricao)})
	}
	if len(pl.mudancas) == 0 {
		return nil, errEntrada("nada a alterar: os valores enviados já são os atuais")
	}
	return pl, nil
}

func escritaEditarTipoMaterial(cfg ConfigServidor) Escrita[entradaEditarTipoMaterial] {
	return Escrita[entradaEditarTipoMaterial]{
		Nome: "editar_tipo_material",
		Descricao: "Edita um tipo de material: só os campos enviados mudam (nome, cor, descricao; descricao vazia limpa o texto). O tipo é " +
			"só rótulo — nome e cor aparecem nas telas e relatórios, mas não mudam atribuição nem categoria de veiculação, então " +
			"nunca sobe para crítico. Os tipos são globais (valem para todos os clientes). Nunca invente o tipo_id: pergunte. " +
			"Mostre o preview e espere a pessoa aprovar antes de confirmar.",
		Nivel: Confirma,
		Recurso: func(in entradaEditarTipoMaterial) (string, *uuid.UUID) {
			if id, err := uuid.Parse(in.TipoID); err == nil {
				return "material_type", &id
			}
			return "material_type", nil
		},
		Tela: func(*Escritas, entradaEditarTipoMaterial) *string { return cfg.link("/material-types") },
		Preview: func(ctx context.Context, E *Escritas, in entradaEditarTipoMaterial) (previa, error) {
			pl, err := planejarEdicaoDeTipo(ctx, E.Leitura, in)
			if err != nil {
				return previa{}, err
			}
			if pl.novo.Name != pl.atual.Name {
				if err := nomeDeTipoEmUso(ctx, E.Leitura, pl.novo.Name, pl.id); err != nil {
					return previa{}, err
				}
			}
			return previa{
				Resumo:   fmt.Sprintf("Editar o tipo de material %q", pl.atual.Name),
				Mudancas: pl.mudancas,
				Efeitos:  []string{"O tipo vale para todos os clientes: o novo nome/cor aparece em todas as telas e relatórios. Atribuição e categoria das veiculações não mudam."},
			}, nil
		},
		Executar: func(ctx context.Context, E *Escritas, _ *Principal, in entradaEditarTipoMaterial) (feito, error) {
			pl, err := planejarEdicaoDeTipo(ctx, E.Leitura, in)
			if err != nil {
				return feito{}, err
			}
			mt, err := E.Leitura.TiposMaterial.Update(ctx, pl.id, pl.novo)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return feito{}, errNaoEncontrado("tipo de material")
				}
				if violacaoDeUnicidade(err) {
					return feito{}, errRecusa(acoes.CodigoNomeDuplicado, fmt.Sprintf("já existe um tipo de material chamado %q", pl.novo.Name))
				}
				return feito{}, err
			}
			return feito{Resultado: saidaTipoMaterial{Tipo: tipoMaterialDe(*mt)}, Recurso: "material_type", RecursoID: &mt.ID}, nil
		},
	}
}

// ── editar_material ─────────────────────────────────────────────────────

const (
	tetoTituloMaterial  = 200
	tetoRoteiroMaterial = 20000
	// tetoRoteiroNoPreview: o roteiro inteiro não vai para a resposta do preview.
	tetoRoteiroNoPreview = 200
)

type entradaEditarMaterial struct {
	MaterialID string  `json:"material_id" jsonschema:"Id (uuid) do material, vindo de listar_materiais ou listar_materiais_campanha. Nunca invente: pergunte."`
	Titulo     string  `json:"titulo,omitempty" jsonschema:"Novo título (até 200 caracteres). Omita para manter."`
	TipoID     string  `json:"tipo_id,omitempty" jsonschema:"Id (uuid) do novo tipo, vindo de listar_tipos_material. Omita para manter. Não há como deixar o material sem tipo."`
	Roteiro    *string `json:"roteiro,omitempty" jsonschema:"Novo roteiro (texto falado, até 20000 caracteres); texto vazio limpa o roteiro; omita para manter."`
	CampoConfirmacao
}

// edicaoDeMaterial é o plano de uma edição: o que muda e com que valores.
type edicaoDeMaterial struct {
	id    uuid.UUID
	atual *catalog.Material

	mudaTitulo bool
	titulo     string

	mudaTipo     bool
	tipoNovo     uuid.UUID
	tipoNovoNome string

	mudaRoteiro bool
	roteiro     *string // nil = limpar

	mudancas []mudanca
}

// planejarEdicaoDeMaterial valida a entrada, lê o material (e o tipo novo) e diz o
// que muda — SÓ os campos enviados que de fato diferem do atual (F3-10). Recusa a
// edição que não muda nada.
func planejarEdicaoDeMaterial(ctx context.Context, L *Leitura, in entradaEditarMaterial) (*edicaoDeMaterial, error) {
	id, err := uuidDaEntrada("material_id", in.MaterialID)
	if err != nil {
		return nil, err
	}
	pl := &edicaoDeMaterial{id: id}

	titulo := aparar(in.Titulo)
	if in.Titulo != "" && titulo == "" {
		return nil, errEntrada("o título do material não pode ficar em branco")
	}
	if err := limiteDeRunas("titulo", titulo, tetoTituloMaterial); err != nil {
		return nil, err
	}
	var tipoNovo uuid.UUID
	if aparar(in.TipoID) != "" {
		if tipoNovo, err = uuidDaEntrada("tipo_id", in.TipoID); err != nil {
			return nil, err
		}
	}
	var roteiro string
	if in.Roteiro != nil {
		roteiro = aparar(*in.Roteiro)
		if err := limiteDeRunas("roteiro", roteiro, tetoRoteiroMaterial); err != nil {
			return nil, err
		}
	}
	if titulo == "" && tipoNovo == uuid.Nil && in.Roteiro == nil {
		return nil, errEntrada("informe ao menos um campo a alterar (titulo, tipo_id ou roteiro)")
	}

	m, err := L.Materiais.Get(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNaoEncontrado("material")
	}
	if err != nil {
		return nil, err
	}
	pl.atual = m

	if titulo != "" && titulo != m.Title {
		pl.mudaTitulo, pl.titulo = true, titulo
		pl.mudancas = append(pl.mudancas, mudanca{Campo: "titulo", De: m.Title, Para: titulo})
	}
	if tipoNovo != uuid.Nil && (m.TypeID == nil || *m.TypeID != tipoNovo) {
		novo, err := L.TiposMaterial.Get(ctx, tipoNovo)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errNaoEncontrado("tipo de material")
		}
		if err != nil {
			return nil, err
		}
		pl.mudaTipo, pl.tipoNovo, pl.tipoNovoNome = true, tipoNovo, novo.Name
		var de any
		if m.TypeID != nil {
			atual, err := L.TiposMaterial.Get(ctx, *m.TypeID)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return nil, err
			}
			if atual != nil {
				de = atual.Name
			}
		}
		pl.mudancas = append(pl.mudancas, mudanca{Campo: "tipo", De: de, Para: novo.Name})
	}
	if in.Roteiro != nil && !igualTexto(m.Script, roteiro) {
		pl.mudaRoteiro, pl.roteiro = true, ptrTexto(roteiro)
		var de, para any
		if m.Script != nil && *m.Script != "" {
			de = recorte(*m.Script, tetoRoteiroNoPreview)
		}
		if roteiro != "" {
			para = recorte(roteiro, tetoRoteiroNoPreview)
		}
		pl.mudancas = append(pl.mudancas, mudanca{Campo: "roteiro", De: de, Para: para})
	}
	if len(pl.mudancas) == 0 {
		return nil, errEntrada("nada a alterar: os valores enviados já são os atuais do material")
	}
	return pl, nil
}

const ressalvaRecatMaterial = "A recategorização das veiculações do material roda em segundo plano e leva até 30 s."

func escritaEditarMaterial(cfg ConfigServidor) Escrita[entradaEditarMaterial] {
	return Escrita[entradaEditarMaterial]{
		Nome: "editar_material",
		Descricao: "Edita um material (o áudio do comercial): título, roteiro e/ou tipo — só os campos enviados mudam; roteiro vazio limpa o " +
			"texto. Título e roteiro são só rótulo. A armadilha é o tipo: as regras de distribuição são por tipo, então trocar o tipo " +
			"recategoriza as veiculações do material em TODAS as campanhas. Sobe para crítico quando o tipo muda e o material já tem " +
			"veiculação aprovada; nesse caso nada é feito e a resposta traz o link da tela (a troca de tipo se faz na etapa de " +
			"materiais da campanha, logado). Não troca o áudio nem o cliente do material. Nunca invente ids: pergunte. " +
			"Mostre o preview e espere a pessoa aprovar antes de confirmar.",
		Nivel: Confirma,
		Escalar: func(ctx context.Context, E *Escritas, in entradaEditarMaterial) (Escalada, error) {
			if aparar(in.TipoID) == "" {
				return Escalada{}, nil
			}
			pl, err := planejarEdicaoDeMaterial(ctx, E.Leitura, in)
			if err != nil {
				return Escalada{}, err
			}
			if !pl.mudaTipo {
				return Escalada{}, nil
			}
			ha, err := E.Leitura.Veiculacoes.HaVeiculacao(ctx, catalog.EscopoVeiculacao{MaterialID: &pl.id})
			if err != nil {
				return Escalada{}, err
			}
			if !ha {
				return Escalada{}, nil
			}
			return Escalada{Nivel: Critico, Motivo: "O material já tem veiculação aprovada: trocar o tipo recategoriza as veiculações dele em todas as campanhas " +
				"(cota, déficit e bonificação de dias passados, que o cliente já viu)."}, nil
		},
		Recurso: func(in entradaEditarMaterial) (string, *uuid.UUID) {
			if id, err := uuid.Parse(in.MaterialID); err == nil {
				return "material", &id
			}
			return "material", nil
		},
		Tela: func(*Escritas, entradaEditarMaterial) *string { return cfg.link("/materials") },
		Preview: func(ctx context.Context, E *Escritas, in entradaEditarMaterial) (previa, error) {
			pl, err := planejarEdicaoDeMaterial(ctx, E.Leitura, in)
			if err != nil {
				return previa{}, err
			}
			pv := previa{Resumo: fmt.Sprintf("Editar o material %q", pl.atual.Title), Mudancas: pl.mudancas}
			if pl.mudaTitulo {
				pv.Efeitos = append(pv.Efeitos, "O título é só rótulo: não afeta fingerprint, detecção nem categoria.")
			}
			if pl.mudaRoteiro {
				pv.Efeitos = append(pv.Efeitos, "O roteiro aparece no detalhe da veiculação; não afeta a detecção.")
			}
			if pl.mudaTipo {
				// O preview também é o que o ramo crítico devolve: o texto tem de dizer a verdade nos dois casos.
				ha, err := E.Leitura.Veiculacoes.HaVeiculacao(ctx, catalog.EscopoVeiculacao{MaterialID: &pl.id})
				if err != nil {
					return previa{}, err
				}
				efeito := fmt.Sprintf("As regras de distribuição são por tipo: o material passa a contar nas regras do tipo %q e as "+
					"veiculações dele, em todas as campanhas, são recategorizadas em segundo plano (até 30 s). ", pl.tipoNovoNome)
				if ha {
					efeito += "O material já tem veiculação aprovada: a cota, o déficit e a bonificação de dias passados podem mudar."
				} else {
					efeito += "O material não tem veiculação aprovada, então hoje não há número passado a mudar."
				}
				pv.Efeitos = append(pv.Efeitos, efeito)
			}
			return pv, nil
		},
		Executar: func(ctx context.Context, E *Escritas, _ *Principal, in entradaEditarMaterial) (feito, error) {
			pl, err := planejarEdicaoDeMaterial(ctx, E.Leitura, in)
			if err != nil {
				return feito{}, err
			}
			// O tipo primeiro: é o que pode recusar (FK) e o que dispara o recat.
			if pl.mudaTipo {
				if err := E.Acoes.TrocarTipoMaterial(ctx, pl.id, &pl.tipoNovo); err != nil {
					return feito{}, err
				}
			}
			if pl.mudaTitulo {
				if err := E.Acoes.Materiais.UpdateTitle(ctx, pl.id, pl.titulo); err != nil {
					if errors.Is(err, pgx.ErrNoRows) {
						return feito{}, errNaoEncontrado("material")
					}
					return feito{}, err
				}
			}
			if pl.mudaRoteiro {
				if err := E.Acoes.Materiais.UpdateScript(ctx, pl.id, pl.roteiro); err != nil {
					if errors.Is(err, pgx.ErrNoRows) {
						return feito{}, errNaoEncontrado("material")
					}
					return feito{}, err
				}
			}
			saida, err := lerMaterialCompleto(ctx, E.Leitura, pl.id)
			if err != nil {
				return feito{}, err
			}
			f := feito{Resultado: saida, Recurso: "material", RecursoID: &pl.id}
			if pl.mudaTipo {
				f.Ressalvas = []string{ressalvaRecatMaterial}
			}
			return f, nil
		},
	}
}
