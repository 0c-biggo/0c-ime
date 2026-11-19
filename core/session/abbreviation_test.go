package session

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

func newAbbreviationSession(on bool) *Session {
	s := newTonelessSession(true)
	s.Abbreviation = on
	return s
}

func TestAbbreviationInitialsOnly(t *testing.T) {
	s := newAbbreviationSession(true)
	typeBare(s, "ㄋㄏ")
	if snap := key(s, proto.KeyEnter); snap.Commit != "你好" {
		t.Fatalf("只打聲母 ㄋㄏ 是你好：%q", snap.Commit)
	}
	s = newAbbreviationSession(true)
	typeBare(s, "ㄒㄗ")
	if snap := key(s, proto.KeyEnter); snap.Commit != "現在" {
		t.Fatalf("ㄒㄗ 是現在：%q", snap.Commit)
	}
}

func TestAbbreviationMixesWithFullSyllables(t *testing.T) {
	s := newAbbreviationSession(true)
	typeBare(s, "ㄋㄧㄏ")
	if snap := key(s, proto.KeyEnter); snap.Commit != "你好" {
		t.Fatalf("ㄋㄧ＋ㄏ 是你好：%q", snap.Commit)
	}
	s = newAbbreviationSession(true)
	typeBare(s, "ㄨㄇ")
	if snap := key(s, proto.KeyEnter); snap.Commit != "我們" {
		t.Fatalf("介音 ㄨ 也能簡拼：%q", snap.Commit)
	}
}

func TestAbbreviationWithTone(t *testing.T) {
	s := newAbbreviationSession(true)
	typeBare(s, "ㄏˋㄧˋ")
	if snap := key(s, proto.KeyEnter); snap.Commit != "會議" {
		t.Fatalf("ㄏˋ 只限四聲：%q", snap.Commit)
	}
	s = newAbbreviationSession(true)
	typeBare(s, "ㄏˊㄧˋ")
	if snap := key(s, proto.KeyEnter); snap.Commit != "回憶" {
		t.Fatalf("ㄏˊ 只限二聲：%q", snap.Commit)
	}
}

func TestAbbreviationKeepsTypedCompleteSyllables(t *testing.T) {
	// 打了聲調的完整音節照舊硬限制，不展開成同聲母的其他音節。
	s := newAbbreviationSession(true)
	typeBare(s, "ㄋㄧˋㄏㄠˇ")
	if snap := key(s, proto.KeyEnter); snap.Commit != "逆好" {
		t.Fatalf("ㄋㄧˋ 只能是逆：%q", snap.Commit)
	}
}

func TestAbbreviationDisplaysTypedSymbols(t *testing.T) {
	s := newAbbreviationSession(true)
	typeBare(s, "ㄐˇ")
	snap := typeBare(s, "ㄐ")
	if got := snap.Text(); got != "ㄐˇㄐ" {
		t.Fatalf("沒有字的簡拼照打的樣子顯示：%q", got)
	}
}

func TestAbbreviationOff(t *testing.T) {
	s := newAbbreviationSession(false)
	if snap := typeBare(s, "ㄋㄏ"); snap.Text() != "ㄏ" {
		t.Fatalf("關閉時新聲母照舊取代舊聲母：%q", snap.Text())
	}
	s = newTonelessSession(false)
	s.Abbreviation = true
	if snap := typeBare(s, "ㄋㄏ"); snap.Text() != "ㄏ" {
		t.Fatalf("沒開聲調選填時簡拼不作用：%q", snap.Text())
	}
}

func TestAbbreviationLearnsLexiconReading(t *testing.T) {
	s := newAbbreviationSession(true)
	typeBare(s, "ㄋㄏ")
	if snap := key(s, proto.KeyEnter); snap.Commit != "你好" {
		t.Fatalf("ㄋㄏ：%q", snap.Commit)
	}
	b, err := json.Marshal(s.model.Store)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(string(b), string([]rune{zhuyin.UntonedMark, zhuyin.AbbrevMark})) {
		t.Fatalf("個人資料不能有內部記號：%s", b)
	}
	if !strings.Contains(string(b), "你好") {
		t.Fatalf("簡拼打出的詞照常以詞庫讀音學習：%s", b)
	}
}

