# CSV Detalhado no formato do fornecedor — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fazer o "CSV Detalhado" da plataforma sair no layout do relatório do fornecedor externo (10 colunas + rodapé de totais), preservando as informações nossas que o layout não cobre.

**Architecture:** Toda a mudança de formato mora em `reportcsv.WriteDetailed()`, que já é o ponto único de formatação — os dois consumidores (`/detections/export` e o zip do pós-venda) herdam sem mudar de chamada. Duas colunas novas (`Identificador`, `Preço`) exigem dados que a query de export ainda não traz, então `IterateForExport` ganha `s.short_id` e dois JOINs de pricing. O rodapé é acumulado em memória durante o stream, em estruturas O(emissoras + materiais) — o export continua streamando linha a linha.

**Tech Stack:** Go 1.26, `encoding/csv`, pgx v5, `golang.org/x/text` (já é dep direta — `internal/geo/geo.go` usa), testify. Sem migration, sem dependência nova, sem CLI novo.

**Spec:** [`docs/superpowers/specs/2026-08-14-detailed-csv-vendor-format-design.md`](../specs/2026-08-14-detailed-csv-vendor-format-design.md)

---

## Contexto pra quem nunca viu este repo

- O backend Go vive em `workers/`. Rode tudo de lá (`cd workers`).
- `reportcsv` é um pacote deliberadamente burro: recebe um `io.Writer` e um
  iterador, e só formata. Ele existe porque o mesmo arquivo é servido por dois
  caminhos e duplicar a formatação faria os dois divergirem no primeiro ajuste.
- `catalog.DetectionEnriched` é uma veiculação já com os JOINs de emissora,
  material e cliente. `Detection` (embutida) tem os campos crus da tabela.
- Campos nullable no banco chegam como **ponteiro**. Nunca desreferencie sem
  checar `!= nil` — é a fonte de panic mais comum neste código.
- A view `detection_attributions` é a fonte da verdade de "quais veiculações
  contam"; não mexa no `WHERE` da query de export.
- **Não rode `go test ./...` inteiro pra validar seu trabalho.** Há um flaky
  conhecido (`internal/catalog TestBuildDailySummary_WithDowntime` falha antes
  das ~13:00 UTC). Rode os pacotes que você tocou.

## File Structure

| Arquivo | Responsabilidade |
|---|---|
| `workers/internal/reportcsv/format.go` (**criar**) | Helpers puros de formatação de célula e de nome de arquivo. Sem I/O, sem dependência de `catalog`. |
| `workers/internal/reportcsv/format_test.go` (**criar**) | Testes dos helpers. |
| `workers/internal/reportcsv/footer.go` (**criar**) | Acumulador e escrita do rodapé de totais. |
| `workers/internal/reportcsv/footer_test.go` (**criar**) | Testes do acumulador. |
| `workers/internal/reportcsv/reportcsv.go` (modificar) | `WriteDetailed` reescrita. `WriteConsolidated` **intocada**. |
| `workers/internal/reportcsv/reportcsv_test.go` (modificar) | Testes do CSV detalhado no formato novo. |
| `workers/internal/catalog/detections.go` (modificar) | 2 campos em `DetectionEnriched`, SELECT/JOIN/Scan em `IterateForExport`. |
| `workers/internal/catalog/campaigns.go` (modificar) | `ClientNameFor`. |
| `workers/internal/api/handlers/detections.go` (modificar) | Nome do arquivo baixado. |
| `frontend/src/components/CampaignReportsMenu.jsx` (modificar) | Texto do hint. |
| `docs/features/campaign-reports.md` (modificar) | Documentação do formato novo. |

O pacote é dividido em três arquivos por responsabilidade (formatação pura /
agregação do rodapé / escrita dos CSVs) em vez de inchar `reportcsv.go`. Os
dois arquivos novos são testáveis sem montar `DetectionEnriched`.

---

## Task 1: Helpers de formatação de célula

**Files:**
- Create: `workers/internal/reportcsv/format.go`
- Test: `workers/internal/reportcsv/format_test.go`

- [ ] **Step 1: Escreva os testes que falham**

Crie `workers/internal/reportcsv/format_test.go`:

