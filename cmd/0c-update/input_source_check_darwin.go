//go:build darwin

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/update"
)

// 更新交接後的輸入來源自我檢查（#351），說明見 input_source_check_mac.go。

// checkInputSourceAfterHandover 由背景排程的 auto 在交接之後呼叫；測試替換掉，不碰這台機器的輸入來源（isolate_darwin_test.go）。
var checkInputSourceAfterHandover = checkInstalledInputSource

// inspectInputSource 以 App 內附的 0c 在新的行程查詢輸入來源（只查詢，不登記、不啟用、不改選）。
func inspectInputSource(tool, app string) (inputSourceHealth, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, tool, "--inspect-input-source", app).Output()
	if err != nil {
		return inputSourceHealth{}, fmt.Errorf("查詢輸入來源失敗：%w", err)
	}
	return parseInputSourceHealth(out)
}

// restartTextInputMenuAgent 結束輸入法選單的系統程式，launchd 會立即重新啟動它（重新讀取輸入來源清單）。
func restartTextInputMenuAgent() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "/usr/bin/killall", "TextInputMenuAgent").CombinedOutput(); err != nil {
		return fmt.Errorf("killall TextInputMenuAgent：%s (%w)", string(out), err)
	}
	return nil
}

// secureInputOn：安全輸入開著（某個 App 呼叫了 EnableSecureEventInput）。這時系統把第三方輸入法變灰，查到的不是更新造成的。
func secureInputOn() bool {
	ctx, cancel := context.WithTimeout(context.Background(), ioregTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/sbin/ioreg", "-l", "-d", "1", "-w", "0", "-k", "IOConsoleUsers").Output()
	return err == nil && len(secureInputPIDs(string(out))) > 0
}

// checkInstalledInputSource 檢查目前安裝的 App 的輸入來源，每次安裝只做一次；條件不成立時（還沒交接、剛裝好等）
// 留給下一次喚起。修好或修不好都記進更新紀錄，修不好的交給登入後的補做。
func checkInstalledInputSource(base string) {
	r, err := readMacReceiptFile(receiptPath())
	if err != nil || r == nil || !r.Active || r.App != update.MacApp {
		return
	}
	app := installedApp()
	tool := filepath.Join(app, "Contents/MacOS/0c")
	info, err := os.Stat(tool)
	if err != nil {
		return
	}
	receipt, err := os.Stat(receiptPath())
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	version := bundleBuildVersion(ctx, app)
	cancel()
	gate := inputSourceCheckGate{
		Key:          fmt.Sprintf("%s/%d", r.Version, info.Sys().(*syscall.Stat_t).Ino),
		Last:         loadInputSourceCheck(base),
		Version:      version,
		Pending:      inputSourcePending(base),
		SinceInstall: time.Since(receipt.ModTime()),
	}
	if gate.wait() != "" {
		return // 便宜的條件先看，不必每次喚起都跑 ps、ioreg
	}
	gate.Relogin = macReloginRequired(base)
	left, err := observeMacShells(tool)
	gate.ShellsLeft = err != nil || len(left) > 0
	gate.SecureInput = !gate.Relogin && !gate.ShellsLeft && secureInputOn()
	if gate.wait() != "" {
		return
	}
	outcome, steps := inputSourceRepair{
		inspect:     func() (inputSourceHealth, error) { return inspectInputSource(tool, app) },
		register:    func() error { return activateInputSource(tool, app) },
		restartMenu: restartTextInputMenuAgent,
		wait:        time.Sleep,
	}.run()
	saveInputSourceCheck(base, inputSourceCheck{Key: gate.Key, Version: r.Version, At: time.Now().UTC(), Outcome: outcome, Steps: steps})
	if outcome == "ok" {
		return
	}
	detail := outcome
	if len(steps) > 0 && steps[len(steps)-1].Detail != "" {
		detail += "：" + steps[len(steps)-1].Detail
	}
	_, _ = autoupdate.EditState(base, func(s *autoupdate.State) error {
		s.Record("input_source_check", detail)
		return nil
	})
	if outcome == "failed" {
		// 系統登出前不一定重讀輸入來源的設定（Apple DTS 775526）：交給登入後的補做，狀態摘要的 input_source_pending 也看得到。
		if err := deferInputSource(tool, app, false); err != nil {
			fmt.Fprintln(os.Stderr, "0c-update: 無法記下登入後補做輸入來源：", err)
		}
	}
}
