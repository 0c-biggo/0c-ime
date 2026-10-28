package session

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/rerank"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

const rerankCacheLimit = 128

// rerankEntry 是一段的排序快取。評分器的分數只取決於前文、讀音與候選；排除的候選（vetoed）另外取決於
// 個人資料，記下算出它時的個人資料版本（Gen 與時鐘），版本沒變就不重算。版本變了而排除結果相同時
// 沿用原決策，與把 vetoed 放進鍵值的做法結果相同。
type rerankEntry struct {
	gen      int
	time     int64
	vetoed   []bool
	decision rerankDecision
}

// rerankDecision 是一段的排序結果：顯示的候選、融合分數最接近它的次佳候選與兩者差距。
type rerankDecision struct {
	winner, second int
	gap            float64
}

// neuralHint 是最後一段的次佳候選；以既有替換提示呈現，Tab 逐詞鎖定後成為手選。
type neuralHint struct {
	replace, text string
	segs          []decode.Seg
}

// rerankPath 僅在本次輸入事件的同步決策窗口內執行。沒有背景回呼可寫入 session。
// 字面符號（標點、英數）是解碼邊界：兩側各段獨立排序，段前已顯示的文字（含標點）接在
// 已確認前文之後當作前文。任一手選／敏感狀態仍整次保留原解碼，不跨越保護邊界。
// 單音節段（例：已提交「有下載失敗」後單獨打 ㄇㄚ）只在套件宣告 TextWeight 時以 ModeText
// 評分：句尾助詞常被打成一聲，讀音會偏向「媽」，改由前文決定。
func (s *Session) rerankPath() {
	s.hint = nil
	if !s.rerankAllowed() {
		return
	}
	lat := s.lattice()
	for start := 0; start < len(s.syls); {
		if s.literals[start] {
			start++
			continue
		}
		end := start + 1
		for end < len(s.syls) && !s.literals[end] {
			end++
		}
		if s.rerankable(start, end) {
			s.rerankSegment(lat, start, end)
		}
		start = end
	}
}

func (s *Session) rerankAllowed() bool {
	return s.Rerank.Scorer != nil && s.allowRerank && s.comp.Empty() && s.cand == nil && !s.private && !s.paused && !s.english && len(s.locks) == 0
}

// rerankable：段落長度在模型範圍內；有沒打聲調音節（#150）的段落沒有共用讀音可送，照統計引擎。
func (s *Session) rerankable(start, end int) bool {
	n := end - start
	return (n >= 2 && n <= 32 || n == 1 && s.Rerank.TextWeight > 0) && !zhuyin.HasUntoned(s.syls[start:end])
}

