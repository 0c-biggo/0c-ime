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
	if err := listInputMethodOnce(base, listsOwnDesktop, addInputMethod); err != nil {
		fmt.Fprintln(os.Stderr, "0c-update: 未能把靈犀注音加入輸入法清單（可在 Windows 語言設定的「中文（台灣）」新增「靈犀注音」鍵盤）：", err)
	}
}

// listsOwnDesktop：這個程序是不是工作階段登入的那位使用者（見 ownsDesktop）。以另一個帳號提權的安裝程式不加入，只提示
// 這次安裝其實裝在那個帳號。
func listsOwnDesktop() bool {
	elevated := windows.GetCurrentProcessToken().IsElevated()
	var process, session account
	var err error
	process.sid, err = processUserSID()
	if err == nil {
		session, err = sessionAccount()
	}
	if err == nil && session.sid == "" && session.name != "" {
		process.name, err = processAccount() // 工作階段使用者的 SID 查不到時退回比名稱
	}
	if ownsDesktop(process, session, err, elevated) {
		return true
	}
	if elevated && err == nil && session.name != "" {
		if process.name == "" {
			if process.name, err = processAccount(); err != nil {
				process.name = process.sid
			}
		}
		// 這時整套安裝都在那個管理員帳號（見 ownsDesktop），不只是清單。
		fmt.Fprintf(os.Stderr, "0c-update: 系統管理員權限是以另一個帳號（%[1]s）確認的：這次安裝的程式、自動更新與登入啟動都在 %[1]s，"+
			"不在目前桌面使用者（%[2]s），也沒有加入輸入法清單。要給 %[2]s 使用，請以 %[2]s 自己的帳號確認系統管理員權限重新安裝\n",
			process.name, session.name)
	}
	return false
}

// processAccount 是這個程序的帳號名稱（網域\帳號）。
func processAccount() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	name, domain, _, err := user.User.Sid.LookupAccount("")
	if err != nil {
		return "", err
	}
	return domain + `\` + name, nil
}

// accountSID 以帳號名稱（網域\帳號）查 SID；查不到時為空字串。
func accountSID(name string) string {
	sid, _, _, err := windows.LookupSID("", name)
	if err != nil {
		return ""
	}
	return sid.String()
}

// sessionAccount 是這個程序所在的工作階段登入的使用者；沒有登入使用者（例如 session 0 的服務、SSH）時兩者皆空。
// 提權的程序與桌面使用者在同一個工作階段，看到的是桌面使用者，不是確認 UAC 的帳號。工作階段只給名稱
// （取得使用者 token 的 WTSQueryUserToken 要 SYSTEM 權限），SID 由名稱查（LookupAccountName）。
func sessionAccount() (account, error) {
	var session uint32
	if err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &session); err != nil {
		return account{}, err
	}
	query := func(class uintptr) (string, error) {
		var buffer *uint16
		var size uint32
		r, _, err := wtsQuerySessionInformation.Call(0, uintptr(session), class,
			uintptr(unsafe.Pointer(&buffer)), uintptr(unsafe.Pointer(&size)))
		if r == 0 {
			return "", fmt.Errorf("查詢工作階段的使用者：%w", err)
		}
		defer wtsFreeMemory.Call(uintptr(unsafe.Pointer(buffer)))
		return windows.UTF16PtrToString(buffer), nil
	}
	const wtsUserName, wtsDomainName = 5, 7
	user, err := query(wtsUserName)
	if err != nil || user == "" {
		return account{}, err
	}
	domain, err := query(wtsDomainName)
	if err != nil {
		return account{}, err
	}
	name := domain + `\` + user
	return account{sid: accountSID(name), name: name}, nil
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
