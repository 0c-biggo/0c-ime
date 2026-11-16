package main

import (
	"runtime"

	"github.com/0c-biggo/0c-ime/internal/update"
)

// buildVersion 由打包工具以 -ldflags "-X main.buildVersion=<版本>" 寫入（package_windows.py、package_macos.py、
// package_release.py、Makefile 本來就對 0c-update 傳這個參數）；開發建置為 dev。
var buildVersion = "dev"

// 查詢索引與下載時的 User-Agent 帶版本與平台（`0c-update/<版本> (<GOOS>; <GOARCH>)`），不帶任何識別碼。
// 伺服器紀錄因此看得出有多少使用者停在哪一版、哪個平台的更新器；本來用的是 Go 的預設值，看不出來。
func init() {
	update.UserAgent = update.UpdaterUserAgent(buildVersion, runtime.GOOS, runtime.GOARCH)
}
