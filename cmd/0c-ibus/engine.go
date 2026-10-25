//go:build linux

package main

// 每個 IBus 輸入欄位一個 engine：把按鍵送給 0c-server，把快照畫成組字區、提示列與候選表。
// 所有輸入法行為都在核心；這裡只做轉換與顯示（與 Fcitx5 外殼 engine.cpp 對應）。

import (
	"slices"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/godbus/dbus/v5"
)

// X keysym。
const (
	keyBackSpace   = 0xff08
	keyTab         = 0xff09
	keyReturn      = 0xff0d
	keyEscape      = 0xff1b
	keyHome        = 0xff50
	keyLeft        = 0xff51
	keyUp          = 0xff52
	keyRight       = 0xff53
	keyDown        = 0xff54
	keyPageUp      = 0xff55
	keyPageDown    = 0xff56
	keyEnd         = 0xff57
	keyKPEnter     = 0xff8d
	keyKPHome      = 0xff95
	keyKPEnd       = 0xff9c
	keyKPMultiply  = 0xffaa
	keyKPAdd       = 0xffab
	keyKPSubtract  = 0xffad
	keyKPDecimal   = 0xffae
	keyKPDivide    = 0xffaf
	keyKP0         = 0xffb0
	keyKP9         = 0xffb9
	keyKPEqual     = 0xffbd
	keyShiftL      = 0xffe1
	keyShiftR      = 0xffe2
	keyCapsLock    = 0xffe5
	keyDelete      = 0xffff
	keyF11         = 0xffc8
	keyISOLeftTab  = 0xfe20
	keySpace       = 0x20
	modifiedMask   = maskControl | maskMod1 | maskMod4 | maskSuper | maskHyper | maskMeta
	settingsLabel  = "⚙ 靈犀注音設定"
	vocabLabel     = "我的詞彙…"
	propInputMode  = "InputMode"
	propSettings   = "settings"
	propVocab      = "vocab"
	propUpdate     = "update"
	propSimplified = "simplified"
	simplifiedText = "輸出簡體字"
	recoveryNotice = "連線恢復，請確認組字後再繼續。"
)

// toKey 把 IBus 的按鍵轉成協定的 Key；不該交給輸入法的按鍵回傳空字串。
func toKey(keyval, state uint32) string {
	// 帶 Ctrl、Alt、Super 的組合鍵一律交還應用程式。
	if state&modifiedMask != 0 {
		return ""
	}
	switch keyval {
	case keyReturn, keyKPEnter:
		return proto.KeyEnter
	case keyEscape:
		return proto.KeyEscape
	case keyBackSpace:
		return proto.KeyBackspace
	case keyDelete:
		return proto.KeyDelete
	case keyTab: // Shift+Tab 送 BackTab：組字中核心吃掉不動作，沒在組字時交還應用程式（#106）
		if state&maskShift != 0 {
			return proto.KeyBackTab
		}
		return proto.KeyTab
	case keyISOLeftTab:
		return proto.KeyBackTab
	case keyLeft:
		return proto.KeyLeft
	case keyRight:
		return proto.KeyRight
	case keyHome, keyKPHome: // 組字中移到組字區最前；沒在組字時核心交還應用程式（#106）
		return proto.KeyHome
	case keyEnd, keyKPEnd:
		return proto.KeyEnd
	case keyUp:
		return proto.KeyUp
	case keyDown:
		return proto.KeyDown
	case keyPageDown:
		return proto.KeyPageDown
	case keyPageUp:
		return proto.KeyPageUp
	case keySpace:
		return " "
	}
	// 只把可見的 ASCII 交給核心；排列由核心解讀。數字鍵盤送 "KP" 加上字元，核心不當注音鍵（#75）。
	switch {
	case keyval >= 0x21 && keyval <= 0x7e:
		return string(rune(keyval))
	case keyval >= keyKP0 && keyval <= keyKP9:
		return proto.KeypadPrefix + string(rune('0'+keyval-keyKP0))
	}
	if c := map[uint32]rune{keyKPMultiply: '*', keyKPAdd: '+', keyKPSubtract: '-', keyKPDecimal: '.',
		keyKPDivide: '/', keyKPEqual: '='}[keyval]; c != 0 {
		return proto.KeypadPrefix + string(c)
	}
	return ""
}

