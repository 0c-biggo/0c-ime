package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnglishAutocompleteTogglePersistsAndDefaultsOff(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if s, err := Load(path); err != nil || s.EnglishAutocomplete {
		t.Fatalf("default: %+v %v", s, err)
	}
	if err := os.WriteFile(path, []byte(`{"selection":"statistical","future":42}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"on", "off", "on"} {
		s, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Set("english-autocomplete", value); err != nil {
			t.Fatal(err)
		}
		if err := Save(path, s); err != nil {
			t.Fatal(err)
		}
		back, err := Load(path)
		if err != nil || back.EnglishAutocomplete != (value == "on") || back.Selection != Statistical {
			t.Fatalf("restart: %+v %v", back, err)
		}
		b, _ := os.ReadFile(path)
		if !strings.Contains(string(b), `"future": 42`) {
			t.Fatalf("lost unknown setting: %s", b)
		}
	}
}

func TestEnglishAutocompleteUnknownValueRetainedUntilExplicitChange(t *testing.T) {
	var s Settings
	if err := json.Unmarshal([]byte(`{"english_autocomplete":"later","punctuation":false}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.EnglishAutocomplete || s.Punctuation {
		t.Fatal("invalid value must default only its own field")
	}
	s.Set("selection", Statistical)
	b, _ := json.Marshal(s)
	if !strings.Contains(string(b), `"english_autocomplete":"later"`) {
		t.Fatal(string(b))
	}
	s.Set("english-autocomplete", "off")
	b, _ = json.Marshal(s)
	if !strings.Contains(string(b), `"english_autocomplete":false`) {
		t.Fatal(string(b))
	}
}
