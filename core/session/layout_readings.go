package session

import (
	"slices"

	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/layout"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 排列本身的歧義讀音（#183）：許氏單打 a 再按聲調可以是 ㄘ 也可以是 ㄟ，e 是 ㄧ 或 ㄝ，l 是 ㄦ 或 ㄥ。
// 兩種讀音都是使用者按的鍵，不是近音：音節格在這個位置放進同一串按鍵的所有讀音，預設讀音成本 0、
// 其他讀音 layout.AltCost，由解碼依前後文挑。
//
// 音節格一律以預設讀音為第一個假設（不看 syls 目前是哪一個），所以解碼結果不會因為上一次挑了哪個讀音而改變。
// 解碼器回報走了非預設讀音的詞（Seg.Matched）；absorbReadings 把挑中的讀音寫回 syls、清掉 Matched，
// 之後的顯示、學習與簡體轉換都看到真正的讀音，不會被當成近音（不標示修正、照常學習）。

// expand 是組字的音節格：使用者打的音，加上排列的歧義讀音。
func (s *Session) expand(syls []zhuyin.Syllable) input.Lattice {
	lat := s.front.Expand(syls)
	for i, y := range syls {
		if s.literals[i] {
			continue
		}
		readings := s.layout.Readings(y)
		if len(readings) < 2 {
			continue
		}
		row := []input.Hyp{{Syl: readings[0]}}
		for _, r := range readings[1:] {
			row = append(row, input.Hyp{Syl: r, Cost: layout.AltCost})
		}
		for _, h := range lat[i] {
			if !slices.ContainsFunc(row, func(x input.Hyp) bool { return x.Syl == h.Syl }) {
				row = append(row, h)
			}
		}
		lat[i] = row
	}
	return lat
}

// absorbReadings 把路徑在歧義位置挑中的讀音寫回 syls；只因歧義讀音而不同的詞清掉 Matched。
// 回報 syls 是否改變。
func (s *Session) absorbReadings() bool {
	changed := false
	for i, g := range s.path {
		if g.End > len(s.syls) || s.literals[g.Start] {
			continue
		}
		var matched []zhuyin.Syllable
		if g.Matched != "" {
			matched = zhuyin.ParseReading(g.Matched)
			if len(matched) != g.End-g.Start {
				continue
			}
		}
		onlyAlias := true
		for at := g.Start; at < g.End; at++ {
			readings := s.layout.Readings(s.syls[at])
			used := zhuyin.Syllable("")
			if matched != nil {
				used = matched[at-g.Start]
			}
			if len(readings) < 2 {
				onlyAlias = onlyAlias && (used == "" || used == s.syls[at])
				continue
			}
			if used == "" {
				used = readings[0] // 零成本的弧走的是第一個假設，也就是預設讀音
			}
			if !slices.Contains(readings, used) {
				onlyAlias = false // 近音（模糊音）換到別的讀音：照舊由近音的規則處理
				continue
			}
			if s.syls[at] != used {
				if !changed {
					s.syls = slices.Clone(s.syls) // 不改到存起來準備還原的切片
				}
				s.syls[at] = used
				changed = true
			}
		}
		if matched != nil && onlyAlias {
			s.path[i].Matched = ""
		}
	}
	return changed
}
