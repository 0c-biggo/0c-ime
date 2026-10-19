//go:build linux

package main

// 跨提交上下文只在應用程式回報完全相符的插入結果後生效（同 Fcitx5 外殼的 context.h）。
// 游標與選取端點以字元計，與 IBus 的 SetSurroundingText 相同。

import (
	"time"
	"unicode/utf8"
)

const maxDocumentBytes = 16384

type docText struct {
	text           string
	cursor, anchor uint32
	valid          bool
}

func (t docText) usable() bool {
	return t.valid && len(t.text) <= maxDocumentBytes && t.cursor == t.anchor &&
		int(t.cursor) <= utf8.RuneCountInString(t.text)
}

type documentContext struct {
	expected  docText
	token     uint64
	confirmed bool
	action    *uint64
}

func (d *documentContext) clear() { *d = documentContext{} }

func (d *documentContext) tracked() bool { return d.token != 0 }

// committed 在送出提交文字前呼叫；這時核心已清除上一段上下文。
func (d *documentContext) committed(before docText, commit string, token uint64) {
	d.clear()
	if token == 0 || commit == "" || !before.usable() {
		return
	}
	r := []rune(before.text)
	text := string(r[:before.cursor]) + commit + string(r[before.cursor:])
	if len(text) > maxDocumentBytes {
		return
	}
	cursor := before.cursor + uint32(utf8.RuneCountInString(commit))
	d.expected = docText{text: text, cursor: cursor, anchor: cursor, valid: true}
	d.token = token
}

func (d *documentContext) matches(t docText) bool {
	return t.usable() && d.expected.valid && t.cursor == d.expected.cursor && t.text == d.expected.text
}

// observed 只由 surrounding-text 更新確認；讀取舊快取不能充當提交回覆。
func (d *documentContext) observed(now docText) {
	if d.token == 0 {
		return
	}
	if !d.matches(now) {
		d.clear()
		return
	}
	if !d.confirmed {
		d.confirmed = true
		t := d.token
		d.action = &t
	}
}

// validate 在每次操作前核對目前文字與游標；尚未收到更新就開始下一段時放棄。
func (d *documentContext) validate(now docText) {
	if d.token != 0 && (!d.confirmed || !d.matches(now)) {
		d.clear()
	}
}

func (d *documentContext) takeAction() (uint64, bool) {
	if d.action == nil {
		return 0, false
	}
	a := *d.action
	d.action = nil
	return a, true
}

// shiftTap 每個輸入欄位分別追蹤一個 Shift 鍵（右 Shift，以及設定開啟時的左 Shift）；只有完整的單鍵按放才切換模式。
type shiftTap struct {
	down, armed bool
	pressed     time.Time     // 第一次按下的時間
	held        time.Duration // 上一次放開時按住多久
}

// key 的 target：這個事件是否為追蹤的那一個 Shift 鍵；now 是收到事件的時間。回傳 true 表示這是一次單按的放開，
// 按住多久要另外用 within 比對門檻（門檻來自引擎設定，#117）。
func (r *shiftTap) key(target, release, modified bool, now time.Time) bool {
	if !target {
		// 只有另一個按下事件構成組合鍵；桌面／RDP 補送的另一側 Shift 放開不取消已觀察到的單按。
		if !release {
			r.armed = false
		}
		return false
	}
	if !release {
		// GNOME Text Editor 等 GTK4 程式把每個 Shift 按放都交給 IBus 兩次。重複的按下不能取消這次單按，
		// 否則在這些程式裡 Shift 永遠不會切換（同 macOS #71 的 Chromium）。
		if !r.down {
			r.down = true
			r.armed = !modified
			r.pressed = now
		} else if modified {
			r.armed = false
		}
		return false
	}
	toggle := r.down && r.armed && !modified
	if r.down {
		r.held = now.Sub(r.pressed)
	}
	r.reset()
	return toggle
}

// within：上一次放開時按住的時間不超過 limit（#117：預設 300 ms，按著 Shift 猶豫後放開不算切換）；limit <= 0 不限時。
func (r *shiftTap) within(limit time.Duration) bool { return limit <= 0 || r.held <= limit }

func (r *shiftTap) reset() { r.down, r.armed = false, false }
