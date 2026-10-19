// Package suggest 實作 Tab 補全。
//
// 補全只來自既有的個人片語與系統詞，是忠實的——不生成新內容。
// 不理會建議時，顯示、解碼與學習與沒有此功能完全相同。
package suggest

import (
	"git.biggo.com/0c/ime/core/decode"
	"git.biggo.com/0c/ime/core/input"
	"git.biggo.com/0c/ime/core/layout"
	"git.biggo.com/0c/ime/core/zhuyin"
)

// Suggestion 是一個補全建議。
type Suggestion struct {
	Word    string            // 完整的詞
	Start   int               // 該詞在組字區的起點（音節位置）
	Rest    string            // 尚未輸入的部分，以淡色顯示
	Syls    []zhuyin.Syllable // 尚未輸入的音節
	Conf    float64
	Replace string // 非空時為將被替換的已顯示前綴，必須清楚預覽後才接受
}

// Options 控制何時建議。寧可少建議，不要讓使用者養成忽略它的習慣。
type Options struct {
	MinConf        float64
	MinSyllables   int
	AttentionCost  float64 // 檢視建議的等效按鍵成本；0 停用效益門檻
	RewritePrefix  bool    // 可明確預覽並替換未鎖定的同音尾端
	MinRewriteConf float64 // 延伸詞間的條件信心門檻；另受 MinConf 限制
}

// DefaultOptions 回傳補全信心與操作效益門檻。
func DefaultOptions() Options {
	// 同音改寫預設由使用者按 Tab 要求預覽。
	return Options{MinConf: 0.4, MinSyllables: 1, AttentionCost: 0.5, MinRewriteConf: 0.7}
}

// For 依解碼結果產生建議；沒有合格的建議時回傳 nil。
func For(d *decode.Decoder, lat input.Lattice, path []decode.Seg, o Options) *Suggestion {
	return ForWithLocks(d, lat, path, nil, o)
}

// ForWithLocks 保留既有追加補全，沒有合格建議時才找同音替換。
// 僅檢查解碼分段的邊界，最多 decode.MaxLen 音節（自訂詞可以更長），不跨手選詞。
func ForWithLocks(d *decode.Decoder, lat input.Lattice, path []decode.Seg, locks map[int]decode.Lock, o Options) *Suggestion {
	if len(path) == 0 {
		return nil
	}
	if g := explicitAppend(d, lat, path, locks, o); g != nil {
		return g
	}
	g := appendOnly(d, lat, path, o)
	if g != nil && overlaps(locks, g.Start, len(lat)) {
		g = nil
	}
	if x := crossAppend(d, lat, path, locks, o); x != nil && (g == nil || x.Conf > g.Conf) {
		g = x
	}
	if g != nil {
		return g
	}
	if !o.RewritePrefix {
		return nil
	}
	var best *Suggestion
	for i, seg := range path {
		typed := len(lat) - seg.Start
		if typed < 2 || typed >= decode.MaxLen(d.Lex) || overlaps(locks, seg.Start, len(lat)) {
			continue
		}
		left := d.InitialLeft()
		if i > 0 {
			left = path[i-1].Word
		}
		c, ok := d.CompleteHomophones(lat, seg.Start, left)
		if !ok || c.Conf < o.MinRewriteConf {
			continue
		}
		var displayed string
		for _, tail := range path[i:] {
			displayed += tail.Word
		}
		if string([]rune(c.Entry.Surface)[:typed]) == displayed {
			continue
		}
		g := completion(c, seg.Start, typed, o)
		if g != nil && (best == nil || g.Conf > best.Conf) {
			g.Replace, best = displayed, g
		}
	}
	return best
}

func overlaps(locks map[int]decode.Lock, start, end int) bool {
	for at, lock := range locks {
		if at < end && start < at+lock.Len {
			return true
		}
	}
	return false
}

