package english

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/0c-biggo/0c-ime/core/binfmt"
)

// 格式 3（APP 中英混打的記憶體）：同一份詞頻字典，排成可以直接在唯讀位元組上查詢的樣子。格式 2
// 的詞條長短不一、只能從頭解碼，解成 Go 的 slice 與 trie 要約 7.8 MB 常駐、載入時配置約 29 MB；
// 手機把格式 3 mmap 進來，詞、旗標、權重與 trie 都留在乾淨的檔案頁，Go heap 只多一個 struct
// （iOS 鍵盤的記憶體上限不算乾淨的檔案頁，同 core/lexicon 的 Map）。
//
// 內容與格式 2 一一對應（EncodeMapped、Encode 互轉逐位元組相同），查詢結果相同。資料包（words.bin）
// 仍是格式 2；格式 3 是宿主從它轉出來的執行期檔案，桌面不讀。各欄（小端序，binfmt）：
//
//	text        所有詞的小寫鍵串接，依字典序
//	textOff     uint32 × (n+1)，第 i 個詞是 text[textOff[i]:textOff[i+1]]
//	weight      float64 × n
//	flags       uint8 × n：flagNoSuggest、flagProtected、flagDisplay、flagCorpusOnly（同格式 2）
//	displayAt   uint32 × k，有常見寫法的詞，遞增
//	display     常見寫法串接，與 displayAt 對齊
//	displayOff  uint32 × (k+1)
//	contraction uint32 × c，可建議的縮寫（含撇號的詞），依（去掉撇號的拼法, index）排序
//	nodes       13 位元組 × m：letter，接著 child、next、word、best 各 3 位元組（同 trieNode，id 從 1 起）
var magicMapped = []byte("0cEN\x00\x03")

const mappedVersion = 1

const nodeBytes = 13

// maxNode：trie 節點的欄位是 3 位元組。
const maxNode = 1<<24 - 1

func u24(b []byte) uint32 { return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 }

// columns 是格式 3 的各欄，直接指向資料包的位元組。
type columns struct {
	n                                 int
	texts, display                    string
	textOff, weights, flags           []byte
	displayAt, displayOff, contracted []byte
	nodes                             []byte
	nodeCount                         int
}

func u32At(b []byte, i int) uint32 { return binary.LittleEndian.Uint32(b[4*i:]) }

func (c *columns) text(i int) string {
	return c.texts[u32At(c.textOff, i):u32At(c.textOff, i+1)]
}

func (c *columns) weight(i int) float64 {
	return math.Float64frombits(binary.LittleEndian.Uint64(c.weights[8*i:]))
}

func (c *columns) node(id uint32) trieNode {
	b := c.nodes[nodeBytes*int(id-1):]
	return trieNode{letter: b[0], child: u24(b[1:]), next: u24(b[4:]), word: u24(b[7:]), best: u24(b[10:])}
}

func (c *columns) child(id uint32, letter byte) uint32 {
	for x := c.node(id).child; x != 0; {
		n := c.node(x)
		if n.letter == letter {
			return x
		}
		x = n.next
	}
	return 0
}

