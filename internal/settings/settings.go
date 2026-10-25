// Package settings 是使用者可切換的引擎設定；Linux 標準目錄使用 0c/settings.json，缺檔時相容 zuin/settings.json。
// 引擎只在啟動時讀取；檔案不存在或欄位缺漏時使用預設值。
//
// 檔案前後相容：較新版本寫入本版不認得的欄位、已知欄位的未知值，都不會讓整份設定被拒收或重設，
// 只有讀不懂的那個欄位退回預設；存檔時原文保留，回復到舊版再改任何設定，新版的設定也不會消失。
// 檔案的 schema 比本版新時只讀不寫（ErrReadOnly）。
package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/0c-biggo/0c-ime/core/fuzzy"
)

const (
	Neural      = "neural"      // 神經模型依前文重排統計引擎的候選（預設）
	Statistical = "statistical" // 只用統計引擎
	FileName    = "settings.json"

	// SchemaVersion 是本版寫入、也完全理解的檔案格式。
	SchemaVersion = 2

	LayoutStandard = "standard" // 標準（大千）注音鍵盤排列
	LayoutETen     = "eten"     // 倚天 41 鍵（#83）
	LayoutHsu      = "hsu"      // 許氏（#183）
)

// 讀取結果的檔案狀態。
const (
	StateOK         = "ok"
	StateMissing    = "missing"
	StateCorrupt    = "corrupt"
	StateUnreadable = "unreadable"
)

// SetError 的代碼，供機器可讀的輸出使用。
const (
	CodeUnknownKey   = "unknown_key"
	CodeInvalidValue = "invalid_value"
)

var (
	// ErrReadOnly：設定檔來自較新版本，本版不覆寫。
	ErrReadOnly = errors.New("設定檔來自較新版本，本版只讀不寫")
	// ErrCorrupt：設定檔不是可解析的 JSON 物件。
	ErrCorrupt = errors.New("設定檔無法解析")
)

type Settings struct {
	Selection           string `json:"selection"`
	Punctuation         bool   `json:"punctuation"`
	DocumentContext     bool   `json:"document_context"`
	MixedInput          bool   `json:"mixed_input"`
	EnglishAutocomplete bool   `json:"english_autocomplete"`
	LeftShift           bool   `json:"left_shift"` // 舊欄位：ToggleKey 含左 Shift。讀新檔時由 ToggleKey 推出，存檔時一併寫回給舊版讀
	// ToggleKey 是單按哪個 Shift 切換注音／英文（#117）：right、both、left、none。macOS 預設 none，Windows both（#206），Linux right。
	// 檔案沒有這個欄位時沿用平台預設；舊欄位 left_shift=true 保留為 both。Shift 加字母一律打大寫，不算切換。
	ToggleKey string `json:"toggle_key"`
	// ModeMemory 是切換視窗或輸入框後的中英模式（#115）：native 照平台原本的做法（Windows 每個程式各自記住、
	// Linux 回到注音），shared 全部共用同一個模式（macOS 的預設，同系統輸入方式全域共用）。
	ModeMemory string `json:"mode_memory"`
	// StartMode 是開始時的中英模式（#160）：zhuyin（預設，檔案沒有這個欄位時也是）或 english。沒有記住的模式時
	// 從它開始：Windows 每個新開的程式、macOS／Linux 每次換到輸入框（mode_memory=native），或登入後
	// 第一個模式（mode_memory=shared）。
	StartMode          string `json:"start_mode"`
	EscClear           bool   `json:"esc_clear"`            // 按 Esc 清除整段組字（多數注音輸入法的預設）；false 時只清未打完的注音
	SpaceSelects       bool   `json:"space_selects"`        // 注音打完後空白開候選（預設）；false 時空白輸入空格、↓ 選字（#107、#110）
	AdvanceAfterSelect bool   `json:"advance_after_select"` // 從候選窗選字後游標移到所選詞後面；預設 false 游標留在原處（#133）
	CandidateCursor    string `json:"candidate_cursor"`
	CandidateLayout    string `json:"candidate_layout"`
	EmojiShortcut      string `json:"emoji_shortcut"`
	EmojiEqualsFirst   bool   `json:"emoji_equals_first"`
	KeyboardLayout     string `json:"keyboard_layout"` // standard（預設）、eten（倚天 41 鍵，#83）或 hsu（許氏，#183）
	// OutputSimplified：送出時把繁體轉成簡體（#118）；組字、候選與學習仍是繁體。預設 false。
	OutputSimplified bool `json:"output_simplified"`
	// SimplifiedHotkey：按 SimplifiedHotkeyLabel 的快捷鍵切換「輸出簡體字」（#192）。預設 false：外殼不攔這個鍵，
	// 照舊交給應用程式。切換跟輸入法選單同一條即時請求（output_simplified），不重啟引擎。
	SimplifiedHotkey bool `json:"simplified_hotkey"`
	// SelectionKeys 是候選窗的選字鍵（#112）：1234567890（預設，檔案沒有這個欄位時也是）、1234qweras 或 asdfghjkl;。
	// 換成字母鍵時數字 1–0 仍照位置選字。
	SelectionKeys string `json:"selection_keys"`
	// CtrlPunctuation：中文模式按 Ctrl＋, . ' ; 與 Ctrl＋Shift＋/ 1 打全形標點（#168）。預設 true；
	// 想保留程式快速鍵（VS Code 的 Ctrl＋, 等）時關閉，這些鍵照舊交給應用程式。
	CtrlPunctuation bool `json:"ctrl_punctuation"`
	// CapsLockEnglish：Caps Lock 開著時直接打英文（#173，Windows、Linux）。預設 false：注音模式不理 Caps Lock（#168）。
	// 開啟時 Caps Lock 是暫時的英文模式：字母大寫、數字與符號半形，關掉回到原本的模式。macOS 一律如此（#84），不用這一項。
	CapsLockEnglish bool `json:"caps_lock_english"`
	// Toneless：不打聲調也能選字（#150，聲調選填）。預設 false。開啟時沒打聲調的音節由解碼決定、有打的照聲調；
	// 中英混打的自動判斷停用（與混打二選一），已儲存的 mixed_input 不動，關掉這一項就照舊。
	Toneless bool  `json:"toneless"`
	Fuzzy    Fuzzy `json:"fuzzy,omitzero"`
	// 0.2.0 開發期間的內部開關 preview_0_2（#128）已拿掉：自訂詞彙、加詞按鍵、「我的片語」、本機 API 等
	// 入口 0.2.0 起所有人都有。舊檔裡的 preview_0_2 不論值為何都忽略，存檔時移除（retiredKeys）。

	// extra 是本版沒有解讀、但存檔要原樣寫回的成員（JSON 物件，鍵已排序）：未知欄位，
	// 以及已知欄位中本版讀不懂的值。用原文字串保存，Settings 才能維持可比較。
	extra    string
	profiles string // canonical JSON keeps Settings comparable
	// readOnly：檔案的 schema 比本版新，或不是整數，所以不覆寫。
	readOnly       bool
	profileInvalid bool
}

// Fuzzy 是各組模糊音的啟用方式：off、select（預設，開候選時合併近音）或 auto（另在語境明顯支持近音時自動改字）。
// 欄位缺漏視為預設 select，關閉要明確寫 off；值不認得（包含不是字串的 JSON）時只有那一組當作 off。
// 本版不認得的成員（較新版本新增的配對、已知配對的非字串值、型別不同的整個 fuzzy 欄位）
// 原文保留到下次存檔：較新版本寫入的設定不被舊版沖掉，也不會讓整份設定檔被拒收。
type Fuzzy struct {
	ZZh   string // ㄗ／ㄓ
	CCh   string // ㄘ／ㄔ
	SSh   string // ㄙ／ㄕ
	EnEng string // ㄣ／ㄥ

	extra string // 其他成員的原文（JSON 物件，鍵已排序）；沒有時為空字串
	other string // fuzzy 欄位不是物件時的原文
}

func (f *Fuzzy) field(p fuzzy.Pair) *string {
	switch p {
	case fuzzy.ZZh:
		return &f.ZZh
	case fuzzy.CCh:
		return &f.CCh
	case fuzzy.SSh:
		return &f.SSh
	}
	return &f.EnEng
}

func (f *Fuzzy) UnmarshalJSON(b []byte) error {
	*f = Fuzzy{}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(b, &members); err != nil {
		f.other = string(b) // 型別不同：視為全部關閉，原文保留
		return nil
	}
	for _, p := range fuzzy.Pairs() {
		raw, ok := members[p.ID()]
		var value string
		if ok && json.Unmarshal(raw, &value) == nil {
			*f.field(p) = value
			delete(members, p.ID())
		}
	}
	if len(members) > 0 {
		extra, err := json.Marshal(members)
		if err != nil {
			return err
		}
		f.extra = string(extra)
	}
	return nil
}

func (f Fuzzy) MarshalJSON() ([]byte, error) {
	members := map[string]json.RawMessage{}
	if f.extra != "" {
		if err := json.Unmarshal([]byte(f.extra), &members); err != nil {
			return nil, err
		}
	}
	for _, p := range fuzzy.Pairs() {
		if v := *f.field(p); v != "" {
			raw, _ := json.Marshal(v)
			members[p.ID()] = raw
		}
	}
	if len(members) == 0 && f.other != "" {
		return []byte(f.other), nil
	}
	return json.Marshal(members)
}

// set 修改一組配對；寫入明確的值就蓋掉先前保留的非字串原文。
func (f *Fuzzy) set(p fuzzy.Pair, value string) {
	*f.field(p) = value
	f.other = ""
	if f.extra == "" {
		return
	}
	var members map[string]json.RawMessage
	if json.Unmarshal([]byte(f.extra), &members) != nil {
		return
	}
	delete(members, p.ID())
	f.extra = ""
	if len(members) > 0 {
		if b, err := json.Marshal(members); err == nil {
			f.extra = string(b)
		}
	}
}

// hasMember：這組配對在檔案裡有本版讀不懂的值（保留在 extra），不能當成缺漏套用預設。
func (f Fuzzy) hasMember(p fuzzy.Pair) bool {
	if f.extra == "" {
		return false
	}
	var members map[string]json.RawMessage
	if json.Unmarshal([]byte(f.extra), &members) != nil {
		return false
	}
	_, ok := members[p.ID()]
	return ok
}

// hasUnknown：fuzzy 欄位含本版不認得、存檔時原樣保留的內容。
func (f Fuzzy) hasUnknown() bool { return f.extra != "" || f.other != "" }

// DefaultFuzzy 是沒有設定時各組模糊音的模式。
const DefaultFuzzy = "select"

// FuzzyConfig 轉成核心使用的設定。整個 fuzzy 欄位型別不對時全部關閉。
func (s Settings) FuzzyConfig() fuzzy.Config {
	var c fuzzy.Config
	if s.Fuzzy.other != "" {
		return c
	}
	for _, p := range fuzzy.Pairs() {
		value := *s.Fuzzy.field(p)
		if value == "" && !s.Fuzzy.hasMember(p) {
			value = DefaultFuzzy
		}
		if m, ok := fuzzy.ParseMode(value); ok {
			c = c.With(p, m)
		}
	}
	return c
}

func Default() Settings {
	s := Settings{Selection: Neural, Punctuation: false, DocumentContext: true, CandidateCursor: defaultCandidateCursor(runtime.GOOS), CandidateLayout: "vertical", EmojiShortcut: "equals", EmojiEqualsFirst: true, EscClear: true, SpaceSelects: true, KeyboardLayout: LayoutStandard, SelectionKeys: SelectDigits, CtrlPunctuation: true,
		ToggleKey: defaultToggleKey(runtime.GOOS), ModeMemory: defaultModeMemory(runtime.GOOS), StartMode: StartZhuyin}
	s.LeftShift = s.TogglesLeft() // 舊欄位跟著切換鍵：Windows 預設左右 Shift 時是 true（#206）
	return s
}

// 選字鍵（#112）。值就是依序對應第 1–10 項的鍵；不開放自訂。
const (
	SelectDigits = "1234567890"
	SelectQwer   = "1234qweras"
	SelectHome   = "asdfghjkl;"
)

// 中英切換鍵（#117）。
const (
	ToggleRight = "right"
	ToggleBoth  = "both"
	ToggleLeft  = "left"
	ToggleNone  = "none"
)

// macOS 預設沿用系統的輸入來源切換，避免誤觸 Shift 後留在英文模式；想用 Shift 的使用者可在設定開啟。
// Windows 比照系統內建注音的習慣，左右 Shift 單按都切換（#206；舊用戶的 right 由 AdoptBothShift 換一次）。
// Linux 的 Fcitx5 預設把左 Shift 當暫時切換輸入法，維持右 Shift。
func defaultToggleKey(goos string) string {
	switch goos {
	case "darwin":
		return ToggleNone
	case "windows":
		return ToggleBoth
	}
	return ToggleRight
}

// 切換視窗後的中英模式（#115）。
const (
	ModeNative = "native"
	ModeShared = "shared"
)

// 開始時的中英模式（#160）；值與引擎快照的 mode 相同。
const (
	StartZhuyin  = "zhuyin"
	StartEnglish = "english"
)

// StartsEnglish 回報開始時是否為英文模式：設定檔的 start_mode，但沒有 Shift 能切回注音時一律是注音（#184）。
func (s Settings) StartsEnglish() bool { return s.startsEnglish(runtime.GOOS) }

func (s Settings) startsEnglish(goos string) bool {
	return s.StartMode == StartEnglish && !s.startModeLocked(goos)
}

// EffectiveStartMode 是實際使用的開始模式（zhuyin／english），引擎回報給外殼與設定程式的就是它。
func (s Settings) EffectiveStartMode() string {
	if s.StartsEnglish() {
		return StartEnglish
	}
	return StartZhuyin
}

// startModeLocked：中英切換鍵設為不用 Shift、這個平台又沒有其他切回注音的方法時，開始時的模式固定是注音（#184）。
// 不然開始時英文就再也打不出注音：macOS 的 Caps Lock 短按切換的是系統輸入來源、按住只是暫時的大寫英文，
// Fcitx5 沒有切換中英的選單項目。Windows 一律可以按 Ctrl＋空白鍵或點工作列的中／英，不受限制。
// 設定檔的值不動，改回用 Shift 切換就照原本的選擇。
func (s Settings) startModeLocked(goos string) bool {
	return s.ToggleKey == ToggleNone && goos != "windows"
}

