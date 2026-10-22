package personal

import (
	"sort"
	"unicode/utf8"
)

// 人稱代名詞的性別寫法（他／她、你／妳）不由語境決定。「他」「你」本身不分性別，
// 寫成中性字最多是不夠精確；系統依語境猜成「她」「妳」則是替使用者決定了性別，
// 猜錯就是冒犯。因此語言模型與神經選字都不能自動切換，預設一律是中性字，
// 只有使用者自己的明確選取累積成習慣後才改用「她」「妳」。
//
// 習慣分兩層：
//   - 鄰近詞：明確選過「她」時，同一次組字裡附近的詞（例：男朋友）記為線索；
//     同一線索旁明確選了兩次以上、且多數時候是「她」，下次出現在它附近就預設「她」。
//     只記左右一個音節的語境更正太寬（「她的男朋友」會讓所有「ㄊㄚ ㄉㄜ˙」都變成「她」），
//     這裡改以詞為線索並要求重複。
//   - 整體：明確選「她」五次以上、且全部用法八成以上是「她」，才在沒有線索時預設「她」
//     （例：平常寫給女性朋友、一律用「妳」的使用者）。
//
// 安全方向不對稱：線索旁最近一次明確選取是中性字，就不再因這個線索預設變體。

// pronounPairs 由代名詞字對應到中性字及是否為變體。
var pronounPairs = map[rune]struct {
	neutral, variant rune
	isVariant        bool
}{
	'他': {'他', '她', false}, '她': {'他', '她', true},
	'你': {'你', '妳', false}, '妳': {'你', '妳', true},
}

const (
	pronounPassive       = 0.25 // 被動接受顯示結果；明確選取為 1
	pronounWindow        = 6    // 線索：代名詞前後幾個音節內的詞
	pronounCueRepeats    = 2    // 線索旁至少明確選了幾次變體
	pronounCueShare      = 2.0 / 3
	pronounGlobalRepeats = 5
	pronounGlobalShare   = 0.8
	pronounDecayAt       = 40  // 加權次數超過此值即全部減半，讓近期習慣為準
	maxPronounCues       = 256 // 每組代名詞最多保留的線索
)

// PronounCount 是一組代名詞在某條件下的用法次數。
type PronounCount struct {
	Variant, Neutral             float64 // 加權次數：明確選取 1、被動接受 pronounPassive
	ManualVariant, ManualNeutral float64 // 只計明確選取（不含 Tab 補全）
	LastManual                   int     // 最近一次明確選取：1 變體、-1 中性、0 尚無
	Updated                      uint64  // 最近一次更新的序號（PronounHabit.Serial），供逐出最久未用的線索
}

func (c *PronounCount) add(variant, manual bool) {
	w := pronounPassive
	if manual {
		w = 1
	}
	switch {
	case variant:
		c.Variant += w
		if manual {
			c.ManualVariant++
			c.LastManual = 1
		}
	default:
		c.Neutral += w
		if manual {
			c.ManualNeutral++
			c.LastManual = -1
		}
	}
	if c.Variant+c.Neutral > pronounDecayAt {
		c.Variant, c.Neutral, c.ManualVariant, c.ManualNeutral = c.Variant/2, c.Neutral/2, c.ManualVariant/2, c.ManualNeutral/2
	}
}

func (c PronounCount) share() float64 {
	if c.Variant+c.Neutral == 0 {
		return 0
	}
	return c.Variant / (c.Variant + c.Neutral)
}

// PronounHabit 是一組代名詞（以中性字為鍵）的使用者習慣。
type PronounHabit struct {
	All    PronounCount
	Cues   map[string]PronounCount // 鄰近詞（變體字一律寫成中性字）→ 在它附近的用法
	Serial uint64
}

// PronounSite 是路徑中一個人稱代名詞字。
type PronounSite struct {
	Seg     int // 路徑索引
	Offset  int // 字在詞內的位置
	Neutral rune
	Variant bool     // 目前寫成變體（她／妳）
	Cues    []string // 前後 pronounWindow 個音節內的其他詞
}

// SwapPronoun 把 word 第 offset 個字換成同組的另一種寫法。
func SwapPronoun(word string, offset int) string {
	rs := []rune(word)
	p := pronounPairs[rs[offset]]
	if p.isVariant {
		rs[offset] = p.neutral
	} else {
		rs[offset] = p.variant
	}
	return string(rs)
}

// NeutralPronouns 把字串中的「她」「妳」寫成中性字，供比較兩段文字是否只差在代名詞性別。
func NeutralPronouns(s string) string {
	rs := []rune(s)
	for i, r := range rs {
		if p, ok := pronounPairs[r]; ok {
			rs[i] = p.neutral
		}
	}
	return string(rs)
}

