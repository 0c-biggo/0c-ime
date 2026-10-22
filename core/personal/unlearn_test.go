package personal

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 親子＋丼（手選）連用兩次晉升「親子丼」；丼另有讀音手選與更正，供撤銷與復原比對。
func unlearnModel() (*Model, []zhuyin.Syllable) {
	lex := lexicon.NewMem()
	for _, e := range []struct{ w, r string }{{"親子", "ㄑㄧㄣ ㄗˇ"}, {"丼", "ㄐㄧㄥˇ"}, {"井", "ㄐㄧㄥˇ"}, {"好", "ㄏㄠˇ"}} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
	}
	m := NewModel(lex, lm.NewMem())
	syls := zhuyin.ParseReading("ㄑㄧㄣ ㄗˇ ㄐㄧㄥˇ")
	return m, syls
}

func learnOyakodon(m *Model, syls []zhuyin.Syllable, n int) {
	for range n {
		m.Learn(Commit{Syls: syls, Path: []Seg{{Word: "親子", Start: 0, End: 2}, {Word: "丼", Start: 2, End: 3, Manual: true}}})
	}
}

// 「移除」（#182）：清掉學習紀錄、不留 tombstone，之後照常選字會再學回來；「不再學」（Forget）擋住被動學回。
func TestUnlearnLetsWordComeBack(t *testing.T) {
	m, syls := unlearnModel()
	learnOyakodon(m, syls, 2)
	if _, ok := m.Store.Phrases["親子丼"]; !ok {
		t.Fatal("兩次後應晉升為個人片語")
	}
	m.Unlearn("親子丼")
	if _, ok := m.Store.Phrases["親子丼"]; ok || m.Store.Tombstones["親子丼"] || m.Store.PhraseSeen["親子丼"] != 0 || m.Store.Uni["親子丼"] != 0 {
		t.Fatalf("移除要清掉片語與計數、不留 tombstone：%+v", m.Store)
	}
	learnOyakodon(m, syls, 2)
	if _, ok := m.Store.Phrases["親子丼"]; !ok {
		t.Fatal("移除之後照常選字應再學回來")
	}

	m.Forget("親子丼")
	learnOyakodon(m, syls, 3)
	if _, ok := m.Store.Phrases["親子丼"]; ok || !m.Store.Tombstones["親子丼"] {
		t.Fatal("不再學之後被動使用不得學回")
	}
}

// 英文詞：移除只刪證據、不留正規化 tombstone；不再學兩個都留。
func TestUnlearnEnglish(t *testing.T) {
	m, _ := unlearnModel()
	m.Store.English["kubectl"] = EnglishEvidence{Text: "Kubectl", Confirmed: 3, Days: 2, Usage: 3}
	m.Unlearn("Kubectl")
	if _, ok := m.Store.English["kubectl"]; ok || len(m.Store.Tombstones) != 0 {
		t.Fatalf("%+v %+v", m.Store.English, m.Store.Tombstones)
	}
	m.Forget("Kubectl")
	if !m.Store.Tombstones["kubectl"] || !m.Store.Tombstones["Kubectl"] {
		t.Fatalf("%+v", m.Store.Tombstones)
	}
	if got := m.Forgotten(); !slices.Equal(got, []string{"Kubectl"}) {
		t.Fatalf("同一個英文詞的大小寫只列一次：%v", got)
	}
	if !m.Relearn("Kubectl") || len(m.Store.Tombstones) != 0 {
		t.Fatalf("恢復學習要連正規化的 tombstone 一起刪：%v", m.Store.Tombstones)
	}
}

// 「不再學的詞」：列出 tombstone，恢復學習後照常學回來。
func TestRelearnAndForgotten(t *testing.T) {
	m, syls := unlearnModel()
	learnOyakodon(m, syls, 2)
	m.Forget("親子丼")
	m.Forget("井")
	if got := m.Forgotten(); !slices.Equal(got, []string{"井", "親子丼"}) {
		t.Fatalf("%v", got)
	}
	if m.Relearn("沒有忘記") {
		t.Fatal("不在清單裡的詞沒有可以恢復的")
	}
	gen := m.Gen
	if !m.Relearn("親子丼") || m.Store.Tombstones["親子丼"] || m.Gen == gen {
		t.Fatal("恢復學習要刪 tombstone 並讓解碼結果過期")
	}
	learnOyakodon(m, syls, 2)
	if _, ok := m.Store.Phrases["親子丼"]; !ok {
		t.Fatal("恢復學習之後照常學回來")
	}
}

