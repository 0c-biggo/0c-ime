package session

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/proto"
)

// #365：數字旁也補時，緊跟在半形數字後面的半形 % 算數字的一部分，它跟後面的漢字之間也補。
// 數字前面有沒有漢字照一般規則；前面不是數字的 %、全形 ％、全形數字照舊不動。只補英文時不管 %。
func TestSpaceCJKPercentBelongsToTheNumber(t *testing.T) {
	for _, tc := range []struct{ in, on, all string }{
		{"有50%的人", "有50%的人", "有 50% 的人"},
		{"50%的人", "50%的人", "50% 的人"}, // 開頭沒有漢字，前面不補
		{"成長50%以上", "成長50%以上", "成長 50% 以上"},
		{"漲了3.5%左右", "漲了3.5%左右", "漲了 3.5% 左右"},
		{"100%棉", "100%棉", "100% 棉"},
		{"約50%~60%的人", "約50%~60%的人", "約 50%~60% 的人"},
		{"的50%", "的50%", "的 50%"},
		// 已有空白、標點旁不補
		{"有 50% 的人", "有 50% 的人", "有 50% 的人"},
		{"50%，的", "50%，的", "50%，的"},
		{"（50%）的", "（50%）的", "（50%）的"},
		// % 前面不是半形數字：不算數字的一部分
		{"折扣%的", "折扣%的", "折扣%的"},
		{"%的", "%的", "%的"},
		{"50%%的", "50%%的", "50%%的"},
		{"50 %的", "50 %的", "50 %的"},
		{"a%的", "a%的", "a%的"},
		// 全形 ％、全形數字、千分比不處理
		{"有50％的人", "有50％的人", "有 50％的人"},
		{"有５０%的人", "有５０%的人", "有５０%的人"},
		{"有5‰的人", "有5‰的人", "有 5‰的人"},
		// % 後面接英文、數字：英文與數字之間本來就不補
		{"50%off的", "50%off 的", "50%off 的"},
		{"用50%Python", "用50%Python", "用 50%Python"},
		// 小數、千分位、負號、貨幣符號照相鄰兩個字元的規則（0c-ime#91）
		{"共1,000元", "共1,000元", "共 1,000 元"},
		{"零下-5度", "零下-5度", "零下-5 度"},
		{"花了$100買", "花了$100買", "花了$100 買"},
	} {
		if got := spaceCJK(tc.in, true, false); got != tc.on {
			t.Errorf("%q 只補英文：%q，要 %q", tc.in, got, tc.on)
		}
		if got := spaceCJK(tc.in, true, true); got != tc.all {
			t.Errorf("%q 英文與數字：%q，要 %q", tc.in, got, tc.all)
		}
		if got := spaceCJK(tc.in, false, false); got != tc.in {
			t.Errorf("%q 都關：%q", tc.in, got)
		}
		if again := spaceCJK(tc.all, true, true); again != tc.all {
			t.Errorf("%q 補過再補一次不能變：%q", tc.all, again)
		}
	}
}

// 組字中切到英文打的「50%」跟前後的字一起送出：「有50%的」送出「有 50% 的」；只補英文時照原樣。
func TestCJKSpacePercentInsideComposition(t *testing.T) {
	for _, tc := range []struct {
		latin, digits bool
		want          string
	}{{false, false, "我50%想"}, {true, false, "我50%想"}, {true, true, "我 50% 想"}} {
		s := newSession()
		s.CJKLatinSpace, s.CJKDigitSpace = tc.latin, tc.digits
		typeSyl(s, "ㄨㄛˇ")
		s.Handle(toEnglish)
		mixedType(s, "50%")
		s.Handle(toEnglish)
		typeSyl(s, "ㄒㄧㄤˇ")
		if snap := key(s, proto.KeyEnter); snap.Commit != tc.want {
			t.Errorf("latin=%v digits=%v：送出 %q，要 %q", tc.latin, tc.digits, snap.Commit, tc.want)
		}
	}
}
