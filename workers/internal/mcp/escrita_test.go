package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"radiocheck/internal/acoes"
	"radiocheck/internal/mcp/mcptest"
)

// entradaEscritaTeste é o In das escritas de teste: embute o campo reservado
// do motor, como toda tool confirma/escalável.
type entradaEscritaTeste struct {
	CampoConfirmacao
	Valor string `json:"valor" jsonschema:"Um valor qualquer"`
}

// sondaEscrita conta e programa o que o Executar/Escalar de teste fazem.
type sondaEscrita struct {
	execucoes int
	escalada  Escalada
	erroExec  error
	panica    bool
	resultado any
}

func (s *sondaEscrita) escrita(nome string, nivel Nivel, escalavel bool) Escrita[entradaEscritaTeste] {
	d := Escrita[entradaEscritaTeste]{
		Nome: nome, Descricao: "Escrita de teste.", Nivel: nivel,
		Preview: func(_ context.Context, _ *Escritas, in entradaEscritaTeste) (previa, error) {
			return previa{Resumo: "vai gravar " + in.Valor}, nil
		},
		Executar: func(_ context.Context, _ *Escritas, _ *Principal, in entradaEscritaTeste) (feito, error) {
			s.execucoes++
			if s.panica {
				panic("boom")
			}
			if s.erroExec != nil {
				return feito{}, s.erroExec
			}
			id := uuid.New()
			var res any = map[string]any{"valor": in.Valor}
			if s.resultado != nil {
				res = s.resultado
			}
			return feito{Resultado: res, Recurso: "teste", RecursoID: &id}, nil
		},
		Tela: func(_ *Escritas, _ entradaEscritaTeste) *string {
			u := "https://portal.teste/tela"
			return &u
		},
	}
	if escalavel {
		d.Escalar = func(_ context.Context, _ *Escritas, _ entradaEscritaTeste) (Escalada, error) {
			return s.escalada, nil
		}
	}
	return d
}

// bancadaEscrita é o banco de teste mais o relógio que o motor consulta.
type bancadaEscrita struct {
	ctx   context.Context
	pool  *pgxpool.Pool
	E     *Escritas
	agora time.Time
}

func novaBancadaEscrita(t *testing.T) *bancadaEscrita {
	t.Helper()
	ctx, pool := mcptest.DB(t)
	_, err := pool.Exec(ctx, `DELETE FROM audit_log`) // o mcptest não trunca o audit_log
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `TRUNCATE mcp_confirmacoes_usadas`)
	require.NoError(t, err)
	b := &bancadaEscrita{ctx: ctx, pool: pool, agora: time.Now()}
	b.E = &Escritas{
		Confirmacoes: &Confirmacoes{DB: pool}, Segredo: []byte(mcptest.SegredoTeste), DB: pool,
		Agora: func() time.Time { return b.agora },
	}
	return b
}

func (b *bancadaEscrita) servidor(p *Principal, ds ...Escrita[entradaEscritaTeste]) *sdk.Server {
	cfg := ConfigServidor{Versao: "teste", Escritas: b.E}
	s := sdk.NewServer(&sdk.Implementation{Name: "t", Version: "1"}, nil)
	for _, d := range ds {
		ferramentaEscrita(s, cfg, p, d)
	}
	return s
}

// chama devolve o JSON do resultado e exige que NÃO seja isError.
func chama(t *testing.T, s *sdk.Server, nome string, args any) map[string]any {
	t.Helper()
	texto, erro := chamarNoServidor(t, s, nome, args)
	require.False(t, erro, "%s devolveu erro: %s", nome, texto)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(texto), &out), texto)
	return out
}

// chamaErro exige isError e devolve o texto.
func chamaErro(t *testing.T, s *sdk.Server, nome string, args any) string {
	t.Helper()
	texto, erro := chamarNoServidor(t, s, nome, args)
	require.True(t, erro, "%s deveria falhar, devolveu: %s", nome, texto)
	return texto
}

