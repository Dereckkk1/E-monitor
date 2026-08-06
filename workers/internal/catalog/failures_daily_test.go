package catalog

import (
	"github.com/google/uuid"
	"testing"
	"time"
)

func dayOf(s string) time.Time {
	d, err := time.ParseInLocation("2006-01-02", s, time.Local)
	if err != nil {
		panic(err)
	}
	return d
}

// A série tem que ser densa: um gráfico de barras com dias faltando mente sobre
// o padrão do mês. Dia sem falha existe, com zero.
func TestBuildDailyResult_FillsGapsWithZeroDays(t *testing.T) {
	got := buildDailyResult(dayOf("2026-07-01"), dayOf("2026-07-05"), map[string]DailyPoint{
		"2026-07-02": {Stations: 3, Campaigns: 5, Deficit: 12, DownSeconds: 900},
		"2026-07-05": {Stations: 1, Campaigns: 1, Deficit: 2},
	})

	if len(got.Days) != 5 {
		t.Fatalf("dias = %d, esperado 5 (range inclusivo nas duas pontas)", len(got.Days))
	}
	want := []string{"2026-07-01", "2026-07-02", "2026-07-03", "2026-07-04", "2026-07-05"}
	for i, w := range want {
		if got.Days[i].Date != w {
			t.Errorf("dia[%d] = %q, esperado %q", i, got.Days[i].Date, w)
		}
	}
	if got.Days[0].Stations != 0 || got.Days[2].Stations != 0 {
		t.Errorf("dias sem falha deviam vir zerados, veio %+v / %+v", got.Days[0], got.Days[2])
	}
	if got.Days[1].Stations != 3 || got.Days[1].Deficit != 12 {
		t.Errorf("dia com dado não bateu: %+v", got.Days[1])
	}
}

func TestBuildDailyResult_Summary(t *testing.T) {
	got := buildDailyResult(dayOf("2026-07-01"), dayOf("2026-07-04"), map[string]DailyPoint{
		"2026-07-01": {Stations: 2, Deficit: 4, DownSeconds: 100},
		"2026-07-03": {Stations: 7, Deficit: 20, DownSeconds: 500},
	})
	s := got.Summary

	if s.DaysTotal != 4 {
		t.Errorf("DaysTotal = %d, esperado 4", s.DaysTotal)
	}
	if s.DaysWithFailure != 2 {
		t.Errorf("DaysWithFailure = %d, esperado 2", s.DaysWithFailure)
	}
	if s.PeakStations != 7 || s.PeakDate != "2026-07-03" {
		t.Errorf("pico = %d em %q, esperado 7 em 2026-07-03", s.PeakStations, s.PeakDate)
	}
	if s.TotalDeficit != 24 {
		t.Errorf("TotalDeficit = %d, esperado 24", s.TotalDeficit)
	}
	if s.TotalDownSeconds != 600 {
		t.Errorf("TotalDownSeconds = %d, esperado 600", s.TotalDownSeconds)
	}
	// Média sobre TODOS os dias do range (9/4), não só os dias com falha — é o
	// número que a UI compara com cada barra na linha de referência.
	if s.AvgStations != 2.25 {
		t.Errorf("AvgStations = %v, esperado 2.25 (9 emissoras / 4 dias)", s.AvgStations)
	}
}

// Range de um dia só: from == to devolve exatamente 1 ponto, não 0 nem 2.
func TestBuildDailyResult_SingleDayRange(t *testing.T) {
	got := buildDailyResult(dayOf("2026-07-10"), dayOf("2026-07-10"), map[string]DailyPoint{
		"2026-07-10": {Stations: 1},
	})
	if len(got.Days) != 1 {
		t.Fatalf("dias = %d, esperado 1", len(got.Days))
	}
	if got.From != "2026-07-10" || got.To != "2026-07-10" {
		t.Errorf("range devolvido = %s..%s", got.From, got.To)
	}
}

// Range sem falha alguma: série cheia de zeros, PeakDate vazio (a UI usa isso
// pra decidir entre gráfico e empty state, então não pode virar "0001-01-01").
func TestBuildDailyResult_NoFailuresAtAll(t *testing.T) {
	got := buildDailyResult(dayOf("2026-07-01"), dayOf("2026-07-03"), map[string]DailyPoint{})

	if len(got.Days) != 3 {
		t.Fatalf("dias = %d, esperado 3", len(got.Days))
	}
	if got.Summary.DaysWithFailure != 0 {
		t.Errorf("DaysWithFailure = %d, esperado 0", got.Summary.DaysWithFailure)
	}
	if got.Summary.PeakDate != "" {
		t.Errorf("PeakDate = %q, esperado vazio quando nada falhou", got.Summary.PeakDate)
	}
	if got.Summary.AvgStations != 0 {
		t.Errorf("AvgStations = %v, esperado 0", got.Summary.AvgStations)
	}
}

