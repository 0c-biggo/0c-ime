package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/0c-biggo/0c-ime/internal/installs"
)

// 系統安裝的升級交接（#181）：apt upgrade 換掉 /usr 下的 0c-server、字典或神經 worker 之後，執行中的引擎
// 還是舊的；系統安裝沒有 0c-update 幫忙交接（由套件管理員更新）。引擎自己每分鐘至多看一次這幾個檔案的
// stat（不讀內容），發現換了、而且連續兩次觀察都相同（套件已經裝完），就在背景用新的執行檔核對字典能載入
// （不擋存檔、失聯偵測與閒置結束）；核對通過、使用者停手又沒有組字時，走與 0c-update 相同的交接（Drain：
// 存檔、斷開連線），最後以 exec 換成新的執行檔；外殼下一個按鍵重連到新引擎並重播組字。官網安裝由
// 0c-update 交接，不啟用這段（installWatchEnabled）。
const (
	installWatchInterval = time.Minute
	installWatchIdle     = 30 * time.Second // 最後一個按鍵之後這麼久才交接
	installCheckTimeout  = 2 * time.Minute  // 新執行檔 -check-data 的上限
	// installCheckRetries 是同一組檔案暫時性核對失敗（逾時、執行不了）的次數上限；每次間隔加倍，
	// 用完才當成核對失敗，等檔案再變。
	installCheckRetries = 3
)

// distributionMarker 是發行版套件放的模式標記（$PREFIX/share/0c/distribution.json，與 $PREFIX/share/0c/data 同層）。
func distributionMarker(data string) string {
	return filepath.Join(filepath.Dir(filepath.Clean(data)), "distribution.json")
}

// installWatchEnabled 決定 -install-watch auto 是否啟用：只在 Linux 的系統安裝——執行檔在建置時設定的系統
// libexec/0c（installs.InSystem），或字典旁有發行版的模式標記。官網安裝（~/.local 底下，解開符號連結後比對）
// 一律不開，由 0c-update 交接；開發建置（例如 /tmp 下的執行檔）兩者都不是，也不開。macOS 的 App 在
// /Applications 但由 0c-update 交接，所以只限 Linux。
func installWatchEnabled(goos, exe, data string, l installs.Layout) bool {
	if goos != "linux" || exe == "" || l.InPersonal(exe) {
		return false
	}
	if l.InSystem(exe) {
		return true
	}
	_, err := os.Stat(distributionMarker(data))
	return err == nil
}

// installWatch 追蹤安裝檔案有沒有被換掉，並記下新檔案的核對結果；交接由呼叫端執行。
// poll 在儲存迴圈呼叫，核對在另一個 goroutine 完成後呼叫 finish，所以欄位都受 mu 保護。
type installWatch struct {
	mu       sync.Mutex
	paths    []string
	base     []os.FileInfo // 啟動時的狀態；nil 表示當時不存在
	seen     []os.FileInfo // 上一次看到、與啟動時不同的狀態
	rejected []os.FileInfo // 新執行檔確定載入不了的狀態：檔案再變之前不再核對
	passed   []os.FileInfo // 核對通過的狀態：交接逾時（一直在組字）之後不必再核對一次
	checking bool          // 核對進行中
	failures int           // seen 這一組暫時性核對失敗的次數
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
	if a == nil || b == nil || len(a) != len(b) {
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
// 檔案穩定了），也不是已經確定核對失敗的那一組。changed 只表示這一次第一次看到變化（給紀錄用）。
func (w *installWatch) poll(now time.Time) (ready, changed bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if now.Before(w.next) {
		return false, false
	}
	w.next = now.Add(w.interval)
	cur := w.stat()
	if sameStamp(cur, w.base) {
		w.seen, w.failures = nil, 0
		return false, false
	}
	if sameStamp(cur, w.rejected) {
		return false, false
	}
	if sameStamp(cur, w.seen) {
		return true, false
	}
	w.seen, w.failures = cur, 0
	return false, true
}

// 檔案穩定之後要做的事（nextStep）。
const (
	watchWait     = iota // 核對進行中，等結果
	watchCheck           // 開始核對 seen 這一組（在背景執行 checkNewEngine，完成後呼叫 finish）
	watchHandOver        // 已核對通過：可以交接
)

// nextStep 在 poll 回報 ready 之後呼叫；watchCheck 時回傳要核對的那一組，交給 finish。
func (w *installWatch) nextStep() (int, []os.FileInfo) {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch {
	case sameStamp(w.seen, w.passed):
		return watchHandOver, nil
	case w.checking:
		return watchWait, nil
	}
	w.checking = true
	return watchCheck, w.seen
}

// finish 記下 stamp 這一組的核對結果。通過時讓下一次 poll 立刻進行（不必再等一個間隔才交接）；
// 確定載入不了時拒絕到檔案再變；暫時性失敗（逾時、執行不了）以加倍的間隔重試，次數用完才拒絕。
// 回傳 true 表示這組檔案已被拒絕。
func (w *installWatch) finish(stamp []os.FileInfo, err error, now time.Time) (rejected bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.checking = false
	var check *checkError
	switch {
	case err == nil:
		w.passed = stamp
		w.next = now
		return false
	case errors.As(err, &check) && check.transient && w.failures+1 < installCheckRetries && sameStamp(stamp, w.seen):
		w.failures++
		w.next = now.Add(w.interval << w.failures)
		return false
	}
	w.rejected = stamp
	if sameStamp(stamp, w.seen) {
		w.seen, w.failures = nil, 0
	}
	return true
}

// checkNewEngine 以新的執行檔核對字典能載入（-check-data 不開管道、不碰個人資料）；
// 套件裝到一半、或新版與字典對不上時不交接，舊引擎照常服務。ctx 在引擎結束時取消。
func checkNewEngine(ctx context.Context, exe, data string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-check-data", "-data", data)
	cmd.Env = append(os.Environ(), "ZC_ENGINE_REDIRECTED=1")
	cmd.WaitDelay = 5 * time.Second // 逾時被結束後，留著輸出管線的子行程不讓這裡一直等
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	// 只有新程式自己跑完、回報失敗（結束碼非 0）才確定載入不了；逾時、被訊號結束、執行不了（例如套件
	// 正在換檔的 ETXTBSY、ENOENT）都可能是暫時的。
	var exit *exec.ExitError
	definite := ctx.Err() == nil && errors.As(err, &exit) && exit.Exited()
	return &checkError{err: err, out: truncateUTF8(strings.TrimSpace(string(out)), 300), transient: !definite}
}

type checkError struct {
	err       error
	out       string
	transient bool
}

func (e *checkError) Error() string {
	if e.transient {
		return e.err.Error() + "（稍後重試）：" + e.out
	}
	return e.err.Error() + "：" + e.out
}

// truncateUTF8 截到至多 n 位元組，不切開 UTF-8 字元（紀錄裡的中文訊息不會變成亂碼）。
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

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
