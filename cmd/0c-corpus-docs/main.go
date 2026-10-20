// 0c-corpus-docs 由清理、分組後的新聞 JSONL 統計詞次與文件頻率。
// 使用凍結詞庫斷詞；排除與既有評測含相同五字以上片段的整份文件。
// 輸出研究統計，不編譯、不啟用模型。
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/internal/corpus"
	"github.com/0c-biggo/0c-ime/internal/datadir"
	"github.com/0c-biggo/0c-ime/internal/eval"
)

type document struct {
	Body      string   `json:"body"`
	Group     string   `json:"dedup_group"`
	Publisher string   `json:"publisher"`
	Date      string   `json:"publish_date"`
	Bodies    []string `json:"dedup_bodies"`
}

type trie struct {
	end  bool
	next map[rune]*trie
}

func (t *trie) add(s string) {
	for _, r := range s {
		if t.next == nil {
			t.next = map[rune]*trie{}
		}
		if t.next[r] == nil {
			t.next[r] = &trie{}
		}
		t = t.next[r]
	}
	t.end = true
}
func (t *trie) contains(s string) bool {
	rs := []rune(s)
	for i := range rs {
		n := t
		for _, r := range rs[i:] {
			n = n.next[r]
			if n == nil {
				break
			}
			if n.end {
				return true
			}
		}
	}
	return false
}
func overlaps(body string, banned *trie) bool {
	found := false
	corpus.HanChunks(body, 5, func(chunk string) {
		if banned.contains(chunk) {
			found = true
		}
	})
	return found
}

func documentOverlaps(d document, banned *trie) bool {
	if overlaps(d.Body, banned) {
		return true
	}
	for _, body := range d.Bodies {
		if overlaps(body, banned) {
			return true
		}
	}
	return false
}

// 每個去重群組算一份文件。句界與未知字界不建立跨界詞對。
func addDocument(v *corpus.Vocab, c *corpus.Counts, df map[uint32]uint64, bdf map[uint64]uint64, body string) {
	seen := map[uint32]bool{}
	pairs := map[uint64]bool{}
	corpus.HanChunks(body, 1, func(chunk string) {
		v.Segment(chunk, func(ids []uint32) {
			c.Add(v, ids)
			prev := v.BOS
			for _, id := range ids {
				seen[id] = true
				pairs[uint64(prev)<<32|uint64(id)] = true
				prev = id
			}
			pairs[uint64(prev)<<32|uint64(v.EOS)] = true
		})
	})
	for id := range seen {
		df[id]++
	}
	for pair := range pairs {
		bdf[pair]++
	}
}

