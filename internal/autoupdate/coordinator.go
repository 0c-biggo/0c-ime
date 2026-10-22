package autoupdate

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/0c-biggo/0c-ime/internal/e2esync"
	"github.com/0c-biggo/0c-ime/internal/filelock"
	"github.com/0c-biggo/0c-ime/internal/update"
)

// ErrNeedsConsent：平台需要使用者前景確認（例如 Windows 外殼更新的系統管理員權限）。
var ErrNeedsConsent = errors.New("needs user consent")

// ErrAtLogin：此平台的外殼只能在下次登入、桌面啟動前安裝。
var ErrAtLogin = errors.New("shell update deferred to next login")

// ErrShellBusy：外殼正在組字或使用中，保留已準備套件，稍後再試。
var ErrShellBusy = errors.New("等待完成組字或閒置")

// ErrShellHealth：完整外殼未通過運作檢查，平台已回復原版。
var ErrShellHealth = errors.New("更新檢查失敗，已回復原版")

// ErrOldShellLoaded：跨過登入邊界後仍有舊版外殼在執行（macOS：系統開到 backups 裡的舊 App，#98）。
var ErrOldShellLoaded = errors.New("仍在執行舊版外殼")

// OldShellDetail 是 ErrOldShellLoaded 的受阻說明；設定程式以這段文字辨識，改動時要一起改 app.js。
const OldShellDetail = "已安裝，但系統仍開著更新前留下的舊版輸入法；登出再登入後改用新版"

// ErrShellRetiring：新版外殼已裝好、不必重新登入，舊版的輸入法程式還在執行（macOS，#170）。背景安裝的交接只在
// 使用者離開電腦（螢幕鎖定或關閉）時做，以免已開著的 App 漏字（#178）。
var ErrShellRetiring = errors.New("舊版輸入法程式等使用者離開時結束")

// ShellRetiringDetail 是 ErrShellRetiring 的受阻說明；設定程式以這段文字辨識，改動時要一起改 app.js 與 engine-state.js
// （舊的說明文字也要留著認得：狀態檔可能還是 0.2.1／0.2.2 寫的）。
const ShellRetiringDetail = "已安裝，離開電腦（螢幕鎖定或關閉）時自動換成新版，不必登出"

// A notification is consent for one verified asset, not for a later release.
var ErrTargetChanged = errors.New("待辦版本已變更，請從更新選單確認目前版本")

// LiveShellPlatform 支援在登入中的桌面安全重載外殼。
type LiveShellPlatform interface{ ReloadsShell() bool }

// ShellActivationChecker 核對需要重新登入的平台是否已跨過外殼安裝邊界。
// 新引擎開始服務不代表同一工作階段的舊 DLL 已卸載。
type ShellActivationChecker interface {
	ShellReady(context.Context, string) (bool, error)
}

// ShellLoadProver 表示平台的 ShellReady 已核對沒有舊外殼在執行（macOS：輸入法程序都對應新版執行檔），
// 不只是跨過登入邊界（Windows 只比登入時間）。只有這種平台，引擎閒置結束時也可以算生效。
type ShellLoadProver interface{ ShellLoadProven() bool }

// PendingShellEngineHandover：外殼要重新登入才全面生效，引擎卻可以先換（Windows：舊 DLL 留在執行中的
// 應用程式裡，但它們和核心更新一樣能連新引擎）。0.2.0 起引擎常駐不閒置結束（#120），不換的話舊引擎
// 會跑到登出，介面已是新版、引擎卻不支援新功能。macOS（#77／#98 自己的路徑）與 Linux（登入中重載外殼）不實作。
type PendingShellEngineHandover interface{ HandsOverEngineBeforeRelogin() bool }

// ShellRecoverer 在模式／閒置判斷之前收尾已開始的外殼交易。
type ShellRecoverer interface {
	RecoverShell(context.Context) (bool, error)
}

func (c *Coordinator) liveShell() bool {
	p, ok := c.Platform.(LiveShellPlatform)
	return ok && p.ReloadsShell()
}

// Conditions 是切換前觀察到的使用狀態；Known 為 false 表示平台無法判斷。
type Conditions struct {
	Idle         time.Duration
	IdleKnown    bool
	Battery      int // 0–100
	Charging     bool
	BatteryKnown bool
	Metered      bool // legacy observation; download policy ignores network cost
}

// Platform 由各作業系統實作；測試以假平台注入時間、網路、閒置與故障。
type Platform interface {
	Installed() (string, error)                                               // 已安裝（收據）的版本
	Running(ctx context.Context) (version string, running bool, err error)    // 以 health 核對運作中的引擎
	Fetch(ctx context.Context, lastSerial uint64) (*update.SignedFeed, error) // 已驗證的簽章索引
	Download(ctx context.Context, t Target, dir string) (string, error)       // 回傳已核對雜湊的檔案
	Prepare(ctx context.Context, archive string, t Target) (string, error)    // 準備版本目錄並隔離檢查，回傳相對目錄
	SwitchCore(dir, version string) error                                     // 改引擎指標並記下原指標；已指向 dir 時不改動（可重跑）
	RevertCore(dir string) error                                              // 撤銷切換到 dir：指標仍是 dir 時改回原指標，否則不做事（可重跑）
	Drain(ctx context.Context) error                                          // 請運作中的引擎在沒有組字時交接；沒有引擎時為 nil
	StartEngine(ctx context.Context) error
	InstallShell(ctx context.Context, archive string) (pending bool, err error) // 外殼更新；可能回 ErrNeedsConsent
	Conditions() Conditions
	Notify(n Notice) error
	Target() (goos, arch string)
}

