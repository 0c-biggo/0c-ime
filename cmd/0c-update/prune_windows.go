//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/0c-biggo/0c-ime/internal/update"
	"golang.org/x/sys/windows"
)

// pruneCreationFlags：沒有主控台（不繼承安裝精靈的主控台、不閃視窗）、自成行程群組（呼叫端的 Ctrl+C／關閉不連帶），
// 低優先順序。標準輸入輸出由 os/exec 接到 NUL，不繼承呼叫端的管線（安裝精靈的 nsExec 讀輸出到行程結束）。
const pruneCreationFlags = windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP | windows.BELOW_NORMAL_PRIORITY_CLASS

func pruneProcess(exe, base string, flags uint32) *exec.Cmd {
	cmd := exec.Command(exe, "prune")
	cmd.Dir = base // 不占住呼叫端的工作目錄（安裝程式的暫存目錄）
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags, HideWindow: true}
	return cmd
}

// spawnPrune 啟動背景清理，不等它結束。優先脫離呼叫端所在的 job（工作排程器、安裝程式結束時不帶走它），
// 不允許脫離時照常啟動。
func spawnPrune(exe string) error {
	var err error
	for _, flags := range []uint32{pruneCreationFlags | createBreakawayFromJob, pruneCreationFlags} {
		cmd := pruneProcess(exe, basePath(), flags)
		if err = cmd.Start(); err == nil {
			return cmd.Process.Release()
		}
	}
	return err
}

// pruneExecutable：回復、解除安裝時用自己（在安裝目錄裡時），否則用收據目前版本目錄裡的 0c-update。
func pruneExecutable() string {
	self, _ := os.Executable()
	fallback := ""
	if data := installedDataDir(basePath()); data != "" {
		fallback = filepath.Join(filepath.Dir(data), updaterName())
	}
	return pruneExecutableFor(self, basePath(), fallback)
}

// pruneCommand 是背景清理行程（0c-update prune）：低 I/O 與記憶體優先順序，拿不到安裝鎖就放棄，交給下一次。
func pruneCommand() error {
	_ = windows.SetPriorityClass(windows.CurrentProcess(), windows.PROCESS_MODE_BACKGROUND_BEGIN)
	return pruneOnce(basePath())
}

func pruneOnce(base string) error {
	err := update.PruneWindows(base)
	if errors.Is(err, update.ErrInstallLocked) {
		return nil
	}
	return err
}

// pruneInstallsNow：排程喚起時補做的清理（沒有東西可清時不拿鎖、幾乎不花時間）。
func pruneInstallsNow(base string) {
	if err := update.PruneWindows(base); err != nil && !errors.Is(err, update.ErrInstallLocked) {
		fmt.Fprintln(os.Stderr, "0c-update: 清理舊版失敗：", err)
	}
}
