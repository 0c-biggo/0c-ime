package session

import (
	"slices"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/layout"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// #183：許氏排列。同一鍵依目前的注音暫存代表不同符號，d f j s 在打了注音之後是聲調、空白是一聲；
// 單打一個鍵的歧義（a 是 ㄘ 或 ㄟ）由解碼依前後文挑。中英混打依標準排列設計，許氏下停用。
func hsuSession(t *testing.T) *Session {
	t.Helper()
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"今", "ㄐㄧㄣ", 2e-3}, {"天", "ㄊㄧㄢ", 3e-3}, {"今天", "ㄐㄧㄣ ㄊㄧㄢ", 2e-3}, {"天氣", "ㄊㄧㄢ ㄑㄧˋ", 1e-3},
		{"氣", "ㄑㄧˋ", 1e-3}, {"很", "ㄏㄣˇ", 5e-3}, {"好", "ㄏㄠˇ", 4e-3}, {"我", "ㄨㄛˇ", 1e-2}, {"們", "ㄇㄣ˙", 3e-3},
		{"我們", "ㄨㄛˇ ㄇㄣ˙", 4e-3}, {"去", "ㄑㄩˋ", 3e-3}, {"公園", "ㄍㄨㄥ ㄩㄢˊ", 5e-4}, {"散步", "ㄙㄢˋ ㄅㄨˋ", 4e-4},
		{"次", "ㄘˋ", 2e-3}, {"此", "ㄘˇ", 1e-3}, {"欸", "ㄟˋ", 1e-5}, {"欸", "ㄝˋ", 1e-5}, {"一", "ㄧ", 1e-2},
		{"二", "ㄦˋ", 2e-3}, {"安", "ㄢ", 1e-3}, {"哦", "ㄛˊ", 1e-4}, {"是", "ㄕˋ", 1e-2}, {"知", "ㄓ", 1e-3},
		{"想", "ㄒㄧㄤˇ", 3e-3}, {"吃", "ㄔ", 2e-3}, {"的", "ㄉㄜ˙", 3e-2}, {"這", "ㄓㄜˋ", 5e-3},
		{"欸你好", "ㄟˋ ㄋㄧˇ ㄏㄠˇ", 1e-3}, {"你", "ㄋㄧˇ", 8e-3}, {"你好", "ㄋㄧˇ ㄏㄠˇ", 2e-3}, {"國", "ㄍㄨㄛˊ", 1e-3},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	s := New(personal.NewModel(lex, sys))
	s.SetLayout(layout.Hsu)
	return s
}

func TestHsuTypesSentences(t *testing.T) {
	for _, c := range []struct{ keys, want string }{
		{"gen tem ", "今天"},     // g 接 ㄧ 改成 ㄐ（新酷音的容錯）
		{"jen tem ", "今天"},     // j 接 ㄧ 是 ㄐ
		{"jen tem vej", "今天氣"}, // v 接 ㄧ 是 ㄑ；j 在注音之後是 ˋ
		{"hnfhwf", "很好"},       // n 有聲母時是 ㄣ、f 是 ˇ
		{"xhfmns", "我們"},       // h 有介音時是 ㄛ、s 是輕聲
		{"vuj", "去"},           //
		{"gxl umd", "公園"},      // l 是 ㄥ、m 是 ㄢ、d 是 ˊ
		{"smjbxj", "散步"},       //
		{"cj", "是"},            // c 單獨是 ㄕ
		{"j ", "知"},            // j 單獨是 ㄓ
		{"cekf", "想"},          // c 接 ㄧ 是 ㄒ
		{"cke", "ㄒㄧㄤ"},         // 按鍵順序不同也是 ㄒㄧㄤ，還沒按聲調
		{"m ", "安"},            // 只打 ㄇ 就按聲調：ㄢ
		{"hd", "哦"},            // ㄏ → ㄛ
		{"lj", "二"},            // ㄌ → ㄦ
		{"dgs", "的"},           // d 在空的暫存是 ㄉ，g 有聲母時是 ㄜ
		{"jgj", "這"},           //
		{"aj", "次"},            // a 單打是 ㄘ
		{"e ", "一"},            // e 單打是 ㄧ
		{"gen tem tem veaj", "今天天ㄑㄧㄟˋ"}, // 沒有字的音節原樣留下（#109）
	} {
		s := hsuSession(t)
		if snap := typeKeys(s, c.keys); !snap.Handled || snap.Text() != c.want {
			t.Errorf("許氏 %q 應組出 %q，實際 %q（%+v）", c.keys, c.want, snap.Text(), snap)
		}
	}
}

