package mcp

// Task 13: criar_cliente, editar_cliente, editar_pmm_target e editar_emissora.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/api/handlers"
	"radiocheck/internal/catalog"
	"radiocheck/internal/hub"
	"radiocheck/internal/mcp/mcptest"
)

// hubCapturando é um hub de verdade (hub.Client) apontado para um servidor de
// teste que guarda os envelopes: é o que a ROTA HTTP usa (ClientsHandler.Hub é
// *hub.Client), então a paridade compara o que ela mandou com o que o HubFalso
// da tool registrou.
func hubCapturando(t *testing.T) (*hub.Client, chan map[string]any) {
	t.Helper()
	emitiu := make(chan map[string]any, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var env map[string]any
		_ = json.NewDecoder(r.Body).Decode(&env)
		select {
		case emitiu <- env:
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return hub.New(srv.URL, "chave"), emitiu
}

// dadosDoEvento põe o payload (struct ou mapa) em mapa, sem os campos que
// identificam o cliente (ids e CNPJ diferem de propósito entre os dois lados).
func dadosDoEvento(t *testing.T, dados any, sem ...string) map[string]any {
	t.Helper()
	bruto, err := json.Marshal(dados)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(bruto, &m))
	for _, k := range sem {
		delete(m, k)
	}
	return m
}

func contaClientes(t *testing.T, b *bancadaTool) int {
	t.Helper()
	var n int
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT count(*) FROM clients`).Scan(&n))
	return n
}

type linhaCliente struct {
	Nome, CNPJ, Cidade, UF, ContatoNome, ContatoEmail, Telefone, Rotulo, Logo, CEP string
	Ativo                                                                          bool
}

func lerLinhaCliente(t *testing.T, b *bancadaTool, id uuid.UUID) linhaCliente {
	t.Helper()
	var l linhaCliente
	var cnpj, cidade, uf, cn, ce, tel, rot, logo, cep *string
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT name, cnpj, city, state, contact_name, contact_email, phone, target_label, logo_url, cep, is_active
		FROM clients WHERE id = $1`, id).Scan(&l.Nome, &cnpj, &cidade, &uf, &cn, &ce, &tel, &rot, &logo, &cep, &l.Ativo))
	l.CNPJ, l.Cidade, l.UF, l.ContatoNome, l.ContatoEmail, l.Telefone, l.Rotulo, l.Logo, l.CEP =
		textoDoPtr(cnpj), textoDoPtr(cidade), textoDoPtr(uf), textoDoPtr(cn), textoDoPtr(ce), textoDoPtr(tel), textoDoPtr(rot), textoDoPtr(logo), textoDoPtr(cep)
	return l
}

