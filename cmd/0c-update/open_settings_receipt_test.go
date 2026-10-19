package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeReceipt(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "install-receipt.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSettingsAppFromReceiptFindsTheNestedAppAndItsRecordedHashes(t *testing.T) {
	receipt := writeReceipt(t, `{"version":"0.1.0-dev.43","app":"0c.app","active":true,"files":{
		"Contents/MacOS/0c":"aaa",
		"Contents/Helpers/0c-settings.app/Contents/MacOS/0c-settings":"bbb",
		"Contents/Helpers/0c-settings.app/Contents/Info.plist":"ccc"}}`)
	app, files := settingsAppFromReceipt(receipt, "/Users/u/Library/Input Methods")
	if want := filepath.Join("/Users/u/Library/Input Methods", "0c.app", "Contents", "Helpers", "0c-settings.app"); app != want {
		t.Fatal(app)
	}
	if len(files) != 2 || files["Contents/MacOS/0c-settings"] != "bbb" || files["Contents/Info.plist"] != "ccc" {
		t.Fatalf("只該有設定程式自己的檔案，路徑相對它：%v", files)
	}
}

func TestSettingsAppFromReceiptSaysNothingForOldInstallsAndRemovedOnes(t *testing.T) {
	for name, body := range map[string]string{
		"解除安裝後": `{"app":"0c.app","active":false,"files":{"Contents/Helpers/0c-settings.app/x":"a"}}`,
		"損毀":    `{not json`,
	} {
		if app, files := settingsAppFromReceipt(writeReceipt(t, body), "/x"); app != "" || files != nil {
			t.Errorf("%s：%q %v", name, app, files)
		}
	}
	if app, _ := settingsAppFromReceipt(filepath.Join(t.TempDir(), "absent.json"), "/x"); app != "" {
		t.Error("沒有收據")
	}
	// 沒有記錄設定程式的舊版收據：有路徑但沒有檔案，LocateBundle 會回 ErrNotFound。
	app, files := settingsAppFromReceipt(writeReceipt(t, `{"app":"0c.app","files":{"Contents/MacOS/0c":"a"}}`), "/im")
	if app == "" || len(files) != 0 {
		t.Fatalf("%q %v", app, files)
	}
	// 0.1.0-dev.21 以前的收據沒有 app 欄位：App 叫 Zuin.app。
	if legacy, _ := settingsAppFromReceipt(writeReceipt(t, `{"files":{}}`), "/im"); legacy != filepath.Join("/im", "Zuin.app", "Contents", "Helpers", "0c-settings.app") {
		t.Fatal(legacy)
	}
}
