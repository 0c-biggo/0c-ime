package importer

import (
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestFreeCounts(t *testing.T) {
	lex := map[string]bool{"台灣大學": true, "台灣": true, "台": true, "大學": true, "灣": true}
	occ := map[string]float64{"台灣大學": 10, "台灣": 30, "台": 50, "大學": 15, "灣": 30}
	f := FreeCounts(occ, lex)
	// 由長到短：台灣 = 30 − 10（在台灣大學裡）；大學 = 15 − 10；
	// 台與灣被包含於台灣大學（10）與台灣（自由次數 20），各扣 30。
	want := map[string]float64{"台灣大學": 10, "台灣": 20, "大學": 5, "台": 20, "灣": 0}
	for w, n := range want {
		if f[w] != n {
			t.Errorf("%s：%g 應為 %g", w, f[w], n)
		}
	}
	if f := FreeCounts(map[string]float64{"台灣": 5, "台": 1}, map[string]bool{"台灣": true, "台": true}); f["台"] != 0 {
		t.Errorf("負值應以 0 為下限：%g", f["台"])
	}
}

func TestReadMcBopomofo(t *testing.T) {
	base := "行 ㄒㄧㄥˊ xing2 vu/6 big5\n行 ㄏㄤˊ hang2 c;6 big5\n行 ㄏㄤˋ hang4 c;4 big5\n丼 ㄐㄧㄥˇ jing3 ru/3 big5\nㄝ ㄝ e , big5\n妈 ㄇㄚ ma a8 utf8\n\uF901 ㄍㄥ geng eng utf8\n"
	maps := "銀行 ㄧㄣˊ ㄏㄤˊ\n一次 ㄧ ㄘˋ\n一次 ㄧˊ ㄘˋ\n"
	occ := "銀行 12\n行 30\nˇ 48\n"
	h1, h2 := "行 ㄒㄧㄥˊ\n", "行 ㄏㄤˊ\n"
	m, err := ReadMcBopomofo(strings.NewReader(base), strings.NewReader(maps), strings.NewReader(occ),
		[3]io.Reader{strings.NewReader(h1), strings.NewReader(h2), nil})
	if err != nil {
		t.Fatal(err)
	}
	grades := map[string]string{}
	for _, c := range m.Chars {
		grades[c.Char+c.Reading] = c.Grade
	}
	for k, want := range map[string]string{"行ㄒㄧㄥˊ": "primary", "行ㄏㄤˊ": "common", "行ㄏㄤˋ": "attested", "丼ㄐㄧㄥˇ": "primary"} {
		if grades[k] != want {
			t.Errorf("%s：%q 應為 %q", k, grades[k], want)
		}
	}
	if _, ok := grades["ㄝㄝ"]; ok {
		t.Error("非漢字應略過")
	}
	if _, ok := grades["\uF901ㄍㄥ"]; ok {
		t.Error("相容表意字應略過")
	}
	for _, c := range m.Chars {
		if (c.Char == "妈") != (c.Origin == RareOrigin) {
			t.Errorf("不在 Big5 的字應標為罕用：%+v", c)
		}
	}
	if len(m.Words) != 3 || m.Occ["銀行"] != 12 || len(m.Occ) != 2 { // 7 行字表：行×3、丼、妈 有效
		t.Errorf("詞 %d、次數 %v", len(m.Words), m.Occ)
	}
}

func TestReadCNS(t *testing.T) {
	phon := "1-4421\tㄧ\n1-4421\tㄧˊ\n1-4422\t˙ㄇㄜ\n1-9999\tㄅㄚ\n1-5000\tㄇㄡˇ\n1-5001\tㄇㄡˇ\n"
	maps := "1-4421\t4E00\n1-4422\t9EBC\n1-5000\t67D0\n1-5001\t2A6D6\n" // 某、一個生僻字
	vouched := func(ch, r string) bool { return ch == "某" }
	cs, ph, err := ReadCNS(strings.NewReader(phon), []io.Reader{strings.NewReader(maps)}, vouched)
	if err != nil {
		t.Fatal(err)
	}
	if ph != 1 {
		t.Errorf("只有佔位讀音且無背書的字應略過 1 個，實際 %d", ph)
	}
	got := []string{}
	for _, c := range cs {
		got = append(got, c.Char+c.Reading+":"+c.Grade)
	}
	if strings.Join(got, " ") != "一ㄧ:attested 一ㄧˊ:attested 麼ㄇㄜ˙:primary 某ㄇㄡˇ:primary" {
		t.Errorf("%v", got)
	}
}

func TestReadCEDICT(t *testing.T) {
	in := strings.Join([]string{
		"# comment",
		"垃圾 垃圾 [la1 ji1] /trash/refuse/garbage/(Taiwan pr. [le4 se4])/",
		"企業 企业 [qi3 ye4] /company/firm/enterprise/",
		"親子丼 亲子丼 [qin1 zi3 dong4] /oyakodon/",
		"甚麼 甚么 [shen2 me5] /variant of 什麼|什么[shen2 me5]/",
		"卡拉OK 卡拉OK [ka3 la1 O K] /karaoke/",
		"丼 丼 [dong4] /bowl/",
		"一會兒 一会儿 [yi1 hui4 r5] /a moment/",
	}, "\n")
	ws, _, err := ReadCEDICT(strings.NewReader(in), map[string]bool{"企業": true})
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, w := range ws {
		got = append(got, w.Word+" "+w.Reading+" "+w.Grade)
	}
	want := []string{
		"垃圾 ㄌㄚ ㄐㄧ common", "垃圾 ㄌㄜˋ ㄙㄜˋ primary", // 台灣讀音為主，對岸讀音為通行
		"企業 ㄑㄧˇ ㄧㄝˋ common", // 已知詞：CEDICT 讀音只作通行音
		"親子丼 ㄑㄧㄣ ㄗˇ ㄉㄨㄥˋ primary",
		"一會兒 ㄧ ㄏㄨㄟˋ ㄦ˙ primary",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("\n實際 %v\n應為 %v", got, want)
	}
}

func TestReadARPA(t *testing.T) {
	in := "\\data\\\nngram 1=2\nngram 2=1\n\n\\1-grams:\n-1.5 台灣\n-2 大學 -0.3\n\n\\2-grams:\n-0.5 台灣 大學\n\\end\\\n"
	var got []string
	err := ReadARPA(strings.NewReader(in), func(o int, p float64, ws []string) {
		got = append(got, fmt.Sprint(o, p, ws))
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "|") != "1 -1.5 [台灣]|1 -2 [大學]|2 -0.5 [台灣 大學]" {
		t.Errorf("%v", got)
	}
}
