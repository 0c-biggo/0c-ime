package session

import (
	"slices"
	"sync"

	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 觸控鍵盤的相鄰鍵容錯（手機）：外殼每次點按依觸控位置送來鄰鍵與成本（proto.Request.Near），
// 核心在完成的音節旁放入「其中一鍵換成鄰鍵」的讀音，由語言模型決定（觸控鍵盤常見的空間模型）；
// 不打聲調時，打的鍵接不上目前的音節、而最可能的鄰鍵接得上，就改用鄰鍵，不把音節拆開。
//
// 漏按一鍵（#335）：完成的音節旁另放「補上一個沒打到的注音」的讀音（ㄏㄚ → ㄏㄨㄚ、ㄠˇ → ㄏㄠˇ），成本是固定的
// SpatialOptions.Missed；不打聲調時 ㄒ＋ㄠ 這種接不上的組音，在組音時補上介音（ㄒㄧㄠ）。
// 多按一鍵、按錯順序不在這裡處理：聲調選填時多出來的鍵幾乎都自成一個音節（ㄗㄨㄨㄥ 是 ㄗㄨ＋ㄨㄥ、ㄠㄏ 是 ㄠ＋ㄏ），
// 要把兩個打出來的音節合成一個，見 merge.go 的合併弧（SpatialOptions.Swap 等）；在音節裡去掉一鍵的讀音在 APP 的
// 模擬只多救回 1%，卻讓沒打錯的句子被改壞（#335 的量測）。

// SpatialOptions 由宿主設定。Weight 是鄰鍵成本乘上的權重，0 關閉整個功能（桌面不送 Near，也一樣）。
// Penalty 是換成鄰鍵的固定成本，加在乘過權重的成本上：打到的鍵永遠不加成本，修正要明顯勝出才會取代它
// （文獻上避免把罕用詞改錯的做法，Ouyang 2017、Fowler 2015）。
// MaxCost 以上（乘過權重之前）的鄰鍵不用，免得音節格太大；0 表示 DefaultSpatialMaxCost。
//
// Missed 是漏按一鍵的讀音成本（自然對數單位，同鄰鍵成本與語言模型），0 不放這種讀音。漏掉的鍵沒有觸控位置，
// 成本只能是固定的，大約是 -ln(每鍵漏按的機率)，由 APP 的 TestTypoReplay 調。只在 Weight > 0 時有作用。
//
// Swap 是合併弧（merge.go，APP#25）：相鄰兩、三個打出來的音節，對調其中一組相鄰的鍵就合成一個音節（ㄠ＋ㄏ → ㄏㄠ、
// ㄏˇ＋ㄠ → ㄏㄠˇ），成本固定是 Swap；0 不放。Repeat 同 Swap，是「重複按了一鍵」：去掉連續兩個相同的鍵之一就合成
// 一個音節（ㄏ＋ㄏㄠ → ㄏㄠ、ㄗㄨ＋ㄨㄥ → ㄗㄨㄥ）。Rejoin 是「按到鄰鍵，切法跟著變了」：其中一鍵換成它的鄰鍵就合成
// 一個音節（ㄖ＋ㄧㄤ → ㄒㄧㄤ，想打「向」），成本是 Weight×鄰鍵成本＋Rejoin（取代 Penalty）。同 Missed，都只在 Weight > 0
// 時有作用。
type SpatialOptions struct {
	Weight  float64
	Penalty float64
	MaxCost float64
	Missed  float64
	Swap    float64
	Repeat  float64
	Rejoin  float64
}

// DefaultSpatialMaxCost：比打到的鍵多 8 個自然對數單位以上的鄰鍵，機率不到三千分之一，不值得解碼。
const DefaultSpatialMaxCost = 8.0

func (o SpatialOptions) on() bool { return o.Weight > 0 }

// merging 回報合併弧（merge.go）有沒有開。
func (o SpatialOptions) merging() bool { return o.on() && (o.Swap > 0 || o.Repeat > 0 || o.Rejoin > 0) }

func (o SpatialOptions) maxCost() float64 {
	if o.MaxCost > 0 {
		return o.MaxCost
	}
	return DefaultSpatialMaxCost
}

// spatialKey 是組音中一個注音的鄰鍵。
type spatialKey struct {
	sym  rune
	cost float64
}

func (s *Session) nearKeys(near []proto.NearKey) []spatialKey {
	if !s.Spatial.on() {
		return nil
	}
	var out []spatialKey
	for _, n := range near {
		r := []rune(n.Symbol)
		if len(r) != 1 || zhuyin.ClassOf(r[0]) == zhuyin.None || n.Cost < 0 || n.Cost > s.Spatial.maxCost() {
			continue
		}
		out = append(out, spatialKey{r[0], n.Cost})
	}
	slices.SortStableFunc(out, func(a, b spatialKey) int {
		switch {
		case a.cost < b.cost:
			return -1
		case a.cost > b.cost:
			return 1
		}
		return 0
	})
	return out
}

// spatialSubstitute 在打的鍵放不進組音時找最可能的修正：把打的鍵換成鄰鍵，或把組音裡前面某一鍵換成它的鄰鍵
// （ㄒ＋ㄚ 接不上，ㄒ 的鄰鍵 ㄏ 接得上：哈）。回傳要放進組音的鍵與它的鄰鍵；換的是前面的鍵時直接改掉組音。
// 沒有可行的修正時 ok 為 false，照原本的規則處理。
func (s *Session) spatialSubstitute(sym rune, near []spatialKey) (rune, []spatialKey, bool) {
	// 目前的組音自己就是完整音節時（ㄕ 是「使」），接不上的鍵是下一個音節的開頭（使用 ㄕ＋ㄩㄥ），不是按錯。
	alone := zhuyin.Syllable(s.comp.String()).Legal()
	misfit := zhuyin.ClassOf(sym) == zhuyin.Tone && s.comp.Empty() || s.toneless() && !alone && s.untonedBreak(sym) && !s.breakIsNew(sym)
	if !misfit {
		return sym, near, false
	}
	fits := func(r rune) bool {
		if zhuyin.ClassOf(r) == zhuyin.Tone {
			return !s.comp.Empty()
		}
		return !s.untonedBreak(r)
	}
	bestCost, bestSym := -1.0, rune(0)
	for _, n := range near {
		if fits(n.sym) {
			bestCost, bestSym = n.cost, n.sym
			break // 已依成本排序
		}
	}
	// 換掉組音裡前面的一鍵（同一類的鄰鍵），打的鍵照舊。
	var fixClass zhuyin.Class
	var fixSym rune
	if zhuyin.ClassOf(sym) != zhuyin.Tone {
		saved := s.comp
		for cl, slot := range s.compNear {
			if cl == zhuyin.Tone || saved.Slot(cl) != slot.sym {
				continue
			}
			for _, n := range slot.near {
				if zhuyin.ClassOf(n.sym) != cl || bestCost >= 0 && n.cost >= bestCost {
					continue
				}
				trial := saved.Clone()
				trial.Replace(cl, n.sym)
				s.comp = trial
				ok := fits(sym)
				s.comp = saved
				if ok {
					bestCost, bestSym, fixClass, fixSym = n.cost, 0, cl, n.sym
				}
			}
		}
	}
	if bestCost >= 0 && s.rejoinsLoneInitial(sym, bestCost) {
		return sym, near, false
	}
	// 漏按介音（#335）：ㄒ＋ㄣ 接不上，補上 ㄧ 就是 ㄒㄧㄣ。只在沒有接得上的鄰鍵時補：組音這一步只能挑一種，
	// 按錯鄰鍵比漏按常見得多，比成本挑補鍵會把 ㄉ＋ㄝ（的，ㄜ 按到 ㄝ）補成 ㄉㄧㄝ（爹）；APP 的模擬裡兩種挑法救回的
	// 漏按一樣多，比成本的少救回 2 句換鍵（#335 的量測）。
	if m, others := s.missedMedial(sym, fits); m != 0 && bestCost < 0 {
		s.comp.Add(m)
		s.noteSpatial(m, others) // 其他也接得上的介音（ㄒㄩㄣ）留給音節格
		return sym, near, true
	}
	switch {
	case bestCost < 0:
		return sym, near, false
	case bestSym == 0:
		old := s.compNear[fixClass]
		var rest []spatialKey
		for _, m := range old.near {
			if m.sym != fixSym {
				rest = append(rest, spatialKey{m.sym, max(0, m.cost-bestCost)})
			}
		}
		s.comp.Replace(fixClass, fixSym)
		s.compNear[fixClass] = spatialSlot{fixSym, rest}
		return sym, near, true
	}
	var rest []spatialKey
	for _, m := range near {
		if m.sym != bestSym {
			rest = append(rest, spatialKey{m.sym, max(0, m.cost-bestCost)})
		}
	}
	return bestSym, rest, true
}

// rejoinsLoneInitial 回報打的鍵接不上、組音只有一個要有韻母的聲母（ㄏ）時，要不要把換鍵留給合併弧（APP#36）。cost 是
// 最可能的鄰鍵的觸控成本。只打首音時這是正常的打法：「好意」是 ㄏ＋ㄧ，ㄏ 是簡拼、ㄧ 是下一個音節，不是按錯 ㄒ。組音這一步
// 換鍵沒有成本、也不經語言模型，換錯就整句改壞（3000 句的觸控雜訊裡，沒打錯卻被改壞的只打首音句子有 26 句都是這樣）。
// 開著換鍵的合併弧（Rejoin）時：
//   - 正在連續簡拼（前一個音節也是只打聲母的簡拼，同合併弧的 runAbbreviated）時一律不換；
//   - 其他時候（句首、完整音節之後）觸控成本超過 loneSubstituteMax 時不換。
//
// 不換的時候兩個音節照打的樣子放，合併弧在整句裡比較「簡拼＋下一個音節」與「換鍵合成一個音節」兩種切法（ㄒ＋ㄚ 換成
// 哈 照樣救得回來）。沒開 Rejoin 時照原本在組音時換。單獨就是音節的（ㄕ、ㄧ）在這之前就不算接不上（spatialSubstitute 的 alone）。
func (s *Session) rejoinsLoneInitial(sym rune, cost float64) bool {
	if !s.Spatial.merging() || s.Spatial.Rejoin <= 0 || !s.abbreviation() || zhuyin.ClassOf(sym) == zhuyin.Tone ||
		!zhuyin.Abbreviable(zhuyin.Syllable(s.comp.String())) {
		return false
	}
	return cost > loneSubstituteMax || s.cursor > 0 && !s.literals[s.cursor-1] && runAbbreviated(s.syls[s.cursor-1])
}

// loneSubstituteMax：組音只有一個要有韻母的聲母、又不是正在連續簡拼時，觸控明顯偏向鄰鍵（成本不超過這個值）就照舊在組音時
// 換，其他留給合併弧（rejoinsLoneInitial）。APP 的模擬（CV 3000 句、Gboard 觸控雜訊、APP 的設定 Rejoin 3；同合併弧在只打
// 首音的一串裡不換鍵）：
//
//	門檻         只打首音改壞/1864  打聲調救回/2220  不打聲調救回/1901
//	原本（不分）  26                1121            833
//	一律不換      0                 1098            787
//	2            1（會議場所）      1117            809
//
// 一律不換時，組音時換鍵不用付合併的成本、有些句子靠它救回，在整句比較時卻輸給簡拼的讀法（近年 的 ㄐ 按成 ㄍ，整句是
// 「過癮年」）；打錯的句子留著兩個音節，每鍵都要重新比較合併，本機不打聲調每鍵 P95 多 15%。門檻 2 救回只少 1–3%，
// 延遲跟原本差不多。
const loneSubstituteMax = 2.0

// missedMedial 是補進組音之後讓 sym 接得上的介音（Missed 開啟時；第一個合法的，其他放在 others、成本 0）。
// 只補介音：組音只有聲母、打的是韻母或介音時，中間漏掉的只能是介音。
func (s *Session) missedMedial(sym rune, fits func(rune) bool) (rune, []spatialKey) {
	if s.Spatial.Missed <= 0 || zhuyin.ClassOf(sym) == zhuyin.Tone || s.comp.Slot(zhuyin.Medial) != 0 || s.comp.Slot(zhuyin.Final) != 0 {
		return 0, nil
	}
	saved := s.comp
	defer func() { s.comp = saved }()
	var first rune
	var others []spatialKey
	for _, m := range "ㄧㄨㄩ" {
		if m == sym {
			continue
		}
		trial := saved.Clone()
		trial.Add(m)
		if !zhuyin.LegalPrefix(zhuyin.Syllable(trial.String())) {
			continue
		}
		s.comp = trial
		if fits(sym) {
			if first == 0 {
				first = m
			} else {
				others = append(others, spatialKey{m, 0})
			}
		}
	}
	return first, others
}

// breakIsNew：打的鍵本來就該開始新的音節（目前的音節已有同一類或更後面的注音，例如打完 ㄏㄠ 再打 ㄋ），
// 不是「接不上」。只有接在後面卻不成音節（ㄏ＋ㄦ）才算可能按錯。
func (s *Session) breakIsNew(sym rune) bool {
	cl := zhuyin.ClassOf(sym)
	return s.comp.Slot(cl) != 0 || lastClass(s.comp) >= cl
}

// noteSpatial 記下剛放進組音的注音的鄰鍵。
func (s *Session) noteSpatial(sym rune, near []spatialKey) {
	if s.compNear == nil {
		s.compNear = map[zhuyin.Class]spatialSlot{}
	}
	s.compNear[zhuyin.ClassOf(sym)] = spatialSlot{sym, near}
}

type spatialSlot struct {
	sym  rune
	near []spatialKey
}

// keyNears 是剛完成的音節每個注音（照「聲母 介音 韻母」）的鄰鍵，給合併弧換鍵用（merge.go）；沒開或都沒有鄰鍵時是 nil。
func (s *Session) keyNears(before zhuyin.Composer) []spatialSlot {
	if !s.Spatial.merging() || s.Spatial.Rejoin <= 0 || len(s.compNear) == 0 {
		return nil
	}
	var out []spatialSlot
	found := false
	for cl := zhuyin.Initial; cl <= zhuyin.Final; cl++ {
		r := before.Slot(cl)
		if r == 0 {
			continue
		}
		slot := spatialSlot{sym: r}
		if n, ok := s.compNear[cl]; ok && n.sym == r {
			slot.near = n.near
			found = found || len(n.near) > 0
		}
		out = append(out, slot)
	}
	if !found {
		return nil
	}
	return out
}

// spatialAlternatives 是剛完成的音節換掉一鍵（鄰鍵）的合法讀音與成本。before 是完成前的組音狀態（打聲調完成時不含聲調）。
// 簡拼音節本來就展開成所有以它開頭的音節，不另外放。補上一鍵的讀音只看打的音節，由 missedReadings 在展開音節格時放。
func (s *Session) spatialAlternatives(before zhuyin.Composer, done zhuyin.Syllable) []input.Hyp {
	if !s.Spatial.on() || len(s.compNear) == 0 || done.Abbreviated() {
		return nil
	}
	slots := map[zhuyin.Class]rune{}
	for cl := zhuyin.Initial; cl <= zhuyin.Final; cl++ {
		if r := before.Slot(cl); r != 0 {
			slots[cl] = r
		}
	}
	untoned := done.Untoned()
	tone := done.ToneOf()
	build := func(slots map[zhuyin.Class]rune, tone int) (zhuyin.Syllable, bool) {
		var b []rune
		for cl := zhuyin.Initial; cl <= zhuyin.Final; cl++ {
			if r := slots[cl]; r != 0 {
				b = append(b, r)
			}
		}
		base := zhuyin.Syllable(string(b))
		if len(b) == 0 || !base.Legal() {
			return "", false
		}
		if untoned {
			return zhuyin.Untone(base), true
		}
		return base.WithTone(tone), true
	}
	var out []input.Hyp
	// add 放入讀音 y，成本 cost；同一個讀音有幾種修正時取最便宜的。
	add := func(y zhuyin.Syllable, cost float64) {
		if y == done {
			return
		}
		for _, v := range y.Tones() {
			// 換掉一鍵的讀音是修正、不是打的音：不標 Untoned（同打了聲調的音節），組字與候選標 corrected，
			// Fits、decode.Reading 與近音清單都把它當成近音。沒打聲調時照樣展開成五個聲調（y.Tones）。
			if i := slices.IndexFunc(out, func(h input.Hyp) bool { return h.Syl == v }); i >= 0 {
				out[i].Cost = min(out[i].Cost, cost)
				continue
			}
			out = append(out, input.Hyp{Syl: v, Cost: cost, Corrected: true})
		}
	}
	near := func(y zhuyin.Syllable, cost float64) {
		if cost <= s.Spatial.maxCost() {
			add(y, s.Spatial.Weight*cost+s.Spatial.Penalty)
		}
	}
	for cl, slot := range s.compNear {
		if cl == zhuyin.Tone {
			if untoned {
				continue
			}
			for _, n := range slot.near {
				if t := toneNumber(n.sym); t > 0 {
					if y, ok := build(slots, t); ok {
						near(y, n.cost)
					}
				}
			}
			continue
		}
		if slots[cl] != slot.sym {
			continue // 這一格後來換過別的注音
		}
		for _, n := range slot.near {
			ncl := zhuyin.ClassOf(n.sym)
			if ncl == zhuyin.Tone || ncl != cl && slots[ncl] != 0 {
				continue
			}
			alt := map[zhuyin.Class]rune{}
			for k, v := range slots {
				alt[k] = v
			}
			delete(alt, cl)
			alt[ncl] = n.sym
			if y, ok := build(alt, tone); ok {
				near(y, n.cost)
			}
		}
	}
	return out
}

// maxMissedReadings 是每個音節補上一鍵的讀音上限：ㄧ、ㄚ 這種只有韻母的音節補上聲母就有上百個帶調讀音，
// 全部放進音節格會讓手機的每鍵與候選延遲加倍（#335）。依單音節的常用程度（syllablePrior）留前面的，沒有字的不放。
// APP 的模擬（每句漏按一鍵，300 句）裡 12 個在打聲調時救回的跟不設上限相同，不打聲調少 13 句；8 個兩種打法各少 5 句。
const maxMissedReadings = 12

// missedWindow：補上一鍵的讀音只放在最後這幾個音節；更前面的只留路徑已經走的（後面打的字已經判斷過它們）。
// 音節格不必隨句子變長而變寬，每鍵與候選的延遲不再跟句長成正比。APP 的模擬裡 5 個跟不設限的救回數相同，3 個少 5 句。
const missedWindow = 5

// missedFor 是打的音節 y 補上一鍵的讀音（Missed 開啟時）：音節表裡多一個注音的合法音節，打的音節有聲調時同聲調、
// 沒打聲調時五個聲調都算；依 syllablePrior 留下最常用的 maxMissedReadings 個（沒有字的不放），常用的在前。
// 只看打的音節，記在 session 裡（syllablePrior 也只看系統詞庫）。
func (s *Session) missedFor(y zhuyin.Syllable) []zhuyin.Syllable {
	if out, ok := s.missedMemo[y]; ok {
		return out
	}
	var ys []zhuyin.Syllable
	for _, b := range missedKey()[y.Base()] {
		if y.Untoned() {
			ys = append(ys, zhuyin.Untone(b).Tones()...)
		} else {
			ys = append(ys, b.WithTone(y.ToneOf()))
		}
	}
	var out []zhuyin.Syllable
	ps := map[zhuyin.Syllable]float64{}
	for _, t := range ys {
		if p := s.syllablePrior(t); p > 0 {
			out, ps[t] = append(out, t), p
		}
	}
	slices.SortStableFunc(out, func(a, b zhuyin.Syllable) int {
		switch {
		case ps[a] > ps[b]:
			return -1
		case ps[a] < ps[b]:
			return 1
		}
		return 0
	})
	out = out[:min(len(out), maxMissedReadings)]
	if s.missedMemo == nil {
		s.missedMemo = map[zhuyin.Syllable][]zhuyin.Syllable{}
	}
	s.missedMemo[y] = out
	return out
}

// missedAt 是位置 at 補上一鍵的讀音。簡拼音節本來就展開成所有以它開頭的音節；旁邊是簡拼音節時使用者正在簡拼
// （ㄋㄧ 的 ㄧ 不是漏了聲母），而且簡拼的寬列再加上補鍵的讀音會讓候選清單慢上十倍，都不放。九宮格與原樣文字的位置也不放。
func (s *Session) missedAt(syls []zhuyin.Syllable, at int) []zhuyin.Syllable {
	if !s.Spatial.on() || s.Spatial.Missed <= 0 || s.literals[at] || len(s.nine[at]) > 0 {
		return nil
	}
	if syls[at].Abbreviated() || at > 0 && syls[at-1].Abbreviated() || at+1 < len(syls) && syls[at+1].Abbreviated() {
		return nil
	}
	return s.missedFor(syls[at])
}

// missedKey 由音節表算出：打的注音（不帶聲調，不一定合法，例如 ㄒㄠ）補上一個注音之後的所有合法音節。
var missedKey = sync.OnceValue(func() map[zhuyin.Syllable][]zhuyin.Syllable {
	m := map[zhuyin.Syllable][]zhuyin.Syllable{}
	for _, b := range zhuyin.Inventory() {
		r := []rune(string(b))
		for i := range r {
			if less := zhuyin.Syllable(string(slices.Delete(slices.Clone(r), i, i+1))); less != "" {
				m[less] = append(m[less], b)
			}
		}
	}
	return m
})

func toneNumber(r rune) int {
	switch r {
	case zhuyin.Tone1:
		return 1
	case 'ˊ':
		return 2
	case 'ˇ':
		return 3
	case 'ˋ':
		return 4
	case '˙':
		return 5
	}
	return 0
}

// widenSpatial 在音節格放入各位置記下的鄰鍵讀音，以及補上一鍵的讀音（missedAt，成本 Missed）。
func (s *Session) widenSpatial(syls []zhuyin.Syllable, lat input.Lattice) input.Lattice {
	missed := s.Spatial.on() && s.Spatial.Missed > 0
	if len(s.spatial) == 0 && !missed || len(syls) != len(s.syls) {
		return lat
	}
	var taken map[int]zhuyin.Syllable
	if missed && len(syls) > missedWindow {
		taken = s.pathReadings()
	}
	for at := range lat {
		alts, extra := s.spatial[at], []zhuyin.Syllable(nil)
		if missed {
			extra = s.missedAt(syls, at)
			if taken != nil && at < len(syls)-missedWindow {
				// 往前 missedWindow 個音節以外：只留路徑已經走的補鍵讀音，其他的已經有後面的字可以判斷過了。
				if y, ok := taken[at]; ok && slices.Contains(extra, y) {
					extra = []zhuyin.Syllable{y}
				} else {
					extra = nil
				}
			}
		}
		if len(alts) == 0 && len(extra) == 0 || s.literals[at] {
			continue
		}
		row := slices.Clone(lat[at])
		for _, h := range alts {
			if !slices.ContainsFunc(row, func(x input.Hyp) bool { return x.Syl == h.Syl }) {
				row = append(row, h)
			}
		}
		for _, y := range extra {
			if i := slices.IndexFunc(row, func(x input.Hyp) bool { return x.Syl == y }); i < 0 {
				row = append(row, input.Hyp{Syl: y, Cost: s.Spatial.Missed, Corrected: true})
			} else if row[i].Corrected {
				row[i].Cost = min(row[i].Cost, s.Spatial.Missed)
			}
		}
		lat[at] = row
	}
	return lat
}

// pathCorrections 是候選清單用的音節格：[start, end) 以外的位置只留目前路徑走的修正讀音（觸控鍵盤，#335）。
// 候選在前後文裡評分，前後文是畫面上的句子；每個位置十幾個修正讀音都放進去，前後文的計算會讓開候選慢一倍，
// 排序卻幾乎只看路徑走的那一個。清單位置上的修正讀音照舊全部列舉。沒有修正讀音時（桌面）原樣回傳。
func (s *Session) pathCorrections(lat input.Lattice, start, end int) input.Lattice {
	if len(s.spatial) == 0 && (!s.Spatial.on() || s.Spatial.Missed <= 0) {
		return lat
	}
	taken := s.pathReadings()
	for at := range lat {
		if at >= start && at < end || !slices.ContainsFunc(lat[at], func(h input.Hyp) bool { return h.Corrected && h.Syl != taken[at] }) {
			continue
		}
		lat[at] = slices.DeleteFunc(slices.Clone(lat[at]), func(h input.Hyp) bool { return h.Corrected && h.Syl != taken[at] })
	}
	return lat
}

// pathReadings 是目前路徑在各位置走的修正讀音（Seg.Matched）。
func (s *Session) pathReadings() map[int]zhuyin.Syllable {
	taken := map[int]zhuyin.Syllable{}
	for _, g := range s.path {
		if g.Matched == "" {
			continue
		}
		if r := zhuyin.ParseReading(g.Matched); len(r) == g.End-g.Start {
			for k, y := range r {
				taken[g.Start+k] = y
			}
		}
	}
	return taken
}

// labelSpatial 標出開著的候選裡、讀音要靠鄰鍵替換、補上一鍵或合併弧才得到的那些（proto.CandidateCorrected）。
func (s *Session) labelSpatial(pos int) {
	if len(s.spatial) == 0 && len(s.merges) == 0 && (!s.Spatial.on() || s.Spatial.Missed <= 0) || s.cand == nil {
		return
	}
	base := s.front.Expand(s.syls)
	for i, c := range s.cand.items {
		if i >= len(s.cand.labels) {
			break
		}
		for k, y := range c.Entry.Reading {
			at := pos + k
			if _, ok := s.mergedAt(at); ok {
				s.cand.labels[i] = proto.CandidateCorrected // 合併弧合成的音節（merge.go）：讀音整個是修正
				break
			}
			if at >= len(base) || slices.ContainsFunc(base[at], func(h input.Hyp) bool { return h.Syl == y }) {
				continue
			}
			if slices.ContainsFunc(s.spatial[at], func(h input.Hyp) bool { return h.Syl == y }) || slices.Contains(s.missedAt(s.syls, at), y) {
				s.cand.labels[i] = proto.CandidateCorrected
				break
			}
		}
	}
}