// ShellIdleGate is used by platforms that replace a live installation even when
// the loaded shell itself is not reloaded until login (macOS).
type ShellIdleGate interface {
	ShellNeedsIdle() bool
}

type Coordinator struct {
	Base     string
	Platform Platform
	Now      func() time.Time
	Jitter   func(max time.Duration) time.Duration

	CheckEvery     time.Duration // 6 小時
	CheckJitter    time.Duration // 30 分鐘
	IdleNeeded     time.Duration // 5 分鐘
	DownloadBatt   int           // 30%
	SwitchBatt     int           // 40%
	RemindAfter    time.Duration // 已準備但未生效多久後提醒（24 小時）
	Force          bool          // 使用者「現在檢查」
	Consent        bool          // 使用者親自確認安裝（finish）：詢問模式也套用
	ConsentVersion string        // 通知／選單綁定的版本；空值為明確的 CLI finish
	ConsentSHA256  string
}

func (c *Coordinator) defaults() {
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Jitter == nil {
		c.Jitter = func(max time.Duration) time.Duration { return randomDuration(max) }
	}
	if c.CheckEvery == 0 {
		c.CheckEvery = 6 * time.Hour
	}
	if c.CheckJitter == 0 {
		c.CheckJitter = 30 * time.Minute
	}
	if c.IdleNeeded == 0 {
		c.IdleNeeded = 5 * time.Minute
	}
	if c.DownloadBatt == 0 {
		c.DownloadBatt = 30
	}
	if c.SwitchBatt == 0 {
		c.SwitchBatt = 40
	}
	if c.RemindAfter == 0 {
		c.RemindAfter = 24 * time.Hour
	}
}

// Run 做一次協調：鎖定、讀狀態、推進一步、保存。多個喚起同時發生時只有一個會執行。
func (c *Coordinator) Run(ctx context.Context) (*State, error) {
	c.defaults()
	unlock, err := lock(c.Base)
	if err != nil {
		return nil, err
	}
	defer unlock()
	defer ClearOperation(c.Base)
	s, err := Load(c.Base)
	if err != nil {
		return nil, err
	}
	if !c.consentMatches(s) {
		return s, ErrTargetChanged
	}
	if p, ok := c.Platform.(ShellRecoverer); ok {
		recovered, err := p.RecoverShell(ctx)
		if err != nil {
			reason := "install_failed"
			if errors.Is(err, ErrShellBusy) {
				reason = "engine_busy"
			}
			if errors.Is(err, ErrShellHealth) && s.Target != nil {
				reason = "health_failed"
				s.Stage = StageFailed
				if !s.failed(s.Target.Version) {
					s.Failed = append(s.Failed, s.Target.Version)
				}
				s.record(c.Now(), "failed", s.Target.Version, err.Error())
			}
			s.block(c.Now(), reason, err.Error())
			return s, c.save(s)
		}
		if recovered && s.Target != nil && s.Target.Kind == "shell" {
			c.activate(s, c.Now())
			return s, c.save(s)
		}
	}
	err = c.step(ctx, s)
	return s, errors.Join(err, c.save(s))
}

// ErrUpdateInProgress：更新協調器忙碌或切換／回復進行中，等協調器收尾後再檢查。
var ErrUpdateInProgress = errors.New("更新作業進行中，請稍後再檢查")

// Check 只查詢已簽章索引並記錄結果（時間、失敗次數、最新版本與對這台電腦的資格），不下載、
// 不準備、不切換；等待重新登入的外殼先在本機核對一次（#98：重新登入後按「檢查更新」不能仍顯示待生效）。
// 供使用者手動「檢查更新」使用；與 Run 共用協調器鎖，最多等 2 秒，不會和背景更新同時改狀態。持續忙碌時回 ErrUpdateInProgress，與設定寫入衝突區分。
// 查詢失敗不是錯誤：結果記在狀態裡（CheckFailures、LastError），呼叫端據此顯示。
func (c *Coordinator) Check(ctx context.Context) (*State, error) {
	c.defaults()
	unlock, err := waitLock(ctx, c.Base, "coordinator.lock")
	if err != nil {
		if errors.Is(err, ErrLocked) {
			return nil, ErrUpdateInProgress
		}
		return nil, err
	}
	defer unlock()
	defer ClearOperation(c.Base)
	s, err := Load(c.Base)
	if err != nil {
		return nil, err
	}
	if s.Switched || s.RestartPending {
		return s, ErrUpdateInProgress
	}
	c.confirmPendingShell(ctx, s, c.Now())
	c.discover(ctx, s, c.Now())
	return s, c.save(s)
}

