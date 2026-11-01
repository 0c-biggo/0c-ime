package personal

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
)

func zhuyinKeyModel() *Model {
	m := NewModel(lexicon.NewMem(), lm.NewMem())
	m.AdvanceTime(1000 * daySeconds)
	return m
}

func TestZhuyinKeysPickKeepDrop(t *testing.T) {
	m := zhuyinKeyModel()
	if m.HasZhuyinKeys() || m.ZhuyinKeyPicked("我", "ㄉ") {
		t.Fatal("空白個人資料沒有紀錄")
	}
	gen := m.Gen
	m.LearnZhuyinKeys(nil)
	m.LearnZhuyinKeys([]ZhuyinKeyCommit{{Left: "我", Symbol: "ㄉ", Action: ZhuyinKeyKeep}, {Left: "我", Symbol: "ㄉ", Action: ZhuyinKeyDrop}})
	if m.HasZhuyinKeys() || m.Gen != gen {
		t.Fatalf("沒有紀錄時留著與選回都不動：%v gen %d→%d", m.Store.ZhuyinKeys, gen, m.Gen)
	}

	m.LearnZhuyinKeys([]ZhuyinKeyCommit{{Left: "我", Symbol: "ㄉ", Action: ZhuyinKeyPick}, {Symbol: "ㄅ", Action: ZhuyinKeyPick}})
	if !m.ZhuyinKeyPicked("我", "ㄉ") || !m.ZhuyinKeyPicked("", "ㄅ") {
		t.Fatalf("一次選擇就生效：%v", m.Store.ZhuyinKeys)
	}
	for _, c := range [][2]string{{"我", "ㄅ"}, {"", "ㄉ"}, {"你", "ㄉ"}} {
		if m.ZhuyinKeyPicked(c[0], c[1]) {
			t.Fatalf("%q＋%s 不算", c[0], c[1])
		}
	}
	if m.Gen == gen {
		t.Fatal("個人資料改變要遞增 Gen")
	}

	// 同一天選很多次，長期證據照每日上限；隔很久沒用就過期。
	for range 10 {
		m.LearnZhuyinKeys([]ZhuyinKeyCommit{{Left: "你", Symbol: "ㄉ", Action: ZhuyinKeyPick}})
	}
	if e := m.Store.ZhuyinKeys[pair("你", "ㄉ")]; e.Long > 1 {
		t.Fatalf("每日上限：%+v", e)
	}
	m.AdvanceTime(m.Store.Time + 130*daySeconds)
	if m.ZhuyinKeyPicked("我", "ㄉ") {
		t.Fatal("一次選擇 130 天沒用應過期")
	}

	// 照常用著暫停衰減，不增加。
	m = zhuyinKeyModel()
	m.LearnZhuyinKeys([]ZhuyinKeyCommit{{Left: "我", Symbol: "ㄉ", Action: ZhuyinKeyPick}})
	long := m.Store.ZhuyinKeys[pair("我", "ㄉ")].Long
	for range 10 {
		m.AdvanceTime(m.Store.Time + 60*daySeconds)
		m.LearnZhuyinKeys([]ZhuyinKeyCommit{{Left: "我", Symbol: "ㄉ", Action: ZhuyinKeyKeep}})
	}
	if got := m.Store.ZhuyinKeys[pair("我", "ㄉ")].Long; got != long || !m.ZhuyinKeyPicked("我", "ㄉ") {
		t.Fatalf("照常用著：%v，原本 %v", got, long)
	}

	// 選回原按鍵整筆消失；一次組字裡以後面的為準。
	m.LearnZhuyinKeys([]ZhuyinKeyCommit{{Left: "我", Symbol: "ㄉ", Action: ZhuyinKeyDrop}})
	if m.HasZhuyinKeys() {
		t.Fatalf("選回應消失：%v", m.Store.ZhuyinKeys)
	}
	m.LearnZhuyinKeys([]ZhuyinKeyCommit{{Left: "我", Symbol: "ㄉ", Action: ZhuyinKeyDrop}, {Left: "我", Symbol: "ㄉ", Action: ZhuyinKeyPick}})
	if !m.ZhuyinKeyPicked("我", "ㄉ") {
		t.Fatal("後面選的算")
	}
	m.LearnZhuyinKeys([]ZhuyinKeyCommit{{Left: "我", Symbol: "ㄉ", Action: ZhuyinKeyPick}, {Left: "我", Symbol: "ㄉ", Action: ZhuyinKeyDrop}})
	if m.ZhuyinKeyPicked("我", "ㄉ") {
		t.Fatal("後面反悔的算")
	}
}

