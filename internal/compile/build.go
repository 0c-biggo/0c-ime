package compile

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// Options 控制讀音展開規則（docs/13 的 L2、L3），每一條都可單獨關閉以量測其效果。
type Options struct {
	Public           bool    // 公開包：輸出前要求可散布來源與完整授權聲明
	RelativeReadings bool    // 以詞內最高的正常讀音權重為基準，避免來源未標主音就整詞降權
	InheritVariants  bool    // L2：單字的變體讀音繼承到含該字的詞
	YiBuSandhi       bool    // L3：一、不的本調與變調
	NeutralTone      bool    // L3：輕聲可用本調輸入
	PolyphoneCommon  bool    // L3：破音字可用該字的主讀音輸入
	MaxVariants      int     // 每個詞由規則產生的讀音上限
	UnigramAlpha     float64 // 詞頻平滑：每個詞加上的虛擬次數（以各來源正規化後的比例計）

	// Unigram 是詞頻來源的混合權重，如 "mcbopomofo=1,chewing-v4=1"。各來源先各自正規化成
	// 機率分布，再依權重線性混合。空字串表示所有來源等權重。
	Unigram string
	// UnigramFallback 以多個來源共同觀察到的頻率補足主來源缺口；空白停用。
	// 各來源先正規化，取最小值乘上 FallbackScale；有主要計數或人工 Boost 的詞不補。
	UnigramFallback string
	FallbackScale   float64
	// Bigram 是 bigram 來源與權重，如 "chewing-v4=0.7,zhwiki=0.3"：各來源的 P(詞|前詞)
	// 依權重線性混合（某來源沒有的配對以 0 計）。空字串表示不用 bigram。
	Bigram string
	// BigramMax 是 bigram 的保留上限（0 表示不剪枝）。依「保留這一筆對模型的貢獻」排序：
	// P(前詞)·P(詞|前詞)·log(有這筆時的機率 ÷ 沒有時的機率)，即對內插模型的熵剪枝。
	BigramMax int
	// PruneMu 是剪枝時假設的執行期內插權重 μ（見 personal.Params.Mu）。
	PruneMu float64
	// PruneBy 是剪枝的排序標準："entropy"（預設，見 pruneBigrams）、"joint"（只看 P(l,w)）、
	// "ratio"（只看 log 提升幅度）。供比較用。
	PruneBy string
	// BMPOnly 只記在 manifest 裡、不影響編譯：基本平面以外的字只收 data/ 主詞庫審過列入的
	// （常見 Linux 字型不涵蓋擴充 B 區以後的字，大量收錄會讓候選出現方塊）。欄位保留讓
	// 搬到單一詞庫前後的 manifest 相同，神經模型的字典配對不必重做。
	BMPOnly bool
	// Exclude 是要排除的來源 ID（逗號分隔）：只來自這些來源的字與詞不編入。供消融使用。
	Exclude string
	// Demote 讓「只來自這些來源、又沒有詞頻」的字詞的 unigram 乘上係數，如 "cns11643=0.01"。
	// 會同時影響整句解碼；預設不用，改用 Rare。
	Demote string
	// Rare 是罕用來源（逗號分隔）：只來自這些來源的字詞標為罕用，候選視窗排在其他字之後；
	// 整句解碼不受影響。有沒有詞頻不改變這一點：維基、新聞與 PTT 的次數也數到日文新字體、
	// 簡化字與粵語用字（気、国、嘅），它們的 bigram 會在單字候選把字抬到常用字前面（#171）。
	Rare string
	// Boost 讓特定來源的詞在沒有詞頻時得到虛擬次數，如 "moi-places=5"。
	// 用在地名這類確定存在、但詞頻語料沒涵蓋的詞。
	Boost string
}

func DefaultOptions() Options {
	return Options{InheritVariants: true, YiBuSandhi: true, NeutralTone: true, PolyphoneCommon: true, MaxVariants: 4, UnigramAlpha: 0.5, BMPOnly: true}
}