// 手機的注音鍵盤送 symbol、QWERTY 送 key：共存時前者走聲調選填與簡拼，後者走混打。
func symbolType(s *Session, text string) proto.Snapshot {
	var snap proto.Snapshot
	for _, r := range text {
		snap = s.Handle(proto.Request{Type: proto.Symbol, Symbol: string(r)})
	}
	return snap
}

func coexistSession() *Session {
	s := mixedSession()
	s.Toneless, s.Abbreviation, s.MixedWithToneless = true, true, true
	return s
}

func TestMixedWithTonelessSymbolsUseToneless(t *testing.T) {
	s := coexistSession()
	symbolType(s, "ㄨㄛㄔ")
	if snap := s.Handle(proto.Request{Type: proto.Commit}); snap.Commit != "我吃" {
		t.Fatalf("注音鍵不打聲調也能選字：%q", snap.Commit)
	}
}

func TestMixedWithTonelessKeysUseMixed(t *testing.T) {
	s := coexistSession()
	mixedType(s, "hello ")
	if snap := s.Handle(proto.Request{Type: proto.Commit}); !strings.Contains(snap.Commit, "hello") {
		t.Fatalf("實體按鍵照混打判斷英文：%q", snap.Commit)
	}
	s = coexistSession()
	mixedType(s, "ji3")
	if snap := s.Handle(proto.Request{Type: proto.Commit}); snap.Commit != "我" {
		t.Fatalf("實體按鍵打聲調的注音照舊：%q", snap.Commit)
	}
}

func TestMixedWithTonelessSymbolsThenKeys(t *testing.T) {
	s := coexistSession()
	symbolType(s, "ㄨㄛˇ")
	mixedType(s, "hello")
	symbolType(s, "ㄨㄛˇ")
	if snap := s.Handle(proto.Request{Type: proto.Commit}); snap.Commit != "我hello我" {
		t.Fatalf("注音鍵與英文接著打：%q", snap.Commit)
	}
}

func TestMixedWithTonelessPendingSymbolsFinishBeforeKeys(t *testing.T) {
	s := coexistSession()
	symbolType(s, "ㄨㄛ")
	mixedType(s, "hello")
	if snap := s.Handle(proto.Request{Type: proto.Commit}); snap.Commit != "我hello" {
		t.Fatalf("注音鍵打到一半切到字母，字母不能接在注音後面：%q", snap.Commit)
	}
}

// 注音鍵打到一半按實體的編輯鍵：作用在未打完的注音上，不先把它收成音節。Backspace 刪一個注音、Esc 清掉未打完的注音，
// 之後接著用注音鍵打完同一個音節。
func TestMixedWithTonelessEditKeysActOnPendingSymbols(t *testing.T) {
	s := coexistSession()
	symbolType(s, "ㄨㄛ")
	key(s, proto.KeyBackspace)
	if s.comp.String() != "ㄨ" || len(s.syls) != 0 {
		t.Fatalf("Backspace 刪一個注音：comp %q syls %v", s.comp.String(), s.syls)
	}
	symbolType(s, "ㄛˇ")
	if snap := s.Handle(proto.Request{Type: proto.Commit}); snap.Commit != "我" {
		t.Fatalf("退格後接著打：%q", snap.Commit)
	}

	s = coexistSession()
	symbolType(s, "ㄨㄛˇㄔ")
	key(s, proto.KeyEscape)
	if !s.comp.Empty() || len(s.syls) != 1 {
		t.Fatalf("Esc 只清掉未打完的注音：comp %q syls %v", s.comp.String(), s.syls)
	}
	if snap := s.Handle(proto.Request{Type: proto.Commit}); snap.Commit != "我" {
		t.Fatalf("Esc 之後送出：%q", snap.Commit)
	}
}

