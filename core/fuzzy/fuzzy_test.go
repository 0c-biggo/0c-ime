package fuzzy

import (
	"slices"
	"testing"

	"git.biggo.com/0c/ime/core/input"
	"git.biggo.com/0c/ime/core/zhuyin"
)

func all() Config {
	var c Config
	for _, p := range Pairs() {
		c = c.With(p, Select)
	}
	return c
}

func alts(c Config, s string) []string {
	var out []string
	for _, a := range c.Alternatives(zhuyin.Syllable(s)) {
		out = append(out, string(a.Syl))
	}
	return out
}

func TestAlternativesPerPair(t *testing.T) {
	for _, tc := range []struct {
		pair Pair
		in   string
		want []string
	}{
		{ZZh, "ㄗ", []string{"ㄓ"}},       // F01：聲調不變（一聲）
		{ZZh, "ㄓˋ", []string{"ㄗˋ"}},     // F04：正反向對等
		{ZZh, "ㄗㄞˋ", []string{"ㄓㄞˋ"}},   // 帶韻母
		{CCh, "ㄘ", []string{"ㄔ"}},       // F05
		{CCh, "ㄔㄞˊ", []string{"ㄘㄞˊ"}},   // F07
		{SSh, "ㄙㄥ", []string{"ㄕㄥ"}},     // F09
		{SSh, "ㄙㄢ", []string{"ㄕㄢ"}},     // F11 反向
		{SSh, "ㄕㄢ", []string{"ㄙㄢ"}},     // F11
		{EnEng, "ㄆㄣˊ", []string{"ㄆㄥˊ"}}, // F14
		{EnEng, "ㄧㄥ", []string{"ㄧㄣ"}},   // F15
		{EnEng, "ㄅㄥˇ", []string{"ㄅㄣˇ"}}, // F16
		{EnEng, "ㄩㄣˊ", []string{"ㄩㄥˊ"}}, // F24：依現行音節表
		{EnEng, "ㄧㄣ", []string{"ㄧㄥ"}},   // F24
		{EnEng, "ㄕㄨㄣ", nil},             // F23：ㄕㄨㄥ 不在音節表內，不產生非法候選
		{SSh, "ㄕㄨㄥˋ", []string{"ㄙㄨㄥˋ"}}, // F12：來源 ㄕㄨㄥˋ 不在音節表，但結果合法
		{EnEng, "ㄕㄨㄥˋ", []string{"ㄕㄨㄣˋ"}},
		{EnEng, "ㄙㄨㄣ", []string{"ㄙㄨㄥ"}}, // ㄙㄨㄥ 合法
		{ZZh, "ㄅㄚ", nil},                // 無關音節
		{ZZh, "ㄗㄠˇ˙", nil},              // 非法結構不展開
	} {
		if got := alts(Config{}.With(tc.pair, Select), tc.in); !slices.Equal(got, tc.want) {
			t.Errorf("%s 只開 %s：%s → %v，應為 %v", tc.in, tc.pair.ID(), tc.in, got, tc.want)
		}
	}
}

// F19、F17、F18：同音節聲母＋韻母可同時換，成本為兩次。
func TestAlternativesBothSides(t *testing.T) {
	c := all()
	got := c.Alternatives("ㄙㄣ")
	if syls := alts(c, "ㄙㄣ"); !slices.Equal(syls, []string{"ㄕㄣ", "ㄙㄥ", "ㄕㄥ"}) {
		t.Fatalf("ㄙㄣ 的替代應依聲母、韻母、兩者排列：%v", syls)
	}
	last := got[len(got)-1]
	if last.Syl != "ㄕㄥ" || len(last.Rules) != 2 || last.Rules[0] != SSh || last.Rules[1] != EnEng {
		t.Fatalf("ㄙㄣ → ㄕㄥ 應用到 s-sh 與 en-eng：%+v", last)
	}
	if last.Cost() != 2*SwapCost || got[0].Cost() != SwapCost {
		t.Fatalf("成本：%v、%v", got[0].Cost(), last.Cost())
	}
}

