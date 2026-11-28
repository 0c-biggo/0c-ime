package english

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
)

// 詞頻字典（APP#21）：權重由語料詞頻換算（internal/englishpack），保留常見寫法（I、iPhone、
// OK）、收縮寫（don't、I'm），並標出台灣常用英文。舊字典（New、NewFiltered、資料包格式 1）
// 的查詢結果不受影響：這裡的排序、寫法與撇號只在 Ranked() 為真時才用。

// Entry 是詞頻字典的一個詞。Text 是小寫鍵，可在字母之間含 ASCII 撇號；Display 是常見寫法，
// 與 Text 只差大小寫，空字串表示照 Text。Weight 越大越常用。Protected 是台灣常用英文表裡的詞：
// 照打、不自動修正（GG、XD、po）。CorpusOnly 是只來自語料統計、不在審查過的詞表（ESDB、
// technical.tsv）的詞；其中三個字母以內的，中英混打不認（Mixed）。
type Entry struct {
	Text, Display string
	Weight        float64
	NoSuggest     bool
	Protected     bool
	CorpusOnly    bool
}

// NewRanked 建立詞頻字典。不合法的詞條是錯誤（資料包編譯時就擋下），不默默略過。
func NewRanked(entries []Entry) (*Dictionary, error) {
	entries = slices.Clone(entries)
	slices.SortFunc(entries, func(a, b Entry) int { return strings.Compare(a.Text, b.Text) })
	d := newRanked(len(entries))
	for _, e := range entries {
		if err := d.addRanked(e); err != nil {
			return nil, err
		}
	}
	return d.finishRanked()
}

func newRanked(n int) *Dictionary {
	return &Dictionary{ranked: true, contraction: map[string][]uint32{},
		words: make([]Word, 0, n), noSuggest: make([]bool, 0, n), protected: make([]bool, 0, n), corpusOnly: make([]bool, 0, n)}
}

// addRanked 檢查並加入一個詞；詞要照字典序、不重複。
func (d *Dictionary) addRanked(e Entry) error {
	if !rankedKey(e.Text) || e.Weight <= 0 || math.IsNaN(e.Weight) || math.IsInf(e.Weight, 0) {
		return fmt.Errorf("英文詞條不合法：%q", e.Text)
	}
	if n := len(d.words); n > 0 && d.words[n-1].Text >= e.Text {
		return fmt.Errorf("英文詞條重複或順序不對：%q", e.Text)
	}
	if e.Display != "" && (e.Display == e.Text || strings.ToLower(e.Display) != e.Text) {
		return fmt.Errorf("英文常見寫法不合法：%q", e.Display)
	}
	i := uint32(len(d.words))
	d.words = append(d.words, Word{e.Text, e.Weight})
	d.noSuggest = append(d.noSuggest, e.NoSuggest)
	d.protected = append(d.protected, e.Protected)
	d.corpusOnly = append(d.corpusOnly, e.CorpusOnly)
	if e.Display != "" {
		d.displayAt = append(d.displayAt, i)
		d.display = append(d.display, e.Display)
	}
	if bare := strings.ReplaceAll(e.Text, "'", ""); bare != e.Text && !e.NoSuggest {
		d.contraction[bare] = append(d.contraction[bare], i)
	}
	return nil
}

func (d *Dictionary) finishRanked() (*Dictionary, error) {
	if len(d.words) == 0 || len(d.words) > MaxPackWords {
		return nil, fmt.Errorf("英文詞數不合法")
	}
	d.buildIndex()
	d.buildMixed()
	return d, nil
}

// mixedShortWord：語料新詞在這個長度以內的，中英混打不認（#358）。這些詞（da、ji、ka、tv、sr、ae）
// 多半是人名片段、拼音或單字母代號，認了會把 Dan␣、A股、T恤 切成 Da斯、Ae五、Tv預；四個字母以上的
// （iphone、tesla）與縮寫（don't）照認。
const mixedShortWord = 3

// shortCorpusWord 是混打不認的詞：只來自語料、三個字母以內、不是縮寫。
func shortCorpusWord(text string, corpusOnly bool) bool {
	return corpusOnly && len(text) <= mixedShortWord && !strings.Contains(text, "'")
}

