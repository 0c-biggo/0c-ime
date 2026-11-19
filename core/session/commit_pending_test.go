package session

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/proto"
)

// #322：外殼替使用者結束組字（失焦、切到別的輸入法、App 結束組字、按 App 的快捷鍵）與 Caps Lock 切英文時，
// 未打完的注音不當成文字送進文件，只送已組好的字——跟單按 Shift 切英文（#90）一樣。
// 使用者自己按 Enter 照舊原樣送出（要打注音符號的人靠它）。

func TestCommitRequestDropsPendingZhuyin(t *testing.T) {
	for name, tc := range map[string]struct {
		keys, commit string
	}{
		"只有未打完的注音": {"g", ""},
		"前面有中文":    {"ji3vu;3g", "我想"},
		"兩個注音符號":   {"ji3vu", "我"},
	} {
		s := newSession()
		typeKeys(s, tc.keys)
		snap := s.Handle(proto.Request{Type: proto.Commit})
		if snap.Commit != tc.commit || snap.Text() != "" || s.Composing() {
			t.Fatalf("%s：外殼結束組字只送出已組好的字 %q：%+v", name, tc.commit, snap)
		}
	}
}

func TestEnterStillCommitsPendingZhuyinLiterally(t *testing.T) {
	s := newSession()
	typeKeys(s, "ji3vu;3g")
	if snap := key(s, proto.KeyEnter); snap.Commit != "我想ㄕ" {
		t.Fatalf("使用者按 Enter 照畫面原樣送出：%+v", snap)
	}
}

func TestCommitRequestWithPendingCandidatesOpen(t *testing.T) {
	s := newSession()
	typeKeys(s, "ji3t") // 我 + 未打完的 ㄔ
	if snap := key(s, proto.KeyDown); snap.Candidates == nil || s.cand == nil || !s.cand.pending {
		t.Fatalf("前置：未打完注音的候選應開啟：%+v", snap)
	}
	if snap := s.Handle(proto.Request{Type: proto.Commit}); snap.Commit != "我" || snap.Candidates != nil || s.Composing() {
		t.Fatalf("候選開著也只送已組好的字：%+v", snap)
	}
}

func TestCommitRequestKeepsMixedEnglishLiteral(t *testing.T) {
	s := mixedSession()
	typeSyl(s, "ㄨㄛˇ")
	if snap := mixedType(s, "hel"); snap.Text() != "我hel" {
		t.Fatalf("前置：暫存按鍵顯示成英文：%+v", snap)
	}
	// 畫面上是英文，不是未打完的注音：照畫面送出。
	if snap := s.Handle(proto.Request{Type: proto.Commit}); snap.Commit != "我hel" {
		t.Fatalf("顯示成英文的暫存按鍵照舊送出：%+v", snap)
	}
	s = mixedSession()
	typeSyl(s, "ㄨㄛˇ")
	if snap := mixedType(s, "xu"); snap.Text() != "我ㄌㄧ" {
		t.Fatalf("前置：暫存按鍵顯示成注音：%+v", snap)
	}
	if snap := s.Handle(proto.Request{Type: proto.Commit}); snap.Commit != "我" {
		t.Fatalf("顯示成注音的暫存按鍵不送出：%+v", snap)
	}
}

func TestCommitRequestTonelessFinishesSyllable(t *testing.T) {
	// 聲調選填（#150）：已是完整音節的注音照解碼送出，跟 Shift 切英文（setMode）一樣。
	s := newTonelessSession(true)
	typeKeys(s, "ji3g")
	if snap := s.Handle(proto.Request{Type: proto.Commit}); len([]rune(snap.Commit)) != 2 {
		t.Fatalf("聲調選填時完整音節照常送出：%+v", snap)
	}
}

