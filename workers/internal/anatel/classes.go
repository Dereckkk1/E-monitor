package anatel

import "strings"

// ClassSpec descreve uma classe de emissora do Plano Básico da Anatel.
//
// CoverageKm é a "distância máxima ao contorno protegido (66 dBµV/m)" —
// o raio, em km, dentro do qual o sinal é protegido contra interferência.
// É a melhor aproximação disponível de área de cobertura a partir da classe.
type ClassSpec struct {
	Class      string
	MaxERPkW   float64
	RefHeightM int
	CoverageKm float64 // 0 = não definido em distância para esta classe
}

// fmClasses é a Tabela de Requisitos Máximos do serviço de FM
// (Anatel, Resolução nº 546/2010). As distâncias foram obtidas para o canal
// 201 e servem como referência — a norma permite ERP/altura maiores desde que
// a distância ao contorno protegido não seja ultrapassada em nenhuma direção,
// então tratamos CoverageKm como TETO da cobertura da classe.
//
//	CLASSE   ERP(kW)  dBk    CONTORNO(km)   ALTURA REF.(m)
//	E1       100      20,0   78,5           600
//	E2        75      18,8   67,5           450
//	E3        60      17,8   54,5           300
//	A1        50      17,0   38,5           150
//	A2        30      14,8   35,0           150
//	A3        15      11,8   30,0           150
//	A4         5       7,0   24,0           150
//	B1         3       4,8   16,5            90
//	B2         1       0,0   12,5            90
//	C          0,3    -5,2    7,5            60
var fmClasses = map[string]ClassSpec{
	"E1": {Class: "E1", MaxERPkW: 100, RefHeightM: 600, CoverageKm: 78.5},
	"E2": {Class: "E2", MaxERPkW: 75, RefHeightM: 450, CoverageKm: 67.5},
	"E3": {Class: "E3", MaxERPkW: 60, RefHeightM: 300, CoverageKm: 54.5},
	"A1": {Class: "A1", MaxERPkW: 50, RefHeightM: 150, CoverageKm: 38.5},
	"A2": {Class: "A2", MaxERPkW: 30, RefHeightM: 150, CoverageKm: 35.0},
	"A3": {Class: "A3", MaxERPkW: 15, RefHeightM: 150, CoverageKm: 30.0},
	"A4": {Class: "A4", MaxERPkW: 5, RefHeightM: 150, CoverageKm: 24.0},
	"B1": {Class: "B1", MaxERPkW: 3, RefHeightM: 90, CoverageKm: 16.5},
	"B2": {Class: "B2", MaxERPkW: 1, RefHeightM: 90, CoverageKm: 12.5},
	"C":  {Class: "C", MaxERPkW: 0.3, RefHeightM: 60, CoverageKm: 7.5},
}

// omClasses são as classes de Onda Média (AM) do PBOM.
//
// CoverageKm é DELIBERADAMENTE 0: ao contrário do FM, a norma de OM define o
// contorno protegido em INTENSIDADE DE CAMPO (mV/m da onda de superfície), não
// em distância. A distância real depende da frequência e da condutividade do
// solo da radial, variando por um fator de vários múltiplos entre duas
// emissoras de mesma classe. Derivar um raio fixo da classe seria inventar
// número; quem consome isso deve tratar AM como "classe conhecida, cobertura
// indeterminada" — ver docs/features/anatel-station-class-coverage.md.
var omClasses = map[string]ClassSpec{
	"A": {Class: "A", CoverageKm: 0},
	"B": {Class: "B", CoverageKm: 0},
	"C": {Class: "C", CoverageKm: 0},
}

// ClassRadCom identifica emissoras do Serviço de Radiodifusão Comunitária.
// Elas não constam do Plano Básico de FM (o PBFM tem ZERO registros em
// 87,9 MHz) — têm plano próprio. Não precisam de match: a lei fixa os
// parâmetros para todas igualmente.
const ClassRadCom = "RADCOM"

// radComSpec: Lei 9.612/98 + Decreto 2.615/98 — potência máxima de 25 W ERP,
// sistema irradiante de no máximo 30 m, e cobertura restrita a um raio de
// no máximo 1.000 m a partir da antena transmissora.
var radComSpec = ClassSpec{Class: ClassRadCom, MaxERPkW: 0.025, RefHeightM: 30, CoverageKm: 1.0}

// Spec devolve a especificação da classe para uma banda ("FM" ou "AM").
// A classe RADCOM é reconhecida em qualquer banda (é sempre FM na prática).
func Spec(band, class string) (ClassSpec, bool) {
	class = strings.ToUpper(strings.TrimSpace(class))
	if class == ClassRadCom {
		return radComSpec, true
	}
	switch strings.ToUpper(strings.TrimSpace(band)) {
	case "FM":
		s, ok := fmClasses[class]
		return s, ok
	case "AM", "OM":
		s, ok := omClasses[class]
		return s, ok
	}
	return ClassSpec{}, false
}

// TransbordoFactor estende o contorno protegido para incluir o TRANSBORDO — a
// área onde o sinal ainda é ouvido na prática, além do contorno em que ele é
// juridicamente protegido contra interferência. As duas coisas são diferentes:
// o contorno protegido é uma garantia regulatória de qualidade, não o limite
// físico da propagação.
//
// O fator 1,5 (+50%) vem do sistema E-radios, que já usa esse mesmo buffer
// sobre a mesma tabela da Res. 546/2010 para desenhar cobertura no mapa
// (signalads-frontend/src/pages/Map/index.js). Adotado aqui para os dois
// sistemas darem a mesma resposta sobre a mesma emissora.
//
// NÃO copiamos o fallback deles de classe desconhecida → A4 (24 km): aplicado
// a uma comunitária, isso superestimaria o alcance em 24×.
const TransbordoFactor = 1.5

// CoverageKm devolve o raio de cobertura da classe em km.
// ok=false quando a classe é desconhecida OU quando a classe não tem cobertura
// definida em distância (todo o AM). Chamador NUNCA deve assumir 0 = sem
// cobertura: 0 com ok=true não existe por construção.
func CoverageKm(band, class string) (float64, bool) {
	s, ok := Spec(band, class)
	if !ok || s.CoverageKm <= 0 {
		return 0, false
	}
	return s.CoverageKm, true
}

// ReachKm devolve o raio de ALCANCE da classe — o contorno protegido estendido
// pelo TransbordoFactor. É o raio usado para listar as cidades ao alcance da
// emissora; CoverageKm continua sendo o número normativo da Anatel.
//
// ok=false pelos mesmos motivos de CoverageKm (classe desconhecida ou sem
// cobertura definida em distância, isto é, todo o AM).
func ReachKm(band, class string) (float64, bool) {
	km, ok := CoverageKm(band, class)
	if !ok {
		return 0, false
	}
	return km * TransbordoFactor, true
}