func (c *Coordinator) consentMatches(s *State) bool {
	if c.ConsentVersion == "" && c.ConsentSHA256 == "" {
		return true
	}
	return c.Consent && c.ConsentVersion != "" && c.ConsentSHA256 != "" &&
		s.Target != nil && s.Target.Version == c.ConsentVersion && s.Target.SHA256 == c.ConsentSHA256 && !s.failed(s.Target.Version)
}

// save 寫回狀態；執行期間使用者可能改了模式或暫停（選單、命令列），這些使用者設定以磁碟上的最新值為準。
func (c *Coordinator) save(s *State) error {
	_, err := EditState(c.Base, func(latest *State) error {
		if latest.InstallID == s.InstallID {
			s.Mode, s.PausedUntil, s.Feed, s.Test = latest.Mode, latest.PausedUntil, latest.Feed, latest.Test
			// Schedule commands and repair metadata are owned by the external
			// scheduler, not by this coordinator's download snapshot.
			s.ScheduleRemoved = latest.ScheduleRemoved
			s.UpdaterRepairs, s.UpdaterBroken, s.UpdaterNotified = latest.UpdaterRepairs, latest.UpdaterBroken, latest.UpdaterNotified
			// 登入時補做輸入法設定由安裝器（可能就在這次 InstallShell 裡）記下、由排程清除，協調器不改它（#195）。
			s.ProfileAtLogin = latest.ProfileAtLogin
		}
		*latest = *s
		return nil
	})
	return err
}

func (c *Coordinator) step(ctx context.Context, s *State) error {
	now := c.Now()
	// 前一次在回復或重啟途中被中止：不論模式與暫停都先收尾，不能讓未通過檢查的版本留著。
	c.recover(ctx, s, now)
	// 先完成舊外殼確認，再發現較新的核心，讓歷史保留真正的生效紀錄。確認只核對本機、不查詢也不安裝，
	// 手動模式與暫停中也照做：已經裝好的外殼重新登入後就該顯示生效（#98）。
	activatedShell := c.confirmPendingShell(ctx, s, now)
	if !activatedShell {
		// 外殼還在等重新登入：引擎先換成新版（Windows），手動模式與暫停中也做——這是完成已裝好的更新，不是新安裝。
		c.handOverPendingShellEngine(ctx, s, now)
	}
	if !c.Consent && !c.Force && (s.Mode == ModeManual || now.Before(s.PausedUntil)) {
		return nil
	}
	// 時鐘往回跳（例如主機板時間被重設）時，下次查詢時間會落在比一個週期還遠的未來；立即查詢，不等時間追上。
	clockBack := s.NextCheck.Sub(now) > c.CheckEvery+c.CheckJitter || now.Before(s.LastCheck)
	boundConsent := c.ConsentVersion != "" || c.ConsentSHA256 != ""
	// A delayed notification must recheck the signed feed even before the
	// scheduled poll is due. Otherwise a withdrawn cached asset could install.
	if c.Force || boundConsent || s.LastCheck.IsZero() || !now.Before(s.NextCheck) || clockBack {
		c.discover(ctx, s, now)
	}
	if boundConsent && s.LastError != "" {
		return fmt.Errorf("無法重新確認通知版本：%s", s.LastError)
	}
	if !c.consentMatches(s) {
		return ErrTargetChanged
	}
	if s.Target == nil {
		if !activatedShell {
			s.Stage = StageIdle
		}
		s.unblock()
		s.Snooze = nil
		return nil
	}
	// 同一次喚起中能推進多步就推進（下載完直接準備、準備好直接套用）；任一步受阻即停在該階段。
	if s.Stage == StageFound {
		c.download(ctx, s, now)
	}
	if s.Stage == StageDownloaded {
		c.prepare(ctx, s, now)
	}
	switch s.Stage {
	case StagePrepared:
		// 已經開始的切換（當時是自動模式或使用者已確認）要收尾，不能停在指標已改、未經檢查的狀態。
		if !c.Consent && !s.Switched && (s.Mode == ModeAsk || s.Mode == ModeManual || now.Before(s.PausedUntil)) {
			s.block(now, "needs_consent", "已下載，等待使用者安裝")
		} else {
			c.apply(ctx, s, now)
		}
	case StagePending:
		c.confirm(ctx, s, now)
	}
	c.remind(s, now)
	return nil
}

