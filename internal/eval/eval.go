// Package eval 量測選詞品質、逐鍵行為與效能。
package eval

import (
	"hash/fnv"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/0c-biggo/0c-ime/core/engine"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// Reader 以詞庫為參考標音：把一段文字以詞庫做最大機率斷詞，每個詞取 P(r|w) 最高的讀音。
//
// 這是自動標音，會把詞庫的讀音錯誤帶進參考答案，也看不出多音字在語境中的錯讀；
// 所以它量的是「讀音已知時，語言模型與斷詞選對字的能力」，不是讀音覆蓋率。
type Reader struct {
	Lex *lexicon.Compact
	LM  lm.System
	// 同分時的決勝依據（見 NewReader）。charPRW[字][讀音]：單字詞條的 P(r|w)；
	// support[字][讀音]：這個字在詞庫的多字詞裡讀這個音的詞頻總和。
	charPRW, support map[rune]map[zhuyin.Syllable]float64
}

// NewReader 建立標音器，並備妥讀音同分時的決勝依據。
//
// 重、麗、仔這類常用破音字在來源資料裡沒有主讀音，各讀音的 P(r|w) 相同；大埔、埔心這類詞在來源裡
// 有兩個同等級的讀音。同分時若取詞庫順序的第一個，結果只是排序的偶然（過去甚至隨 Go 版本而變），
// 路名的讀音與評測的參考注音都跟著飄。決勝順序：
//  1. 各字的單字讀音等級：埔的 ㄆㄨˇ 是主讀音、ㄅㄨˇ 不是，所以 大埔 取 ㄉㄚˋ ㄆㄨˇ；
//  2. 各字在多字詞裡讀這個音的詞頻：麗看美麗、華麗，仔看仔細；
//  3. 詞庫的儲存順序（依讀音排序，與 Go 版本無關）。
func NewReader(lex *lexicon.Compact, sys lm.System) Reader {
	r := Reader{Lex: lex, LM: sys,
		charPRW: map[rune]map[zhuyin.Syllable]float64{}, support: map[rune]map[zhuyin.Syllable]float64{}}
	add := func(m map[rune]map[zhuyin.Syllable]float64, c rune, y zhuyin.Syllable, v float64, sum bool) {
		if m[c] == nil {
			m[c] = map[zhuyin.Syllable]float64{}
		}
		if sum {
			m[c][y] += v
		} else {
			m[c][y] = max(m[c][y], v)
		}
	}
	lex.All(func(e lexicon.Entry) {
		rs := []rune(e.Surface)
		if e.Correction || len(rs) != len(e.Reading) {
			return
		}
		if len(rs) == 1 {
			add(r.charPRW, rs[0], e.Reading[0], e.PRW, false)
			return
		}
		w := sys.Uni(e.Surface) * e.PRW
		for i, c := range rs {
			add(r.support, c, e.Reading[i], w, true)
		}
	})
	return r
}

// better 回報 P(r|w) 同分的兩個讀音中 a 是否比 b 更可能是 w 的讀音。
func (r Reader) better(w string, a, b *lexicon.Entry) bool {
	rs := []rune(w)
	if len(rs) != len(a.Reading) || len(rs) != len(b.Reading) {
		return false
	}
	var prw, sup float64
	for i, c := range rs {
		if a.Reading[i] == b.Reading[i] {
			continue
		}
		prw += r.charPRW[c][a.Reading[i]] - r.charPRW[c][b.Reading[i]]
		sup += r.support[c][a.Reading[i]] - r.support[c][b.Reading[i]]
	}
	if prw != 0 {
		return prw > 0
	}
	return sup > 0
}

// Annotate 回傳讀音；文字中有詞庫無法涵蓋的字時回傳 false。
func (r Reader) Annotate(text string) ([]zhuyin.Syllable, bool) {
	syls, _, ok := r.Segment(text)
	return syls, ok
}

// Segment 與 Annotate 相同，另外回傳斷詞結果。
func (r Reader) Segment(text string) ([]zhuyin.Syllable, []string, bool) {
	rs := []rune(text)
	n := len(rs)
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
			es := r.Lex.Lookup(w)
			var pick *lexicon.Entry
			for k := range es {
				if es[k].Correction {
					continue
				}
				if pick == nil || es[k].PRW > pick.PRW || es[k].PRW == pick.PRW && r.better(w, &es[k], pick) {
					pick = &es[k]
				}
			}
			if pick == nil {
				continue
			}
			if sc := best[j] + math.Log(r.LM.Uni(w)); sc > best[i] {
				best[i], back[i], read[i] = sc, j, pick.Reading
			}
		}
	}
	if math.IsInf(best[n], -1) {
		return nil, nil, false
	}
	var out []zhuyin.Syllable
	var words []string
	for i := n; i > 0; i = back[i] {
		out = append(read[i], out...)
		words = append([]string{string(rs[back[i]:i])}, words...)
	}
	return out, words, true
}

// IsHan 回報是否為漢字。
func IsHan(r rune) bool { return zhuyin.IsHan(r) }

