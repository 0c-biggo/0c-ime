package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/0c-biggo/0c-ime/internal/atomicfile"
	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/update"
)

// 新版自己安裝（#292）。
//
// 以前背景更新的外殼安裝由使用者電腦上舊的穩定副本執行：舊程式跑新版的安裝（0.2.8 的 install_failed、#289 換不掉
// 排程、#253 留下 needs_relogin 都是這個原因），新版修好的安裝邏輯要再晚一版才用得到。現在舊的更新器只做核對
// （已簽章索引的雜湊、套件清單、Windows 的 Authenticode 簽章者、新版的自我測試），然後執行暫存套件裡新版的 0c-update
// 完成安裝；Windows 需要系統管理員時，提權的也是新版。比照 Omaha、Squirrel、VS Code：更新器只負責把新版交給新版。
//
// 交接命令列是永久契約：之後每一版的更新器都用它呼叫下一版，所以只能加選用參數，不能改既有參數的意義
// （docs/updating.md〈新版自己安裝的交接契約〉）：
//
//	<套件>/<0c-update> install-staged -handover 1 -root <套件根目錄> -version <版本> -result <結果檔>
//	    [-consent] [-login] [-idle-seconds N] [-log <輸出檔>] [-origin-user SID -origin-base 目錄]
//
// 結果檔是一份 JSON（handoverResult）；結束碼 0 表示成功，其他以結果檔為準。沒有結果檔表示新版沒有跑完（當掉、
// 被防毒擋下）：安裝失敗，原版不動，之後再試。

const (
	handoverVersion = 1
	// handoverSince 是第一個有 install-staged 的版本；更舊的套件（例如回復時）照舊由這一版在行程內安裝。
	handoverSince   = "0.2.15-dev.0"
	handoverTimeout = 35 * time.Minute
)