```go
package reportcsv

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func ptrS(s string) *string    { return &s }
func ptrF(f float64) *float64  { return &f }

func TestFormatRadio(t *testing.T) {
	require.Equal(t, "Massa - FM (106.9)", formatRadio("Massa", ptrS("FM"), ptrF(106.9)))
	require.Equal(t, "Massa - FM", formatRadio("Massa", ptrS("FM"), nil))
	require.Equal(t, "Massa (106.9)", formatRadio("Massa", nil, ptrF(106.9)))
	require.Equal(t, "Massa", formatRadio("Massa", nil, nil))
	// Banda vazia é o mesmo que banda ausente — nunca pode sobrar " - " solto.
	require.Equal(t, "Massa", formatRadio("Massa", ptrS(""), nil))
	require.Equal(t, "Massa", formatRadio("  Massa  ", ptrS("   "), nil))
	// Frequência é rótulo de dial: ponto decimal, 1 casa, como no arquivo do
	// fornecedor — e não a vírgula que o resto do CSV usa pra número somável.
	require.Equal(t, "Studio - FM (99.0)", formatRadio("Studio", ptrS("FM"), ptrF(99)))
}

func TestFormatCityUF(t *testing.T) {
	require.Equal(t, "Joinville / SC", formatCityUF(ptrS("Joinville"), ptrS("SC")))
	require.Equal(t, "Joinville", formatCityUF(ptrS("Joinville"), nil))
	require.Equal(t, "SC", formatCityUF(nil, ptrS("SC")))
	require.Equal(t, "", formatCityUF(nil, nil))
	// Separador nunca sai solto quando um dos lados é string vazia.
	require.Equal(t, "Joinville", formatCityUF(ptrS("Joinville"), ptrS("")))
	require.Equal(t, "", formatCityUF(ptrS(""), ptrS("")))
}

func TestFormatBRL(t *testing.T) {
	require.Equal(t, "R$ 0,00", formatBRL(0))
	require.Equal(t, "R$ 6,00", formatBRL(6))
	require.Equal(t, "R$ 53,03", formatBRL(53.03))
	require.Equal(t, "R$ 1.234,50", formatBRL(1234.5))
	require.Equal(t, "R$ 1.234.567,89", formatBRL(1234567.89))
	require.Equal(t, "R$ 999,00", formatBRL(999))
}

func TestSanitizeFilename(t *testing.T) {
	require.Equal(t, "Rogga", SanitizeFilename("Rogga"))
	require.Equal(t, "Acai-Ltda", SanitizeFilename("Açaí Ltda"))
	require.Equal(t, "A-B", SanitizeFilename("A / B"))
	// O ponto sobrevive (está na allowlist — extensões precisam dele); as aspas
	// viram hífen, hífens repetidos colapsam e a ponta é aparada.
	require.Equal(t, "Cliente-S.A", SanitizeFilename(`Cliente "S.A"`))
	require.Equal(t, "", SanitizeFilename(""))
	require.Equal(t, "", SanitizeFilename("   "))
	// Truncado em 60 e sem hífen sobrando na ponta.
	require.LessOrEqual(t, len(SanitizeFilename(strings0(80))), 60)
}

// strings0 devolve uma string de n caracteres 'a' — só pro teste de truncagem.
func strings0(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
```

- [ ] **Step 2: Rode os testes pra confirmar que falham**

Run: `cd workers && go test ./internal/reportcsv/ -run 'TestFormat|TestSanitize' -v`
Expected: FAIL — `undefined: formatRadio`, `undefined: formatCityUF`, `undefined: formatBRL`, `undefined: SanitizeFilename`

- [ ] **Step 3: Implemente os helpers**

Crie `workers/internal/reportcsv/format.go`:

```go
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
```

- [ ] **Step 4: Rode os testes pra confirmar que passam**

Run: `cd workers && go test ./internal/reportcsv/ -run 'TestFormat|TestSanitize' -v`
Expected: PASS em todos.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/reportcsv/format.go workers/internal/reportcsv/format_test.go
git commit -m "feat(reportcsv): helpers de formatacao do layout do fornecedor"
```

---

## Task 2: Acumulador do rodapé de totais

**Files:**
- Create: `workers/internal/reportcsv/footer.go`
- Test: `workers/internal/reportcsv/footer_test.go`

- [ ] **Step 1: Escreva os testes que falham**

Crie `workers/internal/reportcsv/footer_test.go`:

```go
package reportcsv

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// renderFooter roda o acumulador através de um csv.Writer e devolve as linhas
// já divididas, pra o teste asseverar linha a linha.
func renderFooter(t *testing.T, tot *detailedTotals) []string {
	t.Helper()
	var buf bytes.Buffer
	cw := csv.NewWriter(&buf)
	cw.Comma = ';'
	require.NoError(t, tot.write(cw))
	cw.Flush()
	require.NoError(t, cw.Error())
	return strings.Split(strings.ReplaceAll(buf.String(), "\r\n", "\n"), "\n")
}

func TestDetailedTotals_ContaEmissorasDistintasNaoVeiculacoes(t *testing.T) {
	tot := newDetailedTotals()
	// 3 veiculações, 2 emissoras, ambas em SC.
	tot.add("id:1", "SC", "JINGLE A")
	tot.add("id:1", "SC", "JINGLE A")
	tot.add("id:2", "SC", "JINGLE B")

	lines := renderFooter(t, tot)
	require.Contains(t, lines, "TOTAL DE RADIOS MONITORADAS;2")
	require.Contains(t, lines, "TOTAL DE RÁDIOS POR ESTADO COM VEICULAÇÕES;2")
	require.Contains(t, lines, "TOTAL DE VEICULAÇÕES;3")
	// O bloco por UF conta EMISSORAS distintas, não veiculações.
	require.Contains(t, lines, "SC;2")
}

func TestDetailedTotals_ResumoPorComercialOrdenadoDescComDesempate(t *testing.T) {
	tot := newDetailedTotals()
	tot.add("id:1", "SC", "POPULAR")
	tot.add("id:1", "SC", "POPULAR")
	tot.add("id:1", "SC", "POPULAR")
	tot.add("id:1", "SC", "ZEBRA")
	tot.add("id:1", "SC", "ALFA")

	lines := renderFooter(t, tot)
	start := indexOf(lines, "Comercial;Total")
	require.GreaterOrEqual(t, start, 0, "faltou o cabeçalho do resumo por comercial")
	// Maior total primeiro; empate desempata por título asc (ALFA antes de ZEBRA).
	require.Equal(t, "POPULAR;3", lines[start+1])
	require.Equal(t, "ALFA;1", lines[start+2])
	require.Equal(t, "ZEBRA;1", lines[start+3])
}

