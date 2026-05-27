package geo

import (
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"São Paulo":     "sao paulo",
		"SÃO PAULO":     "sao paulo",
		"  São  Paulo ": "sao paulo",
		"Mogi-Mirim":    "mogi-mirim",
		"Brasília":      "brasilia",
	}
	for in, want := range cases {
		if got := normalize(in); got != want {
			t.Errorf("normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLookup_KnownCity(t *testing.T) {
	g, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	lat, lng, ok := g.Lookup("São Paulo", "SP")
	if !ok {
		t.Fatal("São Paulo/SP not found")
	}
	if lat < -24 || lat > -23 || lng < -47 || lng > -46 {
		t.Errorf("São Paulo coords out of expected box: lat=%v lng=%v", lat, lng)
	}
}

func TestLookup_AccentAndCaseInsensitive(t *testing.T) {
	g, _ := New()
	want1, want2, _ := g.Lookup("São Paulo", "SP")
	for _, variant := range []string{"sao paulo", "SÃO PAULO", " São  Paulo "} {
		lat, lng, ok := g.Lookup(variant, "sp")
		if !ok || lat != want1 || lng != want2 {
			t.Errorf("Lookup(%q,\"sp\") = (%v,%v,%v), want (%v,%v,true)", variant, lat, lng, ok, want1, want2)
		}
	}
}

func TestLookup_HomonymResolvedByUF(t *testing.T) {
	g, _ := New()
	latMS, lngMS, okMS := g.Lookup("Bonito", "MS")
	latPE, lngPE, okPE := g.Lookup("Bonito", "PE")
	if !okMS || !okPE {
		t.Fatalf("Bonito missing: MS=%v PE=%v", okMS, okPE)
	}
	if latMS == latPE && lngMS == lngPE {
		t.Error("Bonito/MS and Bonito/PE resolved to the same coords")
	}
}

func TestLookup_NotFound(t *testing.T) {
	g, _ := New()
	if _, _, ok := g.Lookup("Cidade Que Nao Existe XYZ", "SP"); ok {
		t.Error("unexpected hit for nonexistent city")
	}
	if _, _, ok := g.Lookup("", "SP"); ok {
		t.Error("empty city should not match")
	}
	if _, _, ok := g.Lookup("São Paulo", ""); ok {
		t.Error("empty state should not match")
	}
}

func TestDataset_Sanity(t *testing.T) {
	g, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if len(g.byKey) < 5000 || len(g.byKey) > 6000 {
		t.Errorf("unexpected dataset size: %d", len(g.byKey))
	}
	ufs := map[string]bool{}
	for k, c := range g.byKey {
		// k = "<nome>|<UF>"
		uf := k[len(k)-2:]
		ufs[uf] = true
		// Eastern bound is -28 (not the mainland's -34.8) to admit oceanic-island
		// municipalities like Fernando de Noronha/PE at lng -32.4.
		if c.Lat < -34 || c.Lat > 6 || c.Lng < -74 || c.Lng > -28 {
			t.Errorf("coord out of Brazil bbox for key %q: %+v", k, c)
		}
	}
	if len(ufs) != 27 {
		t.Errorf("expected 27 UFs, got %d", len(ufs))
	}
}
