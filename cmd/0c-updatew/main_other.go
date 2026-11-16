//go:build !windows

package main

import (
	"fmt"
	"os"
)

// 只有 Windows 的工作排程器與協定需要它；其他平台的排程（systemd、launchd）本來就不開視窗。
func main() {
	fmt.Fprintln(os.Stderr, "0c-updatew 只用於 Windows；請直接執行 0c-update")
	os.Exit(2)
}
