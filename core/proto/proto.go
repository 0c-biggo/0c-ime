// Package proto 定義核心與平台外殼之間的訊息協定。
//
// 請求是語義事件；回應一律是完整快照，外殼不需要保存狀態。v0 以 JSON 編碼。
package proto

import "strings"

// Version 是協定版本。
const Version = 0

// 請求類型。
const (
	Context    = "context"    // On：確認最近提交的文件位置；ContextID 必須相符。false 清除前文
	Key        = "key"        // 桌面：實體按鍵，排列由核心解讀。Key 為單一字元或具名鍵
	Symbol     = "symbol"     // 觸控：直接送注音符號，不假造按鍵
	Backspace  = "backspace"  //
	Cursor     = "cursor"     // To：組字位置（每音節或字面符號各占一格）
	Candidates = "candidates" // 開啟候選；Length 可指定詞長，0 表示由核心決定
	Page       = "page"       // Index：頁碼
	Select     = "select"     // Index：候選的 ID
	Close      = "close"      // 關閉候選，保留組字
	Accept     = "accept"     // 接受 Tab 補全
	Complete   = "complete"   // Word：接受補全（觸控：候選列上的補全詞）；未打完的注音先不打聲調就完成，補全仍是 Word 才接受
	Commit     = "commit"     // 外殼替使用者結束組字（失焦、換輸入法等）：送出已組好的字，未打完的注音不送（#322）；帶 Word（觸控）時送出外殼畫面上的這句，解出來不同時照這串字重新斷詞，對不齊就不送（Handled false）
	Cancel     = "cancel"     // 放棄組字，不學習
	Forget     = "forget"     // Word：撤銷與該詞有關的學習
	Fix        = "fix"        // To、Length、Word：把從 To 起 Length 個音節直接固定成 Word，不必先開候選（觸控從句首清單點詞）
	Literal    = "literal"    // Length、Word：把組字尾端最後 Length 個打的音節（原樣留下的注音算一個）連同未打完的注音換成字面文字 Word，不核對讀音；之後游標在這段文字後面時退格還原成原本的注音（觸控：點注音鍵拼出的英文字，APP#33）
	Predict    = "predict"    // 沒在組字時，列出已確認前文之後最可能接的詞（觸控的聯想詞）；以 select 送出
	Unlearn    = "unlearn"    // Word：撤銷與該詞有關的學習，不留 tombstone，之後照常可以再學（觸控：長按學過的候選）
	UndoLearn  = "undo_learn" // 還原上一次提交（或點聯想詞）學到的內容，不留 tombstone（觸控：送出後立刻退格）；只能還原一次
	Pause      = "pause"      // On：暫停／恢復學習
	Mode       = "mode"       // On：設定中英模式（true 為英文）；外殼重連後用它還原模式
	Toggle     = "toggle"     // 中英模式切換（單按 Shift）；Restore 見 Request
	Display    = "display"    // 外殼可呈現的欄數；1 表示原生直排。
	// CapsLock（#173）：On 為按鍵事件帶的 Caps Lock 鎖定狀態。設定「Caps Lock 開著時直接打英文」開著時，
	// 開啟先送出組字、暫時切到英文，關閉回到原本的模式；設定關閉時回 handled=false、什麼都不改。
	// 外殼只在引擎回報 caps_lock_english=true、而且狀態跟快照的 CapsLock 不同時送（Windows、Linux）。
	CapsLock = "caps_lock"
	// OutputSimplified（#169）：On 為「輸出簡體字」要開或關。輸入法選單的開關送這個請求，引擎立即改變之後送出的
	// 文字（所有輸入框，包括已經在組字區、還沒送出的字），並寫進設定檔；組字不變、不重啟引擎。快照的 Notices
	// 帶「已切換為輸出簡體字／繁體字」，OutputSimplified 是切換後的狀態。外殼只在 health／open 回報
	// output_simplified_live=true 時送；引擎不支援即時切換時回 handled=false、什麼都不改。
	// 這是全域設定，不屬於組字：外殼不把它記進斷線重播的紀錄。
	OutputSimplified = "output_simplified"
	// NineReading（注音九宮格，觸控）：To、Word。把一個九宮格音節固定成讀音 Word（快照 nine_readings.readings 的一項），
	// 移除其他讀音（左欄「注音選擇」）。To 是快照 nine_readings.at：pending 時（To 等於游標、還有沒完成的鍵）固定的是那串鍵
	// 的拼法，之後照常按聲調鍵完成；否則是第 To 個已完成的音節，蓋到它的手選解除、重新解碼。Word 不是那個位置的讀音時
	// 什麼都不改，Notices 帶「沒有這個讀音」。
	// 自動切字（引擎的 NineSegment）時另有 Length：這個拼法佔幾顆鍵（快照 nine_readings.keys 的那一項），0 是目前的鍵數。
	// 效果是固定第 To 個音節的切點與拼法（nine_segments 的 fixed），後面的鍵重新切；拼法與鍵數對不上時同樣不改。
	NineReading = "nine_reading"
)

