package reportcsv

import (
	"encoding/csv"
	"sort"
	"strconv"
)

// detailedTotals acumula, DURANTE o stream do CSV detalhado, o que o rodapé de
// resumo precisa. Guarda chaves distintas, não linhas: a memória é
// O(emissoras + materiais) — dezenas de entradas — e não O(veiculações), então
// o export continua streamando um arquivo de qualquer tamanho.
type detailedTotals struct {
	total    int
	stations map[string]struct{}
	byUF     map[string]map[string]struct{}
	byComerc map[string]int
}

func newDetailedTotals() *detailedTotals {
	return &detailedTotals{
		stations: map[string]struct{}{},
		byUF:     map[string]map[string]struct{}{},
		byComerc: map[string]int{},
	}
}

// add registra uma veiculação. `uf` pode ser "" (emissora sem estado
// cadastrado): ela continua contando no total geral e ganha um grupo de chave
// vazia no resumo por estado, senão o total geral e a soma do bloco por UF
// divergiriam.
func (t *detailedTotals) add(stationKey, uf, comercial string) {
	t.total++
	t.stations[stationKey] = struct{}{}
	if t.byUF[uf] == nil {
		t.byUF[uf] = map[string]struct{}{}
	}
	t.byUF[uf][stationKey] = struct{}{}
	t.byComerc[comercial]++
}

// write emite o rodapé no formato do relatório do fornecedor: duas linhas em
// branco, o bloco de totais, o resumo por estado e o resumo por comercial.
//
// "TOTAL DE RADIOS MONITORADAS" sai sem acento em RADIOS de propósito — é
// assim no arquivo original.
func (t *detailedTotals) write(cw *csv.Writer) error {
	rows := [][]string{
		{""}, {""},
		{"TOTAL DE RADIOS MONITORADAS", strconv.Itoa(len(t.stations))},
		{"TOTAL DE RÁDIOS POR ESTADO COM VEICULAÇÕES", strconv.Itoa(len(t.stations))},
		{"TOTAL DE VEICULAÇÕES", strconv.Itoa(t.total)},
		{""},
		{"RESUMO DE RÁDIOS POR ESTADO COM VEICULAÇÕES"},
		{"UF", "TOTAL"},
	}

	ufs := make([]string, 0, len(t.byUF))
	for uf := range t.byUF {
		ufs = append(ufs, uf)
	}
	sort.Slice(ufs, func(i, j int) bool {
		// UF vazia (emissora sem estado) vai por último.
		if (ufs[i] == "") != (ufs[j] == "") {
			return ufs[j] == ""
		}
		return ufs[i] < ufs[j]
	})
	for _, uf := range ufs {
		rows = append(rows, []string{uf, strconv.Itoa(len(t.byUF[uf]))})
	}

	rows = append(rows,
		[]string{""}, []string{""},
		[]string{"RESUMO DE VEICULAÇÕES POR COMERCIAL"},
		[]string{"Comercial", "Total"},
	)

	type comercTotal struct {
		title string
		n     int
	}
	list := make([]comercTotal, 0, len(t.byComerc))
	for title, n := range t.byComerc {
		list = append(list, comercTotal{title, n})
	}
	// Total desc; empate desempata por título asc pro arquivo ser
	// determinístico — sem isso o golden test fica flaky.
	sort.Slice(list, func(i, j int) bool {
		if list[i].n != list[j].n {
			return list[i].n > list[j].n
		}
		return list[i].title < list[j].title
	})
	for _, c := range list {
		rows = append(rows, []string{c.title, strconv.Itoa(c.n)})
	}

	for _, r := range rows {
		if err := cw.Write(r); err != nil {
			return err
		}
	}
	return nil
}
