// Package inputstyle defines versioned, serializable input habits. It contains
// no platform I/O; shells and the core share the same immutable preset catalog.
package inputstyle

import "fmt"

const Version = 1

// Rules contains behavior that cannot be expressed by the older settings.
// A zero Rules preserves the existing engine behavior for older callers.
type Rules struct {
	CapsLetter          string `json:"caps_letter"`
	CandidateNavigation string `json:"candidate_navigation"`
	ShiftDelete         string `json:"shift_delete"`
	Up                  string `json:"up"`
	CandidateTab        string `json:"candidate_tab"`
	CandidatePageSize   int    `json:"candidate_page_size"`
	Version             int    `json:"version"`
	Punctuation         string `json:"punctuation"`
	ShiftSpace          string `json:"shift_space"`
	Tab                 string `json:"tab"`
	CandidateSpace      string `json:"candidate_space"`
	CandidateEnter      string `json:"candidate_enter"`
	ShiftLetter         string `json:"shift_letter"`
}

func (r Rules) Validate() error {
	if r == (Rules{}) {
		return nil
	}
	if r.CandidatePageSize != 9 && r.CandidatePageSize != 10 {
		return fmt.Errorf("invalid candidate page size %d", r.CandidatePageSize)
	}
	if r.Version != Version {
		return fmt.Errorf("unsupported input style rules version %d", r.Version)
	}
	for _, f := range []struct {
		key, value string
		allowed    []string
	}{
		{"caps_letter", r.CapsLetter, []string{"platform", "lower"}},
		{"candidate_navigation", r.CandidateNavigation, []string{"0c", "horizontal"}},
		{"shift_delete", r.ShiftDelete, []string{"0c", "tone"}},
		{"up", r.Up, []string{"0c", "cycle"}},
		{"candidate_tab", r.CandidateTab, []string{"length", "grid", "next"}},
		{"punctuation", r.Punctuation, []string{"0c", "microsoft", "standard", "chewing"}},
		{"shift_space", r.ShiftSpace, []string{"disabled", "fullwidth", "space", "single"}},
		{"tab", r.Tab, []string{"0c", "cycle", "segment"}},
		{"candidate_space", r.CandidateSpace, []string{"page", "select"}},
		{"candidate_enter", r.CandidateEnter, []string{"select", "commit"}},
		{"shift_letter", r.ShiftLetter, []string{"inline", "commit", "lower"}},
	} {
		ok := false
		for _, v := range f.allowed {
			ok = ok || f.value == v
		}
		if !ok {
			return fmt.Errorf("invalid input style %s: %q", f.key, f.value)
		}
	}
	return nil
}

// Behavior is the complete snapshot of the style-managed legacy settings.
// Keeping scalar values allows settings snapshots to remain comparable.
type Behavior struct {
	ToggleKey           string `json:"toggle_key"`
	ModeMemory          string `json:"mode_memory"`
	StartMode           string `json:"start_mode"`
	CapsLockEnglish     bool   `json:"caps_lock_english"`
	EscClear            bool   `json:"esc_clear"`
	SpaceSelects        bool   `json:"space_selects"`
	AdvanceAfterSelect  bool   `json:"advance_after_select"`
	CandidateCursor     string `json:"candidate_cursor"`
	CandidateLayout     string `json:"candidate_layout"`
	SelectionKeys       string `json:"selection_keys"`
	MixedInput          bool   `json:"mixed_input"`
	EnglishAutocomplete bool   `json:"english_autocomplete"`
	EmojiShortcut       string `json:"emoji_shortcut"`
	EmojiEqualsFirst    bool   `json:"emoji_equals_first"`
	CtrlPunctuation     bool   `json:"ctrl_punctuation"`
	Toneless            bool   `json:"toneless"`
}

type Preset struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Rules    Rules    `json:"rules"`
	Behavior Behavior `json:"behavior"`
}

func Get(id string) (Preset, bool) { return GetForPlatform(id, "linux") }

func GetForPlatform(id, goos string) (Preset, bool) {
	b := Behavior{ToggleKey: "right", ModeMemory: "native", StartMode: "zhuyin", EscClear: true, SpaceSelects: true, CandidateCursor: "after", CandidateLayout: "vertical", SelectionKeys: "1234567890", EmojiShortcut: "equals", EmojiEqualsFirst: true, CtrlPunctuation: true}
	if goos == "windows" {
		b.ToggleKey = "both"
	}
	if goos == "darwin" {
		b.ToggleKey = "none"
		b.ModeMemory = "shared"
		b.CandidateCursor = "before"
	}
	r := Rules{Version: Version, CapsLetter: "platform", CandidateNavigation: "0c", ShiftDelete: "0c", Up: "0c", CandidateTab: "length", CandidatePageSize: 10, Punctuation: "0c", ShiftSpace: "disabled", Tab: "0c", CandidateSpace: "page", CandidateEnter: "select", ShiftLetter: "inline"}
	p := Preset{ID: id, Rules: r, Behavior: b}
	switch id {
	case "0c":
		p.Name = "靈犀注音"
	case "microsoft":
		p.Name = "微軟新注音"
		p.Behavior.ToggleKey = "both"
		p.Behavior.EmojiShortcut = "off"
		p.Rules.Punctuation = "microsoft"
		p.Rules.ShiftSpace = "fullwidth"
		p.Rules.Up = "cycle"
		p.Rules.CandidateTab = "grid"
	case "chewing":
		p.Name = "新酷音"
		p.Behavior.CandidateCursor = "before"
		p.Behavior.CandidateLayout = "horizontal"
		p.Behavior.EmojiShortcut = "off"
		p.Rules.Tab = "segment"
		p.Rules.Punctuation = "chewing"
		p.Rules.CandidateNavigation = "horizontal"
		p.Rules.ShiftSpace = "fullwidth"
	case "mcbopomofo":
		p.Name = "小麥注音"
		p.Behavior.ToggleKey = "none"
		p.Behavior.CandidateCursor = "before"
		p.Behavior.CapsLockEnglish = true
		p.Behavior.EscClear = false
		p.Behavior.EmojiShortcut = "off"
		p.Rules.Punctuation = "standard"
		p.Rules.Tab = "cycle"
		p.Rules.ShiftSpace = "space"
		p.Rules.ShiftLetter = "commit"
		p.Rules.CandidatePageSize = 9
		p.Rules.CapsLetter = "lower"
	case "macos":
		p.Name = "macOS 內建注音"
		p.Behavior.ToggleKey = "none"
		p.Behavior.CandidateCursor = "before"
		p.Behavior.CapsLockEnglish = true
		p.Behavior.EmojiShortcut = "off"
		p.Rules.ShiftDelete = "tone"
	default:
		return Preset{}, false
	}
	return p, true
}

func Presets() []Preset { return PresetsForPlatform("linux") }
func PresetsForPlatform(goos string) []Preset {
	var out []Preset
	for _, id := range []string{"0c", "microsoft", "chewing", "mcbopomofo", "macos"} {
		p, _ := GetForPlatform(id, goos)
		out = append(out, p)
	}
	return out
}