// 輸入模式。
const (
	ModeZhuyin  = "zhuyin"
	ModeEnglish = "english"
)

// 具名鍵。
const (
	KeyEnter      = "Enter"
	KeyEscape     = "Escape"
	KeyBackspace  = "Backspace"
	KeyDelete     = "Delete"
	KeyTab        = "Tab"
	KeyShiftSpace = "ShiftSpace"
	KeyBackTab    = "BackTab" // Shift+Tab；組字中吃掉不動作
	KeyLeft       = "Left"
	KeyRight      = "Right"
	KeyUp         = "Up"
	KeyDown       = "Down"
	KeyPageDown   = "PageDown"
	KeyPageUp     = "PageUp"
	KeyHome       = "Home" // 組字中移到組字區最前；候選開啟時到第一個候選
	KeyEnd        = "End"  // 組字中移到組字區最後；候選開啟時到最後一個候選
	// 0.2.0（#123）：外殼只在引擎回報 vocab_keys=true 時送這些鍵；引擎沒有開放這些功能（Preview02 為零值）時回 handled=false。
	KeyShiftLeft      = "ShiftLeft"      // 組字中往左標示要加入詞庫的範圍
	KeyShiftRight     = "ShiftRight"     // 組字中往右標示
	KeyShiftDelete    = "ShiftDelete"    // 候選窗的反悔鍵：刪除自訂詞或忘記學到的詞
	KeyShiftBackspace = "ShiftBackspace" // 同上（Mac 筆電沒有獨立的 Delete 鍵）
)

// Ctrl＋標點（#168）：中文模式按 Ctrl＋, 打「，」等全形標點。Key 為 CtrlPrefix 加上不按 Ctrl 時這一鍵產生的字元
// （Ctrl+Shift+/ 是 "Ctrl+?"、Ctrl+Shift+1 是 "Ctrl+!"）。外殼只在引擎回報 ctrl_punctuation=true、而且在中文模式時
// 送這些鍵，其他 Ctrl 組合鍵照舊交給應用程式；引擎在設定關閉或英文模式時回 handled=false，外殼照一般快捷鍵處理
// （先送出組字再交給應用程式，#106）。
//
// #231 加了 Ctrl+Shift+; ( ) 的 "Ctrl+:"、"Ctrl+("、"Ctrl+)"。舊引擎不認得，外殼只在引擎的 ctrl_punctuation_keys
// （CtrlPunctKeys）列了這個字元時才送；最早的六個鍵只看 ctrl_punctuation（舊引擎沒有 ctrl_punctuation_keys）。
const (
	CtrlPrefix       = "Ctrl+"
	KeyCtrlComma     = "Ctrl+," // ，
	KeyCtrlPeriod    = "Ctrl+." // 。
	KeyCtrlQuote     = "Ctrl+'" // 、
	KeyCtrlSemicolon = "Ctrl+;" // ；
	KeyCtrlQuestion  = "Ctrl+?" // ？（Ctrl+Shift+/）
	KeyCtrlExclaim   = "Ctrl+!" // ！（Ctrl+Shift+1）
	// #231
	KeyCtrlColon      = "Ctrl+:" // ：（Ctrl+Shift+;）
	KeyCtrlParenLeft  = "Ctrl+(" // （（Ctrl+Shift+9）
	KeyCtrlParenRight = "Ctrl+)" // ）（Ctrl+Shift+0）
)

// CtrlPunctKeys 是引擎認得的 Ctrl＋標點鍵（去掉 CtrlPrefix 的字元），在 health 與 open 的 info 以 ctrl_punctuation_keys 回報。
const CtrlPunctKeys = ",.';?!:()"