func (b *bancadaEscrita) auditorias(t *testing.T, acao string) int {
	t.Helper()
	var n int
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT count(*) FROM audit_log WHERE action = $1`, acao).Scan(&n))
	return n
}

func (b *bancadaEscrita) confirmacoesUsadas(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT count(*) FROM mcp_confirmacoes_usadas`).Scan(&n))
	return n
}

// 1
func TestEscrita_LivreExecutaNaPrimeiraChamadaEAudita(t *testing.T) {
	b := novaBancadaEscrita(t)
	sonda := &sondaEscrita{}
	s := b.servidor(principalTeste, sonda.escrita("escreve_livre", Livre, false))

	out := chama(t, s, "escreve_livre", map[string]any{"valor": "x"})
	require.Equal(t, true, out["executou"])
	require.Equal(t, map[string]any{"valor": "x"}, out["resultado"])
	require.Equal(t, 1, sonda.execucoes)

	var ator uuid.UUID
	var recurso string
	var meta map[string]any
	require.NoError(t, b.pool.QueryRow(b.ctx,
		`SELECT actor_id, resource, metadata FROM audit_log WHERE action = 'mcp.escrita.escreve_livre'`).Scan(&ator, &recurso, &meta))
	require.Equal(t, principalTeste.UserID, ator)
	require.Equal(t, "teste", recurso)
	require.Equal(t, "mcp", meta["origem"])
	require.Equal(t, "escreve_livre", meta["tool"])
	require.Equal(t, "livre", meta["nivel"])
	require.Equal(t, principalTeste.TokenID.String(), meta["mcp"].(map[string]any)["token_id"])
	require.Equal(t, map[string]any{"valor": "x"}, meta["args"])
}

// 2 + 3
func TestEscrita_ConfirmaPedePreviewENaSegundaExecutaUmaVez(t *testing.T) {
	b := novaBancadaEscrita(t)
	sonda := &sondaEscrita{}
	s := b.servidor(principalTeste, sonda.escrita("escreve_conf", Confirma, false))

	pre := chama(t, s, "escreve_conf", map[string]any{"valor": "x"})
	require.Equal(t, false, pre["executou"])
	require.Equal(t, "vai gravar x", pre["preview"].(map[string]any)["resumo"])
	tok, _ := pre["confirmacao"].(string)
	require.NotEmpty(t, tok)
	require.Equal(t, instante(b.agora.Add(ttlConfirmacao)), pre["expira_em"])
	require.Equal(t, 0, sonda.execucoes, "o preview não executa")
	require.Equal(t, 0, b.auditorias(t, "mcp.escrita.escreve_conf"), "o preview não audita")
	require.Equal(t, 0, b.confirmacoesUsadas(t))

	out := chama(t, s, "escreve_conf", map[string]any{"valor": "x", "confirmacao": tok})
	require.Equal(t, true, out["executou"])
	require.Equal(t, 1, sonda.execucoes)
	require.Equal(t, 1, b.auditorias(t, "mcp.escrita.escreve_conf"))
	require.Equal(t, 1, b.confirmacoesUsadas(t))
}

// 4
func TestEscrita_ReusoDoTokenNaoEhErroENaoExecutaDeNovo(t *testing.T) {
	b := novaBancadaEscrita(t)
	sonda := &sondaEscrita{}
	s := b.servidor(principalTeste, sonda.escrita("escreve_conf", Confirma, false))
	tok := chama(t, s, "escreve_conf", map[string]any{"valor": "x"})["confirmacao"].(string)

	require.Equal(t, true, chama(t, s, "escreve_conf", map[string]any{"valor": "x", "confirmacao": tok})["executou"])
	de2 := chama(t, s, "escreve_conf", map[string]any{"valor": "x", "confirmacao": tok})
	require.Equal(t, false, de2["executou"])
	require.Contains(t, de2["erro"], "já foi usada")
	require.Contains(t, de2["erro"], "Peça o preview outra vez")
	require.Equal(t, 1, sonda.execucoes)
}

