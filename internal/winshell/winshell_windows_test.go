//go:build windows

package winshell

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

// 通知內容交給系統的 XmlDocument 解析（不送出通知、不碰桌面與登錄）：ToastXML 的輸出必須是它接受的 XML，
// 不合法的內容要回報錯誤而不是送出空白通知。
func TestToastXMLLoadsIntoWindowsXmlDocument(t *testing.T) {
	load := func(content string) error {
		return withWinRT(func() error {
			doc, err := loadXMLDocument(content)
			if err == nil {
				release(doc)
			}
			return err
		})
	}
	ok := ToastXML(Toast{Title: `靈犀注音 <測試> & "引號"`, Body: "內文",
		Action: "zc-update:finish?sha256=ab&version=0.2.12", Snooze: "zc-update:snooze?sha256=ab&version=0.2.12"})
	if err := load(ok); err != nil {
		t.Fatal(err)
	}
	if err := load(`<toast><visual>`); err == nil {
		t.Fatal("不完整的 XML 應被拒絕")
	}
}

// 以 IShellLinkW 建立的捷徑：目標、參數、說明都寫入 .lnk；覆寫既有的捷徑時舊參數不留下（#284 的舊樣式 --headless 參數）。
func TestCreateShortcutWritesEveryField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "靈犀注音設定（0c）.lnk")
	target := filepath.Join(os.Getenv("SystemRoot"), `System32\notepad.exe`)
	if err := CreateShortcut(Shortcut{Path: path, Target: target, Args: "--headless old", Dir: dir, Icon: target, Description: "舊"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(lnkText(t, path), "--headless old") {
		t.Fatal("第一次建立的參數應寫入")
	}
	if err := CreateShortcut(Shortcut{Path: path, Target: target, Dir: dir, Icon: target, Description: "設定靈犀注音輸入法"}); err != nil {
		t.Fatal(err)
	}
	text := lnkText(t, path)
	for _, want := range []string{"notepad.exe", "設定靈犀注音輸入法"} {
		if !strings.Contains(text, want) {
			t.Fatalf(".lnk 缺少 %q", want)
		}
	}
	if strings.Contains(text, "--headless") {
		t.Fatal("覆寫後舊參數不得留下")
	}
	if err := CreateShortcut(Shortcut{Path: filepath.Join(dir, "沒有這個資料夾", "x.lnk"), Target: target}); err == nil {
		t.Fatal("存不了的位置應回報錯誤")
	}
}

// lnkText 把 .lnk 的內容以兩種對齊方式當成 UTF-16LE 解讀並串起來（StringData 以 UTF-16LE 存放，位移不一定是偶數），供比對用。
func lnkText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for start := 0; start < 2; start++ {
		var u []uint16
		for i := start; i+1 < len(b); i += 2 {
			u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
		}
		out = append(out, string(utf16.Decode(u)))
	}
	return strings.Join(out, "\x00")
}