// CtrlPunct 回傳 Ctrl＋標點鍵打出的全形標點；不是 CtrlPunctKeys 裡的鍵回傳 false。
func CtrlPunct(k string) (string, bool) {
	p, ok := map[string]string{KeyCtrlComma: "，", KeyCtrlPeriod: "。", KeyCtrlQuote: "、",
		KeyCtrlSemicolon: "；", KeyCtrlQuestion: "？", KeyCtrlExclaim: "！",
		KeyCtrlColon: "：", KeyCtrlParenLeft: "（", KeyCtrlParenRight: "）"}[k]
	return p, ok
}

// KeypadPrefix 標示數字鍵盤（NumLock 開啟時的數字與 . + - * / =）：Key 為前綴加上該鍵產生的字元，
// 例如 "KP1"、"KP."。核心不把它當注音鍵；沒在組字時交還應用程式，由系統送出原本的字元。
const KeypadPrefix = "KP"

// KeypadChar 解出數字鍵盤按鍵產生的字元；不是數字鍵盤回傳 false。
func KeypadChar(k string) (rune, bool) {
	rest, ok := strings.CutPrefix(k, KeypadPrefix)
	if !ok {
		return 0, false
	}
	r := []rune(rest)
	if len(r) != 1 || r[0] < 0x21 || r[0] > 0x7e {
		return 0, false
	}
	return r[0], true
}

type Request struct {
	ContextEnabled bool   `json:"context_enabled,omitempty"` // 外殼具備文件位置確認能力
	ContextID      uint64 `json:"context_id,omitempty"`
	Type           string `json:"type"`
	Key            string `json:"key,omitempty"`
	Symbol         string `json:"symbol,omitempty"`
	Index          int    `json:"index,omitempty"`
	Length         int    `json:"length,omitempty"`
	To             int    `json:"to,omitempty"`
	Word           string `json:"word,omitempty"`
	On             bool   `json:"on,omitempty"`
	Private        bool   `json:"private,omitempty"` // 平台標示密碼／敏感欄位；本次組字不學習
	Columns        int    `json:"columns,omitempty"`
	// Restore 只用於 Toggle：外殼確認上一次切換之後沒有任何按鍵或其他操作（防誤觸的立即切回），
	// 核心才把上次切到英文時清掉的未打完注音放回。舊外殼不送，切回時不還原。
	Restore bool `json:"restore,omitempty"`
	// Near（觸控鍵盤，只用於 Symbol）：這次點按也可能是想按的鄰鍵，Cost 是比打到的鍵多出的負對數機率，
	// 由外殼依觸控位置的空間模型算出。核心在音節格放入換成鄰鍵的讀音，
	// 打的鍵接不上音節時也可改用鄰鍵；沒有 Near 時行為與沒有此功能時相同。
	Near []NearKey `json:"near,omitempty"`
	// Group（注音九宮格，只用於 Symbol）：這顆鍵代表的一組注音，例如 "ㄇㄋㄜㄝ"（三星式分組）；這時 Symbol 不看、Near 不用。
	// 鍵先存著，按聲調鍵（Group 全是聲調符號，例如 "ˊˇˋ˙"：音節結束、聲調未指定）、空白（Symbol "ˉ"，一聲）
	// 或指定聲調（Symbol 是單一聲調符號）時，核心把每顆鍵各取一個注音組成合法音節，全部放進音節格由解碼依前後文挑。
	// 有沒完成的九宮格鍵時，不帶 Group 的單一注音（長按選的）當成只有那個注音的一顆鍵。
	// 沒有 Group（也沒有沒完成的九宮格鍵）時行為與沒有此功能時相同。
	//
	// 自動切字（引擎的 NineSegment）時鍵一律接上、不必按聲調鍵：接不上目前音節的鍵由核心切成新的音節，
	// 聲調鍵是錨點（音節在那裡結束；只有一個聲調時那個音節帶那個聲調），Backspace 一次刪一顆鍵（先刪錨點）。
	Group string `json:"group,omitempty"`
	// DigitBefore（#337）：外殼讀到的游標前一個字是不是阿拉伯數字；nil 是外殼沒讀或讀不到。核心只在沒有組字時看，
	// 記成選字模型的一個位元（見 rerank.Request.DigitBefore），不記數字本身。外殼看不到英數模式的按鍵時才需要送
	// （Windows TSF 在開始組字的第一個按鍵讀）；其他外殼的英數按鍵都經過核心，由核心自己記。
	DigitBefore *bool `json:"digit_before,omitempty"`
}