// 九宮格的鍵還沒完成音節時按實體 Backspace：刪最後一顆鍵（同觸控的 backspace 請求），不先把那串鍵收成音節。
func TestNineKeyPhysicalBackspaceDropsOneKey(t *testing.T) {
	s := nineSession()
	press(s, 7, 2)
	key(s, proto.KeyBackspace)
	if len(s.nineKeys) != 1 || len(s.syls) != 0 {
		t.Fatalf("nine keys %v syls %v", s.nineKeys, s.syls)
	}
	press(s, 2, 0)
	if got := s.snapshot().Text(); got != "你" {
		t.Fatalf("退格後接著打：%q", got)
	}
	s = nineSession()
	press(s, 8, 4, 0, 7, 2)
	key(s, proto.KeyEscape)
	if s.ninePending() || len(s.syls) != 1 {
		t.Fatalf("Esc 只清掉還沒完成的鍵：nine keys %v syls %v", s.nineKeys, s.syls)
	}
}

func TestTonelessWithoutCoexistStillDisablesMixed(t *testing.T) {
	s := mixedSession()
	s.Toneless = true
	if s.mixedInput() {
		t.Fatal("沒開共存時聲調選填照舊停用混打")
	}
}

// 打字途中預覽候選（手機的候選列）不能改變組字狀態：每一鍵都預覽和完全不預覽，之後的畫面要逐鍵相同。
func TestPreviewPendingDoesNotChangeTyping(t *testing.T) {
	for _, text := range []string{"ㄋㄧㄏㄠ", "ㄋㄏ", "ㄨㄛㄇㄣ", "ㄒㄧㄢㄗㄞ", "ㄏㄨㄟˋㄧ", "ㄋㄧㄏㄠˇㄨㄇ"} {
		plain, previewed := newAbbreviationSession(true), newAbbreviationSession(true)
		for _, r := range text {
			req := proto.Request{Type: proto.Symbol, Symbol: string(r)}
			want, _ := json.Marshal(plain.Handle(req))
			got, _ := json.Marshal(previewed.Handle(req))
			previewed.PreviewPending()
			if string(want) != string(got) {
				t.Fatalf("%s 打到 %c：\n沒預覽 %s\n有預覽 %s", text, r, want, got)
			}
		}
		a, b := plain.Handle(proto.Request{Type: proto.Commit}), previewed.Handle(proto.Request{Type: proto.Commit})
		if a.Commit != b.Commit {
			t.Fatalf("%s 送出：沒預覽 %q，有預覽 %q", text, a.Commit, b.Commit)
		}
	}
}

// 預覽的候選和真的開啟候選相同，選字時 ID 才對得上。
func TestPreviewPendingMatchesCandidates(t *testing.T) {
	for _, text := range []string{"ㄋㄧㄏㄠ", "ㄋㄏ", "ㄨㄛㄇㄣ"} {
		s := newAbbreviationSession(true)
		symbolType(s, text)
		preview := s.PreviewPending()
		if preview == nil || len(preview.Items) == 0 {
			t.Fatalf("%s 沒有預覽候選", text)
		}
		snap := s.Handle(proto.Request{Type: proto.Candidates})
		a, _ := json.Marshal(preview.Items)
		b, _ := json.Marshal(snap.Candidates.Items)
		if string(a) != string(b) {
			t.Fatalf("%s：預覽 %s，實際 %s", text, a, b)
		}
	}
}

func TestPreviewPendingNothingToFinish(t *testing.T) {
	s := newAbbreviationSession(true)
	if s.PreviewPending() != nil {
		t.Fatal("沒有組字時沒有預覽")
	}
	symbolType(s, "ㄋㄧˇ")
	if s.PreviewPending() != nil {
		t.Fatal("打完聲調就沒有未打完的注音")
	}
	if off := newTonelessSession(false); off.PreviewPending() != nil {
		t.Fatal("沒開聲調選填時沒有預覽")
	}
}
