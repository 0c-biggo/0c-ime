package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// #191：加入語言清單用的識別必須是 DLL 註冊的那一組（改了 CLSID 等於換一個輸入法）。
func TestInputTipMatchesTSFRegistration(t *testing.T) {
	b, err := os.ReadFile("../../platforms/windows/tsf/src/guids.rs")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	guid := func(name string) string {
		m := regexp.MustCompile(`pub const ` + name + `: GUID = GUID::from_u128\(0x([0-9a-f]{8})_([0-9a-f]{4})_([0-9a-f]{4})_([0-9a-f]{4})_([0-9a-f]{12})\);`).FindStringSubmatch(src)
		if m == nil {
			t.Fatalf("guids.rs 沒有 %s", name)
		}
		return strings.ToUpper(fmt.Sprintf("{%s-%s-%s-%s-%s}", m[1], m[2], m[3], m[4], m[5]))
	}
	lang := regexp.MustCompile(`pub const LANGID: u16 = 0x([0-9a-fA-F]{4});`).FindStringSubmatch(src)
	if lang == nil {
		t.Fatal("guids.rs 沒有 LANGID")
	}
	if want := lang[1] + ":" + guid("CLSID_ZUIN") + guid("GUID_PROFILE"); inputTip != want {
		t.Fatalf("inputTip = %s，DLL 註冊的是 %s", inputTip, want)
	}
}

// #191：第一次安裝（或從沒有這一步的舊版更新）加入一次；之後使用者自己移除的，更新不再放回去；失敗下次再試。
func TestListInputMethodOnce(t *testing.T) {
	base := t.TempDir()
	yes := func() bool { return true }
	calls := 0
	fail := errors.New("input.dll 失敗")
	add := func() error {
		calls++
		if calls == 1 {
			return fail
		}
		return nil
	}
	if err := listInputMethodOnce(base, yes, add); !errors.Is(err, fail) {
		t.Fatalf("第一次失敗應回報錯誤，得到 %v", err)
	}
	if _, err := os.Stat(inputListMarker(base)); !os.IsNotExist(err) {
		t.Fatal("加入失敗不能留下記號，否則之後不會再試")
	}
	if err := listInputMethodOnce(base, yes, add); err != nil || calls != 2 {
		t.Fatalf("下一次安裝要重試：err=%v calls=%d", err, calls)
	}
	if b, err := os.ReadFile(inputListMarker(base)); err != nil || strings.TrimSpace(string(b)) != inputTip {
		t.Fatalf("記號內容不對：%q %v", b, err)
	}
	for i := 0; i < 3; i++ { // 之後的更新
		if err := listInputMethodOnce(base, yes, add); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("加入過之後不能再改使用者的清單，呼叫了 %d 次", calls)
	}
}

// #191 發版前 code review：已加入過的帳號先看記號就結束，不查身分（token、帳號名稱、工作階段的使用者）。
// 登入工作與每日檢查每次都會走到這裡，網域電腦連不到網域控制站時查帳號可能卡住，拖慢引擎啟動。
// 不是工作階段的使用者時不加入、不留記號，那個帳號之後自己安裝或登入時仍會加入。
func TestListInputMethodOnceChecksMarkerBeforeIdentity(t *testing.T) {
	base := t.TempDir()
	asked, added := 0, 0
	add := func() error { added++; return nil }
	notOwner := func() bool { asked++; return false }
	if err := listInputMethodOnce(base, notOwner, add); err != nil || asked != 1 || added != 0 {
		t.Fatalf("不是工作階段的使用者：err=%v asked=%d added=%d", err, asked, added)
	}
	if _, err := os.Stat(inputListMarker(base)); !os.IsNotExist(err) {
		t.Fatal("沒有加入不能留下記號")
	}
	owner := func() bool { asked++; return true }
	if err := listInputMethodOnce(base, owner, add); err != nil || asked != 2 || added != 1 {
		t.Fatalf("工作階段的使用者：err=%v asked=%d added=%d", err, asked, added)
	}
	for i := 0; i < 3; i++ { // 之後的登入與每日檢查
		if err := listInputMethodOnce(base, owner, add); err != nil {
			t.Fatal(err)
		}
	}
	if asked != 2 || added != 1 {
		t.Fatalf("有記號之後不能再查身分：asked=%d added=%d", asked, added)
	}
}

// #191 code review：在 UAC 對話框輸入另一個系統管理員帳號（over-the-shoulder）時，提權的安裝程式是那個管理員，
// 不能替它把靈犀注音加進清單（加進去的是管理員的 HKCU，桌面使用者的清單不變）。
func TestOwnsDesktop(t *testing.T) {
	for _, c := range []struct {
		name, process, session string
		err                    error
		elevated, want         bool
	}{
		{"同一位使用者以系統管理員權限安裝", `PC\amy`, `PC\amy`, nil, true, true},
		{"登入工作與每日檢查（一般權限）", `PC\amy`, `PC\amy`, nil, false, true},
		{"帳號大小寫不同", `pc\Amy`, `PC\amy`, nil, true, true},
		{"以另一個管理員帳號確認 UAC", `PC\admin`, `PC\amy`, nil, true, false},
		{"另一個帳號的一般權限程序（runas）", `PC\bob`, `PC\amy`, nil, false, false},
		{"沒有登入使用者的工作階段（服務、SSH）", `PC\amy`, "", nil, true, false},
		{"查不到工作階段：系統管理員不加", `PC\amy`, "", errors.New("wts"), true, false},
		{"查不到工作階段：一般權限是自己的帳號", `PC\amy`, "", errors.New("wts"), false, true},
	} {
		if got := ownsDesktop(c.process, c.session, c.err, c.elevated); got != c.want {
			t.Errorf("%s：ownsDesktop = %v，預期 %v", c.name, got, c.want)
		}
	}
}
