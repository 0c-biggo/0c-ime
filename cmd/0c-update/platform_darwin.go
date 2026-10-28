//go:build darwin

package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/update"
)

var (
	migrateOnce sync.Once
	supportDir  string
	// bundleUpdater 是剛安裝的套件內附的 0c-update；排程改用它，背景更新後更新器才會跟著換新。
	bundleUpdater string
)

// 與 EngineClient 一致：個人詞庫、settings.json、收據與備份都在 Application Support/0c。
// 0.1.0-dev.21 以前在 Application Support/Zuin，第一次執行時搬過來並在原位置留下連結。
func paths() update.MacPaths {
	home, _ := os.UserHomeDir()
	support := filepath.Join(home, "Library/Application Support")
	migrateOnce.Do(func() {
		current, legacy := filepath.Join(support, "0c"), filepath.Join(support, "Zuin")
		supportDir = current
		if _, err := update.MigrateMacSupport(legacy, current); err != nil {
			fmt.Fprintln(os.Stderr, "0c-update: 無法搬移舊的個人資料目錄：", err)
			if info, e := os.Lstat(legacy); e == nil && info.IsDir() {
				if _, e = os.Stat(current); os.IsNotExist(e) {
					supportDir = legacy // 搬不動時照舊使用，不讓個人詞庫消失
				}
			}
		}
	})
	// 清理舊備份時跳過仍在使用的（#179，見 backup_inuse_mac.go）。
	// 回復時先把備份裡為了背景項目改名的排程 plist 改回（#205，見 agent_backup_mac.go）。
	return update.MacPaths{InputMethods: filepath.Join(home, "Library/Input Methods"), Support: supportDir, InUse: macBackupInUse(),
		PrepareRollback: func(app string) { restoreBundledAgent(app) }}
}
func basePath() string    { return paths().Support }
func receiptPath() string { return filepath.Join(basePath(), "install-receipt.json") }

const restartHint = "完成組字後執行 killall 0c-server，下一個按鍵會以新設定啟動引擎；或登出再登入。"

// activateTimeout：--activate-input-source 最久跑多久。0.2.7 起的工具會依序等使用者在父項與模式的系統詢問按「允許」
// （各約 60 秒）並自己在約 156 秒時結束（InputSourceApproval.deadline）；更早的工具沒有上限，系統設定卡住時會一直等
// （#238：登入後的排程 40 秒還沒結束）。這裡再加一層，安裝程式與排程不會無限期等下去。
var activateTimeout = 3 * time.Minute

