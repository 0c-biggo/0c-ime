package personal

import (
	"slices"
	"sort"

	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 更正是有界的條件偏好，不刪除不同語境的歷史計數。8 是工程初值，
// 代表新更正對不相符文字最多施加 8 個自然對數的成本，30 日後減半。
const correctionCost = 8.0
const maxCorrections = 2048
const maxCorrectionLength = 8

type Correction struct {
	Reading             string
	Left, Right         zhuyin.Syllable // 空值表示組字／字面符號邊界，不是任意語境
	LeftWord, RightWord string          // 只供撤銷相鄰詞的學習依賴，不作為手選答案
	Word                string
	Updated             int64
	Serial              uint64
	Usage               UsageEvidence
}

func correctionKey(reading string, left, right zhuyin.Syllable) string {
	return reading + "\t" + string(left) + "\t" + string(right)
}

// CorrectionFor 在片語合併之前按原始選取區間取證；字面符號不算鄰接讀音。
func CorrectionFor(syls []zhuyin.Syllable, g Seg) (Correction, bool) {
	if !g.Manual || g.Completion || g.Partial || g.Start < 0 || g.End > len(syls) || g.End <= g.Start || g.End-g.Start > maxCorrectionLength || len([]rune(g.Word)) != g.End-g.Start {
		return Correction{}, false
	}
	for _, y := range syls[g.Start:g.End] {
		if !y.Legal() {
			return Correction{}, false
		}
	}
	c := Correction{Reading: zhuyin.Key(syls[g.Start:g.End]), Word: g.Word}
	if g.Start > 0 && syls[g.Start-1].Legal() {
		c.Left = syls[g.Start-1]
	}
	if g.End < len(syls) && syls[g.End].Legal() {
		c.Right = syls[g.End]
	}
	return c, true
}

// contestCorrections 處理與這次手選矛盾的已存更正：只看在這串音節裡會生效、且與 [start,end)
// 重疊的紀錄（與 CorrectionPenalty 同一套比對）。逐筆依強度處理：
//   - 已衰減過半：照舊由這次選取取代。
//   - 近期確認過不只一次：扣掉一次確認、保留紀錄，一次反向選取不抹掉長期習慣。
//   - 近期只確認過一次：與這次選取互相抵銷，刪除紀錄。
//
// 只要有近期紀錄被反駁就回報 true，這次選取不建立新紀錄：該位置交回模型（或仍同意這次選取的
// 其他紀錄）依上下文判斷，再選一次才記住。避免最後一次（試打、故意選錯）以全強度蓋過先前的選擇。
func (m *Model) contestCorrections(syls []zhuyin.Syllable, start, end int, word string) (contested bool) {
	return m.resolveCorrections(syls, start, end, word, false)
}

// supersedeCorrections 用於親手加入自訂詞（#122）：這是對這組讀音最新的明確選擇，與它不一致的舊更正直接刪除，
// 不像一般手選那樣先削弱、累積到第二次才換掉。回報有沒有刪掉任何更正（使用者以前選過別的寫法）。
func (m *Model) supersedeCorrections(syls []zhuyin.Syllable, start, end int, word string) bool {
	return m.resolveCorrections(syls, start, end, word, true)
}

func (m *Model) resolveCorrections(syls []zhuyin.Syllable, start, end int, word string, force bool) (contested bool) {
	chars := []rune(word)
	unit := m.P.WManual - m.P.WPassive
	for i := max(0, start-maxCorrectionLength+1); i < end; i++ {
		if !syls[i].Legal() {
			continue
		}
		var left zhuyin.Syllable
		if i > 0 && syls[i-1].Legal() {
			left = syls[i-1]
		}
		for j := i + 1; j <= len(syls) && j-i <= maxCorrectionLength; j++ {
			if !syls[j-1].Legal() {
				break
			}
			if j <= start {
				continue
			}
			var right zhuyin.Syllable
			if j < len(syls) && syls[j].Legal() {
				right = syls[j]
			}
			k := correctionKey(zhuyin.Key(syls[i:j]), left, right)
			old, ok := m.Store.Corrections[k]
			if !ok || m.Store.Tombstones[old.Word] {
				continue
			}
			oldChars := []rune(old.Word)
			if len(oldChars) != j-i {
				continue
			}
			agree := true
			for p := max(i, start); p < min(j, end); p++ {
				if oldChars[p-i] != chars[p-start] {
					agree = false
					break
				}
			}
			if agree {
				continue
			}
			u := old.Usage.at(m.Store.Time)
			switch {
			case force:
				delete(m.Store.Corrections, k)
				contested = true
			case decay(1, m.Store.Time-old.Updated, 30*daySeconds) < 0.5:
				delete(m.Store.Corrections, k)
			case unit > 0 && max(u.Short, u.Long) >= 1.5*unit:
				u.Short, u.Long = max(0, u.Short-unit), max(0, u.Long-unit)
				old.Usage = u
				m.Store.Corrections[k] = old
				contested = true
			default:
				delete(m.Store.Corrections, k)
				contested = true
			}
		}
	}
	return contested
}

func (m *Model) learnCorrections(c Commit) {
	selected := filter(c.Selected, func(g Seg) bool { return !g.Fuzzy })
	for _, g := range slices.Concat(c.Path, selected) {
		x, ok := CorrectionFor(c.Syls, g)
		if !ok {
			continue
		}
		// 故意推翻錯音修正的區間沿用不學習規則。
		blocked := false
		for _, r := range c.Rejected {
			if r.Correction && r.Start < g.End && g.Start < r.End {
				blocked = true
				break
			}
		}
		if blocked {
			continue
		}
		for _, neighbor := range c.Path {
			// 被較長的詞包住的手選，依附的是包住它的那個詞。
			if neighbor.End == g.Start || neighbor.Start < g.Start && g.Start < neighbor.End {
				x.LeftWord = neighbor.Word
			}
			if neighbor.Start == g.End || neighbor.Start < g.End && g.End < neighbor.End {
				x.RightWord = neighbor.Word
			}
		}
		if m.contestCorrections(c.Syls, g.Start, g.End, x.Word) {
			continue
		}
		k := correctionKey(x.Reading, x.Left, x.Right)
		old := m.Store.Corrections[k]
		if old.Word == x.Word {
			x.Usage = old.Usage
		}
		m.Store.CorrectionSerial++
		x.Serial = m.Store.CorrectionSerial
		x.Updated = m.Store.Time
		x.Usage = x.Usage.add(m.Store.Time, max(0, m.P.WManual-m.P.WPassive))
		m.Store.Corrections[k] = x
	}
	// 固定順序逐出最早明確選取；舊個人詞條、讀音及一般計數不受影響。
	if len(m.Store.Corrections) > maxCorrections {
		keys := make([]string, 0, len(m.Store.Corrections))
		for k := range m.Store.Corrections {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			a, b := m.Store.Corrections[keys[i]], m.Store.Corrections[keys[j]]
			if a.Serial != b.Serial {
				return a.Serial < b.Serial
			}
			return keys[i] < keys[j]
		})
		for _, k := range keys[:len(keys)-maxCorrections] {
			delete(m.Store.Corrections, k)
		}
	}
}

