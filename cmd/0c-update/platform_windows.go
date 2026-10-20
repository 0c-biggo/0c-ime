//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/update"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const clsid = `CLSID\{138C35D0-7F08-4CEF-BA90-C34C16301528}\InprocServer32`

func basePath() string    { return filepath.Join(os.Getenv("LOCALAPPDATA"), "Zuin") }
func receiptPath() string { return filepath.Join(basePath(), "install-receipt.json") }
func preflight() error {
	if runtime.GOARCH != "amd64" || os.Getenv("PROCESSOR_ARCHITEW6432") == "ARM64" || os.Getenv("PROCESSOR_ARCHITECTURE") == "ARM64" {
		return fmt.Errorf("目前 Windows 套件僅驗證 x64 架構；ARM64 尚不支援")
	}
	if os.Getenv("LOCALAPPDATA") == "" || !filepath.IsAbs(os.Getenv("LOCALAPPDATA")) {
		return fmt.Errorf("缺少有效的 LOCALAPPDATA")
	}
	if !windows.GetCurrentProcessToken().IsElevated() {
		return fmt.Errorf("TSF 註冊需要以同一位桌面使用者的系統管理員 PowerShell 執行；請先完成組字")
	}
	return nil
}

type registrar struct{}

func init() {
	// 固定路徑的目錄一建立就開放沙箱讀取並由檔案繼承（#121）。
	update.FixedDirHook = func(dir string) error { return grantAppContainer(dir, true) }
}

// 兩個登錄視圖：64 位元應用程式讀 WOW64_64KEY，32 位元應用程式讀 WOW64_32KEY（HKCR\WOW6432Node\CLSID，#112）。
// 收據與安裝交易只看 64 位元視圖；32 位元那份由 DLL 註冊時一併寫入（platforms/windows/tsf/src/register.rs）。
var registryViews = []uint32{registry.WOW64_64KEY, registry.WOW64_32KEY}

func registeredIn(view uint32) (string, error) {
	key, err := registry.OpenKey(registry.CLASSES_ROOT, clsid, registry.QUERY_VALUE|view)
	if err == windows.ERROR_FILE_NOT_FOUND {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer key.Close()
	value, _, err := key.GetStringValue("")
	return value, err
}

func (registrar) Current() (string, error) { return registeredIn(registry.WOW64_64KEY) }
func invokeReg(dll string, remove bool) error {
	systemRoot := os.Getenv("SystemRoot")
	if !filepath.IsAbs(systemRoot) {
		return fmt.Errorf("無效的 SystemRoot")
	}
	args := []string{"/s"}
	if remove {
		args = append(args, "/u")
	}
	args = append(args, dll)
	command := exec.Command(filepath.Join(systemRoot, "System32", "regsvr32.exe"), args...)
	if err := command.Run(); err != nil {
		return fmt.Errorf("TSF 註冊命令失敗（%s）：%w", filepath.Base(dll), err)
	}
	return nil
}

// appContainerSIDs 是 ALL APPLICATION PACKAGES 與 ALL RESTRICTED APPLICATION PACKAGES。
var appContainerSIDs = []string{"S-1-15-2-1", "S-1-15-2-2"}

// appContainerEntries 是讀取／執行權限的 ACE；inherit 時由目錄底下新建的檔案與子目錄繼承。
func appContainerEntries(inherit bool) ([]windows.EXPLICIT_ACCESS, error) {
	var entries []windows.EXPLICIT_ACCESS
	inheritance := uint32(windows.NO_INHERITANCE)
	if inherit {
		inheritance = windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT
	}
	for _, s := range appContainerSIDs {
		sid, err := windows.StringToSid(s)
		if err != nil {
			return nil, err
		}
		entries = append(entries, windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       inheritance,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		})
	}
	return entries, nil
}

// grantAppContainerRead 讓沙箱應用程式（AppContainer，例如 Windows 搜尋框）讀得到並載入 TSF DLL。
// 版本目錄在 %LOCALAPPDATA%，繼承來的權限只有本人、SYSTEM 與系統管理員；少了這兩筆，
// 沙箱宿主載不進 DLL，TSF 改用別的輸入法或直接打出英文。管道的 ACL 同樣開給這兩個 SID。
func grantAppContainerRead(file string) error { return grantAppContainer(file, false) }

