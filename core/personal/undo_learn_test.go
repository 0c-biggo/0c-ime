package personal

import (
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 觸控鍵盤送出後立刻退格會撤銷這次學到的內容。Learn 在 Undoable 裡記下改到的項目，
// Restore 之後整份個人資料要跟學習之前一樣：一般計數、證據、手選、個人讀音、片語晉升、更正與代名詞習慣。
func TestUndoableLearnRestoresEverything(t *testing.T) {
	m, _ := journalStore(400, 7)
	r := rand.New(rand.NewPCG(9, 1))
	words := []string{"他", "她", "老師", "世界", "親子", "丼", "很好", "是", "事", "我們"}
	syl := zhuyin.Syllable("ㄕˋ")
	for round := range 300 {
		n := 1 + r.IntN(4)
		var path []Seg
		var syls []zhuyin.Syllable
		at := 0
		for range n {
			w := words[r.IntN(len(words))]
			l := len([]rune(w))
			path = append(path, Seg{Word: w, Start: at, End: at + l, Manual: r.IntN(2) == 0, Completion: r.IntN(5) == 0})
			for range l {
				syls = append(syls, syl)
			}
			at += l
		}
		c := Commit{Syls: syls, Path: path}
		if r.IntN(6) == 0 {
			c.Rejected = []RejectedSeg{{Word: "事", Start: 0, End: 1, Correction: true}}
		}
		// Most rounds keep what they learn, so phrases get promoted and corrections contested.
		if r.IntN(3) != 0 {
			m.Learn(c)
			continue
		}
		before := cloneModel(m)
		u := m.Undoable(func() { m.Learn(c) })
		if u == nil {
			continue
		}
		if !m.Restore(u) {
			t.Fatalf("round %d: restore refused", round)
		}
		before.Store.CorrectionSerial = m.Store.CorrectionSerial // 只是排序用的流水號
		storesClose(t, fmt.Sprint("round ", round), before.Store, m.Store)
	}
}

func TestUndoableLearnFollowRestores(t *testing.T) {
	m, _ := journalStore(200, 3)
	before := cloneModel(m)
	u := m.Undoable(func() { m.LearnFollow("你好", "朋友") })
	if u == nil || !m.Restore(u) {
		t.Fatal("nothing to restore")
	}
	storesClose(t, "follow", before.Store, m.Store)
}