// handoverResult 是結果檔的內容。Reason：busy、health、needs_consent、at_login、disk_full、unsupported、failed。
type handoverResult struct {
	Schema  int    `json:"schema"`
	OK      bool   `json:"ok"`
	Pending bool   `json:"pending,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Error   string `json:"error,omitempty"`
	Version string `json:"version,omitempty"` // 執行安裝的 0c-update 版本
}

// handoverRequest 是交接參數。
type handoverRequest struct {
	Root, Version, Result, Log string
	Consent, Login             bool
	Idle                       time.Duration
	OriginUser, OriginBase     string
}

func (r handoverRequest) args() []string {
	a := []string{"install-staged", "-handover", strconv.Itoa(handoverVersion), "-root", r.Root, "-version", r.Version, "-result", r.Result}
	if r.Consent {
		a = append(a, "-consent")
	}
	if r.Login {
		a = append(a, "-login")
	}
	if r.Idle > 0 {
		a = append(a, "-idle-seconds", strconv.Itoa(int(r.Idle/time.Second)))
	}
	if r.Log != "" {
		a = append(a, "-log", r.Log)
	}
	if r.OriginUser != "" || r.OriginBase != "" {
		a = append(a, "-origin-user", r.OriginUser, "-origin-base", r.OriginBase)
	}
	return a
}

func resultFor(pending bool, err error) handoverResult {
	r := handoverResult{Schema: 1, OK: err == nil, Pending: pending && err == nil, Version: buildVersion}
	if err == nil {
		return r
	}
	r.Error = err.Error()
	switch {
	case errors.Is(err, autoupdate.ErrShellBusy):
		r.Reason = "busy"
	case errors.Is(err, autoupdate.ErrShellHealth):
		r.Reason = "health"
	case errors.Is(err, autoupdate.ErrNeedsConsent):
		r.Reason = "needs_consent"
	case errors.Is(err, autoupdate.ErrAtLogin):
		r.Reason = "at_login"
	case errors.Is(err, update.ErrInsufficientSpace) || errors.Is(err, syscall.ENOSPC):
		r.Reason = "disk_full"
	default:
		r.Reason = "failed"
	}
	return r
}

// errorOf 把結果換回協調器認得的錯誤。
func (r handoverResult) errorOf() error {
	if r.OK {
		return nil
	}
	detail := r.Error
	if detail == "" {
		detail = "新版安裝程式沒有說明原因"
	}
	switch r.Reason {
	case "busy":
		return fmt.Errorf("%w：%s", autoupdate.ErrShellBusy, detail)
	case "health":
		return fmt.Errorf("%w：%s", autoupdate.ErrShellHealth, detail)
	case "needs_consent":
		return fmt.Errorf("%w：%s", autoupdate.ErrNeedsConsent, detail)
	case "at_login":
		return fmt.Errorf("%w：%s", autoupdate.ErrAtLogin, detail)
	case "disk_full":
		return fmt.Errorf("%w：%s", update.ErrInsufficientSpace, detail)
	}
	return errors.New(detail)
}

// installStagedRun 是交接後新版做的事（測試替換）：與以前在行程內做的完全相同。
var installStagedRun = func(p *system, ctx context.Context, root string, r *update.Release) (bool, error) {
	return p.installStaged(ctx, root, r)
}

// installStagedCommand 是 install-staged：舊的更新器核對完套件後，交給新版的這個命令完成安裝。
func installStagedCommand(args []string) error {
	flags := flag.NewFlagSet("install-staged", flag.ContinueOnError)
	contract := flags.Int("handover", 0, "交接契約版本")
	root := flags.String("root", "", "已解開並核對的套件根目錄")
	version := flags.String("version", "", "索引上的版本")
	result := flags.String("result", "", "結果檔")
	logPath := flags.String("log", "", "輸出改寫到這個檔")
	consent := flags.Bool("consent", false, "使用者親自要求安裝")
	login := flags.Bool("login", false, "登入時、桌面啟動前")
	idle := flags.Int("idle-seconds", 0, "外殼切換前需要的閒置秒數")
	originUser := flags.String("origin-user", "", "UAC 交接的原使用者")
	originBase := flags.String("origin-base", "", "UAC 交接的原安裝目錄")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *result == "" || !filepath.IsAbs(*result) {
		return fmt.Errorf("install-staged 需要 -result 絕對路徑")
	}
	if *logPath != "" {
		if f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
			defer f.Close()
			os.Stdout, os.Stderr = f, f
		}
	}
	finish := func(pending bool, err error) error {
		out := resultFor(pending, err)
		if werr := atomicfile.WriteJSON(*result, out); werr != nil {
			return errors.Join(err, werr)
		}
		return err
	}
	if *contract != handoverVersion {
		out := handoverResult{Schema: 1, Reason: "unsupported", Error: fmt.Sprintf("不支援交接契約 %d", *contract), Version: buildVersion}
		if err := atomicfile.WriteJSON(*result, out); err != nil {
			return err
		}
		return errors.New(out.Error)
	}
	if err := validateHandoverOrigin(*originUser, *originBase); err != nil {
		return finish(false, err)
	}
	if !filepath.IsAbs(*root) {
		return finish(false, fmt.Errorf("-root 必須是絕對路徑"))
	}
	// 核對交給自己的套件：舊的更新器核對過，但檔案在使用者可寫的目錄，提權執行時再核對一次。
	r, err := update.Verify(*root, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return finish(false, err)
	}
	if r.Version != *version {
		return finish(false, fmt.Errorf("套件版本 %s 與索引 %s 不符", r.Version, *version))
	}
	p := &system{base: basePath(), consent: *consent, atLogin: *login, shellIdle: time.Duration(*idle) * time.Second}
	if p.shellIdle <= 0 {
		p.shellIdle = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	return finish(installStagedRun(p, ctx, *root, r))
}

// stagedUpdater 是套件裡新版的 0c-update（測試替換）。
var stagedUpdater = func(root string) string {
	switch runtime.GOOS {
	case "linux":
		return filepath.Join(root, "bin", "0c-update")
	}
	return filepath.Join(root, updaterName())
}

// handsOver：這一版的套件有 install-staged，交給它安裝。
func handsOver(version string) bool {
	order, err := update.CompareVersion(version, handoverSince)
	return err == nil && order >= 0
}

// installCancelled：Windows 交接時使用者在 UAC 按了「否」。協調器把它當成還需要確認（待辦保留）；finish 結束時照實回報。
var installCancelled error

// handOverInstall 核對新版的 0c-update（簽章者、自我測試），交給它安裝並讀回結果。
func (p *system) handOverInstall(ctx context.Context, root string, r *update.Release) (bool, error) {
	exe := stagedUpdater(root)
	if !regularFile(exe) {
		return false, keptOriginal("套件裡沒有 0c-update")
	}
	if err := verifyPublisher(exe); err != nil {
		return false, keptOriginal(err.Error())
	}
	switch result, detail := qualifyUpdaterFrom(p.base, exe, r.Version, p.archiveSHA); result {
	case selftestFail:
		return false, keptOriginal("新版更新器自我測試未通過（" + detail + "）")
	case selftestInconclusive:
		return false, fmt.Errorf("新版更新器自我測試無法完成，稍後再試（%s）", detail)
	}
	resultPath := filepath.Join(filepath.Dir(root), "handover-result.json")
	os.Remove(resultPath)
	req := handoverRequest{Root: root, Version: r.Version, Result: resultPath, Consent: p.consent, Login: p.atLogin, Idle: p.shellIdle}
	ctx, cancel := context.WithTimeout(ctx, handoverTimeout)
	defer cancel()
	runErr := runHandover(ctx, exe, req)
	if errors.Is(runErr, errHandoverCancelled) {
		installCancelled = runErr
		return false, fmt.Errorf("%w：%v", autoupdate.ErrNeedsConsent, runErr)
	}
	out, err := readHandoverResult(resultPath)
	if err != nil {
		if runErr != nil {
			return false, fmt.Errorf("新版安裝程式沒有完成：%w", runErr)
		}
		return false, err
	}
	return out.Pending, out.errorOf()
}

// readHandoverResult 讀結果檔；沒有或讀不懂時回報錯誤。
func readHandoverResult(path string) (handoverResult, error) {
	var out handoverResult
	b, err := os.ReadFile(path)
	if err != nil {
		return out, fmt.Errorf("新版安裝程式沒有留下結果")
	}
	if err := json.Unmarshal(b, &out); err != nil || out.Schema < 1 {
		return out, fmt.Errorf("新版安裝程式的結果讀不懂：%s", b)
	}
	return out, nil
}

// keptOriginal：新版在開始安裝之前就被擋下（簽章、自我測試），什麼都沒裝。協調器照 ErrShellHealth 暫停這一版，
// 但說明寫「保留原版」，不說「已回復」（沒有東西要回復）。
type keptOriginal string

func (e keptOriginal) Error() string {
	return "新版沒有通過安裝前的檢查，保留原版：" + string(e)
}
func (e keptOriginal) Unwrap() error { return autoupdate.ErrShellHealth }

// errHandoverCancelled：使用者取消了提權新版的 UAC。
var errHandoverCancelled = errors.New("已取消系統管理員確認；原版與更新待辦保留")
