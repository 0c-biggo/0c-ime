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
	s := m.Store
	covered := map[string]bool{}
	for t := range s.Tombstones {
		if EnglishToken(t) && strings.ToLower(t) != t {
			covered[strings.ToLower(t)] = true
		}
	}
	out := []string{}
	for t, on := range s.Tombstones {
		if on && !covered[t] {
			out = append(out, t)
		}
	}
	slices.Sort(out)
	return out
}

// Relearn 是「恢復學習」：刪掉 w 的 tombstone（英文詞連同各種大小寫的正規化那筆），之後照常學回來。
// 沒有可以刪的回傳 false。
func (m *Model) Relearn(w string) bool {
	s := m.Store
	found := false
	if _, ok := s.Tombstones[w]; ok {
		delete(s.Tombstones, w)
		found = true
	}
	if EnglishToken(w) {
		key := strings.ToLower(w)
		for t := range s.Tombstones {
			if EnglishToken(t) && strings.ToLower(t) == key {
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

// Undoable 執行 f（Forget、Unlearn、Relearn 等），回傳復原用的複本；f 沒有改到學習資料時回傳 nil。
func (m *Model) Undoable(f func()) *Undo {
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
