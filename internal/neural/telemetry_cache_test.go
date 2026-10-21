package neural

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// 只刪未送出的遙測事件，共用的 deviceid 與其他檔案留著；沒有這個目錄時什麼都不做。
func TestRemoveTelemetryQueueKeepsDeviceID(t *testing.T) {
	home := t.TempDir()
	if err := RemoveTelemetryQueue(home); err != nil {
		t.Fatalf("沒有目錄：%v", err)
	}
	dir := TelemetryQueueDir(home, runtime.GOOS)
	if dir == "" {
		t.Skip("Windows 沒有遙測佇列檔")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"onnxruntime.db", "onnxruntime.db-wal", "deviceid", "other"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := RemoveTelemetryQueue(home); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{"onnxruntime.db": false, "onnxruntime.db-wal": false, "deviceid": true, "other": true} {
		if _, err := os.Stat(filepath.Join(dir, name)); (err == nil) != want {
			t.Errorf("%s 存在＝%v，要 %v", name, err == nil, want)
		}
	}
}

func TestTelemetryQueueDir(t *testing.T) {
	for goos, want := range map[string]string{
		"linux":   "/h/.cache/Microsoft/DeveloperTools/.onnxruntime",
		"darwin":  "/h/Library/Application Support/Microsoft/DeveloperTools/.onnxruntime",
		"windows": "",
	} {
		if got := TelemetryQueueDir("/h", goos); filepath.ToSlash(got) != want {
			t.Errorf("%s：%q，要 %q", goos, got, want)
		}
	}
}
