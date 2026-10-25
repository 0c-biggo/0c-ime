// Package session 是單一輸入欄位的狀態機。核心擁有全部狀態：組字、游標、鎖定、
// 候選、補全與學習事件；外殼只送請求、畫快照（docs/19 原則 1）。
package session

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/english"
	"github.com/0c-biggo/0c-ime/core/fuzzy"
	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/inputstyle"
	"github.com/0c-biggo/0c-ime/core/layout"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/punct"
	"github.com/0c-biggo/0c-ime/core/rerank"
	"github.com/0c-biggo/0c-ime/core/suggest"
	"github.com/0c-biggo/0c-ime/core/zhconv"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

const perPage = 10

type candState struct {
	start, length int
	before        bool // 前方模式：詞長變動仍以原來的尾端為界，向左延伸
	items         []decode.Candidate
	cursor        int             // 反白的候選（items 的索引）；頁碼由它推得
	literal       bool            // 符號選單：插入或替換字面文字，不作為詞彙學習
	pending       bool            // 選取後消耗尚未完成的注音；取消時保留
	raw           bool            // 一音節漢字候選的最後一項是原樣注音
	matched       []string        // 與 items 等長（或 nil）：近音候選在詞庫中的讀音，原音為空字串
	labels        []string        // 開啟候選時算一次的顯示原因；不隨反白或翻頁改變
	english       bool            // 英文模式的自動補字候選
	mixed         bool            // 尚未定案的中英按鍵候選，保留原始按鍵
	keyLiterals   map[int]string  // candidate index → original physical keys for a phonetic span
	syllable      *pendingLiteral // 原樣留下的音節（#109）：選項是同讀音的各聲調，選入後仍是原樣音節
	kind          string
	direct        bool  // empty composition: selection commits once.
	ids           []int // stable palette IDs, independent of recent order.
	// split > 0 時是混合清單（#161）：items[:split] 是 start/length 的詞，items[split:] 接著列
	// 游標那個字（位置 single）的單字候選，翻頁就看得到，不必先按 Tab 換到單字。
	split  int
	single int
}

func (s *Session) candidatePage() int { return s.cand.cursor / s.pageSize() }

// span 回報第 id 項候選替換的組字範圍：混合清單的單字項目只替換游標那個字。
func (c *candState) span(id int) (start, length int) {
	if c.split > 0 && id >= c.split {
		return c.single, 1
	}
	return c.start, c.length
}

// hasExact 回報目前詞長是否有原音候選（非近音）；混合清單接在後面的單字不算。
func (c *candState) hasExact() bool {
	n := len(c.items)
	if c.split > 0 {
		n = c.split
	}
	for i := range n {
		if i >= len(c.matched) || c.matched[i] == "" {
			return true
		}
	}
	return false
}

// Options 是 session 層級的行為開關。
type Options struct {
	DocumentContextWeight float64         // 經確認的跨提交前詞與 BOS 混合權重，0 停用
	PrefixAware           bool            // 組字尾端視為更長的詞的前綴
	Suggest               suggest.Options // Tab 補全的門檻
	Beam                  int             // 每個位置至多展開幾個有上下文的狀態；0 不限（見 decode.Decoder.Beam）
	Continue              float64         // 尾端「還會繼續打一個更長的詞」的權重 γ（見 decode.Decoder.Continue）
	SeparateContinuation  bool
	FuzzyAuto             fuzzy.AutoPolicy // 模糊音 auto 模式的自動修正門檻；只在有配對設為 auto 時用到
}

func DefaultOptions() Options {
	return Options{DocumentContextWeight: 0.1, PrefixAware: true, Suggest: suggest.DefaultOptions(), Beam: 8, Continue: 0.5, SeparateContinuation: true, FuzzyAuto: fuzzy.DefaultAutoPolicy()}
}

type Session struct {
	styleCycle   *candState
	InputStyle   inputstyle.Rules
	fullWidth    bool
	symbolPrefix bool
	styleGrid    bool
	// Rerank 由宿主在第一次 Handle 前設定；零值保持原引擎。
	Rerank rerank.Options
	// Punct 由宿主設定：提交時補標點（只插入、不改原字）；nil 停用。私人／暫停／英數狀態一律不送出文字。
	Punct punct.Punctuator
	// Fuzzy 由宿主依使用者設定：逐對啟用的模糊音；零值全部關閉，行為與沒有此功能時相同。
	Fuzzy fuzzy.Config
	// Simplified 由宿主依「輸出簡體字」設定（#118）：送出給應用程式的文字轉成簡體，nil 照原樣送出繁體。
	// 組字區、候選、學習、補標點與前文都維持繁體，只有 Snapshot.Commit 改變。
	Simplified *zhconv.Converter
	// Toneless 由宿主依「不打聲調也能選字」設定（#150，預設關）：聲調選填，沒打聲調的音節由解碼決定（untoned.go）。
	// 開啟時停用中英混打的自動判斷。零值時行為與沒有此功能時相同。
	Toneless bool
	// MixedInput is opt-in; English is a shared, immutable vocabulary.
	EnglishAutocomplete   bool
	MixedInput            bool
	English               *english.Dictionary
	EmojiShortcut         string
	EmojiEqualsFirst      bool
	CandidateColumns      int
	CandidateBeforeCursor bool // 由宿主設定；零值維持游標後方選字
	displayColumns        int
	paletteCommit         bool
	paletteChanged        bool
	paletteUndo           *literalSpan
	englishWord           bool // 只有本功能開始的英文單字才在邊界送出
	englishDismissed      bool
	englishTokenBlocked   bool // 網址／識別字：到下一個空白或導覽前不重新補字
	mixed                 pendingKeys
	mixedKeys             map[int]string      // completed phonetic position → original keys, retained until edited/committed
	mixedOrigins          map[int]mixedOrigin // literal positions: independently typed vs system-assisted
	pendingToneKey        *toneKeyPending     // 上一鍵是單獨的聲調鍵：緊接著的空白換成聲調符號（#135）
	untonedRun            *untonedRun         // 聲調選填時這一串連續打的注音鍵（#150），認「我的詞彙」的英文詞
	literalKeys           map[int]string      // 照字面留下的符號位置 → 原按鍵，↓ 第二項可選回（#135 聲調符號、#190 混打的「，」「。」）
	retypeLiteral         bool                // 上一鍵剛把沒有字的音節原樣留下：緊接著的同類介音／韻母是在改它（#85）
	// DocumentContextOff 由宿主依使用者設定：外殼送來的前文確認一律忽略，不以前文選字。
	DocumentContextOff bool
	// SpaceInsertsSpace 由宿主依使用者設定（關閉「空白鍵選字」）：注音打完後空白插入半形空白、↓ 選字，
	// 同中英混打（#107、#110）；零值空白開候選。未打完的注音照樣以空白完成一聲，候選開著時空白照樣換頁。
	SpaceInsertsSpace bool
	// AdvanceAfterSelect 由宿主依使用者設定「選字後游標移到下一個字」（#133）：從候選窗選定詞後，
	// 後方模式游標移到所選詞的結尾，前方模式往後移一個字（同新酷音 autoShiftCur），句尾時不動。
	// 零值游標留在原處。符號、Emoji 與原樣注音本來就插入在游標處、游標停在它們後面，不受影響。
	AdvanceAfterSelect bool
	// SelectionKeys 由宿主依使用者設定（#112）：候選窗每一頁第 1–10 項的選字鍵，例如 1234qweras。
	// 空字串或不合用的值為 1234567890。數字 1–0 不論設定都照位置選字。
	SelectionKeys string
	// CtrlPunct 由宿主依「Ctrl＋標點打全形」設定（#168，預設開）：中文模式的 Ctrl＋, . ' ; 與 Ctrl＋Shift＋/ 1
	// 打全形標點，跟一般標點一樣插進組字區。零值時這些鍵回 handled=false，外殼照一般快捷鍵交給應用程式。
	CtrlPunct bool
	// CapsLockEnglish 由宿主依「Caps Lock 開著時直接打英文」設定（#173，預設關）：外殼送來的 caps_lock 請求才有作用，
	// 開啟時暫時切到英文、關閉時回到原本的模式。零值時 caps_lock 回 handled=false，注音模式照舊不理 Caps Lock（#168）。
	CapsLockEnglish bool
	capsLock        bool // 最近一次 caps_lock 請求的狀態；mode 請求清掉
	capsEnglish     bool // 目前的英文模式是 Caps Lock 帶來的：關掉 Caps Lock 回到注音
	// Preview02 打開時處理 0.2.0 的按鍵（組字中加詞、反悔鍵、「我的片語」，#128）；0.2.0 起宿主一律打開。
	// 零值時這些按鍵照舊回 handled=false 交還應用程式，行為與沒有此功能時相同（測試用它確認這一點）。
	Preview02    bool
	mark         *markState // 組字中加詞的標示範圍（#123）
	notices      []string   // 這次請求中由按鍵處理產生的提示，併入快照
	vocabChanged bool
	forgot       bool
	// EscKeepComposition 由宿主依使用者設定：Esc 只清除未打完的注音，保留已組好的字；零值清除整段（多數注音輸入法的預設）。
	EscKeepComposition bool
	allowRerank        bool
	documentEnabled    bool
	documentLeft       string
	pendingLeft        string
	neuralLeft         string // 外殼確認後的自身提交文字（最多 rerank.MaxContextRunes 字），只給宣告需要前文的評分器
	pendingNeural      string
	rerankCache        map[string]rerankEntry // 分段神經決策快取；提交／取消時清空
	phonetic           *phoneticHint
	phoneticCache      phoneticHintCache
	phoneticUndo       *phoneticUndo
	hint               *neuralHint       // 最後一段的次佳候選提示；Tab 接受
	shownPath          []decode.Seg      // 上次顯示的路徑（含神經改選）；下一鍵重新評分時延伸成候選
	shownSyls          []zhuyin.Syllable // 產生 shownPath 的讀音
	pendingContextID   uint64
	model              *personal.Model
	dec                *decode.Decoder
	front              input.Frontend
	layout             layout.Layout
	Opt                Options

	comp            zhuyin.Composer
	pendingLiterals []pendingLiteral // 暫存成字面注音的組音狀態，撤銷旁邊的英數後可接續
	toggleStash     *pendingStash    // 切到英文時清掉的未打完注音；下一個請求不是立即切回就丟棄（#90）
	syls            []zhuyin.Syllable
	cursor          int
	locks           map[int]decode.Lock
	literals        map[int]bool   // syls 中此位置是字面 Unicode scalar；編輯依 grapheme 邊界
	displayed       map[int]string // 已顯示的漢字；英數切開詞條而解不出時保留畫面
	rejected        []personal.RejectedSeg
	paused          bool
	private         bool         // 敏感欄位組字持續到清空，切回普通欄位也不把殘留文字學進去
	localBlocked    bool         // 暫停／敏感狀態進入後，本次組字不恢復暫時證據
	english         bool         // 英文模式：有組字時英數留在組字區，空白時交還應用程式
	autoLog         []AutoChange // 目前顯示路徑中的自動近音修正（診斷用，每次重新解碼時重建）
	nearReading     bool         // 精確讀音解不出路徑，目前只因近音可成詞而收進組字；顯示保底（已顯示的字照舊，其餘原樣注音）

	path           []decode.Seg
	basePath       []decode.Seg // 不含暫時證據的路徑，鄰詞證據不得從自己的推測反覆累加
	pathSig        string       // 產生 path 的輸入；輸入沒變就不重新解碼
	sug            *suggest.Suggestion
	previewRewrite bool // 使用者要求的同音替換預覽；下一個非 Tab 操作即清除
	cand           *candState
	rev            int
}

