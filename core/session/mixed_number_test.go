package session

import (
	"testing"

	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/lm"
	"git.biggo.com/0c/ime/core/personal"
	"git.biggo.com/0c/ime/core/proto"
	"git.biggo.com/0c/ime/core/zhuyin"
)

// deferredNumberSession 收了 #110 例子用到的字詞：按鍵全是數字或標點的一聲音節（ㄅㄤ、ㄓ、ㄅㄢ、ㄅㄚ、ㄉㄢ）
// 與緊接著的音節能組成的詞，以及數字後面常見的量詞。單個的機率壓到比「單」「個」分開還低，解碼不會連成詞。
func deferredNumberSession() *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"我", "ㄨㄛˇ", 1e-2}, {"有", "ㄧㄡˇ", 5e-3}, {"是", "ㄕˋ", 5e-3}, {"上", "ㄕㄤˋ", 2e-3},
		{"幫", "ㄅㄤ", 3e-4}, {"忙", "ㄇㄤˊ", 5e-4}, {"幫忙", "ㄅㄤ ㄇㄤˊ", 2e-4},
		{"之", "ㄓ", 1e-3}, {"知", "ㄓ", 1e-4}, {"後", "ㄏㄡˋ", 1e-3}, {"之後", "ㄓ ㄏㄡˋ", 1e-3},
		{"班", "ㄅㄢ", 3e-4}, {"班上", "ㄅㄢ ㄕㄤˋ", 2e-5},
		{"八", "ㄅㄚ", 5e-4}, {"個", "ㄍㄜˋ", 2e-3}, {"各", "ㄍㄜˋ", 1e-3}, {"八個", "ㄅㄚ ㄍㄜˋ", 4e-5},
		{"歲", "ㄙㄨㄟˋ", 3e-4}, {"八歲", "ㄅㄚ ㄙㄨㄟˋ", 3e-5},
		{"單", "ㄉㄢ", 2e-4}, {"單是", "ㄉㄢ ㄕˋ", 1e-5}, {"單個", "ㄉㄢ ㄍㄜˋ", 1e-9},
		{"但", "ㄉㄢˋ", 1e-3}, {"但是", "ㄉㄢˋ ㄕˋ", 5e-4},
		{"位", "ㄨㄟˋ", 5e-4}, {"單位", "ㄉㄢ ㄨㄟˋ", 3e-4},
		{"啊", "ㄚ", 1e-3}, {"哀", "ㄞ", 2e-4}, {"安", "ㄢ", 3e-4}, {"兒", "ㄦ", 1e-4}, {"點", "ㄉㄧㄢˇ", 5e-4}, {"年", "ㄋㄧㄢˊ", 5e-4},
		{"當", "ㄉㄤ", 1e-3}, {"張", "ㄓㄤ", 5e-4}, {"燈", "ㄉㄥ", 2e-4}, {"都", "ㄉㄡ", 2e-3}, {"週", "ㄓㄡ", 3e-4}, {"周", "ㄓㄡ", 5e-4},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	s := New(personal.NewModel(lex, sys))
	s.MixedInput = true
	return s
}

// #110、#162：句首（或接在空白、英文、數字後）按鍵全是數字或標點、拼得成一聲字的音節接空白就是中文，
// 接著打的音節與它一起解碼。關閉混打時本來就是這些詞。
func TestMixedDeferredNumberBecomesChineseWord(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"1; a;6", "幫忙"},            // ㄅㄤ ㄇㄤˊ，客戶回報「1; 忙」
		{"5 c.4", "之後"},             // ㄓ ㄏㄡˋ，客戶回報「5 後」
		{"10 g;4", "班上"},            // ㄅㄢ ㄕㄤˋ
		{"18 ek4", "八個"},            // ㄅㄚ ㄍㄜˋ：八本身是數字，量詞不擋（見 TestMixedDeferredNumberStaysNumber 的「我有 18 歲」）
		{"20 g4", "單是"},             // ㄉㄢ ㄕˋ（一聲），關閉混打時也是「單是」
		{"204g4", "但是"},             // 「但是」的 ㄉㄢˋ 是四聲，聲調鍵本來就組成中文
		{"18 njo4", "八歲"},           // 句首的八歲
		{"hello 5 c.4", "hello 之後"}, // 英文後面空一格再打中文（客戶的習慣）
		{"ji3 1; a;6", "我 幫忙"},
		{"5 c.4 ", "之後 "},
	} {
		s := deferredNumberSession()
		snap := mixedType(s, tc.keys)
		if snap.Text() != tc.want {
			t.Errorf("%q 顯示 %q，要 %q", tc.keys, snap.Text(), tc.want)
		}
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q 提交 %q，要 %q", tc.keys, got, tc.want)
		}
	}
}

