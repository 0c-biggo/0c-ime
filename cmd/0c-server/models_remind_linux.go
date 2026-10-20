package main

import (
	"log"
	"os"
	"os/exec"
	"syscall"

	"github.com/0c-biggo/0c-ime/internal/installs"
)

// newModelRemind 只在 Linux、沒有找到模型時建立（macOS、Windows 的安裝包一定內建模型）。
func newModelRemind(userDir, dictionary string, lg *log.Logger) *modelRemind {
	if dictionary == "" {
		return nil
	}
	return &modelRemind{userDir: userDir, dictionary: dictionary, run: func(args ...string) {
		updater := installs.Default().Updater() // 與外殼選單相同：官網安裝優先，再來系統安裝
		cmd := exec.Command(updater, args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // 交接或結束時不連帶停掉它
		if devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0); err == nil {
			cmd.Stdin, cmd.Stdout = devnull, devnull
			defer devnull.Close()
		}
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			lg.Printf("無法檢查模型（%s）：%v", updater, err)
			return
		}
		go cmd.Wait()
	}}
}
