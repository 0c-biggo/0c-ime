package session

import (
	"slices"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/rerank"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// #208：中英混打開著時 ji32 wu0␣ 是「我2 天」。第一次照舊（第2 名、有1 個不受影響）；回頭在 2 上按 ↓ 選 ㄉ 時連後面的空白一起拿掉
// （我ㄉ天），並記住「前字＋按鍵＋空白」，之後 ji32␣ 直接出 ㄉ。選回原按鍵還原空白，也讓這筆習慣消失。

func zhuyinHabitModel() *personal.Model {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"我", "ㄨㄛˇ", 1e-2}, {"窩", "ㄨㄛˇ", 1e-4}, {"天", "ㄊㄧㄢ", 3e-3}, {"的", "ㄉㄜ˙", 2e-2}, {"有", "ㄧㄡˇ", 5e-3},
		{"真", "ㄓㄣ", 2e-3}, {"個", "ㄍㄜ˙", 3e-3}, {"第", "ㄉㄧˋ", 2e-3}, {"名", "ㄇㄧㄥˊ", 1e-3}, {"吃", "ㄔ", 2e-3}, {"兒", "ㄦ", 1e-3},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	return personal.NewModel(lex, sys)
}

func zhuyinHabitSession(m *personal.Model) *Session {
	s := New(m)
	s.MixedInput = true
	return s
}

func typeRaw(s *Session, text string) proto.Snapshot {
	var snap proto.Snapshot
	for _, r := range text {
		snap = key(s, string(r))
	}
	return snap
}

// downPick 把游標移到 at、按 ↓、選文字是 want 的候選。
func downPick(t *testing.T, s *Session, at int, want string) proto.Snapshot {
	t.Helper()
	s.Handle(proto.Request{Type: proto.Cursor, To: at})
	items := downItems(key(s, proto.KeyDown))
	id := slices.Index(items, want)
	if id < 0 {
		t.Fatalf("位置 %d 按 ↓ 沒有 %q：%q", at, want, items)
	}
	return s.Handle(proto.Request{Type: proto.Select, Index: id})
}

// 打一次、回頭選注音、提交：學到這一組。
func learnBackPick(t *testing.T, m *personal.Model, keys string, at int, symbol string) {
	t.Helper()
	s := zhuyinHabitSession(m)
	typeRaw(s, keys)
	downPick(t, s, at, symbol)
	key(s, proto.KeyEnter)
}

func typedText(m *personal.Model, keys string) string {
	return typeRaw(zhuyinHabitSession(m), keys).Text()
}

// 第一次打照舊：沒有選過的人，數字、標點接空白的結果完全相同。
func TestZhuyinHabitFirstTypeStaysLiteral(t *testing.T) {
	for keys, want := range map[string]string{
		"ji32 wu0 ": "我2 天", "ji31 ": "我1 ", "ji3, ": "我, ", "2 ": "2 ", "1 ": "1 ", "- ": "- ", ", ": ", ",
		"u.31 ek7": "有1 個", "2u42 ": "第2 ",
	} {
		m := zhuyinHabitModel()
		if got := typedText(m, keys); got != want {
			t.Errorf("%q 應為 %q，得到 %q", keys, want, got)
		}
		if len(m.Store.ZhuyinKeys) != 0 {
			t.Errorf("%q 沒選過不該有紀錄：%v", keys, m.Store.ZhuyinKeys)
		}
	}
}

