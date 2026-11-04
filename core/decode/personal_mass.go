package decode

import (
	"reflect"
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

// Only a proper prefix of an already reachable complete word needs this guard.
// Ordinary evidence for complete words and learned adjacent word pairs remains
// available. Unfinished future words do not create a protected boundary.
func (d *Decoder) prepareMass(lat input.Lattice) {
	d.longerEnd = nil
	a, ok := d.Score.(Adaptive)
	if !ok || !a.HasAdaptation() {
		d.massScale = nil
		return
	}
	v, ok := d.Score.(MassVersion)
	if ok && !reflect.ValueOf(d.Score).Comparable() {
		ok = false // 身分無法比較（!= 會 panic）：不保留
	}
	if !ok {
		d.massScale = nil // 來源不報版本：保守起見照舊重建
	} else if d.massScale != nil {
		gen, at := v.MassVersion()
		if d.massScorer != d.Score || d.massGen != gen || d.massTime != at {
			d.massScale = nil
		}
	}
	if d.massScale == nil {
		d.massScorer, d.massGen, d.massTime = d.Score, 0, 0
		if ok {
			d.massGen, d.massTime = v.MassVersion()
		}
	}
	d.longerEnd = make([]int, len(lat))
	for at := range lat {
		d.spans(lat, at, func(reading []zhuyin.Syllable, _ float64) {
			if len(reading) <= 1 {
				return
			}
			for _, e := range d.Lex.Exact(reading) {
				if !e.Correction {
					d.longerEnd[at] = max(d.longerEnd[at], at+len(reading))
					break
				}
			}
		})
	}
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
	if from := start - utf8.RuneCountInString(left); from >= 0 && from < len(d.longerEnd) && d.longerEnd[from] > start {
		if !r.SplitConfirmed(left, e.Surface) {
			return 0
		}
		return 2 * lift
	}
	return lift
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
	if start >= 0 && start < len(d.longerEnd) && end < d.longerEnd[start] {
		return d.languageProbability(left, e)
	}
	return d.Score.Prob(left, e.Surface)
}

// Adaptive identifies a scorer with ordinary learned counts. These counts may
// redistribute a reading's alternatives, but must not inflate its total mass.
type Adaptive interface {
	// PriorMass retains the mixture's denominator but excludes positive learned
	// counts. Otherwise normalization itself could inflate unobserved fragments.
	PriorMass(left, word string) float64
	HasAdaptation() bool
}

func (d *Decoder) languageProbability(left string, e lexicon.Entry) float64 {
	p := d.Score.Prob(left, e.Surface)
	a, ok := d.Score.(Adaptive)
	if !ok || !a.HasAdaptation() {
		return p
	}
	key := left + "\t" + zhuyin.Key(e.Reading)
	if factor, ok := d.massScale[key]; ok {
		return p * factor
	}
	// Deduplicate surfaces so duplicated sources cannot manufacture mass.
	entries := d.Lex.Exact(e.Reading)
	weights := map[string]float64{}
	var order []string
	for _, c := range entries {
		if _, ok := weights[c.Surface]; !ok {
			order = append(order, c.Surface)
		}
		weights[c.Surface] = max(weights[c.Surface], c.PRW)
	}
	system, personal := 0.0, 0.0
	for _, word := range order {
		system += a.PriorMass(left, word) * weights[word]
		personal += d.Score.Prob(left, word) * weights[word]
	}
	factor := 1.0
	if system > 0 && personal > 0 {
		factor = min(1, system/personal)
	}
	if d.massScale == nil {
		d.massScale = map[string]float64{}
	}
	d.massScale[key] = factor
	return p * factor
}
