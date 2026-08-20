package anatel

import (
	"math"
	"testing"

	"radiocheck/internal/geo"
)

func mustIndex(t *testing.T) *Index {
	t.Helper()
	ix, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return ix
}

// TestPlansLoad garante que os dois planos embutidos foram parseados. Um
// go:embed que aponta pro arquivo errado, ou um separador trocado, cai aqui em
// vez de virar "nenhuma emissora casou" no backfill.
func TestPlansLoad(t *testing.T) {
	ix := mustIndex(t)
	var fm, om, withCoord int
	for _, r := range ix.records {
		switch r.Band {
		case "FM":
			fm++
		case "AM":
			om++
		}
		if r.HasCoord {
			withCoord++
		}
	}
	if fm < 9000 || om < 2000 {
		t.Errorf("planos parseados parcialmente: FM=%d OM=%d", fm, om)
	}
	if withCoord < fm/2 {
		t.Errorf("só %d de %d registros com coordenada — coluna de lat/long provavelmente errada", withCoord, fm+om)
	}
}

// TestPBFMHasNo879 documenta em teste a razão de existir o tier radcom:
// comunitária não está no Plano Básico de FM.
func TestPBFMHasNo879(t *testing.T) {
	ix := mustIndex(t)
	for _, r := range ix.records {
		if r.Band == "FM" && math.Abs(r.FreqMHz-87.9) < 0.001 {
			t.Fatalf("PBFM tem registro em 87,9 MHz (%s/%s) — revisar a premissa do tier radcom", r.City, r.UF)
		}
	}
}

func TestMatchExact(t *testing.T) {
	ix := mustIndex(t)
	m, ok := ix.Match(Station{
		Name: "Iguatemi - FM (94.7)", Band: "FM", City: "Bebedouro", State: "SP",
		FreqMHz: 94.7, Lat: -20.9491, Lng: -48.4791, HasCoord: true,
	})
	if !ok {
		t.Fatal("Bebedouro 94.7 deveria casar exato")
	}
	if m.Tier != TierExact || m.Class != "A4" {
		t.Errorf("tier=%q classe=%q, esperado exact/A4", m.Tier, m.Class)
	}
	if m.CoverageKm != 24.0 {
		t.Errorf("cobertura=%v, esperado 24.0 (classe A4)", m.CoverageKm)
	}
	if !m.HasDistance || m.DistanceKm > 5 {
		t.Errorf("distância=%v (has=%v); antena deveria estar a poucos km do centroide", m.DistanceKm, m.HasDistance)
	}
}

// TestMatchExactIgnoresCityCasingAndAccents: o cadastro tem "Bebedouro", o
// plano tem o mesmo nome — mas em outras cidades diverge em acento/caixa.
func TestMatchExactIgnoresCityCasingAndAccents(t *testing.T) {
	ix := mustIndex(t)
	base := Station{Band: "FM", City: "Bebedouro", State: "SP", FreqMHz: 94.7}
	variants := []string{"bebedouro", "BEBEDOURO", "  Bebedouro  "}
	for _, v := range variants {
		s := base
		s.City = v
		if m, ok := ix.Match(s); !ok || m.Class != "A4" {
			t.Errorf("variante %q não casou (ok=%v classe=%q)", v, ok, m.Class)
		}
	}
}

// TestMatchGeoLicensedInNeighbouringCity é o caso que motivou o tier geo:
// a CBN de Porto Alegre é licenciada em Canoas. Cidade não bate, dial bate,
// antena a ~6 km — dentro dos 24 km da classe A4.
func TestMatchGeoLicensedInNeighbouringCity(t *testing.T) {
	ix := mustIndex(t)
	m, ok := ix.Match(Station{
		Name: "CBN - FM (79.1)", Band: "FM", City: "Porto Alegre", State: "RS",
		FreqMHz: 79.1, Lat: -30.0318, Lng: -51.2065, HasCoord: true,
	})
	if !ok {
		t.Fatal("deveria casar via tier geo com Canoas")
	}
	if m.Tier != TierGeo || m.Class != "A4" {
		t.Errorf("tier=%q classe=%q, esperado geo/A4", m.Tier, m.Class)
	}
	if m.PlanCity != "Canoas" {
		t.Errorf("município de licença=%q, esperado Canoas", m.PlanCity)
	}
	if m.DistanceKm > 10 {
		t.Errorf("distância=%v km, esperado <10", m.DistanceKm)
	}
}

// TestMatchGeoRejectsDistantCoChannel: a Jovem Pan de Cuiabá tem 90,9 no dial,
// e o registro 90,9 mais próximo do MT é uma classe C em Chapada dos Guimarães
// a ~37 km. Classe C cobre 7,5 km — não pode ser a mesma emissora. Sem o teto
// por classe, esse falso positivo entraria.
func TestMatchGeoRejectsDistantCoChannel(t *testing.T) {
	ix := mustIndex(t)
	m, ok := ix.Match(Station{
		Name: "Jovem Pan", Band: "FM", City: "Cuiabá", State: "MT",
		FreqMHz: 90.9, Lat: -15.601, Lng: -56.0974, HasCoord: true,
	})
	if ok {
		t.Fatalf("co-canal distante foi aceito: %+v", m)
	}
}

