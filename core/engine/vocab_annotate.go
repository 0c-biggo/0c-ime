package engine

import (
	"math"
	"slices"
	"strings"

	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/vocab"
	"git.biggo.com/0c/ime/core/zhuyin"
)

// VocabAnnotate（指令 annotate）：依詞庫幫一串詞標注音，標出破音字讓使用者點選，不悄悄猜。
const VocabAnnotate = "annotate"

// Annotation 是一個詞的建議讀音。
type Annotation struct {
	Text    string          `json:"word"`
	Reading string          `json:"reading"` // 建議的讀音；有字查不到時為空
	Chars   []CharAnnotated `json:"chars"`
	Unknown bool            `json:"unknown,omitempty"` // 有詞庫查不到讀音的字，要使用者自己填
	Exists  bool            `json:"exists,omitempty"`  // 系統詞庫已有這個詞（加入後當作偏好）
	English bool            `json:"english,omitempty"` // 英文詞（#146）：沒有讀音，加入時不給讀音
}

// CharAnnotated 是一個字的讀音建議。Ambiguous 表示這個字有其他常見讀音、而且不是靠整個詞決定的，要使用者確認。
type CharAnnotated struct {
	Char      string   `json:"char"`
	Reading   string   `json:"reading,omitempty"`
	Options   []string `json:"options,omitempty"` // 這個字的所有讀音，常見的在前
	Ambiguous bool     `json:"ambiguous,omitempty"`
}

// 破音字的門檻（避免罕用讀音讓每個字都變黃）。詞庫的讀音權重對次要讀音一律給得很低（曾的 ㄗㄥ 是 0.1），
// 所以主要看「這個字在常用多字詞裡讀這個音的詞頻總和」：第二常見的讀音至少是第一的 supportShare
// （曾 0.14、樂 0.5、行 0.14；覺 0.02、說 0.02 不算），或讀音權重至少是第一的 prwShare（還的 ㄏㄨㄢˊ）。
// 輕聲變體（明的 ㄇㄧㄥ˙）不算另一個讀音。
const (
	supportShare = 0.1
	prwShare     = 0.5
)

type lookuper interface {
	Lookup(surface string) []lexicon.Entry
}

// charSupport 第一次標音時建立：每個字在多字詞裡讀各音的系統詞頻總和。
func (e *Engine) charSupport() map[rune]map[zhuyin.Syllable]float64 {
	e.supportOnce.Do(func() {
		e.support = map[rune]map[zhuyin.Syllable]float64{}
		all, ok := e.Model.Lex.(interface{ All(func(lexicon.Entry)) })
		if !ok {
			return
		}
		all.All(func(x lexicon.Entry) {
			rs := []rune(x.Surface)
			if x.Correction || len(rs) < 2 || len(rs) != len(x.Reading) {
				return
			}
			p := e.Model.LM.Uni(x.Surface)
			for i, c := range rs {
				if e.support[c] == nil {
					e.support[c] = map[zhuyin.Syllable]float64{}
				}
				e.support[c][x.Reading[i]] += p
			}
		})
	})
	return e.support
}