func idDoClientePorCNPJ(t *testing.T, b *bancadaTool, mascarado string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT id FROM clients WHERE cnpj = $1`, mascarado).Scan(&id))
	return id
}

func TestCriarCliente_PreviewNaoEscreveEConfirmarCriaEAvisaOHub(t *testing.T) {
	b := novaBancadaTool(t)
	args := map[string]any{
		"nome": "Cliente Novo", "cnpj": "12345678000190", "cidade": "Uberaba", "uf": "mg",
		"contato_nome": "Maria", "contato_email": "maria@x.com", "telefone": "3499999", "rotulo_target": "  Mulheres 25-45  ",
	}
	require.Contains(t, propriedades(esquemaDa(t, b.cfg, "criar_cliente")), "confirmacao")
	require.NotContains(t, propriedades(esquemaDa(t, b.cfg, "criar_cliente")), "hub_id", "o MCP não grava hub_id")

	pre := b.preview(t, "criar_cliente", args)

	require.Zero(t, contaClientes(t, b), "o preview não escreve")
	require.Empty(t, b.hub.Eventos(), "o preview não avisa o hub")
	require.Zero(t, b.totalAuditorias(t))
	campos := map[string]any{}
	for _, m := range pre["preview"].(map[string]any)["mudancas"].([]any) {
		mm := m.(map[string]any)
		campos[mm["campo"].(string)] = mm["para"]
	}
	require.Equal(t, "12.345.678/0001-90", campos["cnpj"], "o CNPJ sai com a máscara da tela")
	require.Equal(t, "MG", campos["uf"])
	require.Equal(t, "Mulheres 25-45", campos["rotulo_target"], "o rótulo é aparado")

	out := b.confirmarEExecutar(t, "criar_cliente", args)

	require.Equal(t, 1, contaClientes(t, b))
	c := out["resultado"].(map[string]any)["cliente"].(map[string]any) // o formato do obter_cliente
	id := uuid.MustParse(c["id"].(string))
	require.Equal(t, "Cliente Novo", c["nome"])
	require.Equal(t, "12.345.678/0001-90", c["cnpj"])
	require.Equal(t, "Mulheres 25-45", c["rotulo_target"])
	require.Nil(t, c["hub_id"])
	require.NotEmpty(t, out["ressalvas"], "o aviso ao hub é em segundo plano e sem reenvio")
	ev := b.hub.EventosDoTipo("cliente.upsert")
	require.Len(t, ev, 1)
	require.Equal(t, id.String(), dadosDoEvento(t, ev[0].Dados)["idNaPlataforma"])
	b.exigeAuditoria(t, "mcp.escrita.criar_cliente", "client", &id)
}

func TestCriarCliente_ParidadeComARota(t *testing.T) {
	b := novaBancadaTool(t)
	hubRota, emitiu := hubCapturando(t)
	h := &handlers.ClientsHandler{Repo: catalog.NewClients(b.pool), Hub: hubRota}

	var criado catalog.Client
	require.NoError(t, json.Unmarshal(chamarHandlerCorpo(t, h.Create, http.MethodPost, "/clients", map[string]any{
		"name": "Cliente Paridade", "cnpj": "11.222.333/0001-81", "city": "Uberaba", "state": "MG",
		"contact_name": "Maria", "contact_email": "maria@x.com", "phone": "3499999", "target_label": "  Mulheres 25-45  ",
	}, nil, b.admin), &criado))
	b.confirmarEExecutar(t, "criar_cliente", map[string]any{
		"nome": "Cliente Paridade", "cnpj": "99888777000166", "cidade": "Uberaba", "uf": "MG",
		"contato_nome": "Maria", "contato_email": "maria@x.com", "telefone": "3499999", "rotulo_target": "  Mulheres 25-45  ",
	})
	viaTool := idDoClientePorCNPJ(t, b, "99.888.777/0001-66")

	esperado, obtido := lerLinhaCliente(t, b, criado.ID), lerLinhaCliente(t, b, viaTool)
	esperado.CNPJ, obtido.CNPJ = "", "" // diferem de propósito: o CNPJ é único
	require.Equal(t, esperado, obtido, "a mesma linha gravada pela rota e pela tool")

	var env map[string]any
	select {
	case env = <-emitiu:
	case <-time.After(5 * time.Second):
		t.Fatal("a rota não avisou o hub")
	}
	ev := b.hub.EventosDoTipo("cliente.upsert")
	require.Len(t, ev, 1)
	require.Equal(t, "cliente.upsert", env["tipo"])
	// Mesmo evento: tudo igual, menos o que identifica o cliente.
	require.Equal(t, dadosDoEvento(t, env["dados"], "idNaPlataforma", "cnpj"), dadosDoEvento(t, ev[0].Dados, "idNaPlataforma", "cnpj"))
}

func TestCriarCliente_SemHubConfiguradoSoGrava(t *testing.T) {
	b := novaBancadaTool(t)
	b.hub.SemConfiguracao = true

	out := b.confirmarEExecutar(t, "criar_cliente", map[string]any{"nome": "Sem hub"})

	require.Equal(t, 1, contaClientes(t, b))
	require.Empty(t, b.hub.Eventos())
	require.Empty(t, out["ressalvas"], "sem hub não há o que ressalvar")
}

func TestCriarCliente_Recusas(t *testing.T) {
	b := novaBancadaTool(t)
	existente := mcptest.Cliente(t, b.ctx, b.pool, "Cliente Existente")
	_, err := b.pool.Exec(b.ctx, `UPDATE clients SET cnpj = '55.666.777/0001-88' WHERE id = $1`, existente)
	require.NoError(t, err)

	for nome, caso := range map[string]struct {
		args   map[string]any
		codigo string
	}{
		"nome em branco":             {map[string]any{"nome": "   "}, "[entrada_invalida]"},
		"nome longo demais":          {map[string]any{"nome": repete("n", 201)}, "[entrada_invalida]"},
		"CNPJ curto":                 {map[string]any{"nome": "X", "cnpj": "123"}, "[entrada_invalida]"},
		"CNPJ com letras":            {map[string]any{"nome": "X", "cnpj": "12.345.678/0001-9a"}, "[entrada_invalida]"},
		"UF inexistente":             {map[string]any{"nome": "X", "uf": "XX"}, "[entrada_invalida]"},
		"UF de uma letra":            {map[string]any{"nome": "X", "uf": "M"}, "[entrada_invalida]"},
		"e-mail inválido":            {map[string]any{"nome": "X", "contato_email": "nao-e-email"}, "[entrada_invalida]"},
		"e-mail com nome":            {map[string]any{"nome": "X", "contato_email": "Maria <maria@x.com>"}, "[entrada_invalida]"},
		"rótulo longo demais":        {map[string]any{"nome": "X", "rotulo_target": repete("r", 201)}, "[entrada_invalida]"},
		"CNPJ de outro, sem máscara": {map[string]any{"nome": "X", "cnpj": "55666777000188"}, "[cnpj_duplicado]"},
		"CNPJ de outro, com máscara": {map[string]any{"nome": "X", "cnpj": "55.666.777/0001-88"}, "[cnpj_duplicado]"},
	} {
		texto := b.chamarErro(t, "criar_cliente", caso.args)
		require.Contains(t, texto, caso.codigo, nome)
		if caso.codigo == "[cnpj_duplicado]" {
			require.Contains(t, texto, "Cliente Existente", "diz qual cliente já tem o CNPJ")
			require.Contains(t, texto, existente.String())
		}
	}
	require.Equal(t, 1, contaClientes(t, b), "nenhuma recusa criou cliente")
	require.Empty(t, b.hub.Eventos())
	require.Zero(t, b.totalAuditorias(t))
}

func TestCriarCliente_NomeIgualAvisaMasNaoRecusa(t *testing.T) {
	b := novaBancadaTool(t)
	existente := mcptest.Cliente(t, b.ctx, b.pool, "Anunciante Repetido")

	pre := b.preview(t, "criar_cliente", map[string]any{"nome": "ANUNCIANTE REPETIDO"})

	rs := pre["preview"].(map[string]any)["ressalvas"].([]any)
	require.Len(t, rs, 1)
	require.Contains(t, rs[0], existente.String())
}

func semearClienteCompleto(t *testing.T, b *bancadaTool, nome, cnpj string) uuid.UUID {
	t.Helper()
	rot, cidade, uf, cn, ce, tel, logo, cep := "Homens 25-49", "Uberaba", "MG", "Maria", "maria@x.com", "3499999", "http://logo/x.png", "38000-000"
	c, err := catalog.NewClients(b.pool).Create(b.ctx, catalog.CreateClientInput{
		Name: nome, CNPJ: &cnpj, City: &cidade, State: &uf, ContactName: &cn, ContactEmail: &ce, Phone: &tel,
		TargetLabel: &rot, LogoURL: &logo, CEP: &cep,
	})
	require.NoError(t, err)
	return c.ID
}

func TestEditarCliente_ParcialPreviewNaoEscreveEConfirmarGrava(t *testing.T) {
	b := novaBancadaTool(t)
	id := semearClienteCompleto(t, b, "Cliente Antigo", "11.222.333/0001-81")
	antes := lerLinhaCliente(t, b, id)
	args := map[string]any{"cliente_id": id.String(), "cidade": "Uberlândia", "rotulo_target": ""}

	pre := b.preview(t, "editar_cliente", args)

	require.Equal(t, antes, lerLinhaCliente(t, b, id), "o preview não escreve")
	ms := pre["preview"].(map[string]any)["mudancas"].([]any)
	require.Len(t, ms, 2, "só os campos enviados que de fato mudam")
	require.Equal(t, map[string]any{"campo": "cidade", "de": "Uberaba", "para": "Uberlândia"}, ms[0])
	require.Equal(t, map[string]any{"campo": "rotulo_target", "de": "Homens 25-49", "para": nil}, ms[1])
	require.Zero(t, b.totalAuditorias(t))

	out := b.confirmarEExecutar(t, "editar_cliente", args)

	depois := lerLinhaCliente(t, b, id)
	require.Equal(t, "Uberlândia", depois.Cidade)
	require.Equal(t, "", depois.Rotulo, "texto vazio limpa o campo")
	esperado := antes
	esperado.Cidade, esperado.Rotulo = "Uberlândia", ""
	require.Equal(t, esperado, depois, "o resto fica — inclusive logo e CEP, que o MCP nem expõe")
	require.Equal(t, "Uberlândia", out["resultado"].(map[string]any)["cliente"].(map[string]any)["cidade"])
	require.Empty(t, b.hub.Eventos(), "a edição de cliente não avisa o hub")
	require.NotEmpty(t, out["ressalvas"], "avisa que o hub não foi avisado")
	b.exigeAuditoria(t, "mcp.escrita.editar_cliente", "client", &id)
}

func TestEditarCliente_ParidadeComARota(t *testing.T) {
	b := novaBancadaTool(t)
	viaRota := semearClienteCompleto(t, b, "Cliente Paridade", "11.222.333/0001-81")
	viaTool := semearClienteCompleto(t, b, "Cliente Paridade", "99.888.777/0001-66")
	h := &handlers.ClientsHandler{Repo: catalog.NewClients(b.pool)}

	// A rota é PUT total: a mesma edição, com o cadastro inteiro remontado.
	chamarHandlerCorpo(t, h.Update, http.MethodPut, "/clients/x", map[string]any{
		"name": "Nome Novo", "logo_url": "http://logo/x.png", "contact_email": "novo@x.com", "contact_name": "Maria", "phone": "3499999",
		"cnpj": "11.222.333/0001-81", "cep": "38000-000", "city": "Uberaba", "state": "RJ", "target_label": nil,
	}, map[string]string{"id": viaRota.String()}, b.admin)
	b.confirmarEExecutar(t, "editar_cliente", map[string]any{
		"cliente_id": viaTool.String(), "nome": "Nome Novo", "contato_email": "novo@x.com", "uf": "rj", "rotulo_target": "",
	})

	esperado, obtido := lerLinhaCliente(t, b, viaRota), lerLinhaCliente(t, b, viaTool)
	esperado.CNPJ, obtido.CNPJ = "", ""
	require.Equal(t, esperado, obtido)
}

func TestEditarCliente_CNPJ(t *testing.T) {
	b := novaBancadaTool(t)
	a := semearClienteCompleto(t, b, "Cliente A", "11.222.333/0001-81")
	outro := semearClienteCompleto(t, b, "Cliente B", "99.888.777/0001-66")

	// Mesmo CNPJ de outro cliente: recusa com o nome do dono, já no preview.
	texto := b.chamarErro(t, "editar_cliente", map[string]any{"cliente_id": a.String(), "cnpj": "99888777000166"})
	require.Contains(t, texto, "[cnpj_duplicado]")
	require.Contains(t, texto, "Cliente B")
	require.Contains(t, texto, outro.String())

	// Os mesmos 14 dígitos do PRÓPRIO cliente (outra máscara) não são mudança.
	texto = b.chamarErro(t, "editar_cliente", map[string]any{"cliente_id": a.String(), "cnpj": "11222333000181"})
	require.Contains(t, texto, "[entrada_invalida]")

	// CNPJ novo livre: grava com máscara. Vazio apaga.
	b.confirmarEExecutar(t, "editar_cliente", map[string]any{"cliente_id": a.String(), "cnpj": "44555666000177"})
	require.Equal(t, "44.555.666/0001-77", lerLinhaCliente(t, b, a).CNPJ)
	b.confirmarEExecutar(t, "editar_cliente", map[string]any{"cliente_id": a.String(), "cnpj": ""})
	require.Equal(t, "", lerLinhaCliente(t, b, a).CNPJ)

	// Duplicata que JÁ existe (A e B com os mesmos 14 dígitos) não trava a edição de outro campo de B.
	_, err := b.pool.Exec(b.ctx, `UPDATE clients SET cnpj = '99.888.777/0001-66' WHERE id = $1`, a)
	require.NoError(t, err)
	b.confirmarEExecutar(t, "editar_cliente", map[string]any{"cliente_id": outro.String(), "cidade": "Araxá"})
	require.Equal(t, "Araxá", lerLinhaCliente(t, b, outro).Cidade)
}

func TestEditarCliente_Recusas(t *testing.T) {
	b := novaBancadaTool(t)
	id := semearClienteCompleto(t, b, "Cliente X", "11.222.333/0001-81")
	antes := lerLinhaCliente(t, b, id)

	for nome, caso := range map[string]struct {
		args   map[string]any
		codigo string
	}{
		"id inválido":              {map[string]any{"cliente_id": "x", "cidade": "A"}, "[entrada_invalida]"},
		"cliente inexistente":      {map[string]any{"cliente_id": uuid.NewString(), "cidade": "A"}, "[nao_encontrado]"},
		"nenhum campo":             {map[string]any{"cliente_id": id.String()}, "[entrada_invalida]"},
		"nome vazio":               {map[string]any{"cliente_id": id.String(), "nome": ""}, "[entrada_invalida]"},
		"nome em branco":           {map[string]any{"cliente_id": id.String(), "nome": "  "}, "[entrada_invalida]"},
		"CNPJ inválido":            {map[string]any{"cliente_id": id.String(), "cnpj": "12"}, "[entrada_invalida]"},
		"UF inválida":              {map[string]any{"cliente_id": id.String(), "uf": "ZZ"}, "[entrada_invalida]"},
		"e-mail inválido":          {map[string]any{"cliente_id": id.String(), "contato_email": "x"}, "[entrada_invalida]"},
		"rótulo longo demais":      {map[string]any{"cliente_id": id.String(), "rotulo_target": repete("r", 201)}, "[entrada_invalida]"},
		"nada muda":                {map[string]any{"cliente_id": id.String(), "cidade": "Uberaba", "nome": "Cliente X"}, "[entrada_invalida]"},
		"valores já são os atuais": {map[string]any{"cliente_id": id.String(), "contato_nome": "Maria", "telefone": "3499999"}, "[entrada_invalida]"},
	} {
		texto := b.chamarErro(t, "editar_cliente", caso.args)
		require.Contains(t, texto, caso.codigo, nome)
	}
	require.Equal(t, antes, lerLinhaCliente(t, b, id))
	require.Zero(t, b.totalAuditorias(t))
}

// ── editar_pmm_target ───────────────────────────────────────────────────

func linhasPMM(t *testing.T, b *bancadaTool, cliente uuid.UUID) map[uuid.UUID]int {
	t.Helper()
	rows, err := b.pool.Query(b.ctx, `SELECT station_id, pmm_target FROM client_station_pmm WHERE client_id = $1`, cliente)
	require.NoError(t, err)
	defer rows.Close()
	out := map[uuid.UUID]int{}
	for rows.Next() {
		var id uuid.UUID
		var v int
		require.NoError(t, rows.Scan(&id, &v))
		out[id] = v
	}
	require.NoError(t, rows.Err())
	return out
}

// agoraFixa: 29/10/2026, meio-dia em SP (o critério "campanha já iniciada" lê o
// relógio do motor).
func agoraFixa(b *bancadaTool) {
	b.E.Agora = func() time.Time { return mcptest.Quando("2026-10-29", "12:00") }
}

// cenarioPMM: um cliente com uma campanha FUTURA (nada começou: não escala) em E1 e
// E2; E3 não está em campanha nenhuma. E1=1000 e E2=2000 já gravados.
type cenarioPMM struct {
	cli, e1, e2, e3, camp uuid.UUID
}

func semearPMM(t *testing.T, b *bancadaTool, inicio, fim string) cenarioPMM {
	t.Helper()
	c := cenarioPMM{}
	c.cli = mcptest.Cliente(t, b.ctx, b.pool, "Cliente PMM "+uuid.NewString()[:6])
	c.e1 = mcptest.Emissora(t, b.ctx, b.pool, "Rádio 1 "+uuid.NewString()[:6], 0)
	c.e2 = mcptest.Emissora(t, b.ctx, b.pool, "Rádio 2 "+uuid.NewString()[:6], 0)
	c.e3 = mcptest.Emissora(t, b.ctx, b.pool, "Rádio 3 "+uuid.NewString()[:6], 0)
	c.camp = mcptest.Campanha(t, b.ctx, b.pool, c.cli, "Camp PMM", inicio, fim, c.e1, c.e2)
	mcptest.PMMTarget(t, b.ctx, b.pool, c.cli, c.e1, 1000)
	mcptest.PMMTarget(t, b.ctx, b.pool, c.cli, c.e2, 2000)
	return c
}

func entradaPMM(emissora uuid.UUID, v any) map[string]any {
	return map[string]any{"emissora_id": emissora.String(), "pmm_target": v}
}

func TestEditarPMMTarget_PreviewNaoEscreveEConfirmarGrava(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearPMM(t, b, "2026-12-01", "2026-12-31") // campanha futura: não escala
	args := map[string]any{"cliente_id": c.cli.String(), "entradas": []any{
		entradaPMM(c.e1, 1500), entradaPMM(c.e2, nil), entradaPMM(c.e3, 300),
	}}

	pre := b.preview(t, "editar_pmm_target", args)

	require.Equal(t, map[uuid.UUID]int{c.e1: 1000, c.e2: 2000}, linhasPMM(t, b, c.cli), "o preview não escreve")
	pv := pre["preview"].(map[string]any)
	require.Len(t, pv["mudancas"], 3)
	porCampo := map[string]map[string]any{}
	for _, m := range pv["mudancas"].([]any) {
		mm := m.(map[string]any)
		porCampo[mm["campo"].(string)[:len("Rádio 1")]] = mm
	}
	require.EqualValues(t, 1000, porCampo["Rádio 1"]["de"])
	require.EqualValues(t, 1500, porCampo["Rádio 1"]["para"])
	require.EqualValues(t, 2000, porCampo["Rádio 2"]["de"])
	require.Nil(t, porCampo["Rádio 2"]["para"], "null remove")
	require.Nil(t, porCampo["Rádio 3"]["de"])
	require.NotEmpty(t, pv["ressalvas"], "E3 não está em campanha nenhuma do cliente")
	require.Zero(t, b.totalAuditorias(t))

	out := b.confirmarEExecutar(t, "editar_pmm_target", args)

	require.Equal(t, map[uuid.UUID]int{c.e1: 1500, c.e3: 300}, linhasPMM(t, b, c.cli))
	res := out["resultado"].(map[string]any)
	require.EqualValues(t, 2, res["gravadas"])
	require.EqualValues(t, 1, res["removidas"])
	require.Len(t, res["emissoras"], 3)
	linhas := map[string]any{}
	for _, l := range res["emissoras"].([]any) {
		ll := l.(map[string]any)
		linhas[ll["emissora"].(map[string]any)["id"].(string)] = ll["pmm_target"]
	}
	require.EqualValues(t, 1500, linhas[c.e1.String()], "o resultado mostra o valor depois de gravar")
	require.Nil(t, linhas[c.e2.String()])
	require.EqualValues(t, 300, linhas[c.e3.String()])
	require.NotEmpty(t, out["ressalvas"])
	b.exigeAuditoria(t, "mcp.escrita.editar_pmm_target", "client", &c.cli)
	require.NotContains(t, mustJSON(t, out), "stream_url")
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

func TestEditarPMMTarget_ParidadeComARota(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	viaRota := semearPMM(t, b, "2026-12-01", "2026-12-31")
	viaTool := semearPMM(t, b, "2026-12-01", "2026-12-31")
	h := &handlers.ClientTargetPmmHandler{Repo: catalog.NewClientStationPMM(b.pool)}

	// Atualiza uma, apaga outra, insere uma nova (todas efetivas: as contagens batem).
	var resp struct{ Updated, Deleted int }
	require.NoError(t, json.Unmarshal(chamarHandlerCorpo(t, h.Bulk, http.MethodPut, "/clients/x/target-pmm", map[string]any{"entries": []any{
		map[string]any{"station_id": viaRota.e1.String(), "pmm_target": 1500},
		map[string]any{"station_id": viaRota.e2.String(), "pmm_target": nil},
		map[string]any{"station_id": viaRota.e3.String(), "pmm_target": 0},
	}}, map[string]string{"clientID": viaRota.cli.String()}, b.admin), &resp))
	out := b.confirmarEExecutar(t, "editar_pmm_target", map[string]any{"cliente_id": viaTool.cli.String(), "entradas": []any{
		entradaPMM(viaTool.e1, 1500), entradaPMM(viaTool.e2, nil), entradaPMM(viaTool.e3, 0),
	}})

	res := out["resultado"].(map[string]any)
	require.EqualValues(t, resp.Updated, res["gravadas"])
	require.EqualValues(t, resp.Deleted, res["removidas"])
	rota, tool := linhasPMM(t, b, viaRota.cli), linhasPMM(t, b, viaTool.cli)
	require.Equal(t, []int{1500, 0}, []int{rota[viaRota.e1], rota[viaRota.e3]})
	require.Equal(t, []int{1500, 0}, []int{tool[viaTool.e1], tool[viaTool.e3]}, "zero é linha, não ausência")
	require.NotContains(t, tool, viaTool.e2)
	require.Len(t, rota, len(tool))
}

func TestEditarPMMTarget_Escalada(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)

	// (1) veiculação aprovada do cliente em E1: barra.
	exposto := semearPMM(t, b, "2026-12-01", "2026-12-31")
	mat := mcptest.Material(t, b.ctx, b.pool, exposto.cli, mcptest.TipoMaterial(t, b.ctx, b.pool, "Spot"), "Spot")
	mcptest.Vincular(t, b.ctx, b.pool, exposto.camp, mat, exposto.e1)
	mcptest.Veiculacao(t, b.ctx, b.pool, exposto.camp, mat, exposto.e1, "in_slot", mcptest.Quando("2026-10-10", "10:00"))
	out := b.barrado(t, "editar_pmm_target", map[string]any{"cliente_id": exposto.cli.String(), "entradas": []any{entradaPMM(exposto.e1, 1500)}})
	require.Contains(t, out["motivo"], "veiculação")
	require.Equal(t, "https://portal.teste/clients/"+exposto.cli.String()+"/target-pmm", out["links"].(map[string]any)["tela"])
	require.Equal(t, map[uuid.UUID]int{exposto.e1: 1000, exposto.e2: 2000}, linhasPMM(t, b, exposto.cli), "nada foi gravado")
	l := b.auditoria(t, "mcp.escrita.requer_aprovacao")
	require.Len(t, l, 1)
	require.Equal(t, "client", l[0].Recurso)
	require.Empty(t, b.auditoria(t, "mcp.escrita.editar_pmm_target"))

	// ...mas E2 do MESMO cliente, sem tocada e em campanha futura, não escala.
	pre := b.chamar(t, "editar_pmm_target", map[string]any{"cliente_id": exposto.cli.String(), "entradas": []any{entradaPMM(exposto.e2, 2500)}})
	require.NotEmpty(t, pre["confirmacao"], "vizinho: emissora do cliente sem veiculação")
	require.NotContains(t, pre, "nivel")

	// (2) campanha iniciada antes de hoje, sem tocada nenhuma: barra (o total no target aparece mesmo assim).
	iniciada := semearPMM(t, b, "2026-10-01", "2026-10-31")
	out = b.barrado(t, "editar_pmm_target", map[string]any{"cliente_id": iniciada.cli.String(), "entradas": []any{entradaPMM(iniciada.e1, 1500)}})
	require.Contains(t, out["motivo"], "Camp PMM")
	require.Equal(t, map[uuid.UUID]int{iniciada.e1: 1000, iniciada.e2: 2000}, linhasPMM(t, b, iniciada.cli))

	// Vizinhos da campanha iniciada que NÃO escalam:
	//  a) a emissora do lote não está na campanha (E3);
	pre = b.chamar(t, "editar_pmm_target", map[string]any{"cliente_id": iniciada.cli.String(), "entradas": []any{entradaPMM(iniciada.e3, 300)}})
	require.NotEmpty(t, pre["confirmacao"], "emissora fora da campanha iniciada")
	//  b) a campanha inicia HOJE (29/10): nenhum dia passado ainda;
	hoje := semearPMM(t, b, "2026-10-29", "2026-11-30")
	pre = b.chamar(t, "editar_pmm_target", map[string]any{"cliente_id": hoje.cli.String(), "entradas": []any{entradaPMM(hoje.e1, 1500)}})
	require.NotEmpty(t, pre["confirmacao"], "início hoje não é dia passado")
	//  c) a campanha iniciada foi cancelada;
	cancelada := semearPMM(t, b, "2026-10-01", "2026-10-31")
	mcptest.EstadoCampanha(t, b.ctx, b.pool, cancelada.camp, "cancelada")
	pre = b.chamar(t, "editar_pmm_target", map[string]any{"cliente_id": cancelada.cli.String(), "entradas": []any{entradaPMM(cancelada.e1, 1500)}})
	require.NotEmpty(t, pre["confirmacao"], "campanha cancelada não conta")
	//  d) veiculação de OUTRO cliente na mesma emissora não conta.
	outroCli := mcptest.Cliente(t, b.ctx, b.pool, "Outro cliente")
	futuro := semearPMM(t, b, "2026-12-01", "2026-12-31")
	campOutro := mcptest.Campanha(t, b.ctx, b.pool, outroCli, "Camp outro", "2026-12-01", "2026-12-31", futuro.e1)
	matOutro := mcptest.Material(t, b.ctx, b.pool, outroCli, mcptest.TipoMaterial(t, b.ctx, b.pool, "Spot"), "Spot outro")
	mcptest.Veiculacao(t, b.ctx, b.pool, campOutro, matOutro, futuro.e1, "in_slot", mcptest.Quando("2026-10-10", "10:00"))
	pre = b.chamar(t, "editar_pmm_target", map[string]any{"cliente_id": futuro.cli.String(), "entradas": []any{entradaPMM(futuro.e1, 1500)}})
	require.NotEmpty(t, pre["confirmacao"], "o PMM é por cliente: tocada de outro cliente não escala")

	// (3) só as entradas que de fato mudam contam: a exposta já está no valor atual (sem efeito), a outra muda e não é exposta.
	pre = b.chamar(t, "editar_pmm_target", map[string]any{"cliente_id": exposto.cli.String(), "entradas": []any{
		entradaPMM(exposto.e1, 1000), entradaPMM(exposto.e2, 2600)}})
	require.NotEmpty(t, pre["confirmacao"], "entrada sem efeito não escala")
}

// F3-2: entre o preview e a confirmação entrou uma tocada: a confirmação barra e o token fica.
func TestEditarPMMTarget_EscaladaNaConfirmacaoBarraENaoConsomeOToken(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearPMM(t, b, "2026-12-01", "2026-12-31")
	args := map[string]any{"cliente_id": c.cli.String(), "entradas": []any{entradaPMM(c.e1, 1500)}}
	pre := b.preview(t, "editar_pmm_target", args)

	mat := mcptest.Material(t, b.ctx, b.pool, c.cli, mcptest.TipoMaterial(t, b.ctx, b.pool, "Spot"), "Spot")
	mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, mat, c.e1, "in_slot", mcptest.Quando("2026-10-10", "10:00"))
	out := b.chamar(t, "editar_pmm_target", map[string]any{"cliente_id": c.cli.String(), "entradas": args["entradas"], "confirmacao": pre["confirmacao"]})

	require.Equal(t, "critico", out["nivel"])
	require.Equal(t, 1000, linhasPMM(t, b, c.cli)[c.e1])
	var usadas int
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT count(*) FROM mcp_confirmacoes_usadas`).Scan(&usadas))
	require.Zero(t, usadas)
}

func TestEditarPMMTarget_Recusas(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearPMM(t, b, "2026-12-01", "2026-12-31")
	muitas := make([]any, 501)
	for i := range muitas {
		muitas[i] = entradaPMM(uuid.New(), 1)
	}

	for nome, caso := range map[string]struct {
		args   map[string]any
		codigo string
	}{
		"cliente inválido":     {map[string]any{"cliente_id": "x", "entradas": []any{entradaPMM(c.e1, 1)}}, "[entrada_invalida]"},
		"cliente inexistente":  {map[string]any{"cliente_id": uuid.NewString(), "entradas": []any{entradaPMM(c.e1, 1)}}, "[nao_encontrado]"},
		"entradas vazia":       {map[string]any{"cliente_id": c.cli.String(), "entradas": []any{}}, "[entrada_invalida]"},
		"mais de 500":          {map[string]any{"cliente_id": c.cli.String(), "entradas": muitas}, "[entrada_invalida]"},
		"emissora inválida":    {map[string]any{"cliente_id": c.cli.String(), "entradas": []any{map[string]any{"emissora_id": "x", "pmm_target": 1}}}, "[entrada_invalida]"},
		"emissora inexistente": {map[string]any{"cliente_id": c.cli.String(), "entradas": []any{entradaPMM(uuid.New(), 1)}}, "[nao_encontrado]"},
		"emissora repetida":    {map[string]any{"cliente_id": c.cli.String(), "entradas": []any{entradaPMM(c.e1, 1), entradaPMM(c.e1, 2)}}, "[entrada_invalida]"},
		"valor negativo":       {map[string]any{"cliente_id": c.cli.String(), "entradas": []any{entradaPMM(c.e1, -1)}}, "[entrada_invalida]"},
		"valor gigante":        {map[string]any{"cliente_id": c.cli.String(), "entradas": []any{entradaPMM(c.e1, 3000000000)}}, "[entrada_invalida]"},
		"tudo já como pedido":  {map[string]any{"cliente_id": c.cli.String(), "entradas": []any{entradaPMM(c.e1, 1000), entradaPMM(c.e3, nil)}}, "[entrada_invalida]"},
	} {
		texto := b.chamarErro(t, "editar_pmm_target", caso.args)
		require.Contains(t, texto, caso.codigo, nome)
	}
	// pmm_target é obrigatório (mesmo null): sem ele, o schema recusa — esquecer o
	// valor nunca remove uma linha.
	texto := b.chamarErro(t, "editar_pmm_target", map[string]any{"cliente_id": c.cli.String(), "entradas": []any{map[string]any{"emissora_id": c.e1.String()}}})
	require.Contains(t, texto, "pmm_target")
	require.Equal(t, map[uuid.UUID]int{c.e1: 1000, c.e2: 2000}, linhasPMM(t, b, c.cli))
	require.Zero(t, b.totalAuditorias(t))
}