// 回頭選注音：後面那個空白一併拿掉，後面的字連起來；選回原按鍵，空白也回來。
func TestZhuyinHabitBackPickDropsSpaceAndRestores(t *testing.T) {
	s := zhuyinHabitSession(zhuyinHabitModel())
	if snap := typeRaw(s, "ji32 wu0 "); snap.Text() != "我2 天" {
		t.Fatalf("第一次：%q", snap.Text())
	}
	if snap := downPick(t, s, 1, "ㄉ"); snap.Text() != "我ㄉ天" {
		t.Fatalf("選 ㄉ 應為 我ㄉ天：%q", snap.Text())
	}
	s.Handle(proto.Request{Type: proto.Cursor, To: 1})
	items := downItems(key(s, proto.KeyDown))
	if len(items) < 2 || items[0] != "ㄉ" || items[1] != "2 " {
		t.Fatalf("ㄉ 按 ↓ 應為 ㄉ、2␣…：%q", items)
	}
	if snap := s.Handle(proto.Request{Type: proto.Select, Index: 1}); snap.Text() != "我2 天" {
		t.Fatalf("選回 2 應還原空白：%q", snap.Text())
	}
	if snap := downPick(t, s, 1, "ㄉ"); snap.Text() != "我ㄉ天" {
		t.Fatalf("再選一次：%q", snap.Text())
	}
	if snap := key(s, proto.KeyEnter); snap.Commit != "我ㄉ天" {
		t.Fatalf("提交 %q", snap.Commit)
	}

	// 其他注音鍵比照：中文後面的 1 ,；句首的 1 2 - ,；接著打的字母鍵（a ㄇ）。
	for _, c := range []struct {
		keys   string
		at     int
		symbol string
		want   string
	}{
		{"ji31 wu0 ", 1, "ㄅ", "我ㄅ天"},
		{"ji3, wu0 ", 1, "ㄝ", "我ㄝ天"},
		{"ji3a ", 1, "ㄇ", "我ㄇ"},
		{"2 wu0 ", 0, "ㄉ", "ㄉ天"},
		{"1 ", 0, "ㄅ", "ㄅ"},
		{"- ", 0, "ㄦ", "ㄦ"},
		{", ", 0, "ㄝ", "ㄝ"},
	} {
		s := zhuyinHabitSession(zhuyinHabitModel())
		typeRaw(s, c.keys)
		if snap := downPick(t, s, c.at, c.symbol); snap.Text() != c.want {
			t.Errorf("%q 選 %s 應為 %q：%q", c.keys, c.symbol, c.want, snap.Text())
		}
	}

	// 要的是注音加空白時，再按一次空白就有；後面沒有空白的也不多吃字。
	s = zhuyinHabitSession(zhuyinHabitModel())
	typeRaw(s, "ji32  ")
	if snap := downPick(t, s, 1, "ㄉ"); snap.Text() != "我ㄉ " {
		t.Fatalf("只吃一個空白：%q", snap.Text())
	}
	s = zhuyinHabitSession(zhuyinHabitModel())
	typeRaw(s, "ji32")
	if snap := downPick(t, s, 1, "ㄉ"); snap.Text() != "我ㄉ" {
		t.Fatalf("後面沒有空白：%q", snap.Text())
	}

	// 前方模式：游標在 2 後面按 ↓。
	s = zhuyinHabitSession(zhuyinHabitModel())
	s.CandidateBeforeCursor = true
	typeRaw(s, "ji32 wu0 ")
	if snap := downPick(t, s, 2, "ㄉ"); snap.Text() != "我ㄉ天" {
		t.Fatalf("前方模式：%q", snap.Text())
	}
}

