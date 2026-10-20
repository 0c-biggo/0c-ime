package behavior

import (
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
)

// replay 逐音節打字，回傳每一步的顯示、自第幾個音節起一路正確、已顯示的字被回改幾次。
func replay(u *user, want, reading string) (shown []string, stableFrom, rewrites int) {
	prev := []rune{}
	for _, syl := range strings.Fields(reading) {
		cur := []rune(u.typeSyl(syl).Text())
		for i := 0; i < len(prev) && i < len(cur); i++ {
			if prev[i] != cur[i] {
				rewrites++
			}
		}
		shown = append(shown, string(cur))
		prev = cur
	}
	u.do(proto.Request{Type: proto.Cancel})
	stableFrom = len(shown) + 1
	for k := len(shown) - 1; k >= 0 && strings.HasPrefix(want, shown[k]); k-- {
		stableFrom = k + 1
	}
	return
}

// 前綴感知解碼。
func TestPrefixAware(t *testing.T) {
	cases := []struct{ want, reading string }{
		{"麻婆豆腐", MPDF},
		{"麻煩", "ㄇㄚˊ ㄈㄢˊ"},
		{"我想吃麻婆豆腐", WXC + " " + MPDF},
		{"風景", "ㄈㄥ " + J},
		{"我想看風景", "ㄨㄛˇ ㄒㄧㄤˇ ㄎㄢˋ ㄈㄥ " + J},
	}
	total := map[bool]int{}
	for _, c := range cases {
		var stable, rw [2]int
		for k, aware := range []bool{false, true} {
			u := newUser(t, personal.DefaultParams())
			u.e.Session(u.id).Opt.PrefixAware = aware
			shown, s, r := replay(u, c.want, c.reading)
			stable[k], rw[k] = s, r
			total[aware] += r
			t.Logf("%s 前綴感知=%v: %s  自第 %d 鍵起正確，回改 %d 字", c.want, aware, strings.Join(shown, " → "), s, r)
			if shown[len(shown)-1] != c.want {
				t.Errorf("%s：最終結果 %q", c.want, shown[len(shown)-1])
			}
		}
		// 前綴感知不得比只認完整詞差
		if stable[1] > stable[0] || rw[1] > rw[0] {
			t.Errorf("%s：前綴感知較差（穩定 %d→%d，回改 %d→%d）", c.want, stable[0], stable[1], rw[0], rw[1])
		}
	}
	// 使用者回報的案例：打到第二個音就必須是「麻婆」
	u := newUser(t, personal.DefaultParams())
	if _, s, _ := replay(u, "麻婆豆腐", MPDF); s != 2 {
		t.Errorf("麻婆豆腐應自第 2 鍵起正確，實際第 %d 鍵", s)
	}
	if total[true] >= total[false] {
		t.Errorf("回改總字數未下降：%d → %d", total[false], total[true])
	}
	// 顯示什麼就提交什麼
	u.typeAll("ㄇㄚˊ ㄆㄛˊ")
	if got := u.do(proto.Request{Type: proto.Commit}).Commit; got != "麻婆" {
		t.Errorf("只打兩個音即提交應得「麻婆」，實際 %q", got)
	}
}

// walk 逐音節打字，一有正確的建議就按 Tab。回傳總按鍵數與過程。
func walk(u *user, want, reading string) (keys int, log []string) {
	start := u.keys
	syls := strings.Fields(reading)
	for k := 0; k < len(syls); {
		snap := u.typeSyl(syls[k])
		k++
		if proposed := snap.SuggestedText(); proposed != "" && strings.HasPrefix(want, proposed) {
			extra := len([]rune(proposed)) - len([]rune(snap.Text()))
			log = append(log, snap.Text()+"｜"+snap.Suggestion)
			snap = u.do(proto.Request{Type: proto.Key, Key: proto.KeyTab})
			u.keys++
			k += extra
		}
		log = append(log, snap.Text())
	}
	if got := u.do(proto.Request{Type: proto.Commit}).Commit; got != want {
		u.tb.Errorf("walk(%s) 提交 %q", want, got)
	}
	return u.keys - start, log
}

