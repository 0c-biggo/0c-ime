//go:build !windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
)

// 身分記號只放 update/，不寫進 App。inode 可辨識相同版本重裝與回復；mtime/size
// 也納入，原路徑原 inode 被直接替換內容時不沿用已接管結果。
func agentShellIdentity(exe string) string {
	info, err := os.Stat(exe)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%s\n%d:%d:%d:%d", exe, st.Dev, st.Ino, info.Size(), info.ModTime().UnixNano())
}

func agentTakeoverStamp(base string) string { return filepath.Join(base, "update", "agent-shell") }

func agentInstallIdentity(base, exe string) string {
	binary := agentShellIdentity(exe)
	receipt := agentShellIdentity(filepath.Join(base, "install-receipt.json"))
	if binary == "" || receipt == "" {
		return ""
	}
	// 回復可將原本同 inode 的 App 放回；收據的本次交易身分也必須一致。
	return binary + "\n" + receipt
}
func agentTakeoverDone(base, identity string) bool {
	if identity == "" {
		return false
	}
	b, err := os.ReadFile(agentTakeoverStamp(base))
	return err == nil && string(b) == identity
}

type agentTakeoverControl struct {
	identity func() string
	shells   func() ([]uiProcess, error)
	away     func() bool
	retire   func() bool
	pull     func() bool
}

// 只接手已安裝版本。觀察失敗、仍有舊外殼、使用者回來或安裝身分改變，都不拉排程。
// retire 內還會持續核對離開／沒有組字／停手條件；不重送訊號、不另開常駐監看。
func finishAgentTakeover(identity string, c agentTakeoverControl) bool {
	if identity == "" || c.identity() != identity {
		return false
	}
	old, err := c.shells()
	if err != nil {
		return false
	}
	if len(old) > 0 {
		if !c.away() || !c.retire() {
			return false
		}
		old, err = c.shells()
		if err != nil || len(old) > 0 {
			return false
		}
	}
	if c.identity() != identity || !c.pull() {
		return false
	}
	return c.identity() == identity
}

// 選單可能由多個舊外殼同時輪詢；同一安裝 30 秒內最多啟動一次有界接管。
// worker 自己另持 agent-takeover.lock；啟動失敗也只等下一次既有輪詢，沒有常駐重試。
func claimAgentSpawn(base, identity string, now time.Time) bool {
	claimed := false
	_ = autoupdate.WithLock(base, "agent-spawn.lock", func() error {
		stamp := filepath.Join(base, "update", "agent-spawn")
		if info, err := os.Stat(stamp); err == nil {
			b, err := os.ReadFile(stamp)
			age := now.Sub(info.ModTime())
			if err == nil && string(b) == identity && age >= 0 && age < 30*time.Second {
				return nil
			}
		}
		if err := os.WriteFile(stamp, []byte(identity), 0o600); err != nil {
			return err
		}
		claimed = true
		return nil
	})
	return claimed
}