// 記住的範圍只有「前字＋按鍵＋空白」。
func TestZhuyinHabitNarrowScope(t *testing.T) {
	m := zhuyinHabitModel()
	learnBackPick(t, m, "ji32 wu0 ", 1, "ㄉ")
	if got := typedText(m, "ji32 "); got != "我ㄉ" {
		t.Fatalf("選過後 ji32␣ 應為 我ㄉ：%q", got)
	}
	if got := typedText(m, "ji32 wu0 "); got != "我ㄉ天" {
		t.Fatalf("選過後整句：%q", got)
	}
	if snap := key(zhuyinHabitSession(m), "2"); snap.Text() != "2" {
		t.Fatalf("句首的 2：%q", snap.Text())
	}
	base := zhuyinHabitModel()
	for _, keys := range []string{"ji31 ", "ji3 2 ", "u.32 ", "5p 2k72 ", "2 ", "ji32wu0 ", "ji3 ", "ji3a ", "j3 2 "} {
		want := typedText(base, keys)
		if got := typedText(m, keys); got != want {
			t.Errorf("%q 不受影響，應為 %q：%q", keys, want, got)
		}
	}

	// 句首另外選一次才記；不影響中文後面的別組。
	learnBackPick(t, m, "2 ", 0, "ㄉ")
	if got := typedText(m, "2 "); got != "ㄉ" {
		t.Fatalf("句首選過：%q", got)
	}
	if got := typedText(m, "ji3 2 "); got != "我 2 " {
		t.Fatalf("空白後面不是句首：%q", got)
	}
	if got := typedText(m, "u.32 "); got != "有2 " {
		t.Fatalf("別的前字：%q", got)
	}

	// 鍵值看前字，不看斷詞：真的2 選過，前字一樣是「的」就算。
	m = zhuyinHabitModel()
	learnBackPick(t, m, "2k72 ", 1, "ㄉ")
	if got := typedText(m, "5p 2k72 "); got != "真的ㄉ" {
		t.Fatalf("前字相同：%q", got)
	}
}

// 打的當下按 ↓ 選 ㄉ 再按空白也算選過；沒按空白（ji32↓ㄉ wu0）不是「按鍵＋空白」，不記。
func TestZhuyinHabitPendingPick(t *testing.T) {
	m := zhuyinHabitModel()
	s := zhuyinHabitSession(m)
	typeRaw(s, "ji32")
	key(s, proto.KeyDown)
	s.Handle(proto.Request{Type: proto.Select, Index: 1})
	if snap := key(s, " "); snap.Text() != "我ㄉ" {
		t.Fatalf("2↓ㄉ␣：%q", snap.Text())
	}
	key(s, proto.KeyEnter)
	if got := typedText(m, "ji32 "); got != "我ㄉ" {
		t.Fatalf("之後 ji32␣：%q", got)
	}

	m = zhuyinHabitModel()
	s = zhuyinHabitSession(m)
	typeRaw(s, "ji32")
	key(s, proto.KeyDown)
	s.Handle(proto.Request{Type: proto.Select, Index: 1})
	if snap := typeRaw(s, "wu0 "); snap.Text() != "我ㄉ天" {
		t.Fatalf("2↓ㄉ wu0：%q", snap.Text())
	}
	key(s, proto.KeyEnter)
	if got := typedText(m, "ji32 "); got != "我2 " {
		t.Fatalf("沒按空白不記：%q", got)
	}
	if len(m.Store.ZhuyinKeys) != 0 {
		t.Fatalf("紀錄：%v", m.Store.ZhuyinKeys)
	}
}