// ── editar_emissora ─────────────────────────────────────────────────────

type linhaEmissora struct {
	Nome, Banda, Stream string
	Freq, PMM, Lat, Lng *float64
	Cidade, UF          *string
}

func lerLinhaEmissora(t *testing.T, b *bancadaTool, id uuid.UUID) linhaEmissora {
	t.Helper()
	var l linhaEmissora
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT name, band, stream_url, frequency_mhz, pmm, latitude, longitude, city, state
		FROM stations WHERE id = $1`, id).Scan(&l.Nome, &l.Banda, &l.Stream, &l.Freq, &l.PMM, &l.Lat, &l.Lng, &l.Cidade, &l.UF))
	return l
}

// semearEmissora: uma emissora com dial, PMM e cidade conhecida (o geocoding acha).
func semearEmissora(t *testing.T, b *bancadaTool, nome string) uuid.UUID {
	t.Helper()
	id := mcptest.Emissora(t, b.ctx, b.pool, nome, 100)
	_, err := b.pool.Exec(b.ctx, `UPDATE stations SET frequency_mhz = 99.9, city = 'São Paulo', state = 'SP',
		latitude = -23.55, longitude = -46.63,
		metadata = '{"categories":["jovem"],"chave_desconhecida":{"a":1}}'::jsonb WHERE id = $1`, id)
	require.NoError(t, err)
	return id
}

func fl(f float64) *float64 { return &f }

func TestEditarEmissora_ParcialPreviewNaoEscreveEConfirmarGrava(t *testing.T) {
	b := novaBancadaTool(t)
	id := semearEmissora(t, b, "Rádio Antiga")
	antes := lerLinhaEmissora(t, b, id)
	args := map[string]any{"emissora_id": id.String(), "nome": "  Rádio Nova FM  ", "frequencia_mhz": 101.5}

	pre := b.preview(t, "editar_emissora", args)

	require.Equal(t, antes, lerLinhaEmissora(t, b, id), "o preview não escreve")
	ms := pre["preview"].(map[string]any)["mudancas"].([]any)
	require.Len(t, ms, 2)
	require.Equal(t, map[string]any{"campo": "nome", "de": "Rádio Antiga", "para": "Rádio Nova FM"}, ms[0])
	require.Equal(t, map[string]any{"campo": "frequencia_mhz", "de": 99.9, "para": 101.5}, ms[1])
	require.NotEmpty(t, pre["preview"].(map[string]any)["ressalvas"], "mudar o dial: a Anatel não é recalculada")
	require.Zero(t, b.totalAuditorias(t))

	out := b.confirmarEExecutar(t, "editar_emissora", args)

	depois := lerLinhaEmissora(t, b, id)
	esperado := antes
	esperado.Nome, esperado.Freq = "Rádio Nova FM", fl(101.5)
	require.Equal(t, esperado, depois, "o resto fica: PMM, cidade, coordenada e stream_url")
	var meta string
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT metadata::text FROM stations WHERE id = $1`, id).Scan(&meta))
	require.JSONEq(t, `{"categories":["jovem"],"chave_desconhecida":{"a":1}}`, meta, "o metadata inteiro sobrevive")
	res := out["resultado"].(map[string]any) // o formato do obter_emissora
	require.Equal(t, "Rádio Nova FM", res["emissora"].(map[string]any)["nome"])
	require.NotContains(t, mustJSON(t, out), "stream_url", "o stream nunca sai")
	require.NotContains(t, mustJSON(t, out), antes.Stream)
	require.NotEmpty(t, out["ressalvas"])
	b.exigeAuditoria(t, "mcp.escrita.editar_emissora", "station", &id)
}

