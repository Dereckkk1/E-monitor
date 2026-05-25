package catalog

import "testing"

func TestIsBonified_NoExtras(t *testing.T) {
	if IsBonified(5, 0) {
		t.Errorf("zero extras should never be bonified")
	}
}

func TestIsBonified_ExtrasCoverDeficit(t *testing.T) {
	if !IsBonified(3, 3) {
		t.Errorf("extras == deficit should bonify")
	}
	if !IsBonified(2, 5) {
		t.Errorf("extras > deficit should bonify")
	}
}

func TestIsBonified_ExtrasInsufficient(t *testing.T) {
	if IsBonified(5, 3) {
		t.Errorf("extras < deficit should NOT bonify")
	}
}

func TestIsBonified_ZeroDeficit(t *testing.T) {
	// No deficit, irrelevant; explicit choice: not bonified because nothing
	// to compensate. UI only shows bonificada when something failed.
	if IsBonified(0, 5) {
		t.Errorf("zero deficit should not register as bonified")
	}
}
