//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/enginedir"
	"github.com/0c-biggo/0c-ime/internal/winshell"
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
	return autoupdate.CoreLayout{Server: "0c-server.exe", Update: "0c-update.exe", Settings: "0c-settings.exe", MCP: "0c-mcp.exe", Launcher: launcherName}, true
}

// 外殼更新要改 TSF 註冊時需要系統管理員；排程以一般權限執行，所以要使用者確認。註冊不變的見 shellKeepsRegistration（#294）。
func shellUnattended() bool { return windows.GetCurrentProcessToken().IsElevated() }
func elevatedOK() bool      { return windows.GetCurrentProcessToken().IsElevated() }

// ShellActiveWithoutRelogin（autoupdate.ShellWithoutRelogin）：外殼裝好不等重新登入（#253），新引擎接手就算完成。
// 完整更新可能是舊的穩定更新器裝的（例如 0.2.7 → 新版），它不會核對也可能留下 needs_relogin；新版下一次喚起時由協調器補上。
// 新引擎由 StartEngine 經登入工作叫起，不繼承自動更新工作的低優先順序，也不會以系統管理員權限執行。
func (p *system) ShellActiveWithoutRelogin() bool { return true }

const createBreakawayFromJob = 0x01000000

// startEngine 啟動常駐引擎（不閒置結束：沙箱應用程式如 Windows 搜尋框裡的 DLL 無法自己啟動引擎）。
// 有登入排程時交給它啟動：自動更新排程以低優先順序執行，直接啟動的引擎除了 CPU 優先順序，
// 還會繼承低 I/O 與記憶體優先順序，而且整個登入期間都不會結束。
// 「立即安裝」的協調器是提權執行的：沒有登入工作時不直接啟動（startResidentEngine），引擎不能以系統管理員權限常駐。
func startEngine(base string) error { return startResident(base) }

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

// protocolScheme 是通知按鈕啟動 0c-update 的協定；只接受綁定版本及 SHA 的 finish／snooze。
const protocolScheme = "zc-update"

