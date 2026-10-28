package inputstyle

import (
	"encoding/json"
	"testing"
)

func TestCatalogAndCopyAreIndependent(t *testing.T) {
	for _, goos := range []string{"linux", "windows", "darwin"} {
		for _, p := range PresetsForPlatform(goos) {
			if err := p.Rules.Validate(); err != nil {
				t.Fatalf("%s: %v", p.ID, err)
			}
			data, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			var copied Preset
			if err = json.Unmarshal(data, &copied); err != nil || copied != p {
				t.Fatalf("incomplete copy: %s %v", data, err)
			}
			copied.Rules.Tab = "cycle"
			again, _ := GetForPlatform(p.ID, goos)
			if again != p {
				t.Fatal("editing a copy mutated the catalog")
			}
		}
	}
	if _, ok := Get("asus"); ok {
		t.Fatal("excluded vendor")
	}
}

func TestInvalidRulesAreRejected(t *testing.T) {
	p, _ := Get("0c")
	for _, r := range []Rules{{Version: 1}, {Version: 2}, func() Rules { r := p.Rules; r.Tab = "unknown"; return r }()} {
		if r.Validate() == nil {
			t.Fatalf("invalid accepted: %+v", r)
		}
	}
	if err := (Rules{}).Validate(); err != nil {
		t.Fatal(err)
	}
}

// #210：Shift＋字母可改打小寫；預設仍是 inline（大寫）。
func TestShiftLetterLowerIsValid(t *testing.T) {
	p, _ := Get("0c")
	if p.Rules.ShiftLetter != "inline" {
		t.Fatalf("default changed: %q", p.Rules.ShiftLetter)
	}
	p.Rules.ShiftLetter = "lower"
	if err := p.Rules.Validate(); err != nil {
		t.Fatal(err)
	}
	p.Rules.ShiftLetter = "upper"
	if p.Rules.Validate() == nil {
		t.Fatal("unknown shift_letter accepted")
	}
}
