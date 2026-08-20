// Package anatel cruza as emissoras cadastradas no Radiocheck com os Planos
// Básicos de Distribuição de Canais da Anatel (PBFM para FM, PBOM para OM/AM),
// para descobrir a CLASSE de cada emissora e, a partir dela, o raio do contorno
// protegido — que é o que permite listar as cidades no alcance do sinal.
//
// Os planos NÃO trazem o nome/razão social da emissora, só (município, UF,
// frequência, classe, coordenada da antena). Então o cruzamento é por dial +
// geografia, em dois níveis:
//
//	exact — banda + UF + município + frequência batem exatamente.
//	geo   — mesma banda e frequência, e a antena do plano está dentro do raio
//	        de cobertura da própria classe candidata a partir da coordenada da
//	        emissora. Pega o caso comum de emissora licenciada numa cidade e
//	        cadastrada na cidade do mercado (CBN Porto Alegre licenciada em
//	        Canoas, Projeção FM Jaboatão licenciada no Recife, etc).
//
// O tier `geo` usa o raio da CLASSE do candidato como teto — não uma constante
// arbitrária. Isso descarta co-canal distante: uma classe C (7,5 km) a 36 km
// não pode ser a mesma emissora, por mais que o dial bata.
package anatel

import (
	"bytes"
	_ "embed"
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"

	"radiocheck/internal/geo"
)

//go:embed data/PBFM.csv
var pbfmCSV []byte

//go:embed data/PBOM.csv
var pbomCSV []byte

// Record é uma linha do Plano Básico.
type Record struct {
	Band     string // "FM" ou "AM"
	UF       string
	City     string // nome do município como vem do plano
	cityNorm string
	FreqMHz  float64 // FM em MHz, AM em kHz (mesma unidade de stations.frequency_mhz)
	Class    string
	Service  string // FM, RTRFM, OM
	Category string // Principal, Complementar, Reserva ou vazio
	ERPkW    float64
	Lat, Lng float64
	HasCoord bool
}

// Index é o índice em memória dos dois planos.
type Index struct {
	records    []Record
	byCityFreq map[string][]int
	byBandFreq map[string][]int
}

// freqKey discretiza a frequência em centésimos para virar chave inteira
// exata: 94.7 e 94.70 colidem no mesmo bucket, 94.7 e 94.9 não.
func freqKey(f float64) int64 { return int64(math.Round(f * 100)) }

func cityFreqKey(band, uf, cityNorm string, f float64) string {
	return band + "|" + uf + "|" + cityNorm + "|" + strconv.FormatInt(freqKey(f), 10)
}

func bandFreqKey(band string, f float64) string {
	return band + "|" + strconv.FormatInt(freqKey(f), 10)
}

// colFinder resolve índices de coluna pelo nome do cabeçalho, tolerando os
// sufixos que a Anatel varia entre os dois arquivos ("UF PBFM" vs "UF PBOM",
// "Latitude Decimal PBOM" vs "Latitude Decimal").
type colFinder map[string]int

// bom é o byte order mark UTF-8 que a Anatel deixa no começo do arquivo — sem
// removê-lo, a primeira coluna do header nunca casa pelo nome.
const bom = "\uFEFF"

func newColFinder(header []string) colFinder {
	cf := colFinder{}
	for i, h := range header {
		cf[geo.Normalize(strings.TrimPrefix(h, bom))] = i
	}
	return cf
}

// find devolve a coluna cujo nome normalizado é exatamente `exact`; se não
// houver, a primeira (em ordem de nome) que comece por `exact`. -1 se nenhuma.
func (cf colFinder) find(exact string) int {
	if i, ok := cf[exact]; ok {
		return i
	}
	best, bestName := -1, ""
	for name, i := range cf {
		if strings.HasPrefix(name, exact) && (bestName == "" || name < bestName) {
			best, bestName = i, name
		}
	}
	return best
}

