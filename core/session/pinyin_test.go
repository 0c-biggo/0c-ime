package session

import (
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/layout"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// #324：漢語拼音鍵盤。字母是拼音，拼音後面的 1–5 是聲調（5 輕聲）、空白是一聲，切成音節後交給同一個引擎。
func pinyinSession(t *testing.T) *Session {
	t.Helper()
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"你", "ㄋㄧˇ", 8e-3}, {"好", "ㄏㄠˇ", 4e-3}, {"你好", "ㄋㄧˇ ㄏㄠˇ", 2e-3}, {"世", "ㄕˋ", 1e-3}, {"界", "ㄐㄧㄝˋ", 1e-3},
		{"世界", "ㄕˋ ㄐㄧㄝˋ", 1e-3}, {"是", "ㄕˋ", 1e-2}, {"中", "ㄓㄨㄥ", 3e-3}, {"華", "ㄏㄨㄚˊ", 1e-3}, {"中華", "ㄓㄨㄥ ㄏㄨㄚˊ", 1e-3},
		{"民", "ㄇㄧㄣˊ", 1e-3}, {"國", "ㄍㄨㄛˊ", 2e-3}, {"民國", "ㄇㄧㄣˊ ㄍㄨㄛˊ", 1e-3}, {"中華民國", "ㄓㄨㄥ ㄏㄨㄚˊ ㄇㄧㄣˊ ㄍㄨㄛˊ", 5e-4},
		{"的", "ㄉㄜ˙", 3e-2}, {"名", "ㄇㄧㄥˊ", 1e-3}, {"字", "ㄗˋ", 1e-3}, {"名字", "ㄇㄧㄥˊ ㄗ˙", 1e-3}, {"名字", "ㄇㄧㄥˊ ㄗˋ", 1e-3},
		{"西", "ㄒㄧ", 1e-3}, {"安", "ㄢ", 1e-3}, {"西安", "ㄒㄧ ㄢ", 3e-4}, {"先", "ㄒㄧㄢ", 2e-3}, {"現", "ㄒㄧㄢˋ", 3e-3},
		{"方", "ㄈㄤ", 1e-3}, {"案", "ㄢˋ", 5e-4}, {"方案", "ㄈㄤ ㄢˋ", 5e-4}, {"反", "ㄈㄢˇ", 1e-3}, {"乾", "ㄍㄢ", 1e-3},
		{"綠", "ㄌㄩˋ", 1e-3}, {"女", "ㄋㄩˇ", 2e-3}, {"略", "ㄌㄩㄝˋ", 1e-3}, {"媽", "ㄇㄚ", 1e-3}, {"嗎", "ㄇㄚ˙", 5e-3},
		{"年", "ㄋㄧㄢˊ", 2e-3}, {"我", "ㄨㄛˇ", 1e-2}, {"愛", "ㄞˋ", 2e-3}, {"黑", "ㄏㄟ", 1e-3},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	s := New(personal.NewModel(lex, sys))
	s.SetLayout(layout.Pinyin)
	return s
}

func TestPinyinTypesWithToneDigits(t *testing.T) {
	for _, c := range []struct{ keys, want string }{
		{"ni3hao3shi4jie4", "你好世界"},    // GitHub 0c-ime#85 的例子
		{"zhong1hua2min2guo2", "中華民國"}, //
		{"ni3de5ming2zi4", "你的名字"},     // 5 是輕聲
		{"zhong hua2", "中華"},           // 空白是一聲（同新酷音、小麥注音）
		{"xian1", "先"},                 // xian 一個音節
		{"xi'an1", "西安"},               // 隔音符號
		{"xi1an1", "西安"},               //
		{"lv4", "綠"},                   // ü 打 v
		{"nv3", "女"},                   //
		{"lve4", "略"}, {"lue4", "略"},   // lue 同 lve
		{"ma1ma5", "媽嗎"},           //
		{"nihao3", "你好"},           // 前面沒打聲調的音節由解碼決定
		{"ni3hao", "你hao"},         // 還沒收的拼音照打的字母顯示
		{"ni3h", "你h"},             //
		{"wo3ai4ni3", "我愛你"},       //
		{"ni32026nian2", "你2026年"}, // 沒有拼音時數字照字面（沒在組字時交還應用程式，見 TestPinyinDigits）
		{"ni3,", "你，"},             // , . ; 直接打全形標點（同許氏）
		{"ni3'", "你、"},             // 沒有拼音時 ' 是頓號
	} {
		s := pinyinSession(t)
		if snap := typeKeys(s, c.keys); !snap.Handled || snap.Text() != c.want {
			t.Errorf("拼音 %q 應組出 %q，實際 %q", c.keys, c.want, snap.Text())
		}
	}
}