func New(m *personal.Model) *Session {
	return &Session{
		model:            m,
		dec:              &decode.Decoder{Lex: m.Source(), Score: m},
		front:            input.Exact{},
		layout:           layout.Standard,
		Opt:              DefaultOptions(),
		English:          english.Prototype(),
		EmojiShortcut:    "equals",
		EmojiEqualsFirst: true,
		locks:            map[int]decode.Lock{},
	}
}

// SetLayout 由宿主在第一次 Handle 前設定鍵盤排列；nil 為標準排列（#83）。
func (s *Session) SetLayout(l layout.Layout) {
	if l == nil {
		l = layout.Standard
	}
	s.layout = l
}

// mixedInput：中英混打對「哪些數字、標點是注音鍵」的判斷依標準排列寫成，其他排列先停用（#83）；
// 使用者的設定保留，切回標準排列就照舊。
func (s *Session) mixedInput() bool {
	return mixedInputActive(s.MixedInput && !s.toneless(), s.layout == layout.Standard) // 聲調選填與混打二選一（#150）
}

func mixedInputActive(on, standardLayout bool) bool { return on && standardLayout }

// emojiEquals：= 開 Emoji 選單；排列把 = 當注音鍵（倚天的 ㄦ）時停用，= 一律是注音（#83）。
func (s *Session) emojiEquals() bool {
	_, phonetic := s.layout.Symbol('=')
	return s.EmojiShortcut == "equals" && !phonetic
}

func (s *Session) composing() bool {
	return s.symbolPrefix || len(s.syls) > 0 || !s.comp.Empty() || s.mixed.raw != ""
}

// Composing 回報是否有未提交的組字；引擎交接只在沒有組字時進行。
func (s *Session) Composing() bool { return s.composing() || s.cand != nil }

// Handle 處理一個請求並回傳完整快照。
func (s *Session) Handle(r proto.Request) proto.Snapshot {
	cycleKey := r.Type == proto.Key && ((s.InputStyle.Tab == "cycle" && (r.Key == proto.KeyTab || r.Key == proto.KeyBackTab)) || s.InputStyle.Up == "cycle" && r.Key == proto.KeyUp)
	if !cycleKey && r.Type != proto.Context && r.Type != proto.Display {
		s.styleCycle = nil
	}
	if r.Type == proto.OutputSimplified {
		// #169：引擎已換好所有 session 的 Simplified。切換是全域設定、不是對這個輸入框的操作：
		// 不能清掉下面這些只跟著按鍵的狀態（Shift 誤觸還原、Tab 修正的撤銷、加詞標示……）。
		return s.View()
	}
	s.paletteChanged = false
	// 前文確認與欄數不是使用者操作；其他任何請求都表示切到英文後已有動作，不再還原注音。
	stash := s.toggleStash
	if r.Type != proto.Context && r.Type != proto.Display {
		s.toggleStash = nil
	}
	if r.Type != proto.Context && r.Type != proto.Display && r.Type != proto.Backspace && !(r.Type == proto.Key && (r.Key == proto.KeyBackspace || r.Key == proto.KeyEscape)) {
		s.phoneticUndo = nil
	}
	if r.Type != proto.Backspace && !(r.Type == proto.Key && r.Key == proto.KeyBackspace) {
		s.paletteUndo = nil
	}
	if r.Type != proto.Key && r.Type != proto.Context && r.Type != proto.Display {
		s.pendingToneKey = nil // 按鍵由 key 自己取出；其他操作之後的空白不再換成聲調符號（#135）
		s.retypeLiteral = false
		s.untonedRun = nil
	}
	_, keypad := proto.KeypadChar(r.Key)
	s.allowRerank = r.Type == proto.Symbol || r.Type == proto.Backspace || r.Type == proto.Key && (len([]rune(r.Key)) == 1 || keypad || r.Key == proto.KeyBackspace)
	defer func() { s.allowRerank = false }()
	if !(r.Type == proto.Key && (r.Key == proto.KeyTab || r.Key == proto.KeyEscape)) && r.Type != proto.Accept {
		s.previewRewrite = false
	}
	if !s.composing() {
		s.private = r.Private
	} else {
		s.private = s.private || r.Private
	}
	if r.Private || r.Type == proto.Pause && r.On {
		s.clearDocumentContext()
		s.localBlocked = true
		if s.cand != nil && (s.cand.mixed || s.cand.kind != "") {
			s.cand = nil // 不讓已開啟的個人英文候選跨入保護狀態。
		}
	}
	s.model.SyncTime()
	if r.Type != proto.Key {
		s.mark = nil // 加詞的標示只跟著按鍵；滑鼠移動游標、切換模式等都取消
	}
	var commit string
	var notices []string
	handled := true
	switch r.Type {
	case proto.Context:
		s.confirmDocumentContext(r)
	case proto.Display:
		if r.Columns >= 1 && r.Columns <= 10 {
			s.displayColumns = r.Columns
		}
	case proto.Key:
		handled, commit = s.key(r.Key)
	case proto.Symbol:
		s.flushMixed()
		for _, sym := range r.Symbol {
			handled = s.symbol(sym) && handled
		}
	case proto.Backspace:
		s.englishDismissed = false
		s.cand = nil
		handled = s.backspace()
	case proto.Cursor:
		s.flushMixed()
		s.moveTo(r.To)
	case proto.Candidates:
		s.openCandidates(r.Length)
	case proto.Page:
		if s.cand != nil && r.Index >= 0 && r.Index < s.pages() {
			s.cand.cursor = r.Index * s.pageSize()
		}
	case proto.Select:
		if !s.selectCandidate(r.Index) {
			notices = append(notices, "沒有這個候選")
		}
	case proto.Close:
		if s.englishWord {
			s.englishDismissed = true
		}
		s.cand = nil
	case proto.Accept:
		handled = s.accept()
	case proto.Commit:
		commit = s.commit()
	case proto.Cancel:
		s.reset()
	case proto.Forget:
		s.model.Forget(r.Word)
		if s.cand != nil && s.cand.english {
			s.cand = nil
		} else if s.cand != nil && s.cand.mixed {
			s.openMixedCandidates()
		}
		s.pathSig = "" // 明確撤銷仍立即生效，不受神經畫面保留規則影響。
		notices = append(notices, "已撤銷與「"+r.Word+"」有關的學習")
	case proto.Pause:
		s.paused = r.On
		if r.On && s.englishWord {
			commit = s.commit()
		}
	case proto.Toggle, proto.Mode:
		english := !s.english
		if r.Type == proto.Mode {
			english = r.On
			// 外殼重設模式（換欄位、重開 session、macOS 的 Caps Lock）：忘掉記得的 Caps Lock 狀態，
			// 下一個按鍵的鎖定狀態跟快照不同時外殼重新送 caps_lock（#173）。
			s.capsLock = false
		}
		// 使用者自己切換或外殼設定模式之後，關掉 Caps Lock 不再改變模式（#173）。
		s.capsEnglish = false
		s.setMode(english, r.Type == proto.Toggle && r.Restore, stash)
	case proto.CapsLock:
		handled, commit = s.capsLockChanged(r.On)
	default:
		handled = false
	}
	if r.Type == proto.Toggle || r.Type == proto.Mode || r.Type == proto.CapsLock || !handled && r.Type == proto.Key {
		s.clearDocumentContext()
	}
	if s.paletteCommit {
		s.paletteCommit = false
		s.refresh()
		commit = s.snapshot().Text() // direct palette output is exact; no punctuation/model rewrite.
		commit = s.output(commit, commit, s.readingPins(commit))
		s.reset()
	}
	s.refresh()
	// 保護狀態／前文確認／撤銷可以在候選窗仍開啟時變更；不能留下過期的個人或關聯標示。
	if s.cand != nil && !s.cand.mixed && (r.Private || r.Type == proto.Pause || r.Type == proto.Context || r.Type == proto.Forget) {
		s.cand.labels = s.candidateLabels(s.cand)
	}
	s.rev++
	s.refreshEnglishCandidates()
	snap := s.snapshot()
	if hint := s.markHint(); hint != "" {
		s.notices = append(s.notices, hint) // 標示中每個快照都附說明（#123；三平台調查都沒有，最容易拉開差距）
	}
	snap.Handled, snap.Commit, snap.Notices = handled, commit, append(notices, s.notices...)
	snap.PersonalChanged = s.paletteChanged || s.forgot
	snap.VocabChanged, snap.Forgot = s.vocabChanged, s.forgot
	s.notices, s.vocabChanged, s.forgot = nil, false, false
	if commit != "" {
		snap.ContextID = s.pendingContextID
	}
	s.status(&snap)
	return snap
}

