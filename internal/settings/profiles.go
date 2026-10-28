package settings

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/0c-biggo/0c-ime/core/inputstyle"
)

// Profile stores a complete immutable rule snapshot. Built-in catalog updates do
// not change an already selected profile or a user's copy of it.
type Profile struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Builtin bool              `json:"builtin"`
	Source  string            `json:"source"`
	Version int               `json:"version"`
	Values  map[string]string `json:"values"`
	Rules   inputstyle.Rules  `json:"rules"`
}
type profileStore struct {
	Active string    `json:"active"`
	Items  []Profile `json:"items"`
}
type ProfileItem struct {
	Profile
	Description string   `json:"description"`
	Summary     []string `json:"summary"`
	Available   bool     `json:"available"`
	Reason      string   `json:"reason,omitempty"`
}
type ProfileView struct {
	Active  string        `json:"active"`
	Running string        `json:"running"`
	Pending bool          `json:"pending"`
	Items   []ProfileItem `json:"items"`
}

var profileKeys = []string{"toggle-key", "caps-lock-english", "esc-clear", "space-selects", "advance-after-select", "candidate-cursor", "candidate-layout", "selection-keys", "mixed-input", "english-autocomplete", "emoji-shortcut", "emoji-equals-first", "ctrl-punctuation", "toneless"}

