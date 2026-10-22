package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/0c-biggo/0c-ime/core/fuzzy"
	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/neural"
	"github.com/0c-biggo/0c-ime/internal/settings"
	"github.com/0c-biggo/0c-ime/internal/update"
)

// DefaultFeed 是正式發布的已簽章索引（S3 bucket 0c-releases，公開路徑 https://s3.biggo.com/0c-releases/<key>）。
const DefaultFeed = "https://s3.biggo.com/0c-releases/feeds/public-beta.json"

// pointerSince 是第一個認得引擎指標的完整安裝版本；更舊的外殼只能以完整安裝更新。
const pointerSince = "0.1.0-dev.18"

// unattendedInstall：這次安裝由背景自動更新執行、沒有使用者在看（macOS 的輸入法程式交接只在使用者離開時做，#178）。
// 終端機、.pkg 與設定程式「立即安裝」（finish）都是使用者自己執行的安裝。這個程式一次只執行一個命令。
var unattendedInstall bool

// system 是真實作業系統上的 autoupdate.Platform。
type system struct {
	base      string
	feed      string
	consent   bool          // 使用者親自執行 finish：可以做需要確認的安裝
	shellIdle time.Duration // 外殼切換前重新核對的閒置門檻
	atLogin   bool          // 登入時、桌面啟動前（Linux login 單元）：可以換外殼
}