// F26：只開一組，其他組不擴充。
func TestOnlyEnabledPairsExpand(t *testing.T) {
	c := Config{}.With(ZZh, Select)
	for _, s := range []string{"ㄘ", "ㄙㄢ", "ㄆㄣˊ", "ㄧㄥ"} {
		if got := alts(c, s); got != nil {
			t.Errorf("只開 z-zh，%s 不應有替代：%v", s, got)
		}
	}
	if !c.Enabled() || (Config{}).Enabled() {
		t.Fatal("Enabled 判斷錯誤")
	}
	if c.Key() != "z-zh=select" || (Config{}).Key() != "" {
		t.Fatalf("Key：%q", c.Key())
	}
}

// F25：全部合法音節 × 五聲調——替代必須合法、去重、聲調保留、不含原音，且全部關閉時沒有任何替代。
func TestAlternativesAcrossInventory(t *testing.T) {
	c := all()
	bases := zhuyin.Inventory()
	if len(bases) < 400 {
		t.Fatalf("音節表只取得 %d 個基底", len(bases))
	}
	n := 0
	for _, base := range bases {
		for tone := 1; tone <= 5; tone++ {
			s := base.WithTone(tone)
			if got := (Config{}).Alternatives(s); got != nil {
				t.Fatalf("全部關閉時 %s 不應有替代", s)
			}
			seen := map[zhuyin.Syllable]bool{}
			for _, a := range c.Alternatives(s) {
				n++
				switch {
				case a.Syl == s:
					t.Errorf("%s 的替代不得含原音", s)
				case !a.Syl.Legal():
					t.Errorf("%s → %s 不是合法音節", s, a.Syl)
				case a.Syl.ToneOf() != s.ToneOf():
					t.Errorf("%s → %s 聲調改變", s, a.Syl)
				case seen[a.Syl]:
					t.Errorf("%s → %s 重複", s, a.Syl)
				case len(a.Rules) < 1 || len(a.Rules) > 2:
					t.Errorf("%s → %s 規則數 %d", s, a.Syl, len(a.Rules))
				}
				seen[a.Syl] = true
			}
		}
	}
	if n == 0 {
		t.Fatal("音節表沒有產生任何替代")
	}
}

// 來源音節只要求結構合法：窮舉所有結構合法的音節（含不在音節表者），結果仍一律合法、聲調不變、不含原音。
func TestAlternativesFromAnyValidSyllable(t *testing.T) {
	c := all()
	split := func(s string) []string {
		out := []string{""}
		for _, r := range s {
			out = append(out, string(r))
		}
		return out
	}
	var total, illegalSources int
	for _, i := range split("ㄅㄆㄇㄈㄉㄊㄋㄌㄍㄎㄏㄐㄑㄒㄓㄔㄕㄖㄗㄘㄙ") {
		for _, m := range split("ㄧㄨㄩ") {
			for _, f := range split("ㄚㄛㄜㄝㄞㄟㄠㄡㄢㄣㄤㄥㄦ") {
				for tone := 1; tone <= 5; tone++ {
					base := zhuyin.Syllable(i + m + f)
					if !base.Valid() {
						continue
					}
					s := base.WithTone(tone)
					if !s.Legal() {
						illegalSources++
					}
					for _, a := range c.Alternatives(s) {
						total++
						if a.Syl == s || !a.Syl.Legal() || a.Syl.ToneOf() != s.ToneOf() {
							t.Fatalf("%s → %s 不合規", s, a.Syl)
						}
					}
				}
			}
		}
	}
	if total == 0 || illegalSources == 0 {
		t.Fatalf("窮舉沒有涵蓋預期情形：替代 %d、不在音節表的來源 %d", total, illegalSources)
	}
}