// registerProtocol 讓 zc-update: 執行穩定的 0c-update（不閃主控台視窗）：經 bin\0c-updatew.exe，沒有它時退回
// conhost --headless（見 launchCommand，#289）。
// 每次送出有按鈕的通知前都會呼叫：內容與現值相同就不寫。反覆改寫協定處理程式是端點防護在找的常駐手法（#289 的後續）。
func registerProtocol() error {
	base := basePath()
	if _, err := os.Stat(stableUpdater(base)); err != nil {
		return err
	}
	command, args := launchCommand(base, "protocol")
	_, err := setRegistryStrings(`Software\Classes\`+protocolScheme, protocolValues(command, args))
	return err
}

// protocolValues 是 zc-update: 協定在 HKCU\Software\Classes 底下的值（子機碼 → 值名稱 → 內容；"" 是預設值）。
func protocolValues(command, args string) []registryString {
	return []registryString{
		{"", "", "URL:靈犀注音更新"},
		{"", "URL Protocol", ""},
		{`shell\open\command`, "", fmt.Sprintf(`"%s" %s "%%1"`, command, args)},
	}
}

// registryString 是 HKCU 某個機碼底下的一個字串值。
type registryString struct{ subkey, name, value string }

// setRegistryStrings 讓 HKCU\<root> 底下的字串值與 values 相同：值已存在且內容相同就不開寫入權限、也不寫，
// 回傳實際寫了幾個。
func setRegistryStrings(root string, values []registryString) (int, error) {
	written := 0
	for _, v := range values {
		path := root
		if v.subkey != "" {
			path += `\` + v.subkey
		}
		if key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE); err == nil {
			have, kind, err := key.GetStringValue(v.name)
			key.Close()
			if err == nil && kind == registry.SZ && have == v.value {
				continue
			}
		}
		key, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.SET_VALUE)
		if err != nil {
			return written, err
		}
		err = key.SetStringValue(v.name, v.value)
		key.Close()
		if err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}

func removeProtocol() {
	for _, k := range []string{`\shell\open\command`, `\shell\open`, `\shell`, ``} {
		registry.DeleteKey(registry.CURRENT_USER, `Software\Classes\`+protocolScheme+k)
	}
}

// toastApp 是通知的 AppUserModelID；在 HKCU 註冊顯示名稱後，通知來源顯示「靈犀注音」。
const toastApp = "Zuin.0c.Update"

// registerToastApp：每次通知前呼叫，顯示名稱已註冊就不寫。
func registerToastApp() error {
	_, err := setRegistryStrings(`Software\Classes\AppUserModelId\`+toastApp, []registryString{{"", "DisplayName", "靈犀注音"}})
	return err
}

// notify 以 ToastGeneric 顯示通知；n.Action 非空時加「立即安裝（手動更新的新版是立即下載）／稍後」按鈕：前者以 zc-update: 協定
// 啟動 0c-update（見 registerProtocol），「稍後 1 小時」保存同一資產的提醒。
// 在行程內以 WinRT 送出（internal/winshell），不再開 powershell.exe -EncodedCommand（#289）。
func notify(n autoupdate.Notice) error {
	app := toastApp
	if registerToastApp() != nil {
		app = `{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe` // 無法註冊時借用系統已註冊的 AUMID，仍要送達
	}
	t := winshell.Toast{Title: n.Title, Body: n.Body, ActionLabel: noticeButtonLabel(n.Action)}
	if n.Action != "" {
		var err error
		if t.Action, err = noticeActionURL(n.Action, n.TargetVersion, n.TargetSHA256); err != nil {
			return err
		}
		t.Snooze, _ = noticeActionURL("snooze", n.TargetVersion, n.TargetSHA256)
		if err = registerProtocol(); err != nil {
			return err
		}
	}
	return winshell.ShowToast(app, t)
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

// 排程的查詢、建立、執行與刪除都在行程內呼叫工作排程器的 COM 介面（internal/winshell），不再開 schtasks.exe：
// 從 %TEMP% 讀 XML 的 schtasks /Create 與頻繁的 /Query、/Run、/Delete 都是端點防護會看的命令列（#289 的後續）。

func taskExists(name string) bool { return winshell.TaskExists(name) }

// errTaskReadOnly：排程的定義不是目前的，但它是提權建立、只開放讀取的（0.2.11 以前的安裝程式），一般權限改不了。
var errTaskReadOnly = errors.New("排程由系統管理員權限建立，一般權限無法更新，下一次完整安裝時會換新")

// ensureTask 讓工作排程器裡的 name 是 definition（見 planTask）：定義相同就不寫；提權時一併把安全描述元開放給本人
// （TaskOpenSDDL），一般權限寫時保留原本的。回傳有沒有寫入。
func ensureTask(name, definition, sid string, elevated bool) (bool, error) {
	stamped, want := stampTaskDefinition(definition)
	current, err := winshell.GetTask(name)
	exists := err == nil
	if err != nil && !errors.Is(err, winshell.ErrTaskNotFound) {
		return false, fmt.Errorf("讀取排程失敗：%w", err)
	}
	switch planTask(exists, taskDefinitionStamp(current.XML), want, winshell.TaskWritableBy(current.SDDL, sid), elevated) {
	case taskKeep:
		return false, nil
	case taskReadOnly:
		return false, errTaskReadOnly
	}
	sddl := ""
	if elevated {
		sddl = winshell.TaskOpenSDDL(sid)
	}
	if err = winshell.RegisterTask(name, stamped, sddl); err != nil {
		return false, fmt.Errorf("建立排程失敗：%w", err)
	}
	return true, nil
}

// deleteTask 刪除排程；不存在不算失敗（回傳 false, nil）。本人只能讀的排程（提權建立）不嘗試刪除，
// 照實回報 errTaskReadOnly，不印「已移除」（#284）。
func deleteTask(name, sid string) (bool, error) {
	current, err := winshell.GetTask(name)
	switch {
	case errors.Is(err, winshell.ErrTaskNotFound):
		return false, nil
	case err != nil:
		return false, err
	case !elevatedOK() && !winshell.TaskWritableBy(current.SDDL, sid):
		return false, errors.New("排程由系統管理員權限建立，一般權限無法移除；請從「已安裝的應用程式」解除安裝靈犀注音，或重新安裝後再移除")
	}
	if err = winshell.DeleteTask(name); err != nil && !errors.Is(err, winshell.ErrTaskNotFound) {
		return false, err
	}
	return err == nil, nil
}

// currentSID 是目前使用者的 SID（排程名稱、Principal 與安全描述元都用它）。
func currentSID() string {
	if u, err := user.Current(); err == nil {
		return u.Uid
	}
	return ""
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

// installSchedule 以工作排程器建立使用者層級的工作：登入 5 分鐘後與每 15 分鐘喚起，只在使用者登入時
// 執行（才能判斷閒置、發通知）；經 GUI 程式 bin\0c-updatew.exe 執行，不閃出主控台視窗、也不經 conhost --headless（#289）。
func installSchedule(base string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	version := buildVersion
	if bundleUpdater != "" {
		// 外殼更新由舊的穩定副本自己執行：改用剛安裝的版本目錄裡的，之後的排程與選單才是新版。
		self, version = bundleUpdater, bundleVersion
	}
	return installScheduleFrom(base, self, version)
}

// installScheduleFrom 以 source 換上穩定副本（與它旁邊的啟動器），並讓排程是目前程式的定義（相同就不寫，見 ensureTask）。
// 0.2.11 以前提權建立的排程只開放讀取，一般權限改不了，舊的 conhost 定義要等提權的完整安裝；這一版起提權建立的排程
// 開放給本人，之後定義再變，一般權限的 schedule repair 就換得掉（refreshTaskDefinitions）。
//
// 換穩定副本前先讓 source 自我測試（#293）：沒通過就保留原本的穩定副本，排程照樣建立（指著同一個位置），不讓安裝失敗。
func installScheduleFrom(base, source, version string) error {
	if err := adoptStableUpdater(source, version, base); err != nil && !errors.Is(err, errNotQualified) && !errors.Is(err, errStableNewer) {
		return err
	}
	if err := installLauncherBeside(source, stableLauncher(base)); err != nil {
		fmt.Fprintln(os.Stderr, "0c-update: 排程用的啟動器未更新，排程仍以 conhost 執行：", err)
	}
	u, err := user.Current()
	if err != nil {
		return err
	}
	changed, err := ensureTask(taskName(), updateTaskXML(u.Username, u.Uid, base), u.Uid, elevatedOK())
	switch {
	case errors.Is(err, errTaskReadOnly):
		// 註冊不變的外殼更新以一般權限安裝（#294）：舊的提權排程照舊執行（它跑的是剛換上的穩定副本），
		// 不擋後面的登入排程與引擎。
		fmt.Fprintln(os.Stderr, "0c-update: 自動更新排程沒有換成目前的定義：", err)
	case err != nil:
		return err
	case changed:
		fmt.Println("已建立自動更新排程（工作排程器 " + taskName() + "）")
	default:
		fmt.Println("自動更新排程已是目前的定義（工作排程器 " + taskName() + "）")
	}
	// 引擎排程與自動更新分開：使用者關掉自動更新（schedule remove）時引擎仍要在登入時啟動。
	if err = installEngineTask(u, base); err != nil {
		fmt.Fprintln(os.Stderr, "0c-update: 登入啟動引擎的排程未建立：", err)
	}
	if protocolCommand() != "" { // 先前的通知註冊過協定：一併換成目前的定義
		_ = registerProtocol()
	}
	return nil
}

// updateTaskXML 是自動更新排程的定義：登入 5 分鐘後與每隔 scheduleEvery 執行 `auto`。
func updateTaskXML(username, uid, base string) string {
	command, args := launchCommand(base, "auto")
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-16"?>
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
`, xmlEscape(username), xmlEscape(uid), xmlEscape(command), xmlEscape(args), int(scheduleEvery(base).Minutes()))
}

// engineTaskXML：登入時立即以穩定副本執行 `engine`，引擎在使用者第一次打字前就常駐。
// 沙箱應用程式（Windows 搜尋框等 AppContainer）裡的 TSF DLL 不能啟動程式，只能連上已在執行的引擎。
// 優先順序 4 是一般；引擎由 startEngine 明確以一般優先順序啟動。
func engineTaskXML(username, uid, base string) string {
	command, args := launchCommand(base, "engine")
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
`, xmlEscape(username), xmlEscape(uid), xmlEscape(command), xmlEscape(args))
}

func installEngineTask(u *user.User, base string) error {
	changed, err := ensureTask(engineTaskName(), engineTaskXML(u.Username, u.Uid, base), u.Uid, elevatedOK())
	if err != nil {
		return err
	}
	if changed {
		fmt.Println("已建立登入啟動引擎的排程（工作排程器 " + engineTaskName() + "）")
	}
	return nil
}

// refreshTaskDefinitions（schedule repair，通常是一般權限）：排程還在、但定義不是這一版的（例如 #289 以前的
// conhost --headless 寫法），而且本人改得了（這一版起提權建立的排程開放給本人）時換成目前的定義，不必等下一次 UAC。
// 定義相同不寫；本人只能讀的舊排程不嘗試（見 planTask）。排程不見了不在這裡建：自動更新排程由 repairUpdater 重建
// （有次數限制與通知），登入排程由 ensureEngineTask 補回。解除安裝之後、使用者關掉自動更新時不動自動更新排程。
func refreshTaskDefinitions(base string) {
	if noActiveInstall() {
		return
	}
	if version, err := installedVersion(); err != nil || version == "" {
		return
	}
	if _, err := os.Stat(stableUpdater(base)); err != nil {
		return
	}
	u, err := user.Current()
	if err != nil {
		return
	}
	tasks := []struct{ name, definition, label string }{{engineTaskName(), engineTaskXML(u.Username, u.Uid, base), "登入啟動引擎的排程"}}
	if s, err := autoupdate.Load(base); err == nil && !s.ScheduleRemoved {
		tasks = append(tasks, struct{ name, definition, label string }{taskName(), updateTaskXML(u.Username, u.Uid, base), "自動更新排程"})
	}
	for _, t := range tasks {
		if !taskExists(t.name) {
			continue
		}
		switch changed, err := ensureTask(t.name, t.definition, u.Uid, elevatedOK()); {
		case errors.Is(err, errTaskReadOnly):
			// 0.2.11 以前提權建立：等下一次完整安裝，不每次嘗試
		case err != nil:
			fmt.Fprintln(os.Stderr, "0c-update: "+t.label+"沒有換成目前的定義：", err)
		case changed:
			fmt.Println("已把" + t.label + "換成目前的定義（工作排程器 " + t.name + "）")
		}
	}
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
		err = installEngineTask(u, base)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "0c-update: 登入啟動引擎的排程未補回：", err)
	}
}

// removeResidentEngine（uninstall）：移除登入排程，並請常駐引擎存檔後結束（不再閒置結束，否則會留到登出）。
// 仍在組字時引擎不交接，留到登出。
func removeResidentEngine() {
	if _, err := deleteTask(engineTaskName(), currentSID()); err != nil {
		fmt.Fprintln(os.Stderr, "0c-update: 登入啟動引擎的排程沒有移除：", err)
	}
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
	// 登入工作也在安裝、回復交接引擎之後由提權的安裝程式叫起：以使用者權限順便請舊版的候選窗程式換新，
	// 再確定有候選窗程式在執行：工作列選單靠它執行命令（#243），沙箱應用程式也啟動不了它。
	defer func() {
		settleInstalledShell(basePath()) // 引擎起來了：舊版更新器留下的「重新登入後生效」在這裡清掉（#253）
		handOverUI(uiWaitTask)
		ensureUI()
	}()
	listInputMethod(basePath()) // 舊版更新器在背景裝上這一版時沒有加入輸入法清單（#191）

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

// removeSchedule（uninstall、schedule remove、#284 的殘留排程）：通知註冊與協定一定移除；排程刪不掉時照實回報。
func removeSchedule() error {
	registry.DeleteKey(registry.CURRENT_USER, `Software\Classes\AppUserModelId\`+toastApp)
	removeProtocol()
	switch removed, err := deleteTask(taskName(), currentSID()); {
	case err != nil:
		return fmt.Errorf("自動更新排程沒有移除：%w", err)
	case removed:
		fmt.Println("已移除自動更新排程")
	default:
		fmt.Println("沒有自動更新排程")
	}
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

// setupInputMethod：註冊由安裝完成，加入語言清單由 afterInstall 的 listInputMethod 做，不需額外設定。
func setupInputMethod(string) error  { return nil }
func inputSourcePending(string) bool { return false }

// retryInputMethod：此平台登入時補做就是 addInputMethod，不分是不是背景排程（macOS 見 auto_darwin.go）。
func retryInputMethod(bool) error { return addInputMethod() }

// openScheduleSettings：只有 macOS 的排程可能在系統設定被關閉。
func openScheduleSettings() error {
	return fmt.Errorf("此平台沒有系統設定的背景執行開關")
}