// 5
func TestEscrita_TokenInvalidoNaoEhErro(t *testing.T) {
	b := novaBancadaEscrita(t)
	sonda := &sondaEscrita{}
	s := b.servidor(principalTeste, sonda.escrita("escreve_conf", Confirma, false))
	tok := chama(t, s, "escreve_conf", map[string]any{"valor": "x"})["confirmacao"].(string)

	semEfeito := func(rotulo string, srv *sdk.Server, args map[string]any) {
		out := chama(t, srv, "escreve_conf", args)
		require.Equal(t, false, out["executou"], rotulo)
		require.Contains(t, out["erro"], "inválida ou expirada", rotulo)
		require.Contains(t, out["erro"], "Peça o preview outra vez", rotulo)
	}
	semEfeito("args mudados", s, map[string]any{"valor": "y", "confirmacao": tok})
	semEfeito("lixo", s, map[string]any{"valor": "x", "confirmacao": "lixo"})
	outro := b.servidor(&Principal{UserID: uuid.New(), TokenID: uuid.New()}, sonda.escrita("escreve_conf", Confirma, false))
	semEfeito("token de outro usuário", outro, map[string]any{"valor": "x", "confirmacao": tok})

	b.agora = b.agora.Add(11 * time.Minute)
	semEfeito("vencido", s, map[string]any{"valor": "x", "confirmacao": tok})

	require.Equal(t, 0, sonda.execucoes)
	require.Equal(t, 0, b.confirmacoesUsadas(t), "token inválido não consome nada")
}

// 6
func TestEscrita_FalhaDeNegocioDevolveCodigoELiberaOToken(t *testing.T) {
	b := novaBancadaEscrita(t)
	sonda := &sondaEscrita{erroExec: &acoes.Recusa{Mensagem: "cliente inexistente", Codigo: "nao_encontrado"}}
	s := b.servidor(principalTeste, sonda.escrita("escreve_conf", Confirma, false))
	tok := chama(t, s, "escreve_conf", map[string]any{"valor": "x"})["confirmacao"].(string)

	texto := chamaErro(t, s, "escreve_conf", map[string]any{"valor": "x", "confirmacao": tok})
	require.Equal(t, "cliente inexistente [nao_encontrado]", texto)
	require.Equal(t, 0, b.confirmacoesUsadas(t), "recusa de negócio libera o token")
	require.Equal(t, 0, b.auditorias(t, "mcp.escrita.escreve_conf"), "nada foi escrito: nada a auditar")

	sonda.erroExec = nil // a causa foi consertada: o MESMO token funciona
	out := chama(t, s, "escreve_conf", map[string]any{"valor": "x", "confirmacao": tok})
	require.Equal(t, true, out["executou"])
	require.Equal(t, 2, sonda.execucoes)
}

// 7
func TestEscrita_ErroInternoEPanicNaoLiberamOTokenENemDerrubamOProcesso(t *testing.T) {
	for _, caso := range []struct {
		nome  string
		sonda *sondaEscrita
	}{
		{"erro interno", &sondaEscrita{erroExec: errors.New("pq: SQLSTATE 23505 tabela secreta")}},
		{"panic", &sondaEscrita{panica: true}},
	} {
		t.Run(caso.nome, func(t *testing.T) {
			b := novaBancadaEscrita(t)
			s := b.servidor(principalTeste, caso.sonda.escrita("escreve_conf", Confirma, false))
			tok := chama(t, s, "escreve_conf", map[string]any{"valor": "x"})["confirmacao"].(string)

			texto := chamaErro(t, s, "escreve_conf", map[string]any{"valor": "x", "confirmacao": tok})
			require.Contains(t, texto, "[interno]")
			require.NotContains(t, texto, "SQLSTATE", "o erro cru não vaza ao modelo")
			require.NotContains(t, texto, "boom")
			require.Equal(t, 1, b.confirmacoesUsadas(t), "não libera: a escrita pode ter gravado algo")

			de2 := chama(t, s, "escreve_conf", map[string]any{"valor": "x", "confirmacao": tok})
			require.Equal(t, false, de2["executou"])
			require.Contains(t, de2["erro"], "já foi usada")
			require.Equal(t, 1, caso.sonda.execucoes)
		})
	}
}

