package vocab

import (
	"fmt"
	"slices"
	"strings"

	"git.biggo.com/0c/ime/core/zhuyin"
)

// node 是讀音前綴樹的一個節點：自訂詞最長 32 字，比一般系統詞的 8 個音節（decode.MaxWordLen）長得多，
// 所以另建這棵樹，只有前綴相符時才往後找。
type node struct {
	children map[zhuyin.Syllable]*node
	words    []int // 讀音恰好走到這裡的詞（f.Words 的位置），依檔案順序
	size     int   // 子樹中的讀音數；HasPrefix 用
}

// Match 是一個讀音命中的自訂詞。
type Match struct {
	Text    string
	Reading []zhuyin.Syllable
}

// reindex 依檔案內容重建索引：只用有效、在上限內的詞；其餘記進 problems，檔案內容不動。
func (s *Set) reindex() {
	s.index = node{}
	s.byText = map[string]int{}
	s.english = nil
	s.problems = nil
	s.longest = 0
	for i, w := range s.f.Words {
		if _, dup := s.byText[w.Text]; dup {
			s.problems = append(s.problems, fmt.Sprintf("「%s」重複出現，只使用第一筆", w.Text))
			continue
		}
		if len(s.byText) >= MaxWords {
			s.problems = append(s.problems, fmt.Sprintf("自訂詞超過上限 %d，第 %d 筆以後沒有使用", MaxWords, i+1))
			break
		}
		norm, err := Normalize(w.Text, w.Readings, i)
		if err != nil || norm.Text != w.Text {
			msg := "詞面前後有空白"
			if err != nil {
				msg = err.Msg
			}
			s.problems = append(s.problems, "沒有使用："+msg)
			continue
		}
		s.byText[w.Text] = i
		if IsEnglish(w.Text) {
			if s.english == nil {
				s.english = map[string]bool{}
			}
			s.english[w.Text] = true
			continue
		}
		for _, key := range norm.Readings {
			syls := zhuyin.ParseReading(key)
			s.longest = max(s.longest, len(syls))
			n := &s.index
			n.size++
			for _, syl := range syls {
				if n.children == nil {
					n.children = map[zhuyin.Syllable]*node{}
				}
				c := n.children[syl]
				if c == nil {
					c = &node{}
					n.children[syl] = c
				}
				n = c
				n.size++
			}
			n.words = append(n.words, i)
		}
	}
}

func (s *Set) find(syls []zhuyin.Syllable) *node {
	n := &s.index
	for _, syl := range syls {
		if n = n.children[syl]; n == nil {
			return nil
		}
	}
	return n
}

// Exact 回傳讀音恰為 syls 的自訂詞，依檔案順序。
func (s *Set) Exact(syls []zhuyin.Syllable) []Match {
	n := s.find(syls)
	if n == nil || len(syls) == 0 {
		return nil
	}
	out := make([]Match, 0, len(n.words))
	for _, i := range n.words {
		out = append(out, Match{Text: s.f.Words[i].Text, Reading: slices.Clone(syls)})
	}
	return out
}

// HasPrefix 回報是否有讀音以 syls 開頭的自訂詞（含恰好相等）。
func (s *Set) HasPrefix(syls []zhuyin.Syllable) bool {
	n := s.find(syls)
	return n != nil && n.size > 0
}

// Prefixed 回傳讀音以 syls 開頭的自訂詞（含恰好相等），依讀音長度、檔案順序排列。
// limit 大於 0 時至多回傳 limit 筆。
func (s *Set) Prefixed(syls []zhuyin.Syllable, limit int) []Match {
	n := s.find(syls)
	if n == nil || len(syls) == 0 {
		return nil
	}
	var out []Match
	level := []struct {
		n    *node
		path []zhuyin.Syllable
	}{{n, slices.Clone(syls)}}
	for len(level) > 0 { // 逐層走訪：短的讀音先出現，結果不依 map 走訪順序
		var next []struct {
			n    *node
			path []zhuyin.Syllable
		}
		for _, it := range level {
			for _, i := range it.n.words {
				out = append(out, Match{Text: s.f.Words[i].Text, Reading: it.path})
				if limit > 0 && len(out) >= limit {
					return out
				}
			}
			keys := make([]zhuyin.Syllable, 0, len(it.n.children))
			for k := range it.n.children {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			for _, k := range keys {
				next = append(next, struct {
					n    *node
					path []zhuyin.Syllable
				}{it.n.children[k], append(it.path[:len(it.path):len(it.path)], k)})
			}
		}
		level = next
	}
	return out
}

// HasEnglish 回報 raw 是否恰好是一個英文詞（大小寫照字面）。混打時這串按鍵照字面顯示、提交（#146）。
func (s *Set) HasEnglish(raw string) bool { return s.english[raw] }

// HasEnglishPrefix 回報是否有英文詞以 raw 開頭（含恰好相等）。英文詞裡有聲調鍵（ipv6、s3）時，
// 混打打到一半的那個數字不當聲調。英文詞不多（使用者親手加的），逐一比對即可。
func (s *Set) HasEnglishPrefix(raw string) bool {
	if raw == "" {
		return false
	}
	for w := range s.english {
		if strings.HasPrefix(w, raw) {
			return true
		}
	}
	return false
}

// EnglishLen 回報有效的英文詞數。
func (s *Set) EnglishLen() int { return len(s.english) }