// Result 是編譯結果。
type Result struct {
	Lexicon *lexicon.Compact
	LM      *lm.Compact
	Errors  []Problem
	Report  Report
	Sources []Source // 實際被使用的來源，供 manifest 的姓名標示
}

type key struct {
	word, reading string
}

type entry struct {
	word    string
	reading []zhuyin.Syllable
	prw     float64
	grade   Grade
	origins map[string]bool
	correct bool
}

// Build 合併、展開、驗證並產生資料包內容。回傳的 Errors 非空時不得出版。
func Build(d *Data, o Options) *Result {
	res := &Result{Report: Report{RuleCounts: map[string]int{}}}
	fail := func(loc, format string, a ...any) {
		res.Errors = append(res.Errors, Problem{loc, fmt.Sprintf(format, a...)})
	}

	// 每個來源都必須在帳本內；來源與散布狀態會寫入 manifest。
	// 公開資料包另由 Write 的 Public 檢查驗證來源與授權聲明。
	used := map[string]bool{}
	checkOrigins := func(loc string, os []string) {
		for _, id := range os {
			if strings.HasPrefix(id, "rule:") {
				continue
			}
			if _, ok := d.Sources[id]; !ok {
				fail(loc, "來源 %q 不在帳本內", id)
			} else {
				used[id] = true
			}
		}
	}

	all := map[key]*entry{}
	add := func(word string, r []zhuyin.Syllable, g Grade, prw float64, origins []string, correct bool) *entry {
		k := key{word, zhuyin.Key(r)}
		e, ok := all[k]
		if !ok {
			e = &entry{word: word, reading: r, origins: map[string]bool{}, correct: correct}
			all[k] = e
		}
		if prw > e.prw {
			e.prw, e.grade = prw, g
		}
		e.correct = e.correct && correct // 同一讀音若也是正常讀音，就不是修正
		for _, x := range origins {
			e.origins[x] = true
		}
		return e
	}

	excluded := map[string]bool{}
	for _, x := range strings.Split(o.Exclude, ",") {
		if x = strings.TrimSpace(x); x != "" {
			excluded[x] = true
		}
	}
	keep := func(origins []string) bool {
		for _, x := range origins {
			if !excluded[x] {
				return true
			}
		}
		return false
	}

	// 單字表：同一字的讀音彙整，供規則使用
	chars := map[string][]CharReading{}
	for _, c := range d.Chars {
		if !keep(c.Origins) {
			continue
		}
		checkOrigins("chars:"+c.Char, c.Origins)
		chars[c.Char] = append(chars[c.Char], c)
		add(c.Char, []zhuyin.Syllable{c.Reading}, c.Grade, c.Grade.PRW(), c.Origins, false)
	}
	primaryOf := map[string]zhuyin.Syllable{}
	for ch, rs := range chars {
		ps := map[zhuyin.Syllable]bool{}
		for _, r := range rs {
			if r.Grade == Primary && r.VariantOf == "" {
				ps[r.Reading] = true // 多來源同意同一讀音，不是主讀音衝突
			}
		}
		if len(ps) == 1 {
			for reading := range ps {
				primaryOf[ch] = reading
			}
		}
	}
	for _, w := range d.Words {
		if !keep(w.Origins) {
			continue
		}
		checkOrigins("words:"+w.Word, w.Origins)
		add(w.Word, w.Reading, w.Grade, w.Grade.PRW(), w.Origins, false)
	}

	// 讀音展開：對每個多字詞的每個來源讀音套規則。產生的讀音帶較低的 P(r|w)。
	base := make([]*entry, 0, len(all))
	for _, e := range all {
		if len([]rune(e.word)) > 1 {
			base = append(base, e)
		}
	}
	sort.Slice(base, func(i, j int) bool { // 可重現
		if base[i].word != base[j].word {
			return base[i].word < base[j].word
		}
		return zhuyin.Key(base[i].reading) < zhuyin.Key(base[j].reading)
	})
	for _, e := range base {
		for _, v := range variants(e, chars, primaryOf, o) {
			if _, exists := all[key{e.word, zhuyin.Key(v.reading)}]; !exists {
				res.Report.RuleCounts[v.rule]++
			}
			add(e.word, v.reading, e.grade, v.prw, []string{v.rule}, false)
		}
	}

	// 自動修正：修正讀音照字面解讀不得成詞。
	if o.RelativeReadings {
		normalizeReadings(all)
	}
	normal := map[string][]string{}
	for _, e := range all {
		k := zhuyin.Key(e.reading)
		normal[k] = append(normal[k], e.word)
	}
	for _, c := range d.Corrections {
		loc := "corrections:" + c.Word
		checkOrigins(loc, c.Origins)
		if ws := normal[zhuyin.Key(c.Reading)]; len(ws) > 0 {
			sort.Strings(ws)
			fail(loc, "修正讀音 %s 照字面即成詞（%s），不得列入自動修正", zhuyin.Key(c.Reading), strings.Join(ws, "、"))
			continue
		}
		if !hasWord(all, c.Word) {
			fail(loc, "修正的目標詞 %q 不在詞庫內", c.Word)
			continue
		}
		add(c.Word, c.Reading, Correction, Correction.PRW(), c.Origins, true)
	}

	// 字詞一致性：詞條每個位置的讀音，應是該字已知的讀音或可由規則解釋。
	// 不一致不阻擋出版，但列入報告——這同時是發現新通行音的機制。
	res.Report.Inconsistent = consistency(d.Words, chars)

	// 組出詞條
	rareSrc := map[string]bool{}
	for _, x := range strings.Split(o.Rare, ",") {
		if x = strings.TrimSpace(x); x != "" {
			rareSrc[x] = true
		}
	}
	surfaceOrigins := map[string]map[string]bool{}
	for _, e := range all {
		if surfaceOrigins[e.word] == nil {
			surfaceOrigins[e.word] = map[string]bool{}
		}
		for x := range e.origins {
			if !strings.HasPrefix(x, "rule:") {
				surfaceOrigins[e.word][x] = true
			}
		}
	}
	isRare := func(w string) bool {
		if len(rareSrc) == 0 {
			return false
		}
		for x := range surfaceOrigins[w] {
			if !rareSrc[x] {
				return false
			}
		}
		return len(surfaceOrigins[w]) > 0
	}
	var entries []lexicon.Entry
	for _, e := range all {
		entries = append(entries, lexicon.Entry{Surface: e.word, Reading: e.reading, PRW: e.prw, Correction: e.correct, Rare: isRare(e.word)})
	}
	lex, err := lexicon.Build(entries)
	if err != nil {
		fail("lexicon", "%v", err)
		return res
	}
	res.Lexicon = lex

	// 可達性回歸：每一筆使用者回報的「打不出來」都必須打得出來，永不移除。
	for _, r := range d.Reach {
		found := false
		for _, e := range lex.Exact(r.Reading) {
			found = found || e.Surface == r.Word
		}
		if !found {
			fail("reachability:"+r.Word, "打 %s 的候選沒有 %q（%s）", zhuyin.Key(r.Reading), r.Word, r.Note)
		}
	}

	// 語言模型：詞庫中每個詞面都有 unigram。各來源先各自正規化，再依權重線性混合；
	// 沒有次數的詞給平滑後的最小值。
	words := map[string]bool{}
	for _, e := range all {
		words[e.word] = true
	}
	weights, err := parseWeights(o.Unigram, d.Counts)
	if err != nil {
		fail("lm", "%v", err)
		return res
	}
	boosts, err := parseBoost(o.Boost)
	if err != nil {
		fail("lm", "%v", err)
		return res
	}
	boosted := map[string]float64{} // 詞 → 虛擬次數
	for _, w := range d.Words {
		for _, src := range w.Origins {
			if k, ok := boosts[src]; ok && k > boosted[w.Word] {
				boosted[w.Word] = k
			}
		}
	}
	demote, err := parseBoost(o.Demote)
	if err != nil {
		fail("lm", "%v", err)
		return res
	}
	// 每個詞面的來源（字與詞合併）；只來自降級來源的詞才降級
	allOrigins := map[string]map[string]bool{}
	for _, e := range all {
		if allOrigins[e.word] == nil {
			allOrigins[e.word] = map[string]bool{}
		}
		for x := range e.origins {
			if !strings.HasPrefix(x, "rule:") {
				allOrigins[e.word][x] = true
			}
		}
	}
	demoted := func(w string) float64 {
		f := 1.0
		for x := range allOrigins[w] {
			k, ok := demote[x]
			if !ok {
				return 1
			}
			f = math.Min(f, k)
		}
		return f
	}
	uni := map[string]float64{}
	covered := map[string]bool{}
	total := 0.0
	for origin, w := range weights {
		if w == 0 {
			continue
		}
		checkOrigins("lm", []string{origin})
		counts := d.Counts[origin]
		sum := 0.0
		for x := range words {
			sum += counts[x]
		}
		total += sum
		denom := sum + o.UnigramAlpha*float64(len(words))
		for x := range words {
			c := counts[x]
			if c == 0 {
				c = boosted[x]
			}
			uni[x] += w * (c + o.UnigramAlpha) / denom
			if counts[x] > 0 {
				covered[x] = true
			}
		}
	}
	if len(weights) == 0 {
		for x := range words {
			uni[x] = 1 / float64(len(words))
		}
	}
	if len(demote) > 0 {
		for x := range words {
			if !covered[x] && boosted[x] == 0 {
				uni[x] *= demoted(x)
			}
		}
	}
	var bi map[[2]string]float64
	if o.Bigram != "" {
		srcs := map[string]map[string]float64{}
		for k := range d.Bigrams {
			srcs[k] = nil
		}
		bw, err := parseWeights(o.Bigram, srcs)
		if err != nil {
			fail("lm", "bigram：%v", err)
			return res
		}
		bi = map[[2]string]float64{}
		for src, w := range bw {
			checkOrigins("lm", []string{src})
			if err := readBigrams(d.Bigrams[src], words, w, bi); err != nil {
				fail("lm", "%v", err)
				return res
			}
		}
		if o.BigramMax > 0 && len(bi) > o.BigramMax {
			res.Report.BigramsBeforePrune = len(bi)
			bi = pruneBigrams(bi, uni, o.BigramMax, o.PruneMu, o.PruneBy)
		}
		res.Report.Bigrams = len(bi)
		for k := range bi {
			if k[0] == lm.BOS {
				uni[lm.BOS] = 1e-12 // 句首只作為前詞出現；unigram 表需要它才能存 bigram
				break
			}
		}
	}
	// 保留原本以主要詞頻選出的 bigram 集合，補足詞頻不另改剪枝範圍。
	if o.UnigramFallback != "" {
		sources := strings.Split(o.UnigramFallback, ",")
		counts := make([]map[string]float64, 0, len(sources))
		seen := map[string]bool{}
		for _, src := range sources {
			src = strings.TrimSpace(src)
			c, ok := d.Counts[src]
			if !ok || seen[src] {
				fail("lm", "補足詞頻來源不存在或重複：%q", src)
				return res
			}
			seen[src] = true
			checkOrigins("lm", []string{src})
			counts = append(counts, c)
		}
		res.Report.FallbackWords, err = fallbackUnigrams(uni, words, covered, boosted, counts, o.FallbackScale)
		if err != nil {
			fail("lm", "%v", err)
			return res
		}
	}
	model, err := lm.BuildCompact(uni, bi)
	if err != nil {
		fail("lm", "%v", err)
		return res
	}
	res.LM = model

	for id := range used {
		res.Sources = append(res.Sources, d.Sources[id])
	}
	sort.Slice(res.Sources, func(i, j int) bool { return res.Sources[i].ID < res.Sources[j].ID })
	res.Report.fill(all, lex, len(covered), len(words), total)
	return res
}

