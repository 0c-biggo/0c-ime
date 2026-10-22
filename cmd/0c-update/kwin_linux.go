//go:build !windows && !darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"
)

// KDE Plasma Wayland（#204）：KWin 依 kwinrc [Wayland] InputMethod（「系統設定 → 鍵盤 → 虛擬鍵盤」選的 .desktop）
// 自己啟動輸入法，交給它 Wayland 輸入法協定的連線（WAYLAND_SOCKET，或經 fcitx5-wayland-launcher）。KWin 只重開
// 當掉的輸入法；更新時正常退出的 Fcitx5 要請 KWin 重新啟動：InputMethod 先清空再寫回，KWin 收到 KConfig 的
// 變更通知、看到值改變才會停止與啟動（等同在「虛擬鍵盤」先選「無」再選回原本的項目）。更新器自己啟動的 fcitx5
// 拿不到這條連線，原生 Wayland 程式就打不了字。
//
// 通知由這裡自己送 org.kde.kconfig.notify.ConfigChanged（KConfigWatcher 收的訊號），不用 kwriteconfig --notify：
// Plasma 5（KF 5.115）的 kwriteconfig5 沒有 --notify，Plasma 6 送出的也是同一個訊號。

var kwinrcArgs = []string{"--file", "kwinrc", "--group", "Wayland", "--key", "InputMethod"}

// kwinIM 是 kwinrc [Wayland] InputMethod 的原值。Inherited：使用者的 kwinrc 沒有這個鍵，值來自系統預設
// （例如 /etc/xdg/kwinrc）；寫回時刪掉使用者 kwinrc 裡（清空時寫入）的鍵，不把系統預設變成固定寫在使用者設定裡。
type kwinIM struct {
	Value     string `json:"input_method"`
	Inherited bool   `json:"inherited,omitempty"`
}

// fcitx：虛擬鍵盤選的是 Fcitx 5（org.fcitx.Fcitx5.desktop）或 Fcitx 5 Wayland Launcher。
func (im kwinIM) fcitx() bool {
	return strings.Contains(strings.ToLower(filepath.Base(im.Value)), "fcitx")
}

// 測試替換用。
var (
	kconfigTool = func(names ...string) (string, error) {
		var err error
		for _, name := range names {
			var path string
			if path, err = exec.LookPath(name); err == nil {
				return path, nil
			}
		}
		return "", err
	}
	// runKConfig 回傳標準輸出；失敗時錯誤帶出 kreadconfig／kwriteconfig 的 stderr。
	runKConfig = func(ctx context.Context, env []string, path string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, path, args...)
		cmd.Env = env
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return out, fmt.Errorf("%s：%s (%w)", filepath.Base(path), strings.TrimSpace(stderr.String()), err)
		}
		return out, nil
	}
	kwinIMAvailable = func(ctx context.Context, conn *dbus.Conn) (bool, error) {
		if conn == nil {
			return false, fmt.Errorf("沒有 D-Bus 連線")
		}
		var v dbus.Variant
		err := conn.Object("org.kde.KWin", "/VirtualKeyboard").CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0,
			"org.kde.kwin.VirtualKeyboard", "available").Store(&v)
		if err != nil {
			return false, err
		}
		available, ok := v.Value().(bool)
		if !ok {
			return false, fmt.Errorf("VirtualKeyboard.available：%v", v)
		}
		return available, nil
	}
	// emitKConfigChanged 送出 KConfigWatcher 收的變更通知：路徑是 /<設定檔名>，參數 a{saay} 為群組 → 鍵名。
	emitKConfigChanged = func(conn *dbus.Conn, file, group, key string) error {
		if conn == nil {
			return fmt.Errorf("沒有 D-Bus 連線")
		}
		return conn.Emit(dbus.ObjectPath("/"+file), "org.kde.kconfig.notify.ConfigChanged", map[string][][]byte{group: {[]byte(key)}})
	}
	// kwinStateDir 是交接紀錄所在的個人資料目錄。
	kwinStateDir = basePath
	// kwinBus 連到 session bus，回傳關閉連線的函式。
	kwinBus = func() (*dbus.Conn, func(), error) {
		conn, err := dbus.ConnectSessionBus()
		if err != nil {
			return nil, nil, err
		}
		return conn, func() { conn.Close() }, nil
	}
	// 等待 KWin 的輪詢間隔與期限：清空後停用輸入法、舊 Fcitx5 結束、寫回後啟動 fcitx5。
	kwinPoll                                   = 100 * time.Millisecond
	kwinClearWait, kwinGoneWait, kwinStartWait = 3 * time.Second, 5 * time.Second, 8 * time.Second
)