// View 回傳目前的畫面，不改任何狀態（不算一次操作、Revision 不變）。給不屬於組字的全域切換用（#169 輸出簡體字）。
func (s *Session) View() proto.Snapshot {
	snap := s.snapshot()
	snap.Handled = true
	if hint := s.markHint(); hint != "" {
		snap.Notices = []string{hint}
	}
	s.status(&snap)
	return snap
}

// status 填入快照裡不屬於組字的狀態，Handle 與 View 共用。
func (s *Session) status(snap *proto.Snapshot) {
	snap.Mode = proto.ModeZhuyin
	if s.english {
		snap.Mode = proto.ModeEnglish
	}
	snap.CapsLock, snap.CapsEnglish = s.capsLock, s.capsEnglish
	snap.OutputSimplified = s.Simplified != nil // #169：外殼的狀態顯示跟著
}

// setMode 切換中英模式（單按 Shift、外殼設定模式、Caps Lock）。切到英文清掉未打完的注音（#90），已組好的字與游標保留；
// 切換本身也關閉候選。restore 是外殼確認中間沒有任何操作就立即切回注音：把剛清掉的注音放回原處（防誤觸）。
func (s *Session) setMode(english, restore bool, stash *pendingStash) {
	s.symbolPrefix = false
	if english && !s.english {
		s.finishUntoned() // 聲調選填：已是完整音節的注音留在組字區，不當成打到一半清掉（#150）
		s.toggleStash = s.clearPendingZhuyin()
	}
	s.flushMixed()
	s.clearDocumentContext()
	s.cand = nil
	if !english && s.english && restore && stash != nil {
		s.comp, s.mixed = stash.comp, stash.mixed
	}
	s.english = english
	s.englishWord, s.englishDismissed, s.englishTokenBlocked = false, false, false
}

// capsLockChanged 處理外殼回報的 Caps Lock 狀態（#173，設定「Caps Lock 開著時直接打英文」）。設定關閉時回 handled=false、
// 什麼都不改，跟沒有這個功能時一樣。開啟 Caps Lock：注音模式先送出組字（同 macOS 的 #84），暫時切到英文；
// 已經是英文模式（單按 Shift 切的）就只記下狀態。關閉：Caps Lock 帶來的英文回到注音，使用者自己切的英文不動。
// 開著 Caps Lock 時單按 Shift 照常切換（capsEnglish 清掉），之後關掉 Caps Lock 不再改模式。
func (s *Session) capsLockChanged(on bool) (bool, string) {
	if !s.CapsLockEnglish {
		return false, ""
	}
	if on == s.capsLock {
		return true, ""
	}
	s.capsLock = on
	var commit string
	switch {
	case on && !s.english:
		commit = s.commit()
		s.setMode(true, false, nil)
		s.toggleStash = nil // 組字已送出，沒有可放回的注音
		s.capsEnglish = true
	case !on && s.capsEnglish:
		s.setMode(false, false, nil)
		s.capsEnglish = false
	}
	return true, commit
}

// CapsLockOn 回報外殼最近一次 caps_lock 請求是開啟（#173；設定關閉時一律 false）。全部共用時引擎據此讓 Caps Lock
// 開著的輸入框不跟上別的輸入框切回的注音。
func (s *Session) CapsLockOn() bool { return s.capsLock }

// EnglishMode 回報目前是否為英文模式（引擎在「全部共用」時用來同步各輸入框，#115）。
func (s *Session) EnglishMode() bool { return s.english }

