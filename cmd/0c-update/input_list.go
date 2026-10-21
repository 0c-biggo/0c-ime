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
// 先看記號再查身分（owns，見 ownsDesktop）：登入工作與每日檢查每次都會走到這裡，已加入過的帳號不必查 token 與帳號名稱
// （網域電腦連不到網域控制站時查帳號可能要等很久，會拖慢引擎啟動）。owns 為 false 時不加入、不留記號。
func listInputMethodOnce(base string, owns func() bool, add func() error) error {
	marker := inputListMarker(base)
	if _, err := os.Stat(marker); err == nil {
		return nil
	}
	if !owns() {
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

// account 是一個 Windows 帳號：SID 與「網域\帳號」名稱，查不到的留空字串。
type account struct{ sid, name string }

// ownsDesktop 判斷這個程序能不能替它的帳號加入輸入法清單（#191）：InstallLayoutOrTip 寫的是這個程序的 HKCU，記號寫在它的
// LOCALAPPDATA，只有這個程序就是工作階段登入的那位使用者時才加入。在 UAC 對話框輸入另一個系統管理員帳號
// （over-the-shoulder）時，提權的安裝程式是那個管理員：加進去的會是管理員的清單、桌面使用者的清單不變，還可能在桌面使用者的
// 工作階段裡切換鍵盤。這時不加入，等那個帳號自己登入時由它的登入工作（`0c-update engine`）加入。
// 以 SID 比對：Azure AD 與 Microsoft 帳號的電腦上，token 查到的名稱與工作階段的使用者名稱寫法可能不同。工作階段使用者的
// SID 查不到時才退回比名稱。session 兩者皆空是沒有登入使用者（服務、SSH），不加入；查不到工作階段（err）時，
// 系統管理員權限的程序不加入，一般權限的照舊加入（自己的帳號）。
func ownsDesktop(process, session account, err error, elevated bool) bool {
	if err != nil {
		return !elevated
	}
	if process.sid != "" && session.sid != "" {
		return strings.EqualFold(process.sid, session.sid)
	}
	return session.name != "" && strings.EqualFold(process.name, session.name)
}
