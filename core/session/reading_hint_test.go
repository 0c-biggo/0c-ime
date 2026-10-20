package session

import (
	"testing"
	"unicode"

	"github.com/0c-biggo/0c-ime/core/proto"
)

// #162：中英混打時照字面顯示的數字按鍵，按空白會組成中文時，快照附上讀音提示（20 → ㄉㄢ），
// 那段組字的樣式是 reading，讓使用者事先看得出空白出中文、Enter 留數字（比照常見注音輸入法：數字灰字、下面附注音）。
// 拼不成音節、數字寫法、已經是注音的組字都沒有提示。
func TestMixedReadingHint(t *testing.T) {
	type hint struct {
		start, length int
		text          string
	}
	for _, tc := range []struct {
		keys string
		want *hint
	}{
		{"2", nil}, // ㄉ 自己沒有字：空白是「2 」
		{"20", &hint{0, 2, "ㄉㄢ"}},
		{"18", &hint{0, 2, "ㄅㄚ"}},
		{"10", &hint{0, 2, "ㄅㄢ"}},
		{"5", &hint{0, 1, "ㄓ"}},
		{"202", nil}, // 打 2026 的途中：拼不成就不提示
		{"2026", nil},
		{"100", nil},
		{"15", nil},
		{"05", nil},  // 順序顛倒
		{"318", nil}, // 18 緊接在字面的 3 後面
		{"3.18", nil},
		{"3.5", nil},
		{"6", nil}, // 單一聲調鍵
		{"ji320", &hint{1, 2, "ㄉㄢ"}},
		{"ji3 20", &hint{2, 2, "ㄉㄢ"}},
		{"hello 20", &hint{6, 2, "ㄉㄢ"}},
		{"15 20", &hint{3, 2, "ㄉㄢ"}},
		{"2;", &hint{0, 2, "ㄉㄤ"}}, // #116 不是數字寫法的按鍵
		{"2.", &hint{0, 2, "ㄉㄡ"}},
		{"ji3,2.", &hint{2, 2, "ㄉㄡ"}}, // 標點照字面，後段是中文
		{",20", nil},
		{"ji3", nil}, // 已經是中文
		{"ru", nil},  // 字母組成的注音本來就顯示成注音
		{"5pm", nil},
	} {
		s := deferredNumberSession()
		snap := mixedType(s, tc.keys)
		got := snap.ReadingHint
		switch {
		case tc.want == nil && got != nil:
			t.Errorf("%q 不該有提示：%+v", tc.keys, *got)
			continue
		case tc.want == nil:
			if reading(snap) != "" {
				t.Errorf("%q 沒有提示卻有 reading 樣式：%+v", tc.keys, snap.Preedit)
			}
			continue
		case got == nil:
			t.Errorf("%q 沒有提示，要 %+v（組字 %+v）", tc.keys, *tc.want, snap.Preedit)
			continue
		}
		if got.Start != tc.want.start || got.Length != tc.want.length || got.Text != tc.want.text {
			t.Errorf("%q 提示 %+v，要 %+v", tc.keys, *got, *tc.want)
		}
		if r := []rune(snap.Text()); got.Start+got.Length != snap.Cursor.Rune || got.Start+got.Length > len(r) || reading(snap) != string(r[got.Start:got.Start+got.Length]) {
			t.Errorf("%q 提示位置 %+v 與組字 %+v（游標 %d）不符", tc.keys, *got, snap.Preedit, snap.Cursor.Rune)
		}
	}
}

// 提示與按空白的結果一致：有提示時空白出中文，沒有提示時空白不增加中文字。
func TestMixedReadingHintMatchesSpace(t *testing.T) {
	for _, keys := range []string{
		"2", "20", "18", "10", "5", "8", "9", "0", "1", "202", "2026", "100", "12", "15", "05", "81", "318", "3.18",
		"ji320", "ji3 20", "hello 20", "15 20", "20 18", "6 18", "2;", "1;", "5/", "2.", "5.", "1.", "10.", "ji3,2.",
		",20", "-20", "10:30", "1,000", "5 15", "1 5 12", "5 5", "8 8", "1 5 8", "5 2;",
	} {
		s := deferredNumberSession()
		before := mixedType(s, keys)
		after := key(s, " ")
		chinese := hanCount(after.Text()) > hanCount(before.Text())
		if hinted := before.ReadingHint != nil; hinted != chinese {
			t.Errorf("%q 提示 %v，空白後 %q（前 %q）", keys, before.ReadingHint, after.Text(), before.Text())
		}
	}
}

// 提示只在可以按空白的時候：候選開著、用 ↓ 選過原按鍵，或沒開中英混打（畫面本來就是注音）時都沒有。
func TestMixedReadingHintOnlyWhenSpaceDecides(t *testing.T) {
	s := deferredNumberSession()
	mixedType(s, "20")
	if snap := key(s, proto.KeyDown); snap.Candidates == nil || snap.ReadingHint != nil {
		t.Fatalf("候選開著：%+v", snap)
	}
	snap := s.Handle(proto.Request{Type: proto.Select, Index: 0})
	if snap.Text() != "20" || snap.ReadingHint != nil {
		t.Fatalf("選了原按鍵：%q %+v", snap.Text(), snap.ReadingHint)
	}
	if got := key(s, proto.KeyEnter).Commit; got != "20" {
		t.Fatalf("Enter 提交 %q", got)
	}

	s = deferredNumberSession()
	s.MixedInput = false
	if snap := mixedType(s, "20"); snap.ReadingHint != nil || snap.Text() != "ㄉㄢ" {
		t.Fatalf("沒開混打：%q %+v", snap.Text(), snap.ReadingHint)
	}

	// Enter 照字面送出數字，提示隨組字一起消失。
	s = deferredNumberSession()
	mixedType(s, "20")
	if snap := key(s, proto.KeyEnter); snap.Commit != "20" || snap.ReadingHint != nil {
		t.Fatalf("Enter：%+v", snap)
	}
}

func reading(snap proto.Snapshot) string {
	out := ""
	for _, g := range snap.Preedit {
		if g.Style == proto.StyleReading {
			out += g.Text
		}
	}
	return out
}

func hanCount(text string) int {
	n := 0
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			n++
		}
	}
	return n
}
