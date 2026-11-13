package mcp

// programar_campanha (2026-11-11), o preço confirma em campanha sem veiculação e o
// perfil /mcp/programacao.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
	"radiocheck/internal/mcp/tokens"
)

// cenarioProg: um cliente, quatro emissoras (E4 fica fora da campanha) e dois tipos.
type cenarioProg struct {
	cli, e1, e2, e3, e4 uuid.UUID
	spot, aoVivo        uuid.UUID
}

func semearProg(t *testing.T, b *bancadaTool) cenarioProg {
	t.Helper()
	return cenarioProg{
		cli:    mcptest.Cliente(t, b.ctx, b.pool, "Fenac "+uuid.NewString()[:6]),
		e1:     mcptest.Emissora(t, b.ctx, b.pool, "98 FM", 1000),
		e2:     mcptest.Emissora(t, b.ctx, b.pool, "Campina", 1000),
		e3:     mcptest.Emissora(t, b.ctx, b.pool, "Coqueiros", 1000),
		e4:     mcptest.Emissora(t, b.ctx, b.pool, "Fora", 1000),
		spot:   mcptest.TipoMaterial(t, b.ctx, b.pool, "Spot 30\""),
		aoVivo: mcptest.TipoMaterial(t, b.ctx, b.pool, "Ao vivo"),
	}
}

func regraProg(nome string, tipo uuid.UUID, emissoras []any, inicio, fim string, dias []any, porDia int) map[string]any {
	return map[string]any{
		"nome": nome, "tipo_id": tipo.String(), "emissora_ids": emissoras,
		"inicio": inicio, "fim": fim, "dias": dias,
		"inicio_horario": "07:00", "fim_horario": "19:00", "insercoes_por_dia": porDia,
	}
}

var segASex = []any{"seg", "ter", "qua", "qui", "sex"}

// argsFenac: o caso real da FENAC (PI 6966 + 6967), com hoje = 2026-10-29 (agoraFixa).
func argsFenac(c cenarioProg) map[string]any {
	todas := strsDe(c.e1, c.e2, c.e3)
	return map[string]any{
		"cliente_id": c.cli.String(), "nome": "341 Fenac | Midia 2027 | Nov/26 e Jan/27",
		"inicio": "2026-11-23", "fim": "2027-01-29", "emissora_ids": todas,
		"regras": []any{
			regraProg("PI 6966 - Novembro", c.spot, todas, "2026-11-23", "2026-11-27", segASex, 2),
			regraProg("PI 6967 - Janeiro", c.spot, todas, "2027-01-18", "2027-01-29", segASex, 2),
		},
		"precos": []any{
			precoPorInsercao(c.e1, valorDoTipo(c.spot, 56.25)),
			precoPorInsercao(c.e2, valorDoTipo(c.spot, 68.02)),
			precoConsolidado(c.e3, 885),
		},
	}
}

type contagemProg struct{ Campanhas, Regras, Precos, PrecosPorTipo int }

