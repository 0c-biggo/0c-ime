package session

import (
	"slices"
	"testing"

	"github.com/0c-biggo/0c-ime/core/english"
	"github.com/0c-biggo/0c-ime/core/proto"
)

// #372：單按 Shift 切英文、前面的中文還沒送出時（#90 保留組字），接著打的英文一樣補字。英文字接在游標處，
// 跟中英混打的英文字一樣（s.mixed），有中文組字時沒有一個鍵交給程式（#106）。

// compositionEnglishSession 是「我想打一段自」的測試字典加上英文字典，開著英文自動補字。
func compositionEnglishSession() *Session {
	s := sentenceSession()
	s.EnglishAutocomplete = true
	s.English = english.New([]english.Word{
		{Text: "program", Weight: 100}, {Text: "progress", Weight: 80}, {Text: "project", Weight: 60},
		{Text: "hello", Weight: 100}, {Text: "help", Weight: 50},
	})
	return s
}

// typedWoXiang 打「我想」、單按 Shift 切到英文，組字留著。
func typedWoXiang(t *testing.T, s *Session) {
	t.Helper()
	typeKeys(s, "ji3vu;3")
	if snap := s.Handle(proto.Request{Type: proto.Toggle}); snap.Text() != "我想" || snap.Mode != proto.ModeEnglish {
		t.Fatalf("前置：%+v", snap)
	}
}

func shownCandidates(snap proto.Snapshot) []string {
	if snap.Candidates == nil {
		return nil
	}
	var out []string
	for _, it := range snap.Candidates.Items {
		out = append(out, it.Text)
	}
	return out
}

func firstCandidate(snap proto.Snapshot) string {
	if texts := shownCandidates(snap); len(texts) > 0 {
		return texts[0]
	}
	return ""
}

func englishCandidates(snap proto.Snapshot) bool {
	return slices.Contains(shownCandidates(snap), "program") || slices.Contains(shownCandidates(snap), "hello")
}

func TestEnglishInCompositionCompletes(t *testing.T) {
	s := compositionEnglishSession()
	typedWoXiang(t, s)
	var snap proto.Snapshot
	for _, k := range "progr" {
		if snap = key(s, string(k)); !snap.Handled || snap.Commit != "" {
			t.Fatalf("%c：組字中的字母不交給程式：%+v", k, snap)
		}
		// 快照照舊：english_pending 只在組字只剩這個英文字時是 true；有組字時不能開始新的單字。
		if snap.EnglishPending || snap.EnglishCanStart {
			t.Fatalf("%c：快照旗標：%+v", k, snap)
		}
	}
	if snap.Text() != "我想progr" || snap.Cursor.Rune != 7 || firstCandidate(snap) != "program" {
		t.Fatalf("我想progr 要有補字候選：%+v", snap)
	}
	if snap.Candidates.Title != "按 Tab 補全或修正拼字 · Esc 關閉" || snap.Candidates.Items[0].Key != "" {
		t.Fatalf("英文候選只能按 Tab 接受：%+v", snap.Candidates)
	}
	if snap = key(s, proto.KeyTab); !snap.Handled || snap.Text() != "我想program" || snap.Candidates != nil || snap.Cursor.Rune != 9 {
		t.Fatalf("Tab 接受：%+v", snap)
	}
	if snap = key(s, proto.KeyEnter); !snap.Handled || snap.Commit != "我想program" || snap.Text() != "" {
		t.Fatalf("Enter 送出整段：%+v", snap)
	}
	// 沒有組字時照舊：english_pending、english_can_start。
	if snap = key(s, "h"); !snap.EnglishPending || snap.Text() != "h" {
		t.Fatalf("送出後從空白組字區開始：%+v", snap)
	}
}

