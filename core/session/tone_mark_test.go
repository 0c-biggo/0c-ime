package session

import (
	"slices"
	"strings"
	"testing"

	"git.biggo.com/0c/ime/core/proto"
)

// #135：沒開中英混打時，注音暫存是空的、單按聲調鍵再按空白，輸出聲調符號（同新酷音：
// libchewing 的注音暫存可以只有聲調，空白完成後查到詞庫裡只有聲調的詞 ˇ ˊ ˋ ˙）。
// 按下聲調鍵時先照舊顯示數字；只有緊接著的空白把它換成聲調符號，其他按鍵照舊是數字。

func toneMarkSession() *Session {
	return syllableLiteralSession(word{"個", "ㄍㄜˋ", 2e-3})
}

func TestLoneToneKeyPlusSpaceTypesToneMark(t *testing.T) {
	for k, want := range map[string]string{"3": "ˇ", "4": "ˋ", "6": "ˊ", "7": "˙"} {
		s := toneMarkSession()
		snap := key(s, k)
		if !snap.Handled || snap.Text() != k || snap.Commit != "" {
			t.Fatalf("%s：沒在組字時聲調鍵先以數字留在組字區，等下一鍵決定：%+v", k, snap)
		}
		if snap = key(s, " "); !snap.Handled || snap.Text() != want || snap.Candidates != nil || snap.Cursor.Rune != 1 {
			t.Fatalf("%s＋空白應為 %s：%+v", k, want, snap)
		}
		if snap = key(s, proto.KeyEnter); snap.Commit != want {
			t.Fatalf("%s＋空白提交 %q", k, snap.Commit)
		}
		assertNoZhuyinLearned(t, s, want)
	}

	// 組字中也一樣：前一個字是中文、注音暫存是空的。
	s := toneMarkSession()
	typeSyl(s, "ㄨㄛˇ")
	if snap := keys(s, "3", " "); snap.Text() != "我ˇ" || snap.Candidates != nil {
		t.Fatalf("組字中聲調鍵＋空白：%+v", snap)
	}
	// 接著再打一次也是聲調符號；接著打的注音照常組字。
	if snap := keys(s, "4", " "); snap.Text() != "我ˇˋ" {
		t.Fatalf("連續兩次：%q", snap.Text())
	}
	if snap := typeSyl(s, "ㄍㄜˋ"); snap.Text() != "我ˇˋ個" {
		t.Fatalf("聲調符號後接注音：%q", snap.Text())
	}

	// 關閉「空白鍵選字」時空白照樣完成聲調鍵（同新酷音：空白先交給注音暫存當一聲）。
	s = toneMarkSession()
	s.SpaceInsertsSpace = true
	if snap := keys(s, "3", " "); snap.Text() != "ˇ" {
		t.Fatalf("空白插入空白的設定下：%q", snap.Text())
	}
}

func TestToneKeyStaysDigitUnlessSpaceFollows(t *testing.T) {
	// 3 接 Enter：照舊是數字。
	s := toneMarkSession()
	if snap := keys(s, "3", proto.KeyEnter); snap.Commit != "3" || snap.Text() != "" {
		t.Fatalf("3＋Enter 應送出數字：%+v", snap)
	}
	// 3 接注音：數字留著，注音從新的音節開始。
	s = toneMarkSession()
	key(s, "3")
	if snap := typeSyl(s, "ㄍㄜˋ"); snap.Text() != "3個" {
		t.Fatalf("3 後接注音：%q", snap.Text())
	}
	if snap := key(s, proto.KeyEnter); snap.Commit != "3個" {
		t.Fatalf("提交 %q", snap.Commit)
	}
	// 3 接退格：刪掉數字，回到沒有組字。
	s = toneMarkSession()
	key(s, "3")
	if snap := key(s, proto.KeyBackspace); !snap.Handled || snap.Text() != "" || s.Composing() {
		t.Fatalf("3＋退格：%+v", snap)
	}
	// 中間隔了別的操作，空白就不再把數字換成聲調符號。
	s = toneMarkSession()
	typeSyl(s, "ㄨㄛˇ")
	keys(s, "3", proto.KeyLeft, proto.KeyRight)
	if snap := key(s, " "); strings.ContainsAny(snap.Text(), "ˇ") {
		t.Fatalf("隔了游標移動後空白不換聲調符號：%q", snap.Text())
	}
}