func TestCapsLockDropsPendingZhuyin(t *testing.T) {
	s := capsSession()
	typeKeys(s, "ji3vu;3g")
	snap := caps(s, true)
	if snap.Commit != "我想" || snap.Text() != "" || snap.Mode != proto.ModeEnglish {
		t.Fatalf("開 Caps Lock 切英文只送已組好的字：%+v", snap)
	}
	s = capsSession()
	key(s, "g")
	if snap := caps(s, true); snap.Commit != "" || s.Composing() || snap.Mode != proto.ModeEnglish {
		t.Fatalf("只有未打完的注音時什麼都不送：%+v", snap)
	}
	if snap := key(s, "a"); snap.Handled {
		t.Fatalf("接著打英文直接交給應用程式：%+v", snap)
	}
}

// unfinishedFree 是快照裡不標 Unfinished 的組字：應用程式或框架自己把組字送進文件時（Fcitx5 的用戶端組字）
// 會送出的文字。
func unfinishedFree(snap proto.Snapshot) (kept, unfinished string) {
	for _, g := range snap.Preedit {
		if g.Unfinished {
			unfinished += g.Text
		} else {
			kept += g.Text
		}
	}
	return kept, unfinished
}

// #322 的 Linux 外殼：Fcitx5 失焦時由用戶端照畫面送出組字，外殼只能靠 Unfinished（DontCommit）把未打完的注音
// 留在文件外。標出來的範圍必須剛好是 commit 請求不送的部分，否則文件與學到的字不一致。
func TestUnfinishedSegmentsMatchCommitRequest(t *testing.T) {
	type step func(*Session) proto.Snapshot
	keys := func(k string) step { return func(s *Session) proto.Snapshot { return typeKeys(s, k) } }
	named := func(k string) step { return func(s *Session) proto.Snapshot { return key(s, k) } }
	mixed := func(k string) step { return func(s *Session) proto.Snapshot { return mixedType(s, k) } }
	syl := func(y string) step { return func(s *Session) proto.Snapshot { return typeSyl(s, y) } }
	for name, tc := range map[string]struct {
		session          func() *Session
		steps            []step
		kept, unfinished string
	}{
		"只有未打完的注音":     {newSession, []step{keys("g")}, "", "ㄕ"},
		"已組好的字加未打完的注音": {newSession, []step{keys("ji3vu;3g")}, "我想", "ㄕ"},
		"兩個注音符號":       {newSession, []step{keys("ji3vu")}, "我", "ㄒㄧ"},
		"句中的未打完注音":     {newSession, []step{keys("ji3"), named(proto.KeyLeft), keys("g")}, "我", "ㄕ"},
		"候選開著":         {newSession, []step{keys("ji3t"), named(proto.KeyDown)}, "我", "ㄔ"},
		"只有組好的字":       {newSession, []step{keys("ji3vu;3")}, "我想", ""},
		"混打顯示成英文":      {mixedSession, []step{syl("ㄨㄛˇ"), mixed("hel")}, "我hel", ""},
		"混打顯示成注音":      {mixedSession, []step{syl("ㄨㄛˇ"), mixed("xu")}, "我", "ㄌㄧ"},
	} {
		s := tc.session()
		var snap proto.Snapshot
		for _, st := range tc.steps {
			snap = st(s)
		}
		if kept, unfinished := unfinishedFree(snap); kept != tc.kept || unfinished != tc.unfinished {
			t.Fatalf("%s：未打完的注音應標成 %q、其餘 %q：%+v", name, tc.unfinished, tc.kept, snap.Preedit)
		}
		if got := s.Handle(proto.Request{Type: proto.Commit}); got.Commit != tc.kept {
			t.Fatalf("%s：commit 請求送出 %q，應與畫面上不標 Unfinished 的 %q 相同", name, got.Commit, tc.kept)
		}
	}
}

func TestUnfinishedTonelessSyllable(t *testing.T) {
	// 聲調選填（#150）：完整音節在 commit 請求時換成解碼的字，畫面上的注音不能照樣送進文件，也標 Unfinished。
	s := newTonelessSession(true)
	if kept, unfinished := unfinishedFree(typeKeys(s, "ji3g")); kept != "我" || unfinished != "ㄕ" {
		t.Fatalf("聲調選填時未打聲調的注音也不照畫面送出：%q %q", kept, unfinished)
	}
}
