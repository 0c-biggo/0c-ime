package settings

import (
	"encoding/json"
	"testing"
)

func TestPaletteSettingsPreserveUnknownAndExplicitChoice(t *testing.T) {
	var s Settings
	if err := json.Unmarshal([]byte(`{"candidate_layout":"future-layout","emoji_shortcut":"future-key","other":3}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.CandidateLayout != "vertical" || s.EmojiShortcut != "equals" {
		t.Fatal("invalid setting did not use the default")
	}
	b, _ := json.Marshal(s)
	var raw map[string]any
	json.Unmarshal(b, &raw)
	if raw["candidate_layout"] != "future-layout" || raw["emoji_shortcut"] != "future-key" {
		t.Fatal("future choices erased")
	}
	if err := s.Set("candidate-layout", "grid"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("emoji-shortcut", "equals"); err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(s)
	var restored Settings
	json.Unmarshal(b, &restored)
	if restored.CandidateLayout != "grid" || restored.EmojiShortcut != "equals" {
		t.Fatal("settings did not roundtrip")
	}
	if err := restored.Set("emoji-shortcut", "slash"); err == nil {
		t.Fatal("unsupported occupied key accepted")
	}
}

func TestEmojiDefaultsAndExplicitOffSurviveRoundTrip(t *testing.T) {
	for _, input := range []string{`{}`, `{"emoji_shortcut":"off","emoji_equals_first":false,"future":7}`} {
		var s Settings
		if err := json.Unmarshal([]byte(input), &s); err != nil {
			t.Fatal(err)
		}
		if input == `{}` {
			if s.EmojiShortcut != "equals" || !s.EmojiEqualsFirst {
				t.Fatal("default must enable palette with equals first")
			}
		} else if s.EmojiShortcut != "off" || s.EmojiEqualsFirst {
			t.Fatal("explicit user opt-out was overridden")
		}
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		var restored Settings
		if err = json.Unmarshal(b, &restored); err != nil || restored != s {
			t.Fatalf("roundtrip: %+v %v", restored, err)
		}
	}
	var s Settings
	json.Unmarshal([]byte(`{"emoji_equals_first":"future"}`), &s)
	b, _ := json.Marshal(s)
	var raw map[string]any
	json.Unmarshal(b, &raw)
	if !s.EmojiEqualsFirst || raw["emoji_equals_first"] != "future" {
		t.Fatal("unknown option must be preserved")
	}
	if err := s.Set("emoji-equals-first", "off"); err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(s)
	json.Unmarshal(b, &raw)
	if s.EmojiEqualsFirst || raw["emoji_equals_first"] != false {
		t.Fatal("explicit setting must replace unknown value")
	}
}
