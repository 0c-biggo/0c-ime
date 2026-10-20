package importer

import (
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/zhuyin"
	"github.com/0c-biggo/0c-ime/internal/compile"
)

func TestReadTWSEISIN(t *testing.T) {
	html := `<tr><td bgcolor=#FAFAD2>2327　國巨*</td></tr>` +
		`<tr><td bgcolor=#FAFAD2>3661　世芯-KY</td></tr>` +
		`<tr><td bgcolor=#FAFAD2>2330　台積電</td></tr>` +
		`<tr><td bgcolor=#FAFAD2>0050　元大台灣50</td></tr>` + // ETF：代號也是四碼，但名稱含數字，不收
		`<tr><td bgcolor=#FAFAD2>030001　統一5C購01</td></tr>` + // 權證
		`<tr><td bgcolor=#FAFAD2>6589　TPK-KY</td></tr>` + // 不是漢字
		`<tr><td bgcolor=#FAFAD2>2331　台積電</td></tr>` // 重複
	got, err := ReadTWSEISIN(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "世芯,台積電,國巨" {
		t.Errorf("簡稱：%v", got)
	}
}

func TestTaiwanReadings(t *testing.T) {
	mc := []WordLine{
		{"星期", "ㄒㄧㄥ ㄑㄧˊ", "primary", "y", "mcbopomofo"},
		{"日期", "ㄖˋ ㄑㄧˊ", "primary", "y", "mcbopomofo"},
		{"期間", "ㄑㄧˊ ㄐㄧㄢ", "primary", "y", "mcbopomofo"},
		{"期待", "ㄑㄧˊ ㄉㄞˋ", "primary", "y", "mcbopomofo"},
		{"學期", "ㄒㄩㄝˊ ㄑㄧˊ", "primary", "y", "mcbopomofo"},
	}
	ce := []WordLine{
		{"星期", "ㄒㄧㄥ ㄑㄧ", "common", "n", "cc-cedict"},
		{"日期", "ㄖˋ ㄑㄧ", "common", "n", "cc-cedict"},
		{"期間", "ㄑㄧ ㄐㄧㄢ", "common", "n", "cc-cedict"},
		{"期待", "ㄑㄧ ㄉㄞˋ", "common", "n", "cc-cedict"},
		{"活期", "ㄏㄨㄛˊ ㄑㄧ", "primary", "n", "cc-cedict"}, // 只有 CC-CEDICT：補上台灣讀音
		{"學期", "ㄒㄩㄝˊ ㄑㄧ", "common", "n", "cc-cedict"},
	}
	rules, added := TaiwanReadings(ce, mc)
	if rules != 1 || len(added) != 1 || added[0] != (WordLine{"活期", "ㄏㄨㄛˊ ㄑㄧˊ", "primary", "y", "cc-cedict"}) {
		t.Errorf("規則 %d、新增 %v", rules, added)
	}
	// 只出現兩次的對照不夠穩定，不採用
	if rules, added := TaiwanReadings(ce[3:], mc); rules != 0 || len(added) != 0 {
		t.Errorf("次數不足仍採用：%d %v", rules, added)
	}
}

func TestFixUsesWordInternalReading(t *testing.T) {
	var words []compile.WordReading
	add := func(w, r, origin string) {
		words = append(words, compile.WordReading{Word: w, Reading: zhuyin.ParseReading(r), Origins: []string{origin}})
	}
	for i := 0; i < 25; i++ {
		add("和平", "ㄏㄜˊ ㄆㄧㄥˊ", "mcbopomofo")
	}
	add("和你", "ㄏㄢˋ ㄋㄧˇ", "mcbopomofo")
	for i := 0; i < 30; i++ {
		add("中和", "ㄓㄨㄥ ㄏㄢˋ", "moi-places") // 自動標音的名稱不算進統計
	}
	m := WordInternalReadings(words, "moi-places")
	if got := m.Fix("佳和", []string{"ㄐㄧㄚ", "ㄏㄢˋ"}, []bool{true, true}); got[1] != "ㄏㄜˊ" {
		t.Errorf("落單的和應改讀 ㄏㄜˊ：%v", got)
	}
	if got := m.Fix("佳和", []string{"ㄐㄧㄚ", "ㄏㄢˋ"}, []bool{false, false}); got[1] != "ㄏㄢˋ" {
		t.Errorf("屬於詞庫詞的位置不應改：%v", got)
	}
}
