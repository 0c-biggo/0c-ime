//go:build windows || darwin

package main

import (
	"context"
	"fmt"
	"github.com/0c-biggo/0c-ime/internal/update"
)

func reloadableShell() bool { return false }
func (p *system) installReloadableShell(context.Context, string, *update.Release) (bool, error) {
	return false, fmt.Errorf("此平台不支援外殼重載")
}

func (p *system) RecoverShell(context.Context) (bool, error) { return false, nil }

// restoreStuckInputMethod：只有 Linux 的 Fcitx5 有 #93 卡住後要補回的輸入法清單。
func restoreStuckInputMethod(string, bool) {}

// finishKWinIM、repairKWinFcitx：只有 Linux 的 KDE Plasma Wayland 由 KWin 啟動輸入法（#204）。
func finishKWinIM(string)    {}
func repairKWinFcitx(string) {}