// normalizeReadings 保留每個詞內的讀音成本差異，不把「沒有主讀音標記」
// 誤當成這個詞較少出現。來源 grade 與 origins 不變；此值是相對相容度，
// 並非由標音語料估計的機率。規則讀音也依同一尺度調整，錯音修正不參與。
func normalizeReadings(all map[key]*entry) {
	best := map[string]float64{}
	for _, e := range all {
		if !e.correct {
			best[e.word] = max(best[e.word], e.prw)
		}
	}
	for _, e := range all {
		if !e.correct && best[e.word] > 0 {
			e.prw /= best[e.word]
		}
	}
}

func hasWord(all map[key]*entry, w string) bool {
	for k := range all {
		if k.word == w {
			return true
		}
	}
	return false
}

type variant struct {
	reading []zhuyin.Syllable
	prw     float64
	rule    string
}

// variants 產生一個詞條由規則衍生的讀音，依 P(r|w) 由高到低、至多 MaxVariants 個。
// 每條規則只做單一位置的替換；一、不的變調可與其他規則並存。
func variants(e *entry, chars map[string][]CharReading, primaryOf map[string]zhuyin.Syllable, o Options) []variant {
	runes := []rune(e.word)
	var out []variant
	sub := func(i int, s zhuyin.Syllable, factor float64, rule string) {
		if s == e.reading[i] {
			return
		}
		r := append([]zhuyin.Syllable(nil), e.reading...)
		r[i] = s
		out = append(out, variant{r, e.prw * factor, rule})
	}
	for i, ch := range runes {
		c, s := string(ch), e.reading[i]
		if o.InheritVariants {
			for _, cr := range chars[c] {
				if cr.VariantOf == s {
					sub(i, cr.Reading, cr.Grade.PRW(), "rule:inherit")
				}
			}
		}
		if o.YiBuSandhi && i+1 < len(runes) {
			next := e.reading[i+1].ToneOf()
			switch {
			case c == "一" && s.Base() == "ㄧ":
				switch {
				case next == 4 || next == 5:
					sub(i, "ㄧˊ", 1, "rule:yi-bu")
				default:
					sub(i, "ㄧˋ", 1, "rule:yi-bu")
				}
				sub(i, "ㄧ", 1, "rule:yi-bu") // 本調
			case c == "不" && s.Base() == "ㄅㄨ":
				if next == 4 {
					sub(i, "ㄅㄨˊ", 1, "rule:yi-bu")
				}
				sub(i, "ㄅㄨˋ", 1, "rule:yi-bu")
			}
		}
		if o.NeutralTone && s.ToneOf() == 5 {
			if t := baseTone(chars[c], s); t != "" {
				sub(i, t, 0.5, "rule:neutral")
			}
		}
		if o.PolyphoneCommon && len(runes) > 1 {
			if p, ok := primaryOf[c]; ok && p.Base() != s.Base() {
				sub(i, p, 0.2, "rule:polyphone")
			}
		}
	}
	// 詞裡有兩個以上的一／不（一五一十、一模一樣、不三不四）：上面一次只變一個位置，
	// 實際念法是每個都變調，另外產生全部變調的讀音。
	if o.YiBuSandhi {
		r := append([]zhuyin.Syllable(nil), e.reading...)
		n := 0
		for i, ch := range runes {
			if i+1 >= len(runes) {
				break
			}
			next := e.reading[i+1].ToneOf()
			switch {
			case ch == '一' && r[i].Base() == "ㄧ":
				n++
				r[i] = "ㄧˋ"
				if next == 4 || next == 5 {
					r[i] = "ㄧˊ"
				}
			case ch == '不' && r[i].Base() == "ㄅㄨ":
				n++
				r[i] = "ㄅㄨˋ"
				if next == 4 {
					r[i] = "ㄅㄨˊ"
				}
			}
		}
		if n >= 2 && zhuyin.Key(r) != zhuyin.Key(e.reading) {
			out = append(out, variant{r, e.prw, "rule:yi-bu"})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].prw > out[j].prw })
	if len(out) > o.MaxVariants {
		out = out[:o.MaxVariants]
	}
	return out
}

