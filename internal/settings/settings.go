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
	"strconv"
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
	LayoutPinyin   = "pinyin"   // 漢語拼音（#324）
	LayoutXiaohe   = "xiaohe"   // 小鶴雙拼
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
	// ShiftTapMS 是單按 Shift 切換中英的時限（毫秒，#319）：按下到放開在這個時間內才算單按。只接受 ShiftTapChoices，
	// 預設 300（缺欄位時也是）。外殼從 health 的 shift_tap_ms 拿到它。不屬於輸入習慣風格。
	ShiftTapMS int `json:"shift_tap_ms"`
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
	KeyboardLayout     string `json:"keyboard_layout"` // standard（預設）、eten（倚天 41 鍵，#83）、hsu（許氏，#183）或 pinyin（漢語拼音，#324）
	// OutputSimplified：送出時把繁體轉成簡體（#118）；組字、候選與學習仍是繁體。預設 false。
	OutputSimplified bool `json:"output_simplified"`
	// SimplifiedHotkey：按 SimplifiedHotkeyLabel 的快捷鍵切換「輸出簡體字」（#192）。預設 false：外殼不攔這個鍵，
	// 照舊交給應用程式。切換跟輸入法選單同一條即時請求（output_simplified），不重啟引擎。
	SimplifiedHotkey bool `json:"simplified_hotkey"`
	// SelectionKeys 是候選窗的選字鍵（#112）：1234567890（預設，檔案沒有這個欄位時也是）、1234qweras 或 asdfghjkl;。
	// 換成字母鍵時數字 1–0 仍照位置選字。
	SelectionKeys string `json:"selection_keys"`
	// CtrlPunctuation：中文模式按 Ctrl＋, . ' ; 與 Ctrl＋Shift＋/ 1 ; 9 0 打全形標點（#168、#231）。預設 true；
	// 想保留程式快速鍵（VS Code 的 Ctrl＋, 等）時關閉，這些鍵照舊交給應用程式。
	CtrlPunctuation bool `json:"ctrl_punctuation"`
	// ShiftSymbolHalfwidth：注音模式的 Shift＋符號鍵打原本那個全形標點的半形版本（#258，Shift＋, 是 ,、Shift＋9 是 (），
	// 沒有對應半形的（『』……——）打鍵帽字元；全形標點改按 Ctrl＋符號。
	// 預設 false（缺欄位時也是），照舊打全形標點。不屬於輸入習慣風格，任何風格都能單獨開關。
	ShiftSymbolHalfwidth bool `json:"shift_symbol_halfwidth"`
	// AllSymbolsHalfwidth：「標點寬度」選「所有符號鍵打半形」（#282）：注音模式所有排列有全形標點的鍵（含未按 Shift 的
	// [ ] ' \）都打鍵帽上的半形字元，全形標點用 Ctrl＋符號（這時 Ctrl＋標點固定開啟，「不打聲調也能選字」暫停）。
	// 只在 ShiftSymbolHalfwidth 也是 true 時成立：舊版只認得那一個欄位，讀到的是最接近的「Shift＋符號打半形」；
	// 舊版把它關掉時，這一欄的殘值不會讓新版變回全部半形（讀取時兩者取 AND）。預設 false（缺欄位時也是）。
	AllSymbolsHalfwidth bool `json:"all_symbols_halfwidth"`
	// CJKLatinSpace：送出時在同一次送出的文字裡，漢字與英文字母直接相連的地方補一個半形空格（#273，「用Python寫」送出
	// 「用 Python 寫」）。預設 false（缺欄位時也是）。不屬於輸入習慣風格，任何風格都能單獨開關。
	CJKLatinSpace bool `json:"cjk_latin_space"`
	// CJKDigitSpace：送出時在同一次送出的文字裡，漢字與半形數字直接相連的地方補一個半形空格（「第2版」送出「第 2 版」）。
	// 跟 CJKLatinSpace 各自開關。預設 false（缺欄位時也是）。不屬於輸入習慣風格，任何風格都能單獨開關。
	CJKDigitSpace bool `json:"cjk_digit_space"`
	// ZhuyinErrorHint：打錯注音時提示（#300）。按了聲調但讀音沒有字（模糊音的近音也沒有）時不收這個聲調，注音停住等修改，
	// Windows、macOS 發出提示音，Linux 在組字區標示；緊接著再按一次同一個聲調照舊原樣留下。只有一個注音符號時不擋。預設 true（缺欄位時也是）。
	// 中英混打與不打聲調時不作用。不屬於輸入習慣風格，任何風格都能單獨開關。
	ZhuyinErrorHint bool `json:"zhuyin_error_hint"`
	// PersonalLearning：記住我選的字詞（#297，跟手機 App 的 personal_learning 同名）。預設 true（缺欄位時也是）。
	// 關閉時引擎照常選字（神經選字、依前文選字、已學到的資料都照用），只是送出與手選不再寫進個人資料（user.json）。
	// 「我的詞彙」不受影響。不屬於輸入習慣風格，任何風格都能單獨開關。
	PersonalLearning bool `json:"personal_learning"`
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
	s := Settings{Selection: Neural, Punctuation: false, DocumentContext: true, CandidateCursor: defaultCandidateCursor(runtime.GOOS), CandidateLayout: "vertical", EmojiShortcut: "equals", EmojiEqualsFirst: true, EscClear: true, SpaceSelects: true, KeyboardLayout: LayoutStandard, SelectionKeys: SelectDigits, CtrlPunctuation: true, ZhuyinErrorHint: true, PersonalLearning: true,
		ToggleKey: defaultToggleKey(runtime.GOOS), ShiftTapMS: DefaultShiftTapMS, ModeMemory: defaultModeMemory(runtime.GOOS), StartMode: StartZhuyin}
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

