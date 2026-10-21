package personal

import (
	"fmt"
	"maps"
	"math"
	"math/rand/v2"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
)

// #182 發版前審查：Undoable 以前整份 Clone 再逐類 diff（個人資料到上限時一次 20～30 ms，在引擎鎖裡），
// 勾多個詞時 Unlearn 又逐詞各掃一遍整份資料（100 個詞將近一秒）。現在改成操作時記下改到的項目、
// 多個詞一起掃一遍。這裡用舊的做法當對照，確認結果完全一樣。

// undoableByDiff 是以前的 Undoable：整份複製、執行 f、逐類比對。只供對照。
func undoableByDiff(m *Model, f func()) *Undo {
	before := m.Store.Clone()
	f()
	s := m.Store
	u := &Undo{
		store: s,
		bi:    diffFloat(before.Bi, s.Bi), ctx: diffFloat(before.Ctx, s.Ctx), uni: diffFloat(before.Uni, s.Uni), uses: diffFloat(before.Uses, s.Uses),
		total:       floatChange{before.Total, s.Total, true, true},
		corrections: diff(before.Corrections, s.Corrections, same[Correction]),
		byContext:   diff(before.EvidenceByContext, s.EvidenceByContext, same[UsageEvidence]),
		byWord:      diff(before.EvidenceByWord, s.EvidenceByWord, same[UsageEvidence]),
		picked:      diff(before.Picked, s.Picked, slices.Equal[[]string]),
		phraseSeen:  diff(before.PhraseSeen, s.PhraseSeen, same[int]),
		phrases:     diff(before.Phrases, s.Phrases, same[string]),
		tombstones:  diff(before.Tombstones, s.Tombstones, same[bool]),
		readings:    diff(before.Readings, s.Readings, same[bool]),
		rejects:     diff(before.Rejects, s.Rejects, same[int]),
		pronouns:    diff(before.Pronouns, s.Pronouns, func(a, b PronounHabit) bool { return reflect.DeepEqual(a, b) }),
		english:     diff(before.English, s.English, same[EnglishEvidence]),
		order:       before.PhraseOrder, orderAfter: slices.Clone(s.PhraseOrder),
	}
	if u.empty() && before.Total == s.Total && slices.Equal(before.PhraseOrder, s.PhraseOrder) {
		return nil
	}
	return u
}

func diff[V any](before, after map[string]V, eq func(a, b V) bool) patch[V] {
	p := patch[V]{}
	for k, b := range before {
		if a, ok := after[k]; !ok || !eq(a, b) {
			p[k] = change[V]{before: b, after: a, had: true, has: ok}
		}
	}
	for k, a := range after {
		if _, ok := before[k]; !ok {
			p[k] = change[V]{after: a, has: true}
		}
	}
	return p
}

func diffFloat(before, after map[string]float64) floatPatch {
	p := floatPatch{}
	for k, b := range before {
		if a, ok := after[k]; !ok || a != b {
			p[k] = floatChange{b, a, true, ok}
		}
	}
	for k, a := range after {
		if _, ok := before[k]; !ok {
			p[k] = floatChange{0, a, false, true}
		}
	}
	return p
}

// journalWords 是合成個人資料用的字：數量少，詞與詞之間大量互相包含（「世」在「世界」「世界觀」裡），
// 涵蓋左詞含、右詞含、片語含被刪詞的情況。另有英文詞與代名詞「她」。
var journalRunes = []rune("世界觀親子丼井好她們他天氣很是的不了我")

