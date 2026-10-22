//go:build !windows && !darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/update"
	"github.com/godbus/dbus/v5"
	"golang.org/x/sys/unix"
)

func init() {
	// dev.42 以前的工作列外殼使用 pipe 接收更新器輸出。即使換上新版
	// bootstrap，外殼退出仍會關閉讀端；後續安裝訊息不能中斷恢復交易。
	signal.Ignore(syscall.SIGPIPE)
}

func reloadableShell() bool { return true }

const guardName = "org.biggo.ime.Update"
const guardInterface = "org.biggo.ime.Update1"

type shellStatus struct {
	Busy             bool   `json:"busy"`
	Idle             int64  `json:"idle_seconds"`
	Version          string `json:"version"`
	Program          string `json:"program"`
	RestoreSupported bool   `json:"restore_supported"`
	// IBus 外殼（0c-ibus）才有：外殼種類、是否為選用中的輸入法與 ibus-daemon 位址。
	Frontend    string `json:"frontend,omitempty"`
	Active      bool   `json:"active,omitempty"`
	IBusAddress string `json:"ibus_address,omitempty"`
}

func guardStatus(ctx context.Context, conn *dbus.Conn) (shellStatus, error) {
	var out string
	err := conn.Object(guardName, "/update").CallWithContext(ctx, guardInterface+".Status", 0).Store(&out)
	var s shellStatus
	if err == nil {
		err = json.Unmarshal([]byte(out), &s)
	}
	return s, err
}

// shellRestart 保存實際桌面啟動參數。更新器中止後，下次喚起仍會恢復輸入法；
// 個人詞庫不屬於此交易，不在回復時覆蓋。
type shellRestart struct {
	// Frontend 是被更新的外殼：空字串為 Fcitx5（舊紀錄也是），ibus 為 0c-ibus。
	Frontend   string   `json:"frontend,omitempty"`
	Address    string   `json:"ibus_address,omitempty"` // IBus：外殼回報的 ibus-daemon 位址
	Args       []string `json:"args"`
	Env        []string `json:"env"`
	Group      string   `json:"group"`
	IM         string   `json:"im"`
	Program    string   `json:"program,omitempty"`
	OldVersion string   `json:"old_version"`
	Backup     string   `json:"backup,omitempty"`
	Phase      string   `json:"phase"`
	// KWinIM：Fcitx5 由 KDE Plasma Wayland 的 KWin 啟動時，kwinrc [Wayland] InputMethod 的值（.desktop 路徑）。
	// 有值就交還 KWin 重新啟動，不自己執行 fcitx5。
	KWinIM string `json:"kwin_input_method,omitempty"`
	// KWinIMInherited：KWinIM 來自系統預設（使用者的 kwinrc 沒有這個鍵），寫回時刪掉鍵而不是寫死。
	KWinIMInherited bool `json:"kwin_input_method_inherited,omitempty"`
}

func shellJournal(base string) string { return filepath.Join(base, "update", "shell-restart.json") }
func saveShellRestart(base string, s *shellRestart) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	path := shellJournal(base)
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err = os.WriteFile(path+".new", b, 0600); err != nil {
		return err
	}
	return os.Rename(path+".new", path)
}

func fcitxProcesses() ([]int, error) { return inputProcesses("fcitx5") }

// procRoot 是行程資訊的來源；測試以假目錄取代。
var procRoot = "/proc"

type inputProc struct {
	pid, ppid int
	start     string // /proc/PID/stat 的 starttime：PID 被重用時不會相同
}

// settle 是確認多個同名行程前的等待；測試替換成直接改假 /proc。
var settle = func() { time.Sleep(150 * time.Millisecond) }

