//go:build windows

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"git.biggo.com/0c/ime/internal/autoupdate"
	"git.biggo.com/0c/ime/internal/enginedir"
	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func pipeName() string {
	if u, err := user.Current(); err == nil && u.Uid != "" {
		return `\\.\pipe\0c-` + strings.ToLower(u.Uid)
	}
	return `\\.\pipe\0c`
}

func dialEngine(timeout time.Duration) (net.Conn, error) { return winio.DialPipe(pipeName(), &timeout) }

// Windows 的 0c-update 在版本目錄與穩定副本，沒有另一份 PATH 上的副本。核心更新可能剛帶來設定程式，
// 所以順便讓開始功能表的項目跟上。
// 背景更新由舊版的穩定更新器安裝新版；新版的更新器下一次執行時補上「已安裝的應用程式」的版本號（#82）。
func refreshPathUpdater(string) { ensureSettingsEntry(); syncUninstallEntry() }

func coreLayout() (autoupdate.CoreLayout, bool) {
	return autoupdate.CoreLayout{Server: "0c-server.exe", Update: "0c-update.exe", Settings: "0c-settings.exe", MCP: "0c-mcp.exe"}, true
}

// 外殼更新要改 TSF 註冊，需要系統管理員；排程以一般權限執行，所以要使用者確認。
func shellUnattended() bool { return windows.GetCurrentProcessToken().IsElevated() }
func elevatedOK() bool      { return windows.GetCurrentProcessToken().IsElevated() }

// HandsOverEngineBeforeRelogin（autoupdate.PendingShellEngineHandover）：外殼更新等重新登入時，常駐引擎先換新版。
// 背景完整更新可能是舊的穩定更新器裝的（例如 dev.64 → 0.2.0），它不會交接；新版下一次喚起時由協調器補上。
// 新引擎由 StartEngine 經登入工作叫起，不繼承自動更新工作的低優先順序，也不會以系統管理員權限執行。
func (p *system) HandsOverEngineBeforeRelogin() bool { return true }

const createBreakawayFromJob = 0x01000000

// startEngine 啟動常駐引擎（不閒置結束：沙箱應用程式如 Windows 搜尋框裡的 DLL 無法自己啟動引擎）。
// 有登入排程時交給它啟動：自動更新排程以低優先順序執行，直接啟動的引擎除了 CPU 優先順序，
// 還會繼承低 I/O 與記憶體優先順序，而且整個登入期間都不會結束。
func startEngine(base string) error {
	if taskExists(engineTaskName()) {
		if err := quietCommand(schtasksPath(), "/Run", "/TN", engineTaskName()).Run(); err == nil {
			return nil
		}
	}
	return spawnEngine(base)
}

// spawnEngine 直接啟動引擎指標所指的版本（沒有指標時用收據的版本目錄），參數與 TSF DLL 相同。
// 排程工作結束時不帶走它：優先脫離工作排程器的 job；明確用一般優先順序，不繼承排程的 CPU 優先順序。
func spawnEngine(base string) error {
	exe, data, ok := enginedir.Resolve(base)
	if !ok {
		b, err := os.ReadFile(receiptPath())
		if err != nil {
			return err
		}
		var r struct {
			Directory string `json:"directory"`
		}
		if err = json.Unmarshal(b, &r); err != nil || !strings.HasPrefix(r.Directory, "versions/") {
			return fmt.Errorf("找不到目前的引擎")
		}
		dir := filepath.Join(base, filepath.FromSlash(r.Directory))
		exe, data = filepath.Join(dir, "0c-server.exe"), filepath.Join(dir, "data")
	}
	const flagsBase = windows.CREATE_NO_WINDOW | windows.NORMAL_PRIORITY_CLASS
	for _, flags := range []uint32{flagsBase | createBreakawayFromJob, flagsBase} {
		cmd := exec.Command(exe, "-data", data)
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags, HideWindow: true}
		if err := cmd.Start(); err == nil {
			go cmd.Wait()
			return nil
		} else if flags == flagsBase {
			return err
		}
	}
	return nil
}

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	kernel32             = windows.NewLazySystemDLL("kernel32.dll")
	procGetLastInputInfo = user32.NewProc("GetLastInputInfo")
	procGetTickCount     = kernel32.NewProc("GetTickCount")
	procGetPowerStatus   = kernel32.NewProc("GetSystemPowerStatus")
)