// NearKey 是一次點按的鄰鍵假設。
type NearKey struct {
	Symbol string  `json:"symbol"`
	Cost   float64 `json:"cost"`
}

// 組字區分段的樣式。
const (
	StyleNormal    = "normal"
	StyleLocked    = "locked"    // 使用者選過；永不自動更改
	StyleCorrected = "corrected" // 輸入法自動修正了讀音；以低調樣式標示
	StylePartial   = "partial"   // 尾端：某個更長的詞的前綴
	StylePending   = "pending"   // 尚未完成的注音符號
	StyleMarked    = "marked"    // 組字中用 Shift＋←／→ 標示、要加入詞庫的範圍（#123）
	// StyleReading 是中英混打時照字面顯示、按空白會組成中文的數字按鍵（#162，見 ReadingHint）。
	// 外殼以低調的樣式（灰字、點線）畫；舊外殼不認得時當一般組字。
	StyleReading = "reading"
)

type Segment struct {
	Text  string `json:"text"`
	Style string `json:"style"`
	// Unfinished 標出外殼替使用者結束組字（commit 請求：失焦、切到別的輸入法）時不照畫面送出的那段：
	// 未打完的注音（含中英混打顯示成注音的暫存按鍵）會丟掉，聲調選填／九宮格已是完整音節的會換成解碼的字（#322）。
	// 由應用程式或框架自己把畫面上的組字送進文件的平台（Fcitx5 的用戶端組字、IBus 的 COMMIT 模式）
	// 靠它不把這段帶進文件。同樣是 pending 樣式、照畫面送出的保底注音與英文暫存按鍵不標。舊核心沒有這一項。
	Unfinished bool `json:"unfinished,omitempty"`
}

// CursorPos 同時以兩種單位給出游標在組字區文字中的位置，外殼不自行換算。
type CursorPos struct {
	Rune  int `json:"rune"`
	UTF16 int `json:"utf16"`
}

type CandidateItem struct {
	ID   int    `json:"id"`
	Text string `json:"text"`
	// Note 是低調的附註；模糊音近音候選為「近音 ㄓ ㄕˋ」（詞庫的對應讀音）。沒有附註時省略。
	Note string `json:"note,omitempty"`
	// Label 是候選的顯示原因，不參與排序；未知值可忽略。近音候選優先顯示 Note。
	Label string `json:"label,omitempty"`
	// Key 是這一項的選字鍵（#112），外殼照著畫成鍵標（1、q、;）。預設是 1–0；使用者可改成 1234qweras
	// 或 asdfghjkl;，這時數字 1–0 仍照位置選字。英文模式的自動補字候選只能按 Tab 接受，沒有 Key、不畫鍵標。
	// 舊核心沒有這個欄位：外殼退回依位置畫 1–0（英文模式不畫）。
	Key string `json:"key,omitempty"`
}

const (
	CandidateCommon   = "common"
	CandidateRelated  = "related"
	CandidatePersonal = "personal"
	// CandidateCorrected：這個候選的讀音是把某個按鍵換成鄰鍵才得到的（觸控的相鄰鍵容錯）。外殼把它排在
	// 打的音本身的候選後面，不當成一般候選的長短順序。
	CandidateCorrected = "corrected"
	// CandidateReading：這個候選是打的音本身照字面寫出來（單字清單結尾的原樣注音，例如 ㄏㄠˇ；拼音排列是拼音），
	// 不是字。選了照字面留在組字裡、不學習。手機外殼靠它排候選，不必自己猜「整個都是注音符號」（#361）；
	// 桌面外殼照舊把它當最後一項，不認得的標示略過。舊核心沒有這一項。
	CandidateReading = "reading"
)

type CandidateList struct {
	Items  []CandidateItem `json:"items"` // 目前這一頁
	Page   int             `json:"page"`
	Pages  int             `json:"pages"`
	Start  int             `json:"start"`  // 候選對應的組字起點
	Length int             `json:"length"` // 替換格數；0 表示插入符號或選取未完成注音
	// Highlight 是反白的候選在 Items 裡的索引；上下鍵移動、Enter 選取。外殼只照著畫。
	Highlight int    `json:"highlight"`
	Columns   int    `json:"columns,omitempty"` // 0/1: legacy vertical; 2..5: row-major grid; 9/10: one horizontal page.
	Kind      string `json:"kind,omitempty"`    // emoji / kaomoji; absent for ordinary candidates.
	Title     string `json:"title,omitempty"`
	// StepKeys：這是改字的候選，小寫 j／k 把要修改的字換到前一個／後一個字（#193）。外殼據此顯示「J K 換字」；
	// 舊核心沒有這個欄位，也沒有 j／k 換字。
	StepKeys bool `json:"step_keys,omitempty"`
}

