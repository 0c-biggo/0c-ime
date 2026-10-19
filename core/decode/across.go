package decode

import "git.biggo.com/0c/ime/core/input"

// CompleteAcross 找從 path[from] 起、跨過後面幾個分段的補全：畫面上 path[from:] 的字是某個詞的開頭（#143）。
// Completion 與 Complete 相同，只在「從這裡開始的詞」之間比較；解碼器把這段切開，表示切開的讀法可能比較好，
// 所以另外回傳這段兩種讀法的比例，由呼叫端決定怎麼用：
//
//	合起來的詞群 ÷（合起來的詞群 ＋ 目前路徑在這段）
//
// 合起來的詞群是從 path[from] 起、開頭與畫面相同的詞；目前路徑是 path[from:] 前面各完整詞的機率，乘上最後一段
// 的詞群（與 Complete 相同）。兩個詞群裡比已打部分更長的詞都和解碼尾端一樣乘上 Continue。只用系統先驗（明確修正照常扣分）：
// 學過的切法改變解碼結果，不改變這個比例。
func (d *Decoder) CompleteAcross(lat input.Lattice, path []Seg, from int) (Completion, float64, bool) {
	if from < 0 || from >= len(path)-1 {
		return Completion{}, 0, false
	}
	var displayed string
	for _, seg := range path[from:] {
		displayed += seg.Word
	}
	merged := append(path[:from:from], Seg{Word: displayed, Start: path[from].Start, End: len(lat), Partial: true})
	c, group, ok := d.complete(lat, merged)
	if !ok {
		return Completion{}, 0, false
	}
	_, current, ok := d.complete(lat, path)
	if !ok {
		return Completion{}, 0, false
	}
	ev, _ := d.Score.(Evidenced)
	left := d.InitialLeft()
	if from > 0 {
		left = path[from-1].Word
	}
	for _, seg := range path[from : len(path)-1] {
		p := d.Score.Prob(left, seg.Word)
		if ev != nil {
			p = ev.SystemProb(left, seg.Word)
		}
		current *= p * d.correctionFactor(seg.Start, seg.End, seg.Word)
		left = seg.Word
	}
	return c, group / (group + current), true
}
