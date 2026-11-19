package session

import (
	"fmt"
	"strings"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 簡拼音節的位置不准評分器改讀音。開了簡拼時，沒打聲調的 ㄧ ㄨ ㄩ ㄓ ㄔ ㄕ ㄖ ㄗ ㄘ ㄙ 是「以它開頭的所有音節」，
// 送給評分器的卻是那個完整音節（rerank.MaskTones），評分器會把「ㄧ 簡拼 ㄧㄠ」推回「ㄧ」。照原樣送時，校準組改成
// 只打首音的 1,155 例比不用模型差 2.1～2.5pp；完全不送又丟掉完整讀音時的增益。所以照樣送評分，但候選在簡拼位置的
// 詞庫讀音（基底）跟統計首選不同的不能贏：評分器只能在同一個讀法裡換字。量測與取捨見 commit 訊息。
//
// 注音九宮格（ninekey.go）的位置同理：送出的是統計首選走的那個讀音，評分器不准把它換成同一串鍵的另一個拼法。

// readingsAt 回傳候選 r（段落從 start 起）在 at（絕對位置）各音節的詞庫讀音；有一個解不出來 ok 就是 false。
func (s *Session) readingsAt(lat input.Lattice, start int, r decode.Hypothesis, at []int) ([]zhuyin.Syllable, bool) {
	out := make([]zhuyin.Syllable, len(at))
	found := 0
	for _, g := range r.Segments {
		gs, ge := g.Start+start, g.End+start
		var reading []zhuyin.Syllable
		for k, i := range at {
			if i < gs || i >= ge {
				continue
			}
			if reading == nil {
				ok := false
				if g.Matched != "" {
					reading, ok = zhuyin.ParseReading(g.Matched), true
				} else {
					reading, ok = decode.Reading(s.dec.Lex, lat, gs, ge, g.Word)
				}
				if !ok || len(reading) != ge-gs {
					return nil, false
				}
			}
			out[k] = reading[i-gs]
			found++
		}
	}
	return out, found == len(at)
}

// vetoAbbrevReadings 標出在簡拼位置（與九宮格位置）換了讀音的候選：詞庫讀音的基底跟統計首選 rows[0] 不同，或解不出來。
// 段內沒有簡拼音節時回傳 nil、true；統計首選本身解不出讀音時 ok 為 false，整段照統計引擎。
func (s *Session) vetoAbbrevReadings(lat input.Lattice, start, end int, rows []decode.Hypothesis) (vetoed []bool, ok bool) {
	var at []int
	for i := start; i < end; i++ {
		if s.syls[i].Abbreviated() || len(s.nine[i]) > 0 {
			at = append(at, i)
		}
	}
	if len(at) == 0 || len(rows) == 0 {
		return nil, true
	}
	first, ok := s.readingsAt(lat, start, rows[0], at)
	if !ok {
		return nil, false
	}
	for i := 1; i < len(rows); i++ {
		r, ok := s.readingsAt(lat, start, rows[i], at)
		same := ok
		for k := 0; same && k < len(at); k++ {
			same = r[k].Base() == first[k].Base()
		}
		if !same {
			if vetoed == nil {
				vetoed = make([]bool, len(rows))
			}
			vetoed[i] = true
		}
	}
	return vetoed, true
}

// vetoAbbrev 是 rerankSegment 呼叫的 vetoAbbrevReadings；測試換成計數的版本，確認快取命中時不再算。
var vetoAbbrev = (*Session).vetoAbbrevReadings

// abbrevVetoKey 是 vetoAbbrevReadings 除了候選文字與分段以外依賴的輸入，併進重排快取的鍵：段內每個音節的完整寫法
// （含簡拼與沒打聲調的標記；遮罩過的讀音分不出簡拼）、九宮格位置的讀音，以及候選各段記下的詞庫讀音（Matched）。
// 音節格其他的假設（近音、鄰鍵）都有成本又不是打的音，decode.Reading 不看。段內沒有簡拼或九宮格位置時檢查一定通過，
// 回傳空字串。
func (s *Session) abbrevVetoKey(start, end int, rows []decode.Hypothesis) string {
	var b strings.Builder
	for i := start; i < end; i++ {
		if s.syls[i].Abbreviated() || len(s.nine[i]) > 0 {
			if b.Len() == 0 {
				b.WriteString("#abbrev|")
				for j := start; j < end; j++ {
					b.WriteString(string(s.syls[j]))
					b.WriteByte(' ')
				}
			}
			fmt.Fprintf(&b, "|%d:%s", i, zhuyin.Key(s.nine[i]))
		}
	}
	if b.Len() == 0 {
		return ""
	}
	for _, r := range rows {
		b.WriteByte('|')
		for _, g := range r.Segments {
			b.WriteString(g.Matched)
			b.WriteByte(';')
		}
	}
	return b.String()
}

// mergeVetoes 把 extra 併進 vetoed（任一為 true 就排除）；兩者都沒有排除時回傳 nil。
func mergeVetoes(vetoed, extra []bool) []bool {
	if extra == nil {
		return vetoed
	}
	out := make([]bool, len(extra))
	for i := range extra {
		out[i] = extra[i] || i < len(vetoed) && vetoed[i]
	}
	return out
}