// #78 的數字不能退步：拼不成一聲字的數字（15、12、2024）、小數、時間、條列接空白都維持數字，後面接的中文照常；
// 拼得成字的數字（#162 的 20 單）要數字時按 Enter，組字區裡的其他字一起送出。
func TestMixedDeferredNumberStaysNumber(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"15 ", "15 "},
		{"20", "20"}, // 沒按空白就按 Enter：照字面送出數字
		{"ji3u.3 20", "我有 20"},
		{"1. ", "1. "},   // 條列編號：ㄅㄡ 組不成音節
		{"10. ", "10. "}, // ㄅㄢㄡ 組不成音節
		{"1, ", "1, "},
		{"3.5 ", "3.5 "}, // 3 是聲調鍵，先照字面送出，待定的只剩 .5
		{"ji33.5 ", "我3.5 "},
		{"3.14 ", "3.14 "},
		{"1,000 ", "1,000 "},
		{"ji3u.3 15 ek4", "我有 15 個"},
		{"15 ek4", "15 個"},
		{"15 jo4", "15 位"},
		{"ji3u.3 12 njo4", "我有 12 歲"},
		{"15 ji3", "15 我"},
		{"15 hello", "15 hello"},
		{"12 15 ", "12 15 "},
		{"318 ", "318 "},     // 3 是聲調鍵先照字面送出，待定的 18 是 318 的一部分
		{"318 ek4", "318 個"}, // 以前會被 #110 的回頭改寫改成「3八個」
		{"3.18 ", "3.18 "},
		{"05 ", "05 "}, // 按鍵順序顛倒（ㄢㄉ）：日期、編號
		{"81 ", "81 "},
		{"hello 15 ", "hello 15 "},
		{"ji3 2 ", "我 2 "},
		{"10:30 ", "10:30 "},
		{"ji310 ", "我班"}, // 前一個字是中文：照舊是一聲
	} {
		s := deferredNumberSession()
		mixedType(s, tc.keys)
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q 提交 %q，要 %q", tc.keys, got, tc.want)
		}
	}
}

// #89、#114、#116：句首打 ㄉㄤ（2;）、ㄉㄡ（2.）接空白要成「當」「都」。; 與結尾的 / 不會出現在數字裡，結尾的 .
// 組得成字時也當中文；組不成字的條列（1. 10.）、列舉（1,）與小數仍是數字（見 TestMixedDeferredNumberStaysNumber）。
func TestMixedPunctuatedDigitsAreNotNumbers(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"2; ", "當"},
		{"1; ", "幫"},
		{"5; ", "張"},
		{"2/ ", "燈"},
		{"hello 2; ", "hello 當"},
		{"15 2; ", "15 當"},
		{"20 2; ", "單當"},
		{"2; 2; ", "當當"},
		{"2. ", "都"},       // #116：結尾的 . 組得成字時是中文，「2. 」條列按 ↓ 選原按鍵
		{"5. ", "周"},       // #114
		{"1/ ", "1/ "},     // ㄅㄥ 不在測試字典：組不成字時照舊是數字
		{"ji3,2. ", "我,都"}, // #116：符號後面接著打都
		{",2; ", ",當"},
		{",20 ", ",20 "}, // 標點後面的數字照舊
		{"ji3,3.5 ", "我,3.5 "},
		{"2;", "2;"},   // 還沒按空白：照字面顯示與提交
		{";  ", ";  "}, // 沒有數字的標點照舊
	} {
		s := deferredNumberSession()
		mixedType(s, tc.keys)
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q 提交 %q，要 %q", tc.keys, got, tc.want)
		}
	}
}