func (s *Session) key(k string) (handled bool, commit string) {
	if s.paletteUndo != nil && k != proto.KeyBackspace {
		s.paletteUndo = nil
	}
	toneKey := s.pendingToneKey // 只對緊接著的這一鍵有效（#135）
	s.pendingToneKey = nil
	retype := s.retypeLiteral // 同上，只有緊接著的這一鍵能改剛留下的錯音節（#85）
	s.retypeLiteral = false
	run := s.untonedRun // 聲調選填時連續打的注音鍵（#150）：只有緊接著的這一鍵能接續
	s.untonedRun = nil
	if handled, commit, done := s.styleKey(k); done {
		return handled, commit
	}
	k = s.normalizeVocabKey(k) // #123 的新鍵名在沒有對應功能時就是原本的鍵
	// 數字鍵盤永遠不是注音鍵（#75）：英文模式與候選選號同主鍵盤數字；沒在組字交還應用程式；
	// 組字中以半形插在游標處，未打完的注音保留成符號（同 Shift＋字母）。
	if c, ok := proto.KeypadChar(k); ok {
		switch {
		case s.english || s.cand != nil && c >= '0' && c <= '9':
			k = string(c)
		case s.cand != nil:
			return true, "" // 候選開啟時吃掉運算鍵，不與選字同時解讀
		case !s.composing():
			return false, ""
		default:
			s.flushMixed()
			s.insertPendingLiteral()
			s.insertLiteral(string(c))
			return true, ""
		}
	}
	if p, ok := proto.CtrlPunct(k); ok {
		return s.ctrlPunct(p), ""
	}
	if s.english && s.EnglishAutocomplete {
		if handled, commit, active := s.englishKey(k); active {
			return handled, commit
		}
	}
	if s.english && !s.composing() {
		return false, ""
	}
	// 英文模式的空白、標點與數字都按原樣插入，不開候選或解讀成注音。
	if s.english {
		if runes := []rune(k); len(runes) == 1 && unicode.IsPrint(runes[0]) {
			s.insertPendingLiteral()
			s.insertLiteral(k)
			return true, ""
		}
	}
	if s.Preview02 && s.cand == nil {
		if handled, consumed := s.vocabKey(k); consumed {
			return handled, ""
		}
	}
	if s.cand != nil {
		if s.paletteKey(k) {
			return true, ""
		}
		switch k {
		case proto.KeyShiftDelete, proto.KeyShiftBackspace:
			s.forgetCandidate() // normalizeVocabKey 已確認開關打開、是一般候選
		case proto.KeyEscape, proto.KeyBackspace, proto.KeyDelete:
			s.cand = nil // 關閉候選、保留組字；Backspace／Del 不刪字（#106）
		case proto.KeyEnter:
			s.selectCandidate(s.candidateID(s.cand.cursor))
			if s.InputStyle.CandidateEnter == "commit" {
				return true, s.commit()
			}
		case " ":
			if s.InputStyle.CandidateSpace == "select" {
				s.selectCandidate(s.candidateID(s.cand.cursor))
			} else {
				s.cand.cursor = (s.candidatePage() + 1) % s.pages() * s.pageSize()
			}
		case proto.KeyPageDown:
			s.cand.cursor = (s.candidatePage() + 1) % s.pages() * s.pageSize()
		case proto.KeyPageUp:
			s.cand.cursor = (s.candidatePage() + s.pages() - 1) % s.pages() * s.pageSize()
		case proto.KeyDown:
			if s.candidateColumns() > 1 {
				s.gridMove(k)
			} else {
				s.moveHighlight(1)
			}
		case proto.KeyUp:
			if s.candidateColumns() > 1 {
				s.gridMove(k)
			} else {
				s.moveHighlight(-1)
			}
		case proto.KeyRight, proto.KeyLeft:
			if s.candidateColumns() > 1 {
				s.gridMove(k)
			} else if k == proto.KeyRight {
				s.cand.cursor = (s.candidatePage() + 1) % s.pages() * s.pageSize()
			} else {
				s.cand.cursor = (s.candidatePage() + s.pages() - 1) % s.pages() * s.pageSize()
			}
		case proto.KeyHome:
			s.cand.cursor = 0 // 第一個候選（#106）
		case proto.KeyEnd:
			s.cand.cursor = len(s.cand.items) - 1 // 清單的最後一個候選（混合清單是單字的原樣注音）
		case proto.KeyTab:
			switch s.InputStyle.CandidateTab {
			case "grid":
				s.styleGrid = true
			case "next":
				s.moveHighlight(1)
			default:
				s.nextCandidateLength()
			}
		default:
			if n := s.selectionIndex(k); n >= 0 {
				s.selectCandidate(s.candidateID(s.candidatePage()*s.pageSize() + n))
			} else if k == "j" { // 選字鍵優先：asdfghjkl; 時上面已選字（#193）
				s.stepCandidate(-1)
			} else if k == "k" {
				s.stepCandidate(1)
			}
		}
		return true, "" // 候選開啟時吃掉所有按鍵（含 Shift+Tab 等沒有作用的鍵），不與注音鍵同時解讀、不交給應用程式
	}
	if k == "=" && s.emojiEquals() && !s.private && s.paletteTriggerAllowed() {
		s.openPalette()
		return true, ""
	}
	if s.mixedKey(k) {
		return true, ""
	}
	if s.untonedEnglish(run, k) {
		return true, "" // 「我的詞彙」裡的英文詞接空白照字面送出（#150，同混打的 #146）
	}
	if s.untonedSeparator(k) {
		return true, ""
	}
	s.untonedKey(k)
	switch k {
	case proto.KeyBackspace:
		return s.backspace(), ""
	case proto.KeyDelete:
		if !s.composing() {
			return false, ""
		}
		s.flushMixed()
		s.insertPendingLiteral()
		at := s.cursor
		s.cursor = s.literalStep(at, 1)
		for s.cursor > at {
			s.eraseBeforeCursor()
		}
		return true, ""
	case proto.KeyEnter:
		if !s.composing() {
			return false, ""
		}
		return true, s.commit()
	case proto.KeyEscape:
		if s.undoPhonetic() {
			return true, ""
		}
		if s.mixed.accepted != "" {
			s.mixed.accepted = ""
			return true, ""
		}
		if s.previewRewrite {
			s.previewRewrite = false
			return true, "" // 先關掉明確要求的替換預覽，保留組字
		}
		// 未打完的注音先單獨清掉，已組好的字留著；再按一次才依設定清除整段。
		if s.mixed.raw != "" {
			s.clearMixed()
			return true, ""
		}
		if !s.comp.Empty() {
			s.comp.Reset()
			return true, ""
		}
		if !s.composing() {
			return false, ""
		}
		if !s.EscKeepComposition {
			s.reset()
		}
		return true, "" // 保留組字時仍吃掉 Esc，不讓應用程式在組字中關閉對話框
	case proto.KeyTab:
		// 補全照舊；組字中沒有可接受的補全時吃掉不動作，不讓應用程式在組字中移動焦點或插入定位字元（#106）。
		if s.mixed.raw != "" {
			return s.acceptMixed() || s.composing(), ""
		}
		if s.english {
			return s.composing(), ""
		}
		if s.phonetic != nil || s.sug != nil {
			return s.accept() || s.composing(), ""
		}
		if s.hint != nil && s.cursor == len(s.syls) && s.comp.Empty() && s.cand == nil {
			return s.acceptHint() || s.composing(), ""
		}
		// 沒有自動建議時可要求同音替換；先預覽，第二次 Tab 才接受。
		if s.cursor == len(s.syls) && s.comp.Empty() {
			s.previewRewrite = true
			s.refresh()
			if s.sug != nil {
				return true, ""
			}
			s.previewRewrite = false
		}
		return s.composing(), "" // 沒有合格補全：組字中吃掉，沒有組字才交還應用程式
	case proto.KeyUp, proto.KeyPageUp, proto.KeyPageDown, proto.KeyBackTab:
		// 候選沒開時這些鍵在組字中沒有作用；吃掉不動作，免得應用程式的游標或焦點離開組字區（#106）。
		// 沒有組字時交還應用程式。
		return s.composing(), ""
	case proto.KeyLeft, proto.KeyRight, proto.KeyHome, proto.KeyEnd:
		if !s.composing() {
			return false, ""
		}
		s.flushMixed()
		if s.english {
			// 英文模式按畫面字元移動，不能被尚未完成的注音卡住。
			s.insertPendingLiteral()
		}
		// Home／End 移到組字區頭尾（#106）。注音打到一半時同左右鍵：游標不動、按鍵照樣吃掉，
		// 不讓應用程式的游標離開組字區。
		switch k {
		case proto.KeyLeft:
			s.moveTo(s.literalStep(s.cursor, -1))
		case proto.KeyRight:
			s.moveTo(s.literalStep(s.cursor, 1))
		case proto.KeyHome:
			s.moveTo(0)
		default:
			s.moveTo(len(s.syls))
		}
		return true, ""
	case proto.KeyDown:
		if s.english {
			return false, ""
		}
		if !s.composing() {
			return false, ""
		}
		s.openCandidates(0)
		return true, ""
	}
	if k == "`" {
		s.flushMixed()
		s.openSymbols(symbolPalette(), s.cursor, 0, !s.comp.Empty())
		return true, ""
	}
	runes := []rune(k)
	if len(runes) != 1 {
		return false, ""
	}
	if runes[0] == ' ' {
		if s.finishToneKey(toneKey) {
			return true, "" // 單獨的聲調鍵＋空白：聲調符號（#135）
		}
		s.restorePendingLiteral() // 返回暫存注音後，空白仍須能完成一聲。
		if s.comp.Empty() {
			if len(s.syls) == 0 {
				return false, ""
			}
			// 中英混打：注音打完後空白就是半形空白（同英文單字後的空白），↓ 選字（#78）。
			// 使用者關閉「空白鍵選字」時一般注音模式也一樣（#107、#110）。
			// 緊接在數字後面（連打的聲調鍵 34、數字鍵盤）也是空白，同沒在組字時打數字再按空白（#135）。
			if s.mixedInput() || s.SpaceInsertsSpace || s.literalNumberBefore() != "" {
				s.insertLiteral(" ")
				return true, ""
			}
			s.openCandidates(0)
			return true, ""
		}
	}
	if sym, ok := s.layout.Symbol(runes[0]); ok {
		if retype && s.retypeSyllableLiteral(runes[0]) {
			return true, ""
		}
		before := s.untonedRunFrom(run)
		if s.pressKey(runes[0]) {
			s.trackUntonedKey(before, k)
			return true, ""
		}
		// 沒有待完成音節的聲調鍵不可漏到應用程式，否則數字會跑到組字之前。
		// 沒開中英混打時，沒在組字也先留在組字區，緊接著空白就是聲調符號（#135）。
		if s.toneKey(k, sym) {
			return true, ""
		}
	}
	// 標點保留在組字區，可移動、替換候選；Enter 才提交。
	if p, ok := s.layout.Punct(runes[0]); ok {
		s.insertPendingLiteral()
		s.insertPunctKey(k, p)
		return true, ""
	}
	// 其他可見字元（Shift＋字母、@、#、落單聲調鍵等）在游標處逐字保留。
	if s.composing() && unicode.IsPrint(runes[0]) {
		s.insertPendingLiteral()
		s.insertLiteral(k)
		return true, ""
	}
	return false, ""
}

// ctrlPunct 處理 Ctrl＋標點（#168）：設定關閉或英文模式時不處理，外殼照一般快捷鍵先送出組字再交給應用程式。
// 中文模式跟一般標點（Shift＋, 等）走同一條路：未打完的注音留成符號，標點插在游標處、留在組字區，沒在組字時也一樣。
// 中英混打打到一半的英文先定案（Shift＋, 在原樣英文後是半形 <，Ctrl＋, 一律是全形）。候選開啟時同其他按鍵吃掉不動作。
func (s *Session) ctrlPunct(p string) bool {
	if !s.CtrlPunct || s.english {
		return false
	}
	if s.cand != nil {
		return true
	}
	s.flushMixed()
	s.insertPendingLiteral()
	s.insertLiteral(p)
	return true
}

// pressKey 依鍵盤排列解讀注音鍵（#183）：許氏同一鍵依目前的注音暫存代表不同符號，所以先接回暫存的注音再解讀。
// 一般符號由排列加進暫存；聲調鍵（含空白的一聲）照 symbol 完成音節，暫存是空的時候回傳 false（落單的聲調鍵，#135）。
func (s *Session) pressKey(key rune) bool {
	s.restorePendingLiteral()
	next, sym, ok := s.layout.Press(s.comp, key)
	if !ok {
		return false
	}
	if zhuyin.ClassOf(sym) != zhuyin.Tone && s.untonedBreak(sym) {
		s.finishUntoned() // 聲調選填：這個注音接不上目前的音節，目前的音節不打聲調就完成，這一鍵開始新的音節（#150）
		next, sym, _ = s.layout.Press(s.comp, key)
	}
	if zhuyin.ClassOf(sym) == zhuyin.Tone {
		if s.comp.Empty() {
			return false
		}
		s.comp = next // 許氏的收尾修正：只打 ㄇ 就按聲調是 ㄢ
		return s.symbol(sym)
	}
	s.comp = next
	return true
}

func (s *Session) symbol(sym rune) bool {
	s.restorePendingLiteral()
	if s.untonedBreak(sym) {
		s.finishUntoned() // 聲調選填：這個注音接不上目前的音節，目前的音節不打聲調就完成（#150）
	}
	before := s.comp
	done, ok := s.comp.Add(sym)
	if ok {
		s.completeSyllable(before, done)
		return true
	}
	return zhuyin.ClassOf(sym) != zhuyin.None && !(zhuyin.ClassOf(sym) == zhuyin.Tone && s.comp.Empty())
}