func contarProg(t *testing.T, b *bancadaTool) contagemProg {
	t.Helper()
	var c contagemProg
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT
		(SELECT count(*) FROM campaigns),
		(SELECT count(*) FROM distribution_rules),
		(SELECT count(*) FROM campaign_station_pricing),
		(SELECT count(*) FROM campaign_station_type_pricing)`).Scan(&c.Campanhas, &c.Regras, &c.Precos, &c.PrecosPorTipo))
	return c
}

func TestProgramarCampanha_PreviewConfereEConfirmarGravaTudo(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearProg(t, b)
	args := argsFenac(c)
	require.Contains(t, propriedades(esquemaDa(t, b.cfg, "programar_campanha")), "confirmacao")

	pre := b.preview(t, "programar_campanha", args)

	require.Equal(t, contagemProg{}, contarProg(t, b), "o preview não escreve")
	require.Empty(t, b.hub.Eventos())
	texto := textoDoPreview(t, pre)
	// A conferência do servidor: 10 (nov) + 20 (jan) = 30 por emissora.
	require.Contains(t, texto, "30 inserções × R$ 56,25 = R$ 1.687,50", "98 FM por inserção")
	require.Contains(t, texto, "30 inserções × R$ 68,02 = R$ 2.040,60", "Campina por inserção")
	require.Contains(t, texto, "30 inserções; consolidado: R$ 885,00", "Coqueiros consolidado")
	require.Contains(t, texto, "90 inserções; valor previsto R$ 4.613,10")
	require.Contains(t, texto, "(5 dias)")
	require.Contains(t, texto, "(10 dias)")
	require.Contains(t, texto, "transação só")
	require.NotContains(t, texto, "SOMAM", "as duas regras não se sobrepõem")

	out := b.confirmarEExecutar(t, "programar_campanha", args)

	require.Equal(t, contagemProg{Campanhas: 1, Regras: 2, Precos: 3, PrecosPorTipo: 2}, contarProg(t, b))
	res := out["resultado"].(map[string]any)
	camp := res["campanha"].(map[string]any)["campanha"].(map[string]any)
	id := uuid.MustParse(camp["id"].(string))
	require.Equal(t, "programada", camp["estado"])
	require.Len(t, res["regras"], 2)
	require.EqualValues(t, 3, res["precos_gravados"])
	require.EqualValues(t, 90, res["totais"].(map[string]any)["insercoes"])
	require.InDelta(t, 4613.10, res["totais"].(map[string]any)["valor_previsto"], 0.001)

	var regras []catalog.DistributionRule
	regras, err := catalog.NewDistributionRules(b.pool).ListByCampaign(b.ctx, id)
	require.NoError(t, err)
	require.Len(t, regras, 2)
	require.Equal(t, "PI 6966 - Novembro", regras[0].Name)
	require.EqualValues(t, 62, regras[0].WeekdayMask, "seg a sex")
	require.Equal(t, "07:00", regras[0].TimeStart)
	require.Equal(t, []uuid.UUID{c.e1, c.e2, c.e3}, regras[0].StationIDs)
	require.Len(t, b.hub.EventosDoTipo("campanha.upsert"), 1, "a programação avisa o hub uma vez")
	b.exigeAuditoria(t, "mcp.escrita.programar_campanha", "campaign", &id)
}

// O tudo-ou-nada é da transação: uma falha do banco no meio (aqui, a FK do tipo na
// 2ª regra) não deixa a campanha nem a 1ª regra gravadas.
func TestProgramacaoDeCampanha_FalhaNoMeioNaoGravaNada(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearProg(t, b)
	ini, _ := time.Parse("2006-01-02", "2026-11-23")
	fim, _ := time.Parse("2006-01-02", "2026-11-27")
	regra := func(tipo uuid.UUID) catalog.CreateDistributionRuleInput {
		return catalog.CreateDistributionRuleInput{TypeID: tipo, StationIDs: []uuid.UUID{c.e1}, StartDate: ini, EndDate: fim,
			WeekdayMask: 62, TimeStart: "07:00", TimeEnd: "19:00", PlaysPerDay: 2}
	}
	_, err := catalog.NewProgramacaoDeCampanha(b.pool).Gravar(b.ctx, catalog.ProgramacaoInput{
		Campanha: catalog.CreateCampaignInput{ClientID: c.cli, Name: "Meio do caminho", StartDate: ini, EndDate: fim, TargetStations: []uuid.UUID{c.e1}},
		Regras:   []catalog.CreateDistributionRuleInput{regra(c.spot), regra(uuid.New())},
	})
	require.Error(t, err)
	require.Equal(t, contagemProg{}, contarProg(t, b), "nada fica gravado")
}

// Início no passado: um pedido pendente só; aprovado, grava tudo de uma vez.
func TestProgramarCampanha_InicioPassadoViraPedidoUnicoEAprovarGravaTudo(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b) // hoje = 2026-10-29
	c := semearProg(t, b)
	args := argsFenac(c)
	args["inicio"] = "2026-10-20"
	regras := args["regras"].([]any)
	regras[0].(map[string]any)["inicio"] = "2026-10-20"

	out := b.pedePendente(t, "programar_campanha", args)

	require.Contains(t, out["motivo"], "ativa a campanha em até 60 s")
	require.Equal(t, contagemProg{}, contarProg(t, b), "o pedido não escreve")
	ap := b.aprovar(t, out["pendente_id"].(string))
	require.Equal(t, contagemProg{Campanhas: 1, Regras: 2, Precos: 3, PrecosPorTipo: 2}, contarProg(t, b))
	require.EqualValues(t, 3, ap.resultadoJSON(t)["precos_gravados"])
}

func TestProgramarCampanha_Recusas(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearProg(t, b)
	casos := map[string]struct {
		mexe   func(a map[string]any)
		espera string
	}{
		"sem regras":    {func(a map[string]any) { a["regras"] = []any{} }, "informe ao menos uma regra"},
		"sem emissoras": {func(a map[string]any) { a["emissora_ids"] = []any{} }, "informe as emissoras-alvo"},
		"emissora da regra fora da campanha": {func(a map[string]any) {
			a["regras"].([]any)[1].(map[string]any)["emissora_ids"] = strsDe(c.e1, c.e4)
		}, `regras[1] ("PI 6967 - Janeiro"): emissora(s) fora da campanha`},
		"regra fora do período": {func(a map[string]any) {
			a["regras"].([]any)[0].(map[string]any)["inicio"] = "2026-11-20"
		}, `regras[0] ("PI 6966 - Novembro"): a vigência da regra`},
		"regra sem dia nenhum na vigência": {func(a map[string]any) {
			a["regras"].([]any)[0].(map[string]any)["dias"] = []any{"sab", "dom"}
			a["regras"].([]any)[0].(map[string]any)["fim"] = "2026-11-27"
		}, "a regra nunca valeria"},
		"preço de emissora fora da campanha": {func(a map[string]any) {
			a["precos"] = []any{precoConsolidado(c.e4, 10)}
		}, "precos: a emissora"},
		"preço com 3 casas": {func(a map[string]any) {
			a["precos"] = []any{precoPorInsercao(c.e1, valorDoTipo(c.spot, 26.862))}
		}, "no máximo 2 casas decimais"},
	}
	for nome, caso := range casos {
		args := argsFenac(c)
		caso.mexe(args)
		texto := b.chamarErro(t, "programar_campanha", args)
		require.Contains(t, texto, caso.espera, nome)
	}
	require.Equal(t, contagemProg{}, contarProg(t, b))
}

func TestProgramarCampanha_RessalvasDoPreview(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearProg(t, b)
	args := argsFenac(c)
	args["emissora_ids"] = strsDe(c.e1, c.e2, c.e3, c.e4)
	regras := args["regras"].([]any)
	// Uma regra repetida (o erro mais comum) e um testemunhal sem preço do tipo.
	args["regras"] = append(regras, regras[0],
		regraProg("Testemunhal", c.aoVivo, strsDe(c.e1), "2026-11-24", "2026-11-24", segASex, 1))
	args["precos"] = []any{precoPorInsercao(c.e1, valorDoTipo(c.spot, 56.25))}

	texto := textoDoPreview(t, b.preview(t, "programar_campanha", args))

	require.Contains(t, texto, "regras[0] e regras[2] (Spot 30")
	require.Contains(t, texto, "valem juntas em 5 dia(s) em 3 emissora(s): a meta desses dias é a SOMA (2 + 2 = 4 por dia)")
	require.Contains(t, texto, "1 emissora(s)-alvo sem nenhuma regra")
	require.Contains(t, texto, "3 emissora(s)-alvo sem preço")
	require.Regexp(t, `Tipo com regra mas sem valor unitário[^"]*Ao vivo[^"]* em 98 FM`, texto)
	require.Contains(t, texto, "Sem codigo_hub")
}

// O nome repetido no mesmo cliente aparece no preview (o modelo NÃO deve programar de novo).
func TestProgramarCampanha_AvisaCampanhaDuplicada(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearProg(t, b)
	b.confirmarEExecutar(t, "programar_campanha", argsFenac(c))
	texto := textoDoPreview(t, b.preview(t, "programar_campanha", argsFenac(c)))
	require.Contains(t, texto, "já tem uma campanha com este nome")
}

// ── editar_precos_campanha: confirma sem veiculação, crítico com ─────────

func TestEditarPrecosCampanha_FuturaSemVeiculacaoEhConfirma(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearCritCamp(t, b, "programada", uuid.Nil) // nenhuma veiculação; começa em 2026-10-01
	b.E.Agora = func() time.Time { return mcptest.Quando("2026-09-15", "12:00") }
	args := argsPrecos(c.camp, precoConsolidado(c.e2, 1500.75))

	// Recusa no caminho confirma: o mesmo preço já gravado é sem_mudanca.
	require.Contains(t, b.chamarErro(t, "editar_precos_campanha", argsPrecos(c.camp, precoPorInsercao(c.e1, valorDoTipo(c.tipo, 10)))), "sem_mudanca")

	out := b.confirmarEExecutar(t, "editar_precos_campanha", args)

	require.EqualValues(t, 1, out["resultado"].(map[string]any)["gravadas"])
	require.Len(t, linhasDePreco(t, b, c.camp), 2)
	require.Empty(t, b.auditoria(t, "mcp.pendente.criada"))

	// Com uma veiculação aprovada, o mesmo pedido (outro valor) vira pendente.
	comVeiculacao(t, b, c)
	pend := b.pedePendente(t, "editar_precos_campanha", argsPrecos(c.camp, precoConsolidado(c.e2, 99)))
	require.Contains(t, pend["motivo"], "já tem veiculação aprovada")
}

// A campanha que já começou é crítica mesmo SEM veiculação: o /insights mostra o investido
// contratado desde o primeiro dia (achado da revisão de 2026-11-11).
func TestEditarPrecosCampanha_JaComecouSemVeiculacaoEhCritico(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b) // hoje = 2026-10-29; a campanha começou em 2026-10-01
	c := semearCritCamp(t, b, "ativa", uuid.Nil)
	pend := b.pedePendente(t, "editar_precos_campanha", argsPrecos(c.camp, precoConsolidado(c.e2, 99)))
	require.Contains(t, pend["motivo"], "a campanha já começou")
}

func TestDiasCobertos_FormaFechadaIgualAoLaco(t *testing.T) {
	ini, _ := time.Parse("2006-01-02", "2026-11-01")
	for dias := 0; dias < 40; dias++ {
		fim := ini.AddDate(0, 0, dias)
		for mascara := int16(0); mascara < 128; mascara++ {
			esperado := 0
			for d := ini; !d.After(fim); d = d.AddDate(0, 0, 1) {
				if (1<<int(d.Weekday()))&int(mascara) != 0 {
					esperado++
				}
			}
			require.Equal(t, esperado, diasCobertos(ini, fim, mascara), "%d dias, máscara %d", dias+1, mascara)
		}
	}
	longe, _ := time.Parse("2006-01-02", "2100-12-31")
	require.Equal(t, 0, diasCobertos(longe, ini, 127), "fim antes do início")
}

func TestEditarPrecosCampanha_DescricaoDizQuandoEscala(t *testing.T) {
	for _, tl := range listarToolsDoServidor(t) {
		if tl.Name != "editar_precos_campanha" {
			continue
		}
		require.Contains(t, tl.Description, "Sobe para crítico quando a campanha já começou")
		require.Contains(t, tl.Description, "ou já tem veiculação aprovada")
		require.Contains(t, tl.Description, "aprovar_em")
		require.Contains(t, tl.Description, "Nunca invente ids")
		require.True(t, strings.HasSuffix(tl.Description, " Nível: confirma."))
		return
	}
	t.Fatal("editar_precos_campanha não está no tools/list")
}

// ── perfil /mcp/programacao ─────────────────────────────────────────────

func TestPerfilProgramacao_SoAsToolsDoPerfil(t *testing.T) {
	require.True(t, slices.IsSorted(ToolsPerfilProgramacao))
	for _, n := range ToolsPerfilProgramacao {
		require.Contains(t, ToolsEsperadas, n, "o perfil só pode citar tool que existe")
	}
	ctx := context.Background()
	ct, st := sdk.NewInMemoryTransports()
	_, err := novoServidor(ConfigServidor{Versao: "teste", Perfil: ToolsPerfilProgramacao}, principalTeste).Connect(ctx, st, nil)
	require.NoError(t, err)
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "perfil", Version: "1"}, nil).Connect(ctx, ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { cs.Close() })
	res, err := cs.ListTools(ctx, nil)
	require.NoError(t, err)
	nomes := make([]string, len(res.Tools))
	for i, tl := range res.Tools {
		nomes[i] = tl.Name
	}
	require.Equal(t, ToolsPerfilProgramacao, nomes)

	// Bem menor que o catálogo inteiro (o motivo do perfil).
	tamanho := func(tools []*sdk.Tool) int { b, _ := json.Marshal(tools); return len(b) }
	require.Less(t, tamanho(res.Tools)*3, tamanho(listarToolsDoServidor(t)), "o perfil tem de ser bem menor que o tools/list inteiro")
}

func TestPerfilProgramacao_PontaAPonta(t *testing.T) {
	ctx, pool := mcptest.DB(t)
	m := NovoModulo(DepsModulo{Pool: pool, Segredo: []byte(mcptest.SegredoTeste), Origem: "https://api.teste", Portal: "https://portal.teste", Versao: "teste", Log: zap.NewNop()})
	require.NotNil(t, m)
	r := chi.NewRouter()
	m.MontarPublico(r)
	srv := httptest.NewServer(r)
	defer srv.Close()
	claro, _, err := tokens.NovoStore(pool, []byte(mcptest.SegredoTeste)).Emitir(ctx, tokens.Novo{
		UserID: mcptest.Usuario(t, ctx, pool, "admin", true), Tipo: tokens.TipoPessoal, Nome: "t", Validade: time.Hour})
	require.NoError(t, err)

	listar := func(caminho, bearer string) (*http.Response, []byte) {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+caminho, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Mcp-Protocol-Version", "2025-06-18")
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		corpo, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		return res, corpo
	}

	// Sem bearer: 401 apontando os metadados DO PERFIL (o cliente confere o resource).
	sem, _ := listar("/mcp/programacao", "")
	require.Equal(t, http.StatusUnauthorized, sem.StatusCode)
	require.Contains(t, sem.Header.Get("WWW-Authenticate"), `resource_metadata="https://api.teste/.well-known/oauth-protected-resource/mcp/programacao"`)

	// O mesmo token vale nos dois endereços; o perfil lista só as tools dele.
	ok, corpo := listar("/mcp/programacao", claro)
	require.Equal(t, http.StatusOK, ok.StatusCode)
	var lista struct {
		Result struct {
			Tools []struct{ Name string } `json:"tools"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(corpo, &lista), string(corpo))
	require.Len(t, lista.Result.Tools, len(ToolsPerfilProgramacao))
	completo, corpoCompleto := listar("/mcp", claro)
	require.Equal(t, http.StatusOK, completo.StatusCode)
	require.Less(t, len(corpo)*3, len(corpoCompleto), "o perfil é bem menor que o tools/list inteiro")

	res, err := http.Get(srv.URL + "/.well-known/oauth-protected-resource/mcp/programacao")
	require.NoError(t, err)
	defer res.Body.Close()
	var meta map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&meta))
	require.Equal(t, "https://api.teste/mcp/programacao", meta["resource"])
}
