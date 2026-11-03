package mcp

import "strings"

// O texto do resumo vem, em parte, de dado que o modelo digitou (nome de campanha, observação…). Um
// caractere de controle de direção (bidi) no meio dele embaralha a ordem em que a pessoa LÊ o texto
// — "R$ 10 → 100" pode aparecer como "R$ 100 → 10" — e é exatamente o que ela confere antes de
// aprovar. Aqui eles saem do resumo no servidor, antes de ser gravado e antes de ser comparado; a
// tela ainda isola cada valor (unicode-bidi: isolate), como segunda camada.

// ehControleBidi: marcas de direção (LRM/RLM/ALM), embeddings e overrides (LRE..RLO, PDF) e isolates
// (LRI..PDI).
func ehControleBidi(r rune) bool {
	switch {
	case r == 0x200E, r == 0x200F, r == 0x061C:
		return true
	case r >= 0x202A && r <= 0x202E:
		return true
	case r >= 0x2066 && r <= 0x2069:
		return true
	}
	return false
}

// semBidi tira os controles de direção do texto.
func semBidi(s string) string {
	if !strings.ContainsFunc(s, ehControleBidi) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if ehControleBidi(r) {
			return -1
		}
		return r
	}, s)
}

func semBidiLista(ss []string) []string {
	if ss == nil {
		return nil
	}
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = semBidi(s)
	}
	return out
}

// semBidiValor limpa os textos de um valor de mudança (de/para): string, lista e objeto, em
// qualquer profundidade; número, booleano e nulo passam como estão.
func semBidiValor(v any) any {
	switch x := v.(type) {
	case string:
		return semBidi(x)
	case []string:
		return semBidiLista(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = semBidiValor(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[semBidi(k)] = semBidiValor(e)
		}
		return out
	}
	return v
}

// limparPrevia devolve o resumo sem controles de direção em nenhum texto. É aplicada logo depois de
// todo Preview (ao criar o pedido e na conferência da aprovação), de modo que o gravado e o
// recalculado seguem comparáveis.
func limparPrevia(p previa) previa {
	out := previa{
		Resumo:    semBidi(p.Resumo),
		Efeitos:   semBidiLista(p.Efeitos),
		Ressalvas: semBidiLista(p.Ressalvas),
	}
	if p.Mudancas != nil {
		out.Mudancas = make([]mudanca, len(p.Mudancas))
		for i, m := range p.Mudancas {
			out.Mudancas[i] = mudanca{Campo: semBidi(m.Campo), De: semBidiValor(m.De), Para: semBidiValor(m.Para)}
		}
	}
	return out
}
