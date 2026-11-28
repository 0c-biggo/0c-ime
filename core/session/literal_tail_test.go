package session

import (
	"reflect"
	"slices"
	"testing"

	"github.com/0c-biggo/0c-ime/core/proto"
)

func literalTailSession(toneless bool) *Session {
	s := newSession()
	s.Toneless, s.Abbreviation, s.MixedWithToneless = toneless, toneless, toneless
	s.MixedInput = true
	return s
}

// literal 把尾端換成字面文字：手機點注音鍵拼出的英文字（APP#33）。ㄇㄣㄣ 是 app 的鍵：不打聲調時是收完的 ㄇㄣ
// 加上未打完的 ㄣ，換掉一個位置連同未打完的注音；打聲調時三鍵都還沒收成音節。
func TestLiteralTailReplacesTheKeys(t *testing.T) {
	for _, c := range []struct {
		name     string
		toneless bool
		keys     string
		length   int
		want     string
	}{
		{"toneless", true, "ㄨㄛㄇㄣㄣ", 1, "我app"},
		{"toned, still open", false, "ㄨㄛˇㄇㄣㄣ", 0, "我app"},
		{"whole composition", true, "ㄇㄣㄣ", 1, "app"},
		{"after a word picked", true, "ㄈㄥㄐㄧㄥㄇㄣㄣ", 1, "風景app"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := literalTailSession(c.toneless)
			typeSymbols(s, c.keys)
			v := s.Handle(proto.Request{Type: proto.Literal, Length: c.length, Word: "app"})
			if slices.Contains(v.Notices, "沒有這個候選") || v.Text() != c.want {
				t.Fatalf("literal：%q %v，要 %q", v.Text(), v.Notices, c.want)
			}
			if v.Cursor.Rune != len([]rune(c.want)) {
				t.Fatalf("游標 %d，要在結尾", v.Cursor.Rune)
			}
			if got := s.Handle(proto.Request{Type: proto.Commit}).Commit; got != c.want {
				t.Fatalf("送出 %q，要 %q", got, c.want)
			}
		})
	}
}

// 中文接在英文字後面打，Enter 一起送出。
func TestLiteralTailThenChinese(t *testing.T) {
	s := literalTailSession(true)
	typeSymbols(s, "ㄨㄛㄇㄣㄣ")
	s.Handle(proto.Request{Type: proto.Literal, Length: 1, Word: "app"})
	typeSymbols(s, "ㄒㄧㄤㄔ")
	if got := s.Handle(proto.Request{Type: proto.Commit}).Commit; got != "我app想吃" {
		t.Fatalf("送出 %q", got)
	}
}

// 退格把字面文字換回原本的注音：組字跟換之前一樣，接著打的鍵也跟沒換過一樣。中間接著打了字、又刪回來，也一樣還原。
func TestLiteralTailBackspaceRestoresTheKeys(t *testing.T) {
	for _, c := range []struct {
		name     string
		toneless bool
		keys     string
		length   int
		between  string // 換了之後接著打、再刪掉的鍵
	}{
		{"toneless", true, "ㄨㄛㄇㄣㄣ", 1, ""},
		{"toned", false, "ㄨㄛˇㄇㄣㄣ", 0, ""},
		{"typed on and erased", true, "ㄨㄛㄇㄣㄣ", 1, "ㄒㄧㄤ"},
		{"toned, typed on and erased", false, "ㄨㄛˇㄇㄣㄣ", 0, "ㄒㄧㄤˇ"},
	} {
		t.Run(c.name, func(t *testing.T) {
			plain := literalTailSession(c.toneless)
			typeSymbols(plain, c.keys)
			want := plain.View()
			s := literalTailSession(c.toneless)
			typeSymbols(s, c.keys)
			s.Handle(proto.Request{Type: proto.Literal, Length: c.length, Word: "app"})
			typeSymbols(s, c.between)
			for range c.between {
				if s.View().Text() == "我app" {
					break
				}
				s.Handle(proto.Request{Type: proto.Backspace})
			}
			if s.View().Text() != "我app" {
				t.Fatalf("刪回英文字後：%q", s.View().Text())
			}
			got := s.Handle(proto.Request{Type: proto.Backspace})
			if !reflect.DeepEqual(got.Preedit, want.Preedit) || got.Cursor != want.Cursor {
				t.Fatalf("退格後 %+v，要跟換之前一樣 %+v", got.Preedit, want.Preedit)
			}
			// 還原之後再退格是一般的退格，接著打的鍵跟沒換過的組字一樣。
			for _, step := range []func(*Session) proto.Snapshot{
				func(x *Session) proto.Snapshot { return x.Handle(proto.Request{Type: proto.Backspace}) },
				func(x *Session) proto.Snapshot { typeSymbols(x, "ㄣㄒㄧㄤ"); return x.View() },
			} {
				if a, b := step(plain), step(s); !reflect.DeepEqual(a.Preedit, b.Preedit) {
					t.Fatalf("之後不同：%+v，沒換過是 %+v", b.Preedit, a.Preedit)
				}
			}
			if a, b := plain.Handle(proto.Request{Type: proto.Commit}).Commit, s.Handle(proto.Request{Type: proto.Commit}).Commit; a != b {
				t.Fatalf("送出 %q，沒換過是 %q", b, a)
			}
		})
	}
}

