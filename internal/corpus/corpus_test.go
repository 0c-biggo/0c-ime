package corpus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConvert(t *testing.T) {
	dir := t.TempDir()
	write := func(name, s string) { os.WriteFile(filepath.Join(dir, name), []byte(s), 0o644) }
	write("STPhrases.txt", "# c\n软件\t軟件\n视频\t視頻\n")
	write("STCharacters.txt", "这\t這\n个\t個 箇\n软\t軟\n")
	write("TWPhrases.txt", "軟件\t軟體\n視頻\t影片\n")
	write("TWVariants.txt", "裏\t裡\n")
	c, err := S2TWP(dir)
	if err != nil {
		t.Fatal(err)
	}
	for in, want := range map[string]string{"这个软件": "這個軟體", "看视频": "看影片", "家裏": "家裡", "這個軟體": "這個軟體"} {
		if got := c.Convert(in); got != want {
			t.Errorf("%q → %q，應為 %q", in, got, want)
		}
	}
}

func TestPagesAndClean(t *testing.T) {
	xml := strings.Join([]string{
		"<page>", "<title>甲</title>", "<ns>0</ns>",
		`<text bytes="1" xml:space="preserve">'''甲'''是{{模板|參數=不要}}一種[[水果|好吃的水果]]&lt;ref&gt;引用內容&lt;/ref&gt;。`,
		"[[File:圖.jpg|說明]]第二行[[台灣]]的產物&lt;!-- 註解 --&gt;</text>", "</page>",
		"<page>", "<ns>0</ns>", `<redirect title="乙" />`, `<text>#重新導向 [[乙]]</text>`, "</page>",
		"<page>", "<ns>4</ns>", `<text>維基百科命名空間</text>`, "</page>",
	}, "\n")
	var got []string
	if err := Pages(strings.NewReader(xml), func(s string) { got = append(got, Clean(s)) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("應只有一個主命名空間、非重新導向的條目：%q", got)
	}
	var chunks []string
	HanChunks(got[0], 2, func(c string) { chunks = append(chunks, c) })
	// 單字的「甲」「是」不足兩字；連結文字併入正文；模板、引用、檔案連結與註解都已移除
	if s := strings.Join(chunks, "|"); s != "一種好吃的水果|第二行台灣的產物" {
		t.Errorf("%q", s)
	}
}

func TestSegmentAndCount(t *testing.T) {
	v := NewVocab(map[string]float64{"台灣": 0.1, "台": 0.01, "灣": 0.01, "大": 0.01, "學": 0.01, "大學": 0.1, "台灣大學": 0.05})
	var segs [][]string
	v.Segment("台灣大學台灣X台", func(ids []uint32) {
		var ws []string
		for _, id := range ids {
			ws = append(ws, v.Name(id))
		}
		segs = append(segs, ws)
	})
	if len(segs) != 2 || strings.Join(segs[0], " ") != "台灣大學 台灣" || strings.Join(segs[1], " ") != "台" {
		t.Fatalf("%v", segs)
	}
	c := NewCounts()
	c.Add(v, []uint32{v.id["台灣"], v.id["大學"]})
	if c.Bi[uint64(v.BOS)<<32|uint64(v.id["台灣"])] != 1 || c.Bi[uint64(v.id["大學"])<<32|uint64(v.EOS)] != 1 || c.Left[v.id["台灣"]] != 1 {
		t.Errorf("%+v", c)
	}
}