type lastInputInfo struct {
	size uint32
	time uint32
}

type powerStatus struct {
	acLine, flag, percent, saver uint8
	lifeTime, fullLifeTime       uint32
}

func conditions() autoupdate.Conditions {
	var c autoupdate.Conditions
	li := lastInputInfo{size: uint32(unsafe.Sizeof(lastInputInfo{}))}
	if r, _, _ := procGetLastInputInfo.Call(uintptr(unsafe.Pointer(&li))); r != 0 {
		now, _, _ := procGetTickCount.Call()
		c.Idle, c.IdleKnown = time.Duration(uint32(now)-li.time)*time.Millisecond, true
	}
	var ps powerStatus
	if r, _, _ := procGetPowerStatus.Call(uintptr(unsafe.Pointer(&ps))); r != 0 {
		c.Charging = ps.acLine == 1
		// flag 128：沒有電池；percent 255：未知。
		if ps.flag&128 == 0 && ps.percent <= 100 {
			c.Battery, c.BatteryKnown = int(ps.percent), true
		}
	}
	return c
}

// toastScript 以 ToastGeneric 顯示通知；ZC_TOAST_ACTION 非空時加「立即安裝／稍後」按鈕：
// 「立即安裝」以 zc-update: 協定啟動 0c-update（見 registerProtocol），「稍後 1 小時」保存同一資產的提醒。
const toastScript = `$ErrorActionPreference='Stop'
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] > $null
[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime] > $null
$doc = New-Object Windows.Data.Xml.Dom.XmlDocument
$doc.LoadXml('<toast><visual><binding template="ToastGeneric"><text/><text/></binding></visual></toast>')
$t = $doc.GetElementsByTagName('text')
$t.Item(0).AppendChild($doc.CreateTextNode($env:ZC_TOAST_TITLE)) > $null
$t.Item(1).AppendChild($doc.CreateTextNode($env:ZC_TOAST_BODY)) > $null
if ($env:ZC_TOAST_ACTION) {
  $toast = $doc.SelectSingleNode('/toast')
  $toast.SetAttribute('scenario', 'reminder')
  $actions = $doc.CreateElement('actions')
  $install = $doc.CreateElement('action')
  $install.SetAttribute('content', '立即安裝')
  $install.SetAttribute('activationType', 'protocol')
  $install.SetAttribute('arguments', $env:ZC_TOAST_ACTION)
  $later = $doc.CreateElement('action')
  $later.SetAttribute('content', '稍後 1 小時')
  $later.SetAttribute('activationType', 'protocol')
  $later.SetAttribute('arguments', $env:ZC_TOAST_SNOOZE)
  $actions.AppendChild($install) > $null
  $actions.AppendChild($later) > $null
  $toast.AppendChild($actions) > $null
}
$n = [Windows.UI.Notifications.ToastNotification]::new($doc)
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($env:ZC_TOAST_APP).Show($n)`

// protocolScheme 是通知按鈕啟動 0c-update 的協定；只接受綁定版本及 SHA 的 finish／snooze。
const protocolScheme = "zc-update"