// 自動加空格（#273、#365）照舊處理整段送出的字串。
func TestEnglishInCompositionCJKSpace(t *testing.T) {
	for _, tc := range []struct {
		name         string
		latin, digit bool
		keys, want   string
	}{
		{"on", true, false, "progr\t", "我想 program"},
		{"all", true, true, "progr\t", "我想 program"},
		{"on 數字不補", true, false, "progr\t 2", "我想 program 2"},
		{"all 數字", true, true, "2", "我想 2"},
	} {
		s := compositionEnglishSession()
		s.CJKLatinSpace, s.CJKDigitSpace = tc.latin, tc.digit
		typedWoXiang(t, s)
		for _, k := range tc.keys {
			name := string(k)
			if k == '\t' {
				name = proto.KeyTab
			}
			key(s, name)
		}
		if snap := key(s, proto.KeyEnter); snap.Commit != tc.want {
			t.Fatalf("%s：%+v", tc.name, snap)
		}
	}
}

// 游標在組字中間也補字，只要游標左右不是英文字母或數字。
func TestEnglishInCompositionMiddle(t *testing.T) {
	s := compositionEnglishSession()
	typeKeys(s, "ji3vu;3283u 2j04y4")
	s.Handle(proto.Request{Type: proto.Toggle})
	key(s, proto.KeyLeft)
	key(s, proto.KeyLeft) // 「一」後面
	snap := typeKeys(s, "hel")
	if snap.Text() != "我想打一hel段自" || snap.Cursor.Rune != 7 || firstCandidate(snap) != "hello" || snap.EnglishPending {
		t.Fatalf("組字中間補字：%+v", snap)
	}
	if snap = key(s, proto.KeyTab); snap.Text() != "我想打一hello段自" || snap.Cursor.Rune != 9 {
		t.Fatalf("Tab：%+v", snap)
	}
	if snap = key(s, proto.KeyEnter); snap.Commit != "我想打一hello段自" {
		t.Fatalf("送出：%+v", snap)
	}

	// 游標夾在英文字中間（te|st）或數字後面：在改一個已經打好的詞，照舊按原樣插入、不補字。
	s = compositionEnglishSession()
	typedWoXiang(t, s)
	typeKeys(s, "test")
	key(s, proto.KeyLeft)
	key(s, proto.KeyLeft)
	if snap = key(s, "h"); !snap.Handled || snap.Text() != "我想tehst" || snap.Candidates != nil || s.englishWord {
		t.Fatalf("英文字中間：%+v", snap)
	}
	s = compositionEnglishSession()
	typedWoXiang(t, s)
	key(s, "3")
	key(s, " ") // 空白清掉 token 的限制，只剩左邊的字不是數字的條件
	key(s, proto.KeyLeft)
	if snap = typeKeys(s, "hel"); snap.Text() != "我想3hel " || snap.Candidates != nil {
		t.Fatalf("數字後面：%+v", snap)
	}
	// 游標右邊是英文字（|test）也一樣。
	s = compositionEnglishSession()
	typedWoXiang(t, s)
	typeKeys(s, "test")
	key(s, proto.KeyHome)
	key(s, proto.KeyRight)
	key(s, proto.KeyRight) // 我想|test
	if snap = typeKeys(s, "hel"); snap.Text() != "我想heltest" || snap.Candidates != nil {
		t.Fatalf("英文字前面：%+v", snap)
	}
}

// 空白、標點、數字：英文字先變成組字裡的一般文字，再插進組字，不送出、不交給程式。
// 數字、底線、網址標點照舊讓接著打的字停止補字，到下一個空白或導覽鍵為止。
func TestEnglishInCompositionBoundaries(t *testing.T) {
	for _, tc := range []struct {
		k, want string
		blocks  bool
	}{
		{" ", "我想progr ", false},
		{".", "我想progr.", true},
		{",", "我想progr,", true},
		{"1", "我想progr1", true},
		{"_", "我想progr_", true},
		{"/", "我想progr/", true},
		{"KP1", "我想progr1", true},
	} {
		s := compositionEnglishSession()
		typedWoXiang(t, s)
		typeKeys(s, "progr")
		snap := key(s, tc.k)
		if !snap.Handled || snap.Commit != "" || snap.Text() != tc.want || snap.Candidates != nil || s.englishWord {
			t.Fatalf("%q：%+v", tc.k, snap)
		}
		snap = typeKeys(s, "hel")
		if (snap.Candidates == nil) != tc.blocks || snap.Text() != tc.want+"hel" {
			t.Fatalf("%q 之後的 hel 補字=%v：%+v", tc.k, snap.Candidates != nil, snap)
		}
		if tc.blocks {
			key(s, " ")
			if snap = typeKeys(s, "hel"); snap.Candidates == nil {
				t.Fatalf("%q：空白之後重新補字：%+v", tc.k, snap)
			}
		}
	}
}