func hashFile(path string) string {
	b, e := os.ReadFile(path)
	check(e)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func main() {
	input := flag.String("input", "", "prepare_news_corpus 輸出的 JSONL")
	pack := flag.String("vocab", "build/release/cross-commit-calibrated-20260921/data", "固定斷詞資料包")
	exclude := flag.String("exclude", "data/raw/cv/*.txt", "評測全文 glob，僅用於排除，不參與計數")
	out := flag.String("out", "", "新的研究輸出目錄")
	flag.Parse()
	if *input == "" || *out == "" {
		check(fmt.Errorf("需要 -input 與 -out"))
	}
	if _, e := os.Stat(*out); e == nil {
		check(fmt.Errorf("輸出目錄已存在"))
	} else if !os.IsNotExist(e) {
		check(e)
	}
	eng, e := datadir.Open(*pack)
	check(e)
	lex := eng.Model.Lex.(*lexicon.Compact)
	probs := map[string]float64{}
	lex.All(func(e lexicon.Entry) { probs[e.Surface] = eng.Model.LM.Uni(e.Surface) })
	vocab := corpus.NewVocab(probs)
	counts := corpus.NewCounts()
	df := map[uint32]uint64{}
	bdf := map[uint64]uint64{}
	banned := &trie{}
	files, e := filepath.Glob(*exclude)
	check(e)
	if len(files) == 0 {
		check(fmt.Errorf("沒有評測排除檔"))
	}
	inputs := map[string]string{}
	for _, path := range files {
		inputs[path] = hashFile(path)
		f, e := os.Open(path)
		check(e)
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 4096), 16<<20)
		for sc.Scan() {
			for _, chunk := range eval.Chunks(sc.Text(), 5) {
				banned.add(chunk)
			}
		}
		check(sc.Err())
		check(f.Close())
	}
	f, e := os.Open(*input)
	check(e)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 4096), 16<<20)
	seen := map[string]bool{}
	publishers := map[string]int{}
	dates := map[string]int{}
	total, duplicate, excluded, accepted := 0, 0, 0, 0
	var acceptedLines []string
	for sc.Scan() {
		var d document
		check(json.Unmarshal(sc.Bytes(), &d))
		total++
		if d.Group == "" || d.Body == "" {
			check(fmt.Errorf("文件缺少去重群組或正文"))
		}
		if seen[d.Group] {
			duplicate++
			continue
		}
		seen[d.Group] = true
		if documentOverlaps(d, banned) {
			excluded++
			continue
		}
		accepted++
		acceptedLines = append(acceptedLines, sc.Text())
		publishers[d.Publisher]++
		date := d.Date
		if len(date) > 10 {
			date = date[:10]
		}
		dates[date]++
		addDocument(vocab, counts, df, bdf, d.Body)
	}
	check(sc.Err())
	check(f.Close())
	check(os.MkdirAll(*out, 0700))
	write := func(name string, fn func(*bufio.Writer)) {
		f, e := os.Create(filepath.Join(*out, name))
		check(e)
		w := bufio.NewWriter(f)
		fn(w)
		check(w.Flush())
		check(f.Close())
	}
	write("accepted.jsonl", func(w *bufio.Writer) {
		for _, line := range acceptedLines {
			fmt.Fprintln(w, line)
		}
	})
	ids := make([]uint32, 0, len(counts.Uni))
	for id := range counts.Uni {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var tokens uint64
	write("unigram.tsv", func(w *bufio.Writer) {
		fmt.Fprintln(w, "# word\ttoken_count\tdocument_count")
		for _, id := range ids {
			tokens += counts.Uni[id]
			fmt.Fprintf(w, "%s\t%d\t%d\n", vocab.Name(id), counts.Uni[id], df[id])
		}
	})
	pairs := make([]uint64, 0, len(counts.Bi))
	for p := range counts.Bi {
		pairs = append(pairs, p)
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i] < pairs[j] })
	write("bigram.tsv", func(w *bufio.Writer) {
		fmt.Fprintln(w, "# left\tright\tpair_count\tdocument_count\tleft_count")
		for _, p := range pairs {
			l, r := uint32(p>>32), uint32(p)
			fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%d\n", vocab.Name(l), vocab.Name(r), counts.Bi[p], bdf[p], counts.Left[l])
		}
	})
	report := map[string]any{"input_sha256": hashFile(*input), "tokenizer_manifest_sha256": hashFile(filepath.Join(*pack, "manifest.json")), "evaluation_exclusions": inputs,
		"documents": total, "duplicate_groups_skipped": duplicate, "evaluation_overlap_documents_excluded": excluded, "counted_documents": accepted,
		"tokens": tokens, "unique_words": len(ids), "unique_pairs_including_boundaries": len(pairs), "publishers": publishers, "dates": dates,
		"unigram_sha256": hashFile(filepath.Join(*out, "unigram.tsv")), "bigram_sha256": hashFile(filepath.Join(*out, "bigram.tsv")),
		"accepted_sha256": hashFile(filepath.Join(*out, "accepted.jsonl")),
		"limits":          []string{"Maximum-unigram tokenizer with frozen existing vocabulary; cannot discover new words or readings.", "Whole-group exclusion on any member containing an exact evaluation Han substring of at least 5 characters; conservative false positives possible, paraphrase overlap remains undetected.", "Sampling and partition policy defined upstream; this command only counts the supplied partition and does not import models.", "Document counts use one representative per prepared near-duplicate group; title and body not counted twice."}}
	b, e := json.MarshalIndent(report, "", "  ")
	check(e)
	check(os.WriteFile(filepath.Join(*out, "report.json"), append(b, '\n'), 0644))
	fmt.Println(string(b))
}
