package session

import (
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/0c-biggo/0c-ime/core/proto"
)

// 亂數探測（快取照常開著）：打鍵、長按的單一注音、聲調鍵、退格、移游標、左欄、fix 混在一起，每一步檢查：
//   - 畫面上的詞都列得出來、fix 得了（在它的位置），只移游標不改變切法；
//   - 記下的拒絕字數等於它佔的音節數（學習照 Syls[Start:End] 取讀音）；
//   - 每個還沒固定的音節都有讀音時，按鍵圖解得出路徑（不會落到退路）。
func TestNineSegmentProbe(t *testing.T) {
	m, words := listModel()
	rng := rand.New(rand.NewSource(99))
	steps := 0
	for round := range 8 {
		s := New(m)
		s.NineSegment, s.Toneless, s.Abbreviation, s.MixedWithToneless = true, true, true, true
		var queue []string
		for range 40 {
			if len(queue) == 0 {
				for _, y := range words[rng.Intn(len(words))].Reading {
					for _, r := range string(y.Base()) {
						queue = append(queue, listKey(r))
					}
				}
			}
			var r proto.Request
			switch op := rng.Intn(100); {
			case op < 70:
				r = proto.Request{Type: proto.Symbol, Group: queue[0]}
				queue = queue[1:]
			case op < 75:
				r = proto.Request{Type: proto.Symbol, Group: string([]rune("ㄈㄅㄋㄍ")[rng.Intn(4)])}
			case op < 83:
				r = proto.Request{Type: proto.Symbol, Group: nineTone}
			case op < 90:
				r = proto.Request{Type: proto.Backspace}
			case op < 94:
				r = proto.Request{Type: proto.Cursor, To: rng.Intn(len(s.syls) + 1)}
			case op < 97:
				if nr := s.View().NineReadings; nr != nil && len(nr.Readings) > 0 {
					k := rng.Intn(len(nr.Readings))
					r = proto.Request{Type: proto.NineReading, To: nr.At, Word: nr.Readings[k], Length: nr.Keys[k]}
				} else {
					r = proto.Request{Type: proto.Key, Key: proto.KeyEnd}
				}
			default:
				if len(s.path) > 0 {
					g := s.path[rng.Intn(len(s.path))]
					r = proto.Request{Type: proto.Fix, To: g.Start, Length: g.End - g.Start, Word: g.Word}
				} else {
					r = proto.Request{Type: proto.Key, Key: proto.KeyEnd}
				}
			}
			before := segs(s)
			s.Handle(r)
			steps++
			where := fmt.Sprintf("round %d after %+v (segments %s -> %s)", round, r, before, segs(s))
			for _, rej := range s.rejected {
				if utf8.RuneCountInString(rej.Word) != rej.End-rej.Start {
					t.Fatalf("%s: rejection %+v", where, rej)
				}
			}
			if s.nineReadable() && !s.nineGraphDecodes() {
				t.Fatalf("%s: every syllable readable but the key graph has no path", where)
			}
			checkShownWordsFit(t, s, rng, where)
		}
	}
	if steps < 300 {
		t.Fatalf("only %d steps", steps)
	}
}

// checkShownWordsFit：畫面上的一個詞（每一步抽一個，全部都查要好幾分鐘；不算尾端前綴、字面文字）照手機的做法在
// 淺複本上把游標移到詞首（只移游標不重新切）、列它那個詞長，列得出來；在另一份複本上 fix 得了。
func checkShownWordsFit(t *testing.T, s *Session, rng *rand.Rand, where string) {
	t.Helper()
	if len(s.path) == 0 {
		return
	}
	g := s.path[rng.Intn(len(s.path))]
	if g.Partial || s.literals[g.Start] || g.End-g.Start != utf8.RuneCountInString(g.Word) {
		return
	}
	if strings.ContainsAny(g.Word, "你妳他她") {
		return // 代名詞習慣（pronoun.go）把「妳」「她」顯示成中性字；亂數詞庫裡兩個字的讀音不同
	}
	where = fmt.Sprintf("%s: shown %q at %d+%d", where, g.Word, g.Start, g.End-g.Start)
	c := *s
	c.cand = nil
	c.CandidateBeforeCursor = false
	c.Handle(proto.Request{Type: proto.Cursor, To: g.Start})
	if segs(&c) != segs(s) {
		t.Fatalf("%s: moving the cursor re-cut %s -> %s", where, segs(s), segs(&c))
	}
	c.Handle(proto.Request{Type: proto.Candidates, Length: g.End - g.Start})
	var items []string
	if c.cand != nil {
		for _, it := range c.cand.items {
			items = append(items, it.Entry.Surface)
		}
	}
	if !slices.Contains(items, g.Word) {
		t.Fatalf("%s is not listed (path %v; listed %v)", where, s.path, items)
	}
	f := *s
	f.cand = nil
	if snap := f.Handle(proto.Request{Type: proto.Fix, To: g.Start, Length: g.End - g.Start, Word: g.Word}); len(snap.Notices) > 0 {
		t.Fatalf("%s cannot be fixed (path %v): %v", where, s.path, snap.Notices)
	}
}