// 「移除」清掉含這個詞的紀錄（我ㄉ、我、ㄉ 都對得到，天 對不到）；「不再學」另外擋住之後的選擇，「恢復學習」解除；復原完全回到操作前。
func TestZhuyinKeysUnlearnForgetUndo(t *testing.T) {
	seed := func() *Model {
		m := zhuyinKeyModel()
		m.LearnZhuyinKeys([]ZhuyinKeyCommit{{Left: "我", Symbol: "ㄉ", Action: ZhuyinKeyPick}, {Left: "你", Symbol: "ㄉ", Action: ZhuyinKeyPick}, {Symbol: "ㄉ", Action: ZhuyinKeyPick}})
		return m
	}
	for w, left := range map[string]int{"我ㄉ": 2, "我": 2, "ㄉ": 0, "天": 3} {
		m := seed()
		before := m.Store.Clone()
		u := m.Undoable(func() { m.Unlearn(w) })
		if got := len(m.Store.ZhuyinKeys); got != left {
			t.Errorf("移除 %q 應留 %d 筆：%v", w, left, m.Store.ZhuyinKeys)
		}
		if left == 3 {
			if u != nil {
				t.Errorf("%q 沒有改到，不該有復原", w)
			}
			continue
		}
		if !m.Restore(u) || !reflect.DeepEqual(m.Store, before) {
			t.Errorf("復原 %q：%v", w, m.Store.ZhuyinKeys)
		}
	}

	m := seed()
	before := m.Store.Clone()
	u := m.Undoable(func() { m.Forget("我ㄉ") })
	if m.ZhuyinKeyPicked("我", "ㄉ") || !m.ZhuyinKeyPicked("你", "ㄉ") {
		t.Fatalf("不再學：%v", m.Store.ZhuyinKeys)
	}
	m.LearnZhuyinKeys([]ZhuyinKeyCommit{{Left: "我", Symbol: "ㄉ", Action: ZhuyinKeyPick}})
	if m.ZhuyinKeyPicked("我", "ㄉ") {
		t.Fatal("不再學之後選了不記")
	}
	if !m.Restore(u) || !reflect.DeepEqual(m.Store, before) {
		t.Fatalf("復原不再學：%v %v", m.Store.ZhuyinKeys, m.Store.Tombstones)
	}
	m.Forget("我ㄉ")
	if !m.Relearn("我ㄉ") {
		t.Fatal("恢復學習")
	}
	m.LearnZhuyinKeys([]ZhuyinKeyCommit{{Left: "我", Symbol: "ㄉ", Action: ZhuyinKeyPick}})
	if !m.ZhuyinKeyPicked("我", "ㄉ") {
		t.Fatal("恢復學習後選了要記")
	}
}

func TestZhuyinKeysPrune(t *testing.T) {
	m := zhuyinKeyModel()
	m.LearnZhuyinKeys([]ZhuyinKeyCommit{{Left: "我", Symbol: "ㄉ", Action: ZhuyinKeyPick}})
	if st := m.Prune(); st.ZhuyinKeys != 0 || !m.ZhuyinKeyPicked("我", "ㄉ") {
		t.Fatalf("剛選的不清：%+v", st)
	}
	// 清理與判斷用同一個門檻：還算數的不會被清掉，清掉的本來就不算。
	for _, days := range []int64{100, 110, 116, 118, 125} {
		m := zhuyinKeyModel()
		m.LearnZhuyinKeys([]ZhuyinKeyCommit{{Left: "我", Symbol: "ㄉ", Action: ZhuyinKeyPick}})
		m.AdvanceTime(m.Store.Time + days*daySeconds)
		live := m.ZhuyinKeyPicked("我", "ㄉ")
		m.Prune()
		if _, kept := m.Store.ZhuyinKeys[pair("我", "ㄉ")]; kept != live {
			t.Fatalf("%d 天：判斷 %v，清理後留著 %v", days, live, kept)
		}
	}
	// 上限是保險：超過時先清最弱的。
	m = zhuyinKeyModel()
	m.LearnZhuyinKeys([]ZhuyinKeyCommit{{Left: "舊", Symbol: "ㄉ", Action: ZhuyinKeyPick}})
	m.AdvanceTime(m.Store.Time + 30*daySeconds)
	m.LearnZhuyinKeys([]ZhuyinKeyCommit{{Left: "新", Symbol: "ㄉ", Action: ZhuyinKeyPick}})
	policy := DefaultPrunePolicy()
	policy.MaxWord = 1
	if st := m.PruneWith(policy); st.ZhuyinKeys != 1 || !m.ZhuyinKeyPicked("新", "ㄉ") || m.ZhuyinKeyPicked("舊", "ㄉ") {
		t.Fatalf("上限：%+v %v", st, m.Store.ZhuyinKeys)
	}
}

// 格式 6 還沒發布，欄位併進去不再升版：舊檔沒有這份紀錄，讀進來是空的、可以寫；存檔來回一致。
func TestZhuyinKeysStoreFormat(t *testing.T) {
	if StoreFormat != 6 {
		t.Fatalf("這個欄位併進格式 6：%d", StoreFormat)
	}
	for _, src := range []string{`{"Format":5}`, `{"Format":6}`, `{"Format":6,"ZhuyinKeys":null}`, `{}`} {
		s, err := LoadStore(strings.NewReader(src))
		if err != nil {
			t.Fatal(err)
		}
		if s.ZhuyinKeys == nil || len(s.ZhuyinKeys) != 0 {
			t.Fatalf("%s：%v", src, s.ZhuyinKeys)
		}
	}
	m := zhuyinKeyModel()
	m.LearnZhuyinKeys([]ZhuyinKeyCommit{{Left: "我", Symbol: "ㄉ", Action: ZhuyinKeyPick}, {Symbol: "ㄅ", Action: ZhuyinKeyPick}})
	var buf bytes.Buffer
	if err := m.Store.Save(&buf); err != nil {
		t.Fatal(err)
	}
	back, err := LoadStore(bytes.NewReader(buf.Bytes()))
	if err != nil || !reflect.DeepEqual(back.ZhuyinKeys, m.Store.ZhuyinKeys) {
		t.Fatalf("存檔來回：%v %v", err, back.ZhuyinKeys)
	}
	c := m.Store.Clone()
	c.ZhuyinKeys["x\ty"] = UsageEvidence{}
	if _, ok := m.Store.ZhuyinKeys["x\ty"]; ok {
		t.Fatal("Clone 共用 ZhuyinKeys")
	}
}
