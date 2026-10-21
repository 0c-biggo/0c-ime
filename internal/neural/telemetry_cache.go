package neural

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

// TelemetryQueueDir 是 onnxruntime 1.30.0 在 Linux／macOS 寫遙測資料的目錄（NoTelemetryEnv 的說明）；Windows 改寫 ETW，沒有這個目錄。
func TelemetryQueueDir(home, goos string) string {
	switch goos {
	case "linux":
		return filepath.Join(home, ".cache/Microsoft/DeveloperTools/.onnxruntime")
	case "darwin":
		return filepath.Join(home, "Library/Application Support/Microsoft/DeveloperTools/.onnxruntime")
	}
	return ""
}

// RemoveTelemetryQueue 刪掉 0.2.3 以前的 worker 留下、還沒送出的遙測事件（onnxruntime.db 與 SQLite 的附屬檔），
// 免得同一個帳號的其他 onnxruntime 程式之後把它送出。deviceid 與目錄本身不動：那是所有 onnxruntime 程式共用的。
func RemoveTelemetryQueue(home string) error {
	dir := TelemetryQueueDir(home, runtime.GOOS)
	if home == "" || dir == "" {
		return nil
	}
	var errs []error
	for _, name := range []string{"onnxruntime.db", "onnxruntime.db-wal", "onnxruntime.db-shm", "onnxruntime.db-journal"} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
