//go:build !windows

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/settingsapp"
)

// syncMacSettingsEntry 的路徑由呼叫端提供，安裝／回復測試不需碰真的家目錄或 LaunchServices。
// 舊版沒有內附啟動器時移除入口；驗證錯誤則保留，讓開啟時的既有核對流程說明失敗原因。
func syncMacSettingsEntry(applications, receipt, inputMethods, stable string) (bool, error) {
	r, err := readMacSettingsReceipt(receipt)
	if errors.Is(err, settingsapp.ErrNotFound) {
		return false, settingsapp.RemoveMacLauncher(applications)
	}
	if err != nil {
		return false, err
	}
	root := filepath.Join(inputMethods, r.App)
	settingsFiles := settingsapp.BundleFiles(r.Files, settingsAppPrefix)
	launcherFiles := settingsapp.BundleFiles(r.Files, settingsLauncherPrefix)
	if len(settingsFiles) == 0 || len(launcherFiles) == 0 {
		return false, settingsapp.RemoveMacLauncher(applications)
	}
	if _, err := settingsapp.LocateBundle(filepath.Join(root, settingsAppPrefix), settingsFiles); err != nil {
		return false, err
	}
	source := filepath.Join(root, settingsLauncherPrefix)
	if err := settingsapp.VerifyBundle(source, launcherFiles); err != nil {
		return false, err
	}
	// 排程關閉或建立失敗仍要有開得起來的設定入口；只換目前收據的更新器，不重建背景排程。
	updater := filepath.Join(root, "Contents/MacOS/0c-update")
	want := r.Files["Contents/MacOS/0c-update"]
	if err := settingsapp.EnsureMacUpdater(updater, want, stable); err != nil {
		return false, err
	}
	return settingsapp.EnsureMacLauncher(applications, source, launcherFiles)
}

// 設定程式入口的 LaunchServices 登記（#353）：安裝最後執行內附的 0c-settings-launcher --register-entry。桌面掛著沒人
// 回答的系統詢問時（例如別的 App 要取用卸除式卷宗），LSRegisterURL 會一直等，整個安裝（含背景自動更新）跟著停住。
// 入口登記失敗不該擋住輸入法更新：加上時限，逾時就結束它、記下來，之後的排程喚起（refreshPathUpdater）再補登記。

// registerEntryTimeout：平常幾十毫秒就完成。
var registerEntryTimeout = 20 * time.Second

// runRegisterEntry 執行登記程式，逾時就結束它並回報。
func runRegisterEntry(program, entry string) error {
	ctx, cancel := context.WithTimeout(context.Background(), registerEntryTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, "--register-entry", entry)
	cmd.WaitDelay = 2 * time.Second // 被結束後，它留下的子行程拿著輸出也不再等
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("登記設定程式入口超過 %v 沒有結束（桌面可能有沒回答的系統詢問），已結束它，之後再補登記", registerEntryTimeout)
	}
	if err != nil {
		return fmt.Errorf("登記設定程式入口失敗：%s (%w)", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// settingsEntryPending：上次登記沒有完成。入口的檔案已經同步好（syncMacSettingsEntry 不再回報改變），沒有這個記號
// 就不會再登記。
func settingsEntryPending(base string) string {
	return filepath.Join(base, "update", "settings-entry-register")
}

const (
	settingsEntryEvent      = "settings_entry"
	settingsEntryRegistered = "已補登記設定程式入口"
)

// registerSettingsEntry 登記入口並維護記號與更新紀錄：失敗寫記號、記進更新紀錄（連續失敗只記一筆；被協調器存檔蓋掉時，
// 下一次補登記又失敗會再記）；之前失敗過、這次成功時記下已補登記。
func registerSettingsEntry(base string, register func() error) error {
	marker := settingsEntryPending(base)
	_, statErr := os.Stat(marker)
	err := register()
	if err != nil {
		_ = os.MkdirAll(filepath.Dir(marker), 0o700)
		_ = os.WriteFile(marker, []byte(err.Error()+"\n"), 0o600)
		_, _ = autoupdate.EditState(base, func(s *autoupdate.State) error {
			if last := lastEvent(s, settingsEntryEvent); last == nil || last.Detail == settingsEntryRegistered {
				s.Record(settingsEntryEvent, err.Error())
			}
			return nil
		})
		return err
	}
	if statErr == nil {
		os.Remove(marker)
		_, _ = autoupdate.EditState(base, func(s *autoupdate.State) error {
			s.Record(settingsEntryEvent, settingsEntryRegistered)
			return nil
		})
	}
	return nil
}

func lastEvent(s *autoupdate.State, kind string) *autoupdate.Event {
	for i := len(s.History) - 1; i >= 0; i-- {
		if s.History[i].Kind == kind {
			return &s.History[i]
		}
	}
	return nil
}