func (p *system) Installed() (string, error) {
	if v := autoupdate.PointerVersion(p.base); v != "" {
		return v, nil
	}
	return installedVersion()
}
func (p *system) Running(context.Context) (string, bool, error) {
	info, running, err := autoupdate.Health(dialEngine)
	return info["version"], running, err
}
func (p *system) Fetch(ctx context.Context, lastSerial uint64) (*update.SignedFeed, error) {
	f, _, err := update.ReadSignedFeed(ctx, update.Client(), p.feed, update.TrustedKeys(), time.Now(), lastSerial)
	return f, err
}
func (p *system) Download(ctx context.Context, t autoupdate.Target, dir string) (string, error) {
	goos, arch := p.Target()
	keep := "0c-" + t.SHA256
	entries, _ := os.ReadDir(dir)
	for _, e := range entries { // 只保留本次目標
		if !strings.HasPrefix(e.Name(), keep) {
			os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
	interactive := p.consent || autoupdate.DownloadRequested(p.base, t)
	var lastRequest, lastProgress time.Time
	return update.DownloadWithOptions(ctx, update.Client(), update.Asset{OS: goos, Arch: arch, URL: t.URL, SHA256: t.SHA256, Size: t.Size}, dir, update.DownloadOptions{
		BytesPerSecond: update.BackgroundBytesPerSecond,
		Rate: func() int64 {
			if !interactive && time.Since(lastRequest) >= 250*time.Millisecond {
				lastRequest = time.Now()
				interactive = autoupdate.DownloadRequested(p.base, t)
			}
			if interactive {
				return 0
			}
			return update.BackgroundBytesPerSecond
		},
		Progress: func(progress update.DownloadProgress) {
			if progress.Phase == "downloading" && progress.Received != progress.Total && time.Since(lastProgress) < 250*time.Millisecond {
				return
			}
			lastProgress = time.Now()
			_ = autoupdate.WriteOperation(p.base, autoupdate.Operation{Version: t.Version, SHA256: t.SHA256, Phase: progress.Phase, Received: progress.Received, Total: progress.Total, Interactive: interactive})
		},
	})
}
func (p *system) Prepare(_ context.Context, archive string, t autoupdate.Target) (string, error) {
	layout, ok := coreLayout()
	if !ok || !shellSupportsPointer() {
		return "", autoupdate.ErrNeedsShell
	}
	goos, arch := p.Target()
	return autoupdate.PrepareCore(p.base, scratchRoot(), archive, t, goos, arch, layout)
}
func (p *system) SwitchCore(dir, version string) error {
	return autoupdate.SwitchPointer(p.base, dir, version)
}
func (p *system) RevertCore(dir string) error { return autoupdate.RevertPointer(p.base, dir) }
func (p *system) Drain(ctx context.Context) error {
	return autoupdate.Drain(ctx, dialEngine, 40*time.Second)
}
func (p *system) StartEngine(ctx context.Context) error {
	if err := startEngine(p.base); err != nil {
		return err
	}
	return waitForEngine(ctx, dialEngine)
}

// waitForEngine 等引擎開始服務，最多 15 秒。
func waitForEngine(ctx context.Context, dial autoupdate.Dialer) error {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := dial(time.Second); err == nil {
			c.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return fmt.Errorf("引擎未在 15 秒內開始服務")
}
func (p *system) InstallShell(ctx context.Context, archive string) (bool, error) {
	if !p.ReloadsShell() && deferShell() && !p.atLogin {
		return false, autoupdate.ErrAtLogin
	}
	if !p.consent && !p.atLogin && !shellUnattended() {
		return false, autoupdate.ErrNeedsConsent
	}
	s, err := autoupdate.Load(p.base)
	if err != nil || s.Target == nil {
		return false, fmt.Errorf("沒有待安裝的版本")
	}
	goos, arch := p.Target()
	root, cleanup, err := update.StageIn(scratchRoot(), archive, s.Target.SHA256, goos, arch)
	if err != nil {
		return false, err
	}
	defer cleanup()
	r, err := update.Verify(root, goos, arch)
	if err != nil {
		return false, err
	}
	if r.Version != s.Target.Version {
		return false, fmt.Errorf("套件版本 %s 與索引 %s 不符", r.Version, s.Target.Version)
	}
	if p.ReloadsShell() {
		return p.installReloadableShell(ctx, root, r)
	}
	// 沒有使用者親自確認的安裝（背景自動更新）：macOS 只在使用者離開時交接輸入法程式（#178）。
	unattendedInstall = !p.consent
	if err = install(root, r); err != nil {
		return false, err
	}
	afterInstall()
	// Windows 的外殼在重新登入後才全面載入；macOS 安裝時已請舊的輸入法程式結束（#170），只有要重新登入的情況才回 pending，
	// 其餘由協調器接著以 ShellReady 核對。
	return shellPendingAfterInstall(), nil
}

func (p *system) ReloadsShell() bool { return reloadableShell() }

func (p *system) ShellNeedsIdle() bool { return runtime.GOOS == "darwin" }

// Conditions：OS 無法判斷閒置時（Wayland 非 GNOME、無桌面等），以引擎距上次按鍵的時間代替；
// 引擎沒有執行即視為閒置。
func (p *system) Conditions() autoupdate.Conditions {
	c := conditions()
	if c.IdleKnown {
		return c
	}
	info, running, err := autoupdate.Health(dialEngine)
	switch {
	case err == nil && !running:
		c.Idle, c.IdleKnown = 24*time.Hour, true
	case err == nil && info["idle_seconds"] != "":
		var sec int
		if _, e := fmt.Sscan(info["idle_seconds"], &sec); e == nil {
			c.Idle, c.IdleKnown = time.Duration(sec)*time.Second, true
		}
	}
	return c
}
func (p *system) Notify(n autoupdate.Notice) error { return notify(n) }
func (p *system) Target() (string, string)         { return runtime.GOOS, runtime.GOARCH }

// Activated：核心更新生效後，把新版的 0c-update 換成排程使用的穩定副本，並清掉用不到的核心目錄。
func (p *system) Activated(t autoupdate.Target, dir string) {
	if dir != "" {
		src := filepath.Join(p.base, filepath.FromSlash(dir), filepath.Base(updaterName()))
		if _, err := os.Stat(src); err == nil {
			_ = replaceUpdater(src, stableUpdater(p.base))
		}
	}
	refreshStableMCP(p.base) // 核心目錄附了新的 0c-mcp 時換上（Windows）
	autoupdate.PruneCore(p.base)
	os.RemoveAll(filepath.Join(p.base, "update", "downloads"))
}

// installStagedShell 在登入時完成排定的外殼更新：已下載、已核對的套件以完整安裝套用。
func installStagedShell(p *system) error {
	s, err := autoupdate.Load(p.base)
	if err != nil || s.Stage != autoupdate.StagePending || s.Target == nil || s.Target.Kind != "shell" || s.Download == "" {
		return err
	}
	if v, _ := installedVersion(); v == s.Target.Version {
		return nil
	}
	_, err = p.InstallShell(context.Background(), s.Download)
	return err
}

func updaterName() string {
	if runtime.GOOS == "windows" {
		return "0c-update.exe"
	}
	return "0c-update"
}

// stableUpdater 是排程執行的 0c-update 位置，不隨版本目錄移動。
func stableUpdater(base string) string { return filepath.Join(base, "bin", updaterName()) }

// removeOldCopies 清掉先前替換留下的舊檔（dst.old、dst.old-N）；仍在執行的刪不掉，留到下次。
func removeOldCopies(dst string) {
	entries, err := os.ReadDir(filepath.Dir(dst))
	if err != nil {
		return
	}
	prefix := filepath.Base(dst) + ".old"
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			os.Remove(filepath.Join(filepath.Dir(dst), e.Name()))
		}
	}
}

// replaceUpdater 以改名換上新檔；Windows 上執行中的檔案不能覆寫但可改名，舊檔下次清除。
func replaceUpdater(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	if a, err := filepath.Abs(src); err == nil {
		if b, err := filepath.Abs(dst); err == nil && strings.EqualFold(a, b) {
			return nil
		}
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	os.Remove(tmp)
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	// 舊檔改名後留給下次清除（Windows 上執行中的檔案刪不掉）。名字每次都不同：上一次留下的 .old 可能仍被執行中的
	// 行程當作映像（例如下載很久的 auto），固定名字會讓下一次替換在改名時失敗，穩定副本就停在舊版。
	removeOldCopies(dst)
	old := fmt.Sprintf("%s.old-%d", dst, time.Now().UnixNano())
	if err = os.Rename(dst, old); err != nil && !errors.Is(err, os.ErrNotExist) {
		os.Remove(tmp)
		return err
	}
	if err = os.Rename(tmp, dst); err != nil {
		os.Rename(old, dst)
		return err
	}
	os.Remove(old) // Windows 上仍在執行時會失敗，留待下次
	return nil
}

func shellSupportsPointer() bool {
	v, err := installedVersion()
	if err != nil || v == "" {
		return false
	}
	order, err := update.CompareVersion(v, pointerSince)
	return err == nil && order >= 0
}

// autoCommand 處理 auto、finish、mode、pause、resume、schedule。
func autoCommand(action string, args []string) (resultErr error) {
	jsonRequested := false
	for _, arg := range args {
		if isJSONFlag(arg) {
			jsonRequested = true
		}
	}
	defer func() {
		if jsonRequested && resultErr != nil && !errors.Is(resultErr, errReported) {
			if errors.Is(resultErr, autoupdate.ErrLocked) {
				resultErr = autoupdate.ErrUpdateInProgress
			}
			resultErr = failJSON(os.Stdout, resultErr)
		}
	}()
	base := basePath()
	switch action {
	case "mode":
		if len(args) != 1 || (args[0] != autoupdate.ModeAuto && args[0] != autoupdate.ModeAsk && args[0] != autoupdate.ModeManual) {
			return fmt.Errorf("用法：0c-update mode auto|ask|manual")
		}
		return editState(base, func(s *autoupdate.State) { s.Mode = args[0] })
	case "pause":
		if len(args) != 1 {
			return fmt.Errorf("用法：0c-update pause <天數>，例如 pause 7")
		}
		var days int
		if _, err := fmt.Sscan(args[0], &days); err != nil || days < 1 || days > 30 {
			return fmt.Errorf("暫停天數須為 1–30")
		}
		return editState(base, func(s *autoupdate.State) { s.PausedUntil = time.Now().Add(time.Duration(days) * 24 * time.Hour) })
	case "resume":
		return editState(base, func(s *autoupdate.State) { s.PausedUntil = time.Time{} })
	case "snooze":
		flags := flag.NewFlagSet(action, flag.ContinueOnError)
		target := flags.String("target", "", "通知中的版本")
		hash := flags.String("target-sha256", "", "通知中的資產 SHA-256")
		if err := flags.Parse(args); err != nil {
			return err
		}
		_, hashError := hex.DecodeString(*hash)
		if flags.NArg() != 0 || !update.ValidVersion(*target) || len(*hash) != 64 || hashError != nil || strings.ToLower(*hash) != *hash {
			return fmt.Errorf("用法：0c-update snooze -target <版本> -target-sha256 <SHA-256>")
		}
		return autoupdate.SnoozeNotice(base, *target, *hash, time.Now())
	case "protocol":
		if len(args) != 1 {
			return fmt.Errorf("不支援的網址")
		}
		command, commandArgs, err := noticeProtocolCommand(args[0])
		if err != nil {
			notifyOutcome(nil, err)
			return err
		}
		return autoCommand(command, commandArgs)
	case "notify-action":
		// 由有按鈕的通知啟動：等使用者回應，按下「立即安裝」就執行 finish 並回報結果。
		flags := flag.NewFlagSet(action, flag.ContinueOnError)
		n := autoupdate.Notice{}
		flags.StringVar(&n.Title, "title", "", "")
		flags.StringVar(&n.Body, "body", "", "")
		flags.StringVar(&n.Action, "action", "finish", "")
		flags.StringVar(&n.TargetVersion, "target", "", "")
		flags.StringVar(&n.TargetSHA256, "target-sha256", "", "")
		if err := flags.Parse(args); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Hour)
		defer cancel()
		chosen, err := notifyWithAction(ctx, n)
		if err == nil && chosen == "later" {
			return autoupdate.SnoozeNotice(base, n.TargetVersion, n.TargetSHA256, time.Now())
		}
		if err != nil || chosen != "finish" {
			return err
		}
		finishArgs := []string{"-notify"}
		if n.TargetVersion != "" || n.TargetSHA256 != "" {
			finishArgs = append(finishArgs, "-target", n.TargetVersion, "-target-sha256", n.TargetSHA256)
		}
		return autoCommand("finish", finishArgs)
	case "schedule":
		flags := flag.NewFlagSet(action, flag.ContinueOnError)
		feed := flags.String("feed", "", "改用其他已簽章索引（測試用）；default 回到正式索引")
		report := flags.Bool("notify", false, "repair：無法自動修復時以通知告知使用者")
		var test autoupdate.TestTiming
		if update.E2EBuild {
			flags.Func("test-check", "E2E：查詢週期", durationFlag(&test.CheckEvery))
			flags.Func("test-idle", "E2E：切換前需要的閒置時間", durationFlag(&test.IdleNeeded))
			flags.Func("test-remind", "E2E：已準備未生效多久後提醒", durationFlag(&test.RemindAfter))
		}
		if len(args) == 0 {
			return fmt.Errorf("用法：0c-update schedule install|remove|repair|status|open-settings [-feed URL|default] [-notify]")
		}
		sub := args[0]
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("不接受額外位置參數")
		}
		if test != (autoupdate.TestTiming{}) {
			if err := editState(base, func(s *autoupdate.State) { s.Test = &test }); err != nil {
				return err
			}
		}
		switch sub {
		case "configure":
			// E2E feed selection must not register, restart, or trigger the OS
			// job. Installation still creates its own production schedule.
			if !update.E2EBuild || *feed == "" {
				return fmt.Errorf("configure 僅供 E2E 指定 -feed；不變更 OS 排程")
			}
			return setFeed(base, *feed)
		case "install":
			if *feed != "" {
				if err := setFeed(base, *feed); err != nil {
					return err
				}
			}
			if err := installSchedule(base); err != nil {
				return err
			}
			return setScheduleRemoved(base, false)
		case "remove":
			if err := removeSchedule(); err != nil {
				return err
			}
			return setScheduleRemoved(base, true)
		case "repair":
			// 由 0c-ui 定期以版本目錄內的 0c-update 執行（穩定副本可能已被防毒軟體移除）。
			ensureEngineTask(base)
			ensureEngineService(base)
			repairFixedDLL(base)
			refreshStableMCP(base) // 外殼更新由舊的穩定更新器執行，它不認得 0c-mcp；由新版在這裡補上（Windows）
			listInputMethod(base)  // 同上，舊的更新器不會把靈犀注音加入輸入法清單（Windows，#191）
			return repairUpdater(base, *report, platformRepair)
		case "status":
			fmt.Println(scheduleStatus())
			return nil
		case "adopt":
			// 內部使用（macOS，#205）：status 發現 launchd 啟動不了排程時，在脫離的行程裡重新註冊。
			adoptSchedule(true)
			return nil
		case "open-settings":
			// macOS：使用者在系統設定關閉背景執行後，開啟登入項目頁讓他重新允許。
			return openScheduleSettings()
		}
		return fmt.Errorf("未知的 schedule 動作：%s", sub)
	}
	flags := flag.NewFlagSet(action, flag.ContinueOnError)
	asJSON := flags.Bool("json", false, "以 JSON 回報更新結果")
	feed := flags.String("feed", "", "本次改用的已簽章索引；預設為 schedule install -feed 設定的或正式索引")
	now := flags.Bool("now", false, "立即查詢，不等下一次到期")
	login := flags.Bool("login", false, "登入時、桌面啟動前執行（只做本機工作，不查詢或下載）")
	report := flags.Bool("notify", false, "完成後以通知回報結果（從選單或通知按鈕啟動時）")
	target := flags.String("target", "", "只完成此通知／選單指定的版本")
	targetHash := flags.String("target-sha256", "", "通知／選單指定的資產 SHA-256")
	originUser := flags.String("origin-user", "", "UAC 交接的原使用者（內部使用）")
	originBase := flags.String("origin-base", "", "UAC 交接的原安裝目錄（內部使用）")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("不接受額外位置參數")
	}
	// 這個 CLI 一個行程只執行一個命令。-json 時整個命令的 stdout 只留結果的那一份 JSON（設定程式「立即安裝」以
	// serde_json 解析整份 stdout）：安裝程式、登入時補做輸入法清單等印給人看的訊息一律改到 stderr（#195）。
	// 錯誤由 autoCommand 的 defer 以 failJSON 寫到 os.Stdout，這裡的 defer 先執行，屆時已換回原本的 stdout。
	output := os.Stdout
	if *asJSON {
		os.Stdout = os.Stderr
		defer func() { os.Stdout = output }()
	}
	if err := validateElevationOrigin(action, *originUser, *originBase); err != nil {
		return err
	}
	if *target != "" || *targetHash != "" {
		_, hashError := hex.DecodeString(*targetHash)
		if action != "finish" || !update.ValidVersion(*target) || len(*targetHash) != 64 || hashError != nil || strings.ToLower(*targetHash) != *targetHash {
			return fmt.Errorf("通知更新目標無效")
		}
	}
	if action == "finish" && !elevatedOK() && pendingShell(base) {
		args := []string{"finish"}
		if *feed != "" {
			args = append(args, "-feed", *feed)
		}
		if *target != "" {
			args = append(args, "-target", *target, "-target-sha256", *targetHash)
		}
		err := relaunchElevated(args)
		if *report {
			s, loadErr := autoupdate.Load(base)
			if err == nil && loadErr != nil {
				err = loadErr
			}
			notifyOutcome(s, err)
		}
		if err == nil && *asJSON {
			installed, _ := installedVersion()
			s, loadErr := autoupdate.Load(base)
			if loadErr != nil {
				return loadErr
			}
			return emitJSON(output, checkResult{Schema: contractSchema, OK: true, Update: autoupdate.ViewOf(s, installed)})
		}
		return err
	}
	if *feed == "" {
		*feed = DefaultFeed
		if s, err := autoupdate.Load(base); err == nil && s.Feed != "" {
			*feed = s.Feed
		}
	}
	p := &system{base: base, feed: *feed, consent: action == "finish", atLogin: *login}
	if *login && !p.ReloadsShell() {
		if err := installStagedShell(p); err != nil {
			fmt.Fprintln(os.Stderr, "0c-update: 登入時安裝排定的更新失敗：", err)
		}
	}
	// macOS 的 launchd 在登入時以 auto 喚起（不帶 -login）；新安裝的輸入法要重新登入後才登記得到，所以每次都補試。
	// macOS 看安裝時留下的記號（inputSourcePending），不看 ProfileAtLogin（#195）；記號留著時每個登入工作階段補試一次，
	// 失敗的重試只會一直跳系統詢問，連續幾個工作階段都失敗就停止（addInputMethod）。不補做時只有背景排程的 auto
	// 查詢使用者是不是已經自己加入（一個工作階段一次）；設定程式「立即安裝」、選單、通知按鈕不必等這個查詢。
	if *login || retriesInputMethodEveryRun {
		if s, err := autoupdate.Load(base); err == nil && (s.ProfileAtLogin || inputSourcePending(base)) {
			if err := retryLoginInputMethod(action == "auto" && !*asJSON && !*report); err != nil {
				fmt.Fprintln(os.Stderr, "0c-update: 加入輸入法清單失敗：", err)
			} else if s.ProfileAtLogin { // macOS 只有記號時不必改狀態檔；改了也不印狀態，免得混進 -json 的結果
				_, _ = autoupdate.EditState(base, func(s *autoupdate.State) error { s.ProfileAtLogin = false; return nil })
			}
		}
	}
	// #93 卡住時換上的外殼：重新登入後把 Fcitx5 存掉的靈犀注音補回輸入法清單（一次）。
	restoreStuckInputMethod(base, *login)
	// #204 KDE Plasma Wayland：補寫上次沒寫回的 kwinrc 虛擬鍵盤（登入時也做，KWin 啟動時就讀得到）；背景排程另外把
	// 舊版更新器自己啟動的 Fcitx5 在閒置時交還 KWin（一次性修復，條件不成立時不動）。
	finishKWinIM(base)
	if action == "auto" && !*login && !*asJSON && !*report {
		repairKWinFcitx(base)
	}
	// 登入單元排在 graphical-session-pre.target 之前，桌面要等它結束才會啟動；
	// 這裡只做上面的本機工作。查詢索引、下載（可達數百 MB、背景限速）與外殼重載／
	// 中斷回復都交給 0c-update.timer，否則使用者登入後會停在黑畫面直到完成或逾時。
	// #120：0.2.0 起引擎常駐，引擎沒在跑就叫起來。以暫時性 service 啟動只要幾十毫秒、不等它載入，
	// 登入單元也做，引擎在登入當下就起來，不必等 timer 第一次喚起（OnStartupSec=5min）。
	ensureEngineService(base)
	// Windows：背景完整更新由舊的穩定更新器安裝，它不會請舊版的 0c-ui 結束；新版的穩定副本每次喚起時補上。
	handOverUI(uiWaitAuto)
	if *login {
		return nil
	}
	co := &autoupdate.Coordinator{Base: base, Platform: p, Force: *now, Consent: action == "finish", ConsentVersion: *target, ConsentSHA256: *targetHash}
	if s, err := autoupdate.Load(base); err == nil && s.Test != nil && update.E2EBuild {
		co.CheckEvery, co.IdleNeeded, co.RemindAfter = time.Duration(s.Test.CheckEvery), time.Duration(s.Test.IdleNeeded), time.Duration(s.Test.RemindAfter)
		co.CheckJitter = time.Second
	}
	p.shellIdle = co.IdleNeeded
	if p.shellIdle == 0 {
		p.shellIdle = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	s, err := co.Run(ctx)
	refreshPathUpdater(base)
	if action == "auto" && !*asJSON && !*report {
		// #179：安裝後啟動的背景清理沒做完（關機、被結束）時由排程補做；這次裝了新版就在這裡一起清，不另外啟動。
		// 只在背景排程做，選單、設定程式等著結果的呼叫不做。
		pruneInstalls(base)
		pruneFrom = ""
	}
	if *report {
		notifyOutcome(s, err)
	}
	if err != nil {
		return err
	}
	if *asJSON {
		installed, _ := p.Installed()
		return emitJSON(output, checkResult{Schema: contractSchema, OK: true, Update: autoupdate.ViewOf(s, installed)})
	}
	printState(s)
	if action == "finish" && s.Target == nil {
		// 只完成已下載的更新；修復安裝不靠它（#93 的安裝器曾誤導使用者來執行它）。
		fmt.Println("目前沒有已下載、待完成的更新。")
	}
	return nil
}

