//go:build !windows && !darwin

package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/installs"
	"github.com/godbus/dbus/v5"
)

func runtimeDir() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return dir
	}
	return os.TempDir()
}

func dialEngine(timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("unix", filepath.Join(runtimeDir(), "0c", "0c.sock"), timeout)
}

func coreLayout() (autoupdate.CoreLayout, bool) {
	return autoupdate.CoreLayout{Server: "bin/0c-server", Update: "bin/0c-update", Settings: "bin/0c-settings"}, true
}

// Linux 自動完整安裝使用組字保護、引擎交接與 Fcitx5 重載，不需要登出。
func shellUnattended() bool { return true }

// deferShell 回報這位使用者的 Fcitx5 或引擎是否在執行。
func deferShell() bool {
	entries, _ := os.ReadDir("/proc")
	uid := os.Getuid()
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		var st syscall.Stat_t
		if syscall.Stat(filepath.Join("/proc", e.Name()), &st) != nil || int(st.Uid) != uid {
			continue
		}
		comm, _ := os.ReadFile(filepath.Join("/proc", e.Name(), "comm"))
		switch strings.TrimSpace(string(comm)) {
		case "fcitx5", "0c-server", "zuin-server":
			return true
		}
	}
	return false
}
func elevatedOK() bool { return true }
func relaunchElevated([]string) error {
	return fmt.Errorf("不需要提權")
}

// startEngine 以穩定入口啟動引擎；它依 engine.json 轉交到目前的版本目錄。引擎依共用規則找（internal/installs，#181）：
// 官網安裝的 ~/.local/bin/0c-server（不帶參數，與以前相同），沒有時是系統安裝的 libexec/0c/0c-server -data <系統字典>。
//
// 優先請 systemd user manager 以暫時性 service 啟動（#141）：行程由 manager 產生，不繼承呼叫者——
// 自動更新排程 0c-update.service 的 Nice=10 與 idle I/O 類別；直接啟動的引擎會帶著這兩個設定
// 服務到登出，一般使用者也調不回來。新引擎也不留在 0c-update.service 的 cgroup 裡，登出時由
// manager 以 SIGTERM 正常結束（先存檔）。不能用 systemd-run --scope：scope 的行程由呼叫者產生，
// 照樣繼承。沒有 systemd、沒有 user bus（ssh、su、容器）或單元建立失敗時退回直接啟動。
func startEngine(string) error {
	server, args := installs.Default().Server()
	if _, err := os.Stat(server); err == nil {
		err = startEngineUnit(server, args...)
		if err == nil {
			return nil
		}
		fmt.Fprintln(os.Stderr, "0c-update: 改為直接啟動引擎：", err)
	}
	return spawnEngineDirect(server, args...)
}

// 測試替換用。
var (
	lookSystemdRun = func() (string, error) { return exec.LookPath("systemd-run") }
	runSystemdRun  = func(ctx context.Context, path string, args []string) ([]byte, error) {
		return exec.CommandContext(ctx, path, args...).CombinedOutput()
	}
	spawnEngineDirect = spawnEngine
)

func startEngineUnit(server string, args ...string) error {
	path, err := lookSystemdRun()
	if err != nil {
		return err
	}
	// systemd-run 等到 manager 產生行程就返回（Type=simple）；manager 卡住時不能拖住交接。
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if out, err := runSystemdRun(ctx, path, engineUnitArgs(server, time.Now(), os.Getenv, args...)); err != nil {
		return fmt.Errorf("systemd-run：%s (%w)", bytes.TrimSpace(out), err)
	}
	return nil
}

