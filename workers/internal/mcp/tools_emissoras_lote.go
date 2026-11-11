package mcp

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

	"radiocheck/internal/catalog"
)

// buscar_emissoras_lote: o agente que programa campanha casa as emissoras do
// PI mãe por dial + UF. Uma buscar_emissoras por emissora eram ~12 idas e
// voltas por campanha — e cada ida a mais é uma chance do conector do Gemini
// cair no meio (log da VM, 2026-11-11: todas as respostas 200 em <20 ms e o
// agente relatando "sessão caiu"). Esta resolve o lote numa consulta.

const (
	tetoLinhasLote     = 100
	tetoCandidatosLote = 5
)

// Resultados possíveis de cada linha do lote.
const (
	loteUnica           = "unica"
	loteVarias          = "varias"
	loteCidadeDiferente = "cidade_diferente"
	loteNenhuma         = "nenhuma"
)

type linhaEmissoraLote struct {
	Ref    string `json:"ref,omitempty" jsonschema:"Rótulo livre que volta igual na resposta, para saber qual linha do PI é qual (ex.: 'Vale FM | 99,9 | Juazeiro do Norte/CE')."`
	Dial   string `json:"dial" jsonschema:"Dial como está no PI: '99,9' ou '99.9'."`
	UF     string `json:"uf" jsonschema:"Sigla do estado (ex.: CE)."`
	Banda  string `json:"banda,omitempty" jsonschema:"FM ou AM (opcional)."`
	Cidade string `json:"cidade,omitempty" jsonschema:"Cidade como está no PI (opcional, recomendado). Compara sem acento e sem maiúscula."`
	Nome   string `json:"nome,omitempty" jsonschema:"Nome da emissora como está no PI (opcional). Só serve de pista: vem em nome_confere e ordena os candidatos."`
}

type entradaBuscarEmissorasLote struct {
	Emissoras []linhaEmissoraLote `json:"emissoras" jsonschema:"As emissoras a resolver, de 1 a 100."`
}

type candidatoLote struct {
	ID            string   `json:"id"`
	Nome          string   `json:"nome"`
	Banda         string   `json:"banda"`
	FrequenciaMHz *float64 `json:"frequencia_mhz"`
	Cidade        *string  `json:"cidade"`
	UF            *string  `json:"uf"`
	// null quando a linha não trouxe cidade/nome.
	CidadeConfere *bool `json:"cidade_confere"`
	NomeConfere   *bool `json:"nome_confere"`
}

type resultadoLote struct {
	Indice          int             `json:"indice"`
	Ref             string          `json:"ref"`
	Dial            string          `json:"dial"`
	UF              string          `json:"uf"`
	Resultado       string          `json:"resultado"`
	TotalCandidatos int             `json:"total_candidatos"`
	Candidatos      []candidatoLote `json:"candidatos"`
}

type saidaBuscarEmissorasLote struct {
	Itens     []resultadoLote `json:"itens"`
	Resumo    map[string]int  `json:"resumo"`
	Ressalvas []string        `json:"ressalvas"`
}

const ressalvaLote = "unica = dial + UF (+ cidade, se informada) casaram com uma emissora só: confira o nome. " +
	"varias e cidade_diferente pedem escolha: use cidade e nome; na dúvida, pergunte ao usuário. " +
	"nenhuma: informe o que foi buscado e nunca troque por uma emissora parecida."