// rerankSegment 以與 decodeWithLiterals 相同的段落設定取 N-best；段內顯示必須等於 N-best 首選，
// 否則不得偷換。沒有變動的段落以快取重用決策，避免每鍵重新評分整句。
func (s *Session) rerankSegment(lat input.Lattice, start, end int) {
	opt := s.Rerank
	s.configureDocumentContext(start)
	s.dec.Evidence = nil
	s.configureCorrections(start, end, true)
	rows, _ := s.dec.NBest(lat[start:end], nil, s.Opt.PrefixAware, 8)
	var shown, before strings.Builder
	first, last := -1, -1
	for i, g := range s.path {
		switch {
		case g.Start >= start && g.End <= end:
			shown.WriteString(g.Word)
			if first < 0 {
				first = i
			}
			last = i
		case g.End <= start:
			before.WriteString(g.Word)
		case g.Start < end:
			return // 顯示段跨越邊界：契約不一致，不偷換
		}
	}
	if first < 0 || len(rows) == 0 || rows[0].Text != shown.String() {
		return
	} // 搜尋契約不一致時不得偷換首選。
	rows = s.extendShown(lat, start, end, rows)
	rows = s.expandNeutral(lat, start, end, rows)
	context := ""
	if opt.ContextRunes > 0 {
		context = lastRunes(s.neuralContext()+before.String(), min(opt.ContextRunes, rerank.MaxContextRunes))
	}
	req := rerank.Request{AllowPartial: opt.AllowPartial, Revision: s.rev + 1, Context: context, Reading: slices.Clone(s.syls[start:end]), Candidates: rows}
	weight, margin := opt.Weight, opt.Margin
	if end-start == 1 {
		req.Mode, weight, margin = rerank.ModeText, opt.TextWeight, opt.TextMargin
	}
	if !rerank.Supported(req) {
		return
	}
	// 排除哪些候選（vetoed）只取決於候選與個人資料，個人資料沒變時沿用快取，不必每鍵重算（#101）。
	key := rerankKey(req) + vetoInputs(rows)
	entry, cached := s.rerankCache[key]
	if !cached || entry.gen != s.model.Gen || entry.time != s.model.Store.Time {
		vetoed := s.vetoRepeats(rows, s.overturnsUser(rows, start, end))
		if !cached || !slices.Equal(vetoed, entry.vetoed) {
			// 宿主只能取得複本；即使有錯誤，也不能就地修改候選／手選狀態。
			copied := req
			copied.Candidates = slices.Clone(rows)
			for i := range copied.Candidates {
				copied.Candidates[i].Segments = slices.Clone(rows[i].Segments)
			}
			scores, err := opt.Scorer.Score(copied)
			if err != nil {
				return // 不快取失敗；下次按鍵可再試
			}
			entry.decision = decide(rows, scores, weight, margin, vetoed)
		}
		entry.gen, entry.time, entry.vetoed = s.model.Gen, s.model.Store.Time, vetoed
		if s.rerankCache == nil || len(s.rerankCache) >= rerankCacheLimit {
			s.rerankCache = map[string]rerankEntry{}
		}
		s.rerankCache[key] = entry
	}
	decision := entry.decision
	if end == len(s.syls) && opt.HintGap > 0 && decision.second >= 0 && decision.gap < opt.HintGap && plainSegments(rows[decision.second].Segments) {
		hint := &neuralHint{text: rows[decision.second].Text, replace: rows[decision.winner].Text}
		for _, g := range rows[decision.second].Segments {
			g.Start += start
			g.End += start
			hint.segs = append(hint.segs, g)
		}
		s.hint = hint
	}
	winner := decision.winner
	if winner <= 0 {
		return
	}
	chosen := slices.Clone(rows[winner].Segments)
	for i := range chosen {
		chosen[i].Start += start
		chosen[i].End += start
	}
	s.path = slices.Concat(s.path[:first], chosen, s.path[last+1:])
}

// extendShownK 是每組鎖定另取的候選數：延伸部分的首選與一個替代。
const extendShownK = 2

// extendShown 處理連續輸入時前段改選被擠掉（#20）：多處同音歧義的組合很快占滿 N-best，
// 已被神經改對的前段可能不在新一鍵的候選中，模型只能挑別的組合而把前段換回。
// 這裡把上次顯示的段內詞（讀音未變者）逐詞鎖定另取 N-best 附在後面：一組連最後一詞也鎖，
// 一組放開最後一詞（它可能與新音節合成長詞，例：做→做事）。原首選仍是 rows[0]，覆寫規則不變。
func (s *Session) extendShown(lat input.Lattice, start, end int, rows []decode.Hypothesis) []decode.Hypothesis {
	var inside []decode.Seg
	for _, g := range s.shownPath {
		if g.Start >= start && g.End <= end && g.End <= len(s.shownSyls) && !g.Partial && !g.Correction && g.Matched == "" &&
			slices.Equal(s.shownSyls[g.Start:g.End], s.syls[g.Start:g.End]) {
			inside = append(inside, g)
		}
	}
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.Text] = true
	}
	out := slices.Clone(rows)
	for _, words := range [][]decode.Seg{inside, inside[:max(len(inside)-1, 0)]} {
		locks := map[int]decode.Lock{}
		for _, g := range words {
			if g.End < end { // 至少留一個音節給延伸
				locks[g.Start-start] = decode.Lock{Word: g.Word, Len: g.End - g.Start}
			}
		}
		if len(locks) == 0 {
			continue
		}
		extra, _ := s.dec.NBest(lat[start:end], locks, s.Opt.PrefixAware, extendShownK)
		for _, r := range extra {
			if len(out) >= rerank.MaxCandidates {
				return out
			}
			if seen[r.Text] {
				continue
			}
			seen[r.Text] = true
			r.Segments = slices.Clone(r.Segments)
			out = append(out, r)
		}
	}
	return out
}