// Um dia pode ter déficit sem downtime (silent-gap). Ele conta como dia com
// falha — senão o gráfico esconde justamente o modo de falha mais silencioso.
func TestBuildDailyResult_DeficitWithoutDowntimeCountsAsFailure(t *testing.T) {
	got := buildDailyResult(dayOf("2026-07-01"), dayOf("2026-07-02"), map[string]DailyPoint{
		"2026-07-02": {Stations: 1, Deficit: 3, DownSeconds: 0},
	})
	if got.Summary.DaysWithFailure != 1 {
		t.Errorf("DaysWithFailure = %d, esperado 1 (silent-gap conta)", got.Summary.DaysWithFailure)
	}
}

// Empate no pico resolve pelo dia mais antigo — determinístico entre requests.
func TestBuildDailyResult_PeakTieKeepsEarliestDay(t *testing.T) {
	got := buildDailyResult(dayOf("2026-07-01"), dayOf("2026-07-03"), map[string]DailyPoint{
		"2026-07-01": {Stations: 5},
		"2026-07-03": {Stations: 5},
	})
	if got.Summary.PeakDate != "2026-07-01" {
		t.Errorf("PeakDate = %q, esperado 2026-07-01 no empate", got.Summary.PeakDate)
	}
}

// A série tem que atravessar virada de mês sem buraco nem dia repetido — é o
// caso "últimos 30 dias" no começo de qualquer mês.
func TestBuildDailyResult_CrossesMonthBoundary(t *testing.T) {
	got := buildDailyResult(dayOf("2026-07-30"), dayOf("2026-08-02"), map[string]DailyPoint{})
	want := []string{"2026-07-30", "2026-07-31", "2026-08-01", "2026-08-02"}
	if len(got.Days) != len(want) {
		t.Fatalf("dias = %d, esperado %d", len(got.Days), len(want))
	}
	for i, w := range want {
		if got.Days[i].Date != w {
			t.Errorf("dia[%d] = %q, esperado %q", i, got.Days[i].Date, w)
		}
	}
}

// Fevereiro bissexto: 2028-02-29 existe e não pode ser pulado nem duplicado.
func TestBuildDailyResult_LeapDay(t *testing.T) {
	got := buildDailyResult(dayOf("2028-02-27"), dayOf("2028-03-01"), map[string]DailyPoint{})
	want := []string{"2028-02-27", "2028-02-28", "2028-02-29", "2028-03-01"}
	if len(got.Days) != len(want) {
		t.Fatalf("dias = %d, esperado %d: %+v", len(got.Days), len(want), got.Days)
	}
	for i, w := range want {
		if got.Days[i].Date != w {
			t.Errorf("dia[%d] = %q, esperado %q", i, got.Days[i].Date, w)
		}
	}
}

// acc2points conta emissoras DISTINTAS. A Q1 devolve uma linha por (dia,
// emissora) — mas a MESMA emissora reaparece se ela tiver déficit em campanhas
// diferentes num JOIN futuro, e o set é o que impede o número de divergir da
// lista que a aba "Por emissora" renderiza.
func TestAcc2Points_CountsDistinctStations(t *testing.T) {
	s1, s2 := uuid.New(), uuid.New()
	acc := map[string]*dayAcc{
		"2026-07-02": {
			stations:  map[uuid.UUID]struct{}{s1: {}, s2: {}},
			campaigns: 5,
			deficit:   12,
			downSec:   900,
			downEvts:  3,
		},
	}
	got := acc2points(acc)
	p, ok := got["2026-07-02"]
	if !ok {
		t.Fatal("dia ausente no resultado")
	}
	if p.Stations != 2 {
		t.Errorf("Stations = %d, esperado 2 (set de emissoras)", p.Stations)
	}
	if p.Date != "2026-07-02" {
		t.Errorf("Date = %q, esperado 2026-07-02", p.Date)
	}
	if p.Campaigns != 5 || p.Deficit != 12 || p.DownSeconds != 900 || p.DownEvents != 3 {
		t.Errorf("campos não bateram: %+v", p)
	}
}

// Ponto vindo do banco com Date divergente da chave: a CHAVE manda. Protege
// contra um dia inteiro se deslocar no eixo X por causa de um scan torto.
func TestBuildDailyResult_KeyWinsOverStructDate(t *testing.T) {
	got := buildDailyResult(dayOf("2026-07-01"), dayOf("2026-07-01"), map[string]DailyPoint{
		"2026-07-01": {Date: "1999-01-01", Stations: 4},
	})
	if got.Days[0].Date != "2026-07-01" {
		t.Errorf("Date = %q, esperado a chave 2026-07-01", got.Days[0].Date)
	}
}