func TestEditarEmissora_ParidadeComARota(t *testing.T) {
	b := novaBancadaTool(t)
	viaRota := semearEmissora(t, b, "Rádio Paridade")
	viaTool := semearEmissora(t, b, "Rádio Paridade")
	h := &handlers.StationsHandler{Repo: catalog.NewStations(b.pool)}
	atual := lerLinhaEmissora(t, b, viaRota)

	// A rota é PUT total: a mesma edição com o cadastro inteiro remontado.
	chamarHandlerCorpo(t, h.Update, http.MethodPut, "/stations/x", map[string]any{
		"name": "Rádio do Rio", "band": atual.Banda, "frequency_mhz": 102.1, "city": "Rio de Janeiro", "state": "RJ",
		"stream_url": atual.Stream, "pmm": 250.5, "meta": map[string]any{"categories": []string{"jovem"}},
	}, map[string]string{"id": viaRota.String()}, b.admin)
	b.confirmarEExecutar(t, "editar_emissora", map[string]any{
		"emissora_id": viaTool.String(), "nome": "Rádio do Rio", "frequencia_mhz": 102.1, "cidade": "Rio de Janeiro", "uf": "rj", "pmm": 250.5})

	esperado, obtido := lerLinhaEmissora(t, b, viaRota), lerLinhaEmissora(t, b, viaTool)
	esperado.Stream, obtido.Stream = "", "" // a fixture dá um stream único a cada uma
	require.Equal(t, esperado, obtido, "mesmas colunas, inclusive a coordenada re-derivada da cidade")
	require.InDelta(t, -22.9, *obtido.Lat, 0.5)
}

