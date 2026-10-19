package main

import (
	"git.biggo.com/0c/ime/internal/corpus"
	"testing"
)

func TestDocumentFrequencyAndBoundaries(t *testing.T) {
	v := corpus.NewVocab(map[string]float64{"甲": .5, "乙": .5})
	c := corpus.NewCounts()
	df := map[uint32]uint64{}
	bdf := map[uint64]uint64{}
	addDocument(v, c, df, bdf, "甲甲，乙")
	var a, b uint32
	for i, w := range v.Words {
		if w == "甲" {
			a = uint32(i)
		} else {
			b = uint32(i)
		}
	}
	if c.Uni[a] != 2 || df[a] != 1 || df[b] != 1 {
		t.Fatal(c.Uni, df)
	}
	if c.Bi[uint64(a)<<32|uint64(b)] != 0 {
		t.Fatal("跨標點計入 bigram")
	}
	addDocument(v, c, df, bdf, "甲甲甲")
	if c.Uni[a] != 5 || df[a] != 2 || bdf[uint64(a)<<32|uint64(a)] != 2 {
		t.Fatal(c.Uni, df, bdf)
	}
}

func TestEvaluationOverlapSubstring(t *testing.T) {
	b := &trie{}
	b.add("這是一個測試")
	if !overlaps("前綴這是一個測試後綴", b) {
		t.Fatal("未排除嵌入評測片段")
	}
	if overlaps("這是一個，測試", b) {
		t.Fatal("跨標點拼出不存在的重疊")
	}
}

func TestAnyDuplicateMemberExcludesGroup(t *testing.T) {
	b := &trie{}
	b.add("這是一個測試")
	d := document{Body: "代表文件未出現該片段", Bodies: []string{"另外一份這是一個測試內容"}}
	if !documentOverlaps(d, b) {
		t.Fatal("未檢查非代表文件")
	}
}
