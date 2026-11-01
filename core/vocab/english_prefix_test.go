package vocab

import (
	"slices"
	"testing"
)

// #247：英文模式補字用的前綴列舉：不分大小寫比對、照詞條字面回傳，短的在前、同長度照檔案順序。
func TestEnglishPrefix(t *testing.T) {
	s := New()
	var entries []Entry
	for _, w := range []string{"pCloud", "Rclone", "PCIe", "ls", "LS", "pcb", "git-lfs", "ipv6"} {
		entries = append(entries, Entry{Text: w})
	}
	entries = append(entries, Entry{Text: "世傑", Readings: []string{"ㄕˋ ㄐㄧㄝˊ"}})
	if _, err := s.Add(entries, SourceManual, 1); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		raw   string
		limit int
		want  []string
	}{
		{"pc", 0, []string{"pcb", "PCIe", "pCloud"}},
		{"PC", 0, []string{"pcb", "PCIe", "pCloud"}},
		{"pc", 2, []string{"pcb", "PCIe"}},
		{"rcl", 0, []string{"Rclone"}},
		{"pcloud", 0, []string{"pCloud"}}, // 只差大小寫也列出
		{"ls", 0, []string{"ls", "LS"}},   // ls 和 LS 是兩個詞；要不要排除恰好相同的由呼叫端決定
		{"git", 0, []string{"git-lfs"}},
		{"ip", 0, []string{"ipv6"}},
		{"pcloudx", 0, nil},
		{"zz", 0, nil},
		{"", 0, nil},
		{"世", 0, nil}, // 漢字詞不在這裡
	} {
		if got := s.EnglishPrefix(c.raw, c.limit); !slices.Equal(got, c.want) {
			t.Errorf("EnglishPrefix(%q, %d) = %q，要 %q", c.raw, c.limit, got, c.want)
		}
	}
	if _, err := s.Remove([]string{"PCIe"}, SourceManual, 2); err != nil {
		t.Fatal(err)
	}
	if got := s.EnglishPrefix("pc", 0); !slices.Equal(got, []string{"pcb", "pCloud"}) {
		t.Errorf("刪除後：%q", got)
	}
	if got := s.Clone().EnglishPrefix("pc", 0); !slices.Equal(got, []string{"pcb", "pCloud"}) {
		t.Errorf("Clone 後：%q", got)
	}
}
