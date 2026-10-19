package compile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/zhuyin"
)

const fixture = "../../testdata/compile"

func load(t *testing.T, dir string) *Data {
	t.Helper()
	d, errs, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) > 0 {
		t.Fatalf("讀取錯誤：%v", errs)
	}
	return d
}

func prw(lex *lexicon.Compact, reading, word string) float64 {
	for _, e := range lex.Exact(zhuyin.ParseReading(reading)) {
		if e.Surface == word {
			return e.PRW
		}
	}
	return 0
}

func near(a, b float64) bool { return a > b-1e-6 && a < b+1e-6 }

func TestDuplicatePrimaryReadingStillExpands(t *testing.T) {
	d := load(t, fixture)
	d.Chars = append(d.Chars, CharReading{Char: "說", Reading: "ㄕㄨㄛ", Grade: Primary, Origins: []string{"curated"}})
	r := Build(d, DefaultOptions())
	if len(r.Errors) != 0 {
		t.Fatal(r.Errors)
	}
	if got := prw(r.Lexicon, "ㄕㄨㄛ ㄈㄨˊ", "說服"); !near(got, 0.2) {
		t.Fatalf("相同主讀音重複收錄不應停用通行打法：%g", got)
	}
	// 真正不同的兩個主讀音仍視為歧義，不依 map 順序猜一個。
	d.Chars = append(d.Chars, CharReading{Char: "說", Reading: "ㄕㄨㄟˋ", Grade: Primary, Origins: []string{"curated"}})
	d.Reach = nil // 此反例刻意禁止展開，不套用 fixture 的通行音可達性要求
	r = Build(d, DefaultOptions())
	if len(r.Errors) != 0 {
		t.Fatal(r.Errors)
	}
	if got := prw(r.Lexicon, "ㄕㄨㄛ ㄈㄨˊ", "說服"); got != 0 {
		t.Fatalf("不同主讀音仍須保留歧義：%g", got)
	}
}

func TestBuildRulesAndChecks(t *testing.T) {
	d := load(t, fixture)
	// 從新 ㄘㄨㄥˊ ㄒㄧㄣ 是詞，所以不能把它列為自動修正——先確認正常的修正通過
	res := Build(d, DefaultOptions())
	if len(res.Errors) > 0 {
		t.Fatalf("不應有錯誤：%v", res.Errors)
	}
	lex := res.Lexicon
	for _, c := range []struct {
		reading, word string
		want          float64
		why           string
	}{
		{"ㄑㄧㄣ ㄗˇ ㄉㄨㄥˋ", "親子丼", 1, "主讀音"},
		{"ㄑㄧㄣ ㄗˇ ㄐㄧㄥˇ", "親子丼", 0.1, "L2：繼承丼的可考讀音"},
		{"ㄑㄧˇ ㄧㄝˋ", "企業", 0.5, "L2：繼承企的通行讀音"},
		{"ㄕㄨㄛ ㄈㄨˊ", "說服", 0.2, "L3：破音字以主讀音輸入"},
		{"ㄉㄨㄥ ㄒㄧ", "東西", 0.5, "L3：輕聲以本調輸入"},
		{"ㄧˊ ㄘˋ", "一次", 1, "L3：一的變調與本調同為主讀音"},
		{"ㄅㄨˊ ㄏㄨㄟˋ", "不會", 1, "L3：不的變調"},
		{"ㄧㄣ ㄍㄞ", "應該", 0.3, "自動修正"},
		{"ㄐㄧㄥˇ", "丼", 0.1, "單字的可考讀音"},
	} {
		if got := prw(lex, c.reading, c.word); !near(got, c.want) {
			t.Errorf("%s（%s）：%s → %s 的 P(r|w) = %g，應為 %g", c.why, c.reading, c.reading, c.word, got, c.want)
		}
	}
	for _, e := range lex.Exact(zhuyin.ParseReading("ㄧㄣ ㄍㄞ")) {
		if e.Surface == "應該" && !e.Correction {
			t.Error("修正讀音應標記為 correction")
		}
	}
	for _, e := range lex.Exact(zhuyin.ParseReading("ㄧㄥ ㄍㄞ")) {
		if e.Correction {
			t.Error("正常讀音不得標記為 correction")
		}
	}
	// 詞頻：有次數的詞較高；詞庫外的詞不影響（只計詞庫內）
	if !(res.LM.Uni("企業") > res.LM.Uni("風景") && res.LM.Uni("風景") > res.LM.Uni("說服")) {
		t.Errorf("詞頻順序：企業 %g 風景 %g 說服 %g", res.LM.Uni("企業"), res.LM.Uni("風景"), res.LM.Uni("說服"))
	}
	if res.Report.CountedWords != 2 {
		t.Errorf("有詞頻的詞：%d", res.Report.CountedWords)
	}
}