func (c *Coordinator) discover(ctx context.Context, s *State, now time.Time) {
	c.phase(s, "checking")
	s.LastCheck = now
	feed, err := c.Platform.Fetch(ctx, s.FeedSerial)
	if err != nil {
		s.CheckFailures++
		s.LastError = err.Error()
		// 退避：15 分鐘起倍增，最多一般週期；到期補查由排程的登入／恢復連線觸發。
		backoff := 15 * time.Minute << min(s.CheckFailures-1, 4)
		s.NextCheck = now.Add(min(backoff, c.CheckEvery))
		s.record(now, "check_failed", "", err.Error())
		return
	}
	s.CheckFailures, s.LastError = 0, ""
	s.LastSuccess = now
	s.FeedSerial = feed.Serial
	s.NextCheck = now.Add(c.CheckEvery + c.Jitter(c.CheckJitter))
	goos, arch := c.Platform.Target()
	installed, err := c.Platform.Installed()
	if err != nil {
		s.block(now, "install_unknown", err.Error())
		return
	}
	if s.Target != nil && feed.IsWithdrawn(s.Target.Version) && s.Stage != StageActive {
		s.record(now, "withdrawn", s.Target.Version, "")
		// Retain a switched transaction until rollback succeeds. Otherwise a
		// failed revert could forget the unverified pointer and its original route.
		if s.Switched && !s.failed(s.Target.Version) {
			s.Failed = append(s.Failed, s.Target.Version)
			s.Stage = StageFailed
		}
		if err := c.drop(s); err != nil {
			s.block(now, "revert_failed", err.Error())
			return
		}
	}
	asset, err := feed.Select(goos, arch)
	isNewer, withdrawn, failedBefore := newer(feed.Version, installed), feed.IsWithdrawn(feed.Version), s.failed(feed.Version)
	inBatch := inRollout(s.InstallID, feed.Version, feed.Rollout)
	eligible := err == nil && !withdrawn && !failedBefore && isNewer && inBatch
	s.Latest = &Latest{Version: feed.Version, Eligible: eligible}
	switch {
	case eligible || !isNewer:
	case withdrawn:
		s.Latest.Reason = ReasonWithdrawn
	case err != nil:
		s.Latest.Reason = ReasonNoAsset
	case failedBefore:
		s.Latest.Reason = ReasonFailed
	case !inBatch:
		s.Latest.Reason = ReasonRollout
	}
	if !eligible {
		// 切換進行中時，已安裝版本（引擎指標）就是目標本身；由確認或回復收尾，不在這裡丟棄。
		// 外殼收據也可能先寫入新版，但舊工作階段仍在執行；保留 pending，直到 confirm 核對生效。
		pendingShell := s.Target != nil && s.Target.Kind == "shell" && s.Stage == StagePending && s.Target.Version == installed
		if s.Target != nil && !s.Switched && !pendingShell && !newer(s.Target.Version, installed) {
			if err := c.drop(s); err != nil {
				s.block(now, "revert_failed", err.Error())
			}
		}
		return
	}
	if s.Target != nil && s.Target.Version == feed.Version && s.Target.SHA256 == asset.SHA256 {
		return
	}
	if s.Target != nil && s.Target.Kind == "shell" && s.Stage == StagePending && asset.Kind == "core" {
		// 外殼尚未完成重登時，核心更新不能抹掉這項待辦；較新的完整外殼仍可取代它。
		if p, ok := c.Platform.(ShellActivationChecker); ok {
			_, err := p.ShellReady(ctx, s.Target.Version)
			c.awaitShell(s, now, err)
			return
		}
	}
	// 新版本（或同版本不同資產）取代舊目標；受阻起算時間保留。
	if s.Target != nil {
		s.record(now, "superseded", s.Target.Version, feed.Version)
		// 舊目標可能已切換但未確認（中途被中止）：先撤銷，新目標的原指標才會是確認過的版本。
		if s.Switched {
			if err := c.Platform.RevertCore(s.Prepared); err != nil {
				s.block(now, "revert_failed", err.Error())
				return
			}
			s.Switched, s.RestartPending = false, true
		}
	}
	s.Target = &Target{Version: feed.Version, Kind: asset.Kind, URL: asset.URL, SHA256: asset.SHA256, Size: asset.Size, FoundAt: now}
	s.Stage, s.Download, s.Prepared = StageFound, "", ""
	s.clear("needs_relogin")
	s.record(now, "found", feed.Version, asset.Kind)
}

func (c *Coordinator) drop(s *State) error {
	if s.Switched {
		if err := c.Platform.RevertCore(s.Prepared); err != nil {
			return err
		}
		// 撤回的版本已切換（或正要切換）但未確認：回到原版；它的引擎可能已被按鍵啟動，交接後改啟動原版。
		s.Switched, s.RestartPending = false, true
	}
	s.Target, s.Download, s.Prepared = nil, "", ""
	s.Snooze = nil
	s.Stage = StageIdle
	s.unblock()
	return nil
}

func (c *Coordinator) download(ctx context.Context, s *State, now time.Time) {
	cond := c.Platform.Conditions()
	if cond.BatteryKnown && !cond.Charging && cond.Battery < c.DownloadBatt {
		s.block(now, "low_battery", fmt.Sprintf("電量 %d%%", cond.Battery))
		return
	}
	// Publish the target before a slow download so every settings window sees it.
	if err := c.save(s); err != nil {
		s.block(now, "download_failed", err.Error())
		return
	}
	c.phase(s, "downloading")
	path, err := c.Platform.Download(ctx, *s.Target, filepath.Join(c.Base, "update", "downloads"))
	if err != nil {
		s.block(now, resourceReason(err, "download_failed"), err.Error())
		return
	}
	s.clear("metered_network", "low_battery", "download_failed", "disk_full")
	s.Download, s.Stage = path, StageDownloaded
	s.record(now, "downloaded", s.Target.Version, "")
}