// baseTone 找出輕聲音節在該字的非輕聲讀音：同一基本音節、等級最高者。
func baseTone(rs []CharReading, s zhuyin.Syllable) zhuyin.Syllable {
	var best zhuyin.Syllable
	bestG := Grade(-1)
	for _, r := range rs {
		if r.Reading.Base() == s.Base() && r.Reading.ToneOf() != 5 && r.Grade > bestG {
			best, bestG = r.Reading, r.Grade
		}
	}
	return best
}

func consistency(words []WordReading, chars map[string][]CharReading) []string {
	var out []string
	for _, w := range words {
		for i, ch := range []rune(w.Word) {
			s := w.Reading[i]
			ok := len(chars[string(ch)]) == 0 // 字表沒有這個字：無從檢查
			for _, cr := range chars[string(ch)] {
				ok = ok || cr.Reading == s || s.ToneOf() == 5 && cr.Reading.Base() == s.Base()
			}
			ok = ok || (ch == '一' || ch == '不') && (s.Base() == "ㄧ" || s.Base() == "ㄅㄨ")
			if !ok {
				out = append(out, fmt.Sprintf("%s %s：「%c」讀 %s 不在字表", w.Word, zhuyin.Key(w.Reading), ch, s))
			}
		}
	}
	sort.Strings(out)
	return out
}