// 復原（#182）：操作前留一份複本，復原後學習資料跟操作前完全一樣；中間學到的新東西不會被蓋掉。
func TestUndoRestoresForgetAndUnlearn(t *testing.T) {
	for _, op := range []struct {
		name string
		do   func(*Model)
	}{
		{"不再學", func(m *Model) { m.Forget("親子丼"); m.Forget("丼") }},
		{"移除", func(m *Model) { m.Unlearn("親子丼"); m.Unlearn("丼") }},
		{"恢復學習", func(m *Model) { m.Relearn("好") }},
	} {
		t.Run(op.name, func(t *testing.T) {
			m, syls := unlearnModel()
			learnOyakodon(m, syls, 3)
			m.Learn(Commit{Syls: zhuyin.ParseReading("ㄐㄧㄥˇ"), Path: []Seg{{Word: "井", End: 1, Manual: true}}})
			m.Forget("好")
			before := m.Store.Clone()
			prob := m.Prob("親子", "丼")
			u := m.Undoable(func() { op.do(m) })
			if u == nil || reflect.DeepEqual(m.Store, before) {
				t.Fatal("操作應改變學習資料")
			}
			gen := m.Gen
			if !m.Restore(u) || m.Gen == gen {
				t.Fatal("復原失敗")
			}
			if !reflect.DeepEqual(m.Store, before) {
				t.Fatalf("復原後要跟操作前一樣\n前 %+v\n後 %+v", before, m.Store)
			}
			if got := m.Prob("親子", "丼"); got != prob {
				t.Fatalf("復原後機率 %g，原本 %g（多樣性索引要重算）", got, prob)
			}
			if m.Restore(u) {
				t.Fatal("同一份複本只能復原一次")
			}
		})
	}
}

func TestUndoKeepsLaterLearning(t *testing.T) {
	m, syls := unlearnModel()
	learnOyakodon(m, syls, 3)
	before := m.Store.Clone()
	u := m.Undoable(func() { m.Forget("丼") })
	// 操作後又打了別的字。
	m.Learn(Commit{Syls: zhuyin.ParseReading("ㄏㄠˇ"), Path: []Seg{{Word: "好", End: 1, Manual: true}}})
	if !m.Restore(u) {
		t.Fatal("復原失敗")
	}
	if _, ok := m.Store.Phrases["親子丼"]; !ok || m.Store.Tombstones["丼"] || m.Store.Bi[pair("親子", "丼")] != before.Bi[pair("親子", "丼")] {
		t.Fatal("被忘記的學習要回來")
	}
	if m.Store.Uni["好"] == 0 || !m.Store.Readings["好\tㄏㄠˇ"] {
		t.Fatal("操作之後的學習不能被復原蓋掉")
	}
	want := before.Total + m.P.WPassive
	if d := m.Store.Total - want; d > 1e-9 || d < -1e-9 {
		t.Fatalf("Total %g，應為 %g", m.Store.Total, want)
	}
	for k, v := range m.Store.Ctx {
		sum := 0.0
		for bk, bv := range m.Store.Bi {
			if l, _, _ := strings.Cut(bk, "\t"); l == k {
				sum += bv
			}
		}
		if d := v - sum; d > 1e-9 || d < -1e-9 {
			t.Fatalf("Ctx[%s]=%g 與 Bi 合計 %g 不一致", k, v, sum)
		}
	}
	if !slices.Contains(m.Store.PhraseOrder, "親子丼") {
		t.Fatalf("片語順序要放回：%v", m.Store.PhraseOrder)
	}
}

// 換了一份個人資料（重新載入）之後，舊的複本不能灌回去。
func TestUndoRefusesOtherStore(t *testing.T) {
	m, syls := unlearnModel()
	learnOyakodon(m, syls, 2)
	u := m.Undoable(func() { m.Forget("丼") })
	m.Store = NewStore()
	if m.Restore(u) || len(m.Store.Phrases) != 0 {
		t.Fatal("換過個人資料後不能復原")
	}
	if m.Restore(nil) {
		t.Fatal("nil")
	}
}

// 沒有改到任何東西時不留複本。
func TestUndoableNoChange(t *testing.T) {
	m, _ := unlearnModel()
	if u := m.Undoable(func() { m.Unlearn("沒學過") }); u != nil {
		t.Fatalf("%+v", u)
	}
}
