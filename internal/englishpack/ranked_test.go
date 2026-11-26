package englishpack

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/english"
)

func compileRanked(t *testing.T) (*english.Dictionary, *english.Bigrams, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "pack")
	if err := CompileRanked("../../data/english", dir); err != nil {
		t.Fatal(err)
	}
	d, m, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Format != 2 || !d.Ranked() {
		t.Fatalf("format %d", m.Format)
	}
	b, err := LoadBigrams(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	return d, b, dir
}

func candidateTexts(cs []english.Candidate) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Text)
	}
	return out
}

// APP#21 的驗收案例，用實際的詞頻資料包。
func TestRankedPackOnTheAcceptanceCases(t *testing.T) {
	d, b, _ := compileRanked(t)
	for typed, want := range map[string]string{"teh": "the", "helo": "hello", "wich": "which", "dont": "don't", "im": "I'm", "didnt": "didn't", "recieve": "receive"} {
		if got := d.Corrections(typed, 3); len(got) == 0 || got[0].Text != want {
			t.Errorf("%s: %v", typed, candidateTexts(got))
		}
	}
	for _, typed := range []string{"ill", "were", "wed", "its", "XD", "GG", "po", "iPhone", "ok"} {
		if !d.Has(typed) || d.Corrections(typed, 3) != nil {
			t.Errorf("%s 是字，不能修正", typed)
		}
	}
	if got := strings.ToLower(strings.Join(candidateTexts(d.Prefix("app", 3)), " ")); !slices.Contains(strings.Fields(got), "app") || !slices.Contains(strings.Fields(got), "apple") {
		t.Errorf("app: %v", got)
	}
	for word, want := range map[string]string{"i": "I", "iphone": "iPhone", "ai": "AI", "youtube": "YouTube", "i'm": "I'm", "hello": "hello"} {
		if got := d.Display(word); got != want {
			t.Errorf("%s: %q", word, got)
		}
	}
	for _, word := range []string{"xd", "gg", "qq", "ptt", "iphone"} {
		if !d.Protected(word) {
			t.Errorf("%s 不在台灣常用英文", word)
		}
	}
	if got := b.After(english.ChineseEnglish, "笑死", 1); len(got) == 0 || got[0].Text != "xd" {
		t.Errorf("笑死 → %v", got)
	}
	if got := b.After(english.EnglishEnglish, "of", 1); len(got) == 0 || got[0].Text != "the" {
		t.Errorf("of → %v", got)
	}
}

// 桌面打包（Compile，格式 1）不讀 frequency/ 與 display.tsv：有沒有它們，產物逐位元組相同。
func TestDesktopPackIgnoresFrequency(t *testing.T) {
	src := t.TempDir()
	for _, name := range []string{"source.json", "esdb.tsv", "technical.tsv", "LICENSE.txt"} {
		b, err := os.ReadFile(filepath.Join("../../data/english", name))
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(src, name), b, 0600)
	}
	without, with := filepath.Join(t.TempDir(), "pack"), filepath.Join(t.TempDir(), "pack")
	if err := Compile(src, without); err != nil {
		t.Fatal(err)
	}
	if err := Compile("../../data/english", with); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"words.bin", "manifest.json", "NOTICE.txt"} {
		a, _ := os.ReadFile(filepath.Join(without, name))
		b, _ := os.ReadFile(filepath.Join(with, name))
		if !bytes.Equal(a, b) {
			t.Fatalf("%s 因 frequency/ 改變", name)
		}
	}
	if _, err := LoadBigrams(with, nil); err == nil {
		t.Fatal("format 1 has no pairs")
	}
}

func TestRankedPackDeterministicAndRejectsTampering(t *testing.T) {
	_, _, a := compileRanked(t)
	_, _, b := compileRanked(t)
	for _, name := range []string{"words.bin", "bigram.bin", "manifest.json", "NOTICE.txt"} {
		x, _ := os.ReadFile(filepath.Join(a, name))
		y, _ := os.ReadFile(filepath.Join(b, name))
		if !bytes.Equal(x, y) {
			t.Fatalf("nondeterministic %s", name)
		}
	}
	notice, _ := os.ReadFile(filepath.Join(a, "NOTICE.txt"))
	if strings.Contains(string(notice), "並非語料詞頻") || !strings.Contains(string(notice), "english-corpus-20261123-r5") {
		t.Fatal("notice")
	}
	for _, name := range []string{"manifest.json", "english-unigram.tsv", "mixed-bigram.tsv"} {
		src := t.TempDir()
		os.CopyFS(src, os.DirFS("../../data/english"))
		path := filepath.Join(src, "frequency", name)
		data, _ := os.ReadFile(path)
		os.WriteFile(path, append(data, '\n'), 0600)
		out := filepath.Join(t.TempDir(), "pack")
		if err := CompileRanked(src, out); err == nil {
			t.Fatalf("accepted tampered %s", name)
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Fatal("rejected source published output")
		}
	}
	os.WriteFile(filepath.Join(a, "bigram.bin"), []byte("tampered"), 0600)
	if _, err := LoadBigrams(a, nil); err == nil {
		t.Fatal("accepted tampered pairs")
	}
}