func TestNumberLike(t *testing.T) {
	for raw, want := range map[string]bool{
		"20": true, "3.5": true, "1,000": true, "10:30": true, "5/8": true, "2-3": true, "2.": true, "1,": true,
		"2;": false, "1;": false, "5/": false, "2-": false, "": false, ";": false, ".5": false, "2..3": false, "1;2": false,
	} {
		if got := numberLike(raw); got != want {
			t.Errorf("numberLike(%q) = %v，要 %v", raw, got, want)
		}
	}
}

// 改成中文後保留原始按鍵：↓ 仍可選回數字原樣。
func TestMixedDeferredNumberKeepsPhysicalKeys(t *testing.T) {
	s := deferredNumberSession()
	if snap := mixedType(s, "10 g;4"); snap.Text() != "班上" {
		t.Fatalf("改成中文：%q", snap.Text())
	}
	s.Handle(proto.Request{Type: proto.Cursor, To: 0})
	s.Handle(proto.Request{Type: proto.Candidates, Length: 1})
	if snap := pickText(t, s, "10 "); snap.Text() != "10 上" {
		t.Fatalf("選回數字：%q", snap.Text())
	}
	if got := key(s, proto.KeyEnter).Commit; got != "10 上" {
		t.Fatalf("提交：%q", got)
	}
	if len(s.mixedKeys) != 0 {
		t.Fatal("提交後仍保留按鍵紀錄")
	}
}

// 句首或空白、英文、數字之後，數字鍵能組成注音字時按空白出字；單鍵與兩位數遵守相同規則。
// 要保留數字須按 Enter；後接量詞、英文或數字不會將已組出的中文字改回原按鍵。
// 單獨組不成字的鍵、小數與時間仍按數字處理；reading_hint 提示空白將組成的讀音。
func TestMixedSingleKeyWord(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"5 ", "之"},
		{"8 ", "啊"},
		{"9 ", "哀"},
		{"0 ", "安"},
		{"- ", "- "}, // 標點鍵不算：條列的「- 」維持原樣
		{"- ji3", "- 我"},
		{". ", ". "},
		{"1 ", "1 "}, // ㄅ 單獨組不成字
		{"2 ", "2 "},
		{"5 ek4", "之個"},  // 量詞也不改回數字（#162）：要數字按 Enter
		{"8 2u03", "啊點"}, // ㄉㄧㄢˇ
		{"0 su06", "安年"},
		{"5 jo4", "之位"},
		{"5 c.4", "之後"},  // 組成詞：照舊是中文
		{"8 ji3", "啊我"},  // 一般的字：照常
		{"20 ", "單"},     // #162：兩位數拼得成字也出字
		{"20 ek4", "單個"}, // 要「20 個」按 Enter
		{"2024 su06", "2024 年"},
		{"20 c.4", "單後"},
		{"hello 5 ", "hello 之"},
		{"hello 5 ek4", "hello 之個"},
		{"20 5 ", "單之"},
		{"15 5 ", "15 之"},
		{"ji35 ", "我之"}, // 前面是中文：本來就是注音
		{"ji35 ek4", "我之個"},
		{"5 hello ", "之hello "}, // 跟 20 hello 一樣：要數字按 Enter
		{"- hello ", "- hello "},
		{"hello - hello ", "hello - hello "},
		{"5 20 ", "之單"},   // #162：拼得成字的照常出字
		{"5 15 ", "之15 "}, // 後面的 15 拼不成字照舊是數字，前面的 5 仍是之
		{"1 5 12 ", "1 之12 "},
		{"1 5 10 20 ", "1 之班單"},
		{"5 2; ", "之當"},
		{"5 5 ", "之之"},
		{"8 8 ", "啊啊"}, // 以前會當成數字列舉「8 8 」
		{"1 5 8 ", "1 之啊"},
		{"5 c.4 ", "之後 "},
	} {
		s := deferredNumberSession()
		snap := mixedType(s, tc.keys)
		if snap.Text() != tc.want {
			t.Errorf("%q 顯示 %q，要 %q", tc.keys, snap.Text(), tc.want)
		}
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q 提交 %q，要 %q", tc.keys, got, tc.want)
		}
	}
}