// 8
func TestEscrita_EscaladaNaSegundaChamadaBarraENaoConsomeOToken(t *testing.T) {
	b := novaBancadaEscrita(t)
	sonda := &sondaEscrita{escalada: Escalada{Nivel: Confirma, Motivo: "x"}}
	s := b.servidor(principalTeste, sonda.escrita("escreve_esc", Livre, true))

	pre := chama(t, s, "escreve_esc", map[string]any{"valor": "x"})
	require.Equal(t, false, pre["executou"])
	tok := pre["confirmacao"].(string)

	// Entre o preview e a confirmação o estado mudou: agora é crítico.
	sonda.escalada = Escalada{Nivel: Critico, Motivo: "entrou uma tocada no dia"}
	out := chama(t, s, "escreve_esc", map[string]any{"valor": "x", "confirmacao": tok})
	require.Equal(t, false, out["executou"])
	require.Equal(t, "critico", out["nivel"])
	require.Equal(t, "entrou uma tocada no dia", out["motivo"])
	require.Equal(t, 0, sonda.execucoes)
	require.Equal(t, 0, b.confirmacoesUsadas(t), "o token não foi consumido")
}

// 9
func TestEscrita_CriticoEhBarradoComMotivoEAvisoELink(t *testing.T) {
	b := novaBancadaEscrita(t)
	sonda := &sondaEscrita{escalada: Escalada{Nivel: Critico, Motivo: "atinge veiculação já ocorrida"}}
	s := b.servidor(principalTeste, sonda.escrita("escreve_esc", Confirma, true))

	out := chama(t, s, "escreve_esc", map[string]any{"valor": "x"}) // sem isError
	require.Equal(t, false, out["executou"])
	require.Equal(t, "critico", out["nivel"])
	require.Equal(t, "atinge veiculação já ocorrida", out["motivo"])
	require.Equal(t, "vai gravar x", out["preview"].(map[string]any)["resumo"])
	require.Contains(t, out["aviso"], "Nada aconteceu")
	require.Contains(t, out["aviso"], "faça pela tela, logado")
	require.Equal(t, "https://portal.teste/tela", out["links"].(map[string]any)["tela"])
	require.NotContains(t, out, "confirmacao", "crítico não tem token")
	require.Equal(t, 0, sonda.execucoes)
	require.Equal(t, 1, b.auditorias(t, "mcp.escrita.requer_aprovacao"))
	require.Equal(t, 0, b.auditorias(t, "mcp.escrita.escreve_esc"))

	// Declarada crítica (sem escalada) também nunca executa.
	d := sonda.escrita("escreve_crit", Critico, false)
	d.Tela = nil
	out = chama(t, b.servidor(principalTeste, d), "escreve_crit", map[string]any{"valor": "y"})
	require.Equal(t, "critico", out["nivel"])
	require.Contains(t, out, "links")
	require.Nil(t, out["links"].(map[string]any)["tela"], "sem tela: null, não ausente")
	require.Equal(t, 0, sonda.execucoes)
}

type execerQuebrado struct{}

func (execerQuebrado) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("audit_log fora do ar")
}

// 10
func TestEscrita_AuditoriaForaDoArNaoDerrubaAEscritaJaFeita(t *testing.T) {
	b := novaBancadaEscrita(t)
	b.E.DB = execerQuebrado{}
	sonda := &sondaEscrita{}
	s := b.servidor(principalTeste, sonda.escrita("escreve_livre", Livre, false))

	out := chama(t, s, "escreve_livre", map[string]any{"valor": "x"})
	require.Equal(t, true, out["executou"], "a escrita aconteceu: o modelo não pode repetir")
	require.Equal(t, 1, sonda.execucoes)
}

// 11
func TestEscrita_SemEscritasFalhaFechado(t *testing.T) {
	s := sdk.NewServer(&sdk.Implementation{Name: "t", Version: "1"}, nil)
	sonda := &sondaEscrita{}
	ferramentaEscrita(s, ConfigServidor{Versao: "teste"}, principalTeste, sonda.escrita("escreve_livre", Livre, false))
	texto := chamaErro(t, s, "escreve_livre", map[string]any{"valor": "x"})
	require.Contains(t, texto, "[interno]")
	require.Equal(t, 0, sonda.execucoes)
}