// completeSyllable 收進一個剛完成的音節：before 是完成前的組音狀態，done 是完成的音節（打了聲調，
// 或聲調選填時沒打聲調的音節）。
func (s *Session) completeSyllable(before zhuyin.Composer, done zhuyin.Syllable) {
	done = s.neutralFallback(done)
	if s.joinPrefixLiteral(done) {
		return
	}
	// 收進這個音節之後若整段解不出任何路徑（例如 ㄏㄟˊ、單打的 ㄅ＋空白，或候選關閉後多按了
	// 一個數字鍵組出的 ㄅㄨㄛˇ），收進去會讓組字區空白，之後打什麼都顯示不出來（使用者回報）。
	// 改成把整個音節連同聲調原樣留在組字區（#109、#107）：看得到、可退格，Enter 照樣送出；
	// 使用者明確打的聲調不丟，接著打的注音從新的音節開始，不會蓋掉它。
	// 原本就解不出路徑（保底顯示中）時不擋，照舊收進去，否則會變成什麼都打不進去。
	s.refresh()
	decodable := len(s.syls) == 0 || len(s.path) > 0 || s.nearReading
	saved := s.saveSyllables()
	s.insert(done)
	// 精確讀音沒有字、但展開近音後整段可成詞的音節照樣收進來（例如 ㄕㄨㄥˋ 打的是 ㄙㄨㄥˋ）：
	// 顯示維持原樣注音，只有開候選時才出現近音。
	if s.refresh(); decodable && len(s.path) == 0 {
		if s.fuzzyDecodable() {
			s.nearReading = true
		} else {
			s.restoreSyllables(saved)
			s.insertSyllableLiteral(before, done)
			s.retypeLiteral = true // 多半是打錯：緊接著的同類介音／韻母改這個讀音（#85）
		}
	} else if decodable && len(s.path) > 0 && !s.completeDecodable() {
		// 只能當某個更長的詞的開頭、自己沒有字的音節（ㄟ 只有「工口」的 ㄟ ㄌㄨㄛˊ，#107）：
		// 解碼器會先顯示那個詞的第一個字，接著打的音節接不上時就卡成「工ㄋㄧˇ」。
		// 同樣原樣留下注音；下一個音節能和它成詞（ㄋㄧ＋ㄗ˙＝妮子）時再收回去解碼。
		s.restoreSyllables(saved)
		s.insertSyllableLiteral(before, done)
		s.pendingLiterals[len(s.pendingLiterals)-1].prefix = true
	}
}

// insert 在游標處插入一個音節；其後的鎖定右移，跨越游標的鎖定解除。
func (s *Session) insert(syl zhuyin.Syllable) {
	at := s.cursor
	s.syls = append(s.syls[:at:at], append([]zhuyin.Syllable{syl}, s.syls[at:]...)...)
	s.shiftLocks(at, +1)
	s.cursor++
}

func (s *Session) backspace() bool {
	if s.undoPhonetic() {
		return true
	}
	if span := s.paletteUndo; span != nil {
		s.paletteUndo = nil
		if s.cursor == span.end {
			for s.cursor > span.start {
				s.eraseBeforeCursor()
			}
			s.restorePendingLiteral()
			return true
		}
	}
	if s.mixed.raw != "" {
		if s.mixed.accepted != "" {
			s.mixed.accepted = ""
		} else {
			s.mixed.raw = s.mixed.raw[:len(s.mixed.raw)-1]
			s.mixed.assistedPrefix = min(s.mixed.assistedPrefix, len(s.mixed.raw))
			if s.mixed.raw == "" {
				s.clearMixed()
			}
		}
		return true
	}
	s.restorePendingLiteral()
	if !s.comp.Empty() {
		s.comp = s.layout.Backspace(s.comp) // 許氏退掉最後一鍵，那一鍵順便改掉的聲母一起還原（#183）
		return true
	}
	if s.cursor == 0 {
		return s.composing()
	}
	to := s.literalStep(s.cursor, -1)
	for s.cursor > to {
		s.eraseBeforeCursor()
	}
	s.restorePendingLiteral()
	return true
}

// 內部替換／還原只刪指定的一格，不能觸發使用者退格的注音還原。
func (s *Session) eraseBeforeCursor() {
	at := s.cursor - 1
	s.syls = append(s.syls[:at:at], s.syls[at+1:]...)
	s.shiftLocks(at, -1)
	s.cursor--
}

// shiftLocks 處理在 at 插入（delta=+1）或刪除（delta=-1）一個音節後的鎖定與拒絕紀錄。
// 完全在編輯點之前的鎖定不動；完全在之後的平移；被這次編輯切到的解除。
func (s *Session) shiftLocks(at, delta int) {
	s.shiftMixedKeys(at, delta)
	s.literalKeys = shiftKeys(s.literalKeys, at, delta)
	s.shiftMixedOrigins(at, delta)
	s.shiftLiterals(at, delta)
	s.shiftPendingLiterals(at, delta)
	s.shiftDisplayed(at, delta)
	next := map[int]decode.Lock{}
	for start, l := range s.locks {
		after := start >= at
		if delta < 0 {
			after = start > at
		}
		switch {
		case start+l.Len <= at:
			next[start] = l
		case after:
			next[start+delta] = l
		}
	}
	s.locks = next
	kept := s.rejected[:0]
	for _, r := range s.rejected {
		if r.End <= at {
			kept = append(kept, r)
		}
	}
	s.rejected = kept
}

func (s *Session) moveTo(to int) {
	s.finishUntoned()
	if !s.comp.Empty() {
		return // 音節打到一半不移動游標
	}
	s.cursor = s.literalFloor(max(0, min(to, len(s.syls))))
	s.cand = nil
}

func (s *Session) lattice() input.Lattice { return s.lockLattice(s.expand(s.syls), 0, 0) }

// signature 描述決定解碼結果的所有輸入。只打了聲母、介音等未完成符號時音節序列不變，
// 不需要重新解碼。
func (s *Session) signature() string {
	var b strings.Builder
	cursor := s.cursor
	generation := s.model.Gen
	if s.Rerank.Scorer != nil {
		cursor = 0
		generation = 0
	} // 導覽或其他欄位的學習不換掉已顯示文字；下次修改音節才採用最新模型。
	fmt.Fprintf(&b, "%s|%d|%v|%d|%d|%g|%v|%v|", zhuyin.Key(s.syls), cursor, s.Opt.PrefixAware, generation, s.Opt.Beam, s.Opt.Continue, s.paused, s.private)
	fmt.Fprintf(&b, "%v|%s|%q|", s.Opt.SeparateContinuation, s.contextLeft(0), s.neuralContext())
	fmt.Fprintf(&b, "%g|", s.Opt.DocumentContextWeight)
	if s.Fuzzy.AutoEnabled() { // 自動修正的規則、模式與門檻都決定顯示路徑；關閉時簽章與沒有此功能時相同
		fmt.Fprintf(&b, "fuzzy:%s:%v|", s.Fuzzy.Key(), s.Opt.FuzzyAuto)
	}
	starts := make([]int, 0, len(s.locks))
	for st := range s.locks {
		starts = append(starts, st)
	}
	sort.Ints(starts)
	for _, st := range starts {
		fmt.Fprintf(&b, "%d:%s:%d:%v:%d:%s;", st, s.locks[st].Word, s.locks[st].Len, s.locks[st].Completion, s.locks[st].LiteralBoundary, s.locks[st].Matched)
		if s.locks[st].Segmentation {
			b.WriteString("seg;")
		}
	}
	for i := range s.syls {
		if s.literals[i] {
			fmt.Fprintf(&b, "literal:%d;", i)
		}
	}
	return b.String()
}

func (s *Session) refresh() {
	for at, lock := range s.locks {
		boundary := at - 1
		if lock.LiteralBoundary > 0 {
			boundary = at + lock.Len
		}
		if lock.LiteralBoundary != 0 && !s.literals[boundary] {
			delete(s.locks, at) // 分隔英數已刪除，恢復正常解碼；其他標點不影響此邊界。
		}
	}
	if sig := s.signature(); sig != s.pathSig {
		s.dec.Beam, s.dec.Continue = s.Opt.Beam, s.Opt.Continue
		s.dec.SeparateContinuation = s.Opt.SeparateContinuation
		s.path = s.decodeWithLiterals()
		s.rerankPath()
		s.autoFuzzy()
		s.applyPronounHabit()
		if s.absorbReadings() {
			sig = s.signature() // 寫回的是同一串按鍵的另一個讀音，音節格不變，不必再解一次
		}
		s.shownPath, s.shownSyls = slices.Clone(s.path), slices.Clone(s.syls)
		if len(s.path) > 0 {
			s.displayed = map[int]string{}
			for _, g := range s.path {
				for i, r := range []rune(g.Word) {
					if !s.literals[g.Start+i] {
						s.displayed[g.Start+i] = string(r)
					}
				}
			}
		}
		s.pathSig = sig
	}
	if len(s.path) > 0 {
		s.nearReading = false
	}
	s.sug = nil
	if !s.english && s.mixed.raw == "" && s.cursor == len(s.syls) && s.comp.Empty() && s.cand == nil && !s.autoFuzzed() {
		s.sug = s.suggestAfterLiteral()
	}
	s.refreshPhoneticHint()
}