// engineUnitArgs 產生 systemd-run 的參數。單元名稱每次不同：被交接的舊引擎可能還在存檔，
// 它所在的單元還沒卸載，同名會被拒絕；--collect 讓異常結束的單元也自動卸載，不留 failed 紀錄
// 佔住名稱。manager 不傳呼叫者的環境，只帶引擎會讀的 HOME（個人資料與 engine.json）與
// XDG_RUNTIME_DIR（socket），確保與等候交接的 dialEngine 同一個位置。不指定 Nice、
// IOSchedulingClass：沿用 manager 的一般優先權（使用者也無權調低 nice 值，指定 0 反而可能失敗）。
func engineUnitArgs(server string, now time.Time, getenv func(string) string, serverArgs ...string) []string {
	args := []string{"--user", "--collect", "--quiet",
		"--unit=0c-server-" + strconv.FormatInt(now.UnixNano(), 10),
		"--description=靈犀注音引擎"}
	for _, k := range []string{"HOME", "XDG_RUNTIME_DIR"} {
		if v := getenv(k); v != "" {
			args = append(args, "--setenv="+k+"="+v)
		}
	}
	return append(append(args, "--", server), serverArgs...)
}

// spawnEngine 直接啟動引擎。新行程自成一個 session，排程服務結束時不會被一併停止
// （0c-update.service 設 KillMode=process），但會繼承呼叫者的優先權與 cgroup。
func spawnEngine(server string, args ...string) error {
	cmd := exec.Command(server, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		return err
	}
	defer devnull.Close()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, devnull, devnull
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

func gdbus(args ...string) (string, error) {
	out, err := exec.Command("gdbus", append([]string{"call"}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}

// conditions：GNOME 以 Mutter IdleMonitor 取得閒置時間；電池讀 /sys。
func conditions() autoupdate.Conditions {
	var c autoupdate.Conditions
	if out, err := gdbus("--session", "--dest", "org.gnome.Mutter.IdleMonitor", "--object-path", "/org/gnome/Mutter/IdleMonitor/Core",
		"--method", "org.gnome.Mutter.IdleMonitor.GetIdletime"); err == nil {
		// (uint64 12345,)
		f := strings.Fields(strings.Trim(out, "(),"))
		if ms, err := strconv.ParseUint(strings.TrimSuffix(f[len(f)-1], ","), 10, 64); err == nil {
			c.Idle, c.IdleKnown = time.Duration(ms)*time.Millisecond, true
		}
	}
	supplies, _ := filepath.Glob("/sys/class/power_supply/*")
	for _, s := range supplies {
		kind, _ := os.ReadFile(filepath.Join(s, "type"))
		switch strings.TrimSpace(string(kind)) {
		case "Battery":
			if b, err := os.ReadFile(filepath.Join(s, "capacity")); err == nil {
				if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
					c.Battery, c.BatteryKnown = n, true
				}
			}
			if st, _ := os.ReadFile(filepath.Join(s, "status")); strings.TrimSpace(string(st)) == "Charging" || strings.TrimSpace(string(st)) == "Full" {
				c.Charging = true
			}
		case "Mains":
			if b, _ := os.ReadFile(filepath.Join(s, "online")); strings.TrimSpace(string(b)) == "1" {
				c.Charging = true
			}
		}
	}
	if !c.IdleKnown {
		if conn, err := dbus.ConnectSessionBus(); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			if s, err := guardStatus(ctx, conn); err == nil {
				c.Idle = time.Duration(s.Idle) * time.Second
				c.IdleKnown = true
			}
			cancel()
			conn.Close()
		}
	}
	return c
}

const (
	notifyName = "org.freedesktop.Notifications"
	notifyPath = "/org/freedesktop/Notifications"
)

// 通知直接走 D-Bus：按鈕的 ActionInvoked 只單播給送出通知的那條連線，所以送出與等待要用同一條連線
// （notify-send 部分版本也會誤判通知服務不支援按鈕）。
func notifyBus() (*dbus.Conn, dbus.BusObject, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, nil, err
	}
	return conn, conn.Object(notifyName, notifyPath), nil
}

func sendNotice(obj dbus.BusObject, n autoupdate.Notice, actions []string) (uint32, error) {
	var id uint32
	expire := int32(-1) // 通知服務預設
	if len(actions) > 0 {
		expire = 0 // 需要使用者確認的通知不自行消失，直到按下或關閉
	}
	err := obj.Call(notifyName+".Notify", 0, "靈犀注音", uint32(0), "input-keyboard", n.Title, n.Body,
		actions, map[string]dbus.Variant{}, expire).Store(&id)
	return id, err
}