// withoutWaylandSocket：WAYLAND_SOCKET 是 KWin 交給舊 Fcitx5 的 fd 編號；從 Fcitx5 的選單啟動的更新器也繼承了它，
// 那個 fd 在新行程裡已失效，自己啟動的程式不能帶著。
func withoutWaylandSocket(env []string) []string {
	out := make([]string, 0, len(env))
	for _, pair := range env {
		if !strings.HasPrefix(pair, "WAYLAND_SOCKET=") {
			out = append(out, pair)
		}
	}
	return out
}

func kconfigEnv(extra []string) []string {
	return withoutWaylandSocket(append(os.Environ(), extra...))
}

// envValue 取 env 裡最後一個 key 的值（與 exec 相同，後面的優先）。
func envValue(env []string, key string) string {
	value := ""
	for _, pair := range env {
		if k, v, ok := strings.Cut(pair, "="); ok && k == key {
			value = v
		}
	}
	return value
}

// userKwinrc 是使用者自己的 kwinrc（KConfig 寫入的位置）。
func userKwinrc(env []string) string {
	if dir := envValue(env, "XDG_CONFIG_HOME"); filepath.IsAbs(dir) {
		return filepath.Join(dir, "kwinrc")
	}
	home := envValue(env, "HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return filepath.Join(home, ".config", "kwinrc")
}

// userInputMethodLines 回傳使用者 kwinrc 的各行，與 [Wayland] 群組裡 InputMethod（可帶 [$e] 之類的標記）所在的行。
func userInputMethodLines(path string) ([]string, []int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	lines := strings.Split(string(raw), "\n")
	group := ""
	var at []int
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		if line[0] == '[' {
			group = line
			continue
		}
		key, _, ok := strings.Cut(line, "=")
		if !ok {
			key = line // InputMethod[$d]：遮蔽系統預設的刪除標記
		}
		key = strings.TrimSpace(key)
		if i := strings.IndexByte(key, '['); i >= 0 {
			key = key[:i]
		}
		if group == "[Wayland]" && key == "InputMethod" {
			at = append(at, i)
		}
	}
	return lines, at, nil
}

func userSetsInputMethod(path string) bool {
	_, at, err := userInputMethodLines(path)
	return err == nil && len(at) > 0
}

