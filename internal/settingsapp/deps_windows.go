//go:build windows

package settingsapp

import (
	"golang.org/x/sys/windows/registry"
)

// WebView2Runtime 是設定程式的網頁引擎：Windows 11 與近年的 Windows 10 都已內建；缺少時 Tauri 程式一啟動就失敗，
// 所以在啟動前偵測並說明。
const WebView2Runtime = "Microsoft Edge WebView2 Runtime"

// WebView2Hint 是缺少時的處理方式。只說明，不替使用者下載或安裝。
const WebView2Hint = "請安裝 Microsoft Edge WebView2 Runtime（Evergreen 版，https://developer.microsoft.com/microsoft-edge/webview2/ ）。"

// 所有安裝來源登記的位置：Evergreen Runtime 的產品代碼固定，pv 是版本，0.0.0.0 代表已解除安裝。
const webView2Client = `Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`

func registryValue(root registry.Key, path string, access uint32) string {
	key, err := registry.OpenKey(root, path, registry.QUERY_VALUE|access)
	if err != nil {
		return ""
	}
	defer key.Close()
	value, _, err := key.GetStringValue("pv")
	if err != nil {
		return ""
	}
	return value
}

// WebView2Installed 依登錄檔判斷（整機安裝或只裝給目前使用者）。
func WebView2Installed() bool {
	return webView2Present(
		registryValue(registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\`+webView2Client, registry.WOW64_64KEY),
		registryValue(registry.LOCAL_MACHINE, `SOFTWARE\`+webView2Client, registry.WOW64_64KEY),
		registryValue(registry.CURRENT_USER, `Software\`+webView2Client, 0),
	)
}

// MissingLibraries 回傳缺少的執行環境；Windows 只有 WebView2。
func MissingLibraries(string) []string {
	if WebView2Installed() {
		return nil
	}
	return []string{WebView2Runtime}
}

func DisplayAvailable() bool { return true }