// explicitAppend 找跨過幾個解碼分段的自訂詞補全（#122）：「新阿姆」解成「新｜阿姆」時，尾端那一段只看得到
// 「阿姆斯特丹」，使用者加的「新阿姆斯特朗…砲」卻是從「新」開始。從最長的範圍找起，只接受畫面上的字不變、
// 往後延伸的自訂詞；最後一段本身由 appendOnly 處理。沒有任何自訂詞時不做事。
func explicitAppend(d *decode.Decoder, lat input.Lattice, path []decode.Seg, locks map[int]decode.Lock, o Options) *Suggestion {
	xp, ok := d.Score.(decode.Explicit)
	if !ok || !xp.HasExplicit() {
		return nil
	}
	for i := 0; i < len(path); i++ {
		start := path[i].Start
		typed := len(lat) - start
		if typed < 2 || overlaps(locks, start, len(lat)) {
			continue
		}
		var displayed string
		for _, seg := range path[i:] {
			displayed += seg.Word
		}
		merged := append(path[:i:i], decode.Seg{Word: displayed, Start: start, End: len(lat), Partial: true})
		c, ok := d.Complete(lat, merged)
		if !ok || len(c.Entry.Reading) <= typed || !xp.Explicit(c.Entry.Surface) {
			continue
		}
		if g := completion(c, start, typed, o); g != nil {
			return g
		}
	}
	return nil
}

// crossAppend 找跨過幾個解碼分段的系統詞補全（#143）。學過「沒有｜不散的筵席」的切法後，「天下沒有不」解成
// 「天下｜沒有｜不」，最後一段只剩「不」，appendOnly 找不到夠有把握的延伸；從「天下」那一段找起，畫面上的字
// 正好是系統詞「天下沒有不散的筵席」的開頭。
//
// 解碼器切開這段，表示它認為切開的讀法比較可能，所以不能只在延伸詞之間比（#142 撤回的做法，打「新阿」就以
// 信心 1 提示 16 個音節的梗）。信心另外乘上 decode.CompleteAcross 的比例：延伸詞群 ÷（延伸詞群＋目前路徑），
// 只用系統先驗，更長的詞和解碼尾端一樣乘上 Continue；比例要過半，也就是不看個人資料時合起來的讀法比目前的切法
// 可能。學過的切法改變解碼結果，不改變這個比例，所以有沒有學過都一樣提示。
// 和最後一段的補全比信心，取高的；自訂詞由 explicitAppend 先處理，行為不變。
func crossAppend(d *decode.Decoder, lat input.Lattice, path []decode.Seg, locks map[int]decode.Lock, o Options) *Suggestion {
	var best *Suggestion
	for i := 0; i < len(path)-1; i++ {
		start := path[i].Start
		typed := len(lat) - start
		if typed < 2 || typed >= decode.MaxLen(d.Lex) || overlaps(locks, start, len(lat)) {
			continue
		}
		c, ratio, ok := d.CompleteAcross(lat, path, i)
		if !ok || len(c.Entry.Reading) <= typed || ratio <= 0.5 {
			continue
		}
		c.Conf *= ratio
		if g := completion(c, start, typed, o); g != nil && (best == nil || g.Conf > best.Conf) {
			best = g
		}
	}
	return best
}

func appendOnly(d *decode.Decoder, lat input.Lattice, path []decode.Seg, o Options) *Suggestion {
	c, ok := d.Complete(lat, path)
	if !ok {
		return nil
	}
	last := path[len(path)-1]
	typed := last.End - last.Start
	return completion(c, last.Start, typed, o)
}

func completion(c decode.Completion, start, typed int, o Options) *Suggestion {
	if saved := len(c.Entry.Reading) - typed; saved < o.MinSyllables || c.Conf < o.MinConf {
		return nil
	}
	// 每個符號一鍵，另有聲調／一聲空白；Tab 本身也要一鍵。標準與倚天 41 鍵都是一鍵一符號，鍵數相同（#83）。
	// 這是操作效益的代理量，不宣稱等同真實輸入時間。
	keys := 0
	for _, syl := range c.Entry.Reading[typed:] {
		keys += len(layout.Standard.Keys(syl))
	}
	if o.AttentionCost > 0 && c.Conf*float64(keys-1) <= o.AttentionCost {
		return nil
	}
	return &Suggestion{
		Word:  c.Entry.Surface,
		Start: start,
		Rest:  string([]rune(c.Entry.Surface)[typed:]),
		Syls:  c.Entry.Reading[typed:],
		Conf:  c.Conf,
	}
}