// 每個音節最多四支（原音＋聲母＋韻母＋兩者）。以下固定現行音節表的分支分布：
// 419 個基底；只換一個符號時 254／144／21 個基底有 1／2／3 支；允許同時換兩邊時 20 個基底有 4 支。
func TestBranchDistributionMatchesInventory(t *testing.T) {
	c := all()
	single, both := map[int]int{}, map[int]int{}
	for _, b := range zhuyin.Inventory() {
		one, full := 1, 1
		for _, a := range c.Alternatives(b) {
			full++
			if len(a.Rules) == 1 {
				one++
			}
		}
		single[one]++
		both[full]++
	}
	if n := len(zhuyin.Inventory()); n != 419 {
		t.Fatalf("音節表基底數 %d，預期為 419", n)
	}
	if single[1] != 254 || single[2] != 144 || single[3] != 21 {
		t.Errorf("單符號分支分布 %v", single)
	}
	if both[4] != 20 || both[1] != 254 {
		t.Errorf("含兩邊同換的分支分布 %v", both)
	}
	for n := range both {
		if n > 4 {
			t.Errorf("出現 %d 個分支，超過上限 4", n)
		}
	}
}

func TestWidenKeepsOriginalFirstAndExistingHypotheses(t *testing.T) {
	syls := zhuyin.ParseReading("ㄗ ㄕˋ ㄅㄚ")
	exact := input.Exact{}.Expand(syls)
	lat := Config{}.With(ZZh, Select).With(SSh, Select).Widen(exact, syls, 0, 2)
	if lat[0][0] != (input.Hyp{Syl: "ㄗ"}) || lat[1][0] != (input.Hyp{Syl: "ㄕˋ"}) {
		t.Fatalf("原音必須是第一個假設且成本 0：%+v", lat)
	}
	if len(lat[0]) != 2 || lat[0][1].Syl != "ㄓ" || lat[0][1].Cost != SwapCost {
		t.Fatalf("ㄗ 的假設：%+v", lat[0])
	}
	if len(lat[2]) != 1 {
		t.Fatalf("範圍外的位置不展開：%+v", lat[2])
	}
	if len(exact[0]) != 1 {
		t.Fatal("Widen 不得修改傳入的音節格")
	}
	// 已有的近音讀音（先前鎖定留下）不重複加入。
	exact[0] = append(exact[0], input.Hyp{Syl: "ㄓ", Cost: 9})
	again := Config{}.With(ZZh, Select).Widen(exact, syls, 0, 1)
	if len(again[0]) != 2 || again[0][1].Cost != 9 {
		t.Fatalf("既有假設應保留：%+v", again[0])
	}
}

func TestFrontendIsAnInputFrontend(t *testing.T) {
	var f input.Frontend = Frontend{all()}
	lat := f.Expand(zhuyin.ParseReading("ㄧㄣ ㄍㄞ"))
	if len(lat) != 2 || len(lat[0]) != 2 || lat[0][1].Syl != "ㄧㄥ" || len(lat[1]) != 1 {
		t.Fatalf("%+v", lat)
	}
}

func TestParse(t *testing.T) {
	for _, p := range Pairs() {
		if got, ok := ParsePair(p.ID()); !ok || got != p {
			t.Errorf("ParsePair(%q)", p.ID())
		}
		if p.Symbols() == "" {
			t.Errorf("%s 缺符號說明", p.ID())
		}
	}
	if _, ok := ParsePair("f-h"); ok {
		t.Error("未支援的配對不得被接受")
	}
	for s, want := range map[string]Mode{"off": Off, "select": Select, "auto": Auto} {
		if got, ok := ParseMode(s); !ok || got != want || got.String() != s {
			t.Errorf("ParseMode(%q)", s)
		}
	}
	if _, ok := ParseMode("learn"); ok {
		t.Error("未知的模式不得被接受")
	}
}

