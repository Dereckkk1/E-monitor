package catalog

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

// ─── Unitários (sem DB) ─────────────────────────────────────────────────────

func TestNormalizeDial(t *testing.T) {
	cases := []struct{ in, want string }{
		// Vírgula é como se escreve dial no Brasil.
		{"99,9", "99.9"},
		{"99.9", "99.9"},
		// A coluna é NUMERIC(6,2): o token tem que sair no mesmo formato que o
		// dialTextExpr produz do lado do banco.
		{"99.90", "99.9"},
		{"100,00", "100"},
		// Zero significativo do inteiro não pode ser comido.
		{"1080", "1080"},
		{"990", "990"},
		{"0,5", "0.5"},
		// Não-numéricos não viram cláusula de dial.
		{"jb", ""},
		{"rj", ""},
		{"99.9.9", ""},
		{"", ""},
	}
	for _, c := range cases {
		require.Equalf(t, c.want, normalizeDial(c.in), "normalizeDial(%q)", c.in)
	}
}

func TestBuildTokenSearch_EmptyQuery(t *testing.T) {
	ts := buildTokenSearch("   ", 1)
	require.Empty(t, ts.Where)
	require.Empty(t, ts.Args)
	// Score vazio é o sinal de "não ordene por relevância" — sem ele o List
	// mudaria a ordenação do catálogo inteiro quando não há busca.
	require.Equal(t, "", ts.Score)
	require.Equal(t, 1, ts.Next)
}

func TestBuildTokenSearch_PlaceholdersAlignWithArgs(t *testing.T) {
	// Token numérico consome DOIS placeholders (o token cru e o dial
	// normalizado); token de texto consome um. Se isso desalinhar, o pgx
	// devolve erro de argumento em runtime.
	ts := buildTokenSearch("Jb 99,9 RJ", 3)
	require.Len(t, ts.Where, 3)
	require.Equal(t, []any{"Jb", "99,9", "99.9", "RJ"}, ts.Args)
	require.Equal(t, 3+len(ts.Args), ts.Next)
}

// ─── Integração (precisa de TEST_DATABASE_URL) ──────────────────────────────

type stationFixture struct {
	name  string
	band  string
	freq  float64
	city  string
	state string
}

func seedStations(t *testing.T, ctx context.Context, repo *Stations, fx ...stationFixture) {
	t.Helper()
	for _, f := range fx {
		_, err := repo.Create(ctx, CreateStationInput{
			Name:         f.name,
			Band:         f.band,
			FrequencyMHz: f64Ptr(f.freq),
			City:         strPtr(f.city),
			State:        strPtr(f.state),
			StreamURL:    "http://example.com/" + f.name,
		})
		require.NoError(t, err)
	}
}

func namesOf(out ListOutput) []string {
	names := make([]string, 0, len(out.Data))
	for _, st := range out.Data {
		names = append(names, st.Name)
	}
	return names
}