func TestPinyinCommitsSentence(t *testing.T) {
	s := pinyinSession(t)
	typeKeys(s, "ni3hao3shi4jie4")
	if snap := key(s, proto.KeyEnter); snap.Commit != "你好世界" {
		t.Errorf("送出 %q", snap.Commit)
	}
	// Enter 先把沒打聲調的拼音收完再送出。
	s = pinyinSession(t)
	typeKeys(s, "ni3hao")
	if snap := key(s, proto.KeyEnter); snap.Commit != "你好" {
		t.Errorf("ni3hao⏎ 送出 %q，應為 你好", snap.Commit)
	}
	// 切不成拼音的字母照字面送出：拼音模式也能用 Enter 打出英文字母。
	s = pinyinSession(t)
	typeKeys(s, "hello")
	if snap := key(s, proto.KeyEnter); snap.Commit != "hello" {
		t.Errorf("hello⏎ 送出 %q", snap.Commit)
	}
}

func TestPinyinPendingDisplayAndEditing(t *testing.T) {
	s := pinyinSession(t)
	snap := typeKeys(s, "ni3hao")
	if len(snap.Preedit) == 0 || !snap.Preedit[len(snap.Preedit)-1].Unfinished || snap.Preedit[len(snap.Preedit)-1].Text != "hao" {
		t.Fatalf("還沒收的拼音應以 Unfinished 顯示：%+v", snap.Preedit)
	}
	if snap.Cursor.Rune != 4 {
		t.Errorf("游標應在拼音之後（4），實際 %d", snap.Cursor.Rune)
	}
	if snap = key(s, proto.KeyBackspace); snap.Text() != "你ha" {
		t.Errorf("退格刪一個字母：%q", snap.Text())
	}
	if snap = key(s, proto.KeyEscape); snap.Text() != "你" {
		t.Errorf("Esc 只清掉拼音：%q", snap.Text())
	}
	if snap = key(s, proto.KeyBackspace); snap.Text() != "" {
		t.Errorf("沒有拼音時退格刪字：%q", snap.Text())
	}
	// 退格到沒有字母，隔音符號一起退。
	s = pinyinSession(t)
	typeKeys(s, "xi'")
	if snap = key(s, proto.KeyBackspace); snap.Text() != "xi" {
		t.Errorf("退格退掉隔音符號：%q", snap.Text())
	}
	// Backspace 請求（觸控）一樣刪一個字母。
	if snap = s.Handle(proto.Request{Type: proto.Backspace}); snap.Text() != "x" {
		t.Errorf("Backspace 請求：%q", snap.Text())
	}
}

// 聲調與空白只收得成拼音：切不成音節、或這個聲調沒有字時不收，字母留著（同 #300 打錯注音時提示）。
func TestPinyinRefusesUnusableTone(t *testing.T) {
	for _, keys := range []string{"zhognguo2", "hei2", "hello "} {
		s := pinyinSession(t)
		s.ZhuyinErrorHint = true
		snap := typeKeys(s, keys)
		want := strings.TrimSuffix(keys[:len(keys)-1], " ")
		if snap.Text() != want || !snap.Bell {
			t.Errorf("%q 應擋下聲調並提示，畫面 %q、Bell %v", keys, snap.Text(), snap.Bell)
		}
	}
	s := pinyinSession(t)
	if snap := typeKeys(s, "hei1"); snap.Text() != "黑" || snap.Bell {
		t.Errorf("hei1 應是 黑：%q", snap.Text())
	}
}