func (c *Coordinator) prepare(ctx context.Context, s *State, now time.Time) {
	c.phase(s, "preparing")
	if s.Target.Kind == "shell" {
		s.Stage, s.PreparedAt = StagePrepared, now // 外殼更新由平台安裝程式在套用時驗證
		return
	}
	dir, err := c.Platform.Prepare(ctx, s.Download, *s.Target)
	if errors.Is(err, ErrNeedsShell) {
		// 外殼不認得引擎指標：這一版改以完整安裝套用。
		s.Target.Kind = "shell"
		s.record(now, "needs_shell", s.Target.Version, "")
		s.Stage, s.PreparedAt = StagePrepared, now
		return
	}
	if err != nil {
		s.block(now, resourceReason(err, "prepare_failed"), err.Error())
		return
	}
	s.clear("prepare_failed", "disk_full")
	s.Prepared, s.PreparedAt, s.Stage = dir, now, StagePrepared
	s.record(now, "prepared", s.Target.Version, dir)
}

func resourceReason(err error, fallback string) string {
	if errors.Is(err, update.ErrInsufficientSpace) || errors.Is(err, syscall.ENOSPC) {
		return "disk_full"
	}
	return fallback
}

// safe 判斷能否在此時切換：使用者閒置且電量足夠；閒置無法判斷時只在沒有引擎執行時切換。
func (c *Coordinator) safe(ctx context.Context, s *State, now time.Time) bool {
	cond := c.Platform.Conditions()
	if cond.BatteryKnown && !cond.Charging && cond.Battery < c.SwitchBatt {
		s.block(now, "low_battery", fmt.Sprintf("電量 %d%%", cond.Battery))
		return false
	}
	if c.Consent {
		return true // 使用者親自要求現在完成；是否正在組字仍由 drain 判斷
	}
	if cond.IdleKnown {
		if cond.Idle < c.IdleNeeded {
			s.block(now, "waiting_idle", "使用中，等待閒置")
			return false
		}
		return true
	}
	if _, running, err := c.Platform.Running(ctx); err == nil && !running {
		return true
	}
	s.block(now, "waiting_idle", "無法判斷閒置，等待引擎結束或下次登入")
	return false
}

func (c *Coordinator) apply(ctx context.Context, s *State, now time.Time) {
	if s.Target.Kind == "shell" {
		needsIdle := c.liveShell()
		if gate, ok := c.Platform.(ShellIdleGate); ok {
			needsIdle = needsIdle || gate.ShellNeedsIdle()
		}
		if needsIdle && !c.safe(ctx, s, now) {
			return
		}
		// 平台安裝器需要已保存的目標；中止後也能以同一套件接續。
		if err := c.save(s); err != nil {
			s.block(now, "install_failed", err.Error())
			return
		}
		c.phase(s, "installing")
		pending, err := c.Platform.InstallShell(ctx, s.Download)
		switch {
		case errors.Is(err, ErrShellHealth):
			if !s.failed(s.Target.Version) {
				s.Failed = append(s.Failed, s.Target.Version)
			}
			s.Stage = StageFailed
			s.block(now, "health_failed", err.Error())
			s.record(now, "failed", s.Target.Version, err.Error())
		case errors.Is(err, ErrShellBusy):
			s.block(now, "engine_busy", err.Error())
		case errors.Is(err, ErrNeedsConsent):
			s.block(now, "needs_consent", "需要確認安裝")
		case errors.Is(err, ErrAtLogin):
			s.Stage = StagePending
			s.block(now, "needs_relogin", "登出再登入時自動完成安裝")
			s.record(now, "staged_for_login", s.Target.Version, "")
		case err != nil:
			s.block(now, resourceReason(err, "install_failed"), err.Error())
		case pending:
			s.Stage = StagePending
			s.block(now, "needs_relogin", "已安裝，重新登入後生效")
			s.record(now, "installed", s.Target.Version, "pending")
		default:
			s.Stage = StagePending
			if c.liveShell() {
				c.activate(s, now)
			} else {
				c.confirm(ctx, s, now)
			}
		}
		return
	}
	if !c.safe(ctx, s, now) {
		return
	}
	c.phase(s, "installing")
	if err := e2esync.Pause(c.Base, "core_safe"); err != nil {
		s.block(now, "e2e_sync_failed", err.Error())
		return
	}
	// 先存下「正在切換」：切換後、存檔前被中止時，下一次喚起才知道指標可能已改，
	// 不會把指標所指的未確認版本當成已安裝（原指標由平台記在指標檔內）。
	if !s.Switched {
		s.Switched = true
		if err := c.save(s); err != nil {
			s.Switched = false
			s.block(now, "switch_failed", "無法保存狀態："+err.Error())
			return
		}
	}
	if err := c.Platform.SwitchCore(s.Prepared, s.Target.Version); err != nil {
		if c.Platform.RevertCore(s.Prepared) == nil {
			s.Switched = false
		}
		s.block(now, "switch_failed", err.Error())
		return
	}
	s.record(now, "switched", s.Target.Version, s.Prepared)
	if err := c.Platform.Drain(ctx); err != nil {
		// 仍有組字：指標已指向新版，下一次引擎啟動（閒置結束或重新登入）即生效。
		s.Stage = StagePending
		s.block(now, "engine_busy", err.Error())
		return
	}
	c.verifyCore(ctx, s, now)
}