func TestToneKeyRunStaysNumber(t *testing.T) {
	// 多個聲調鍵連打是數字（新酷音會把 34 打成 ˋ，這裡保留數字）：接空白是數字加空白。
	for _, run := range []string{"34", "67", "3467", "33"} {
		s := toneMarkSession()
		for _, k := range run {
			key(s, string(k))
		}
		snap := key(s, " ")
		if !snap.Handled || snap.Text() != run+" " || snap.Candidates != nil {
			t.Fatalf("%s＋空白應為數字加空白：%+v", run, snap)
		}
		if snap = key(s, proto.KeyEnter); snap.Commit != run+" " {
			t.Fatalf("%s＋空白提交 %q", run, snap.Commit)
		}
	}
	// 組字中前面緊接著數字（數字鍵盤打的也算）的聲調鍵也是數字的一部分。
	s := toneMarkSession()
	typeSyl(s, "ㄨㄛˇ")
	if snap := keys(s, "KP1", "3", " "); snap.Text() != "我13 " {
		t.Fatalf("數字後的聲調鍵：%q", snap.Text())
	}
}

func TestToneMarkDownOffersOriginalKeySecond(t *testing.T) {
	s := toneMarkSession()
	keys(s, "3", " ")
	snap := key(s, proto.KeyDown)
	if snap.Candidates == nil || len(snap.Candidates.Items) < 2 {
		t.Fatalf("聲調符號上按 ↓ 應開候選：%+v", snap)
	}
	if got := snap.Candidates.Items[0].Text + " " + snap.Candidates.Items[1].Text; got != "ˇ 3" {
		t.Fatalf("第一個是聲調符號、第二個是原本的數字：%q", got)
	}
	snap = s.Handle(proto.Request{Type: proto.Select, Index: 1})
	if snap.Text() != "3" || snap.Candidates != nil {
		t.Fatalf("選回數字：%+v", snap)
	}
	if snap = key(s, proto.KeyEnter); snap.Commit != "3" {
		t.Fatalf("提交 %q", snap.Commit)
	}

	// 句中的聲調符號：後面又打了字、游標移回來，原按鍵仍跟著它。
	s = toneMarkSession()
	typeSyl(s, "ㄨㄛˇ")
	keys(s, "3", " ")
	typeSyl(s, "ㄍㄜˋ")
	keys(s, proto.KeyLeft, proto.KeyLeft) // 游標在「我」與 ˇ 之間，後方模式選游標後的字
	if snap = key(s, proto.KeyDown); snap.Candidates == nil || snap.Candidates.Items[0].Text != "ˇ" || snap.Candidates.Items[1].Text != "3" {
		t.Fatalf("ˇ 在句中也能選回數字：%+v", snap.Candidates)
	}
	// 從符號選單選的聲調符號沒有原按鍵，↓ 照舊只列符號組。
	s = toneMarkSession()
	key(s, "`")
	for i, item := range symbolPalette() {
		if item == "ˇ" {
			s.Handle(proto.Request{Type: proto.Select, Index: i})
			break
		}
	}
	if s.cand != nil || len(s.syls) != 1 {
		t.Fatalf("符號選單選 ˇ：%q", s.snapshot().Text())
	}
	if snap = key(s, proto.KeyDown); snap.Candidates == nil || snap.Candidates.Items[1].Text == "3" {
		t.Fatalf("符號選單的 ˇ 沒有原按鍵：%+v", snap.Candidates)
	}
}

func TestToneMarkLeavesMixedInputAlone(t *testing.T) {
	s := toneMarkSession()
	s.MixedInput = true
	snap := keys(s, "3", " ")
	if strings.ContainsAny(snap.Text(), "ˇ") || !strings.HasPrefix(snap.Text(), "3") {
		t.Fatalf("中英混打照舊（#110／#134）：%q", snap.Text())
	}
	// 英文模式沒在組字時照舊交還應用程式。
	s = toneMarkSession()
	s.Handle(proto.Request{Type: proto.Mode, On: true})
	if key(s, "3").Handled {
		t.Fatal("英文模式閒置時數字交還應用程式")
	}
}

func TestETenLoneToneKeyPlusSpace(t *testing.T) {
	// 倚天 41 鍵的聲調在 1（輕聲）2 3 4；5、6 不是注音鍵，照舊交還應用程式。
	for k, want := range map[string]string{"1": "˙", "2": "ˊ", "3": "ˇ", "4": "ˋ"} {
		s := etenSession(t)
		if snap := keys(s, k, " "); !snap.Handled || snap.Text() != want {
			t.Fatalf("倚天 %s＋空白應為 %s：%+v", k, want, snap)
		}
	}
	// 中英混打在倚天排列下停用，設定開著也一樣有聲調符號。
	s := etenSession(t)
	s.MixedInput = true
	if snap := keys(s, "3", " "); snap.Text() != "ˇ" {
		t.Fatalf("倚天＋混打設定：%q", snap.Text())
	}
	s = etenSession(t)
	if snap := keys(s, "1", "2", " "); snap.Text() != "12 " {
		t.Fatalf("倚天連打數字：%q", snap.Text())
	}
}

