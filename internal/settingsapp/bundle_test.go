//go:build !windows

package settingsapp

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.biggo.com/0c/ime/internal/update"
)

// fakeApp 建立一個 .app 目錄樹並回傳收據會記錄的雜湊（路徑相對 .app）。
func fakeApp(t *testing.T) (string, map[string]string) {
	t.Helper()
	app := filepath.Join(t.TempDir(), "0c-settings.app")
	files := map[string]os.FileMode{
		"Contents/MacOS/0c-settings":            0o755,
		"Contents/Info.plist":                   0o644,
		"Contents/Resources/AppIcon.icns":       0o644,
		"Contents/_CodeSignature/CodeResources": 0o644,
	}
	hashes := map[string]string{}
	for name, mode := range files {
		path := filepath.Join(app, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("content of "+name), mode); err != nil {
			t.Fatal(err)
		}
		sum, err := update.Hash(path)
		if err != nil {
			t.Fatal(err)
		}
		hashes[name] = sum
	}
	return app, hashes
}

func TestBundleFilesKeepsOnlyTheSubtreeRelativeToIt(t *testing.T) {
	receipt := map[string]string{
		"Contents/MacOS/0c": "a",
		"Contents/Helpers/0c-settings.app/Contents/Info.plist": "b",
		"Contents/Helpers/0c-settings.app/Contents/MacOS/x":    "c",
		"Contents/Helpers/0c-settings.app-evil/Contents/y":     "d",
		"Contents/Helpers/0c-settings.app/":                    "e",
	}
	got := BundleFiles(receipt, "Contents/Helpers/0c-settings.app")
	if len(got) != 2 || got["Contents/Info.plist"] != "b" || got["Contents/MacOS/x"] != "c" {
		t.Fatalf("%v", got)
	}
	if len(BundleFiles(receipt, "Contents/Helpers/missing.app")) != 0 {
		t.Fatal("沒有這個子目錄時應為空")
	}
}

func TestVerifyBundleAcceptsExactlyTheRecordedTree(t *testing.T) {
	app, hashes := fakeApp(t)
	if err := VerifyBundle(app, hashes); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyBundleRejectsAnyDeviationFromTheRecord(t *testing.T) {
	for name, tamper := range map[string]func(app string){
		"檔案被改": func(app string) {
			os.WriteFile(filepath.Join(app, "Contents/MacOS/0c-settings"), []byte("evil"), 0o755)
		},
		"多出檔案": func(app string) { os.WriteFile(filepath.Join(app, "Contents/extra.dylib"), []byte("x"), 0o644) },
		"少了檔案": func(app string) { os.Remove(filepath.Join(app, "Contents/Resources/AppIcon.icns")) },
		"符號連結": func(app string) {
			target := filepath.Join(app, "Contents/Resources/AppIcon.icns")
			os.Remove(target)
			os.Symlink("/etc/hosts", target)
		},
		"檔案群組可寫":  func(app string) { os.Chmod(filepath.Join(app, "Contents/Info.plist"), 0o664) },
		"目錄其他人可寫": func(app string) { os.Chmod(filepath.Join(app, "Contents/Resources"), 0o777) },
	} {
		app, hashes := fakeApp(t)
		tamper(app)
		if err := VerifyBundle(app, hashes); err == nil {
			t.Errorf("%s：應拒絕", name)
		}
	}
	if app, _ := fakeApp(t); VerifyBundle(app, nil) == nil {
		t.Error("沒有記錄就無從核對")
	}
}

func TestLocateBundleDistinguishesMissingFromUnverifiable(t *testing.T) {
	app, hashes := fakeApp(t)
	if loc, err := LocateBundle(app, hashes); err != nil || loc.Path != app || loc.Source != SourceInstall {
		t.Fatalf("%+v %v", loc, err)
	}
	// 舊版套件：收據沒有記錄設定程式，也沒有這個目錄。
	if _, err := LocateBundle(filepath.Join(t.TempDir(), "absent.app"), hashes); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := LocateBundle(app, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("收據沒有記錄：%v", err)
	}
	// 目錄在、記錄也有，但內容不符：不能當成「沒有」，要說明核對失敗。
	os.WriteFile(filepath.Join(app, "Contents/MacOS/0c-settings"), []byte("evil"), 0o755)
	if _, err := LocateBundle(app, hashes); err == nil || errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "雜湊") {
		t.Fatalf("%v", err)
	}
}