// SuggestionEdit 描述按 Tab 後替換的組字範圍，位置單位是顯示字元（rune）。
// 有此欄位時 suggestion 是「原文 → 完整詞」提示，不能當尾綴串接。
type SuggestionEdit struct {
	Start  int    `json:"start"`
	Length int    `json:"length"`
	Text   string `json:"text"`
}

// ReadingHint（#162）：中英混打時照字面顯示的數字按鍵（20），按空白會組成中文時的讀音提示。
// Start、Length 是那段按鍵在組字文字中的位置（顯示字元），那段組字的樣式是 StyleReading；Text 是讀音（ㄉㄢ）。
// 按鍵一定在游標前面，外殼把 Text 畫在游標附近；按 Enter 照字面送出數字。舊外殼忽略這個欄位。
type ReadingHint struct {
	Start  int    `json:"start"`
	Length int    `json:"length"`
	Text   string `json:"text"`
}

type Snapshot struct {
	FullWidth           bool            `json:"full_width,omitempty"`
	EnglishAutocomplete bool            `json:"english_autocomplete,omitempty"`
	EnglishPending      bool            `json:"english_pending,omitempty"` // 組字只有英文自動補字的這個字；接在保留的中文組字裡時是 false，照一般組字（#372）
	EnglishCanStart     bool            `json:"english_can_start,omitempty"`
	PersonalChanged     bool            `json:"personal_changed,omitempty"` // confirmed palette ordering changed; host may save.
	VocabChanged        bool            `json:"vocab_changed,omitempty"`    // 組字中加詞或反悔鍵改了自訂詞彙：宿主立即存 vocab.json（#123）
	Forgot              bool            `json:"forgot,omitempty"`           // 反悔鍵忘記了學到的詞：宿主同 Forget 請求盡快存檔
	ContextID           uint64          `json:"context_id,omitempty"`       // 本次提交可供外殼確認的代號；不是文件文字
	Revision            int             `json:"revision"`
	Handled             bool            `json:"handled"`              // false：外殼把原始按鍵交還應用程式
	Commit              string          `json:"commit,omitempty"`     // 至多一次
	Preedit             []Segment       `json:"preedit,omitempty"`    //
	Cursor              CursorPos       `json:"cursor"`               //
	Suggestion          string          `json:"suggestion,omitempty"` // 一般為補全後半；有 SuggestionEdit 時為替換提示
	SuggestionEdit      *SuggestionEdit `json:"suggestion_edit,omitempty"`
	Candidates          *CandidateList  `json:"candidates,omitempty"` //
	Notices             []string        `json:"notices,omitempty"`    //
	Mode                string          `json:"mode"`                 // zhuyin 或 english
	// CapsLock（#173）：核心記得的 Caps Lock 狀態（最近一次 caps_lock 請求；切換模式的 mode 請求會清掉）。
	// 外殼看到按鍵的鎖定狀態跟它不同時才送 caps_lock，換欄位、重開 session 後自然重新同步。
	CapsLock bool `json:"caps_lock,omitempty"`
	// CapsEnglish（#173）：目前的英文模式是 Caps Lock 帶來的，關掉 Caps Lock 會回到注音。外殼自己記中英模式時
	// （Windows 每個程式各自記住）記的是這一項以外的模式，重開 session 時不把它當成使用者切的英文。
	CapsEnglish bool         `json:"caps_english,omitempty"`
	ReadingHint *ReadingHint `json:"reading_hint,omitempty"`
	// OutputSimplified（#169）：送出的文字目前會轉成簡體（設定「輸出簡體字」）。外殼據此在狀態顯示標出簡體，
	// 從設定程式或別的輸入框切換後，下一個快照就跟上。只有回報 output_simplified_live 的引擎才有這一項，
	// 舊引擎沒有時外殼不能當成「繁體」，要看 health 的 output_simplified。
	OutputSimplified bool `json:"output_simplified,omitempty"`
	// Bell（#300）：這一鍵被擋下，外殼要提示使用者。目前只有「打錯注音時提示」：按了聲調但讀音沒有字，聲調不收，
	// 注音停在組字區（style pending 的那一段）等使用者修改。Windows 用 MessageBeep、macOS 用 NSBeep；Fcitx5、IBus
	// 沒有統一的提示音，把這個快照裡 pending 的那一段畫成醒目樣式。只出現在擋下按鍵的那一次請求的快照，下一個快照
	// 就沒有。舊外殼不認得這一項時不提示，聲調照樣被擋（注音停住，再按一次同一個聲調照舊收下）。
	Bell bool `json:"bell,omitempty"`
	// NineReadings（注音九宮格）：左欄「注音選擇」要畫的讀音；沒有九宮格的鍵或音節時省略。
	NineReadings *NineReadings `json:"nine_readings,omitempty"`
	// NineSegments（注音九宮格自動切字）：組字裡每個九宮格音節目前的切法，依序；沒開自動切字或沒有九宮格的鍵時省略。
	// 組字條照它畫，外殼不自己從送出的鍵推。
	NineSegments []NineSegment `json:"nine_segments,omitempty"`
}

