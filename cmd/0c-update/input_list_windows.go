//go:build windows

package main

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procInstallLayoutOrTip = windows.NewLazySystemDLL("input.dll").NewProc("InstallLayoutOrTip")

// listInputMethod 把靈犀注音加入這個帳號的語言清單（#191）。安裝只在 HKLM 註冊 TSF profile，Windows 不會自動把新的
// 輸入法加進使用者的清單：「進階鍵盤設定 → 覆寫預設輸入法」只列清單裡的輸入法，Win＋空白也切換不到。
// 每個帳號只加一次（見 listInputMethodOnce）；失敗只提示，不影響安裝結果。
//
// 安裝後（afterInstall）做；已安裝的使用者由背景更新換到新版時，執行安裝的是舊版的穩定更新器，它的 afterInstall 沒有這一步，
// 所以新版的登入工作（`0c-update engine`）與候選窗程式的每日檢查（`schedule repair`）也各補一次。沒有安裝時不做。
func listInputMethod(base string) {
	if version, err := installedVersion(); err != nil || version == "" {
		return
	}
	if err := listInputMethodOnce(base, addInputMethod); err != nil {
		fmt.Fprintln(os.Stderr, "0c-update: 未能把靈犀注音加入輸入法清單（可在 Windows 語言設定的「中文（台灣）」新增「靈犀注音」鍵盤）：", err)
	}
}

// addInputMethod 以 input.dll 的 InstallLayoutOrTip 把靈犀注音加入目前帳號的語言清單並啟用（小狼毫的安裝程式也這樣做）。
// 旗標 0：不帶 ILOT_DEFPROFILE，不改預設輸入法，排在「中文（台灣）」原有的鍵盤後面；清單沒有中文（台灣）時一併加入這個語言，
// 原有的語言與顯示語言不變。已在清單裡時不變動。
func addInputMethod() error {
	if err := procInstallLayoutOrTip.Find(); err != nil {
		return err
	}
	tip, err := windows.UTF16PtrFromString(inputTip)
	if err != nil {
		return err
	}
	ok, _, callErr := procInstallLayoutOrTip.Call(uintptr(unsafe.Pointer(tip)), 0)
	if ok == 0 {
		return fmt.Errorf("InstallLayoutOrTip 失敗：%v", callErr)
	}
	return nil
}