// verifyCore 啟動指標所指的引擎並以 health 核對版本；不符即回復原指標並暫停該版。
func (c *Coordinator) verifyCore(ctx context.Context, s *State, now time.Time) {
	if err := c.Platform.StartEngine(ctx); err != nil {
		c.fail(ctx, s, now, "新版引擎無法啟動："+err.Error())
		return
	}
	version, running, err := c.Platform.Running(ctx)
	if err != nil || !running || version != s.Target.Version {
		c.fail(ctx, s, now, fmt.Sprintf("運作版本 %q，預期 %s：%v", version, s.Target.Version, err))
		return
	}
	c.activate(s, now)
}

func (c *Coordinator) confirm(ctx context.Context, s *State, now time.Time) {
	// 舊 Linux 更新器留下 pending／needs_relogin 時，由新流程接手完整安裝。
	// 已安裝也要由平台核對真正載入的外殼，不能只看引擎版本。
	if s.Target.Kind == "shell" && c.liveShell() {
		c.apply(ctx, s, now)
		return
	}
	shellConfirmed := false
	if s.Target.Kind == "shell" {
		if p, ok := c.Platform.(ShellActivationChecker); ok {
			ready, err := p.ShellReady(ctx, s.Target.Version)
			if !ready || err != nil {
				c.awaitShell(s, now, err)
				return
			}
			prover, ok := c.Platform.(ShellLoadProver)
			shellConfirmed = ok && prover.ShellLoadProven()
		}
	}
	version, running, err := c.Platform.Running(ctx)
	if err == nil && running && version == s.Target.Version {
		c.activate(s, now)
		return
	}
	// 平台已確認沒有舊外殼（macOS：新的登入工作階段，執行中的輸入法程序都對應新版執行檔，或還沒有 App
	// 載入輸入法，#98）。引擎閒置結束不代表沒生效：新外殼下一次按鍵就從同一個新 App 啟動引擎。
	// 還有舊引擎在跑時才等它交接（#77）。
	if shellConfirmed && err == nil && !running {
		c.activate(s, now)
		return
	}
	if s.Target.Kind == "core" && err == nil {
		if running {
			// 仍是切換前的引擎（先前仍在組字）：安全時機再請它交接。
			if !c.safe(ctx, s, now) {
				return
			}
			if derr := c.Platform.Drain(ctx); derr != nil {
				s.block(now, "engine_busy", derr.Error())
				return
			}
		}
		// 指標已指向新版：啟動並核對，不以「沒有引擎在執行」當作生效。
		c.verifyCore(ctx, s, now)
		return
	}
	if s.Blocked == nil {
		s.block(now, "needs_relogin", "重新登入後生效")
	}
}

// confirmPendingShell 核對等待重新登入的外殼是否已生效，回傳是否因此生效。只用於需要重新登入的平台；
// 可在登入中重載外殼的平台（Linux）由 confirm 接著安裝，不在這裡做。
func (c *Coordinator) confirmPendingShell(ctx context.Context, s *State, now time.Time) bool {
	if s.Target == nil || s.Target.Kind != "shell" || s.Stage != StagePending || c.liveShell() {
		return false
	}
	if _, ok := c.Platform.(ShellActivationChecker); !ok {
		return false
	}
	c.confirm(ctx, s, now)
	return s.Target == nil && s.Stage == StageActive
}