// revertUserInputMethod 刪掉使用者 kwinrc 裡的 InputMethod，回到系統預設（同 KConfig 的 revertToDefault）。
// 不用 kwriteconfig --delete：有系統預設時它寫的是遮蔽預設的 InputMethod[$d]，值會變成空的。
func revertUserInputMethod(path string) error {
	real, err := filepath.EvalSymlinks(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	lines, at, err := userInputMethodLines(real)
	if err != nil || len(at) == 0 {
		return err
	}
	info, err := os.Stat(real)
	if err != nil {
		return err
	}
	kept := make([]string, 0, len(lines))
	for i, line := range lines {
		if !slices.Contains(at, i) {
			kept = append(kept, line)
		}
	}
	tmp := real + ".0c-new"
	if err = os.WriteFile(tmp, []byte(strings.Join(kept, "\n")), info.Mode().Perm()); err != nil {
		return err
	}
	if err = os.Rename(tmp, real); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// readKWinIM 讀 kwinrc [Wayland] InputMethod（合併系統預設後的值），並分辨它是不是寫在使用者的 kwinrc。
func readKWinIM(env []string) (kwinIM, error) {
	tool, err := kconfigTool("kreadconfig6", "kreadconfig5")
	if err != nil {
		return kwinIM{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := runKConfig(ctx, env, tool, kwinrcArgs...)
	if err != nil {
		return kwinIM{}, err
	}
	im := kwinIM{Value: strings.TrimSpace(string(out))}
	im.Inherited = im.Value != "" && !userSetsInputMethod(userKwinrc(env))
	return im, nil
}

// writeKWinIM 寫入 InputMethod 並通知 KWin。im 為 nil 時清空（虛擬鍵盤選「無」）；原值來自系統預設時刪掉使用者的鍵。
// conn 為 nil 時只寫檔（KWin 還沒執行，啟動時會讀到）。
func writeKWinIM(ctx context.Context, conn *dbus.Conn, env []string, im *kwinIM) error {
	if im != nil && im.Inherited {
		if err := revertUserInputMethod(userKwinrc(env)); err != nil {
			return fmt.Errorf("還原 kwinrc 的系統預設：%w", err)
		}
	} else {
		tool, err := kconfigTool("kwriteconfig6", "kwriteconfig5")
		if err != nil {
			return err
		}
		args := append(append([]string{}, kwinrcArgs...), "")
		if im != nil {
			args = append(args[:len(args)-1], "--type", "path", im.Value)
		}
		if _, err = runKConfig(ctx, env, tool, args...); err != nil {
			return err
		}
	}
	if conn == nil {
		return nil
	}
	if err := emitKConfigChanged(conn, "kwinrc", "Wayland", "InputMethod"); err != nil {
		return fmt.Errorf("通知 KWin：%w", err)
	}
	return nil
}

// kwinPending 是 InputMethod 清空後、寫回前的紀錄：寫回失敗或更新器在兩次寫入之間被中止時，下一次執行補寫，
// 虛擬鍵盤不會一直停在「無」。
type kwinPending struct {
	kwinIM
	Env []string `json:"env,omitempty"` // 讀寫 kwinrc 用的 XDG_CONFIG_HOME／XDG_CONFIG_DIRS
}

func kwinPendingPath(base string) string {
	return filepath.Join(base, "update", "kwin-input-method.json")
}

func saveKWinPending(base string, im kwinIM, env []string) error {
	p := kwinPending{kwinIM: im}
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_CONFIG_DIRS"} {
		if v := envValue(env, key); v != "" {
			p.Env = append(p.Env, key+"="+v)
		}
	}
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	path := kwinPendingPath(base)
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err = os.WriteFile(path+".new", b, 0o600); err != nil {
		return err
	}
	return os.Rename(path+".new", path)
}

// restoreKWinIM 寫回原值，失敗時稍後重試；仍失敗就保留紀錄，下一次執行（finishKWinIM）補寫。
func restoreKWinIM(ctx context.Context, conn *dbus.Conn, base string, im kwinIM, env []string) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(3 * kwinPoll)
		}
		if err = writeKWinIM(ctx, conn, env, &im); err == nil {
			os.Remove(kwinPendingPath(base))
			return nil
		}
	}
	return fmt.Errorf("恢復 kwinrc InputMethod=%s（已保留紀錄，下次執行時補寫）：%w", im.Value, err)
}

// waitUntil 每 kwinPoll 檢查一次 ok，最多等 limit。
func waitUntil(ctx context.Context, limit time.Duration, ok func() bool) bool {
	deadline := time.Now().Add(limit)
	for {
		if ok() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(kwinPoll):
		}
	}
}

