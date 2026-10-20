package session

import (
	"slices"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/fuzzy"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/vocab"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// markSession：「是結」會贏過沒有加入的「世傑」，用來驗證加詞後立刻打得出。
func markSession() *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"我", "ㄨㄛˇ", 1e-2}, {"和", "ㄏㄜˊ", 1e-2}, {"是", "ㄕˋ", 1e-2}, {"世", "ㄕˋ", 4e-5}, {"結", "ㄐㄧㄝˊ", 2e-5}, {"傑", "ㄐㄧㄝˊ", 4e-5},
		{"甲乙", "ㄐㄧㄚˇ ㄧˇ", 1e-6}, {"丙丁", "ㄅㄧㄥˇ ㄉㄧㄥ", 2e-6},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	s := New(personal.NewModel(lex, sys))
	s.Preview02 = true
	return s
}

func selectAt(t *testing.T, s *Session, pos int, want string) {
	t.Helper()
	s.Handle(proto.Request{Type: proto.Cursor, To: pos})
	snap := key(s, proto.KeyDown)
	for i := 0; i < 20 && snap.Candidates != nil; i++ {
		for _, it := range snap.Candidates.Items {
			if it.Text == want {
				s.Handle(proto.Request{Type: proto.Select, Index: it.ID})
				return
			}
		}
		snap = key(s, proto.KeyTab)
	}
	t.Fatalf("候選裡沒有 %q", want)
}

func TestMarkAndAddWord(t *testing.T) {
	s := markSession()
	typeReadingLocal(s, "ㄨㄛˇ ㄏㄜˊ ㄕˋ ㄐㄧㄝˊ")
	selectAt(t, s, 2, "世")
	selectAt(t, s, 3, "傑")
	s.Handle(proto.Request{Type: proto.Cursor, To: 4})
	key(s, proto.KeyShiftLeft)
	snap := key(s, proto.KeyShiftLeft)
	var marked []string
	for _, g := range snap.Preedit {
		if g.Style == proto.StyleMarked {
			marked = append(marked, g.Text)
		}
	}
	if strings.Join(marked, "") != "世傑" || snap.Cursor.Rune != 2 || !snap.Handled {
		t.Fatalf("標示範圍：%+v", snap)
	}
	if !slices.Contains(snap.Notices, "Enter 加入詞庫：世傑（ㄕˋ ㄐㄧㄝˊ）　Esc 取消") {
		t.Fatalf("標示中附說明與讀音：%v", snap.Notices)
	}
	snap = key(s, proto.KeyEnter)
	if snap.Commit != "" || !snap.VocabChanged || !slices.Contains(snap.Notices, "已加入詞庫：世傑") || snap.Text() != "我和世傑" || snap.Cursor.Rune != 4 {
		t.Fatalf("Enter 加入、不提交、游標回到開始標示的位置：%+v", snap)
	}
	if w, ok := s.model.Vocab.Lookup("世傑"); !ok || w.Readings[0] != "ㄕˋ ㄐㄧㄝˊ" || w.Source != "compose" {
		t.Fatalf("讀音用使用者打的注音：%+v", w)
	}
	for _, g := range key(s, proto.KeyEnter).Preedit {
		if g.Style == proto.StyleMarked {
			t.Fatal("加入後標示消失")
		}
	}
	// 加入後立刻打得出。
	s.Handle(proto.Request{Type: proto.Cancel})
	if got := typeReadingLocal(s, "ㄕˋ ㄐㄧㄝˊ").Text(); got != "世傑" {
		t.Fatalf("加入後：%q", got)
	}
}

func TestMarkCancelAndOtherKeys(t *testing.T) {
	s := markSession()
	typeReadingLocal(s, "ㄨㄛˇ ㄏㄜˊ")
	key(s, proto.KeyShiftLeft)
	if snap := key(s, proto.KeyEscape); snap.Text() != "我和" || hasMarked(snap) {
		t.Fatalf("Esc 只取消標示、保留組字：%+v", snap)
	}
	key(s, proto.KeyShiftLeft)
	if snap := key(s, proto.KeyLeft); hasMarked(snap) || snap.Cursor.Rune != 0 {
		t.Fatalf("其他按鍵取消標示後照常處理：%+v", snap)
	}
	if s.model.Vocab != nil && s.model.Vocab.Len() != 0 {
		t.Fatal("取消不加詞")
	}
	// 沒在組字：交還應用程式（照常選取文字）。
	x := markSession()
	if key(x, proto.KeyShiftLeft).Handled {
		t.Fatal("沒在組字時交還應用程式")
	}
}