// defaultModeMemory：照各平台使用者的習慣：macOS 系統的輸入方式預設全域共用；
// Windows 的注音輸入法每個程式各自記住、Linux 的框架沒有一致的習慣，維持平台原本的做法。
func defaultModeMemory(goos string) string {
	if goos == "darwin" {
		return ModeShared
	}
	return ModeNative
}

// TogglesLeft／TogglesRight 回報單按左／右 Shift 是否切換中英。
func (s Settings) TogglesLeft() bool  { return s.ToggleKey == ToggleBoth || s.ToggleKey == ToggleLeft }
func (s Settings) TogglesRight() bool { return s.ToggleKey == ToggleBoth || s.ToggleKey == ToggleRight }

func (s *Settings) setToggleKey(v string) {
	s.ToggleKey = v
	s.LeftShift = s.TogglesLeft()
}

// defaultCandidateCursor 只由宿主決定；核心不讀取作業系統。
func defaultCandidateCursor(goos string) string {
	if goos == "darwin" {
		return "before"
	}
	return "after"
}

// UnmarshalJSON 逐欄位解讀：讀不懂的欄位退回預設，原文放進 extra 等存檔時寫回。
func (s *Settings) UnmarshalJSON(b []byte) error {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(b, &members); err != nil {
		return err
	}
	*s = Default()
	kept := map[string]json.RawMessage{}
	_, hasToggle := members["toggle_key"]
	take := func(key string) (json.RawMessage, bool) {
		raw, ok := members[key]
		delete(members, key)
		return raw, ok
	}
	if raw, ok := take("selection"); ok {
		var v string
		if json.Unmarshal(raw, &v) == nil && (v == Neural || v == Statistical) {
			s.Selection = v
		} else {
			kept["selection"] = raw
		}
	}
	for _, field := range []struct {
		key string
		dst *bool
	}{{"punctuation", &s.Punctuation}, {"document_context", &s.DocumentContext}, {"mixed_input", &s.MixedInput}, {"english_autocomplete", &s.EnglishAutocomplete}, {"left_shift", &s.LeftShift}, {"esc_clear", &s.EscClear}, {"space_selects", &s.SpaceSelects}, {"advance_after_select", &s.AdvanceAfterSelect}, {"emoji_equals_first", &s.EmojiEqualsFirst}, {"output_simplified", &s.OutputSimplified}, {"simplified_hotkey", &s.SimplifiedHotkey}, {"ctrl_punctuation", &s.CtrlPunctuation}, {"caps_lock_english", &s.CapsLockEnglish}, {"toneless", &s.Toneless}} {
		if raw, ok := take(field.key); ok {
			var v bool
			if json.Unmarshal(raw, &v) == nil {
				*field.dst = v
			} else {
				kept[field.key] = raw
			}
		}
	}
	for _, key := range retiredKeys {
		take(key) // 已拿掉的鍵：不論值為何都不保留，下次存檔就從檔案消失
	}
	if raw, ok := take("fuzzy"); ok {
		if err := json.Unmarshal(raw, &s.Fuzzy); err != nil {
			return err
		}
	}
	for _, field := range []struct {
		key    string
		dst    *string
		values []string
	}{
		{"candidate_cursor", &s.CandidateCursor, []string{"before", "after"}},
		{"candidate_layout", &s.CandidateLayout, []string{"vertical", "grid", "horizontal"}},
		{"emoji_shortcut", &s.EmojiShortcut, []string{"off", "equals"}},
		{"keyboard_layout", &s.KeyboardLayout, []string{LayoutStandard, LayoutETen, LayoutHsu}},
		{"selection_keys", &s.SelectionKeys, []string{SelectDigits, SelectQwer, SelectHome}},
		{"toggle_key", &s.ToggleKey, []string{ToggleRight, ToggleBoth, ToggleLeft, ToggleNone}},
		{"mode_memory", &s.ModeMemory, []string{ModeNative, ModeShared}},
		{"start_mode", &s.StartMode, []string{StartZhuyin, StartEnglish}},
	} {
		if raw, ok := take(field.key); ok {
			var value string
			if json.Unmarshal(raw, &value) == nil && slices.Contains(field.values, value) {
				*field.dst = value
			} else {
				kept[field.key] = raw
			}
		}
	}
	// 舊檔只有 left_shift：推出切換鍵。新檔以 toggle_key 為準，left_shift 跟著它（舊版寫的 left_shift 不蓋過它）。
	if !hasToggle && s.LeftShift {
		s.ToggleKey = ToggleBoth
	}
	s.LeftShift = s.TogglesLeft()
	if raw, ok := take("schema"); ok {
		var n int
		if json.Unmarshal(raw, &n) != nil || n > SchemaVersion {
			s.readOnly = true // 比本版新，或本版讀不懂的格式標記：不覆寫
		}
	}
	if raw, ok := take("input_profile"); ok {
		s.loadProfiles(raw)
	}
	for key, raw := range members {
		kept[key] = raw
	}
	if len(kept) > 0 {
		extra, err := json.Marshal(kept)
		if err != nil {
			return err
		}
		s.extra = string(extra)
	}
	return nil
}