// journalStore 合成一份每類學習資料都有的個人資料；n 是詞對數，其餘各類照比例。
func journalStore(n int, seed uint64) (*Model, []string) {
	r := rand.New(rand.NewPCG(seed, 1))
	word := func() string {
		k := 1 + r.IntN(3)
		var b strings.Builder
		for range k {
			b.WriteRune(journalRunes[r.IntN(len(journalRunes))])
		}
		return b.String()
	}
	lex := lexicon.NewMem()
	lex.Add(lexicon.Entry{Surface: "他們", PRW: 1})
	m := NewModel(lex, lm.NewMem())
	s := m.Store
	for range n {
		l, x := word(), word()
		c := r.Float64()*3 + 0.01
		s.Bi[pair(l, x)] += c
		s.Ctx[l] += c
		s.Uni[x] += c
		s.Uses[x] += c
		s.Total += c
	}
	for range n / 3 {
		l, x := word(), word()
		ev := UsageEvidence{Short: r.Float64(), Long: r.Float64(), Updated: 100, Day: 1, DayWeight: 0.5}
		s.EvidenceByContext[pair(l, x)] = ev
		s.EvidenceByWord[x] = ev
		s.EvidenceByWord[word()] = ev // 只有全域、沒有上下文的
	}
	for i := range n / 20 {
		w := word()
		s.Corrections[fmt.Sprint("k", i)] = Correction{Reading: "ㄕˋ", Word: w, LeftWord: word(), RightWord: word(), Serial: uint64(i)}
		s.Readings[w+"\tㄕˋ ㄐㄧㄝˋ"] = true
		s.Rejects[w+"\tㄕˋ"] = 1 + i%3
		s.Picked[fmt.Sprint("syl", i%50)] = append(s.Picked[fmt.Sprint("syl", i%50)], w)
		if i%4 == 0 {
			ph := word() + word()
			if _, ok := s.Phrases[ph]; !ok {
				s.Phrases[ph] = "ㄕˋ"
				s.PhraseOrder = append(s.PhraseOrder, ph)
				s.Uni[ph] += 2
				s.Total += 2
			}
			s.PhraseSeen[ph] = 2
			s.PhraseSeen[word()+word()] = 1
		}
	}
	s.Pronouns["他"] = PronounHabit{Cues: map[string]PronounCount{"老師": {}}, Serial: 3}
	s.English["hello"] = EnglishEvidence{Text: "Hello", Confirmed: 2, Days: 1, Usage: 2}
	s.English["kubectl"] = EnglishEvidence{Text: "kubectl", Confirmed: 1, Days: 1, Usage: 1}
	s.Tombstones["很好"], s.Tombstones["Git"], s.Tombstones["git"] = true, true, true
	// 被刪的詞：一定有的幾個，加上隨機挑的
	ws := []string{"世界", "她", "Hello", "丼", "Git"}
	for len(ws) < 40 {
		if w := word(); !slices.Contains(ws, w) {
			ws = append(ws, w)
		}
	}
	return m, ws
}

func cloneModel(m *Model) *Model {
	c := *m
	c.Store = m.Store.Clone()
	c.diversityIndex = diversityIndex{}
	return &c
}

// storesClose 比較兩份個人資料；浮點數允許加減順序造成的誤差（map 的走訪順序本來就不固定）。
func storesClose(t *testing.T, what string, a, b *Store) {
	t.Helper()
	floats := func(name string, x, y map[string]float64) {
		if len(x) != len(y) {
			t.Fatalf("%s：%s 筆數 %d ≠ %d", what, name, len(x), len(y))
		}
		for k, v := range x {
			if w, ok := y[k]; !ok || math.Abs(v-w) > 1e-9*math.Max(1, math.Abs(v)) {
				t.Fatalf("%s：%s[%q] %v ≠ %v（%v）", what, name, k, v, w, ok)
			}
		}
	}
	floats("Bi", a.Bi, b.Bi)
	floats("Ctx", a.Ctx, b.Ctx)
	floats("Uni", a.Uni, b.Uni)
	floats("Uses", a.Uses, b.Uses)
	if math.Abs(a.Total-b.Total) > 1e-9*math.Max(1, math.Abs(a.Total)) {
		t.Fatalf("%s：Total %v ≠ %v", what, a.Total, b.Total)
	}
	x, y := *a, *b
	x.Bi, x.Ctx, x.Uni, x.Uses, x.Total = nil, nil, nil, nil, 0
	y.Bi, y.Ctx, y.Uni, y.Uses, y.Total = nil, nil, nil, nil, 0
	if !reflect.DeepEqual(x, y) {
		for _, f := range []string{"Corrections", "EvidenceByContext", "EvidenceByWord", "Picked", "PhraseSeen", "Phrases", "PhraseOrder", "Tombstones", "Readings", "Rejects", "Pronouns", "English"} {
			if !reflect.DeepEqual(reflect.ValueOf(x).FieldByName(f).Interface(), reflect.ValueOf(y).FieldByName(f).Interface()) {
				t.Fatalf("%s：%s 不同", what, f)
			}
		}
		t.Fatalf("%s：個人資料不同", what)
	}
}