func managedKey(key string) bool {
	if _, ok := lookupRule(key); ok {
		return true
	}
	if key == "left-shift" {
		return true
	}
	for _, k := range profileKeys {
		if k == key {
			return true
		}
	}
	return false
}
func profileError(message string) error {
	return &SetError{Code: CodeInvalidValue, Key: "profile", Msg: message}
}
func validProfileName(name string) bool {
	if name != strings.TrimSpace(name) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 80 {
		return false
	}
	for _, c := range name {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}
func (s Settings) profileValues() map[string]string {
	values := map[string]string{}
	for _, k := range profileKeys {
		if v, ok := s.Value(k); ok {
			values[k] = v
		}
	}
	// caps-lock-english is hidden on macOS but remains part of the snapshot.
	values["caps-lock-english"] = onOff(s.CapsLockEnglish)
	return values
}
func builtinProfile(p inputstyle.Preset) Profile {
	b := p.Behavior
	values := map[string]string{
		"toggle-key": b.ToggleKey, "caps-lock-english": onOff(b.CapsLockEnglish), "esc-clear": onOff(b.EscClear), "space-selects": onOff(b.SpaceSelects), "advance-after-select": onOff(b.AdvanceAfterSelect), "candidate-cursor": b.CandidateCursor, "candidate-layout": b.CandidateLayout, "selection-keys": b.SelectionKeys, "mixed-input": onOff(b.MixedInput), "english-autocomplete": onOff(b.EnglishAutocomplete), "emoji-shortcut": b.EmojiShortcut, "emoji-equals-first": onOff(b.EmojiEqualsFirst), "ctrl-punctuation": onOff(b.CtrlPunctuation), "toneless": onOff(b.Toneless),
	}
	return Profile{ID: p.ID, Name: p.Name, Builtin: true, Source: p.ID, Version: inputstyle.Version, Values: values, Rules: p.Rules}
}
func (s Settings) store() profileStore {
	if s.profiles != "" {
		var st profileStore
		if json.Unmarshal([]byte(s.profiles), &st) == nil {
			return st
		}
	}
	p, _ := inputstyle.GetForPlatform("0c", runtime.GOOS)
	return profileStore{Active: "legacy", Items: []Profile{{ID: "legacy", Name: "我的原有設定", Source: "legacy", Version: inputstyle.Version, Values: s.profileValues(), Rules: p.Rules}}}
}
func (s *Settings) putStore(st profileStore) { b, _ := json.Marshal(st); s.profiles = string(b) }
func (s Settings) ActiveProfile() Profile {
	st := s.store()
	for _, p := range st.Items {
		if p.ID == st.Active {
			return p
		}
	}
	p, _ := inputstyle.GetForPlatform("0c", runtime.GOOS)
	return builtinProfile(p)
}
func (s Settings) ProfileRules() inputstyle.Rules {
	if s.profileInvalid {
		p, _ := inputstyle.GetForPlatform("0c", runtime.GOOS)
		return p.Rules
	}
	return s.ActiveProfile().Rules
}
func (s Settings) ProfileFingerprint() string {
	p := s.ActiveProfile()
	b, _ := json.Marshal(struct {
		ID     string
		Values map[string]string
		Rules  inputstyle.Rules
	}{p.ID, s.profileValues(), p.Rules})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func (s Settings) profileLocked(key string) bool { return managedKey(key) && s.ActiveProfile().Builtin }
func (s *Settings) syncProfile() {
	if s.profiles == "" {
		s.putStore(s.store())
		return
	}
	st := s.store()
	for i := range st.Items {
		if st.Items[i].ID == st.Active && !st.Items[i].Builtin {
			st.Items[i].Values = s.profileValues()
		}
	}
	s.putStore(st)
}
func (s *Settings) applyProfile(p Profile) error {
	if err := p.Rules.Validate(); err != nil {
		return err
	}
	next := *s
	next.profiles = ""
	for _, k := range profileKeys {
		v, ok := p.Values[k]
		if !ok {
			return profileError("風格缺少設定：" + k)
		}
		if k == "caps-lock-english" {
			if v != "on" && v != "off" {
				return profileError("Caps Lock 設定無效")
			}
			next.CapsLockEnglish = v == "on"
			next.forget("caps_lock_english")
			continue
		}
		if err := next.Set(k, v); err != nil {
			return err
		}
	}
	next.profiles = s.profiles
	*s = next
	return nil
}
func (s Settings) profileAvailability(p Profile) (bool, string) {
	// A preset never silently changes the separately selected physical layout.
	if s.KeyboardLayout != LayoutStandard && p.Rules != inputstyleDefaultRules() {
		return false, "此風格目前支援標準注音排列；請先在「鍵盤與符號」選擇標準排列"
	}
	return true, ""
}
func (s Settings) Profiles() ProfileView {
	st := s.store()
	out := ProfileView{Active: st.Active, Items: []ProfileItem{}}
	for _, preset := range inputstyle.PresetsForPlatform(runtime.GOOS) {
		p := builtinProfile(preset)
		for _, saved := range st.Items {
			if saved.ID == p.ID {
				p = saved
				break
			}
		}
		out.Items = append(out.Items, s.profileItem(p))
	}
	for _, p := range st.Items {
		if !p.Builtin {
			out.Items = append(out.Items, s.profileItem(p))
		}
	}
	return out
}
func (s Settings) profileItem(p Profile) ProfileItem {
	available, reason := s.profileAvailability(p)
	description := "固定操作習慣；需要微調時可複製成自訂"
	if !p.Builtin {
		description = "自訂操作習慣，可在各設定頁微調"
	} else if p.ID != "0c" {
		description = "參考常用操作的相近風格；字詞排序、候選視窗與未列出的功能仍使用靈犀注音"
		if p.ID == "microsoft" {
			description += "。此版參考全半形、反引號前導與選字鍵；反斜線、引號、角括號與問號已對照 Windows 11 注音實測，其餘操作依靈犀規則"
		}
		if p.ID == "macos" {
			description += "。此版參考選字位置、空白選字與退回聲調；系統輸入來源切換仍由 macOS 管理"
		}
	}
	shift := map[string]string{"disabled": "Shift＋空白不切換字寬", "fullwidth": "Shift＋空白切換全半形（含英數）", "space": "Shift＋空白插入空白"}[p.Rules.ShiftSpace]
	tab := map[string]string{"0c": "Tab 使用靈犀候選操作", "cycle": "Tab／Shift＋Tab 輪換字詞", "segment": "Tab 調整斷詞"}[p.Rules.Tab]
	values := map[string]string{}
	for k, v := range p.Values {
		values[k] = v
	}
	for _, sp := range ruleSpecs() {
		values[sp.key] = sp.get(p.Rules)
	}
	p.Values = values
	return ProfileItem{Profile: p, Description: description, Summary: []string{shift, tab}, Available: available, Reason: reason}
}

// ProfileAction is transactional even for callers holding an in-memory snapshot.
// Filesystem callers must also hold settings-write.lock across read/action/save.
func (s *Settings) ProfileAction(action, id, name string) error {
	if s.ReadOnly() {
		return ErrReadOnly
	}
	next := *s
	st := next.store()
	idx := -1
	for i, p := range st.Items {
		if p.ID == id {
			idx = i
			break
		}
	}
	var p Profile
	if idx >= 0 {
		p = st.Items[idx]
	} else if preset, ok := inputstyle.GetForPlatform(id, runtime.GOOS); ok {
		p = builtinProfile(preset)
	} else {
		return profileError("找不到指定的輸入習慣")
	}
	switch action {
	case "select", "copy":
		if available, reason := next.profileAvailability(p); !available {
			return profileError(reason)
		}
		if action == "copy" {
			name = strings.TrimSpace(name)
			if !validProfileName(name) {
				return profileError("名稱須為 1～80 字，不能包含控制字元")
			}
			if len(st.Items) >= 64 {
				return profileError("自訂風格已達上限，請先刪除不使用的風格")
			}
			var token [12]byte
			if _, err := rand.Read(token[:]); err != nil {
				return err
			}
			p.ID = "custom-" + hex.EncodeToString(token[:])
			p.Name = name
			p.Builtin = false
			idx = -1
		}
		if err := next.applyProfile(p); err != nil {
			return err
		}
		if idx < 0 {
			st.Items = append(st.Items, p)
		}
		st.Active = p.ID
	case "rename":
		if p.Builtin {
			return profileError("內建風格不能改名；請先複製成自訂")
		}
		name = strings.TrimSpace(name)
		if !validProfileName(name) {
			return profileError("名稱須為 1～80 字，不能包含控制字元")
		}
		st.Items[idx].Name = name
	case "delete":
		if p.Builtin {
			return profileError("內建風格不能刪除")
		}
		if st.Active == id {
			return profileError("請先切換至其他風格，再刪除目前的風格")
		}
		st.Items = append(st.Items[:idx], st.Items[idx+1:]...)
	default:
		return profileError("不支援的風格操作")
	}
	next.putStore(st)
	if err := next.validateProfileCombination(); err != nil {
		return err
	}
	*s = next
	return nil
}
func (s *Settings) loadProfiles(raw json.RawMessage) {
	// Unknown formats stay readable through the flat projection, but never writable.
	s.profiles = string(raw)
	s.profileInvalid = true
	var st profileStore
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&st) != nil || st.Active == "" || len(st.Items) == 0 || len(st.Items) > 64 {
		s.readOnly = true
		return
	}
	seen := map[string]bool{}
	found := false
	for _, p := range st.Items {
		if !validProfileID(p.ID) || seen[p.ID] || !validProfileName(p.Name) || p.Version != inputstyle.Version || p.Rules.Version != inputstyle.Version || p.Rules.Validate() != nil {
			s.readOnly = true
			return
		}
		seen[p.ID] = true
		_, reserved := inputstyle.GetForPlatform(p.ID, runtime.GOOS)
		if reserved != p.Builtin || (!reserved && p.ID != "legacy" && !strings.HasPrefix(p.ID, "custom-")) {
			s.readOnly = true
			return
		}
		for k := range p.Values {
			if !managedKey(k) || k == "left-shift" {
				s.readOnly = true
				return
			}
		}
		if p.Builtin {
			if _, ok := inputstyle.GetForPlatform(p.ID, runtime.GOOS); !ok {
				s.readOnly = true
				return
			}
		}
		check := *s
		if err := check.applyProfile(p); err != nil {
			s.readOnly = true
			return
		}
		if p.ID == st.Active {
			found = true
		}
	}
	if !found {
		s.readOnly = true
		return
	}
	s.profileInvalid = false
	// Flat fields are the compatibility projection, never an override of a preset.
	if err := s.applyProfile(s.ActiveProfile()); err != nil {
		s.readOnly = true
		s.profileInvalid = true
	}
	s.putStore(st)
	if s.validateProfileCombination() != nil {
		s.readOnly = true
		s.profileInvalid = true
	}
}
func freshSettings() Settings {
	s := Default()
	if err := s.ProfileAction("select", "0c", ""); err != nil {
		panic(fmt.Sprintf("invalid built-in input style: %v", err))
	}
	st := s.store()
	st.Items = st.Items[1:]
	s.putStore(st)
	return s
}

func inputstyleDefaultRules() inputstyle.Rules {
	p, _ := inputstyle.GetForPlatform("0c", runtime.GOOS)
	return p.Rules
}

func validProfileID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// styleConflict 是一條操作規則跟靈犀擴充功能真正衝突的組合（#233）。表外的規則（標點的其他值、候選操作、
// 每頁候選數、Tab 輪換……）跟中英混打、英文補全、聲調選填可以並用。reason 是功能停用時顯示的原因，
// disabledBy 依它找回規則，所以每條都不同。
type styleConflict struct {
	rule, feature, reason string
	when                  func(inputstyle.Rules) bool
}

func styleConflicts() []styleConflict {
	return []styleConflict{
		// 混打的英文照半形留在組字區，全形下打出「我Ｈello」這種半形全形夾雜。
		{"style-shift-space", "mixed-input", "Shift＋空白切換全半形時關閉", func(r inputstyle.Rules) bool { return r.ShiftSpace == "fullwidth" }},
		// Tab 要接受混打的英文補全；調整斷詞時 Tab 一律拿去改斷詞。
		{"style-tab", "mixed-input", "Tab 調整斷詞時關閉", func(r inputstyle.Rules) bool { return r.Tab == "segment" }},
		// 混打靠 Shift＋字母打大寫英文（DNS、iPhone）；送出組字會把字拆開（我ㄛPhone），打小寫則改掉大小寫。
		{"style-shift-letter", "mixed-input", "Shift＋英文字母送出組字時關閉", func(r inputstyle.Rules) bool { return r.ShiftLetter == "commit" }},
		{"style-shift-letter", "mixed-input", "Shift＋英文字母打小寫時關閉", func(r inputstyle.Rules) bool { return r.ShiftLetter == "lower" }},
		// 聲調選填用 ' 切字、\ 打頓號；微軟參考的半形 ' 會吃掉沒打完的音節，小麥與新酷音的 \ 是全形反斜線。
		{"style-punctuation", "toneless", "標點操作不是靈犀時關閉", func(r inputstyle.Rules) bool { return r.Punctuation != "0c" }},
	}
}

// styleConflictReason 回報功能 key 因目前的規則停用的原因；沒有衝突時為空字串。
func (s Settings) styleConflictReason(key string) string {
	rules := s.ProfileRules()
	for _, c := range styleConflicts() {
		if c.feature == key && c.when(rules) {
			return c.reason
		}
	}
	return ""
}

// styleConflictRule 是停用原因所在的規則 key（Item.DisabledBy）；不是規則造成的回傳空字串。
func styleConflictRule(reason string) string {
	for _, c := range styleConflicts() {
		if c.reason == reason {
			return c.rule
		}
	}
	return ""
}

func (s Settings) featureOn(key string) bool {
	switch key {
	case "mixed-input":
		return s.MixedInput
	case "english-autocomplete":
		return s.EnglishAutocomplete
	case "toneless":
		return s.Toneless
	}
	return false
}

func (s Settings) validateProfileCombination() error {
	p := s.ActiveProfile()
	if ok, reason := s.profileAvailability(p); !ok {
		return profileError(reason)
	}
	rules := s.ProfileRules()
	for _, c := range styleConflicts() {
		if c.when(rules) && s.featureOn(c.feature) {
			feature, _ := lookup(c.feature)
			rule, _ := lookupRule(c.rule)
			return profileError(fmt.Sprintf("「%s」與「%s：%s」不能同時使用；請先關閉其中一項", feature.label, rule.label, rule.labels[rule.get(rules)]))
		}
	}
	return nil
}