func supportsActions(obj dbus.BusObject) bool {
	var caps []string
	if obj.Call(notifyName+".GetCapabilities", 0).Store(&caps) != nil {
		return false
	}
	for _, c := range caps {
		if c == "actions" {
			return true
		}
	}
	return false
}

func notify(n autoupdate.Notice) error {
	conn, obj, err := notifyBus()
	if err != nil {
		return err
	}
	defer conn.Close()
	if n.Action == "" || !supportsActions(obj) {
		_, err = sendNotice(obj, n, nil)
		return err
	}
	// 有按鈕的通知要等使用者回應：交給獨立的 notify-action 行程（自成 session，不隨排程服務結束）。
	self, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{"notify-action", "-title", n.Title, "-body", n.Body, "-action", n.Action}
	if n.TargetVersion != "" {
		args = append(args, "-target", n.TargetVersion, "-target-sha256", n.TargetSHA256)
	}
	cmd := exec.Command(self, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return startNoticeHelper(cmd, 15*time.Second)
}

// notifyWithAction 顯示有「立即安裝／稍後」按鈕的通知，等到使用者按下（回傳動作名稱）、
// 關閉通知或逾時（回傳空字串）。
func notifyWithAction(ctx context.Context, n autoupdate.Notice) (string, error) {
	return noticeWithActions(ctx, n, []string{n.Action, "立即安裝", "later", "稍後 1 小時"})
}

// noticeWithActions 顯示帶按鈕的通知（actions 是 D-Bus 的「動作代碼, 按鈕文字」成對清單），等使用者按下
// （回傳動作代碼）、關閉通知或逾時（回傳空字串）。ZC_NOTIFY_ACK_FD=3 時在通知服務接受後回報父行程。
func noticeWithActions(ctx context.Context, n autoupdate.Notice, actions []string) (string, error) {
	conn, obj, err := notifyBus()
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if err = conn.AddMatchSignal(dbus.WithMatchInterface(notifyName), dbus.WithMatchObjectPath(notifyPath)); err != nil {
		return "", err
	}
	signals := make(chan *dbus.Signal, 16)
	conn.Signal(signals)
	id, err := sendNotice(obj, n, actions)
	if err != nil {
		return "", err
	}
	// The parent may record success only after the notification service accepts it.
	if os.Getenv("ZC_NOTIFY_ACK_FD") == "3" {
		ack := os.NewFile(3, "notification-delivery")
		if ack == nil {
			return "", fmt.Errorf("通知回報管道不存在")
		}
		_, ackErr := ack.Write([]byte("ready\n"))
		ack.Close()
		if ackErr != nil {
			obj.Call(notifyName+".CloseNotification", 0, id)
			return "", ackErr
		}
	}

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case sig, ok := <-signals:
			if !ok {
				return "", fmt.Errorf("通知服務連線中斷")
			}
			if len(sig.Body) < 2 {
				continue
			}
			if got, _ := sig.Body[0].(uint32); got != id {
				continue
			}
			switch sig.Name {
			case notifyName + ".ActionInvoked":
				action, _ := sig.Body[1].(string)
				return action, nil
			case notifyName + ".NotificationClosed":
				return "", nil
			}
		}
	}
}

const unitName = "0c-update"

func unitDir() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "systemd/user")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config/systemd/user")
}