func parseFloat(s string) (float64, bool) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", "."))
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func parsePlan(raw []byte, band string) ([]Record, error) {
	r := csv.NewReader(bytes.NewReader(raw))
	r.Comma = ';'
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("anatel: header %s: %w", band, err)
	}
	cf := newColFinder(header)
	iMun := cf.find("municipio-uf")
	iUF := cf.find("uf")
	iFreq := cf.find("frequencia")
	iClass := cf.find("classe")
	iSrv := cf.find("servico")
	iCat := cf.find("categoria da estacao")
	iERP := cf.find("erp")
	iLat := cf.find("latitude decimal")
	iLng := cf.find("longitude decimal")
	if iMun < 0 || iUF < 0 || iFreq < 0 || iClass < 0 {
		return nil, fmt.Errorf("anatel: %s sem colunas obrigatórias (header=%v)", band, header)
	}

	at := func(rec []string, i int) string {
		if i < 0 || i >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[i])
	}

	var out []Record
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("anatel: linha %s: %w", band, err)
		}
		freq, ok := parseFloat(at(rec, iFreq))
		if !ok {
			continue
		}
		class := strings.ToUpper(at(rec, iClass))
		if class == "" {
			continue
		}
		// "Município-UF" vem como "Acrelândia - AC": o nome é tudo antes do
		// último " - " (há municípios com hífen no nome).
		mun := at(rec, iMun)
		if i := strings.LastIndex(mun, " - "); i >= 0 {
			mun = mun[:i]
		}
		e := Record{
			Band:     band,
			UF:       strings.ToUpper(at(rec, iUF)),
			City:     mun,
			cityNorm: geo.Normalize(mun),
			FreqMHz:  freq,
			Class:    class,
			Service:  at(rec, iSrv),
			Category: at(rec, iCat),
		}
		e.ERPkW, _ = parseFloat(at(rec, iERP))
		lat, okLat := parseFloat(at(rec, iLat))
		lng, okLng := parseFloat(at(rec, iLng))
		if okLat && okLng && (lat != 0 || lng != 0) {
			e.Lat, e.Lng, e.HasCoord = lat, lng, true
		}
		out = append(out, e)
	}
	return out, nil
}

// New carrega e indexa os dois planos embutidos.
func New() (*Index, error) {
	fm, err := parsePlan(pbfmCSV, "FM")
	if err != nil {
		return nil, err
	}
	om, err := parsePlan(pbomCSV, "AM")
	if err != nil {
		return nil, err
	}
	idx := &Index{
		records:    append(fm, om...),
		byCityFreq: make(map[string][]int, len(fm)+len(om)),
		byBandFreq: make(map[string][]int, 2048),
	}
	if len(idx.records) == 0 {
		return nil, fmt.Errorf("anatel: planos vazios")
	}
	for i := range idx.records {
		e := &idx.records[i]
		ck := cityFreqKey(e.Band, e.UF, e.cityNorm, e.FreqMHz)
		idx.byCityFreq[ck] = append(idx.byCityFreq[ck], i)
		if e.HasCoord {
			bk := bandFreqKey(e.Band, e.FreqMHz)
			idx.byBandFreq[bk] = append(idx.byBandFreq[bk], i)
		}
	}
	return idx, nil
}

// Len devolve quantos registros os planos trouxeram.
func (ix *Index) Len() int { return len(ix.records) }

// Station é a entrada do cruzamento — o que sabemos da emissora no Radiocheck.
type Station struct {
	Name     string
	Band     string
	City     string
	State    string
	FreqMHz  float64
	Lat, Lng float64
	HasCoord bool
}

// Tiers do cruzamento.
const (
	TierExact  = "exact"
	TierGeo    = "geo"
	TierRadCom = "radcom"
)

// Match é o resultado do cruzamento de uma emissora.
type Match struct {
	Tier       string
	Class      string
	CoverageKm float64 // contorno protegido; 0 quando a classe não define em distância (AM)
	ReachKm    float64 // contorno + transbordo; é o raio que lista as cidades
	ERPkW      float64
	Lat, Lng   float64
	HasCoord   bool
	// DistanceKm é a distância entre a coordenada da emissora e a antena do
	// plano. Só preenchida quando ambas existem — serve de auditoria do match.
	DistanceKm  float64
	HasDistance bool
	// PlanCity/PlanUF é o município de LICENÇA, que pode diferir do cadastrado.
	PlanCity, PlanUF string
	// Ambiguous indica que havia mais de um candidato no tier `exact` e o
	// desempate por Categoria/ERP escolheu um.
	Ambiguous bool
}