// inputProcesses 回傳目前使用者名為 names 的行程，排除兩種外殼的暫時子行程：
//
// 外殼（lib0c.so，跑在 Fcitx5 裡）用 posix_spawn 啟動 0c-update status --json、用兩次 fork 啟動 0c-server，
// 子行程在 exec 之前沿用父行程的 comm（「fcitx5」），那幾毫秒內會被數成第二個 Fcitx5，更新器就回報「有多個
// Fcitx5 工作階段」而不套用。posix_spawn 的子行程父行程仍是 Fcitx5，直接排除；兩次 fork 的孫行程在中間層
// 退出後由 init 或 systemd --user 接手，父行程不在名單內，只能靠時間分辨：找到不只一個時隔一小段時間重掃兩次，
// 只留每次都在、啟動時間相同的行程。暫時子行程很快 exec 成別的名字；另一個桌面的 Fcitx5 會一直在，仍各算一個。
func inputProcesses(names ...string) ([]int, error) {
	found, err := scanInputProcesses(names)
	if err != nil {
		return nil, err
	}
	pids := withoutForkChildren(found)
	if len(pids) <= 1 {
		return pids, nil
	}
	for range 2 {
		settle()
		again, err := scanInputProcesses(names)
		if err != nil {
			return nil, err
		}
		still := make(map[inputProc]bool, len(again))
		for _, p := range again {
			still[inputProc{pid: p.pid, start: p.start}] = true
		}
		kept := found[:0]
		for _, p := range found {
			if still[inputProc{pid: p.pid, start: p.start}] {
				kept = append(kept, p)
			}
		}
		found = kept
	}
	return withoutForkChildren(found), nil
}

func scanInputProcesses(names []string) ([]inputProc, error) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, err
	}
	var found []inputProc
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		var st syscall.Stat_t
		dir := filepath.Join(procRoot, e.Name())
		if syscall.Stat(dir, &st) != nil || int(st.Uid) != os.Getuid() {
			continue
		}
		comm, _ := os.ReadFile(filepath.Join(dir, "comm"))
		stat, _ := os.ReadFile(filepath.Join(dir, "stat"))
		// comm 可含空白與括號，從最後一個「) 」起算。已退出、等待父程序收屍的行程不再持有外殼。
		raw := string(stat)
		i := strings.LastIndex(raw, ") ")
		if i < 0 {
			continue
		}
		f := strings.Fields(raw[i+2:])
		if len(f) == 0 || f[0] == "Z" {
			continue
		}
		p := inputProc{pid: pid}
		if len(f) > 1 {
			p.ppid, _ = strconv.Atoi(f[1])
		}
		if len(f) > 19 {
			p.start = f[19]
		}
		for _, name := range names {
			if strings.TrimSpace(string(comm)) == name {
				found = append(found, p)
				break
			}
		}
	}
	return found, nil
}

// withoutForkChildren 排除父行程也在名單內的行程（posix_spawn／單次 fork 的子行程）。
func withoutForkChildren(found []inputProc) []int {
	in := make(map[int]bool, len(found))
	for _, p := range found {
		in[p.pid] = true
	}
	var pids []int
	for _, p := range found {
		if !in[p.ppid] {
			pids = append(pids, p.pid)
		}
	}
	return pids
}