// 使用者回報：用過一次「麻婆豆腐」之後，只打「麻」就建議「婆豆腐」——麻後面還有很多字。
// 解碼可以選過一次就記住，但主動建議要在同一個前綴的詞群裡累積足夠的觀察。
func TestOneUseDoesNotTriggerSuggestion(t *testing.T) {
	u := newUser(t, personal.DefaultParams())
	suggested := 0
	for n := 1; n <= 8 && suggested == 0; n++ {
		walk(u, "麻婆豆腐", MPDF)
		if u.typeAll("ㄇㄚˊ").Suggestion != "" {
			suggested = n
		}
		u.do(proto.Request{Type: proto.Cancel})
	}
	t.Logf("在句首用過 %d 次「麻婆豆腐」後，單打 ㄇㄚˊ 才開始建議", suggested)
	if suggested == 1 { // 次數取決於操作來源、上下文、詞群競爭與時間，不是固定 4 次
		t.Error("只用過一次就建議，太衝動")
	}
	if suggested == 0 {
		t.Error("用過 8 次仍不建議：常用的詞最終應該要能補全")
	}
	// 系統本來就有把握的建議不受影響
	if s := u.typeAll("ㄇㄚˊ ㄆㄛˊ"); s.Suggestion != "豆腐" {
		t.Errorf("打到 麻婆 應建議「豆腐」，實際 %q", s.Suggestion)
	}
}

// Tab 補全。
func TestTabCompletion(t *testing.T) {
	u := newUser(t, personal.DefaultParams())
	for _, c := range []struct {
		want, reading string
		keys          int
	}{
		{"麻婆豆腐", MPDF, 7},                 // 12 → 7
		{"我想吃麻婆豆腐", WXC + " " + MPDF, 16}, // 21 → 16
		{"麻煩", "ㄇㄚˊ ㄈㄢˊ", 6},              // 無建議
		{"風景", "ㄈㄥ " + J, 7},              // 無建議
	} {
		u := newUser(t, personal.DefaultParams())
		keys, log := walk(u, c.want, c.reading)
		t.Logf("%s: %s  按鍵 %d", c.want, strings.Join(log, " → "), keys)
		if keys != c.keys {
			t.Errorf("%s：按鍵 %d 應為 %d", c.want, keys, c.keys)
		}
	}

	// 個人片語的補全：系統詞庫沒有「親子丼」
	if s := u.typeAll(QZ); s.Suggestion != "" {
		t.Errorf("學習前不應有建議，實際 %q", s.Suggestion)
	}
	u.do(proto.Request{Type: proto.Cancel})
	learnedAt := 0
	// 第一次修正後其餘皆為被動提交，現在權重較低；不能再沿用 raw Uses 的 8 次上限。
	for n := 1; n <= 24 && learnedAt == 0; n++ {
		u.typeAndFix(QZJ, "親子丼")
		if u.typeAll(QZ).Suggestion == "丼" {
			learnedAt = n
		}
		u.do(proto.Request{Type: proto.Cancel})
	}
	if learnedAt == 0 {
		t.Fatal("一次修正後持續被動使用 24 次仍未建議個人片語")
	}
	t.Logf("打過 %d 次後，打 ㄑㄧㄣ ㄗˇ 即建議「丼」", learnedAt)
	// 用得夠多之後，打到「親」就可能直接建議「子丼」，比 7 鍵更少
	if keys, log := walk(u, "親子丼", QZJ); keys > 7 {
		t.Errorf("親子丼：按鍵 %d 應不多於 7（%v）", keys, log)
	}

	// 不理會建議時行為不變
	a, b := newUser(t, personal.DefaultParams()), newUser(t, personal.DefaultParams())
	b.e.Session(b.id).Opt.Suggest.MinConf = 2 // 永不建議
	for _, syl := range strings.Fields(MPDF) {
		if x, y := a.typeSyl(syl).Text(), b.typeSyl(syl).Text(); x != y {
			t.Errorf("有無補全功能的顯示不同：%q vs %q", x, y)
		}
	}
	// 低信心時不建議
	c := newUser(t, personal.DefaultParams())
	typed := c.typeAll("ㄇㄚˊ")
	if typed.Suggestion != "" {
		t.Errorf("單打 ㄇㄚˊ 不應建議，實際 %q", typed.Suggestion)
	}
	// 沒有建議時組字中的 Tab 吃掉不動作，不交給應用程式（#106）
	if s := c.do(proto.Request{Type: proto.Key, Key: proto.KeyTab}); !s.Handled || s.Commit != "" || s.Text() != typed.Text() {
		t.Errorf("沒有建議時 Tab 應吃掉且組字不變：%+v", s)
	}
}