// undoClose 比較兩份復原複本記下的項目（浮點數同上）。
func undoClose(t *testing.T, what string, a, b *Undo) {
	t.Helper()
	if (a == nil) != (b == nil) {
		t.Fatalf("%s：複本 %v vs %v", what, a == nil, b == nil)
	}
	if a == nil {
		return
	}
	near := func(x, y float64) bool { return math.Abs(x-y) <= 1e-9*math.Max(1, math.Abs(x)) }
	floats := func(name string, x, y floatPatch) {
		if !slices.Equal(slices.Sorted(maps.Keys(x)), slices.Sorted(maps.Keys(y))) {
			t.Fatalf("%s：%s 記下的項目不同：%d vs %d", what, name, len(x), len(y))
		}
		for k, c := range x {
			d := y[k]
			if c.had != d.had || c.has != d.has || !near(c.before, d.before) || !near(c.after, d.after) {
				t.Fatalf("%s：%s[%q] %+v vs %+v", what, name, k, c, d)
			}
		}
	}
	floats("bi", a.bi, b.bi)
	floats("ctx", a.ctx, b.ctx)
	floats("uni", a.uni, b.uni)
	floats("uses", a.uses, b.uses)
	if !near(a.total.before, b.total.before) || !near(a.total.after, b.total.after) {
		t.Fatalf("%s：total %+v vs %+v", what, a.total, b.total)
	}
	for _, p := range []struct {
		name string
		x, y any
	}{
		{"corrections", a.corrections, b.corrections}, {"byContext", a.byContext, b.byContext}, {"byWord", a.byWord, b.byWord},
		{"picked", a.picked, b.picked}, {"phraseSeen", a.phraseSeen, b.phraseSeen}, {"phrases", a.phrases, b.phrases},
		{"tombstones", a.tombstones, b.tombstones}, {"readings", a.readings, b.readings}, {"rejects", a.rejects, b.rejects},
		{"pronouns", a.pronouns, b.pronouns}, {"english", a.english, b.english},
		{"order", a.order, b.order}, {"orderAfter", a.orderAfter, b.orderAfter},
	} {
		if !reflect.DeepEqual(p.x, p.y) {
			t.Fatalf("%s：%s 記下的項目不同\n%v\n%v", what, p.name, p.x, p.y)
		}
	}
}

func journalOps(ws []string) []struct {
	name string
	f    func(m *Model)
} {
	return []struct {
		name string
		f    func(m *Model)
	}{
		{"unlearn1", func(m *Model) { m.Unlearn(ws[0]) }},
		{"forget1", func(m *Model) { m.Forget(ws[0]) }},
		{"unlearnAll3", func(m *Model) { m.UnlearnAll(ws[:3]) }},
		{"forgetAll", func(m *Model) { m.ForgetAll(ws) }},
		{"unlearnAll", func(m *Model) { m.UnlearnAll(ws) }},
		{"relearn", func(m *Model) { m.Relearn("Git"); m.Relearn("很好"); m.Relearn("不在") }},
		{"relearnAll", func(m *Model) { m.RelearnAll([]string{"Git", "很好", "不在"}) }},
		{"noop", func(m *Model) { m.Relearn("不在") }},
	}
}

// 多個詞一起移除／不再學，跟逐一做的結果相同（含左詞、右詞、片語、手選單字、更正左右鄰、英文、代名詞）。
func TestUnlearnAllMatchesOneByOne(t *testing.T) {
	for seed := range uint64(5) {
		base, ws := journalStore(3000, seed)
		for _, n := range []int{1, 3, containsAnyLoop, containsAnyLoop + 1, len(ws)} {
			for _, forget := range []bool{false, true} {
				a, b := cloneModel(base), cloneModel(base)
				if forget {
					a.ForgetAll(ws[:n])
					for _, w := range ws[:n] {
						b.Forget(w)
					}
				} else {
					a.UnlearnAll(ws[:n])
					for _, w := range ws[:n] {
						b.Unlearn(w)
					}
				}
				storesClose(t, fmt.Sprintf("seed %d n=%d forget=%v", seed, n, forget), a.Store, b.Store)
				if reflect.DeepEqual(a.Store.Bi, base.Store.Bi) {
					t.Fatalf("seed %d n=%d：合成資料沒有被刪到的詞對", seed, n)
				}
			}
		}
	}
}