// expandNeutral 處理句尾助詞打錯聲調：「嘛」只在 ㄇㄚˊ／ㄇㄚ˙ 的候選中，打成 ㄇㄚˇ 時
// 原 N-best 根本沒有它。最後音節基底在 NeutralBases 內且不是輕聲時，另以「最後音節改輕聲」取 N-best，
// 不在原清單的候選扣 NeutralExpand 後附在後面。原首選仍是 rows[0]，覆寫規則不變；
// 評分器以打出讀音與輕聲讀音分別評分取大（套件宣告 neutral_expand_penalty）。
func (s *Session) expandNeutral(lat input.Lattice, start, end int, rows []decode.Hypothesis) []decode.Hypothesis {
	opt := s.Rerank
	last := s.syls[end-1]
	if opt.NeutralExpand <= 0 || end-start > 32 || last.ToneOf() == 5 || !opt.NeutralBases[last.Base()] {
		return rows
	}
	neutral := slices.Clone(lat[start:end])
	neutral[len(neutral)-1] = []input.Hyp{{Syl: last.WithTone(5)}}
	syls := slices.Clone(s.syls[start:end])
	syls[len(syls)-1] = last.WithTone(5)
	penalty := s.dec.CorrectionPenalty
	s.dec.CorrectionPenalty = s.model.CorrectionPenalty(syls, nil)
	extra, _ := s.dec.NBest(neutral, nil, s.Opt.PrefixAware, 8)
	s.dec.CorrectionPenalty = penalty
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.Text] = true
	}
	out := slices.Clone(rows)
	for _, r := range extra {
		if len(out) >= rerank.MaxCandidates {
			break
		}
		if seen[r.Text] {
			continue
		}
		seen[r.Text] = true
		r.LogScore -= opt.NeutralExpand
		r.Segments = slices.Clone(r.Segments)
		out = append(out, r)
	}
	return out
}

// neuralKeeps 是神經不得改寫的寫法：統計首選是左邊時，該區間固定不換（只排除右邊的異體時，模型會改選
// 別的同音字，例如 ㄕㄜˊ ㄇㄛ˙ 變成「蛇麼」）。右邊是使用者可能要的異體，選過就不再限制。過渡用：選字模型在沒有個人資料時
// 把高頻寫法換成系統詞頻低一個數量級以上的異體（#96：「什麼」詞頻 1.3e-3，被換成 5.6e-5 的「甚麼」），
// 要等 models repo 修正訓練資料。只放異體或明顯罕用的寫法，不放需要語境判斷的同音詞；模型修好後移除。
// 右邊仍在候選中；使用者用這個讀音選過右邊的寫法後，此表不再限制，交回既有規則。
var neuralKeeps = map[string][]string{
	"什麼": {"甚麼"},
}