func styles(s proto.Snapshot) string {
	var out []string
	for _, g := range s.Preedit {
		out = append(out, g.Text+":"+g.Style)
	}
	return strings.Join(out, " ")
}

// 高頻錯誤的自動修正。
func TestAutoCorrect(t *testing.T) {
	u := newUser(t, personal.DefaultParams())
	snap := u.typeAll(YG)
	if snap.Text() != "應該" || styles(snap) != "應該:corrected" {
		t.Errorf("ㄧㄣ ㄍㄞ 應得「應該」並標示為修正，實際 %s", styles(snap))
	}
	u.do(proto.Request{Type: proto.Cancel})
	if shown, _, _ := replay(u, "應該", YG); strings.Join(shown, "→") != "因→應該" {
		t.Errorf("逐鍵過程應為 因→應該，實際 %v", shown)
	}
	if got := u.show("ㄨㄛˇ " + YG + " ㄔ"); got != "我應該吃" {
		t.Errorf("句中：%q", got)
	}
	// 精確輸入不受影響，且不被標示為修正
	for reading, want := range map[string]string{"ㄧㄥ ㄍㄞ": "應該", "ㄧㄣ ㄨㄟˋ": "因為", "ㄩㄢˊ ㄧㄣ": "原因", "ㄧㄣ": "因"} {
		snap := u.typeAll(reading)
		if snap.Text() != want || strings.Contains(styles(snap), "corrected") {
			t.Errorf("%s → %s", reading, styles(snap))
		}
		u.do(proto.Request{Type: proto.Cancel})
	}

	// 故意要打「因該」：對單一字選字即可；候選列出使用者實際打的讀音的字
	u = newUser(t, personal.DefaultParams())
	u.typeAll(YG)
	if c := texts(u.singles(0)); !contains(c, "因") || contains(c, "應") {
		t.Errorf("位置 0 的候選應是 ㄧㄣ 的字，實際 %s", c)
	}
	u.do(proto.Request{Type: proto.Cancel})
	if before, n := u.typeAndFix(YG, "因該"); before != "應該" || n != 1 {
		t.Errorf("修正前 %q，手動 %d 次", before, n)
	}
	if got := u.show(YG); got != "應該" {
		t.Errorf("拒絕一次後修正應仍有效（一次可能是偶然），實際 %q", got)
	}
	u.typeAndFix(YG, "因該")
	if got := u.show(YG); got != "因該" {
		t.Errorf("拒絕兩次後應不再自動修正，實際 %q", got)
	}
	if u.show("ㄧㄥ ㄍㄞ") != "應該" || u.show("ㄧㄣ ㄨㄟˋ") != "因為" {
		t.Error("停用應只針對這一項")
	}

	// 手選過的字不因後續輸入而被修正蓋掉
	u = newUser(t, personal.DefaultParams())
	u.fix(u.typeAll(YG), "因該")
	u.do(proto.Request{Type: proto.Cursor, To: 2})
	if snap := u.typeSyl("ㄔ"); snap.Text() != "因該吃" || !strings.HasPrefix(styles(snap), "因:locked") {
		t.Errorf("鎖定後繼續打字：%s", styles(snap))
	}
}