// Enter 照畫面送出整段（#235），不替使用者接受反白的建議；Tab 才接受。
func TestEnglishInCompositionEnterAndTab(t *testing.T) {
	s := compositionEnglishSession()
	typedWoXiang(t, s)
	typeKeys(s, "progr")
	if snap := key(s, proto.KeyEnter); !snap.Handled || snap.Commit != "我想progr" {
		t.Fatalf("Enter 不接受建議：%+v", snap)
	}
	if snap := key(s, proto.KeyEnter); snap.Handled {
		t.Fatalf("送出後的 Enter 交給程式：%+v", snap)
	}
	// 沒有建議時 Tab 吃掉不動作，英文字留著（#106）。
	s = compositionEnglishSession()
	typedWoXiang(t, s)
	typeKeys(s, "zq")
	if snap := key(s, proto.KeyTab); !snap.Handled || snap.Text() != "我想zq" || !s.englishWord {
		t.Fatalf("沒有建議的 Tab：%+v", snap)
	}
	if snap := typeKeys(s, "x"); snap.Text() != "我想zqx" {
		t.Fatalf("Tab 之後繼續打：%+v", snap)
	}
}

// 英文候選開著時 ↑↓ 在清單裡移動（同沒有組字時）；沒開或已用 Esc 關掉時 ↓ 照 #369 開中文候選。
func TestEnglishInCompositionUpDown(t *testing.T) {
	s := compositionEnglishSession()
	typedWoXiang(t, s)
	snap := typeKeys(s, "pro")
	if snap = key(s, proto.KeyDown); !snap.Handled || snap.Candidates == nil || snap.Candidates.Highlight != 1 || snap.Text() != "我想pro" {
		t.Fatalf("↓ 在英文候選裡移動：%+v", snap)
	}
	second := snap.Candidates.Items[1].Text
	if snap = key(s, proto.KeyUp); snap.Candidates.Highlight != 0 {
		t.Fatalf("↑：%+v", snap)
	}
	key(s, proto.KeyDown)
	if snap = key(s, proto.KeyTab); snap.Text() != "我想"+second {
		t.Fatalf("Tab 接受第二個 %s：%+v", second, snap)
	}

	// Esc 關掉英文候選之後，↓ 開中文候選。
	s = compositionEnglishSession()
	typeKeys(s, "ji3vu;3283u 2j04y4")
	s.Handle(proto.Request{Type: proto.Toggle})
	typeKeys(s, "progr")
	if snap = key(s, proto.KeyEscape); !snap.Handled || snap.Candidates != nil || snap.Text() != "我想打一段自progr" {
		t.Fatalf("Esc 關英文候選：%+v", snap)
	}
	snap = key(s, proto.KeyDown)
	if !snap.Handled || snap.Candidates == nil || englishCandidates(snap) || snap.Text() != "我想打一段自progr" || s.englishWord {
		t.Fatalf("↓ 開中文候選：%+v", snap)
	}
	// 回到「自」前面選「字」（#369 的回報），英文字已經是一般文字。
	key(s, proto.KeyEscape)
	for range 6 {
		key(s, proto.KeyLeft)
	}
	snap = key(s, proto.KeyDown)
	if snap = key(s, candidateKey(t, snap, "字")); snap.Text() != "我想打一段字progr" {
		t.Fatalf("選字：%+v", snap)
	}

	// 英文字不到兩個字母、沒有候選時，↓ 也開中文候選。
	s = compositionEnglishSession()
	typedWoXiang(t, s)
	key(s, "p")
	if snap = key(s, proto.KeyDown); snap.Candidates == nil || englishCandidates(snap) || snap.Text() != "我想p" {
		t.Fatalf("一個字母時 ↓：%+v", snap)
	}
}

