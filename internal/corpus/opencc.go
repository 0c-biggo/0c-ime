// Package corpus 從外部語料（目前是中文維基百科）產生 n-gram 統計：抽取文字、轉台灣繁體、
// 以詞庫斷詞、計數。只用於離線資料管線。
package corpus

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Stage 是一個轉換階段：以最長匹配把詞典的鍵換成值（多個值取第一個）。
type Stage struct {
	dict   map[string]string
	maxLen int // 鍵的最大字數
}

// LoadStage 讀取一或多個 OpenCC 詞典檔（「鍵<TAB>值 值…」）。
func LoadStage(paths ...string) (*Stage, error) {
	s := &Stage{dict: map[string]string{}}
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			l := sc.Text()
			if l == "" || l[0] == '#' {
				continue
			}
			k, v, ok := strings.Cut(l, "\t")
			if !ok {
				continue
			}
			if i := strings.IndexByte(v, ' '); i >= 0 {
				v = v[:i]
			}
			s.dict[k] = v
			if n := utf8.RuneCountInString(k); n > s.maxLen {
				s.maxLen = n
			}
		}
		f.Close()
		if err := sc.Err(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Convert 以最長匹配轉換。
func (s *Stage) Convert(in string) string {
	var b strings.Builder
	b.Grow(len(in))
	// 字元邊界
	bounds := make([]int, 0, len(in)/3+1)
	for i := range in {
		bounds = append(bounds, i)
	}
	bounds = append(bounds, len(in))
	for i := 0; i < len(bounds)-1; {
		matched := false
		for n := min(s.maxLen, len(bounds)-1-i); n >= 1; n-- {
			if v, ok := s.dict[in[bounds[i]:bounds[i+n]]]; ok {
				b.WriteString(v)
				i += n
				matched = true
				break
			}
		}
		if !matched {
			b.WriteString(in[bounds[i]:bounds[i+1]])
			i++
		}
	}
	return b.String()
}

// Converter 依序套用多個階段。
type Converter []*Stage

// S2TWP 建立 OpenCC 的 s2twp 流程：簡轉繁（詞組、單字）→ 台灣用語 → 台灣字形。
func S2TWP(dir string) (Converter, error) {
	st, err := LoadStage(filepath.Join(dir, "STPhrases.txt"), filepath.Join(dir, "STCharacters.txt"))
	if err != nil {
		return nil, err
	}
	tp, err := LoadStage(filepath.Join(dir, "TWPhrases.txt"))
	if err != nil {
		return nil, err
	}
	tv, err := LoadStage(filepath.Join(dir, "TWVariants.txt"))
	if err != nil {
		return nil, err
	}
	return Converter{st, tp, tv}, nil
}

func (c Converter) Convert(s string) string {
	for _, st := range c {
		s = st.Convert(s)
	}
	return s
}

// ---------------------------------------------------------------- 維基

// Pages 串流讀取 MediaWiki 匯出 XML，對每個主命名空間、非重新導向的條目呼叫 fn（已解除 XML 跳脫）。
// 以逐行掃描取代完整的 XML 解析：匯出檔的 <ns>、<redirect>、<text> 各自成行，速度快很多。
func Pages(r io.Reader, fn func(text string)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	var ns string
	var redirect, inText bool
	var text strings.Builder
	for sc.Scan() {
		l := sc.Text()
		t := strings.TrimSpace(l)
		switch {
		case inText:
			if i := strings.Index(l, "</text>"); i >= 0 {
				text.WriteString(l[:i])
				inText = false
				if ns == "0" && !redirect {
					fn(unescape(text.String()))
				}
				text.Reset()
			} else {
				text.WriteString(l)
				text.WriteByte('\n')
			}
		case t == "<page>":
			ns, redirect = "", false
		case strings.HasPrefix(t, "<ns>"):
			ns = strings.TrimSuffix(strings.TrimPrefix(t, "<ns>"), "</ns>")
		case strings.HasPrefix(t, "<redirect"):
			redirect = true
		case strings.HasPrefix(t, "<text"):
			i := strings.Index(l, ">")
			if strings.HasSuffix(t, "/>") {
				continue
			}
			rest := l[i+1:]
			if j := strings.Index(rest, "</text>"); j >= 0 {
				if ns == "0" && !redirect {
					fn(unescape(rest[:j]))
				}
				continue
			}
			inText = true
			text.WriteString(rest)
			text.WriteByte('\n')
		}
	}
	return sc.Err()
}

var unescaper = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#039;", "'", "&amp;", "&")

func unescape(s string) string { return unescaper.Replace(s) }

// Clean 移除維基標記中不屬於正文的部分：模板、引用、註解、檔案與分類連結；[[目標|文字]] 保留文字。
// 之後只取純漢字片段，殘留的標記只會把片段切開，不會混進詞裡。
func Clean(s string) string {
	s = dropBetween(s, "<!--", "-->")
	s = dropNested(s, "{{", "}}")
	s = dropNested(s, "{|", "|}")
	s = dropRefs(s)
	var b strings.Builder
	for {
		i := strings.Index(s, "[[")
		if i < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:i])
		j := strings.Index(s[i:], "]]")
		if j < 0 {
			break
		}
		link := s[i+2 : i+j]
		s = s[i+j+2:]
		if k := strings.IndexByte(link, ':'); k > 0 && !strings.Contains(link[:k], "|") {
			continue // File:、Category: 等
		}
		if k := strings.LastIndexByte(link, '|'); k >= 0 {
			link = link[k+1:]
		}
		b.WriteString(link) // 連結文字是正文的一部分，前後不插入分隔
	}
	return b.String()
}

func dropBetween(s, open, close string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, open)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		j := strings.Index(s[i+len(open):], close)
		if j < 0 {
			return b.String()
		}
		s = s[i+len(open)+j+len(close):]
		b.WriteByte(' ')
	}
}

func dropNested(s, open, close string) string {
	var b strings.Builder
	depth := 0
	for i := 0; i < len(s); {
		switch {
		case strings.HasPrefix(s[i:], open):
			depth++
			i += len(open)
		case depth > 0 && strings.HasPrefix(s[i:], close):
			depth--
			i += len(close)
			if depth == 0 {
				b.WriteByte(' ')
			}
		default:
			if depth == 0 {
				b.WriteByte(s[i])
			}
			i++
		}
	}
	return b.String()
}

func dropRefs(s string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, "<ref")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		rest := s[i:]
		end := strings.IndexByte(rest, '>')
		if end < 0 {
			return b.String()
		}
		if rest[end-1] == '/' { // <ref ... />
			s = rest[end+1:]
			continue
		}
		j := strings.Index(rest, "</ref>")
		if j < 0 {
			s = rest[end+1:]
			continue
		}
		s = rest[j+len("</ref>"):]
		b.WriteByte(' ')
	}
}
