package compile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCurated(t *testing.T, lines ...string) string {
	p := filepath.Join(t.TempDir(), "curated-bigram.tsv")
	if err := os.WriteFile(p, []byte("# 前詞\t詞\t機率\t原因\n"+strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// 人工 bigram 與混合值取最大值，而且不剪枝（佔用保留上限的名額）。
func TestCuratedBigramMaxAndKept(t *testing.T) {
	o := DefaultOptions()
	o.Bigram = "toycorpus=3,imp=1" // 企業→風景 混合後 0.3、<s>→企業 0.375
	o.BigramMax = 2
	o.CuratedBigram = writeCurated(t,
		"企業\t風景\t0.1\t比混合值低：取混合值",
		"說服\t東西\t0.4\t混合裡沒有、剪枝分數最低：仍要保留")
	res := Build(load(t, fixture), o)
	if len(res.Errors) > 0 {
		t.Fatal(res.Errors)
	}
	close := func(got, want float64) bool { return got > want*0.963 && got < want*1.037 }
	if got := res.LM.Bi("說服", "東西"); !close(got, 0.4) {
		t.Errorf("P(東西|說服) = %g，要 0.4", got)
	}
	if res.Report.Bigrams != 2 || res.Report.CuratedBigrams != 2 {
		t.Errorf("bigram %d 筆、人工 %d 筆", res.Report.Bigrams, res.Report.CuratedBigrams)
	}
	if got := res.LM.Bi("企業", "風景"); !close(got, 0.3) {
		t.Errorf("P(風景|企業) = %g，要取最大值 0.3", got)
	}
	if !strings.Contains(res.Report.String(), "人工 bigram 2 筆") {
		t.Errorf("報告沒寫人工 bigram：%s", res.Report.String())
	}
}

func TestCuratedBigramErrors(t *testing.T) {
	for name, line := range map[string]string{
		"詞不在詞庫":  "企業\t不在詞庫\t0.1\tx",
		"前詞不在詞庫": "不在詞庫\t風景\t0.1\tx",
		"沒寫原因":   "企業\t風景\t0.1",
		"機率不合法":  "企業\t風景\t2\tx",
	} {
		o := DefaultOptions()
		o.Bigram = "toycorpus=1"
		o.CuratedBigram = writeCurated(t, line)
		if res := Build(load(t, fixture), o); len(res.Errors) == 0 {
			t.Errorf("%s：應該是編譯錯誤", name)
		}
	}
	o := DefaultOptions()
	o.Bigram = "toycorpus=1"
	o.CuratedBigram = writeCurated(t, "企業\t風景\t0.1\ta", "企業\t風景\t0.2\tb")
	if res := Build(load(t, fixture), o); len(res.Errors) == 0 {
		t.Error("重複的配對應該是編譯錯誤")
	}
	o = DefaultOptions()
	o.CuratedBigram = writeCurated(t, "企業\t風景\t0.1\tx")
	if res := Build(load(t, fixture), o); len(res.Errors) == 0 {
		t.Error("沒有 bigram 來源時不能用人工 bigram")
	}
}

// 不用人工 bigram 時 manifest 的 options 不出現這個欄位，舊配方編出的 manifest 不變。
func TestCuratedBigramAbsentFromManifestWhenOff(t *testing.T) {
	b, err := json.Marshal(DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "CuratedBigram") {
		t.Errorf("%s", b)
	}
}
