package personal

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/symbols"
)

// #290：符號選單「最近」分頁的順序與分數存在格式 7。
// 新版讀舊檔：格式 6 以前沒有這兩個欄位，讀進來是空的，其他資料照舊；存檔寫成格式 7、來回一致。
func TestSymbolRecentOldFormats(t *testing.T) {
	if StoreFormat != 7 {
		t.Fatalf("符號的最近使用加在格式 7：%d", StoreFormat)
	}
	for _, format := range []int{0, 4, 5, 6} {
		s, err := LoadStore(strings.NewReader(fmt.Sprintf(`{"Format":%d,"Uni":{"保留":2},"EmojiRecent":["😀"]}`, format)))
		if err != nil || s.SymbolRecent != nil || s.SymbolStats != nil || s.Uni["保留"] != 2 || len(s.EmojiRecent) != 1 {
			t.Fatalf("格式 %d：%v %+v", format, err, s)
		}
		m := &Model{Store: s, Now: func() int64 { return 1e9 }}
		m.RememberSymbol("※")
		var b bytes.Buffer
		if err := s.Save(&b); err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(b.Bytes(), []byte(`"Format":7`)) || !bytes.Contains(b.Bytes(), []byte(`"SymbolRecent":["※"]`)) {
			t.Fatalf("存成格式 7：%s", b.String())
		}
		back, err := LoadStore(&b)
		if err != nil || !reflect.DeepEqual(back, s) {
			t.Fatalf("來回一致：%v", err)
		}
	}
}

// 舊版讀新檔：格式 6 的版本看到格式 7 只照常輸入，不讀入也不寫回（LoadStore 對較新格式回傳 NewerFormatError，
// 宿主照 AGENTS.md 處理）。這裡用本版模擬「比自己新一版」的檔案；實際用 0.2.11 的程式讀格式 7 另見 issue 驗證紀錄。
func TestSymbolRecentNewerFormatRejected(t *testing.T) {
	_, err := LoadStore(strings.NewReader(fmt.Sprintf(`{"Format":%d,"SymbolRecent":["※"],"Later":1}`, StoreFormat+1)))
	var newer *NewerFormatError
	if !errors.As(err, &newer) {
		t.Fatalf("較新格式要拒絕：%v", err)
	}
}

// 檔案裡的最近清單只收目錄裡的符號：重複、任意文字丟掉，最多 MaxRecent 項；分數只留給清單裡的。
func TestSymbolRecentCleanAndBounded(t *testing.T) {
	s, err := LoadStore(strings.NewReader(`{"Format":7,"SymbolRecent":["※","任意文字","※","，"],"SymbolStats":{"※":{"s":3},"任意文字":{"s":9},"→":{"s":1}}}`))
	if err != nil || !reflect.DeepEqual(s.SymbolRecent, []string{"※", "，"}) || len(s.SymbolStats) != 1 || s.SymbolStats["※"].Score != 3 {
		t.Fatalf("%v %+v %+v", err, s.SymbolRecent, s.SymbolStats)
	}
	m := &Model{Store: NewStore(), Now: func() int64 { return 1e9 }}
	for _, tab := range symbols.Tabs {
		for _, item := range tab.Items {
			m.RememberSymbol(item)
		}
	}
	m.RememberSymbol("not a symbol")
	if len(m.Store.SymbolRecent) != symbols.MaxRecent || len(m.Store.SymbolStats) != symbols.MaxRecent {
		t.Fatalf("有上限：%d %d", len(m.Store.SymbolRecent), len(m.Store.SymbolStats))
	}
	if m.Store.Total != 0 || len(m.Store.Uni) != 0 || len(m.Store.English) != 0 || len(m.Store.EmojiRecent) != 0 {
		t.Fatal("符號的最近使用不碰學習與表情")
	}
	// 常用的排前面，新選一次的不會立刻擠掉它（同表情的遲滯）。
	m = &Model{Store: NewStore(), Now: func() int64 { return 1e9 }}
	for range 3 {
		m.RememberSymbol("→")
	}
	m.RememberSymbol("※")
	if !reflect.DeepEqual(m.Store.SymbolRecent, []string{"→", "※"}) {
		t.Fatalf("常用＋最近排序：%v", m.Store.SymbolRecent)
	}
}
