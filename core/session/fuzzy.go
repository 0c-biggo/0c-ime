package session

import (
	"slices"
	"strings"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/fuzzy"
	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// fuzzyFirstPage 是候選第一頁為近音保留的位置數（依原音候選相對次序不變為前提）。
// 原音候選不足時近音自然前移；近音更多時其餘接在原音候選之後，不會永遠藏在數十頁後。
const fuzzyFirstPage = 3

// nearWindow 回傳含近音假設的視窗版本；視窗內沒有任何音節有近音替代時回傳 false，
// 不必再算一次前後文。每詞換音次數與評分的詞面數都有上限（fuzzy.MaxWordCost、fuzzy.MaxNearCandidates）。
func (s *Session) nearWindow(lat input.Lattice, pos, length int) (decode.Window, bool) {
	if !s.Fuzzy.Enabled() {
		return decode.Window{}, false
	}
	wide := s.Fuzzy.Widen(lat, s.syls, pos, pos+length)
	for i := pos; i < pos+length; i++ {
		if len(wide[i]) > len(lat[i]) {
			// 只列舉有成本的讀音：使用者打的音已由原音版本負責，不必再評分一次，名額也只計近音詞面。
			return decode.Window{Lat: wide, MaxCost: fuzzy.MaxWordCost, MaxCandidates: fuzzy.MaxNearCandidates, OnlyCosted: true}, true
		}
	}
	return decode.Window{}, false
}

// nearOnly 從近音視窗的候選中取出只能由近音讀音到達者，連同各自在詞庫中的讀音。
// 與原音候選同詞面者一律歸原音：原音來源與排序不因近音存在而改變。
func (s *Session) nearOnly(all, exact []decode.Candidate, pos, length int) (items []decode.Candidate, matched []string) {
	typed := zhuyin.Key(s.syls[pos : pos+length])
	have := map[string]bool{}
	for _, c := range exact {
		have[c.Entry.Surface] = true
	}
	for _, c := range all {
		key := zhuyin.Key(c.Entry.Reading)
		if key == typed || have[c.Entry.Surface] {
			continue
		}
		items = append(items, c)
		matched = append(matched, key)
	}
	return items, matched
}

// mergeFuzzy 把近音候選接進原音候選：原音候選保持相對次序，第一頁尾端固定留給近音，其餘近音
// 接在剩下的原音候選之後。回傳的 matched 與 items 等長；原音為空字串。
func mergeFuzzy(exact, near []decode.Candidate, nearMatched []string) (items []decode.Candidate, matched []string) {
	if len(near) == 0 {
		return exact, nil
	}
	quota := min(len(near), fuzzyFirstPage)
	head := min(len(exact), perPage-quota)
	lead := min(len(near), perPage-head)
	items = make([]decode.Candidate, 0, len(exact)+len(near))
	matched = make([]string, 0, cap(items))
	items = append(items, exact[:head]...)
	matched = append(matched, make([]string, head)...)
	items = append(items, near[:lead]...)
	matched = append(matched, nearMatched[:lead]...)
	items = append(items, exact[head:]...)
	matched = append(matched, make([]string, len(exact)-head)...)
	items = append(items, near[lead:]...)
	matched = append(matched, nearMatched[lead:]...)
	return items, matched
}

// nearNote 是候選的附註；原音候選沒有。
func nearNote(matched string) string {
	if matched == "" {
		return ""
	}
	return "近音 " + matched
}

// fuzzyDecodable 回報展開近音之後，目前輸入的每個字面符號分段是否都解得出路徑（只問有沒有路徑，
// 不評分）。輸入端據此接受「精確讀音沒有字、近音可成詞」的音節。解出的路徑不顯示、不保存，
// 不更動任何 session 狀態。
func (s *Session) fuzzyDecodable() bool {
	if !s.Fuzzy.Enabled() || len(s.syls) == 0 {
		return false
	}
	lat := s.Fuzzy.Widen(s.lattice(), s.syls, 0, len(s.syls))
	maxCost := s.dec.MaxCost
	s.dec.MaxCost = fuzzy.MaxWordCost
	defer func() { s.dec.MaxCost = maxCost }()
	for start := 0; start < len(lat); {
		if s.literals[start] {
			start++
			continue
		}
		end := start + 1
		for end < len(lat) && !s.literals[end] {
			end++
		}
		if len(s.dec.Decode(lat[start:end], s.locksIn(start, end), s.Opt.PrefixAware)) == 0 {
			return false
		}
		start = end
	}
	return true
}

// locksIn 回傳完全落在 [begin, end) 內的鎖定，位置換成相對於 begin。
func (s *Session) locksIn(begin, end int) map[int]decode.Lock {
	locks := map[int]decode.Lock{}
	for at, lock := range s.locks {
		if at >= begin && at+lock.Len <= end {
			locks[at-begin] = lock
		}
	}
	return locks
}

// lockLattice 在已鎖定的跨音區段補上它的詞庫讀音，讓鎖定的詞在解碼時仍有路徑；
// 設定之後關閉也不受影響——鎖定存在，路徑就存在。與 [skipFrom, skipTo) 相交的鎖定不補：
// 開候選時那些鎖定即將被選取取代，不能再當前後文或原音候選的讀音來源。
// 成本取 0：鎖定限制了該起點的詞，通過這個讀音的路徑都會等量扣除同一個成本，不影響排序；
// 取 0 也不佔用候選查詢的每詞換音額度（Decoder.MaxCost）。
func (s *Session) lockLattice(lat input.Lattice, skipFrom, skipTo int) input.Lattice {
	for at, lock := range s.locks {
		if lock.Matched == "" || at < skipTo && skipFrom < at+lock.Len {
			continue
		}
		matched := zhuyin.ParseReading(lock.Matched)
		if len(matched) != lock.Len || at < 0 || at+lock.Len > len(lat) {
			continue
		}
		for i, syl := range matched {
			if !slices.ContainsFunc(lat[at+i], func(h input.Hyp) bool { return h.Syl == syl }) {
				lat[at+i] = append(lat[at+i], input.Hyp{Syl: syl})
			}
		}
	}
	return lat
}

// matchedFor 回傳 [start, start+length) 若有跨音鎖定覆蓋，各位置採用的詞庫讀音（沒有鎖定的位置用
// 使用者打的音）；完全沒有跨音鎖定相交時回傳空字串。鎖定被標點切開時，兩半各自帶走自己的讀音。
func (s *Session) matchedFor(start, length int) string {
	syls := slices.Clone(s.syls[start : start+length])
	touched := false
	for at, lock := range s.locks {
		if lock.Matched == "" {
			continue
		}
		matched := zhuyin.ParseReading(lock.Matched)
		if len(matched) != lock.Len {
			continue
		}
		for i, syl := range matched {
			if pos := at + i; pos >= start && pos < start+length {
				syls[pos-start] = syl
				touched = true
			}
		}
	}
	if !touched {
		return ""
	}
	return strings.TrimSpace(zhuyin.Key(syls))
}

// fuzzyTouches 回報路徑中 [start, end) 是否與任何跨音鎖定或自動近音修正相交；這樣的段落不進學習。
func (s *Session) fuzzyTouches(start, end int) bool {
	for at, lock := range s.locks {
		if lock.Matched != "" && at < end && start < at+lock.Len {
			return true
		}
	}
	return slices.ContainsFunc(s.path, func(g decode.Seg) bool { return g.Matched != "" && g.Start < end && start < g.End })
}

// autoFuzzed 回報目前顯示的路徑是否含自動近音修正。
func (s *Session) autoFuzzed() bool {
	return slices.ContainsFunc(s.path, func(g decode.Seg) bool { return g.Matched != "" })
}