func (s *Session) openCandidates(length int) {
	s.styleGrid = false
	if s.englishWord {
		s.englishDismissed = false
		s.cand = nil
		s.refreshEnglishCandidates()
		return
	}
	if s.mixed.raw != "" {
		s.openMixedCandidates()
		return
	}
	if !s.comp.Empty() && !s.finishUntoned() {
		s.openSymbols([]string{s.comp.String()}, s.cursor, 0, true)
		return
	}
	if len(s.syls) == 0 {
		return
	}
	before := s.candidateBefore()
	pos := min(s.cursor, len(s.syls)-1)
	if before {
		pos = max(0, s.cursor-1)
	}
	if s.literals[pos] {
		if p, ok := s.syllableLiteralAt(pos); ok {
			s.openSyllableLiteral(p)
			return
		}
		start := s.literalFloor(pos)
		end := s.literalStep(start, 1)
		var text strings.Builder
		for _, r := range s.syls[start:end] {
			text.WriteString(string(r))
		}
		alternatives := symbolAlternatives(text.String())
		if (s.InputStyle.Punctuation == "standard" || s.InputStyle.Punctuation == "chewing") && (text.String() == "＼" || text.String() == "／") {
			alternatives = []string{text.String()}
			if text.String() == "＼" {
				alternatives = append(alternatives, "／")
			} else {
				alternatives = append(alternatives, "＼")
			}
		}
		s.openSymbols(s.literalKeyAlternatives(alternatives, start, end-start), start, end-start, false)
		return
	}
	anchor := pos
	if before {
		anchor = pos + 1
	}
	open := func(length int) bool { return s.openAnchored(anchor, length, before) }
	if length > 0 {
		open(length)
		return
	}
	// 以游標所指的現有分詞為優先。前方模式選詞首到游標，後方模式選游標到詞尾；
	// 游標在詞的兩端時可重選整詞，Tab 與上下鍵保留其他詞長。
	s.refresh()
	for _, g := range s.path {
		if g.Start <= pos && pos < g.End {
			length := g.End - pos
			if before {
				length = anchor - g.Start
			}
			if open(length) {
				return
			}
		}
	}
	// 沒有現成分詞時由最長的詞長找起。只有「也有原音候選」的詞長才算數，所以與沒有模糊音時
	// 選同一個詞長；只有近音的詞長仍可用 Tab 或方向鍵切換進入。
	maxLen := min(decode.MaxLen(s.dec.Lex), len(s.syls)-pos)
	if before {
		maxLen = min(decode.MaxLen(s.dec.Lex), anchor)
	}
	for l := maxLen; l >= 1; l-- {
		if open(l) && (l == 1 || s.cand.hasExact()) {
			return
		}
	}
}

// stepCandidate 把要修改的字往前（d=-1，j）或往後（d=1，k）換一格並重開候選（#193，照新酷音）：
// 不必 Esc、←／→、再 ↓。「要修改的字」是緊貼游標的那個字：後方模式是候選的第一個字，前方模式是最後一個字；
// 標點等原樣文字整段算一格。換到的位置照 ↓ 的規則開窗（前方／後方模式、優先用現有分詞），
// 後方模式從句尾開的前方候選也以最後一個字為準往前換，不會開回同一個字。已在頭尾時不動。
// 只用於改字的候選：Emoji／顏文字、中英混打的按鍵候選、注音打到一半與 ` 的符號選單都不套用。
func (s *Session) stepCandidate(d int) {
	if !s.canStepCandidate() {
		return
	}
	c := s.cand
	at := c.start
	if c.before {
		at = s.literalFloor(c.start + c.length - 1)
	}
	if d < 0 && at == 0 {
		return
	}
	next := s.literalStep(at, d)
	if next >= len(s.syls) {
		return
	}
	s.cursor = next
	if s.CandidateBeforeCursor {
		s.cursor = s.literalStep(next, 1)
	}
	s.openCandidates(0)
}

// canStepCandidate：目前的候選是改字的候選，j／k 會換前後一個字（stepCandidate）。快照的 StepKeys 也依它判斷，
// 外殼的「J K 換字」提示跟實際行為一致（#193）。
func (s *Session) canStepCandidate() bool {
	c := s.cand
	return c != nil && c.kind == "" && !c.direct && !c.mixed && !c.english && !c.pending && c.length > 0 &&
		s.comp.Empty() && s.mixed.raw == "" && !s.englishWord
}

// candidateBefore 決定這次開窗選游標前方還是後方的詞。後方模式的游標在組字尾端時後面沒有字，
// 照字面只能選最後一個字，Tab 與上下鍵也換不到較長的詞；打完一句回頭改最後一個詞很常見，
// 所以這時改用前方模式的錨點，選結束在游標的詞。方向記在候選裡，換詞長時維持同一個錨點。
func (s *Session) candidateBefore() bool {
	return s.CandidateBeforeCursor || s.cursor >= len(s.syls)
}

// openAnchored 保留查詢方向；切換詞長時不把前方模式的尾端當成詞首。
// 詞長超過一個字時，詞的候選後面接著列游標那個字的單字（#161）：後方模式是詞的第一個字，
// 前方模式是最後一個字，都是緊貼游標的那個字，Tab 換詞長時也不變。
func (s *Session) openAnchored(anchor, length int, before bool) bool {
	pos, single := anchor, anchor
	if before {
		pos -= length
		single = anchor - 1
	}
	if !s.openAt(pos, length) {
		return false
	}
	if length > 1 {
		s.appendSingles(single)
	}
	s.cand.before = before
	return true
}

// appendSingles 把位置 at 的單字候選接在目前詞長的候選後面，成為一張清單（#161）。
// 兩段各自查詢、各自算顯示原因，所以排序、近音附註、原始按鍵與原樣注音都與單獨開啟時相同。
func (s *Session) appendSingles(at int) {
	words := s.cand
	if !s.openAt(at, 1) {
		s.cand = words
		return
	}
	singles := s.cand
	s.cand = words
	split := len(words.items)
	if words.matched != nil || singles.matched != nil {
		matched := make([]string, split+len(singles.items))
		copy(matched, words.matched)
		copy(matched[split:], singles.matched)
		words.matched = matched
	}
	words.items = append(words.items, singles.items...)
	words.labels = append(words.labels, singles.labels...)
	for id, keys := range singles.keyLiterals {
		words.keyLiterals[split+id] = keys
	}
	words.raw = singles.raw // 最後一項是單字的原樣注音
	words.split, words.single = split, at
}

func (s *Session) openAt(pos, length int) bool {
	if pos < 0 || length <= 0 || pos+length > len(s.syls) {
		return false
	}
	for i := pos; i < pos+length; i++ {
		if s.literals[i] {
			return false
		}
	}
	s.refresh()
	pinned := func(string) bool { return false }
	if length == 1 {
		syl := s.syls[pos]
		pinned = func(w string) bool { return s.model.PickedSingle(syl, w) || s.model.VocabReading(w, s.syls[pos:pos+1]) }
	} else if reading := s.syls[pos : pos+length]; s.model.HasVocab() {
		pinned = func(w string) bool { return s.model.VocabReading(w, reading) } // 自訂詞保證在第一頁（#122）
	}
	// 標點／原樣注音界定上下文，不讓候選評分跨到另一段。
	begin, end := pos, pos+length
	for begin > 0 && !s.literals[begin-1] {
		begin--
	}
	for end < len(s.syls) && !s.literals[end] {
		end++
	}
	locks := s.locksIn(begin, end)
	s.configureDocumentContext(begin)
	s.dec.Evidence = s.localEvidence(begin)
	s.configureCorrections(begin, end, true)
	// 與視窗相交的跨音鎖定即將被這次選取取代：不補它們的讀音，原音候選只依打出的音查詢。
	lat := s.lockLattice(s.expand(s.syls), pos, pos+length)
	windows := []decode.Window{{Lat: lat[begin:end], Pinned: pinned}}
	if near, ok := s.nearWindow(lat, pos, length); ok {
		near.Lat = near.Lat[begin:end]
		windows = append(windows, near) // 近音版本與原音版本共用同一份前後文計算
	}
	if repair, ok := s.phoneticWindow(lat, pos, length); ok {
		repair.Lat = repair.Lat[begin:end]
		windows = append(windows, repair)
	}
	found := s.dec.CandidatesInContextWindows(lat[begin:end], pos-begin, length, locks, windows...)
	items := found[0]
	s.orderPronounCandidates(items, pos, length, pinned)
	var matched []string
	var near []decode.Candidate
	var nearMatched []string
	for _, group := range found[1:] {
		// Original readings win ties in provenance; dialect suggestions keep
		// their order, and duplicate new suggestions consume no extra slots.
		known := append(slices.Clone(items), near...)
		add, readings := s.nearOnly(group, known, pos, length)
		near = append(near, add...)
		nearMatched = append(nearMatched, readings...)
	}
	if len(near) > 0 {
		items, matched = mergeFuzzy(items, near, nearMatched)
	}
	// Keep physical keys recoverable without creating Chinese-word preferences.
	// The ordinary raw-zhuyin candidate remains last for existing clients.
	keyLiterals := map[int]string{}
	if keys := s.originalMixedKeys(pos, length); keys != "" {
		// 只用數字鍵出的字（#134 的 5 之、#162 的 20 單）：要數字的人多，原按鍵排第二個，第一個仍是目前的字。
		at := len(items)
		if length == 1 && at > 1 && s.digitWordKeys(pos) {
			at = 1
		}
		keyLiterals[at] = keys
		items = slices.Insert(items, at, rawCandidate(keys))
		if matched != nil {
			matched = slices.Insert(matched, min(at, len(matched)), "")
		}
	}
	if length == 1 {
		items = append(items, rawCandidate(s.syls[pos].Text()))
		if matched != nil {
			matched = append(matched, "")
		}
	}
	if len(items) == 0 {
		return false
	}
	s.cand = &candState{start: pos, length: length, items: items, raw: length == 1, matched: matched, keyLiterals: keyLiterals}
	s.cand.labels = s.candidateLabels(s.cand)
	return true
}

