package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// 舊版本目錄與備份的清理（#179，Windows）。安裝、回復、解除安裝不再同步清理：第一次升級要刪的舊版可達數 GB、
// 上百個目錄，安裝精靈會停在進度條一分多鐘，像是當掉。改成成功之後、0c-update 結束前啟動脫離的背景行程
// （0c-update prune），安裝精靈不必等它；排程每次喚起（auto）再補做一次，背景行程沒做完（關機、被結束）時由它接手。

// pruneFrom 是這次要以哪個 0c-update 在背景清理；空字串表示不需要。安裝、回復、解除安裝成功時設定，main 結束前啟動。
var pruneFrom string

// startPrune 啟動背景清理行程；測試換成不做事（TestMain），不在測試機上啟動真的程式。
var startPrune = spawnPrune

// pruneInstalls 是排程喚起時在同一個行程裡做的清理；測試可替換。
var pruneInstalls = pruneInstallsNow

// launchPrune 在 0c-update 結束前呼叫。啟動失敗只提示，下次排程喚起時再清。
func launchPrune() {
	exe := pruneFrom
	pruneFrom = ""
	if exe == "" {
		return
	}
	if err := startPrune(exe); err != nil {
		fmt.Fprintln(os.Stderr, "0c-update: 背景清理舊版未啟動，下次自動更新檢查時再清：", err)
	}
}

// pruneExecutableFor 選背景清理要執行的 0c-update：自己在安裝目錄裡（穩定副本、版本目錄）就用自己；
// 否則（安裝程式解壓到暫存目錄的那一份）用 fallback，不占住暫存目錄，安裝程式結束時才刪得掉。
func pruneExecutableFor(self, base, fallback string) string {
	if self != "" && base != "" {
		rel, err := filepath.Rel(filepath.Clean(base), filepath.Clean(self))
		if err == nil && rel != "." && !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return self
		}
	}
	return fallback
}