// retriesInputMethodEveryRun：每次 auto／finish 都補做登入時的輸入法清單（macOS，見 autoCommand 裡的說明）；測試在 Linux 上打開它走同一條路。
var retriesInputMethodEveryRun = runtime.GOOS == "darwin"

// retryLoginInputMethod 是 autoCommand 補做輸入法清單的入口（retryInputMethod）；測試替換用，確認只有背景排程會查詢。
var retryLoginInputMethod = retryInputMethod

// notifyOutcome 回報使用者親自要求（選單、通知按鈕）的結果；背景排程不發這類通知。
func notifyOutcome(s *autoupdate.State, err error) {
	n := autoupdate.Notice{Title: "靈犀注音"}
	switch {
	case err != nil:
		n.Body = "更新沒有完成：" + err.Error()
	case s.Target == nil && s.CheckFailures > 0:
		n.Body = "目前無法連線更新伺服器，稍後會自動再試。"
	case s.Stage == autoupdate.StageActive || s.Stage == autoupdate.StageIdle:
		v, _ := (&system{base: basePath()}).Installed()
		n.Body = "已是最新版本 " + v + "。"
	case s.Blocked != nil && s.Blocked.Reason == "needs_relogin":
		n.Body = s.Target.Version + " " + s.Blocked.Detail + "。"
	case s.Blocked != nil && s.Blocked.Reason == "engine_busy":
		if s.Mode == autoupdate.ModeAuto {
			n.Body = "請先完成組字，閒置時會再嘗試更新至 " + s.Target.Version + "。"
		} else {
			n.Body = "請先完成組字，再按「完成更新」以更新至 " + s.Target.Version + "。"
		}
	case s.Blocked != nil:
		n.Body = "更新暫時無法完成：" + s.Blocked.Detail
	default:
		n.Body = "更新進行中（" + s.Stage + "）。"
	}
	_ = notify(n)
}