// IsRadCom reconhece rádio comunitária. O PBFM não tem NENHUM registro em
// 87,9 MHz — comunitária tem plano próprio (RadCom) — então tentar casar essas
// emissoras contra o PBFM só produz falso negativo. Como a Lei 9.612/98 fixa
// os mesmos parâmetros para todas elas, a classe é determinável sem plano.
func IsRadCom(name string, freqMHz float64) bool {
	if math.Abs(freqMHz-87.9) < 0.001 {
		return true
	}
	return strings.Contains(geo.Normalize(name), "comunit")
}

// pickBest desempata candidatos do tier exact: a estação Principal ganha da
// Complementar/Reserva (é a emissora de verdade; as outras são retransmissão
// ou reserva técnica no mesmo canal), depois a de maior ERP.
func (ix *Index) pickBest(ids []int) (int, bool) {
	if len(ids) == 0 {
		return 0, false
	}
	rank := func(cat string) int {
		switch strings.ToLower(strings.TrimSpace(cat)) {
		case "principal":
			return 0
		case "":
			return 1
		default: // complementar, reserva
			return 2
		}
	}
	sorted := append([]int(nil), ids...)
	sort.SliceStable(sorted, func(a, b int) bool {
		ra, rb := rank(ix.records[sorted[a]].Category), rank(ix.records[sorted[b]].Category)
		if ra != rb {
			return ra < rb
		}
		return ix.records[sorted[a]].ERPkW > ix.records[sorted[b]].ERPkW
	})
	return sorted[0], true
}

func (ix *Index) matchFrom(i int, tier string, s Station) Match {
	e := ix.records[i]
	km, _ := CoverageKm(e.Band, e.Class)
	reach, _ := ReachKm(e.Band, e.Class)
	m := Match{
		Tier: tier, Class: e.Class, CoverageKm: km, ReachKm: reach, ERPkW: e.ERPkW,
		Lat: e.Lat, Lng: e.Lng, HasCoord: e.HasCoord,
		PlanCity: e.City, PlanUF: e.UF,
	}
	if e.HasCoord && s.HasCoord {
		m.DistanceKm = geo.DistanceKm(s.Lat, s.Lng, e.Lat, e.Lng)
		m.HasDistance = true
	}
	return m
}

// Match cruza uma emissora contra os planos. ok=false quando nenhum tier
// produziu candidato aceitável.
func (ix *Index) Match(s Station) (Match, bool) {
	if IsRadCom(s.Name, s.FreqMHz) {
		spec, _ := Spec(s.Band, ClassRadCom)
		return Match{
			Tier: TierRadCom, Class: ClassRadCom,
			CoverageKm: spec.CoverageKm,
			ReachKm:    spec.CoverageKm * TransbordoFactor,
			ERPkW:      spec.MaxERPkW,
			Lat:        s.Lat, Lng: s.Lng, HasCoord: s.HasCoord,
		}, true
	}

	band := strings.ToUpper(strings.TrimSpace(s.Band))
	uf := strings.ToUpper(strings.TrimSpace(s.State))
	cityNorm := geo.Normalize(s.City)

	if ids := ix.byCityFreq[cityFreqKey(band, uf, cityNorm, s.FreqMHz)]; len(ids) > 0 {
		if i, ok := ix.pickBest(ids); ok {
			m := ix.matchFrom(i, TierExact, s)
			m.Ambiguous = len(ids) > 1
			return m, true
		}
	}

	// Tier geo: só faz sentido quando existe raio de classe para validar o
	// candidato. Em AM não existe (contorno protegido de OM é definido em
	// mV/m, não em km), então não há fallback geográfico — sem teto, o
	// vizinho co-canal mais próximo seria sempre "aceito".
	if !s.HasCoord {
		return Match{}, false
	}
	// Repare: o teto aqui é CoverageKm (contorno protegido), NÃO ReachKm.
	// Identificar QUAL emissora é esta e estimar ATÉ ONDE ela é ouvida são
	// decisões diferentes, com tolerâncias diferentes. A primeira quer o
	// critério normativo mais apertado; afrouxá-la em 50% faria cada classe
	// engolir co-canais de vizinhos a mais — exatamente o falso positivo que
	// este tier existe para evitar. O transbordo entra depois, só para ampliar
	// a lista de cidades da emissora já identificada.
	bestIdx, bestDist := -1, math.MaxFloat64
	for _, i := range ix.byBandFreq[bandFreqKey(band, s.FreqMHz)] {
		e := ix.records[i]
		radius, ok := CoverageKm(e.Band, e.Class)
		if !ok {
			continue
		}
		d := geo.DistanceKm(s.Lat, s.Lng, e.Lat, e.Lng)
		if d <= radius && d < bestDist {
			bestIdx, bestDist = i, d
		}
	}
	if bestIdx < 0 {
		return Match{}, false
	}
	return ix.matchFrom(bestIdx, TierGeo, s), true
}

