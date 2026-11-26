package session

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"slices"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/layout"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/vocab"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 自動切字的快取（逐鍵增量解碼 nineSearch／sylSearch、讀音列 nineRowCache、常用程度 basePriors／prefixPriors）
// 永遠不給過期的結果：同一串操作做兩次，一次快取照常留著，一次每個請求前全部清掉，每一步的快照逐一相同。
// 操作夾著會讓快取失效的事：學習（個人資料版本改變）、自訂詞彙改變、鎖定與固定改變、在快取的範圍前面插入／刪除／
// 改鍵、切換自動切字與鍵盤排列、退格後重新切。
func TestNineSegmentCachesNeverStale(t *testing.T) {
	type step func(s *Session) proto.Snapshot
	key := func(k int) step {
		return func(s *Session) proto.Snapshot {
			return s.Handle(proto.Request{Type: proto.Symbol, Group: nineGroups[k]})
		}
	}
	keys := func(reading string) []step {
		var out []step
		for _, k := range keysOf(reading) {
			out = append(out, key(k))
		}
		return out
	}
	req := func(r proto.Request) step {
		return func(s *Session) proto.Snapshot { return s.Handle(r) }
	}
	do := func(f func(s *Session)) step {
		return func(s *Session) proto.Snapshot { f(s); return s.View() }
	}
	pick := func(word string, length int) step {
		return func(s *Session) proto.Snapshot {
			snap := s.Handle(proto.Request{Type: proto.Candidates, Length: length})
			if snap.Candidates != nil {
				for _, it := range snap.Candidates.Items {
					if it.Text == word {
						return s.Handle(proto.Request{Type: proto.Select, Index: it.ID})
					}
				}
			}
			return s.Handle(proto.Request{Type: proto.Close})
		}
	}
	cases := map[string][]step{
		"learning": slices.Concat(keys("ㄋㄧ ㄏㄠ"),
			[]step{req(proto.Request{Type: proto.Commit})},
			keys("ㄋㄧ"),
			[]step{do(func(s *Session) {
				s.model.Learn(personal.Commit{Syls: zhuyin.ParseReading("ㄇㄧˇ ㄈㄣˇ"), Path: []personal.Seg{{Word: "米粉", Start: 0, End: 2, Manual: true}}})
			})},
			keys("ㄈㄣ ㄒㄧㄢ")),
		"vocabulary": slices.Concat(keys("ㄒㄧ ㄢ"),
			[]step{do(func(s *Session) {
				if _, err := s.model.VocabAdd([]vocab.Entry{{Text: "仙", Readings: []string{"ㄒㄧㄢ"}}, {Text: "喜安", Readings: []string{"ㄒㄧˇ ㄢ"}}}, "test"); err != nil {
					panic(err)
				}
			})},
			keys("ㄕ ㄒㄧㄢ")),
		"locks and fixed": slices.Concat(keys("ㄒㄧㄢ ㄋㄧ"),
			[]step{pick("西安", 1), req(proto.Request{Type: proto.NineReading, To: 2, Word: "ㄋㄧ", Length: 2})},
			keys("ㄏㄠ"),
			[]step{req(proto.Request{Type: proto.Fix, To: 0, Length: 2, Word: "西安"}), req(proto.Request{Type: proto.Backspace})},
			keys("ㄠ ㄕ")),
		"edits before the span": slices.Concat(keys("ㄒㄧ ㄢ ㄋㄧ ㄏㄠ"),
			[]step{req(proto.Request{Type: proto.Cursor, To: 1}), key(3), key(8), req(proto.Request{Type: proto.Backspace}),
				req(proto.Request{Type: proto.Cursor, To: 0}), key(7), req(proto.Request{Type: proto.Key, Key: proto.KeyEnd})},
			keys("ㄈㄣ")),
		"switching": slices.Concat(keys("ㄋㄧ ㄏ"),
			[]step{do(func(s *Session) { s.NineSegment = false })},
			keys("ㄠ"),
			[]step{key(0), do(func(s *Session) { s.NineSegment = true })},
			keys("ㄋㄧ"),
			[]step{do(func(s *Session) { s.SetLayout(layout.Hsu) })},
			keys("ㄏㄠ"),
			[]step{do(func(s *Session) { s.SetLayout(nil) })},
			keys("ㄇㄧ")),
		"backspace re-cuts": slices.Concat(keys("ㄒㄧㄢ ㄕ"),
			[]step{req(proto.Request{Type: proto.Backspace}), req(proto.Request{Type: proto.Backspace})},
			keys("ㄢ ㄋㄧ"),
			[]step{req(proto.Request{Type: proto.Backspace}), req(proto.Request{Type: proto.Backspace}), req(proto.Request{Type: proto.Backspace})},
			keys("ㄧ ㄢ")),
	}
	for name, steps := range cases {
		run := func(fresh bool) []string {
			s := segSession()
			s.Toneless, s.Abbreviation, s.MixedWithToneless = true, true, true
			var out []string
			for _, f := range steps {
				if fresh {
					dropNineCaches(s)
				}
				data, _ := json.Marshal(f(s))
				out = append(out, string(data)+" "+segs(s))
			}
			return out
		}
		cached, fresh := run(false), run(true)
		for i := range cached {
			if cached[i] != fresh[i] {
				t.Fatalf("%s: step %d\ncached %s\nfresh  %s", name, i, cached[i], fresh[i])
			}
		}
	}
}