// Chunks 把句子在非漢字處切開，只留長度 ≥ min 的純漢字片段。
func Chunks(s string, min int) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) >= min {
			out = append(out, string(cur))
		}
		cur = cur[:0]
	}
	for _, r := range s {
		if zhuyin.IsHan(r) {
			cur = append(cur, r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

// Typist 以注音符號請求在一個 session 上打字，並記錄每個請求的處理時間。
type Typist struct {
	E        *engine.Engine
	ID       uint64
	Latency  []time.Duration
	Tab      TabStats
	Rewrites RewriteStats
}

// TabStats 是 Tab 補全的被動統計：只看建議，不實際接受，所以不影響正確率的量測。
// 「正確」指組字加上建議正好是參考答案的前綴；接受後的那幾個音節不再重複計算。
type TabStats struct {
	Keys, Syllables int // 全部按鍵數（注音符號＋聲調）與音節數
	Shown, Correct  int // 出現建議的次數；其中正確的次數
	SavedKeys       int // 接受所有正確建議可省下的按鍵（已扣掉 Tab 本身）
	SavedSyllables  int
}

func keysOf(s zhuyin.Syllable) int {
	n := len([]rune(string(s)))
	if s.ToneOf() == 1 {
		n++ // 一聲要按空白鍵
	}
	return n
}

// NewTypist 開一個暫停學習的 session：評測量的是開箱即用的品質，提交不得改變之後的結果。
func NewTypist(e *engine.Engine) *Typist {
	t := &Typist{E: e, ID: e.OpenSession()}
	t.do(proto.Request{Type: proto.Pause, On: true})
	t.Latency = nil
	return t
}

// Commit 提交並回傳提交的文字。
func (t *Typist) Commit() string { return t.do(proto.Request{Type: proto.Commit}).Commit }

func (t *Typist) do(r proto.Request) proto.Snapshot {
	start := time.Now()
	s, err := t.E.Do(t.ID, r)
	t.Latency = append(t.Latency, time.Since(start))
	if err != nil {
		panic(err)
	}
	return s
}

// Type 打一個音節（逐符號，含聲調；一聲送一聲記號），回傳打完後的快照。
func (t *Typist) Type(s zhuyin.Syllable) proto.Snapshot {
	var snap proto.Snapshot
	for _, r := range string(s) {
		snap = t.do(proto.Request{Type: proto.Symbol, Symbol: string(r)})
	}
	if s.ToneOf() == 1 {
		snap = t.do(proto.Request{Type: proto.Symbol, Symbol: string(zhuyin.Tone1)})
	}
	return snap
}

// Cancel 放棄組字，不學習。
func (t *Typist) Cancel() { t.do(proto.Request{Type: proto.Cancel}) }

// Replay 逐音節打完 reading 後提交，回傳提交的文字、自第幾個音節起一路是 want 的前綴、
// 已顯示字元被回改的次數。
func (t *Typist) Replay(want string, reading []zhuyin.Syllable) (final string, stableFrom, rewrites int) {
	defer func() {
		if final == "" && len(reading) > 0 {
			t.Cancel()
		}
	}()
	var shown []string
	prev := []rune{}
	skip := 0 // 已被前一個正確建議涵蓋的音節數
	for i, s := range reading {
		snap := t.Type(s)
		cur := []rune(snap.Text())
		t.Rewrites.Observe(string(prev), string(cur), want, i+1)
		t.Tab.Keys += keysOf(s)
		t.Tab.Syllables++
		if skip > 0 {
			skip--
		} else if proposed := snap.SuggestedText(); proposed != "" {
			t.Tab.Shown++
			extra := len([]rune(proposed)) - len(cur)
			if extra > 0 && strings.HasPrefix(want, proposed) && i+extra < len(reading) {
				t.Tab.Correct++
				saved := -1 // Tab 本身
				for _, y := range reading[i+1 : i+1+extra] {
					saved += keysOf(y)
				}
				t.Tab.SavedKeys += saved
				t.Tab.SavedSyllables += extra
				skip = extra
			}
		}
		for i := 0; i < len(prev) && i < len(cur); i++ {
			if prev[i] != cur[i] {
				rewrites++
			}
		}
		shown = append(shown, string(cur))
		prev = cur
	}
	final = t.Commit()
	stableFrom = len(shown) + 1
	for k := len(shown) - 1; k >= 0 && strings.HasPrefix(want, shown[k]); k-- {
		stableFrom = k + 1
	}
	return final, stableFrom, rewrites
}

// EditDistance 以字元為單位。
func EditDistance(a, b string) int {
	x, y := []rune(a), []rune(b)
	prev := make([]int, len(y)+1)
	cur := make([]int, len(y)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(x); i++ {
		cur[0] = i
		for j := 1; j <= len(y); j++ {
			c := 1
			if x[i-1] == y[j-1] {
				c = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+c)
		}
		prev, cur = cur, prev
	}
	return prev[len(y)]
}

// Percentile 回傳 p（0–100）分位數。
func Percentile(ds []time.Duration, p float64) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[min(len(s)-1, int(math.Ceil(p/100*float64(len(s))))-1)]
}

// Split 以內容雜湊把片段分成開發集（約 1/5，調參用）與測試集（報告用）。
// 以雜湊而非位置切分，片段集合改變時同一個片段仍落在同一邊。
func Split(chunks []string, which string) []string {
	if which == "all" {
		return chunks
	}
	var out []string
	for _, c := range chunks {
		h := fnv.New32a()
		h.Write([]byte(c))
		if (h.Sum32()%5 == 0) == (which == "dev") {
			out = append(out, c)
		}
	}
	return out
}
