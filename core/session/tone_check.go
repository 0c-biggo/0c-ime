package session

import "github.com/0c-biggo/0c-ime/core/zhuyin"

// refusedTone 是被「打錯注音時提示」擋下的聲調（#300）：按聲調前的注音、聲調與游標位置。
type refusedTone struct {
	reading string
	tone    int
	cursor  int
}

// refuseTone 依「打錯注音時提示」（#300）決定要不要擋下剛按的聲調。只在收進 done 會讓整段解不出字、
// 本來要原樣留下（#109）的時候才問；讀音 done 自己有字、是某個詞的開頭（#107 的 ㄟ）、接得上前面的字，
// 或開了模糊音時近音有字，都不擋。擋下時注音停在暫存（before，不帶聲調），快照的 Bell 要外殼提示。
//
// 只有一個符號的注音（ㄟ、ㄏ＋聲調、ㄅ＋空白）不擋，照舊原樣留下（#109）：聊天常打「ㄟ你在哪裡」，
// 單一符號多半是故意的注音文，擋下後接著打的注音會全疊到同一個音節上（ㄟ＋ㄋㄧˇ…＝倆）。
//
// 故意打注音文的退路：緊接著在同一處、同一個注音再按同一個聲調（上一個請求擋下的 toneRetry）就不擋，
// 照舊原樣留下，ㄏㄟˊ 多按一次仍打得出來，#85 的緊接改韻母也照舊。中英混打與聲調選填不作用。
func (s *Session) refuseTone(before zhuyin.Composer, done zhuyin.Syllable) bool {
	if !s.ZhuyinErrorHint || s.mixedInput() || s.toneless() || s.pinyinLayout() || done.Untoned() || singleSymbol(&before) {
		return false
	}
	at := refusedTone{reading: before.String(), tone: done.ToneOf(), cursor: s.cursor}
	if s.toneRetry != nil && *s.toneRetry == at {
		return false
	}
	if s.readingReachable(done) || s.nearReachable(done) {
		return false
	}
	s.comp = before
	s.toneRefused = &at
	s.bell = true
	return true
}

// singleSymbol：注音暫存的聲母、介音、韻母只有一個有符號。
func singleSymbol(c *zhuyin.Composer) bool {
	n := 0
	for cl := zhuyin.Initial; cl <= zhuyin.Final; cl++ {
		if c.Slot(cl) != 0 {
			n++
		}
	}
	return n == 1
}

// nearReachable：開了模糊音時，done 的近音有字或是某個詞的開頭。
func (s *Session) nearReachable(done zhuyin.Syllable) bool {
	for _, alt := range s.Fuzzy.Alternatives(done) {
		seq := []zhuyin.Syllable{alt.Syl}
		if len(s.model.Source().Exact(seq)) > 0 || s.model.Source().HasPrefix(seq) {
			return true
		}
	}
	return false
}