// Report 是編譯報告。
type Report struct {
	Entries, Words, Syllables, CountedWords int
	TotalCount                              float64
	RuleCounts                              map[string]int
	Inconsistent                            []string
	MaxSingleCandidates                     int
	Bigrams, BigramsBeforePrune             int
	FallbackWords                           int
	MaxSingleReading                        string
	Corrections                             int
}

func (r *Report) fill(all map[key]*entry, lex *lexicon.Compact, covered, words int, total float64) {
	r.Entries, r.Words, r.CountedWords, r.TotalCount = lex.Len(), words, covered, total
	per := map[string]int{}
	for _, e := range all {
		if len(e.reading) == 1 {
			per[string(e.reading[0])]++
		}
		if e.correct {
			r.Corrections++
		}
	}
	r.Syllables = len(per)
	for s, n := range per {
		if n > r.MaxSingleCandidates || n == r.MaxSingleCandidates && s < r.MaxSingleReading {
			r.MaxSingleCandidates, r.MaxSingleReading = n, s
		}
	}
}

// String 產生人類可讀的報告。
func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "詞條（詞面×讀音）：%d\n詞面：%d，其中有詞頻：%d（%.1f%%），總次數 %.0f\n",
		r.Entries, r.Words, r.CountedWords, 100*float64(r.CountedWords)/math.Max(1, float64(r.Words)), r.TotalCount)
	if r.FallbackWords > 0 {
		fmt.Fprintf(&b, "主要詞頻缺口由多來源補足：%d 詞\n", r.FallbackWords)
	}
	fmt.Fprintf(&b, "單字讀音種類：%d；候選最多的讀音：%s（%d 個單字）\n", r.Syllables, r.MaxSingleReading, r.MaxSingleCandidates)
	fmt.Fprintf(&b, "自動修正讀音：%d\nbigram：%d", r.Corrections, r.Bigrams)
	if r.BigramsBeforePrune > 0 {
		fmt.Fprintf(&b, "（剪枝前 %d）", r.BigramsBeforePrune)
	}
	b.WriteString("\n")
	var rules []string
	for k := range r.RuleCounts {
		rules = append(rules, k)
	}
	sort.Strings(rules)
	for _, k := range rules {
		fmt.Fprintf(&b, "規則 %s 新增讀音：%d\n", k, r.RuleCounts[k])
	}
	fmt.Fprintf(&b, "字詞不一致：%d 筆（不阻擋出版；前 20 筆）\n", len(r.Inconsistent))
	for i, s := range r.Inconsistent {
		if i == 20 {
			break
		}
		fmt.Fprintf(&b, "    %s\n", s)
	}
	return b.String()
}