func registrarEmissorasLote(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "buscar_emissoras_lote",
		"Resolve VÁRIAS emissoras numa chamada, por dial + UF (o jeito de casar as linhas de um PI). Para cada linha devolve "+
			"resultado = unica | varias | cidade_diferente | nenhuma e até 5 candidatos (id, nome, banda, dial, cidade/UF, "+
			"cidade_confere, nome_confere). Dial aceita vírgula ou ponto; cidade compara sem acento. Use no lugar de uma "+
			"buscar_emissoras por emissora. Nunca troque uma emissora não encontrada por outra parecida.",
		func(ctx context.Context, L *Leitura, in entradaBuscarEmissorasLote) (*sdk.CallToolResult, error) {
			if len(in.Emissoras) == 0 || len(in.Emissoras) > tetoLinhasLote {
				return recusa(fmt.Sprintf("informe de 1 a %d emissoras", tetoLinhasLote), "entrada_invalida")
			}
			linhas := make([]linhaNormalizada, len(in.Emissoras))
			var ufs, dials []string
			for i, l := range in.Emissoras {
				n, msg := normalizarLinhaLote(l)
				if msg != "" {
					return recusa(fmt.Sprintf("emissoras[%d]: %s", i, msg), "entrada_invalida")
				}
				linhas[i] = n
				if !slices.Contains(ufs, n.uf) {
					ufs = append(ufs, n.uf)
				}
				if !slices.Contains(dials, n.dial) {
					dials = append(dials, n.dial)
				}
			}
			estacoes, err := L.Emissoras.CandidatosPorDial(ctx, ufs, dials)
			if err != nil {
				return nil, err
			}
			return ok(resolverLote(in.Emissoras, linhas, estacoes))
		})
}

type linhaNormalizada struct {
	dial, uf, banda string
	cidade          string // dobrada; "" = não informada
	nomeTokens      []string
	nomeInformado   bool
	dialNoNome      *regexp.Regexp
}

func normalizarLinhaLote(l linhaEmissoraLote) (linhaNormalizada, string) {
	dial := catalog.NormalizeDial(l.Dial)
	if dial == "" {
		return linhaNormalizada{}, fmt.Sprintf("dial %q não é um número (ex.: 99,9)", l.Dial)
	}
	uf := strings.ToUpper(strings.TrimSpace(l.UF))
	if len(uf) != 2 {
		return linhaNormalizada{}, fmt.Sprintf("uf %q: use a sigla de 2 letras", l.UF)
	}
	banda := strings.ToUpper(strings.TrimSpace(l.Banda))
	if banda != "" && banda != "FM" && banda != "AM" {
		return linhaNormalizada{}, fmt.Sprintf("banda %q: use FM ou AM", l.Banda)
	}
	// O dial no nome ("89 - FM (89.1)"), com ponto ou vírgula, sem colar em outro número.
	padrao := strings.ReplaceAll(regexp.QuoteMeta(dial), `\.`, `[.,]`)
	return linhaNormalizada{
		dial: dial, uf: uf, banda: banda,
		cidade:        dobrar(l.Cidade),
		nomeTokens:    tokensDeNome(l.Nome),
		nomeInformado: strings.TrimSpace(l.Nome) != "",
		dialNoNome:    regexp.MustCompile(`(^|[^0-9.,])` + padrao + `($|[^0-9])`),
	}, ""
}