func TestMarkRejectsPrivateAndLiterals(t *testing.T) {
	s := markSession()
	s.Handle(proto.Request{Type: proto.Key, Key: "j", Private: true})
	typeReadingLocal(s, "ㄛˇ ㄏㄜˊ")
	key(s, proto.KeyShiftLeft)
	key(s, proto.KeyShiftLeft)
	if snap := key(s, proto.KeyEnter); !slices.Contains(snap.Notices, "這裡不加入詞庫") || snap.VocabChanged {
		t.Fatalf("私人欄位不加詞：%+v", snap)
	}
}

// 開關關閉：新鍵名就是原本的鍵（←、→、Delete、⌫），行為與沒有這個功能時相同。
func TestMarkKeysAreOrdinaryKeysWithoutPreview(t *testing.T) {
	s := markSession()
	s.Preview02 = false
	typeReadingLocal(s, "ㄨㄛˇ ㄏㄜˊ")
	if snap := key(s, proto.KeyShiftLeft); !snap.Handled || snap.Cursor.Rune != 1 || hasMarked(snap) {
		t.Fatalf("同 ←：%+v", snap)
	}
	if snap := key(s, proto.KeyShiftRight); snap.Cursor.Rune != 2 || hasMarked(snap) {
		t.Fatalf("同 →：%+v", snap)
	}
	key(s, proto.KeyDown)
	if snap := key(s, proto.KeyShiftDelete); snap.VocabChanged || snap.Forgot || snap.Candidates != nil || snap.Text() != "我和" {
		t.Fatalf("同 Delete：候選開著時關窗、不刪字：%+v", snap)
	}
	if snap := key(s, proto.KeyShiftBackspace); snap.Text() != "我" {
		t.Fatalf("同 ⌫：%+v", snap)
	}
}

// 開關打開但候選窗沒開：反悔鍵就是 Delete／⌫，不會漏給應用程式；候選窗開著時 Shift＋←／→ 照左右鍵換頁。
func TestUndoKeyWithoutCandidatesIsOrdinary(t *testing.T) {
	s := markSession()
	typeReadingLocal(s, "ㄨㄛˇ ㄏㄜˊ")
	if snap := key(s, proto.KeyShiftBackspace); !snap.Handled || snap.Text() != "我" {
		t.Fatalf("同 ⌫：%+v", snap)
	}
	x := markSession()
	if snap := key(x, proto.KeyShiftBackspace); snap.Handled {
		t.Fatal("沒在組字時同 ⌫，交還應用程式")
	}
	typeReadingLocal(s, "ㄏㄜˊ")
	key(s, proto.KeyDown)
	if snap := key(s, proto.KeyShiftLeft); snap.Candidates == nil || hasMarked(snap) {
		t.Fatalf("候選窗開著時同 ←：%+v", snap)
	}
}

// 反悔鍵：自訂詞刪除（可復原），學到的詞忘記，內建詞不動。
func TestUndoKeyInCandidates(t *testing.T) {
	s := markSession()
	if _, err := s.model.VocabAdd([]vocab.Entry{{Text: "世傑", Readings: []string{"ㄕˋ ㄐㄧㄝˊ"}}}, vocab.SourceManual); err != nil {
		t.Fatal(err)
	}
	typeReadingLocal(s, "ㄕˋ ㄐㄧㄝˊ")
	snap := key(s, proto.KeyDown)
	if snap.Candidates == nil || snap.Candidates.Items[0].Text != "世傑" {
		t.Fatalf("%+v", snap.Candidates)
	}
	snap = key(s, proto.KeyShiftBackspace)
	if !snap.VocabChanged || s.model.IsVocab("世傑") || !slices.Contains(snap.Notices, "已從詞庫刪除：世傑（可在設定程式復原）") || snap.Candidates == nil {
		t.Fatalf("刪除自訂詞並重開候選：%+v", snap)
	}
	for _, it := range snap.Candidates.Items {
		if it.Text == "世傑" {
			t.Fatal("刪掉的詞不在候選裡")
		}
	}
	if len(s.model.Vocab.Batches()) != 2 {
		t.Fatal("刪除記成可以復原的批次")
	}
	// 內建詞：不動。
	snap = key(s, proto.KeyShiftDelete)
	if snap.Forgot || snap.VocabChanged {
		t.Fatalf("內建詞沒有可刪的：%+v", snap)
	}
}

func hasMarked(snap proto.Snapshot) bool {
	for _, g := range snap.Preedit {
		if g.Style == proto.StyleMarked {
			return true
		}
	}
	return false
}

