//go:build !windows

package main

import "github.com/0c-biggo/0c-ime/internal/update"

// 換上模式預設啟用的 App 之前，先補上舊版只允許了父項的模式（#348）：只讀文字的判斷放這裡，Linux 上也跑得到單元測試；
// 查詢與啟用在 input_source_legacy_darwin.go。
//
// 0.2.15 起 Info.plist 的模式預設啟用，第一次安裝只問一次「允許」。代價是啟用清單裡只有父項的安裝（0.2.14 以前父項的
// 「允許」晚於 60 秒、模式的詢問被拒絕或沒理）換上新 App 之後，TIS 把模式當成已啟用，再送模式的要求系統照樣問，按了
// 「允許」卻不寫進清單，只能請使用者在系統設定移除再加入（.13 macOS 15.8.2 實測，見 InputSourceLookup.swift）。
// 舊 App 的模式預設未啟用，對它送模式的要求是有效的（#238），所以由新版安裝程式在換掉 App 之前，以舊 App 內附的 0c
// 送出模式的要求（跳過已啟用的父項，問一次）。0.2.14 以前的背景更新器自己安裝、不經過這裡，那條路只能靠新 App 的說明。

const (
	// legacyModeSince：從這一版起 0c --activate-input-source 一次只送一個要求、跳過已啟用的父項（#238）。更早的會同時送出
	// 父項與模式，不能拿來補。
	legacyModeSince = "0.2.7"
	// modeFollowsSince：從這一版起模式預設啟用；這之後的 App 不必（也沒辦法）這樣補。
	modeFollowsSince = "0.2.15-0"
)

// legacyModeMissing：已安裝的舊 App（版本 version）的啟用清單裡有父項、沒有模式，換 App 之前應該先以它補送模式的要求。
// h 是新版 0c --inspect-input-source 對舊 App 的查詢結果（看系統的啟用清單）；讀不到清單（Listed 為 nil）時不補。
func legacyModeMissing(version string, h inputSourceHealth) bool {
	if order, err := update.CompareVersion(version, legacyModeSince); err != nil || order < 0 {
		return false
	}
	if order, err := update.CompareVersion(version, modeFollowsSince); err != nil || order >= 0 {
		return false
	}
	if len(h.Sources) != 2 {
		return false
	}
	parent, mode := h.Sources[0], h.Sources[1]
	return parent.Present && parent.Listed != nil && *parent.Listed && mode.Present && mode.Listed != nil && !*mode.Listed
}
