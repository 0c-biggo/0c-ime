//go:build windows

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/0c-biggo/0c-ime/internal/updatew"
	"golang.org/x/sys/windows"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	exe, err := os.Executable()
	if err != nil {
		return 1
	}
	base, logPath, err := plan(exe, args)
	if err != nil {
		return 2 // GUI 程式沒有地方顯示訊息；排程的「上次執行結果」看得到結束碼
	}
	// 只有 0c-update 已建立的 update\ 目錄才寫紀錄；不自己建目錄（解除安裝後殘留的排程不在使用者目錄留下東西）。
	var out io.Writer
	if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600); err == nil {
		defer f.Close()
		out = f
		fmt.Fprintf(f, "%s 0c-updatew %s\n", time.Now().Format(time.RFC3339), strings.Join(args, " "))
	}
	code := updatew.Launch(updatew.Env{
		Base: base,
		Now:  time.Now,
		Hash: hashFile,
		Exec: func(path string, args []string) (error, int) {
			if out != nil {
				fmt.Fprintf(out, "%s 執行 %s\n", time.Now().Format(time.RFC3339), path)
			}
			cmd := exec.Command(path, args...)
			cmd.Stdout, cmd.Stderr = out, out
			cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
			if err := cmd.Start(); err != nil {
				return err, 0
			}
			code := 0
			if err := cmd.Wait(); err != nil {
				var exit *exec.ExitError
				if errors.As(err, &exit) {
					code = exit.ExitCode()
				} else {
					code = 1
				}
			}
			if out != nil {
				fmt.Fprintf(out, "%s 結束碼 %d\n", time.Now().Format(time.RFC3339), code)
			}
			return nil, code
		},
		Log: out,
	}, args)
	return code
}

// hashFile 是檔案的 SHA-256；不存在、被防毒隔離或讀不到時為空字串。
func hashFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}