func dropNineCaches(s *Session) {
	s.nineSearch, s.sylSearch = nil, nil
	s.nineRowCache, s.basePriors, s.prefixPriors, s.readingMemo = nil, nil, nil, nil
}

// APP 的不變式：自動切字時候選列上的每個詞都放得進它的範圍（fix 會成功）。照手機的做法在 session 的淺複本上列表：
// 游標移到句首、依序列每種詞長（多字的詞長接著列單字），每個列出來的詞在另一份複本上 fix。快取照常開著。
func TestNineSegmentListedWordsFit(t *testing.T) {
	m, words := listModel()
	rng := rand.New(rand.NewSource(77))
	checked := 0
	for round := range 6 {
		s := New(m)
		s.NineSegment, s.Toneless, s.Abbreviation, s.MixedWithToneless = true, true, true, true
		s.CandidateBeforeCursor = true
		var reading []zhuyin.Syllable
		for len(reading) < 2+rng.Intn(5) {
			reading = append(reading, words[rng.Intn(len(words))].Reading...)
		}
		for _, y := range reading {
			for _, r := range string(y.Base()) {
				s.Handle(proto.Request{Type: proto.Symbol, Group: listKey(r)})
				checked += checkListedWordsFit(t, s, round)
			}
			if rng.Intn(3) == 0 {
				s.Handle(proto.Request{Type: proto.Symbol, Group: nineTone})
			}
		}
		s.Handle(proto.Request{Type: proto.Cancel})
	}
	if checked < 300 {
		t.Fatalf("only %d words checked", checked)
	}
}

func checkListedWordsFit(t *testing.T, s *Session, round int) int {
	t.Helper()
	saved := *s
	defer func() { *s = saved }()
	total := len(s.syls)
	s.CandidateBeforeCursor = false
	s.Handle(proto.Request{Type: proto.Cursor, To: 0})
	checked := 0
	for length := min(4, total); length >= 1; length-- {
		s.Handle(proto.Request{Type: proto.Close})
		snap := s.Handle(proto.Request{Type: proto.Candidates, Length: length})
		if s.cand == nil || snap.Candidates == nil {
			continue
		}
		c := *s.cand
		for id, item := range c.items {
			if c.raw && id == len(c.items)-1 || c.keyLiterals[id] != "" {
				continue // 原樣注音
			}
			start, n := c.span(id)
			trial := *s
			trial.cand = nil
			fixed := trial.Handle(proto.Request{Type: proto.Fix, To: start, Length: n, Word: item.Entry.Surface})
			if len(fixed.Notices) > 0 {
				t.Fatalf("round %d: %q listed for %d+%d but fix fails (%v); segments %s", round, item.Entry.Surface, start, n, fixed.Notices, segs(s))
			}
			checked++
		}
	}
	return checked
}

