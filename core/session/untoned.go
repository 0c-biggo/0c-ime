package session

import (
	"maps"
	"math"
	"slices"
	"sort"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/layout"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 聲調選填（#150，設定「不打聲調也能選字」，預設關閉）。
//
// 開啟時，注音照「聲母 介音 韻母」的順序接在目前的音節後面；接不上（同類已有、順序在前面，
// 或接上去就不是任何音節的開頭）而目前的注音已經是完整音節時，目前的音節不打聲調就完成，
// 這個注音開始新的音節：ㄋㄧㄏㄠ 是 ㄋㄧ＋ㄏㄠ。沒打聲調的音節在音節格裡展開成五個聲調，由解碼決定；
// 有打聲調的音節照舊硬限制，而且固定那個音節的開頭和結尾。
//
// 目前的注音還不是完整音節時照原本的規則（同類的後者取代前者：ㄋ＋ㄏ 是 ㄏ），所以打錯聲母仍可直接改。
// 組字中按 ' 只切音節（untonedSeparator）。只在標準排列生效（toneless）。
// Enter、↓、方向鍵、Tab、標點與切換英文之前，未打完但已是完整音節的注音也先不打聲調就完成；
// 空白照舊是一聲。關閉時沒有任何音節會帶 zhuyin.UntonedMark，行為與沒有此功能時相同。

// toneless 回報聲調選填是否生效：只在標準排列。倚天 41 鍵的 ' 是 ㄘ（沒有分隔鍵），許氏一個鍵兼聲母、韻母與聲調，
// 「接不上就開新音節」的規則在那裡不成立；設定保留，切回標準排列就照舊（同混打，#83、#183）。
func (s *Session) toneless() bool { return s.Toneless && s.layout == layout.Standard }

// abbreviation 回報簡拼是否生效：只在聲調選填生效時。
func (s *Session) abbreviation() bool { return s.Abbreviation && s.toneless() }

// untonedSyllable 回傳未打完的注音 base 不打聲調就完成時的音節：簡拼生效而 base 只有一個聲母或介音時是簡拼音節
// （ㄓ 也包括 ㄓㄨ、ㄓㄠ……），其他完整音節是沒打聲調的音節；都不是時回傳 ""。
func (s *Session) untonedSyllable(base zhuyin.Syllable) zhuyin.Syllable {
	if s.abbreviation() && zhuyin.Abbreviable(base) {
		return zhuyin.Abbreviate(base, 0)
	}
	if base.Legal() {
		return zhuyin.Untone(base)
	}
	return ""
}

// abbreviatedTone 處理簡拼生效時「聲母或介音直接接聲調」（ㄅˇ、空白的一聲）：打的注音本身不是這個聲調的
// 完整音節時，回傳只限聲調的簡拼音節；是的話（ㄓˋ、ㄧˊ）照舊是那個音節，打聲調的輸入結果不變。
func (s *Session) abbreviatedTone(before zhuyin.Composer, done zhuyin.Syllable) zhuyin.Syllable {
	prefix := zhuyin.Syllable(before.String())
	if !s.abbreviation() || done.Legal() || !zhuyin.Abbreviable(prefix) {
		return done
	}
	return zhuyin.Abbreviate(prefix, done.ToneOf())
}

// editsPending 回報實體按鍵 k 是不是作用在未打完的注音上的編輯鍵（Backspace 刪一個注音、Esc 清掉未打完的注音）。
// 這兩個鍵之前不完成注音鍵或九宮格打到一半的音節（finishSymbolPending），否則會先收成音節、再整個刪掉。
// 其他非字元鍵（Enter、方向鍵、Tab、Delete）本來就會用到整段組字，照舊先完成（聲調選填的 untonedKey 也會做）。
func editsPending(k string) bool { return k == proto.KeyBackspace || k == proto.KeyEscape }

// finishSymbolPending：共存時（MixedWithToneless）注音鍵打到一半就切到實體按鍵，未打完的注音不打聲調就完成，
// 不讓字母接在注音後面當成注音鍵。
func (s *Session) finishSymbolPending() {
	if s.MixedWithToneless && s.symbolPending {
		s.finishUntoned()
	}
	s.symbolPending = false
}

// PreviewPending 回傳「未打完的注音現在不打聲調就完成」時的候選，不改變組字狀態：手機的候選列在打字途中
// 一直更新，選字時才真的完成音節（這時由 Candidates 請求開啟的是同一份候選，ID 相同）。沒有未打完的注音、
// 或注音還不能完成時回傳 nil。
func (s *Session) PreviewPending() *proto.CandidateList {
	nine := s.ninePending() && len(s.nineReadings(s.nineKeys, nineToneKey)) > 0 // 九宮格的鍵同未打完的注音
	if !nine && (!s.toneless() || s.comp.Empty() || s.untonedSyllable(zhuyin.Syllable(s.comp.String())) == "") {
		return nil
	}
	saved := *s
	s.syls, s.locks = slices.Clone(s.syls), maps.Clone(s.locks)
	s.literals, s.displayed = maps.Clone(s.literals), maps.Clone(s.displayed)
	s.finishUntoned()
	s.openCandidates(0)
	out := s.snapshot().Candidates
	*s = saved
	return out
}

// untonedSeparator 處理聲調選填時的 '（負責人 2026-10-22 決定，同 RIME 的分隔鍵）：組字中按 ' 只切音節、不出頓號——
// 未打完而已是完整音節的注音不打聲調就完成，下一個注音開始新的音節（ㄒㄧ'ㄢ → 西安）；沒有未打完的注音（剛打完聲調）
// 或注音還不是完整音節時什麼都不做，免得組字中多出意外的頓號。沒有組字時照舊是頓號：句首不會有要切的音節，
// 頓號照常打得出來；組字中要頓號按 \ 或 Ctrl＋'。關閉聲調選填時 ' 照舊。
func (s *Session) untonedSeparator(k string) bool {
	if k != "'" || !s.toneless() || !s.composing() || s.english {
		return false
	}
	s.finishUntoned()
	return true
}

// untonedBreak 回報 sym 是否要先把目前的注音不打聲調就完成，再開始新的音節。
func (s *Session) untonedBreak(sym rune) bool {
	if !s.toneless() || s.comp.Empty() {
		return false
	}
	cl := zhuyin.ClassOf(sym)
	if cl != zhuyin.Initial && cl != zhuyin.Medial && cl != zhuyin.Final {
		return false
	}
	cur := zhuyin.Syllable(s.comp.String())
	if s.untonedSyllable(cur) == "" {
		return false // 還不是完整音節（也不能簡拼）：照原本的取代規則
	}
	if s.comp.Slot(cl) == 0 && lastClass(s.comp) < cl && zhuyin.LegalPrefix(zhuyin.Syllable(string(cur)+string(sym))) {
		return false // 接得上：ㄒㄧ＋ㄢ 是 ㄒㄧㄢ
	}
	return true
}

func lastClass(c zhuyin.Composer) zhuyin.Class {
	last := zhuyin.None
	for cl := zhuyin.Initial; cl <= zhuyin.Final; cl++ {
		if c.Slot(cl) != 0 {
			last = cl
		}
	}
	return last
}

// finishUntoned 把未打完、但已是完整音節（或能簡拼）的注音當成沒打聲調的音節收進組字。
// 沒開聲調選填、沒有未打完的注音，或注音還不是完整音節也不能簡拼時不動作，回傳 false。
func (s *Session) finishUntoned() bool {
	if s.ninePending() {
		return s.finishNine(nineToneKey) // 九宮格的鍵：不打聲調就完成（不看聲調選填的設定，九宮格本來就靠解碼定讀音）
	}
	if !s.toneless() || s.comp.Empty() {
		return false
	}
	done := s.untonedSyllable(zhuyin.Syllable(s.comp.String()))
	if done == "" {
		return false
	}
	before := s.comp
	s.comp.Reset()
	// Enter、↓、方向鍵這些按鍵平常不重新評分（音節沒變）；這裡多了一個音節，跟打聲調完成音節一樣讓神經評分，
	// 否則 Enter 送出的會是整段的統計結果，前面神經改過的字也被換回去。
	allow := s.allowRerank
	s.allowRerank = true
	s.completeSyllable(before, done)
	s.allowRerank = allow
	return true
}

// untonedKey 在聲調選填時，於會用到整段組字的按鍵之前完成未打完的音節（Enter、↓、方向鍵、Tab）。
func (s *Session) untonedKey(k string) {
	if !s.toneless() || s.comp.Empty() {
		return
	}
	switch k {
	case proto.KeyEnter, proto.KeyDown, proto.KeyLeft, proto.KeyRight, proto.KeyHome, proto.KeyEnd, proto.KeyTab:
		s.finishUntoned()
	}
}

// untonedRun 是聲調選填時從沒有未打完注音的狀態起、連續打在游標處的注音鍵（實體按鍵）。
// 聲調選填停用混打的自動英文判斷（負責人決定與混打二選一），只保留「我的詞彙」裡的英文詞（#146）：
// 這串按鍵恰好是使用者加的英文詞（大小寫相同）時，接空白、Enter 或標點就把它換回英文照字面送出，
// 跟混打的 ls␣ 一樣。打字當中畫面照注音解，換的時機在這個詞結束時。
type untonedRun struct {
	keys   string    // 實體按鍵
	starts []runMark // 每個沒有未打完注音的時間點：從那裡起的按鍵都可能是一個英文詞（ji3ls 的 ls）
	cursor int       // 打完這串按鍵後的游標、音節數與未打完的注音；下一鍵前有任何不同（退格、移游標）就不再接續
	syls   int
	comp   string
}

type runMark struct{ key, pos int } // keys[key:] 從音節位置 pos 開始

// untonedRunFrom 回傳這一鍵要接續的那串按鍵：上一鍵留下的、狀態沒變的接著用；否則從游標處開始新的一串
// （游標前還有不知道按鍵的未打完注音時，要等它完成才有起點）。沒開聲調選填或沒有自訂英文詞時回傳 nil。
func (s *Session) untonedRunFrom(run *untonedRun) *untonedRun {
	if !s.toneless() || s.mixedInput() || !s.Preview02 || !s.model.HasVocab() {
		return nil // 混打共存時（MixedWithToneless）英文詞由混打判斷
	}
	if run == nil || run.cursor != s.cursor || run.syls != len(s.syls) || run.comp != s.comp.String() {
		run = &untonedRun{}
	}
	next := *run
	if s.comp.Empty() {
		next.starts = append(slices.Clip(run.starts), runMark{len(run.keys), s.cursor})
	}
	return &next
}

// trackUntonedKey 記下這一鍵（空白是一聲，不算詞的一部分）。
func (s *Session) trackUntonedKey(run *untonedRun, k string) {
	if run == nil || k == " " || len(run.starts) == 0 {
		return
	}
	run.keys += k
	run.cursor, run.syls, run.comp = s.cursor, len(s.syls), s.comp.String()
	s.untonedRun = run
}

// untonedEnglish：空白、Enter 或標點鍵之前，從某個起點到游標的按鍵恰好是「我的詞彙」的英文詞時（取最長的），
// 把那一段換成英文字面文字。空白插入半形空白（不開候選、不是一聲）；Enter 與標點換完後照常處理，回傳 false。
func (s *Session) untonedEnglish(run *untonedRun, k string) bool {
	if run == nil || run.cursor != s.cursor || run.syls != len(s.syls) || run.comp != s.comp.String() {
		return false
	}
	rs := []rune(k)
	_, phonetic := s.layout.Symbol(rs[0])
	_, punct := s.layout.Punct(rs[0])
	if k != " " && k != proto.KeyEnter && !(len(rs) == 1 && punct && !phonetic) {
		return false
	}
	for _, m := range run.starts {
		word := run.keys[m.key:]
		if word == "" || !s.vocabEnglish(word) {
			continue
		}
		s.comp.Reset()
		for s.cursor > m.pos {
			s.eraseBeforeCursor()
		}
		s.insertLiteral(word)
		if k == " " {
			s.insertLiteral(" ")
			return true
		}
		return false
	}
	return false
}

// typedReading 回傳 [start, end) 的讀音：沒打聲調的音節換成目前顯示路徑在詞庫裡走的讀音。
// 有沒打聲調的音節而路徑對不上（例如那段還沒解出字）時回傳 false。
func (s *Session) typedReading(start, end int) ([]zhuyin.Syllable, bool) {
	syls := s.syls[start:end]
	if !zhuyin.HasUntoned(syls) {
		return syls, true
	}
	resolved, ok := s.resolvedSyllables()
	if !ok {
		return nil, false
	}
	out := resolved[start:end]
	return out, !zhuyin.HasUntoned(out)
}

// resolvedSyllables 回傳 s.syls 的複本，其中顯示路徑上每個詞的沒打聲調音節換成詞庫讀音（學習、加詞用）。
// 換不出來的位置保留記號，ok 為 false；呼叫端不得把帶記號的讀音寫進個人資料。
func (s *Session) resolvedSyllables() (out []zhuyin.Syllable, ok bool) {
	out = slices.Clone(s.syls)
	if !zhuyin.HasUntoned(out) {
		return out, true
	}
	lat := s.lattice()
	ok = true
	for _, g := range s.path {
		if g.End > len(out) || !zhuyin.HasUntoned(out[g.Start:g.End]) {
			continue
		}
		reading, found := []zhuyin.Syllable(nil), false
		if g.Matched != "" {
			reading, found = zhuyin.ParseReading(g.Matched), true
		} else {
			reading, found = decode.Reading(s.dec.Lex, lat, g.Start, g.End, g.Word)
		}
		if !found || len(reading) != g.End-g.Start {
			ok = false
			continue
		}
		copy(out[g.Start:g.End], reading)
	}
	return out, ok && !zhuyin.HasUntoned(out)
}

// lockReadings 回傳 resolved 的複本：仍帶記號、但被手選鎖定蓋住的位置換成那個手選詞自己的詞庫讀音。
// 包住手選的詞換不回讀音時（例如只有修正讀音的自動修正詞），手選本身照樣用它的讀音學習，跟打聲調時一樣；
// 換不回來的位置保留記號，照舊不學。只給提交時的學習用：路徑的段是否不學仍依 resolved 判斷。
func (s *Session) lockReadings(resolved []zhuyin.Syllable) []zhuyin.Syllable {
	if !zhuyin.HasUntoned(resolved) {
		return resolved
	}
	out := slices.Clone(resolved)
	var lat input.Lattice
	for at, lock := range s.locks {
		end := at + lock.Len
		if lock.LiteralBoundary != 0 || lock.Matched != "" || s.literals[at] || end > len(out) || !zhuyin.HasUntoned(out[at:end]) {
			continue
		}
		if lat == nil {
			lat = s.lattice()
		}
		if reading, ok := decode.Reading(s.dec.Lex, lat, at, end, lock.Word); ok && len(reading) == lock.Len {
			copy(out[at:end], reading)
		}
	}
	return out
}

// rejectedReadings 回傳要交給學習的被推翻區段：聲調選填時，被推翻的自動修正帶上那個修正詞條自己的讀音，
// 拒絕次數才記得到個人化視圖停用修正時查的鍵（打聲調時那段音節就是修正讀音，照舊不帶）。
func (s *Session) rejectedReadings() []personal.RejectedSeg {
	if !zhuyin.HasUntoned(s.syls) {
		return s.rejected
	}
	out := slices.Clone(s.rejected)
	var lat input.Lattice
	for i, r := range out {
		if !r.Correction || r.End > len(s.syls) || !zhuyin.HasUntoned(s.syls[r.Start:r.End]) {
			continue
		}
		if lat == nil {
			lat = s.lattice()
		}
		if reading, ok := decode.CorrectionReading(s.dec.Lex, lat, r.Start, r.End, r.Word); ok && len(reading) == r.End-r.Start {
			out[i].Reading = reading
		}
	}
	return out
}

// untonedTouches 回報 [start, end) 是否有讀音沒能換回詞庫讀音的沒打聲調音節；這種段落不學習。
func untonedTouches(resolved []zhuyin.Syllable, start, end int) bool {
	return end <= len(resolved) && zhuyin.HasUntoned(resolved[start:end])
}

// maxAbbreviatedReadings 是簡拼位置保留的讀音數：只打 ㄌ 時有上百個帶調音節，全部進入音節格會讓
// 連續簡拼的每鍵延遲超過手機的預算。依單音節的常用程度留前面的，打的注音本身是完整音節時一定保留；
// 排在後面的音節要打完整注音才找得到。
const maxAbbreviatedReadings = 64

// topAbbreviated 依 syllablePrior 留下簡拼音節 y 的位置最常用的 maxAbbreviatedReadings 個讀音，保持原本順序。
func (s *Session) topAbbreviated(y zhuyin.Syllable, row []input.Hyp) []input.Hyp {
	if len(row) <= maxAbbreviatedReadings {
		return row
	}
	typed := y.Base()
	ps := make([]float64, len(row))
	idx := make([]int, len(row))
	for i, h := range row {
		idx[i] = i
		ps[i] = s.syllablePrior(h.Syl) * math.Exp(-h.Cost)
		if h.Syl.Base() == typed {
			ps[i] = math.Inf(1) // 打的注音本身是完整音節（ㄕ 的 ㄕ、ㄕˊ……）
		}
	}
	sort.SliceStable(idx, func(a, b int) bool { return ps[idx[a]] > ps[idx[b]] })
	keep := idx[:maxAbbreviatedReadings]
	sort.Ints(keep)
	out := make([]input.Hyp, 0, len(keep))
	for _, i := range keep {
		out = append(out, row[i])
	}
	return out
}

// syllablePrior 是帶調音節的常用程度：讀這個音的系統單字中，無上下文機率乘讀音權重最高的一筆。
// 系統詞庫不變，記在 session 裡；個人學習不影響簡拼保留哪些音節。
func (s *Session) syllablePrior(y zhuyin.Syllable) float64 {
	if p, ok := s.sylPrior[y]; ok {
		return p
	}
	if s.sylPrior == nil {
		s.sylPrior = map[zhuyin.Syllable]float64{}
	}
	best := 0.0
	for _, e := range s.model.Lex.Exact([]zhuyin.Syllable{y}) {
		best = max(best, s.model.SystemProb(lm.BOS, e.Surface)*e.PRW)
	}
	s.sylPrior[y] = best
	return best
}
