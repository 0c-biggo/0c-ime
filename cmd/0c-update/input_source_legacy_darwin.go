//go:build darwin

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// 說明見 input_source_legacy_mac.go。

// completeLegacyInputMode 在換上新 App 之前呼叫：newApp 是套件裡的新 App（用它的 0c 查詢，看系統的啟用清單），
// 已安裝的舊 App 只有父項在啟用清單裡時，以舊 App 的 0c 送出模式的要求並等「允許」。失敗不影響安裝。
var completeLegacyInputMode = func(newApp string) {
	old := installedApp()
	oldTool := filepath.Join(old, "Contents/MacOS/0c")
	if _, err := os.Stat(oldTool); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	version := bundleBuildVersion(ctx, old)
	cancel()
	h, err := inspectInputSource(filepath.Join(newApp, "Contents/MacOS/0c"), old)
	if err != nil || !legacyModeMissing(version, h) {
		return
	}
	fmt.Println("「靈犀注音」的輸入模式還沒加入系統的輸入方式，先請系統加入；系統詢問時請按「允許」。")
	if err := activateInputSource(oldTool, old); err != nil {
		fmt.Printf("輸入模式尚未加入：%v\n", err)
	}
}
