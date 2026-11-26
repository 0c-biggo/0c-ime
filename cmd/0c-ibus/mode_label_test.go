//go:build linux

package main

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

// #357：頂列的指示是「中」／「英」（同 Windows 的輸入指示器）；選單的模式文字照鍵盤排列寫「注音」或「拼音」（#324）。
// 設定程式換排列時引擎重啟、舊連線跟著斷：下一次聚焦就連上新引擎、重開 session，選單換成新的字；
// 引擎沒在執行時聚焦不為此啟動。
func TestModeLabelFollowsKeyboardLayout(t *testing.T) {
	e, rec, sock := newTestEngine(t)
	stop := serveWith(t, sock, map[string]string{"keyboard_layout": "pinyin", "toggle_key": "right"}, false)
	if p := e.modeProperty(); p.symbol != "中" || p.label != "注音（右 Shift 切換英文）" {
		t.Fatalf("還沒連上引擎時照注音：%+v", p)
	}
	typeKeys(e, "a")
	e.processKey(keyEscape, 0, 0)
	if p := e.modeProperty(); p.symbol != "中" || p.label != "拼音（右 Shift 切換英文）" {
		t.Fatalf("拼音排列：%+v", p)
	}
	e.toggleMode()
	if p := e.modeProperty(); p.symbol != "英" || p.label != "英文（右 Shift 切換拼音）" {
		t.Fatalf("拼音排列的英文模式：%+v", p)
	}
	e.toggleMode()

	// 設定程式改回標準排列：引擎重啟，舊連線斷了。
	stop()
	e.session.client.conn.Close()
	stop = serveWith(t, sock, map[string]string{"keyboard_layout": "standard", "toggle_key": "right"}, false)
	defer func() { stop() }()
	if p := e.modeProperty(); p.label != "拼音（右 Shift 切換英文）" {
		t.Fatalf("還沒聚焦時沿用舊的排列：%+v", p)
	}
	rec.take()
	focus(e, true)
	sig := rec.take()
	if p, ok := registeredMode(sig); !ok || p != "注音（右 Shift 切換英文）" {
		t.Fatalf("換排列後聚焦，選單應是注音：%q %v", p, ok)
	}
	// GNOME Shell 換 engine 後只收第一個 RegisterProperties：同一個 engine 再聚焦時要以 UpdateProperty 送新的字（.13 GNOME 50 實測）。
	if p, ok := propertyUpdate(sig, propInputMode); !ok || variantText(t, p.Label) != "注音（右 Shift 切換英文）" {
		t.Fatalf("換排列後聚焦，要以 UpdateProperty 送新的模式文字：%+v %v", p, ok)
	}
	// 排列沒變時聚焦不多送 UpdateProperty（GNOME 每收到一個就重建觸控鍵盤）。
	focus(e, true)
	if _, ok := propertyUpdate(rec.take(), propInputMode); ok {
		t.Fatal("排列沒變時聚焦不應送模式的 UpdateProperty")
	}

	// 引擎結束、沒有重新啟動：聚焦不為此啟動引擎（測試的 0c-server 路徑不存在，連得上就是偷偷連了）。
	stop()
	e.session.client.conn.Close()
	stop = func() {}
	focus(e, true)
	if e.session.client.conn != nil {
		t.Fatal("引擎沒在執行時聚焦不應啟動或連上引擎（斷掉的連線放掉）")
	}
}

// registeredMode 找出 RegisterProperties 送出的中／英屬性的選單文字。
func registeredMode(sig []recorded) (string, bool) {
	for _, s := range sig {
		if s.name != "RegisterProperties" {
			continue
		}
		for _, v := range s.args[0].(dbus.Variant).Value().(ibusPropList).Props {
			if p := v.Value().(ibusProperty); p.Key == propInputMode {
				text, _ := textOf(p.Label)
				return text, true
			}
		}
	}
	return "", false
}
