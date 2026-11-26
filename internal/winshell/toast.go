// Package winshell 在行程內以 COM／WinRT 建立開始功能表捷徑（IShellLinkW）與送出 Windows 通知
// （ToastNotificationManager），取代先前的 powershell.exe -EncodedCommand（#289）。
//
// 「隱藏視窗的 PowerShell 執行 Base64 編碼的命令」與「conhost --headless 帶起另一個程式」一樣，是端點防護常見的
// 偵測規則（例如 Sigma 的 Suspicious Encoded PowerShell Command Line）；自動更新每 15 分鐘就可能送一次通知，
// 留著它，裝了這類防護的公司電腦一樣可能擋掉或告警。直接呼叫系統的 COM 介面不必再開任何子行程。
//
// 通知內容的組裝（ToastXML）在各平台都能測試；實際呼叫只在 Windows。
package winshell

import (
	"encoding/xml"
	"strings"
)

// Toast 是一則通知：標題、內文，以及選用的「立即安裝」「稍後 1 小時」按鈕（以 protocol 啟動的網址）。
type Toast struct {
	Title, Body string
	Action      string // 「立即安裝」的網址；空字串表示沒有按鈕
	ActionLabel string // Action 按鈕的文字；空字串為「立即安裝」（手動更新的新版是「立即下載」，#340）
	Snooze      string // 「稍後 1 小時」的網址
}

func esc(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s)) // 寫入 strings.Builder 不會失敗；引號也會跳脫，可放進屬性值
	return b.String()
}

// ToastXML 是 ToastGeneric 範本的通知內容，與先前 PowerShell 以 DOM 組出的相同：有按鈕時加 scenario="reminder"
// （通知停留到使用者處理）與兩個 activationType="protocol" 的按鈕。
func ToastXML(t Toast) string {
	var b strings.Builder
	b.WriteString(`<toast`)
	if t.Action != "" {
		b.WriteString(` scenario="reminder"`)
	}
	b.WriteString(`><visual><binding template="ToastGeneric"><text>` + esc(t.Title) + `</text><text>` + esc(t.Body) + `</text></binding></visual>`)
	if t.Action != "" {
		b.WriteString(`<actions>`)
		label := t.ActionLabel
		if label == "" {
			label = "立即安裝"
		}
		b.WriteString(`<action content="` + esc(label) + `" activationType="protocol" arguments="` + esc(t.Action) + `"/>`)
		b.WriteString(`<action content="稍後 1 小時" activationType="protocol" arguments="` + esc(t.Snooze) + `"/>`)
		b.WriteString(`</actions>`)
	}
	b.WriteString(`</toast>`)
	return b.String()
}
