package decode

import (
	"math"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// MassVersion 是選配介面（比照 ReadingPreference）：評分源回報個人資料版本，
// 版本不變時 massScale 跨搜尋保留。沒有實作的來源維持每次重建。
type MassVersion interface {
	MassVersion() (gen int, time int64)
}

// maxMassKeys 是 massScale 跨搜尋保留時的上限。真實資料包上一般打字一個 session 最多約 2,200 筆；
// 暫停學習又沒有跨小時（版本不變）時會一直累積，約 9 萬個請求長到 10 萬筆。超過就整份清掉重算。
const maxMassKeys = 16384

// Only a proper prefix of an already reachable complete word needs this guard.
// Ordinary evidence for complete words and learned adjacent word pairs remains
// available. Unfinished future words do not create a protected boundary.
func (d *Decoder) prepareMass(lat input.Lattice) {
	if !d.prepareScale(lat) {
		return
	}
	d.longerEnd = make([]int, len(lat))
	ids := d.rowIDsFor(lat)
	for at := range lat {
		if ids == nil {
			d.longerEnd[at] = d.longerFrom(lat, at)
		} else {
			d.longerEnd[at] = d.longerRemembered(lat, ids, at)
		}
	}
}

// longerMemoKey 是 longerMemo 的失效條件：詞庫、評分源報的版本（學習會加個人片語，自訂詞彙會加詞）、成本上限。
type longerMemoKey struct {
	lex, priorOwner any
	gen             int
	time            int64
	priorVersion    uint64
	maxCost         float64
}

// longerEntry 是記下的一次 longerFrom：從起點走過的列（相對位置與編號）、結果跟長度有關時那時剩幾列（否則 -1），
// 以及結果（相對位置，0 是沒有）。
type longerEntry struct {
	offs   []int32
	ids    []uint32
	tail   int
	longer int
}

// maxLongerMemo 是 longerMemo 與 rowIDs 最多記幾筆，超過就清掉重記；maxLongerEntries 是同一個起點列最多記幾筆。
const (
	maxLongerMemo    = 1 << 15
	maxLongerEntries = 8
)

// rowIDsFor 是每一列內容的編號（spans 只看假設的讀音、寬度與成本），順便確認 longerMemo 還有效。不能記（詞庫
// 不能比較、評分源不報版本，或 FreshMass）時回傳 nil。
//
// 前綴保護（longerEnd）只在有學到的計數時算，每個非逐鍵的解碼、每種詞長的候選視窗都要對整段重算；手機的候選列
// 每一鍵要算好幾次，學習打開時是每一鍵最大的一筆。大部分的起點走過的列跟上一次相同。
func (d *Decoder) rowIDsFor(lat input.Lattice) []uint32 {
	if d.FreshMass || !reflect.ValueOf(d.Lex).Comparable() {
		return nil
	}
	k := longerMemoKey{lex: d.Lex, maxCost: d.MaxCost}
	if v, ok := d.Score.(MassVersion); ok {
		k.gen, k.time = v.MassVersion()
	} else {
		return nil // 不報版本：不知道個人片語什麼時候改變
	}
	if v, ok := d.Score.(PriorVersion); ok {
		k.priorOwner, k.priorVersion = v.PriorVersion()
		if !reflect.ValueOf(k.priorOwner).Comparable() {
			return nil
		}
	}
	if d.longerMemo == nil || d.longerMemoKey != k || len(d.longerMemo) > maxLongerMemo || len(d.rowIDs) > maxLongerMemo {
		d.longerMemo, d.rowIDs, d.longerMemoKey = map[uint32][]longerEntry{}, map[string]uint32{}, k
	}
	ids := make([]uint32, len(lat))
	var b strings.Builder
	for i, row := range lat {
		b.Reset()
		for _, h := range row {
			b.WriteString(string(h.Syl))
			b.WriteByte(0)
			b.WriteString(strconv.Itoa(h.Span))
			b.WriteByte(0)
			b.WriteString(strconv.FormatUint(math.Float64bits(h.Cost), 16))
			b.WriteByte(1)
		}
		id, ok := d.rowIDs[b.String()]
		if !ok {
			id = uint32(len(d.rowIDs))
			d.rowIDs[b.String()] = id
		}
		ids[i] = id
	}
	return ids
}

// longerRemembered 是 longerFrom(lat, at)：起點那一列記過、那次走過的列現在都一樣（結果跟長度有關時剩下的列數也
// 一樣）就沿用，否則重走一次並記下走過哪些列。spans 的走法只看讀過的列與詞庫，所以結果相同。
func (d *Decoder) longerRemembered(lat input.Lattice, ids []uint32, at int) int {
	entries := d.longerMemo[ids[at]]
	for _, e := range entries {
		if e.tail >= 0 && e.tail != len(lat)-at {
			continue
		}
		same := true
		for k, off := range e.offs {
			if p := at + int(off); p >= len(lat) || ids[p] != e.ids[k] {
				same = false
				break
			}
		}
		if same {
			if e.longer == 0 {
				return 0
			}
			return at + e.longer
		}
	}
	seen := map[int]bool{}
	e := longerEntry{tail: -1}
	longer := 0
	d.spansSeen(lat, at, func(sp span) {
		if len(sp.reading) <= 1 {
			return
		}
		for _, x := range d.Lex.Exact(sp.reading) {
			if !x.Correction {
				longer = max(longer, sp.end)
				break
			}
		}
	}, func(pos int) {
		if pos < 0 {
			e.tail = len(lat) - at
		} else if !seen[pos] {
			seen[pos] = true
			e.offs, e.ids = append(e.offs, int32(pos-at)), append(e.ids, ids[pos])
		}
	})
	if longer > 0 {
		e.longer = longer - at
	}
	if len(entries) >= maxLongerEntries {
		entries = entries[1:]
	}
	d.longerMemo[ids[at]] = append(entries, e)
	return longer
}

// prepareScale 是 prepareMass 中與位置無關的部分：音節格寬度（width）與 massScale 的失效判斷，清掉 longerEnd。
// 回報評分源有沒有學到的計數，也就是要不要算前綴保護（longerEnd）。
func (d *Decoder) prepareScale(lat input.Lattice) bool {
	d.width = lat.Width()
	d.longerEnd = nil
	d.massLat = lat
	a, ok := d.Score.(Adaptive)
	if !ok || !a.HasAdaptation() {
		d.massScale, d.homophones, d.majorMemo = nil, nil, nil
		return false
	}
	v, ok := d.Score.(MassVersion)
	if ok && !reflect.ValueOf(d.Score).Comparable() {
		ok = false // 身分無法比較（!= 會 panic）：不保留
	}
	d.keepMajor(ok)
	if !ok || d.FreshMass {
		d.massScale, d.homophones = nil, nil // 來源不報版本（或測試要求每次重建）：照舊重建
	} else if d.massScale != nil {
		gen, at := v.MassVersion()
		if d.massScorer != d.Score || d.massGen != gen || d.massTime != at {
			d.massScale, d.homophones = nil, nil
		}
	}
	if d.massScale == nil {
		d.massScorer, d.massGen, d.massTime = d.Score, 0, 0
		if ok {
			d.massGen, d.massTime = v.MassVersion()
		}
	}
	return true
}

// longerFrom 是從 at 起最長的完整多音節詞（不算自動修正）結束的位置；沒有時是 0。
func (d *Decoder) longerFrom(lat input.Lattice, at int) int {
	longer := 0
	d.spans(lat, at, func(sp span) {
		if len(sp.reading) <= 1 {
			return
		}
		for _, e := range d.Lex.Exact(sp.reading) {
			if !e.Correction {
				longer = max(longer, sp.end)
				break
			}
		}
	})
	return longer
}

// ReadingPreference 是使用者反覆選用的詞在同讀音裡的有上限加分（personal 的讀音習慣，#218）。
type ReadingPreference interface {
	ReadingLift(word string, reading []zhuyin.Syllable) float64
	// SplitConfirmed 回報 left 後面接 word 是否真的提交過。
	SplitConfirmed(left, word string) bool
}

// readingLift 是弧的讀音習慣加分。前綴保護不動，也不讓加分繞過它（#218）：
//   - 這個詞本身是更長完整詞的前綴時（程式 對 程式碼）不加分，否則「程式碼」會被拆成「程式｜嗎」。
//   - left 是某個完整詞的前綴時（改｜程式 對 改成），這一刀可能拆壞完整詞：沒打過這個配對就不加分；
//     打過的話多給一份，抵銷前綴保護壓在 left 上的量，否則使用者打過的切法還是贏不了。
func (d *Decoder) readingLift(start, end int, left string, e lexicon.Entry) float64 {
	r, ok := d.Score.(ReadingPreference)
	if !ok {
		return 0
	}
	lift := r.ReadingLift(e.Surface, e.Reading)
	if lift <= 0 {
		return 0
	}
	if start >= 0 && start < len(d.longerEnd) && d.longerEnd[start] > end {
		return 0
	}
	if d.leftSplit(start, left) {
		if !r.SplitConfirmed(left, e.Surface) {
			return 0
		}
		return 2 * lift
	}
	return lift
}

// leftSplit 回報結束在 start 的前一個詞 left 是不是某個完整詞的前綴（從 left 的起點有更長的完整詞跨過 start）。
// 一般的音節格 left 的起點就是 start 減去它的字數。按鍵圖的一個字佔 1–width 個位置，起點不只一種，
// 任何一種可能的起點有更長的完整詞跨過 start 就算（保守：寧可不加讀音習慣，也不讓它拆壞完整詞）。
func (d *Decoder) leftSplit(start int, left string) bool {
	n := utf8.RuneCountInString(left)
	if d.width <= 1 {
		from := start - n
		return from >= 0 && from < len(d.longerEnd) && d.longerEnd[from] > start
	}
	for from := max(0, start-d.width*n); from <= start-n; from++ {
		if from < len(d.longerEnd) && d.longerEnd[from] > start {
			return true
		}
	}
	return false
}

// HabitLift 是解碼器在 h 裡替 seg 實際加的讀音習慣分數，跟 arcLog 走同一段 readingLift（前綴保護下是 0、打過的切法是兩份）。
// lat 是產生 h 的音節格，h 的位置相對於它；d.Left 也要和當時相同。補全的尾端、經近音到達的詞不加分。
func (d *Decoder) HabitLift(lat input.Lattice, h Hypothesis, seg Seg, reading []zhuyin.Syllable) float64 {
	r, ok := d.Score.(ReadingPreference)
	if !ok || seg.Partial || seg.Matched != "" || r.ReadingLift(seg.Word, reading) <= 0 {
		return 0
	}
	left := d.InitialLeft()
	for _, g := range h.Segments {
		if g.End == seg.Start {
			left = g.Word
		}
	}
	d.prepareMass(lat)
	return d.readingLift(seg.Start, seg.End, left, lexicon.Entry{Surface: seg.Word, Reading: reading})
}

func (d *Decoder) arcProbability(start, end int, left string, e lexicon.Entry) float64 {
	p := 0.0
	if start >= 0 && start < len(d.longerEnd) && end < d.longerEnd[start] {
		p = d.languageProbability(left, e)
	} else {
		p = d.Score.Prob(left, e.Surface)
	}
	if minorReading(e) {
		// 上限真的會壓低分數（這個字在這個前文有學到的計數）才查 typedAsMajor：沒有學習紀錄時不走這一段。
		if limit := d.unlearnedMass(left, e.Surface); limit < p && !d.typedAsMajor(start, end, e) {
			p = limit
		}
	}
	return p
}

// typedAsMajor 回報 e（單字的次要讀音）在 start 這個沒打聲調的位置，是不是跟這個字的主讀音或通行音同一個音節、
// 只差聲調（#373 的後續）。不打聲調打 ㄒㄧㄤ 時「鄉」同時有 ㄒㄧㄤ（主讀音）與 ㄒㄧㄤˋ（可考讀音）兩條弧，
// 使用者打的鍵就是他平常打這個字的鍵，學到的計數本來就是這樣打出來的，次要讀音的上限（minorReading）不適用。
// 兩條弧只在前綴保護下有差別：languageProbability 依帶聲調的讀音分組，ㄒㄧㄤ 那一組被學過的「鄉」佔滿而壓回
// 系統的量，學過的「雲林縣｜鄉｜道」要靠 ㄒㄧㄤˋ 那條弧才贏得過跨過這裡的「想到」；封頂之後整句打不回來。
// 打了聲調的位置（明確打 ㄒㄧㄤˋ）、簡拼展開，以及別的音節的次要讀音（喔 的 ㄨㄛˋ、鞥 的 ㄧˋ）不算，照舊封頂。
func (d *Decoder) typedAsMajor(start, end int, e lexicon.Entry) bool {
	if start < 0 || start >= len(d.massLat) {
		return false
	}
	y := e.Reading[0]
	typed := false
	for _, h := range d.massLat[start] {
		if h.Syl == y && h.Untoned && !h.Abbreviated && start+h.Width() == end {
			typed = true
			break
		}
	}
	if !typed {
		return false
	}
	base := y.Base()
	for _, h := range d.massLat[start] {
		if h.Syl == y || !h.Untoned || h.Abbreviated || start+h.Width() != end || h.Syl.Base() != base {
			continue
		}
		if d.majorReading(e.Surface, h.Syl) {
			return true
		}
	}
	return false
}

// majorKey 是 majorMemo 的 key：字與單音節讀音。
type majorKey struct {
	word string
	syl  zhuyin.Syllable
}

// majorReading 回報 word 有沒有讀音 y 的單字詞條，而且不是次要讀音（minorReading）。結果只跟詞庫有關
// （個人化視圖把親手選過的讀音改成 1 時個人資料版本會變），所以跟 massScale 用同樣的條件跨搜尋保留。
func (d *Decoder) majorReading(word string, y zhuyin.Syllable) bool {
	k := majorKey{word, y}
	if v, ok := d.majorMemo[k]; ok {
		return v
	}
	v := false
	for _, x := range d.Lex.Exact([]zhuyin.Syllable{y}) {
		if x.Surface == word && !x.Correction && !minorReading(x) {
			v = true
			break
		}
	}
	if d.majorMemo == nil || len(d.majorMemo) >= maxMassKeys {
		d.majorMemo = map[majorKey]bool{}
	}
	d.majorMemo[k] = v
	return v
}

// majorMemoKey 是 majorMemo 的失效條件：詞庫、評分源與個人資料版本。
type majorMemoKey struct {
	lex, scorer any
	gen         int
	time        int64
}

// keepMajor 在每次搜尋開始時決定 majorMemo 能不能沿用；versioned 是評分源報版本而且身分可以比較。
func (d *Decoder) keepMajor(versioned bool) {
	if !versioned || d.FreshMass || !reflect.ValueOf(d.Lex).Comparable() {
		d.majorMemo, d.majorMemoKey = nil, majorMemoKey{}
		return
	}
	k := majorMemoKey{lex: d.Lex, scorer: d.Score}
	k.gen, k.time = d.Score.(MassVersion).MassVersion()
	if d.majorMemoKey != k {
		d.majorMemo, d.majorMemoKey = nil, k
	}
}

// minorWeight 是單字次要讀音的讀音權重上限：詞庫的可考讀音（internal/compile 的 Attested 等級，#320 之後是
// min(0.1, …)）。主讀音與通行音（1、0.5）、使用者親手用這個讀音選過的字與自訂詞（個人化視圖改成 1）都在這之上。
const minorWeight = 0.1

// minorReading 回報 e 是不是單字的次要讀音（#373）。學到的計數只記詞面、不記讀音：單獨送出過幾次「喔」（ㄛ）的人，
// 「喔」在句首的個人機率是系統的幾百倍，乘上次要讀音的 0.1 仍然蓋過那個音真正常用的字——單打 ㄨ 變成「喔」
// （本來是「烏」），不打聲調的 ㄨㄛ 變成「喔」（本來是「我」）；學過「鞥」（ㄥ）的人打 ㄧˋ 也是「鞥」。
// 那些計數是用別的讀音打出來的，所以次要讀音不沾學到的計數，分數不超過沒學過這個字時的量（unlearnedMass）；
// 次要讀音的用量本來就不跟著字頻成長，也不該跟著個人用量成長。使用者真的用這個讀音選過這個字時，
// 個人化視圖已經把權重改成 1（personal 的 Readings），不在此限，照常學習。
// 多字詞（破音字用常用音打的規則讀音）與自動修正不算：那些讀音就是使用者打這個詞的方式。
func minorReading(e lexicon.Entry) bool {
	return len(e.Reading) == 1 && !e.Correction && float32(e.PRW) <= float32(minorWeight)
}

// unlearnedMass 是 word 接在 left 後面、不算學到的計數時的機率（Adaptive.PriorMass，分母與 Prob 相同）；
// 評分源沒有學到的計數時回傳 +Inf（不設上限）。沒學過的詞 Prob 本來就不超過它，取小之後數值不變。
func (d *Decoder) unlearnedMass(left, word string) float64 {
	a, ok := d.Score.(Adaptive)
	if !ok || !a.HasAdaptation() {
		return math.Inf(1)
	}
	return a.PriorMass(left, word)
}

// Adaptive identifies a scorer with ordinary learned counts. These counts may
// redistribute a reading's alternatives, but must not inflate its total mass.
type Adaptive interface {
	// PriorMass retains the mixture's denominator but excludes positive learned
	// counts. Otherwise normalization itself could inflate unobserved fragments.
	PriorMass(left, word string) float64
	HasAdaptation() bool
}

// MassTerms 是 Adaptive 評分源的選配介面：一次回傳 (PriorMass(left, w), Prob(left, w))，兩者共用一次語言模型的查詢。
// self 是解碼器拿著的評分源；實作在 self 不是自己時（包著它的評分器沿用了這個方法）回傳 false，解碼器改回分開查。
type MassTerms interface {
	MassTerms(self any, left, w string) (prior, prob float64, ok bool)
}

// homophones 是一個讀音的詞面（去掉重複，依 Exact 的順序）與各自最大的讀音權重。
type homophones struct {
	words   []string
	weights []float64
}

// homophonesOf 是 languageProbability 要彙總的同音詞。跟前一個詞無關，同一個讀音平均要配上幾十個前詞，
// 所以依讀音記下，跟 massScale 一起失效（詞庫換了也重算）。
func (d *Decoder) homophonesOf(key string, reading []zhuyin.Syllable) homophones {
	cache := d.massScale != nil && reflect.ValueOf(d.Lex).Comparable()
	if !cache || d.homLex != d.Lex {
		d.homophones, d.homLex = nil, nil
	}
	if h, ok := d.homophones[key]; ok {
		return h
	}
	// Deduplicate surfaces so duplicated sources cannot manufacture mass.
	var h homophones
	at := map[string]int{}
	for _, c := range d.Lex.Exact(reading) {
		i, ok := at[c.Surface]
		if !ok {
			i = len(h.words)
			at[c.Surface] = i
			h.words, h.weights = append(h.words, c.Surface), append(h.weights, 0)
		}
		h.weights[i] = max(h.weights[i], c.PRW)
	}
	if !cache {
		return h
	}
	if d.homophones == nil || len(d.homophones) >= maxMassKeys {
		d.homophones, d.homLex = map[string]homophones{}, d.Lex
	}
	d.homophones[key] = h
	return h
}

// massKey 是 massScale 的 key：前一個詞與讀音。
type massKey struct{ left, reading string }

func (d *Decoder) languageProbability(left string, e lexicon.Entry) float64 {
	p := d.Score.Prob(left, e.Surface)
	a, ok := d.Score.(Adaptive)
	if !ok || !a.HasAdaptation() {
		return p
	}
	key := massKey{left, string(e.Reading[0])}
	if len(e.Reading) > 1 {
		key.reading = zhuyin.Key(e.Reading)
	}
	if factor, ok := d.massScale[key]; ok {
		return p * factor
	}
	h := d.homophonesOf(key.reading, e.Reading)
	system, personal := 0.0, 0.0
	terms, _ := d.Score.(MassTerms)
	for i, word := range h.words {
		if terms != nil {
			if prior, prob, ok := terms.MassTerms(d.Score, left, word); ok {
				system += prior * h.weights[i]
				personal += prob * h.weights[i]
				continue
			}
		}
		system += a.PriorMass(left, word) * h.weights[i]
		personal += d.Score.Prob(left, word) * h.weights[i]
	}
	factor := 1.0
	if system > 0 && personal > 0 {
		factor = min(1, system/personal)
	}
	if d.massScale == nil || len(d.massScale) >= maxMassKeys {
		d.massScale = map[massKey]float64{} // 同一版本內打很久（暫停學習）也不無限長大；清掉只是之後重算
	}
	d.massScale[key] = factor
	return p * factor
}
