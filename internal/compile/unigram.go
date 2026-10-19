package compile

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"
)

// unigramHeader 是 lm/unigram.tsv 的第一行前綴，後面接各來源 ID（以 tab 分隔）。
const unigramHeader = "# 詞"

// Unigram 是 lm/unigram.tsv：每個詞一行，各來源的次數各一欄；空格表示該來源沒有這個詞。
// 欄位由 0c-import、0c-corpus 整欄更新，不手改。次數照原字串保存，重寫不改變數值。
type Unigram struct {
	Sources []string
	Words   []string            // 依字串排序
	Cells   map[string][]string // 詞 → 各來源的次數字串，與 Sources 對齊
}

// ReadUnigram 讀 lm/unigram.tsv。
func ReadUnigram(p string) (*Unigram, error) {
	fh, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	u := &Unigram{Cells: map[string][]string{}}
	if !sc.Scan() {
		return nil, fmt.Errorf("空檔案")
	}
	head := strings.Split(strings.TrimRight(sc.Text(), "\r"), "\t")
	if head[0] != unigramHeader || len(head) < 2 {
		return nil, fmt.Errorf("第一行須為 %q 加上各來源 ID", unigramHeader+"\t<來源 ID>…")
	}
	u.Sources = head[1:]
	seen := map[string]bool{}
	for _, s := range u.Sources {
		if s == "" || seen[s] {
			return nil, fmt.Errorf("第一行的來源 ID %q 空白或重複", s)
		}
		seen[s] = true
	}
	for ln := 2; sc.Scan(); ln++ {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != len(u.Sources)+1 || f[0] == "" {
			return nil, fmt.Errorf("第 %d 行須有詞與 %d 個次數欄", ln, len(u.Sources))
		}
		if _, dup := u.Cells[f[0]]; dup {
			return nil, fmt.Errorf("第 %d 行的 %q 重複", ln, f[0])
		}
		u.Words = append(u.Words, f[0])
		u.Cells[f[0]] = f[1:]
	}
	return u, sc.Err()
}

// SetColumn 以 counts（詞 → 次數字串）整欄取代來源 src；沒有這一欄就加在最後。
// 所有欄都空的詞會移除。
func (u *Unigram) SetColumn(src string, counts map[string]string) {
	i := -1
	for j, s := range u.Sources {
		if s == src {
			i = j
		}
	}
	if i < 0 {
		u.Sources = append(u.Sources, src)
		for w, c := range u.Cells {
			u.Cells[w] = append(c, "")
		}
		i = len(u.Sources) - 1
	}
	for _, c := range u.Cells {
		c[i] = ""
	}
	for w, v := range counts {
		c := u.Cells[w]
		if c == nil {
			c = make([]string, len(u.Sources))
			u.Cells[w] = c
		}
		c[i] = v
	}
	u.Words = u.Words[:0]
	for w, c := range u.Cells {
		if strings.Join(c, "") == "" {
			delete(u.Cells, w)
			continue
		}
		u.Words = append(u.Words, w)
	}
	sort.Strings(u.Words)
}

// Write 寫回 lm/unigram.tsv。
func (u *Unigram) Write(p string) error {
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	fmt.Fprintln(w, unigramHeader+"\t"+strings.Join(u.Sources, "\t"))
	fmt.Fprintln(w, "# 每個詞一行，各來源的次數各一欄，空格表示該來源沒有這個詞。由 0c-import／0c-corpus 整欄更新，不要手改；來源見 sources/ledger.tsv。")
	for _, word := range u.Words {
		fmt.Fprintln(w, word+"\t"+strings.Join(u.Cells[word], "\t"))
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