// pendingShell 回報待完成的是否為外殼更新（Windows 需要系統管理員；核心更新不需要）。
func pendingShell(base string) bool {
	s, err := autoupdate.Load(base)
	return err == nil && s.Target != nil && s.Target.Kind == "shell"
}

func durationFlag(d *autoupdate.Duration) func(string) error {
	return func(v string) error {
		x, err := time.ParseDuration(v)
		if err != nil || x <= 0 {
			return fmt.Errorf("無效的時間：%s", v)
		}
		*d = autoupdate.Duration(x)
		return nil
	}
}

// scheduleEvery 是排程喚起間隔：15 分鐘；E2E 建置設定了測試週期時縮短為 1 分鐘（L3 以正式參數執行）。
func scheduleEvery(base string) time.Duration {
	if update.E2EBuild {
		if s, err := autoupdate.Load(base); err == nil && s.Test != nil {
			return time.Minute
		}
	}
	return 15 * time.Minute
}

func setFeed(base, feed string) error {
	if feed == "default" {
		feed = ""
	} else if !strings.HasPrefix(feed, "https://") {
		return fmt.Errorf("更新索引必須是 HTTPS 網址")
	}
	return editState(base, func(s *autoupdate.State) {
		if s.Feed != feed {
			// 不同的索引有各自的序號；前一個索引的失敗與退避不延後新索引的查詢。
			s.Feed, s.FeedSerial = feed, 0
			s.NextCheck, s.CheckFailures, s.LastError = time.Time{}, 0, ""
			s.LastCheck = time.Time{}
		}
	})
}

