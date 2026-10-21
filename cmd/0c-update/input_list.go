package main

import (
	"os"
	"path/filepath"
	"strings"
)

// inputTip 是靈犀注音在 Windows 語言清單裡的識別：語言（zh-TW）、TSF 的 CLSID 與語言 profile，
// 與 platforms/windows/tsf/src/guids.rs 的 LANGID、CLSID_ZUIN、GUID_PROFILE 相同。
const inputTip = "0404:{138C35D0-7F08-4CEF-BA90-C34C16301528}{EF62F743-9855-4C94-A658-1F0F98B7B840}"

// inputListMarker 記下已替這個帳號把靈犀注音加入過語言清單（#191）。
func inputListMarker(base string) string { return filepath.Join(base, "update", "input-list") }

// listInputMethodOnce 第一次安裝（或從還沒有這一步的舊版更新）時把靈犀注音加入這個帳號的輸入法清單，
// 之後不再加：使用者自己從清單移除的，更新不會再放回去。add 失敗時不留記號，下次安裝或更新再試。
func listInputMethodOnce(base string, add func() error) error {
	marker := inputListMarker(base)
	if _, err := os.Stat(marker); err == nil {
		return nil
	}
	if err := add(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		return err
	}
	return os.WriteFile(marker, []byte(inputTip+"\n"), 0o600)
}

// ownsDesktop 判斷這個程序能不能替它的帳號加入輸入法清單（#191）：InstallLayoutOrTip 寫的是這個程序的 HKCU，記號寫在它的
// LOCALAPPDATA，只有這個程序就是工作階段登入的那位使用者時才加入。在 UAC 對話框輸入另一個系統管理員帳號
// （over-the-shoulder）時，提權的安裝程式是那個管理員：加進去的會是管理員的清單、桌面使用者的清單不變，還可能在桌面使用者的
// 工作階段裡切換鍵盤。這時不加入，等那個帳號自己登入時由它的登入工作（`0c-update engine`）加入。
// process、session 是「網域\帳號」；查不到工作階段的使用者時，系統管理員權限的程序不加入，一般權限的照舊加入（自己的帳號）。
func ownsDesktop(process, session string, err error, elevated bool) bool {
	if err != nil {
		return !elevated
	}
	return process != "" && strings.EqualFold(process, session)
}