// 出中文的按鍵（-␣ 兒、t␣ 吃、5␣ 之）在 ↓ 選注音也算；↓ 第二項回得去原按鍵。
func TestZhuyinHabitPickOverChineseChar(t *testing.T) {
	m := zhuyinHabitModel()
	s := zhuyinHabitSession(m)
	if snap := typeRaw(s, "ji3- "); snap.Text() != "我兒" {
		t.Fatalf("我-␣：%q", snap.Text())
	}
	if snap := downPick(t, s, 1, "ㄦ"); snap.Text() != "我ㄦ" {
		t.Fatalf("選 ㄦ：%q", snap.Text())
	}
	key(s, proto.KeyEnter)
	if got := typedText(m, "ji3- "); got != "我ㄦ" {
		t.Fatalf("之後 我-␣：%q", got)
	}
	if got := typedText(m, "u.3- "); got != "有兒" {
		t.Fatalf("別的前字照舊：%q", got)
	}
	s = zhuyinHabitSession(m)
	typeRaw(s, "ji3- ")
	if snap := downPick(t, s, 1, "- "); snap.Text() != "我- " {
		t.Fatalf("選回原按鍵：%q", snap.Text())
	}
	key(s, proto.KeyEnter)
	if got := typedText(m, "ji3- "); got != "我兒" {
		t.Fatalf("反悔後回到兒：%q", got)
	}

	// 單打聲母組得成字的（t␣ 吃）比照；有打聲調的（t4 那類）不是「按鍵＋空白」。
	m = zhuyinHabitModel()
	s = zhuyinHabitSession(m)
	if snap := typeRaw(s, "ji3t "); snap.Text() != "我吃" {
		t.Fatalf("我t␣：%q", snap.Text())
	}
	downPick(t, s, 1, "ㄔ")
	key(s, proto.KeyEnter)
	if got := typedText(m, "ji3t "); got != "我ㄔ" {
		t.Fatalf("之後 我t␣：%q", got)
	}
	m = zhuyinHabitModel()
	s = zhuyinHabitSession(m)
	typeRaw(s, "ji3wu0 ")
	downPick(t, s, 1, "ㄊㄧㄢ")
	key(s, proto.KeyEnter)
	if len(m.Store.ZhuyinKeys) != 0 {
		t.Fatalf("整個音節的原樣注音不記：%v", m.Store.ZhuyinKeys)
	}
}

// 預設就是注音的（c␣ ㄏ）沒有選擇，不記；組字取消不記；改成別的聲調不記。
func TestZhuyinHabitOnlyFinalChoiceCounts(t *testing.T) {
	m := zhuyinHabitModel()
	s := zhuyinHabitSession(m)
	if snap := typeRaw(s, "ji3c "); snap.Text() != "我ㄏ" {
		t.Fatalf("我c␣：%q", snap.Text())
	}
	key(s, proto.KeyEnter)
	if len(m.Store.ZhuyinKeys) != 0 {
		t.Fatalf("c␣ 預設就是注音：%v", m.Store.ZhuyinKeys)
	}

	s = zhuyinHabitSession(m)
	typeRaw(s, "ji32 ")
	downPick(t, s, 1, "ㄉ")
	key(s, proto.KeyEscape)
	if len(m.Store.ZhuyinKeys) != 0 {
		t.Fatalf("取消不記：%v", m.Store.ZhuyinKeys)
	}

	s = zhuyinHabitSession(m)
	typeRaw(s, "ji32 ")
	downPick(t, s, 1, "ㄉ")
	downPick(t, s, 1, "2 ")
	key(s, proto.KeyEnter)
	if len(m.Store.ZhuyinKeys) != 0 {
		t.Fatalf("選回 2 才提交不記：%v", m.Store.ZhuyinKeys)
	}

	s = zhuyinHabitSession(m)
	typeRaw(s, "ji32 ")
	downPick(t, s, 1, "ㄉ")
	downPick(t, s, 1, "ㄉˊ")
	key(s, proto.KeyEnter)
	if len(m.Store.ZhuyinKeys) != 0 {
		t.Fatalf("換成別的聲調不是這個選擇：%v", m.Store.ZhuyinKeys)
	}

	// 前字改過：記的是提交時畫面上的前字。
	s = zhuyinHabitSession(m)
	typeRaw(s, "ji32 ")
	downPick(t, s, 1, "ㄉ")
	downPick(t, s, 0, "窩")
	key(s, proto.KeyEnter)
	if !m.ZhuyinKeyPicked("窩", "ㄉ") || m.ZhuyinKeyPicked("我", "ㄉ") {
		t.Fatalf("應記在窩，不是我：%v", m.Store.ZhuyinKeys)
	}
}