func styleKey(key string, shift bool, supported func() bool) string {
	if key == " " && shift && supported() {
		return "ShiftSpace"
	}
	return key
}

// foldCaps：中文模式不理 Caps Lock（#168，同常見注音輸入法與 ibus-chewing 的 self_key_sym_fix），字母換回沒開 Caps Lock 時的
// 大小寫：沒按 Shift 是小寫注音鍵，Shift＋字母照舊是大寫。小麥 Caps 風格也沿用此規則；其他英文風格保留 OS 大小寫。
func foldCaps(keyval, state uint32) uint32 {
	if state&maskLock == 0 || !(keyval >= 'a' && keyval <= 'z' || keyval >= 'A' && keyval <= 'Z') {
		return keyval
	}
	if state&maskShift != 0 {
		return keyval &^ 0x20
	}
	return keyval | 0x20
}

// A lower-case Caps style uses Shift for upper-case letters even when the OS
// already inverted Caps+Shift to lower-case. Other English styles keep OS case.
func foldStyleCaps(keyval, state uint32, english, lower bool) uint32 {
	if !english || lower {
		return foldCaps(keyval, state)
	}
	return keyval
}

// ctrlPunctKey：只按著 Ctrl（可以加 Shift，沒有 Alt／Super 等）的 , . ' ; ? ! 換成協定的 Ctrl＋標點鍵名（#168）。
// keyval 是已經套用 Shift 的字元（Ctrl+Shift+/ 是 question），其他鍵回傳空字串。
func ctrlPunctKey(keyval, state uint32) string {
	if state&maskControl == 0 || state&(modifiedMask&^maskControl) != 0 {
		return ""
	}
	switch keyval {
	case ',', '.', '\'', ';', '?', '!':
		return proto.CtrlPrefix + string(rune(keyval))
	}
	return ""
}

// simplifiedHotkey：切換「輸出簡體字」的 Ctrl＋Shift＋F11（#192，settings.SimplifiedHotkeyLabel）。只按著 Ctrl 與 Shift，
// 有 Alt／Super 等其他修飾鍵就不是；Caps Lock、Num Lock 不影響。要不要攔由呼叫端看引擎回報的 simplified_hotkey。
func simplifiedHotkey(keyval, state uint32) bool {
	return keyval == keyF11 && state&(modifiedMask|maskShift) == maskControl|maskShift
}

// isModifierKey：Shift／Ctrl／Alt／Super 等修飾鍵本身（X keysym Shift_L…Hyper_R、ISO_Level3／5_Shift、Mode_switch）。
func isModifierKey(keyval uint32) bool {
	return keyval >= 0xffe1 && keyval <= 0xffee || keyval == 0xfe03 || keyval == 0xfe11 || keyval == 0xff7e
}

// vocabKey 把按著 Shift 的 ←→、Delete、Backspace 換成 0.2.0 的新鍵名（#123）：組字中 Shift＋←／→ 標示要加入詞庫的範圍，
// 候選窗的 Shift＋Delete／⌫ 刪除自訂詞或忘記學到的詞。key 是 toKey 的結果（已排除 Ctrl／Alt／Super）；
// supported 回報引擎是否認得新鍵名（open 回覆的 vocab_keys），只在真的要換名時才問。舊引擎不認得新鍵名，
// 沒回報支援時照舊送原本的鍵；新引擎在沒有對應功能時把新鍵名當原本的鍵處理，外殼不必追蹤候選窗狀態。
func vocabKey(key string, shift bool, supported func() bool) string {
	if !shift {
		return key
	}
	renamed := map[string]string{proto.KeyLeft: proto.KeyShiftLeft, proto.KeyRight: proto.KeyShiftRight,
		proto.KeyDelete: proto.KeyShiftDelete, proto.KeyBackspace: proto.KeyShiftBackspace}[key]
	if renamed == "" || !supported() {
		return key
	}
	return renamed
}