func (c *columns) common(i int) string {
	k := len(c.displayAt) / 4
	lo, hi := 0, k
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if int(u32At(c.displayAt, mid)) < i {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < k && int(u32At(c.displayAt, lo)) == i {
		return c.display[u32At(c.displayOff, lo):u32At(c.displayOff, lo+1)]
	}
	return c.text(i)
}

// short：混打字典不認的詞（shortCorpusWord）。
func (c *columns) short(i int) bool {
	return shortCorpusWord(c.text(i), c.flags[i]&flagCorpusOnly != 0)
}

// letterBit 是 trie 字母（a–z、撇號）的位元；不是這些字母是 0。
func letterBit(c byte) uint32 {
	switch {
	case c >= 'a' && c <= 'z':
		return 1 << (c - 'a')
	case c == '\'':
		return 1 << 26
	}
	return 0
}

// compareBare 比較 word 去掉撇號後的拼法與 bare。
func compareBare(word, bare string) int {
	j := 0
	for i := 0; i < len(word); i++ {
		if word[i] == '\'' {
			continue
		}
		if j == len(bare) {
			return 1
		}
		if word[i] != bare[j] {
			if word[i] < bare[j] {
				return -1
			}
			return 1
		}
		j++
	}
	if j < len(bare) {
		return -1
	}
	return 0
}

// 以下是兩種字典共用的存取函式：記憶體裡的字典（New、Decode 格式 1、2）讀 slice，格式 3 讀位元組。

func (d *Dictionary) text(i int) string {
	if d.col != nil {
		return d.col.text(i)
	}
	return d.words[i].Text
}

func (d *Dictionary) weight(i int) float64 {
	if d.col != nil {
		return d.col.weight(i)
	}
	return d.words[i].Weight
}

func (d *Dictionary) word(i int) Word { return Word{d.text(i), d.weight(i)} }

func (d *Dictionary) node(id uint32) trieNode {
	if d.col != nil {
		return d.col.node(id)
	}
	return d.nodes[id-1]
}

func (d *Dictionary) unsuggested(i int) bool {
	if d.col != nil {
		return d.col.flags[i]&flagNoSuggest != 0
	}
	return d.noSuggest[i]
}

func (d *Dictionary) isProtected(i int) bool {
	if d.col != nil {
		return d.col.flags[i]&flagProtected != 0
	}
	return d.protected != nil && d.protected[i]
}

// flagsAt 是格式 2、3 的旗標，不含 flagDisplay（寫法另外看 common）。
func (d *Dictionary) flagsAt(i int) byte {
	if d.col != nil {
		return d.col.flags[i] &^ flagDisplay
	}
	flag := byte(0)
	if d.noSuggest[i] {
		flag |= flagNoSuggest
	}
	if d.protected != nil && d.protected[i] {
		flag |= flagProtected
	}
	if d.corpusOnly != nil && d.corpusOnly[i] {
		flag |= flagCorpusOnly
	}
	return flag
}

// contractions 依 index 遞增，對去掉撇號拼成 bare 的每個可建議縮寫呼叫 fn。
func (d *Dictionary) contractions(bare string, fn func(uint32)) {
	if d.col == nil {
		for _, i := range d.contraction[bare] {
			fn(i)
		}
		return
	}
	c := d.col
	k := len(c.contracted) / 4
	lo, hi := 0, k
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if compareBare(c.text(int(u32At(c.contracted, mid))), bare) < 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	for ; lo < k; lo++ {
		i := u32At(c.contracted, lo)
		if compareBare(c.text(int(i)), bare) != 0 {
			return
		}
		fn(i)
	}
}

func (d *Dictionary) nodeCount() int {
	if d.col != nil {
		return d.col.nodeCount
	}
	return len(d.nodes)
}

// EncodeMapped 把詞頻字典寫成格式 3（見上）。舊字典（格式 1）不收。
func (d *Dictionary) EncodeMapped() ([]byte, error) {
	if !d.Ranked() {
		return nil, errors.New("格式 3 只收詞頻字典")
	}
	n := d.Len()
	w := binfmt.NewWriter(string(magicMapped), mappedVersion)
	var text, display strings.Builder
	textOff := make([]byte, 0, 4*(n+1))
	weight := make([]byte, 0, 8*n)
	flags := make([]byte, 0, n)
	var displayAt, displayOff, contracted []byte
	displayOff = binary.LittleEndian.AppendUint32(displayOff, 0)
	type bareIndex struct {
		bare string
		i    uint32
	}
	var bares []bareIndex
	for i := range n {
		textOff = binary.LittleEndian.AppendUint32(textOff, uint32(text.Len()))
		t := d.text(i)
		text.WriteString(t)
		weight = binary.LittleEndian.AppendUint64(weight, math.Float64bits(d.weight(i)))
		flag := d.flagsAt(i)
		if common := d.common(i); common != t {
			flag |= flagDisplay
			displayAt = binary.LittleEndian.AppendUint32(displayAt, uint32(i))
			display.WriteString(common)
			displayOff = binary.LittleEndian.AppendUint32(displayOff, uint32(display.Len()))
		}
		flags = append(flags, flag)
	}
	textOff = binary.LittleEndian.AppendUint32(textOff, uint32(text.Len()))
	// 縮寫：每個去掉撇號的拼法，照 index 遞增（contractions 的順序）。
	for i := range n {
		t := d.text(i)
		if strings.Contains(t, "'") && !d.unsuggested(i) {
			bares = append(bares, bareIndex{strings.ReplaceAll(t, "'", ""), uint32(i)})
		}
	}
	sortBares := func(a, b bareIndex) int {
		if c := strings.Compare(a.bare, b.bare); c != 0 {
			return c
		}
		return int(a.i) - int(b.i)
	}
	for i := 1; i < len(bares); i++ { // 插入排序：縮寫只有幾十個
		for j := i; j > 0 && sortBares(bares[j-1], bares[j]) > 0; j-- {
			bares[j-1], bares[j] = bares[j], bares[j-1]
		}
	}
	for _, b := range bares {
		contracted = binary.LittleEndian.AppendUint32(contracted, b.i)
	}
	m := d.nodeCount()
	if m > maxNode {
		return nil, errors.New("英文字典的 trie 過大")
	}
	nodes := make([]byte, 0, nodeBytes*m)
	for id := uint32(1); id <= uint32(m); id++ {
		x := d.node(id)
		nodes = append(nodes, x.letter)
		for _, v := range [4]uint32{x.child, x.next, x.word, x.best} {
			nodes = append(nodes, byte(v), byte(v>>8), byte(v>>16))
		}
	}
	w.String(text.String())
	w.Raw(n+1, textOff)
	w.Raw(n, weight)
	w.Raw(n, flags)
	w.Raw(len(displayAt)/4, displayAt)
	w.String(display.String())
	w.Raw(len(displayOff)/4, displayOff)
	w.Raw(len(contracted)/4, contracted)
	w.Raw(m, nodes)
	out := w.Finish()
	if len(out) > MaxPackBytes {
		return nil, errors.New("英文資料包過大")
	}
	return out, nil
}

// Map 讀格式 3，不複製、不解碼：字典直接查 b。str 把 b 裡的文字（詞與寫法串接）轉成字串；宿主
// mmap 進來時傳入不複製的轉換（unsafe.String，core 不用 unsafe，同 lexicon.Map），str 為 nil 時複製。
// b 在字典的整個生命期內不能改，也不能 munmap。
//
// 載入時逐項檢查（約 82,000 詞、19 萬個 trie 節點，各讀一遍、不配置大塊記憶體）：詞照字典序、權重
// 與旗標合法、寫法與縮寫跟詞對得上、trie 是一棵樹而且兄弟的字母不重複、每個詞剛好在它的字母走到的
// 節點、每個子樹的 best 是它裡面最好的可建議詞。檢查過的檔案，查詢結果跟格式 2 解出來的字典相同。
func Map(b []byte, str func([]byte) string) (*Dictionary, error) {
	if len(b) > MaxPackBytes {
		return nil, errors.New("英文資料包標頭或大小不合法")
	}
	if str == nil {
		str = func(b []byte) string { return string(b) }
	}
	r := binfmt.NewReader(b, string(magicMapped), mappedVersion)
	c := &columns{}
	textBytes := r.View(1)
	c.textOff, c.weights, c.flags = r.View(4), r.View(8), r.View(1)
	c.displayAt = r.View(4)
	displayBytes := r.View(1)
	c.displayOff, c.contracted = r.View(4), r.View(4)
	c.nodes = r.View(nodeBytes)
	if err := r.Done(); err != nil {
		return nil, err
	}
	c.texts, c.display = str(textBytes), str(displayBytes)
	c.n, c.nodeCount = len(c.flags), len(c.nodes)/nodeBytes
	d := &Dictionary{ranked: true, col: c}
	if err := c.check(d); err != nil {
		return nil, err
	}
	for i := range c.n {
		if c.short(i) {
			view := *d
			view.hideShort, view.mixed = true, nil
			d.mixed = &view
			break
		}
	}
	return d, nil
}

var errMapped = errors.New("英文資料包（格式 3）內容不合法")

func (c *columns) check(d *Dictionary) error {
	n, m := c.n, c.nodeCount
	if n == 0 || n > MaxPackWords || len(c.textOff) != 4*(n+1) || len(c.weights) != 8*n || u32At(c.textOff, 0) != 0 ||
		int(u32At(c.textOff, n)) != len(c.texts) || m < 2 || m > maxNode {
		return errMapped
	}
	previous := ""
	contractions := 0
	for i := range n {
		at, end := u32At(c.textOff, i), u32At(c.textOff, i+1)
		if end <= at || end > uint32(len(c.texts)) {
			return errMapped
		}
		t := c.texts[at:end]
		w := c.weight(i)
		if !rankedKey(t) || i > 0 && t <= previous || !(w > 0) || math.IsInf(w, 0) ||
			c.flags[i]&^(flagNoSuggest|flagProtected|flagDisplay|flagCorpusOnly) != 0 {
			return fmt.Errorf("英文詞條 %d 不合法", i)
		}
		if strings.Contains(t, "'") && c.flags[i]&flagNoSuggest == 0 {
			contractions++
		}
		previous = t
	}
	// 寫法：有 flagDisplay 的詞剛好是 displayAt，寫法與詞只差大小寫、而且不同。
	k := len(c.displayAt) / 4
	if len(c.displayOff) != 4*(k+1) || u32At(c.displayOff, 0) != 0 || int(u32At(c.displayOff, k)) != len(c.display) {
		return errMapped
	}
	j := 0
	for i := range n {
		if c.flags[i]&flagDisplay == 0 {
			continue
		}
		if j >= k || int(u32At(c.displayAt, j)) != i {
			return errMapped
		}
		t := c.text(i)
		at, end := u32At(c.displayOff, j), u32At(c.displayOff, j+1)
		if end < at || int(end-at) != len(t) || end > uint32(len(c.display)) {
			return errMapped
		}
		shown, differs := c.display[at:end], false
		for x := range len(t) {
			ch := shown[x]
			if ch >= 'A' && ch <= 'Z' {
				ch += 'a' - 'A'
				differs = true
			}
			if ch != t[x] {
				return fmt.Errorf("英文常見寫法不合法：%q", shown)
			}
		}
		if !differs {
			return fmt.Errorf("英文常見寫法不合法：%q", shown)
		}
		j++
	}
	if j != k {
		return errMapped
	}
	// 縮寫：所有可建議、含撇號的詞，各一次，依（拼法, index）遞增。
	if len(c.contracted)/4 != contractions {
		return errMapped
	}
	for x := range contractions {
		i := u32At(c.contracted, x)
		if int(i) >= n || !strings.Contains(c.text(int(i)), "'") || c.flags[i]&flagNoSuggest != 0 {
			return errMapped
		}
		if x > 0 {
			p := u32At(c.contracted, x-1)
			bare := strings.ReplaceAll(c.text(int(i)), "'", "")
			if cmp := compareBare(c.text(int(p)), bare); cmp > 0 || cmp == 0 && p >= i {
				return errMapped
			}
		}
	}
	// trie：從根走一遍，每個節點只走到一次（是一棵樹）、兄弟的字母不重複、深度不超過最長的詞；
	// 有詞的節點，詞就是從根走到它的字母，所以 child 一路查得到每個詞。best 是自己（可建議時）
	// 與子樹裡最好的詞。只讀一遍節點，不配置大塊記憶體（只有一份每節點一位元的走過紀錄）。
	seen := make([]uint64, (m+64)/64)
	var path [MaxWordBytes]byte
	visited, words := 0, 0
	var visit func(id uint32, depth int) bool
	visit = func(id uint32, depth int) bool {
		if id == 0 || id > uint32(m) || seen[id/64]&(1<<(id%64)) != 0 {
			return false
		}
		seen[id/64] |= 1 << (id % 64)
		visited++
		x := c.node(id)
		if x.word > uint32(n) || x.best > uint32(n) || depth == 0 && (x.letter != 0 || x.next != 0 || x.word != 0) {
			return false
		}
		if depth > 0 {
			path[depth-1] = x.letter
		}
		want := uint32(0)
		if x.word != 0 {
			if c.text(int(x.word-1)) != string(path[:depth]) { // 比較不配置
				return false
			}
			words++
			if c.flags[x.word-1]&flagNoSuggest == 0 {
				want = x.word
			}
		}
		var letters uint32
		for y := x.child; y != 0; {
			if y > uint32(m) { // 越界的 child／next：先擋下，不讀到節點欄外面（會 panic）
				return false
			}
			z := c.node(y)
			bit := letterBit(z.letter)
			if depth == MaxWordBytes || bit == 0 || letters&bit != 0 || !visit(y, depth+1) {
				return false
			}
			letters |= bit
			if d.better(z.best, want) {
				want = z.best
			}
			y = z.next
		}
		return x.best == want
	}
	if !visit(1, 0) || visited != m || words != n {
		return errMapped
	}
	return nil
}