// 候選開著時（↓ 開的中文候選）打字母照候選的規則吃掉，不開始補字；關掉之後再打照常補字。
func TestEnglishInCompositionWhileCandidatesOpen(t *testing.T) {
	s := compositionEnglishSession()
	typedWoXiang(t, s)
	key(s, proto.KeyDown)
	for _, k := range []string{"p", "r", "o"} {
		if snap := key(s, k); !snap.Handled || snap.Text() != "我想" || s.englishWord || englishCandidates(snap) {
			t.Fatalf("%s：候選開著時不補字：%+v", k, snap)
		}
	}
	key(s, proto.KeyEscape)
	if snap := typeKeys(s, "pro"); snap.Text() != "我想pro" || !englishCandidates(snap) {
		t.Fatalf("候選關掉後補字：%+v", snap)
	}
}

// ←→ Home End：英文字先變成一般文字、收起英文候選，再在組字裡移動；回到那個字旁邊時不再補字。
func TestEnglishInCompositionNavigation(t *testing.T) {
	for _, k := range []string{proto.KeyLeft, proto.KeyRight, proto.KeyHome, proto.KeyEnd} {
		s := compositionEnglishSession()
		typedWoXiang(t, s)
		typeKeys(s, "progr")
		snap := key(s, k)
		if !snap.Handled || snap.Candidates != nil || snap.Text() != "我想progr" || s.englishWord || snap.Commit != "" {
			t.Fatalf("%s：%+v", k, snap)
		}
		key(s, proto.KeyEnd)
		if snap = key(s, "a"); snap.Text() != "我想progra" || snap.Candidates != nil {
			t.Fatalf("%s：回到英文字旁邊不再補字：%+v", k, snap)
		}
	}
	// 移到中文之間再打，是新的英文字，照常補字。
	s := compositionEnglishSession()
	typedWoXiang(t, s)
	typeKeys(s, "progr")
	key(s, proto.KeyHome)
	key(s, proto.KeyRight)
	if snap := typeKeys(s, "hel"); snap.Text() != "我hel想progr" || !englishCandidates(snap) {
		t.Fatalf("移到中文之間：%+v", snap)
	}
}

// 退格：先撤銷已接受的建議，再一個一個刪英文字母；英文字刪光後照舊刪中文。
func TestEnglishInCompositionBackspace(t *testing.T) {
	s := compositionEnglishSession()
	typedWoXiang(t, s)
	typeKeys(s, "progr")
	key(s, proto.KeyTab)
	for _, want := range []string{"我想progr", "我想prog", "我想pro", "我想pr", "我想p", "我想", "我"} {
		if snap := key(s, proto.KeyBackspace); !snap.Handled || snap.Text() != want || snap.Commit != "" {
			t.Fatalf("退格到 %s：%+v", want, snap)
		}
	}
	if s.englishWord || s.mixed.raw != "" {
		t.Fatal("英文字刪光了還留著狀態")
	}
	// 刪光再打，又是新的英文字。
	if snap := typeKeys(s, "hel"); snap.Text() != "我hel" || !englishCandidates(snap) {
		t.Fatalf("刪光再打：%+v", snap)
	}
}

// Esc：第一次關英文候選（撤銷已接受的建議），第二次照「Esc 清除整段組字」的設定。
func TestEnglishInCompositionEscape(t *testing.T) {
	for _, keep := range []bool{false, true} {
		s := compositionEnglishSession()
		s.EscKeepComposition = keep
		typedWoXiang(t, s)
		typeKeys(s, "progr")
		if snap := key(s, proto.KeyEscape); !snap.Handled || snap.Candidates != nil || snap.Text() != "我想progr" {
			t.Fatalf("keep=%v 第一次 Esc：%+v", keep, snap)
		}
		snap := key(s, proto.KeyEscape)
		want := ""
		if keep {
			want = "我想progr"
		}
		if !snap.Handled || snap.Commit != "" || snap.Text() != want {
			t.Fatalf("keep=%v 第二次 Esc：%+v", keep, snap)
		}
	}
	// 已接受的建議：Esc 撤銷回打的字母並關候選。
	s := compositionEnglishSession()
	typedWoXiang(t, s)
	typeKeys(s, "progr")
	key(s, proto.KeyTab)
	if snap := key(s, proto.KeyEscape); snap.Text() != "我想progr" || snap.Candidates != nil {
		t.Fatalf("Esc 撤銷建議：%+v", snap)
	}
}