// overturnsUser 標出會推翻使用者明確選擇的候選（#96），以及 neuralKeeps 禁止的改寫。評分器只看讀音與前文，不知道使用者親手選過
// 什麼；它的分差乘上權重後常比有界的個人更正成本大（例：「ㄕㄜˊ ㄇㄜ˙」一律換成「甚麼」），
// 不擋的話使用者怎麼選都會被換回去。只約束原首選已經照使用者選擇顯示的位置，其餘交給評分器：
//   - 有語境更正：候選與已存更正不符的成本高於原首選，代表要把使用者在這個讀音與鄰音改掉的字換回去。
//   - 多字詞：原首選某區間正是使用者親手用這個讀音選過的詞，候選在該區間換成使用者沒用這個讀音
//     選過的寫法。使用者兩種都選過時交回評分器依前文判斷。單字的同音字隨語境而定，只看有語境更正。
//
// 比對以音節為準：文字依分段對回音節（alignSyllables），字數與音節數不同的候選（尾端補全顯示整個詞、
// 原樣片段）也照樣檢查；對不上又與受保護的位置重疊的候選一律排除，寧可不換也不推翻使用者（#99）。
//
// 沒有任何候選被排除時回傳 nil。
func (s *Session) overturnsUser(rows []decode.Hypothesis, start, end int) []bool {
	n := end - start
	shown, shownLoose := alignSyllables(rows[0], n)
	aligned := func(from, to int) bool { return shownLoose == nil || !slices.Contains(shownLoose[from:to], true) }
	syls := s.syls[start:end]
	type span struct{ from, to int }
	var confirmed []span
	maxLen := decode.MaxLen(s.dec.Lex) // 自訂詞可以超過系統詞的 8 音節（#122）
	for from := 0; from < n; from++ {
		for to := from + 2; to <= n && to-from <= maxLen; to++ {
			if aligned(from, to) && s.model.ChoseReading(string(shown[from:to]), syls[from:to]) {
				confirmed = append(confirmed, span{from, to})
			}
		}
	}
	// 統計首選中 neuralKeeps 的寫法：候選在同一區間換成別的字就排除（使用者選過的異體除外）。
	type keep struct {
		span
		word string
		alts []string
	}
	var keeps []keep
	for word, alts := range neuralKeeps {
		w := []rune(word)
		for from := 0; from+len(w) <= n; from++ {
			if aligned(from, from+len(w)) && string(shown[from:from+len(w)]) == word {
				keeps = append(keeps, keep{span{from, from + len(w)}, word, alts})
			}
		}
	}
	penalty := s.dec.CorrectionPenalty
	if penalty == nil && len(confirmed) == 0 && len(keeps) == 0 {
		return nil
	}
	// 受保護的位置：使用者選過的詞、neuralKeeps 的寫法與有語境更正的音節。
	protected := make([]bool, n)
	for _, c := range confirmed {
		for at := c.from; at < c.to; at++ {
			protected[at] = true
		}
	}
	for _, k := range keeps {
		for at := k.from; at < k.to; at++ {
			protected[at] = true
		}
	}
	// 更正成本逐音節累加；對不上的位置不計（候選在受保護處對不上又改了寫法時，下面直接排除）。
	cost := func(chars []rune, loose []bool) float64 {
		if loose == nil {
			return penalty(0, n, string(chars))
		}
		total := 0.0
		for at := range n {
			if !loose[at] {
				total += penalty(at, at+1, string(chars[at]))
			}
		}
		return total
	}
	shownCost := 0.0
	if penalty != nil {
		for at := range n {
			protected[at] = protected[at] || penalty(at, at+1, string(utf8.RuneError)) > 0
		}
		shownCost = cost(shown, shownLoose)
	}
	var vetoed []bool
	for i := 1; i < len(rows); i++ {
		text, loose := alignSyllables(rows[i], n)
		overturn := false
		for at := range loose {
			if loose[at] && protected[at] && !sameSegmentAt(rows[0], rows[i], at) {
				overturn = true // 對不上音節的寫法蓋住受保護的位置：無法確認沒有換掉使用者的字
				break
			}
		}
		overturn = overturn || penalty != nil && cost(text, loose) > shownCost+1e-9
		for _, c := range confirmed {
			if overturn {
				break
			}
			alt := string(text[c.from:c.to])
			overturn = alt != string(shown[c.from:c.to]) && !s.model.ChoseReading(alt, syls[c.from:c.to])
		}
		for _, k := range keeps {
			if overturn {
				break
			}
			alt := string(text[k.from:k.to])
			overturn = alt != k.word && !(slices.Contains(k.alts, alt) && s.model.ChoseReading(alt, syls[k.from:k.to]))
		}
		if overturn {
			if vetoed == nil {
				vetoed = make([]bool, len(rows))
			}
			vetoed[i] = true
		}
	}
	return vetoed
}