// 反悔：在 ㄉ 上選回 2 讓這筆習慣消失；↓ 再按 Enter 留在 ㄉ 不算反悔，也回得去。
func TestZhuyinHabitBackOut(t *testing.T) {
	m := zhuyinHabitModel()
	learnBackPick(t, m, "ji32 ", 1, "ㄉ")
	s := zhuyinHabitSession(m)
	typeRaw(s, "ji32 wu0 ")
	s.Handle(proto.Request{Type: proto.Cursor, To: 1})
	key(s, proto.KeyDown)
	if snap := s.Handle(proto.Request{Type: proto.Select, Index: 0}); snap.Text() != "我ㄉ天" {
		t.Fatalf("留在 ㄉ：%q", snap.Text())
	}
	if snap := downPick(t, s, 1, "2 "); snap.Text() != "我2 天" {
		t.Fatalf("選回 2：%q", snap.Text())
	}
	key(s, proto.KeyEnter)
	if len(m.Store.ZhuyinKeys) != 0 {
		t.Fatalf("反悔後紀錄應消失：%v", m.Store.ZhuyinKeys)
	}
	if got := typedText(m, "ji32 wu0 "); got != "我2 天" {
		t.Fatalf("反悔後重打：%q", got)
	}

	// 習慣還在時，打的當下按 ↓ 選 2 再按空白也是反悔。
	learnBackPick(t, m, "ji32 ", 1, "ㄉ")
	s = zhuyinHabitSession(m)
	typeRaw(s, "ji32")
	key(s, proto.KeyDown)
	s.Handle(proto.Request{Type: proto.Select, Index: 0})
	if snap := key(s, " "); snap.Text() != "我2 " {
		t.Fatalf("↓ 選 2 再空白：%q", snap.Text())
	}
	key(s, proto.KeyEnter)
	if got := typedText(m, "ji32 "); got != "我2 " {
		t.Fatalf("打的當下反悔：%q", got)
	}

	// 取消不算反悔；同一次組字裡有選、有反悔，以後面的為準。
	learnBackPick(t, m, "ji32 ", 1, "ㄉ")
	s = zhuyinHabitSession(m)
	typeRaw(s, "ji32 ")
	downPick(t, s, 1, "2 ")
	key(s, proto.KeyEscape)
	if got := typedText(m, "ji32 "); got != "我ㄉ" {
		t.Fatalf("取消不算：%q", got)
	}
	s = zhuyinHabitSession(m)
	typeRaw(s, "ji32 ji32 ") // 我ㄉ我ㄉ
	downPick(t, s, 3, "2 ")
	key(s, proto.KeyEnter)
	if got := typedText(m, "ji32 "); got != "我2 " {
		t.Fatalf("後面反悔：%q", got)
	}
}

// 私人欄位、暫停學習時不學也不套用。
func TestZhuyinHabitPrivateAndPaused(t *testing.T) {
	// 宿主在私人欄位的每個請求都帶 Private。
	privately := func(s *Session, keys string) proto.Snapshot {
		var snap proto.Snapshot
		for _, r := range keys {
			snap = s.Handle(proto.Request{Type: proto.Key, Key: string(r), Private: true})
		}
		return snap
	}
	for _, c := range []struct {
		name string
		typ  func(*Session, string) proto.Snapshot
	}{
		{"暫停", func(s *Session, keys string) proto.Snapshot {
			s.Handle(proto.Request{Type: proto.Pause, On: true})
			return typeRaw(s, keys)
		}},
		{"私人", privately},
	} {
		// 不學：選字照常，提交後沒有紀錄。
		m := zhuyinHabitModel()
		s := zhuyinHabitSession(m)
		c.typ(s, "ji32 ")
		s.Handle(proto.Request{Type: proto.Cursor, To: 1, Private: c.name == "私人"})
		if items := downItems(s.Handle(proto.Request{Type: proto.Key, Key: proto.KeyDown, Private: c.name == "私人"})); len(items) < 2 || items[1] != "ㄉ" {
			t.Fatalf("%s：↓ 照常有 ㄉ：%q", c.name, items)
		}
		if snap := s.Handle(proto.Request{Type: proto.Select, Index: 1, Private: c.name == "私人"}); snap.Text() != "我ㄉ" {
			t.Fatalf("%s：選字照常：%q", c.name, snap.Text())
		}
		s.Handle(proto.Request{Type: proto.Key, Key: proto.KeyEnter, Private: c.name == "私人"})
		if len(m.Store.ZhuyinKeys) != 0 {
			t.Fatalf("%s：不學：%v", c.name, m.Store.ZhuyinKeys)
		}
		// 不套用，也不因為保護狀態裡的字面按鍵而扣掉：那裡沒有看到習慣。
		learnBackPick(t, m, "ji32 ", 1, "ㄉ")
		s = zhuyinHabitSession(m)
		if snap := c.typ(s, "ji32 "); snap.Text() != "我2 " {
			t.Fatalf("%s：不套用：%q", c.name, snap.Text())
		}
		s.Handle(proto.Request{Type: proto.Key, Key: proto.KeyEnter, Private: c.name == "私人"})
		if !m.ZhuyinKeyPicked("我", "ㄉ") {
			t.Fatalf("%s：紀錄不動：%v", c.name, m.Store.ZhuyinKeys)
		}
	}
}