// 「今天天氣很好我們去公園散步」的許氏按鍵一路打完，Enter 送出。
func TestHsuCommitsSentence(t *testing.T) {
	s := hsuSession(t)
	typeKeys(s, "gen tem tem vejhnfhwfxhfmnsvujgxl umdsmjbxj")
	if snap := key(s, proto.KeyEnter); snap.Commit != "今天天氣很好我們去公園散步" {
		t.Errorf("送出 %q", snap.Commit)
	}
}

// a 單打是 ㄘ 也可能是 ㄟ：音節格兩種讀音都有，前後文支持「欸你好」時解成欸，顯示不標示修正、讀音寫回 ㄟˋ，
// 學習記在 ㄟˋ 底下；候選裡也選得到「欸」。
func TestHsuAmbiguousKeyFollowsContext(t *testing.T) {
	s := hsuSession(t)
	snap := typeKeys(s, "aj")
	if snap.Text() != "次" {
		t.Fatalf("aj 預設讀音 ㄘˋ：%q", snap.Text())
	}
	snap = typeKeys(s, "nefhwf")
	if snap.Text() != "欸你好" {
		t.Fatalf("前後文支持時 aj 解成「欸」：%q", snap.Text())
	}
	for _, seg := range snap.Preedit {
		if seg.Style == proto.StyleCorrected {
			t.Errorf("許氏的歧義讀音不是近音，不可標示修正：%+v", snap.Preedit)
		}
	}
	if s.syls[0] != "ㄟˋ" {
		t.Errorf("挑中的讀音應寫回組字：%v", s.syls)
	}
	if snap := key(s, proto.KeyEnter); snap.Commit != "欸你好" {
		t.Fatalf("送出 %q", snap.Commit)
	}

	// 單打 aj 開候選：次、此之外也有「欸」，選了就是 ㄟˋ。
	s = hsuSession(t)
	typeKeys(s, "aj")
	key(s, proto.KeyDown)
	idx := -1
	for i, c := range s.cand.items {
		if c.Entry.Surface == "欸" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatalf("候選應有「欸」：%v", surfaces(s.cand.items))
	}
	s.Handle(proto.Request{Type: proto.Select, Index: idx})
	snap = s.View()
	if snap.Text() != "欸" || s.syls[0] != "ㄟˋ" {
		t.Errorf("選「欸」後應為 欸／ㄟˋ：%q %v", snap.Text(), s.syls)
	}
	// 選過之後接著打，「欸」不會被換回「次」。
	snap = typeKeys(s, "e ")
	if !strings.HasPrefix(snap.Text(), "欸") || s.syls[0] != "ㄟˋ" {
		t.Errorf("選過的「欸」應保留：%q %v", snap.Text(), s.syls)
	}
	// 學習記在真正的讀音 ㄟˋ 底下，不會讓標準排列打 ㄘˋ 時冒出「欸」。
	key(s, proto.KeyEnter)
	st := s.model.Store
	if !st.Readings["欸\tㄟˋ"] || st.Readings["欸\tㄘˋ"] || !slices.Contains(st.Picked["ㄟˋ"], "欸") || slices.Contains(st.Picked["ㄘˋ"], "欸") {
		t.Errorf("手選的「欸」應記在 ㄟˋ：Readings=%v Picked=%v", st.Readings, st.Picked)
	}
}

func surfaces(items []decode.Candidate) []string {
	var out []string
	for _, c := range items {
		out = append(out, c.Entry.Surface)
	}
	return out
}

