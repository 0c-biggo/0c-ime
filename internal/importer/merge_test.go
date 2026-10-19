package importer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func table(t *testing.T, k Kind, s string) (*Table, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "t.tsv")
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	tb, err := ReadTable(p, k)
	if err != nil {
		t.Fatal(err)
	}
	return tb, p
}

func whats(cs []Change) string {
	var out []string
	for _, c := range cs {
		out = append(out, c.What+":"+c.Row[0])
	}
	return strings.Join(out, " ")
}

func TestDiffAndApplyWords(t *testing.T) {
	tb, p := table(t, Words, "# 詞面\t讀音\tgrade\tnorm\torigin\t備註\n"+
		"風景\tㄈㄥ ㄐㄧㄥˇ\tcommon\ty\tsrc\n"+
		"企業\tㄑㄧˋ ㄧㄝˋ\tprimary\ty\tcurated\t人工審過\n"+
		"舊詞\tㄐㄧㄡˋ ㄘˊ\tprimary\ty\tsrc\n"+
		"共有\tㄍㄨㄥˋ ㄧㄡˇ\tprimary\ty\tother,src\n")
	staged := [][]string{
		{"風景", "ㄈㄥ ㄐㄧㄥˇ", "primary", "y", "src"},  // 等級較高
		{"企業", "ㄑㄧˋ ㄧㄝˋ", "attested", "y", "src"}, // 人工已有：加上來源，等級不降
		{"新詞", "ㄒㄧㄣ ㄘˊ", "primary", "y", "src"},
		{"工口", "ㄟ ㄌㄨㄛˊ", "primary", "n", "src"},         // 列為不收
		{"\U00022CF0詞", "ㄇㄚ ㄘˊ", "primary", "y", "src"}, // 基本平面外
		{"別的", "ㄅㄧㄝˊ ㄉㄜ˙", "primary", "y", "other"},     // 其他來源，這次不比
	}
	cs := Diff(tb, staged, "src", map[string]bool{"工口\tㄟ ㄌㄨㄛˊ": true})
	if got := whats(cs); got != "joined:企業 rejected:工口 added:新詞 grade:風景 nonbmp:\U00022CF0詞 removed:共有 removed:舊詞" {
		t.Fatalf("差異：%s", got)
	}
	tb.Apply(cs)
	if err := tb.Write(p); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	want := "# 詞面\t讀音\tgrade\tnorm\torigin\t備註\n" +
		"企業\tㄑㄧˋ ㄧㄝˋ\tprimary\ty\tcurated,src\t人工審過\n" +
		"共有\tㄍㄨㄥˋ ㄧㄡˇ\tprimary\ty\tother\n" +
		"新詞\tㄒㄧㄣ ㄘˊ\tprimary\ty\tsrc\n" +
		"風景\tㄈㄥ ㄐㄧㄥˇ\tprimary\ty\tsrc\n"
	if string(b) != want {
		t.Errorf("寫回：\n%s\n應為：\n%s", b, want)
	}
}

// 同一字的讀音先後會影響編譯時的讀音展開：寫回只依字排序，同一字維持原順序，新讀音接在後面。
func TestWriteKeepsOrderWithinAKey(t *testing.T) {
	tb, p := table(t, Chars, "丼\tㄐㄧㄥˇ\tattested\ty\tㄉㄨㄥˋ\tcurated\n乙\tㄧˇ\tprimary\ty\t\tsrc\n丼\tㄉㄨㄥˋ\tprimary\tn\t\tcurated\n")
	tb.Apply(Diff(tb, [][]string{{"丼", "ㄉㄢˇ", "attested", "y", "", "src"}, {"乙", "ㄧˇ", "primary", "y", "", "src"}}, "src", nil))
	if err := tb.Write(p); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	want := "丼\tㄐㄧㄥˇ\tattested\ty\tㄉㄨㄥˋ\tcurated\n丼\tㄉㄨㄥˋ\tprimary\tn\t\tcurated\n丼\tㄉㄢˇ\tattested\ty\t\tsrc\n乙\tㄧˇ\tprimary\ty\t\tsrc\n"
	if string(b) != want {
		t.Errorf("寫回：\n%s\n應為：\n%s", b, want)
	}
}

func TestReadTableRejectsCommentsBetweenRows(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.tsv")
	os.WriteFile(p, []byte("# 開頭\n甲\tㄐㄧㄚˇ\tprimary\ty\ta\n# 中間的註解\n"), 0o644)
	if _, err := ReadTable(p, Words); err == nil || !strings.Contains(err.Error(), "備註欄") {
		t.Errorf("中間的註解應被拒絕：%v", err)
	}
}