// buildMixed 建立混打用的同一份字典：共用詞、索引與寫法，只多一份標記。沒有要藏的詞就不建。
func (d *Dictionary) buildMixed() {
	var hidden []bool
	for i, w := range d.words {
		if shortCorpusWord(w.Text, d.corpusOnly[i]) {
			if hidden == nil {
				hidden = make([]bool, len(d.words))
			}
			hidden[i] = true
		}
	}
	if hidden != nil {
		view := *d
		view.hidden, view.mixed = hidden, nil
		d.mixed = &view
	}
}

// Mixed 是中英混打判斷用的字典：不認三個字母以內的語料新詞（mixedShortWord），其餘同 d。英文模式的
// 補字與修正用 d 本身。舊字典（格式 1）與沒有這種詞的字典就是 d。
func (d *Dictionary) Mixed() *Dictionary {
	if d == nil || d.mixed == nil {
		return d
	}
	return d.mixed
}

// Ranked 表示權重是詞頻，並有常見寫法、縮寫與台灣常用英文。
func (d *Dictionary) Ranked() bool { return d != nil && d.ranked }

// RankedKey 是詞頻字典能收的詞形（資料包編譯時用）。
func RankedKey(s string) bool { return rankedKey(s) }

// rankedKey：小寫字母開頭，其餘是小寫字母或夾在字母之間的撇號（don't、y'all）。
func rankedKey(s string) bool {
	if len(s) < 1 || len(s) > MaxWordBytes || s[0] < 'a' || s[0] > 'z' || s[len(s)-1] == '\'' || strings.Contains(s, "''") {
		return false
	}
	for i := range s {
		if c := s[i]; !(c >= 'a' && c <= 'z' || c == '\'') {
			return false
		}
	}
	return true
}

func rankedTyped(s string) bool {
	if s == "" || s[0] == '\'' {
		return false
	}
	for i := range s {
		if c := s[i]; !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '\'') {
			return false
		}
	}
	return true
}

// Prior 是詞的權重；不在字典裡是 0。
func (d *Dictionary) Prior(word string) float64 {
	if i, ok := d.lookup(word); ok {
		return d.weight(i)
	}
	return 0
}

// Protected 表示照打就好、不能自動修正的台灣常用英文（GG、XD、po、iPhone）。
func (d *Dictionary) Protected(word string) bool {
	i, ok := d.lookup(word)
	return ok && d.isProtected(i)
}

// Display 是 word 該顯示的寫法（iphone → iPhone、i → I、ok → OK），套上打的大小寫；
// 不在字典裡是空字串。
func (d *Dictionary) Display(word string) string {
	i, ok := d.lookup(word)
	if !ok {
		return ""
	}
	if !d.ranked {
		return word
	}
	return d.cased(word, i)
}

func (d *Dictionary) common(i int) string {
	if d.col != nil {
		return d.col.common(i)
	}
	if at, ok := slices.BinarySearch(d.displayAt, uint32(i)); ok {
		return d.display[at]
	}
	return d.words[i].Text
}

// cased 依打的字決定詞的寫法：打全大寫（兩個字母以上）就全大寫；第一個以外的字母有大寫（DeepS、
// PCm）而且打的是詞的開頭時，打的部分照打、後面照常見寫法接（#358）；有常見寫法時用它（iPhone 不因
// 句首變成 IPhone）；否則句首大寫只把第一個字母變大。
func (d *Dictionary) cased(typed string, i int) string {
	common := d.common(i)
	letters := 0
	upper := 0
	inner := false
	for j := range typed {
		if c := typed[j]; c >= 'A' && c <= 'Z' {
			upper++
			letters++
			inner = inner || j > 0
		} else if c >= 'a' && c <= 'z' {
			letters++
		}
	}
	switch {
	case letters >= 2 && upper == letters:
		return strings.ToUpper(common)
	case inner && len(typed) <= len(common) && strings.EqualFold(typed, common[:len(typed)]):
		return typed + common[len(typed):]
	case common != d.text(i):
		return common
	case typed != "" && typed[0] >= 'A' && typed[0] <= 'Z':
		return strings.ToUpper(common[:1]) + common[1:]
	}
	return common
}