func TestAutoPolicyThreshold(t *testing.T) {
	p := AutoPolicy{Margin: 6, Short: 3, PerExtra: 1}
	for _, c := range []struct {
		span, swaps int
		want        float64
	}{{1, 1, 9}, {2, 1, 6}, {2, 2, 7}, {4, 3, 8}, {1, 2, 10}} {
		if got := p.Threshold(c.span, c.swaps); got != c.want {
			t.Errorf("Threshold(%d, %d) = %g，應為 %g", c.span, c.swaps, got, c.want)
		}
	}
	if d := DefaultAutoPolicy(); d.Margin <= 0 || d.Short < 0 || d.PerExtra < 0 {
		t.Errorf("預設門檻必須為正：%+v", d)
	}
}

func TestAutoConfig(t *testing.T) {
	c := Config{}.With(ZZh, Auto).With(CCh, Select)
	if !c.Enabled() || !c.AutoEnabled() {
		t.Fatal("Enabled／AutoEnabled")
	}
	only := c.AutoOnly()
	if only.Mode(ZZh) != Auto || only.Mode(CCh) != Off {
		t.Fatalf("AutoOnly 只留 auto 配對：%q", only.Key())
	}
	if (Config{}.With(CCh, Select)).AutoEnabled() || (Config{}).AutoEnabled() {
		t.Fatal("沒有 auto 配對時 AutoEnabled 必須為假")
	}
	if c.Key() != "z-zh=auto,c-ch=select" {
		t.Fatalf("Key = %q", c.Key())
	}
}

func TestRules(t *testing.T) {
	ids := func(ps []Pair) []string {
		var out []string
		for _, p := range ps {
			out = append(out, p.ID())
		}
		return out
	}
	for _, c := range []struct {
		typed, matched string
		want           []string
	}{
		{"ㄗ", "ㄓ", []string{"z-zh"}},
		{"ㄔ", "ㄘ", []string{"c-ch"}},
		{"ㄙㄣ", "ㄕㄥ", []string{"s-sh", "en-eng"}}, // 聲母配對在前、韻母配對在後
		{"ㄧㄣ", "ㄧㄥ", []string{"en-eng"}},
		{"ㄅㄚˋ", "ㄅㄚˋ", nil},
		{"ㄅㄚˋ", "ㄆㄚˋ", nil},                // 不是任何配對的互換
		{"ㄕㄨㄥˋ", "ㄙㄨㄥˋ", []string{"s-sh"}}, // 來源音節不在音節表也能比
	} {
		if got := ids(Rules(zhuyin.Syllable(c.typed), zhuyin.Syllable(c.matched))); !slices.Equal(got, c.want) {
			t.Errorf("Rules(%s, %s) = %v，應為 %v", c.typed, c.matched, got, c.want)
		}
	}
	// 與 Alternatives 一致：每個替代的 Rules 就是 Rules(來源, 替代) 算出來的。
	cfg := Config{}.With(ZZh, Select).With(CCh, Select).With(SSh, Select).With(EnEng, Select)
	for _, s := range []zhuyin.Syllable{"ㄗㄣ", "ㄕㄥˋ", "ㄘㄥˊ", "ㄧㄣ"} {
		for _, a := range cfg.Alternatives(s) {
			if got := Rules(s, a.Syl); !slices.Equal(got, a.Rules) {
				t.Errorf("%s→%s：Rules %v 與 Alternatives 的 %v 不一致", s, a.Syl, got, a.Rules)
			}
		}
	}
}

func TestThresholdForAddsNeuralExtraOnlyWhenNeuralChangedIt(t *testing.T) {
	p := AutoPolicy{Margin: 6, Short: 3, PerExtra: 1, NeuralExtra: 2}
	if p.ThresholdFor(2, 1, false) != 6 || p.ThresholdFor(2, 1, true) != 8 || p.ThresholdFor(1, 2, true) != 12 {
		t.Errorf("ThresholdFor：%g %g %g", p.ThresholdFor(2, 1, false), p.ThresholdFor(2, 1, true), p.ThresholdFor(1, 2, true))
	}
	if DefaultAutoPolicy().NeuralExtra != 0 {
		t.Error("預設不加神經額外門檻，直到資料支持")
	}
}