// #134：出字後 ↓ 的第二個候選是原按鍵（第一個是目前的字），選了變回數字。
// #110 改成中文的其他情況，原按鍵仍排在後面。
func TestMixedSingleKeyWordKeepsPhysicalKeys(t *testing.T) {
	s := deferredNumberSession()
	mixedType(s, "5 ")
	s.Handle(proto.Request{Type: proto.Cursor, To: 0})
	snap := s.Handle(proto.Request{Type: proto.Candidates, Length: 1})
	if items := snap.Candidates.Items; len(items) < 4 || items[0].Text != "之" || items[1].Text != "5 " || items[2].Text != "知" {
		t.Fatalf("候選：%+v", items)
	}
	if snap := s.Handle(proto.Request{Type: proto.Select, Index: 1}); snap.Text() != "5 " {
		t.Fatalf("選回數字：%q", snap.Text())
	}
	if got := key(s, proto.KeyEnter).Commit; got != "5 " {
		t.Fatalf("提交：%q", got)
	}
	s = deferredNumberSession()
	mixedType(s, "10 g;4") // #110 的班上：原按鍵仍在最後（原樣注音之前）
	s.Handle(proto.Request{Type: proto.Cursor, To: 0})
	snap = s.Handle(proto.Request{Type: proto.Candidates, Length: 1})
	if items := snap.Candidates.Items; len(items) < 2 || items[len(items)-2].Text != "10 " {
		t.Fatalf("#110 原按鍵位置改變：%+v", items)
	}
}

// #162：單鍵字後面接量詞不再改回數字。要「5 個」先按 Enter 送出 5，或不空格打 5ek4（數字照字面、量詞以數字當語境）。
func TestMixedSingleKeyWordNumberNeedsEnter(t *testing.T) {
	s := deferredNumberSession()
	mixedType(s, "5")
	if got := key(s, proto.KeyEnter).Commit; got != "5" {
		t.Fatalf("5 Enter 提交 %q", got)
	}
	s = deferredNumberSession()
	if snap := mixedType(s, "5ek4"); snap.Text() != "5個" {
		t.Fatalf("5ek4 顯示 %q", snap.Text())
	}
	s = deferredNumberSession()
	mixedType(s, "5 ")
	s.Handle(proto.Request{Type: proto.Cursor, To: 0})
	if snap := mixedType(s, "ek4"); snap.Text() != "個之" {
		t.Fatalf("游標移開：%q", snap.Text())
	}
}

// #162 規則一致後，單鍵字後面接英文或拼不成字的數字也照樣是中文；要還原只能回到那個字上按 ↓ 選原按鍵（第二個），
// 選了要還原成原按鍵、後面的英文和數字不動。後面那段拼不成字的數字照舊沒有讀音提示。
func TestMixedSingleKeyWordRestoreAfterFollowing(t *testing.T) {
	for _, tc := range []struct{ keys, shown, restored string }{
		{"5 hello ", "之hello ", "5 hello "},
		{"5 15 ", "之15 ", "5 15 "},
	} {
		s := deferredNumberSession()
		if snap := mixedType(s, tc.keys); snap.Text() != tc.shown {
			t.Fatalf("%q 顯示 %q，要 %q", tc.keys, snap.Text(), tc.shown)
		}
		s.Handle(proto.Request{Type: proto.Cursor, To: 0})
		snap := s.Handle(proto.Request{Type: proto.Candidates, Length: 1})
		if snap.Candidates == nil || len(snap.Candidates.Items) < 2 || snap.Candidates.Items[1].Text != "5 " {
			t.Fatalf("%q 回到之上的候選：%+v", tc.keys, snap.Candidates)
		}
		if snap := s.Handle(proto.Request{Type: proto.Select, Index: 1}); snap.Text() != tc.restored {
			t.Fatalf("%q 選回原按鍵：%q，要 %q", tc.keys, snap.Text(), tc.restored)
		}
	}
	s := deferredNumberSession()
	if snap := mixedType(s, "5 15"); snap.ReadingHint != nil {
		t.Fatalf("之後面的 15 拼不成字，不該有讀音提示：%+v", snap.ReadingHint)
	}
}