// restartKWinIM 請 KWin 重新啟動輸入法：清空 InputMethod，等 KWin 停用（VirtualKeyboard.available 變 false）
// 與舊的 Fcitx5 結束，再寫回原值，最後確認 KWin 真的啟動了 fcitx5。任一步不成立就回傳錯誤，由呼叫端改為直接啟動；
// kwinrc 一律寫回原值。
func restartKWinIM(ctx context.Context, conn *dbus.Conn, base string, im kwinIM, env []string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := saveKWinPending(base, im, env); err != nil {
		return fmt.Errorf("記錄 kwinrc 原值：%w", err)
	}
	if err := writeKWinIM(ctx, conn, env, nil); err != nil {
		if e := restoreKWinIM(ctx, conn, base, im, env); e != nil {
			err = fmt.Errorf("%w；%v", err, e)
		}
		return fmt.Errorf("清除 kwinrc InputMethod：%w", err)
	}
	cleared := waitUntil(ctx, kwinClearWait, func() bool {
		available, err := kwinIMAvailable(ctx, conn)
		return err == nil && !available
	})
	if cleared {
		// KWin 停用時結束它啟動的輸入法；舊行程還在時寫回，新的 fcitx5 會因為已有執行中的實例而退出。
		waitUntil(ctx, kwinGoneWait, func() bool { pids, err := fcitxProcesses(); return err == nil && len(pids) == 0 })
	}
	if err := restoreKWinIM(ctx, conn, base, im, env); err != nil {
		return err
	}
	if !cleared {
		return fmt.Errorf("KWin 在 3 秒內沒有停用輸入法（VirtualKeyboard.available 沒有變成 false）")
	}
	if !waitUntil(ctx, kwinStartWait, func() bool { pids, err := fcitxProcesses(); return err == nil && len(pids) > 0 }) {
		return fmt.Errorf("寫回 kwinrc 後 KWin 沒有啟動 Fcitx5")
	}
	return nil
}

