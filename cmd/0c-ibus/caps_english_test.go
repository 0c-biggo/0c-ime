//go:build linux

package main

import (
	"fmt"
	"net"
	"sync/atomic"
	"testing"

	"git.biggo.com/0c/ime/bridge/ipc"
	core "git.biggo.com/0c/ime/core/engine"
)

// commits 收集這段期間送出的提交文字。
func commits(t *testing.T, rec *recorder) []string {
	t.Helper()
	var out []string
	for _, s := range rec.take() {
		if s.name == "CommitText" {
			out = append(out, variantText(t, s.args[0]))
		}
	}
	return out
}

// pressCaps 模擬按放 Caps Lock：按下時事件帶的是之前的鎖定狀態，放開時已經鎖定（X11／xkbcommon 的 LockMods）。
func pressCaps(e *engine, wasOn bool) {
	before := uint32(0)
	if wasOn {
		before = maskLock
	}
	e.processKey(keyCapsLock, 0x3a, before)
	e.processKey(keyCapsLock, 0x3a, maskLock|maskRelease)
}

func typeLocked(e *engine, keys string) {
	for _, k := range keys {
		e.processKey(uint32(k), 0, maskLock)
		e.processKey(uint32(k), 0, maskLock|maskRelease)
	}
}

// #173：「Caps Lock 開著時直接打英文」。設定關閉或舊引擎時照 0.2.1（注音模式不理 Caps Lock）；開啟時 Caps Lock 是暫時的英文。
func TestCapsLockEnglishSetting(t *testing.T) {
	for _, tc := range []struct {
		name string
		info map[string]string
		on   bool
	}{{"關", map[string]string{"caps_lock_english": "false"}, false}, {"舊引擎", map[string]string{}, false}, {"開", map[string]string{"caps_lock_english": "true"}, true}} {
		t.Run(tc.name, func(t *testing.T) {
			e, rec, sock := newTestEngine(t)
			eng := toyEngine(t)
			eng.CapsLockEnglish = true // 引擎認得；外殼只看 info 決定要不要送
			defer serveEngine(t, sock, eng, tc.info)()
			typeKeys(e, "ji3")
			rec.take()
			pressCaps(e, false)
			got := commits(t, rec)
			if tc.on != (len(got) == 1 && got[0] == "我") || tc.on != e.english {
				t.Fatalf("組字中開 Caps Lock：開啟時先送出組字並切到英文，關閉時不動：%v %v %q", got, e.english, e.preedit)
			}
			if !tc.on {
				typeLocked(e, "JI3") // 0.2.1：照打注音
				if e.preedit != "我我" || e.english {
					t.Fatalf("設定關閉時 Caps Lock 開著照打注音：%q", e.preedit)
				}
				return
			}
			// ABC123：字母、數字都交給應用程式（大寫、半形由系統決定），不進組字。
			for _, k := range "ABC123,." {
				if e.processKey(uint32(k), 0, maskLock) || e.preedit != "" {
					t.Fatalf("暫時英文的 %q 應交給應用程式：%q", k, e.preedit)
				}
			}
			// Ctrl＋標點也照英文模式交給應用程式。
			if e.processKey(',', 0, maskLock|maskControl) {
				t.Fatal("暫時英文的 Ctrl+, 應交給應用程式")
			}
			// 指示器（注／英屬性）跟著英文。
			if p := e.modeProperty(); p.symbol != "英" {
				t.Fatalf("Caps Lock 開著時指示器應是英：%+v", p)
			}
			// 關 Caps Lock：回到注音，照打注音。
			pressCaps(e, true)
			if e.english {
				t.Fatal("關 Caps Lock 應回到注音")
			}
			typeKeys(e, "ji3")
			if e.preedit != "我" {
				t.Fatalf("回到注音後 ji3 → 我：%q", e.preedit)
			}
			e.processKey(keyEscape, 0, 0)
			e.processKey(keyEscape, 0, 0)

			// 先用 Shift 切到英文再開關 Caps Lock：只影響大小寫，不改變中英模式。
			if !tapRightShift(e) || !e.english {
				t.Fatal("右 Shift 應切到英文")
			}
			pressCaps(e, false)
			pressCaps(e, true)
			if !e.english {
				t.Fatal("Shift 切的英文在開關 Caps Lock 後應維持英文")
			}
			tapRightShift(e)

			// Caps Lock 開著時單按 Shift 照常切回注音，照打注音（不理 Caps Lock），之後關 Caps Lock 也維持注音。
			pressCaps(e, false)
			if !e.english || !tapRightShift(e) || e.english {
				t.Fatal("Caps Lock 開著時 Shift 應切回注音")
			}
			typeLocked(e, "JI3")
			if e.preedit != "我" {
				t.Fatalf("Shift 切回注音後 Caps Lock 開著照打注音：%q", e.preedit)
			}
			rec.take()
			pressCaps(e, true)
			if e.english || e.preedit != "我" || len(commits(t, rec)) != 0 {
				t.Fatalf("關 Caps Lock 應維持注音、不送出組字：%v %q", e.english, e.preedit)
			}
			e.processKey(keyEscape, 0, 0)

			// 按鍵帶的鎖定狀態為準：沒經過 Caps Lock 鍵（例如在別的視窗開的）也會在下一個按鍵同步。
			if e.processKey('A', 0, maskLock) || !e.english {
				t.Fatal("帶著鎖定狀態的按鍵應同步成英文並交給應用程式")
			}
			if !e.processKey('j', 0, 0) || e.english || e.preedit != "ㄨ" {
				t.Fatalf("沒有鎖定狀態的按鍵應同步回注音：%v %q", e.english, e.preedit)
			}
		})
	}
}

