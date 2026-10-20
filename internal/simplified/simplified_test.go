package simplified

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

// 內附的字典必須是 OpenCC ver.1.4.2 的上游原文；換版本時連同 opencc/README.md、
// data/sources/ledger.tsv 與 testdata/simplified/tw2s.tsv 一起更新。
var upstream = map[string]string{
	"CJK_Compatibility_Ideographs.txt": "663cc10b7de7c7cee3ff9df1ee963977d6c26c00b23f8fe670fab481283ed4f9",
	"TWVariants.txt":                   "e187278e119c427ca561180ac5da5b20e9f8681190458f35c327ce499e95a6a5",
	"TWVariantsRevPhrases.txt":         "5ebfb4bdc938c2b14e01ace378988d5d3dc12462b3496ef1d424951ccd371256",
	"TSPhrases.txt":                    "362fa1b9a7d6edd04b462a32e12c9fef3adae822ab1dee9c83561cc37c06cb1f",
	"TSCharacters.txt":                 "737c21c66f55a419dd6956cb3089476cdefc5a36877452631617696df1e5d925",
	"LICENSE":                          "b534e465949558eec2597b04f5092b5e161236a68dfbfd04d547592ac3964308",
}

func TestEmbeddedDictionariesAreUpstream(t *testing.T) {
	entries, err := files.ReadDir("opencc")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(upstream) {
		t.Fatalf("內附 %d 個檔案，預期 %d 個", len(entries), len(upstream))
	}
	for name, want := range upstream {
		sum := sha256.Sum256([]byte(read(name)))
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Errorf("%s 不是固定的上游版本：%s", name, got)
		}
	}
	if !strings.Contains(License(), "Apache License") {
		t.Fatal("授權全文不對")
	}
}

// 對照上游 OpenCC 1.4.2 實際的轉換結果（Python 套件 opencc==1.4.2、tw2s、不含豆腐字字典）：
// 每個詞組字典的鍵各一行，加上文件裡的句子與手選的難例（顯著、計畫、乾隆、什麼、英文、Emoji、標點）。
func TestTW2SMatchesUpstreamOpenCC(t *testing.T) {
	conv, err := TW2S()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("../../testdata/simplified/tw2s.tsv")
	if err != nil {
		t.Fatal(err)
	}
	n, bad := 0, 0
	for _, line := range strings.Split(string(b), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		in, want, ok := strings.Cut(line, "\t")
		if !ok {
			t.Fatalf("格式不對：%q", line)
		}
		n++
		if got := conv.Convert(in); got != want {
			bad++
			if bad <= 20 {
				t.Errorf("%s → %s，上游是 %s", in, got, want)
			}
		}
	}
	if n < 2000 || bad > 0 {
		t.Fatalf("%d 行中 %d 行與上游不同", n, bad)
	}
}

func TestTW2SKnownCases(t *testing.T) {
	conv, err := TW2S()
	if err != nil {
		t.Fatal(err)
	}
	for in, want := range map[string]string{
		"顯著":                  "显著", // 不是「显着」
		"計畫":                  "计划",
		"他看著我":                "他看着我",
		"乾隆喝乾了酒":              "乾隆喝干了酒",
		"什麼":                  "什么", // 不是「什幺」
		"頭髮發財":                "头发发财",
		"臺灣":                  "台湾",
		"iPhone 17，NT$32,900": "iPhone 17，NT$32,900",
		"😀（｡◕‿◕｡）「」……——": "😀（｡◕‿◕｡）「」……——",
		"ㄅㄆㄇ": "ㄅㄆㄇ",
	} {
		if got := conv.Convert(in); got != want {
			t.Errorf("%s → %s，預期 %s", in, got, want)
		}
	}
	if again, _ := TW2S(); again != conv {
		t.Fatal("轉換器應只建一次")
	}
}
