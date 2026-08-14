package reportcsv

import (
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// formatRadio monta a coluna "Rádio" do relatório do fornecedor:
// "Massa - FM (106.9)". Banda e frequência são nullable no schema, então as
// três variações menores existem — e nenhuma pode deixar um separador solto
// (" - " ou "()" vazio) na célula.
//
// A frequência sai com PONTO decimal, ao contrário do resto do CSV: aqui ela é
// rótulo de dial ("106.9 FM"), não número que o Excel vá somar.
func formatRadio(name string, band *string, freqMHz *float64) string {
	head := strings.TrimSpace(name)
	if band != nil {
		if b := strings.TrimSpace(*band); b != "" {
			if head == "" {
				head = b
			} else {
				head += " - " + b
			}
		}
	}
	if freqMHz != nil {
		f := "(" + strconv.FormatFloat(*freqMHz, 'f', 1, 64) + ")"
		if head == "" {
			return f
		}
		return head + " " + f
	}
	return head
}

// formatCityUF monta "Joinville / SC". Ambos os lados são nullable; o
// separador só aparece quando os dois existem.
func formatCityUF(city, state *string) string {
	c, s := "", ""
	if city != nil {
		c = strings.TrimSpace(*city)
	}
	if state != nil {
		s = strings.TrimSpace(*state)
	}
	switch {
	case c != "" && s != "":
		return c + " / " + s
	case c != "":
		return c
	default:
		return s
	}
}

// formatBRL escreve moeda no padrão pt-BR: "R$ 1.234,50". Usa espaço comum, e
// não o NBSP do arquivo original do fornecedor — visualmente idêntico e sem o
// risco de um byte invisível confundir quem processa o CSV.
func formatBRL(v float64) string {
	s := strconv.FormatFloat(v, 'f', 2, 64)
	intPart, decPart, _ := strings.Cut(s, ".")
	neg := strings.HasPrefix(intPart, "-")
	if neg {
		intPart = intPart[1:]
	}
	var b strings.Builder
	for i := 0; i < len(intPart); i++ {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteByte(intPart[i])
	}
	sign := ""
	if neg {
		sign = "-"
	}
	return "R$ " + sign + b.String() + "," + decPart
}

// SanitizeFilename transforma o nome do cliente em algo seguro pro header
// Content-Disposition: sem acento, sem espaço, sem caractere que navegador ou
// sistema de arquivos rejeite. Content-Disposition com byte não-ASCII quebra em
// parte dos navegadores, e sanitizar é mais simples que `filename*=UTF-8''`.
//
// É exportada porque quem monta o header é o handler HTTP, não este pacote.
func SanitizeFilename(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, err := transform.String(t, s)
	if err != nil {
		out = s
	}
	var b strings.Builder
	for _, r := range out {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	res := b.String()
	for strings.Contains(res, "--") {
		res = strings.ReplaceAll(res, "--", "-")
	}
	res = strings.Trim(res, "-._")
	if len(res) > 60 {
		res = strings.Trim(res[:60], "-._")
	}
	return res
}