// 加詞標示範圍的反白色（GNOME 的強調藍配白字）。IBus 的前景／背景屬性值是 0xRRGGBB；
// GTK 與 Qt 的 IBus 輸入模組轉成文字顏色，不支援顏色的前端至少還有底線。
const (
	markedBackground = 0x3584e4
	markedForeground = 0xffffff
)

// 中英混打時按空白會組成中文的數字（reading，#162）畫成灰字，比照常見注音輸入法；淺色、深色主題都看得到。
// 讀音（ㄉㄢ）在提示列。
const readingForeground = 0x8a8a8a

// preeditAttrs 是一段組字 [start, end)（字元位置）的屬性。
func preeditAttrs(style string, start, end uint32) []attribute {
	switch style {
	case proto.StyleLocked: // 使用者選過的字
		return []attribute{{attrUnderline, underlineDouble, start, end}}
	case proto.StyleCorrected: // 自動修正：以不同樣式標示
		return []attribute{{attrUnderline, underlineError, start, end}}
	case proto.StyleMarked: // Shift＋←／→ 標示要加入詞庫的範圍（#123）：反白
		return []attribute{{attrUnderline, underlineSingle, start, end},
			{attrBackground, markedBackground, start, end}, {attrForeground, markedForeground, start, end}}
	case proto.StyleReading: // 按空白會組成中文的數字（#162）：灰字加底線
		return []attribute{{attrUnderline, underlineSingle, start, end}, {attrForeground, readingForeground, start, end}}
	}
	return []attribute{{attrUnderline, underlineSingle, start, end}}
}

type engine struct {
	svc     *service
	path    dbus.ObjectPath
	session *session
	shift   shiftTap
	left    shiftTap
	context documentContext
	active  bool

	caps, purpose, hints uint32
	surrounding          docText

	// 上一次送出的畫面；相同時不重送，避免候選窗閃爍。組字連分段樣式一起比，只改樣式（例如 Esc 取消加詞標示）也要重畫。
	preedit   string
	preCursor uint32
	preSegs   []proto.Segment
	aux       string
	auxRest   string // aux 去掉這次快照的提示之後的內容；切換「輸出簡體字」的提示收起後換回它（#169）
	table     []string
	tableAt   int
	ids       []int // 目前候選頁的候選 ID
	english   bool
}

func (e *engine) private() bool {
	return e.purpose == purposePassword || e.purpose == purposePIN || e.hints&hintPrivate != 0
}

// state 對應 Fcitx 外殼的 state()：每次操作前依欄位類型更新隱私標記。
func (e *engine) state() {
	e.session.setPrivate(e.private())
	if !e.session.contextAllowed() {
		e.context.clear()
	}
}

func (e *engine) document() docText {
	if !e.svc.documentContext || e.private() || e.caps&capSurroundingText == 0 ||
		!e.surrounding.valid || len(e.surrounding.text) > maxDocumentBytes {
		return docText{}
	}
	return e.surrounding
}

func (e *engine) syncContext() {
	e.state()
	if e.session.contextAllowed() {
		e.context.validate(e.document())
	} else {
		e.context.validate(docText{})
	}
	if action, ok := e.context.takeAction(); ok && !e.session.documentContext(action) {
		e.context.clear()
	}
}

func (e *engine) emit(signal string, args ...interface{}) {
	e.svc.signal(e.path, ifaceEngine+"."+signal, args...)
}