func TestUndoKeyForgetsLearnedWord(t *testing.T) {
	s := markSession()
	s.model.Store.Uni["是"] = 2
	s.model.Store.Total = 2
	typeReadingLocal(s, "ㄕˋ")
	if snap := key(s, proto.KeyDown); snap.Candidates.Items[snap.Candidates.Highlight].Text != "是" {
		t.Fatalf("%+v", snap.Candidates)
	}
	snap := key(s, proto.KeyShiftDelete)
	if !snap.Forgot || !snap.PersonalChanged || !s.model.Store.Tombstones["是"] || !slices.Contains(snap.Notices, "不再推薦：是") {
		t.Fatalf("學到的詞走 Forget：%+v", snap)
	}
}

func TestMarkHintsAndEdits(t *testing.T) {
	s := markSession()
	typeReadingLocal(s, "ㄨㄛˇ ㄏㄜˊ ㄕˋ")
	if snap := key(s, proto.KeyShiftLeft); !slices.Contains(snap.Notices, "再多選一個字（單字的讀音請到設定程式「我的詞彙」加入）") {
		t.Fatalf("一個字：%v", snap.Notices)
	}
	if snap := key(s, proto.KeyEnter); !slices.Contains(snap.Notices, "至少選兩個字；單字的讀音請到設定程式「我的詞彙」加入") || snap.VocabChanged || snap.Cursor.Rune != 3 {
		t.Fatalf("單字不加，游標回原位：%+v", snap)
	}
	// Esc 也回到原位。
	key(s, proto.KeyShiftLeft)
	if snap := key(s, proto.KeyEscape); snap.Cursor.Rune != 3 {
		t.Fatalf("%+v", snap)
	}
	// ⌫ 刪掉選取的字。
	key(s, proto.KeyShiftLeft)
	key(s, proto.KeyShiftLeft)
	if snap := key(s, proto.KeyBackspace); snap.Text() != "我" || snap.Cursor.Rune != 1 || hasMarked(snap) {
		t.Fatalf("刪除選取：%+v", snap)
	}
}

// 已在詞庫、系統詞庫已有的詞：說明與結果照實講（三平台調查：重複或內建時不能說「已加入」）。
func TestMarkDuplicateAndSystemWord(t *testing.T) {
	s := markSession()
	s.model.Lex.(*lexicon.Mem).Add(lexicon.Entry{Surface: "我和", Reading: zhuyin.ParseReading("ㄨㄛˇ ㄏㄜˊ"), PRW: 1})
	typeReadingLocal(s, "ㄨㄛˇ ㄏㄜˊ")
	key(s, proto.KeyShiftLeft)
	if snap := key(s, proto.KeyShiftLeft); !slices.Contains(snap.Notices, "Enter 設為優先：我和（ㄨㄛˇ ㄏㄜˊ）　Esc 取消") {
		t.Fatalf("內建詞：%v", snap.Notices)
	}
	if snap := key(s, proto.KeyEnter); !slices.Contains(snap.Notices, "已設為優先：我和") {
		t.Fatalf("%v", snap.Notices)
	}
	key(s, proto.KeyShiftLeft)
	if snap := key(s, proto.KeyShiftLeft); !slices.Contains(snap.Notices, "「我和」已在詞庫　Esc 取消") {
		t.Fatalf("已在詞庫：%v", snap.Notices)
	}
	if snap := key(s, proto.KeyEnter); !slices.Contains(snap.Notices, "「我和」已在詞庫") || snap.VocabChanged {
		t.Fatalf("%+v", snap)
	}
}

// 候選窗以附註標出自訂詞（四個外殼都會畫附註）。
func TestCandidateNoteForCustomWord(t *testing.T) {
	s := markSession()
	s.Fuzzy = fuzzy.Config{}.With(fuzzy.SSh, fuzzy.Select) // 模糊音預設開著：近音附註不能蓋掉「我的詞彙」（Fcitx5 驗收）
	s.model.VocabAdd([]vocab.Entry{{Text: "世傑", Readings: []string{"ㄕˋ ㄐㄧㄝˊ"}}}, vocab.SourceManual)
	typeReadingLocal(s, "ㄕˋ ㄐㄧㄝˊ")
	snap := key(s, proto.KeyDown)
	if snap.Candidates.Items[0].Text != "世傑" || snap.Candidates.Items[0].Note != "我的詞彙" {
		t.Fatalf("%+v", snap.Candidates.Items)
	}
}

