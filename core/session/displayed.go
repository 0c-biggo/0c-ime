package session

import (
	"slices"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

func (s *Session) shiftDisplayed(at, delta int) {
	if len(s.displayed) == 0 {
		return
	}
	next := map[int]string{}
	for pos, text := range s.displayed {
		switch {
		case pos < at:
			next[pos] = text
		case delta > 0 || pos > at:
			next[pos+delta] = text
		}
	}
	s.displayed = next
}

func (s *Session) fallbackDisplay(pos int, syl zhuyin.Syllable) (string, string) {
	if s.literals[pos] {
		return string(syl), proto.StyleNormal
	}
	// 例如詞庫只有「麻煩」的完整讀音：插入英數後不能把已顯示的「煩」變回注音。
	// 保底文字僅供顯示／提交，不冒充可解碼路徑，也不學成新的單字讀音。
	// 近音保底（nearOnly）同理：不能因為後面多了一個只能靠近音成詞的音節，就把前面已顯示的字變回注音。
	if (len(s.literals) > 0 || s.nearReading) && s.displayed[pos] != "" {
		return s.displayed[pos], proto.StyleNormal
	}
	if s.nearReading && s.nearShown[pos] != "" {
		return s.nearShown[pos], proto.StyleNormal
	}
	return syl.Text(), proto.StylePending
}

// decodeAroundUnreadable 在整段解不出精確路徑時，把精確讀音沒有字的音節（只能靠近音成詞，例如
// 打錯聲母的 ㄗㄨㄞˇ）當成邊界，其餘各段照常解碼、照常送神經選字，回傳各位置的保底文字。否則一個這樣的
// 音節會讓後面打的每個音節都只顯示注音、提交也是注音（使用者回報「揣摩上意」打成 ㄗㄨㄞˇㄇㄛˊㄕㄤˋㄧˋ）。
// 結果只供顯示／提交，不是解碼路徑：不學習、不進個人資料，那個音節仍要開候選選近音或改打。
func (s *Session) decodeAroundUnreadable() map[int]string {
	lat := s.lattice()
	readable := func(i int) bool {
		for _, h := range lat[i] {
			if len(s.model.Source().Exact([]zhuyin.Syllable{h.Syl})) > 0 {
				return true
			}
		}
		return false
	}
	// 只是顯示用的解碼，不能改掉候選查詢沿用的前文、證據與更正設定；神經選字借用 path 逐段送出，
	// 完成後還原（保底狀態沒有路徑，也不留神經次佳提示）。
	saved, path, hint, left := *s.dec, s.path, s.hint, s.neuralLeft
	defer func() { *s.dec, s.path, s.hint, s.neuralLeft = saved, path, hint, left }()
	var segs []decode.Seg // 字面符號與各段解出的詞，依位置排列
	var runs [][2]int
	for start := 0; start < len(lat); {
		if s.literals[start] {
			segs = append(segs, decode.Seg{Word: string(s.syls[start]), Start: start, End: start + 1})
			start++
			continue
		}
		if !readable(start) {
			start++
			continue
		}
		end := start + 1
		for end < len(lat) && !s.literals[end] && readable(end) {
			end++
		}
		s.configureDocumentContext(start) // 左邊是原樣注音時 start > 0，沒有文件前文
		s.dec.Evidence = nil
		s.configureCorrections(start, end, false)
		part := s.dec.Decode(lat[start:end], s.locksIn(start, end), s.Opt.PrefixAware)
		for _, g := range part {
			g.Start, g.End = g.Start+start, g.End+start
			segs = append(segs, g)
		}
		if len(part) > 0 {
			runs = append(runs, [2]int{start, end})
		}
		start = end
	}
	if s.rerankAllowed() {
		for _, r := range runs {
			if !s.rerankable(r[0], r[1]) {
				continue
			}
			// 前文只到最近一個沒有字的音節為止：那裡是原樣注音，接不上文件前文與更前面的字。
			from := 0
			for i := r[0] - 1; i >= 0; i-- {
				if !s.literals[i] && !readable(i) {
					from = i + 1
					break
				}
			}
			s.neuralLeft = left
			if from > 0 {
				s.neuralLeft = ""
			}
			s.path = slices.DeleteFunc(slices.Clone(segs), func(g decode.Seg) bool { return g.Start < from || g.End > r[1] })
			s.rerankSegment(lat, r[0], r[1])
			chosen := slices.DeleteFunc(s.path, func(g decode.Seg) bool { return g.Start < r[0] })
			i := slices.IndexFunc(segs, func(g decode.Seg) bool { return g.Start >= r[0] })
			j := slices.IndexFunc(segs, func(g decode.Seg) bool { return g.Start >= r[1] })
			if j < 0 {
				j = len(segs)
			}
			segs = slices.Concat(segs[:i], chosen, segs[j:])
		}
	}
	shown := map[int]string{}
	for _, g := range segs {
		if s.literals[g.Start] {
			continue
		}
		for i, r := range []rune(g.Word) {
			shown[g.Start+i] = string(r)
		}
	}
	return shown
}
