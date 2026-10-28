package session

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/english"
	"github.com/0c-biggo/0c-ime/core/inputstyle"
	"github.com/0c-biggo/0c-ime/core/proto"
)

// hostText 模擬宿主收到的文字：送出的字加上交還宿主的按鍵，最後一段是還在組字區的文字。
func hostText(s *Session, keys ...string) (string, proto.Snapshot) {
	var out string
	var snap proto.Snapshot
	for _, k := range keys {
		snap = key(s, k)
		out += snap.Commit
		if !snap.Handled && len([]rune(k)) == 1 {
			out += k
		}
	}
	return out, snap
}

func chars(s string) []string {
	var out []string
	for _, r := range s {
		out = append(out, string(r))
	}
	return out
}

func customStyle(rule func(*inputstyle.Rules)) *Session {
	s := styleSession("0c")
	rule(&s.InputStyle)
	s.English = english.New([]english.Word{{Text: "hello", Weight: 100}, {Text: "help", Weight: 50}, {Text: "users", Weight: 10}, {Text: "i", Weight: 10}, {Text: "a", Weight: 10}})
	return s
}

// #233：靈犀風格只改標點時，混打打到一半的英文收下該風格的半形標點（比照 mixedKey 的 : \ '），
// 不先送出英文、把後面的字母當注音（i'm → ㄛ'淤、a?q=1 → a?ㄆ=、C:\Users → C:＼Users）。
func TestMixedEnglishKeepsStylePunctuation(t *testing.T) {
	for _, tc := range []struct{ punct, keys, want string }{
		{"microsoft", "i'm ", "i'm "},
		{"microsoft", "a?q=1 ", "a?q=1 "},
		{"microsoft", "a<b ", "a<b "},
		{"microsoft", "hello\"world ", "hello\"world "},
		{"microsoft", "C:\\Users ", "C:\\Users "},
		{"standard", "C:\\Users\\hello ", "C:\\Users\\hello "},
		{"chewing", "C:\\Users ", "C:\\Users "},
		{"chewing", "i'm ", "i'm "},
	} {
		s := customStyle(func(r *inputstyle.Rules) { r.Punctuation = tc.punct })
		s.MixedInput = true
		got, _ := hostText(s, append(chars(tc.keys), proto.KeyEnter)...)
		if got != tc.want {
			t.Errorf("%s %q：%q，要 %q", tc.punct, tc.keys, got, tc.want)
		}
	}
	// 風格本身的標點不變：剛打完中文、沒有待定英文時照風格（微軟半形、小麥／新酷音全形反斜線）。
	for punct, want := range map[string]string{"microsoft": "我\\", "standard": "我＼", "chewing": "我＼"} {
		s := customStyle(func(r *inputstyle.Rules) { r.Punctuation = punct })
		s.MixedInput = true
		if got, _ := hostText(s, "j", "i", "3", "\\", proto.KeyEnter); got != want {
			t.Errorf("%s 中文後的 \\：%q，要 %q", punct, got, want)
		}
	}
}

// #233：英文補全打到一半的字遇到微軟半形標點，跟靈犀風格一樣先送出這個字再交給程式，不留在組字區。
func TestEnglishAutocompleteWithMicrosoftPunctuation(t *testing.T) {
	for _, keys := range []string{"a?b ", "a<b ", "say \"hi\" ", "don't "} {
		s := customStyle(func(r *inputstyle.Rules) { r.Punctuation = "microsoft" })
		s.EnglishAutocomplete = true
		s.Handle(proto.Request{Type: proto.Mode, On: true})
		got, snap := hostText(s, chars(keys)...)
		if got != keys || snap.Text() != "" {
			t.Errorf("%q：送出 %q、組字區 %q", keys, got, snap.Text())
		}
	}
}

