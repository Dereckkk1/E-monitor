package acoes

import (
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/catalog"
	"radiocheck/internal/hub"
	"radiocheck/internal/mcp/mcptest"
)

// novoCenarioCampanha: o cenário comum com hub e supervisor falsos ligados.
func novoCenarioCampanha(t *testing.T) (*cenario, *mcptest.HubFalso, *mcptest.SupervisorFalso) {
	t.Helper()
	c := novoCenario(t)
	hf := &mcptest.HubFalso{}
	sf := &mcptest.SupervisorFalso{Campanhas: catalog.NewCampaigns(c.pool)}
	c.srv.Hub = hf
	c.srv.Supervisor = sf
	return c, hf, sf
}

func (c *cenario) entradaCampanha(codigo string) CriarCampanhaEntrada {
	return CriarCampanhaEntrada{
		ClienteID: c.cliente, Nome: "Verão 2027", Inicio: dia("2027-01-10"), Fim: dia("2027-02-10"),
		EmissoraIDs: []uuid.UUID{c.emissora}, CodigoHub: codigo,
	}
}

func (c *cenario) hubCodeGravado(t *testing.T, id uuid.UUID) string {
	t.Helper()
	var s *string
	require.NoError(t, c.pool.QueryRow(c.ctx, `SELECT hub_code FROM campaigns WHERE id = $1`, id).Scan(&s))
	if s == nil {
		return ""
	}
	return *s
}

func (c *cenario) campanhaNotificada(t *testing.T, id uuid.UUID) bool {
	t.Helper()
	var n *string
	require.NoError(t, c.pool.QueryRow(c.ctx, `SELECT hub_notified_at::text FROM campaigns WHERE id = $1`, id).Scan(&n))
	return n != nil
}

func TestCriarCampanha_GravaNasceProgramadaEAvisaOHubComOCodigoCanonico(t *testing.T) {
	c, hf, _ := novoCenarioCampanha(t)
	hf.Conferir = func(string) (*hub.CampanhaDoHub, error) {
		return mcptest.CampanhaDoClienteLigado("Verão", c.cliente), nil
	}

	out, err := c.srv.CriarCampanha(c.ctx, c.entradaCampanha("  eh 7k4m2x "))

	require.NoError(t, err)
	require.Equal(t, "programada", out.Status, "nasce programada; quem ativa é o scheduler")
	require.Equal(t, []uuid.UUID{c.emissora}, out.TargetStations)
	require.Equal(t, "EH-7K4M2X", c.hubCodeGravado(t, out.ID))
	require.Equal(t, []string{"EH-7K4M2X"}, hf.Conferidos())
	evs := hf.EventosDoTipo("campanha.upsert")
	require.Len(t, evs, 1)
	dados := evs[0].Dados.(hub.CampanhaUpsert)
	require.Equal(t, out.ID.String(), dados.IDNaPlataforma)
	require.Equal(t, c.cliente.String(), dados.IDClienteNaPlataforma)
	require.Equal(t, "EH-7K4M2X", dados.HubCode)
	// O emissor marca a entrega, que tira a campanha da fila de reemissão.
	require.True(t, c.campanhaNotificada(t, out.ID))
}

func TestCriarCampanha_SemCodigoNaoConsultaOHubMasAvisa(t *testing.T) {
	c, hf, _ := novoCenarioCampanha(t)

	out, err := c.srv.CriarCampanha(c.ctx, c.entradaCampanha(""))

	require.NoError(t, err)
	require.Empty(t, hf.Conferidos())
	require.Equal(t, "", c.hubCodeGravado(t, out.ID))
	require.Len(t, hf.EventosDoTipo("campanha.upsert"), 1)
}

func TestCriarCampanha_SemHubNenhumSegue(t *testing.T) {
	c := novoCenario(t) // Hub nil
	out, err := c.srv.CriarCampanha(c.ctx, c.entradaCampanha("EH-7K4M2X"))
	require.NoError(t, err)
	require.Equal(t, "EH-7K4M2X", c.hubCodeGravado(t, out.ID), "só a normalização local")

	c.srv.Hub = &mcptest.HubFalso{SemConfiguracao: true}
	out, err = c.srv.CriarCampanha(c.ctx, c.entradaCampanha("EH-7K4M2X"))
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, out.ID)
}