// CorrectionPenalty 只比對已輸入的音節，按位置施加成本，因此分詞或片語升格
// 不會繞過更正。重疊偏好以最新明確選取為準，不把多個視窗重複累加。
// overrides 是當次仍有效的手選；不持久化、不衰減，優先於已提交的偏好。
func (m *Model) CorrectionPenalty(syls []zhuyin.Syllable, overrides []Seg) func(int, int, string) float64 {
	if len(m.Store.Corrections) == 0 && len(overrides) == 0 {
		return nil
	}
	local := map[string]Correction{}
	for _, g := range overrides {
		if c, ok := CorrectionFor(syls, g); ok {
			c.Serial = ^uint64(0)
			c.Updated = m.Store.Time
			local[correctionKey(c.Reading, c.Left, c.Right)] = c
		}
	}
	type choice struct {
		char   rune
		cost   float64
		serial uint64
		key    string
	}
	chosen := make([]choice, len(syls))
	have := false
	pick := func(i int, k string, c Correction) {
		if m.Store.Tombstones[c.Word] {
			return
		}
		chars := []rune(c.Word)
		cost := correctionCost * decay(1, m.Store.Time-c.Updated, 30*daySeconds) / float64(len(chars))
		for at, ch := range chars {
			p := &chosen[i+at]
			if p.cost == 0 || c.Serial > p.serial || c.Serial == p.serial && k > p.key {
				*p = choice{ch, cost, c.Serial, k}
				have = true
			}
		}
	}
	untoned := zhuyin.HasUntoned(syls)
	if untoned {
		// 聲調選填（#150）：紀錄存的是詞庫讀音，沒打聲調的音節與同基底的任何聲調相容。只有這種輸入才走這裡，
		// 全部打了聲調時照下面原本的精確比對。
		m.untonedCorrections(syls, pick)
	}
	for i := 0; i < len(syls) && !untoned; i++ {
		if !syls[i].Legal() {
			continue
		}
		var left zhuyin.Syllable
		if i > 0 && syls[i-1].Legal() {
			left = syls[i-1]
		}
		for j := i + 1; j <= len(syls) && j-i <= maxCorrectionLength; j++ {
			if !syls[j-1].Legal() {
				break
			}
			var right zhuyin.Syllable
			if j < len(syls) && syls[j].Legal() {
				right = syls[j]
			}
			k := correctionKey(zhuyin.Key(syls[i:j]), left, right)
			c, ok := local[k]
			if !ok {
				c, ok = m.Store.Corrections[k]
			}
			if !ok || m.Store.Tombstones[c.Word] {
				continue
			}
			chars := []rune(c.Word)
			if len(chars) != j-i {
				continue
			}
			cost := correctionCost * decay(1, m.Store.Time-c.Updated, 30*daySeconds) / float64(len(chars))
			for at, ch := range chars {
				p := &chosen[i+at]
				if p.cost == 0 || c.Serial > p.serial || c.Serial == p.serial && k > p.key {
					*p = choice{ch, cost, c.Serial, k}
					have = true
				}
			}
		}
	}
	if !have {
		return nil
	}
	return func(start, end int, word string) float64 {
		if start < 0 || end > len(chosen) || end <= start {
			return 0
		}
		chars := []rune(word)
		if len(chars) < end-start {
			return 0
		}
		total := 0.0
		for i := start; i < end; i++ {
			if chosen[i].cost > 0 && chars[i-start] != chosen[i].char {
				total += chosen[i].cost
			}
		}
		return total
	}
}