func resolverLote(entrada []linhaEmissoraLote, linhas []linhaNormalizada, estacoes []catalog.Station) saidaBuscarEmissorasLote {
	out := saidaBuscarEmissorasLote{
		Itens:     make([]resultadoLote, len(linhas)),
		Resumo:    map[string]int{loteUnica: 0, loteVarias: 0, loteCidadeDiferente: 0, loteNenhuma: 0},
		Ressalvas: []string{ressalvaLote},
	}
	dono := map[string]int{} // id da emissora única → primeira linha que a resolveu
	for i, l := range linhas {
		var achadas []candidatoLote
		for _, st := range estacoes {
			if !casaDialUF(l, st) {
				continue
			}
			achadas = append(achadas, candidatoDe(l, st))
		}
		r := resultadoLote{Indice: i, Ref: entrada[i].Ref, Dial: l.dial, UF: l.uf}
		if l.cidade != "" {
			var naCidade []candidatoLote
			for _, c := range achadas {
				if *c.CidadeConfere {
					naCidade = append(naCidade, c)
				}
			}
			if len(naCidade) == 0 && len(achadas) > 0 {
				r.Resultado = loteCidadeDiferente
			} else {
				achadas = naCidade
			}
		}
		// Nome que confere primeiro; a ordem do banco (nome, id) desempata.
		slices.SortStableFunc(achadas, func(a, b candidatoLote) int {
			return pesoNome(b) - pesoNome(a)
		})
		if r.Resultado == "" {
			switch len(achadas) {
			case 0:
				r.Resultado = loteNenhuma
			case 1:
				r.Resultado = loteUnica
			default:
				r.Resultado = loteVarias
			}
		}
		r.TotalCandidatos = len(achadas)
		r.Candidatos = achadas[:min(len(achadas), tetoCandidatosLote)]
		if r.Candidatos == nil {
			r.Candidatos = []candidatoLote{}
		}
		out.Resumo[r.Resultado]++
		if r.Resultado == loteUnica {
			id := r.Candidatos[0].ID
			if j, visto := dono[id]; visto {
				out.Ressalvas = append(out.Ressalvas, fmt.Sprintf(
					"emissoras[%d] e emissoras[%d] caíram na mesma emissora (%s): confira se o PI repete a emissora ou se uma linha está errada",
					j, i, r.Candidatos[0].Nome))
			} else {
				dono[id] = i
			}
		}
		out.Itens[i] = r
	}
	return out
}

func casaDialUF(l linhaNormalizada, st catalog.Station) bool {
	if st.State == nil || *st.State != l.uf {
		return false
	}
	if l.banda != "" && st.Band != l.banda {
		return false
	}
	if st.FrequencyMHz != nil {
		return dialDe(*st.FrequencyMHz) == l.dial
	}
	return l.dialNoNome.MatchString(st.Name)
}

// dialDe espelha o dialTextExpr do catalog (NUMERIC(6,2) sem zeros à direita).
func dialDe(f float64) string {
	return catalog.NormalizeDial(strconv.FormatFloat(f, 'f', 2, 64))
}

func candidatoDe(l linhaNormalizada, st catalog.Station) candidatoLote {
	c := candidatoLote{
		ID: st.ID.String(), Nome: st.Name, Banda: st.Band, FrequenciaMHz: st.FrequencyMHz,
		Cidade: st.City, UF: st.State,
	}
	if l.cidade != "" {
		v := st.City != nil && dobrar(*st.City) == l.cidade
		c.CidadeConfere = &v
	}
	if l.nomeInformado {
		v := false
		if len(l.nomeTokens) > 0 {
			nome := dobrar(st.Name)
			for _, t := range l.nomeTokens {
				if strings.Contains(nome, t) {
					v = true
					break
				}
			}
		}
		c.NomeConfere = &v
	}
	return c
}

func pesoNome(c candidatoLote) int {
	if c.NomeConfere != nil && *c.NomeConfere {
		return 1
	}
	return 0
}

// palavrasVazias não distinguem emissora nenhuma ("Rádio Clube FM" × "Clube").
var palavrasVazias = map[string]bool{"fm": true, "am": true, "radio": true, "rd": true, "mhz": true, "khz": true, "web": true}

// tokensDeNome: as palavras do nome no PI que servem de pista — sem acento,
// sem números (o dial já foi casado) e sem palavras vazias.
func tokensDeNome(nome string) []string {
	var out []string
	for _, t := range strings.FieldsFunc(dobrar(nome), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len(t) < 2 || palavrasVazias[t] || strings.IndexFunc(t, unicode.IsLetter) < 0 {
			continue
		}
		out = append(out, t)
	}
	return out
}

// dobrar: minúsculas, sem acento, espaços colapsados ("São  Paulo" = "sao paulo").
func dobrar(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, err := transform.String(t, s)
	if err != nil {
		out = s
	}
	return strings.Join(strings.Fields(strings.ToLower(out)), " ")
}