// 12
func TestEscrita_DescricaoTerminaNoNivelEAnotacoesDeEscrita(t *testing.T) {
	b := novaBancadaEscrita(t)
	sonda := &sondaEscrita{}
	s := b.servidor(principalTeste,
		sonda.escrita("t_livre", Livre, false),
		sonda.escrita("t_livre_escalavel", Livre, true),
		sonda.escrita("t_confirma", Confirma, false),
		sonda.escrita("t_critico", Critico, false))

	ctx := context.Background()
	ct, st := sdk.NewInMemoryTransports()
	_, err := s.Connect(ctx, st, nil)
	require.NoError(t, err)
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "teste", Version: "1"}, nil).Connect(ctx, ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { cs.Close() })
	lista, err := cs.ListTools(ctx, nil)
	require.NoError(t, err)
	require.Len(t, lista.Tools, 4)

	porNome := map[string]*sdk.Tool{}
	for _, tl := range lista.Tools {
		porNome[tl.Name] = tl
	}
	for nome, nivel := range map[string]string{"t_livre": "livre", "t_livre_escalavel": "livre", "t_confirma": "confirma", "t_critico": "critico"} {
		tl := porNome[nome]
		require.True(t, strings.HasSuffix(tl.Description, " Nível: "+nivel+"."), tl.Description)
		require.False(t, tl.Annotations.ReadOnlyHint, nome)
		require.NotNil(t, tl.Annotations.OpenWorldHint, nome)
		require.False(t, *tl.Annotations.OpenWorldHint, nome)
		require.NotNil(t, tl.Annotations.DestructiveHint, nome)
		require.Equal(t, nome != "t_livre", *tl.Annotations.DestructiveHint,
			"só a livre sem escalada é não-destrutiva: %s", nome)
	}
}

