package session

import (
	"testing"

	"git.biggo.com/0c/ime/core/english"
	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/lm"
	"git.biggo.com/0c/ime/core/personal"
	"git.biggo.com/0c/ime/core/proto"
	"git.biggo.com/0c/ime/core/vocab"
	"git.biggo.com/0c/ime/core/zhuyin"
)

// englishVocabSession：標準排列 ls 是 ㄋㄠ（孬）、rm 是 ㄐㄩ（居），一聲都有字，混打原本顯示成注音（#146）。
// 英文字典收 lsd、rmb 這類以 ls、rm 開頭的字，走 firstTone 的路徑（英文前綴也照樣顯示一聲注音）。
func englishVocabSession(t *testing.T, preview bool, words ...string) *Session {
	t.Helper()
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"孬", "ㄋㄠ", 1e-5}, {"腦", "ㄋㄠˇ", 2e-4}, {"居", "ㄐㄩ", 1e-4}, {"句", "ㄐㄩˋ", 1e-4},
		{"我", "ㄨㄛˇ", 1e-2}, {"是", "ㄕˋ", 5e-3},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	m := personal.NewModel(lex, sys)
	s := New(m)
	s.MixedInput = true
	s.Preview02 = preview
	var dict []english.Word
	for _, w := range []string{"lsd", "rmb", "hello", "git"} {
		dict = append(dict, english.Word{Text: w, Weight: 65})
	}
	s.English = english.New(dict)
	if len(words) > 0 {
		var entries []vocab.Entry
		for _, w := range words {
			entries = append(entries, vocab.Entry{Text: w})
		}
		if _, err := m.VocabAdd(entries, vocab.SourceManual); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestMixedVocabEnglishCommitsLiterally(t *testing.T) {
	for _, tc := range []struct{ keys, shown, commit string }{
		{"ls", "ls", "ls"},
		{"ls ", "ls ", "ls "},
		{"rm", "rm", "rm"},
		{"rm ", "rm ", "rm "},
		{"ls -la", "ls -la", "ls -la"},
		{"kubectl ", "kubectl ", "kubectl "},
		{"git-lfs ", "git-lfs ", "git-lfs "},
		{"ipv6 ", "ipv6 ", "ipv6 "}, // 英文詞裡的聲調鍵打到一半不當聲調
		{"ji3ls ", "我ls ", "我ls "},
		// 有打聲調照舊組中文
		{"ls3", "腦", "腦"},
		{"rm4", "句", "句"},
	} {
		s := englishVocabSession(t, true, "ls", "rm", "kubectl", "git-lfs", "ipv6")
		if got := mixedType(s, tc.keys).Text(); got != tc.shown {
			t.Errorf("%q 顯示 %q，要 %q", tc.keys, got, tc.shown)
		}
		if got := key(s, proto.KeyEnter).Commit; got != tc.commit {
			t.Errorf("%q 送出 %q，要 %q", tc.keys, got, tc.commit)
		}
	}
}

// 沒加英文詞、0.2.0 開關關閉、只加了大小寫不同的詞（LS）：行為和原本相同。
func TestMixedVocabEnglishUnchangedWithoutWord(t *testing.T) {
	for _, tc := range []struct {
		name    string
		preview bool
		words   []string
	}{
		{"沒有自訂詞", true, nil},
		{"開關關閉", false, []string{"ls", "rm"}},
		{"大小寫不同", true, []string{"LS", "RM", "lsx"}},
	} {
		for _, c := range []struct{ keys, shown, commit string }{
			{"ls", "ㄋㄠ", "ㄋㄠ"},
			{"ls ", "孬", "孬"},
			{"rm ", "居", "居"},
			{"ls3", "腦", "腦"},
		} {
			s := englishVocabSession(t, tc.preview, tc.words...)
			if got := mixedType(s, c.keys).Text(); got != c.shown {
				t.Errorf("%s：%q 顯示 %q，要 %q", tc.name, c.keys, got, c.shown)
			}
			if got := key(s, proto.KeyEnter).Commit; got != c.commit {
				t.Errorf("%s：%q 送出 %q，要 %q", tc.name, c.keys, got, c.commit)
			}
		}
	}
}

// ↓ 仍可選注音解釋：選了 ㄋㄠ 之後空白照舊完成一聲。
func TestMixedVocabEnglishDownPicksPhonetic(t *testing.T) {
	s := englishVocabSession(t, true, "ls")
	mixedType(s, "ls")
	key(s, proto.KeyDown)
	if s.cand == nil || len(s.cand.items) < 2 || s.cand.items[0].Entry.Surface != "ls" {
		t.Fatalf("↓ 第一個是原按鍵：%+v", s.cand)
	}
	if snap := pickText(t, s, "ㄋㄠ"); snap.Text() != "ㄋㄠ" {
		t.Fatalf("選注音解釋：%q", snap.Text())
	}
	if snap := key(s, " "); snap.Text() != "孬" {
		t.Fatalf("選了注音後空白完成一聲：%q", snap.Text())
	}
}

// 英文詞照字面時不提示英文補全或更正（ls → lsd）。
func TestMixedVocabEnglishNoSuggestion(t *testing.T) {
	s := englishVocabSession(t, true, "ls")
	s.English = english.New([]english.Word{{Text: "lsblk", Weight: 65}})
	if snap := mixedType(s, "ls"); snap.Suggestion != "" || snap.Text() != "ls" {
		t.Fatalf("%+v", snap)
	}
}

// 英文模式與非混打模式不受影響。
func TestMixedVocabEnglishOtherModes(t *testing.T) {
	for _, words := range [][]string{nil, {"ls"}} {
		s := englishVocabSession(t, true, words...)
		s.Handle(proto.Request{Type: proto.Toggle})
		for _, k := range "ls " {
			if snap := key(s, string(k)); snap.Handled {
				t.Errorf("%v 英文模式：%q 要直接交給應用程式：%+v", words, k, snap)
			}
		}
		s = englishVocabSession(t, true, words...)
		s.MixedInput = false
		snap := mixedType(s, "ls ")
		if snap.Text() != "孬" {
			t.Errorf("%v 非混打：%q", words, snap.Text())
		}
	}
}