// CoveredCity é um município dentro do contorno protegido de uma emissora.
type CoveredCity struct {
	geo.Municipality
	DistanceKm float64
	// IsHome marca o município de licença/cadastro da emissora. Ver
	// WithHomeCities para por que ele pode aparecer fora do raio.
	IsHome bool
}

// CityRef identifica um município pelo nome + UF, como está no cadastro ou no
// plano da Anatel.
type CityRef struct{ City, UF string }

// WithHomeCities garante que os municípios de referência da emissora — o de
// licença e o do cadastro — estejam na lista de cobertura, marcados IsHome.
//
// Por que forçar: o teste de cobertura mede a distância da antena ao CENTROIDE
// do município, e as duas pontas têm erro. A antena costuma ficar num morro
// fora da cidade (as de Guanambi/BA estão 11–19 km a nordeste da sede), e o
// centroide não é a sede. Com isso um município podia cair fora do próprio
// contorno protegido — a Guanambi FM 96,3, classe B1 de 16,5 km, tinha a
// antena a 19,1 km do centroide e "não cobria Guanambi". Como a emissora é
// licenciada para atender aquele município, a cobertura da sede é premissa da
// outorga, não algo a inferir de geometria aproximada.
//
// Consequência: uma linha IsHome PODE ter DistanceKm maior que o raio da
// classe. A distância real fica gravada — quem auditar vê o quanto estourou.
func WithHomeCities(g *geo.Geocoder, cities []CoveredCity, lat, lng float64, refs ...CityRef) []CoveredCity {
	for _, ref := range refs {
		m, ok := g.LookupMunicipality(ref.City, ref.UF)
		if !ok {
			continue // cidade fora do dataset (grafia, distrito, exterior)
		}
		found := false
		for i := range cities {
			if cities[i].IBGECode == m.IBGECode {
				cities[i].IsHome = true
				found = true
				break
			}
		}
		if found {
			continue
		}
		cities = append(cities, CoveredCity{
			Municipality: m,
			DistanceKm:   geo.DistanceKm(lat, lng, m.Lat, m.Lng),
			IsHome:       true,
		})
	}
	sort.Slice(cities, func(a, b int) bool { return cities[a].DistanceKm < cities[b].DistanceKm })
	return cities
}

// CoverageCities devolve os municípios cujo centroide está dentro de `radiusKm`
// do ponto (lat,lng), ordenados do mais próximo ao mais distante.
func CoverageCities(g *geo.Geocoder, lat, lng, radiusKm float64) []CoveredCity {
	if radiusKm <= 0 {
		return nil
	}
	var out []CoveredCity
	for _, m := range g.All() {
		d := geo.DistanceKm(lat, lng, m.Lat, m.Lng)
		if d <= radiusKm {
			out = append(out, CoveredCity{Municipality: m, DistanceKm: d})
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].DistanceKm < out[b].DistanceKm })
	return out
}

var (
	defaultOnce sync.Once
	defaultIdx  *Index
	defaultErr  error
)

// Default devolve um Index carregado uma vez por processo.
func Default() (*Index, error) {
	defaultOnce.Do(func() { defaultIdx, defaultErr = New() })
	return defaultIdx, defaultErr
}