// registerProtocol 讓 zc-update: 以 conhost --headless 執行穩定的 0c-update（不閃主控台視窗）。
func registerProtocol() error {
	stable := stableUpdater(basePath())
	if _, err := os.Stat(stable); err != nil {
		return err
	}
	root, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\`+protocolScheme, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer root.Close()
	if err = root.SetStringValue("", "URL:靈犀注音更新"); err != nil {
		return err
	}
	if err = root.SetStringValue("URL Protocol", ""); err != nil {
		return err
	}
	cmd, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\`+protocolScheme+`\shell\open\command`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer cmd.Close()
	conhost := filepath.Join(os.Getenv("SystemRoot"), `System32\conhost.exe`)
	return cmd.SetStringValue("", fmt.Sprintf(`"%s" --headless "%s" protocol "%%1"`, conhost, stable))
}

func removeProtocol() {
	for _, k := range []string{`\shell\open\command`, `\shell\open`, `\shell`, ``} {
		registry.DeleteKey(registry.CURRENT_USER, `Software\Classes\`+protocolScheme+k)
	}
}

// toastApp 是通知的 AppUserModelID；在 HKCU 註冊顯示名稱後，通知來源顯示「靈犀注音」。
const toastApp = "Zuin.0c.Update"

func registerToastApp() error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\AppUserModelId\`+toastApp, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	return key.SetStringValue("DisplayName", "靈犀注音")
}

func encodePS(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, len(u)*2)
	for i, v := range u {
		b[2*i], b[2*i+1] = byte(v), byte(v>>8)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func notify(n autoupdate.Notice) error {
	title, body := n.Title, n.Body
	ps := filepath.Join(os.Getenv("SystemRoot"), `System32\WindowsPowerShell\v1.0\powershell.exe`)
	cmd := exec.Command(ps, "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-EncodedCommand", encodePS(toastScript))
	app := toastApp
	if registerToastApp() != nil {
		app = `{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe` // 無法註冊時仍要送達
	}
	action, snooze := "", ""
	if n.Action != "" {
		var err error
		if action, err = noticeActionURL(n.Action, n.TargetVersion, n.TargetSHA256); err != nil {
			return err
		}
		snooze, _ = noticeActionURL("snooze", n.TargetVersion, n.TargetSHA256)
		if err = registerProtocol(); err != nil {
			return err
		}
	}
	cmd.Env = append(os.Environ(), "ZC_TOAST_TITLE="+title, "ZC_TOAST_BODY="+body, "ZC_TOAST_APP="+app, "ZC_TOAST_ACTION="+action, "ZC_TOAST_SNOOZE="+snooze)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s (%w)", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// taskNameFor 是工作排程器裡本使用者的工作名稱（同一台電腦的每個使用者各有一份）。
func taskNameFor(prefix string) string {
	if u, err := user.Current(); err == nil && u.Uid != "" {
		return prefix + "-" + strings.ToLower(u.Uid)
	}
	return prefix
}

func taskName() string       { return taskNameFor("0c-update") }
func engineTaskName() string { return taskNameFor("0c-engine") }

func schtasksPath() string { return filepath.Join(os.Getenv("SystemRoot"), `System32\schtasks.exe`) }

func quietCommand(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
	return cmd
}

func taskExists(name string) bool {
	return quietCommand(schtasksPath(), "/Query", "/TN", name).Run() == nil
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

// installSchedule 以工作排程器建立使用者層級的工作：登入 5 分鐘後與每 15 分鐘喚起，只在使用者登入時
// 執行（才能判斷閒置、發通知）；以 conhost --headless 執行，不閃出主控台視窗。
func installSchedule(base string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if bundleUpdater != "" {
		// 外殼更新由舊的穩定副本自己執行：改用剛安裝的版本目錄裡的，之後的排程與選單才是新版。
		self = bundleUpdater
	}
	return installScheduleFrom(base, self)
}

// installScheduleFrom 以 source 換上穩定副本，並以目前程式的定義（重新）建立排程。
func installScheduleFrom(base, source string) error {
	stable := stableUpdater(base)
	if err := replaceUpdater(source, stable); err != nil {
		return err
	}
	u, err := user.Current()
	if err != nil {
		return err
	}
	conhost := filepath.Join(os.Getenv("SystemRoot"), `System32\conhost.exe`)
	args := fmt.Sprintf(`--headless %s auto`, syscall.EscapeArg(stable))
	task := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo><Description>靈犀注音自動更新</Description></RegistrationInfo>
  <Triggers>
    <LogonTrigger><Enabled>true</Enabled><UserId>%[1]s</UserId><Delay>PT5M</Delay><Repetition><Interval>PT%[5]dM</Interval></Repetition></LogonTrigger>
    <TimeTrigger><Enabled>true</Enabled><StartBoundary>2026-01-01T00:00:00</StartBoundary><Repetition><Interval>PT%[5]dM</Interval></Repetition></TimeTrigger>
  </Triggers>
  <Principals><Principal id="Author"><UserId>%[2]s</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <StartWhenAvailable>true</StartWhenAvailable>
    <IdleSettings><StopOnIdleEnd>false</StopOnIdleEnd><RestartOnIdle>false</RestartOnIdle></IdleSettings>
    <ExecutionTimeLimit>PT1H</ExecutionTimeLimit>
    <Priority>7</Priority>
  </Settings>
  <Actions Context="Author"><Exec><Command>%[3]s</Command><Arguments>%[4]s</Arguments></Exec></Actions>
</Task>
`, xmlEscape(u.Username), xmlEscape(u.Uid), xmlEscape(conhost), xmlEscape(args), int(scheduleEvery(base).Minutes()))
	if err = createTask(taskName(), task); err != nil {
		return err
	}
	fmt.Println("已建立自動更新排程（工作排程器 " + taskName() + "）")
	// 引擎排程與自動更新分開：使用者關掉自動更新（schedule remove）時引擎仍要在登入時啟動。
	if err = installEngineTask(u, conhost, stable); err != nil {
		fmt.Fprintln(os.Stderr, "0c-update: 登入啟動引擎的排程未建立：", err)
	}
	return nil
}

