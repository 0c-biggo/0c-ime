package session

import (
	"reflect"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/layout"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 小鶴雙拼：每兩鍵一個音節，轉成拼音後走漢語拼音鍵盤的同一條路。
func xiaoheSession(t *testing.T, toneless bool) *Session {
	t.Helper()
	s := pinyinSession(t)
	s.SetLayout(layout.Xiaohe)
	s.Toneless = toneless
	return s
}

func TestXiaoheTypesWithToneDigits(t *testing.T) {
	for _, c := range []struct{ keys, want string }{
		{"ni3hc3ui4jp4", "你好世界"},
		{"vs1hx2mb2go2", "中華民國"},
		{"ni3de5mk2zi4", "你的名字"},
		{"vs hx2", "中華"}, // 空白是一聲
		{"xm1", "先"},     //
		{"xi1an1", "西安"}, // 雙拼沒有 xian 的切法歧義
		{"lv4", "綠"}, {"nv3", "女"}, {"lt4", "略"},
		{"nihc3", "你好"}, // 前面沒打聲調的音節由解碼決定
		{"wo3ai4ni3", "我愛你"},
		{"ni3hc", "你hao"}, // 還沒收的雙拼顯示成拼音
		{"ni3h", "你h"},    // 落單的一鍵照打
		{"ni3,", "你，"},
	} {
		s := xiaoheSession(t, false)
		if snap := typeKeys(s, c.keys); !snap.Handled || snap.Text() != c.want {
			t.Errorf("小鶴 %q 應組出 %q，實際 %q", c.keys, c.want, snap.Text())
		}
	}
}

func TestXiaoheToneless(t *testing.T) {
	for _, c := range []struct{ keys, want string }{
		{"nihcuijp ", "你好世界"},
		{"vshxmbgo ", "中華民國"},
		{"woaini ", "我愛你"},
	} {
		s := xiaoheSession(t, true)
		var commit string
		for _, k := range c.keys {
			commit += key(s, string(k)).Commit
		}
		if commit != c.want {
			t.Errorf("不打聲調 %q 應送出 %q，實際 %q", c.keys, c.want, commit)
		}
	}
	// 落單的一鍵：空白不收、響鈴，按鍵留著。
	s := xiaoheSession(t, true)
	typeKeys(s, "nih")
	if snap := key(s, " "); snap.Commit != "" || snap.Text() != "你h" {
		t.Errorf("nih␣ 送出 %q、組字 %q", snap.Commit, snap.Text())
	}
	// Enter 送出：拼不成的按鍵照字面。
	s = xiaoheSession(t, true)
	typeKeys(s, "nihc")
	if snap := key(s, proto.KeyEnter); snap.Commit != "你好" {
		t.Errorf("nihc⏎ 送出 %q", snap.Commit)
	}
}

// 逐字轉換：不打聲調時每打完兩鍵就換成字，不必等空白。
func TestXiaoheLiveConversion(t *testing.T) {
	s := xiaoheSession(t, true)
	for _, c := range []struct{ k, want string }{
		{"n", "n"}, {"i", "你"}, {"h", "你h"}, {"c", "你好"}, {"u", "你好sh"}, {"i", "你好是"}, {"j", "你好是j"}, {"p", "你好世界"},
	} {
		if snap := key(s, c.k); snap.Text() != c.want {
			t.Fatalf("按 %q 後組字 %q，應為 %q", c.k, snap.Text(), c.want)
		}
	}
	if snap := key(s, " "); snap.Commit != "你好世界" {
		t.Errorf("空白送出 %q", snap.Commit)
	}
	// 退格刪掉最後一個字、留下聲母鍵；再打韻母鍵補回。
	s = xiaoheSession(t, true)
	typeKeys(s, "nihc")
	if snap := key(s, proto.KeyBackspace); snap.Text() != "你h" {
		t.Errorf("退格後組字 %q，應為 你h", snap.Text())
	}
	if snap := key(s, proto.KeyBackspace); snap.Text() != "你" {
		t.Errorf("再退格組字 %q，應為 你", snap.Text())
	}
	if snap := typeKeys(s, "hc"); snap.Text() != "你好" {
		t.Errorf("補打後組字 %q", snap.Text())
	}
	// 拼不成音節的第二鍵不收、響鈴。
	s = xiaoheSession(t, true)
	s.ZhuyinErrorHint = true
	typeKeys(s, "ni")
	key(s, "b")
	if snap := key(s, "v"); !snap.Bell || snap.Text() != "你b" {
		t.Errorf("bv 應響鈴並留著 b，實際 bell=%v 組字 %q", snap.Bell, snap.Text())
	}
	// 打聲調時照舊等聲調或空白，不逐字轉換。
	s = xiaoheSession(t, false)
	if snap := typeKeys(s, "ni"); snap.Text() != "ni" {
		t.Errorf("打聲調模式 ni 組字 %q", snap.Text())
	}
}

// 不打聲調時音節收成後緊接著的 1–5 是這個音節的聲調：ni3hc 是「你好」、你標三聲；nihc3 的 3 標在「好」。
// 結果跟打聲調時打同樣的鍵相同。
func TestXiaoheToneDigitAfterLiveConversion(t *testing.T) {
	syls := func(s *Session) string {
		var out []string
		for _, y := range s.syls {
			out = append(out, string(y))
		}
		return strings.Join(out, " ")
	}
	ni, hao := string(zhuyin.Untone("ㄋㄧ")), string(zhuyin.Untone("ㄏㄠ"))
	for _, c := range []struct{ keys, text, syls string }{
		{"ni3hc", "你好", "ㄋㄧˇ " + hao},
		{"nihc3", "你好", ni + " ㄏㄠˇ"},
		{"ni3hc3", "你好", "ㄋㄧˇ ㄏㄠˇ"},
		{"ni3de5", "你的", "ㄋㄧˇ ㄉㄜ˙"},
		{"ni33", "你3", "ㄋㄧˇ"},         // 一個音節只標一次，之後的數字照字面（同漢語拼音的 ni33）
		{"ni3hc32", "你好2", "ㄋㄧˇ ㄏㄠˇ"}, //
	} {
		s := xiaoheSession(t, true)
		snap := typeKeys(s, c.keys)
		if snap.Text() != c.text {
			t.Errorf("不打聲調 %q 組字 %q，應為 %q", c.keys, snap.Text(), c.text)
		}
		if got := syls(s); !strings.HasPrefix(got, c.syls) {
			t.Errorf("不打聲調 %q 的音節 %q，應為 %q", c.keys, got, c.syls)
		}
		if snap := key(s, " "); snap.Commit != c.text {
			t.Errorf("不打聲調 %q␣ 送出 %q，應為 %q", c.keys, snap.Commit, c.text)
		}
	}
	// 跟打聲調時的 ni3 完全相同。
	toned, live := xiaoheSession(t, false), xiaoheSession(t, true)
	a, b := typeKeys(toned, "ni3"), typeKeys(live, "ni3")
	a.Revision, b.Revision = 0, 0
	if !reflect.DeepEqual(a, b) {
		t.Errorf("ni3：打聲調 %+v\n不打聲調 %+v", a, b)
	}
}

// 聲調數字只標剛收成、還沒接下一鍵的音節；其他情況同漢語拼音排列。
func TestXiaoheToneDigitEdges(t *testing.T) {
	// 已經按了下一個聲母鍵：數字是落單聲母的聲調，收不成音節，同漢語拼音的 nih3：不收、響鈴，按鍵留著。
	s := xiaoheSession(t, true)
	s.ZhuyinErrorHint = true
	typeKeys(s, "nih")
	if snap := key(s, "3"); !snap.Handled || !snap.Bell || snap.Text() != "你h" {
		t.Errorf("nih3 應響鈴並留著 h：bell=%v 組字 %q", snap.Bell, snap.Text())
	}
	if snap := key(s, "c"); snap.Text() != "你好" || !s.syls[1].Untoned() {
		t.Errorf("nih3c 組字 %q、音節 %v", snap.Text(), s.syls)
	}
	// 這個聲調沒有字（hei2）：同漢語拼音，不收、響鈴，字照舊不打聲調；接著打對的聲調照收。
	s = xiaoheSession(t, true)
	s.ZhuyinErrorHint = true
	typeKeys(s, "hw")
	if snap := key(s, "2"); !snap.Bell || snap.Text() != "黑" || !s.syls[0].Untoned() {
		t.Errorf("hw2 應響鈴、留著不打聲調的黑：bell=%v 組字 %q 音節 %v", snap.Bell, snap.Text(), s.syls)
	}
	if snap := key(s, "1"); snap.Bell || snap.Text() != "黑" || s.syls[0] != "ㄏㄟ" {
		t.Errorf("hw21 組字 %q、音節 %v", snap.Text(), s.syls)
	}
	// 候選開著時數字照舊是選字。
	s = xiaoheSession(t, true)
	typeKeys(s, "ni")
	if snap := key(s, proto.KeyDown); snap.Candidates == nil {
		t.Fatal("↓ 沒有開候選")
	}
	if snap := key(s, "1"); snap.Candidates != nil || snap.Text() != "你" || !s.syls[0].Untoned() {
		t.Errorf("候選開著時 1 應選字：組字 %q、音節 %v", snap.Text(), s.syls)
	}
	// 中間有別的按鍵（移動游標）之後，數字照一般規則。
	s = xiaoheSession(t, true)
	typeKeys(s, "ni")
	key(s, proto.KeyLeft)
	key(s, proto.KeyRight)
	if snap := key(s, "3"); snap.Text() != "你3" || !s.syls[0].Untoned() {
		t.Errorf("移動游標後 3 應照字面：組字 %q、音節 %v", snap.Text(), s.syls)
	}
	// 打聲調時不變：數字收進還沒收的兩鍵（ni3hc 的 hc 還在等聲調）。
	s = xiaoheSession(t, false)
	if snap := typeKeys(s, "ni3hc"); snap.Text() != "你hao" {
		t.Errorf("打聲調 ni3hc 組字 %q", snap.Text())
	}
}

// j q x y 後面的 ü 打 v 也可以（jv＝ju，同 Rime 小鶴雙拼），打聲調與不打聲調都一樣。
func TestXiaoheJVIsJU(t *testing.T) {
	want := map[string]zhuyin.Syllable{"jv": "ㄐㄩ", "qv": "ㄑㄩ", "xv": "ㄒㄩ", "yv": "ㄩ", "ju": "ㄐㄩ", "lv": "ㄌㄩ"}
	for _, toneless := range []bool{false, true} {
		for pair, base := range want {
			s := xiaoheSession(t, toneless)
			s.ZhuyinErrorHint = true
			snap := typeKeys(s, pair+"4")
			if snap.Bell || len(s.syls) != 1 || s.syls[0] != base.WithTone(4) {
				t.Errorf("toneless=%v %s4：bell=%v 音節 %v", toneless, pair, snap.Bell, s.syls)
			}
		}
	}
}
