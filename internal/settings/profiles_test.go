package settings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfilesMigrationAndCompleteRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	original := `{"schema":1,"toggle_key":"left","mixed_input":true,"punctuation":true,"keyboard_layout":"standard","mode_memory":"shared","future_field":{"x":1}}`
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	s := r.Settings
	if s.ActiveProfile().Builtin || !s.MixedInput || !s.Punctuation || s.ToggleKey != "left" {
		t.Fatal("legacy settings changed")
	}
	before, _ := os.ReadFile(path)
	if string(before) != original {
		t.Fatal("read modified settings")
	}
	for _, id := range []string{"microsoft", "chewing", "mcbopomofo", "macos", "0c"} {
		if err = s.ProfileAction("select", id, ""); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if !s.ActiveProfile().Builtin || !s.Punctuation || s.ModeMemory != "shared" || s.KeyboardLayout != "standard" {
			t.Fatalf("profile replaced unrelated settings: %s", id)
		}
		if err = s.Set("toggle-key", "right"); err == nil {
			t.Fatal("preset can be modified")
		}
		if err = Save(path, s); err != nil {
			t.Fatal(err)
		}
		back, err := Load(path)
		if err != nil || back != s {
			t.Fatalf("%s round trip differs: %v", id, err)
		}
	}
	if err = s.ProfileAction("select", "legacy", ""); err != nil {
		t.Fatal(err)
	}
	if !s.MixedInput || s.ToggleKey != "left" || !s.Punctuation {
		t.Fatal("old profile was not frozen")
	}
	if err = Save(path, s); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `"future_field"`) {
		t.Fatal("lost unknown field")
	}
}
func TestProfileCopiesRulesAndKeepsCustomChanges(t *testing.T) {
	s := freshSettings()
	if len(s.Profiles().Items) != 5 {
		t.Fatal("fresh install should have five built-ins only")
	}
	if err := s.ProfileAction("copy", "microsoft", " 我的習慣 "); err != nil {
		t.Fatal(err)
	}
	id := s.ActiveProfile().ID
	if s.ActiveProfile().Name != "我的習慣" || s.ProfileRules().ShiftSpace != "fullwidth" {
		t.Fatal("copy did not retain all rules")
	}
	if err := s.Set("style-shift-space", "space"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("esc-clear", "off"); err != nil {
		t.Fatal(err)
	}
	fingerprint := s.ProfileFingerprint()
	if err := s.ProfileAction("select", "mcbopomofo", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.ProfileAction("select", id, ""); err != nil {
		t.Fatal(err)
	}
	if s.ProfileRules().ShiftSpace != "space" || s.EscClear || s.ProfileFingerprint() != fingerprint {
		t.Fatal("custom values not restored")
	}
	if err := s.ProfileAction("rename", id, "工作用"); err != nil {
		t.Fatal(err)
	}
	if s.ProfileFingerprint() != fingerprint {
		t.Fatal("rename unnecessarily changes input rules")
	}
	before := s
	if err := s.ProfileAction("delete", id, ""); err == nil || s != before {
		t.Fatal("active delete must be atomic and rejected")
	}
	if err := s.ProfileAction("rename", id, "bad\nname"); err == nil || s != before {
		t.Fatal("invalid name must not alter snapshot")
	}
	if err := s.ProfileAction("select", "0c", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.ProfileAction("delete", id, ""); err != nil {
		t.Fatal(err)
	}
	for _, p := range s.Profiles().Items {
		if p.ID == id {
			t.Fatal("deleted custom still present")
		}
	}
}
func TestProfileLayoutGuardCannotBeBypassedByCopy(t *testing.T) {
	s := freshSettings()
	if err := s.ProfileAction("select", "microsoft", ""); err != nil {
		t.Fatal(err)
	}
	before := s
	if err := s.Set("keyboard-layout", "hsu"); err == nil || s != before {
		t.Fatal("incompatible layout accepted")
	}
	if err := s.ProfileAction("copy", "microsoft", "copy"); err != nil {
		t.Fatal(err)
	}
	before = s
	if err := s.Set("keyboard-layout", "eten"); err == nil || s != before {
		t.Fatal("copy bypasses layout guard")
	}
	if err := s.ProfileAction("select", "0c", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("keyboard-layout", "hsu"); err != nil {
		t.Fatal(err)
	}
	before = s
	if err := s.ProfileAction("select", "microsoft", ""); err == nil || s != before {
		t.Fatal("select silently changed layout")
	}
}
func TestUnknownProfileMetadataReadOnlyAndSafeRules(t *testing.T) {
	s := freshSettings()
	raw, _ := json.Marshal(s)
	for _, replacement := range []struct{ from, to string }{
		{`"version":1`, `"version":99`},
		{`"shift_space":"disabled"`, `"shift_space":"future"`},
		{`"rules":{`, `"rules":{"future":true,`},
		{`"builtin":true`, `"builtin":false`},
		{`"id":"0c"`, `"id":"unknown"`},
	} {
		body := strings.Replace(string(raw), replacement.from, replacement.to, 1)
		var got Settings
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatal(err)
		}
		if !got.ReadOnly() || got.ProfileRules().Validate() != nil || got.ProfileRules().ShiftSpace != "disabled" {
			t.Fatalf("unsafe future profile: %s", replacement.to)
		}
		if err := Save(filepath.Join(t.TempDir(), FileName), got); !errors.Is(err, ErrReadOnly) {
			t.Fatal("future profile overwritten")
		}
	}
}
func TestProfileLocksAllManagedEntrypoints(t *testing.T) {
	s := freshSettings()
	for _, it := range s.Items() {
		if !managedKey(it.Key) {
			continue
		}
		if !it.Locked || it.Value == "" {
			t.Fatalf("hidden or unlocked true value: %s", it.Key)
		}
		before := s
		var se *SetError
		if err := s.Set(it.Key, it.Value); !errors.As(err, &se) || se.Code != "locked_by_profile" || s != before {
			t.Fatalf("lock bypass for %s: %v", it.Key, err)
		}
	}
	user := filepath.Join(t.TempDir(), "user.json")
	if err := Save(Path(user), s); err != nil {
		t.Fatal(err)
	}
	if _, err := Change(user, [2]string{"left-shift", "on"}); err == nil {
		t.Fatal("legacy menu bypasses profile lock")
	}
	if _, err := Change(user, [2]string{"output-simplified", "on"}); err != nil {
		t.Fatal(err)
	}
}

func TestCustomRulesCannotBypassCompatibility(t *testing.T) {
	s := Default()
	if err := s.ProfileAction("copy", "0c", "custom"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("style-punctuation", "microsoft"); err != nil {
		t.Fatal(err)
	}
	for _, p := range [][2]string{{"keyboard-layout", "hsu"}, {"mixed-input", "on"}, {"english-autocomplete", "on"}, {"toneless", "on"}} {
		before := s
		if err := s.Set(p[0], p[1]); err == nil || s != before {
			t.Fatalf("unsupported combination accepted: %v", p)
		}
	}
	s = Default()
	if err := s.Set("english-autocomplete", "on"); err != nil {
		t.Fatal(err)
	}
	before := s
	if err := s.Set("style-shift-space", "fullwidth"); err == nil || s != before {
		t.Fatal("reverse-order rule edit bypassed constraint")
	}
}