func TestCriarCampanha_AsQuatroRecusasDoCodigoDoHub(t *testing.T) {
	outro := uuid.New()
	casos := map[string]struct {
		codigo   string
		conferir func(*cenario) func(string) (*hub.CampanhaDoHub, error)
		mensagem string
	}{
		"texto que não é código": {"abc", nil, "código do hub inválido"},
		"o hub não conhece o código": {"EH-7K4M2X", func(*cenario) func(string) (*hub.CampanhaDoHub, error) {
			return func(string) (*hub.CampanhaDoHub, error) {
				return nil, &hub.Error{Status: http.StatusNotFound, Message: "não achei"}
			}
		}, "código do hub não encontrado"},
		"o código é de outro cliente": {"EH-7K4M2X", func(*cenario) func(string) (*hub.CampanhaDoHub, error) {
			return func(string) (*hub.CampanhaDoHub, error) {
				return mcptest.CampanhaDoClienteLigado("Verão do outro", outro), nil
			}
		}, `esse código é da campanha "Verão do outro", de outro cliente (Cliente do hub)`},
	}
	for nome, caso := range casos {
		t.Run(nome, func(t *testing.T) {
			c, hf, _ := novoCenarioCampanha(t)
			if caso.conferir != nil {
				hf.Conferir = caso.conferir(c)
			}

			_, err := c.srv.CriarCampanha(c.ctx, c.entradaCampanha(caso.codigo))

			r := recusaDe(t, err)
			require.Equal(t, CodigoHub, r.Codigo)
			require.Equal(t, caso.mensagem, r.Mensagem)
			var n int
			require.NoError(t, c.pool.QueryRow(c.ctx, `SELECT count(*) FROM campaigns WHERE client_id = $1 AND name = 'Verão 2027'`, c.cliente).Scan(&n))
			require.Zero(t, n, "recusado não grava")
			require.Empty(t, hf.Eventos(), "recusado não emite")
		})
	}
}

// A ligação (Ligacoes.Fatos) SUBSTITUI a regra de "outro cliente": é o quarto
// desfecho de recusa. O teste do handler (campaigns_ligacao_test.go) cobre a
// mensagem inteira; aqui só se prova que a ação traduz o estado não-ligado em
// Recusa{codigo_hub} com a mensagem da ligação.
func TestConferirCodigoHub_LigacaoIncompletaRecusa(t *testing.T) {
	c, hf, _ := novoCenarioCampanha(t)
	c.srv.Ligacoes = catalog.NewLigacoes(c.pool)
	// O cliente local não tem hub_id → a ligação não está completa.
	hf.Conferir = func(string) (*hub.CampanhaDoHub, error) {
		d := mcptest.CampanhaDoClienteLigado("Verão", c.cliente)
		return d, nil
	}

	_, err := c.srv.ConferirCodigoHub(c.ctx, c.cliente, "EH-7K4M2X")

	r := recusaDe(t, err)
	require.Equal(t, CodigoHub, r.Codigo)
	require.Contains(t, r.Mensagem, "ainda não está ligado")
}

// Decisão 2: hub que não responde (mudo, 401, 429, 5xx) NÃO para o cadastro.
func TestCriarCampanha_HubForaDoArSegueEGravaOCodigoCanonico(t *testing.T) {
	c, hf, _ := novoCenarioCampanha(t)
	hf.Conferir = func(string) (*hub.CampanhaDoHub, error) {
		return nil, &hub.Error{Status: http.StatusServiceUnavailable, Message: "hub fora"}
	}

	out, err := c.srv.CriarCampanha(c.ctx, c.entradaCampanha("eh-7k4m2x"))

	require.NoError(t, err)
	require.Equal(t, "EH-7K4M2X", c.hubCodeGravado(t, out.ID))
}

func TestCriarCampanha_NomeOuClienteVazioEhEntradaInvalida(t *testing.T) {
	c := novoCenario(t)
	e := c.entradaCampanha("")
	e.Nome = ""
	require.Equal(t, CodigoEntradaInvalida, recusaDe(t, func() error { _, err := c.srv.CriarCampanha(c.ctx, e); return err }()).Codigo)
	e = c.entradaCampanha("")
	e.ClienteID = uuid.Nil
	require.Equal(t, CodigoEntradaInvalida, recusaDe(t, func() error { _, err := c.srv.CriarCampanha(c.ctx, e); return err }()).Codigo)
}

func (c *cenario) campanhaComCodigo(t *testing.T, codigo string) *catalog.Campaign {
	t.Helper()
	out, err := catalog.NewCampaigns(c.pool).Create(c.ctx, catalog.CreateCampaignInput{
		ClientID: c.cliente, Name: "Original", StartDate: dia("2027-01-10"), EndDate: dia("2027-02-10"),
		TargetStations: []uuid.UUID{}, HubCode: codigo,
	})
	require.NoError(t, err)
	return out
}

