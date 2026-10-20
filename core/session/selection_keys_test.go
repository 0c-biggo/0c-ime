package session

import (
	"strings"
	"testing"

	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/lm"
	"git.biggo.com/0c/ime/core/personal"
	"git.biggo.com/0c/ime/core/proto"
	"git.biggo.com/0c/ime/core/zhuyin"
)

// manySession 的 ㄕˋ 有 14 個單字，候選窗有兩頁（每頁 10 項，最後一項是原樣注音）。
func manySession(keys string) *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for i, w := range []rune("是事市試式世室士視示適勢誓釋") {
		lex.Add(lexicon.Entry{Surface: string(w), Reading: zhuyin.ParseReading("ㄕˋ"), PRW: 1})
		sys.SetUni(string(w), 1e-2/float64(i+1))
	}
	s := New(personal.NewModel(lex, sys))
	s.SelectionKeys = keys
	return s
}

func openMany(t *testing.T, s *Session) proto.Snapshot {
	t.Helper()
	typeSyl(s, "ㄕˋ")
	snap := key(s, proto.KeyDown)
	if snap.Candidates == nil || len(snap.Candidates.Items) != 10 || snap.Candidates.Pages < 2 {
		t.Fatalf("要有兩頁候選：%+v", snap.Candidates)
	}
	return snap
}

func itemKeys(snap proto.Snapshot) string {
	var b strings.Builder
	for _, item := range snap.Candidates.Items {
		b.WriteString(item.Key)
	}
	return b.String()
}

// #112：候選的鍵標由核心給，外殼照著畫；預設是 1–0，每一頁都從頭標。
func TestSelectionKeysDefaultLabelsDigits(t *testing.T) {
	for _, keys := range []string{"", "1234567890", "abc", "1234qwera", "1123qweras", "1234QWERAS", "ㄅ234qweras"} {
		s := manySession(keys)
		snap := openMany(t, s)
		if got := itemKeys(snap); got != "1234567890" {
			t.Fatalf("SelectionKeys=%q 的鍵標：%q", keys, got)
		}
		if snap := key(s, proto.KeyPageDown); itemKeys(snap) != "1234567890"[:len(snap.Candidates.Items)] {
			t.Fatalf("第二頁也從 1 開始：%q", itemKeys(snap))
		}
		// 預設時 q 跟以前一樣沒有作用（候選開著時吃掉）。
		key(s, proto.KeyPageUp)
		before := s.snapshot().Text()
		if snap := key(s, "q"); !snap.Handled || snap.Candidates == nil || snap.Text() != before {
			t.Fatalf("SelectionKeys=%q 時 q 不選字：%+v", keys, snap)
		}
	}
}

func TestSelectionKeysLettersSelectByPosition(t *testing.T) {
	for _, tc := range []struct {
		keys, press string
		index       int
	}{
		{"1234qweras", "q", 4}, {"1234qweras", "w", 5}, {"1234qweras", "s", 9}, {"1234qweras", "1", 0},
		{"asdfghjkl;", "a", 0}, {"asdfghjkl;", "l", 8}, {"asdfghjkl;", ";", 9},
		// 數字照舊依位置選字（舊外殼、自己畫候選窗的程式只顯示數字），數字鍵盤也是。
		{"1234qweras", "5", 4}, {"1234qweras", "0", 9}, {"asdfghjkl;", "1", 0}, {"asdfghjkl;", "KP3", 2},
	} {
		s := manySession(tc.keys)
		snap := openMany(t, s)
		if got := itemKeys(snap); got != tc.keys {
			t.Fatalf("鍵標 %q，要 %q", got, tc.keys)
		}
		want := snap.Candidates.Items[tc.index].Text
		if snap := key(s, tc.press); !snap.Handled || snap.Candidates != nil || snap.Text() != want {
			t.Fatalf("%s 按 %s 選第 %d 項 %q：%+v", tc.keys, tc.press, tc.index+1, want, snap)
		}
	}
}

func TestSelectionKeysOnSecondPage(t *testing.T) {
	s := manySession("1234qweras")
	openMany(t, s)
	snap := key(s, proto.KeyPageDown)
	if got := itemKeys(snap); got != "1234qweras"[:len(snap.Candidates.Items)] {
		t.Fatalf("第二頁鍵標：%q", got)
	}
	want := snap.Candidates.Items[1].Text
	if snap := key(s, "2"); snap.Text() != want {
		t.Fatalf("第二頁按 2：%q，要 %q", snap.Text(), want)
	}
	// 第二頁沒有第 6 項：w 吃掉、不選字。
	s = manySession("1234qweras")
	openMany(t, s)
	snap = key(s, proto.KeyPageDown)
	if len(snap.Candidates.Items) >= 6 {
		t.Skip("第二頁項目多於預期")
	}
	if snap := key(s, "w"); !snap.Handled || snap.Candidates == nil {
		t.Fatalf("沒有那一項時不選字：%+v", snap)
	}
}

// 沒有候選窗時，選字鍵就是注音鍵：q 是 ㄆ、a 是 ㄇ、; 是 ㄤ。
func TestSelectionKeysOnlyApplyWhileCandidatesOpen(t *testing.T) {
	s := manySession("asdfghjkl;")
	if snap := key(s, "a"); !snap.Handled || snap.Text() != "ㄇ" {
		t.Fatalf("a 是 ㄇ：%+v", snap)
	}
	if snap := key(s, ";"); snap.Text() != "ㄇㄤ" {
		t.Fatalf("; 是 ㄤ：%q", snap.Text())
	}
}

// 符號選單與 Emoji 選單也用同一組選字鍵。
func TestSelectionKeysInEmojiPalette(t *testing.T) {
	s := newSession()
	s.SelectionKeys = "1234qweras"
	snap := key(s, "=")
	if snap.Candidates == nil || snap.Candidates.Kind == "" {
		t.Fatalf("= 開 Emoji 選單：%+v", snap)
	}
	if got := itemKeys(snap); got != "1234qweras"[:len(snap.Candidates.Items)] {
		t.Fatalf("Emoji 鍵標：%q", got)
	}
	want := snap.Candidates.Items[4].Text
	if snap := key(s, "q"); snap.Commit != want && snap.Text() != want {
		t.Fatalf("q 選第 5 個 Emoji %q：%+v", want, snap)
	}
}

// 英文模式的自動補字候選只能用 Tab 接受，數字是文字：不標鍵。
func TestSelectionKeysEnglishAutocompleteUnlabeled(t *testing.T) {
	s := autocompleteSession()
	s.SelectionKeys = "1234qweras"
	snap := mixedType(s, "hel")
	if snap.Candidates == nil {
		t.Fatal("要有補字候選")
	}
	if got := itemKeys(snap); got != "" {
		t.Fatalf("英文補字不標鍵：%q", got)
	}
}
