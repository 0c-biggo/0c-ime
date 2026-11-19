package session

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 注音九宮格（手機）：一顆鍵代表一組注音（三星式分組，例如 ㄇㄋㄜㄝ），外殼在 symbol 請求的 Group 送這顆鍵的那組注音。
// 這一版只靠聲調鍵與空白分音節：鍵先存在 nineKeys（目前音節的鍵組序列），按聲調鍵（Group 全是聲調符號，聲調未指定）、
// 空白（一聲，Symbol 是 ˉ）或指定的聲調（Symbol 是單一聲調符號）時，每顆鍵各取一個注音、照「聲母 介音 韻母」的順序組合，
// 留下國語實際存在的音節，依單音節常用程度排序，第一個是組字的代表讀音，全部放進同一位置的音節格、成本都是 0，
// 由解碼依前後文挑；挑中的讀音寫回組字（absorbNine），顯示、學習與神經評分看到的都是它。
//
// 跟相鄰鍵容錯（spatial.go）的差別：九宮格的每個讀音都是使用者按的鍵，不是按錯，所以成本 0、候選不標 corrected；
// 音節格的假設一律標 input.Hyp.Untoned（同沒打聲調、簡拼的展開，是打的音不是近音），解碼器據此裁切弧數，
// 候選、Fits 與 decode.Reading 都把它們當成打的音。九宮格的鍵不看 Near，兩種容錯不相乘。
//
// 連續按鍵不分音節的切分（不按聲調鍵）這一版不做；一個音節最多三個注音，接上去就組不成任何音節開頭的鍵直接忽略。

// maxNineReadings 是一個九宮格音節最多保留幾個讀音（含代表讀音）。三星分組下兩三顆鍵合法的組合多半不到 10 個；
// 沒打聲調時每個讀音再展開成五個聲調，12 個讀音是 60 個假設，跟簡拼的 maxAbbreviatedReadings（64）同一個量級，
// 每鍵延遲不超出簡拼已經量過的範圍。外殼送很大的鍵組時才會截掉，依常用程度留前面的。
const maxNineReadings = 12

// nineToneKey 是九宮格聲調鍵在這一版的意思：聲調未指定（沒打聲調的音節）。
const nineToneKey = 0

// ninePending 回報是否有還沒完成音節的九宮格鍵。
func (s *Session) ninePending() bool { return len(s.nineKeys) > 0 }

// nineSymbol 處理九宮格的 symbol 請求：group 非空是一顆九宮格鍵；group 是空的（已有九宮格鍵時）是長按選的注音
// （每個符號各是只有自己的一顆鍵）或聲調（空白的 ˉ、指定的聲調）。回傳 handled：鍵被忽略（接不上音節開頭）或聲調
// 收不成音節時是 false，外殼據此知道這一下什麼都沒改（APP 的 bridge 照 handled 記下每一步，退格才對得上）。
func (s *Session) nineSymbol(symbol, group string) bool {
	if group == "" {
		handled := false
		for _, r := range nineGroup(symbol) {
			if zhuyin.ClassOf(r) != zhuyin.Tone {
				handled = s.pressNine(string(r)) || handled
			} else if s.ninePending() {
				handled = s.finishNine(toneNumber(r)) || handled
			}
		}
		return handled
	}
	keys := []rune(nineGroup(group))
	tones := 0
	for _, r := range keys {
		if zhuyin.ClassOf(r) == zhuyin.Tone {
			tones++
		}
	}
	switch {
	case len(keys) == 0 || tones > 0 && tones < len(keys):
		return false // 沒有注音，或聲調跟注音混在同一顆鍵：不認得
	case tones == 0:
		s.adoptComposer()
		return s.pressNine(string(keys))
	}
	s.adoptComposer()
	if !s.ninePending() {
		return false // 落單的聲調鍵（同 symbol：沒有注音時不收）
	}
	tone := nineToneKey // 聲調鍵（好幾個聲調）：聲調未指定
	if tones == 1 {
		tone = toneNumber(keys[0])
	}
	return s.finishNine(tone)
}

