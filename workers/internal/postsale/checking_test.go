package postsale

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		name                                   string
		programmed, identified, deficit, extras int
		want                                   RowKind
	}{
		{"entregou exatamente o contratado", 10, 10, 0, 0, KindConforming},
		{"entregou a mais", 10, 10, 0, 3, KindAbove},
		{"ficou devendo", 10, 7, 3, 0, KindCompensation},
		{"deve mas compensou de sobra", 10, 7, 3, 5, KindCompensation},
		{"deve e compensou parcialmente", 10, 7, 3, 1, KindCompensation},
		{"sem plano mas tocou", 0, 0, 0, 4, KindAbove},
		{"sem plano e sem tocada", 0, 0, 0, 0, KindConforming},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Classify(c.deficit, c.extras)
			if got != c.want {
				t.Fatalf("Classify(deficit=%d, extras=%d) = %q, quer %q",
					c.deficit, c.extras, got, c.want)
			}
		})
	}
}

func TestDeliveryPct(t *testing.T) {
	cases := []struct {
		programmed, identified int
		want                   *int
	}{
		{10, 7, ptrInt(70)},
		{10, 10, ptrInt(100)},
		{10, 12, ptrInt(120)},
		{0, 0, nil},         // nada programado e nada tocado → indeterminado
		{0, 4, ptrInt(100)}, // sem plano mas tocou → 100%, não divide por zero
		{3, 1, ptrInt(33)},  // arredonda
	}
	for _, c := range cases {
		got := DeliveryPct(c.programmed, c.identified)
		switch {
		case c.want == nil && got != nil:
			t.Fatalf("DeliveryPct(%d,%d) = %d, quer nil", c.programmed, c.identified, *got)
		case c.want != nil && got == nil:
			t.Fatalf("DeliveryPct(%d,%d) = nil, quer %d", c.programmed, c.identified, *c.want)
		case c.want != nil && *got != *c.want:
			t.Fatalf("DeliveryPct(%d,%d) = %d, quer %d", c.programmed, c.identified, *got, *c.want)
		}
	}
}

func ptrInt(v int) *int { return &v }
