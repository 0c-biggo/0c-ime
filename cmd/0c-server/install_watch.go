package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// 系統安裝的升級交接（#181）：apt upgrade 換掉 /usr 下的 0c-server、字典或神經 worker 之後，執行中的引擎
// 還是舊的；系統安裝沒有 0c-update 幫忙交接（由套件管理員更新）。引擎自己每分鐘至多看一次這幾個檔案的
// stat（不讀內容），發現換了、而且連續兩次觀察都相同（套件已經裝完），就在使用者停手、沒有組字時先用新的
// 執行檔核對字典能載入，再走與 0c-update 相同的交接（Drain：存檔、斷開連線），最後以 exec 換成新的執行檔；
// 外殼下一個按鍵重連到新引擎並重播組字。官網安裝由 0c-update 交接，不啟用這段（installWatchEnabled）。
const (
	installWatchInterval = time.Minute
	installWatchIdle     = 30 * time.Second // 最後一個按鍵之後這麼久才交接
	installCheckTimeout  = 2 * time.Minute  // 新執行檔 -check-data 的上限
)

// distributionMarker 是發行版套件放的模式標記（$PREFIX/share/0c/distribution.json，與 $PREFIX/share/0c/data 同層）。
func distributionMarker(data string) string {
	return filepath.Join(filepath.Dir(filepath.Clean(data)), "distribution.json")
}

// installWatchEnabled 決定 -install-watch auto 是否啟用：只在 Linux 的系統安裝——有發行版標記，
// 或執行檔不在使用者家目錄裡（/usr/libexec/0c、/usr/local/libexec/0c、/opt）。官網安裝與開發建置都在
// 家目錄裡；macOS 的 App 在 /Applications 但由 0c-update 交接，所以只限 Linux。
func installWatchEnabled(goos, exe, data, home string) bool {
	if goos != "linux" || exe == "" {
		return false
	}
	if _, err := os.Stat(distributionMarker(data)); err == nil {
		return true
	}
	if home == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(home), filepath.Clean(exe))
	return err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// installWatch 追蹤安裝檔案有沒有被換掉；只做判斷，交接由呼叫端執行。
type installWatch struct {
	paths    []string
	base     []os.FileInfo // 啟動時的狀態；nil 表示當時不存在
	seen     []os.FileInfo // 上一次看到、與啟動時不同的狀態
	rejected []os.FileInfo // 新執行檔核對失敗的狀態：檔案再變之前不再重試
	interval time.Duration
	next     time.Time
}

// newInstallWatch 記下啟動時的狀態。running 是執行中程式的 stat（Linux 的 /proc/self/exe 指向實際執行的
// inode，即使啟動當下檔案正好被換掉也不會把新檔當成基準）；nil 時改看 paths[0]。
func newInstallWatch(paths []string, running os.FileInfo, interval time.Duration, now time.Time) *installWatch {
	w := &installWatch{paths: paths, interval: interval, next: now.Add(interval)}
	w.base = w.stat()
	if running != nil && len(w.base) > 0 {
		w.base[0] = running
	}
	return w
}

func (w *installWatch) stat() []os.FileInfo {
	out := make([]os.FileInfo, len(w.paths))
	for i, p := range w.paths {
		if st, err := os.Stat(p); err == nil {
			out[i] = st
		}
	}
	return out
}

func sameStamp(a, b []os.FileInfo) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		switch {
		case a[i] == nil || b[i] == nil:
			if a[i] != b[i] {
				return false
			}
		case !os.SameFile(a[i], b[i]) || a[i].Size() != b[i].Size() || !a[i].ModTime().Equal(b[i].ModTime()):
			return false
		}
	}
	return true
}

// poll 至多每 interval 看一次。回傳 ready：檔案與啟動時不同，而且與上一次觀察相同（套件已經裝完、
// 檔案穩定了），也不是已經核對失敗的那一組。changed 只表示這一次第一次看到變化（給紀錄用）。
func (w *installWatch) poll(now time.Time) (ready, changed bool) {
	if now.Before(w.next) {
		return false, false
	}
	w.next = now.Add(w.interval)
	cur := w.stat()
	if sameStamp(cur, w.base) {
		w.seen = nil
		return false, false
	}
	if w.rejected != nil && sameStamp(cur, w.rejected) {
		return false, false
	}
	if w.seen != nil && sameStamp(cur, w.seen) {
		return true, false
	}
	w.seen = cur
	return false, true
}

// reject 記下目前這組檔案核對失敗；檔案再變時才重新考慮。
func (w *installWatch) reject() { w.rejected, w.seen = w.seen, nil }

// checkNewEngine 以新的執行檔核對字典能載入（-check-data 不開管道、不碰個人資料）；
// 套件裝到一半、或新版與字典對不上時不交接，舊引擎照常服務。
func checkNewEngine(exe, data string) error {
	ctx, cancel := context.WithTimeout(context.Background(), installCheckTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-check-data", "-data", data)
	cmd.Env = append(os.Environ(), "ZC_ENGINE_REDIRECTED=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return &checkError{err: err, out: msg}
	}
	return nil
}

type checkError struct {
	err error
	out string
}

func (e *checkError) Error() string { return e.err.Error() + "：" + e.out }

// runningExecutable 回傳執行中程式的 stat：Linux 經 /proc/self/exe 取得實際執行的 inode。
func runningExecutable() os.FileInfo {
	if runtime.GOOS != "linux" {
		return nil
	}
	st, err := os.Stat("/proc/self/exe")
	if err != nil {
		return nil
	}
	return st
}
