package settingsapp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"
)

// MissingLibraries 列出設定程式需要、但這台電腦找不到的共用函式庫（例如沒裝 WebKitGTK）。
// Tauri 缺函式庫時沒有退路，載入器會在程式開始前以 exit 127 結束，所以啟動前先查，才能用人看得懂的話說明。
// 沒有 ldd 或查詢失敗時回傳空清單，交給啟動本身判斷。
func MissingLibraries(path string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ldd", path).Output()
	if err != nil && !errors.As(err, new(*exec.ExitError)) {
		return nil
	}
	return parseMissing(string(out))
}

func parseMissing(ldd string) []string {
	var missing []string
	for _, line := range strings.Split(ldd, "\n") {
		if name, rest, ok := strings.Cut(line, "=>"); ok && strings.Contains(rest, "not found") {
			missing = append(missing, strings.TrimSpace(name))
		}
	}
	return missing
}

// DisplayAvailable 回報這個行程是否接得到圖形桌面（Wayland 或 X11）。
func DisplayAvailable() bool {
	return os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("DISPLAY") != ""
}