// 單按 Shift 切回注音：英文字變成組字裡的一般文字，接著打注音。
func TestEnglishInCompositionToggleBack(t *testing.T) {
	s := compositionEnglishSession()
	typedWoXiang(t, s)
	typeKeys(s, "progr")
	snap := s.Handle(proto.Request{Type: proto.Toggle})
	if snap.Mode != proto.ModeZhuyin || snap.Text() != "我想progr" || snap.Candidates != nil || s.englishWord {
		t.Fatalf("切回注音：%+v", snap)
	}
	if snap = typeKeys(s, "ji3"); snap.Text() != "我想progr我" {
		t.Fatalf("接著打注音：%+v", snap)
	}
	if snap = key(s, proto.KeyEnter); snap.Commit != "我想progr我" {
		t.Fatalf("送出：%+v", snap)
	}
}

// 私密欄位與暫停不補字；字母照舊按原樣插在組字裡。
func TestEnglishInCompositionPrivateAndPaused(t *testing.T) {
	s := compositionEnglishSession()
	typedWoXiang(t, s)
	var snap proto.Snapshot
	for _, k := range "progr" {
		snap = s.Handle(proto.Request{Type: proto.Key, Key: string(k), Private: true})
	}
	if !snap.Handled || snap.Text() != "我想progr" || snap.Candidates != nil || s.englishWord {
		t.Fatalf("私密欄位：%+v", snap)
	}

	s = compositionEnglishSession()
	typedWoXiang(t, s)
	s.Handle(proto.Request{Type: proto.Pause, On: true})
	if snap = typeKeys(s, "progr"); !snap.Handled || snap.Text() != "我想progr" || snap.Candidates != nil || s.englishWord {
		t.Fatalf("暫停：%+v", snap)
	}

	// 打到一半暫停：英文字變成一般文字，組字留著、不送出。
	s = compositionEnglishSession()
	typedWoXiang(t, s)
	typeKeys(s, "progr")
	if snap = s.Handle(proto.Request{Type: proto.Pause, On: true}); snap.Commit != "" || snap.Text() != "我想progr" || snap.Candidates != nil || s.englishWord {
		t.Fatalf("打到一半暫停：%+v", snap)
	}
	// 打到一半變成私密欄位：候選收起，接著的字母按原樣插入。
	s = compositionEnglishSession()
	typedWoXiang(t, s)
	typeKeys(s, "progr")
	if snap = s.Handle(proto.Request{Type: proto.Key, Key: "a", Private: true}); !snap.Handled || snap.Text() != "我想progra" || snap.Candidates != nil {
		t.Fatalf("打到一半變成私密欄位：%+v", snap)
	}
	if snap = s.Handle(proto.Request{Type: proto.Key, Key: proto.KeyEnter, Private: true}); snap.Commit != "我想progra" {
		t.Fatalf("私密欄位送出：%+v", snap)
	}
	if len(s.model.Store.English) != 0 {
		t.Fatalf("私密欄位不學：%+v", s.model.Store.English)
	}
}

