package englishpack

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// displayList 是 data/english/display.tsv 的內容（#358 後續，負責人確認的清單；字典裡沒有的 Korea、
// America、Europe 不列）。
var displayList = map[string]string{
	"taiwan": "Taiwan", "taipei": "Taipei", "japan": "Japan", "china": "China", "chinese": "Chinese",
	"english": "English", "american": "American",
	"google": "Google", "facebook": "Facebook", "netflix": "Netflix", "microsoft": "Microsoft", "samsung": "Samsung",
	"instagram": "Instagram", "android": "Android", "windows": "Windows", "linux": "Linux", "mac": "Mac",
	"intel": "Intel", "tesla": "Tesla", "twitter": "Twitter", "telegram": "Telegram", "bitcoin": "Bitcoin",
	"amazon": "Amazon", "sony": "Sony",
	"dram": "DRAM", "ram": "RAM", "bios": "BIOS", "fps": "FPS", "ssh": "SSH", "id": "ID", "hp": "HP", "qr": "QR", "lg": "LG",
	"ii": "II", "iii": "III", "iv": "IV",
}

type rankedRecord struct {
	text    string
	weight  uint64
	flag    byte // 不含「有寫法」的位元
	display string
}

// readRankedWords 逐條讀格式 2 的 words.bin（core/english/codec.go 的 encodeRanked）。
func readRankedWords(t *testing.T, dir string) []rankedRecord {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "words.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(b, []byte("0cEN\x00\x02")) {
		t.Fatal("不是格式 2")
	}
	n := binary.LittleEndian.Uint32(b[6:])
	b = b[10:]
	out := make([]rankedRecord, 0, n)
	for range n {
		l := int(b[0])
		r := rankedRecord{text: string(b[1 : 1+l]), weight: binary.LittleEndian.Uint64(b[1+l:]), flag: b[9+l] &^ 4}
		hasDisplay := b[9+l]&4 != 0
		b = b[10+l:]
		if hasDisplay {
			r.display, b = string(b[:l]), b[l:]
		}
		out = append(out, r)
	}
	if len(b) != 0 {
		t.Fatal("words.bin 有多餘內容")
	}
	return out
}

// #358 後續：display.tsv 只補寫法。清單裡每個詞照它顯示；跟清單空白時編出的資料包比，詞、權重、旗標、
// 詞對與混打認不認逐一相同，寫法只在清單裡的詞不同。
func TestRankedPackDisplayListOnlySetsDisplay(t *testing.T) {
	d, _, with := compileRanked(t)
	src := t.TempDir()
	if err := os.CopyFS(src, os.DirFS("../../data/english")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "display.tsv"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	without := filepath.Join(t.TempDir(), "pack")
	if err := CompileRanked(src, without); err != nil {
		t.Fatal(err)
	}
	d0, _, err := Load(without)
	if err != nil {
		t.Fatal(err)
	}
	for word, want := range displayList {
		if !d0.Has(word) {
			t.Errorf("%s 不在字典裡", word)
		}
		if got := d.Display(word); got != want {
			t.Errorf("%s 顯示成 %q，要 %q", word, got, want)
		}
		if got := d0.Display(word); got != word {
			t.Errorf("沒有清單時 %s 顯示成 %q", word, got)
		}
	}
	a, b := readRankedWords(t, without), readRankedWords(t, with)
	if len(a) != len(b) {
		t.Fatalf("詞數 %d → %d", len(a), len(b))
	}
	changed := 0
	for i := range a {
		x, y := a[i], b[i]
		if x.text != y.text || x.weight != y.weight || x.flag != y.flag {
			t.Fatalf("%s：詞、權重或旗標改變（%+v → %+v）", x.text, x, y)
		}
		if want, ok := displayList[x.text]; ok {
			if y.display != want {
				t.Errorf("%s 的寫法 %q，要 %q", x.text, y.display, want)
			}
			changed++
		} else if x.display != y.display {
			t.Errorf("%s 不在清單裡，寫法卻從 %q 變成 %q", x.text, x.display, y.display)
		}
		if d0.Mixed().Has(x.text) != d.Mixed().Has(x.text) || d0.Protected(x.text) != d.Protected(x.text) || d0.Prior(x.text) != d.Prior(x.text) {
			t.Errorf("%s 的混打、保護或權重改變", x.text)
		}
	}
	if changed != len(displayList) {
		t.Fatalf("清單 %d 個詞，資料包裡找到 %d 個", len(displayList), changed)
	}
	x, _ := os.ReadFile(filepath.Join(without, "bigram.bin"))
	y, _ := os.ReadFile(filepath.Join(with, "bigram.bin"))
	if !bytes.Equal(x, y) {
		t.Fatal("bigram.bin 因寫法清單改變")
	}
	// 短詞（id、hp、ii）混打照舊認：它們是格式 1 就有的詞，清單沒讓任何詞變成新的英文詞。
	for _, w := range []string{"id", "hp", "qr", "lg", "ram", "fps", "ssh", "ii", "iv"} {
		if !d.Mixed().Has(w) || !d0.Mixed().Has(w) {
			t.Errorf("%s：混打認不認改變", w)
		}
	}
	// 查詢照打的大小寫套上寫法：全大寫照打，小寫與句首大寫用清單的寫法。
	for typed, want := range map[string]string{"taiwan": "Taiwan", "Taiwan": "Taiwan", "TAIWAN": "TAIWAN", "dram": "DRAM", "Id": "ID", "iii": "III"} {
		if got := d.Display(typed); got != want {
			t.Errorf("Display(%s) = %q，要 %q", typed, got, want)
		}
	}
	if got := d.Prefix("netf", 1); len(got) == 0 || got[0].Text != "Netflix" {
		t.Errorf("Prefix(netf) = %v，要 Netflix", candidateTexts(got))
	}
}

// display.tsv 只能補字典裡已有的詞的寫法；不在字典裡、不是大小寫變化、重複、欄數不對，或 technical.tsv
// 第三欄已有寫法的，都不編，也不留產物。
func TestDisplayListRejectsNewWordsAndBadForms(t *testing.T) {
	src := t.TempDir()
	if err := os.CopyFS(src, os.DirFS("../../data/english")); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		"korea\tKorea", "taiwan\tTaiwanese", "taiwan\ttaiwan", "taiwan\tTaiwan\ntaiwan\tTAIWAN",
		"pdf\tPdf", "taiwan\tTaiwan\t1", "Taiwan\tTaiwan", "taiwan",
	} {
		if err := os.WriteFile(filepath.Join(src, "display.tsv"), []byte(body+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(t.TempDir(), "pack")
		if err := CompileRanked(src, out); err == nil {
			t.Errorf("%q 應該被拒絕", body)
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Errorf("%q：拒絕後不該留下產物", body)
		}
	}
	if err := os.Remove(filepath.Join(src, "display.tsv")); err != nil {
		t.Fatal(err)
	}
	if err := CompileRanked(src, filepath.Join(t.TempDir(), "pack")); err == nil {
		t.Error("少了 display.tsv 應該拒絕")
	}
}