// handOverPendingShellEngine：外殼已安裝、等重新登入（StagePending），執行中的引擎還不是目標版本時，
// 比照核心更新在安全時機請它交接（Drain），再啟動指標所指的新版並以 health 核對。典型情境是 dev.64 的
// 舊穩定更新器背景裝好 0.2.0：安裝由舊程式執行、不會交接，新的穩定更新器下一次喚起時在這裡補上。
// 外殼本身仍要重新登入才生效：狀態維持 needs_relogin、不 activate，引擎版本對了也不算外殼生效。
// 仍在組字（Drain 失敗）或不是安全時機就照舊等登出，下一次喚起再試；新引擎版本不符只記錄，不回復外殼。
func (c *Coordinator) handOverPendingShellEngine(ctx context.Context, s *State, now time.Time) {
	if s.Target == nil || s.Target.Kind != "shell" || s.Stage != StagePending || c.liveShell() {
		return
	}
	if p, ok := c.Platform.(PendingShellEngineHandover); !ok || !p.HandsOverEngineBeforeRelogin() {
		return
	}
	version, running, err := c.Platform.Running(ctx)
	if err != nil || !running || version == s.Target.Version {
		return
	}
	var relogin *Blocked
	if s.Blocked != nil && s.Blocked.Reason == "needs_relogin" {
		b := *s.Blocked
		relogin = &b
	}
	defer func() { // safe 會改成 waiting_idle 等原因；外殼仍是等重新登入
		if relogin != nil {
			s.Blocked = relogin
		} else {
			c.awaitShell(s, now, nil)
		}
	}()
	if !c.safe(ctx, s, now) {
		return
	}
	if err := c.Platform.Drain(ctx); err != nil {
		recordOnce(s, now, "engine_handover_waiting", s.Target.Version, err.Error())
		return
	}
	if err := c.Platform.StartEngine(ctx); err != nil {
		recordOnce(s, now, "engine_handover_failed", s.Target.Version, err.Error())
		return
	}
	got, running, err := c.Platform.Running(ctx)
	if err != nil || !running || got != s.Target.Version {
		recordOnce(s, now, "engine_handover_failed", s.Target.Version, fmt.Sprintf("運作版本 %q：%v", got, err))
		return
	}
	s.record(now, "engine_handed_over", got, "外殼重新登入後生效")
}

// recordOnce：每次喚起都可能重試的交接，同一版本、同一種結果連續發生時只記一次，不把歷程裡的安裝與生效紀錄擠掉。
func recordOnce(s *State, now time.Time, kind, version, detail string) {
	if n := len(s.History); n > 0 && s.History[n-1].Kind == kind && s.History[n-1].Version == version {
		return
	}
	s.record(now, kind, version, detail)
}

func (c *Coordinator) awaitShell(s *State, now time.Time, err error) {
	if errors.Is(err, ErrShellRetiring) {
		// 不必登出：使用者離開電腦時的排程喚起會交接，之後 confirm 就會生效。不算要使用者處理的事，不發通知。
		s.block(now, "waiting_idle", ShellRetiringDetail)
		return
	}
	detail := "重新登入後生效"
	switch {
	case errors.Is(err, ErrOldShellLoaded):
		detail = OldShellDetail
	case err != nil:
		detail += "；無法確認登入工作階段：" + err.Error()
	}
	s.block(now, "needs_relogin", detail)
}

// Activator 是平台可選的收尾：生效後更新穩定的 0c-update 副本、清除舊版本目錄與下載。
type Activator interface {
	Activated(t Target, dir string)
}

func (c *Coordinator) activate(s *State, now time.Time) {
	if a, ok := c.Platform.(Activator); ok {
		a.Activated(*s.Target, s.Prepared)
	}
	s.record(now, "active", s.Target.Version, "")
	s.Target, s.Download, s.Prepared, s.Previous, s.Switched = nil, "", "", "", false
	s.Stage = StageActive
	s.unblock()
	s.NoticeFor, s.NoticeCount = "", 0
	s.Snooze = nil
}

// fail 記下失敗並回復原指標。先存檔再動作：回復途中被中止時，下一次喚起由 recover 接著完成，
// 不會再切到這個版本。
func (c *Coordinator) fail(ctx context.Context, s *State, now time.Time, detail string) {
	if !s.failed(s.Target.Version) {
		s.Failed = append(s.Failed, s.Target.Version)
	}
	s.record(now, "failed", s.Target.Version, detail)
	s.Stage = StageFailed
	s.block(now, "health_failed", detail)
	if err := c.save(s); err != nil {
		s.record(now, "save_failed", s.Target.Version, err.Error())
	}
	c.recover(ctx, s, now)
}

// recover 收尾失敗的切換：撤銷指標，請未通過檢查的引擎交接退出後啟動原版。
// 引擎仍在組字時不強制換掉，記為待重啟，之後每次喚起再試。
func (c *Coordinator) recover(ctx context.Context, s *State, now time.Time) {
	if s.Switched && s.Target != nil && s.failed(s.Target.Version) {
		if err := c.Platform.RevertCore(s.Prepared); err != nil {
			s.record(now, "revert_failed", s.Target.Version, err.Error())
			return
		}
		s.Switched, s.RestartPending = false, true
	}
	if !s.RestartPending {
		return
	}
	if err := c.Platform.Drain(ctx); err != nil {
		s.record(now, "restart_waiting", "", err.Error())
		return
	}
	if err := c.Platform.StartEngine(ctx); err != nil {
		s.record(now, "restart_failed", "", err.Error())
		return
	}
	s.RestartPending = false
}