// vetoRepeats 另外排除受非詞疊字上限約束的候選（decode.Hypothesis.NonWordRepeat：兩個相同單字
// 拼成、或 AABB 長詞前兩字的「恩恩」，同讀音有詞庫詞「嗯嗯」），只在統計首選本身沒有這個標記時才擋。
// 評分器只看文字，照語料寫法偏好「恩恩」，權重乘上去後總是把詞換回去（#139）。不受上限約束的疊字
// （詞庫詞只是字比較常用、詞庫詞的字罕用得多、或使用者自己提交過）照常交給評分器。
//
// 尾端只打了一個音節的長詞開頭（decode.Hypothesis.WeakPrefix）同樣處理：ㄟ 有自己的字「欸」，
// 「工」只是「工口」的開頭，評分器卻因為「社工」通順而把「跆拳道社欸」換成「跆拳道社工」（#107）。
// 統計首選本身就是這種前綴時（前綴的機率夠高，例如突然的「突」），代表證據夠強，照常交給評分器。
func (s *Session) vetoRepeats(rows []decode.Hypothesis, vetoed []bool) []bool {
	vetoed = vetoFlagged(rows, vetoed, func(h decode.Hypothesis) bool { return h.NonWordRepeat })
	return vetoFlagged(rows, vetoed, func(h decode.Hypothesis) bool { return h.WeakPrefix })
}

// vetoFlagged 在統計首選 rows[0] 沒有 flag 時，排除其餘帶 flag 的候選。
func vetoFlagged(rows []decode.Hypothesis, vetoed []bool, flag func(decode.Hypothesis) bool) []bool {
	if len(rows) < 2 || flag(rows[0]) {
		return vetoed
	}
	for i := 1; i < len(rows); i++ {
		if flag(rows[i]) {
			if vetoed == nil {
				vetoed = make([]bool, len(rows))
			}
			vetoed[i] = true
		}
	}
	return vetoed
}

// alignSyllables 依候選的分段把文字對回段內 n 個音節：chars[i] 是第 i 個音節的字。分段字數等於音節數時
// 逐字對應；尾端補全（Partial）的詞面比已打的音節長時，前面的字對已打的音節。分段缺漏、重疊或字數
// 對不上的位置標在 loose，chars 填 utf8.RuneError；全部對得上時 loose 為 nil。沒有分段時字數等於音節數
// 才逐字對應。
func alignSyllables(h decode.Hypothesis, n int) (chars []rune, loose []bool) {
	chars = make([]rune, n)
	if len(h.Segments) == 0 {
		if text := []rune(h.Text); len(text) == n {
			return text, nil
		}
	}
	set := make([]bool, n)
	mark := func(at int) {
		if loose == nil {
			loose = make([]bool, n)
		}
		loose[at] = true
	}
	for _, g := range h.Segments {
		from, to := max(g.Start, 0), min(g.End, n)
		word := []rune(g.Word)
		fits := g.Start >= 0 && g.End <= n && g.Start < g.End && (len(word) == g.End-g.Start || g.Partial && len(word) > g.End-g.Start)
		for at := from; at < to; at++ {
			if set[at] || !fits {
				mark(at)
			} else {
				chars[at] = word[at-g.Start]
			}
			set[at] = true
		}
	}
	for at := range n {
		if !set[at] {
			mark(at)
		}
		if loose != nil && loose[at] {
			chars[at] = utf8.RuneError
		}
	}
	return chars, loose
}

// sameSegmentAt 回報兩個候選蓋住第 at 個音節的分段是否完全相同（同一個詞、同一個區間），也就是這個位置沒有改寫。
func sameSegmentAt(a, b decode.Hypothesis, at int) bool {
	find := func(h decode.Hypothesis) (decode.Seg, bool) {
		for _, g := range h.Segments {
			if g.Start <= at && at < g.End {
				return g, true
			}
		}
		return decode.Seg{}, false
	}
	x, ok := find(a)
	y, found := find(b)
	return ok && found && x.Word == y.Word && x.Start == y.Start && x.End == y.End && x.Partial == y.Partial
}

