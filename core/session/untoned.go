package session

import (
	"slices"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 聲調選填（#150，設定「不打聲調也能選字」，預設關閉；docs/50）。
//
// 開啟時，注音照「聲母 介音 韻母」的順序接在目前的音節後面；接不上（同類已有、順序在前面，
// 或接上去就不是任何音節的開頭）而目前的注音已經是完整音節時，目前的音節不打聲調就完成，
// 這個注音開始新的音節：ㄋㄧㄏㄠ 是 ㄋㄧ＋ㄏㄠ。沒打聲調的音節在音節格裡展開成五個聲調，由解碼決定；
// 有打聲調的音節照舊硬限制，而且固定那個音節的開頭和結尾。
//
// 目前的注音還不是完整音節時照原本的規則（同類的後者取代前者：ㄋ＋ㄏ 是 ㄏ），所以打錯聲母仍可直接改。
// Enter、↓、方向鍵、Tab、標點與切換英文之前，未打完但已是完整音節的注音也先不打聲調就完成；
// 空白照舊是一聲。關閉時沒有任何音節會帶 zhuyin.UntonedMark，行為與沒有此功能時相同。

// untonedBreak 回報 sym 是否要先把目前的注音不打聲調就完成，再開始新的音節。
func (s *Session) untonedBreak(sym rune) bool {
	if !s.Toneless || s.comp.Empty() {
		return false
	}
	cl := zhuyin.ClassOf(sym)
	if cl != zhuyin.Initial && cl != zhuyin.Medial && cl != zhuyin.Final {
		return false
	}
	cur := zhuyin.Syllable(s.comp.String())
	if !cur.Legal() {
		return false // 還不是完整音節：照原本的取代規則
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

// finishUntoned 把未打完、但已是完整音節的注音當成沒打聲調的音節收進組字。
// 沒開聲調選填、沒有未打完的注音，或注音還不是完整音節時不動作，回傳 false。
func (s *Session) finishUntoned() bool {
	if !s.Toneless || s.comp.Empty() {
		return false
	}
	base := zhuyin.Syllable(s.comp.String())
	if !base.Legal() {
		return false
	}
	before := s.comp
	s.comp.Reset()
	// Enter、↓、方向鍵這些按鍵平常不重新評分（音節沒變）；這裡多了一個音節，跟打聲調完成音節一樣讓神經評分，
	// 否則 Enter 送出的會是整段的統計結果，前面神經改過的字也被換回去。
	allow := s.allowRerank
	s.allowRerank = true
	s.completeSyllable(before, zhuyin.Untone(base))
	s.allowRerank = allow
	return true
}

// untonedKey 在聲調選填時，於會用到整段組字的按鍵之前完成未打完的音節（Enter、↓、方向鍵、Tab）。
func (s *Session) untonedKey(k string) {
	if !s.Toneless || s.comp.Empty() {
		return
	}
	switch k {
	case proto.KeyEnter, proto.KeyDown, proto.KeyLeft, proto.KeyRight, proto.KeyHome, proto.KeyEnd, proto.KeyTab:
		s.finishUntoned()
	}
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

// untonedTouches 回報 [start, end) 是否有讀音沒能換回詞庫讀音的沒打聲調音節；這種段落不學習。
func untonedTouches(resolved []zhuyin.Syllable, start, end int) bool {
	return end <= len(resolved) && zhuyin.HasUntoned(resolved[start:end])
}