func fakeOpen(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "open")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := openCommand
	openCommand = path
	t.Cleanup(func() { openCommand = old })
	return path
}

// fakeNotify 換掉 notifyutil，記錄每次送出的參數。
func fakeNotify(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "notify")
	path := filepath.Join(dir, "notifyutil")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho \"$@\" >> "+out+"\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := notifyCommand
	notifyCommand = path
	t.Cleanup(func() { notifyCommand = old })
	return out
}

func TestNavigateNotificationNamesAreScopedToTheUser(t *testing.T) {
	// 與 platforms/settings/src/updater.rs 的 navigate_notification 測試是同一個字串。
	if got := NavigateNotification(501, "vocab"); got != "user.uid.501.com.biggo.0c.settings.page.vocab" {
		t.Fatal(got)
	}
	if got := NavigateNotification(501, "../x"); got != "user.uid.501.com.biggo.0c.settings.page.overview" {
		t.Fatalf("不認得的頁面送總覽：%s", got)
	}
}

func TestLaunchAppAsksARunningAppToSwitchPage(t *testing.T) {
	opened := filepath.Join(t.TempDir(), "args")
	fakeOpen(t, `echo "$@" > `+opened)
	notified := fakeNotify(t, "")
	if err := LaunchApp("/x.app", LaunchOptions{Page: "vocab"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(notified)
	if want := fmt.Sprintf("-p user.uid.%d.com.biggo.0c.settings.page.vocab", os.Getuid()); strings.TrimSpace(string(b)) != want {
		t.Fatalf("%q，要 %q", b, want)
	}
	// open 失敗時不送；notifyutil 失敗或不在都不影響結果（視窗已帶到前面，只是不切頁）。
	os.Remove(notified)
	fakeOpen(t, "exit 1")
	if err := LaunchApp("/x.app", LaunchOptions{Page: "vocab"}); err == nil {
		t.Fatal("open 失敗要回報")
	}
	if _, err := os.Stat(notified); err == nil {
		t.Fatal("open 失敗時不應送切頁通知")
	}
	fakeOpen(t, "exit 0")
	fakeNotify(t, "exit 3")
	if err := LaunchApp("/x.app", LaunchOptions{Page: "vocab"}); err != nil {
		t.Fatal(err)
	}
	notifyCommand = filepath.Join(t.TempDir(), "absent")
	if err := LaunchApp("/x.app", LaunchOptions{Page: "vocab"}); err != nil {
		t.Fatal(err)
	}
}

func TestLaunchAppPassesTheBundleAndPageToLaunchServices(t *testing.T) {
	out := filepath.Join(t.TempDir(), "args")
	fakeOpen(t, `echo "$@" > `+out)
	fakeNotify(t, "")
	if err := LaunchApp("/Users/u/Library/Input Methods/0c.app/Contents/Helpers/0c-settings.app", LaunchOptions{Page: "update"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out)
	if got := strings.TrimSpace(string(b)); got != "/Users/u/Library/Input Methods/0c.app/Contents/Helpers/0c-settings.app --args --page update" {
		t.Fatalf("%q", got)
	}
	if err := LaunchApp("/x.app", LaunchOptions{Page: "--evil"}); err != nil {
		t.Fatal(err)
	}
	if b, _ = os.ReadFile(out); !strings.Contains(string(b), "--page overview") {
		t.Fatalf("不認得的頁面開總覽：%q", b)
	}
}

func TestLaunchAppReportsWhyOpenFailedAndNeverHangs(t *testing.T) {
	fakeOpen(t, `echo "The application cannot be opened because its signature is invalid" >&2; exit 1`)
	var exit *ExitError
	if err := LaunchApp("/x.app", LaunchOptions{}); !errors.As(err, &exit) || exit.Code != 1 || !strings.Contains(exit.Tail, "signature is invalid") {
		t.Fatalf("%v", err)
	}
	fakeOpen(t, "exec sleep 30")
	start := time.Now()
	if err := LaunchApp("/x.app", LaunchOptions{Wait: 300 * time.Millisecond}); err == nil {
		t.Fatal("open 卡住時要逾時")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("逾時後應立刻返回")
	}
}
