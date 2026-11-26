//go:build darwin

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/filelock"
)

// 安裝時「允許」晚出現的補做（#348），說明見 input_source_followup_mac.go。

const inputSourceFollowUpLock = "input-source-followup.lock"

func init() { followUpInputSource = followUpInstalledInputSource }

// followUpInstalledInputSource 由背景排程的 auto 在補做的同一個登入工作階段呼叫（retryInputSource）。
func followUpInstalledInputSource(base, tool, app string, since time.Duration) bool {
	plan := planFollowUp(since)
	if plan == followUpNone {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	version := bundleBuildVersion(ctx, app)
	cancel()
	if !inspectInputSourceSupported(version) {
		return false // 0.2.15 以前的 0c 沒有 --inspect-input-source：照舊
	}
	if plan == followUpSpawn {
		// 安裝時 postinstall 衍生的程式會在安裝程式刪掉腳本目錄後被 syspolicyd 結束（engineAfterInstall），所以由
		// 排程（安裝後立即喚起）起背景程式；已經在跑的那個拿著鎖，新起的會自己結束。
		spawnInputSourceFollowUp()
		return true
	}
	_, _ = runInputSourceFollowUp(base, time.Now())
	return true
}

// spawnInputSourceFollowUp 起脫離排程的背景程式（setsid：排程結束時 launchd 才不會一起收掉，見 engine_service_darwin.go）。
var spawnInputSourceFollowUp = func() {
	self, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(self, "input-source-followup")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0); err == nil {
		defer devnull.Close()
		cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, devnull, devnull
	}
	if cmd.Start() == nil {
		go cmd.Wait()
	}
}

// inputSourceFollowUpCommand：0c-update input-source-followup（內部用，由 spawnInputSourceFollowUp 啟動）。
// 查到安裝時記下補做後 inputSourceFollowUpWatch 為止。
func inputSourceFollowUpCommand() error {
	base := basePath()
	r := loadInputSourceRetry(base)
	if r == nil {
		return nil
	}
	outcome, err := runInputSourceFollowUp(base, r.At.Add(inputSourceFollowUpWatch))
	if err != nil {
		return err
	}
	fmt.Println(outcome)
	return nil
}

// runInputSourceFollowUp 查到 until 為止（不晚於現在時只查一次）。另一個補做正在跑時直接結束。
func runInputSourceFollowUp(base string, until time.Time) (string, error) {
	unlock, err := filelock.Acquire(base, inputSourceFollowUpLock)
	if err != nil {
		if errors.Is(err, filelock.ErrLocked) {
			return "busy", nil
		}
		return "", err
	}
	defer unlock()
	b, err := os.ReadFile(inputSourceMarker(base))
	if err != nil {
		return "none", nil // 已經啟用（記號清掉了）
	}
	tool, app, ok := deferredInputSource(b, fileExists)
	if !ok {
		return "none", nil
	}
	r := loadInputSourceRetry(base)
	cur, boot := macLoginStamp(time.Now()), macBootTime()
	if r != nil {
		if crossed, _ := r.session().crossed(cur.ASID, boot); crossed {
			return "none", nil // 已經跨過登入邊界：交給登入後的補做
		}
	}
	var activateErr error
	outcome := inputSourceFollowUp{
		inspect: func() (inputSourceHealth, error) { return inspectInputSource(tool, app) },
		activate: func() error {
			activateErr = activateInputSource(tool, app)
			return activateErr
		},
		requested:     func() bool { return modeRequestedIn(base, cur, boot) },
		markRequested: func() { saveModeRequested(base, cur) },
		finish:        func() { finishInputSourceFollowUp(base, r) },
		now:           time.Now,
		wait:          time.Sleep,
		until:         until,
		poll:          inputSourceFollowUpPoll,
	}.run()
	if outcome != "waiting" {
		// waiting 不記：之後每次排程喚起都可能查一次，不能塞滿更新紀錄。
		detail := "輸入模式已啟用（安裝時沒等到的「允許」之後才按下），不必重新登入"
		if outcome == "requested" {
			detail = "已請系統啟用輸入模式，還沒有允許；登入後會再試"
			if activateErr != nil {
				// 0.2.15 起的 App 在啟用清單只有父項時不送模式的要求（送了也加不進去，#348），錯誤裡是請使用者移除再加入的說明。
				detail = "輸入模式還沒啟用：" + activateErr.Error()
			}
		}
		_, _ = autoupdate.EditState(base, func(s *autoupdate.State) error {
			s.Record("input_source_followup", detail)
			return nil
		})
	}
	return outcome, nil
}
