// 0c-updatew 是 Windows 上不開主控台的 0c-update 啟動器（#289）：工作排程器的自動更新、登入啟動引擎，以及通知按鈕的
// zc-update: 協定都執行它，它再以「沒有視窗的主控台」（CREATE_NO_WINDOW）執行同一個目錄的 0c-update.exe，
// 等它結束並傳回結束碼，輸出寫進 %LOCALAPPDATA%\Zuin\update\<動作>-last.log。
//
// 為什麼需要它：0c-update 是主控台程式，工作排程器直接執行會閃出主控台視窗（Windows 11 還會交給 Windows
// Terminal 開一個分頁），所以先前以 conhost.exe --headless "<0c-update.exe>" … 執行。「conhost --headless 帶起另一個
// 程式」是惡意程式藏視窗的常見手法，Elastic「Proxy Execution via Console Window Host」、Splunk「Windows ConHost with
// Headless Argument」等端點防護規則直接看行程命令列，#284 的開始功能表捷徑就被 SEP 隔離。這支程式以 GUI 子系統
// （-H windowsgui）建置，本身沒有主控台；PIME 的 PIMELauncher 同樣是 GUI 程式，以 CREATE_NO_WINDOW 啟動後端。
//
// 子行程用 CREATE_NO_WINDOW 而不是 DETACHED_PROCESS：0c-update 有些子行程（schtasks、0c-server -check-data）沒有
// 另外指定旗標，要繼承一個看不見的主控台；完全沒有主控台時它們會各自開一個看得見的視窗。
//
// 只接受排程與協定用到的三種呼叫，不是通用的「隱藏視窗執行任意程式」工具。路徑與三種呼叫是永久不變的契約，
// 0c-update.exe 連續異常時改執行上一版 0c-update.prev.exe（見 internal/updatew）。
package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// plan 檢查參數並回傳個人資料目錄與輸出紀錄檔的位置。exe 是這支程式自己的路徑（<個人資料目錄>\bin\0c-updatew.exe）；
// 要執行的是同一個目錄的 0c-update.exe（或上一版 0c-update.prev.exe，由 updatew.Launch 決定）。
func plan(exe string, args []string) (base, log string, err error) {
	if !filepath.IsAbs(exe) {
		return "", "", fmt.Errorf("需要絕對路徑：%q", exe)
	}
	switch {
	case len(args) == 1 && (args[0] == "auto" || args[0] == "engine"):
	case len(args) == 2 && args[0] == "protocol" && strings.HasPrefix(strings.ToLower(args[1]), "zc-update:"):
	default:
		return "", "", fmt.Errorf("0c-updatew 只由排程與通知按鈕執行（auto、engine、protocol zc-update:…）；其他操作請用 0c-update.exe")
	}
	base = filepath.Dir(filepath.Dir(exe))
	return base, filepath.Join(base, "update", args[0]+"-last.log"), nil
}
