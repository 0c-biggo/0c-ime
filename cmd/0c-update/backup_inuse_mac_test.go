//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/0c-biggo/0c-ime/internal/update"
)

// launchctl print 的實際輸出（macOS 15.7.7 VM，SMAppService 登記的排程），節錄。
const launchctlAgent = `gui/501/com.biggo.0c.update = {
	active count = 1
	path = (submitted by smd.81)
	type = Submitted
	managed_by = com.apple.xpc.ServiceManagement
	state = running

	program identifier = Contents/MacOS/0c-update (mode: 2)
	parent bundle identifier = com.biggo.inputmethod.0c
	parent bundle version = 100.2.9000000
	BTM uuid = C47680F4-4B7E-4027-926E-6D5B455AFDFC
	arguments = {
		0c-update
		auto
	}
}
`

func writeBackupApp(t *testing.T, dir, version string) {
	t.Helper()
	contents := filepath.Join(dir, update.MacApp, "Contents")
	if err := os.MkdirAll(filepath.Join(contents, "MacOS"), 0o700); err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
	<key>CFBundleIdentifier</key>
	<string>com.biggo.inputmethod.0c</string>
	<key>CFBundleVersion</key>
	<string>` + version + `</string>
</dict></plist>
`
	os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(plist), 0o600)
}

// #179：清理備份前判斷使用中——有程序從裡面執行或開著檔案、背景項目登記的是裡面那個版本的 App、
// 或查不到使用情形時，都不刪。
func TestMacBackupInUse(t *testing.T) {
	if got := agentBundleVersion(launchctlAgent); got != "100.2.9000000" {
		t.Fatalf("agent version %q", got)
	}
	if agentBundleVersion("gui/501/com.biggo.0c.update = {\n\tpath = /Users/lily/Library/LaunchAgents/com.biggo.0c.update.plist\n}") != "" {
		t.Fatal("legacy LaunchAgents plist has no parent bundle")
	}
	if !agentNotRegistered("Bad request.\nCould not find service \"com.biggo.0c.update\" in domain for user gui: 501\n") {
		t.Fatal("missing service not recognized")
	}
	if agentNotRegistered("Bad request.\nCould not find domain for user gui: 599\n") {
		t.Fatal("a missing GUI domain says nothing about the registration")
	}

	backups := filepath.Join(t.TempDir(), "Library/Application Support/0c/backups")
	running := filepath.Join(backups, "macos-20261001-010203-11")
	registered := filepath.Join(backups, "macos-20261002-010203-22")
	idle := filepath.Join(backups, "macos-20261003-010203-33")
	similar := filepath.Join(backups, "macos-20261003-010203-3")
	writeBackupApp(t, running, "100.1.6400000")
	writeBackupApp(t, registered, "100.2.9000000")
	writeBackupApp(t, idle, "100.2.1000000")
	writeBackupApp(t, similar, "100.2.1000000")
	// lsof -F fn 的格式（VM 實測）：p 行是程序，f 是檔案描述（txt 執行檔與程式庫、cwd 工作目錄、數字是開著的檔案），n 是路徑。
	lsof := "p446\nfcwd\nn/\nftxt\nn" + running + "/0c.app/Contents/MacOS/0c\nftxt\nn/usr/lib/dyld\n" +
		"p532\nftxt\nn" + similar + "3x/elsewhere\n" + // 名稱開頭相同的別的目錄
		"p553\nftxt\nn/System/Library/CoreServices/TextInputMenuAgent.app/Contents/MacOS/TextInputMenuAgent\nftxt\nn" + idle + "/0c.app/Contents/Resources/0c.tiff\nf12\nn" + idle + "/0c.app/Contents/Info.plist\n" +
		"p648\nftxt\nn/usr/bin/python3\nftxt\nn" + similar + "/0c.app/Contents/Resources/neural/python/lib/numpy/_core/_multiarray_umath.so\n"
	open := lsofPaths(lsof)
	if !slices.Contains(open, running+"/0c.app/Contents/MacOS/0c") || slices.Contains(open, "446") || slices.Contains(open, idle+"/0c.app/Contents/Resources/0c.tiff") ||
		!slices.Contains(open, similar+"/0c.app/Contents/Resources/neural/python/lib/numpy/_core/_multiarray_umath.so") {
		t.Fatalf("lsof paths %v", open)
	}
	u := backupUsage{open: open, agent: agentBundleVersion(launchctlAgent)}
	// idle：只有系統的輸入法選單映射著舊圖示、開著 Info.plist，刪掉也讀得到，不算使用中；
	// similar：有程序載入裡面的 Python 模組。
	prefix := filepath.Join(backups, "macos-20261001-010203-1") // running 的名稱開頭，不是它的上層
	writeBackupApp(t, prefix, "100.2.1000000")
	for dir, want := range map[string]bool{running: true, registered: true, idle: false, similar: true, prefix: false} {
		if got := u.inUse(dir, appBundleVersion); got != want {
			t.Errorf("inUse(%s) = %v, want %v", filepath.Base(dir), got, want)
		}
	}
	// 沒有背景項目登記：只看開著的檔案。
	u.agent = ""
	if u.inUse(registered, appBundleVersion) {
		t.Fatal("no registration, nothing open: not in use")
	}
	// 查不到使用情形：一律保留。
	if !(backupUsage{unknown: true}).inUse(idle, appBundleVersion) {
		t.Fatal("unknown usage must keep the backup")
	}
	if appBundleVersion(filepath.Join(idle, "missing.app")) != "" {
		t.Fatal("version of a missing app")
	}
}