// afterInstall：完整安裝成功後建立（或更新）自動更新排程；失敗只提示，不影響安裝結果。
func afterInstall() {
	if os.Getenv("ZC_NO_SCHEDULE") != "" {
		return
	}
	// 0.2.3 以前的 worker 會讓 onnxruntime 留下未送出的遙測事件；新版不再讀寫，安裝後刪掉，免得別的程式送出（docs/46）。
	if home, err := os.UserHomeDir(); err == nil {
		if err := neural.RemoveTelemetryQueue(home); err != nil {
			fmt.Fprintln(os.Stderr, "0c-update: 舊的推論元件遙測暫存未刪除：", err)
		}
	}
	// Windows：第一次安裝（或從還沒有這一步的版本更新）時把靈犀注音加入這個帳號的輸入法清單（#191）。
	listInputMethod(basePath())
	if err := installSchedule(basePath()); err != nil {
		fmt.Fprintln(os.Stderr, "0c-update: 自動更新排程未建立：", err)
		return
	}
	_ = setScheduleRemoved(basePath(), false)
	engineAfterInstall(basePath())
	ensureSettingsEntry() // 排程之後做，ZC_NO_SCHEDULE 的測試環境不會碰到真實的應用程式選單
}

// forgetInstalledTarget：使用者自己完整安裝後，背景更新準備的同版或舊版待辦已沒有意義，
// 立刻清掉，選單才不會繼續顯示「立即安裝」同一版。失敗只提示，下一次查詢也會清掉。
func forgetInstalledTarget(installed string) {
	if _, err := autoupdate.ForgetInstalled(basePath(), installed); err != nil {
		fmt.Fprintln(os.Stderr, "0c-update: 更新待辦未清除：", err)
	}
}

