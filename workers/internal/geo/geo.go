// Package geo resolves a Brazilian city + UF (state abbreviation) to the
// latitude/longitude of the municipality's centroid, using an embedded IBGE
// dataset. No network calls: the lookup is an in-memory map built once from a
// CSV compiled into the binary. Because we only ever have city+UF (never a
// street address), municipality-centroid precision is the best achievable.
package geo

import (
	"bytes"
	_ "embed"
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

//go:embed data/municipios.csv
var municipiosCSV []byte

type coord struct{ Lat, Lng float64 }

// Geocoder holds an in-memory index keyed by "<normalized name>|<UF>".
type Geocoder struct {
	byKey    map[string]coord
	idxByKey map[string]int // mesma chave de byKey, apontando para `all`
	all      []Municipality
}

// Municipality é um município do dataset IBGE com seu centroide.
type Municipality struct {
	IBGECode int
	Name     string
	UF       string
	Lat, Lng float64
}

// ufByCode maps the numeric IBGE state code to the 2-letter UF abbreviation.
// Fixed table — the 27 federative units don't change.
var ufByCode = map[string]string{
	"11": "RO", "12": "AC", "13": "AM", "14": "RR", "15": "PA", "16": "AP", "17": "TO",
	"21": "MA", "22": "PI", "23": "CE", "24": "RN", "25": "PB", "26": "PE", "27": "AL", "28": "SE", "29": "BA",
	"31": "MG", "32": "ES", "33": "RJ", "35": "SP",
	"41": "PR", "42": "SC", "43": "RS",
	"50": "MS", "51": "MT", "52": "GO", "53": "DF",
}

func key(normName, uf string) string { return normName + "|" + uf }

// normalize lowercases, strips diacritics, trims, and collapses internal
// whitespace so "São  Paulo", "sao paulo" and "SÃO PAULO" map to one key.
func normalize(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, err := transform.String(t, s)
	if err != nil {
		out = s
	}
	out = strings.ToLower(strings.TrimSpace(out))
	out = strings.Join(strings.Fields(out), " ")
	return out
}

// New builds a Geocoder from the embedded dataset. Returns an error only if the
// embedded CSV is malformed — a build-time error, not a runtime one.
func New() (*Geocoder, error) {
	r := csv.NewReader(bytes.NewReader(municipiosCSV))
	r.FieldsPerRecord = -1 // tolerate trailing columns we don't read
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("geo: read header: %w", err)
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.TrimSpace(strings.ToLower(h))] = i
	}
	iName, okN := col["nome"]
	iLat, okLa := col["latitude"]
	iLng, okLo := col["longitude"]
	iUF, okU := col["codigo_uf"]
	iCode := col["codigo_ibge"]
	if !okN || !okLa || !okLo || !okU {
		return nil, fmt.Errorf("geo: dataset missing required columns (have %v)", header)
	}
	g := &Geocoder{
		byKey:    make(map[string]coord, 6000),
		idxByKey: make(map[string]int, 6000),
		all:      make([]Municipality, 0, 6000),
	}
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("geo: read row: %w", err)
		}
		uf := ufByCode[strings.TrimSpace(rec[iUF])]
		if uf == "" {
			continue // unknown/blank state code → skip defensively
		}
		lat, err1 := strconv.ParseFloat(strings.TrimSpace(rec[iLat]), 64)
		lng, err2 := strconv.ParseFloat(strings.TrimSpace(rec[iLng]), 64)
		if err1 != nil || err2 != nil {
			continue
		}
		g.byKey[key(normalize(rec[iName]), uf)] = coord{Lat: lat, Lng: lng}
		code, _ := strconv.Atoi(strings.TrimSpace(rec[iCode]))
		g.idxByKey[key(normalize(rec[iName]), uf)] = len(g.all)
		g.all = append(g.all, Municipality{
			IBGECode: code,
			Name:     strings.TrimSpace(rec[iName]),
			UF:       uf,
			Lat:      lat,
			Lng:      lng,
		})
	}
	if len(g.byKey) == 0 {
		return nil, fmt.Errorf("geo: empty dataset")
	}
	return g, nil
}

// Lookup returns the municipality centroid for city+state, or ok=false if the
// pair isn't in the dataset. Applies the same normalization used at index time.
func (g *Geocoder) Lookup(city, state string) (lat, lng float64, ok bool) {
	city = strings.TrimSpace(city)
	state = strings.ToUpper(strings.TrimSpace(state))
	if city == "" || state == "" {
		return 0, 0, false
	}
	c, ok := g.byKey[key(normalize(city), state)]
	if !ok {
		return 0, 0, false
	}
	return c.Lat, c.Lng, true
}

// LookupMunicipality é como Lookup, mas devolve o município inteiro (código
// IBGE e nome oficial), não só o centroide.
func (g *Geocoder) LookupMunicipality(city, state string) (Municipality, bool) {
	city = strings.TrimSpace(city)
	state = strings.ToUpper(strings.TrimSpace(state))
	if city == "" || state == "" {
		return Municipality{}, false
	}
	i, ok := g.idxByKey[key(normalize(city), state)]
	if !ok {
		return Municipality{}, false
	}
	return g.all[i], true
}

var (
	defaultOnce sync.Once
	defaultGeo  *Geocoder
)

// Default returns a process-wide Geocoder, loaded once. It panics if the
// embedded dataset fails to load: that can only happen with a corrupt build,
// which the package tests catch before merge — so failing loudly at startup is
// preferable to silently disabling geocoding in production.
func Default() *Geocoder {
	defaultOnce.Do(func() {
		g, err := New()
		if err != nil {
			panic("geo: load embedded dataset: " + err.Error())
		}
		defaultGeo = g
	})
	return defaultGeo
}

// All devolve todos os municípios do dataset embutido. A fatia é a interna —
// tratada como somente-leitura pelos chamadores (o dataset é imutável).
func (g *Geocoder) All() []Municipality { return g.all }

// DistanceKm devolve a distância em grande-círculo (haversine) entre dois
// pontos, em quilômetros. Precisão de esfera basta aqui: o erro contra o
// elipsoide (<0,3%) é ordens de grandeza menor que o erro de usar o centroide
// do município como posição da cidade.
func DistanceKm(lat1, lng1, lat2, lng2 float64) float64 {
	const earthKm = 6371.0
	rad := func(d float64) float64 { return d * math.Pi / 180 }
	dLat, dLng := rad(lat2-lat1), rad(lng2-lng1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(rad(lat1))*math.Cos(rad(lat2))*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * earthKm * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

// Normalize expõe a normalização de nome de município usada no índice
// (minúsculas, sem acentos, espaços colapsados). Outros pacotes que cruzam
// nomes de cidade com este dataset PRECISAM usar a mesma função, senão as
// chaves divergem.
func Normalize(s string) string { return normalize(s) }