// 多個詞一起恢復學習，跟逐一做的結果相同（英文詞各種大小寫一起解除）。
func TestRelearnAllMatchesOneByOne(t *testing.T) {
	for seed := range uint64(5) {
		base, ws := journalStore(3000, seed)
		base.ForgetAll(ws)
		ws = append(ws, "Git", "GIT", "不在")
		for _, n := range []int{1, 2, len(ws)} {
			a, b := cloneModel(base), cloneModel(base)
			a.RelearnAll(ws[:n])
			for _, w := range ws[:n] {
				b.Relearn(w)
			}
			storesClose(t, fmt.Sprintf("seed %d n=%d", seed, n), a.Store, b.Store)
		}
	}
}

// Undoable 只記改到的項目，記下的內容跟整份比對的做法相同；復原後跟操作前完全一樣。
func TestUndoableJournalMatchesFullDiff(t *testing.T) {
	for seed := range uint64(5) {
		base, ws := journalStore(3000, seed)
		for _, op := range journalOps(ws) {
			what := fmt.Sprintf("seed %d %s", seed, op.name)
			a, b := cloneModel(base), cloneModel(base)
			ua := a.Undoable(func() { op.f(a) })
			ub := undoableByDiff(b, func() { op.f(b) })
			storesClose(t, what+" 操作後", a.Store, b.Store)
			undoClose(t, what, ua, ub)
			if a.undo != nil {
				t.Fatalf("%s：Undoable 結束後還在記錄", what)
			}
			if ua == nil {
				continue
			}
			if !a.Restore(ua) {
				t.Fatalf("%s：不能復原", what)
			}
			if !reflect.DeepEqual(a.Store, base.Store) {
				storesClose(t, what+" 復原後", a.Store, base.Store)
				t.Fatalf("%s：復原後不完全相同", what)
			}
		}
	}
}

// 不在 Undoable 裡的移除不記錄，也不會留下記錄的複本。
func TestUnlearnOutsideUndoableRecordsNothing(t *testing.T) {
	m, ws := journalStore(500, 1)
	m.ForgetAll(ws)
	if m.undo != nil || !reflect.DeepEqual(noUndo, Undo{}) {
		t.Fatalf("不在 Undoable 裡卻記錄了：%v", noUndo)
	}
}

func TestForgottenCount(t *testing.T) {
	m, ws := journalStore(500, 2)
	m.ForgetAll(ws)
	m.Forget("Kubectl")
	m.Store.Tombstones["關掉的"] = false
	if got, want := m.ForgottenCount(), len(m.Forgotten()); got != want || want == 0 {
		t.Fatalf("ForgottenCount %d，Forgotten %d", got, want)
	}
}

// containsAny 不論詞多詞少，都等於逐一 strings.Contains（含多位元組字、基本平面以外的字、英文、空字串、
// 不合法的 UTF-8）。
func TestContainsAny(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	alphabet := []string{"世", "界", "觀", "親", "子", "丼", "a", "b", "\t", "c", "她", "𠮷", "😀", "\xe4", "\xb8"}
	str := func(max int) string {
		var b strings.Builder
		for range r.IntN(max + 1) {
			b.WriteString(alphabet[r.IntN(len(alphabet))])
		}
		return b.String()
	}
	for range 300 {
		var ws []string
		for range 1 + r.IntN(20) {
			ws = append(ws, str(3))
		}
		has := containsAny(ws)
		for range 50 {
			s := str(12)
			want := slices.ContainsFunc(ws, func(w string) bool { return strings.Contains(s, w) })
			if has(s) != want {
				t.Fatalf("%q in %q：%v，應為 %v", ws, s, has(s), want)
			}
		}
	}
}

func BenchmarkContainsAny(b *testing.B) {
	keys := slices.Collect(maps.Keys(capStore().Store.Bi))
	words := slices.Sorted(maps.Keys(capStore().Store.Uni))
	for _, n := range []int{1, 4, 8, 9, 16, 100} {
		var ws []string
		for i := range n {
			ws = append(ws, words[i*len(words)/n])
		}
		has := containsAny(ws)
		b.Run(fmt.Sprint("words=", n), func(b *testing.B) {
			for b.Loop() {
				for _, k := range keys[:10000] {
					has(k)
				}
			}
		})
	}
}