func TestDetailedTotals_UFsOrdenadasEVaziaPorUltimo(t *testing.T) {
	tot := newDetailedTotals()
	tot.add("id:1", "SC", "A")
	tot.add("id:2", "RS", "A")
	tot.add("id:3", "", "A") // emissora sem estado cadastrado

	lines := renderFooter(t, tot)
	start := indexOf(lines, "UF;TOTAL")
	require.GreaterOrEqual(t, start, 0)
	require.Equal(t, "RS;1", lines[start+1])
	require.Equal(t, "SC;1", lines[start+2])
	require.Equal(t, ";1", lines[start+3], "UF vazia entra no resumo, por último")
	// A emissora sem UF não pode sumir do total geral.
	require.Contains(t, lines, "TOTAL DE RADIOS MONITORADAS;3")
}

func TestDetailedTotals_Vazio(t *testing.T) {
	lines := renderFooter(t, newDetailedTotals())
	require.Contains(t, lines, "TOTAL DE VEICULAÇÕES;0")
	require.Contains(t, lines, "TOTAL DE RADIOS MONITORADAS;0")
	require.Contains(t, lines, "UF;TOTAL")
	require.Contains(t, lines, "Comercial;Total")
}

func indexOf(lines []string, want string) int {
	for i, l := range lines {
		if l == want {
			return i
		}
	}
	return -1
}
```

- [ ] **Step 2: Rode os testes pra confirmar que falham**

Run: `cd workers && go test ./internal/reportcsv/ -run TestDetailedTotals -v`
Expected: FAIL — `undefined: detailedTotals`, `undefined: newDetailedTotals`

- [ ] **Step 3: Implemente o acumulador**

Crie `workers/internal/reportcsv/footer.go`:

```go
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
```

- [ ] **Step 4: Rode os testes pra confirmar que passam**

Run: `cd workers && go test ./internal/reportcsv/ -run TestDetailedTotals -v`
Expected: PASS nos 4 testes.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/reportcsv/footer.go workers/internal/reportcsv/footer_test.go
git commit -m "feat(reportcsv): acumulador do rodape de totais do CSV detalhado"
```

---

## Task 3: `Identificador` e `Preço` na query de export

**Files:**
- Modify: `workers/internal/catalog/detections.go` (struct `DetectionEnriched` ~linha 854; `IterateForExport` ~linha 1157)

Esta task não tem teste unitário próprio: `IterateForExport` é SQL puro contra
o banco, e o pacote não tem harness de DB. A verificação é o build + o teste de
integração da Task 8 (opcional). O que protege aqui é a ordem do `Scan` bater
com a ordem do `SELECT` — confira contando os campos.

- [ ] **Step 1: Acrescente os dois campos na struct**

Em `workers/internal/catalog/detections.go`, na struct `DetectionEnriched`,
depois de `ClientName`:

```go
	// StationShortID é o identificador curto e estável da emissora
	// (stations.short_id). Vira a coluna "Identificador" do CSV detalhado, que
	// espelha o layout do relatório do fornecedor.
	StationShortID *int32 `json:"station_short_id,omitempty"`
	// UnitPrice é o valor unitário contratado pra (campanha, emissora, tipo de
	// material). Só existe quando o pricing da emissora está em modo
	// `per_insertion` — em `consolidated` não há valor por inserção, e o campo
	// fica nil.
	UnitPrice *float64 `json:"unit_price,omitempty"`
```

- [ ] **Step 2: Acrescente as colunas no SELECT de `IterateForExport`**

Na query de `IterateForExport`, a última linha do SELECT hoje é
`cmp.client_id, cli.name`. Troque por:

```go
		       cmp.client_id, cli.name,
		       s.short_id,
		       CASE WHEN csp.mode = 'per_insertion' THEN cstp.unit_value END
```

- [ ] **Step 3: Acrescente os dois JOINs**

Logo depois do `LEFT JOIN client_station_pmm cst ...` (o último JOIN da query),
antes do `WHERE`:

```go
		LEFT JOIN campaign_station_pricing csp
		       ON csp.campaign_id = d.campaign_id AND csp.station_id = d.station_id
		LEFT JOIN campaign_station_type_pricing cstp
		       ON cstp.campaign_id = d.campaign_id
		      AND cstp.station_id  = d.station_id
		      AND cstp.type_id     = m.type_id
```

Ambos são por chave primária composta e `campaign_station_type_pricing` já tem
`idx_campaign_station_type_pricing_lookup`. `d.campaign_id` é nullable
(veiculação órfã): nesse caso nenhum dos dois casa e `UnitPrice` fica nil, que é
o certo.

O `CASE WHEN csp.mode = 'per_insertion'` é redundante hoje (a app só popula
`campaign_station_type_pricing` nesse modo), mas não existe FK cruzando as
duas tabelas — o comentário da própria `0022_pricing.up.sql` diz que "fica a
cargo da app validar". O guard impede que uma linha órfã de pricing vire
cobrança no relatório.

