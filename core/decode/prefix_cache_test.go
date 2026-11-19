package decode

import (
	"fmt"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/vocab"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// topPrefixed 的快取只影響速度（#307）：同一串操作（解碼、加自訂詞、學習、刪自訂詞），快取上限 4096、512、1
// 或不快取，每一步的尾端詞群、解碼結果與補全都要逐字相同。手機把上限調小，答案不能因此改變。
func TestPrefixCacheSizeDoesNotChangeResults(t *testing.T) {
	var want string
	for _, keys := range []int{0, 4096, 512, 1, -1} {
		got := prefixCacheScript(t, keys)
		if keys == 0 {
			want = got
			continue
		}
		if got != want {
			t.Fatalf("PrefixCacheKeys=%d 的結果與預設不同：\n預設\n%s\n%d\n%s", keys, want, keys, got)
		}
	}
	// 前提：加自訂詞真的改變了系統詞的前 M 名（「假意」的頻率下限把「甲乙」擠出去），否則上面的比對什麼也沒驗到。
	steps := strings.Split(want, "\n")
	if !strings.Contains(steps[0], "甲乙") || strings.Contains(steps[1], "甲乙") || !strings.Contains(steps[3], "甲乙") {
		t.Fatalf("測試前提：加自訂詞「假意」後系統詞的前 M 名應改變、刪掉後恢復\n%s", want)
	}
}

// 學習不改變 personal.Model 的 Prior：快取跨學習保留（桌面每句提交後不必重排），只有自訂詞彙改變時重算。
func TestPrefixCacheKeptAcrossLearning(t *testing.T) {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, x := range []struct {
		w, r string
		p    float64
	}{{"甲", "ㄐㄧㄚˇ", 1e-3}, {"甲乙", "ㄐㄧㄚˇ ㄧˇ", 5e-8}, {"乙", "ㄧˇ", 1e-3}} {
		lex.Add(lexicon.Entry{Surface: x.w, Reading: zhuyin.ParseReading(x.r), PRW: 1})
		sys.SetUni(x.w, x.p)
	}
	m := personal.NewModel(lex, sys)
	d := &Decoder{Lex: m.Source(), Score: m}
	lat := input.Exact{}.Expand(zhuyin.ParseReading("ㄐㄧㄚˇ"))
	d.Decode(lat, nil, true)
	d.cache["保留"] = nil
	m.Learn(personal.Commit{Syls: zhuyin.ParseReading("ㄐㄧㄚˇ ㄧˇ"), Path: []personal.Seg{{Word: "甲乙", End: 2, Manual: true}}})
	d.Decode(lat, nil, true)
	if _, ok := d.cache["保留"]; !ok {
		t.Fatal("學習之後 Prior 沒變，快取應保留")
	}
	if _, err := m.VocabAdd([]vocab.Entry{{Text: "甲乙", Readings: []string{"ㄐㄧㄚˇ ㄧˇ"}}}, vocab.SourceManual); err != nil {
		t.Fatal(err)
	}
	d.Decode(lat, nil, true)
	if _, ok := d.cache["保留"]; ok {
		t.Fatal("自訂詞彙改了，快取應重算")
	}
}

// learnedPrior 沒有 Prior：前 M 名依含個人計數的 Prob 排序，只報 MassVersion。
type learnedPrior struct {
	p   map[string]float64
	gen int
}

func (s *learnedPrior) Prob(_ string, w string) float64 { return s.p[w] }
func (s *learnedPrior) MassVersion() (int, int64)       { return s.gen, 0 }

// 評分源沒有 Prior 時看個人資料版本；TopM 改變時也重算。
func TestPrefixCacheFollowsMassVersionWithoutPrior(t *testing.T) {
	lex := lexicon.NewMem()
	for _, x := range []struct{ w, r string }{{"甲", "ㄐㄧㄚˇ"}, {"甲乙", "ㄐㄧㄚˇ ㄧˇ"}, {"假意", "ㄐㄧㄚˇ ㄧˋ"}} {
		lex.Add(lexicon.Entry{Surface: x.w, Reading: zhuyin.ParseReading(x.r), PRW: 1})
	}
	reading := zhuyin.ParseReading("ㄐㄧㄚˇ")
	top := func(d *Decoder) []string {
		var out []string
		for _, e := range d.topPrefixed(lex, reading) {
			out = append(out, e.Surface)
		}
		return out
	}
	for _, keys := range []int{0, -1} {
		s := &learnedPrior{p: map[string]float64{"甲": .5, "甲乙": .2, "假意": .1}}
		d := &Decoder{Lex: lex, Score: s, TopM: 2, PrefixCacheKeys: keys}
		if got := fmt.Sprint(top(d)); got != "[甲 甲乙]" {
			t.Fatalf("keys=%d：%s", keys, got)
		}
		s.p["假意"], s.gen = .3, 1
		if got := fmt.Sprint(top(d)); got != "[甲 假意]" {
			t.Fatalf("keys=%d：個人資料版本變了，前 M 名應重排：%s", keys, got)
		}
		d.TopM = 3
		if got := fmt.Sprint(top(d)); got != "[甲 假意 甲乙]" {
			t.Fatalf("keys=%d：TopM 變了應重算：%s", keys, got)
		}
	}
}

func prefixCacheScript(t *testing.T, keys int) string {
	t.Helper()
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, x := range []struct {
		w, r string
		p    float64
	}{
		{"甲", "ㄐㄧㄚˇ", 1e-3}, {"假", "ㄐㄧㄚˇ", 3e-8}, {"甲乙", "ㄐㄧㄚˇ ㄧˇ", 5e-8}, {"假意", "ㄐㄧㄚˋ ㄧˋ", 1e-9},
		{"假意", "ㄐㄧㄚˇ ㄧˋ", 1e-9}, {"乙", "ㄧˇ", 1e-3}, {"意", "ㄧˋ", 1e-3}, {"天氣", "ㄊㄧㄢ ㄑㄧˋ", 1e-2}, {"天空", "ㄊㄧㄢ ㄎㄨㄥ", 1e-2}, {"天下", "ㄊㄧㄢ ㄒㄧㄚˋ", 1e-2},
	} {
		lex.Add(lexicon.Entry{Surface: x.w, Reading: zhuyin.ParseReading(x.r), PRW: 1})
		sys.SetUni(x.w, x.p)
	}
	m := personal.NewModel(lex, sys)
	d := &Decoder{Lex: m.Source(), Score: m, TopM: 2, PrefixCacheKeys: keys}
	lat := input.Exact{}.Expand(zhuyin.ParseReading("ㄐㄧㄚˇ"))
	var b strings.Builder
	step := func() {
		var tail []string
		for _, e := range d.tailEntries(lat, 0) {
			tail = append(tail, fmt.Sprintf("%s/%g", e.e.Surface, e.e.PRW))
		}
		path := d.Decode(lat, nil, true)
		var words []string
		for _, g := range path {
			words = append(words, g.Word)
		}
		c, ok := d.Complete(lat, path)
		fmt.Fprintf(&b, "尾端 %v｜路徑 %v｜補全 %s %.6g %v\n", tail, words, c.Entry.Surface, c.Conf, ok)
	}
	step()
	if _, err := m.VocabAdd([]vocab.Entry{{Text: "假意", Readings: []string{"ㄐㄧㄚˇ ㄧˋ"}}}, vocab.SourceManual); err != nil {
		t.Fatal(err)
	}
	step()
	m.Learn(personal.Commit{Syls: zhuyin.ParseReading("ㄐㄧㄚˇ ㄧˇ"), Path: []personal.Seg{{Word: "甲乙", End: 2, Manual: true}}})
	step()
	if _, err := m.VocabRemove([]string{"假意"}, vocab.SourceManual); err != nil {
		t.Fatal(err)
	}
	step()
	return b.String()
}
