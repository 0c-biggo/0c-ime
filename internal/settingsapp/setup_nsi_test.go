package settingsapp

import (
	"os"
	"strings"
	"testing"
)

// 安裝程序在建立捷徑之後、寫入標記檔之前被中止時（#138），只有 uninstall.exe 的保險刪得掉設定捷徑。
func TestUninstallerDeletesSettingsShortcut(t *testing.T) {
	b, err := os.ReadFile("../../platforms/windows/installer/setup.nsi")
	if err != nil {
		t.Fatal(err)
	}
	want := `Delete "$SMPROGRAMS\` + ShortcutFolder + `\` + ShortcutName + `"`
	if !strings.Contains(string(b), want) {
		t.Fatalf("setup.nsi 的解除安裝沒有刪除 %s", want)
	}
}
