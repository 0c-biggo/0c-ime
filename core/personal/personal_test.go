package personal

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

func TestStoreRoundTrip(t *testing.T) {
	m := NewModel(lexicon.NewMem(), lm.NewMem())
	syls := zhuyin.ParseReading("ㄑㄧㄣ ㄗˇ ㄐㄧㄥˇ")
	for i := 0; i < 2; i++ {
		m.Learn(Commit{Syls: syls, Path: []Seg{{Word: "親子", Start: 0, End: 2}, {Word: "丼", Start: 2, End: 3, Manual: true}}})
	}
	if _, ok := m.Store.Phrases["親子丼"]; !ok {
		t.Fatal("兩次後應晉升為個人片語")
	}
	var buf bytes.Buffer
	if err := m.Store.Save(&buf); err != nil {
		t.Fatal(err)
	}
	back, err := LoadStore(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m.Store, back) {
		t.Errorf("存檔來回不一致\n%+v\n%+v", m.Store, back)
	}
}

// 加入版本號以前的檔案照常讀；存檔寫入目前版本；較新版本的檔案拒絕讀入，避免舊版誤讀後寫回。
func TestStoreFormatVersion(t *testing.T) {
	legacy, err := LoadStore(bytes.NewBufferString(`{"Uni":{"丼":1},"Total":1}`))
	if err != nil || legacy.Uni["丼"] != 1 || legacy.Format != 0 {
		t.Fatalf("舊格式讀取失敗：%v %+v", err, legacy)
	}
	var buf bytes.Buffer
	if err := legacy.Save(&buf); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte(fmt.Sprintf(`"Format":%d`, StoreFormat))) {
		t.Fatalf("存檔未寫入格式版本：%s", buf.String())
	}
	_, err = LoadStore(bytes.NewBufferString(fmt.Sprintf(`{"Format":%d,"Uni":{"丼":1},"Later":{"x":1}}`, StoreFormat+1)))
	var newer *NewerFormatError
	if !errors.As(err, &newer) || newer.Format != StoreFormat+1 {
		t.Fatalf("應拒絕較新格式：%v", err)
	}
}

// 機率恆 ≤ 1，且在高頻上下文中單次觀察的影響自動變小。
func TestProbIsBoundedAndConservative(t *testing.T) {
	sys := lm.NewMem()
	sys.SetUni("丼", 5e-6)
	m := NewModel(lexicon.NewMem(), sys)
	m.observe("親子", "丼", 1)
	sparse := m.Prob("親子", "丼")
	for i := 0; i < 500; i++ {
		m.observe("的", "事", 1)
	}
	m.observe("的", "丼", 1)
	busy := m.Prob("的", "丼")
	if sparse < 0.4 || sparse > 1 {
		t.Errorf("稀疏上下文一次觀察應有力：%g", sparse)
	}
	if busy > 0.01 {
		t.Errorf("高頻上下文一次觀察應保守：%g", busy)
	}
}
