// Package english provides bounded, read-only English candidate lookup.
// Hosts supply vocabulary; the core does not load files or access the network.
package english

import (
	"cmp"
	"math"
	"slices"
	"sort"
	"strings"
)

const MaxWordBytes = 48

// Word.Weight is a ranking prior, not an observed usage count.
type Word struct {
	Text   string
	Weight float64
}

type Candidate struct {
	Text     string
	Distance int
	Weight   float64
}

// Dictionary is immutable after construction and can be shared by sessions.
// A compact trie covers every word. Each subtree remembers its best suggestible
// word, so prefix queries need not scan or truncate an alphabetical range.
type Dictionary struct {
	words     []Word
	nodes     []trieNode
	noSuggest []bool
}

type trieNode struct {
	child, next, word, best uint32 // indices + 1; zero means absent
	letter                  byte
}

const MaxCandidates = 32

func New(words []Word) *Dictionary {
	return NewFiltered(words, nil)
}

// NewFiltered keeps blocked words recognizable but never recommends them.
func NewFiltered(words []Word, noSuggest []string) *Dictionary {
	m := map[string]Word{}
	for _, w := range words {
		if !asciiLetters(w.Text) {
			continue
		}
		w.Text = strings.ToLower(w.Text)
		if !letters(w.Text) || len(w.Text) > MaxWordBytes || w.Weight <= 0 || math.IsNaN(w.Weight) || math.IsInf(w.Weight, 0) {
			continue
		}
		if prev, ok := m[w.Text]; !ok || w.Weight > prev.Weight {
			m[w.Text] = w
		}
	}
	d := &Dictionary{}
	for _, w := range m {
		d.words = append(d.words, w)
	}
	sort.Slice(d.words, func(i, j int) bool { return d.words[i].Text < d.words[j].Text })
	d.noSuggest = make([]bool, len(d.words))
	for _, text := range noSuggest {
		text = strings.ToLower(text)
		i := sort.Search(len(d.words), func(i int) bool { return d.words[i].Text >= text })
		if i < len(d.words) && d.words[i].Text == text {
			d.noSuggest[i] = true
		}
	}
	d.buildIndex()
	return d
}

func letters(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < 'a' || c > 'z' {
			return false
		}
	}
	return true
}