func (s *Session) pages() int { return (len(s.cand.items) + s.pageSize() - 1) / s.pageSize() }

// 候選開啟時 Tab 切至下一個較長詞長，最長後回到最短；不接受補全或提交。
func (s *Session) nextCandidateLength() {
	c := s.cand
	if c == nil || c.literal || c.mixed {
		return
	}
	anchor, maxLen := s.candidateAnchor(c)
	for i := 1; i <= maxLen; i++ {
		length := (c.length-1+i)%maxLen + 1
		if s.openAnchored(anchor, length, c.before) {
			return
		}
	}
}

func (s *Session) candidateAnchor(c *candState) (int, int) {
	if c.before {
		end := c.start + c.length
		return end, min(decode.MaxLen(s.dec.Lex), end)
	}
	return c.start, min(decode.MaxLen(s.dec.Lex), len(s.syls)-c.start)
}

// moveHighlight 把反白往下（d=1）或往上（d=-1）移一項。各詞長的候選視為由長到短接在一起的
// 一份清單：走過目前詞長的尾端就接到較短的詞長，走過開頭就回到較長的；頭尾相接，
// 所以方向鍵永遠有反應（使用者回報：上下鍵不能移動，像是卡死）。
// 混合清單已經含單字（#161），所以從多字詞長出發時跳過「只有單字」的詞長，往上回到較長詞長時
// 停在它的最後一個詞，都不重複走一次單字。
func (s *Session) moveHighlight(d int) {
	c := s.cand
	if c.literal || c.mixed {
		c.cursor = (c.cursor + d + len(c.items)) % len(c.items)
		return
	}
	if n := c.cursor + d; n >= 0 && n < len(c.items) {
		c.cursor = n
		return
	}
	anchor, maxLen := s.candidateAnchor(c)
	for i := 1; i <= maxLen; i++ { // i == maxLen 時回到原詞長：只有這一種詞長有候選
		l := ((c.length-1-d*i)%maxLen+maxLen)%maxLen + 1
		if l == 1 && c.split > 0 {
			continue
		}
		if s.openAnchored(anchor, l, c.before) {
			if d < 0 {
				s.cand.cursor = len(s.cand.items) - 1
				if s.cand.split > 0 {
					s.cand.cursor = s.cand.split - 1 // 停在較長詞長的最後一個詞，不再走一次單字
				}
			}
			return
		}
	}
}

// selectCandidate 鎖定選取的詞。手動選取是硬條件；被換掉的原區段記下來供學習使用。
func (s *Session) selectCandidate(id int) bool {
	if s.cand != nil && s.cand.ids != nil {
		id = slices.Index(s.cand.ids, id)
	}
	if s.cand == nil || id < 0 || id >= len(s.cand.items) {
		return false
	}
	if s.cand.english {
		s.mixed.accepted = s.cand.items[id].Entry.Surface
		s.cand = nil
		return true
	}
	if s.cand.mixed {
		return s.selectMixed(id)
	}
	if s.cand.literal || s.cand.keyLiterals[id] != "" || s.cand.raw && id == len(s.cand.items)-1 {
		return s.selectLiteral(id)
	}
	start, length := s.cand.span(id)
	c, end := s.cand.items[id].Entry, start+length
	shown, auto := slices.Clone(s.path), slices.Clone(s.autoLog)
	for _, g := range s.path {
		if g.Start <= start && start < g.End && (g.Word != c.Surface || g.Start != start || g.End != end) {
			s.rejected = append(s.rejected, personal.RejectedSeg{Word: g.Word, Start: g.Start, End: g.End, Correction: g.Correction})
		}
	}
	for st, l := range s.locks {
		if st < end && start < st+l.Len {
			delete(s.locks, st)
		}
	}
	lock := decode.Lock{Word: c.Surface, Len: length}
	if id < len(s.cand.matched) {
		lock.Matched = s.cand.matched[id] // 跨音手選：鎖定連同它的讀音，近音路徑持續到鎖定解除
	}
	s.locks[start] = lock
	if s.AdvanceAfterSelect {
		if s.cand.before {
			s.cursor = s.literalStep(end, 1)
		} else {
			s.cursor = end
		}
	}
	s.cand = nil
	s.refreshSelection(shown, auto, decode.Seg{Word: c.Surface, Start: start, End: end, Correction: c.Correction, Matched: lock.Matched})
	return true
}

// accept 接受 Tab 補全：補上尚未輸入的音節並鎖定整個詞。接受是明確操作，
// 學習時保留補全來源，權重低於主動手選，避免系統建議自我強化。
func (s *Session) accept() bool {
	if s.englishWord {
		return s.acceptEnglish()
	}
	if s.mixed.raw != "" {
		return s.acceptMixed()
	}
	if s.phonetic != nil {
		return s.acceptPhonetic()
	}
	if s.sug == nil {
		return false
	}
	g := s.sug
	// 即使上層誤留建議，也不能跨越使用者鎖定的詞。
	for at, lock := range s.locks {
		if at < len(s.syls) && g.Start < at+lock.Len {
			return false
		}
	}
	s.syls = append(s.syls, g.Syls...)
	s.locks[g.Start] = decode.Lock{Word: g.Word, Len: len(s.syls) - g.Start, Completion: true}
	s.cursor = len(s.syls)
	s.previewRewrite = false
	return true
}

func (s *Session) commit() string {
	// Resolve pending English exactly as displayed; never accept a suggestion on Enter.
	s.flushMixed()
	s.finishUntoned() // 聲調選填：最後一個沒打聲調的音節也照解碼結果送出（#150）
	if s.Rerank.Scorer == nil {
		s.refresh()
	} // 神經模式提交已顯示路徑；Enter 不啟動推論或接受其他欄位的模型更新。
	text := s.snapshot().Text() // 未完成注音可能在句中，依畫面位置提交。
	pins := s.readingPins(text)
	// 沒打聲調的音節換回詞庫讀音再學（#150）；換不回來的詞不學（同近音），但包在裡面的手選用自己的讀音學。
	syls, _ := s.resolvedSyllables()
	ev := personal.Commit{Syls: s.lockReadings(syls), Rejected: s.rejectedReadings()}
	for _, g := range s.path {
		if s.literals[g.Start] {
			continue // 留下位置間隙，學習模型會在符號後重設上下文
		}
		l, locked := s.locks[g.Start]
		if l.LiteralBoundary != 0 {
			continue // 臨時保留的詞片段不是使用者選字，也不當作完整詞學習。
		}
		ev.Path = append(ev.Path, personal.Seg{
			Word: g.Word, Start: g.Start, End: g.End, Partial: g.Partial,
			Manual:     locked && l.Word == g.Word && l.Len == g.End-g.Start,
			Completion: locked && l.Word == g.Word && l.Len == g.End-g.Start && l.Completion,
			Suggested:  locked && l.Suggested,
			Fuzzy:      s.fuzzyTouches(g.Start, g.End) || untonedTouches(syls, g.Start, g.End),
		})
	}
	ev.Selected = s.enclosedSelections(ev.Syls)
	if !s.paused && !s.private && len(ev.Path) > 0 {
		s.model.Learn(ev)
	}
	if s.mixedInput() && !s.paused && !s.private && !s.localBlocked {
		s.model.LearnEnglish(s.mixedEnglishCommits())
	}
	shown := text
	text = s.punctuate(text)
	tail := s.committedTail()
	neural := s.neuralCommitContext(text)
	s.reset()
	if tail != "" && text != "" {
		s.pendingLeft = tail
		s.pendingContextID = uint64(s.rev + 1)
	}
	if neural != "" {
		s.pendingNeural = neural
		s.pendingContextID = uint64(s.rev + 1)
	}
	return s.output(shown, text, pins) // 學習與前文都用上面的繁體
}

// readingPins 找出提交文字中讀音能決定簡體寫法的字（zhconv.Pin），以 text 的字元位置為鍵。
// 只在 text 正好是路徑上各詞依序接起來時才找（未打完的注音夾在句中等情況位置對不上，交給 OpenCC）。
func (s *Session) readingPins(text string) map[int]string {
	if s.Simplified == nil || len(s.path) == 0 {
		return nil
	}
	var pins map[int]string
	var joined strings.Builder
	at := 0
	resolved, _ := s.resolvedSyllables() // 沒打聲調的音節用詞庫讀音（#150）；關閉時就是 s.syls
	for _, g := range s.path {
		runes := []rune(g.Word)
		joined.WriteString(g.Word)
		reading := resolved[g.Start:min(g.End, len(resolved))]
		if g.Matched != "" {
			reading = zhuyin.ParseReading(g.Matched) // 近音到達的詞，用詞庫的讀音
		}
		if !s.literals[g.Start] && len(reading) == len(runes) {
			for k, c := range runes {
				if out, ok := zhconv.Pin(c, string(reading[k])); ok {
					if pins == nil {
						pins = map[int]string{}
					}
					pins[at+k] = out
				}
			}
		}
		at += len(runes)
	}
	if joined.String() != text {
		return nil
	}
	return pins
}

