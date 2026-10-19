package behavior

import (
	"fmt"
	"testing"

	"git.biggo.com/0c/ime/core/personal"
	"git.biggo.com/0c/ime/core/proto"
)

type check struct {
	name   string
	ok     bool
	detail string
}

// qSuite 是 Q1–Q6、拉鋸、有界與無外溢。回傳每一項的結果，供參數掃描重用。
func qSuite(tb testing.TB, p personal.Params) []check {
	var res []check
	add := func(name string, ok bool, detail ...any) {
		res = append(res, check{name, ok, fmt.Sprint(detail...)})
	}

	// Q1 通行讀音可達：系統詞庫收了「親子丼」時不需任何學習
	u := newUser(tb, p,
		extra{"親子丼", QZ + " " + D, 1.0, 2e-6},
		extra{"親子丼", QZJ, 0.1, 2e-6})
	add("Q1 通行讀音 ㄉㄨㄥˋ 直接正確", u.show(QZ+" "+D) == "親子丼")
	add("Q1 以辭典音 ㄐㄧㄥˇ 輸入亦正確", u.show(QZJ) == "親子丼")
	add("Q1 精確輸入不退步", u.show(D) == "動" && u.show(J) == "景" && u.show("ㄈㄥ "+J) == "風景")

	// Q1' 詞庫沒有該詞時，通行讀音仍可達（候選含丼）
	u = newUser(tb, p)
	u.typeAll(QZ + " " + D)
	c := texts(u.singles(2))
	u.do(proto.Request{Type: proto.Cancel})
	add("Q1' 無詞條時 ㄉㄨㄥˋ 候選含丼", contains(c, "丼"), c)

	// 以下一律在「系統詞庫沒有親子丼」的狀態測學習能力（代表任何未收錄詞）
	before, n := u.typeAndFix(QZJ, "親子丼")
	add("Q2 學習前為親子景、手動 1 次", before == "親子景" && n == 1, before, n)
	add("Q2 第二次自動正確", u.show(QZJ) == "親子丼")
	add("Q2 句中亦正確", u.show(WXC+" "+QZJ) == "我想吃親子丼")

	add("Q3 單打 ㄐㄧㄥˇ 仍為景", u.show(J) == "景")
	add("Q3 我想吃+ㄐㄧㄥˇ 不變丼", u.show(WXC+" "+J) == "我想吃景")
	add("Q3 風景不受影響", u.show("ㄈㄥ "+J) == "風景")

	u.typeAll(QZJ)
	c = texts(u.singles(2))
	u.do(proto.Request{Type: proto.Cancel})
	add("Q5 親子後的候選首位為丼", first(c) == "丼", c)
	u.typeAll(J)
	c = texts(u.singles(0))
	u.do(proto.Request{Type: proto.Cancel})
	add("Q5 無上下文時丼在前段但不搶首位", first(c) == "景" && index(c, "丼") < 5, c)

	u.typeAndFix(J, "景")
	u.typeAndFix("ㄎㄢˋ "+J, "看警")
	add("Q4 選過景／警後親子丼仍正確", u.show(QZJ) == "親子丼")
	add("Q4 看+ㄐㄧㄥˇ 記得警", u.show("ㄎㄢˋ "+J) == "看警")
	right := 0
	for i := 0; i < 60; i++ {
		if b, _ := u.typeAndFix(QZJ, "親子丼"); b == "親子丼" {
			right++
		}
		u.typeAndFix(J, "景")
	}
	add("Q4 60 輪交錯：親子丼自動轉對", right == 60, right, "/60")
	add("Q4 60 輪後單打 ㄐㄧㄥˇ 仍為景", u.show(J) == "景")
	_, promoted := u.e.Model.Store.Phrases["親子丼"]
	add("片語：親子丼已晉升為個人詞", promoted)

	u.do(proto.Request{Type: proto.Forget, Word: "丼"})
	add("Q6 撤銷後回到學習前", u.show(QZJ) == "親子景")
	for i := 0; i < 5; i++ { // 被動接受錯誤首選 5 次
		u.typeAll(QZJ)
		u.do(proto.Request{Type: proto.Commit})
	}
	_, promoted = u.e.Model.Store.Phrases["親子丼"]
	add("Q6 被動使用不會學回", u.show(QZJ) == "親子景" && !promoted)
	u.typeAndFix(QZJ, "親子丼")
	add("Q6 明確手選一次即壓過 5 次被動累積", u.show(QZJ) == "親子丼")

	u = newUser(tb, p)
	for i := 0; i < 500; i++ {
		u.typeAndFix(QZJ, "親子丼")
	}
	pr := u.e.Model.Prob("親子", "丼")
	add("有界：連打 500 次後分數仍 ≤ 1", pr <= 1, pr)
	add("無外溢：連打 500 次後單打 ㄐㄧㄥˇ 仍為景", u.show(J) == "景", "丼的全域計數=", u.e.Model.Store.Uni["丼"])
	return res
}

func contains(s, sub string) bool { return index(s, sub) >= 0 }

func index(s, sub string) int {
	for i, r := range []rune(s) {
		if string(r) == sub {
			return i
		}
	}
	return -1
}

func first(s string) string {
	if r := []rune(s); len(r) > 0 {
		return string(r[0])
	}
	return ""
}

func TestQ(t *testing.T) {
	res := qSuite(t, personal.DefaultParams())
	t.Logf("%d 項檢查", len(res))
	for _, c := range res {
		if !c.ok {
			t.Errorf("%s  %s", c.name, c.detail)
		}
	}
}

// TestParamSweep：Q1–Q6 是結構性成立，還是只在某組參數下成立？
// 固定的參數掃描格點；κ_uni ≥ 3e3 時必須全數通過。
func TestParamSweep(t *testing.T) {
	if testing.Short() {
		t.Skip("參數掃描較慢")
	}
	combos, clean := 0, 0
	for _, kb := range []float64{0.3, 1, 3, 10} {
		for _, ku := range []float64{1000, 3000, 10000, 100000} {
			for _, wp := range []float64{0, 0.1, 0.25, 0.5, 1} {
				p := personal.DefaultParams()
				p.KappaBi, p.KappaUni, p.WPassive = kb, ku, wp
				combos++
				bad := 0
				for _, c := range qSuite(t, p) {
					if !c.ok {
						bad++
						if ku >= 3000 {
							t.Errorf("κ_bi=%g κ_uni=%g 被動=%g：%s %s", kb, ku, wp, c.name, c.detail)
						}
					}
				}
				if bad == 0 {
					clean++
				}
			}
		}
	}
	t.Logf("%d/%d 組參數全數通過", clean, combos)
}

// 以參考實作再跑一次 Q 套件：兩種資料格式的行為必須相同。
func TestReferenceImplementation(t *testing.T) {
	useReference = true
	defer func() { useReference = false }()
	for _, c := range qSuite(t, personal.DefaultParams()) {
		if !c.ok {
			t.Errorf("參考實作：%s  %s", c.name, c.detail)
		}
	}
}