func (e *Engine) annotate(texts []string) []Annotation {
	lk, _ := e.Model.Lex.(lookuper)
	support := e.charSupport()
	out := make([]Annotation, 0, len(texts))
	for _, t := range texts {
		t = strings.TrimSpace(t)
		a := Annotation{Text: t}
		if vocab.IsEnglish(t) {
			a.English = true
			out = append(out, a)
			continue
		}
		if lk == nil || t == "" {
			out = append(out, a)
			continue
		}
		rs := []rune(t)
		n := len(rs)
		// 以詞庫做最大機率斷詞；整個詞在詞庫裡時直接用它的讀音。
		best := make([]float64, n+1)
		back := make([]int, n+1)
		read := make([][]zhuyin.Syllable, n+1)
		for i := 1; i <= n; i++ {
			best[i] = math.Inf(-1)
			for j := max(0, i-8); j < i; j++ {
				if math.IsInf(best[j], -1) {
					continue
				}
				w := string(rs[j:i])
				pick := bestReading(lk.Lookup(w), i-j)
				if pick == nil {
					continue
				}
				if sc := best[j] + math.Log(e.Model.LM.Uni(w)); sc > best[i] {
					best[i], back[i], read[i] = sc, j, pick.Reading
				}
			}
		}
		a.Chars = make([]CharAnnotated, n)
		for i, r := range rs {
			a.Chars[i] = CharAnnotated{Char: string(r), Options: charOptions(lk.Lookup(string(r)), support[r])}
		}
		if math.IsInf(best[n], -1) {
			a.Unknown = true
			for i := range a.Chars {
				if len(a.Chars[i].Options) > 0 {
					a.Chars[i].Reading = a.Chars[i].Options[0]
					a.Chars[i].Ambiguous = ambiguous(lk.Lookup(a.Chars[i].Char), support[rs[i]])
				}
			}
			out = append(out, a)
			continue
		}
		var syls []zhuyin.Syllable
		for i := n; i > 0; i = back[i] {
			single := i-back[i] == 1
			for k := back[i]; k < i; k++ {
				c := &a.Chars[k]
				c.Reading = string(read[i][k-back[i]])
				c.Ambiguous = single && ambiguous(lk.Lookup(c.Char), support[rs[k]])
			}
			syls = append(slices.Clone(read[i]), syls...)
		}
		a.Reading = zhuyin.Key(syls)
		a.Exists = len(lk.Lookup(t)) > 0
		out = append(out, a)
	}
	return out
}

func bestReading(es []lexicon.Entry, n int) *lexicon.Entry {
	var pick *lexicon.Entry
	for k := range es {
		if es[k].Correction || len(es[k].Reading) != n {
			continue
		}
		if pick == nil || es[k].PRW > pick.PRW {
			pick = &es[k]
		}
	}
	return pick
}

// charOptions 列出一個字的讀音：讀音權重高的在前，同權重時常用詞裡出現多的在前（樂：ㄌㄜˋ、ㄩㄝˋ…）。
func charOptions(es []lexicon.Entry, support map[zhuyin.Syllable]float64) []string {
	es = slices.DeleteFunc(slices.Clone(es), func(e lexicon.Entry) bool { return e.Correction || len(e.Reading) != 1 })
	slices.SortStableFunc(es, func(a, b lexicon.Entry) int {
		switch {
		case a.PRW != b.PRW:
			return cmpDesc(a.PRW, b.PRW)
		default:
			return cmpDesc(support[a.Reading[0]], support[b.Reading[0]])
		}
	})
	var out []string
	for _, x := range es {
		if s := string(x.Reading[0]); !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func ambiguous(es []lexicon.Entry, support map[zhuyin.Syllable]float64) bool {
	var prw, sup []float64
	seen := map[zhuyin.Syllable]bool{}
	bases := map[zhuyin.Syllable]int{}
	for _, x := range es {
		if !x.Correction && len(x.Reading) == 1 {
			bases[x.Reading[0].Base()]++
		}
	}
	for _, x := range es {
		if x.Correction || len(x.Reading) != 1 {
			continue
		}
		y := x.Reading[0]
		if seen[y] || y.ToneOf() == 5 && bases[y.Base()] > 1 { // 輕聲變體不算另一個讀音；了的 ㄌㄜ˙ 本身是主讀音
			continue
		}
		seen[y] = true
		prw = append(prw, x.PRW)
		sup = append(sup, support[y])
	}
	return second(prw) >= prwShare*first(prw) && first(prw) > 0 || second(sup) >= supportShare*first(sup) && first(sup) > 0
}

func first(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	return slices.Max(xs)
}

func second(xs []float64) float64 {
	if len(xs) < 2 {
		return 0
	}
	s := slices.Sorted(slices.Values(xs))
	return s[len(s)-2]
}

func cmpDesc(a, b float64) int {
	switch {
	case a > b:
		return -1
	case a < b:
		return 1
	}
	return 0
}
