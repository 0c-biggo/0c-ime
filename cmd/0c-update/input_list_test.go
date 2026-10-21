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
	calls := 0
	fail := errors.New("input.dll 失敗")
	add := func() error {
		calls++
		if calls == 1 {
			return fail
		}
		return nil
	}
	if err := listInputMethodOnce(base, add); !errors.Is(err, fail) {
		t.Fatalf("第一次失敗應回報錯誤，得到 %v", err)
	}
	if _, err := os.Stat(inputListMarker(base)); !os.IsNotExist(err) {
		t.Fatal("加入失敗不能留下記號，否則之後不會再試")
	}
	if err := listInputMethodOnce(base, add); err != nil || calls != 2 {
		t.Fatalf("下一次安裝要重試：err=%v calls=%d", err, calls)
	}
	if b, err := os.ReadFile(inputListMarker(base)); err != nil || strings.TrimSpace(string(b)) != inputTip {
		t.Fatalf("記號內容不對：%q %v", b, err)
	}
	for i := 0; i < 3; i++ { // 之後的更新
		if err := listInputMethodOnce(base, add); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("加入過之後不能再改使用者的清單，呼叫了 %d 次", calls)
	}
}