func TestEditarEmissora_GeocodificacaoQueFalhaEAnatelViramRessalva(t *testing.T) {
	b := novaBancadaTool(t)
	id := semearEmissora(t, b, "Rádio Cidade")

	out := b.confirmarEExecutar(t, "editar_emissora", map[string]any{"emissora_id": id.String(), "cidade": "Cidade Inexistente XYZ"})

	l := lerLinhaEmissora(t, b, id)
	require.Equal(t, "Cidade Inexistente XYZ", *l.Cidade)
	require.InDelta(t, -23.55, *l.Lat, 0.001, "a coordenada anterior fica")
	texto := strings.Join(ressalvasDe(out), " | ")
	require.Contains(t, texto, "coordenada")
	require.Contains(t, texto, "Anatel")

	// Só o nome: sem aviso de Anatel nem de coordenada.
	out = b.confirmarEExecutar(t, "editar_emissora", map[string]any{"emissora_id": id.String(), "nome": "Outro nome"})
	require.Empty(t, out["ressalvas"])
}

func ressalvasDe(out map[string]any) []string {
	var rs []string
	for _, r := range out["ressalvas"].([]any) {
		rs = append(rs, r.(string))
	}
	return rs
}

// O PMM da emissora entra nos impactos de todas as campanhas (campaigns.go,
// insights.go, detections.go): trocá-lo reescreve números de dias já veiculados.
func TestEditarEmissora_PMMComVeiculacaoEhBarradoENadaMuda(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearMaterial(t, b, true) // emissora c.est com uma tocada aprovada
	antes := lerLinhaEmissora(t, b, c.est)
	args := map[string]any{"emissora_id": c.est.String(), "pmm": 5000}

	out := b.barrado(t, "editar_emissora", args)

	require.Contains(t, out["motivo"], "veiculação")
	require.Contains(t, out["motivo"], "PMM")
	require.Equal(t, "https://portal.teste/stations/"+c.est.String()+"/edit", out["links"].(map[string]any)["tela"])
	require.Equal(t, antes, lerLinhaEmissora(t, b, c.est), "nada foi gravado")
	require.Contains(t, out["preview"].(map[string]any)["efeitos"].([]any)[len(out["preview"].(map[string]any)["efeitos"].([]any))-1], "já tem veiculação aprovada")
	l := b.auditoria(t, "mcp.escrita.requer_aprovacao")
	require.Len(t, l, 1)
	require.Equal(t, "station", l[0].Recurso)
	require.Equal(t, c.est, *l[0].RecursoID)
	require.Empty(t, b.auditoria(t, "mcp.escrita.editar_emissora"))
}