func (e *engine) processKey(keyval, keycode, state uint32) bool {
	e.state()
	release := state&maskRelease != 0
	modified := state&modifiedMask != 0
	// 另一側 Shift 還按著（左右同時按）也不算單按。
	now := e.svc.now()
	toggle := e.shift.key(keyval == keyShiftR, release, modified || e.left.down, now)
	leftToggle := e.left.key(keyval == keyShiftL, release, modified || e.shift.down, now)
	// 單按 Shift 切換注／英，哪一側依中英切換鍵設定（toggle-key：預設右 Shift，#117）；不切換的一側交給應用程式。
	// 只在確定是單按的放開時才問引擎（Shift＋字母不查詢）；按住超過門檻（預設 0.3 秒）不算切換。
	if keyval == keyShiftR || keyval == keyShiftL {
		tap, held := toggle, e.shift
		if keyval == keyShiftL {
			tap, held = leftToggle, e.left
		}
		if !tap {
			return false
		}
		k := e.session.shiftKeys()
		enabled := k.right
		if keyval == keyShiftL {
			enabled = k.left
		}
		if enabled && held.within(k.tap) {
			e.toggleMode()
		}
		// 切換了也不吃掉放開（同 ibus-chewing、RIME、Fcitx5 的 AltTriggerKeys）：GNOME Wayland 下 VS Code 等
		// Chromium／Electron 經 GNOME Shell 收鍵，只看按鍵事件，收到按下卻收不到放開就以為 Shift 一直按著（#165）。
		return false
	}
	if release {
		return false
	}
	// 用快捷鍵切換簡體字（#192）：設定開著（引擎回報 simplified_hotkey）時吃掉這個鍵，跟選單的「輸出簡體字」一樣即時切換：
	// 組字不動、提示列顯示「已切換為…」、注／英指示跟著變，之後送出的字照新設定。設定關閉或舊引擎時照一般快捷鍵交給應用程式。
	if simplifiedHotkey(keyval, state) && e.session.simplifiedHotkey() {
		e.session.toggled = false
		e.toggleSimplified()
		return true
	}
	// Caps Lock 開著時直接打英文（#173）：按鍵事件帶的是按下之前的鎖定狀態（同 RIME 的 ascii_composer），
	// Caps Lock 鍵本身要反過來算；其他按鍵照它帶的狀態，猜錯時下一個按鍵就改正。不讀 LED（Wayland 讀不到）。
	if keyval == keyCapsLock {
		e.syncCaps(state&maskLock == 0, false)
	} else {
		e.syncCaps(state&maskLock != 0, true)
	}
	lower := e.session.english && state&maskLock != 0 && e.session.styleCapsLower()
	keyval = foldStyleCaps(keyval, state, e.session.english, lower)
	if !e.session.english {
		// Ctrl＋標點打全形（#168）：只在中文模式、引擎回報 ctrl_punctuation（認得鍵名而且設定開著）時送；
		// 引擎沒處理（設定剛關掉、模式不同步）就照下面的一般快捷鍵先送出組字再交給應用程式。
		if k := ctrlPunctKey(keyval, state); k != "" && e.session.ctrlPunct() {
			e.syncContext()
			snap := e.session.key(k)
			if snap.ok && snap.Handled {
				e.render(snap)
				return true
			}
		}
	}
	key := toKey(keyval, state)
	key = styleKey(key, state&maskShift != 0, e.session.styleShiftSpace)
	// 按著 Shift 的 ←→、Delete、Backspace：引擎回報支援時送 0.2.0 的新鍵名（#123）。
	key = vocabKey(key, state&maskShift != 0, e.session.vocabKeys)
	if key == "" {
		e.session.toggled = false // 交還應用程式的快捷鍵也算切換之後的操作（#90）
		boundary := ""
		switch {
		case keyval == keyHome || keyval == keyKPHome:
			boundary = "Home"
		case keyval == keyEnd || keyval == keyKPEnd:
			boundary = "End"
		case keyval == keyISOLeftTab || keyval == keyTab && state&maskShift != 0:
			boundary = "BackTab"
		}
		// 組合鍵（單按修飾鍵本身除外）先送出組字，再交給應用程式（#106，同 macOS）。
		if modified && !isModifierKey(keyval) {
			if snap := e.session.commitForShortcut(); snap.ok {
				e.render(snap)
			}
		}
		if snap := e.session.bypassEnglish(boundary); snap.ok {
			e.render(snap)
		}
		e.context.clear()
		e.syncContext()
		return false
	}
	e.syncContext()
	snap := e.session.key(key)
	if snap.reconnected || !snap.Handled || !snap.ok {
		e.context.clear()
	}
	if !snap.ok { // server 不可用：放行按鍵，不得卡住應用程式
		return false
	}
	e.render(snap) // 先送出提交的文字，再決定是否把按鍵交還應用程式，順序才不會顛倒
	return snap.Handled
}