// 學習：照自動補字的開關學，學習來源照 flushMixed 記下自行輸入或系統建議；「記住我選的字詞」關掉時不學（#297）。
func TestEnglishInCompositionLearning(t *testing.T) {
	commit := func(s *Session, keys ...string) string {
		typeKeys(s, "ji3vu;3")
		s.Handle(proto.Request{Type: proto.Toggle})
		for _, k := range keys {
			if len(k) > 1 && k != proto.KeyTab {
				typeKeys(s, k)
			} else {
				key(s, k)
			}
		}
		snap := key(s, proto.KeyEnter)
		s.Handle(proto.Request{Type: proto.Toggle}) // 回到注音，下一段從頭打
		return snap.Commit
	}
	// 自己打完的字：學到一次確認。
	s := compositionEnglishSession()
	if got := commit(s, "widgetx"); got != "我想widgetx" {
		t.Fatal(got)
	}
	if e := s.model.Store.English["widgetx"]; e.Confirmed != 1 {
		t.Fatalf("自行輸入要學：%+v", s.model.Store.English)
	}
	// 按 Tab 接受的建議是系統建議：還沒學過的詞不因此學到（同混打）。
	s = compositionEnglishSession()
	if got := commit(s, "progr", proto.KeyTab); got != "我想program" {
		t.Fatal(got)
	}
	if _, ok := s.model.Store.English["program"]; ok {
		t.Fatalf("系統建議不自我強化：%+v", s.model.Store.English)
	}
	// 學過的詞再被接受：只加使用次數，不算確認。
	commit(s, "program")
	before := s.model.Store.English["program"]
	commit(s, "progr", proto.KeyTab)
	if after := s.model.Store.English["program"]; after.Confirmed != before.Confirmed || after.Usage <= before.Usage {
		t.Fatalf("系統建議的來源：before=%+v after=%+v", before, after)
	}
	// 「記住我選的字詞」關掉：不學。
	s = compositionEnglishSession()
	s.NoLearning = true
	commit(s, "widgetx")
	if len(s.model.Store.English) != 0 {
		t.Fatalf("NoLearning：%+v", s.model.Store.English)
	}
	// 自動補字關掉：字母照舊按原樣插入，跟以前一樣不學。
	s = compositionEnglishSession()
	s.EnglishAutocomplete = false
	if got := commit(s, "widgetx"); got != "我想widgetx" || len(s.model.Store.English) != 0 {
		t.Fatalf("自動補字關掉：%q %+v", got, s.model.Store.English)
	}
	// #328 不變：英文模式單獨送出的一串（沒有中文）不學。
	s = compositionEnglishSession()
	s.Handle(proto.Request{Type: proto.Mode, On: true})
	typeKeys(s, "widgetx")
	key(s, proto.KeyEnter)
	if len(s.model.Store.English) != 0 {
		t.Fatalf("單獨送出的英文：%+v", s.model.Store.English)
	}
}

// 總檢：有中文組字時，外殼送得出來的每一個鍵核心都回 handled，Windows 的 composing_wants 與 TestKeyDown 才一致
// （#106）；四個外殼都把這種組字當一般組字（english_pending 是 false）。
func TestEnglishInCompositionNeverDeclinesAKey(t *testing.T) {
	keys := []string{
		proto.KeyEnter, proto.KeyEscape, proto.KeyBackspace, proto.KeyDelete, proto.KeyTab, proto.KeyBackTab,
		proto.KeyLeft, proto.KeyRight, proto.KeyUp, proto.KeyDown, proto.KeyPageUp, proto.KeyPageDown,
		proto.KeyHome, proto.KeyEnd, proto.KeyShiftLeft, proto.KeyShiftRight, proto.KeyShiftDelete, proto.KeyShiftBackspace,
	}
	for r := rune(0x20); r <= 0x7e; r++ {
		keys = append(keys, string(r))
		if r > 0x20 {
			keys = append(keys, proto.KeypadPrefix+string(r))
		}
	}
	states := map[string]func(*Session){
		"中文組字":     func(s *Session) {},
		"英文字有候選":   func(s *Session) { typeKeys(s, "progr") },
		"一個字母":     func(s *Session) { typeKeys(s, "p") },
		"Esc 關掉候選": func(s *Session) { typeKeys(s, "progr"); key(s, proto.KeyEscape) },
		"接受了建議":    func(s *Session) { typeKeys(s, "progr"); key(s, proto.KeyTab) },
		"組字中間":     func(s *Session) { key(s, proto.KeyLeft); typeKeys(s, "hel") },
		"中文候選開著":   func(s *Session) { typeKeys(s, "progr"); key(s, proto.KeyEscape); key(s, proto.KeyDown) },
	}
	for name, setup := range states {
		for _, preview := range []bool{false, true} {
			for _, k := range keys {
				s := compositionEnglishSession()
				s.Preview02 = preview
				typedWoXiang(t, s)
				setup(s)
				before := s.snapshot()
				if snap := key(s, k); !snap.Handled || snap.EnglishPending {
					t.Fatalf("%s（preview=%v）按 %q 交給了程式：前 %+v 後 %+v", name, preview, k, before, snap)
				}
			}
		}
	}
}
