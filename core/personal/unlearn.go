package personal

import (
	"reflect"
	"slices"
	"strings"
)

// 「靈犀學到的」頁的移除、不再學、恢復學習與復原（#182）。Unlearn／Forget 在 personal.go。

// Forgotten 列出不再學的詞（tombstone），依字面排序。英文詞的 Forget 會同時留下原寫法與正規化（小寫）兩筆，
// 只列原寫法。
func (m *Model) Forgotten() []string {
	covered := m.forgottenCovered()
	out := []string{}
	for t, on := range m.Store.Tombstones {
		if on && !covered[t] {
			out = append(out, t)
		}
	}
	slices.Sort(out)
	return out
}

// ForgottenCount 是 len(Forgotten())，不建清單也不排序；每次「靈犀學到的」頁的操作都要回報。
func (m *Model) ForgottenCount() int {
	covered := m.forgottenCovered()
	n := 0
	for t, on := range m.Store.Tombstones {
		if on && !covered[t] {
			n++
		}
	}
	return n
}

// forgottenCovered：英文詞有原寫法時，正規化（小寫）那筆不另外列。
func (m *Model) forgottenCovered() map[string]bool {
	var covered map[string]bool
	for t := range m.Store.Tombstones {
		if EnglishToken(t) && strings.ToLower(t) != t {
			if covered == nil {
				covered = map[string]bool{}
			}
			covered[strings.ToLower(t)] = true
		}
	}
	return covered
}

// Relearn 是「恢復學習」：刪掉 w 的 tombstone（英文詞連同各種大小寫的正規化那筆），之後照常學回來。
// 沒有可以刪的回傳 false。
func (m *Model) Relearn(w string) bool {
	s := m.Store
	r := m.recording()
	found := false
	if _, ok := s.Tombstones[w]; ok {
		note(r.tombstones, s.Tombstones, w)
		delete(s.Tombstones, w)
		found = true
	}
	if EnglishToken(w) {
		key := strings.ToLower(w)
		for t := range s.Tombstones {
			if EnglishToken(t) && strings.ToLower(t) == key {
				note(r.tombstones, s.Tombstones, t)
				delete(s.Tombstones, t)
				found = true
			}
		}
	}
	if found {
		m.Gen++
	}
	return found
}

// Undo 是一次學習資料操作之前的複本，只存被改到的項目。只能用在同一份個人資料、只能復原一次。
// 它只在記憶體裡（引擎重新啟動就沒了），不寫進 user.json：使用者要求刪除的內容不能因為復原而留在磁碟上。
type Undo struct {
	store *Store
	used  bool

	bi, ctx, uni, uses floatPatch
	total              floatChange

	corrections patch[Correction]
	byContext   patch[UsageEvidence]
	byWord      patch[UsageEvidence]
	picked      patch[[]string]
	phraseSeen  patch[int]
	phrases     patch[string]
	tombstones  patch[bool]
	readings    patch[bool]
	rejects     patch[int]
	pronouns    patch[PronounHabit]
	english     patch[EnglishEvidence]

	order, orderAfter []string
}

// Undoable 執行 f，回傳復原用的複本；f 沒有改到學習資料時回傳 nil。
//
// f 只能呼叫 Forget、ForgetAll、Unlearn、UnlearnAll、Relearn：複本不是整份個人資料，而是這些操作改每一項之前
// 用 note 記下的原值（m.undo 不是 nil 的期間）。以前整份複製再逐類比對，個人資料到上限時一次要 20～30 ms，
// 都在引擎鎖裡；現在只跟改到的項目數有關。其他會改學習資料的方法沒有記錄，不能放進 f。
func (m *Model) Undoable(f func()) *Undo {
	s := m.Store
	u := &Undo{
		store: s,
		bi:    floatPatch{}, ctx: floatPatch{}, uni: floatPatch{}, uses: floatPatch{},
		total:       floatChange{before: s.Total, had: true, has: true},
		corrections: patch[Correction]{}, byContext: patch[UsageEvidence]{}, byWord: patch[UsageEvidence]{},
		picked: patch[[]string]{}, phraseSeen: patch[int]{}, phrases: patch[string]{}, tombstones: patch[bool]{},
		readings: patch[bool]{}, rejects: patch[int]{}, pronouns: patch[PronounHabit]{}, english: patch[EnglishEvidence]{},
		order: slices.Clone(s.PhraseOrder),
	}
	m.undo = u
	defer func() { m.undo = nil }()
	f()
	if m.Store != s { // f 換了一份個人資料：記下的改動不屬於現在這份
		return nil
	}
	u.bi.settle(s.Bi)
	u.ctx.settle(s.Ctx)
	u.uni.settle(s.Uni)
	u.uses.settle(s.Uses)
	u.total.after = s.Total
	u.corrections.settle(s.Corrections, same[Correction])
	u.byContext.settle(s.EvidenceByContext, same[UsageEvidence])
	u.byWord.settle(s.EvidenceByWord, same[UsageEvidence])
	u.picked.settle(s.Picked, slices.Equal[[]string])
	u.phraseSeen.settle(s.PhraseSeen, same[int])
	u.phrases.settle(s.Phrases, same[string])
	u.tombstones.settle(s.Tombstones, same[bool])
	u.readings.settle(s.Readings, same[bool])
	u.rejects.settle(s.Rejects, same[int])
	u.pronouns.settle(s.Pronouns, func(a, b PronounHabit) bool { return reflect.DeepEqual(a, b) })
	u.english.settle(s.English, same[EnglishEvidence])
	u.orderAfter = slices.Clone(s.PhraseOrder)
	if u.empty() && u.total.before == s.Total && slices.Equal(u.order, s.PhraseOrder) {
		return nil
	}
	return u
}

// recording 回傳正在記錄的複本；沒有在 Undoable 裡時是 noUndo，它的 patch 都是 nil，note 什麼也不做。
func (m *Model) recording() *Undo {
	if m.undo != nil {
		return m.undo
	}
	return &noUndo
}

var noUndo Undo // 只讀：欄位維持零值

// note 在 m[k] 被改之前呼叫，記下第一次看到的原值；p 是 nil（沒有在記錄）時不做事。
func note[V any](p patch[V], m map[string]V, k string) {
	if p == nil {
		return
	}
	if _, seen := p[k]; !seen {
		v, ok := m[k]
		p[k] = change[V]{before: v, had: ok}
	}
}

func noteFloat(p floatPatch, m map[string]float64, k string) {
	if p == nil {
		return
	}
	if _, seen := p[k]; !seen {
		v, ok := m[k]
		p[k] = floatChange{before: v, had: ok}
	}
}

// settle 在 f 結束後補上每一項現在的值；結果跟操作前一樣的項目拿掉。
func (p patch[V]) settle(m map[string]V, eq func(a, b V) bool) {
	for k, c := range p {
		a, ok := m[k]
		if ok == c.had && (!ok || eq(a, c.before)) {
			delete(p, k)
			continue
		}
		c.after, c.has = a, ok
		p[k] = c
	}
}

func (p floatPatch) settle(m map[string]float64) {
	for k, c := range p {
		a, ok := m[k]
		if ok == c.had && (!ok || a == c.before) {
			delete(p, k)
			continue
		}
		c.after, c.has = a, ok
		p[k] = c
	}
}

func (u *Undo) empty() bool {
	return len(u.bi)+len(u.ctx)+len(u.uni)+len(u.uses)+len(u.corrections)+len(u.byContext)+len(u.byWord)+len(u.picked)+
		len(u.phraseSeen)+len(u.phrases)+len(u.tombstones)+len(u.readings)+len(u.rejects)+len(u.pronouns)+len(u.english) == 0
}

