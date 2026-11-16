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

// #296：句首選過一次 ㄓ（在「之」的清單裡選原樣注音）之後，句首的 5␣ 照紀錄出 ㄓ，
// 「之前」打成「ㄓ前」；在 ㄓ 上按 ↓ 只有 ㄓ、原按鍵與各聲調，回不到「之」。原樣音節自己有字時，
// ↓ 在各聲調後面接著列這個讀音的字；選了換回一般的字，也算反悔，選過的注音文紀錄消失。
// 句首後來改成不記也不套用（TestZhuyinHabitStartKeepsChars），中文後面（我ㄓ前）照舊是這條路。
func zhuyinCharsModel() *personal.Model {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"之", "ㄓ", 4e-3}, {"支", "ㄓ", 1e-3}, {"知", "ㄓ", 8e-4}, {"前", "ㄑㄧㄢˊ", 2e-3}, {"之前", "ㄓ ㄑㄧㄢˊ", 1e-3},
		{"是", "ㄓˋ", 1e-2}, {"至", "ㄓˋ", 1e-3}, {"吃", "ㄔ", 2e-3}, {"我", "ㄨㄛˇ", 1e-2}, {"窩", "ㄨㄛˇ", 1e-4}, {"天", "ㄊㄧㄢ", 3e-3},
		{"真", "ㄓㄣ", 2e-3},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	return personal.NewModel(lex, sys)
}

func TestZhuyinHabitSyllableBackToChar(t *testing.T) {
	m := zhuyinCharsModel()
	if got := typedText(m, "ji35 fu06"); got != "我之前" {
		t.Fatalf("沒有紀錄時 我5␣fu06：%q", got)
	}
	// 在「之」的清單選最後的原樣注音 ㄓ：記下「我」後面的 ㄓ。
	learnBackPick(t, m, "ji35 ", 1, "ㄓ")
	if !m.ZhuyinKeyPicked("我", "ㄓ") {
		t.Fatalf("應記下 我ㄓ：%v", m.Store.ZhuyinKeys)
	}

	s := zhuyinHabitSession(m)
	if snap := typeRaw(s, "ji35 fu06"); snap.Text() != "我ㄓ前" {
		t.Fatalf("紀錄套用（客戶看到的畫面）：%q", snap.Text())
	}
	s.Handle(proto.Request{Type: proto.Cursor, To: 1})
	items := downItems(key(s, proto.KeyDown))
	if want := []string{"ㄓ", "5 ", "ㄓˊ", "ㄓˇ", "ㄓˋ", "ㄓ˙", "之", "支", "知"}; !slices.Equal(items, want) {
		t.Fatalf("ㄓ 的 ↓ 應接著列出字：%q，要 %q", items, want)
	}
	snap := s.Handle(proto.Request{Type: proto.Select, Index: slices.Index(items, "之")})
	if snap.Text() != "我之前" {
		t.Fatalf("選回之：%q", snap.Text())
	}
	// 換回來的字跟平常 5␣ 出的字一樣：↓ 的清單（之前、原按鍵、之、5␣…）與沒有紀錄時相同。
	plain := zhuyinHabitSession(zhuyinCharsModel())
	typeRaw(plain, "ji35 fu06")
	plain.Handle(proto.Request{Type: proto.Cursor, To: 1})
	want := downItems(key(plain, proto.KeyDown))
	s.Handle(proto.Request{Type: proto.Cursor, To: 1})
	if items := downItems(key(s, proto.KeyDown)); !slices.Equal(items, want) || !slices.Contains(items, "5 ") {
		t.Fatalf("之 的 ↓：%q，沒有紀錄時 %q", items, want)
	}
	key(s, proto.KeyEscape)
	key(s, proto.KeyEnter)
	if m.ZhuyinKeyPicked("我", "ㄓ") {
		t.Fatalf("選回字是反悔，紀錄應消失：%v", m.Store.ZhuyinKeys)
	}
	if got := typedText(m, "ji35 fu06"); got != "我之前" {
		t.Fatalf("反悔後重打：%q", got)
	}

	// 選別的字照樣鎖定那個字。
	learnBackPick(t, m, "ji35 ", 1, "ㄓ")
	s = zhuyinHabitSession(m)
	typeRaw(s, "ji35 ")
	if snap := downPick(t, s, 1, "支"); snap.Text() != "我支" {
		t.Fatalf("選支：%q", snap.Text())
	}

	// 中文後面的 t␣（吃）同一條路。
	m = zhuyinCharsModel()
	learnBackPick(t, m, "ji3t ", 1, "ㄔ")
	s = zhuyinHabitSession(m)
	if snap := typeRaw(s, "ji3t "); snap.Text() != "我ㄔ" {
		t.Fatalf("紀錄套用：%q", snap.Text())
	}
	if snap := downPick(t, s, 1, "吃"); snap.Text() != "我吃" {
		t.Fatalf("選回吃：%q", snap.Text())
	}
	key(s, proto.KeyEnter)
	if got := typedText(m, "ji3t "); got != "我吃" {
		t.Fatalf("反悔後重打：%q", got)
	}
}

