//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

// refreshStableLauncher（核心更新生效、schedule repair）：讓 bin\0c-updatew.exe 跟上目前版本（#289）。
//
// 只換檔案，不在這裡改寫排程或協定。排程定義由 repair 的 refreshTaskDefinitions 在定義不同、而且本人改得了時換新
// （0.2.11 以前提權建立、只能讀的排程不嘗試，等下一次提權的完整安裝）；協定在下一次送出有按鈕的通知前由
// registerProtocol 換新。兩者都只在內容不同時寫：以一般權限反覆建立排程、改寫協定處理程式像端點防護在找的
// 常駐手法（未簽章的測試建置在 .14 這樣做時被 Defender 判為 Behavior:Win32/Execution.A!ml）。
func refreshStableLauncher(base string) {
	source, want := currentStableSource(base, receiptPath(), launcherName)
	switch changed, err := syncStableLauncher(source, want, stableLauncher(base)); {
	case err != nil:
		fmt.Fprintln(os.Stderr, "0c-update: 排程用的啟動器未更新：", err)
	case changed:
		fmt.Println("已更新排程用的啟動器：" + stableLauncher(base))
	}
}

// launchCommand 是排程與協定要執行的命令與參數（verb 是 0c-update 的子命令）。啟動器在時直接執行它；
// 不在時（還沒有附啟動器的舊版套件）退回 conhost --headless 執行穩定更新器，排程照樣能跑、也不閃視窗。
func launchCommand(base string, verb ...string) (string, string) {
	if regularFile(stableLauncher(base)) {
		return stableLauncher(base), joinArgs(verb)
	}
	conhost := filepath.Join(os.Getenv("SystemRoot"), `System32\conhost.exe`)
	return conhost, joinArgs(append([]string{"--headless", stableUpdater(base)}, verb...))
}

func joinArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = syscall.EscapeArg(a)
	}
	return strings.Join(quoted, " ")
}

// protocolCommand 是目前註冊的 zc-update: 協定命令；沒有註冊時為空字串。
func protocolCommand() string {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Classes\`+protocolScheme+`\shell\open\command`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer key.Close()
	v, _, _ := key.GetStringValue("")
	return v
}