// 13: o roteiro inteiro não vai para o audit_log.
func TestEscrita_AuditoriaCortaTextoLongoEOrdenaAsChaves(t *testing.T) {
	b := novaBancadaEscrita(t)
	sonda := &sondaEscrita{}
	s := b.servidor(principalTeste, sonda.escrita("escreve_livre", Livre, false))

	longo := strings.Repeat("ã", 2000)
	chama(t, s, "escreve_livre", map[string]any{"valor": longo})

	var args map[string]any
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT metadata->'args' FROM audit_log WHERE action = 'mcp.escrita.escreve_livre'`).Scan(&args))
	v := args["valor"].(string)
	require.Equal(t, strings.Repeat("ã", 500)+"…[cortado]", v)
}

// O resultado que não serializa depois de escrito nunca vira erro.
func TestEscrita_ResultadoQueNaoSerializaViraAvisoNaoErro(t *testing.T) {
	b := novaBancadaEscrita(t)
	sonda := &sondaEscrita{resultado: map[string]any{"canal": make(chan int)}}
	s := b.servidor(principalTeste, sonda.escrita("escreve_livre", Livre, false))

	out := chama(t, s, "escreve_livre", map[string]any{"valor": "x"})
	require.Equal(t, true, out["executou"])
	require.Nil(t, out["resultado"])
	require.Contains(t, out["aviso"], "executada")
	require.Equal(t, 1, b.auditorias(t, "mcp.escrita.escreve_livre"))
}

// Ressalvas do Executar chegam à resposta.
func TestEscrita_RessalvasDoExecutarChegamAResposta(t *testing.T) {
	b := novaBancadaEscrita(t)
	d := (&sondaEscrita{}).escrita("escreve_livre", Livre, false)
	d.Executar = func(context.Context, *Escritas, *Principal, entradaEscritaTeste) (feito, error) {
		return feito{Resultado: map[string]any{}, Ressalvas: []string{"a recategorização roda em segundo plano e leva até 30 s"}}, nil
	}
	out := chama(t, b.servidor(principalTeste, d), "escreve_livre", map[string]any{"valor": "x"})
	require.Equal(t, []any{"a recategorização roda em segundo plano e leva até 30 s"}, out["ressalvas"])
}

// A escalada nunca desce do declarado.
func TestEscrita_EscaladaNuncaDesceODeclarado(t *testing.T) {
	b := novaBancadaEscrita(t)
	sonda := &sondaEscrita{escalada: Escalada{Nivel: Livre}}
	s := b.servidor(principalTeste, sonda.escrita("escreve_conf", Confirma, true))
	out := chama(t, s, "escreve_conf", map[string]any{"valor": "x"})
	require.Equal(t, false, out["executou"], "declarada confirma, a escalada 'livre' não a rebaixa")
	require.NotEmpty(t, out["confirmacao"])
	require.Equal(t, 0, sonda.execucoes)
}

// Um token com mais de 200 caracteres nunca é válido: a resposta é a de token
// inválido, sem consumir nada (nem tocar o banco do uso único).
func TestEscrita_ConfirmacaoComMaisDe200CaracteresEhInvalida(t *testing.T) {
	b := novaBancadaEscrita(t)
	sonda := &sondaEscrita{}
	s := b.servidor(principalTeste, sonda.escrita("escreve_conf", Confirma, false))

	out := chama(t, s, "escreve_conf", map[string]any{"valor": "x", "confirmacao": strings.Repeat("a", 201)})
	require.Equal(t, false, out["executou"])
	require.Contains(t, out["erro"], "inválida ou expirada")
	require.Equal(t, 0, sonda.execucoes)
	require.Equal(t, 0, b.confirmacoesUsadas(t))
}

// entradaSemConfirmacao é o erro de programação que o registro detecta: uma
// tool confirma cujo In esqueceu de embutir CampoConfirmacao.
type entradaSemConfirmacao struct {
	Valor string `json:"valor" jsonschema:"Um valor qualquer"`
}

// Sem o campo, o modelo nunca conseguiria devolver o token e giraria em
// previews para sempre: o registro loga o erro (sem panic, regra 6.5) e a
// chamada falha fechada, sem preview.
func TestEscrita_ConfirmaSemCampoConfirmacaoFalhaFechadoELogaNoRegistro(t *testing.T) {
	b := novaBancadaEscrita(t)
	nucleo, logs := observer.New(zapcore.ErrorLevel)
	cfg := ConfigServidor{Versao: "teste", Escritas: b.E, Log: zap.New(nucleo)}

	previews := 0
	d := Escrita[entradaSemConfirmacao]{
		Nome: "tool_mal_formada", Descricao: "Mal formada.", Nivel: Confirma,
		Preview: func(context.Context, *Escritas, entradaSemConfirmacao) (previa, error) {
			previews++
			return previa{Resumo: "x"}, nil
		},
		Executar: func(context.Context, *Escritas, *Principal, entradaSemConfirmacao) (feito, error) {
			t.Fatal("Executar não pode rodar")
			return feito{}, nil
		},
	}
	s := sdk.NewServer(&sdk.Implementation{Name: "t", Version: "1"}, nil)
	require.NotPanics(t, func() { ferramentaEscrita(s, cfg, principalTeste, d) })
	require.Equal(t, 1, logs.FilterMessageSnippet("CampoConfirmacao").Len(), "o erro de configuração é logado no registro")

	texto := chamaErro(t, s, "tool_mal_formada", map[string]any{"valor": "x"})
	require.Contains(t, texto, "[interno]")
	require.Equal(t, 0, previews, "nem o preview roda: o modelo não pode girar em previews")

	// Escalável (livre com Escalar) também precisa do campo.
	nucleo2, logs2 := observer.New(zapcore.ErrorLevel)
	dl := Escrita[entradaSemConfirmacao]{
		Nome: "livre_escalavel_mal_formada", Descricao: "x", Nivel: Livre,
		Escalar: func(context.Context, *Escritas, entradaSemConfirmacao) (Escalada, error) { return Escalada{}, nil },
		Executar: func(context.Context, *Escritas, *Principal, entradaSemConfirmacao) (feito, error) {
			return feito{}, nil
		},
	}
	ferramentaEscrita(sdk.NewServer(&sdk.Implementation{Name: "t", Version: "1"}, nil),
		ConfigServidor{Escritas: b.E, Log: zap.New(nucleo2)}, principalTeste, dl)
	require.Equal(t, 1, logs2.Len())

	// Livre pura e crítica pura não pedem token: sem erro de configuração.
	nucleo3, logs3 := observer.New(zapcore.ErrorLevel)
	cfg3 := ConfigServidor{Escritas: b.E, Log: zap.New(nucleo3)}
	ferramentaEscrita(sdk.NewServer(&sdk.Implementation{Name: "t", Version: "1"}, nil), cfg3, principalTeste,
		Escrita[entradaSemConfirmacao]{Nome: "livre_pura", Descricao: "x", Nivel: Livre,
			Executar: func(context.Context, *Escritas, *Principal, entradaSemConfirmacao) (feito, error) {
				return feito{}, nil
			}})
	ferramentaEscrita(sdk.NewServer(&sdk.Implementation{Name: "t", Version: "1"}, nil), cfg3, principalTeste,
		Escrita[entradaSemConfirmacao]{Nome: "critica_pura", Descricao: "x", Nivel: Critico})
	require.Equal(t, 0, logs3.Len())
}

// Cliente que desconecta DEPOIS de o token ser consumido não pode deixar a
// escrita pela metade nem o token queimado: o Executar roda sem o
// cancelamento do pedido (mas com prazo próprio).
func TestEscrita_ExecutarNaoHerdaOCancelamentoDoPedido(t *testing.T) {
	b := novaBancadaEscrita(t)
	cfg := ConfigServidor{Versao: "teste", Escritas: b.E}

	var errDoCtx error
	var temPrazo bool
	var prazoRestante time.Duration
	d := (&sondaEscrita{}).escrita("escreve_livre", Livre, false)
	d.Executar = func(ctx context.Context, _ *Escritas, _ *Principal, in entradaEscritaTeste) (feito, error) {
		errDoCtx = ctx.Err()
		var dl time.Time
		dl, temPrazo = ctx.Deadline()
		prazoRestante = time.Until(dl)
		return feito{Resultado: map[string]any{"valor": in.Valor}}, nil
	}

	pedido, desconecta := context.WithCancel(b.ctx)
	desconecta() // o cliente já foi embora quando o Executar começa
	in := entradaEscritaTeste{Valor: "x"}
	args, err := argsCanonicos(in)
	require.NoError(t, err)
	res, _, err := executarEAuditar(pedido, cfg, b.E, principalTeste, d, in, args, Livre, "")
	require.NoError(t, err)
	require.False(t, res.IsError, "%v", res.Content)
	require.NoError(t, errDoCtx, "o ctx do Executar não herda o cancelamento do pedido")
	require.True(t, temPrazo, "mas tem prazo próprio")
	require.LessOrEqual(t, prazoRestante, prazoExecucao)
	require.Greater(t, prazoRestante, prazoExecucao-5*time.Second)
	require.Equal(t, 1, b.auditorias(t, "mcp.escrita.escreve_livre"), "a auditoria também roda")
}

// O prazo do Executar existe para o pedido não ficar pendurado para sempre.
// Estourá-lo é falha interna: não libera o token (a escrita pode ter gravado).
func TestEscrita_ExecutarTemPrazoEEstourarNaoLiberaOToken(t *testing.T) {
	b := novaBancadaEscrita(t)
	anterior := prazoExecucao
	prazoExecucao = 100 * time.Millisecond
	t.Cleanup(func() { prazoExecucao = anterior })

	d := (&sondaEscrita{}).escrita("escreve_conf", Confirma, false)
	d.Executar = func(ctx context.Context, _ *Escritas, _ *Principal, _ entradaEscritaTeste) (feito, error) {
		<-ctx.Done()
		return feito{}, ctx.Err()
	}
	s := b.servidor(principalTeste, d)
	tok := chama(t, s, "escreve_conf", map[string]any{"valor": "x"})["confirmacao"].(string)
	texto := chamaErro(t, s, "escreve_conf", map[string]any{"valor": "x", "confirmacao": tok})
	require.Contains(t, texto, "[interno]")
	require.Equal(t, 1, b.confirmacoesUsadas(t))
}