// CorrectionUsage 補全只增加「同一個手選詞」的額外證據，不能因選過單字就
// 強化它的所有延伸詞。沒有已輸入右文時，不套用要求右鄰的紀錄。
func (m *Model) CorrectionUsage(syls []zhuyin.Syllable) func(int, string) float64 {
	if len(m.Store.Corrections) == 0 {
		return nil
	}
	byStart := map[int]map[string]float64{}
	m.indexCorrections()
	if zhuyin.HasUntoned(syls) { // 聲調選填（#150）：見 CorrectionPenalty
		m.untonedCorrections(syls, func(i int, _ string, c Correction) {
			if m.Store.Tombstones[c.Word] {
				return
			}
			u := c.Usage.at(m.Store.Time)
			if byStart[i] == nil {
				byStart[i] = map[string]float64{}
			}
			byStart[i][c.Word] = max(byStart[i][c.Word], u.Short+u.Long)
		})
		return func(start int, word string) float64 { return byStart[start][word] }
	}
	for i := range syls {
		var left zhuyin.Syllable
		if i > 0 && syls[i-1].Legal() {
			left = syls[i-1]
		}
		for _, record := range m.correctionIndex.byPrefix[string(left)+"\t"+string(syls[i])] {
			c, reading := record.c, record.reading
			if m.Store.Tombstones[c.Word] {
				continue
			}
			n := min(len(reading), len(syls)-i)
			match := true
			for j := 0; j < n; j++ {
				if syls[i+j] != reading[j] {
					match = false
					break
				}
			}
			if !match {
				continue
			}
			var right zhuyin.Syllable
			if i+len(reading) < len(syls) && syls[i+len(reading)].Legal() {
				right = syls[i+len(reading)]
			}
			if right != c.Right {
				continue
			}
			u := c.Usage.at(m.Store.Time)
			if byStart[i] == nil {
				byStart[i] = map[string]float64{}
			}
			byStart[i][c.Word] = max(byStart[i][c.Word], u.Short+u.Long)
		}
	}

	return func(start int, word string) float64 { return byStart[start][word] }
}

