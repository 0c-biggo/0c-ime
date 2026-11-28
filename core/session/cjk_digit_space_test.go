package session

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/proto"
)

// 數字旁也補空格（GitHub 0c-ime#91；設定「自動加空格」選「中英文與數字之間」，#365）：漢字與半形數字直接相連時補一個
// 半形空格。session 的兩個開關各自獨立，設定只提供「關閉／中英文之間／中英文與數字之間」。
func TestSpaceCJKDigitRules(t *testing.T) {
	for _, tc := range []struct {
		in            string
		latin, digits bool
		want          string
	}{
		{"第2版", false, true, "第 2 版"},
		{"2026年11月25日", false, true, "2026 年 11 月 25 日"},
		{"3月", false, true, "3 月"},
		{"共 3 個", false, true, "共 3 個"},
		// 小數點、全形數字、全形標點旁不補；緊跟在數字後面的 % 算數字的一部分（#365，見 TestSpaceCJKPercentBelongsToTheNumber）
		{"3.5倍", false, true, "3.5 倍"},
		{"50%的人", false, true, "50% 的人"},
		{"第２版", false, true, "第２版"},
		{"（3）號", false, true, "（3）號"},
		// 只開數字時英文旁不補；英文與數字之間本來就不補
		{"用Python3寫", false, true, "用Python3 寫"},
		{"用Python3寫", true, false, "用 Python3寫"},
		{"用Python3寫", true, true, "用 Python3 寫"},
		{"iPhone15手機", true, true, "iPhone15 手機"},
		// 兩個都關時不動
		{"第2版", false, false, "第2版"},
		{"2", false, true, "2"}, {"", false, true, ""},
	} {
		if got := spaceCJK(tc.in, tc.latin, tc.digits); got != tc.want {
			t.Errorf("%q latin=%v digits=%v：%q，要 %q", tc.in, tc.latin, tc.digits, got, tc.want)
		}
	}
}

// 組字中切到英文打的數字跟組字一起送出，交界補；只開中英文空格時數字旁照舊不補。
func TestCJKDigitSpaceInsideComposition(t *testing.T) {
	for _, tc := range []struct {
		latin, digits bool
		want          string
	}{{false, false, "我3想"}, {true, false, "我3想"}, {false, true, "我 3 想"}, {true, true, "我 3 想"}} {
		s := newSession()
		s.CJKLatinSpace, s.CJKDigitSpace = tc.latin, tc.digits
		typeSyl(s, "ㄨㄛˇ")
		s.Handle(toEnglish)
		mixedType(s, "3")
		s.Handle(toEnglish)
		typeSyl(s, "ㄒㄧㄤˇ")
		if snap := key(s, proto.KeyEnter); snap.Commit != tc.want {
			t.Errorf("latin=%v digits=%v：送出 %q，要 %q", tc.latin, tc.digits, snap.Commit, tc.want)
		}
	}
}

// 私人欄位照原樣送出。
func TestCJKDigitSpaceSkipsPrivateFields(t *testing.T) {
	s := newSession()
	s.CJKDigitSpace = true
	typeSyl(s, "ㄨㄛˇ")
	s.Handle(toEnglish)
	s.Handle(proto.Request{Type: proto.Key, Key: "3", Private: true})
	if got := s.Handle(proto.Request{Type: proto.Key, Key: proto.KeyEnter, Private: true}).Commit; got != "我3" {
		t.Fatalf("私人欄位：%q", got)
	}
}
