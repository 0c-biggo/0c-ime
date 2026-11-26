//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// launchHandoverElevated 以 UAC 啟動新版（測試替換）。
var launchHandoverElevated = launchElevated

// runHandover 執行新版的 install-staged，先以一般權限：要不要系統管理員由新版判斷（#294 的 TSF 註冊指紋；之後註冊
// 的變化舊版不一定看得懂）。新版回報 needs_consent（要改 HKLM 的註冊）時，使用者親自要求安裝（-consent）就以 UAC
// 提權同一個新版、同一串參數再執行一次（不是提權這一份舊的），帶上原使用者與安裝目錄讓新版核對；背景排程不提權，
// 結果照實留著，協調器停在 needs_consent。提權的行程沒有主控台可以接，輸出改寫到結果檔旁邊，結束後印出來。
// 已經是系統管理員（從系統管理員命令列執行 finish、或舊的提權路徑）時直接執行。
func runHandover(ctx context.Context, exe string, req handoverRequest) error {
	direct := func() error {
		cmd := exec.CommandContext(ctx, exe, req.args()...)
		hideWindow(cmd)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		return cmd.Run()
	}
	if elevatedOK() {
		return direct()
	}
	err := direct()
	out, readErr := readHandoverResult(req.Result)
	if readErr != nil || out.Reason != "needs_consent" || !req.Consent {
		return err
	}
	os.Remove(req.Result)
	sid, err := processUserSID()
	if err != nil {
		return err
	}
	req.OriginUser, req.OriginBase = sid, basePath()
	req.Log = filepath.Join(filepath.Dir(req.Result), "handover.log")
	err = runElevated(exe, req.args(), launchHandoverElevated)
	if b, e := os.ReadFile(req.Log); e == nil {
		os.Stdout.Write(b)
	}
	if userCancelled(err) {
		return fmt.Errorf("%w（%w）", errHandoverCancelled, err)
	}
	return err
}