// parseWeights 解析 "a=1,b=0.5"；空字串表示所有詞頻來源等權重。回傳正規化後（總和為 1）的權重。
func parseWeights(spec string, counts map[string]map[string]float64) (map[string]float64, error) {
	w := map[string]float64{}
	if spec == "" {
		for k := range counts {
			w[k] = 1
		}
	} else {
		for _, part := range strings.Split(spec, ",") {
			k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
			x := 1.0
			if ok {
				var err error
				if x, err = strconv.ParseFloat(v, 64); err != nil || x < 0 {
					return nil, fmt.Errorf("詞頻權重 %q 不合法", part)
				}
			}
			if _, have := counts[k]; !have {
				return nil, fmt.Errorf("沒有詞頻來源 %q", k)
			}
			w[k] = x
		}
	}
	sum := 0.0
	for _, x := range w {
		sum += x
	}
	for k := range w {
		if sum > 0 {
			w[k] /= sum
		}
	}
	return w, nil
}

func parseBoost(spec string) (map[string]float64, error) {
	out := map[string]float64{}
	if spec == "" {
		return out, nil
	}
	for _, part := range strings.Split(spec, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		x, err := strconv.ParseFloat(v, 64)
		if !ok || err != nil || x < 0 {
			return nil, fmt.Errorf("虛擬次數 %q 不合法", part)
		}
		out[k] = x
	}
	return out, nil
}