// Contractions 是去掉撇號拼成 typed 的縮寫（dont → don't、im → I'm、its → it's）。typed 本身
// 也是字的時候（its、ill、were、wed）照樣列出，由外殼決定只建議、不自動修正：Corrections
// 對字典裡的字一律不給修正。
func (d *Dictionary) Contractions(typed string) []Candidate {
	if !d.Ranked() || !d.typable(typed) || strings.Contains(typed, "'") {
		return nil
	}
	var out []Candidate
	d.contractions(strings.ToLower(typed), func(i uint32) {
		out = append(out, Candidate{Text: d.cased(typed, int(i)), Distance: 1, Weight: d.weight(int(i))})
	})
	slices.SortFunc(out, func(a, b Candidate) int {
		return cmp.Or(cmp.Compare(b.Weight, a.Weight), strings.Compare(a.Text, b.Text))
	})
	return out
}

// 打字成本（以編輯數計）。重複字母少打或多打一個（helo、helllo）、漏打撇號（dont）比一般的
// 編輯常見；鍵盤上相鄰的鍵打錯（o→p）比不相鄰的（c→t）常見。這是 QWERTY 的靜態近似，
// 有觸點的空間模型（APP#23）要另外接。
const (
	apostropheCost = 0.25
	doubledCost    = 0.5
	editCost       = 1
	farCost        = 1.5
	// rankedWeightPerCost：權重差多少抵一個編輯。權重每 10 分是詞頻差 10 倍（englishpack），
	// 所以詞頻差一萬倍才抵一個編輯，差 10 倍抵 0.25。
	rankedWeightPerCost = 40
)

// TypoCost 是想打 word 卻打成 typed 的成本（不分大小寫），見上面的常數。
func TypoCost(typed, word string) float64 {
	a, b := strings.ToLower(typed), strings.ToLower(word)
	if len(a) > MaxWordBytes || len(b) > MaxWordBytes {
		return math.Inf(1)
	}
	var rows [3][MaxWordBytes + 1]float64
	for j := 0; j <= len(b); j++ {
		if j > 0 {
			rows[0][j] = rows[0][j-1] + omitCost(b, j-1)
		}
	}
	for i := 1; i <= len(a); i++ {
		row, prev, before := &rows[i%3], &rows[(i-1)%3], &rows[(i+1)%3]
		row[0] = prev[0] + extraCost(a, i-1)
		for j := 1; j <= len(b); j++ {
			sub := prev[j-1]
			if a[i-1] != b[j-1] {
				sub += substituteCost(a[i-1], b[j-1])
			}
			row[j] = min(sub, prev[j]+extraCost(a, i-1), row[j-1]+omitCost(b, j-1))
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] && a[i-1] != a[i-2] {
				row[j] = min(row[j], before[j-2]+editCost)
			}
		}
	}
	return rows[len(a)%3][len(b)]
}

// omitCost：word 的第 j 個字少打了。
func omitCost(word string, j int) float64 {
	switch {
	case word[j] == '\'':
		return apostropheCost
	case j > 0 && word[j-1] == word[j]:
		return doubledCost
	}
	return editCost
}

// extraCost：打的第 i 個字是多出來的。
func extraCost(typed string, i int) float64 {
	if i > 0 && typed[i-1] == typed[i] {
		return doubledCost
	}
	return editCost
}

func substituteCost(typed, want byte) float64 {
	if adjacent(typed, want) {
		return editCost
	}
	return farCost
}

var qwertyRows = [3]string{"qwertyuiop", "asdfghjkl", "zxcvbnm"}
var qwertyIndent = [3]float64{0, 0.25, 0.75}

// adjacent：QWERTY 上相鄰的兩個鍵（同一排左右，或上下排錯開半個鍵以內）。
func adjacent(x, y byte) bool {
	rx, cx := keyAt(x)
	ry, cy := keyAt(y)
	if rx < 0 || ry < 0 {
		return false
	}
	dy, dx := ry-rx, math.Abs(cy-cx)
	if dy == 0 {
		return dx == 1
	}
	return (dy == 1 || dy == -1) && dx <= 0.8
}