// grantAppContainer 在檔案或目錄的 DACL 加上沙箱讀取權限；inherit 時由目錄底下的檔案繼承。
// 固定路徑的目錄（#121）用繼承：換檔時新複製的 .new 一建立就有權限，改名後保留，不必每次再設。
func grantAppContainer(file string, inherit bool) error {
	sd, err := windows.GetNamedSecurityInfo(file, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if dacl == nil {
		return nil // NULL DACL 已允許所有人；以它合併會只剩這兩筆，反而擋住本人
	}
	entries, err := appContainerEntries(inherit)
	if err != nil {
		return err
	}
	acl, err := windows.ACLFromEntries(entries, dacl)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(file, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}

// Register 每次註冊（安裝、更新、回復）都補上沙箱讀取權限，舊版目錄回復時也一樣；32 位元 DLL（x86\0c_tsf.dll，#112）
// 同樣處理。補不上只影響沙箱應用程式，一般應用程式照常可用，所以只提示、不讓安裝失敗。
// regsvr32 只對 x64 DLL 執行：DLL 註冊時同時寫入 32 位元視圖（有 x86 那份才寫），profile 與類別兩個視圖共用。
func (registrar) Register(dll string) error {
	files := []string{dll}
	if _, err := os.Stat(update.WindowsDLL32Of(dll)); err == nil {
		files = append(files, update.WindowsDLL32Of(dll))
	}
	for _, file := range files {
		if err := grantAppContainerRead(file); err != nil {
			fmt.Fprintf(os.Stderr, "0c-update: 無法讓沙箱應用程式（Windows 搜尋框等）讀取 %s：%v\n", file, err)
		}
	}
	return invokeReg(dll, false)
}

// Unregister 以 regsvr32 /u 取消註冊；DLL 已不存在（例如被防毒軟體隔離）時 regsvr32 無法執行，
// 直接刪除 DLL 註冊時寫入的 COM 與 TSF 機碼（platforms/windows/tsf/src/register.rs），解除安裝才不會卡住（#82）。
// 新版 DLL 取消註冊時兩個視圖都刪；32 位元視圖仍指向這一版的 x86 DLL（例如 x64 DLL 是不認得它的舊版）時這裡補刪。
func (registrar) Unregister(dll string) error {
	if _, err := os.Stat(dll); os.IsNotExist(err) {
		return removeRegistration()
	}
	if err := invokeReg(dll, true); err != nil {
		return err
	}
	if current, err := registeredIn(registry.WOW64_32KEY); err == nil && sameDLL(current, update.WindowsDLL32Of(dll)) {
		if err := deleteTree(registry.CLASSES_ROOT, clsidRoot(), registry.WOW64_32KEY); err != nil && err != windows.ERROR_FILE_NOT_FOUND {
			return fmt.Errorf("刪除 32 位元註冊：%w", err)
		}
	}
	return nil
}

func sameDLL(a, b string) bool {
	return a != "" && strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func clsidRoot() string { return strings.TrimSuffix(clsid, `\InprocServer32`) }

// removeRegistration 刪除兩個視圖的 CLSID 與共用的 TSF 機碼。
func removeRegistration() error {
	guid := strings.TrimPrefix(clsidRoot(), `CLSID\`)
	for _, k := range []struct {
		root registry.Key
		path string
		view uint32
	}{
		{registry.CLASSES_ROOT, clsidRoot(), registry.WOW64_64KEY},
		{registry.CLASSES_ROOT, clsidRoot(), registry.WOW64_32KEY},
		{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\CTF\TIP\` + guid, registry.WOW64_64KEY},
	} {
		if err := deleteTree(k.root, k.path, k.view); err != nil && err != windows.ERROR_FILE_NOT_FOUND {
			return fmt.Errorf("刪除註冊機碼 %s：%w", k.path, err)
		}
	}
	return nil
}

// deleteTree 刪除指定登錄視圖裡的機碼與所有子機碼。
func deleteTree(root registry.Key, path string, view uint32) error {
	key, err := registry.OpenKey(root, path, registry.ENUMERATE_SUB_KEYS|view)
	if err != nil {
		return err
	}
	names, err := key.ReadSubKeyNames(-1)
	key.Close()
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := deleteTree(root, path+`\`+name, view); err != nil && err != windows.ERROR_FILE_NOT_FOUND {
			return err
		}
	}
	return deleteKeyIn(root, path, view)
}

var procRegDeleteKeyExW = windows.NewLazySystemDLL("advapi32.dll").NewProc("RegDeleteKeyExW")

// deleteKeyIn 是指定視圖的 RegDeleteKeyExW；registry.DeleteKey 只會刪本行程（64 位元）視圖的機碼。
func deleteKeyIn(root registry.Key, path string, view uint32) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	r, _, _ := procRegDeleteKeyExW.Call(uintptr(root), uintptr(unsafe.Pointer(p)), uintptr(view), 0)
	if r != 0 {
		return syscall.Errno(r)
	}
	return nil
}

// repairFixedDLL：schedule repair（0c-ui 每天以一般權限執行）時復原固定路徑的 TSF DLL（#121）：換檔中斷、
// 防毒軟體刪掉或改動固定檔時從版本目錄補回，並清掉不再被載入的 .old。不需要系統管理員，失敗只記錄。
func repairFixedDLL(base string) {
	changed, err := update.RepairFixed(base)
	kind, detail := "", ""
	switch {
	case errors.Is(err, update.ErrInstallLocked):
		return // 更新正在進行，由它處理
	case err != nil:
		kind, detail = "tsf_repair_failed", err.Error()
		fmt.Fprintln(os.Stderr, "0c-update: 輸入法元件未能復原：", err)
	case changed:
		kind = "tsf_repaired"
		fmt.Println(`已從版本目錄補回輸入法元件（tsf\0c_tsf.dll）`)
	}
	if kind != "" {
		_, _ = autoupdate.EditState(base, func(s *autoupdate.State) error { s.Record(kind, detail); return nil })
	}
}

// deployTSF：版本目錄的 TSF DLL 被 regsvr32 時（舊版更新器只認得版本目錄，#121）由 DLL 以這個命令呼叫，
// 把這個版本目錄部署到固定路徑並以 regsvr32 註冊固定路徑。呼叫的更新器正持有安裝鎖，所以這裡不再上鎖；
// 收據由呼叫的更新器在註冊後寫入。
func deployTSF() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dir := filepath.Dir(exe)
	versions := filepath.Dir(dir)
	if !strings.EqualFold(filepath.Base(versions), "versions") {
		return fmt.Errorf("deploy-tsf 只能從版本目錄執行")
	}
	base := filepath.Dir(versions)
	b, err := os.ReadFile(filepath.Join(dir, "release.json"))
	if err != nil {
		return err
	}
	var release update.Release
	if err = json.Unmarshal(b, &release); err != nil {
		return err
	}
	r := &update.Receipt{Schema: 1, Product: "0c", Version: release.Version, Directory: "versions/" + filepath.Base(dir), Files: release.Files, Active: true}
	if err = update.DeployFixed(base, r, "version-registration"); err != nil {
		return err
	}
	return registrar{}.Register(update.FixedDLL(base))
}

func reportLeftovers(left []string) {
	if len(left) > 0 {
		fmt.Printf("舊版 zuin-* 程式仍被執行中的應用程式載入，暫時保留；登出再登入後，下次更新會再清除：\n  %s\n", strings.Join(left, "\n  "))
	}
}

// bundleUpdater 是剛安裝的版本目錄裡的 0c-update；建立排程時用它覆寫穩定副本。完整外殼更新由舊的穩定副本自己執行，
// 沒有這個，installSchedule 只會把「自己」複製到自己身上，穩定副本就停在舊版（macOS 同樣用 bundleUpdater）。
var bundleUpdater string

func install(bundle string, release *update.Release) error {
	if err := preflight(); err != nil {
		return err
	}
	layout, err := update.WindowsLayoutOf(release.Files)
	if err != nil {
		return err
	}
	// The matched engine must load its dictionary before any registration changes.
	probe := exec.Command(filepath.Join(bundle, layout.Server), "-check-data", "-data", filepath.Join(bundle, "data"))
	if result, err := probe.CombinedOutput(); err != nil {
		return fmt.Errorf("配對詞庫檢查失敗：%s (%w)", strings.TrimSpace(string(result)), err)
	}
	directory, left, err := update.InstallWindows(bundle, basePath(), runtime.GOARCH, registrar{})
	if err != nil {
		return err
	}
	if _, ok := release.Files[updaterName()]; ok {
		bundleUpdater = filepath.Join(directory, updaterName())
		pruneFrom = bundleUpdater // 剛裝好的版本一定保留；不用安裝程式暫存目錄裡的那一份（#179）
	}
	refreshStableMCP(basePath())
	syncUninstallEntry()
	handOverAfterInstall(release.Version, nil) // 新引擎由 afterInstall 的 ensureEngineService 經登入工作叫起
	// 舊版的候選窗程式不會自己換新（#159 的全螢幕檢查在 0c-ui）：閒置時請它結束，DLL 下一次需要時啟動新版。
	handOverUI(uiWaitInstall)
	fmt.Printf("已安裝靈犀注音至 %s\n個人詞庫保留，舊版程序不會被強制停止。請登出再登入，讓所有應用程式載入新版。\n", directory)
	reportLeftovers(left)
	return nil
}

// engineRunning 找出仍在執行的引擎；它結束時會把記憶體中的個人詞庫寫回，蓋掉剛還原的檔案。
func engineRunning() ([]string, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	var found []string
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		name := windows.UTF16ToString(entry.ExeFile[:])
		if strings.EqualFold(name, "0c-server.exe") || strings.EqualFold(name, "zuin-server.exe") {
			found = append(found, fmt.Sprintf("%s (PID %d)", name, entry.ProcessID))
		}
	}
	return found, nil
}
func rollback(_ string, restoreUser bool) error {
	if err := preflight(); err != nil {
		return err
	}
	if restoreUser {
		running, err := engineRunning()
		if err != nil {
			return err
		}
		if len(running) > 0 {
			return fmt.Errorf("還原個人詞庫前請完成組字並結束引擎（或登出再以系統管理員執行），否則引擎結束時會寫回舊內容：%s", strings.Join(running, "、"))
		}
	}
	left, err := update.RollbackWindows(basePath(), registrar{}, restoreUser)
	if err != nil {
		return err
	}
	ensureSettingsEntry()        // 回到沒有設定程式的舊版時移除開始功能表項目；回到有的版本則留著
	refreshStableMCP(basePath()) // 回到沒有附 0c-mcp 的舊版時移走，設定程式照實註明
	syncUninstallEntry()
	pruneFrom = pruneExecutable() // #179：舊版在背景清，不讓回復等它
	if version, err := installedVersion(); err == nil {
		handOverAfterInstall(version, func() { ensureEngineService(basePath()) })
		handOverUI(uiWaitInstall) // 回到的版本的 0c-ui 由 DLL 下一次需要時啟動
	}
	if restoreUser {
		fmt.Println("已回復上一版註冊，並還原升級前的個人詞庫；回復前的詞庫另存於新備份。要撤回，請用本版套件的 0c-update.exe 再執行 rollback -restore-user（0.1.0-dev.15 以前的工具不支援）。請登出再登入。")
	} else {
		fmt.Println("已回復上一版註冊；請登出再登入。個人詞庫保留；若舊版無法讀取新版格式，會照常輸入但不存檔，可改用 rollback -restore-user。")
	}
	reportLeftovers(left)
	return nil
}
func uninstall() error {
	if err := preflight(); err != nil {
		return err
	}
	changed, err := update.UninstallWindows(basePath(), registrar{})
	if err != nil {
		return err
	}
	pruneFrom = pruneExecutable() // #179：回復用的那一份之外，更早的在背景清
	// 收據已停用：移走 AI 工具用的 0c-mcp。AI 工具正在執行它時刪不掉，改名讓原路徑空出來（之後 bin 整個移除）。
	refreshStableMCP(basePath())
	stopUI(uiWaitInstall)
	if errors.Is(changed, update.ErrForeignRegistration) {
		fmt.Printf("已停用這個帳號的靈犀注音。%v（可能是其他 Windows 帳號的安裝），請在那個帳號移除。\n", changed)
		removeSettingsEntry()
		return nil
	}
	if changed != nil {
		fmt.Printf("注意：%v。仍照常取消註冊；若是防毒軟體隔離了檔案，重新安裝前請先確認。\n", changed)
	}
	removeSettingsEntry()
	fmt.Println("已取消靈犀注音註冊；請登出再登入。版本檔案、備份及個人詞庫保留，可用 rollback 恢復。")
	return nil
}

const restartHint = "完成組字後登出再登入，或在工作管理員結束 0c-server.exe，下一個按鍵會以新設定啟動引擎。"

// installIntegrity：Windows 的檔案核對在更新與解除安裝時提示（#82），status 不另外顯示。
func installIntegrity() string { return "" }
