// Package input 定義輸入層交給解碼器的契約：帶成本的音節格。
//
// 鍵盤精確輸入時每個位置只有一個音節、成本 0；容錯通道、部分注音與未來的語音前端
// 會在同一位置放入多個帶成本的假設。解碼器對所有前端一視同仁。
package input

import "git.biggo.com/0c/ime/core/zhuyin"

// Hyp 是某個位置的一個音節假設。Cost 為負對數機率，0 表示確定。
type Hyp struct {
	Syl  zhuyin.Syllable
	Cost float64
}

// Lattice 的每個元素是一個位置的所有假設。
type Lattice [][]Hyp

// Frontend 把使用者輸入的音節序列展開成音節格。
type Frontend interface {
	Expand(syls []zhuyin.Syllable) Lattice
}

// Exact 是精確通道：使用者打什麼就是什麼。
type Exact struct{}

func (Exact) Expand(syls []zhuyin.Syllable) Lattice {
	lat := make(Lattice, len(syls))
	for i, s := range syls {
		lat[i] = []Hyp{{Syl: s}}
	}
	return lat
}
