package main

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// #198：以 root 執行時要替哪個帳號安裝。
func TestRootTarget(t *testing.T) {
	for _, c := range []struct {
		name               string
		sudo               string
		console, homeOwner int
		want               int
		err                error
	}{
		{"直接 sudo 0c-update：下指令的人", "501", 501, 501, 501, nil},
		{"sudo 優先於螢幕前的人", "502", 501, 502, 502, nil},
		{"sudo installer：postinstall 沒有 SUDO_*，HOME 是同一人", "", 501, 501, 501, nil},
		{"MDM：HOME 是 /var/root", "", 501, 0, 501, nil},
		{"MDM：沒有 HOME", "", 501, -1, 501, nil},
		{"登入畫面：/dev/console 屬於 root", "", 0, 501, 0, errNoConsoleUser},
		{"讀不到 /dev/console", "", -1, -1, 0, errNoConsoleUser},
		{"SUDO_UID 是 root 不算", "0", 501, 501, 501, nil},
		{"SUDO_UID 亂寫不算", "x", 501, 501, 501, nil},
	} {
		got, err := rootTarget(c.sudo, c.console, c.homeOwner)
		if got != c.want || !errors.Is(err, c.err) {
			t.Errorf("%s：得到 %d, %v；要 %d, %v", c.name, got, err, c.want, c.err)
		}
	}
	// ssh 以 A 帳號 sudo installer、螢幕前是 B：不知道要裝給誰，不裝
	_, err := rootTarget("", 501, 502)
	var amb *ambiguousTarget
	if !errors.As(err, &amb) || amb.home != 502 || amb.console != 501 {
		t.Fatalf("HOME 與螢幕前的人不同時要拒絕：%v", err)
	}
	amb.homeName, amb.consoleName = "alice", "bob"
	if msg := amb.Error(); !strings.Contains(msg, "alice") || !strings.Contains(msg, "bob") || !strings.Contains(msg, "CurrentUserHomeDirectory") {
		t.Errorf("說明要寫出兩個帳號與不加 sudo 的安裝方式：%s", msg)
	}
}

func TestParseAccountRecord(t *testing.T) {
	a, err := parseAccountRecord("admin:********:501:20::0:0:Managed User:/Users/admin:/bin/zsh\n")
	if err != nil || a != (macAccount{Name: "admin", UID: 501, Home: "/Users/admin"}) {
		t.Fatalf("%+v %v", a, err)
	}
	for _, bad := range []string{"", "admin:*:501:20:/Users/admin:/bin/zsh", "admin:*:x:20::0:0::/Users/admin:/bin/zsh", "admin:*:501:20::0:0::relative:/bin/zsh"} {
		if _, err := parseAccountRecord(bad); err == nil {
			t.Errorf("%q 應該解析失敗", bad)
		}
	}
	for _, a := range []macAccount{{Name: "root", UID: 0}, {Name: "_mbsetupuser", UID: 248}, {Name: "x", UID: 0}} {
		if !systemAccount(a) {
			t.Errorf("%+v 是系統帳號", a)
		}
	}
	if systemAccount(macAccount{Name: "admin", UID: 501}) {
		t.Error("一般帳號不是系統帳號")
	}
}

// 0.2.3 以 sudo installer 安裝後，root 的引擎與 worker 從使用者的 App 執行（VM 實測的 ps 輸出）。
func TestEnginesUnder(t *testing.T) {
	ps := `    1 /sbin/launchd
  759 /Users/admin/Library/Input Methods/0c.app/Contents/MacOS/0c-server
  760 /Users/admin/Library/Input Methods/0c.app/Contents/Resources/neural/python/bin/python3
  800 /Users/admin/Library/Application Support/0c/backups/macos-1/0c.app/Contents/MacOS/0c-server
  801 /Users/other/Library/Input Methods/0c.app/Contents/MacOS/0c-server
  802 /Users/admin/Library/Input Methods/0c.application/x
  x junk
`
	prefixes := []string{"/Users/admin/Library/Input Methods/0c.app/", "/Users/admin/Library/Application Support/0c/"}
	if got := enginesUnder(ps, prefixes); !reflect.DeepEqual(got, []int{759, 760, 800}) {
		t.Errorf("得到 %v", got)
	}
}