// setScheduleRemoved 記錄使用者是否自己關閉排程；關閉時 schedule repair 不會重建。重新建立時一併清除修復紀錄。
func setScheduleRemoved(base string, removed bool) error {
	_, err := autoupdate.EditState(base, func(s *autoupdate.State) error {
		s.ScheduleRemoved = removed
		if !removed {
			s.UpdaterBroken, s.UpdaterRepairs = "", nil
		}
		return nil
	})
	return err
}

func editState(base string, change func(*autoupdate.State)) error {
	s, err := autoupdate.EditState(base, func(s *autoupdate.State) error { change(s); return nil })
	if err != nil {
		return err
	}
	printState(s)
	return nil
}

func printState(s *autoupdate.State) {
	fmt.Printf("自動更新：%s", s.Mode)
	if time.Now().Before(s.PausedUntil) {
		fmt.Printf("（暫停至 %s）", s.PausedUntil.Local().Format("2006-01-02 15:04"))
	}
	fmt.Printf("\n階段：%s\n", s.Stage)
	if s.Target != nil {
		fmt.Printf("目標版本：%s（%s）\n", s.Target.Version, s.Target.Kind)
	}
	if s.Snooze != nil && time.Now().Before(s.Snooze.Until) {
		fmt.Printf("更新提醒稍後至：%s\n", s.Snooze.Until.Local().Format("2006-01-02 15:04"))
	}
	if s.Blocked != nil {
		fmt.Printf("待處理：%s（自 %s）%s\n", s.Blocked.Reason, s.Blocked.Since.Local().Format("2006-01-02 15:04"), s.Blocked.Detail)
		switch s.Blocked.Reason {
		case "needs_consent":
			fmt.Println("執行 0c-update finish 完成安裝。")
		case "needs_relogin":
			fmt.Println("登出再登入後生效。")
		}
	}
	if !s.LastSuccess.IsZero() {
		fmt.Printf("上次成功查詢：%s\n", s.LastSuccess.Local().Format("2006-01-02 15:04"))
	}
	if s.LastError != "" {
		fmt.Printf("上次查詢失敗：%s\n", s.LastError)
	}
}