// MarshalJSON 依固定順序寫出已知欄位，其後接保留的原文成員。
func (s Settings) MarshalJSON() ([]byte, error) {
	kept := map[string]json.RawMessage{}
	if s.extra != "" {
		if err := json.Unmarshal([]byte(s.extra), &kept); err != nil {
			return nil, err
		}
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	put := func(key string, raw []byte) {
		if buf.Len() > 1 {
			buf.WriteByte(',')
		}
		k, _ := json.Marshal(key)
		buf.Write(k)
		buf.WriteByte(':')
		buf.Write(raw)
	}
	typed := func(key string, value any) error {
		if raw, ok := kept[key]; ok { // 本版讀不懂的值：原文寫回，直到使用者明確改它
			delete(kept, key)
			put(key, raw)
			return nil
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		put(key, raw)
		return nil
	}
	put("schema", []byte(fmt.Sprint(SchemaVersion)))
	for _, field := range []struct {
		key   string
		value any
	}{{"selection", s.Selection}, {"punctuation", s.Punctuation}, {"document_context", s.DocumentContext}, {"mixed_input", s.MixedInput}, {"english_autocomplete", s.EnglishAutocomplete}, {"left_shift", s.LeftShift}, {"esc_clear", s.EscClear}, {"space_selects", s.SpaceSelects}, {"candidate_cursor", s.CandidateCursor}, {"advance_after_select", s.AdvanceAfterSelect}, {"candidate_layout", s.CandidateLayout}, {"emoji_shortcut", s.EmojiShortcut}, {"emoji_equals_first", s.EmojiEqualsFirst}, {"keyboard_layout", s.KeyboardLayout}, {"selection_keys", s.SelectionKeys}, {"toggle_key", s.ToggleKey}, {"mode_memory", s.ModeMemory}, {"start_mode", s.StartMode}, {"output_simplified", s.OutputSimplified}, {"simplified_hotkey", s.SimplifiedHotkey}, {"ctrl_punctuation", s.CtrlPunctuation}, {"caps_lock_english", s.CapsLockEnglish}, {"toneless", s.Toneless}} {
		if err := typed(field.key, field.value); err != nil {
			return nil, err
		}
	}
	if s.profiles != "" {
		put("input_profile", []byte(s.profiles))
	}
	if s.Fuzzy != (Fuzzy{}) {
		raw, err := json.Marshal(s.Fuzzy)
		if err != nil {
			return nil, err
		}
		put("fuzzy", raw)
	}
	names := make([]string, 0, len(kept))
	for key := range kept {
		names = append(names, key)
	}
	sort.Strings(names)
	for _, key := range names {
		put(key, kept[key])
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// forget 丟掉某個欄位保留的原文：使用者明確設定它之後，不再寫回舊值。
func (s *Settings) forget(key string) {
	if s.extra == "" {
		return
	}
	var kept map[string]json.RawMessage
	if json.Unmarshal([]byte(s.extra), &kept) != nil {
		return
	}
	delete(kept, key)
	s.extra = ""
	if len(kept) > 0 {
		if b, err := json.Marshal(kept); err == nil {
			s.extra = string(b)
		}
	}
}

// ReadOnly 回報檔案是否來自較新版本：是的話 Save 會拒絕。
func (s Settings) ReadOnly() bool { return s.readOnly }

// notes 說明讀取時的退回與保留，給人讀（settings --json 的 notes）。
func (s Settings) notes() []string {
	var out []string
	var kept map[string]json.RawMessage
	if s.extra != "" {
		_ = json.Unmarshal([]byte(s.extra), &kept)
	}
	for _, key := range []string{"selection", "punctuation", "document_context", "mixed_input", "english_autocomplete", "left_shift", "esc_clear", "space_selects", "candidate_cursor", "advance_after_select", "candidate_layout", "emoji_shortcut", "emoji_equals_first", "keyboard_layout", "selection_keys", "toggle_key", "mode_memory", "start_mode", "output_simplified", "simplified_hotkey", "ctrl_punctuation", "caps_lock_english", "toneless"} {
		if raw, ok := kept[key]; ok {
			out = append(out, fmt.Sprintf("%s：本版不認得 %s，暫用預設值，存檔時保留原值", strings.ReplaceAll(key, "_", "-"), raw))
			delete(kept, key)
		}
	}
	if len(kept) > 0 {
		names := make([]string, 0, len(kept))
		for key := range kept {
			names = append(names, key)
		}
		sort.Strings(names)
		out = append(out, "含本版不認得的欄位："+strings.Join(names, "、")+"；存檔時原樣保留")
	}
	if s.Fuzzy.hasUnknown() {
		out = append(out, "fuzzy：含本版不認得的成員，存檔時原樣保留")
	}
	if s.readOnly {
		out = append(out, ErrReadOnly.Error())
	}
	return out
}

// Report 是讀取設定檔的完整結果：除了設定值，還有檔案狀態與讀取時的退回說明。
type Report struct {
	Path     string // 實際讀取的路徑；新舊檔都不存在時為指定的新路徑
	Settings Settings
	State    string   // StateOK、StateMissing、StateCorrupt、StateUnreadable
	Schema   int      // 檔案中的 schema；沒有時為 0
	ReadOnly bool     // 檔案來自較新版本：Save 會拒絕
	Notes    []string // 讀不懂而退回預設、或原樣保留的欄位
}

// readFile 區分真正缺檔與已存在但目標遺失的符號連結；後者不可啟用舊檔退回。
func readFile(path string) (b []byte, missing bool, err error) {
	b, err = os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		_, statErr := os.Lstat(path)
		if errors.Is(statErr, fs.ErrNotExist) {
			return nil, true, nil
		}
		if statErr != nil {
			return nil, false, statErr
		}
	}
	return b, false, err
}

// Read 讀取設定。Linux 標準新路徑不存在時才讀舊路徑；兩者都不存在回傳預設值。
// 個別欄位讀不懂只有那個欄位退回預設，不算錯誤。
// 只有檔案讀不到或根本不是 JSON 物件才回錯誤（同時回傳預設值與對應的 State）。
func Read(path string) (Report, error) {
	b, missing, err := readFile(path)
	if missing {
		if legacy := legacyPath(path); legacy != "" {
			old, oldMissing, oldErr := readFile(legacy)
			if !oldMissing {
				b, path, missing, err = old, legacy, false, oldErr
			}
		}
	}
	if missing {
		return Report{Path: path, Settings: freshSettings(), State: StateMissing}, nil
	}
	if err != nil {
		return Report{Path: path, Settings: Default(), State: StateUnreadable}, err
	}
	var s Settings
	if err = json.Unmarshal(b, &s); err != nil {
		return Report{Path: path, Settings: Default(), State: StateCorrupt}, fmt.Errorf("%s：%w：%v", path, ErrCorrupt, err)
	}
	r := Report{Path: path, Settings: s, State: StateOK, ReadOnly: s.readOnly, Notes: s.notes()}
	var head struct {
		Schema int `json:"schema"`
	}
	_ = json.Unmarshal(b, &head) // schema 不是整數時保持 0；ReadOnly 已另外標記
	r.Schema = head.Schema
	return r, nil
}

// Load 讀取設定；檔案不存在時回傳預設值。讀不懂的個別欄位已退回預設，不算錯誤；
// 檔案不是可解析的 JSON 物件時回傳預設值與錯誤，呼叫端應回退預設值並記錄。
func Load(path string) (Settings, error) {
	r, err := Read(path)
	return r.Settings, err
}

// KeepToggleKey 在設定檔沒有記下中英切換鍵時寫入 value，回報是否寫入（#167：升級前的平台預設和現在不同，
// 升級的人保留原本的行為）。已有 toggle_key、舊版的 left_shift=true（已推出 both）、來自較新版本或無法解析的檔案都不動；
// 寫過一次之後檔案就有 toggle_key，使用者之後自己改的值不會被蓋回去。
// 只加這一個欄位：其他欄位與未知欄位逐字保留，不把其餘的預設值寫成使用者的選擇；缺檔時只寫 schema 與 toggle_key。
func KeepToggleKey(path, value string) (bool, error) {
	if !slices.Contains([]string{ToggleRight, ToggleBoth, ToggleLeft, ToggleNone}, value) {
		return false, &SetError{Code: CodeInvalidValue, Msg: "不認得的切換鍵：" + value}
	}
	b, missing, err := readFile(path)
	if err != nil {
		return false, err
	}
	if missing && legacyPath(path) != "" {
		if _, oldMissing, _ := readFile(legacyPath(path)); !oldMissing {
			return false, nil // 讀的是舊路徑的設定：不在新路徑另寫一份把它蓋掉
		}
	}
	entry, _ := json.Marshal(value)
	var out []byte
	if missing {
		out = []byte(fmt.Sprintf("{\n  \"schema\": %d,\n  \"toggle_key\": %s\n}\n", SchemaVersion, entry))
	} else {
		var members map[string]json.RawMessage
		if err = json.Unmarshal(b, &members); err != nil || members == nil {
			return false, fmt.Errorf("%s：%w", path, ErrCorrupt)
		}
		var s Settings
		if err = json.Unmarshal(b, &s); err != nil {
			return false, fmt.Errorf("%s：%w：%v", path, ErrCorrupt, err)
		}
		// 舊版的 left_shift=true 已推出 both：看檔案裡的原值，不看由切換鍵推出的 s.LeftShift（平台預設是左右 Shift 時也是 true）。
		if _, ok := members["toggle_key"]; ok || s.readOnly || string(bytes.TrimSpace(members["left_shift"])) == "true" {
			return false, nil
		}
		body := bytes.TrimRight(b, " \t\r\n")
		if len(body) == 0 || body[len(body)-1] != '}' {
			return false, fmt.Errorf("%s：%w", path, ErrCorrupt)
		}
		sep := ","
		if len(members) == 0 {
			sep = ""
		}
		out = append(append([]byte{}, body[:len(body)-1]...), []byte(sep+"\n  \"toggle_key\": "+string(entry)+"\n}\n")...)
		// 逐字插入前一個成員之後：前面是空白或換行時照樣是合法 JSON，寫入前再核對一次。
		var check map[string]json.RawMessage
		if json.Unmarshal(out, &check) != nil || len(check) != len(members)+1 {
			return false, fmt.Errorf("%s：%w", path, ErrCorrupt)
		}
	}
	return true, writeAtomic(path, out)
}

// Save 先寫暫存檔再改名，中斷時保留原設定。讀到的未知欄位與讀不懂的值原樣寫回；
// 來自較新版本的檔案回傳 ErrReadOnly，不覆寫。
func Save(path string, s Settings) error {
	if s.readOnly {
		return ErrReadOnly
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(b, '\n'))
}

func writeAtomic(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".settings-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// Quarantine 把無法解析的設定檔改名保留（settings.json.corrupt-時間），回傳新路徑；
// 之後 Read 會視為檔案不存在，使用者可以從預設值重新設定而不遺失原檔。
func Quarantine(path string, now time.Time) (string, error) {
	dst := path + ".corrupt-" + now.UTC().Format("20060102T150405Z")
	return dst, os.Rename(path, dst)
}

// SetError 是 Set 的錯誤：Code 供機器判斷（CodeUnknownKey、CodeInvalidValue），Msg 給人讀。
type SetError struct {
	Code string
	Key  string
	Msg  string
}

func (e *SetError) Error() string { return e.Msg }

type spec struct {
	key, label, description, def string
	values                       []string
	value                        func(Settings) string
}

// valueLabels 是選項的顯示名稱（Item.ValueLabels）；設定程式沒有時用自己的對照表。
func (s Settings) valueLabels(key string) map[string]string {
	switch key {
	case "toggle-key":
		return map[string]string{ToggleRight: "右 Shift", ToggleBoth: "左右 Shift", ToggleLeft: "左 Shift", ToggleNone: "不用 Shift"}
	case "start-mode":
		return map[string]string{StartZhuyin: "注音", StartEnglish: "英文"}
	case "selection-keys":
		return map[string]string{SelectDigits: "1 2 3 4 5 6 7 8 9 0", SelectQwer: "1 2 3 4 q w e r a s", SelectHome: "a s d f g h j k l ;"}
	case "mode-memory":
		return map[string]string{ModeNative: modeNativeLabel(runtime.GOOS, s.StartsEnglish()), ModeShared: "全部共用同一個模式"}
	case "keyboard-layout":
		return map[string]string{LayoutStandard: "標準", LayoutETen: "倚天 41 鍵", LayoutHsu: "許氏"}
	}
	return nil
}

// disabledReason 回報某一項目前是否因其他設定而不起作用，以及給人讀的原因；空字串表示可用。
// 中英混打與 = 開 Emoji 選單依標準排列設計，倚天 41 鍵時停用（#83）；許氏的聲調在字母鍵上，也停用混打，
// = 不是注音鍵，Emoji 選單照舊（#183）。已儲存的值不動，切回標準排列就照舊。
func (s Settings) disabledReason(key string) string {
	return s.disabledReasonOn(runtime.GOOS, key)
}

func (s Settings) disabledReasonOn(goos, key string) string {
	if s.styleExtensionDisabled(key) {
		return styleExtensionReason
	}
	switch key {
	case "mixed-input", "emoji-shortcut", "emoji-equals-first":
		if s.KeyboardLayout == LayoutETen {
			return "倚天 41 鍵時關閉"
		}
		if s.KeyboardLayout == LayoutHsu && key == "mixed-input" {
			return "許氏排列時關閉"
		}
		if key == "mixed-input" && s.Toneless {
			return "不打聲調時關閉" // 聲調選填與混打二選一（#150）
		}
	case "toneless":
		// 只在標準排列生效（#150）：倚天 41 鍵的 ' 是 ㄘ、沒有分隔鍵，許氏一個鍵兼聲母、韻母與聲調。
		switch s.KeyboardLayout {
		case LayoutETen:
			return "倚天 41 鍵時關閉"
		case LayoutHsu:
			return "許氏排列時關閉"
		}
	case "start-mode":
		if s.startModeLocked(goos) {
			return startModeLockedReason
		}
	}
	return ""
}

// startModeLockedReason：不用 Shift 切換時沒有鍵能從英文切回注音，所以固定從注音開始（#184）。
const startModeLockedReason = "不用 Shift 切換時固定從注音開始"

// disabledBy 回報停用原因來自哪一項設定（Item.DisabledBy）：設定程式把各項分在不同頁面（#207），
// 原因那一項在別頁時，用它帶人過去改。新增停用原因時要一起加在這裡（TestDisabledItemsNameTheirCause）。
func disabledBy(reason string) string {
	switch reason {
	case styleExtensionReason:
		return "style-punctuation"
	case "倚天 41 鍵時關閉", "許氏排列時關閉":
		return "keyboard-layout"
	case startModeLockedReason:
		return "toggle-key"
	case "不打聲調時關閉": // 中英混打（#150）
		return "toneless"
	}
	return ""
}

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

func specs() []spec {
	out := []spec{
		{"selection", "選字方式", "神經選字會依前文重新排序候選，需要載入模型；只用統計引擎則完全依詞頻與個人詞庫，不載入模型", Neural, []string{Neural, Statistical},
			func(s Settings) string { return s.Selection }},
		{"punctuation", "提交時補標點（Beta）", "提交時依句意補上標點，可能漏補或錯插；預設關閉，可自行開啟試用", "off", []string{"on", "off"},
			func(s Settings) string { return onOff(s.Punctuation) }},
		{"document-context", "依前文選字", "依自己剛提交的文字選字", "on", []string{"on", "off"},
			func(s Settings) string { return onOff(s.DocumentContext) }},
		{"mixed-input", "中英混打（試用）", "直接在注音中打英文；提供英文補全與拼字修正，由你確認接受。開啟時空白鍵輸入空白，按 ↓ 選字；數字拼得成注音時，按 Enter 留數字。若已啟用 Shift 中英切換，混打開或關都能單按 Shift 切到英文", "off", []string{"on", "off"},
			func(s Settings) string { return onOff(s.MixedInput) }},
		{"english-autocomplete", "英文模式自動補字", "單按 Shift 切換到英文模式後，輸入至少兩個字母時顯示補全候選，三個字母起也提供拼字修正；按 Tab 補全或修正拼字、Esc 關閉。與中英混打分開設定，空白與 Enter 保留原操作", "off", []string{"on", "off"},
			func(s Settings) string { return onOff(s.EnglishAutocomplete) }},
		{"toggle-key", "中英切換鍵", toggleKeyDescription(runtime.GOOS), Default().ToggleKey, []string{ToggleRight, ToggleBoth, ToggleLeft, ToggleNone},
			func(s Settings) string { return s.ToggleKey }},
		{"start-mode", "開始時的中英模式", startModeDescription(runtime.GOOS), StartZhuyin, []string{StartZhuyin, StartEnglish},
			func(s Settings) string { return s.StartMode }},
		{"mode-memory", "切換視窗後的中英模式", "換到別的程式或輸入框時，中英模式要不要跟著", Default().ModeMemory, []string{ModeNative, ModeShared},
			func(s Settings) string { return s.ModeMemory }},
		{"esc-clear", "按 Esc 清除整段組字", "關閉時 Esc 只清除還沒打完的注音，已組好的字保留", "on", []string{"on", "off"},
			func(s Settings) string { return onOff(s.EscClear) }},
		{"space-selects", "空白鍵選字", "關閉後，注音打完按空白會輸入空格，改用 ↓ 選字", "on", []string{"on", "off"},
			func(s Settings) string { return onOff(s.SpaceSelects) }},
		{"candidate-cursor", "修改字詞的位置", "按 ↓（開啟空白鍵選字、沒開中英混打時也可按空白鍵）開候選時，修改游標前方或後方的字詞；橫排時前方是左側，後方是右側。游標在句尾時後方沒有字，一律修改前方的詞", Default().CandidateCursor, []string{"before", "after"}, func(s Settings) string { return s.CandidateCursor }},
		{"advance-after-select", "選字後游標移到下一個字", "從候選窗選好字後，游標跳到所選的詞後面，可以接著按 ↓ 改下一個字；修改游標前方字詞時往後移一個字。游標在句尾時不動", "off", []string{"on", "off"},
			func(s Settings) string { return onOff(s.AdvanceAfterSelect) }},
		{"candidate-layout", "選字排列", "直排，或每排五個的多排選字（選字鍵 1234qweras 時每排四個）；Linux 原生候選窗依桌面介面呈現", "vertical", []string{"vertical", "grid", "horizontal"}, func(s Settings) string { return s.CandidateLayout }},
		{"output-simplified", "輸出簡體字", "組字和候選仍顯示繁體，送出時才轉成簡體字；我的詞彙與我的片語也一起轉，英文、數字與標點不變", "off", []string{"on", "off"},
			func(s Settings) string { return onOff(s.OutputSimplified) }},
		{"simplified-hotkey", "用快捷鍵切換簡體字", simplifiedHotkeyDescription(runtime.GOOS), "off", []string{"on", "off"},
			func(s Settings) string { return onOff(s.SimplifiedHotkey) }},
		{"emoji-shortcut", "Emoji 選單", "在注音模式按 = 開啟選單；Tab 切換顏文字，選過的排前面", "equals", []string{"off", "equals"}, func(s Settings) string { return s.EmojiShortcut }},
		{"emoji-equals-first", "將 = 放在第一項", "關閉後，按兩次 == 可輸入一個 =", "on", []string{"on", "off"}, func(s Settings) string { return onOff(s.EmojiEqualsFirst) }},
		{"keyboard-layout", "鍵盤排列", "注音鍵盤排列。倚天 41 鍵的 = 是 ㄦ、' 是 ㄘ，頓號改按 \\，這時不提供 Emoji 選單與中英混打。許氏只用字母鍵：同一鍵依位置代表聲母或韻母，聲調是 d f j s、一聲按空白，, . ; 直接打「，。；」，這時不提供中英混打", LayoutStandard, []string{LayoutStandard, LayoutETen, LayoutHsu},
			func(s Settings) string { return s.KeyboardLayout }},
		{"selection-keys", "選字鍵", "候選窗用哪一排鍵選第 1 到第 10 個字；換成字母鍵時，數字鍵仍可照位置選字", SelectDigits, []string{SelectDigits, SelectQwer, SelectHome},
			func(s Settings) string { return s.SelectionKeys }},
		{"ctrl-punctuation", "Ctrl＋標點打全形", ctrlPunctuationDescription(runtime.GOOS), "on", []string{"on", "off"},
			func(s Settings) string { return onOff(s.CtrlPunctuation) }},
	}
	// Caps Lock 開著時直接打英文（#173）：只有 Windows、Linux。macOS 開 Caps Lock 一律切到英文大寫（#84），沒有這一項。
	if CapsLockEnglishSetting(runtime.GOOS) {
		out = append(out, spec{"caps-lock-english", "Caps Lock 開著時直接打英文",
			"開著 Caps Lock 時暫時切到英文：字母大寫、數字和符號半形，正在打的字先送出；關掉 Caps Lock 回到注音。關閉時注音模式不理 Caps Lock，照打注音",
			"off", []string{"on", "off"}, func(s Settings) string { return onOff(s.CapsLockEnglish) }})
	}
	out = append(out, spec{"toneless", "不打聲調也能選字", tonelessDescription, "off", []string{"on", "off"},
		func(s Settings) string { return onOff(s.Toneless) }})
	for _, p := range fuzzy.Pairs() {
		out = append(out, spec{"fuzzy-" + p.ID(), "模糊音 " + p.Symbols(), "把近音的字也列進候選，原音仍排在前面；「自動修正」（實驗）會在上下文明顯支持近音時另外自動改字", DefaultFuzzy, []string{"off", "select", "auto"},
			func(s Settings) string { return s.FuzzyConfig().Mode(p).String() }})
	}
	for _, r := range ruleSpecs() {
		r := r
		out = append(out, spec{r.key, r.label, r.description, r.get(inputstyleDefaultRules()), r.values, func(s Settings) string { return r.get(s.ProfileRules()) }})
	}
	return out
}

// tonelessDescription 是「不打聲調也能選字」（#150）的說明：怎麼切音節、空白還是一聲、混打停用。
const tonelessDescription = "聲調可打可不打：ㄋㄧㄏㄠ 就是「你好」，沒打聲調的字依前後文決定，有打聲調的字照聲調；少打聲調時選字比較容易錯，拿不定的字可以補聲調。" +
	"接不上前一個音節的注音會開始新的字；ㄒㄧㄢ 是一個字，要「西安」請打 ㄒㄧ'ㄢ（組字中的 ' 用來切字，頓號改按 \\）或在 ㄒㄧ 後面補聲調。" +
	"沒打完的注音按空白仍是一聲，送出請按 Enter。開啟時中英混打停用，英文請用 Shift 切換，「我的詞彙」裡的英文詞照樣認得。只在標準排列有效"

// CapsLockEnglishSetting 回報這個平台有沒有「Caps Lock 開著時直接打英文」（#173）：Windows、Linux 有；
// macOS 開 Caps Lock 一律切到英文大寫（#84、#67），沒有這一項，設定檔裡的值也不用。
func CapsLockEnglishSetting(goos string) bool { return goos != "darwin" }

// SimplifiedHotkeyLabel 是切換「輸出簡體字」的快捷鍵（#192），設定說明與 0c-update 的文字用它；四個外殼各自比對同一組鍵。
// 負責人在 Ctrl＋Shift＋F1～F12 裡選定 F11（#192）：兩個相鄰修飾鍵；撞到的只有冷門功能（Word 解除功能變數鎖定、
// JetBrains 的書籤清單、Konsole 切換分割），瀏覽器、VS Code、Windows、GNOME 都沒有用。Mac 一樣用 ⌃⇧F11：
// 單按 F11 是顯示桌面，加了 ⌃⇧ 就不是；F 鍵預設是媒體鍵的鍵盤要加按 fn。
func SimplifiedHotkeyLabel(goos string) string {
	if goos == "darwin" {
		return "⌃⇧F11"
	}
	return "Ctrl＋Shift＋F11"
}

// simplifiedHotkeyDescription：說明快捷鍵、會占用哪個鍵、組字中按的效果（#192）。
func simplifiedHotkeyDescription(goos string) string {
	d := "開啟後按 " + SimplifiedHotkeyLabel(goos) + " 切換輸出簡體字／繁體字，正在組字的字送出時也照新的設定"
	if goos == "darwin" {
		return d + "。F11 是音量鍵的鍵盤要加按 fn；開啟時這個鍵不再交給 App"
	}
	return d + "。筆電的 F11 是媒體鍵時要加按 Fn；開啟時這個鍵不再交給應用程式"
}

// toggleKeyDescription：Windows 另外提醒 Ctrl＋空白鍵。那是繁中輸入法的系統熱鍵，外殼接到就切換（#117），不受這一項影響。
func toggleKeyDescription(goos string) string {
	d := "單按哪個 Shift 切換注音／英文；按住 Shift 超過 0.3 秒、或 Shift 加字母（打大寫）不算切換。沒用來切換的 Shift 照系統原本的用途"
	if goos == "windows" {
		d += "。預設左右 Shift 都可以切換，不想讓左 Shift 切換可改成「右 Shift」；按著 Shift 點滑鼠選取也不算切換。也可以按 Ctrl＋空白鍵"
	} else if goos == "darwin" {
		d += "。Mac 預設不用 Shift，使用系統的輸入來源切換；想用 Shift 可在這裡開啟"
	}
	return d
}

// ctrlPunctuationDescription：說明會占用哪些程式快速鍵、可以關掉（#168）。Mac 是 Control 鍵（⌃），程式快速鍵多用 ⌘，
// 撞到的少，不舉 VS Code 的例子。
func ctrlPunctuationDescription(goos string) string {
	if goos == "darwin" {
		return "注音模式按 ⌃, 打「，」、⌃. 打「。」、⌃' 打「、」、⌃; 打「；」、⌃⇧/ 打「？」、⌃⇧1 打「！」，沒在打字時也可以。" +
			"注音模式時這幾個鍵不再交給 App，要保留時可以關掉；英文模式不受影響"
	}
	d := "注音模式按 Ctrl＋, 打「，」、Ctrl＋. 打「。」、Ctrl＋' 打「、」、Ctrl＋; 打「；」、Ctrl＋Shift＋/ 打「？」、Ctrl＋Shift＋1 打「！」，沒在打字時也可以。" +
		"會占用程式的這幾個快速鍵（例如 VS Code 的 Ctrl＋, 開設定、Ctrl＋. 快速修正），要保留時可以關掉；英文模式不受影響"
	if goos == "linux" {
		d += "。Fcitx5 的剪貼簿開著時，Ctrl＋; 照舊叫出剪貼簿"
	}
	return d
}

// startModeDescription：沒有記住的模式時從哪一個開始（#160）。Windows 的 native 是每個程式各自記住，所以是新開的程式；
// macOS／Linux 的 native 是每次回到它。全部共用時只有登入後（引擎啟動時）的第一個模式。
func startModeDescription(goos string) string {
	d := "換到輸入框時回到這個模式"
	if goos == "windows" {
		d = "新開的程式從這個模式開始"
	}
	return d + "；切換視窗後「全部共用同一個模式」時，是登入後一開始的模式"
}

// modeNativeLabel：native 照平台原本的做法，意思依平台而不同；macOS／Linux 回到開始時的模式（#160）。
func modeNativeLabel(goos string, english bool) string {
	if goos == "windows" {
		return "每個程式各自記住"
	}
	if english {
		return "每次回到英文"
	}
	return "每次回到注音"
}

// retiredKeys 是已拿掉的設定檔欄位：讀檔時忽略，存檔時不寫回。
var retiredKeys = []string{"preview_0_2"}

// PreviewRetiredKey 是 0.2.0 開發期間的內部開關（#128）。0.2.0 起功能正式開放：set preview-0-2 on 仍然成功
// 但不改任何東西（保留舊版呼叫相容性），off 回 CodeInvalidValue，說明已不能關閉。
const PreviewRetiredKey = "preview-0-2"

// PreviewRetiredNote 是 set preview-0-2 on 時給人讀的提示。
const PreviewRetiredNote = "preview-0-2 已不需要設定：0.2.0 的我的詞彙、加詞按鍵、我的片語與本機 API 已正式開放"

// hiddenSpecs 是可以用 Set 修改、但不列進 Items 與 Keys 的項目；設定程式看不到。
func hiddenSpecs() []spec {
	return []spec{
		// 舊鍵：0.1.0-dev.63 以前的設定程式與腳本用它；改成 toggle-key。
		{"left-shift", "左 Shift 切換中英", "舊設定，改用 toggle-key", onOff(Default().TogglesLeft()), []string{"on", "off"},
			func(s Settings) string { return onOff(s.TogglesLeft()) }},
		{PreviewRetiredKey, "0.2.0 預覽", "已正式開放，不再需要設定", "on", []string{"on"},
			func(Settings) string { return "on" }},
	}
}

func lookup(key string) (spec, bool) {
	for _, sp := range append(specs(), hiddenSpecs()...) {
		if sp.key == key {
			return sp, true
		}
	}
	return spec{}, false
}

// Keys 列出可用 Set 修改的項目與允許值，供命令列說明使用。
func Keys() map[string]string {
	keys := map[string]string{}
	for _, sp := range specs() {
		keys[sp.key] = strings.Join(sp.values, "|")
	}
	return keys
}

// Item 描述一個可修改的設定及其目前的值，是 settings --json 契約的一項。
type Item struct {
	Locked       bool     `json:"locked"`
	LockedReason string   `json:"locked_reason,omitempty"`
	Key          string   `json:"key"`
	Type         string   `json:"type"` // 目前都是 choice：從 Values 擇一
	Values       []string `json:"values"`
	Default      string   `json:"default"`
	Value        string   `json:"value"` // 設定檔中的值（讀不懂的值顯示為預設值）
	Label        string   `json:"label"`
	Description  string   `json:"description"`
	// Disabled：這一項目前因其他設定而不起作用（例如倚天 41 鍵時的中英混打），介面顯示為不能改，
	// 旁邊顯示 DisabledReason。Value 仍是設定檔中的值，條件解除後照舊生效。舊版介面忽略這兩個欄位。
	Disabled       bool   `json:"disabled,omitempty"`
	DisabledReason string `json:"disabled_reason,omitempty"`
	// DisabledBy：造成停用的那一項的 key（例如許氏時的中英混打是 keyboard-layout）；舊版介面忽略。
	DisabledBy string `json:"disabled_by,omitempty"`
	// Effective：停用期間實際使用、與 Value 不同的值（不用 Shift 時的開始模式固定為注音，#184）；
	// 介面照它顯示選項，0c-update 拿它跟引擎載入的值比較。沒有時就是 Value。舊版介面忽略。
	Effective string `json:"effective,omitempty"`
	// ValueLabels 是選項的顯示名稱（例如 native 在 Windows 是「每個程式各自記住」）；舊版介面忽略，照自己的對照表。
	ValueLabels map[string]string `json:"value_labels,omitempty"`
}

// Items 依固定順序列出所有可修改的設定。
func (s Settings) Items() []Item {
	list := specs()
	out := make([]Item, 0, len(list))
	for _, sp := range list {
		reason := s.disabledReason(sp.key)
		value, effective := sp.value(s), ""
		if sp.key == "start-mode" && reason != "" && value != StartZhuyin {
			effective = StartZhuyin
		}
		out = append(out, Item{Key: sp.key, Type: "choice", Values: sp.values, Default: sp.def, Value: value, Label: sp.label, Description: sp.description,
			Disabled: reason != "", DisabledReason: reason, DisabledBy: disabledBy(reason), Effective: effective, ValueLabels: s.valueLabels(sp.key)})
	}
	for i := range out {
		if sp, ok := lookupRule(out[i].Key); ok {
			out[i].ValueLabels = sp.labels
		}
		if out[i].Key == "candidate-layout" {
			out[i].ValueLabels = map[string]string{"vertical": "直排", "grid": "多排", "horizontal": "橫排"}
		}
		if s.profileLocked(out[i].Key) {
			out[i].Locked = true
			out[i].LockedReason = "此項由選用的輸入習慣固定；請先複製成自訂"
		}
	}
	return out
}

// Value 回傳某個設定目前的值（命令列的寫法）。
func (s Settings) Value(key string) (string, bool) {
	sp, ok := lookup(key)
	if !ok {
		return "", false
	}
	return sp.value(s), true
}

// Set 依命令列的鍵與值修改設定；錯誤為 *SetError。明確設定某個欄位會丟掉先前為它保留的原文。
func (s *Settings) Set(key, value string) error {
	next := *s
	if err := next.set(key, value); err != nil {
		return err
	}
	if err := next.validateProfileCombination(); err != nil {
		return err
	}
	*s = next
	return nil
}

func (s *Settings) set(key, value string) error {
	if s.ReadOnly() {
		return ErrReadOnly
	}
	if s.profileLocked(key) {
		return &SetError{Code: "locked_by_profile", Key: key, Msg: "此項由選用的輸入習慣固定；請先複製成自訂"}
	}
	sp, ok := lookup(key)
	if !ok {
		names := make([]string, 0, len(specs()))
		for _, other := range specs() {
			names = append(names, other.key)
		}
		sort.Strings(names)
		return &SetError{Code: CodeUnknownKey, Key: key, Msg: fmt.Sprintf("未知的設定 %q；可用：%s", key, strings.Join(names, "、"))}
	}
	valid := false
	for _, v := range sp.values {
		valid = valid || v == value
	}
	if !valid && key == PreviewRetiredKey {
		return &SetError{Code: CodeInvalidValue, Key: key, Msg: "0.2.0 的我的詞彙、加詞按鍵、我的片語與本機 API 已正式開放，preview-0-2 不能再關閉；" +
			"不想讓 AI 助手連線，在設定程式關閉「讓 AI 幫你加詞」或執行 0c-update api disable（預設就是關閉）"}
	}
	if !valid {
		return &SetError{Code: CodeInvalidValue, Key: key, Msg: fmt.Sprintf("%s 只接受 %s", key, strings.Join(sp.values, " 或 "))}
	}
	if rule, ok := lookupRule(key); ok {
		return s.setRule(rule, value)
	}
	switch key {
	case "selection":
		s.Selection = value
		s.forget("selection")
	case "punctuation":
		s.Punctuation = value == "on"
		s.forget("punctuation")
	case "document-context":
		s.DocumentContext = value == "on"
		s.forget("document_context")
	case "english-autocomplete":
		s.EnglishAutocomplete = value == "on"
		s.forget("english_autocomplete")
	case "left-shift": // 舊鍵：左 Shift 開關，右 Shift 維持原狀
		right := s.TogglesRight()
		switch {
		case value == "on" && right:
			s.setToggleKey(ToggleBoth)
		case value == "on":
			s.setToggleKey(ToggleLeft)
		case right:
			s.setToggleKey(ToggleRight)
		default:
			s.setToggleKey(ToggleNone)
		}
		s.forget("left_shift")
		s.forget("toggle_key")
	case "toggle-key":
		s.setToggleKey(value)
		s.forget("toggle_key")
		s.forget("left_shift")
	case "mode-memory":
		s.ModeMemory = value
		s.forget("mode_memory")
	case "start-mode":
		s.StartMode = value
		s.forget("start_mode")
	case "esc-clear":
		s.EscClear = value == "on"
		s.forget("esc_clear")
	case "space-selects":
		s.SpaceSelects = value == "on"
		s.forget("space_selects")
	case "advance-after-select":
		s.AdvanceAfterSelect = value == "on"
		s.forget("advance_after_select")
	case "output-simplified":
		s.OutputSimplified = value == "on"
		s.forget("output_simplified")
	case "simplified-hotkey":
		s.SimplifiedHotkey = value == "on"
		s.forget("simplified_hotkey")
	case "ctrl-punctuation":
		s.CtrlPunctuation = value == "on"
		s.forget("ctrl_punctuation")
	case "caps-lock-english":
		s.CapsLockEnglish = value == "on"
		s.forget("caps_lock_english")
	case "toneless":
		s.Toneless = value == "on"
		s.forget("toneless")
	case "mixed-input":
		s.MixedInput = value == "on"
		s.forget("mixed_input")
	case "candidate-cursor":
		s.CandidateCursor = value
		s.forget("candidate_cursor")
	case "candidate-layout":
		s.CandidateLayout = value
		s.forget("candidate_layout")
	case "emoji-shortcut":
		s.EmojiShortcut = value
		s.forget("emoji_shortcut")
	case "emoji-equals-first":
		s.EmojiEqualsFirst = value == "on"
		s.forget("emoji_equals_first")
	case "keyboard-layout":
		next := *s
		next.KeyboardLayout = value
		if available, reason := next.profileAvailability(s.ActiveProfile()); !available {
			return profileError(reason)
		}
		s.KeyboardLayout = value
		s.forget("keyboard_layout")
	case "selection-keys":
		s.SelectionKeys = value
		s.forget("selection_keys")
	case PreviewRetiredKey: // 已正式開放：不改任何東西
		return nil
	default:
		p, _ := fuzzy.ParsePair(strings.TrimPrefix(key, "fuzzy-"))
		if value == DefaultFuzzy {
			value = "" // 回到預設：不在設定檔留下欄位；off 要明確寫入
		}
		s.Fuzzy.set(p, value)
	}
	s.syncProfile()
	return nil
}