// 不再學、移除、恢復學習、復原，以及衰減後自然消失。
func TestZhuyinHabitForgetAndAge(t *testing.T) {
	m := zhuyinHabitModel()
	learnBackPick(t, m, "ji32 ", 1, "ㄉ")
	m.Forget("我ㄉ")
	if got := typedText(m, "ji32 "); got != "我2 " {
		t.Fatalf("不再學之後：%q", got)
	}
	learnBackPick(t, m, "ji32 ", 1, "ㄉ")
	if got := typedText(m, "ji32 "); got != "我2 " {
		t.Fatalf("不再學之後選了也不記：%q", got)
	}
	if !slices.Contains(m.Forgotten(), "我ㄉ") {
		t.Fatalf("不再學清單：%v", m.Forgotten())
	}
	m.Relearn("我ㄉ")
	learnBackPick(t, m, "ji32 ", 1, "ㄉ")
	if got := typedText(m, "ji32 "); got != "我ㄉ" {
		t.Fatalf("恢復學習後再選一次：%q", got)
	}

	u := m.Undoable(func() { m.Unlearn("我ㄉ") })
	if got := typedText(m, "ji32 "); got != "我2 " {
		t.Fatalf("移除之後：%q", got)
	}
	if !m.Restore(u) {
		t.Fatal("復原失敗")
	}
	if got := typedText(m, "ji32 "); got != "我ㄉ" {
		t.Fatalf("復原之後：%q", got)
	}

	// 照常用著不衰減；沒用的衰減到門檻以下就不套用，清理時拿掉。
	day := int64(24 * 60 * 60)
	m = zhuyinHabitModel()
	m.AdvanceTime(1000 * day)
	learnBackPick(t, m, "ji32 ", 1, "ㄉ")
	for range 8 {
		m.AdvanceTime(m.Store.Time + 30*day)
		s := zhuyinHabitSession(m)
		if snap := typeRaw(s, "ji32 "); snap.Text() != "我ㄉ" {
			t.Fatalf("照常用著：%q", snap.Text())
		}
		key(s, proto.KeyEnter)
	}
	m.AdvanceTime(m.Store.Time + 200*day)
	if got := typedText(m, "ji32 "); got != "我2 " {
		t.Fatalf("太久沒用：%q", got)
	}
	if st := m.Prune(); st.ZhuyinKeys != 1 || len(m.Store.ZhuyinKeys) != 0 {
		t.Fatalf("清理：%+v %v", st, m.Store.ZhuyinKeys)
	}
}