// finishKWinIM 補寫上次沒寫回的 InputMethod（每次執行更新器都檢查，登入時也做）。剛寫下的紀錄可能屬於另一個
// 正在交接的更新器，30 秒內的不碰。現在已有值（寫回其實成功，或使用者自己選了）就只刪紀錄。
func finishKWinIM(base string) {
	path := kwinPendingPath(base)
	info, err := os.Stat(path)
	if err != nil || time.Since(info.ModTime()) < 30*time.Second {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var p kwinPending
	if json.Unmarshal(raw, &p) != nil || p.Value == "" {
		os.Remove(path)
		return
	}
	env := kconfigEnv(p.Env)
	current, err := readKWinIM(env)
	if err != nil {
		fmt.Fprintln(os.Stderr, "0c-update: 讀不到 kwinrc 的 InputMethod，稍後再補寫：", err)
		return
	}
	if current.Value != "" {
		os.Remove(path)
		return
	}
	conn, done, err := kwinBus()
	if err != nil {
		if kwinWaylandRunning() {
			return // 通知不到執行中的 KWin，下次再補
		}
		conn = nil // 登入時 KWin 還沒啟動：只寫檔，它啟動時會讀到
	} else {
		defer done()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = restoreKWinIM(ctx, conn, base, p.kwinIM, env); err != nil {
		fmt.Fprintln(os.Stderr, "0c-update:", err)
		return
	}
	fmt.Fprintln(os.Stderr, "0c-update: 已把 kwinrc 的 InputMethod 寫回", p.Value)
}

// userProcessRunning：目前使用者有符合 match 的行程（dir 為 /proc/PID，comm 已去掉換行）。
func userProcessRunning(match func(dir, comm string) bool) bool {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return false
	}
	for _, e := range entries {
		dir := filepath.Join(procRoot, e.Name())
		var st syscall.Stat_t
		if _, err := strconv.Atoi(e.Name()); err != nil || syscall.Stat(dir, &st) != nil || int(st.Uid) != os.Getuid() {
			continue
		}
		comm, _ := os.ReadFile(filepath.Join(dir, "comm"))
		if match(dir, strings.TrimSpace(string(comm))) {
			return true
		}
	}
	return false
}

func kwinWaylandRunning() bool {
	return userProcessRunning(func(_, comm string) bool { return comm == "kwin_wayland" })
}

// inFcitxRestartUnit：Fcitx5 跑在更新器以 systemd-run 建立的 0c-fcitx5-*.service 裡（0.2.4 以前的更新器在 KDE
// Plasma Wayland 也這樣重新啟動，原生 Wayland 程式打不了字）。
func inFcitxRestartUnit(dir string) bool {
	raw, err := os.ReadFile(filepath.Join(dir, "cgroup"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		path := line[strings.LastIndexByte(line, ':')+1:]
		unit := filepath.Base(path)
		if strings.HasPrefix(unit, "0c-fcitx5-") && strings.HasSuffix(unit, ".service") {
			return true
		}
	}
	return false
}

// ===== 一次性修復：交還 KWin =====
//
// 0.2.5 由 0.2.4 以前的更新器安裝，它照舊自己啟動 Fcitx5（0c-fcitx5-*.service），KDE Plasma Wayland 的原生程式
// 打不了字，只切換虛擬鍵盤也修不好（KWin 啟動的新 fcitx5 看到已有實例就退出）。新版的穩定更新器由排程喚起時
// （每 15 分鐘）檢查：Plasma Wayland、kwinrc 的虛擬鍵盤是 Fcitx 5 或 Wayland Launcher、唯一的 Fcitx5 在我們的
// 0c-fcitx5-*.service——三者都成立才在沒有組字、閒置時請它退出（同更新的 ExitForUpdate），再交給 KWin 重新啟動。
// 正常狀態（KWin 啟動的 Fcitx5、GNOME、X11）不成立，不會重啟。交還失敗退回直接啟動時記下這次開機，不再重試。

const kwinHandbackIdle = 30 // 秒：最後一個按鍵後多久才請 Fcitx5 退出

type kwinHandback struct {
	Boot    string        `json:"boot"`
	Phase   string        `json:"phase"` // stopping：已請 Fcitx5 退出、還沒交給 KWin；failed：這次開機交還失敗
	Restart *shellRestart `json:"restart,omitempty"`
}

func kwinHandbackPath(base string) string { return filepath.Join(base, "update", "kwin-handback.json") }

func loadKWinHandback(base string) kwinHandback {
	var h kwinHandback
	if raw, err := os.ReadFile(kwinHandbackPath(base)); err == nil {
		_ = json.Unmarshal(raw, &h)
	}
	return h
}

func saveKWinHandback(base string, h kwinHandback) error {
	b, err := json.Marshal(h)
	if err != nil {
		return err
	}
	path := kwinHandbackPath(base)
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err = os.WriteFile(path+".new", b, 0o600); err != nil {
		return err
	}
	return os.Rename(path+".new", path)
}

// noteKWinFallback：交還 KWin 失敗、改為直接啟動。記下這次開機，一次性修復不再重試（否則每次排程都重啟 Fcitx5）。
func noteKWinFallback() {
	if err := saveKWinHandback(kwinStateDir(), kwinHandback{Boot: currentBootID(), Phase: "failed"}); err != nil {
		fmt.Fprintln(os.Stderr, "0c-update: 記錄交還 KWin 失敗：", err)
	}
}

// 測試替換用：一次性修復用到的 D-Bus 操作。
var (
	// handbackPrepare 補上目前的輸入法群組、輸入法與程式（恢復選擇用）；外殼沒有更新保護時回傳錯誤，不重啟。
	handbackPrepare = func(ctx context.Context, conn *dbus.Conn, s *shellRestart) error {
		status, err := guardStatus(ctx, conn)
		if err != nil {
			return err
		}
		if status.Busy {
			return fmt.Errorf("尚有未完成組字")
		}
		if err = controller(ctx, conn, "CurrentInputMethodGroup").Store(&s.Group); err != nil {
			return err
		}
		if err = controller(ctx, conn, "CurrentInputMethod").Store(&s.IM); err != nil {
			return err
		}
		s.Program = status.Program
		return nil
	}
	handbackExit = func(ctx context.Context, conn *dbus.Conn, idle uint64) (string, error) {
		var answer string
		err := conn.Object(guardName, "/update").CallWithContext(ctx, guardInterface+".ExitForUpdate", 0, idle).Store(&answer)
		return answer, err
	}
	handbackRestore = restoreFcitx
)

// repairKWinFcitx 是排程喚起時的一次性修復，見上方說明。任何一步不成立都不動 Fcitx5，下次喚起再看。
func repairKWinFcitx(base string) {
	if _, err := os.Stat(shellJournal(base)); err == nil {
		return // 外殼更新進行中：更新流程自己會交還 KWin
	}
	mark := loadKWinHandback(base)
	pids, err := fcitxProcesses()
	if err != nil {
		return
	}
	if mark.Phase == "stopping" {
		if staleKWinHandback(mark, pids) {
			// 重新開機過（KWin 已照常啟動輸入法），或這次開機已有別人啟動的 Fcitx5：紀錄裡的環境、群組與輸入法
			// 已不適用，留著的話使用者之後自己結束 Fcitx5 時會被拿來重新啟動、改寫 kwinrc。
			os.Remove(kwinHandbackPath(base))
			mark = kwinHandback{}
		} else if len(pids) == 0 {
			// 上次請 Fcitx5 退出後被中止：接著交給 KWin。
			finishKWinHandback(base, mark.Restart)
			return
		}
	}
	if mark.Phase == "failed" && mark.Boot == currentBootID() {
		return
	}
	if len(pids) != 1 || !inFcitxRestartUnit(filepath.Join(procRoot, strconv.Itoa(pids[0]))) || !kwinWaylandRunning() {
		return
	}
	restart, err := captureFcitx(pids[0]) // kwinrc 的虛擬鍵盤是 Fcitx 時記下 KWinIM
	if err != nil || restart.KWinIM == "" {
		return
	}
	conn, done, err := kwinBus()
	if err != nil {
		return
	}
	defer done()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = handbackPrepare(ctx, conn, restart); err != nil {
		return
	}
	if err = saveKWinHandback(base, kwinHandback{Boot: currentBootID(), Phase: "stopping", Restart: restart}); err != nil {
		fmt.Fprintln(os.Stderr, "0c-update: 記錄交還 KWin：", err)
		return
	}
	answer, err := handbackExit(ctx, conn, kwinHandbackIdle)
	if err != nil || answer != "ready" {
		os.Remove(kwinHandbackPath(base)) // 正在打字或組字：下次喚起再試
		return
	}
	if err = waitFcitxStopped(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "0c-update:", err)
		return // 紀錄留著：Fcitx5 之後退出時由下次喚起接手
	}
	fmt.Fprintln(os.Stderr, "0c-update: Fcitx5 由更新器啟動、拿不到 KWin 的輸入法連線；交還 KWin 重新啟動")
	finishKWinHandback(base, restart)
}

// staleKWinHandback：stopping 紀錄只在同一次開機、同一個 Plasma Wayland 工作階段、Fcitx5 還沒被別人重新啟動時有效。
// 開機 ID 不同（或任一邊讀不到）就是重新開機過；沒有 kwin_wayland 就是換到別的工作階段（重新登入 Plasma 時 KWin
// 自己會啟動輸入法）；有不在我們單元裡的 Fcitx5，就是 KWin 或使用者已經啟動了它。這些都不必再交還。
// 我們單元裡的那個是請它退出後還沒結束的舊行程，紀錄留著等它結束。
func staleKWinHandback(mark kwinHandback, pids []int) bool {
	if mark.Restart == nil {
		return true
	}
	if boot := currentBootID(); mark.Boot == "" || boot == "" || mark.Boot != boot {
		return true
	}
	if !kwinWaylandRunning() {
		return true
	}
	for _, pid := range pids {
		if !inFcitxRestartUnit(filepath.Join(procRoot, strconv.Itoa(pid))) {
			return true
		}
	}
	return false
}

func finishKWinHandback(base string, restart *shellRestart) {
	conn, done, err := kwinBus()
	if err != nil {
		fmt.Fprintln(os.Stderr, "0c-update: 無法連線桌面，稍後再交還 KWin：", err)
		return
	}
	defer done()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	err = handbackRestore(ctx, conn, restart)
	if errors.Is(err, context.DeadlineExceeded) || (err != nil && !anyFcitx()) {
		fmt.Fprintln(os.Stderr, "0c-update: 恢復 Fcitx5，稍後再試：", err)
		return // 紀錄留著（stopping），下次喚起再啟動
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "0c-update: 恢復輸入法選擇：", err)
	}
	if h := loadKWinHandback(base); h.Phase == "stopping" {
		os.Remove(kwinHandbackPath(base)) // 交還成功；失敗時 startFcitx 已改記 failed
	}
}

func anyFcitx() bool {
	pids, err := fcitxProcesses()
	return err == nil && len(pids) > 0
}