// NineSegment 是自動切字時組字裡的一個九宮格音節。
type NineSegment struct {
	At   int `json:"at"`   // 音節位置，同 cursor、nine_reading.to、候選範圍用的位置
	Key  int `json:"key"`  // 這個音節從組字裡第幾顆九宮格鍵開始（從 0 起算，固定過的音節的鍵也算）
	Keys int `json:"keys"` // 佔幾顆鍵
	// Reading 是畫面寫法（沒打聲調的不帶聲調、一聲不標），一定不是空字串；還沒打完時是目前最可能的開頭拼法，Partial 為 true。
	Reading string `json:"reading"`
	Anchor  bool   `json:"anchor"` // 這個音節後面按了聲調鍵
	Fixed   bool   `json:"fixed"`  // 使用者用 nine_reading、選字或 fix 固定過，切點與拼法不會再被解碼改掉
	Partial bool   `json:"partial,omitempty"`
}

// NineReadings 是一個九宮格音節可以選的讀音（注音，沒打聲調的不帶聲調、一聲不標）。
// 有還沒完成的九宮格鍵時是那串鍵目前可能的拼法（Pending，音節的開頭），At 是游標（那個音節會插入的位置）；
// 否則是游標前那個有多個讀音的九宮格音節，At 是它的位置。外殼點了其中一項送 nine_reading（To 為 At、Word 為那一項）。
//
// 自動切字時列的是從左邊數來第一個還沒固定的九宮格音節（全部固定時省略）：Readings 包含不同鍵數的拼法（目前的鍵數在前，
// 其餘依鍵數；同鍵數依常用程度），Keys 與它一一對應是每一項佔幾顆鍵，Key 是這個音節第一顆鍵的位置（同 nine_segments.key）。
// 還沒打完的尾端音節是它的鍵的開頭拼法（Pending）。外殼點了送 nine_reading（To 為 At、Word 為那一項、Length 為對應的 Keys）。
type NineReadings struct {
	At       int      `json:"at"`
	Pending  bool     `json:"pending,omitempty"`
	Readings []string `json:"readings"` // 依常用程度；Pending 時第一項是組字區畫的那個
	Current  int      `json:"current"`  // 組字目前用的那一項（Readings 的索引）；Pending 時一律 0
	Key      int      `json:"key,omitempty"`
	Keys     []int    `json:"keys,omitempty"`
}

// SuggestedText 回傳按 Tab 接受後預覽的完整組字；無建議或範圍不合法時為空。
func (s Snapshot) SuggestedText() string {
	if s.Suggestion == "" {
		return ""
	}
	if e := s.SuggestionEdit; e != nil {
		r := []rune(s.Text())
		if e.Start < 0 || e.Length < 0 || e.Start > len(r) || e.Length > len(r)-e.Start {
			return ""
		}
		return string(r[:e.Start]) + e.Text + string(r[e.Start+e.Length:])
	}
	return s.Text() + s.Suggestion
}

// Text 回傳組字區的完整文字。
func (s Snapshot) Text() string {
	out := ""
	for _, g := range s.Preedit {
		out += g.Text
	}
	return out
}
