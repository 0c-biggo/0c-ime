package main

import (
	"fmt"
	"os"
)

// refreshStableMCP 讓 %LOCALAPPDATA%\Zuin\bin\0c-mcp.exe 跟上目前版本（見 syncStableMCP）。在安裝、回復、解除安裝、
// 核心更新生效、schedule repair 與 open-settings 時執行：完整外殼更新由舊的穩定更新器執行，它不認得 0c-mcp，
// 之後由新版的 repair（0c-ui 定期執行）或開啟設定程式補上。失敗只提示，不影響輸入法本身。
func refreshStableMCP(base string) {
	source, want := currentMCP(base, receiptPath())
	switch changed, err := syncStableMCP(source, want, stableMCP(base)); {
	case err != nil:
		fmt.Fprintln(os.Stderr, "0c-update: AI 工具用的 0c-mcp 未更新：", err)
	case changed && source != "":
		fmt.Println("已更新 AI 工具用的 0c-mcp：" + stableMCP(base))
	case changed:
		fmt.Println("已移除 AI 工具用的 0c-mcp：" + stableMCP(base)) // 解除安裝，或回復到沒有附它的版本
	}
}
