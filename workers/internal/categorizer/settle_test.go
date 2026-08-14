package categorizer

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// day6 é 10/06/2026 (quarta-feira) à meia-noite SP — o dia de todas as células
// destes testes.
func day6() time.Time { return time.Date(2026, 6, 10, 0, 0, 0, 0, saoPaulo) }

// at devolve um instante do dia da célula. Derivado de day6() de propósito: se
// as duas datas pudessem divergir, toda tocada viraria out_date e os testes
// continuariam "passando" pelo motivo errado.
func at(h, m int) time.Time {
	d := day6()
	return time.Date(d.Year(), d.Month(), d.Day(), h, m, 0, 0, saoPaulo)
}

func plays(ts ...time.Time) []Play {
	out := make([]Play, len(ts))
	for i, t := range ts {
		out[i] = Play{DetectedAt: t, MaterialID: uuid.Nil}
	}
	return out
}

// Tabela-verdade da spec 2026-08-14 §2. Faixa 10:00–12:00 em todos os casos.
func TestSettle_TruthTable(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	rules := []Rule{mkRule(1, 30, 127, "10:00", "12:00", 2)} // N=2, todo dia

	cases := []struct {
		name  string
		plays []Play
		want  []string
	}{
		{
			name:  "1 dentro 1 fora: a de fora segura o saldo",
			plays: plays(at(10, 30), at(3, 0)),
			want:  []string{CatInSlot, CatOutSlot},
		},
		{
			name:  "2 dentro 1 fora: meta fechada dentro da faixa, a de fora e bonus",
			plays: plays(at(10, 30), at(11, 0), at(3, 0)),
			want:  []string{CatInSlot, CatInSlot, CatBonus},
		},
		{
			name:  "0 dentro 3 fora: nenhuma bonificacao, tudo out_slot",
			plays: plays(at(3, 0), at(4, 0), at(5, 0)),
			want:  []string{CatOutSlot, CatOutSlot, CatOutSlot},
		},
		{
			name:  "4 dentro: as 2 excedentes viram bonus",
			plays: plays(at(10, 10), at(10, 20), at(10, 30), at(10, 40)),
			want:  []string{CatInSlot, CatInSlot, CatBonus, CatBonus},
		},
		{
			name:  "ordem cronologica manda: a de fora chega primeiro mas a meta fecha depois",
			plays: plays(at(3, 0), at(10, 30), at(11, 0)),
			want:  []string{CatBonus, CatInSlot, CatInSlot},
		},
		{
			// Cobre o sort.SliceStable: as tocadas dentro da faixa chegam FORA de
			// ordem no slice. A cota tem que seguir a cronologia (10:10 e 10:20
			// ficam com as 2 vagas), não a posição no slice — senão a 10:40, por
			// ser a primeira do slice, roubaria uma vaga. Sem o sort este caso dá
			// {in_slot, in_slot, bonus}. Importa porque a Task 5 compara com o
			// ROW_NUMBER() OVER (ORDER BY detected_at, id) do SQL.
			name:  "slice fora de ordem: a cota segue a cronologia, nao a posicao no slice",
			plays: plays(at(10, 40), at(10, 10), at(10, 20)),
			want:  []string{CatBonus, CatInSlot, CatInSlot},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Settle(day6(), tc.plays, cmp, rules, nil)
			if len(got) != len(tc.want) {
				t.Fatalf("len = %d, want %d", len(got), len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("play %d (%s): got %q, want %q",
						i, tc.plays[i].DetectedAt.Format("15:04"), got[i], tc.want[i])
				}
			}
		})
	}
}

// Empates no MESMO instante preservam a ordem do slice (pré-condição 2 da
// Settle: é o caller que desempata por id, a Settle só não bagunça). Cobre o
// sort.SliceStable — com sort.Slice (pdqsort, instável) as vagas caem em outros
// índices.
//
// 30 elementos de propósito: abaixo de 13 o sort.Slice cai no insertion sort e
// fica estável por acidente, então um teste com 2 ou 3 empates não pegaria a
// troca.
func TestSettle_TiesPreserveSliceOrder(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	rules := []Rule{mkRule(1, 30, 127, "10:00", "12:00", 3)} // N=3

	// 30 tocadas em 3 instantes (10:10/10:11/10:12), 10 empates em cada.
	ps := make([]Play, 30)
	for i := range ps {
		ps[i] = Play{DetectedAt: at(10, 10+i%3), MaterialID: uuid.Nil}
	}

	got := Settle(day6(), ps, cmp, rules, nil)

	// As 3 vagas são das tocadas de 10:10 (o instante mais cedo) e, no empate,
	// das 3 PRIMEIRAS posições do slice entre elas → índices 0, 3 e 6.
	var inSlotAt []int
	for i, c := range got {
		if c == CatInSlot {
			inSlotAt = append(inSlotAt, i)
		}
	}
	want := []int{0, 3, 6}
	if len(inSlotAt) != len(want) {
		t.Fatalf("in_slot em %v; esperava exatamente %v (N=3)", inSlotAt, want)
	}
	for k := range want {
		if inSlotAt[k] != want[k] {
			t.Fatalf("in_slot em %v, want %v — empate no mesmo instante tem que "+
				"seguir a ordem do slice (sort estável)", inSlotAt, want)
		}
	}
	for i, c := range got {
		if c != CatInSlot && c != CatBonus {
			t.Errorf("play %d: got %q, want in_slot ou bonus (todas dentro da faixa)", i, c)
		}
	}
}

