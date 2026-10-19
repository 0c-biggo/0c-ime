package compile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureWith 把編譯測試資料複製到暫存目錄，再寫入 rejected.tsv。
func fixtureWith(t *testing.T, rejected string) string {
	t.Helper()
	dir := copyFixture(t)
	if err := os.WriteFile(filepath.Join(dir, "lexicon", "rejected.tsv"), []byte("# 詞面\t讀音\t依據\n"+rejected), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// #107：「工口 ㄟ ㄌㄨㄛˊ」審過不收，記在 rejected.tsv；它不在詞庫裡，編譯照常。
func TestRejectedReadingOutsideLexiconIsAccepted(t *testing.T) {
	d := load(t, fixtureWith(t, "工口\tㄟ ㄌㄨㄛˊ\t測試：音譯讀音不收\n"))
	if len(d.Rejected) != 1 || d.Rejected[0].Word != "工口" {
		t.Fatalf("rejected.tsv 沒有讀進來：%+v", d.Rejected)
	}
	if r := Build(d, DefaultOptions()); len(r.Errors) > 0 {
		t.Fatal(r.Errors)
	}
}

func TestRejectedListIsChecked(t *testing.T) {
	for _, c := range []struct{ line, want string }{
		{"工口\tㄟ ㄌㄨㄛˊ\t\n", "依據"},                 // 必須寫明依據
		{"風景\tㄈㄥ ㄐㄧㄥˇ\t矛盾\n", "仍在詞庫"},            // 詞庫裡還有這個讀音
		{"工口\tㄟ ㄌㄨㄛˊ\t一\n工口\tㄟ ㄌㄨㄛˊ\t二\n", "重複"}, // 同一筆列兩次
		{"風景\tㄈㄥ\t字數\n", "音節"},                   // 讀音與字數不符
	} {
		_, errs, err := Load(fixtureWith(t, c.line))
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, e := range errs {
			found = found || strings.Contains(e.Msg, c.want)
		}
		if !found {
			t.Errorf("%q：應有含 %q 的錯誤，得到 %v", c.line, c.want, errs)
		}
	}
}

// 匯入暫存以寬鬆模式讀取：不合法的行略過、計數並留下例子，不是錯誤。
func TestLoadStagingDropsInvalidLines(t *testing.T) {
	dir := t.TempDir()
	write := func(name, s string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("chars-imp.tsv", "# 匯入檔\n景\tㄐㄧㄥˇ\tprimary\ty\t\timp\n怪\tㄍㄨㄞx\tprimary\ty\t\timp\n")
	write("words-imp.tsv", "風景\tㄈㄥ ㄐㄧㄥˇ\tprimary\ty\timp\n三字詞\tㄙㄢ ㄗˋ\tprimary\ty\timp\n")
	write("counts-imp.tsv", "# origin: imp\n風景\t3\n")
	st, err := LoadStaging(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Chars) != 1 || len(st.Words) != 1 {
		t.Fatalf("合法的行：字 %d、詞 %d", len(st.Chars), len(st.Words))
	}
	if st.Dropped.Count["chars-imp.tsv"] != 1 || st.Dropped.Count["words-imp.tsv"] != 1 || len(st.Dropped.Samples["words-imp.tsv"]) != 1 {
		t.Errorf("略過計數：%+v", st.Dropped)
	}
}
