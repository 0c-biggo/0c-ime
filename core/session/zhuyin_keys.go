package session

import (
	"slices"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 選過的注音文（#208，紀錄的範圍與理由見 personal/zhuyin_keys.go）。

// zhuyinKeyOrigin：組字區裡這格注音符號是怎麼來的。c␣ 這種預設就是注音的沒有標記，提交時不動紀錄。
type zhuyinKeyOrigin uint8

const (
	zhuyinKeyPicked  zhuyinKeyOrigin = iota + 1 // 使用者這次選的
	zhuyinKeyApplied                            // 選過的紀錄打出來的
)

func (s *Session) markZhuyinKey(at int, origin zhuyinKeyOrigin) {
	if s.mixedOrigins == nil {
		s.mixedOrigins = map[int]mixedOrigin{}
	}
	o := s.mixedOrigins[at]
	o.zhuyinKey = origin
	s.mixedOrigins[at] = o
}

// keySymbol：單一個鍵在目前排列的聲母、介音或韻母符號；聲調鍵與不是注音的鍵不算。
func (s *Session) keySymbol(key string) (rune, bool) {
	if len(key) != 1 {
		return 0, false
	}
	sym, ok := s.layout.Symbol(rune(key[0]))
	if !ok || zhuyin.ClassOf(sym) == zhuyin.Tone || zhuyin.ClassOf(sym) == zhuyin.None {
		return 0, false
	}
	return sym, true
}

// zhuyinLeft 是紀錄用的前字：句首是空字串，前面是中文時是畫面上的那個字。前面是空白、英數、標點或注音時回 false。
func (s *Session) zhuyinLeft(at int) (string, bool) {
	if at == 0 {
		return "", true
	}
	if at < 0 || at > len(s.syls) || s.literals[at-1] {
		return "", false
	}
	for _, g := range s.path {
		if g.Start < at && at <= g.End {
			if word := []rune(g.Word); at-1-g.Start < len(word) {
				return string(word[at-1-g.Start]), true
			}
		}
	}
	return "", false
}

// zhuyinKeyHabit：待定的單一注音鍵接空白時要不要直接出注音。沒有紀錄的個人資料第一個判斷就結束；私人欄位、暫停時不套用（個人英文同一條件）。
func (s *Session) zhuyinKeyHabit(raw string) bool {
	if !s.model.HasZhuyinKeys() || s.mixed.choice != 0 || s.mixed.accepted != "" || s.englishWord || !s.personalEnglishAllowed() {
		return false
	}
	sym, ok := s.keySymbol(raw)
	if !ok {
		return false
	}
	left, ok := s.zhuyinLeft(s.cursor)
	return ok && s.model.ZhuyinKeyPicked(left, string(sym))
}

// pickKeySymbol：候選選的是單一注音鍵對應的注音符號時換成注音，回傳 true；其他選擇回 false，照一般符號處理。呼叫時游標在 start＋length。
//   - 照字面留下的鍵（我2 的 2）：後面緊接的一個空白一併拿掉（我2 天 → 我ㄉ天），打這個空白本來就是要出這個注音；要空白再按一次。
//   - 鍵接空白出的中文字（-␣ 兒、t␣ 吃）選了最後的原樣注音：沒有空白要拿。
//
// 兩種都留成原樣音節、記下原按鍵，跟 c␣ 的 ㄏ 一樣 ↓ 第二項選得回去（2␣，空白也回來）；有空白的才算選過「按鍵＋空白」。
func (s *Session) pickKeySymbol(start, length int, text string) bool {
	if length != 1 {
		return false
	}
	var keys string
	end := start + 1
	if s.literals[start] {
		sym, ok := s.lonePhoneticKeyAt(start)
		if !ok || string(sym) != text {
			return false
		}
		keys = string(s.syls[start])
		if next := start + 1; next < len(s.syls) && s.literals[next] && s.syls[next] == " " {
			keys += " "
			end = next + 1
		}
	} else {
		keys = s.mixedKeys[start]
		sym, ok := s.keySymbol(keys[:min(1, len(keys))])
		if !ok || len(keys) != 2 || keys[1] != ' ' || string(sym) != text {
			return false
		}
	}
	comp, ok := s.mixedComposer(keys[:1])
	if !ok {
		return false
	}
	before := comp.Clone()
	done, ok := comp.Add(zhuyin.Tone1)
	if !ok {
		return false
	}
	shown := s.shownBeforeLiteralEdit(s.literals[start])
	for s.cursor = end; s.cursor > start; {
		s.eraseBeforeCursor()
	}
	s.insertSyllableLiteral(before, done)
	if s.mixedKeys == nil {
		s.mixedKeys = map[int]string{}
	}
	s.mixedKeys[start] = keys
	if len(keys) == 2 {
		s.markZhuyinKey(start, zhuyinKeyPicked)
	}
	s.keepLiteralEdit(shown, start, end)
	return true
}

// shownBeforeLiteralEdit 記下字面換字面之前的畫面。候選選取不重新評分（rerankAllowed），重新解碼會把神經選過的字換回統計首選（真窩2 → 真我ㄉ）；
// 只動字面文字時兩邊的字都不該變，比照 flushMixed 保留。沒有評分器時兩邊本來就解出一樣的字，不必多做。
func (s *Session) shownBeforeLiteralEdit(literal bool) []decode.Seg {
	if !literal || s.Rerank.Scorer == nil {
		return nil
	}
	return slices.Clone(s.path)
}

// keepLiteralEdit：字面區間 [start, end) 已換成游標前的新字面文字，其餘照 shown 顯示。shown 是 nil 時不動。
func (s *Session) keepLiteralEdit(shown []decode.Seg, start, end int) {
	if shown == nil {
		return
	}
	var path []decode.Seg
	for _, g := range shown {
		switch {
		case g.End <= start:
			path = append(path, g)
		case g.Start >= end:
			g.Start, g.End = g.Start-(end-start), g.End-(end-start)
			path = append(path, g)
		}
	}
	s.refresh()
	s.keepMixedDisplay(path, start, s.cursor-start)
}

// zhuyinKeyCommits 依提交時畫面最後的結果列出事件：標記過、而且還是當初那個符號的注音（換了聲調、刪掉重打的，標記已經跟著消失），
// 以及明確選了原按鍵、後面接空白的單一注音鍵（↓ 選回 2、打的當下 ↓ 選 2 再按空白）。其他都沒有事件。
func (s *Session) zhuyinKeyCommits() []personal.ZhuyinKeyCommit {
	var out []personal.ZhuyinKeyCommit
	for at := range s.syls {
		origin, ok := s.mixedOrigins[at]
		if !ok || !s.literals[at] || origin.zhuyinKey == 0 && !origin.explicit {
			continue
		}
		left, ok := s.zhuyinLeft(at)
		if !ok {
			continue
		}
		event := personal.ZhuyinKeyCommit{Left: left}
		if origin.zhuyinKey != 0 {
			keys := s.mixedKeys[at]
			sym, ok := s.keySymbol(keys[:min(1, len(keys))])
			if !ok || len(keys) != 2 || keys[1] != ' ' || string(s.syls[at]) != string(sym) {
				continue
			}
			event.Symbol, event.Action = string(sym), personal.ZhuyinKeyKeep
			if origin.zhuyinKey == zhuyinKeyPicked {
				event.Action = personal.ZhuyinKeyPick
			}
		} else {
			sym, ok := s.keySymbol(string(s.syls[at]))
			if !ok || at+1 >= len(s.syls) || !s.literals[at+1] || s.syls[at+1] != " " {
				continue
			}
			event.Symbol, event.Action = string(sym), personal.ZhuyinKeyDrop
		}
		out = append(out, event)
	}
	return out
}
