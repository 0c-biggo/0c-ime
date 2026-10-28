package session

import (
	"slices"
	"testing"

	"github.com/0c-biggo/0c-ime/core/proto"
)

// #232：中英混打打閉括號時，往左找組字區內最近、還沒配對的開括號，跟它的全／半形；巢狀要對。
// 以前閉括號的全／半形只看緊接在前面的是不是照字面的英數，打出「我（DNS)」「DNS(我）」「我「DNS]」。
// 「」『』（[ ] { } 打出的引號）也是成對的。
func TestMixedClosingBracketFollowsOpening(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"ji3(DNS)", "我（DNS）"},
		{"DNS(ji3)", "DNS(我)"},
		{"ji3[DNS]", "我「DNS」"},
		{"DNS[ji3]", "DNS[我]"},
		{"ji3{DNS}", "我『DNS』"},
		{"DNS{ji3}", "DNS{我}"},
		{"(DNS)", "（DNS）"},           // 句首的開括號是全形
		{"ji3(DNS)ji3", "我（DNS）我"},   // 之後接著打中文
		{"ji3(DNS) ji3", "我（DNS） 我"}, // 閉括號後接空白
		{"ji3(SEE FOO(X))", "我（SEE FOO(X)）"},
		{"FOO(ji3(DNS))", "FOO(我（DNS）)"},
		{"ji3[DNS(X)]", "我「DNS(X)」"}, // 不同種類的括號各自配對
		{"ji3(DNS[ji3)", "我（DNS[我）"}, // ( 找不到 [ 的配對，跳過不算
		{"ji3([DNS])", "我（「DNS」）"},   // 兩層都是全形
		{"DNS(ji3)ji3)", "DNS(我)我）"}, // 第二個 ) 沒有開括號可配：照舊
		{"ji3(ji3)", "我（我）"},         // 原本就一致的不變
		{"DNS(DNS)", "DNS(DNS)"},
		{"DNS)", "DNS)"}, // 組字區裡沒有開括號（已送出或沒打）：照舊
		{"ji3)", "我）"},
		{"ji3(DNS:)", "我（DNS:）"}, // 英文後面的 : 是網址、路徑的一部分，不影響配對
	} {
		s := mixedSession()
		mixedType(s, tc.keys)
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q 提交 %q，要 %q", tc.keys, got, tc.want)
		}
	}
}

// 開括號已經送出、看不到時照舊：緊接在照字面英數後面的是半形，其他是全形。
func TestMixedClosingBracketWithoutOpeningUnchanged(t *testing.T) {
	s := mixedSession()
	mixedType(s, "ji3(")
	key(s, proto.KeyEnter)
	if got := mixedType(s, "DNS)"); got.Text() != "DNS)" {
		t.Fatalf("開括號已送出：%q", got.Text())
	}
}

// 沒開混打時標點一律全形（#177），不配對。
func TestClosingBracketWithoutMixedUnchanged(t *testing.T) {
	s := newSession()
	typeSyl(s, "ㄨㄛˇ")
	for _, k := range []string{"(", "D", "N", "S", ")"} {
		key(s, k)
	}
	if got := key(s, proto.KeyEnter).Commit; got != "我（DNS）" {
		t.Fatalf("沒開混打：%q", got)
	}
}

// Ctrl＋Shift＋0（#231）是明確要全形，不跟著開括號（#168）。
func TestMixedCtrlParenIsAlwaysFullWidth(t *testing.T) {
	s := mixedSession()
	s.CtrlPunct = true
	mixedType(s, "DNS(ji3")
	key(s, proto.KeyCtrlParenRight)
	if got := key(s, proto.KeyEnter).Commit; got != "DNS(我）" {
		t.Fatalf("Ctrl+)：%q", got)
	}
	s = mixedSession()
	s.CtrlPunct = true
	mixedType(s, "DNS")
	key(s, proto.KeyCtrlParenLeft)
	mixedType(s, "ji3)")
	if got := key(s, proto.KeyEnter).Commit; got != "DNS（我）" {
		t.Fatalf("Ctrl+( 之後的 ) 跟著全形：%q", got)
	}
}

// 配對出的閉括號按 ↓，第二項是另一種寬度，選了換得回來（半形 ) 本身不在任何符號組裡，比照 #190 記在原按鍵）。
func TestMixedPairedClosingBracketDownOffersOtherWidth(t *testing.T) {
	for _, tc := range []struct{ keys, before, paired, other string }{
		{"DNS(ji3)", "DNS(我", ")", "）"},
		{"ji3(DNS)", "我（DNS", "）", ")"},
		{"DNS[ji3]", "DNS[我", "]", "」"},
		{"ji3[DNS]", "我「DNS", "」", "]"},
	} {
		s := mixedSession()
		if snap := mixedType(s, tc.keys); snap.Text() != tc.before+tc.paired {
			t.Fatalf("%q 顯示 %q", tc.keys, snap.Text())
		}
		items := downItems(key(s, proto.KeyDown))
		if len(items) < 2 || items[0] != tc.paired || items[1] != tc.other {
			t.Fatalf("%q 按 ↓：%q", tc.keys, items)
		}
		if tc.paired != ")" && tc.paired != "]" && !slices.Contains(items, symbolAlternatives(tc.paired)[1]) {
			t.Fatalf("%q 按 ↓ 其餘照舊：%q", tc.keys, items)
		}
		if snap := s.Handle(proto.Request{Type: proto.Select, Index: 1}); snap.Text() != tc.before+tc.other {
			t.Fatalf("%q 選另一種寬度：%q", tc.keys, snap.Text())
		}
		if got := key(s, proto.KeyEnter).Commit; got != tc.before+tc.other {
			t.Fatalf("%q 提交：%q", tc.keys, got)
		}
	}
}