// statusJSON 是輸入法選單讀取的狀態：版本、模式、階段、待辦與設定。不含個人資料。
func statusJSON(installed string, diagnostics bool) error {
	out := statusMap(basePath(), installed, dialEngine)
	// 平台診斷要開 ps、lsof 等子程序：只在匯出狀態摘要時收集，輸入法選單每 15 秒的輪詢不帶。
	if diagnostics {
		if d := shellDiagnostics(); d != nil {
			out["shell"] = d
		}
	}
	return json.NewEncoder(os.Stdout).Encode(out)
}

func statusMap(base, installed string, dial autoupdate.Dialer) map[string]any {
	out := map[string]any{"schema": contractSchema, "installed": installed, "engine": autoupdate.PointerVersion(base)}
	out["runtime"] = modelStatus(dial)
	// #181：怎麼更新（self／package-manager），以及這台電腦上找得到的安裝（Linux；外殼用的那一份 active）。
	managed := packageManaged()
	out["updates"] = updatesMode()
	if list := installList(); len(list) > 0 {
		out["installs"] = list
	}
	if managed {
		// 系統套件的引擎不跟官網安裝的核心更新指標（外殼以 -data 指定系統字典）；更新狀態也是官網安裝的。
		out["engine"] = ""
	}
	if s, err := autoupdate.Load(base); err == nil && !managed {
		appendUpdateStateStatus(out, s, time.Now())
		if p := updaterProblem(base, s, platformRepair); p != "" {
			out["updater"] = p
		}
		current := installed
		if pointer := autoupdate.PointerVersion(base); pointer != "" {
			current = pointer // 核心更新後實際運作的版本以引擎指標為準
		}
		view := autoupdate.ViewOf(s, current)
		view.Operation = autoupdate.ReadOperation(base, s.Target)
		out["update"] = view
	}
	// 設定檔的狀態另外回報：損毀或來自較新版本時，介面要能說明，而不是什麼都不顯示。
	r, err := settings.Read(settings.Path(filepath.Join(base, "user.json")))
	out["settings_state"] = r.State
	if r.ReadOnly {
		out["settings_read_only"] = true
	}
	f := featuresOf()
	f.EngineService = true // 0.2.0 起一律常駐（#120）
	f.EngineServiceState, f.EngineServiceUnit = engineServiceState()
	out["features"] = f
	if err == nil {
		st := r.Settings
		fuzzyModes := map[string]string{}
		for _, p := range fuzzy.Pairs() {
			fuzzyModes[p.ID()] = st.FuzzyConfig().Mode(p).String()
		}
		out["settings"] = map[string]any{"selection": st.Selection, "punctuation": st.Punctuation, "document_context": st.DocumentContext, "mixed_input": st.MixedInput, "english_autocomplete": st.EnglishAutocomplete, "left_shift": st.LeftShift, "esc_clear": st.EscClear, "space_selects": st.SpaceSelects, "advance_after_select": st.AdvanceAfterSelect, "output_simplified": st.OutputSimplified, "simplified_hotkey": st.SimplifiedHotkey, "ctrl_punctuation": st.CtrlPunctuation, "caps_lock_english": st.CapsLockEnglish, "toneless": st.Toneless, "keyboard_layout": st.KeyboardLayout, "fuzzy": fuzzyModes}
	}
	return out
}