// 英文單字的 hello␣ 與數字的 2026␣ 不受影響；選回原按鍵的英文詞不在這裡記。
func TestZhuyinHabitLeavesOtherLiteralsAlone(t *testing.T) {
	m := zhuyinHabitModel()
	learnBackPick(t, m, "ji32 ", 1, "ㄉ")
	base := zhuyinHabitModel()
	for _, keys := range []string{"ji32026 ", "ji325 ", "ji32. ", "ji312 "} {
		want := typedText(base, keys)
		if got := typedText(m, keys); got != want {
			t.Errorf("%q 應為 %q：%q", keys, want, got)
		}
	}
	if len(m.Store.ZhuyinKeys) != 1 {
		t.Fatalf("紀錄只有一筆：%v", m.Store.ZhuyinKeys)
	}
}

// 沒有紀錄的個人資料：判斷與提交掃描都不配置記憶體。
func TestZhuyinHabitNoCostWithoutRecords(t *testing.T) {
	s := zhuyinHabitSession(zhuyinHabitModel())
	typeRaw(s, "ji32")
	if n := testing.AllocsPerRun(100, func() {
		if s.zhuyinKeyHabit("2") {
			t.Fatal("沒有紀錄")
		}
	}); n != 0 {
		t.Fatalf("空白個人資料判斷配置 %v 次", n)
	}
	typeRaw(s, " wu0 ")
	if n := testing.AllocsPerRun(100, func() {
		if s.zhuyinKeyCommits() != nil {
			t.Fatal("沒有事件")
		}
	}); n != 0 {
		t.Fatalf("空白個人資料提交掃描配置 %v 次", n)
	}
}

// 神經選字把前字換掉時，記住與套用看的都是畫面上的字，選注音也不改前面的字。
func TestZhuyinHabitFollowsNeuralDisplay(t *testing.T) {
	neural := func(m *personal.Model) *Session {
		s := zhuyinHabitSession(m)
		s.Opt.PrefixAware = false
		s.Rerank = rerank.Options{Weight: 1, Scorer: rankFunc(func(r rerank.Request) ([]float64, error) {
			scores := make([]float64, len(r.Candidates))
			for i, c := range r.Candidates {
				if strings.Contains(c.Text, "窩") {
					scores[i] = 1000
				}
			}
			return scores, nil
		})}
		return s
	}
	// 只有一個字的段落不評分，前面多打一個字。
	m := zhuyinHabitModel()
	s := neural(m)
	if snap := typeRaw(s, "5p ji32 "); snap.Text() != "真窩2 " {
		t.Fatalf("神經把我換成窩：%q", snap.Text())
	}
	if snap := downPick(t, s, 2, "ㄉ"); snap.Text() != "真窩ㄉ" {
		t.Fatalf("選 ㄉ 不改前面的字：%q", snap.Text())
	}
	if snap := downPick(t, s, 2, "2 "); snap.Text() != "真窩2 " {
		t.Fatalf("選回 2 不改前面的字：%q", snap.Text())
	}
	if snap := downPick(t, s, 2, "ㄉ"); snap.Text() != "真窩ㄉ" {
		t.Fatalf("再選 ㄉ：%q", snap.Text())
	}
	key(s, proto.KeyEnter)
	if !m.ZhuyinKeyPicked("窩", "ㄉ") || m.ZhuyinKeyPicked("我", "ㄉ") {
		t.Fatalf("記的是畫面上的字：%v", m.Store.ZhuyinKeys)
	}
	if snap := typeRaw(neural(m), "5p ji32 "); snap.Text() != "真窩ㄉ" {
		t.Fatalf("套用看畫面上的字：%q", snap.Text())
	}
	m = zhuyinHabitModel()
	m.LearnZhuyinKeys([]personal.ZhuyinKeyCommit{{Left: "窩", Symbol: "ㄉ", Action: personal.ZhuyinKeyPick}})
	if got := typedText(m, "5p ji32 "); got != "真我2 " {
		t.Fatalf("畫面上是我，窩的紀錄不套用：%q", got)
	}
}