// 不打聲調：開「不打聲調也能選字」時空白把拼音換成字並送出（同一般拼音輸入法，2026-11-26）；關閉時空白是一聲。
func TestPinyinToneless(t *testing.T) {
	s := pinyinSession(t)
	s.Toneless = true
	if snap := typeKeys(s, "nihaoshijie "); snap.Commit != "你好世界" || snap.Text() != "" {
		t.Errorf("不打聲調的空白應送出 你好世界：commit %q text %q", snap.Commit, snap.Text())
	}
	// 前面打了聲調、已經是字的也一起送出。
	if snap := typeKeys(s, "ni3hao "); snap.Commit != "你好" || snap.Text() != "" {
		t.Errorf("ni3hao␣ 應送出 你好：commit %q text %q", snap.Commit, snap.Text())
	}
	// 切不成音節：不收也不送，字母留著。
	if snap := typeKeys(s, "zhognguo "); snap.Commit != "" || snap.Text() != "zhognguo" {
		t.Errorf("切不成音節時不送：commit %q text %q", snap.Commit, snap.Text())
	}
	key(s, proto.KeyEscape)
	// 要先看字：↓ 先換成字、不送出；沒有未收的拼音時空白照舊（開候選）。
	if snap := typeKeys(s, "nihao"); snap.Text() != "nihao" {
		t.Errorf("還沒收的拼音顯示字母：%q", snap.Text())
	}
	if snap := key(s, proto.KeyRight); snap.Commit != "" || snap.Text() != "你好" {
		t.Errorf("方向鍵先換成字、不送出：commit %q text %q", snap.Commit, snap.Text())
	}
	if snap := key(s, " "); snap.Commit != "" || snap.Candidates == nil {
		t.Errorf("收完之後的空白照舊開候選：commit %q", snap.Commit)
	}
	s = pinyinSession(t)
	if snap := typeKeys(s, "zhong "); snap.Text() != "中" || snap.Commit != "" {
		t.Errorf("關閉時空白是一聲、不送出：%q %q", snap.Text(), snap.Commit)
	}
}

// 切法由解碼依詞庫決定：fangan 照正詞法是 fan gan，但詞庫只有「方案」時解成 fang an；自己就是一個音節的 xian 不切開。
func TestPinyinSegmentationByDecoder(t *testing.T) {
	s := pinyinSession(t)
	s.Toneless = true
	if snap := typeKeys(s, "fangan "); snap.Commit != "方案" {
		t.Errorf("fangan 應解成 方案：%q", snap.Commit)
	}
	s = pinyinSession(t)
	s.Toneless = true
	if snap := typeKeys(s, "xian "); snap.Commit != "現" && snap.Commit != "先" {
		t.Errorf("xian 是一個音節：%q", snap.Commit)
	}
	s = pinyinSession(t)
	s.Toneless = true
	if snap := typeKeys(s, "xi'an "); snap.Commit != "西安" {
		t.Errorf("xi'an：%q", snap.Commit)
	}
}

// 數字的三種用法：剛打的拼音後面是聲調；候選開著時是選字鍵；其他時候照字面（沒在組字時交還應用程式）。
func TestPinyinDigits(t *testing.T) {
	s := pinyinSession(t)
	if snap := key(s, "1"); snap.Handled {
		t.Errorf("沒在組字時數字交還應用程式")
	}
	typeKeys(s, "xian4")
	snap := key(s, proto.KeyDown)
	if snap.Candidates == nil || len(snap.Candidates.Items) == 0 {
		t.Fatalf("↓ 應開候選")
	}
	snap = key(s, "1")
	if snap.Candidates != nil {
		t.Errorf("候選開著時 1 是選字")
	}
	if snap = key(s, "3"); snap.Text() != "現3" {
		t.Errorf("沒有拼音時數字照字面：%q", snap.Text())
	}
}

// 中英混打在拼音鍵盤停用：開著設定字母也是拼音。Shift＋字母照字面。
func TestPinyinMixedInputOff(t *testing.T) {
	s := pinyinSession(t)
	s.MixedInput = true
	if snap := typeKeys(s, "ni3hao3"); snap.Text() != "你好" {
		t.Errorf("混打開著時照拼音：%q", snap.Text())
	}
	if snap := typeKeys(s, "maA"); snap.Text() != "你好媽A" && snap.Text() != "你好嗎A" {
		t.Errorf("Shift＋字母先把拼音收完：%q", snap.Text())
	}
}