func captureFcitx(pid int) (*shellRestart, error) {
	dir := filepath.Join(procRoot, strconv.Itoa(pid))
	raw, err := os.ReadFile(filepath.Join(dir, "cmdline"))
	if err != nil {
		return nil, err
	}
	parts := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
	if len(parts) == 0 || parts[0] == "" {
		return nil, fmt.Errorf("無法讀取 Fcitx5 啟動參數")
	}
	s := &shellRestart{Args: parts[1:], Phase: "stopping"}
	raw, err = os.ReadFile(filepath.Join(dir, "environ"))
	if err != nil {
		return nil, err
	}
	// 保存原桌面與使用者明確啟用的紀錄偏好，不保存 token 或其他應用程式環境。
	keys := map[string]bool{"DISPLAY": true, "WAYLAND_DISPLAY": true, "XAUTHORITY": true, "DBUS_SESSION_BUS_ADDRESS": true,
		"XDG_RUNTIME_DIR": true, "XDG_CONFIG_HOME": true, "XDG_DATA_HOME": true, "XDG_CACHE_HOME": true, "XDG_STATE_HOME": true, "FCITX_ADDON_DIRS": true,
		"ZUIN_TRACE": true, "ZUIN_TRACE_DIR": true}
	kwin := false
	for _, pair := range strings.Split(string(raw), "\x00") {
		key, _, _ := strings.Cut(pair, "=")
		if keys[key] {
			s.Env = append(s.Env, pair)
		}
		// KWin 以 socketpair 啟動輸入法，只有它給的 WAYLAND_SOCKET 能提供 Wayland 輸入法協定；這個 fd 無法沿用。
		kwin = kwin || key == "WAYLAND_SOCKET"
	}
	// 只有 KDE Plasma Wayland 走 KWin 路徑，「虛擬鍵盤」有兩種選法：
	//   - Fcitx 5：KWin 直接啟動 fcitx5，帶 WAYLAND_SOCKET、父行程是 kwin_wayland；
	//   - Fcitx 5 Wayland Launcher：KWin 啟動 fcitx5-wayland-launcher，由它經 D-Bus 叫起 fcitx5 再交出連線，
	//     fcitx5 本身不帶 WAYLAND_SOCKET，父行程也不是 KWin；Fcitx5 結束時 launcher 跟著正常結束，KWin 不會重開。
	// 其他會用 WAYLAND_SOCKET 啟動輸入法的合成器（例如 Weston）和 GNOME、X11 等桌面照原本的方式重新啟動。
	// 0.2.4 以前的更新器在 Plasma Wayland 也自己啟動 Fcitx5（0c-fcitx5-*.service）：虛擬鍵盤仍選 Fcitx 時同樣交還 KWin，
	// 這次更新順便修好（一次性修復見 repairKWinFcitx）。
	byKWin := kwin && parentComm(dir) == "kwin_wayland" || kwinLauncherRunning()
	stray := !byKWin && inFcitxRestartUnit(dir) && kwinWaylandRunning()
	if byKWin || stray {
		im, err := readKWinIM(kconfigEnv(s.Env))
		switch {
		case err != nil || im.Value == "":
			if byKWin {
				fmt.Fprintln(os.Stderr, "0c-update: Fcitx5 由 KWin 啟動，但讀不到 kwinrc 的 InputMethod：", err)
			}
		case stray && !im.fcitx(): // 虛擬鍵盤不是 Fcitx：照原本的方式重新啟動
		default:
			s.KWinIM, s.KWinIMInherited = im.Value, im.Inherited
		}
	}
	return s, nil
}

// kwinLauncherRunning：目前使用者有 KWin 啟動的 fcitx5-wayland-launcher（comm 截成 15 字元：fcitx5-wayland-）。
func kwinLauncherRunning() bool {
	return userProcessRunning(func(dir, comm string) bool {
		return strings.HasPrefix(comm, "fcitx5-wayland") && parentComm(dir) == "kwin_wayland"
	})
}