// #173：在設定程式打開這一項（引擎以新設定重啟）後回到原本的輸入框，按 Caps Lock 就切到英文，
// 不因 open 的快取還是舊的而讓第一個字母照注音打。
func TestCapsLockEnglishAfterSettingTurnedOn(t *testing.T) {
	e, _, sock := newTestEngine(t)
	stop := serveEngine(t, sock, toyEngine(t), map[string]string{"caps_lock_english": "false"})
	typeKeys(e, "j")
	e.processKey(keyEscape, 0, 0)
	stop()
	e.session.client.conn.Close() // 引擎結束：外殼手上的連線斷了，但要到下一次請求才發現
	eng := toyEngine(t)
	eng.CapsLockEnglish = true
	defer serveEngine(t, sock, eng, map[string]string{"caps_lock_english": "true"})()
	pressCaps(e, false)
	if !e.english {
		t.Fatal("設定打開後按 Caps Lock 應立刻切到英文")
	}
	if e.processKey('A', 0, maskLock) || e.preedit != "" {
		t.Fatalf("第一個字母就是英文、交給應用程式：%q", e.preedit)
	}
}

// #173：換輸入框（enable）回到開始時的模式；Caps Lock 還開著時下一個按鍵重新切到英文。
func TestCapsLockEnglishResyncsAfterFocus(t *testing.T) {
	e, _, sock := newTestEngine(t)
	eng := toyEngine(t)
	eng.CapsLockEnglish = true
	defer serveEngine(t, sock, eng, map[string]string{"caps_lock_english": "true"})()
	typeKeys(e, "j") // 先連上引擎：Caps Lock 鍵本身不為此啟動引擎
	e.processKey(keyEscape, 0, 0)
	pressCaps(e, false)
	if !e.english {
		t.Fatal("開 Caps Lock 應切到英文")
	}
	e.finish("focus_out")
	e.enable()
	if e.english {
		t.Fatal("換輸入框回到開始時的注音")
	}
	if e.processKey('A', 0, maskLock) || !e.english {
		t.Fatal("Caps Lock 還開著：下一個按鍵應切到英文並交給應用程式")
	}
	pressCaps(e, true)
	if e.english {
		t.Fatal("關 Caps Lock 應回到注音")
	}
}