// remind：一般通知首次立即、24／72 小時補提醒，每版每天最多一次；
// 使用者主動選擇的同資產一小時提醒另外允許到期補一次。
func (c *Coordinator) remind(s *State, now time.Time) {
	if s.Snooze != nil && (s.Target == nil || s.Stage != StagePrepared || s.Switched || s.failed(s.Snooze.Version) ||
		s.Target.Version != s.Snooze.Version || s.Target.SHA256 != s.Snooze.SHA256) {
		s.Snooze = nil
	}
	if s.Blocked == nil || s.Target == nil {
		return
	}
	snoozedReminder := s.Snooze != nil
	if snoozedReminder && now.Before(s.Snooze.Until) {
		return
	}
	waitingSince := s.Blocked.Since
	// A transient change of blocker must not restart a package's long wait.
	if !s.PreparedAt.IsZero() && (waitingSince.IsZero() || s.PreparedAt.Before(waitingSince)) {
		waitingSince = s.PreparedAt
	}
	actionable := s.Blocked.Reason == "needs_consent" || s.Blocked.Reason == "needs_relogin" || s.Blocked.Reason == "health_failed" ||
		(s.Stage == StagePrepared && !waitingSince.IsZero() && now.Sub(waitingSince) >= c.RemindAfter)
	if !actionable && !snoozedReminder {
		return
	}
	key := s.Target.Version + "/" + s.Blocked.Reason
	count := s.NoticeCount
	if s.NoticeFor != key {
		count = 0
	}
	// NoticeFor belongs to the last successful delivery. A different reason
	// is still the same version; a failed delivery must not relabel its clock.
	sameVersion := strings.HasPrefix(s.NoticeFor, s.Target.Version+"/")
	due := count == 0 ||
		(count == 1 && now.Sub(waitingSince) >= 24*time.Hour) ||
		(count >= 2 && now.Sub(waitingSince) >= 72*time.Hour)
	if !snoozedReminder && (!due || sameVersion && !s.LastNotice.IsZero() && now.Sub(s.LastNotice) < 24*time.Hour) {
		return
	}
	if err := c.Platform.Notify(notice(s)); err != nil {
		s.record(now, "notify_failed", s.Target.Version, err.Error())
		return
	}
	// 送交系統不代表使用者已看見；持久待辦仍由 0c-update status 與輸入法狀態入口提供。
	s.LastNotice = now
	s.NoticeFor, s.NoticeCount = key, count
	if !snoozedReminder {
		s.NoticeCount++
	}
	s.Snooze = nil // consume only after the OS accepts; notification failure may retry
	s.record(now, "notified", s.Target.Version, s.Blocked.Reason)
}

// Notice 是一則通知；Action 非空時通知附「立即安裝」按鈕，按下即執行 0c-update finish。
type Notice struct {
	Title         string `json:"title"`
	Body          string `json:"body"`
	Action        string `json:"action,omitempty"` // "finish" 或空字串
	TargetVersion string `json:"target_version,omitempty"`
	TargetSHA256  string `json:"target_sha256,omitempty"`
}

func notice(s *State) Notice {
	v := s.Target.Version
	switch s.Blocked.Reason {
	case "needs_consent":
		return Notice{Title: "靈犀注音有新版本", Body: fmt.Sprintf("%s 已下載，需要你確認安裝。", v), Action: "finish", TargetVersion: v, TargetSHA256: s.Target.SHA256}
	case "needs_relogin":
		if s.Blocked.Detail == "登出再登入時自動完成安裝" {
			return Notice{Title: "靈犀注音有新版本", Body: fmt.Sprintf("%s 已下載，登出再登入時自動完成安裝。", v)}
		}
		return Notice{Title: "靈犀注音已更新", Body: fmt.Sprintf("%s 已安裝，請登出再登入後使用。", v)}
	case "health_failed":
		return Notice{Title: "靈犀注音更新未完成", Body: fmt.Sprintf("%s 未通過檢查，已保留原版本。", v)}
	}
	return Notice{Title: "靈犀注音更新待完成", Body: fmt.Sprintf("%s 已準備好，將在閒置時完成；也可以現在完成。", v), Action: "finish", TargetVersion: v, TargetSHA256: s.Target.SHA256}
}

// newer 回報 a 是否比已安裝版本 b 新；b 無法判讀（例如 legacy）時視為較舊。
func newer(a, b string) bool {
	if b == "" || !update.ValidVersion(b) {
		return true
	}
	order, err := update.CompareVersion(a, b)
	return err == nil && order > 0
}

// inRollout 依安裝識別碼與版本決定是否在分批比例內（同一裝置對同一版結果固定）。
func inRollout(installID, version string, rollout int) bool {
	sum := sha256.Sum256([]byte(installID + "/" + version))
	return int(binary.BigEndian.Uint32(sum[:4])%100) < rollout
}

func lock(base string) (func(), error) {
	return lockFile(base, "coordinator.lock")
}

// lockFile 是 base/update 下的命名鎖；規則在 internal/filelock，0c-server 寫設定時也用同一套（#169）。
func lockFile(base, name string) (func(), error) { return filelock.Acquire(base, name) }

// processAlive 回報 pid 的行程是否仍在執行。
func processAlive(pid int) bool { return filelock.ProcessAlive(pid) }
