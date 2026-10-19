package settingsapp

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// LaunchOptions 指定如何啟動設定程式。
type LaunchOptions struct {
	Page    string        // 白名單內的頁面；空值開總覽
	Updater string        // 啟動它的 0c-update 絕對路徑：設定程式用同一個更新器讀取契約，版本必定一致
	Log     string        // 設定程式 stdout／stderr 的記錄檔（每次啟動覆寫）；提早失敗時用它說明原因
	Wait    time.Duration // 啟動後觀察多久；期間內結束代表啟動失敗（單一實例交棒給既有視窗則是結束碼 0）
}

// ExitError：設定程式在觀察期內就以非零結束碼退出。
type ExitError struct {
	Code int
	Tail string // 記錄檔最後的內容
}

func (e *ExitError) Error() string {
	if e.Tail == "" {
		return fmt.Sprintf("設定程式啟動後立即結束（結束碼 %d）", e.Code)
	}
	return fmt.Sprintf("設定程式啟動後立即結束（結束碼 %d）：%s", e.Code, e.Tail)
}

// Launch 以脫離的方式啟動設定程式：自成一個工作階段、不接輸入，呼叫端（外殼的選單、桌面捷徑）
// 不必等它。觀察 Wait 這段時間：還在執行或結束碼為 0 都算成功；提早以非零結束碼退出回傳 *ExitError。
func Launch(path string, o LaunchOptions) error {
	args := []string{"--page", Page(o.Page)}
	cmd := exec.Command(path, args...)
	cmd.Env = os.Environ()
	if o.Updater != "" {
		cmd.Env = append(cmd.Env, "ZC_UPDATER="+o.Updater)
	}
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		return err
	}
	defer devnull.Close()
	cmd.Stdin = devnull
	cmd.Stdout, cmd.Stderr = devnull, devnull
	var logFile *os.File
	if o.Log != "" {
		if logFile, err = os.OpenFile(o.Log, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600); err == nil {
			defer logFile.Close()
			cmd.Stdout, cmd.Stderr = logFile, logFile
		}
	}
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			return nil
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return &ExitError{Code: exit.ExitCode(), Tail: tail(o.Log)}
		}
		return err
	case <-time.After(o.Wait):
		return nil
	}
}

func tail(path string) string {
	if path == "" {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if len(b) > 600 {
		b = b[len(b)-600:]
	}
	return strings.TrimSpace(string(b))
}