// Shared by the real status endpoint and the cross-language menu regression.
// The asset hash is essential: the presenter cannot create a bounded action
// without it, even when version/action are present.
func appendUpdateStateStatus(out map[string]any, s *autoupdate.State, now time.Time) {
	out["mode"], out["stage"] = s.Mode, s.Stage
	out["last_error"] = s.LastError
	if now.Before(s.PausedUntil) {
		out["paused_until"] = s.PausedUntil
	}
	if s.Target != nil {
		out["target"] = map[string]string{"version": s.Target.Version, "kind": s.Target.Kind, "sha256": s.Target.SHA256}
		if s.Snooze != nil && s.Target.Version == s.Snooze.Version && s.Target.SHA256 == s.Snooze.SHA256 && now.Before(s.Snooze.Until) {
			out["notice_snoozed_until"] = s.Snooze.Until
		}
	}
	if s.Blocked != nil {
		out["blocked"] = s.Blocked
		out["action"] = actionFor(s)
	}
	if !s.LastSuccess.IsZero() {
		out["last_success"] = s.LastSuccess
	}
}

// actionFor 回傳選單上「立即完成」要執行的動作；沒有可由使用者完成的待辦時為空字串。
func actionFor(s *autoupdate.State) string {
	switch {
	case s.Target == nil:
		return ""
	case reloadableShell() && s.Target.Kind == "shell" && s.Stage == autoupdate.StagePending:
		return "finish"
	case s.Blocked == nil:
		return ""
	case s.Blocked.Reason == "needs_consent", s.Stage == autoupdate.StagePrepared, s.Blocked.Reason == "engine_busy":
		return "finish"
	}
	return ""
}