// #233：小麥的 Caps Lock 小寫（Shift 大寫）開著時英文補全照樣作用；補全不收的字母由這裡送出，不交還宿主。
func TestEnglishAutocompleteWithCapsLower(t *testing.T) {
	s := customStyle(func(r *inputstyle.Rules) { r.CapsLetter = "lower" })
	s.EnglishAutocomplete = true
	s.CapsLockEnglish = true
	s.Handle(proto.Request{Type: proto.CapsLock, On: true})
	snap := mixedType(s, "hel")
	if snap.Text() != "hel" || snap.Candidates == nil || snap.Candidates.Items[0].Text != "hello" {
		t.Fatalf("Caps Lock 英文沒有補全：%+v", snap)
	}
	if snap = key(s, proto.KeyTab); snap.Text() != "hello" {
		t.Fatalf("Tab 接受補全：%+v", snap)
	}
	if got, _ := hostText(s, " ", "H", "e", "l", "p", " "); got != "hello Help " {
		t.Fatalf("Shift 大寫與空白：%q", got)
	}
	// 補全不收的字母（數字後面的識別字）也要吃下來送出，不能交還宿主重播大小寫。
	for _, k := range chars("ab1cd") {
		if snap = key(s, k); !snap.Handled && k != "1" {
			t.Fatalf("%q 交還宿主：%+v", k, snap)
		}
	}
}

// #210：Shift＋字母打小寫。沒在組字時直接送出小寫（不交還宿主打大寫），組字中跟靈犀的大寫一樣留在游標處。
func TestShiftLetterLower(t *testing.T) {
	s := customStyle(func(r *inputstyle.Rules) { r.ShiftLetter = "lower" })
	if snap := key(s, "A"); !snap.Handled || snap.Commit != "a" || snap.Text() != "" {
		t.Fatalf("沒在組字：%+v", snap)
	}
	typeSyl(s, "ㄨㄛˇ")
	if snap := key(s, "B"); snap.Text() != "我b" || snap.Commit != "" {
		t.Fatalf("組字中：%+v", snap)
	}
	typeSyl(s, "ㄐㄧㄥˇ")
	if snap := key(s, proto.KeyEnter); snap.Commit != "我b景" {
		t.Fatalf("送出：%+v", snap)
	}
	// 未打完的注音照靈犀的 Shift＋字母留成符號。
	key(s, "j")
	if snap := key(s, "C"); snap.Text() != "ㄨc" {
		t.Fatalf("未打完的注音：%+v", snap)
	}
	key(s, proto.KeyEscape)
	key(s, proto.KeyEscape)
	// 候選窗開著時同其他按鍵吃掉不動作。
	typeSyl(s, "ㄐㄧㄥˇ")
	key(s, proto.KeyDown)
	if snap := key(s, "D"); !snap.Handled || snap.Commit != "" || snap.Candidates == nil {
		t.Fatalf("候選窗：%+v", snap)
	}
	// 英文模式不受影響：Shift＋字母交還宿主照打大寫。
	e := customStyle(func(r *inputstyle.Rules) { r.ShiftLetter = "lower" })
	e.Handle(proto.Request{Type: proto.Mode, On: true})
	if snap := key(e, "A"); snap.Handled {
		t.Fatalf("英文模式：%+v", snap)
	}
	// 聲調選填可以並用（#150 的 ' 分隔不受影響）。
	u := customStyle(func(r *inputstyle.Rules) { r.ShiftLetter = "lower" })
	u.Toneless = true
	if got, _ := hostText(u, "j", "i", "A", "t", proto.KeyEnter); got != "我a吃" {
		t.Fatalf("聲調選填：%q", got)
	}
	// 全形（Shift＋空白切換）時是全形小寫。
	w := customStyle(func(r *inputstyle.Rules) { r.ShiftLetter = "lower"; r.ShiftSpace = "fullwidth" })
	key(w, proto.KeyShiftSpace)
	if snap := key(w, "A"); snap.Commit != "ａ" {
		t.Fatalf("全形：%+v", snap)
	}
	// 預設（inline）照舊：沒在組字時交還宿主打大寫。
	d := styleSession("0c")
	if snap := key(d, "A"); snap.Handled {
		t.Fatalf("預設改變了：%+v", snap)
	}
}