// syncCaps 把 Caps Lock 狀態告訴引擎（#173）：只在引擎回報 caps_lock_english（認得 caps_lock、設定開著），
// 而且跟引擎記得的狀態不同時才送；開啟時引擎先送出組字、切到英文，關閉時回到原本的模式。
func (e *engine) syncCaps(on, spawn bool) {
	if on == e.session.capsLock || !e.session.capsLockEnglish(spawn) {
		return
	}
	e.context.clear()
	e.syncContext()
	if snap := e.session.handle(proto.Request{Type: proto.CapsLock, On: on}); snap.ok {
		e.render(snap)
	}
}

// toggleMode 切換注／英；引擎不可用時不切換。
func (e *engine) toggleMode() {
	e.context.clear()
	e.syncContext()
	if snap := e.session.toggle(); snap.ok {
		e.render(snap)
	}
}

// selectCandidate 是滑鼠點選；index 是目前這一頁的位置。
func (e *engine) selectCandidate(index uint32) {
	if int(index) >= len(e.ids) {
		return
	}
	e.state()
	e.syncContext()
	e.render(e.session.handle(proto.Request{Type: proto.Select, Index: e.ids[index]}))
}

// key 送出導覽鍵（候選窗的上下頁按鈕）。
func (e *engine) navigate(key string) {
	e.state()
	e.syncContext()
	if snap := e.session.key(key); snap.ok {
		e.render(snap)
	}
}

func (e *engine) enable() {
	e.shift.reset()
	e.left.reset()
	e.active = true
	e.context.clear()
	e.syncContext()
	if snap := e.session.activate(); snap.ok {
		e.render(snap)
	}
}

// finish 是失焦、重設與停用：ibus-daemon 已依 preeditCommit 把畫面上的組字提交給應用程式，
// 這裡只讓核心完成同一段組字（學習並清除），不再送出提交，避免重複。
func (e *engine) finish(reason string) {
	e.shift.reset()
	e.left.reset()
	e.session.toggled = false // 失焦／重設後切回不放回注音；全域 engine 也不能把注音帶到別的視窗（#90）
	e.context.clear()
	e.syncContext()
	if len(e.session.log) > 0 || e.session.recovered != nil || e.preedit != "" || len(e.table) > 0 {
		e.session.handle(proto.Request{Type: proto.Commit})
	}
	e.preedit, e.preCursor, e.preSegs, e.ids = "", 0, nil, nil
	e.clearPanel()
	if reason == "disable" {
		e.active = false
	}
}

func (e *engine) clearPanel() {
	e.emit("UpdatePreeditText", newText(""), uint32(0), false, uint32(preeditCommit))
	e.setAux("")
	e.setTable(nil, nil, 0)
}

func (e *engine) setAux(s string) {
	if s == e.aux {
		return
	}
	e.aux = s
	e.emit("UpdateAuxiliaryText", newText(s), s != "")
}