func keyAt(c byte) (int, float64) {
	for r, row := range qwertyRows {
		if i := strings.IndexByte(row, c); i >= 0 {
			return r, float64(i) + qwertyIndent[r]
		}
	}
	return -1, 0
}

// SortRanked 照詞頻字典的方式排打成 typed 的修正候選：打字成本（TypoCost）扣掉權重／40，
// 越小越前面，同分照字母。個人詞（core/personal）也照這個排，權重照它自己的。
func SortRanked(typed string, cs []Candidate) {
	type scored struct {
		c     Candidate
		score float64
	}
	s := make([]scored, len(cs))
	for i, c := range cs {
		s[i] = scored{c, TypoCost(typed, c.Text) - c.Weight/rankedWeightPerCost}
	}
	slices.SortStableFunc(s, func(a, b scored) int {
		return cmp.Or(cmp.Compare(a.score, b.score), strings.Compare(a.c.Text, b.c.Text))
	})
	for i := range s {
		cs[i] = s[i].c
	}
}

// rankedCorrections：OSA 距離內的詞（同舊字典，含撇號）加上去掉撇號的縮寫，照 SortRanked 排。
// 兩個字母只修縮寫（im → I'm）。
func (d *Dictionary) rankedCorrections(typed string, limit int, minimumPrior float64) []Candidate {
	p := strings.ToLower(typed)
	limit = min(limit, MaxCandidates)
	var out []Candidate
	seen := map[uint32]bool{}
	d.contractions(p, func(i uint32) {
		if w := d.weight(int(i)); w >= minimumPrior {
			seen[i] = true
			out = append(out, Candidate{Text: d.cased(typed, int(i)), Distance: 1, Weight: w})
		}
	})
	if len(p) >= 3 {
		d.within(p, maxEdits(p), func(word uint32, edits int) {
			w := d.word(int(word))
			if seen[word] || w.Weight < minimumPrior {
				return
			}
			out = append(out, Candidate{Text: d.cased(typed, int(word)), Distance: edits, Weight: w.Weight})
		})
	}
	SortRanked(p, out)
	return out[:min(len(out), limit)]
}

// t9Keys 是電話鍵盤（ITU E.161）每個數字鍵的字母；1 打撇號。
var t9Keys = [10]string{1: "'", 2: "abc", 3: "def", 4: "ghi", 5: "jkl", 6: "mno", 7: "pqrs", 8: "tuv", 9: "wxyz"}

// T9 列出照電話鍵拼得出、長度相同的可建議詞（4663 → good、home、gone），權重高的在前，
// 寫法同 Display（APP#24 英文九宮格）。
func (d *Dictionary) T9(digits string, limit int) []Candidate {
	if d == nil || limit <= 0 || digits == "" || len(digits) > MaxWordBytes {
		return nil
	}
	for i := range digits {
		if digits[i] < '1' || digits[i] > '9' {
			return nil
		}
	}
	var found []uint32
	var visit func(id uint32, depth int)
	visit = func(id uint32, depth int) {
		if depth == len(digits) {
			if w := d.node(id).word; w != 0 && !d.unsuggested(int(w-1)) && !d.isHidden(int(w-1)) {
				found = append(found, w-1)
			}
			return
		}
		keys := t9Keys[digits[depth]-'0']
		for c := d.node(id).child; c != 0; {
			child := d.node(c)
			if strings.IndexByte(keys, child.letter) >= 0 {
				visit(c, depth+1)
			}
			c = child.next
		}
	}
	visit(1, 0)
	if len(found) == 0 {
		return nil
	}
	sort.Slice(found, func(i, j int) bool { return d.better(found[i]+1, found[j]+1) })
	out := make([]Candidate, 0, min(limit, len(found)))
	for _, i := range found[:min(limit, len(found))] {
		text := d.text(int(i))
		if d.ranked {
			text = d.cased(strings.ToLower(text), int(i))
		}
		out = append(out, Candidate{Text: text, Weight: d.weight(int(i))})
	}
	return out
}