// TestMatchGeoRequiresCoords: sem coordenada não há tier geo.
func TestMatchGeoRequiresCoords(t *testing.T) {
	ix := mustIndex(t)
	if m, ok := ix.Match(Station{
		Name: "CBN", Band: "FM", City: "Porto Alegre", State: "RS", FreqMHz: 79.1,
	}); ok {
		t.Errorf("casou sem coordenada: %+v", m)
	}
}

// TestAMHasNoGeoTier: existe uma AM 680 em São Gonçalo a 23 km do centroide do
// Rio. Como OM não tem raio de classe, não há como validar o candidato — então
// não casamos, em vez de chutar.
func TestAMHasNoGeoTier(t *testing.T) {
	ix := mustIndex(t)
	if m, ok := ix.Match(Station{
		Name: "Copacabana - AM (680)", Band: "AM", City: "Rio de Janeiro", State: "RJ",
		FreqMHz: 680, Lat: -22.9129, Lng: -43.2003, HasCoord: true,
	}); ok {
		t.Errorf("AM casou via geo (%+v); OM não tem raio de classe para validar", m)
	}
}

// TestAMExactStillWorks: o tier exact independe de raio.
func TestAMExactStillWorks(t *testing.T) {
	ix := mustIndex(t)
	m, ok := ix.Match(Station{
		Name: "Tupi - AM (1280)", Band: "AM", City: "Rio de Janeiro", State: "RJ", FreqMHz: 1280,
	})
	if !ok {
		t.Fatal("AM 1280 Rio deveria casar exato")
	}
	if m.Tier != TierExact {
		t.Errorf("tier=%q, esperado exact", m.Tier)
	}
	if m.CoverageKm != 0 {
		t.Errorf("cobertura=%v; AM não pode ter raio derivado da classe", m.CoverageKm)
	}
}

// TestAmbiguityPrefersPrincipal: Acrelândia/AC 94,1 tem duas entradas — uma
// E3 Principal e uma C Complementar. A Principal é a emissora de verdade.
func TestAmbiguityPrefersPrincipal(t *testing.T) {
	ix := mustIndex(t)
	m, ok := ix.Match(Station{Band: "FM", City: "Acrelândia", State: "AC", FreqMHz: 94.1})
	if !ok {
		t.Fatal("Acrelândia 94.1 deveria casar")
	}
	if m.Class != "E3" {
		t.Errorf("classe=%q, esperado E3 (Principal) e não a Complementar", m.Class)
	}
	if !m.Ambiguous {
		t.Error("match deveria vir marcado como ambíguo (2 candidatos)")
	}
}

func TestRadComShortCircuits(t *testing.T) {
	ix := mustIndex(t)
	cases := []Station{
		{Name: "Sertaneja - Comunitária (87.9)", Band: "FM", City: "Vicentinópolis", State: "GO", FreqMHz: 87.9},
		{Name: "Cidade Nova - Comunitária (104.9)", Band: "FM", City: "Itajuípe", State: "BA", FreqMHz: 104.9},
	}
	for _, s := range cases {
		m, ok := ix.Match(s)
		if !ok {
			t.Fatalf("%s deveria ser reconhecida como RadCom", s.Name)
		}
		if m.Tier != TierRadCom || m.Class != ClassRadCom || m.CoverageKm != 1.0 {
			t.Errorf("%s: %+v, esperado radcom/RADCOM/1.0km", s.Name, m)
		}
	}
}

func TestIsRadCom(t *testing.T) {
	yes := []struct {
		name string
		freq float64
	}{
		{"Qualquer Nome", 87.9},
		{"Tropical - Comunitária (104.9)", 104.9},
		{"RADIO COMUNITARIA X", 100.1},
	}
	for _, c := range yes {
		if !IsRadCom(c.name, c.freq) {
			t.Errorf("IsRadCom(%q,%v)=false", c.name, c.freq)
		}
	}
	if IsRadCom("Jovem Pan", 90.9) {
		t.Error("comercial classificada como RadCom")
	}
}

func TestCoverageCities(t *testing.T) {
	g, err := geo.New()
	if err != nil {
		t.Fatalf("geo: %v", err)
	}
	// Antena da classe A3 em Nova Lima que atende a Grande BH: 30 km de raio
	// tem que incluir Belo Horizonte e Nova Lima, e excluir Ouro Preto (~65 km).
	cities := CoverageCities(g, -19.97121, -43.93003, 30.0)
	if len(cities) < 2 {
		t.Fatalf("só %d cidades em 30 km da Grande BH", len(cities))
	}
	found := map[string]float64{}
	for _, c := range cities {
		found[c.Name] = c.DistanceKm
	}
	for _, want := range []string{"Belo Horizonte", "Nova Lima"} {
		if _, ok := found[want]; !ok {
			t.Errorf("%s não apareceu na cobertura de 30 km", want)
		}
	}
	if d, ok := found["Ouro Preto"]; ok {
		t.Errorf("Ouro Preto (a %.1f km) não deveria entrar num raio de 30 km", d)
	}
	// ordenado do mais próximo ao mais distante
	for i := 1; i < len(cities); i++ {
		if cities[i-1].DistanceKm > cities[i].DistanceKm {
			t.Fatalf("resultado fora de ordem em %d", i)
		}
	}
	// todos dentro do raio
	for _, c := range cities {
		if c.DistanceKm > 30.0 {
			t.Errorf("%s a %.1f km ultrapassa o raio", c.Name, c.DistanceKm)
		}
	}
}