**Não toque no `WHERE`, no `ORDER BY`, nem no filtro de `q`.**

- [ ] **Step 4: Acrescente os dois campos no `Scan`**

No `rows.Scan(...)` de `IterateForExport`, a última linha hoje é
`&det.ClientID, &det.ClientName); err != nil {`. Troque por:

```go
			&det.ClientID, &det.ClientName,
			&det.StationShortID, &det.UnitPrice); err != nil {
```

A ordem do `Scan` tem que bater exatamente com a do `SELECT`. Confira: os dois
campos novos são os **últimos** nos dois lugares.

- [ ] **Step 5: Verifique que compila (nativo e cross-compile linux)**

Run: `cd workers && go build ./... && CGO_ENABLED=0 GOOS=linux go build ./...`
Expected: sem saída, exit 0. O cross-compile é o que o `workers.Dockerfile` faz
(regra 6.1 do CLAUDE.md) — build nativo passar não garante que o do deploy passa.

- [ ] **Step 6: Rode os testes do pacote**

Run: `cd workers && go test ./internal/catalog/`
Expected: `ok radiocheck/internal/catalog`

- [ ] **Step 7: Commit**

```bash
git add workers/internal/catalog/detections.go
git commit -m "feat(catalog): export traz short_id da emissora e preco unitario"
```

---

## Task 4: `ClientNameFor` pro nome do arquivo

**Files:**
- Modify: `workers/internal/catalog/campaigns.go` (depois de `Get`, ~linha 235)

- [ ] **Step 1: Implemente o método**

Em `workers/internal/catalog/campaigns.go`, logo depois de `func (c *Campaigns) Get(...)`:

```go
// ClientNameFor devolve o nome do cliente dono da campanha. Existe pro nome do
// arquivo de export: `Get` carrega a campanha inteira e ainda assim só traz
// `client_id`, não o nome do cliente.
func (c *Campaigns) ClientNameFor(ctx context.Context, campaignID uuid.UUID) (string, error) {
	var name string
	err := c.pool.QueryRow(ctx, `
		SELECT cli.name
		FROM campaigns cmp
		JOIN clients cli ON cli.id = cmp.client_id
		WHERE cmp.id = $1`, campaignID).Scan(&name)
	if err != nil {
		return "", err
	}
	return name, nil
}
```

`context` e `uuid` já estão importados no arquivo — `Get` usa os dois.

- [ ] **Step 2: Verifique que compila**

Run: `cd workers && go build ./internal/catalog/`
Expected: sem saída, exit 0.

- [ ] **Step 3: Commit**

```bash
git add workers/internal/catalog/campaigns.go
git commit -m "feat(catalog): ClientNameFor resolve o cliente dono da campanha"
```

---

## Task 5: `WriteDetailed` no formato do fornecedor

**Files:**
- Modify: `workers/internal/reportcsv/reportcsv.go:116-174` (`WriteDetailed`)
- Modify: `workers/internal/reportcsv/reportcsv_test.go:58-94` (`TestWriteDetailed_TraduzCategoria`)

- [ ] **Step 1: Substitua o teste antigo do detalhado pelos testes do formato novo**

Em `workers/internal/reportcsv/reportcsv_test.go`, **apague**
`TestWriteDetailed_TraduzCategoria` inteiro (linhas 58–94) e acrescente no fim
do arquivo:

```go
// writeDetailed roda WriteDetailed sobre um slice e devolve as linhas.
func writeDetailed(t *testing.T, det []catalog.DetectionEnriched) (string, []string) {
	t.Helper()
	var buf bytes.Buffer
	err := WriteDetailed(&buf, func(cb func(catalog.DetectionEnriched) error) error {
		for _, d := range det {
			if err := cb(d); err != nil {
				return err
			}
		}
		return nil
	})
	require.NoError(t, err)
	out := buf.String()
	return out, strings.Split(strings.ReplaceAll(strings.TrimPrefix(out, bomStr), "\r\n", "\n"), "\n")
}

// detFixture monta uma veiculação completa. Os parâmetros são só o que cada
// teste varia; o resto fica fixo.
func detFixture(shortID int32, station, uf, material, category string, price *float64) catalog.DetectionEnriched {
	band, city, typeName := "FM", "Joinville", "Spot 30s"
	freq, pmm, dur := 106.9, 812.0, 30.0
	target := 400
	client := "Rogga"
	return catalog.DetectionEnriched{
		Detection: catalog.Detection{
			StationName:    station,
			CommercialName: material,
			Category:       category,
			// 21:56:20 em São Paulo (UTC-3) = 00:56:20 UTC do dia seguinte.
			DetectedAt: time.Date(2026, 6, 1, 0, 56, 20, 0, time.UTC),
		},
		StationShortID: &shortID, StationBand: &band, StationFrequencyMHz: &freq,
		StationCity: &city, StationState: &uf,
		StationPMM: &pmm, StationPMMTarget: &target,
		MaterialTypeName: &typeName, MaterialDurationSec: &dur,
		ClientName: &client, UnitPrice: price,
	}
}

func TestWriteDetailed_CabecalhoELinha(t *testing.T) {
	price := 6.0
	out, lines := writeDetailed(t, []catalog.DetectionEnriched{
		detFixture(8407, "Massa", "SC", "JINGLE ROGGA VERÃO 30", "in_slot", &price),
	})

	require.True(t, strings.HasPrefix(out, bomStr), "faltou o BOM que o Excel pt-BR precisa")
	require.Equal(t,
		"Identificador;Data;Hora;Rádio;Cidade / UF;Peça;Comercial;Status;PMM;Preço;Cliente;PMM no target;Duração (s)",
		lines[0])
	// Sem linha em branco entre cabeçalho e dados — o fornecedor tem, a gente não.
	require.Equal(t,
		"8407;31/05/2026;21:56:20;Massa - FM (106.9);Joinville / SC;Spot 30s;JINGLE ROGGA VERÃO 30;Dentro da faixa;812;R$ 6,00;Rogga;400;30",
		lines[1])
}

func TestWriteDetailed_PrecoSoNaLinhaDentroDaFaixa(t *testing.T) {
	price := 53.03
	_, lines := writeDetailed(t, []catalog.DetectionEnriched{
		detFixture(1, "A", "SC", "M", "in_slot", &price),
		detFixture(1, "A", "SC", "M", "out_slot", &price),
		detFixture(1, "A", "SC", "M", "out_date", &price),
		detFixture(1, "A", "SC", "M", "orphan", &price),
		detFixture(1, "A", "SC", "M", "in_slot", nil),
	})

	require.Contains(t, lines[1], "Dentro da faixa;812;R$ 53,03")
	// Fora da faixa / fora da data / bônus não faturam: R$ 0,00 mesmo com
	// unit_value cadastrado.
	require.Contains(t, lines[2], "Fora da faixa;812;R$ 0,00")
	require.Contains(t, lines[3], "Fora da data;812;R$ 0,00")
	require.Contains(t, lines[4], "Bônus;812;R$ 0,00")
	// in_slot sem pricing cadastrado também é R$ 0,00.
	require.Contains(t, lines[5], "Dentro da faixa;812;R$ 0,00")
	require.NotContains(t, strings.Join(lines, "\n"), "orphan")
}

func TestWriteDetailed_CamposNulosNaoDeixamSeparadorSolto(t *testing.T) {
	d := catalog.DetectionEnriched{
		Detection: catalog.Detection{
			StationName:    "Radio Sem Nada",
			CommercialName: "Material X",
			Category:       "in_slot",
			DetectedAt:     time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
		},
	}
	_, lines := writeDetailed(t, []catalog.DetectionEnriched{d})

	require.Equal(t,
		";01/06/2026;09:00:00;Radio Sem Nada;;;Material X;Dentro da faixa;;R$ 0,00;;;",
		lines[1])
}

func TestWriteDetailed_RodapeDeTotais(t *testing.T) {
	_, lines := writeDetailed(t, []catalog.DetectionEnriched{
		detFixture(8407, "Massa", "SC", "JINGLE A", "in_slot", nil),
		detFixture(8407, "Massa", "SC", "JINGLE A", "in_slot", nil),
		detFixture(4229, "Itapoá", "SC", "JINGLE B", "in_slot", nil),
		detFixture(9001, "Alfa", "RS", "JINGLE A", "in_slot", nil),
	})

	joined := strings.Join(lines, "\n")
	require.Contains(t, joined, "TOTAL DE RADIOS MONITORADAS;3")
	require.Contains(t, joined, "TOTAL DE VEICULAÇÕES;4")
	require.Contains(t, joined, "RESUMO DE RÁDIOS POR ESTADO COM VEICULAÇÕES")
	require.Contains(t, joined, "RS;1")
	require.Contains(t, joined, "SC;2")
	require.Contains(t, joined, "JINGLE A;3")
	require.Contains(t, joined, "JINGLE B;1")
}

func TestWriteDetailed_SemVeiculacoes(t *testing.T) {
	out, lines := writeDetailed(t, nil)
	require.True(t, strings.HasPrefix(out, bomStr))
	require.Contains(t, lines[0], "Identificador;Data;Hora")
	require.Contains(t, strings.Join(lines, "\n"), "TOTAL DE VEICULAÇÕES;0")
}
```

O horário do fixture é `00:56:20 UTC` de 01/06 justamente pra provar a
conversão de fuso: em `America/Sao_Paulo` isso é **31/05 às 21:56:20**, que é a
primeira linha do arquivo do fornecedor.

- [ ] **Step 2: Rode os testes pra confirmar que falham**

Run: `cd workers && go test ./internal/reportcsv/ -run TestWriteDetailed -v`
Expected: FAIL — as linhas ainda saem no formato antigo (`Data;Hora;Emissora;...`)
e `catalog.DetectionEnriched` não tem `UnitPrice` se a Task 3 não foi feita.

**A Task 3 precisa estar pronta antes desta.**

- [ ] **Step 3: Reescreva `WriteDetailed`**

Em `workers/internal/reportcsv/reportcsv.go`, substitua a função `WriteDetailed`
inteira (o bloco de comentário + a função, linhas 111–174) por:

```go
// WriteDetailed escreve o CSV detalhado: uma linha por veiculação, no layout
// do relatório do fornecedor externo (Mídia Geral) que os clientes já
// conhecem, seguido do rodapé de totais dele.
//
// As colunas 1–10 são o layout do fornecedor, nesta ordem exata. As 11–13 são
// nossas, e vêm depois pra não perder informação que o layout dele não cobre —
// `Cliente` importa porque este export aceita campaign_id opcional e pode
// misturar campanhas de clientes diferentes.
//
// O iterador é injetado para que o chamador escolha a fonte — streaming HTTP
// (Detections.IterateForExport direto no ResponseWriter) ou buffer em memória
// (bundle do pós-venda) — sem que o formato mude entre os dois.
func WriteDetailed(out io.Writer, iterate func(cb func(catalog.DetectionEnriched) error) error) error {
	if _, err := out.Write(bom); err != nil {
		return err
	}
	cw := csv.NewWriter(out)
	cw.Comma = ';'
	if err := cw.Write([]string{
		"Identificador", "Data", "Hora", "Rádio", "Cidade / UF",
		"Peça", "Comercial", "Status", "PMM", "Preço",
		"Cliente", "PMM no target", "Duração (s)",
	}); err != nil {
		return err
	}

	loc := saoPaulo()
	totals := newDetailedTotals()
	if err := iterate(func(d catalog.DetectionEnriched) error {
		t := d.DetectedAt.In(loc)

		id := ""
		if d.StationShortID != nil {
			id = strconv.FormatInt(int64(*d.StationShortID), 10)
		}
		pmm := ""
		if d.StationPMM != nil {
			pmm = strings.ReplaceAll(fmt.Sprintf("%.0f", *d.StationPMM), ".", ",")
		}
		pmmTarget := ""
		if d.StationPMMTarget != nil {
			pmmTarget = strconv.Itoa(*d.StationPMMTarget)
		}
		dur := ""
		if d.MaterialDurationSec != nil {
			dur = strings.ReplaceAll(fmt.Sprintf("%.0f", *d.MaterialDurationSec), ".", ",")
		}
		uf := ""
		if d.StationState != nil {
			uf = strings.TrimSpace(*d.StationState)
		}

		totals.add(stationKey(d), uf, d.CommercialName)

		return cw.Write([]string{
			id,
			t.Format("02/01/2006"),
			t.Format("15:04:05"),
			formatRadio(d.StationName, d.StationBand, d.StationFrequencyMHz),
			formatCityUF(d.StationCity, d.StationState),
			strOrEmpty(d.MaterialTypeName),
			d.CommercialName,
			CategoryLabelPT(d.Category),
			pmm,
			priceCell(d),
			strOrEmpty(d.ClientName),
			pmmTarget,
			dur,
		})
	}); err != nil {
		return err
	}

	if err := totals.write(cw); err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}

// priceCell devolve o preço unitário da veiculação. Só linha `in_slot` com
// unit_value cadastrado tem valor: a regra de cobrança da 0022_pricing é
// "unit_value × in_slot", então preencher fora-da-faixa / fora-da-data / bônus
// faria a soma da coluna passar do que é efetivamente faturado.
func priceCell(d catalog.DetectionEnriched) string {
	if d.Category == "in_slot" && d.UnitPrice != nil {
		return formatBRL(*d.UnitPrice)
	}
	return formatBRL(0)
}

// stationKey identifica a emissora nas contagens do rodapé. Usa short_id, que é
// estável; o fallback pelo nome só existe porque o campo chega como ponteiro —
// colapsar todas as emissoras sem id num bucket só falsearia o total.
func stationKey(d catalog.DetectionEnriched) string {
	if d.StationShortID != nil {
		return "id:" + strconv.FormatInt(int64(*d.StationShortID), 10)
	}
	return "name:" + d.StationName
}
```

- [ ] **Step 4: Acrescente `strconv` aos imports**

No bloco de import de `workers/internal/reportcsv/reportcsv.go`, acrescente
`"strconv"` entre `"io"` e `"strings"`.

- [ ] **Step 5: Rode os testes do pacote inteiro**

Run: `cd workers && go test ./internal/reportcsv/ -v`
Expected: PASS em tudo, incluindo os dois testes de `WriteConsolidated`, que
não podem ter mudado de comportamento.

Se `TestWriteDetailed_CabecalhoELinha` ou
`TestWriteDetailed_CamposNulosNaoDeixamSeparadorSolto` reprovarem por uma
diferença de string, **leia o diff com atenção antes de mudar o teste**: a
expectativa foi escrita a partir do arquivo real do fornecedor.

- [ ] **Step 6: Verifique o cross-compile**

Run: `cd workers && CGO_ENABLED=0 GOOS=linux go build ./...`
Expected: sem saída, exit 0.

- [ ] **Step 7: Commit**

```bash
git add workers/internal/reportcsv/reportcsv.go workers/internal/reportcsv/reportcsv_test.go
git commit -m "feat(reportcsv): CSV detalhado no layout do fornecedor + rodape"
```

---

## Task 6: Nome do arquivo baixado

**Files:**
- Modify: `workers/internal/reportcsv/reportcsv.go:22-28` (expor `SaoPaulo`)
- Modify: `workers/internal/api/handlers/detections.go:619` (montagem do filename)

- [ ] **Step 1: Exponha o helper de fuso**

Em `workers/internal/reportcsv/reportcsv.go`, substitua a função `saoPaulo`:

```go
// SaoPaulo é o fuso de todos os relatórios. Exportada porque o handler HTTP
// formata as datas do nome do arquivo no mesmo fuso do conteúdo — dois fusos
// diferentes no mesmo download dariam um arquivo "01-05 a 31-05" com linhas de
// 30/04 dentro.
func SaoPaulo() *time.Location {
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		return time.FixedZone("BRT", -3*3600)
	}
	return loc
}

func saoPaulo() *time.Location { return SaoPaulo() }
```

- [ ] **Step 2: Monte o nome do arquivo no handler**

Em `workers/internal/api/handlers/detections.go`, substitua a linha

```go
	filename := fmt.Sprintf("veiculacoes_%s.csv", time.Now().Format("20060102_150405"))
```

por:

```go
	// Nome no espírito do relatório do fornecedor — "Rogga-Veiculacoes-
	// 01-05-2026-31-05-2026.csv" — mas sem os códigos internos dele. Cai pro
	// nome genérico quando o export não tem campanha (admin exportando várias
	// de uma vez) ou quando o cliente não resolve.
	filename := fmt.Sprintf("veiculacoes_%s.csv", time.Now().Format("20060102_150405"))
	if f.CampaignID != nil {
		if client, err := h.CampaignRepo.ClientNameFor(r.Context(), *f.CampaignID); err == nil {
			if slug := reportcsv.SanitizeFilename(client); slug != "" {
				if f.StartDate != nil && f.EndDate != nil {
					loc := reportcsv.SaoPaulo()
					filename = fmt.Sprintf("%s-Veiculacoes-%s-%s.csv", slug,
						f.StartDate.In(loc).Format("02-01-2006"),
						f.EndDate.In(loc).Format("02-01-2006"))
				} else {
					filename = fmt.Sprintf("%s-Veiculacoes-%s.csv", slug,
						time.Now().Format("20060102_150405"))
				}
			}
		}
	}
```

O erro de `ClientNameFor` é engolido de propósito: um nome de arquivo genérico
é melhor que um 500 num export que, no resto, funcionaria. O `reportcsv` já
está importado no arquivo (`WriteDetailed` é chamado logo abaixo).

- [ ] **Step 3: Verifique que compila**

Run: `cd workers && go build ./... && CGO_ENABLED=0 GOOS=linux go build ./...`
Expected: sem saída, exit 0.

- [ ] **Step 4: Rode os testes dos pacotes tocados**

Run: `cd workers && go test ./internal/reportcsv/ ./internal/api/handlers/`
Expected: `ok` nos dois.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/reportcsv/reportcsv.go workers/internal/api/handlers/detections.go
git commit -m "feat(api): export detalhado nomeia o arquivo por cliente e periodo"
```

---

## Task 7: Texto do menu no frontend

**Files:**
- Modify: `frontend/src/components/CampaignReportsMenu.jsx:429`

- [ ] **Step 1: Ajuste o hint**

Troque:

```jsx
          hint={gridReport ? 'Uma linha por veiculação (sem filtro de busca)' : 'Uma linha por veiculação'}
```

por:

```jsx
          hint={gridReport ? 'Uma linha por veiculação + resumo (sem filtro de busca)' : 'Uma linha por veiculação + resumo'}
```

O usuário precisa saber que o arquivo tem rodapé antes de jogar numa tabela
dinâmica.

- [ ] **Step 2: Confirme que nada mais precisa mudar**

Run: `grep -n "Uma linha por veiculação" frontend/src/components/CampaignReportsMenu.jsx`
Expected: só a linha alterada. O componente apenas dispara o download; o
formato mora inteiro no backend.

**Não rode `npm install`** nesta task (regra 5 do CLAUDE.md — poda o lockfile no
Windows e quebra o build do Cloudflare Pages). Nenhuma dependência muda aqui.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/components/CampaignReportsMenu.jsx
git commit -m "feat(frontend): hint do CSV detalhado menciona o resumo"
```

---

## Task 8 (opcional): Teste de integração da query

**Files:**
- Modify: o arquivo de teste de integração de `workers/internal/catalog/`

Só faça esta task se o test DB estiver de pé. Ela prova que os dois campos
novos realmente chegam preenchidos — o que nenhum teste unitário cobre.

Setup (ver memória `test-db-native-pg-shadows-docker`): PostgreSQL descartável
`rc-test-pg` na porta **15432** (o PG nativo do Windows ocupa a 5432 e rejeita
a senha), banco **`radiocheck_test`** vazio (o `radiocheck` do container tem
dado e o guard destrutivo barra), e **`-p 1`** — concorrência no mesmo DB gera
deadlock que parece regressão.

- [ ] **Step 1: Escreva o teste**

Crie `workers/internal/catalog/export_pricing_test.go`. Ele reusa
`seedAirtimeFixture` (definida em `detections_test.go`, mesmo pacote), que já
cria cliente + campanha + material + emissora e registra o cleanup:

```go
package catalog

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestIterateForExport_ShortIDEPrecoUnitario prova que as duas colunas novas do
// CSV detalhado ("Identificador" e "Preço") chegam preenchidas — e que o preço
// só existe quando o pricing da emissora está em modo per_insertion.
func TestIterateForExport_ShortIDEPrecoUnitario(t *testing.T) {
	ctx, pool, campID, matID, statID := seedAirtimeFixture(t, "ExportPricing")

	// O material precisa de um tipo: o preço unitário é por TIPO de material.
	typeID := seedType(t, ctx, pool, "Spot 30s ExportPricing")
	_, err := pool.Exec(ctx, `UPDATE materials SET type_id = $2 WHERE id = $1`, matID, typeID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO campaign_station_pricing (campaign_id, station_id, mode)
		VALUES ($1, $2, 'per_insertion')`, campID, statID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO campaign_station_type_pricing (campaign_id, station_id, type_id, unit_value)
		VALUES ($1, $2, $3, 6.00)`, campID, statID, typeID)
	require.NoError(t, err)

	detectedAt := time.Now().Add(-1 * time.Hour)
	dets := NewDetections(pool)
	_, err = dets.Create(ctx, CreateDetectionInput{
		StationID: statID, CommercialID: matID, CampaignID: campID,
		DetectedAt: detectedAt, Confidence: 0.9, HashCount: 50, TemporalCoverage: 0.8,
	})
	require.NoError(t, err)

	collect := func() []DetectionEnriched {
		var got []DetectionEnriched
		require.NoError(t, dets.IterateForExport(ctx,
			ListPagedFilter{CampaignID: &campID},
			func(d DetectionEnriched) error { got = append(got, d); return nil }))
		return got
	}

	got := collect()
	require.Len(t, got, 1)
	require.NotNil(t, got[0].StationShortID, "Identificador não pode vir vazio")
	require.Greater(t, *got[0].StationShortID, int32(0))
	require.NotNil(t, got[0].UnitPrice, "per_insertion tem valor unitário")
	require.InDelta(t, 6.00, *got[0].UnitPrice, 0.001)

	// Em modo consolidated não existe valor por inserção: o CASE da query tem
	// que devolver NULL mesmo com a linha de type_pricing ainda na tabela.
	_, err = pool.Exec(ctx, `
		UPDATE campaign_station_pricing SET mode = 'consolidated', consolidated_value = 100
		WHERE campaign_id = $1 AND station_id = $2`, campID, statID)
	require.NoError(t, err)

	got = collect()
	require.Len(t, got, 1)
	require.Nil(t, got[0].UnitPrice, "consolidated não tem preço por inserção")
}
```

`seedType` já existe em `distribution_rules_test.go` (mesmo pacote). O cleanup
de `campaign_station_pricing` e `campaign_station_type_pricing` vem de graça:
as duas têm `ON DELETE CASCADE` na campanha, que `seedAirtimeFixture` já apaga.

- [ ] **Step 2: Rode**

```bash
cd workers
TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:15432/radiocheck_test?sslmode=disable" \
  go test ./internal/catalog/ -run TestIterateForExport_ShortIDEPrecoUnitario -p 1 -v
```

Expected: PASS. Sem `TEST_DATABASE_URL` o teste faz `t.Skip` — o que também é
um resultado aceitável se o container não estiver de pé, mas então esta task
não provou nada e você deve dizer isso ao reportar.

- [ ] **Step 3: Commit**

```bash
git add workers/internal/catalog/
git commit -m "test(catalog): export popula short_id e preco unitario"
```

---

## Task 9: Documentação

**Files:**
- Modify: `docs/features/campaign-reports.md`

- [ ] **Step 1: Leia o doc atual**

Run: `cat docs/features/campaign-reports.md`

Localize a seção que descreve as colunas do CSV Detalhado.

- [ ] **Step 2: Reescreva a seção do CSV Detalhado**

Documente:
- as 13 colunas na ordem, dizendo quais 10 são o layout do fornecedor
- que `Preço` só é preenchido em linha `Dentro da faixa` com pricing
  `per_insertion`, e por quê (soma da coluna = faturado)
- o rodapé de três blocos e a definição de "rádios monitoradas" (emissoras
  **com veiculação** no período, não emissoras no target)
- a convenção do nome do arquivo e os fallbacks
- que o `relatorio-detalhado.csv` do zip do pós-venda usa o mesmo formato, e
  que pós-vendas já publicados **não** são regerados

- [ ] **Step 3: Atualize o header YAML**

```yaml
---
status: implementado
ultima-verificacao: 2026-08-14
codigo-relacionado:
  - workers/internal/reportcsv/reportcsv.go
  - workers/internal/reportcsv/format.go
  - workers/internal/reportcsv/footer.go
  - workers/internal/catalog/detections.go
  - workers/internal/api/handlers/detections.go
---
```

- [ ] **Step 4: Commit**

```bash
git add docs/features/campaign-reports.md
git commit -m "docs(reports): CSV detalhado no formato do fornecedor"
```

---

## Verificação final antes de abrir PR

- [ ] `cd workers && CGO_ENABLED=0 GOOS=linux go build ./...` — exit 0 (regra 6.1)
- [ ] `cd workers && go test ./internal/reportcsv/ ./internal/catalog/ ./internal/api/handlers/` — `ok` nos três
- [ ] `git diff master --stat` não mostra `migrations/`, `go.mod`, `go.sum` nem `frontend/package*.json` — se mostrar, alguma task saiu do escopo
- [ ] Baixe um CSV Detalhado de uma campanha real em dev, abra no Excel e
      confira: cabeçalho na linha 1, dados na linha 2, rodapé no fim, acentos
      corretos (BOM funcionando), nome do arquivo com cliente e período