// #153：中英混打開著時，落單的聲調鍵照舊是數字（#78、#110、#134），↓ 的第一個候選仍是數字，
// 第二個給這個鍵的聲調符號，讓混打的人也能單獨打出 ˊ。沒開混打（含倚天排列，混打在倚天下停用）
// 還沒按空白就按 ↓ 也一樣。前後緊接著數字、英文的聲調鍵是數字或英數的一部分，不加聲調符號。
func downItems(snap proto.Snapshot) []string {
	if snap.Candidates == nil {
		return nil
	}
	var out []string
	for _, item := range snap.Candidates.Items {
		out = append(out, item.Text)
	}
	return out
}

func hasToneMark(items []string) bool {
	return slices.ContainsFunc(items, func(t string) bool { return strings.ContainsAny(t, "ˇˋˊ˙") })
}

func TestMixedLoneToneKeyDownOffersToneMarkSecond(t *testing.T) {
	for k, want := range map[string]string{"3": "ˇ", "4": "ˋ", "6": "ˊ", "7": "˙"} {
		// 句首。
		s := toneMarkSession()
		s.MixedInput = true
		if snap := key(s, k); snap.Text() != k {
			t.Fatalf("混打 %s 照舊顯示數字：%+v", k, snap)
		}
		items := downItems(key(s, proto.KeyDown))
		if len(items) < 2 || items[0] != k || items[1] != want {
			t.Fatalf("混打句首 %s 按 ↓ 應為 %s、%s：%q", k, k, want, items)
		}
		snap := s.Handle(proto.Request{Type: proto.Select, Index: 1})
		if snap.Text() != want || snap.Candidates != nil {
			t.Fatalf("選聲調符號：%+v", snap)
		}
		if snap = key(s, proto.KeyEnter); snap.Commit != want {
			t.Fatalf("提交 %q", snap.Commit)
		}
		assertNoZhuyinLearned(t, s, want)

		// 句中：接在中文後面。
		s = toneMarkSession()
		s.MixedInput = true
		typeSyl(s, "ㄨㄛˇ")
		key(s, k)
		items = downItems(key(s, proto.KeyDown))
		if len(items) < 2 || items[0] != k || items[1] != want {
			t.Fatalf("混打句中 %s 按 ↓：%q", k, items)
		}
		if snap = s.Handle(proto.Request{Type: proto.Select, Index: 1}); snap.Text() != "我"+want {
			t.Fatalf("句中選聲調符號：%q", snap.Text())
		}
		// 不選、直接 Esc 關掉：數字照舊。
		s = toneMarkSession()
		s.MixedInput = true
		keys(s, k, proto.KeyDown, proto.KeyEscape)
		if snap = key(s, proto.KeyEnter); snap.Commit != k {
			t.Fatalf("↓ 後 Esc 再 Enter 照舊送出數字：%q", snap.Commit)
		}
	}

	// 數字的一部分：前後緊接著數字的聲調鍵不加聲調符號。
	for _, run := range []string{"36", "63", "33"} {
		s := toneMarkSession()
		s.MixedInput = true
		for _, k := range run {
			key(s, string(k))
		}
		if items := downItems(key(s, proto.KeyDown)); hasToneMark(items) {
			t.Fatalf("%s 是數字，↓ 不加聲調符號：%q", run, items)
		}
	}
}

func TestLoneToneKeyDownBeforeSpace(t *testing.T) {
	// 沒開混打：聲調鍵還沒按空白就按 ↓，同樣第二個是聲調符號。
	s := toneMarkSession()
	if items := downItems(keys(s, "6", proto.KeyDown)); len(items) < 2 || items[0] != "6" || items[1] != "ˊ" {
		t.Fatalf("6 按 ↓：%q", items)
	}
	// 倚天排列（混打設定開著也停用）：2 是 ˊ、1 是輕聲。
	for k, want := range map[string]string{"1": "˙", "2": "ˊ", "3": "ˇ", "4": "ˋ"} {
		s := etenSession(t)
		s.MixedInput = true
		if items := downItems(keys(s, k, proto.KeyDown)); len(items) < 2 || items[0] != k || items[1] != want {
			t.Fatalf("倚天 %s 按 ↓：%q", k, items)
		}
		if snap := s.Handle(proto.Request{Type: proto.Select, Index: 1}); snap.Text() != want {
			t.Fatalf("倚天選聲調符號：%q", snap.Text())
		}
	}
}