func (c *cenario) editar(nome string, codigo *string) EditarCampanhaEntrada {
	return EditarCampanhaEntrada{Nome: nome, Inicio: dia("2027-01-10"), Fim: dia("2027-02-10"), CodigoHub: codigo}
}

func ptr(s string) *string { return &s }

func TestEditarCampanha_SoOCodigoQueMudaEmiteParaOHub(t *testing.T) {
	c, hf, _ := novoCenarioCampanha(t)
	hf.Conferir = func(string) (*hub.CampanhaDoHub, error) { return mcptest.CampanhaDoClienteLigado("V", c.cliente), nil }
	camp := c.campanhaComCodigo(t, "EH-7K4M2X")

	// Só o nome muda (sem hub_code no pedido): grava e NÃO emite.
	out, err := c.srv.EditarCampanha(c.ctx, camp.ID, c.editar("  Renomeada  ", nil))
	require.NoError(t, err)
	require.Equal(t, "Renomeada", out.Name)
	require.Empty(t, hf.Eventos())

	// Mesmo código (mesmo em outra grafia): não mudou → não emite.
	_, err = c.srv.EditarCampanha(c.ctx, camp.ID, c.editar("Renomeada", ptr("eh7k4m2x")))
	require.NoError(t, err)
	require.Empty(t, hf.Eventos())

	// Código novo: grava canônico, emite uma vez e a campanha volta à fila de reemissão.
	out, err = c.srv.EditarCampanha(c.ctx, camp.ID, c.editar("Renomeada", ptr("eh-2m4k7x")))
	require.NoError(t, err)
	require.Equal(t, "EH-2M4K7X", c.hubCodeGravado(t, camp.ID))
	require.Equal(t, "EH-2M4K7X", *out.HubCode)
	evs := hf.EventosDoTipo("campanha.upsert")
	require.Len(t, evs, 1)
	require.Equal(t, "EH-2M4K7X", evs[0].Dados.(hub.CampanhaUpsert).HubCode)
}

// Apagar o código TEM de emitir: é o evento sem código que faz o hub parar a
// coleta da proposta.
func TestEditarCampanha_ApagarOCodigoEmiteSemCodigo(t *testing.T) {
	c, hf, _ := novoCenarioCampanha(t)
	camp := c.campanhaComCodigo(t, "EH-7K4M2X")

	out, err := c.srv.EditarCampanha(c.ctx, camp.ID, c.editar("Original", ptr("")))

	require.NoError(t, err)
	require.Nil(t, out.HubCode)
	require.Equal(t, "", c.hubCodeGravado(t, camp.ID))
	evs := hf.EventosDoTipo("campanha.upsert")
	require.Len(t, evs, 1)
	require.Equal(t, "", evs[0].Dados.(hub.CampanhaUpsert).HubCode)
}

// Recusado o código, NADA é gravado — nem o nome (resposta de erro com escrita
// parcial é o defeito que só aparece meses depois).
func TestEditarCampanha_CodigoRecusadoNaoGravaNemONome(t *testing.T) {
	c, hf, _ := novoCenarioCampanha(t) // o hub falso não conhece nenhum código (404)
	camp := c.campanhaComCodigo(t, "")

	_, err := c.srv.EditarCampanha(c.ctx, camp.ID, c.editar("Nome que não pode gravar", ptr("EH-7K4M2X")))

	require.Equal(t, CodigoHub, recusaDe(t, err).Codigo)
	var nome string
	require.NoError(t, c.pool.QueryRow(c.ctx, `SELECT name FROM campaigns WHERE id = $1`, camp.ID).Scan(&nome))
	require.Equal(t, "Original", nome)
	require.Empty(t, hf.Eventos())
}

