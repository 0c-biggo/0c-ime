package session

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// sentenceSession 有「我想打一段自／字」的測試字典，自比字常用，組出來是「自」，要回頭選「字」（#369 的回報）。
func sentenceSession() *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"我", "ㄨㄛˇ", 1e-2}, {"想", "ㄒㄧㄤˇ", 3e-3}, {"打", "ㄉㄚˇ", 2e-3}, {"一", "ㄧ", 5e-3},
		{"段", "ㄉㄨㄢˋ", 1e-3}, {"一段", "ㄧ ㄉㄨㄢˋ", 5e-4}, {"自", "ㄗˋ", 2e-3}, {"字", "ㄗˋ", 1e-3},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	return New(personal.NewModel(lex, sys))
}

// candidateKey 回傳目前這頁候選裡 text 的選字鍵；沒有就讓測試失敗。
func candidateKey(t *testing.T, snap proto.Snapshot, text string) string {
	t.Helper()
	if snap.Candidates == nil {
		t.Fatalf("候選沒開：%+v", snap)
	}
	for _, it := range snap.Candidates.Items {
		if it.Text == text {
			return it.Key
		}
	}
	t.Fatalf("候選裡沒有 %s：%+v", text, snap.Candidates.Items)
	return ""
}

// typedThenEnglish 打「我想打一段自」、單按 Shift 切英文、打 test，游標移回「自」前面：回報者想回頭改字的位置。
func typedThenEnglish(t *testing.T, s *Session) {
	t.Helper()
	typeKeys(s, "ji3vu;3283u 2j04y4")
	s.Handle(proto.Request{Type: proto.Toggle})
	typeKeys(s, "test")
	var snap proto.Snapshot
	for range 5 {
		snap = key(s, proto.KeyLeft)
	}
	if snap.Text() != "我想打一段自test" || snap.Cursor.Rune != 5 || snap.Mode != proto.ModeEnglish {
		t.Fatalf("前置：%+v", snap)
	}
}

// 中英混打切到英文後，前面還沒送出的中文按 ↓ 照樣開候選（#369，同新酷音）；以前 ↓ 交給應用程式，
// 宿主游標離開組字區、選不了字。選完繼續打的英文照舊接在組字裡。
func TestEnglishModeDownSelectsKeptComposition(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		s := sentenceSession()
		s.MixedInput = mixed
		typedThenEnglish(t, s)
		snap := key(s, proto.KeyDown)
		if !snap.Handled || snap.Mode != proto.ModeEnglish || snap.Text() != "我想打一段自test" {
			t.Fatalf("mixed=%v 英文模式 ↓ 應開候選、不交給應用程式：%+v", mixed, snap)
		}
		snap = key(s, candidateKey(t, snap, "字"))
		if !snap.Handled || snap.Candidates != nil || snap.Text() != "我想打一段字test" || snap.Mode != proto.ModeEnglish {
			t.Fatalf("mixed=%v 數字鍵選字：%+v", mixed, snap)
		}
		key(s, proto.KeyEnd)
		typeKeys(s, "ok")
		if snap = key(s, proto.KeyEnter); snap.Commit != "我想打一段字testok" {
			t.Fatalf("mixed=%v 選完繼續打英文再送出：%+v", mixed, snap)
		}
	}
}

// 候選開著時英文模式的可列印字元照候選的規則：空白換頁或選字、Enter 選字、Esc 關閉，不插進組字；
// 候選關掉後再打的字照舊按原樣插入。
func TestEnglishModeCandidateKeys(t *testing.T) {
	// 空白：預設換頁（只有一頁時留在原頁），不插入空白。
	s := sentenceSession()
	typedThenEnglish(t, s)
	key(s, proto.KeyDown)
	if snap := key(s, " "); !snap.Handled || snap.Candidates == nil || snap.Text() != "我想打一段自test" {
		t.Fatalf("候選開著時空白應換頁、不插入：%+v", snap)
	}
	// 空白選字的輸入習慣：選反白的候選。
	s = sentenceSession()
	s.InputStyle.CandidateSpace = "select"
	typedThenEnglish(t, s)
	key(s, proto.KeyDown)
	key(s, proto.KeyDown) // 反白移到第二個（字）
	if snap := key(s, " "); !snap.Handled || snap.Candidates != nil || snap.Text() != "我想打一段字test" {
		t.Fatalf("空白選字：%+v", snap)
	}
	// Enter 選反白的候選，不送出整段。
	s = sentenceSession()
	typedThenEnglish(t, s)
	key(s, proto.KeyDown)
	key(s, proto.KeyDown)
	if snap := key(s, proto.KeyEnter); !snap.Handled || snap.Commit != "" || snap.Candidates != nil || snap.Text() != "我想打一段字test" {
		t.Fatalf("Enter 選字：%+v", snap)
	}
	// Esc 關閉候選、保留組字；之後字母、數字與空白照舊按原樣插在游標處。
	s = sentenceSession()
	typedThenEnglish(t, s)
	key(s, proto.KeyDown)
	if snap := key(s, "a"); !snap.Handled || snap.Candidates == nil || snap.Text() != "我想打一段自test" {
		t.Fatalf("候選開著時字母吃掉不插入：%+v", snap)
	}
	if snap := key(s, proto.KeyEscape); !snap.Handled || snap.Candidates != nil || snap.Text() != "我想打一段自test" {
		t.Fatalf("Esc 關閉候選：%+v", snap)
	}
	typeKeys(s, "a1 ")
	if snap := key(s, proto.KeyEnter); snap.Commit != "我想打一段a1 自test" {
		t.Fatalf("候選關掉後照原樣插入：%+v", snap)
	}
	// 數字鍵盤的數字同主鍵盤數字選字。
	s = sentenceSession()
	typedThenEnglish(t, s)
	if snap := key(s, "KP"+candidateKey(t, key(s, proto.KeyDown), "字")); snap.Candidates != nil || snap.Text() != "我想打一段字test" {
		t.Fatalf("數字鍵盤選字：%+v", snap)
	}
}

// 英文模式沒有組字時 ↓ 照舊交給應用程式（終端機的歷史、清單捲動）。
func TestEnglishModeDownWithoutCompositionGoesToTheApp(t *testing.T) {
	s := sentenceSession()
	s.Handle(proto.Request{Type: proto.Mode, On: true})
	if snap := key(s, proto.KeyDown); snap.Handled || snap.Commit != "" {
		t.Fatalf("沒有組字：%+v", snap)
	}
	// 送出之後也一樣。
	s = sentenceSession()
	typeKeys(s, "ji3")
	s.Handle(proto.Request{Type: proto.Toggle})
	if snap := key(s, proto.KeyEnter); snap.Commit != "我" {
		t.Fatalf("送出：%+v", snap)
	}
	if snap := key(s, proto.KeyDown); snap.Handled {
		t.Fatalf("送出後沒有組字：%+v", snap)
	}
}

// Caps Lock 切英文本來就先送出組字（#173），沒有可選的字，↓ 照舊交給應用程式。
func TestCapsLockEnglishDownUnchanged(t *testing.T) {
	s := sentenceSession()
	s.CapsLockEnglish = true
	typeKeys(s, "ji3vu;3")
	if snap := s.Handle(proto.Request{Type: proto.CapsLock, On: true}); snap.Commit != "我想" || snap.Mode != proto.ModeEnglish {
		t.Fatalf("Caps Lock 先送出組字：%+v", snap)
	}
	if snap := key(s, proto.KeyDown); snap.Handled {
		t.Fatalf("Caps Lock 英文沒有組字，↓ 交給應用程式：%+v", snap)
	}
}