// 單按 Shift 切換中英的時限（#319）：選單的選項與預設，單位毫秒。#117 起固定 300；按得慢的人要長一點，
// 常誤切的人要短一點。RIME 固定 500 毫秒。
var ShiftTapChoices = []int{200, 300, 400, 500, 600}

const DefaultShiftTapMS = 300

// ShiftTapMillis 是引擎回報給外殼的單按時限（毫秒）；值不在選單裡（零值的 Settings）時是預設的 300。
func (s Settings) ShiftTapMillis() int {
	if slices.Contains(ShiftTapChoices, s.ShiftTapMS) {
		return s.ShiftTapMS
	}
	return DefaultShiftTapMS
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
	}{{"punctuation", &s.Punctuation}, {"document_context", &s.DocumentContext}, {"mixed_input", &s.MixedInput}, {"english_autocomplete", &s.EnglishAutocomplete}, {"left_shift", &s.LeftShift}, {"esc_clear", &s.EscClear}, {"space_selects", &s.SpaceSelects}, {"advance_after_select", &s.AdvanceAfterSelect}, {"emoji_equals_first", &s.EmojiEqualsFirst}, {"output_simplified", &s.OutputSimplified}, {"simplified_hotkey", &s.SimplifiedHotkey}, {"ctrl_punctuation", &s.CtrlPunctuation}, {"shift_symbol_halfwidth", &s.ShiftSymbolHalfwidth}, {"all_symbols_halfwidth", &s.AllSymbolsHalfwidth}, {"cjk_latin_space", &s.CJKLatinSpace}, {"cjk_digit_space", &s.CJKDigitSpace}, {"zhuyin_error_hint", &s.ZhuyinErrorHint}, {"personal_learning", &s.PersonalLearning}, {"caps_lock_english", &s.CapsLockEnglish}, {"toneless", &s.Toneless}} {
		if raw, ok := take(field.key); ok {
			var v bool
			if json.Unmarshal(raw, &v) == nil {
				*field.dst = v
			} else {
				kept[field.key] = raw
			}
		}
	}
	s.AllSymbolsHalfwidth = s.AllSymbolsHalfwidth && s.ShiftSymbolHalfwidth // #282：見欄位說明
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
		{"keyboard_layout", &s.KeyboardLayout, []string{LayoutStandard, LayoutETen, LayoutHsu, LayoutPinyin, LayoutXiaohe}},
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
	if raw, ok := take("shift_tap_ms"); ok { // #319：只認選單裡的整數毫秒
		var ms int
		if json.Unmarshal(raw, &ms) == nil && slices.Contains(ShiftTapChoices, ms) {
			s.ShiftTapMS = ms
		} else {
			kept["shift_tap_ms"] = raw
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
	}{{"selection", s.Selection}, {"punctuation", s.Punctuation}, {"document_context", s.DocumentContext}, {"mixed_input", s.MixedInput}, {"english_autocomplete", s.EnglishAutocomplete}, {"left_shift", s.LeftShift}, {"esc_clear", s.EscClear}, {"space_selects", s.SpaceSelects}, {"candidate_cursor", s.CandidateCursor}, {"advance_after_select", s.AdvanceAfterSelect}, {"candidate_layout", s.CandidateLayout}, {"emoji_shortcut", s.EmojiShortcut}, {"emoji_equals_first", s.EmojiEqualsFirst}, {"keyboard_layout", s.KeyboardLayout}, {"selection_keys", s.SelectionKeys}, {"toggle_key", s.ToggleKey}, {"shift_tap_ms", s.ShiftTapMillis()}, {"mode_memory", s.ModeMemory}, {"start_mode", s.StartMode}, {"output_simplified", s.OutputSimplified}, {"simplified_hotkey", s.SimplifiedHotkey}, {"ctrl_punctuation", s.CtrlPunctuation}, {"shift_symbol_halfwidth", s.ShiftSymbolHalfwidth}, {"all_symbols_halfwidth", s.AllSymbolsHalfwidth}, {"cjk_latin_space", s.CJKLatinSpace}, {"cjk_digit_space", s.CJKDigitSpace}, {"zhuyin_error_hint", s.ZhuyinErrorHint}, {"personal_learning", s.PersonalLearning}, {"caps_lock_english", s.CapsLockEnglish}, {"toneless", s.Toneless}} {
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
	for _, key := range []string{"selection", "punctuation", "document_context", "mixed_input", "english_autocomplete", "left_shift", "esc_clear", "space_selects", "candidate_cursor", "advance_after_select", "candidate_layout", "emoji_shortcut", "emoji_equals_first", "keyboard_layout", "selection_keys", "toggle_key", "shift_tap_ms", "mode_memory", "start_mode", "output_simplified", "simplified_hotkey", "ctrl_punctuation", "shift_symbol_halfwidth", "all_symbols_halfwidth", "cjk_latin_space", "cjk_digit_space", "zhuyin_error_hint", "personal_learning", "caps_lock_english", "toneless"} {
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
		return map[string]string{LayoutStandard: "標準", LayoutETen: "倚天 41 鍵", LayoutHsu: "許氏", LayoutPinyin: "漢語拼音", LayoutXiaohe: "小鶴雙拼"}
	case "shift-tap-ms": // #319
		out := map[string]string{}
		for _, ms := range ShiftTapChoices {
			out[strconv.Itoa(ms)] = strconv.FormatFloat(float64(ms)/1000, 'f', -1, 64) + " 秒"
		}
		return out
	case "shift-symbol-halfwidth":
		return map[string]string{"off": "全形標點", "on": "Shift＋符號打半形", "all": "所有符號鍵打半形"}
	}
	return nil
}

// symbolWidth 是「標點寬度」（shift-symbol-halfwidth）的值：off、on（Shift＋符號打半形，#258）或 all（所有符號鍵打半形，#282）。
func (s Settings) symbolWidth() string {
	switch {
	case s.ShiftSymbolHalfwidth && s.AllSymbolsHalfwidth:
		return "all"
	case s.ShiftSymbolHalfwidth:
		return "on"
	}
	return "off"
}

// 「所有符號鍵打半形」（#282）時 Ctrl＋標點固定開啟（全形標點只剩這條路），聲調選填暫停（半形的 ' 不能再當切字鍵）。
// 設定檔裡的值不動，換回其他標點寬度就照舊。
const (
	allSymbolsCtrlReason     = "所有符號鍵打半形時固定開啟"
	allSymbolsTonelessReason = "所有符號鍵打半形時關閉"
)

// EffectiveCtrlPunctuation 是引擎實際用的「Ctrl＋標點打全形」：所有符號鍵打半形時一律開啟（#282）。
func (s Settings) EffectiveCtrlPunctuation() bool {
	return s.CtrlPunctuation || s.symbolWidth() == "all"
}

// EffectiveToneless 是引擎實際用的「不打聲調也能選字」：所有符號鍵打半形時暫停（#282）。
func (s Settings) EffectiveToneless() bool { return s.Toneless && s.symbolWidth() != "all" }

// disabledReason 回報某一項目前是否因其他設定而不起作用，以及給人讀的原因；空字串表示可用。
// 中英混打與 = 開 Emoji 選單依標準排列設計，倚天 41 鍵時停用（#83）；許氏的聲調在字母鍵上，也停用混打，
// = 不是注音鍵，Emoji 選單照舊（#183）。已儲存的值不動，切回標準排列就照舊。
func (s Settings) disabledReason(key string) string {
	return s.disabledReasonOn(runtime.GOOS, key)
}

func (s Settings) disabledReasonOn(goos, key string) string {
	if reason := s.styleConflictReason(key); reason != "" {
		return reason
	}
	switch key {
	case "mixed-input", "emoji-shortcut", "emoji-equals-first":
		if s.KeyboardLayout == LayoutETen {
			return "倚天 41 鍵時關閉"
		}
		if s.KeyboardLayout == LayoutHsu && key == "mixed-input" {
			return "許氏排列時關閉"
		}
		if s.KeyboardLayout == LayoutPinyin && key == "mixed-input" {
			return pinyinMixedReason // 字母都是拼音（#324）
		}
		if s.KeyboardLayout == LayoutXiaohe && key == "mixed-input" {
			return shuangpinMixedReason // 字母都是雙拼鍵
		}
		if key == "mixed-input" && s.EffectiveToneless() {
			return "不打聲調時關閉" // 聲調選填與混打二選一（#150）
		}
	case "ctrl-punctuation":
		if s.symbolWidth() == "all" {
			return allSymbolsCtrlReason
		}
	case "toneless":
		if s.symbolWidth() == "all" {
			return allSymbolsTonelessReason
		}
		// 只在標準排列生效（#150）：倚天 41 鍵的 ' 是 ㄘ、沒有分隔鍵，許氏一個鍵兼聲母、韻母與聲調。
		switch s.KeyboardLayout {
		case LayoutETen:
			return "倚天 41 鍵時關閉"
		case LayoutHsu:
			return "許氏排列時關閉"
		}
	case "space-selects":
		// 混打實際開著時，注音打完的空白一律是空白、↓ 選字（#78），這一項開或關都沒作用（#288）。
		// 混打本身停用（倚天、許氏、不打聲調、輸入習慣衝突）時引擎不混打，空白鍵選字照常。
		if s.MixedInput && s.disabledReasonOn(goos, "mixed-input") == "" {
			return mixedSpaceReason
		}
	case "start-mode":
		if s.startModeLocked(goos) {
			return startModeLockedReason
		}
	case "shift-tap-ms":
		if s.ToggleKey == ToggleNone {
			return shiftTapUnusedReason
		}
	case "zhuyin-error-hint":
		// 聲調選填（#150）與中英混打（#78）有自己處理沒有字的注音的方式，這一項不作用（#300）。
		// 兩者本身停用（倚天、許氏、所有符號鍵打半形、輸入習慣衝突）時照常；聲調選填開著時混打也停用，原因寫聲調選填。
		if s.Toneless && s.disabledReasonOn(goos, "toneless") == "" && s.KeyboardLayout != LayoutPinyin && s.KeyboardLayout != LayoutXiaohe {
			return "不打聲調時關閉" // 漢語拼音不打聲調時照樣用它提示收不成音節的聲調（#324）
		}
		if s.MixedInput && s.disabledReasonOn(goos, "mixed-input") == "" {
			return zhuyinHintMixedReason
		}
	}
	return ""
}

// pinyinMixedReason：漢語拼音鍵盤（#324）的字母都是拼音，中英混打不作用；英文用 Shift 切換。
const pinyinMixedReason = "漢語拼音時關閉"

// shuangpinMixedReason：雙拼的字母都是拼音鍵，中英混打不作用；英文用 Shift 切換。
const shuangpinMixedReason = "雙拼時關閉"

// zhuyinHintMixedReason：中英混打開著時「打錯注音時提示」不作用（#300）。
const zhuyinHintMixedReason = "中英混打時關閉"

// mixedSpaceReason：中英混打開著時空白輸入空格、改按 ↓ 選字，空白鍵選字不起作用（#288）。
const mixedSpaceReason = "中英混打時關閉，按 ↓ 選字"

// startModeLockedReason：不用 Shift 切換時沒有鍵能從英文切回注音，所以固定從注音開始（#184）。
const startModeLockedReason = "不用 Shift 切換時固定從注音開始"

// shiftTapUnusedReason：不用 Shift 切換時，單按 Shift 的時限沒有作用（#319）。
const shiftTapUnusedReason = "不用 Shift 切換時不作用"

// disabledBy 回報停用原因來自哪一項設定（Item.DisabledBy）：設定程式把各項分在不同頁面（#207），
// 原因那一項在別頁時，用它帶人過去改。新增停用原因時要一起加在這裡（TestDisabledItemsNameTheirCause）。
func disabledBy(reason string) string {
	if rule := styleConflictRule(reason); rule != "" {
		return rule // 輸入習慣的規則（#233）
	}
	switch reason {
	case "倚天 41 鍵時關閉", "許氏排列時關閉", pinyinMixedReason, shuangpinMixedReason:
		return "keyboard-layout"
	case startModeLockedReason, shiftTapUnusedReason:
		return "toggle-key"
	case "不打聲調時關閉": // 中英混打（#150）
		return "toneless"
	case allSymbolsCtrlReason, allSymbolsTonelessReason: // #282
		return "shift-symbol-halfwidth"
	case mixedSpaceReason, zhuyinHintMixedReason: // #288、#300
		return "mixed-input"
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
		{"english-autocomplete", "英文模式自動補字", "單按 Shift 切換到英文模式後，輸入至少兩個字母時顯示補全候選，三個字母起也提供拼字修正；按 Tab 補全或修正拼字、Esc 關閉。「我的詞彙」的英文詞排在最前面；自己打過幾次的英文單字會記在本機，之後也會建議（私人欄位與關閉「記住我選的字詞」時不記）。與中英混打分開設定。空白送出單字並輸入空格；Enter 只送出單字，再按一次才換行或執行", "off", []string{"on", "off"},
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
		{"emoji-equals-first", "將 = 放在第一項", "開啟時選單第一項是 =；關閉時第一項直接是表情。開或關都可以連按兩下 = 輸入一個 =；想單按 = 就打出等號，請關閉 Emoji 選單", "on", []string{"on", "off"}, func(s Settings) string { return onOff(s.EmojiEqualsFirst) }},
		{"keyboard-layout", "鍵盤排列", "注音鍵盤排列。倚天 41 鍵的 = 是 ㄦ、' 是 ㄘ，頓號改按 \\，這時不提供 Emoji 選單與中英混打。許氏只用字母鍵：同一鍵依位置代表聲母或韻母，聲調是 d f j s、一聲按空白，, . ; 直接打「，。；」，這時不提供中英混打。" +
			"漢語拼音用拼音打繁體：ni3hao3 是「你好」，數字 1–4 是四聲、5 是輕聲、空白是一聲，ü 打 v，xi'an 的 ' 分開音節；沒打聲調的字依前後文決定，" +
			", . ; 直接打「，。；」，這時不提供中英混打。" +
			"小鶴雙拼每兩鍵一個音節（ni hc 是「你好」，zh＝v、ch＝i、sh＝u），聲調與標點同漢語拼音；建議同時開「不打聲調也能選字」", LayoutStandard, []string{LayoutStandard, LayoutETen, LayoutHsu, LayoutPinyin, LayoutXiaohe},
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
	// #258：放在已有項目之後，0c-update 列出的順序與舊版相同；設定程式另照頁面排在 Ctrl＋標點下面。
	// #282：擴成三選一，鍵名與 on／off 的意思不變，舊的命令與設定檔照舊。
	out = append(out, spec{"shift-symbol-halfwidth", "標點寬度", shiftSymbolHalfwidthDescription(runtime.GOOS), "off", []string{"off", "on", "all"},
		func(s Settings) string { return s.symbolWidth() }})
	// #273：同上放在最後；設定程式排在「Shift＋符號打半形」下面。
	out = append(out, spec{"cjk-latin-space", "中英文之間自動加空格", cjkLatinSpaceDescription, "off", []string{"on", "off"},
		func(s Settings) string { return onOff(s.CJKLatinSpace) }})
	// 中文與數字之間自動加空格：同上放在最後；設定程式排在「中英文之間自動加空格」下面。
	out = append(out, spec{"cjk-digit-space", "中文與數字之間自動加空格", cjkDigitSpaceDescription, "off", []string{"on", "off"},
		func(s Settings) string { return onOff(s.CJKDigitSpace) }})
	// #300：同上放在最後；設定程式排在「不打聲調也能選字」上面。
	out = append(out, spec{"zhuyin-error-hint", "打錯注音時提示", zhuyinErrorHintDescription(runtime.GOOS), "on", []string{"on", "off"},
		func(s Settings) string { return onOff(s.ZhuyinErrorHint) }})
	// #319：同上放在最後；設定程式排在「中英切換鍵」下面。
	tapValues := make([]string, 0, len(ShiftTapChoices))
	for _, ms := range ShiftTapChoices {
		tapValues = append(tapValues, strconv.Itoa(ms))
	}
	out = append(out, spec{"shift-tap-ms", shiftTapLabel, shiftTapDescription, strconv.Itoa(DefaultShiftTapMS), tapValues,
		func(s Settings) string { return strconv.Itoa(s.ShiftTapMillis()) }})
	// #297：同上放在最後；設定程式排在「選字」頁的「打錯注音時提示」下面。
	out = append(out, spec{"personal-learning", "記住我選的字詞", personalLearningDescription, "on", []string{"on", "off"},
		func(s Settings) string { return onOff(s.PersonalLearning) }})
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

// personalLearningDescription 是「記住我選的字詞」（#297）的說明：關閉時什麼照常、什麼不記、已學的怎麼清。
const personalLearningDescription = "選過的字詞會排到前面，常用的英文單字也會記住；學習資料只存在這台電腦。" +
	"關閉後照常選字（神經選字、依前文選字照舊，已經學到的也照常使用），只是不再記下新的選擇、英文單字和最近用過的符號；" +
	"「我的詞彙」照常可以加詞。要清掉已經學到的，到「我的詞彙」的「靈犀學到的」按「清除學習紀錄」"

// tonelessDescription 是「不打聲調也能選字」（#150）的說明：怎麼切音節、空白還是一聲、混打停用。
const tonelessDescription = "聲調可打可不打：ㄋㄧㄏㄠ 就是「你好」，沒打聲調的字依前後文決定，有打聲調的字照聲調；少打聲調時選字比較容易錯，拿不定的字可以補聲調。" +
	"接不上前一個音節的注音會開始新的字；ㄒㄧㄢ 是一個字，要「西安」請打 ㄒㄧ'ㄢ（組字中的 ' 用來切字，頓號改按 \\）或在 ㄒㄧ 後面補聲調。" +
	"沒打完的注音按空白仍是一聲，送出請按 Enter。開啟時中英混打停用，英文請用 Shift 切換，「我的詞彙」裡的英文詞照樣認得。只在標準排列與漢語拼音有效；" +
	"漢語拼音本來就可以不打聲調，開啟時空白把拼音換成字並送出（關閉時空白是一聲）；要先看字再送出，請按 ↓ 或方向鍵"

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
	// 時限可調（#319），這裡不寫秒數，指向那一項，免得說明跟設定不一致。
	d := "單按哪個 Shift 切換注音／英文；按住 Shift 超過「" + shiftTapLabel + "」、或 Shift 加字母（打大寫）不算切換。沒用來切換的 Shift 照系統原本的用途"
	if goos == "windows" {
		d += "。預設左右 Shift 都可以切換，不想讓左 Shift 切換可改成「右 Shift」；按著 Shift 點滑鼠選取也不算切換。也可以按 Ctrl＋空白鍵"
	} else if goos == "darwin" {
		d += "。Mac 預設不用 Shift，使用系統的輸入來源切換；想用 Shift 可在這裡開啟"
	}
	return d
}

// 「單按 Shift 的時限」（#319）的名稱與說明。
const (
	shiftTapLabel       = "單按 Shift 的時限"
	shiftTapDescription = "按下 Shift 到放開在這個時間內才算單按，切換注音／英文。按得比較慢、常切換不成功時調長；打字時常不小心切到英文時調短"
)

// cjkLatinSpaceDescription 是「中英文之間自動加空格」（#273）的說明：什麼時候補、哪裡不補、只看這次送出的字。
const cjkLatinSpaceDescription = "送出時在中文字和英文字母相連的地方補一個半形空格，例如「用Python寫」送出「用 Python 寫」；組字時照打的顯示。" +
	"已有空格、標點和數字旁不補；只看這次送出的字，跟前面已經送出的字之間不補"

// cjkDigitSpaceDescription 是「中文與數字之間自動加空格」的說明。
const cjkDigitSpaceDescription = "送出時在中文字和半形數字相連的地方補一個半形空格，例如「第2版」送出「第 2 版」；組字時照打的顯示。" +
	"已有空格、標點和全形數字旁不補；只看這次送出的字，跟前面已經送出的字之間不補"

// zhuyinErrorHintDescription 是「打錯注音時提示」（#300）的說明：什麼時候擋、怎麼提示（Windows、macOS 提示音，
// Linux 的 Fcitx5、IBus 沒有統一的提示音，標示注音）、故意打注音文的退路、哪些情況不作用。
func zhuyinErrorHintDescription(goos string) string {
	hint := "發出提示音"
	if goos == "linux" {
		hint = "把停住的注音標示出來"
	}
	return "按了聲調，這個注音卻沒有字（例如 ㄅㄩˊ）時，不收這個聲調、注音留著讓你改，並" + hint + "；開了模糊音時近音有字就照常打出。" +
		"只有一個注音符號（例如 ㄟ、ㄅ＋空白）時照舊直接留下。要故意打 ㄏㄟˊ 這類注音，在同一個地方馬上再按一次同一個聲調就會留下。中英混打和不打聲調時不作用"
}

// shiftSymbolHalfwidthDescription：「標點寬度」（#258、#282）的說明。「Shift＋符號打半形」打什麼（Shift＋, 是半形逗號，
// 不是 <）、[ ] 不變、微軟參考本來就半形的鍵照風格（#272）；「所有符號鍵打半形」打按鍵上的字、Ctrl＋標點固定開、
// 聲調選填暫停；兩者全形都改按 Ctrl＋符號或 ↓。Mac 的 Ctrl 寫 ⌃。
func shiftSymbolHalfwidthDescription(goos string) string {
	ctrl := "Ctrl＋"
	if goos == "darwin" {
		ctrl = "⌃"
	}
	return "注音模式的符號鍵打全形標點或半形符號。「Shift＋符號打半形」：Shift＋, 打半形逗號、Shift＋. 打半形句號、Shift＋9 打 (、Shift＋; 打 :，[ ] 照舊打「」；標點操作選微軟參考時，< > ? \" 照舊打按鍵上的字。" +
		"「所有符號鍵打半形」：每個符號鍵都打按鍵上的字，例如 Shift＋- 打 _、Shift＋, 打 <、[ 打 [，這時 " + ctrl + "標點固定開啟，「不打聲調也能選字」暫停。" +
		"半形時全形標點改按 " + ctrl + "符號（例如 " + ctrl + ", 打「，」），或在符號上按 ↓ 換；< > 也在 ↓ 裡"
}

// ctrlPunctuationDescription：說明會占用哪些程式快速鍵、可以關掉（#168）。Mac 是 Control 鍵（⌃），程式快速鍵多用 ⌘，
// 撞到的少，不舉 VS Code 的例子。#231 加了 Ctrl＋Shift＋; 9 0：Windows 舉 Excel（插入時間、取消隱藏列），
// Linux 舉 Konsole（分割畫面）。
func ctrlPunctuationDescription(goos string) string {
	if goos == "darwin" {
		return "注音模式按 ⌃, 打「，」、⌃. 打「。」、⌃' 打「、」、⌃; 打「；」、⌃⇧/ 打「？」、⌃⇧1 打「！」、⌃⇧; 打「：」、⌃⇧9 打「（」、⌃⇧0 打「）」，沒在打字時也可以。" +
			"注音模式時這幾個鍵不再交給 App，要保留時可以關掉；英文模式不受影響"
	}
	d := "注音模式按 Ctrl＋, 打「，」、Ctrl＋. 打「。」、Ctrl＋' 打「、」、Ctrl＋; 打「；」、Ctrl＋Shift＋/ 打「？」、Ctrl＋Shift＋1 打「！」、" +
		"Ctrl＋Shift＋; 打「：」、Ctrl＋Shift＋9 打「（」、Ctrl＋Shift＋0 打「）」，沒在打字時也可以。會占用程式的這幾個快速鍵（例如 VS Code 的 Ctrl＋, 開設定、Ctrl＋. 快速修正"
	switch goos {
	case "windows":
		d += "，Excel 的 Ctrl＋Shift＋; 插入時間、Ctrl＋Shift＋9 取消隱藏列"
	case "linux":
		d += "，Konsole 的 Ctrl＋Shift＋9／0 分割畫面"
	}
	d += "），要保留時可以關掉；英文模式不受影響"
	if goos == "windows" {
		// 輸入法直接切換熱鍵 0x104（Ctrl＋Shift＋0 → 日文輸入法）是繁體中文 Windows 的預設值，日文沒裝也作用，
		// 在 TSF 之前就被系統拿走。負責人決定保留這個鍵、不改系統熱鍵。
		d += "。Windows 預設把 Ctrl＋Shift＋0 留給切換輸入法，這一鍵打不出「）」"
	}
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
		// #282：Ctrl＋標點固定開啟、聲調選填暫停。設定程式把停用的開關畫成關閉，固定開啟的要帶 effective 才畫成開，
		// 設定檔本來就是 on 時也要帶。
		if reason == allSymbolsCtrlReason {
			effective = "on"
		}
		if reason == allSymbolsTonelessReason {
			effective = "off"
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
	case "shift-tap-ms":
		s.ShiftTapMS, _ = strconv.Atoi(value) // 已核對過是選單裡的值
		s.forget("shift_tap_ms")
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
	case "shift-symbol-halfwidth":
		// off：全形標點；on：Shift＋符號打半形（#258）；all：所有符號鍵打半形（#282）。
		s.ShiftSymbolHalfwidth = value != "off"
		s.AllSymbolsHalfwidth = value == "all"
		s.forget("shift_symbol_halfwidth")
		s.forget("all_symbols_halfwidth")
	case "cjk-latin-space":
		s.CJKLatinSpace = value == "on"
		s.forget("cjk_latin_space")
	case "cjk-digit-space":
		s.CJKDigitSpace = value == "on"
		s.forget("cjk_digit_space")
	case "zhuyin-error-hint":
		s.ZhuyinErrorHint = value == "on"
		s.forget("zhuyin_error_hint")
	case "personal-learning":
		s.PersonalLearning = value == "on"
		s.forget("personal_learning")
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