// vetoInputs 是 rerankKey 以外、排除候選時還會用到的候選欄位：各段的詞與兩個上限旗標。
func vetoInputs(rows []decode.Hypothesis) string {
	var b strings.Builder
	for _, c := range rows {
		b.WriteString(strconv.FormatBool(c.NonWordRepeat))
		b.WriteString(strconv.FormatBool(c.WeakPrefix))
		for _, g := range c.Segments {
			b.WriteString(strconv.Itoa(len(g.Word)))
			b.WriteByte(':')
			b.WriteString(g.Word)
		}
		b.WriteByte('|')
	}
	return b.String()
}

// rerankKey 只含決定結果的輸入：前文、讀音與候選（文字、分數、分段）。
func rerankKey(r rerank.Request) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%q|%s|%v|%s|", r.Context, zhuyin.Key(r.Reading), r.AllowPartial, r.Mode)
	for _, c := range r.Candidates {
		fmt.Fprintf(&b, "%q:%.9g:", c.Text, c.LogScore)
		for _, g := range c.Segments {
			fmt.Fprintf(&b, "%d-%d-%v;", g.Start, g.End, g.Partial)
		}
		b.WriteByte('|')
	}
	return b.String()
}

// decide 依 rerank.Choose 決定顯示的候選，另找融合分數最高的其他候選作為提示；分數無效時不提示。
// vetoed 的候選推翻了使用者明確的選擇，既不顯示也不提示；原首選 rows[0] 不會被排除。
func decide(rows []decode.Hypothesis, scores []float64, weight, margin float64, vetoed []bool) rerankDecision {
	if len(rows) != len(scores) {
		return rerankDecision{winner: rerank.Choose(rows, scores, weight, margin), second: -1}
	}
	if vetoed != nil {
		var keptRows []decode.Hypothesis
		var keptScores []float64
		var index []int
		for i := range rows {
			if i == 0 || !vetoed[i] {
				keptRows = append(keptRows, rows[i])
				keptScores = append(keptScores, scores[i])
				index = append(index, i)
			}
		}
		d := decide(keptRows, keptScores, weight, margin, nil)
		d.winner = index[d.winner]
		if d.second >= 0 {
			d.second = index[d.second]
		}
		return d
	}
	d := rerankDecision{winner: rerank.Choose(rows, scores, weight, margin), second: -1}
	fused := make([]float64, len(rows))
	for i, c := range rows {
		fused[i] = c.LogScore + weight*scores[i]
		if !finite(fused[i]) {
			return d
		}
	}
	for i := range rows {
		if i != d.winner && (d.second < 0 || fused[i] > fused[d.second]) {
			d.second = i
		}
	}
	if d.second >= 0 {
		d.gap = fused[d.winner] - fused[d.second]
	}
	return d
}

func finite(x float64) bool { return x == x && x-x == 0 }

// plainSegments：提示接受時逐詞鎖定，只允許完整的詞典詞（不含未打完的前綴或錯字修正）。
func plainSegments(segs []decode.Seg) bool {
	for _, g := range segs {
		if g.Partial || g.Correction {
			return false
		}
	}
	return len(segs) > 0
}

// acceptHint 把提示候選逐詞鎖定（視同手選），之後解碼必須顯示該候選。
func (s *Session) acceptHint() bool {
	h := s.hint
	if h == nil {
		return false
	}
	for _, g := range h.segs {
		for at, lock := range s.locks {
			if at < g.End && g.Start < at+lock.Len {
				return false // 不跨越既有手選
			}
		}
	}
	for _, g := range h.segs {
		s.locks[g.Start] = decode.Lock{Word: g.Word, Len: g.End - g.Start, Suggested: true}
	}
	s.hint = nil
	return true
}
