package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// #199：以另一個系統管理員帳號確認權限（over-the-shoulder）時，整套安裝會落在那個帳號；安裝前就拒絕。
// 只在確定桌面使用者是另一個帳號時拒絕：桌面 shell 的擁有者 SID 與提權程序的 token SID 不同。
// 查不到桌面 shell（session 0、SSH、部署工具以 SYSTEM 靜默安裝）或查不到 token 時照舊安裝，不因為不確定就擋人。
func TestInstallsForOtherAccount(t *testing.T) {
	const amy, admin = "S-1-5-21-1-2-3-1001", "S-1-5-21-1-2-3-500"
	const aad = "S-1-12-1-111-222-333-444"
	for _, c := range []struct {
		name              string
		process, desktop  account
		elevated, refused bool
	}{
		{"桌面使用者自己確認系統管理員權限", account{amy, `PC\amy`}, account{amy, `PC\amy`}, true, false},
		{"以另一個管理員帳號確認權限", account{admin, `PC\admin`}, account{amy, `PC\amy`}, true, true},
		{"SID 大小寫不同仍是同一人", account{"s-1-5-21-1-2-3-1001", `PC\amy`}, account{amy, `PC\amy`}, true, false},
		{"Azure AD 帳號自己提權：名稱寫法不同、SID 相同", account{aad, `AzureAD\AmyLin`}, account{aad, `AzureAD\amy.lin`}, true, false},
		{"Azure AD 帳號的電腦上以另一個管理員確認", account{admin, `PC\admin`}, account{aad, `AzureAD\amy.lin`}, true, true},
		{"名稱相同、SID 不同（別台電腦或網域的同名帳號）", account{admin, `PC\amy`}, account{amy, `PC\amy`}, true, true},
		{"沒有桌面 shell（session 0、SSH、SYSTEM 靜默安裝）", account{amy, `PC\amy`}, account{"", `PC\amy`}, true, false},
		{"沒有登入的使用者", account{admin, `PC\admin`}, account{}, true, false},
		{"查不到 token 的 SID", account{"", `PC\admin`}, account{amy, `PC\amy`}, true, false},
		{"一般權限（安裝本來就會在權限檢查失敗）", account{admin, `PC\admin`}, account{amy, `PC\amy`}, false, false},
	} {
		if got := installsForOtherAccount(c.process, c.desktop, c.elevated); got != c.refused {
			t.Errorf("%s：installsForOtherAccount = %v，預期 %v", c.name, got, c.refused)
		}
	}
}

// #199：對話框要說清楚是哪兩個帳號、要怎麼做，不用 UAC、token、SID 這類詞；本機帳號只寫帳號名稱。
func TestOtherAccountMessage(t *testing.T) {
	err := otherAccountError{admin: account{"S-1-5-21-1-2-3-500", `DESKTOP-5C8I7PS\admin`}, desktop: account{"S-1-5-21-1-2-3-1001", `DESKTOP-5C8I7PS\amy`}}
	msg := err.Error()
	for _, want := range []string{"「admin」", "「amy」", "自己的帳號確認系統管理員權限", "沒有安裝"} {
		if !strings.Contains(msg, want) {
			t.Errorf("訊息缺少 %q：%s", want, msg)
		}
	}
	for _, jargon := range []string{"UAC", "token", "SID", "S-1-5", "DESKTOP-5C8I7PS", "over-the-shoulder", "工作階段", "0c-update"} {
		if strings.Contains(msg, jargon) {
			t.Errorf("訊息不該出現 %q：%s", jargon, msg)
		}
	}
	if !errors.Is(fmt.Errorf("包一層：%w", err), errOtherAccount) {
		t.Fatal("要能以 errors.Is 認出來，0c-update 才會以專用結束碼結束、安裝程式才知道要擋")
	}
	// 帳號名稱一樣只差網域時要寫出網域，否則兩個帳號看起來是同一個。
	same := otherAccountError{admin: account{"S-1-5-21-9-500", `CORP\amy`}, desktop: account{"S-1-5-21-1-1001", `PC\amy`}}.Error()
	if !strings.Contains(same, `CORP\amy`) || !strings.Contains(same, `PC\amy`) {
		t.Errorf("同名帳號要寫出網域：%s", same)
	}
	// 查不到名稱時換個說法，不把 SID 丟給使用者。
	unnamed := otherAccountError{admin: account{"S-1-5-21-1-2-3-500", ""}, desktop: account{"S-1-5-21-1-2-3-1001", `PC\amy`}}.Error()
	if strings.Contains(unnamed, "S-1-5") || !strings.Contains(unnamed, "「amy」") {
		t.Errorf("查不到名稱的帳號：%s", unnamed)
	}
}

// #199：安裝程式要在開始安裝前（.onInit，還沒寫入任何東西）呼叫 check-account，並以同一個結束碼辨認要擋下；
// UTF-16 輸出要在呼叫前就要求，對話框才不是亂碼。
func TestSetupChecksAccountBeforeInstall(t *testing.T) {
	b, err := os.ReadFile("../../platforms/windows/installer/setup.nsi")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	body := s[strings.Index(s, "Function .onInit"):]
	body = body[:strings.Index(body, "FunctionEnd")]
	check := strings.Index(body, `check-account'`)
	if check < 0 {
		t.Fatal(".onInit 沒有呼叫 check-account")
	}
	if unicode := strings.Index(body, "RequestUnicodeOutput"); unicode < 0 || unicode > check {
		t.Fatal("要先要求 UTF-16 輸出再呼叫 check-account")
	}
	if !strings.Contains(body[check:], fmt.Sprintf(`${If} $0 == "%d"`, otherAccountExit)) {
		t.Fatalf("check-account 之後要以結束碼 %d 判斷", otherAccountExit)
	}
	if !strings.Contains(body[check:], "Abort") {
		t.Fatal("確定是另一個帳號時要結束安裝程式")
	}
}
