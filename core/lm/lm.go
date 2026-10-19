// Package lm 提供系統語言模型：unigram 與 bigram 機率。
package lm

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// BOS 是句首的虛擬詞。
const BOS = "<s>"

// Unseen 是未登錄詞的 unigram 機率。
const Unseen = 1e-8

// System 是系統語言模型。
type System interface {
	// Uni 回傳 P(w)；未登錄回傳 Unseen。
	Uni(w string) float64
	// Bi 回傳 P(w | left)；沒有此 bigram 回傳 0。
	Bi(left, w string) float64
}

// Mem 是記憶體內實作。
type Mem struct {
	uni map[string]float64
	bi  map[[2]string]float64
}

func NewMem() *Mem { return &Mem{uni: map[string]float64{}, bi: map[[2]string]float64{}} }

func (m *Mem) SetUni(w string, p float64)      { m.uni[w] = p }
func (m *Mem) SetBi(left, w string, p float64) { m.bi[[2]string{left, w}] = p }

func (m *Mem) Uni(w string) float64 {
	if p, ok := m.uni[w]; ok {
		return p
	}
	return Unseen
}

func (m *Mem) Bi(left, w string) float64 { return m.bi[[2]string{left, w}] }

// HasLeft 回報是否有以 left 為前詞的 bigram。
func (m *Mem) HasLeft(left string) bool {
	for k := range m.bi {
		if k[0] == left {
			return true
		}
	}
	return false
}

// Load 讀取 TSV。兩欄（詞、機率）為 unigram；三欄（前詞、詞、機率）為 bigram。
func Load(r io.Reader) (*Mem, error) {
	m := NewMem()
	sc := bufio.NewScanner(r)
	for ln := 1; sc.Scan(); ln++ {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 2 && len(f) != 3 {
			return nil, fmt.Errorf("第 %d 行：需要 2 或 3 欄", ln)
		}
		p, err := strconv.ParseFloat(f[len(f)-1], 64)
		if err != nil || p <= 0 || p > 1 {
			return nil, fmt.Errorf("第 %d 行：機率 %q 須在 (0,1]", ln, f[len(f)-1])
		}
		if len(f) == 2 {
			m.SetUni(f[0], p)
		} else {
			m.SetBi(f[0], f[1], p)
		}
	}
	return m, sc.Err()
}