// activateInputSource 以 App 內附的工具登記並啟用 app 的輸入來源，停用另一個名稱的舊來源。
// 失敗或逾時時回傳錯誤，由呼叫端提示手動加入、記下登入後補做。
func activateInputSource(tool, app string, force ...bool) error {
	args := []string{"--activate-input-source", app}
	if len(force) > 0 && force[0] {
		args = append(args, "--force")
	}
	ctx, cancel := context.WithTimeout(context.Background(), activateTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, tool, args...)
	cmd.WaitDelay = 5 * time.Second // 工具被結束後，它留下的子行程拿著輸出也不再等
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("%s（啟用輸入來源超過 %v 仍未結束）", strings.TrimSpace(string(out)), activateTimeout)
	}
	if err != nil {
		return fmt.Errorf("%s (%w)", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func install(bundle string, release *update.Release) error {
	if runtime.GOARCH != "arm64" {
		return fmt.Errorf("目前 macOS 套件只支援 Apple Silicon（arm64）")
	}
	// The matched engine must load its dictionary before the installed app changes.
	app := filepath.Join(bundle, update.MacApp)
	probe := exec.Command(filepath.Join(app, "Contents/MacOS/0c-server"), "-check-data", "-data", filepath.Join(app, "Contents/Resources/data"))
	if out, err := probe.CombinedOutput(); err != nil {
		return fmt.Errorf("配對詞庫檢查失敗：%s (%w)", out, err)
	}
	// 簽章身分（Team ID）改變時系統會撤銷原本的輸入法核准，要在安裝後主動觸發重新核准。
	before := installedTeam()
	// #167：0.2.1 以前 macOS 預設單按右 Shift 切換中英，升級的人保留；全新安裝採「不用 Shift」。
	// 在交接引擎之前寫好，新版引擎一啟動就讀到。
	if prev, err := readMacReceiptFile(receiptPath()); (err == nil || os.IsNotExist(err)) && macKeepsRightShift(prev, before != nil) {
		keepRightShift(basePath())
	}
	// 先確認兩個新舊端點都沒有組字，再正常存檔交接；交接失敗不改 App 或收據。
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	home, _ := os.UserHomeDir()
	var dialers []autoupdate.Dialer
	for _, name := range []string{"0c", "Zuin"} {
		sock := filepath.Join(home, "Library/Caches", name, "engine.sock")
		dialers = append(dialers, func(timeout time.Duration) (net.Conn, error) {
			return net.DialTimeout("unix", sock, timeout)
		})
	}
	if err := prepareMacInstall(ctx, dialers); err != nil {
		return err
	}
	destination := paths()
	// Headless manual installs remain supported, but an unknown boundary
	// cannot later be reported as verified automatic shell activation.
	destination.SessionASID, _ = macAquaSession(ctx)
	installed, changed, err := update.InstallMac(bundle, runtime.GOARCH, destination)
	if err != nil {
		return err
	}
	// 保留執行中舊 App 的完整簽章；只修復 0.2.5 留下的改名（#213）。
	restoreBackupAgents(filepath.Join(destination.Support, "backups"))
	if changed != nil {
		// 例如使用者改過 App 內的程式並重新簽章，或防毒軟體動過檔案（#83）：整個 App 已換成核對過的新版。
		fmt.Printf("原本安裝的 App 有檔案被改動或缺少（%v），已照常換成新版；原本的 App 保留在備份。\n", changed)
	}
	// 套件逐檔核對後移除下載隔離標記，並核對簽章完整。
	_ = exec.Command("xattr", "-dr", "com.apple.quarantine", installed).Run()
	if out, err := exec.Command("codesign", "--verify", "--deep", "--strict", installed).CombinedOutput(); err != nil {
		return fmt.Errorf("已安裝的 App 簽章無效：%s (%w)", out, err)
	}
	if u := filepath.Join(bundle, updaterName()); release.Files[updaterName()] != "" {
		bundleUpdater = u
	}
	forgetBackupApps() // 剛移入備份的舊 App 不能再被系統開啟（#77）
	registerInstalledApp(installed)
	ensureSettingsEntry() // 不依賴背景排程；pkg、一行安裝與完整 App 更新都經過這裡。
	fmt.Printf("已安裝靈犀注音至 %s\n個人詞庫保留。\n", installed)
	tool := filepath.Join(installed, "Contents/MacOS/0c")
	after := teamID(installed)
	signatureChanged := before != nil && *before != after
	if signatureChanged {
		fmt.Printf("這一版的程式簽章與上一版不同（%s → %s），macOS 會再詢問一次是否允許「靈犀注音」；請在「系統設定」的詢問中按「允許」，否則無法打字。\n",
			teamLabel(*before), teamLabel(after))
	}
	keepRightShiftAfterUpgrade(basePath()) // 記下這次安裝已判斷過（安裝前已寫好）
	if err = activateInputSource(tool, installed, signatureChanged); err != nil {
		fmt.Printf("輸入來源尚未完成啟用：%v\n", err)
		// 保留實際失敗原因，再由登入時的排程補做啟用。舊的輸入法程式照常能打字，留到重新登入。
		markRelogin(basePath())
		if e := deferInputSource(tool, installed); e != nil {
			fmt.Printf("請登出再登入，並在「系統設定 → 鍵盤 → 文字輸入 → 編輯」加入「靈犀注音」（%v）。\n", e)
			return nil
		}
		fmt.Println("請登出再登入；登入後會自動啟用「靈犀注音」輸入方式（系統詢問時請允許）。")
		return nil
	}
	if signatureChanged {
		// 舊程式的核准還在、照常能打字；結束它之後新版要等「允許」才能用，所以留到重新登入。
		markRelogin(basePath())
		fmt.Println("已啟用「靈犀注音」輸入方式；按「允許」後，登出再登入即改用新版。")
		return nil
	}
	os.Remove(reloginMarker(basePath()))
	clearInputSource(basePath()) // 之前的安裝沒啟用成功留下的記號：這次已啟用（#195）
	fmt.Println("已啟用「靈犀注音」輸入方式（系統詢問時請允許）。")
	// 使用者自己安裝：停手後立即交接並啟動新版。背景自動安裝（unattendedInstall）只在使用者離開時交接，
	// 沒交接的由之後的排程在使用者離開時補上（#178）。
	retired, left := retireMacShells(filepath.Join(installed, "Contents/MacOS/0c"), uiWaitInstall, unattendedInstall)
	switch {
	case len(left) > 0 && unattendedInstall:
		fmt.Println("新版會在你鎖定螢幕或離開電腦時換上，不必登出；在那之前照常可以打字。")
	case len(left) > 0:
		fmt.Println("輸入法正在使用中，新版會在你鎖定螢幕或離開電腦時換上，不必登出；在那之前照常可以打字。")
	case retired > 0:
		fmt.Println("已換成新版，不必登出。")
	case before != nil:
		fmt.Println("下一次切換到「靈犀注音」或開始打字，就是新版；不必登出。")
	}
	return nil
}

// teamID 回傳 App 的簽章 Team ID；ad-hoc 簽章為空字串。
func teamID(app string) string {
	out, _ := exec.Command("codesign", "-dv", app).CombinedOutput()
	for _, line := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(line, "TeamIdentifier="); ok && v != "not set" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// installedTeam 回傳目前已安裝 App（0c.app 或舊的 Zuin.app）的 Team ID；沒有安裝時為 nil。
func installedTeam() *string {
	p := paths()
	for _, name := range []string{update.MacApp, update.LegacyMacApp} {
		app := filepath.Join(p.InputMethods, name)
		if fileExists(app) {
			t := teamID(app)
			return &t
		}
	}
	return nil
}

func teamLabel(t string) string {
	if t == "" {
		return "ad-hoc"
	}
	return t
}

// drainEngines 請仍在執行的舊引擎（含 0.1.0-dev.21 以前 Zuin.app 的）在沒有組字時存檔退出；
// 下一個按鍵由新版 App 啟動新引擎。舊引擎留著時，新外殼可能連到不相容的舊版本。
func drainEngines() {
	home, _ := os.UserHomeDir()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, name := range []string{"0c", "Zuin"} {
		sock := filepath.Join(home, "Library/Caches", name, "engine.sock")
		dial := func(timeout time.Duration) (net.Conn, error) { return net.DialTimeout("unix", sock, timeout) }
		if err := autoupdate.Drain(ctx, dial, 15*time.Second); err != nil {
			fmt.Printf("執行中的引擎正在組字，完成組字後執行 killall 0c-server 即改用新版（%v）。\n", err)
		}
	}
}

// drainResidentEngine：0.2.0 起引擎由排程常駐，不會隨閒置退出（#120）。回復後舊版 App 會連到新版引擎、
// 移除後引擎留在已刪除的 App 裡繼續跑到登出，所以搬移完成後一律請它交接退出。
func drainResidentEngine() { drainEngines() }

func rollback(_ string, restoreUser bool) error {
	if restoreUser {
		return fmt.Errorf("macOS 目前的回復不另存個人詞庫；個人詞庫保留不變")
	}
	// SMAppService 依 App 目前的位置找排程：App 移入備份後就取消不了，必須在搬移之前處理。
	state := ""
	if app := installedApp(); fileExists(appAgent(app)) {
		state, _ = agentState(app, "status")
		target, e := update.MacRollbackTarget(paths())
		// 啟用中的排程一律先取消，回復後依回復的 App 重新建立，背景項目不會指向移入備份的 App。
		// 使用者在系統設定關閉的保留原狀，除非回復到沒有內附排程的舊 App（之後不會再用到這筆紀錄）。
		if state == "enabled" || e == nil && !bundlesAgent(target) {
			_, _ = agentState(app, "unregister")
		}
	}
	r, err := update.RollbackMac(paths())
	if err != nil {
		if state == "enabled" {
			_, _ = agentState(installedApp(), "register")
		}
		return err
	}
	drainResidentEngine()
	if state != "" && !fileExists(appAgent(installedApp())) {
		// 搬移前的取消在 macOS 26 實測不一定生效；以移入備份的 App 再取消，背景項目不留下啟用中的舊紀錄。
		unregisterMovedAgent(filepath.Join(r.Backup, update.MacApp))
	}
	// 備份必須維持完整簽章；舊外殼安全退場後由 adopt 接手排程。
	restoreBackupAgents(filepath.Join(basePath(), "backups"))
	keep := state != "requires-approval"
	if s, e := autoupdate.Load(basePath()); e == nil && s.ScheduleRemoved {
		keep = false
	}
	if app := installedApp(); keep && fileExists(appAgent(app)) {
		removeLegacyAgent()
		if state, e := agentState(app, "register"); e == nil {
			fmt.Println(agentStatusText(state))
		}
	} else if keep && state != "" {
		if e := installLegacyAgent(stableUpdater(basePath()), basePath()); e != nil {
			fmt.Println("自動更新排程未建立：", e)
		}
	}
	// 回復後的 App 可能是舊名稱 Zuin.app（不同的輸入來源）：用剛移入備份的新版工具切換。
	relogin := false
	if r.App != "" {
		if tool := filepath.Join(r.Backup, update.MacApp, "Contents/MacOS/0c"); fileExists(tool) {
			if err = activateInputSource(tool, r.App); err == nil {
				clearInputSource(basePath())
			} else {
				relogin = true
				markRelogin(basePath())
				if e := deferInputSource(tool, r.App); e != nil {
					fmt.Printf("登出再登入後，請在系統設定中確認「靈犀注音」已加入（%v）。\n", e)
				}
			}
		}
	}
	forgetBackupApps()
	registerInstalledApp(r.App)
	ensureSettingsEntry()
	if r.Changed != nil {
		fmt.Printf("回復前的 App 有檔案被改動或缺少（%v），已照常移入備份。\n", r.Changed)
	}
	if r.Restored != nil {
		fmt.Printf("放回的 App 有檔案被改動或缺少（%v），仍照常回復；若無法打字，請重新安裝最新版。\n", r.Restored)
	}
	if relogin || r.App == "" {
		fmt.Println("已回復上一個 App；請登出再登入。個人詞庫保留。")
		return nil
	}
	// 回復後仍在執行的是剛移入備份的那一版：停手後結束，系統下一次需要時開回復的 App（#170）。
	fmt.Println("已回復上一個 App，個人詞庫保留。")
	exe := filepath.Join(r.App, "Contents/MacOS/0c")
	if filepath.Base(r.App) == update.LegacyMacApp {
		exe = filepath.Join(r.App, "Contents/MacOS/Zuin")
	}
	if retired, left := retireMacShells(exe, uiWaitInstall, false); len(left) > 0 {
		fmt.Println("輸入法正在使用中，回復的版本會在你鎖定螢幕或離開電腦時換上，不必登出。")
	} else if retired > 0 {
		fmt.Println("已換成回復的版本，不必登出。")
	} else {
		fmt.Println("下一次切換到「靈犀注音」或開始打字，就是回復的版本；不必登出。")
	}
	if filepath.Base(r.App) == update.MacApp {
		ensureEngineService(basePath()) // 回復的引擎先起來：系統開回復的 App 後第一個按鍵不必等引擎冷啟動
	}
	return nil
}
func uninstall() error {
	if app := installedApp(); fileExists(appAgent(app)) {
		_, _ = agentState(app, "unregister") // App 移走後就無法再取消註冊
	}
	changed, err := update.UninstallMac(paths())
	if err != nil {
		return err
	}
	drainResidentEngine()
	if changed != nil {
		fmt.Printf("已安裝的 App 有檔案被改動或缺少（%v），仍照常移除。\n", changed)
	}
	if target, err := update.MacRollbackTarget(paths()); err == nil {
		unregisterMovedAgent(target) // 搬移前的取消不一定生效，見 unregisterMovedAgent
	}
	// 舊外殼尚未退場時也會讀這個移除選擇，不再自行補註冊；不破壞備份簽章。
	scheduleErr := setScheduleRemoved(basePath(), true)
	restoreBackupAgents(filepath.Join(basePath(), "backups"))
	forgetBackupApps()
	removeSettingsEntry()
	// 移入備份的 App 仍在執行：停手後結束，不留在背景跑到登出（#170）。
	stopUI(uiWaitInstall)
	os.Remove(reloginMarker(basePath()))
	fmt.Println("已移除靈犀注音 App（移入備份，可用 rollback 恢復）；請在選單列的輸入方式改用其他輸入法。個人詞庫、設定與備份保留。")
	if scheduleErr != nil {
		return fmt.Errorf("App 已移除，但無法記錄排程移除選擇：%w", scheduleErr)
	}
	return nil
}

// installIntegrity 是 status 顯示的 App 核對結果：與收據不符時說明哪些檔案，並說明更新仍會照常換新。
func installIntegrity() string {
	changed, err := update.CheckMac(paths())
	switch {
	case err != nil:
		return fmt.Sprintf("App 核對：%v", err)
	case changed != nil:
		return fmt.Sprintf("App 核對：%v；更新時會整個換成新版，原本的 App 移入備份。", changed)
	}
	return ""
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }
