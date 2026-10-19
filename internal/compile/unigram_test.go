package compile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnigramRoundTripAndSetColumn(t *testing.T) {
	p := filepath.Join(t.TempDir(), "unigram.tsv")
	in := "# 詞\ta\tb\n# 說明\n乙\t\t2.5e-05\n甲\t3\t\n"
	if err := os.WriteFile(p, []byte(in), 0o644); err != nil {
		t.Fatal(err)
	}
	u, err := ReadUnigram(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(u.Sources, ",") != "a,b" || u.Cells["乙"][1] != "2.5e-05" || u.Cells["甲"][0] != "3" {
		t.Fatalf("讀取：%+v", u)
	}
	// 整欄取代：b 只剩丙；乙因此所有欄都空，移除。新欄 c 加在最後。
	u.SetColumn("b", map[string]string{"丙": "7"})
	u.SetColumn("c", map[string]string{"甲": "1"})
	if err := u.Write(p); err != nil {
		t.Fatal(err)
	}
	v, err := ReadUnigram(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(v.Sources, ",") != "a,b,c" || strings.Join(v.Words, ",") != "丙,甲" ||
		strings.Join(v.Cells["甲"], "|") != "3||1" || strings.Join(v.Cells["丙"], "|") != "|7|" {
		t.Fatalf("寫回後：%+v", v)
	}
}

func TestUnigramRejectsBadFiles(t *testing.T) {
	for _, s := range []string{
		"# origin: a\n甲\t3\n",  // 舊格式
		"# 詞\ta\ta\n",          // 來源重複
		"# 詞\ta\tb\n甲\t3\n",    // 欄數不足
		"# 詞\ta\n甲\t1\n甲\t2\n", // 詞重複
	} {
		p := filepath.Join(t.TempDir(), "unigram.tsv")
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadUnigram(p); err == nil {
			t.Errorf("%q 應被拒絕", s)
		}
	}
	// 不合法的次數由 Load 回報
	dir := copyFixture(t)
	if err := os.WriteFile(filepath.Join(dir, "lm", "unigram.tsv"), []byte("# 詞\ttoycorpus\n企業\t-1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, errs, _ := Load(dir); !hasError(errs, "不合法") {
		t.Errorf("負的次數應為錯誤：%v", errs)
	}
}
