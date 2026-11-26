package session

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/layout"
	"github.com/0c-biggo/0c-ime/core/proto"
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