// 還原只有一次，而且只在英文字還是組字結尾、游標在它後面時：游標移開、英文字後面還有字時退格照一般的刪。
func TestLiteralTailUndoOnlyAtTheEnd(t *testing.T) {
	s := literalTailSession(true)
	typeSymbols(s, "ㄨㄛㄇㄣㄣ")
	s.Handle(proto.Request{Type: proto.Literal, Length: 1, Word: "app"})
	s.Handle(proto.Request{Type: proto.Cursor, To: 2})
	if got := s.Handle(proto.Request{Type: proto.Backspace}); got.Text() != "我pp" {
		t.Fatalf("游標在英文字中間退格：%q", got.Text())
	}
	s = literalTailSession(true)
	typeSymbols(s, "ㄨㄛㄇㄣㄣ")
	s.Handle(proto.Request{Type: proto.Literal, Length: 1, Word: "app"})
	s.Handle(proto.Request{Type: proto.Backspace})
	s.Handle(proto.Request{Type: proto.Literal, Length: 1, Word: "app"})
	s.Handle(proto.Request{Type: proto.Commit})
	typeSymbols(s, "ㄨㄛ")
	if got := s.Handle(proto.Request{Type: proto.Backspace}); got.Text() != "" && got.Text() != "ㄨ" {
		t.Fatalf("送出後的退格不還原上一次的英文字：%q", got.Text())
	}
}

// 換不了的請求不改組字：位置不夠、游標不在結尾、沒有東西可換、文字不合用。
func TestLiteralTailRefuses(t *testing.T) {
	for _, c := range []struct {
		name   string
		length int
		word   string
		cursor bool
	}{
		{"too long", 5, "app", false},
		{"negative", -1, "app", false},
		{"caret moved", 1, "app", true},
		{"empty word", 1, "", false},
		{"newline", 1, "a\npp", false},
		{"too long a word", 1, string(make([]byte, 100)), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := literalTailSession(true)
			typeSymbols(s, "ㄨㄛㄇㄣㄣ")
			if c.cursor {
				s.Handle(proto.Request{Type: proto.Cursor, To: 1})
			}
			before := s.View()
			got := s.Handle(proto.Request{Type: proto.Literal, Length: c.length, Word: c.word})
			if !slices.Contains(got.Notices, "沒有這個候選") || got.Text() != before.Text() {
				t.Fatalf("%q %v，要不變 %q", got.Text(), got.Notices, before.Text())
			}
		})
	}
	s := literalTailSession(true)
	typeSymbols(s, "ㄨㄛ")
	s.Handle(proto.Request{Type: proto.Candidates})
	s.Handle(proto.Request{Type: proto.Close})
	if got := s.Handle(proto.Request{Type: proto.Literal, Word: "app"}); !slices.Contains(got.Notices, "沒有這個候選") {
		t.Fatalf("沒有未打完的注音、Length 0：%q %v", got.Text(), got.Notices)
	}
}

// 一句裡換了兩個字：刪回前一個字時，退格同樣把它換回注音。
func TestLiteralTailTwoWords(t *testing.T) {
	s := literalTailSession(true)
	typeSymbols(s, "ㄨㄛㄇㄣㄣ")
	s.Handle(proto.Request{Type: proto.Literal, Length: 1, Word: "app"})
	afterFirst := s.View()
	typeSymbols(s, "ㄒㄧㄤㄇㄣㄣ")
	if v := s.Handle(proto.Request{Type: proto.Literal, Length: 1, Word: "app"}); v.Text() != "我app想app" {
		t.Fatalf("第二個字：%q", v.Text())
	}
	s.Handle(proto.Request{Type: proto.Backspace}) // 第二個字換回 ㄇㄣㄣ
	for range 3 {
		s.Handle(proto.Request{Type: proto.Backspace})
	}
	if got := s.View(); got.Text() != "我app想" {
		t.Fatalf("刪掉第二個字的鍵：%q", got.Text())
	}
	s.Handle(proto.Request{Type: proto.Backspace})
	if got := s.View(); !reflect.DeepEqual(got.Preedit, afterFirst.Preedit) {
		t.Fatalf("刪回第一個字：%+v，要 %+v", got.Preedit, afterFirst.Preedit)
	}
	if got := s.Handle(proto.Request{Type: proto.Backspace}); got.Text() == "我" || got.Text() == "我ap" {
		t.Fatalf("第一個字沒有換回注音：%q", got.Text())
	}
}