// nineGroup 把外殼送來的鍵組整理成不重複的注音符號，非注音字元略過。
func nineGroup(group string) string {
	var b strings.Builder
	for _, r := range group {
		if zhuyin.ClassOf(r) != zhuyin.None && !strings.ContainsRune(b.String(), r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// adoptComposer：組音器裡已經有一般注音鍵打的注音時（混用注音鍵與九宮格），把它們當成各自只有一個注音的鍵接進九宮格。
func (s *Session) adoptComposer() {
	if s.comp.Empty() {
		return
	}
	for _, r := range s.comp.String() {
		s.nineKeys = append(slices.Clip(s.nineKeys), string(r))
	}
	s.comp.Reset()
	s.compNear = nil
}

// pressNine 接上一顆鍵；接上去就組不成任何音節開頭時忽略這顆鍵（不自動切音節），回傳 false。
func (s *Session) pressNine(keys string) bool {
	next := append(slices.Clip(s.nineKeys), keys)
	if len(nineSpellings(next, false)) == 0 {
		return false
	}
	s.nineKeys = next
	return true
}

// nineSpellings 列出每顆鍵各取一個注音、照「聲母 介音 韻母」順序組得起來的拼法（不帶聲調）：complete 時只要完整音節，
// 否則是某個音節的開頭（含完整音節本身）。依鍵組裡的順序列，不排序。
func nineSpellings(keys []string, complete bool) []zhuyin.Syllable {
	var out []zhuyin.Syllable
	var walk func(i int, last zhuyin.Class, b []rune)
	walk = func(i int, last zhuyin.Class, b []rune) {
		y := zhuyin.Syllable(string(b))
		if i > 0 && !zhuyin.LegalPrefix(y) {
			return
		}
		if i == len(keys) {
			if !complete || y.Legal() {
				out = append(out, y)
			}
			return
		}
		for _, r := range keys[i] {
			cl := zhuyin.ClassOf(r)
			if cl <= last || cl == zhuyin.Tone {
				continue
			}
			walk(i+1, cl, append(b[:len(b):len(b)], r))
		}
	}
	walk(0, zhuyin.None, nil)
	return out
}

// nineBases 是音節表的所有音節（不帶聲調），算拼法開頭的常用程度用。
var nineBases = zhuyin.Inventory()

// basePrior 是不帶聲調的音節 b 在 tone（0 是聲調未指定，取五個聲調裡最常用的）的常用程度（syllablePrior）。
func (s *Session) basePrior(b zhuyin.Syllable, tone int) float64 {
	if tone > 0 {
		return s.syllablePrior(b.WithTone(tone))
	}
	best := 0.0
	for t := 1; t <= 5; t++ {
		best = max(best, s.syllablePrior(b.WithTone(t)))
	}
	return best
}

// prefixPrior 是以 p 開頭的音節裡最常用的那個的常用程度。
func (s *Session) prefixPrior(p zhuyin.Syllable) float64 {
	best := 0.0
	for _, b := range nineBases {
		if strings.HasPrefix(string(b), string(p)) {
			best = max(best, s.basePrior(b, 0))
		}
	}
	return best
}

// byPrior 依 prior 由高到低排序（同分照原本的順序），留前 maxNineReadings 個。
func byPrior(ys []zhuyin.Syllable, prior func(zhuyin.Syllable) float64) []zhuyin.Syllable {
	ps := make(map[zhuyin.Syllable]float64, len(ys))
	for _, y := range ys {
		ps[y] = prior(y)
	}
	slices.SortStableFunc(ys, func(a, b zhuyin.Syllable) int {
		switch {
		case ps[a] > ps[b]:
			return -1
		case ps[a] < ps[b]:
			return 1
		}
		return 0
	})
	if len(ys) > maxNineReadings {
		ys = ys[:maxNineReadings]
	}
	return ys
}

// nineReadings 是 keys 以聲調 tone（0 是聲調未指定）完成時的讀音：沒打聲調的音節或帶聲調的音節，依常用程度排序。
func (s *Session) nineReadings(keys []string, tone int) []zhuyin.Syllable {
	bases := byPrior(nineSpellings(keys, true), func(b zhuyin.Syllable) float64 { return s.basePrior(b, tone) })
	out := make([]zhuyin.Syllable, len(bases))
	for i, b := range bases {
		if tone == nineToneKey {
			out[i] = zhuyin.Untone(b)
		} else {
			out[i] = b.WithTone(tone)
		}
	}
	return out
}

// pendingNineSpellings 是還沒完成的那串鍵目前可能的拼法（音節開頭），依以它開頭的音節的常用程度排序。
func (s *Session) pendingNineSpellings() []zhuyin.Syllable {
	return byPrior(nineSpellings(s.nineKeys, false), s.prefixPrior)
}

// finishNine 以聲調 tone（0 是聲調未指定）完成那串九宮格鍵：代表讀音照一般音節收進組字（completeSyllable），
// 其他讀音記在同一位置（nine）。組不成任何音節時鍵留著、回傳 false，使用者可以退格改。
func (s *Session) finishNine(tone int) bool {
	readings := s.nineReadings(s.nineKeys, tone)
	if len(readings) == 0 {
		return false
	}
	s.nineKeys = nil
	var before zhuyin.Composer
	for _, r := range readings[0].Base() {
		before.Add(r)
	}
	s.compNear = nil // 九宮格不帶鄰鍵
	if len(readings) > 1 {
		s.nineStaged = readings
	}
	// 同 finishUntoned：多了一個音節，讓神經評分這一次。
	allow := s.allowRerank
	s.allowRerank = true
	s.completeSyllable(before, readings[0])
	s.allowRerank = allow
	s.nineStaged = nil
	return true
}

// widenNine 把九宮格位置的音節格換成那個位置的所有讀音（成本 0、標 Untoned）。音節格只看 nine 記的讀音、
// 不看 syls 目前寫回的是哪一個，所以解碼結果不會因為上一次挑了哪個讀音而改變（同 layout_readings.go）。
func (s *Session) widenNine(syls []zhuyin.Syllable, lat input.Lattice) input.Lattice {
	if len(s.nine) == 0 || len(syls) != len(s.syls) {
		return lat
	}
	for at, readings := range s.nine {
		if at >= len(lat) || s.literals[at] {
			continue
		}
		var row []input.Hyp
		for _, y := range readings {
			for _, v := range y.Tones() {
				row = append(row, input.Hyp{Syl: v, Untoned: true})
			}
		}
		lat[at] = row
	}
	return lat
}

// absorbNine 把路徑在九宮格位置走的讀音寫回 syls（換成 nine 裡含那個讀音的那一項，聲調未指定的仍未指定）。
// 回報 syls 是否改變。解不出讀音的段落（例如尾端前綴）不動。
func (s *Session) absorbNine() bool {
	if len(s.nine) == 0 || len(s.path) == 0 {
		return false
	}
	var lat input.Lattice
	changed := false
	for _, g := range s.path {
		if g.End > len(s.syls) || s.literals[g.Start] {
			continue
		}
		touched := false
		for at := g.Start; at < g.End && !touched; at++ {
			_, touched = s.nine[at]
		}
		if !touched {
			continue
		}
		var reading []zhuyin.Syllable
		ok := false
		if g.Matched != "" {
			reading, ok = zhuyin.ParseReading(g.Matched), true
		} else {
			if lat == nil {
				lat = s.lattice()
			}
			reading, ok = decode.Reading(s.dec.Lex, lat, g.Start, g.End, g.Word)
		}
		if !ok || len(reading) != g.End-g.Start {
			continue
		}
		for at := g.Start; at < g.End; at++ {
			readings, nine := s.nine[at]
			if !nine {
				continue
			}
			i := slices.IndexFunc(readings, func(y zhuyin.Syllable) bool { return slices.Contains(y.Tones(), reading[at-g.Start]) })
			if i < 0 || s.syls[at] == readings[i] {
				continue
			}
			if !changed {
				s.syls = slices.Clone(s.syls) // 不改到存起來準備還原的切片
			}
			s.syls[at] = readings[i]
			changed = true
		}
	}
	return changed
}

// nineSignature 是九宮格讀音在解碼簽章裡的部分；沒有九宮格音節時是空字串，簽章跟沒有這個功能時相同。
func (s *Session) nineSignature() string {
	var b strings.Builder
	for _, at := range slices.Sorted(maps.Keys(s.nine)) {
		fmt.Fprintf(&b, "nine:%d:%s;", at, zhuyin.Key(s.nine[at]))
	}
	return b.String()
}

// NineReadingsAt 回傳第 at 個音節（九宮格音節）可以選的讀音，依常用程度排序，照畫面寫法（沒打聲調的不帶聲調、
// 一聲不標）；不是有多個讀音的九宮格音節時回傳 nil。外殼的左欄「注音選擇」用它查任意位置。
func (s *Session) NineReadingsAt(at int) []string {
	readings := s.nine[at]
	if len(readings) == 0 {
		return nil
	}
	out := make([]string, len(readings))
	for i, y := range readings {
		out[i] = y.Text()
	}
	return out
}

// ReadingAt 回傳第 at 個音節目前的讀音，照畫面寫法（同 NineReadingsAt：沒打聲調的不帶聲調、一聲不標）；at 不在組字
// 範圍時回傳空字串。九宮格音節是解碼挑中、寫回的那個讀音（absorbNine），外殼的組字條用它畫九宮格打的音節：
// 外殼只送了鍵組，不知道是哪個拼法。
func (s *Session) ReadingAt(at int) string {
	if at < 0 || at >= len(s.syls) {
		return ""
	}
	return s.syls[at].Text()
}

// nineSnapshot 是快照的 nine_readings：有還沒完成的九宮格鍵時是它們可能的拼法（pending）；否則是游標前那個音節
// （有多個讀音的九宮格音節才有）。
func (s *Session) nineSnapshot() *proto.NineReadings {
	if s.ninePending() {
		spellings := s.pendingNineSpellings()
		if len(spellings) == 0 {
			return nil // 例如 adoptComposer 收進來的注音組不成開頭：沒有可選的，不送 "readings": null
		}
		out := &proto.NineReadings{At: s.cursor, Pending: true}
		for _, y := range spellings {
			out.Readings = append(out.Readings, string(y))
		}
		return out
	}
	at := s.cursor - 1
	readings := s.NineReadingsAt(at)
	if readings == nil {
		return nil
	}
	return &proto.NineReadings{At: at, Readings: readings, Current: max(0, slices.Index(s.nine[at], s.syls[at]))}
}

// ninePendingText 是還沒完成的九宮格鍵在組字區的樣子：目前最可能的拼法。
func (s *Session) ninePendingText() string {
	if spellings := s.pendingNineSpellings(); len(spellings) > 0 {
		return string(spellings[0])
	}
	return ""
}

// fixNineReading 處理 nine_reading 請求：把九宮格音節（或還沒完成的那串鍵）固定成讀音 reading，移除其他讀音。
// to 等於游標而且有還沒完成的鍵時，固定的是那串鍵：每顆鍵換成只有 reading 的那個注音，之後照常按聲調完成。
// 否則 to 是已完成的音節：那個位置變成一般音節（syls 寫成 reading，不再是九宮格），蓋到它的手選解除，重新解碼。
// reading 照快照 nine_readings.readings 的寫法；不是那個位置的讀音時什麼都不改，回傳 false。
func (s *Session) fixNineReading(to int, reading string) bool {
	if s.ninePending() && to == s.cursor {
		y := zhuyin.Syllable(reading)
		if !slices.Contains(nineSpellings(s.nineKeys, false), y) {
			return false
		}
		keys := make([]string, 0, len(s.nineKeys))
		for _, r := range reading {
			keys = append(keys, string(r))
		}
		s.nineKeys = keys
		return true
	}
	i := slices.Index(s.NineReadingsAt(to), reading)
	if i < 0 {
		return false
	}
	y := s.nine[to][i]
	s.syls = slices.Clone(s.syls)
	s.syls[to] = y
	s.nine = maps.Clone(s.nine)
	delete(s.nine, to)
	locks := map[int]decode.Lock{}
	for at, l := range s.locks {
		if at+l.Len <= to || to < at {
			locks[at] = l
		}
	}
	s.locks = locks
	s.cand = nil
	return true
}
