//go:build !windows

package main

import (
	"context"
	"os"
	"os/exec"
)

// runHandover 執行新版的 install-staged，輸出接在這個行程的輸出上（-json 時已改到 stderr）。Linux、macOS 的安裝
// 不需要提權，新版以同一位使用者執行。
func runHandover(ctx context.Context, exe string, req handoverRequest) error {
	cmd := exec.CommandContext(ctx, exe, req.args()...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}