// 個人資料到上限（詞對 20 萬筆）時，移除與不再學在引擎鎖裡的時間（#182）。before/ 是以前的做法。
// 2026-10-22 在 Ryzen 9 5950X 以 #129 模擬到上限的個人檔（Bi 200,000、EvidenceByContext 53,995、Uni 29,998）實測，
// 不再學 1／10／100／1,000 個詞：以前 32 ms／85 ms／0.6 s／5.9 s，現在 9／15／15／28 ms。
func BenchmarkUnlearnUndoableAtCap(b *testing.B) {
	base := capStore()
	words := slices.Sorted(maps.Keys(base.Store.Uni))
	for _, n := range []int{1, 10, 100} {
		ws := words[:0:0]
		for i := range n {
			ws = append(ws, words[i*len(words)/n])
		}
		b.Run(fmt.Sprint("words=", n), func(b *testing.B) {
			for b.Loop() {
				b.StopTimer()
				m := cloneModel(base)
				runtime.GC() // 複製產生的垃圾不算進操作時間
				b.StartTimer()
				if m.Undoable(func() { m.ForgetAll(ws) }) == nil {
					b.Fatal("沒有改到")
				}
			}
		})
		// 對照：以前的做法（整份複製比對、逐詞掃描）。
		b.Run(fmt.Sprint("before/words=", n), func(b *testing.B) {
			for b.Loop() {
				b.StopTimer()
				m := cloneModel(base)
				runtime.GC() // 複製產生的垃圾不算進操作時間
				b.StartTimer()
				undoableByDiff(m, func() {
					for _, w := range ws {
						m.Forget(w)
					}
				})
			}
		})
	}
}

// capStore 合成到清理上限的個人資料：詞對 20 萬、證據 6 萬、詞 3 萬（DefaultPrunePolicy）。
func capStore() *Model {
	p := DefaultPrunePolicy()
	r := rand.New(rand.NewPCG(7, 7))
	runes := []rune("的一是不了人我在有他這中大來上國個到說們為子和你地出道也時年得就那要下以生會自著去之過家學對可她裡後小麼心多天而能好都然沒日於起還發成事只作當想看文無開手十用主行方又如前所本見經頭面公同三已老從動兩長知民樣現分將外但身些與高意進把法此實回二理美點月明其種聲全工己話兒者向情部正名定女問力機給等幾很業最間新什打便位因重被走電四第門相次東政海口使教西再平真聽世氣信北少關並內加化由卻代軍產入先山五太水萬市眼體別處總才場師書比住員九笑性通目華報立馬命張活難神數件安表原車白應路期叫死常提感金何更反合放做系計或司利受光王果親界及今京務制解各任至清物台象記邊共風戰干接它許八特覺望直服毛林題建南度統色字請交愛讓認算論百吃義科怎元社術結六功指思非流每青管夫連遠資隊跟帶花快條院變聯言權往展該領傳近留紅治決周保達辦運武半候七必城父強步完革深區即求品士轉量空甚眾技輕程告江語英基")
	word := func() string {
		k := 1 + r.IntN(3)
		var b strings.Builder
		for range k {
			b.WriteRune(runes[r.IntN(len(runes))])
		}
		return b.String()
	}
	m := NewModel(lexicon.NewMem(), lm.NewMem())
	s := m.Store
	for len(s.Uni) < p.MaxWord {
		w := word()
		s.Uni[w] += 1
		s.Uses[w] += 1
		s.Total++
		s.EvidenceByWord[w] = UsageEvidence{Short: 1, Long: 1}
	}
	words := slices.Collect(maps.Keys(s.Uni))
	for len(s.Bi) < p.MaxPairs {
		l, x := words[r.IntN(len(words))], words[r.IntN(len(words))]
		s.Bi[pair(l, x)] += 1
		s.Ctx[l] += 1
	}
	for len(s.EvidenceByContext) < p.MaxEvidence {
		l, x := words[r.IntN(len(words))], words[r.IntN(len(words))]
		s.EvidenceByContext[pair(l, x)] = UsageEvidence{Short: 1, Long: 1}
	}
	for i := range 2048 {
		w := words[r.IntN(len(words))]
		s.Corrections[fmt.Sprint(i)] = Correction{Word: w, LeftWord: words[r.IntN(len(words))]}
		s.Readings[w+"\tㄕˋ"] = true
	}
	return m
}