func TestEditarEmissora_VizinhosQueNaoEscalam(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearMaterial(t, b, true) // c.est tem veiculação aprovada
	_, err := b.pool.Exec(b.ctx, `UPDATE stations SET pmm = 100 WHERE id = $1`, c.est)
	require.NoError(t, err)

	naoEscala := func(rotulo string, args map[string]any) {
		pre := b.chamar(t, "editar_emissora", args)
		require.NotEmpty(t, pre["confirmacao"], rotulo)
		require.NotContains(t, pre, "nivel", rotulo)
	}
	// (a) com veiculação, mas sem tocar no PMM: nome, dial, cidade não são número de relatório.
	naoEscala("só o nome", map[string]any{"emissora_id": c.est.String(), "nome": "Outro nome"})
	naoEscala("só o dial", map[string]any{"emissora_id": c.est.String(), "frequencia_mhz": 88.1})
	// (b) PMM igual ao atual (mesmo arredondado a 2 casas) junto com outro campo: não há troca.
	naoEscala("PMM igual ao atual", map[string]any{"emissora_id": c.est.String(), "pmm": 100.001, "nome": "Outro nome"})
	// (c) PMM novo numa emissora SEM veiculação.
	sem := semearEmissora(t, b, "Rádio sem tocada")
	naoEscala("sem veiculação", map[string]any{"emissora_id": sem.String(), "pmm": 5000})
	// (d) só tocada retratada: não é veiculação aprovada.
	retr := semearMaterial(t, b, true)
	_, err = b.pool.Exec(b.ctx, `UPDATE detections SET retracted_at = now() WHERE station_id = $1`, retr.est)
	require.NoError(t, err)
	naoEscala("tocada retratada", map[string]any{"emissora_id": retr.est.String(), "pmm": 5000})
}