// createTask 以 UTF-16 的工作定義建立（或取代）工作排程器的工作。
func createTask(name, task string) error {
	dir, err := os.MkdirTemp("", "0c-task-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	file := filepath.Join(dir, "task.xml")
	units := utf16.Encode([]rune(task))
	b := []byte{0xff, 0xfe}
	for _, v := range units {
		b = append(b, byte(v), byte(v>>8))
	}
	if err = os.WriteFile(file, b, 0o600); err != nil {
		return err
	}
	if out, err := exec.Command(schtasksPath(), "/Create", "/F", "/TN", name, "/XML", file).CombinedOutput(); err != nil {
		return fmt.Errorf("建立排程失敗：%s (%w)", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// engineTaskXML：登入時立即以穩定副本執行 `engine`，引擎在使用者第一次打字前就常駐。
// 沙箱應用程式（Windows 搜尋框等 AppContainer）裡的 TSF DLL 不能啟動程式，只能連上已在執行的引擎。
// 優先順序 4 是一般；引擎由 startEngine 明確以一般優先順序啟動。
func engineTaskXML(username, uid, conhost, stable string) string {
	args := fmt.Sprintf(`--headless %s engine`, syscall.EscapeArg(stable))
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo><Description>靈犀注音引擎：登入時啟動，讓 Windows 搜尋框等沙箱應用程式也能輸入</Description></RegistrationInfo>
  <Triggers>
    <LogonTrigger><Enabled>true</Enabled><UserId>%[1]s</UserId></LogonTrigger>
  </Triggers>
  <Principals><Principal id="Author"><UserId>%[2]s</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <IdleSettings><StopOnIdleEnd>false</StopOnIdleEnd><RestartOnIdle>false</RestartOnIdle></IdleSettings>
    <ExecutionTimeLimit>PT2M</ExecutionTimeLimit>
    <Priority>4</Priority>
  </Settings>
  <Actions Context="Author"><Exec><Command>%[3]s</Command><Arguments>%[4]s</Arguments></Exec></Actions>
</Task>
`, xmlEscape(username), xmlEscape(uid), xmlEscape(conhost), xmlEscape(args))
}

func installEngineTask(u *user.User, conhost, stable string) error {
	if err := createTask(engineTaskName(), engineTaskXML(u.Username, u.Uid, conhost, stable)); err != nil {
		return err
	}
	fmt.Println("已建立登入啟動引擎的排程（工作排程器 " + engineTaskName() + "）")
	return nil
}

// ensureEngineTask（schedule repair）：已安裝而登入排程不見了（例如被防毒軟體移除）時補回。
// 不受 schedule remove 影響：關閉自動更新時引擎仍要在登入時啟動。
func ensureEngineTask(base string) {
	version, err := installedVersion()
	stable := stableUpdater(base)
	if err != nil || version == "" || taskExists(engineTaskName()) {
		return
	}
	if _, err = os.Stat(stable); err != nil {
		return // 穩定副本由 repairUpdater 重建，同時建回排程
	}
	u, err := user.Current()
	if err == nil {
		err = installEngineTask(u, filepath.Join(os.Getenv("SystemRoot"), `System32\conhost.exe`), stable)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "0c-update: 登入啟動引擎的排程未補回：", err)
	}
}

// removeResidentEngine（uninstall）：移除登入排程，並請常駐引擎存檔後結束（不再閒置結束，否則會留到登出）。
// 仍在組字時引擎不交接，留到登出。
func removeResidentEngine() {
	quietCommand(schtasksPath(), "/Delete", "/F", "/TN", engineTaskName()).Run()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := autoupdate.Drain(ctx, dialEngine, 10*time.Second); err != nil {
		fmt.Fprintln(os.Stderr, "0c-update: 引擎仍在執行，登出後結束：", err)
	}
}

// ensureEngine（`0c-update engine`）由登入排程執行：已安裝而引擎還沒在跑時啟動常駐引擎並等它開始服務。
// 這裡直接啟動，不再經過排程（排程本身就以一般優先順序執行）。
func ensureEngine() error {
	version, err := installedVersion()
	if err != nil || version == "" {
		return err
	}
	if c, err := dialEngine(time.Second); err == nil {
		c.Close()
		return nil
	}
	if err = spawnEngine(basePath()); err != nil {
		return err
	}
	return waitForEngine(context.Background(), dialEngine)
}

// restartResidentEngine：套用設定時引擎已退出或原本就沒在跑，立刻以新設定啟動，不等下一個按鍵；
// 下一個按鍵若在沙箱應用程式裡，DLL 無法自己啟動引擎。不等引擎就緒，套用設定不因此卡住。
// 失敗時仍由一般應用程式的下一個按鍵啟動。
func restartResidentEngine() {
	if version, err := installedVersion(); err != nil || version == "" {
		return
	}
	if err := startEngine(basePath()); err != nil {
		fmt.Fprintln(os.Stderr, "0c-update: 引擎未重新啟動：", err)
	}
}

func removeSchedule() error {
	exec.Command(schtasksPath(), "/Delete", "/F", "/TN", taskName()).Run()
	registry.DeleteKey(registry.CURRENT_USER, `Software\Classes\AppUserModelId\`+toastApp)
	removeProtocol()
	fmt.Println("已移除自動更新排程")
	return nil
}

// scheduleExists 回報工作排程器裡有沒有本使用者的自動更新工作（防毒軟體可能連同工作一起移除）。
func scheduleExists() bool { return taskExists(taskName()) }

func scheduleStatus() string {
	if !scheduleExists() {
		return "排程：未建立"
	}
	return "排程：已建立（" + taskName() + "）"
}

// deferShell：外殼更新不需延到登入。
func deferShell() bool { return false }

// notifyWithAction：此平台的通知按鈕尚未實作，改為一般通知。
func notifyWithAction(_ context.Context, n autoupdate.Notice) (string, error) {
	n.Action = ""
	return "", notify(n)
}

// setupInputMethod：此平台由安裝程式註冊輸入法，不需額外設定。
func setupInputMethod(string) error { return nil }
func addInputMethod() error         { return nil }

// openScheduleSettings：只有 macOS 的排程可能在系統設定被關閉。
func openScheduleSettings() error {
	return fmt.Errorf("此平台沒有系統設定的背景執行開關")
}
