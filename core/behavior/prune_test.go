package behavior

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
)

// 清理（#129）之後，同一份個人資料打出來的字與沒清理時相同；之後照常被動使用，
// 兩邊仍然一致，刪除過的詞不會被學回，明確的選擇紀錄都還在。
func TestPrunedLearningBehavesLikeUnpruned(t *testing.T) {
	p := personal.DefaultParams()
	learn := func(u *user) {
		u.typeAndFix(QZJ, "親子丼")
		u.typeAndFix(QZJ, "親子丼")
		u.typeAndFix("ㄎㄢˋ "+J, "看警")
		for i := 0; i < 5; i++ {
			u.typeAll(WXC)
			u.do(proto.Request{Type: proto.Commit})
			u.typeAll("ㄈㄥ " + J)
			u.do(proto.Request{Type: proto.Commit})
		}
		u.do(proto.Request{Type: proto.Forget, Word: "警"})
	}
	readings := []string{QZJ, J, WXC + " " + QZJ, "ㄈㄥ " + J, "ㄎㄢˋ " + J, WXC, MPDF}

	plain, pruned := newUser(t, p), newUser(t, p)
	plain.e.Model.AdvanceTime(1_800_000_000)
	learn(plain)
	pruned.e.Model.Store = plain.e.Model.Store.Clone()
	for _, u := range []*user{plain, pruned} {
		u.e.Model.AdvanceTime(u.e.Model.Store.Time + 2*365*86400)
	}
	st := pruned.e.Model.Prune()
	if st.Bi == 0 || st.EvidenceByContext == 0 {
		t.Fatalf("兩年後應有可清理的計數與證據，否則這個測試沒有意義：%+v", st)
	}
	for round := 0; round < 3; round++ {
		for _, r := range readings {
			a, b := plain.show(r), pruned.show(r)
			if a != b {
				t.Fatalf("第 %d 輪 %s：沒清理 %q、清理後 %q", round, r, a, b)
			}
		}
		// 被動使用：照顯示結果提交，兩邊都學一樣的東西。
		for _, u := range []*user{plain, pruned} {
			for _, r := range readings {
				u.typeAll(r)
				u.do(proto.Request{Type: proto.Commit})
			}
		}
	}
	if got := pruned.show("ㄎㄢˋ " + J); got == "看警" {
		t.Fatal("刪除過的「警」被被動使用學回")
	}
	s := pruned.e.Model.Store
	if _, ok := s.Phrases["親子丼"]; !ok || !s.Readings["丼\tㄐㄧㄥˇ"] || !s.Tombstones["警"] {
		t.Fatal("明確的選擇（個人片語、親手選的讀音、刪除紀錄）被清掉")
	}
}