func TestEditarCampanha_ValidaComOsTextosDaRotaENaoEncontrado(t *testing.T) {
	c := novoCenario(t)
	camp := c.campanhaComCodigo(t, "")
	for nome, caso := range map[string]struct {
		in    EditarCampanhaEntrada
		texto string
	}{
		"nome vazio":          {EditarCampanhaEntrada{Nome: "  ", Inicio: dia("2027-01-10"), Fim: dia("2027-02-10")}, "name is required"},
		"sem datas":           {EditarCampanhaEntrada{Nome: "x"}, "start_date and end_date are required"},
		"fim antes do início": {EditarCampanhaEntrada{Nome: "x", Inicio: dia("2027-02-10"), Fim: dia("2027-01-10")}, "end_date must be on or after start_date"},
	} {
		t.Run(nome, func(t *testing.T) {
			_, err := c.srv.EditarCampanha(c.ctx, camp.ID, caso.in)
			r := recusaDe(t, err)
			require.Equal(t, CodigoEntradaInvalida, r.Codigo)
			require.Equal(t, caso.texto, r.Mensagem)
		})
	}

	_, err := c.srv.EditarCampanha(c.ctx, uuid.New(), c.editar("x", nil))
	require.Equal(t, CodigoNaoEncontrado, recusaDe(t, err).Codigo)
	_, err = c.srv.EditarCampanha(c.ctx, uuid.New(), c.editar("x", ptr("")))
	require.Equal(t, CodigoNaoEncontrado, recusaDe(t, err).Codigo, "com código: o Get vem antes da barreira")
}

func TestTrocarEmissorasCampanha_ComSupervisorDelegaEleGrava(t *testing.T) {
	c, _, sf := novoCenarioCampanha(t)
	camp := c.campanhaComCodigo(t, "")
	outra := mcptest.Emissora(t, c.ctx, c.pool, "Outra", 0)

	require.NoError(t, c.srv.TrocarEmissorasCampanha(c.ctx, camp.ID, []uuid.UUID{c.emissora, outra}))

	require.Equal(t, []mcptest.ChamadaSupervisor{
		{Op: "UpdateStations", Campanha: camp.ID, Emissoras: []uuid.UUID{c.emissora, outra}},
	}, sf.Chamadas())
	var ids []uuid.UUID
	require.NoError(t, c.pool.QueryRow(c.ctx, `SELECT target_stations FROM campaigns WHERE id = $1`, camp.ID).Scan(&ids))
	require.ElementsMatch(t, []uuid.UUID{c.emissora, outra}, ids, "o falso grava como o supervisor real")

	// Lista nula = esvazia (a rota é substituição total) e o supervisor recebe lista vazia, não nil.
	require.NoError(t, c.srv.TrocarEmissorasCampanha(c.ctx, camp.ID, nil))
	ultima := sf.Chamadas()[1]
	require.NotNil(t, ultima.Emissoras)
	require.Empty(t, ultima.Emissoras)
}

func TestTrocarEmissorasCampanha_ErrosDoSupervisor(t *testing.T) {
	c, _, sf := novoCenarioCampanha(t)

	sf.ErroUpdateStations = errors.New("supervisor.UpdateStations: campaign not found: x")
	require.Equal(t, CodigoNaoEncontrado, recusaDe(t, c.srv.TrocarEmissorasCampanha(c.ctx, uuid.New(), nil)).Codigo)

	sf.ErroUpdateStations = errors.New("nats caiu")
	err := c.srv.TrocarEmissorasCampanha(c.ctx, uuid.New(), nil)
	require.Error(t, err)
	var r *Recusa
	require.False(t, errors.As(err, &r), "erro do supervisor é falha interna, não recusa de negócio")
}

func TestTrocarEmissorasCampanha_SemSupervisorGravaNoBanco(t *testing.T) {
	c := novoCenario(t) // Supervisor nil
	camp := c.campanhaComCodigo(t, "")

	require.NoError(t, c.srv.TrocarEmissorasCampanha(c.ctx, camp.ID, []uuid.UUID{c.emissora}))

	var ids []uuid.UUID
	require.NoError(t, c.pool.QueryRow(c.ctx, `SELECT target_stations FROM campaigns WHERE id = $1`, camp.ID).Scan(&ids))
	require.Equal(t, []uuid.UUID{c.emissora}, ids)
	require.Equal(t, CodigoNaoEncontrado, recusaDe(t, c.srv.TrocarEmissorasCampanha(c.ctx, uuid.New(), nil)).Codigo)
}

func TestClienteDoHubConfere(t *testing.T) {
	local := uuid.New()
	require.True(t, clienteDoHubConfere(local.String(), local))
	require.True(t, clienteDoHubConfere(" "+local.String()+" ", local), "espaço em volta")
	require.True(t, clienteDoHubConfere(upper(local.String()), local), "maiúsculas")
	require.True(t, clienteDoHubConfere("", local), "ponte ausente")
	require.True(t, clienteDoHubConfere("nao-e-uuid", local), "lixo = ponte ausente, não divergência")
	require.False(t, clienteDoHubConfere(uuid.NewString(), local))
}

func upper(s string) string {
	b := []byte(s)
	for i, ch := range b {
		if ch >= 'a' && ch <= 'z' {
			b[i] = ch - 32
		}
	}
	return string(b)
}
