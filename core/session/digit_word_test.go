package session

import (
	"slices"
	"testing"

	"git.biggo.com/0c/ime/core/proto"
)

// #162：中英混打時只用數字鍵就拼得成的一聲字（20 ㄉㄢ、18 ㄅㄚ、10 ㄅㄢ、5 ㄓ）按空白就出中文，不論在句首，
// 或接在中文、空白、英文、數字後面（#89、#119、#158 回報的「單」「之」「八代」）。拼不成音節的照舊是數字。
func TestMixedDigitWordAnywhere(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"20 ", "單"},
		{"18 ", "八"},
		{"10 ", "班"},
		{"5 ", "之"},
		{"ji320 ", "我單"},   // 接在中文後面（本來就是中文）
		{"ji3 20 ", "我 單"}, // 接在空白後面
		{"hello 20 ", "hello 單"},
		{"15 20 ", "15 單"}, // 接在數字後面
		{"20 18 ", "單八"},
		{"6 18 ", "6 八"}, // 前面是 #153 的單一聲調鍵
		{"15 ", "15 "},   // ㄅㄓ 拼不成音節
		{"12 ", "12 "},
		{"100 ", "100 "},
		{"2024 ", "2024 "},
		{"318 ", "318 "}, // 18 緊接在字面的 3 後面，是數字的一部分
		{"3.18 ", "3.18 "},
		{"05 ", "05 "}, // 順序顛倒（ㄢㄉ）不算
		{"2 ", "2 "},   // ㄉ 自己沒有字
		{"6 ", "6 "},   // #153：單一聲調鍵照舊是數字
	} {
		s := deferredNumberSession()
		if snap := mixedType(s, tc.keys); snap.Text() != tc.want {
			t.Errorf("%q 顯示 %q，要 %q", tc.keys, snap.Text(), tc.want)
		}
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q 提交 %q，要 %q", tc.keys, got, tc.want)
		}
	}
}

// 要數字按 Enter：還沒按空白的數字照字面送出，組字區裡前面的字一起送出。
func TestMixedDigitWordEnterSendsNumber(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"20", "20"},
		{"18", "18"},
		{"ji3u.3 20", "我有 20"},
		{"hello 20", "hello 20"},
	} {
		s := deferredNumberSession()
		if snap := mixedType(s, tc.keys); snap.Text() != tc.want {
			t.Errorf("%q 顯示 %q，要 %q", tc.keys, snap.Text(), tc.want)
		}
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q 提交 %q，要 %q", tc.keys, got, tc.want)
		}
	}
}

// 出字後按 ↓，第一個是目前的字，第二個是原按鍵（數字連同空白），選了變回數字；不論出字的位置，
// 也不論是不是接著打了別的字（游標移回那個字上）。
func TestMixedDigitWordDownOffersNumberSecond(t *testing.T) {
	for _, tc := range []struct{ keys, first, number, after string }{
		{"18 ", "八", "18 ", "18 "},
		{"20 ", "單", "20 ", "20 "},
		{"ji310 ", "班", "10 ", "我10 "},
		{"hello 20 ", "單", "20 ", "hello 20 "},
	} {
		s := deferredNumberSession()
		mixedType(s, tc.keys)
		items := downItems(key(s, proto.KeyDown))
		if len(items) < 3 || items[0] != tc.first || items[1] != tc.number {
			t.Fatalf("%q 按 ↓：%q", tc.keys, items)
		}
		if snap := s.Handle(proto.Request{Type: proto.Select, Index: 1}); snap.Text() != tc.after {
			t.Fatalf("%q 選回數字：%q", tc.keys, snap.Text())
		}
		if got := key(s, proto.KeyEnter).Commit; got != tc.after {
			t.Fatalf("%q 提交 %q", tc.keys, got)
		}
	}

	s := deferredNumberSession()
	if snap := mixedType(s, "10 g;4"); snap.Text() != "班上" {
		t.Fatalf("接著打的字一起解碼：%q", snap.Text())
	}
	s.Handle(proto.Request{Type: proto.Cursor, To: 0})
	snap := s.Handle(proto.Request{Type: proto.Candidates, Length: 1})
	if items := snap.Candidates.Items; len(items) < 2 || items[1].Text != "10 " {
		t.Fatalf("班上的班按 ↓：%+v", items)
	}
	if snap := s.Handle(proto.Request{Type: proto.Select, Index: 1}); snap.Text() != "10 上" {
		t.Fatalf("選回數字：%q", snap.Text())
	}
}

// 出的字是一般的中文音節：不學成注音，↓ 的同音字照常；暫定數字（#155）的紀錄不再產生，
// 數字上按 ↓ 只有一般的符號候選（#153 的聲調鍵照舊第二個給聲調符號）。
func TestMixedDigitWordLeavesNumbersAlone(t *testing.T) {
	s := deferredNumberSession()
	mixedType(s, "20 ")
	key(s, proto.KeyEnter)
	assertNoZhuyinLearned(t, s, "ㄉㄢ")

	for _, keys := range []string{"15 ", "318 ", "2024 ", "10:30 ", "1. "} {
		s := deferredNumberSession()
		mixedType(s, keys)
		if items := downItems(key(s, proto.KeyDown)); slices.ContainsFunc(items, hasZhuyin) {
			t.Fatalf("%q 按 ↓ 不給注音：%q", keys, items)
		}
	}

	s = deferredNumberSession()
	mixedType(s, "6 18 ")
	s.Handle(proto.Request{Type: proto.Cursor, To: 0})
	if items := downItems(key(s, proto.KeyDown)); !slices.Equal(items, []string{"6", "ˊ"}) {
		t.Fatalf("#153 的聲調鍵按 ↓：%q", items)
	}
}

func hasZhuyin(text string) bool {
	for _, r := range text {
		if r >= 'ㄅ' && r <= 'ㄩ' {
			return true
		}
	}
	return false
}
