package main

import (
	"git.biggo.com/0c/ime/core/zhuyin"
	"git.biggo.com/0c/ime/internal/compile"
	"testing"
)

func TestFindingsMergeSourcesAndFilter(t *testing.T) {
	d := &compile.Data{Counts: map[string]map[string]float64{"freq": {"這": 20, "個": 10, "見": 5}}}
	d.Chars = []compile.CharReading{
		{Char: "這", Reading: "ㄓㄜˋ", Grade: compile.Attested, Origins: []string{"a"}},
		{Char: "這", Reading: "ㄓㄟˋ", Grade: compile.Attested, Origins: []string{"a"}},
		{Char: "這", Reading: "ㄓㄜˋ", Grade: compile.Primary, Origins: []string{"excluded"}},
		{Char: "個", Reading: "ㄍㄜˋ", Grade: compile.Primary, Origins: []string{"a"}},
		{Char: "個", Reading: "ㄍㄜ˙", Grade: compile.Primary, Origins: []string{"b"}},
		{Char: "見", Reading: "ㄐㄧㄢˋ", Grade: compile.Primary, Origins: []string{"a"}},
		{Char: "見", Reading: "ㄐㄧㄢˋ", Grade: compile.Primary, Origins: []string{"b"}},
		{Char: "見", Reading: "ㄒㄧㄢˋ", Grade: compile.Attested, Origins: []string{"a"}},
	}
	r := findings(d, "excluded", true, "freq", 0)
	if len(r) != 2 || r[0].word != "這" || r[0].kind != "missing-primary" || r[1].kind != "multiple-primary" {
		t.Fatalf("相同主讀音不可誤報衝突，排除來源不可補足主讀音：%+v", r)
	}
	if r := findings(d, "excluded", true, "freq", 15); len(r) != 1 || r[0].word != "這" {
		t.Fatal(r)
	}
}

func TestAuditIncludesSingleUnknownReadingAndWords(t *testing.T) {
	d := &compile.Data{Counts: map[string]map[string]float64{"f": {"甲": 12, "甲乙": 8}}, Chars: []compile.CharReading{{Char: "甲", Reading: "ㄐㄧㄚˇ", Grade: compile.Attested, Origins: []string{"a"}}}, Words: []compile.WordReading{{Word: "甲乙", Reading: zhuyin.ParseReading("ㄐㄧㄚˇ ㄧˇ"), Grade: compile.Attested, Origins: []string{"a"}}}}
	if r := findings(d, "", true, "f", 0); len(r) != 1 || r[0].word != "甲" {
		t.Fatal(r)
	}
	if r := wordFindings(d, "", true, "f", 0); len(r) != 1 || r[0].word != "甲乙" {
		t.Fatal(r)
	}
	if r := wordFindings(d, "a", true, "f", 0); len(r) != 0 {
		t.Fatal(r)
	}
}