func (e *engine) setTable(items, labels []string, cursor int) {
	if equalStrings(items, e.table) && cursor == e.tableAt {
		return
	}
	e.table, e.tableAt = items, cursor
	e.emit("UpdateLookupTable", newLookupTable(items, labels, cursor, e.session != nil && e.session.info["candidate_layout"] == "horizontal"), len(items) > 0)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (e *engine) render(snap snapshot) {
	if !snap.ok {
		return
	}
	if snap.reconnected {
		e.context.clear()
	}
	text := snap.Text()
	cursor := uint32(min(max(snap.Cursor.Rune, 0), utf8.RuneCountInString(text)))
	if snap.Commit != "" {
		// 先清掉畫面上的組字再提交：preeditCommit 模式下留著組字，用戶端可能在提交時再送出一次。
		if e.preedit != "" {
			e.emit("UpdatePreeditText", newText(""), uint32(0), false, uint32(preeditCommit))
			e.preedit, e.preCursor, e.preSegs = "", 0, nil
		}
		before := docText{}
		if e.session.contextAllowed() {
			before = e.document()
		}
		e.context.committed(before, snap.Commit, snap.ContextID)
		e.emit("CommitText", newText(snap.Commit))
	}
	if text != e.preedit || cursor != e.preCursor || !slices.Equal(snap.Preedit, e.preSegs) {
		var attrs []attribute
		at := uint32(0)
		for _, g := range snap.Preedit {
			n := uint32(utf8.RuneCountInString(g.Text))
			if n > 0 {
				attrs = append(attrs, preeditAttrs(g.Style, at, at+n)...)
			}
			at += n
		}
		e.emit("UpdatePreeditText", newText(text, attrs...), cursor, text != "", uint32(preeditCommit))
		e.preedit, e.preCursor, e.preSegs = text, cursor, slices.Clone(snap.Preedit)
	}
	// 通知（例如候選窗的 Shift＋Delete 之後「已從詞庫刪除：…」）是使用者剛做的動作的回饋，只出現在這一次快照，
	// 優先於候選標題；標題在下一個按鍵就回來。兩者接在同一列會太長，GNOME 的提示列也不換行。
	// 讀音提示（中英混打時數字按空白會組成的注音，#162）只有讀音本身（ㄉㄢ），比照常見注音輸入法在數字下方附注音；
	// 開候選時核心不送，實際上也不會和補全提示同時出現。
	// rest 是沒有通知時的提示列；切換「輸出簡體字」的提示收起後換回它（#169）。頁碼兩種都帶。
	rest, page := "", ""
	if h := snap.ReadingHint; h != nil && h.Text != "" {
		rest = h.Text
	} else if snap.Suggestion != "" { // Tab 補全：在提示列顯示，不塞進應用程式的組字區
		rest = "⇥ " + snap.Suggestion
	}
	var items, labels []string
	var ids []int
	highlight := 0
	if c := snap.candidates(); len(c) > 0 {
		if snap.Candidates.Title != "" {
			rest = snap.Candidates.Title
		}
		if snap.Candidates.Pages > 1 {
			page = strconv.Itoa(snap.Candidates.Page+1) + "/" + strconv.Itoa(snap.Candidates.Pages)
		}
		for i, it := range c {
			text := it.Text
			if it.Note != "" { // 低調附註（模糊音近音為「近音 ㄓ」）
				text += "  " + it.Note
			}
			items = append(items, text)
			ids = append(ids, it.ID)
			labels = append(labels, candidateLabel(it, i, snap.english()))
		}
		highlight = snap.Candidates.Highlight
	}
	e.ids = ids
	e.auxRest = joinAux(rest, page)
	aux := e.auxRest
	if len(snap.Notices) > 0 {
		aux = joinAux(snap.Notices[0], page)
	}
	e.setAux(aux)
	e.setTable(items, labels, highlight)
	e.updateMode(snap.english())
	// 「輸出簡體字」在設定程式或別的外殼切換後（#169），下一個快照就跟上；只信回報即時切換的引擎（舊引擎沒有這一項）。
	if e.session.simplifiedLive() {
		e.svc.setSimplified(snap.OutputSimplified, true)
	}
}

// joinAux 把提示列的兩段接起來，中間隔一個全形空白；空的那段省略。
func joinAux(a, b string) string {
	if a == "" || b == "" {
		return a + b
	}
	return a + "　" + b
}

// simplifiedNoticeFor 是切換「輸出簡體字」的提示留在提示列上的時間；之後換回原本的內容（測試時縮短）。
var simplifiedNoticeFor = 3 * time.Second

// toggleSimplified 是選單的「輸出簡體字」（#169）：引擎能即時切換時直接在這個輸入框的 session 送請求，組字不動、
// 之後送出的字立刻照新設定，引擎寫設定檔；提示「已切換為輸出簡體字／繁體字」在提示列顯示幾秒。
// 舊引擎退回 0c-update set output-simplified … -apply -notify（寫設定、交接重啟，0c-update 送桌面通知）。
func (e *engine) toggleSimplified() {
	on := !e.svc.simplified
	if e.session.ensureLive() {
		if snap := e.session.outputSimplified(on); snap.ok && snap.Handled {
			e.svc.simplifiedSeq++ // 切換前開始的狀態查詢不得把勾選改回去
			e.render(snap)
			e.svc.setSimplified(snap.OutputSimplified, true)
			if len(snap.Notices) > 0 {
				shown := e.aux // 提示（候選有多頁時後面接著頁碼）；之後的按鍵換掉了就不動
				time.AfterFunc(simplifiedNoticeFor, func() {
					e.svc.mu.Lock()
					defer e.svc.mu.Unlock()
					if e.aux == shown {
						e.setAux(e.auxRest)
					}
				})
			}
			return
		}
	}
	e.svc.queryStatus(simplifiedCommand(on))
}

// candidateLabel 是候選第 i 項的鍵標（#112）：核心給的選字鍵（1、q、;）；舊核心沒有時依位置 1–0。
// 英文模式只有自動補字候選（按 Tab 接受、數字是文字），不畫鍵標；給空白而不是空字串，
// 因為 GNOME Shell 的候選窗遇到空字串會改畫預設的 1–0。
func candidateLabel(it proto.CandidateItem, i int, english bool) string {
	switch {
	case it.Key != "":
		return it.Key
	case english:
		return " "
	}
	return strconv.Itoa((i + 1) % 10)
}

func (e *engine) modeProperty() property {
	// 說明目前的中英切換鍵（toggle-key，#117）；設為不用時只寫模式，點這一項切換。
	hint := map[string]string{"": "右 Shift", "right": "右 Shift", "both": "Shift", "left": "左 Shift"}[e.session.toggleKey()]
	p := property{key: propInputMode, typ: propNormal, sensitive: true, symbol: "注", label: "注音"}
	if e.english {
		p.symbol, p.label = "英", "英文"
	} else if e.svc.simplified {
		// #169：送出時轉成簡體，誤開時從頂列看得出來。GNOME Shell 的頂列只採用不到 3 個字的 symbol（keyboard.js
		// 的 _propertyUpdated），「注·簡」會被丟掉、照舊顯示「注」，所以用兩個字。
		p.symbol, p.label = "注簡", "注音・簡體輸出"
	}
	if hint != "" {
		other := "英文"
		if e.english {
			other = "注音"
		}
		p.label += "（" + hint + " 切換" + other + "）"
	}
	return p
}

func (e *engine) updateMode(english bool) {
	if english == e.english {
		return
	}
	e.english = english
	if e.svc.focused == e {
		e.emit("UpdateProperty", e.modeProperty().variant())
	}
}

// properties 是輸入法選單的內容：注／英、設定、我的詞彙、輸出簡體字、更新狀態。「我的詞彙…」與「輸出簡體字」一律登錄，
// 只用 visible 跟著 features.vocab 與這項設定是否已知顯示或隱藏（見 refreshMenu）。
func (e *engine) properties() []property {
	return []property{e.modeProperty(), e.svc.settingsProperty(), e.svc.vocabProperty(), e.svc.simplifiedProperty(), e.svc.updateProperty()}
}

func (e *engine) registerProperties() {
	e.emit("RegisterProperties", newPropList(e.properties()))
}