// listModel 是亂數詞庫：每個音節、每個聲調都有一兩個單字（同真的詞庫，九宮格的鍵拼得出的音節都有字），加上
// 1,500 個 2–4 字詞與 bigram。
func listModel() (*personal.Model, []lexicon.Entry) {
	rng := rand.New(rand.NewSource(51))
	bases := zhuyin.Inventory()
	slices.Sort(bases)
	var syls []zhuyin.Syllable
	for _, b := range bases {
		for tone := 1; tone <= 5; tone++ {
			syls = append(syls, b.WithTone(tone))
		}
	}
	lex, sys := lexicon.NewMem(), lm.NewMem()
	var words, singles []lexicon.Entry
	char := 0x4E00
	add := func(surface string, reading []zhuyin.Syllable) lexicon.Entry {
		e := lexicon.Entry{Surface: surface, Reading: reading, PRW: []float64{1, 1, 0.5}[rng.Intn(3)]}
		lex.Add(e)
		sys.SetUni(surface, math.Exp(-4-8*rng.Float64()))
		words = append(words, e)
		return e
	}
	for _, y := range syls {
		for range 1 + rng.Intn(2) {
			singles = append(singles, add(string(rune(char)), []zhuyin.Syllable{y}))
			char++
		}
	}
	for range 1500 {
		var surface strings.Builder
		var reading []zhuyin.Syllable
		for range 2 + rng.Intn(3) {
			s := singles[rng.Intn(len(singles))]
			surface.WriteString(s.Surface)
			reading = append(reading, s.Reading...)
		}
		add(surface.String(), reading)
	}
	for range 3000 {
		a, b := words[rng.Intn(len(words))], words[rng.Intn(len(words))]
		sys.SetBi(a.Surface, b.Surface, math.Exp(-1-5*rng.Float64()))
	}
	return personal.NewModel(lex, sys), words
}

func listKey(r rune) string {
	for k := 1; k <= 10; k++ {
		if strings.ContainsRune(nineGroups[k], r) {
			return nineGroups[k]
		}
	}
	panic(fmt.Sprintf("no key for %c", r))
}

// nineGraphDecodes 回報每一段有還沒固定九宮格音節的組字（字面文字之間），在按鍵圖上都解得出路徑（同 resegment 的解碼）。
func (s *Session) nineGraphDecodes() bool {
	sylLat := s.lattice()
	cursor := s.keyCoord(s.cursor)
	for begin := 0; begin < len(s.syls); {
		if s.literals[begin] {
			begin++
			continue
		}
		end := begin + 1
		for end < len(s.syls) && !s.literals[end] {
			end++
		}
		if s.nineSegTouches(begin, end) {
			v := s.nineGraph(sylLat, begin, end, cursor)
			s.configureDocumentContext(begin)
			s.dec.Evidence, s.dec.CorrectionPenalty = nil, nil
			if len(s.dec.Decode(v.lat, s.nineLocks(v, end), false)) == 0 {
				return false
			}
		}
		begin = end
	}
	return true
}

// nineReadable 回報每個還沒固定的九宮格音節在按鍵圖上都有讀音（spanRows，同 resegment 的退路）。
func (s *Session) nineReadable() bool {
	for p := range s.syls {
		if !s.inRun(p) {
			continue
		}
		n := s.nineSeg[p]
		tail := n.anchor < 0 && !s.inRun(p+1)
		if len(n.keys) > 3 || len(s.spanRows(n.keys, n.tone(), tail).readings) == 0 {
			return false
		}
	}
	return true
}