// output 是真正送出的文字：開啟「輸出簡體字」時轉成簡體。shown 是補標點前的文字（pins 的位置以它為準），
// text 是補過標點的文字；補標點只在兩個漢字之間插入，原本的字在 text 裡依序都還在。
func (s *Session) output(shown, text string, pins map[int]string) string {
	if s.Simplified == nil || text == "" {
		return text
	}
	if len(pins) > 0 && shown != text {
		moved := map[int]string{}
		a, b := []rune(shown), []rune(text)
		j := 0
		for i := range a {
			for j < len(b) && b[j] != a[i] {
				j++
			}
			if j == len(b) {
				moved = nil
				break
			}
			if p, ok := pins[i]; ok {
				moved[j] = p
			}
			j++
		}
		pins = moved
	}
	return s.Simplified.ConvertPinned(text, pins)
}

// punctuate 在提交時補標點。學習已依原路徑完成，插入的標點只影響送出的文字與之後的前文。
// 評分器失敗或回傳不合規的編輯時，原文照送。
func (s *Session) punctuate(text string) string {
	if s.Punct == nil || text == "" || s.private || s.paused || s.english {
		return text
	}
	edits, err := s.Punct.Punctuate(text)
	if err != nil {
		return text
	}
	out, _ := punct.Apply(text, edits)
	return out
}

func (s *Session) reset() {
	s.styleCycle = nil
	s.englishWord, s.englishDismissed, s.englishTokenBlocked = false, false, false
	s.paletteUndo = nil
	s.symbolPrefix = false
	s.styleGrid = false
	s.mark = nil
	s.phonetic, s.phoneticUndo = nil, nil
	s.phoneticCache = phoneticHintCache{}
	s.paletteCommit = false
	s.clearMixed()
	s.mixedKeys = nil
	s.mixedOrigins = nil
	s.pendingToneKey, s.literalKeys = nil, nil
	s.clearDocumentContext()
	s.rerankCache = nil
	s.hint = nil
	s.shownPath, s.shownSyls = nil, nil
	s.previewRewrite = false
	s.private = false
	s.localBlocked = false
	s.comp.Reset()
	s.syls, s.cursor, s.locks, s.rejected, s.cand = nil, 0, map[int]decode.Lock{}, nil, nil
	s.literals = nil
	s.pendingLiterals = nil
	s.displayed = nil
	s.nearReading = false
	s.autoLog = nil
}

func (s *Session) snapshot() proto.Snapshot {
	snap := proto.Snapshot{FullWidth: s.fullWidth, Revision: s.rev, EnglishAutocomplete: s.EnglishAutocomplete, EnglishPending: s.englishWord, EnglishCanStart: s.englishCanStart()}
	pending := s.comp.String()
	if s.symbolPrefix {
		pending += "`"
	}
	if s.mixed.raw != "" {
		pending = s.mixedText()
	}
	split := pending != ""
	// 中英混打照字面顯示、按空白會組成中文的數字按鍵（#162）：那段組字以 StyleReading 畫，另附讀音提示。
	hintAt, hintSyl, hinted := s.mixedSpaceReading()
	pendingSegments := func() []proto.Segment {
		if !hinted {
			return []proto.Segment{{Text: pending, Style: proto.StylePending}}
		}
		var out []proto.Segment
		if hintAt > 0 {
			out = append(out, proto.Segment{Text: pending[:hintAt], Style: proto.StylePending})
		}
		return append(out, proto.Segment{Text: pending[hintAt:], Style: proto.StyleReading})
	}
	for _, g := range s.path {
		style := proto.StyleNormal
		switch l, locked := s.locks[g.Start]; {
		case locked && l.Word == g.Word && l.LiteralBoundary == 0:
			style = proto.StyleLocked
		case g.Correction || g.Matched != "":
			style = proto.StyleCorrected
		case g.Partial:
			style = proto.StylePartial
		}
		runes := []rune(g.Word)
		if split && g.Start <= s.cursor && s.cursor < g.End {
			k := s.cursor - g.Start
			if k > 0 {
				snap.Preedit = append(snap.Preedit, proto.Segment{Text: string(runes[:k]), Style: style})
			}
			snap.Preedit = append(snap.Preedit, pendingSegments()...)
			snap.Preedit = append(snap.Preedit, proto.Segment{Text: string(runes[k:]), Style: style})
			split = false
			continue
		}
		snap.Preedit = append(snap.Preedit, proto.Segment{Text: g.Word, Style: style})
	}
	before := s.cursor // 游標之前的字數
	if len(s.path) == 0 && len(s.syls) > 0 {
		// 保底：英數切開詞條時保留已顯示漢字，其餘無法解碼的音節顯示原樣注音。
		// 組字區絕不空白，游標依實際顯示字數計算。
		before = 0
		for i, y := range s.syls {
			text, style := s.fallbackDisplay(i, y)
			if split && i == s.cursor {
				snap.Preedit = append(snap.Preedit, pendingSegments()...)
				split = false
			}
			if i < s.cursor {
				before += len([]rune(text))
			}
			snap.Preedit = append(snap.Preedit, proto.Segment{Text: text, Style: style})
		}
	}
	if split {
		snap.Preedit = append(snap.Preedit, pendingSegments()...)
	}
	if start, end := s.markRange(); start < end && pending == "" {
		snap.Preedit = markSegments(snap.Preedit, start, end) // 沒有未完成注音時，音節與字元一一對應
	}
	// 已解碼音節與字面符號各占一個字元，游標在未完成注音之後。
	runes := []rune(snap.Text())
	snap.Cursor.Rune = min(before+len([]rune(pending)), len(runes))
	snap.Cursor.UTF16 = len(utf16.Encode(runes[:snap.Cursor.Rune]))
	if hinted {
		length := len([]rune(pending)) - hintAt
		snap.ReadingHint = &proto.ReadingHint{Start: snap.Cursor.Rune - length, Length: length, Text: string(hintSyl)}
	}
	if h := s.phonetic; h != nil {
		start := len([]rune(snap.Text())) - len([]rune(h.replace))
		snap.Suggestion = h.replace + " → " + h.entry.Surface
		snap.SuggestionEdit = &proto.SuggestionEdit{Start: start, Length: len([]rune(h.replace)), Text: h.entry.Surface}
	} else if s.sug != nil {
		snap.Suggestion = s.sug.Rest
		if s.sug.Replace != "" {
			// 前方可能含多字元原樣注音，不能直接把音節位置當顯示字元位置。
			start := len([]rune(snap.Text())) - len([]rune(s.sug.Replace))
			snap.Suggestion = s.sug.Replace + " → " + s.sug.Word
			snap.SuggestionEdit = &proto.SuggestionEdit{Start: start, Length: len([]rune(s.sug.Replace)), Text: s.sug.Word}
		}
	} else if h := s.hint; h != nil && s.mixed.raw == "" && s.cursor == len(s.syls) && s.comp.Empty() && s.cand == nil {
		// 神經次佳候選：與既有同音替換提示相同的呈現，按 Tab 才接受。
		start := len([]rune(snap.Text())) - len([]rune(h.replace))
		snap.Suggestion = h.replace + " → " + h.text
		snap.SuggestionEdit = &proto.SuggestionEdit{Start: start, Length: len([]rune(h.replace)), Text: h.text}
	}
	if s.cand != nil {
		// 範圍跟著反白：混合清單反白單字時，外殼標示與標題都只指游標那個字（#161）。
		start, length := s.cand.span(s.cand.cursor)
		cl := &proto.CandidateList{Page: s.candidatePage(), Pages: s.pages(), Start: start, Length: length, Highlight: s.cand.cursor % s.pageSize(), Columns: s.candidateColumns(), Kind: s.cand.kind}
		if cl.Columns == 1 {
			cl.Columns = 0
		}
		if s.cand.english {
			cl.Title = "按 Tab 補全或修正拼字 · Esc 關閉"
		}
		if cl.Kind != "" {
			cl.Title = s.paletteTitle()
		}
		if !s.cand.literal && !s.cand.mixed && cl.Length > 0 {
			cl.Title = "修改「" + s.candidateText() + "」 · Tab 切換詞長"
		}
		// 選字鍵含 j、k（asdfghjkl;）時它們是選字，不是換字。
		cl.StepKeys = s.canStepCandidate() && s.selectionIndex("j") < 0 && s.selectionIndex("k") < 0
		lo := s.candidatePage() * s.pageSize()
		for id := lo; id < min(lo+s.pageSize(), len(s.cand.items)); id++ {
			item := proto.CandidateItem{ID: s.candidateID(id), Text: s.cand.items[id].Entry.Surface}
			if s.cand.kind == "phrases" {
				item.Text = phraseLabel(item.Text)
			}
			if id < len(s.cand.matched) {
				item.Note = nearNote(s.cand.matched[id]) // 原音項目是空字串
			}
			if e := s.cand.items[id].Entry; item.Note == "" && s.cand.kind == "" && len(e.Reading) > 0 && s.model.VocabReading(e.Surface, e.Reading) {
				item.Note = "我的詞彙" // 讓人知道這是自己加的詞、按反悔鍵會刪掉（比照 Apple 注音標「常用」）
			}
			if s.cand.keyLiterals[id] != "" {
				item.Note = "原始按鍵"
			}
			if id < len(s.cand.labels) && s.cand.keyLiterals[id] == "" {
				item.Label = s.cand.labels[id]
			}
			if !s.cand.english { // 英文補字只能按 Tab 接受（#112）
				item.Key = string(s.selectionKeys()[id-lo])
			}
			cl.Items = append(cl.Items, item)
		}
		snap.Candidates = cl
	}
	s.mixedSnapshot(&snap)
	return snap
}

// Learned 列出目前學到的個人片語，依晉升順序。
func (s *Session) Learned() []string {
	out := append([]string(nil), s.model.Store.PhraseOrder...)
	sort.Strings(out)
	return out
}