func asciiLetters(s string) bool {
	if s == "" {
		return false
	}
	for i := range s {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}

func (d *Dictionary) Has(word string) bool {
	if d == nil || len(word) > MaxWordBytes || !asciiLetters(word) {
		return false
	}
	word = strings.ToLower(word)
	i := sort.Search(len(d.words), func(i int) bool { return d.words[i].Text >= word })
	return i < len(d.words) && d.words[i].Text == word
}

// Suggestible distinguishes recognizable no-suggest words from candidates.
func (d *Dictionary) Suggestible(word string) bool {
	if !d.Has(word) {
		return false
	}
	word = strings.ToLower(word)
	i := sort.Search(len(d.words), func(i int) bool { return d.words[i].Text >= word })
	return !d.noSuggest[i]
}

func (d *Dictionary) Prefix(prefix string, limit int) []Candidate {
	if d == nil || limit <= 0 || len(prefix) > MaxWordBytes || !asciiLetters(prefix) {
		return nil
	}
	p := strings.ToLower(prefix)
	if !letters(p) || len(p) > MaxWordBytes {
		return nil
	}
	n := uint32(1)
	for i := range p {
		n = d.child(n, p[i])
		if n == 0 {
			return nil
		}
	}
	limit = min(limit, MaxCandidates)
	var storage [128]prefixItem
	q := prefixHeap{d: d, items: storage[:0]}
	if d.nodes[n-1].best != 0 {
		q.push(prefixItem{node: n, best: d.nodes[n-1].best})
	} else {
		return nil
	}
	out := make([]Candidate, 0, limit)
	for q.Len() > 0 && len(out) < limit {
		item := q.pop()
		if item.node != 0 {
			node := d.nodes[item.node-1]
			if node.word != 0 && !d.noSuggest[node.word-1] {
				q.push(prefixItem{best: node.word})
			}
			for c := node.child; c != 0; c = d.nodes[c-1].next {
				if best := d.nodes[c-1].best; best != 0 {
					q.push(prefixItem{node: c, best: best})
				}
			}
			continue
		}
		w := d.words[item.best-1]
		suffix := w.Text[len(p):]
		if prefix == strings.ToUpper(prefix) {
			suffix = strings.ToUpper(suffix)
		}
		out = append(out, Candidate{Text: prefix + suffix, Weight: w.Weight})
	}
	return out
}

func (d *Dictionary) Corrections(typed string, limit int) []Candidate {
	return d.corrections(typed, limit, 0)
}

// NearCommon checks the whole reachable edit-distance neighbourhood against a
// host-selected prior threshold. Low-prior distance-one words must not hide a
// common distance-two word behind a top-k candidate cutoff.
func (d *Dictionary) NearCommon(typed string, minimumPrior float64) bool {
	if minimumPrior <= 0 || math.IsNaN(minimumPrior) || math.IsInf(minimumPrior, 0) {
		return false
	}
	return len(d.corrections(typed, 1, minimumPrior)) > 0
}

func (d *Dictionary) corrections(typed string, limit int, minimumPrior float64) []Candidate {
	if d == nil || limit <= 0 || len(typed) > MaxWordBytes || !asciiLetters(typed) || d.Has(typed) {
		return nil
	}
	p := strings.ToLower(typed)
	if !letters(p) || len(p) < 3 || len(p) > MaxWordBytes {
		return nil
	}
	maxEdits := 1
	if len(p) >= 6 {
		maxEdits = 2
	}
	limit = min(limit, MaxCandidates)
	var out []Candidate
	allCaps := typed == strings.ToUpper(typed)
	title := typed[0] >= 'A' && typed[0] <= 'Z'
	// One row per trie depth; siblings reuse the same scratch storage. Queries
	// prune only impossible edit-distance branches, never by word position.
	var rows [MaxWordBytes + 1][MaxWordBytes + 1]uint8
	for j := 0; j <= len(p); j++ {
		rows[0][j] = uint8(j)
	}
	var visit func(uint32, int, byte)
	visit = func(id uint32, depth int, previous byte) {
		n := d.nodes[id-1]
		row, prev := &rows[depth], &rows[depth-1]
		row[0] = uint8(depth)
		minimum := row[0]
		for j := 1; j <= len(p); j++ {
			cost := uint8(0)
			if p[j-1] != n.letter {
				cost = 1
			}
			row[j] = min(row[j-1]+1, prev[j]+1, prev[j-1]+cost)
			if depth > 1 && j > 1 && n.letter == p[j-2] && previous == p[j-1] {
				row[j] = min(row[j], rows[depth-2][j-2]+1)
			}
			minimum = min(minimum, row[j])
		}
		if int(minimum) > maxEdits {
			return
		}
		if n.word != 0 && !d.noSuggest[n.word-1] && d.words[n.word-1].Weight >= minimumPrior && int(row[len(p)]) <= maxEdits {
			w, edits := d.words[n.word-1], int(row[len(p)])
			text := w.Text
			if allCaps {
				text = strings.ToUpper(text)
			} else if title {
				text = strings.ToUpper(text[:1]) + text[1:]
			}
			out = append(out, Candidate{Text: text, Distance: edits, Weight: w.Weight})
			rank(out)
			out = out[:min(len(out), limit)]
		}
		for c := n.child; c != 0; c = d.nodes[c-1].next {
			visit(c, depth+1, n.letter)
		}
	}
	for c := d.nodes[0].child; c != 0; c = d.nodes[c-1].next {
		visit(c, 1, 0)
	}
	return out
}

func (d *Dictionary) Len() int {
	if d == nil {
		return 0
	}
	return len(d.words)
}

func (d *Dictionary) better(a, b uint32) bool {
	if a == 0 {
		return false
	}
	if b == 0 {
		return true
	}
	x, y := d.words[a-1], d.words[b-1]
	return x.Weight > y.Weight || x.Weight == y.Weight && x.Text < y.Text
}

func (d *Dictionary) child(id uint32, letter byte) uint32 {
	for c := d.nodes[id-1].child; c != 0; c = d.nodes[c-1].next {
		if d.nodes[c-1].letter == letter {
			return c
		}
	}
	return 0
}

func (d *Dictionary) buildIndex() {
	d.nodes = []trieNode{{}}
	for i, w := range d.words {
		id := uint32(1)
		for at := range w.Text {
			c := d.child(id, w.Text[at])
			if c == 0 {
				d.nodes = append(d.nodes, trieNode{letter: w.Text[at], next: d.nodes[id-1].child})
				c = uint32(len(d.nodes))
				d.nodes[id-1].child = c
			}
			id = c
		}
		d.nodes[id-1].word = uint32(i + 1)
		if !d.noSuggest[i] {
			d.nodes[id-1].best = uint32(i + 1)
		}
	}
	// Children are allocated after their parents, so a reverse pass propagates
	// the best prior without recursion or temporary per-prefix maps.
	for i := len(d.nodes) - 1; i >= 0; i-- {
		for c := d.nodes[i].child; c != 0; c = d.nodes[c-1].next {
			if best := d.nodes[c-1].best; d.better(best, d.nodes[i].best) {
				d.nodes[i].best = best
			}
		}
	}
}

type prefixItem struct{ node, best uint32 }
type prefixHeap struct {
	d     *Dictionary
	items []prefixItem
}

func (h prefixHeap) Len() int { return len(h.items) }
func (h prefixHeap) less(a, b prefixItem) bool {
	if a.best == b.best {
		return a.node == 0 && b.node != 0
	}
	return h.d.better(a.best, b.best)
}
func (h *prefixHeap) push(x prefixItem) {
	i := len(h.items)
	h.items = append(h.items, x)
	for i > 0 {
		parent := (i - 1) / 2
		if !h.less(x, h.items[parent]) {
			break
		}
		h.items[i] = h.items[parent]
		i = parent
	}
	h.items[i] = x
}
func (h *prefixHeap) pop() prefixItem {
	root := h.items[0]
	last := h.items[len(h.items)-1]
	h.items = h.items[:len(h.items)-1]
	if len(h.items) > 0 {
		i := 0
		for {
			child := 2*i + 1
			if child >= len(h.items) {
				break
			}
			if child+1 < len(h.items) && h.less(h.items[child+1], h.items[child]) {
				child++
			}
			if !h.less(h.items[child], last) {
				break
			}
			h.items[i] = h.items[child]
			i = child
		}
		h.items[i] = last
	}
	return root
}

func rank(cs []Candidate) {
	slices.SortFunc(cs, func(a, b Candidate) int {
		if a.Distance != b.Distance {
			return cmp.Compare(a.Distance, b.Distance)
		}
		if a.Weight != b.Weight {
			return cmp.Compare(b.Weight, a.Weight)
		}
		return strings.Compare(a.Text, b.Text)
	})
}

// Optimal string alignment distance includes adjacent transpositions.
func distance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	var before []int
	for i := 1; i <= len(a); i++ {
		row := make([]int, len(b)+1)
		row[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			row[j] = min(row[j-1]+1, prev[j]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				row[j] = min(row[j], before[j-2]+1)
			}
		}
		before, prev = prev, row
	}
	return prev[len(b)]
}