// 回歸案例：先手選提交過「士傑」，再加入自訂詞「世傑」，單打 ㄕˋ ㄐㄧㄝˊ 仍是「士傑」。
// 親手加詞是比過去的手選更新的明確選擇，要勝出。
func TestVocabAddBeatsEarlierPick(t *testing.T) {
	s := markSession()
	s.model.Lex.(*lexicon.Mem).Add(lexicon.Entry{Surface: "士", Reading: zhuyin.ParseReading("ㄕˋ"), PRW: 1})
	typeReadingLocal(s, "ㄕˋ ㄐㄧㄝˊ")
	selectAt(t, s, 0, "士")
	selectAt(t, s, 1, "傑")
	if got := commitText(s); got != "士傑" {
		t.Fatalf("前提：%q", got)
	}
	if got := shown(s, "ㄕˋ ㄐㄧㄝˊ").Text(); got != "士傑" {
		t.Fatalf("前提：手選過後是「士傑」：%q", got)
	}
	s.Handle(proto.Request{Type: proto.Cancel})
	if _, err := s.model.VocabAdd([]vocab.Entry{{Text: "世傑", Readings: []string{"ㄕˋ ㄐㄧㄝˊ"}}}, vocab.SourceManual); err != nil {
		t.Fatal(err)
	}
	if got := shown(s, "ㄕˋ ㄐㄧㄝˊ").Text(); got != "世傑" {
		t.Fatalf("加入自訂詞後：%q", got)
	}
}

// 回歸案例：沒有手選、照預設提交過一次「是傑」，再加入「世傑」，單打仍是「是傑」——
// 句首的連用證據蓋過剛加的詞，而那次提交沒有留下更正，舊的衝突判斷看不到。
// 個別字在別處用過（沒有接連提交這組讀音）不算衝突，不另外記成手選。
func TestVocabAddBeatsEarlierPlainCommit(t *testing.T) {
	s := markSession()
	typeReadingLocal(s, "ㄕˋ ㄐㄧㄝˊ")
	if got := commitText(s); got != "是傑" {
		t.Fatalf("前提：預設提交「是傑」：%q", got)
	}
	if got := shown(s, "ㄕˋ ㄐㄧㄝˊ").Text(); got != "是傑" {
		t.Fatalf("前提：%q", got)
	}
	s.Handle(proto.Request{Type: proto.Cancel})
	if _, err := s.model.VocabAdd([]vocab.Entry{{Text: "世傑", Readings: []string{"ㄕˋ ㄐㄧㄝˊ"}}}, vocab.SourceManual); err != nil {
		t.Fatal(err)
	}
	if got := shown(s, "ㄕˋ ㄐㄧㄝˊ").Text(); got != "世傑" {
		t.Fatalf("加入自訂詞後：%q", got)
	}

	s = markSession()
	for _, r := range []string{"ㄕˋ", "ㄐㄧㄝˊ"} { // 分開提交：沒有「是→傑」的連用
		typeReadingLocal(s, r)
		commitText(s)
		s.Handle(proto.Request{Type: proto.Cancel})
		s.model.Store.Ctx = map[string]float64{}
		s.model.Store.Bi = map[string]float64{}
	}
	before := len(s.model.Store.Corrections)
	s.model.VocabAdd([]vocab.Entry{{Text: "世傑", Readings: []string{"ㄕˋ ㄐㄧㄝˊ"}}}, vocab.SourceManual)
	if len(s.model.Store.Corrections) != before || len(s.model.Store.Readings) != 0 {
		t.Fatalf("沒有衝突時不記成手選：%v %v", s.model.Store.Corrections, s.model.Store.Readings)
	}
}

// 刪除自訂詞後：加詞時記下的學習一起忘記，被動使用不學回；復原後又打得出來。
func TestVocabRemoveForgetsAndUndoRestores(t *testing.T) {
	s := markSession()
	b, _ := s.model.VocabAdd([]vocab.Entry{{Text: "世傑", Readings: []string{"ㄕˋ ㄐㄧㄝˊ"}}}, vocab.SourceManual)
	if got := shown(s, "ㄕˋ ㄐㄧㄝˊ").Text(); got != "世傑" {
		t.Fatalf("%q", got)
	}
	s.Handle(proto.Request{Type: proto.Cancel})
	rm, _ := s.model.VocabRemove([]string{"世傑"}, vocab.SourceManual)
	if !s.model.Store.Tombstones["世傑"] || len(s.model.Store.Corrections) != 0 {
		t.Fatalf("忘記並留下 tombstone：%v %v", s.model.Store.Tombstones, s.model.Store.Corrections)
	}
	if got := shown(s, "ㄕˋ ㄐㄧㄝˊ").Text(); got == "世傑" {
		t.Fatal("刪除後不再出現")
	}
	s.Handle(proto.Request{Type: proto.Cancel})
	if _, err := s.model.VocabUndo(rm.ID, vocab.SourceManual); err != nil {
		t.Fatal(err)
	}
	if got := shown(s, "ㄕˋ ㄐㄧㄝˊ").Text(); got != "世傑" || s.model.Store.Tombstones["世傑"] {
		t.Fatalf("復原後：%q", got)
	}
	_ = b
}
