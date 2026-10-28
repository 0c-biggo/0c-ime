package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/installs"
)

func setPackageManaged(t *testing.T, on bool) {
	t.Helper()
	old := packageManagedCheck
	packageManagedCheck = func() bool { return on }
	t.Cleanup(func() { packageManagedCheck = old })
}

// captureStdout 執行 f，回傳它寫到 stdout 的內容。
func captureStdout(t *testing.T, f func() error) (string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	// 邊寫邊讀：Windows 管道緩衝只有 4 KB，寫完才讀會卡住。
	out := make(chan []byte)
	go func() { b, _ := io.ReadAll(r); out <- b }()
	runErr := f()
	os.Stdout = old
	w.Close()
	return string(<-out), runErr
}

// 由套件管理員更新時（#181），更新、安裝、回復、排程的命令一律拒絕，不碰網路與排程；設定、詞彙、API、
// 開設定程式照常（這裡只核對 run 的閘門，那些命令本身另有測試）。
func TestPackageManagedRefusesUpdateCommands(t *testing.T) {
	setPackageManaged(t, true)
	t.Setenv("HOME", t.TempDir())
	for _, args := range [][]string{
		{"check"}, {"check", "-feed", "https://example.invalid/feed.json"}, {"download", "-feed", "https://example.invalid/"},
		{"download-now"}, {"apply", "-archive", "x", "-sha256", "y"}, {"install", "-online"}, {"verify"},
		{"rollback", "-backup", "/tmp/x"}, {"uninstall"}, {"schedule", "install"}, {"auto"}, {"auto", "-now", "-notify"},
		{"finish"}, {"mode", "manual"}, {"pause", "7"}, {"resume"}, {"snooze"}, {"notify-action"}, {"protocol", "zc://x"},
	} {
		if err := run(args); !errors.Is(err, errPackageManaged) {
			t.Errorf("%v: %v", args, err)
		}
	}
	// 設定程式呼叫的 -json 版本以契約格式回報 package_managed。
	for _, args := range [][]string{{"check", "-json"}, {"download-now", "-json"}, {"finish", "-json"}} {
		out, err := captureStdout(t, func() error { return run(args) })
		if !errors.Is(err, errReported) {
			t.Fatalf("%v: %v", args, err)
		}
		var doc struct {
			OK    bool
			Error struct{ Code string }
		}
		if json.Unmarshal([]byte(out), &doc) != nil || doc.OK || doc.Error.Code != codePackageManaged {
			t.Fatalf("%v: %s", args, out)
		}
	}
	for action := range packageModeCommands {
		if err := refusePackageManaged(action, nil); err != nil {
			t.Errorf("%s 應照常可用：%v", action, err)
		}
	}
}

// #229：輸入習慣（profile，#225）跟 set 一樣只改使用者自己的設定。系統安裝的新使用者預設是內建的 0c，
// 被它鎖住的設定要先複製成自訂才能改，profile 被擋就完全改不了。
func TestPackageManagedAllowsProfile(t *testing.T) {
	setPackageManaged(t, true)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LOCALAPPDATA", home)           // Windows 的設定在 %LOCALAPPDATA%\Zuin，不能寫到建置機使用者的真設定
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir()) // 沒有引擎：只寫設定檔
	if err := run([]string{"set", "mixed-input", "off"}); err == nil || errors.Is(err, errPackageManaged) {
		t.Fatalf("前提：新使用者的 mixed-input 由內建輸入習慣固定：%v", err)
	}
	out, err := captureStdout(t, func() error { return run([]string{"profile", "copy", "0c", "--name", "mine", "-json"}) })
	var doc struct{ OK bool }
	if err != nil || json.Unmarshal([]byte(out), &doc) != nil || !doc.OK {
		t.Fatalf("profile copy：%v %s", err, out)
	}
	if err := run([]string{"set", "mixed-input", "off"}); err != nil {
		t.Fatalf("複製成自訂之後應能改：%v", err)
	}
}

// 官網安裝不受影響：同樣的命令不經過這道閘門。
func TestSelfUpdatingInstallIsNotGated(t *testing.T) {
	setPackageManaged(t, false)
	for _, action := range []string{"check", "auto", "install", "rollback", "uninstall", "schedule"} {
		if err := refusePackageManaged(action, nil); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
	}
	if updatesMode() != installs.UpdatesSelf {
		t.Fatal(updatesMode())
	}
}

// status -json 回報 updates，讓設定程式與外殼選單判斷；套件管理員模式不帶官網安裝的更新狀態與核心指標。
func TestStatusReportsPackageManagerMode(t *testing.T) {
	base := t.TempDir()
	os.MkdirAll(filepath.Join(base, "update"), 0o700)
	s, _ := autoupdate.Load(base)
	s.ScheduleRemoved = true
	autoupdate.Save(base, s)

	setPackageManaged(t, false)
	if m := statusMap(base, "0.2.2", noEngine); m["updates"] != installs.UpdatesSelf || m["update"] == nil || m["mode"] == nil {
		t.Fatalf("self: %v", m)
	}
	setPackageManaged(t, true)
	m := statusMap(base, "0.2.3", noEngine)
	if m["updates"] != installs.UpdatesPackageManager || m["installed"] != "0.2.3" || m["engine"] != "" {
		t.Fatalf("package-manager: %v", m)
	}
	for _, k := range []string{"update", "mode", "stage", "target", "action"} {
		if _, ok := m[k]; ok {
			t.Fatalf("package-manager status should not carry %s: %v", k, m)
		}
	}
}

func TestPrintInstallsOnlyWhenThereIsMoreThanTheUsualOne(t *testing.T) {
	var b bytes.Buffer
	printInstalls(&b, []installs.Install{{Kind: installs.KindPersonal, Active: true}})
	if b.Len() != 0 {
		t.Fatalf("官網安裝一份時輸出不變：%q", b.String())
	}
	printInstalls(&b, []installs.Install{{Kind: installs.KindPersonal, Active: true, Server: "/h/.local/bin/0c-server"},
		{Kind: installs.KindSystem, Server: "/usr/libexec/0c/0c-server", Data: "/usr/share/0c/data"}})
	if !strings.Contains(b.String(), "官網安裝（使用中）") || !strings.Contains(b.String(), "系統套件（未使用）：引擎 /usr/libexec/0c/0c-server") {
		t.Fatal(b.String())
	}
}