func TestForeignOwnedMessage(t *testing.T) {
	msg := foreignOwnedMessage(12, []string{"/Users/a/Library/Input Methods/0c.app"}, "/Users/a/Downloads/0c 0.2.5.pkg", "/x/0c-update", []string{"install"})
	if !strings.Contains(msg, "12 個") || !strings.Contains(msg, "sudo installer -pkg '/Users/a/Downloads/0c 0.2.5.pkg' -target /") {
		t.Errorf("pkg 安裝時要給 sudo installer 指令：%s", msg)
	}
	msg = foreignOwnedMessage(3, nil, "", "/tmp/0c-0.2.5/0c-update", []string{"install", "-bundle", "/tmp/0c-0.2.5"})
	if !strings.Contains(msg, "sudo /tmp/0c-0.2.5/0c-update install -bundle /tmp/0c-0.2.5") {
		t.Errorf("沒有 pkg 時以 sudo 重跑同一個命令：%s", msg)
	}
	if msg = foreignOwnedMessage(1, nil, "<新版的 .pkg 安裝檔>", "", nil); !strings.Contains(msg, "sudo installer -pkg <新版的 .pkg 安裝檔> -target /") {
		t.Errorf("佔位字不加引號：%s", msg)
	}
	if got := shellQuote("it's"); got != `'it'\''s'` {
		t.Errorf("單引號：%s", got)
	}
}

// pkg 的 postinstall 以 root 執行 install -bundle：套件要先複製給使用者，-bundle 換成複本。
func TestInstallBundleArg(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
		ok   bool
	}{
		{[]string{"install", "-bundle", "/tmp/PKInstallSandbox.x/Scripts/p/0c-0.2.5", "-channel", "public-beta"}, "/tmp/PKInstallSandbox.x/Scripts/p/0c-0.2.5", true},
		{[]string{"install", "--bundle=/a/b"}, "/a/b", true},
		{[]string{"install", "-online"}, "", false},
		{[]string{"install", "-online=false", "-bundle", "/a"}, "/a", true},
		{[]string{"apply", "-archive", "/a.tar.gz"}, "", false},
		{[]string{"status"}, "", false},
		{nil, "", false},
	} {
		got, ok := installBundleArg(c.args)
		want := c.want
		if c.ok { // 回傳的是絕對路徑：Windows 上 /a 會變成目前磁碟的 \a
			want, _ = filepath.Abs(c.want)
		}
		if got != want || ok != c.ok {
			t.Errorf("%v：%q %v", c.args, got, ok)
		}
	}
	if got, ok := installBundleArg([]string{"install"}); !ok || !filepath.IsAbs(got) {
		t.Errorf("沒給 -bundle 時是目前目錄的絕對路徑：%q %v", got, ok)
	}
	for _, c := range []struct{ in, want []string }{
		{[]string{"install", "-bundle", "/old", "-channel", "x"}, []string{"install", "-bundle", "/new", "-channel", "x"}},
		{[]string{"install", "--bundle=/old"}, []string{"install", "-bundle=/new"}},
		{[]string{"install", "-channel", "x"}, []string{"install", "-bundle", "/new", "-channel", "x"}},
	} {
		if got := withBundleArg(c.in, "/new"); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%v → %v，要 %v", c.in, got, c.want)
		}
	}
}

// 修復只動靈犀注音自己的路徑，而且都在家目錄的 Library 底下。
func TestMacOwnedPathsStayInOurDirectories(t *testing.T) {
	for _, p := range macOwnedPaths {
		if !strings.HasPrefix(p, "Library/") || strings.Contains(p, "..") ||
			!(strings.Contains(p, "/0c") || strings.Contains(p, "/Zuin") || strings.Contains(p, "com.biggo.")) {
			t.Errorf("不是靈犀注音的路徑：%s", p)
		}
	}
}