func TestRulesCanBeDisabled(t *testing.T) {
	o := DefaultOptions()
	o.InheritVariants, o.YiBuSandhi, o.NeutralTone, o.PolyphoneCommon = false, false, false, false
	d := load(t, fixture)
	d.Reach = nil // 可達性清單依賴規則
	res := Build(d, o)
	if len(res.Errors) > 0 {
		t.Fatal(res.Errors)
	}
	for _, r := range []string{"ㄑㄧㄣ ㄗˇ ㄐㄧㄥˇ", "ㄑㄧˇ ㄧㄝˋ", "ㄕㄨㄛ ㄈㄨˊ", "ㄉㄨㄥ ㄒㄧ", "ㄧˊ ㄘˋ"} {
		if es := res.Lexicon.Exact(zhuyin.ParseReading(r)); len(es) > 0 {
			t.Errorf("規則關閉後 %s 不應有詞條：%v", r, es)
		}
	}
}

// 以暫存目錄複製 fixture 再改壞一處，確認每一項檢查都會擋下出版。
func TestChecksBlockPublishing(t *testing.T) {
	cases := []struct {
		name, file, appendLine, want string
	}{
		{"修正讀音撞到既有的詞", "lexicon/corrections.tsv", "重新\tㄘㄨㄥˊ ㄒㄧㄣ\tcurated\t", "照字面即成詞"},
		{"可達性回歸失敗", "lexicon/reachability.tsv", "ㄆㄧㄥˊ ㄍㄨㄛˇ\t蘋果\t不存在的詞", "候選沒有"},
		{"不在帳本的來源", "lexicon/words.tsv", "樣本\tㄧㄤˋ ㄅㄣˇ\tprimary\ty\tnowhere", "不在帳本"},
		{"修正的目標詞不存在", "lexicon/corrections.tsv", "沒有\tㄇㄟˊ ㄧㄡˇ\tcurated\t", "不在詞庫"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := copyFixture(t)
			appendTo(t, filepath.Join(dir, c.file), c.appendLine)
			res := Build(load(t, dir), DefaultOptions())
			if !hasError(res.Errors, c.want) {
				t.Errorf("應有含 %q 的錯誤，實際：%v", c.want, res.Errors)
			}
		})
	}
	// 開發階段不以授權阻擋：不可散布的來源照樣編入，並記在結果的來源清單中供日後對照
	dir0 := copyFixture(t)
	appendTo(t, filepath.Join(dir0, "lexicon/words.tsv"), "樣本\tㄧㄤˋ ㄅㄣˇ\tprimary\ty\tnd")
	res := Build(load(t, dir0), DefaultOptions())
	found := false
	for _, s := range res.Sources {
		found = found || s.ID == "nd" && !s.Distributable
	}
	if len(res.Errors) > 0 || !found {
		t.Errorf("不可散布的來源應編入並記錄：錯誤 %v，來源 %v", res.Errors, res.Sources)
	}
	// 詞庫檔裡不合法的音節與重複的行都是錯誤（匯入暫存才是略過，見 TestLoadStagingDropsInvalidLines）
	dir := copyFixture(t)
	appendTo(t, filepath.Join(dir, "lexicon/words.tsv"), "壞詞\tㄅㄩ ㄘˋ\tprimary\ty\tcurated")
	appendTo(t, filepath.Join(dir, "lexicon/words.tsv"), "風景\tㄈㄥ ㄐㄧㄥˇ\tcommon\ty\tcurated")
	_, errs, _ := Load(dir)
	if !hasError(errs, "不在國語音節表") || !hasError(errs, "重複出現") {
		t.Errorf("詞庫檔的不合法音節與重複行應為錯誤：%v", errs)
	}
}