// #173 code review：連線還在時 Caps Lock 鍵沿用 open 回覆的 caps_lock_english，不在每次 Caps Lock 問 health（設定預設關，
// 大多數人每按一次 Caps Lock 都多一次往返）。引擎（ipc.Server）的 info 在 open 與 health 都會組一次，以此計數。
func TestCapsLockKeyUsesOpenInfoWhileConnected(t *testing.T) {
	for _, on := range []bool{false, true} {
		e, _, sock := newTestEngine(t)
		eng := toyEngine(t)
		eng.CapsLockEnglish = on
		l, err := net.Listen("unix", sock)
		if err != nil {
			t.Fatal(err)
		}
		var asked atomic.Int32
		go (&ipc.Server{Engine: eng, Info: map[string]string{"caps_lock_english": fmt.Sprint(on)},
			RuntimeInfo: func() map[string]string { asked.Add(1); return nil }}).Serve(l)
		typeKeys(e, "j") // 開好 session
		e.processKey(keyEscape, 0, 0)
		before := asked.Load()
		for i := 0; i < 3; i++ {
			pressCaps(e, false)
			pressCaps(e, true)
		}
		if got := asked.Load(); got != before {
			t.Fatalf("caps_lock_english=%v：連線還在時 Caps Lock 鍵不應問引擎（多問了 %d 次）", on, got-before)
		}
		if e.english {
			t.Fatalf("caps_lock_english=%v：開關 Caps Lock 後回到注音", on)
		}
		l.Close()
	}
}

// alive 只看連線另一端關了沒有：引擎退出（關掉連線）時回 false 並放掉連線，下一次呼叫重連。
func TestClientAliveSeesEngineExit(t *testing.T) {
	sock := t.TempDir() + "/s.sock"
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, _ := l.Accept()
		accepted <- c
	}()
	c := &client{socket: sock}
	if c.alive() {
		t.Fatal("沒有連線時不是 alive")
	}
	if !c.connectOnce() {
		t.Fatal("連不上")
	}
	server := <-accepted
	if !c.alive() || c.conn == nil {
		t.Fatal("連線還在")
	}
	server.Close()
	if c.alive() || c.conn != nil {
		t.Fatal("引擎關掉連線後 alive 應回 false 並放掉連線")
	}
}

// #173 code review：引擎在 Caps Lock 英文中重啟（在設定程式改了別的設定、更新）：重開 session 先還原 Caps Lock，
// 第一個字母就是英文；設定剛關掉時照注音（Linux 重開 session 不還原模式，不會變成永久的英文）。
func TestCapsLockEnglishSurvivesEngineRestart(t *testing.T) {
	e, _, sock := newTestEngine(t)
	on := func() *core.Engine { eng := toyEngine(t); eng.CapsLockEnglish = true; return eng }
	stop := serveEngine(t, sock, on(), map[string]string{"caps_lock_english": "true"})
	typeKeys(e, "j")
	e.processKey(keyEscape, 0, 0)
	pressCaps(e, false)
	if !e.english {
		t.Fatal("開 Caps Lock 應切到英文")
	}
	stop()
	e.session.client.conn.Close() // 引擎結束
	stop = serveEngine(t, sock, on(), map[string]string{"caps_lock_english": "true"})
	if e.processKey('A', 0, maskLock) || !e.english || e.preedit != "" {
		t.Fatalf("重開 session 後 Caps Lock 還開著，A 照英文交給應用程式：english=%v %q", e.english, e.preedit)
	}
	pressCaps(e, true)
	if e.english {
		t.Fatal("重開後關 Caps Lock 也回到注音")
	}
	// Caps Lock 英文中在設定程式關掉這一項：照注音重開，Caps Lock 開著也照打注音（#168）。
	pressCaps(e, false)
	stop()
	e.session.client.conn.Close()
	defer serveEngine(t, sock, toyEngine(t), map[string]string{"caps_lock_english": "false"})()
	if !e.processKey('j', 0, maskLock) || e.english || e.preedit != "ㄨ" {
		t.Fatalf("設定關掉後照注音：english=%v %q", e.english, e.preedit)
	}
}
