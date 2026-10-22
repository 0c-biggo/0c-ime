// Package fuzzy 定義模糊音規則：ㄗ／ㄓ、ㄘ／ㄔ、ㄙ／ㄕ、ㄣ／ㄥ 的逐對近音展開（issue #45）。
//
// 規則只在「已完成的音節」上產生替代假設：保留聲調、不改動其餘符號，且結果必須是
// 現行音節表（zhuyin.Legal）內的合法音節；原音永遠是第一個假設、成本 0。
// 本套件是純規則：不載入詞庫、不依賴模型，也不決定顯示什麼——顯示基準由 session 維持。
package fuzzy

import (
	"strings"

	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// Pair 是一組可逐對開關的近音配對。
type Pair int

const (
	ZZh   Pair = iota // ㄗ／ㄓ
	CCh               // ㄘ／ㄔ
	SSh               // ㄙ／ㄕ
	EnEng             // ㄣ／ㄥ
	numPairs
)

var pairs = [numPairs]struct {
	id    string
	a, b  rune
	final bool // 換的是韻母（ㄣ／ㄥ）而非聲母
}{
	{"z-zh", 'ㄗ', 'ㄓ', false},
	{"c-ch", 'ㄘ', 'ㄔ', false},
	{"s-sh", 'ㄙ', 'ㄕ', false},
	{"en-eng", 'ㄣ', 'ㄥ', true},
}

// Pairs 依固定順序列出所有配對。
func Pairs() []Pair { return []Pair{ZZh, CCh, SSh, EnEng} }

// ID 是設定與診斷用的穩定代號。
func (p Pair) ID() string { return pairs[p].id }

// Symbols 回傳配對的兩個符號，供說明文字使用，例如「ㄗ／ㄓ」。
func (p Pair) Symbols() string { return string(pairs[p].a) + "／" + string(pairs[p].b) }

// ParsePair 由設定代號找配對。
func ParsePair(id string) (Pair, bool) {
	for _, p := range Pairs() {
		if p.ID() == id {
			return p, true
		}
	}
	return 0, false
}

// Mode 是一組配對的啟用方式。
type Mode int

const (
	// Off 不展開，行為與沒有此功能時相同。這也是缺少設定時的預設。
	Off Mode = iota
	// Select 只在開候選時合併近音候選；自動顯示與提交維持原音。
	Select
	// Auto 除了 Select 的行為，另在近音路徑的語境優勢超過門檻（見 AutoPolicy）時，自動改未提交的組字。
	Auto
)

func (m Mode) String() string {
	switch m {
	case Select:
		return "select"
	case Auto:
		return "auto"
	}
	return "off"
}

// ParseMode 由設定值找模式。
func ParseMode(s string) (Mode, bool) {
	switch s {
	case "off":
		return Off, true
	case "select":
		return Select, true
	case "auto":
		return Auto, true
	}
	return Off, false
}

// Config 是各配對的模式。零值全部關閉。
type Config struct{ modes [numPairs]Mode }

// With 回傳把配對 p 設為 m 的新設定。
func (c Config) With(p Pair, m Mode) Config {
	c.modes[p] = m
	return c
}

// Mode 回傳配對 p 目前的模式。
func (c Config) Mode(p Pair) Mode { return c.modes[p] }

// Enabled 回報是否有任何配對啟用。
func (c Config) Enabled() bool {
	for _, m := range c.modes {
		if m != Off {
			return true
		}
	}
	return false
}

// AutoEnabled 回報是否有任何配對啟用自動修正。
func (c Config) AutoEnabled() bool {
	for _, m := range c.modes {
		if m == Auto {
			return true
		}
	}
	return false
}

// AutoOnly 回傳只保留自動修正配對的設定；選字用的配對在自動修正的搜尋中不展開。
func (c Config) AutoOnly() Config {
	var out Config
	for p, m := range c.modes {
		if m == Auto {
			out.modes[p] = Auto
		}
	}
	return out
}

// Key 描述設定內容，可併入快取鍵；全部關閉時為空字串。
func (c Config) Key() string {
	var parts []string
	for _, p := range Pairs() {
		if m := c.modes[p]; m != Off {
			parts = append(parts, p.ID()+"="+m.String())
		}
	}
	return strings.Join(parts, ",")
}

// 搜尋預算（M0 凍結值；數值是工程初值，M2 以校準資料決定，見 docs/47）。
const (
	// SwapCost 是每次換音的通道成本，自然對數尺度；一個音節同時換聲母與韻母為兩次。
	// 成本只用於同一候選池內排序近音，不用來保證原音優先——原音優先由 session 的候選合併規則維持。
	SwapCost = 1.2
	// MaxSwapsPerWord 是單一詞（一次候選查詢）最多可累計的換音次數。連續多個詞各自有自己的額度，
	// 所以整句不限一次；超過額度的近音不再展開，原音路徑與已鎖定路徑仍可用。
	MaxSwapsPerWord = 2
	// MaxNearCandidates 是單次候選查詢最多評分並列出的近音詞面數（依無上下文先驗挑選）。
	// 第一頁固定只露出前幾個，其餘供翻頁；上限同時界定近音的額外評分成本。
	MaxNearCandidates = 60
	// MaxWordCost 是交給解碼器的單詞成本上限；加一點餘量避免浮點累加誤差擋掉剛好兩次的路徑。
	MaxWordCost = MaxSwapsPerWord*SwapCost + 1e-9
)

// AutoPolicy 是自動修正的門檻（自然對數尺度）。近音路徑的分數已含通道成本（SwapCost）；
// 它與原音首選的差必須達到 Threshold 才改組字，所以原音首選有語境支持時不會被改。
// 數值是校準資料決定前的工程初值（docs/47）。
type AutoPolicy struct {
	Margin   float64 // 基本門檻
	Short    float64 // 修改的範圍只有一個音節時額外加的門檻：單字最容易因同音詞頻差而被誤改
	PerExtra float64 // 同一處每多換一個音節額外加的門檻
	// NeuralExtra 是這一處的顯示字已被神經重排改過（不是統計首選）時額外加的門檻：神經不評近音候選，
	// 近音比的是統計首選，這一項讓近音要更明顯才能蓋過神經的選擇。
	NeuralExtra float64
}

// DefaultAutoPolicy 回傳基本門檻、短詞與額外換音的加成；實際門檻由 Threshold 計算。
func DefaultAutoPolicy() AutoPolicy { return AutoPolicy{Margin: 6, Short: 3, PerExtra: 1} }

// Threshold 回傳修改範圍長 span 個音節、其中 swaps 個音節換了音時近音路徑需領先的分數。
func (p AutoPolicy) Threshold(span, swaps int) float64 {
	t := p.Margin
	if span <= 1 {
		t += p.Short
	}
	if swaps > 1 {
		t += p.PerExtra * float64(swaps-1)
	}
	return t
}

// ThresholdFor 與 Threshold 相同，另外在這一處的顯示字已被神經改過（neural 為真）時加 NeuralExtra。
func (p AutoPolicy) ThresholdFor(span, swaps int, neural bool) float64 {
	t := p.Threshold(span, swaps)
	if neural {
		t += p.NeuralExtra
	}
	return t
}

// Alt 是一個音節的近音替代。
type Alt struct {
	Syl   zhuyin.Syllable
	Rules []Pair // 用到的配對：聲母配對在前、韻母配對在後
}

// Cost 是這個替代的通道成本。
func (a Alt) Cost() float64 { return SwapCost * float64(len(a.Rules)) }

func (c Config) pairOf(r rune, final bool) (Pair, rune, bool) {
	for _, p := range Pairs() {
		d := pairs[p]
		if c.modes[p] == Off || d.final != final {
			continue
		}
		switch r {
		case d.a:
			return p, d.b, true
		case d.b:
			return p, d.a, true
		}
	}
	return 0, 0, false
}

// Alternatives 回傳音節 s 的近音替代（不含 s 本身）：只聲母、只韻母、聲母與韻母都換，
// 各自去重並要求在現行音節表內。聲調不變。
// 來源音節只需結構合法：把 ㄕㄨㄥˋ（不在音節表）打成 ㄙㄨㄥˋ 的人，正是這個功能的對象；
// 結果則一律須是表內音節，不產生 ㄕㄨㄥ 這類不存在的候選。
func (c Config) Alternatives(s zhuyin.Syllable) []Alt {
	if !c.Enabled() || !s.Valid() {
		return nil
	}
	r := []rune(string(s))
	initial, final := -1, -1
	for i, x := range r {
		switch zhuyin.ClassOf(x) {
		case zhuyin.Initial:
			initial = i
		case zhuyin.Final:
			final = i
		}
	}
	var iPair, fPair Pair
	var iTo, fTo rune
	var iOK, fOK bool
	if initial >= 0 {
		iPair, iTo, iOK = c.pairOf(r[initial], false)
	}
	if final >= 0 {
		fPair, fTo, fOK = c.pairOf(r[final], true)
	}
	var out []Alt
	seen := map[zhuyin.Syllable]bool{s: true}
	add := func(swap func(r []rune), rules ...Pair) {
		next := append([]rune(nil), r...)
		swap(next)
		alt := zhuyin.Syllable(next)
		if seen[alt] || !alt.Legal() {
			return
		}
		seen[alt] = true
		out = append(out, Alt{Syl: alt, Rules: rules})
	}
	if iOK {
		add(func(x []rune) { x[initial] = iTo }, iPair)
	}
	if fOK {
		add(func(x []rune) { x[final] = fTo }, fPair)
	}
	if iOK && fOK {
		add(func(x []rune) { x[initial], x[final] = iTo, fTo }, iPair, fPair)
	}
	return out
}

// Rules 回傳 matched 相對於使用者打的 typed 用了哪些配對（聲母配對在前、韻母配對在後）。
// 兩者相同或差異不是某個配對的符號互換時回傳 nil；診斷與評測用，不看設定的模式。
func Rules(typed, matched zhuyin.Syllable) []Pair {
	if typed == matched || !typed.Valid() || !matched.Valid() {
		return nil
	}
	t, m := []rune(string(typed)), []rune(string(matched))
	if len(t) != len(m) {
		return nil
	}
	var initial, final []Pair
	for i := range t {
		if t[i] == m[i] {
			continue
		}
		class := zhuyin.ClassOf(t[i])
		for _, p := range Pairs() {
			d := pairs[p]
			if (t[i] == d.a && m[i] == d.b || t[i] == d.b && m[i] == d.a) && d.final == (class == zhuyin.Final) {
				if d.final {
					final = append(final, p)
				} else {
					initial = append(initial, p)
				}
			}
		}
	}
	return append(initial, final...)
}

// Frontend 把輸入展開成帶成本的音節格：每個位置先放原音（成本 0），再放逐對啟用的近音。
// 它實作 input.Frontend，解碼器對它與精確通道一視同仁。
type Frontend struct{ Config Config }

func (f Frontend) Expand(syls []zhuyin.Syllable) input.Lattice {
	return f.Config.Widen(input.Exact{}.Expand(syls), syls, 0, len(syls))
}

// Widen 回傳 lat 的複本，其中 [from, to) 各位置補上近音替代；已存在的假設（例如先前鎖定的
// 近音讀音）不重複加入，也不被較高的成本取代。lat 本身不被修改。
func (c Config) Widen(lat input.Lattice, syls []zhuyin.Syllable, from, to int) input.Lattice {
	out := append(input.Lattice(nil), lat...)
	for i := max(from, 0); i < min(to, len(lat), len(syls)); i++ {
		row := append([]input.Hyp(nil), lat[i]...)
		for _, y := range syls[i].Tones() { // 沒打聲調的音節（#150）：每個聲調各自的近音；其他音節只有自己
			for _, a := range c.Alternatives(y) {
				if !hasSyl(row, a.Syl) {
					row = append(row, input.Hyp{Syl: a.Syl, Cost: a.Cost()})
				}
			}
		}
		out[i] = row
	}
	return out
}

func hasSyl(row []input.Hyp, s zhuyin.Syllable) bool {
	for _, h := range row {
		if h.Syl == s {
			return true
		}
	}
	return false
}