// readBigrams 讀取「前詞<TAB>詞<TAB>P(詞|前詞)」，把 weight×P 加進 out。只保留兩個詞都在
// 詞庫內的配對；前詞可以是句首 "<s>"。
func readBigrams(p string, words map[string]bool, weight float64, out map[[2]string]float64) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := sc.Text()
		if l == "" || l[0] == '#' {
			continue
		}
		a, rest, _ := strings.Cut(l, "\t")
		b, ps, _ := strings.Cut(rest, "\t")
		if !(words[a] || a == lm.BOS) || !words[b] {
			continue
		}
		x, err := strconv.ParseFloat(ps, 64)
		if err != nil || !(x > 0 && x <= 1) {
			return fmt.Errorf("%s：機率 %q 不合法", p, ps)
		}
		out[[2]string{a, b}] += weight * x
	}
	return sc.Err()
}

// pruneBigrams 保留對內插模型貢獻最大的 n 筆 bigram。
//
// 執行期的系統機率是 μ·P_bi(w|l) + (1−μ)·P_uni(w)。拿掉一筆 bigram 後它退回 (1−μ)·P_uni(w)，
// 所以這筆的貢獻以 P(l,w)·log(1 + μ·P_bi/((1−μ)·P_uni(w))) 衡量，其中 P(l,w) ≈ P_uni(l)·P_bi(w|l)。
// 句首 <s> 沒有 unigram，以最常見的詞的機率代替（句首的 bigram 很少，影響不大）。
func pruneBigrams(bi map[[2]string]float64, uni map[string]float64, n int, mu float64, by string) map[[2]string]float64 {
	if mu <= 0 || mu >= 1 {
		mu = 0.7
	}
	maxUni := 0.0
	for _, p := range uni {
		maxUni = math.Max(maxUni, p)
	}
	type scored struct {
		k [2]string
		s float64
	}
	all := make([]scored, 0, len(bi))
	for k, p := range bi {
		pl := uni[k[0]]
		if k[0] == lm.BOS {
			pl = maxUni
		}
		pw := uni[k[1]]
		gain := math.Log1p(mu * p / ((1 - mu) * pw))
		s := pl * p * gain
		switch by {
		case "joint":
			s = pl * p
		case "ratio":
			s = gain
		}
		all = append(all, scored{k, s})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].s != all[j].s {
			return all[i].s > all[j].s
		}
		return all[i].k[0]+"\t"+all[i].k[1] < all[j].k[0]+"\t"+all[j].k[1]
	})
	out := make(map[[2]string]float64, n)
	for _, x := range all[:n] {
		out[x.k] = bi[x.k]
	}
	return out
}