// Célula vazia — a Task 3 chama a Settle por célula e célula sem tocada acontece.
func TestSettle_NoPlays_ReturnsEmptyNotNil(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	rules := []Rule{mkRule(1, 30, 127, "10:00", "12:00", 2)}

	for _, tc := range []struct {
		name string
		in   []Play
	}{{"nil", nil}, {"slice vazio", []Play{}}} {
		t.Run(tc.name, func(t *testing.T) {
			got := Settle(day6(), tc.in, cmp, rules, nil)
			if got == nil {
				t.Fatal("got nil, want slice vazio não-nil")
			}
			if len(got) != 0 {
				t.Fatalf("len = %d, want 0", len(got))
			}
		})
	}
}

// Linha "3 (2 faixas) | 3 dentro da 1ª faixa" da tabela-verdade — a única que os
// casos acima não cobrem. Prova as duas metades da D1: N é a SOMA do plays_per_day
// das regras que valem hoje (2+1=3), e não existe cota por faixa — as 3 tocadas
// concentradas na 1ª faixa são todas in_slot, sem sobrar bônus nem déficit.
func TestSettle_TwoWindows_QuotaIsDaily_NotPerWindow(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	rules := []Rule{
		mkRule(1, 30, 127, "10:00", "12:00", 2),
		mkRule(1, 30, 127, "18:00", "20:00", 1),
	}

	got := Settle(day6(), plays(at(10, 10), at(10, 30), at(11, 0)), cmp, rules, nil)
	for i, c := range got {
		if c != CatInSlot {
			t.Errorf("play %d: got %q, want in_slot (meta do dia = 2+1 = 3)", i, c)
		}
	}
}

// Célula zerada — o caso da campanha 270. N=0 → toda tocada é bônus,
// independente da faixa (que o override deixa gravada mas inerte).
func TestSettle_ZeroedOverride_AllBonus(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	rules := []Rule{mkRule(1, 30, 127, "05:00", "23:59", 2)}
	ov := mkOverride(0, "05:00", "23:59")

	got := Settle(day6(), plays(at(10, 36), at(14, 1), at(15, 17)), cmp, rules, ov)
	for i, c := range got {
		if c != CatBonus {
			t.Errorf("play %d: got %q, want bonus (meta zerada = sem plano no dia)", i, c)
		}
	}
}

// Override com meta > 0: a faixa do override é a única que vale (supersede rules),
// e a meta também vem dele (N=2, não os 5 da regra).
//
// N=2 de propósito: com N=1 a única tocada dentro da faixa já fecharia a meta e a
// de fora viraria bonus pelo passo 4 (linha 2 da tabela-verdade), escondendo o que
// este teste quer provar. Com a meta aberta, a de fora fica out_slot — e se a
// implementação usasse a faixa da REGRA (08:00–10:00) em vez da do override, os
// papéis se inverteriam (09:00 → in_slot, 14:30 → out_slot) e as duas asserções
// falhariam.
func TestSettle_Override_WindowSupersedesRules(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	rules := []Rule{mkRule(1, 30, 127, "08:00", "10:00", 5)} // ignorada
	ov := mkOverride(2, "14:00", "16:00")

	got := Settle(day6(), plays(at(14, 30), at(9, 0)), cmp, rules, ov)
	if got[0] != CatInSlot {
		t.Errorf("14:30 na faixa do override: got %q, want in_slot", got[0])
	}
	if got[1] != CatOutSlot {
		t.Errorf("09:00 (faixa da regra, ignorada): got %q, want out_slot", got[1])
	}
}

// D6: out_date precede tudo e não consome cota. Tocada fora do período da
// campanha continua out_date mesmo com meta sobrando.
func TestSettle_OutDate_DoesNotConsumeQuota(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 9, 0, 0, 0, 0, saoPaulo), // termina 09/06
	}
	rules := []Rule{mkRule(1, 30, 127, "10:00", "12:00", 2)}

	got := Settle(day6(), plays(at(10, 30), at(11, 0)), cmp, rules, nil)
	for i, c := range got {
		if c != CatOutDate {
			t.Errorf("play %d fora do período da campanha: got %q, want out_date", i, c)
		}
	}
}

// D6 + carve-out: material nomeado numa regra específica, tocando fora do
// período DELA, continua out_date mesmo com a célula zerada por override —
// era o caminho que o override mascarava (bug original).
func TestSettle_CarveOutOfPeriod_StaysOutDate_EvenWithZeroedOverride(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	m := uuid.New()
	// Regra específica de m cobre só 01–07/06. O dia é 10/06 → fora do período dele.
	rules := []Rule{mkMatRule(1, 7, 127, "10:00", "12:00", 2, m)}
	ov := mkOverride(0, "00:00", "23:59")

	got := Settle(day6(), []Play{{DetectedAt: at(10, 30), MaterialID: m}}, cmp, rules, ov)
	if got[0] != CatOutDate {
		t.Errorf("carve-out fora do período: got %q, want out_date", got[0])
	}
}