// 退格退掉最後一鍵：cke 的 ㄒㄧㄤ 退一格回到 ck 的 ㄕㄤ；完成的音節照舊整個退掉。
func TestHsuBackspace(t *testing.T) {
	s := hsuSession(t)
	typeKeys(s, "cke")
	if snap := key(s, proto.KeyBackspace); snap.Text() != "ㄕㄤ" {
		t.Errorf("退一格應為 ㄕㄤ：%q", snap.Text())
	}
	if snap := typeKeys(s, "ef"); snap.Text() != "想" {
		t.Errorf("再打 ef 應為「想」：%q", snap.Text())
	}
	typeKeys(s, "je")
	if snap := key(s, proto.KeyBackspace); snap.Text() != "想ㄓ" {
		t.Errorf("je 退一格是 j 的 ㄓ：%q", snap.Text())
	}
}

// #85：沒有字、原樣留下的音節，緊接著的介音／韻母鍵依那個音節按聲調前的注音解讀：
// 許氏打 ㄍㄨㄜˊ（gxgd）沒有字，接著的 h 是要把 ㄜ 改成 ㄛ（國），不是新音節的 ㄏ。
func TestHsuRetypeSyllableLiteral(t *testing.T) {
	s := hsuSession(t)
	snap := typeKeys(s, "gxgd")
	if snap.Text() != "ㄍㄨㄜˊ" {
		t.Fatalf("ㄍㄨㄜˊ 沒有字應原樣留下：%q", snap.Text())
	}
	if snap = typeKeys(s, "hd"); snap.Text() != "國" {
		t.Errorf("接著的 h 是 ㄛ：%q", snap.Text())
	}
}

// 許氏沒有標點以外的數字鍵：沒在組字時數字、- / 交還應用程式；, . ; 打「，。；」，' \ 是頓號，= 照舊開 Emoji 選單。
// 中英混打設定開著也停用（同倚天），空白在沒有注音時照舊交還應用程式。
func TestHsuIdleKeysAndPunct(t *testing.T) {
	s := hsuSession(t)
	s.MixedInput = true
	for _, k := range []string{"1", "3", "4", "6", "7", "0", "-", "/", " ", "A"} {
		if key(s, k).Handled {
			t.Errorf("許氏沒在組字時 %q 應交還應用程式", k)
		}
	}
	if s.mixedInput() {
		t.Error("許氏下中英混打停用")
	}
	for k, want := range map[string]string{",": "，", ".": "。", ";": "；", "'": "、", "\\": "、"} {
		s := hsuSession(t)
		if snap := key(s, k); !snap.Handled || snap.Text() != want {
			t.Errorf("%q 應打 %q：%q", k, want, snap.Text())
		}
	}
	s = hsuSession(t)
	if !s.emojiEquals() {
		t.Error("許氏的 = 不是注音鍵，照舊開 Emoji 選單")
	}
	// 組字中的數字照字面留下（同倚天的 5 6）。
	s = hsuSession(t)
	if snap := typeKeys(s, "hnf3"); snap.Text() != "很3" {
		t.Errorf("組字中的 3 照字面：%q", snap.Text())
	}
}

// IdleKeys 必須與 session 實際的處理結果一致：Windows 外殼依它決定 TestKeyDown 要不要吃，答錯會掉鍵或吃掉應用程式的鍵。
func TestIdleKeysMatchSession(t *testing.T) {
	for _, l := range []layout.Layout{layout.Standard, layout.ETen, layout.Hsu} {
		for _, mixed := range []bool{false, true} {
			for _, emoji := range []string{"equals", "off"} {
				idle := IdleKeys(l, mixed, emoji)
				for r := rune(0x21); r <= 0x7e; r++ {
					s := hsuSession(t)
					s.SetLayout(l)
					s.MixedInput, s.EmojiShortcut, s.Preview02 = mixed, emoji, true
					got := key(s, string(r)).Handled
					if want := strings.ContainsRune(idle, r); got != want {
						t.Errorf("%s 混打=%v Emoji=%s：%q 實際 handled=%v，IdleKeys 回報 %v", l.Name(), mixed, emoji, r, got, want)
					}
				}
			}
		}
	}
	if got := IdleKeys(layout.Hsu, false, "equals"); strings.ContainsAny(got, "0123456789-/ ") || !strings.Contains(got, "abcdefghijklmnoprstuvwxyz") || strings.Contains(got, "q") || !strings.ContainsAny(got, "=,.;'`") {
		t.Errorf("許氏的閒置按鍵 %q", got)
	}
}