func hasError(errs []Problem, sub string) bool {
	for _, e := range errs {
		if strings.Contains(e.Msg, sub) {
			return true
		}
	}
	return false
}

func copyFixture(t *testing.T) string {
	dir := t.TempDir()
	err := filepath.Walk(fixture, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(fixture, p)
		if info.IsDir() {
			return os.MkdirAll(filepath.Join(dir, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func appendTo(t *testing.T, p, line string) {
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.WriteString(line + "\n")
}

func TestBigramMixing(t *testing.T) {
	d := load(t, fixture)
	o := DefaultOptions()
	o.Bigram = "toycorpus=3,imp=1" // 權重正規化為 0.75 與 0.25
	res := Build(d, o)
	if len(res.Errors) > 0 {
		t.Fatal(res.Errors)
	}
	// bigram 機率以 1 位元組量化儲存（lm.Compact），相對誤差在 3.7% 內
	close := func(got, want float64) bool { return got > want*0.963 && got < want*1.037 }
	if got := res.LM.Bi("企業", "風景"); !close(got, 0.75*0.2+0.25*0.6) {
		t.Errorf("混合後的 P(風景|企業) = %g", got)
	}
	if got := res.LM.Bi("<s>", "企業"); !close(got, 0.75*0.5) {
		t.Errorf("句首 bigram：%g", got)
	}
	if res.Report.Bigrams != 2 {
		t.Errorf("詞庫外的配對應略過：%d", res.Report.Bigrams)
	}
	o.Bigram = "沒有這個來源"
	if res := Build(load(t, fixture), o); len(res.Errors) == 0 {
		t.Error("不存在的 bigram 來源應報錯")
	}
}

func TestBigramPruning(t *testing.T) {
	uni := map[string]float64{"企業": 0.1, "風景": 0.01, "說服": 0.001, "東西": 0.2}
	bi := map[[2]string]float64{
		{"企業", "風景"}: 0.5,  // 前詞常見、提升大：保留
		{"說服", "風景"}: 0.5,  // 前詞罕見：先剪
		{"企業", "東西"}: 0.21, // 後詞本來就常見，bigram 幾乎沒有提升：先剪
	}
	got := pruneBigrams(bi, uni, 1, 0.7, "entropy")
	if _, ok := got[[2]string{"企業", "風景"}]; !ok || len(got) != 1 {
		t.Errorf("%v", got)
	}
}

// 基本平面以外的字只要列在詞庫裡就收錄；要不要收由詞庫審查決定，編譯不再過濾。
func TestNonBMPCharsInLexiconAreKept(t *testing.T) {
	dir := copyFixture(t)
	appendTo(t, filepath.Join(dir, "lexicon/chars.tsv"), "\U00022CF0\tㄇㄚ\tattested\ty\t\tcurated")
	if res := Build(load(t, dir), DefaultOptions()); !res.Lexicon.Has("\U00022CF0") || len(res.Errors) > 0 {
		t.Errorf("詞庫列入的擴充 B 區字應收錄：%v", res.Errors)
	}
}

// 一個詞裡有兩個以上的一／不：除了一次變一個位置，也要有全部變調的讀音（一五一十、一模一樣）。
func TestYiBuSandhiAllPositions(t *testing.T) {
	dir := copyFixture(t)
	appendTo(t, filepath.Join(dir, "lexicon/words.tsv"), "一模一樣\tㄧ ㄇㄛˊ ㄧ ㄧㄤˋ\tprimary\ty\tcurated")
	res := Build(load(t, dir), DefaultOptions())
	if len(res.Errors) > 0 {
		t.Fatal(res.Errors)
	}
	for _, r := range []string{"ㄧˋ ㄇㄛˊ ㄧˊ ㄧㄤˋ", "ㄧˋ ㄇㄛˊ ㄧ ㄧㄤˋ", "ㄧ ㄇㄛˊ ㄧˊ ㄧㄤˋ"} {
		if prw(res.Lexicon, r, "一模一樣") == 0 {
			t.Errorf("%s 打不出一模一樣", r)
		}
	}
}