// installSchedule 建立 systemd user timer：登入後 5 分鐘起每 15 分鐘喚起一次；是否該查詢由協調器依
// 狀態決定（約 6 小時一次），其餘喚起只推進已下載的更新或確認生效。
func installSchedule(base string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	stable := stableUpdater(base)
	if err = replaceUpdater(self, stable); err != nil {
		return err
	}
	service := fmt.Sprintf(`[Unit]
Description=靈犀注音自動更新
After=network-online.target

[Service]
Type=oneshot
ExecStart=%s auto
# 交接後啟動的新引擎要留下來服務，不隨本服務結束。
KillMode=process
Nice=10
IOSchedulingClass=idle
TimeoutStartSec=30min
`, systemdQuote(stable))
	timer := fmt.Sprintf(`[Unit]
Description=靈犀注音自動更新排程

[Timer]
OnStartupSec=5min
OnUnitActiveSec=%ds
AccuracySec=10s

[Install]
WantedBy=timers.target
`, int(scheduleEvery(base).Seconds()))
	// 登入單元：使用者管理員啟動（登入）時、圖形工作階段之前，完成排定的外殼更新。
	login := fmt.Sprintf(`[Unit]
Description=靈犀注音登入時完成排定的更新
Before=graphical-session-pre.target xdg-desktop-autostart.target

[Service]
Type=oneshot
ExecStart=%s auto -login
TimeoutStartSec=5min

[Install]
WantedBy=default.target
`, systemdQuote(stable))
	dir := unitDir()
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, unitName+"-login.service"), []byte(login), 0o600); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, unitName+".service"), []byte(service), 0o600); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, unitName+".timer"), []byte(timer), 0o600); err != nil {
		return err
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", "--now", unitName + ".timer"}, {"enable", unitName + "-login.service"}} {
		if out, err := exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl --user %s：%s (%w)", strings.Join(args, " "), bytes.TrimSpace(out), err)
		}
	}
	fmt.Println("已建立自動更新排程（systemd user timer 0c-update.timer）")
	return nil
}

func systemdQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%").Replace(s) + `"`
}

func removeSchedule() error {
	exec.Command("systemctl", "--user", "disable", "--now", unitName+".timer").Run()
	exec.Command("systemctl", "--user", "disable", unitName+"-login.service").Run()
	dir := unitDir()
	os.Remove(filepath.Join(dir, unitName+"-login.service"))
	os.Remove(filepath.Join(dir, unitName+".timer"))
	os.Remove(filepath.Join(dir, unitName+".service"))
	exec.Command("systemctl", "--user", "daemon-reload").Run()
	fmt.Println("已移除自動更新排程")
	return nil
}

func scheduleStatus() string {
	out, err := exec.Command("systemctl", "--user", "is-active", unitName+".timer").Output()
	state := strings.TrimSpace(string(out))
	if err != nil && state == "" {
		state = "未建立"
	}
	return "排程：" + state
}

// fcitxRunning 回報這位使用者的 Fcitx5 是否在執行（執行中會在結束時寫回 profile，不能直接改）。
func fcitxRunning() bool { return processRunning("fcitx5") }

// inputFramework 決定首次安裝把靈犀注音設定到哪個輸入法框架：已經在用 Fcitx5 的維持 Fcitx5；
// 目前桌面跑的是 IBus（Ubuntu／GNOME 預設）且套件帶有 IBus 外殼時用 IBus；都沒在跑時看裝了哪一個。
func inputFramework() string {
	ibusShell := false
	if _, err := os.Stat(homePath(ibusShellPath)); err == nil {
		ibusShell = true
	}
	_, err := exec.LookPath("fcitx5")
	fcitxInstalled := err == nil
	switch {
	// 以前用過 Fcitx5 的紀錄（profile、環境變數）只在 Fcitx5 還裝著時才算數。
	case fcitxRunning(), fcitxInstalled && (fcitxHasZuin() || os.Getenv("GTK_IM_MODULE") == "fcitx"):
		return "fcitx"
	case ibusShell && (processRunning("ibus-daemon") || os.Getenv("GTK_IM_MODULE") == "ibus"):
		return frontendIBus
	}
	if fcitxInstalled {
		return "fcitx"
	}
	if _, err := exec.LookPath("ibus-daemon"); err == nil && ibusShell {
		return frontendIBus
	}
	return ""
}

// fcitxHasZuin：這位使用者的 Fcitx5 輸入法清單已有靈犀注音（之前就以 Fcitx5 使用）。
func fcitxHasZuin() bool {
	b, err := os.ReadFile(fcitxProfilePath())
	return err == nil && zuinItem.Match(b)
}

var zuinItem = regexp.MustCompile(`(?m)^\s*Name=zuin\s*$`)

// fcitxProfilePath 是這位使用者的 Fcitx5 輸入法清單。
func fcitxProfilePath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "fcitx5/profile")
	}
	return homePath(".config/fcitx5/profile")
}

func processRunning(name string) bool {
	pids, err := inputProcesses(name)
	return err == nil && len(pids) > 0
}

// setupInputMethod 在首次安裝後設定輸入法框架並告訴使用者下一步：IBus 立即登錄，不必登出；
// Fcitx5 把靈犀注音加入輸入法清單（Fcitx5 執行中時改在下次登入、桌面啟動前加入）。
func setupInputMethod(base string) error {
	switch inputFramework() {
	case frontendIBus:
		return setupIBus()
	case "":
		fmt.Println("\n系統尚未安裝 Fcitx5 輸入法框架。請執行下列指令（需要系統管理員密碼），再登出並重新登入：")
		fmt.Println("  " + fcitxInstallHint())
		return markProfileAtLogin(base)
	}
	// 執行中的 Fcitx5 已載入外殼（install.py 請它載入新 addon）時直接加進目前的群組，不必登出（#93）。
	live := false
	if fcitxRunning() {
		if live = shellLoaded(fcitxShellLib) && addInputMethodLive() == nil; !live {
			if err := markProfileAtLogin(base); err != nil {
				return err
			}
		}
	} else if err := addInputMethod(); err != nil {
		return err
	}
	if im := os.Getenv("GTK_IM_MODULE"); im != "" && im != "fcitx" {
		fmt.Printf("\n目前的輸入法框架是 %s。請執行 `im-config -n fcitx5`（或在系統設定改用 Fcitx5），再登出並重新登入。\n", im)
	}
	if live {
		fmt.Println("\n安裝完成。Fcitx5 已載入「靈犀注音」並加入輸入法清單，不必登出；按 Ctrl+Space 切換。")
	} else {
		fmt.Println("\n安裝完成。請登出再登入，然後在 Fcitx5 切換到「靈犀注音」（預設 Ctrl+Space）。")
	}
	fmt.Println("更新會自動進行；版本、更新與設定在 Fcitx5 工作列圖示的「靈犀注音」選單。")
	return nil
}

// setupIBus 以 0c-ibus register 把靈犀注音登錄到目前使用者的 IBus 並加入 GNOME 輸入來源。
func setupIBus() error {
	cmd := exec.Command(homePath(ibusShellPath), "register")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("登錄 IBus 失敗：%w", err)
	}
	fmt.Println("\n安裝完成。以 Super+空白鍵（或系統的輸入來源切換）切換到「靈犀注音」，右 Shift 切換中英。")
	fmt.Println("更新會自動進行；版本、更新與設定在頂列輸入法選單的「靈犀注音」項目。")
	return nil
}

func fcitxInstallHint() string {
	osRelease, _ := os.ReadFile("/etc/os-release")
	id := strings.ToLower(string(osRelease))
	switch {
	case strings.Contains(id, "fedora"), strings.Contains(id, "rhel"):
		return "sudo dnf install fcitx5 fcitx5-gtk fcitx5-qt fcitx5-configtool && imsettings-switch fcitx5"
	case strings.Contains(id, "arch"):
		return "sudo pacman -S fcitx5 fcitx5-gtk fcitx5-qt fcitx5-configtool"
	}
	return "sudo apt install fcitx5 fcitx5-frontend-gtk3 fcitx5-frontend-gtk4 fcitx5-frontend-qt5 fcitx5-config-qt && im-config -n fcitx5"
}

// inputSourcePending：Linux 只在 ProfileAtLogin 時於登入時補加輸入法（macOS 見 auto_darwin.go）。
func inputSourcePending(string) bool { return false }

// retryInputMethod：此平台登入時補做就是 addInputMethod，不分是不是背景排程（macOS 見 auto_darwin.go）。
func retryInputMethod(bool) error { return addInputMethod() }

// addInputMethod 把輸入法 ID zuin 加入 Fcitx5 profile 的第一個群組；沒有 profile 時以系統鍵盤配置建立。
// 修改前備份原檔；已有 zuin 時不動。只能在 Fcitx5 沒有執行時呼叫（首次安裝或登入時、桌面啟動前）。
func addInputMethod() error {
	path := fcitxProfilePath()
	dir := filepath.Dir(path)
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		layout := keyboardLayout()
		profile := fmt.Sprintf("[Groups/0]\nName=Default\nDefault Layout=%s\nDefaultIM=keyboard-%s\n\n[Groups/0/Items/0]\nName=keyboard-%s\nLayout=\n\n"+
			"[Groups/0/Items/1]\nName=zuin\nLayout=\n\n[GroupOrder]\n0=Default\n", layout, layout, layout)
		if err = os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(profile), 0o644)
	}
	if err != nil {
		return err
	}
	text := string(b)
	// 逐段解析：Fcitx5 寫出的 profile 在每個鍵前有註解行。
	header := regexp.MustCompile(`^\[Groups/0/Items/(\d+)\]$`)
	next, section := 0, -1
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			section = -1
			if m := header.FindStringSubmatch(line); m != nil {
				section, _ = strconv.Atoi(m[1])
				if section >= next {
					next = section + 1
				}
			}
			continue
		}
		if section >= 0 && strings.TrimSpace(strings.TrimPrefix(line, "Name=")) == "zuin" && strings.HasPrefix(line, "Name=") {
			return nil
		}
	}
	if err = os.WriteFile(path+".0c-backup", b, 0o644); err != nil {
		return err
	}
	text = strings.TrimRight(text, "\n") + fmt.Sprintf("\n\n[Groups/0/Items/%d]\nName=zuin\nLayout=\n", next)
	tmp := path + ".0c-new"
	if err = os.WriteFile(tmp, []byte(text), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// keyboardLayout 取系統的 X11 鍵盤配置（localectl），預設 us。
func keyboardLayout() string {
	out, _ := exec.Command("localectl", "status").Output()
	for _, line := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), ":"); ok && k == "X11 Layout" {
			if l := strings.TrimSpace(strings.Split(v, ",")[0]); regexp.MustCompile(`^[a-z]{2,8}$`).MatchString(l) {
				return l
			}
		}
	}
	return "us"
}

// scheduleExists：修復只針對 Windows 防毒軟體移除排程的情況；這裡不檢查。
func scheduleExists() bool { return true }

// openScheduleSettings：只有 macOS 的排程可能在系統設定被關閉。
func openScheduleSettings() error {
	return fmt.Errorf("此平台沒有系統設定的背景執行開關")
}

// A separate pipe acknowledges delivery without keeping the scheduler alive
// until the user clicks. stdout remains detached for later update diagnostics.
func startNoticeHelper(cmd *exec.Cmd, timeout time.Duration) error {
	reader, writer, err := os.Pipe()
	if err != nil {
		return err
	}
	defer reader.Close()
	cmd.ExtraFiles = []*os.File{writer}
	cmd.Env = append(cmd.Environ(), "ZC_NOTIFY_ACK_FD=3")
	if err = cmd.Start(); err != nil {
		writer.Close()
		return err
	}
	writer.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	accepted := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(reader).ReadString('\n')
		if err == nil && line != "ready\n" {
			err = fmt.Errorf("通知回報格式錯誤")
		}
		accepted <- err
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err = <-accepted:
		if err == nil {
			return nil
		}
	case <-timer.C:
		err = fmt.Errorf("等候通知服務接受逾時")
	}
	cmd.Process.Kill()
	<-done
	return fmt.Errorf("通知未送達：%w", err)
}