func TestPinyinIdleKeys(t *testing.T) {
	idle := IdleKeys(layout.Pinyin, true, "equals")
	for _, r := range "abcxyz,.;'`=<>?[]" {
		if !strings.ContainsRune(idle, r) {
			t.Errorf("拼音鍵盤沒在組字時 %q 應由輸入法處理（%q）", r, idle)
		}
	}
	for _, r := range "0123456789/-ABC@" {
		if strings.ContainsRune(idle, r) {
			t.Errorf("拼音鍵盤沒在組字時 %q 應交還應用程式", r)
		}
	}
	// 逐鍵比對實際處理結果（同其他排列的 IdleKeys 測試）。
	for r := rune(0x21); r <= 0x7e; r++ {
		s := pinyinSession(t)
		got := key(s, string(r)).Handled
		if got != strings.ContainsRune(idle, r) {
			t.Errorf("%q：IdleKeys %v，實際 handled %v", r, strings.ContainsRune(idle, r), got)
		}
	}
}

// 游標移到中間打拼音：切法的前文只看游標前同一段，收進的音節插在游標處。
func TestPinyinInsertInMiddle(t *testing.T) {
	s := pinyinSession(t)
	typeKeys(s, "ni3shi4jie4")
	key(s, proto.KeyLeft)
	key(s, proto.KeyLeft)
	if snap := typeKeys(s, "hao3"); snap.Text() != "你好世界" {
		t.Errorf("中間插入：%q", snap.Text())
	}
	// 拼音打到一半按方向鍵：先收完（不打聲調）再移動。
	s = pinyinSession(t)
	typeKeys(s, "ni3hao")
	if snap := key(s, proto.KeyLeft); snap.Text() != "你好" || snap.Cursor.Rune != 1 {
		t.Errorf("方向鍵先收完拼音：%q 游標 %d", snap.Text(), snap.Cursor.Rune)
	}
}

// 切到英文：已打的拼音收完留在組字區（同聲調選填，#150）。
func TestPinyinToggleEnglishFinishes(t *testing.T) {
	s := pinyinSession(t)
	typeKeys(s, "ni3hao")
	snap := s.Handle(proto.Request{Type: proto.Toggle})
	if snap.Text() != "你好" {
		t.Errorf("切英文：%q", snap.Text())
	}
	if snap = key(s, "a"); snap.Text() != "你好a" {
		t.Errorf("英文模式字母照字面：%q", snap.Text())
	}
}

// 拼音鍵盤的讀音照拼音顯示：單字候選最後的原樣項目是 xian4（不是 ㄒㄧㄢˋ），選了照字面留下；近音附註也寫拼音。
func TestPinyinShowsReadingsInPinyin(t *testing.T) {
	s := pinyinSession(t)
	typeKeys(s, "xian4")
	snap := key(s, proto.KeyDown)
	for snap.Candidates != nil && snap.Candidates.Page < snap.Candidates.Pages-1 {
		snap = key(s, proto.KeyPageDown)
	}
	items := snap.Candidates.Items
	if last := items[len(items)-1]; last.Text != "xian4" {
		t.Fatalf("原樣項目應是拼音：%+v", items)
	} else if snap = s.Handle(proto.Request{Type: proto.Select, Index: last.ID}); snap.Text() != "xian4" {
		t.Errorf("選原樣項目照字面留下：%q", snap.Text())
	}
	if got := s.readingDisplay("ㄙˋ ㄏㄨㄟˋ"); got != "si4 hui4" {
		t.Errorf("近音讀音：%q", got)
	}
	if got := s.syllableDisplay(zhuyin.Untone("ㄕ")); got != "shi" {
		t.Errorf("沒打聲調：%q", got)
	}
	z := pinyinSession(t)
	z.SetLayout(layout.Standard)
	if got := z.readingDisplay("ㄙˋ"); got != "ㄙˋ" {
		t.Errorf("注音排列照舊：%q", got)
	}
}