// PronounSites 找出路徑中的人稱代名詞字。單字一律算；多字詞中只有換成另一種寫法後
// 仍是詞庫裡的詞才算（他們／她們）。「其他」「吉他」的「他」、「崇她社」「她經濟」的「她」
// 是固定詞的一部分，不能換。
func (m *Model) PronounSites(path []Seg) []PronounSite {
	var sites []PronounSite
	for k, g := range path {
		if g.Partial {
			continue
		}
		rs := []rune(g.Word)
		for i, r := range rs {
			p, ok := pronounPairs[r]
			if !ok || len(rs) > 1 && !m.Lex.Has(SwapPronoun(g.Word, i)) {
				continue
			}
			pos := g.Start + i
			site := PronounSite{Seg: k, Offset: i, Neutral: p.neutral, Variant: p.isVariant}
			seen := map[string]bool{}
			for j, x := range path {
				cue := NeutralPronouns(x.Word)
				if j == k || x.Partial || utf8.RuneCountInString(cue) < 2 || seen[cue] ||
					x.End <= pos-pronounWindow || x.Start > pos+pronounWindow {
					continue
				}
				seen[cue] = true
				site.Cues = append(site.Cues, cue)
			}
			sites = append(sites, site)
		}
	}
	return sites
}

// PronounVariant 回報依使用者習慣，在這些鄰近詞旁是否該寫成變體（她／妳）。沒有習慣時為否。
func (m *Model) PronounVariant(neutral rune, cues []string) bool {
	h, ok := m.Store.Pronouns[string(neutral)]
	if !ok {
		return false
	}
	variant := h.All.ManualVariant >= pronounGlobalRepeats && h.All.share() >= pronounGlobalShare
	for _, cue := range cues {
		c, ok := h.Cues[cue]
		if !ok {
			continue
		}
		if c.LastManual < 0 {
			return false // 使用者最近在這個詞旁明確選了中性字
		}
		if c.ManualVariant >= pronounCueRepeats && c.share() >= pronounCueShare {
			variant = true
		}
	}
	return variant
}

// learnPronouns 記錄每個代名詞最後寫成哪一種。線索只由明確選取建立，被動接受只更新既有線索，
// 避免平常打字讓個人資料無限增長。明確選中性字只在整體習慣是變體時建立線索（推翻了變體，
// 需要否決）；否則中性字本來就是預設，為它建線索只會占滿容量、擠掉要累積的變體線索。
func (m *Model) learnPronouns(c Commit) {
	for _, site := range m.PronounSites(c.Path) {
		g := c.Path[site.Seg]
		manual := g.Manual && !g.Completion && !g.Suggested
		key := string(site.Neutral)
		h := m.Store.Pronouns[key]
		if h.Cues == nil {
			h.Cues = map[string]PronounCount{}
		}
		create := manual && (site.Variant || m.PronounVariant(site.Neutral, nil))
		h.Serial++
		h.All.add(site.Variant, manual)
		for _, cue := range site.Cues {
			if x, ok := h.Cues[cue]; ok || create {
				x.add(site.Variant, manual)
				x.Updated = h.Serial
				h.Cues[cue] = x
			}
		}
		if len(h.Cues) > maxPronounCues {
			keys := make([]string, 0, len(h.Cues))
			for k := range h.Cues {
				keys = append(keys, k)
			}
			// 逐出最久未更新的線索：剛建立的線索才累積得到門檻，否決線索也隨時間老化。
			sort.Slice(keys, func(i, j int) bool {
				a, b := h.Cues[keys[i]], h.Cues[keys[j]]
				if a.Updated != b.Updated {
					return a.Updated < b.Updated
				}
				return keys[i] < keys[j]
			})
			for _, k := range keys[:len(keys)-maxPronounCues] {
				delete(h.Cues, k)
			}
		}
		m.Store.Pronouns[key] = h
	}
}

// forgetPronouns 撤銷「她」「妳」或含它們的代名詞詞（她們）時，一併清除該組代名詞的習慣；
// 「崇她社」這類固定詞不算（條件同 PronounSites）。
func (m *Model) forgetPronouns(w string) {
	rs := []rune(w)
	for i, r := range rs {
		if p, ok := pronounPairs[r]; ok && p.isVariant && (len(rs) == 1 || m.Lex.Has(SwapPronoun(w, i))) {
			note(m.recording().pronouns, m.Store.Pronouns, string(p.neutral))
			delete(m.Store.Pronouns, string(p.neutral))
		}
	}
}