// 從原樣音節的清單換了有字的聲調（ㄓ → ㄓˋ）也一樣列字；自己沒有字的原樣注音（c␣ 的 ㄏ）清單不變。
func TestSyllableLiteralCharsOnlyWhenReadable(t *testing.T) {
	m := zhuyinCharsModel()
	learnBackPick(t, m, "ji35 ", 1, "ㄓ")
	s := zhuyinHabitSession(m)
	typeRaw(s, "ji35 ")
	if snap := downPick(t, s, 1, "ㄓˋ"); snap.Text() != "我ㄓˋ" {
		t.Fatalf("換聲調：%q", snap.Text())
	}
	s.Handle(proto.Request{Type: proto.Cursor, To: 1})
	items := downItems(key(s, proto.KeyDown))
	if !slices.Contains(items, "是") || !slices.Contains(items, "至") || slices.Contains(items, "之") {
		t.Fatalf("ㄓˋ 的 ↓：%q", items)
	}
	if snap := s.Handle(proto.Request{Type: proto.Select, Index: slices.Index(items, "是")}); snap.Text() != "我是" {
		t.Fatalf("選是：%q", snap.Text())
	}

	s = zhuyinHabitSession(m)
	typeRaw(s, "ji3c ")
	s.Handle(proto.Request{Type: proto.Cursor, To: 1})
	if items := downItems(key(s, proto.KeyDown)); !slices.Equal(items, []string{"ㄏ", "c ", "ㄏˊ", "ㄏˇ", "ㄏˋ", "ㄏ˙"}) {
		t.Fatalf("ㄏ 的 ↓ 不變：%q", items)
	}
}

// 神經選字把前字換成「窩」時，在選過的 ㄔ 上換回「吃」不把前面的字換回統計首選；反悔記在畫面上的前字。
func TestZhuyinHabitSyllableBackToCharKeepsNeuralDisplay(t *testing.T) {
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
	m := zhuyinCharsModel()
	if snap := typeRaw(neural(m), "5p ji3t "); snap.Text() != "真窩吃" {
		t.Fatalf("神經把我換成窩：%q", snap.Text())
	}
	m.LearnZhuyinKeys([]personal.ZhuyinKeyCommit{{Left: "窩", Symbol: "ㄔ", Action: personal.ZhuyinKeyPick}})
	s := neural(m)
	if snap := typeRaw(s, "5p ji3t "); snap.Text() != "真窩ㄔ" {
		t.Fatalf("紀錄套用：%q", snap.Text())
	}
	if snap := downPick(t, s, 2, "吃"); snap.Text() != "真窩吃" {
		t.Fatalf("換回吃不改前面的字：%q", snap.Text())
	}
	key(s, proto.KeyEnter)
	if m.ZhuyinKeyPicked("窩", "ㄔ") {
		t.Fatalf("反悔：%v", m.Store.ZhuyinKeys)
	}
}

// #296 (b)：鍵接空白本來就出中文字的（5␣ 之、t␣ 吃），在句首選了原樣注音不記成習慣；句首的舊紀錄也不再套用，
// 已經受影響的人更新後 5␣ 直接回到「之」。中文後面照記照用；句首本來就照字面的（2␣、-␣）照舊記。
func TestZhuyinHabitStartKeepsChars(t *testing.T) {
	m := zhuyinCharsModel()
	learnBackPick(t, m, "5 ", 0, "ㄓ")
	if m.ZhuyinKeyPicked("", "ㄓ") {
		t.Errorf("句首之 ↓ ㄓ 不記：%v", m.Store.ZhuyinKeys)
	}
	if got := typedText(m, "5 fu06"); got != "之前" {
		t.Errorf("句首 5␣fu06：%q", got)
	}

	// 句首 t␣ 吃 ↓ ㄔ 不記。
	learnBackPick(t, m, "t ", 0, "ㄔ")
	if got := typedText(m, "t "); m.ZhuyinKeyPicked("", "ㄔ") || got != "吃" {
		t.Errorf("句首吃 ↓ ㄔ 不記：%q %v", got, m.Store.ZhuyinKeys)
	}

	// 中文後面照記照用。
	if got := typedText(m, "ji35 fu06"); got != "我之前" {
		t.Fatalf("沒有紀錄時 我5␣fu06：%q", got)
	}
	learnBackPick(t, m, "ji35 ", 1, "ㄓ")
	if !m.ZhuyinKeyPicked("我", "ㄓ") {
		t.Fatalf("我之 ↓ ㄓ 照記：%v", m.Store.ZhuyinKeys)
	}
	if got := typedText(m, "ji35 fu06"); got != "我ㄓ前" {
		t.Errorf("我後面照用：%q", got)
	}
	if got := typedText(m, "5 fu06"); got != "之前" {
		t.Errorf("我ㄓ 不影響句首：%q", got)
	}

	// 舊版留下的句首紀錄（user.json 原樣）不套用。
	old := zhuyinCharsModel()
	old.LearnZhuyinKeys([]personal.ZhuyinKeyCommit{
		{Left: "", Symbol: "ㄓ", Action: personal.ZhuyinKeyPick},
		{Left: "", Symbol: "ㄔ", Action: personal.ZhuyinKeyPick},
	})
	for keys, want := range map[string]string{"5 fu06": "之前", "5 ": "之", "t ": "吃", "ji35 ": "我之"} {
		if got := typedText(old, keys); got != want {
			t.Errorf("舊的句首紀錄 %q：%q，要 %q", keys, got, want)
		}
	}

	// 句首本來就照字面的鍵照舊：2␣ 選 ㄉ、-␣ 選 ㄦ 都記，之後句首直接出注音。
	m = zhuyinHabitModel()
	for _, c := range []struct{ keys, symbol string }{{"2 ", "ㄉ"}, {"- ", "ㄦ"}} {
		if got := typedText(m, c.keys); got != c.keys {
			t.Fatalf("句首 %q 照字面：%q", c.keys, got)
		}
		learnBackPick(t, m, c.keys, 0, c.symbol)
		if !m.ZhuyinKeyPicked("", c.symbol) {
			t.Errorf("句首 %q 選 %s 照記：%v", c.keys, c.symbol, m.Store.ZhuyinKeys)
		}
		if got := typedText(m, c.keys); got != c.symbol {
			t.Errorf("句首 %q 照用：%q", c.keys, got)
		}
	}
}