func TestCoverageCitiesZeroRadius(t *testing.T) {
	g, err := geo.New()
	if err != nil {
		t.Fatalf("geo: %v", err)
	}
	// Raio 0 = cobertura indeterminada (AM). Não pode devolver "a cidade mais
	// próxima" nem o país inteiro: devolve nada.
	if got := CoverageCities(g, -19.97, -43.93, 0); got != nil {
		t.Errorf("raio 0 devolveu %d cidades", len(got))
	}
}

// TestWithHomeCitiesForcesLicensedCity é o caso real que motivou a função:
// a Guanambi FM 96,3 é classe B1 e a antena do plano está a 19,1 km do
// centroide de Guanambi — fora do contorno protegido de 16,5 km, ou seja, a
// cidade caía fora da própria cobertura.
//
// Em produção esse caso específico hoje é absorvido pelo transbordo (24,75 km
// de alcance), mas o teste continua exercitando o contorno puro de propósito:
// a garantia da sede não pode depender do transbordo existir. Para a RadCom,
// cujo alcance é de só 1,5 km, ela segue sendo a única proteção.
func TestWithHomeCitiesForcesLicensedCity(t *testing.T) {
	g, err := geo.New()
	if err != nil {
		t.Fatalf("geo: %v", err)
	}
	const antLat, antLng, radius = -14.080000, -42.681389, 16.5

	base := CoverageCities(g, antLat, antLng, radius)
	for _, c := range base {
		if c.Name == "Guanambi" {
			t.Fatal("premissa do teste quebrou: Guanambi já entrava sem forçar")
		}
	}

	got := WithHomeCities(g, base, antLat, antLng, CityRef{City: "Guanambi", UF: "BA"})
	var home *CoveredCity
	for i := range got {
		if got[i].Name == "Guanambi" {
			home = &got[i]
		}
	}
	if home == nil {
		t.Fatal("Guanambi não entrou na cobertura da própria emissora")
	}
	if !home.IsHome {
		t.Error("Guanambi entrou sem a marca IsHome — a inclusão forçada tem que ser auditável")
	}
	if home.DistanceKm <= radius {
		t.Errorf("distância %v deveria estar acima do raio %v (é o que o teste documenta)", home.DistanceKm, radius)
	}
	if len(got) != len(base)+1 {
		t.Errorf("adicionou %d cidades, esperado 1", len(got)-len(base))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].DistanceKm > got[i].DistanceKm {
			t.Fatalf("lista saiu fora de ordem em %d", i)
		}
	}
}

// TestWithHomeCitiesMarksExisting: quando a sede JÁ está na cobertura, ela é
// só marcada — não duplicada.
func TestWithHomeCitiesMarksExisting(t *testing.T) {
	g, err := geo.New()
	if err != nil {
		t.Fatalf("geo: %v", err)
	}
	// Antena da A3 de Nova Lima: Belo Horizonte está a 6,8 km, bem dentro.
	base := CoverageCities(g, -19.97121, -43.93003, 30.0)
	got := WithHomeCities(g, base, -19.97121, -43.93003, CityRef{City: "Belo Horizonte", UF: "MG"})
	if len(got) != len(base) {
		t.Fatalf("duplicou município já coberto: %d → %d", len(base), len(got))
	}
	var marked int
	for _, c := range got {
		if c.IsHome {
			marked++
		}
	}
	if marked != 1 {
		t.Errorf("%d municípios marcados IsHome, esperado 1", marked)
	}
}

// TestWithHomeCitiesIgnoresUnknown: cidade fora do dataset (grafia errada,
// distrito administrativo do DF, emissora estrangeira) é ignorada em silêncio,
// não vira linha inventada.
func TestWithHomeCitiesIgnoresUnknown(t *testing.T) {
	g, err := geo.New()
	if err != nil {
		t.Fatalf("geo: %v", err)
	}
	base := CoverageCities(g, -19.97121, -43.93003, 30.0)
	got := WithHomeCities(g, base, -19.97121, -43.93003,
		CityRef{City: "Recanto das Emas", UF: "DF"}, // região administrativa, não município
		CityRef{City: "Buenos Aires", UF: "DF"},     // estrangeira
		CityRef{City: "", UF: ""},                   // sem licença (RadCom)
	)
	if len(got) != len(base) {
		t.Errorf("inventou %d linhas para cidades fora do dataset", len(got)-len(base))
	}
}
