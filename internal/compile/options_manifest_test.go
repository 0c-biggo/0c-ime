package compile

import (
	"encoding/json"
	"strings"
	"testing"
)

// 預設 0 或空值的新編譯參數不寫進 manifest：舊版字典資料（如 Debian 固定的 0c-ime-data）以新工具重建、
// 參數設成 0 時，manifest 要與舊工具的產物逐位元組相同。
func TestNewOptionsOmittedFromManifestWhenZero(t *testing.T) {
	o := DefaultOptions()
	o.AttestedMass, o.CuratedBigram = 0, ""
	b, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{`"AttestedMass"`, `"CuratedBigram"`} {
		if strings.Contains(string(b), k) {
			t.Errorf("%s 為零值時仍寫進 manifest：%s", k, b)
		}
	}
	b, err = json.Marshal(DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"AttestedMass":0.000003`) {
		t.Errorf("預設的 AttestedMass 要寫進 manifest：%s", b)
	}
}
