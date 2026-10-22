//go:build linux

package main

import (
	"slices"
	"testing"
)

func TestSimplifiedHotkeyMatchesOnlyCtrlShiftF11(t *testing.T) {
	for _, c := range []struct {
		keyval, state uint32
		want          bool
	}{
		{keyF11, maskControl | maskShift, true},
		{keyF11, maskControl | maskShift | maskLock, true},    // Caps Lock 開著也一樣
		{keyF11, maskControl | maskShift | 1<<4, true},        // Num Lock（Mod2）
		{keyF11, maskControl, false},                          // Ctrl＋F11
		{keyF11, maskShift, false},                            // Shift＋F11
		{keyF11, 0, false},                                    // F11（全螢幕）
		{keyF11, maskControl | maskShift | maskMod1, false},   // 多按 Alt
		{keyF11, maskControl | maskShift | maskSuper, false},  // 多按 Super
		{keyF11 + 1, maskControl | maskShift, false},          // Ctrl＋Shift＋F12
		{'f', maskControl | maskShift, false},                 // Ctrl＋Shift＋F 不是
		{keyF11, maskControl | maskShift | maskRelease, true}, // 放開由呼叫端先擋掉
	} {
		if got := simplifiedHotkey(c.keyval, c.state); got != c.want {
			t.Errorf("simplifiedHotkey(%#x, %#x) = %v，要 %v", c.keyval, c.state, got, c.want)
		}
	}
}

// #192：設定「用快捷鍵切換簡體字」關著（引擎沒回報 simplified_hotkey）時 Ctrl＋Shift＋F11 照一般快捷鍵交給應用程式，
// 什麼都不切換；開著時吃掉這個鍵，跟選單一樣即時切換：組字不動、提示列「已切換為輸出簡體字」、指示「注簡」，之後送出簡體。
func TestSimplifiedHotkeySwitchesLikeTheMenu(t *testing.T) {
	e, rec, sock := newTestEngine(t)
	eng, saved := liveSimplifiedEngine(t)
	stop := serveEngine(t, sock, eng, map[string]string{"output_simplified_live": "true"})
	svc := e.svc
	svc.update = parseStatus(`{"settings":{"output_simplified":false}}`)
	svc.mu.Lock()
	defer svc.mu.Unlock()
	if e.processKey(keyF11, 0, maskControl|maskShift) || svc.simplified || len(*saved) != 0 {
		t.Fatalf("設定關著：不吃、不切換（%v %v）", svc.simplified, *saved)
	}
	e.processKey(keyF11, 0, maskControl|maskShift|maskRelease)

	// 在設定程式打開：0c-update 請引擎存檔退出、以新設定再起來；外殼第一次按就照新設定。
	stop()
	e.session.client.conn.Close()
	eng, saved = liveSimplifiedEngine(t)
	defer serveEngine(t, sock, eng, map[string]string{"output_simplified_live": "true", "simplified_hotkey": "true"})()
	typeKeys(e, "z/ ru/3") // ㄈㄥ ㄐㄧㄥˇ
	if e.preedit != "風景" {
		t.Fatalf("組字：%q", e.preedit)
	}
	rec.take()
	if !e.processKey(keyF11, 0, maskControl|maskShift) {
		t.Fatal("設定開著：吃掉 Ctrl＋Shift＋F11")
	}
	if e.processKey(keyF11, 0, maskControl|maskShift|maskRelease) {
		t.Fatal("放開照舊不吃")
	}
	sig := rec.take()
	if e.preedit != "風景" || e.aux != "已切換為輸出簡體字" || !svc.simplified || !slices.Equal(*saved, []bool{true}) {
		t.Fatalf("切換後：組字 %q、提示 %q、狀態 %v、寫檔 %v", e.preedit, e.aux, svc.simplified, *saved)
	}
	if commits := commitTexts(t, sig); len(commits) != 0 {
		t.Fatalf("切換不送出組字：%v", commits)
	}
	if p, ok := propertyUpdate(sig, propInputMode); !ok || variantText(t, p.Symbol) != "注簡" {
		t.Fatalf("注／英指示：%+v", p)
	}
	if p, ok := propertyUpdate(sig, propSimplified); !ok || p.State != propStateChecked {
		t.Fatalf("選單打勾：%+v", p)
	}
	for _, r := range e.session.log {
		if r.Type == "output_simplified" {
			t.Fatal("切換不能記進重播紀錄")
		}
	}
	e.processKey(keyReturn, 0, 0)
	if commits := commitTexts(t, rec.take()); !slices.Equal(commits, []string{"风景"}) {
		t.Fatalf("送出：%v", commits)
	}
	// 只按 Ctrl＋F11、多按 Alt 都不是這個鍵，照舊交給應用程式。
	if e.processKey(keyF11, 0, maskControl) || e.processKey(keyF11, 0, maskControl|maskShift|maskMod1) || !svc.simplified {
		t.Fatal("其他組合不切換")
	}
	// 再按一次切回繁體，英文模式也可以切。
	e.toggleMode()
	if !e.processKey(keyF11, 0, maskControl|maskShift) || svc.simplified || !slices.Equal(*saved, []bool{true, false}) {
		t.Fatalf("切回繁體：%v %v", svc.simplified, *saved)
	}
}