// Restore 把 u 記下的項目放回去。操作之後沒再變過的項目回到操作前的值；之後又學到的（例如被移除的詞又打了幾次）
// 保留新的，次數類加回被清掉的部分。個人資料已經換過一份、或已經復原過時回傳 false。
func (m *Model) Restore(u *Undo) bool {
	if u == nil || u.used || u.store != m.Store {
		return false
	}
	u.used = true
	s := m.Store
	u.bi.apply(s.Bi)
	u.ctx.apply(s.Ctx)
	u.uni.apply(s.Uni)
	u.uses.apply(s.Uses)
	s.Total = u.total.restore(s.Total, true)
	u.corrections.apply(s.Corrections, same[Correction], nil)
	u.byContext.apply(s.EvidenceByContext, same[UsageEvidence], nil)
	u.byWord.apply(s.EvidenceByWord, same[UsageEvidence], nil)
	u.picked.apply(s.Picked, slices.Equal[[]string], func(before, cur []string) []string {
		out := slices.Clone(before)
		for _, x := range cur {
			if !slices.Contains(out, x) {
				out = append(out, x)
			}
		}
		return out
	})
	u.phraseSeen.apply(s.PhraseSeen, same[int], func(before, cur int) int { return before + cur })
	u.phrases.apply(s.Phrases, same[string], nil)
	u.tombstones.apply(s.Tombstones, same[bool], nil)
	u.readings.apply(s.Readings, same[bool], nil)
	u.rejects.apply(s.Rejects, same[int], nil)
	u.pronouns.apply(s.Pronouns, func(a, b PronounHabit) bool { return reflect.DeepEqual(a, b) }, nil)
	u.english.apply(s.English, same[EnglishEvidence], nil)
	if slices.Equal(s.PhraseOrder, u.orderAfter) {
		s.PhraseOrder = slices.Clone(u.order)
	} else { // 操作之後又晉升了別的片語：操作前的順序在前，新的接在後面
		order := filter(slices.Clone(u.order), func(ph string) bool { _, ok := s.Phrases[ph]; return ok })
		for _, ph := range s.PhraseOrder {
			if !slices.Contains(order, ph) {
				order = append(order, ph)
			}
		}
		s.PhraseOrder = order
	}
	s.PhraseOrder = filter(s.PhraseOrder, func(ph string) bool { _, ok := s.Phrases[ph]; return ok })
	m.diversityIndex = diversityIndex{} // 計數直接改過：下次查詢整份重算
	m.Gen++
	return true
}

func same[V comparable](a, b V) bool { return a == b }

type change[V any] struct {
	before, after V
	had, has      bool // 操作前／後有沒有這一項
}

type patch[V any] map[string]change[V]

// apply：這一項在操作之後沒再變過就回到操作前；變過的（之後又學到）交給 merge，nil 表示留著新的。
// 操作刪掉、之後也沒再出現的項目一律放回。
func (p patch[V]) apply(m map[string]V, eq func(a, b V) bool, merge func(before, cur V) V) {
	for k, c := range p {
		cur, ok := m[k]
		switch {
		case ok == c.has && (!ok || eq(cur, c.after)):
			if c.had {
				m[k] = c.before
			} else {
				delete(m, k)
			}
		case !ok && c.had:
			m[k] = c.before
		case ok && c.had && merge != nil:
			m[k] = merge(c.before, cur)
		}
	}
}

type floatChange struct {
	before, after float64
	had, has      bool
}

// restore：沒再變過就回到操作前的值（不經浮點加減，完全相同）；變過的加回操作減掉的部分。
func (c floatChange) restore(cur float64, ok bool) float64 {
	if ok == c.has && cur == c.after {
		return c.before
	}
	return cur + c.before - c.after
}

type floatPatch map[string]floatChange

func (p floatPatch) apply(m map[string]float64) {
	for k, c := range p {
		cur, ok := m[k]
		v := c.restore(cur, ok)
		if !c.had && ok == c.has && cur == c.after || v <= 1e-12 {
			delete(m, k)
			continue
		}
		m[k] = v
	}
}