// forgetCorrections 清掉詞本身或左右鄰接詞符合 has 的更正（UnlearnAll）。
func (m *Model) forgetCorrections(has func(string) bool) {
	r := m.recording()
	for k, c := range m.Store.Corrections {
		if has(c.Word) || has(c.LeftWord) || has(c.RightWord) {
			note(r.corrections, m.Store.Corrections, k)
			delete(m.Store.Corrections, k)
		}
	}
}

// 索引只在模型更新後重建，不在每個按鍵反覆解析最多 2,048 份讀音。
// 依左鄰及首音節分桶；排序固定，個人資料換成重載 Store 時亦失效。
type usageRecord struct {
	c       Correction
	reading []zhuyin.Syllable
}
type correctionIndex struct {
	store    *Store
	gen      int
	serial   uint64
	count    int
	byPrefix map[string][]usageRecord
	byBase   map[string][]usageRecord // 首音節去掉聲調：聲調選填（#150）的相容比對用
}

func (m *Model) indexCorrections() {
	idx := &m.correctionIndex
	if idx.store == m.Store && idx.gen == m.Gen && idx.serial == m.Store.CorrectionSerial && idx.count == len(m.Store.Corrections) {
		return
	}
	*idx = correctionIndex{store: m.Store, gen: m.Gen, serial: m.Store.CorrectionSerial, count: len(m.Store.Corrections), byPrefix: map[string][]usageRecord{}, byBase: map[string][]usageRecord{}}
	keys := make([]string, 0, len(m.Store.Corrections))
	for k := range m.Store.Corrections {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		c := m.Store.Corrections[k]
		reading := zhuyin.ParseReading(c.Reading)
		if len(reading) == 0 {
			continue
		}
		prefix := string(c.Left) + "\t" + string(reading[0])
		idx.byPrefix[prefix] = append(idx.byPrefix[prefix], usageRecord{c, reading})
		base := string(reading[0].Base())
		idx.byBase[base] = append(idx.byBase[base], usageRecord{c, reading})
	}
}

// untonedCorrections 對含沒打聲調音節（#150）的 syls 列出相容的更正紀錄：紀錄的讀音與左右鄰，
// 打了聲調的位置要完全相同，沒打聲調的位置只比基底。fn 收到紀錄起點、鍵與紀錄；同一筆紀錄可能在不同起點各出現一次。
func (m *Model) untonedCorrections(syls []zhuyin.Syllable, fn func(i int, key string, c Correction)) {
	m.indexCorrections()
	usable := func(y zhuyin.Syllable) bool { return y.Legal() || y.Untoned() }
	compatible := func(typed, y zhuyin.Syllable) bool {
		if typed.Untoned() {
			return typed.Base() == y.Base()
		}
		return typed == y
	}
	neighbor := func(at int, stored zhuyin.Syllable) bool {
		if at < 0 || at >= len(syls) || !usable(syls[at]) {
			return stored == ""
		}
		return stored != "" && compatible(syls[at], stored)
	}
	for i := range syls {
		if !usable(syls[i]) {
			continue
		}
		for _, record := range m.correctionIndex.byBase[string(syls[i].Base())] {
			c, reading := record.c, record.reading
			j := i + len(reading)
			if j > len(syls) || len(reading) > maxCorrectionLength || len([]rune(c.Word)) != len(reading) {
				continue
			}
			match := true
			for k, y := range reading {
				if !usable(syls[i+k]) || !compatible(syls[i+k], y) {
					match = false
					break
				}
			}
			if match && neighbor(i-1, c.Left) && neighbor(j, c.Right) {
				fn(i, correctionKey(c.Reading, c.Left, c.Right), c)
			}
		}
	}
}
