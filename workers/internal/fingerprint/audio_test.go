package fingerprint

import (
	"strings"
	"testing"
)

func TestParseVariant(t *testing.T) {
	cases := map[string]VariantID{
		"clean":  VariantClean,
		"light":  VariantLight,
		"medium": VariantMedium,
		"heavy":  VariantHeavy,
	}
	for in, want := range cases {
		got, err := ParseVariant(in)
		if err != nil {
			t.Fatalf("ParseVariant(%q) returned error: %v", in, err)
		}
		if got != want {
			t.Errorf("ParseVariant(%q) = %d, want %d", in, got, want)
		}
		if got.String() != in {
			t.Errorf("VariantID(%d).String() = %q, want %q", got, got.String(), in)
		}
	}

	if _, err := ParseVariant("nonsense"); err == nil {
		t.Error("ParseVariant(nonsense) should return an error")
	}
}

// TestFilterChainNotImplemented documents that today only `clean` has a
// filter chain. The other variants intentionally return ErrVariantNotImplemented
// so the CLI / batch driver can surface the gap explicitly instead of
// silently producing wrong fingerprints.
func TestFilterChainNotImplemented(t *testing.T) {
	if _, err := filterChain(VariantClean); err != nil {
		t.Errorf("filterChain(clean) should be implemented, got %v", err)
	}

	for _, v := range []VariantID{VariantLight, VariantMedium, VariantHeavy} {
		if _, err := filterChain(v); err == nil {
			t.Errorf("filterChain(%s) should return ErrVariantNotImplemented", v)
		} else if !strings.Contains(err.Error(), "not implemented") {
			t.Errorf("filterChain(%s) error %q should mention 'not implemented'", v, err)
		}
	}
}