// parentComm 回傳 /proc/PID（dir）父行程的名稱；讀不到時為空字串。
func parentComm(dir string) string {
	stat, err := os.ReadFile(filepath.Join(dir, "stat"))
	if err != nil {
		return ""
	}
	raw := string(stat)
	i := strings.LastIndex(raw, ") ")
	if i < 0 {
		return ""
	}
	f := strings.Fields(raw[i+2:])
	if len(f) < 2 {
		return ""
	}
	comm, err := os.ReadFile(filepath.Join(procRoot, f[1], "comm"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(comm))
}

func controller(ctx context.Context, conn *dbus.Conn, method string, args ...any) *dbus.Call {
	// 重啟後輪詢 State 時，禁止 D-Bus 搶先以 .service 的預設環境啟動 Fcitx5。
	// 必須等待 startFcitx 啟動的行程取得 bus name，才能保留原桌面／addon 路徑。
	return conn.Object("org.fcitx.Fcitx5", "/controller").CallWithContext(ctx, "org.fcitx.Fcitx.Controller1."+method, dbus.FlagNoAutoStart, args...)
}

func waitFcitxStopped(ctx context.Context) error {
	deadline := time.Now().Add(10 * time.Second)
	for {
		pids, err := fcitxProcesses()
		if err != nil {
			return err
		}
		if len(pids) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("Fcitx5 未完成交接；未替換安裝檔案")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func waitServersStopped(ctx context.Context) error {
	deadline := time.Now().Add(10 * time.Second)
	for {
		pids, err := inputProcesses("0c-server", "zuin-server")
		if err != nil {
			return err
		}
		if len(pids) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("引擎已交接但尚未退出；保留原安裝")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func startFcitx(conn *dbus.Conn, s *shellRestart) error {
	pids, err := fcitxProcesses()
	if err != nil || len(pids) > 0 {
		return err
	}
	if s.KWinIM != "" {
		im := kwinIM{Value: s.KWinIM, Inherited: s.KWinIMInherited}
		if err := restartKWinIM(context.Background(), conn, kwinStateDir(), im, kconfigEnv(s.Env)); err == nil {
			return nil
		} else {
			fmt.Fprintln(os.Stderr, "0c-update: 無法交由 KWin 重新啟動 Fcitx5，改為直接啟動：", err)
			noteKWinFallback()
		}
		if pids, err := fcitxProcesses(); err != nil || len(pids) > 0 {
			return err // KWin 晚了一步才啟動
		}
	}
	args := []string{}
	for _, arg := range s.Args {
		if arg != "-D" && arg != "-d" && arg != "-r" && arg != "--replace" {
			args = append(args, arg)
		}
	}
	home, _ := os.UserHomeDir()
	env := fcitxRestartEnv(withoutWaylandSocket(append(os.Environ(), s.Env...)), home)
	// 交給 user manager 產生行程：直接啟動的 Fcitx5 會帶著自動更新排程 0c-update.service 的 Nice=10 與
	// idle I/O 留在它的 cgroup 裡，之後外殼自己啟動的引擎（設定 -apply、當掉後、引擎服務的退路）也跟著
	// 低優先權（#141 的另一條路）。沒有 systemd-run 或建立失敗時照舊直接啟動。
	if err := startFcitxUnit(args, env, time.Now()); err == nil {
		return nil
	} else {
		fmt.Fprintln(os.Stderr, "0c-update: 改為直接啟動 Fcitx5：", err)
	}
	args = append(args, "-d")
	cmd := exec.Command("fcitx5", args...)
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer null.Close()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = null, null, null
	if err = cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

// startFcitxUnit 以暫時性 service 在前景執行 Fcitx5（不加 -d：daemon 化後主行程結束，service 就停了）。
// KillMode=process：Fcitx5 結束時不連帶收掉它啟動的引擎（引擎收到 SIGTERM 雖會存檔，但沒有必要），
// 與原本直接啟動時一樣。輸出不進 journal（原本也是 /dev/null）。
func startFcitxUnit(args, env []string, now time.Time) error {
	path, err := lookSystemdRun()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if out, err := runSystemdRun(ctx, path, fcitxUnitArgs(args, env, now)); err != nil {
		return fmt.Errorf("systemd-run：%s (%w)", bytes.TrimSpace(out), err)
	}
	return nil
}

// fcitxUnitArgs：manager 不傳呼叫者的環境，所以把原本要給 Fcitx5 的環境逐一帶上（systemd 不接受的
// 變數名稱或內容略過）。單元名稱每次不同，舊的還在卸載時不會撞名。
func fcitxUnitArgs(args, env []string, now time.Time) []string {
	out := []string{"--user", "--collect", "--quiet", "--unit=0c-fcitx5-" + strconv.FormatInt(now.UnixNano(), 10),
		"--description=Fcitx5（靈犀注音更新後重新啟動）", "--property=KillMode=process",
		"--property=StandardOutput=null", "--property=StandardError=null"}
	seen := map[string]int{}
	var kept []string
	for _, pair := range env {
		key, _, ok := strings.Cut(pair, "=")
		if !ok || !validEnvName(key) || strings.ContainsAny(pair, "\n\x00") {
			continue
		}
		if i, dup := seen[key]; dup {
			kept[i] = pair // 後面的優先，與 exec 的 Env 相同
			continue
		}
		seen[key] = len(kept)
		kept = append(kept, pair)
	}
	for _, pair := range kept {
		out = append(out, "--setenv="+pair)
	}
	return append(append(out, "--", "fcitx5"), args...)
}

func validEnvName(key string) bool {
	if key == "" || (key[0] >= '0' && key[0] <= '9') {
		return false
	}
	for _, c := range key {
		if !(c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func restoreFcitx(ctx context.Context, conn *dbus.Conn, s *shellRestart) error {
	if err := startFcitx(conn, s); err != nil {
		return err
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		if controller(ctx, conn, "State").Err == nil {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("Fcitx5 未在期限內恢復")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return restoreInputSelection(ctx, conn, s)
}

func restoreInputSelection(ctx context.Context, conn *dbus.Conn, s *shellRestart) error {
	if s.Group != "" {
		var current string
		if err := controller(ctx, conn, "CurrentInputMethodGroup").Store(&current); err != nil {
			return err
		}
		if current != s.Group {
			if err := controller(ctx, conn, "SwitchInputMethodGroup", s.Group).Err; err != nil {
				return err
			}
		}
	}
	if s.IM != "" {
		if err := controller(ctx, conn, "SetCurrentIM", s.IM).Err; err != nil {
			return err
		}
		// 名稱設定可能早於 GTK／IBus 重新連線。新版外殼會在原程式重新取得
		// 焦點或第一個按鍵前恢復；舊版回復仍使用原控制器協定。
		status, err := guardStatus(ctx, conn)
		if err == nil && status.RestoreSupported && s.Program != "" {
			return conn.Object(guardName, "/update").CallWithContext(ctx,
				guardInterface+".RestoreInputMethod", dbus.FlagNoAutoStart, s.Program, s.IM).Err
		}
	}
	return nil
}

func (p *system) installReloadableShell(ctx context.Context, root string, r *update.Release) (bool, error) {
	// 驗證與解壓均在停止桌面輸入前完成。
	if out, err := exec.CommandContext(ctx, filepath.Join(root, "bin/0c-server"), "-check-data", "-data", filepath.Join(root, "data")).CombinedOutput(); err != nil {
		return false, fmt.Errorf("新版引擎隔離檢查失敗：%s (%w)", strings.TrimSpace(string(out)), err)
	}
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return false, fmt.Errorf("無法連線桌面：%w", err)
	}
	defer conn.Close()
	callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	status, guardErr := guardStatus(callCtx, conn)
	cancel()
	// 已生效重跑：核對載入外殼及引擎，不能只看收據。
	if guardErr == nil && status.Version == r.Version {
		v, running, e := p.Running(ctx)
		if e == nil && !running {
			e = p.StartEngine(ctx)
			v, running, _ = p.Running(ctx)
		}
		if e == nil && running && v == r.Version {
			os.Remove(shellJournal(p.base))
			return false, nil
		}
	}
	var restart shellRestart
	raw, journalErr := os.ReadFile(shellJournal(p.base))
	if journalErr == nil {
		if err = json.Unmarshal(raw, &restart); err != nil {
			return false, err
		}
	} else if !os.IsNotExist(journalErr) {
		return false, journalErr
	}
	pids, err := fcitxProcesses()
	if err != nil {
		return false, err
	}
	// 沒有 Fcitx5、使用者登錄了 IBus 外殼：由 IBus 路徑處理（外殼沒在執行時也能直接換檔，不必等登入）。
	if restart.Frontend == frontendIBus || (len(pids) == 0 && os.IsNotExist(journalErr) && ibusRegistered()) {
		return p.installIBusShell(ctx, conn, root, r, restart, journalErr == nil)
	}
	if len(pids) == 0 && os.IsNotExist(journalErr) {
		installed, _ := installedVersion()
		if installed != r.Version {
			if err = install(root, r); err != nil {
				return false, err
			}
			afterInstall()
		}
		return true, nil // 登入前尚無桌面，由後續喚起確認載入。
	}
	if len(pids) > 1 {
		return false, fmt.Errorf("有多個 Fcitx5 工作階段，請保留一個後再更新")
	}
	if len(pids) == 1 && os.IsNotExist(journalErr) && !shellInUse(conn) {
		return p.installUnusedShell(ctx, root, r)
	}
	// 任一失敗也要恢復原桌面的輸入來源。恢復使用獨立 deadline，不受取消的安裝 context 影響。
	defer func() {
		if restart.Phase != "" {
			recovery, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if e := restoreFcitx(recovery, conn, &restart); e != nil {
				fmt.Fprintln(os.Stderr, "0c-update: 恢復輸入法：", e)
			}
		}
	}()
	if len(pids) == 1 {
		info, running, e := autoupdate.Health(dialEngine)
		if e != nil {
			return false, fmt.Errorf("%w：無法確認引擎狀態", autoupdate.ErrShellBusy)
		}
		if running && info["composing"] != "0" {
			return false, fmt.Errorf("%w：請先完成組字，稍後會自動再試", autoupdate.ErrShellBusy)
		}
		if guardErr != nil {
			// dev.40 以前外殼沒有全欄位保護，只允許使用者手動遷移，且目前需為靈犀注音。
			// 自動模式不猜測其他輸入法的組字狀態。
			var im string
			if !p.consent {
				return false, autoupdate.ErrNeedsConsent
			}
			if controller(ctx, conn, "CurrentInputMethod").Store(&im) != nil || im != "zuin" || !running {
				return false, fmt.Errorf("%w：舊外殼首次更新請先切到靈犀注音，完成一次輸入後再按完成更新", autoupdate.ErrShellBusy)
			}
		} else if status.Busy {
			return false, fmt.Errorf("%w：Fcitx5 尚有未完成組字", autoupdate.ErrShellBusy)
		}
		captured, e := captureFcitx(pids[0])
		if e != nil {
			return false, e
		}
		if err = controller(ctx, conn, "CurrentInputMethodGroup").Store(&captured.Group); err != nil {
			return false, err
		}
		if err = controller(ctx, conn, "CurrentInputMethod").Store(&captured.IM); err != nil {
			return false, err
		}
		captured.Program = status.Program
		captured.OldVersion, _ = p.Installed()
		if journalErr == nil {
			captured.OldVersion = restart.OldVersion
			captured.Backup = restart.Backup
		}
		if err = saveShellRestart(p.base, captured); err != nil {
			return false, err
		}
		restart = *captured
		if guardErr == nil {
			needed := p.shellIdle
			if p.consent {
				needed = 0
			}
			var answer string
			callCtx, cancel = context.WithTimeout(ctx, 3*time.Second)
			err = conn.Object(guardName, "/update").CallWithContext(callCtx, guardInterface+".ExitForUpdate", 0, uint64(needed/time.Second)).Store(&answer)
			cancel()
			if err != nil || answer != "ready" {
				remaining, _ := fcitxProcesses()
				if len(remaining) > 0 {
					restart.Phase = ""
				}
				os.Remove(shellJournal(p.base))
				return false, fmt.Errorf("%w：Fcitx5 正在使用，保留新版稍後再試（%s；%v）", autoupdate.ErrShellBusy, answer, err)
			}
		} else {
			if err = controller(ctx, conn, "Exit").Err; err != nil {
				os.Remove(shellJournal(p.base))
				return false, err
			}
		}
		if err = waitFcitxStopped(ctx); err != nil {
			return false, err
		}
	}

	if err = p.Drain(ctx); err == nil {
		err = waitServersStopped(ctx)
	}
	if err != nil {
		return false, fmt.Errorf("%w：%v", autoupdate.ErrShellBusy, err)
	}
	restart.Phase = "installing"
	if err = saveShellRestart(p.base, &restart); err != nil {
		return false, err
	}
	// 安裝器自身逐檔備份／回復，個人詞庫維持原本內容。
	installed, _ := installedVersion()
	if installed != r.Version {
		if err = installLiveBundle(ctx, root, r); err != nil {
			return false, err
		}
		var receipt struct {
			Backup string `json:"backup"`
		}
		if b, e := os.ReadFile(receiptPath()); e == nil {
			_ = json.Unmarshal(b, &receipt)
		}
		restart.Backup = receipt.Backup
	}
	if restart.Backup == "" && restart.OldVersion != "" && restart.OldVersion != r.Version {
		var receipt struct {
			Backup string `json:"backup"`
		}
		if b, e := os.ReadFile(receiptPath()); e == nil {
			_ = json.Unmarshal(b, &receipt)
			restart.Backup = receipt.Backup
		}
	}
	restart.Phase = "verifying"
	if err = saveShellRestart(p.base, &restart); err != nil {
		return false, err
	}
	if err = p.StartEngine(ctx); err == nil {
		err = restoreFcitx(ctx, conn, &restart)
	}
	if err == nil {
		deadline := time.Now().Add(5 * time.Second)
		for {
			status, err = guardStatus(ctx, conn)
			if err == nil && status.Version == r.Version {
				break
			}
			// 舊套件尚無 guard；以實際映射的 inode 與已驗證外殼 SHA 核對。
			if err != nil && loadedShellMatches(r) {
				err = nil
				break
			}
			if time.Now().After(deadline) {
				err = fmt.Errorf("載入外殼版本 %q，預期 %s", status.Version, r.Version)
				break
			}
			select {
			case <-ctx.Done():
				err = ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
			if err != nil && ctx.Err() != nil {
				break
			}
		}
	}
	if err == nil {
		v, running, e := p.Running(ctx)
		if e != nil || !running || v != r.Version {
			err = fmt.Errorf("運作引擎 %q，預期 %s：%v", v, r.Version, e)
		}
	}
	if err != nil {
		// 新版未通過核對：先以相同組字保護退出，再回復安裝備份。
		recovery, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		restart.Phase = "rollback"
		if e := saveShellRestart(p.base, &restart); e != nil {
			return false, fmt.Errorf("%v；無法記錄回復：%v", err, e)
		}
		if e := p.rollbackShell(recovery, conn, &restart); e != nil {
			return false, fmt.Errorf("%v；回復尚未完成：%w", err, e)
		}

		os.Remove(shellJournal(p.base))
		return false, fmt.Errorf("%w：%v", autoupdate.ErrShellHealth, err)
	}
	afterInstall()
	os.Remove(shellJournal(p.base))
	restart.Phase = "" // 已恢復且核對成功，不再重複設定輸入來源。
	fmt.Printf("已更新至 %s，Fcitx5 已重新載入，不需要登出。\n", r.Version)
	return false, nil
}

// rollbackShell 拒絕中斷任何輸入法的組字；受阻時保留 journal，下一次喚起接續回復。
func (p *system) rollbackShell(ctx context.Context, conn *dbus.Conn, restart *shellRestart) error {
	if restart.Backup == "" {
		return fmt.Errorf("缺少完整安裝備份")
	}
	pids, err := fcitxProcesses()
	if err != nil {
		return err
	}
	if len(pids) > 1 {
		return fmt.Errorf("有多個 Fcitx5 工作階段")
	}
	if len(pids) == 1 {
		var answer string
		callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err = conn.Object(guardName, "/update").CallWithContext(callCtx, guardInterface+".ExitForUpdate", 0, uint64(0)).Store(&answer)
		cancel()
		if err != nil && p.consent {
			info, running, e := autoupdate.Health(dialEngine)
			var im string
			if e == nil && (!running || info["composing"] == "0") && controller(ctx, conn, "CurrentInputMethod").Store(&im) == nil && im == "zuin" {
				err = controller(ctx, conn, "Exit").Err
				if err == nil {
					answer = "ready"
				}
			}
		}
		if err != nil || answer != "ready" {
			return fmt.Errorf("%w：請完成組字後再回復（%s；%v）", autoupdate.ErrShellBusy, answer, err)
		}
		if err = waitFcitxStopped(ctx); err != nil {
			return err
		}
	}
	if err = p.Drain(ctx); err != nil {
		return err
	}
	if err = waitServersStopped(ctx); err != nil {
		return err
	}
	// 中途已回復檔案而未恢復桌面時，不再重複還原同一備份。
	installed, _ := installedVersion()
	if installed != restart.OldVersion {
		if err = rollbackLiveBundle(ctx, p.base, restart.Backup); err != nil {
			return err
		}
	}
	if err = p.StartEngine(ctx); err != nil {
		return err
	}
	if err = restoreFcitx(ctx, conn, restart); err != nil {
		return err
	}
	version, running, err := p.Running(ctx)
	if err != nil || !running || version != restart.OldVersion {
		return fmt.Errorf("原版引擎未恢復：%s；%v", version, err)
	}
	return nil
}

func (p *system) RecoverShell(ctx context.Context) (bool, error) {
	raw, err := os.ReadFile(shellJournal(p.base))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var restart shellRestart
	if err = json.Unmarshal(raw, &restart); err != nil {
		return false, err
	}
	if restart.Phase != "rollback" {
		state, err := autoupdate.Load(p.base)
		if err != nil {
			return false, err
		}
		if state.Target == nil || state.Download == "" {
			return false, fmt.Errorf("外殼交易尚未完成但缺少套件；保留備份")
		}
		pending, err := p.InstallShell(ctx, state.Download)
		return !pending && err == nil, err
	}
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return false, err
	}
	defer conn.Close()
	rollback := p.rollbackShell
	if restart.Frontend == frontendIBus {
		rollback = p.rollbackIBusShell
	}
	if err = rollback(ctx, conn, &restart); err != nil {
		return false, err
	}
	os.Remove(shellJournal(p.base))
	return false, fmt.Errorf("%w：已恢復 %s", autoupdate.ErrShellHealth, restart.OldVersion)
}

// loadedShellMatches 核對 Fcitx5 實際映射的是已安裝的新 inode（非 deleted 舊檔）。
// 供 dev.40 以前套件使用；新版另以 guard 的版本回覆核對。
func loadedShellMatches(r *update.Release) bool {
	want := r.Files["lib/lib0c.so"]
	got, err := update.Hash(homePath(fcitxShellLib))
	return err == nil && want != "" && got == want && shellLoaded(fcitxShellLib)
}

func mappedShell(data []byte, path string, inode, device uint64) bool {
	dev := fmt.Sprintf("%02x:%02x", unix.Major(device), unix.Minor(device))
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		if fields[3] == dev && fields[4] == strconv.FormatUint(inode, 10) && strings.Join(fields[5:], " ") == path {
			return true
		}
	}
	return false
}

func installLiveBundle(ctx context.Context, root string, r *update.Release, env ...string) error {
	if _, legacy := r.Files[update.LinuxLegacyServer]; legacy {
		return fmt.Errorf("舊 zuin-* 套件請使用 rollback")
	}
	cmd := exec.CommandContext(ctx, "python3", filepath.Join(root, "install.py"), "install")
	cmd.Env = append(append(os.Environ(), "ZC_MANAGED_RELOAD=1"), env...)
	out, err := cmd.CombinedOutput()
	// 舊版安裝器不認得 managed reload 的提示設定。
	text := strings.ReplaceAll(string(out), "登出再登入後，Fcitx5 會載入「靈犀注音」。", "更新器將重新載入輸入法並核對生效。")
	fmt.Print(text)
	if err != nil {
		return fmt.Errorf("完整安裝失敗：%s (%w)", strings.TrimSpace(text), err)
	}
	return nil
}

func rollbackLiveBundle(ctx context.Context, base, backup string) error {
	script := filepath.Join(base, "tools/install.py")
	err := withPointerSetAside(base, backup, func() error {
		cmd := exec.CommandContext(ctx, "python3", script, "rollback", "--backup", backup)
		cmd.Env = append(os.Environ(), "ZC_MANAGED_RELOAD=1")
		out, err := cmd.CombinedOutput()
		text := strings.ReplaceAll(string(out), "重新登入後生效。", "套件已回復，更新器正在恢復輸入法。")
		fmt.Print(text)
		if err != nil {
			return fmt.Errorf("回復套件失敗：%s (%w)", strings.TrimSpace(text), err)
		}
		return nil
	})
	if err == nil {
		afterRollback(base, removeSchedule)
	}
	return err
}