// O caso que motivou a feature: "Jb 99.9 RJ" tem que achar a JB FM, e só ela —
// cada token casando com um campo diferente (nome, dial, UF).
func TestStations_List_MultiFieldTokens(t *testing.T) {
	ctx, repo := newTestPool(t)
	seedStations(t, ctx, repo,
		stationFixture{"JB FM", "FM", 99.9, "Rio de Janeiro", "RJ"},
		stationFixture{"JB FM Brasília", "FM", 102.1, "Brasília", "DF"},
		stationFixture{"Rádio Cidade", "FM", 99.9, "Rio de Janeiro", "RJ"},
		stationFixture{"Antena 1", "FM", 94.7, "São Paulo", "SP"},
	)

	out, err := repo.List(ctx, ListInput{Q: "Jb 99.9 RJ", Page: 1, Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []string{"JB FM"}, namesOf(out))
	require.EqualValues(t, 1, out.Total)
}

func TestStations_List_AccentInsensitive(t *testing.T) {
	ctx, repo := newTestPool(t)
	seedStations(t, ctx, repo,
		stationFixture{"Rádio Sertão", "FM", 88.1, "Brasília", "DF"},
		stationFixture{"Antena 1", "FM", 94.7, "São Paulo", "SP"},
	)

	// Sem acento no input, com acento no banco.
	out, err := repo.List(ctx, ListInput{Q: "radio sertao", Page: 1, Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []string{"Rádio Sertão"}, namesOf(out))

	// E o inverso: com acento no input, buscando por cidade.
	out, err = repo.List(ctx, ListInput{Q: "brasília", Page: 1, Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []string{"Rádio Sertão"}, namesOf(out))
}

// A coluna é NUMERIC(6,2), então 99.9 vira "99.90". Sem normalizar, "99,9" (o
// jeito brasileiro) não achava nada.
func TestStations_List_DialFormats(t *testing.T) {
	ctx, repo := newTestPool(t)
	seedStations(t, ctx, repo,
		stationFixture{"JB FM", "FM", 99.9, "Rio de Janeiro", "RJ"},
		stationFixture{"Rádio Globo AM", "AM", 1220, "Rio de Janeiro", "RJ"},
		stationFixture{"Nova Brasil", "FM", 100.0, "São Paulo", "SP"},
	)

	for _, q := range []string{"99,9", "99.9", "99.90"} {
		out, err := repo.List(ctx, ListInput{Q: q, Page: 1, Limit: 10})
		require.NoErrorf(t, err, "q=%q", q)
		require.Equalf(t, []string{"JB FM"}, namesOf(out), "q=%q", q)
	}

	// Inteiro com zero significativo não pode ser truncado.
	out, err := repo.List(ctx, ListInput{Q: "1220", Page: 1, Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []string{"Rádio Globo AM"}, namesOf(out))

	// 100.00 no banco tem que casar com "100".
	out, err = repo.List(ctx, ListInput{Q: "100", Page: 1, Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []string{"Nova Brasil"}, namesOf(out))
}

// Casar não basta: a emissora buscada tem que vir PRIMEIRO. Antes disso a
// ordenação era só status → PMM → nome, então quem casava por acidente (mesma
// UF) podia encabeçar a lista.
func TestStations_List_RelevanceRanking(t *testing.T) {
	ctx, repo := newTestPool(t)
	seedStations(t, ctx, repo,
		// Casa só pela UF — o token "jb" não bate em nada aqui.
		stationFixture{"Alpha FM", "FM", 101.7, "Rio de Janeiro", "RJ"},
		// Nome contém "jb" no meio.
		stationFixture{"Rádio JBX", "FM", 105.1, "Niterói", "RJ"},
		// Nome começa com "JB" E dial exato → maior score.
		stationFixture{"JB FM", "FM", 99.9, "Rio de Janeiro", "RJ"},
	)

	out, err := repo.List(ctx, ListInput{Q: "jb rj", Page: 1, Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []string{"JB FM", "Rádio JBX"}, namesOf(out))
}

// Uma sigla de 2 letras casa por acidente dentro de nome com frequência. Quem
// digita "rj" quer emissora do Rio, não a "Gurjão" da Paraíba — mesmo que o
// nome dela contenha as letras. Medido em dado real: sem o peso maior na UF,
// Gurjão/PB e NRJ FM/BA vinham na frente da primeira emissora carioca.
func TestStations_List_UFOutranksAccidentalNameMatch(t *testing.T) {
	ctx, repo := newTestPool(t)
	seedStations(t, ctx, repo,
		stationFixture{"Gurjão Comunitária", "FM", 87.9, "Gurjão", "PB"},
		stationFixture{"NRJ FM", "FM", 100.3, "Salvador", "BA"},
		stationFixture{"FM o Dia", "FM", 100.5, "Rio de Janeiro", "RJ"},
	)

	out, err := repo.List(ctx, ListInput{Q: "rj", Page: 1, Limit: 10})
	require.NoError(t, err)
	require.Equal(t, "FM o Dia", namesOf(out)[0])
}

// Sem Q a ordenação NÃO pode mudar: o wizard de campanha pré-carrega 10.000
// emissoras contando com ela (active → calibrating → paused, depois PMM, nome).
func TestStations_List_NoQueryKeepsDefaultOrder(t *testing.T) {
	ctx, repo := newTestPool(t)
	seedStations(t, ctx, repo,
		stationFixture{"Zulu FM", "FM", 101.7, "Rio de Janeiro", "RJ"},
		stationFixture{"Alpha FM", "FM", 105.1, "Niterói", "RJ"},
	)
	all, err := repo.List(ctx, ListInput{Page: 1, Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []string{"Alpha FM", "Zulu FM"}, namesOf(all))

	active := all.Data[1] // Zulu, alfabeticamente último
	require.NoError(t, repo.UpdateMonitoringStatus(ctx, active.ID, "active"))

	all, err = repo.List(ctx, ListInput{Page: 1, Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []string{"Zulu FM", "Alpha FM"}, namesOf(all))
}

// Os filtros que o clique numa sugestão aplica. O `city` exato existe porque
// passar o nome da cidade como Q traz de quebra emissoras de OUTRA cidade que
// tenham esse nome no `name` — o caso "Rádio Joinville" sediada em Curitiba.
func TestStations_List_CityAndStationIDFilters(t *testing.T) {
	ctx, repo := newTestPool(t)
	seedStations(t, ctx, repo,
		stationFixture{"Band FM", "FM", 96.3, "Joinville", "SC"},
		stationFixture{"Rádio Joinville", "AM", 1080, "Curitiba", "PR"},
	)

	// Q amplo pega as duas.
	out, err := repo.List(ctx, ListInput{Q: "joinville", Page: 1, Limit: 10})
	require.NoError(t, err)
	require.Len(t, out.Data, 2)

	// City exata pega só quem está lá.
	out, err = repo.List(ctx, ListInput{City: "joinville", Page: 1, Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []string{"Band FM"}, namesOf(out))

	target := out.Data[0]
	out, err = repo.List(ctx, ListInput{StationID: &target.ID, Page: 1, Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []string{"Band FM"}, namesOf(out))
	require.EqualValues(t, 1, out.Total)
}

// ─── Emissoras contratadas pelo cliente ─────────────────────────────────────

// seedContract cria um cliente e uma campanha no status pedido apontando pras
// emissoras informadas, e devolve o client_id.
func seedContract(t *testing.T, ctx context.Context, repo *Stations,
	clientName, campaignName, status string, start, end time.Time, stationIDs ...uuid.UUID,
) uuid.UUID {
	t.Helper()
	// clients.name não é UNIQUE, então nada de ON CONFLICT: procura e só cria
	// se não existir. Vários testes daqui semeiam 2 campanhas pro mesmo cliente.
	var clientID uuid.UUID
	err := repo.pool.QueryRow(ctx,
		`SELECT id FROM clients WHERE name = $1`, clientName).Scan(&clientID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = repo.pool.QueryRow(ctx,
			`INSERT INTO clients (name) VALUES ($1) RETURNING id`, clientName).Scan(&clientID)
	}
	require.NoError(t, err)

	_, err = repo.pool.Exec(ctx, `
		INSERT INTO campaigns (client_id, name, start_date, end_date, status, target_stations)
		VALUES ($1,$2,$3,$4,$5,$6)`,
		clientID, campaignName, start, end, status, stationIDs)
	require.NoError(t, err)
	return clientID
}

func idsOf(out ListOutput) map[string]Station {
	m := map[string]Station{}
	for _, st := range out.Data {
		m[st.Name] = st
	}
	return m
}

// O caso da feature: o cliente quer ver as emissoras que tem contratadas sem
// abrir campanha por campanha.
func TestStations_List_ContractedBy(t *testing.T) {
	ctx, repo := newTestPool(t)
	seedStations(t, ctx, repo,
		stationFixture{"Contratada A", "FM", 99.9, "Rio de Janeiro", "RJ"},
		stationFixture{"Contratada B", "FM", 101.7, "São Paulo", "SP"},
		stationFixture{"De outro cliente", "FM", 88.1, "Curitiba", "PR"},
		stationFixture{"De ninguém", "FM", 94.7, "Salvador", "BA"},
	)
	all, err := repo.List(ctx, ListInput{Page: 1, Limit: 50})
	require.NoError(t, err)
	byName := idsOf(all)
	hoje := time.Now()

	meu := seedContract(t, ctx, repo, "Cliente Meu", "camp-ativa", "ativa",
		hoje.AddDate(0, 0, -10), hoje.AddDate(0, 0, 10),
		byName["Contratada A"].ID, byName["Contratada B"].ID)
	seedContract(t, ctx, repo, "Cliente Outro", "camp-outro", "ativa",
		hoje.AddDate(0, 0, -10), hoje.AddDate(0, 0, 10),
		byName["De outro cliente"].ID)

	out, err := repo.List(ctx, ListInput{ContractedBy: []uuid.UUID{meu}, Page: 1, Limit: 50})
	require.NoError(t, err)
	require.Equal(t, []string{"Contratada A", "Contratada B"}, namesOf(out))
	require.EqualValues(t, 2, out.Total)

	// O vínculo vem junto: é o "por que esta emissora é minha".
	c := idsOf(out)["Contratada A"].Contract
	require.NotNil(t, c)
	require.Equal(t, 1, c.Campaigns)
	require.True(t, c.OnAir)
	require.Nil(t, c.StartsAt, "com campanha no ar não existe 'a partir de'")

	// Sem escopo de cliente, `contract` não existe — nem sequer zerado.
	require.Nil(t, idsOf(all)["Contratada A"].Contract)
}

// `programada` conta como contratada (o contrato existe), mas tem que vir
// marcada com a data — senão o cliente acha que já está no ar.
func TestStations_List_ContractedBy_ScheduledIsMarked(t *testing.T) {
	ctx, repo := newTestPool(t)
	seedStations(t, ctx, repo,
		stationFixture{"Já no ar", "FM", 99.9, "Rio de Janeiro", "RJ"},
		stationFixture{"Começa depois", "FM", 101.7, "São Paulo", "SP"},
	)
	all, _ := repo.List(ctx, ListInput{Page: 1, Limit: 50})
	byName := idsOf(all)
	hoje := time.Now()
	inicio := hoje.AddDate(0, 0, 14)

	cid := seedContract(t, ctx, repo, "Cli Prog", "camp-ativa", "ativa",
		hoje.AddDate(0, 0, -5), hoje.AddDate(0, 0, 5), byName["Já no ar"].ID)
	seedContract(t, ctx, repo, "Cli Prog", "camp-programada", "programada",
		inicio, hoje.AddDate(0, 0, 30), byName["Começa depois"].ID)

	out, err := repo.List(ctx, ListInput{ContractedBy: []uuid.UUID{cid}, Page: 1, Limit: 50})
	require.NoError(t, err)
	require.Len(t, out.Data, 2)

	got := idsOf(out)
	require.True(t, got["Já no ar"].Contract.OnAir)
	require.Nil(t, got["Já no ar"].Contract.StartsAt)

	prog := got["Começa depois"].Contract
	require.False(t, prog.OnAir)
	require.NotNil(t, prog.StartsAt)
	require.Equal(t, inicio.Format("2006-01-02"), prog.StartsAt.Format("2006-01-02"))
}

// Cancelada e concluída NÃO são contrato vigente. Cancelada sai por regra
// (docs/features/cancelled-campaign-handling.md); concluída acabou.
func TestStations_List_ContractedBy_ExcludesEndedAndCancelled(t *testing.T) {
	ctx, repo := newTestPool(t)
	seedStations(t, ctx, repo,
		stationFixture{"De campanha concluida", "FM", 99.9, "Rio de Janeiro", "RJ"},
		stationFixture{"De campanha cancelada", "FM", 101.7, "São Paulo", "SP"},
	)
	all, _ := repo.List(ctx, ListInput{Page: 1, Limit: 50})
	byName := idsOf(all)
	hoje := time.Now()

	cid := seedContract(t, ctx, repo, "Cli Morto", "camp-concluida", "concluida",
		hoje.AddDate(0, 0, -60), hoje.AddDate(0, 0, -30), byName["De campanha concluida"].ID)
	seedContract(t, ctx, repo, "Cli Morto", "camp-cancelada", "cancelada",
		hoje.AddDate(0, 0, -10), hoje.AddDate(0, 0, 10), byName["De campanha cancelada"].ID)

	out, err := repo.List(ctx, ListInput{ContractedBy: []uuid.UUID{cid}, Page: 1, Limit: 50})
	require.NoError(t, err)
	require.Empty(t, out.Data)
	require.EqualValues(t, 0, out.Total)
}

// Uma emissora em várias campanhas do mesmo cliente é UMA linha, com a
// contagem — não uma linha por campanha.
func TestStations_List_ContractedBy_DedupesAcrossCampaigns(t *testing.T) {
	ctx, repo := newTestPool(t)
	seedStations(t, ctx, repo,
		stationFixture{"Em duas campanhas", "FM", 99.9, "Rio de Janeiro", "RJ"},
	)
	all, _ := repo.List(ctx, ListInput{Page: 1, Limit: 50})
	id := idsOf(all)["Em duas campanhas"].ID
	hoje := time.Now()

	cid := seedContract(t, ctx, repo, "Cli Dup", "camp-1", "ativa",
		hoje.AddDate(0, 0, -5), hoje.AddDate(0, 0, 5), id)
	seedContract(t, ctx, repo, "Cli Dup", "camp-2", "ativa",
		hoje.AddDate(0, 0, -3), hoje.AddDate(0, 0, 20), id)

	out, err := repo.List(ctx, ListInput{ContractedBy: []uuid.UUID{cid}, Page: 1, Limit: 50})
	require.NoError(t, err)
	require.Len(t, out.Data, 1)
	require.EqualValues(t, 1, out.Total)
	require.Equal(t, 2, out.Data[0].Contract.Campaigns)
}

// Carteira de agência: vários clientes num filtro só, sem duplicar a emissora
// que os dois compartilham.
func TestStations_List_ContractedBy_WalletUnion(t *testing.T) {
	ctx, repo := newTestPool(t)
	seedStations(t, ctx, repo,
		stationFixture{"Compartilhada", "FM", 99.9, "Rio de Janeiro", "RJ"},
		stationFixture{"Só do A", "FM", 101.7, "São Paulo", "SP"},
		stationFixture{"Só do B", "FM", 88.1, "Curitiba", "PR"},
		stationFixture{"De mais ninguém", "FM", 94.7, "Salvador", "BA"},
	)
	all, _ := repo.List(ctx, ListInput{Page: 1, Limit: 50})
	byName := idsOf(all)
	hoje := time.Now()
	de, ate := hoje.AddDate(0, 0, -5), hoje.AddDate(0, 0, 5)

	a := seedContract(t, ctx, repo, "Cli A", "camp-a", "ativa", de, ate,
		byName["Compartilhada"].ID, byName["Só do A"].ID)
	b := seedContract(t, ctx, repo, "Cli B", "camp-b", "ativa", de, ate,
		byName["Compartilhada"].ID, byName["Só do B"].ID)

	out, err := repo.List(ctx, ListInput{ContractedBy: []uuid.UUID{a, b}, Page: 1, Limit: 50})
	require.NoError(t, err)
	require.Equal(t, []string{"Compartilhada", "Só do A", "Só do B"}, namesOf(out))
	require.EqualValues(t, 3, out.Total)
	// A compartilhada aparece uma vez, contando as duas campanhas.
	require.Equal(t, 2, idsOf(out)["Compartilhada"].Contract.Campaigns)
}

// O filtro tem que COMPOR com a busca: o cliente procura "99,9" dentro das
// dele, não no catálogo inteiro.
func TestStations_List_ContractedBy_ComposesWithSearch(t *testing.T) {
	ctx, repo := newTestPool(t)
	seedStations(t, ctx, repo,
		stationFixture{"Minha 99", "FM", 99.9, "Rio de Janeiro", "RJ"},
		stationFixture{"Minha outra", "FM", 101.7, "São Paulo", "SP"},
		stationFixture{"Alheia 99", "FM", 99.9, "Curitiba", "PR"},
	)
	all, _ := repo.List(ctx, ListInput{Page: 1, Limit: 50})
	byName := idsOf(all)
	hoje := time.Now()
	de, ate := hoje.AddDate(0, 0, -5), hoje.AddDate(0, 0, 5)

	cid := seedContract(t, ctx, repo, "Cli Busca", "camp", "ativa", de, ate,
		byName["Minha 99"].ID, byName["Minha outra"].ID)
	seedContract(t, ctx, repo, "Cli Alheio", "camp-alheia", "ativa", de, ate,
		byName["Alheia 99"].ID)

	out, err := repo.List(ctx, ListInput{
		ContractedBy: []uuid.UUID{cid}, Q: "99,9", Page: 1, Limit: 50,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"Minha 99"}, namesOf(out))
}

func TestStations_Suggest_Groups(t *testing.T) {
	ctx, repo := newTestPool(t)
	seedStations(t, ctx, repo,
		stationFixture{"Band FM", "FM", 96.3, "Joinville", "SC"},
		stationFixture{"CBN Joinville", "FM", 93.1, "Joinville", "SC"},
		stationFixture{"Rádio Joinville", "AM", 1080, "Curitiba", "PR"},
		stationFixture{"JB FM", "FM", 99.9, "Rio de Janeiro", "RJ"},
		// "Varjota" contém "rj" no meio de uma palavra — não pode virar
		// sugestão de cidade pra quem digitou a UF.
		stationFixture{"Rádio Varjota", "FM", 98.3, "Varjota", "CE"},
	)

	// Curto demais → grupos vazios, sem erro (o campo chama a cada tecla).
	out, err := repo.Suggest(ctx, SuggestInput{Q: "j"})
	require.NoError(t, err)
	require.Empty(t, out.Stations)
	require.Empty(t, out.Cities)
	require.Empty(t, out.States)

	out, err = repo.Suggest(ctx, SuggestInput{Q: "joinville"})
	require.NoError(t, err)
	require.Len(t, out.Stations, 3)
	// Contagem agregada sobre a tabela inteira, não sobre a página carregada.
	require.Len(t, out.Cities, 1)
	require.Equal(t, "Joinville", out.Cities[0].City)
	require.EqualValues(t, 2, out.Cities[0].Count)

	// Query de 2 letras que é UF → grupo de estado.
	out, err = repo.Suggest(ctx, SuggestInput{Q: "sc"})
	require.NoError(t, err)
	require.Len(t, out.States, 1)
	require.Equal(t, "SC", out.States[0].State)
	require.EqualValues(t, 2, out.States[0].Count)
	// ...e nenhuma cidade: nenhum token casa com nome de cidade, então não faz
	// sentido despejar as cidades de SC (o grupo de UF já cobre).
	require.Empty(t, out.Cities)

	// Match de cidade é por início de PALAVRA: "Varjota" contém "rj" mas não
	// começa nenhuma palavra com isso, então some da sugestão.
	out, err = repo.Suggest(ctx, SuggestInput{Q: "rj"})
	require.NoError(t, err)
	require.Empty(t, out.Cities)
	require.Len(t, out.States, 1)
	require.Equal(t, "RJ", out.States[0].State)

	// Mas o início de palavra no MEIO do nome continua valendo: "Joinville"
	// pra "join", e a segunda palavra de um nome composto também.
	out, err = repo.Suggest(ctx, SuggestInput{Q: "janeiro"})
	require.NoError(t, err)
	require.Len(t, out.Cities, 1)
	require.Equal(t, "Rio de Janeiro", out.Cities[0].City)

	// Multi-token só sugere cidade quando algum token casa com a cidade.
	out, err = repo.Suggest(ctx, SuggestInput{Q: "joinville sc"})
	require.NoError(t, err)
	require.Len(t, out.Cities, 1)
	require.Equal(t, "Joinville", out.Cities[0].City)

	// "jb 99.9 rj" não sugere cidade nenhuma.
	out, err = repo.Suggest(ctx, SuggestInput{Q: "jb 99.9 rj"})
	require.NoError(t, err)
	require.Empty(t, out.Cities)
	require.Len(t, out.Stations, 1)
	require.Equal(t, "JB FM", out.Stations[0].Name)

	// Band filtra os três grupos.
	out, err = repo.Suggest(ctx, SuggestInput{Q: "joinville", Band: "AM"})
	require.NoError(t, err)
	require.Len(t, out.Stations, 1)
	require.Equal(t, "Rádio Joinville", out.Stations[0].Name)
	require.Empty(t, out.Cities) // a única AM de "joinville" fica em Curitiba
}