// technical.tsv 的第三欄是常見寫法：語料沒有的月份、星期與產品名照它顯示（APP#21），桌面包不受影響。
func TestRankedPackUsesTechnicalDisplayForms(t *testing.T) {
	d, _, _ := compileRanked(t)
	for typed, want := range map[string]string{"monday": "Monday", "january": "January", "pdf": "PDF", "airpods": "AirPods", "sync": "sync"} {
		if !d.Has(typed) {
			t.Fatalf("%s 不在詞頻資料包", typed)
		}
		if got := d.Display(typed); got != want {
			t.Fatalf("%s 顯示成 %q，要 %q", typed, got, want)
		}
	}
	if got := d.Display("may"); got != "may" {
		t.Fatalf("may 顯示成 %q：語料裡多半小寫的詞不該被詞表改寫", got)
	}
}

// #358：寫法只留可靠的。語料裡第一個字母以外有大寫的寫法（iPhone、OpenAI、AI）留著；ESDB 的一般英文詞
// 不用語料的寫法（標題與句首的大寫：us→US、line→LINE、total→Total）；只有第一個字母大寫的分不出
// 句首還是專有名詞，也不用。I 與 I 開頭的縮寫照樣大寫；technical.tsv 手寫的寫法照用。
func TestRankedPackKeepsOnlyReliableDisplayForms(t *testing.T) {
	d, _, _ := compileRanked(t)
	for word, want := range map[string]string{
		"iphone": "iPhone", "openai": "OpenAI", "ai": "AI", "gdp": "GDP", "youtube": "YouTube", "xd": "XD",
		"i": "I", "i'm": "I'm", "i'll": "I'll", "pdf": "PDF", "monday": "Monday", "asap": "ASAP",
		"us": "us", "line": "line", "total": "total", "white": "white", "images": "images", "south": "south",
		"furthermore": "furthermore", "don": "don", "beach": "beach", "meanwhile": "meanwhile", "where's": "where's",
	} {
		if got := d.Display(word); got != want {
			t.Errorf("%s 顯示成 %q，要 %q", word, got, want)
		}
	}
	for typed, want := range map[string]string{"iph": "iPhone", "DeepS": "DeepSeek", "ima": "images"} {
		if got := d.Prefix(typed, 1); len(got) == 0 || got[0].Text != want {
			t.Errorf("Prefix(%s) = %v，要 %s", typed, candidateTexts(got), want)
		}
	}
	for _, typed := range []string{"bea", "tot", "sou", "whi", "furt"} {
		if got := d.Prefix(typed, 3); len(got) == 0 || slices.ContainsFunc(got, func(c english.Candidate) bool { return c.Text != strings.ToLower(c.Text) }) {
			t.Errorf("Prefix(%s) = %v：一般英文詞不該有大寫寫法", typed, candidateTexts(got))
		}
	}
}

// #358：語料新加、三個字母以內的詞（da、ji、tv、ae、ai）在混打不認；ESDB、technical.tsv 的短詞與四個字母
// 以上的語料新詞照認。
func TestRankedPackMixedViewHidesShortCorpusWords(t *testing.T) {
	d, _, _ := compileRanked(t)
	m := d.Mixed()
	for _, w := range []string{"da", "ji", "ka", "tv", "sr", "ae", "ai", "gdp", "etf"} {
		if !d.Has(w) || m.Has(w) {
			t.Errorf("%s：完整字典要認、混打不認", w)
		}
	}
	for _, w := range []string{"the", "api", "iphone", "tesla", "don't", "i'm", "sync", "pdf"} {
		if !m.Has(w) {
			t.Errorf("%s：混打要認", w)
		}
	}
	desk := filepath.Join(t.TempDir(), "pack")
	if err := Compile("../../data/english", desk); err != nil {
		t.Fatal(err)
	}
	f1, _, err := Load(desk)
	if err != nil {
		t.Fatal(err)
	}
	// 格式 1 的詞（ESDB＋technical.tsv）在格式 2 都不是語料新詞，混打照認；混打認的短詞就是格式 1 的短詞。
	for _, w := range []string{"a", "an", "at", "id", "pm", "ram", "may", "bar"} {
		if !f1.Has(w) || !m.Has(w) {
			t.Errorf("%s：格式 1 有，混打要認", w)
		}
	}
}

func TestTechnicalDisplayMustBeACaseVariant(t *testing.T) {
	src := t.TempDir()
	for _, name := range []string{"source.json", "esdb.tsv", "LICENSE.txt"} {
		b, err := os.ReadFile(filepath.Join("../../data/english", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(src, "technical.tsv"), []byte("monday\t210\tMonday\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Compile(src, filepath.Join(t.TempDir(), "pack")); err != nil {
		t.Fatalf("合法的寫法被拒絕：%v", err)
	}
	for _, line := range []string{"monday\t210\tMonday!", "monday\t210\tmonday", "monday\t210\tTuesday"} {
		if err := os.WriteFile(filepath.Join(src, "technical.tsv"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := Compile(src, filepath.Join(t.TempDir(), "pack")); err == nil {
			t.Fatalf("%q 應該被拒絕", line)
		}
	}
}