func TestEditarEmissora_EscaladaNaConfirmacaoBarraENaoConsomeOToken(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearMaterial(t, b, false)
	args := map[string]any{"emissora_id": c.est.String(), "pmm": 5000}
	pre := b.preview(t, "editar_emissora", args)

	mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, c.mat, c.est, "bonus", mcptest.Quando("2026-10-10", "10:00"))
	out := b.chamar(t, "editar_emissora", map[string]any{"emissora_id": c.est.String(), "pmm": 5000, "confirmacao": pre["confirmacao"]})

	require.Equal(t, "critico", out["nivel"])
	require.Nil(t, lerLinhaEmissora(t, b, c.est).PMM)
	var usadas int
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT count(*) FROM mcp_confirmacoes_usadas`).Scan(&usadas))
	require.Zero(t, usadas)
}

func TestEditarEmissora_Recusas(t *testing.T) {
	b := novaBancadaTool(t)
	id := semearEmissora(t, b, "Rádio X")
	antes := lerLinhaEmissora(t, b, id)
	i := id.String()

	for nome, caso := range map[string]struct {
		args   map[string]any
		codigo string
	}{
		"id inválido":                {map[string]any{"emissora_id": "x", "nome": "A"}, "[entrada_invalida]"},
		"emissora inexistente":       {map[string]any{"emissora_id": uuid.NewString(), "nome": "A"}, "[nao_encontrado]"},
		"nenhum campo":               {map[string]any{"emissora_id": i}, "[entrada_invalida]"},
		"nome vazio":                 {map[string]any{"emissora_id": i, "nome": "  "}, "[entrada_invalida]"},
		"dial zero":                  {map[string]any{"emissora_id": i, "frequencia_mhz": 0}, "[entrada_invalida]"},
		"dial negativo":              {map[string]any{"emissora_id": i, "frequencia_mhz": -3}, "[entrada_invalida]"},
		"dial estourado":             {map[string]any{"emissora_id": i, "frequencia_mhz": 10000}, "[entrada_invalida]"},
		"cidade vazia":               {map[string]any{"emissora_id": i, "cidade": ""}, "[entrada_invalida]"},
		"UF de uma letra":            {map[string]any{"emissora_id": i, "uf": "S"}, "[entrada_invalida]"},
		"UF inexistente":             {map[string]any{"emissora_id": i, "uf": "XX"}, "[entrada_invalida]"},
		"nome longo demais":          {map[string]any{"emissora_id": i, "nome": repete("n", 201)}, "[entrada_invalida]"},
		"cidade longa demais":        {map[string]any{"emissora_id": i, "cidade": repete("c", 201)}, "[entrada_invalida]"},
		"dial de FM abaixo da faixa": {map[string]any{"emissora_id": i, "frequencia_mhz": 50}, "[entrada_invalida]"},
		"dial de FM acima da faixa":  {map[string]any{"emissora_id": i, "frequencia_mhz": 108.5}, "[entrada_invalida]"},
		"dial em kHz numa FM":        {map[string]any{"emissora_id": i, "frequencia_mhz": 1180}, "[entrada_invalida]"},
		"PMM negativo":               {map[string]any{"emissora_id": i, "pmm": -1}, "[entrada_invalida]"},
		"PMM estourado":              {map[string]any{"emissora_id": i, "pmm": 1e12}, "[entrada_invalida]"},
		"nada muda":                  {map[string]any{"emissora_id": i, "nome": "Rádio X", "frequencia_mhz": 99.9, "cidade": "São Paulo", "uf": "SP", "pmm": 100}, "[entrada_invalida]"},
	} {
		texto := b.chamarErro(t, "editar_emissora", caso.args)
		require.Contains(t, texto, caso.codigo, nome)
	}
	require.Equal(t, antes, lerLinhaEmissora(t, b, id))
	require.Zero(t, b.totalAuditorias(t))
	require.NotContains(t, propriedades(esquemaDa(t, b.cfg, "editar_emissora")), "stream_url", "o MCP nunca troca o stream")
}

// O dial confere com a banda DA EMISSORA (FM em MHz, AM em kHz): o mesmo número
// vale para uma e não para a outra.
func TestEditarEmissora_DialConfereComABandaDaEmissora(t *testing.T) {
	b := novaBancadaTool(t)
	fm := semearEmissora(t, b, "Rádio FM")
	am := semearEmissora(t, b, "Rádio AM")
	_, err := b.pool.Exec(b.ctx, `UPDATE stations SET band = 'AM', frequency_mhz = 1180 WHERE id = $1`, am)
	require.NoError(t, err)

	// AM: 1180 kHz (ela já está lá), 1400 passa; 99.9 (dial de FM) e 20 não.
	b.confirmarEExecutar(t, "editar_emissora", map[string]any{"emissora_id": am.String(), "frequencia_mhz": 1400})
	require.InDelta(t, 1400, *lerLinhaEmissora(t, b, am).Freq, 0.001)
	for _, f := range []float64{99.9, 20, 529, 1701} {
		texto := b.chamarErro(t, "editar_emissora", map[string]any{"emissora_id": am.String(), "frequencia_mhz": f})
		require.Contains(t, texto, "[entrada_invalida]", "AM %v", f)
		require.Contains(t, texto, "kHz")
	}
	// FM: 101.5 passa; 1400 (dial de AM) não.
	b.confirmarEExecutar(t, "editar_emissora", map[string]any{"emissora_id": fm.String(), "frequencia_mhz": 101.5})
	texto := b.chamarErro(t, "editar_emissora", map[string]any{"emissora_id": fm.String(), "frequencia_mhz": 1400})
	require.Contains(t, texto, "[entrada_invalida]")
	require.Contains(t, texto, "MHz")
	require.InDelta(t, 101.5, *lerLinhaEmissora(t, b, fm).Freq, 0.001)
}

// O limite entre dois dias em SP não é o de UTC: às 22h e às 23h30 de SP já é o
// dia seguinte em UTC. "Hoje" é o de SP.
func TestEditarPMMTarget_FronteiraDoDiaEmSP(t *testing.T) {
	b := novaBancadaTool(t)
	for _, hora := range []string{"22:00", "23:30"} {
		b.E.Agora = func() time.Time { return mcptest.Quando("2026-10-29", hora) }
		require.Equal(t, 30, b.E.Agora().UTC().Day(), "%s em SP já é dia 30 em UTC", hora)

		// A campanha começa "hoje em SP" (29/10): nenhum dia passado. NÃO escala.
		hoje := semearPMM(t, b, "2026-10-29", "2026-11-30")
		pre := b.chamar(t, "editar_pmm_target", map[string]any{"cliente_id": hoje.cli.String(), "entradas": []any{entradaPMM(hoje.e1, 1500)}})
		require.NotEmpty(t, pre["confirmacao"], "início hoje (SP) às %s não escala", hora)
		require.NotContains(t, pre, "nivel")

		// Começou ontem em SP (28/10): já há dia passado. Escala.
		ontem := semearPMM(t, b, "2026-10-28", "2026-11-30")
		b.barrado(t, "editar_pmm_target", map[string]any{"cliente_id": ontem.cli.String(), "entradas": []any{entradaPMM(ontem.e1, 1500)}})

		// Começa amanhã em SP (30/10), que já é "hoje" em UTC: NÃO escala.
		amanha := semearPMM(t, b, "2026-10-30", "2026-11-30")
		pre = b.chamar(t, "editar_pmm_target", map[string]any{"cliente_id": amanha.cli.String(), "entradas": []any{entradaPMM(amanha.e1, 1500)}})
		require.NotEmpty(t, pre["confirmacao"], "início amanhã (SP) às %s não escala, mesmo sendo hoje em UTC", hora)
	}
}
