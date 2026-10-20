package session

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/proto"
)

// #173：「Caps Lock 開著時直接打英文」。外殼送 caps_lock 請求回報鎖定狀態；核心決定模式。

func capsSession() *Session { s := newSession(); s.CapsLockEnglish = true; return s }

func caps(s *Session, on bool) proto.Snapshot {
	return s.Handle(proto.Request{Type: proto.CapsLock, On: on})
}

// 設定關閉（零值）：caps_lock 回未處理、什麼都不改，注音模式照舊（0.2.1，#168）。
func TestCapsLockOffKeepsZhuyin(t *testing.T) {
	s := newSession()
	typeKeys(s, "ji3")
	if snap := caps(s, true); snap.Handled || snap.Commit != "" || snap.Mode != proto.ModeZhuyin || snap.CapsLock || snap.Text() != "我" {
		t.Fatalf("設定關閉時 caps_lock 不應改變任何東西：%+v", snap)
	}
	if snap := typeKeys(s, "vu;3"); snap.Text() != "我想" || snap.Mode != proto.ModeZhuyin {
		t.Fatalf("設定關閉時照打注音 ji3vu;3 → 我想：%+v", snap)
	}
}

// 開啟：沒在組字時切到英文，字母、數字交給應用程式（大寫、半形由系統決定）；關閉回到注音。
func TestCapsLockEnglishIdle(t *testing.T) {
	s := capsSession()
	snap := caps(s, true)
	if !snap.Handled || snap.Mode != proto.ModeEnglish || !snap.CapsLock || !snap.CapsEnglish || snap.Commit != "" {
		t.Fatalf("開 Caps Lock 應切到英文：%+v", snap)
	}
	for _, k := range []string{"A", "B", "C", "1", "2", "3", ",", "."} {
		if snap := key(s, k); snap.Handled || snap.Text() != "" {
			t.Fatalf("英文模式的 %s 應交給應用程式：%+v", k, snap)
		}
	}
	// 同樣的狀態重送不動作。
	if snap := caps(s, true); !snap.Handled || snap.Mode != proto.ModeEnglish {
		t.Fatalf("重送開啟應維持英文：%+v", snap)
	}
	snap = caps(s, false)
	if !snap.Handled || snap.Mode != proto.ModeZhuyin || snap.CapsLock || snap.CapsEnglish {
		t.Fatalf("關 Caps Lock 應回到注音：%+v", snap)
	}
	if snap := typeKeys(s, "ji3vu;3"); snap.Text() != "我想" {
		t.Fatalf("回到注音後 ji3vu;3 → 我想：%+v", snap)
	}
}

// 組字中開 Caps Lock：先送出組字（同 macOS 的 #84），再切到英文。
func TestCapsLockCommitsComposition(t *testing.T) {
	s := capsSession()
	typeKeys(s, "ji3vu;3")
	key(s, "v") // 未打完的 ㄒ
	snap := caps(s, true)
	if snap.Commit != "我想ㄒ" || snap.Text() != "" || snap.Mode != proto.ModeEnglish || !snap.Handled {
		t.Fatalf("組字中開 Caps Lock 應先送出組字再切英文：%+v", snap)
	}
	// 候選窗開著時也一樣送出。
	s = capsSession()
	typeKeys(s, "ji3")
	key(s, proto.KeyDown)
	if snap := caps(s, true); snap.Commit == "" || snap.Candidates != nil || snap.Mode != proto.ModeEnglish {
		t.Fatalf("候選開著時開 Caps Lock 應送出組字、關閉候選：%+v", snap)
	}
}

// 用 Shift 切到英文後再開關 Caps Lock：只影響大小寫，不改變中英模式（RIME 0.9.22 修過的坑）。
func TestCapsLockAfterShiftEnglishKeepsEnglish(t *testing.T) {
	s := capsSession()
	typeKeys(s, "ji3")
	s.Handle(proto.Request{Type: proto.Toggle}) // 保留「我」，切到英文
	snap := caps(s, true)
	if snap.Mode != proto.ModeEnglish || snap.CapsEnglish || snap.Commit != "" || snap.Text() != "我" {
		t.Fatalf("已是英文模式時開 Caps Lock 不送出組字、不算 Caps Lock 帶來的英文：%+v", snap)
	}
	if snap := caps(s, false); snap.Mode != proto.ModeEnglish || snap.Text() != "我" {
		t.Fatalf("關 Caps Lock 應維持使用者切的英文：%+v", snap)
	}
}

// Caps Lock 開著時單按 Shift 照常切換；之後關掉 Caps Lock 不再改變使用者選的模式。
func TestCapsLockShiftToggleWins(t *testing.T) {
	s := capsSession()
	caps(s, true)
	snap := s.Handle(proto.Request{Type: proto.Toggle})
	if snap.Mode != proto.ModeZhuyin || snap.CapsEnglish || !snap.CapsLock {
		t.Fatalf("Caps Lock 開著時 Shift 應切回注音：%+v", snap)
	}
	// 外殼在注音模式不理 Caps Lock（#168），送小寫注音鍵。
	if snap := typeKeys(s, "ji3"); snap.Text() != "我" {
		t.Fatalf("切回注音後照打注音：%+v", snap)
	}
	if snap := caps(s, false); snap.Mode != proto.ModeZhuyin || snap.Text() != "我" {
		t.Fatalf("關 Caps Lock 應維持注音、組字不動：%+v", snap)
	}
	// 再切一次到英文：使用者選的英文，關 Caps Lock 不改。
	s = capsSession()
	caps(s, true)
	s.Handle(proto.Request{Type: proto.Toggle})
	s.Handle(proto.Request{Type: proto.Toggle})
	if snap := caps(s, false); snap.Mode != proto.ModeEnglish {
		t.Fatalf("Shift 兩次後是使用者選的英文：%+v", snap)
	}
}

// 外殼重設模式（換欄位、重開 session）時忘掉 Caps Lock 狀態，下一次 caps_lock 重新套用。
func TestCapsLockModeRequestResyncs(t *testing.T) {
	s := capsSession()
	caps(s, true)
	snap := s.Handle(proto.Request{Type: proto.Mode, On: false})
	if snap.Mode != proto.ModeZhuyin || snap.CapsLock || snap.CapsEnglish {
		t.Fatalf("mode 請求應清掉 Caps Lock 狀態：%+v", snap)
	}
	if snap := caps(s, true); snap.Mode != proto.ModeEnglish || !snap.CapsEnglish {
		t.Fatalf("重送開啟應再切到英文：%+v", snap)
	}
	// 外殼記得英文（Windows 重開 session 還原）時開 Caps Lock：已是英文，關掉也不改。
	s = capsSession()
	s.Handle(proto.Request{Type: proto.Mode, On: true})
	caps(s, true)
	if snap := caps(s, false); snap.Mode != proto.ModeEnglish {
		t.Fatalf("外殼設成英文後關 Caps Lock 應維持英文：%+v", snap)
	}
}

// 暫時英文等於英文模式：Ctrl＋標點、中英混打、英文補字都照英文模式。
func TestCapsLockEnglishActsAsEnglishMode(t *testing.T) {
	s := capsSession()
	s.CtrlPunct = true
	caps(s, true)
	if snap := key(s, proto.KeyCtrlComma); snap.Handled {
		t.Fatalf("暫時英文的 Ctrl+, 應交給應用程式：%+v", snap)
	}
	s = capsSession()
	s.MixedInput = true
	caps(s, true)
	if snap := typeKeys(s, "ABC123"); snap.Handled || snap.Text() != "" {
		t.Fatalf("中英混打開著時暫時英文的 ABC123 應交給應用程式：%+v", snap)
	}
	if snap := caps(s, false); snap.Mode != proto.ModeZhuyin {
		t.Fatalf("關 Caps Lock 回到注音：%+v", snap)
	}
	if snap := typeKeys(s, "ji3vu;3"); snap.Text() != "我想" {
		t.Fatalf("混打回到注音後 ji3vu;3 → 我想：%+v", snap)
	}
}
