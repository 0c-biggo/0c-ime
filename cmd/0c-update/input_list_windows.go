//go:build windows

package main

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procInstallLayoutOrTip  = windows.NewLazySystemDLL("input.dll").NewProc("InstallLayoutOrTip")
	wtsEnumerateProcessesEx = wtsapi32.NewProc("WTSEnumerateProcessesExW")
	wtsFreeMemoryEx         = wtsapi32.NewProc("WTSFreeMemoryExW")
)

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
// 這次安裝其實裝在那個帳號。一般權限的程序（登入工作、每日檢查）就是它自己的帳號，不查身分。
func listsOwnDesktop() bool {
	elevated := windows.GetCurrentProcessToken().IsElevated()
	if !elevated {
		return true
	}
	process, err := processUser()
	var session account
	if err == nil {
		session, err = sessionAccount()
	}
	if ownsDesktop(process, session, err, elevated) {
		return true
	}
	if elevated && err == nil && session.name != "" {
		who := process.name
		if who == "" {
			who = process.sid
		}
		// 這時整套安裝都在那個管理員帳號（見 ownsDesktop），不只是清單。
		fmt.Fprintf(os.Stderr, "0c-update: 系統管理員權限是以另一個帳號（%[1]s）確認的：這次安裝的程式、自動更新與登入啟動都在 %[1]s，"+
			"不在目前桌面使用者（%[2]s），也沒有加入輸入法清單。要給 %[2]s 使用，請以 %[2]s 自己的帳號確認系統管理員權限重新安裝\n",
			who, session.name)
	}
	return false
}

// processUser 是這個程序 token 上的使用者：SID 與帳號名稱（網域\帳號），token 只查一次；名稱查不到時留空字串。
func processUser() (account, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return account{}, err
	}
	a := account{sid: user.User.Sid.String()}
	if name, domain, _, err := user.User.Sid.LookupAccount(""); err == nil {
		a.name = domain + `\` + name
	}
	return a, nil
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
// （取得使用者 token 的 WTSQueryUserToken 要 SYSTEM 權限）；SID 取自桌面 shell（explorer.exe）的擁有者（shellUserSID），
// 不靠名稱。沒有 shell 時（剛登入 explorer 還沒起來、explorer 當掉）才以名稱查（LookupAccountName）：Azure AD 與
// Microsoft 帳號的工作階段名稱可能查不到或查到別的寫法。
func sessionAccount() (account, error) {
	session, name, err := sessionUser()
	if err != nil || name == "" {
		return account{}, err
	}
	sid := shellUserSID(session)
	if sid == "" {
		sid = accountSID(name)
	}
	return account{sid: sid, name: name}, nil
}

// sessionUser 是這個程序所在的工作階段與它登入的使用者名稱（網域\帳號）；沒有登入使用者時名稱為空字串。
func sessionUser() (uint32, string, error) {
	var session uint32
	if err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &session); err != nil {
		return 0, "", err
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
		return session, "", err
	}
	domain, err := query(wtsDomainName)
	if err != nil {
		return session, "", err
	}
	return session, domain + `\` + user, nil
}

// shellUserSID 是這個工作階段的桌面 shell（GetShellWindow 的擁有者，通常是 explorer.exe）以哪個帳號執行：登入桌面的使用者。
// 提權的程序與 shell 在同一個桌面，GetShellWindow 拿得到；沒有 shell（session 0、explorer 沒在跑）時為空字串。
func shellUserSID(session uint32) string {
	shell := windows.GetShellWindow()
	if shell == 0 {
		return ""
	}
	var pid uint32
	if _, err := windows.GetWindowThreadProcessId(shell, &pid); err != nil || pid == 0 {
		return ""
	}
	return sessionProcessSID(session, pid)
}

// wtsProcessInfo 是 WTS_PROCESS_INFOW（WTSEnumerateProcessesEx 的 level 0）。
type wtsProcessInfo struct {
	SessionID, ProcessID uint32
	ProcessName          *uint16
	UserSID              *windows.SID
}

// sessionProcessSID 是工作階段 session 裡程序 pid 的擁有者 SID，查不到時為空字串。用 WTSEnumerateProcessesEx（由系統查，
// 系統管理員群組的成員看得到別的帳號的程序），不自己 OpenProcessToken：另一個帳號的 explorer.exe 開不開得了要看它的程序與
// token 的 DACL，不必依賴。
func sessionProcessSID(session, pid uint32) string {
	level := uint32(0)
	var buffer *wtsProcessInfo
	var count uint32
	r, _, _ := wtsEnumerateProcessesEx.Call(0, uintptr(unsafe.Pointer(&level)), uintptr(session),
		uintptr(unsafe.Pointer(&buffer)), uintptr(unsafe.Pointer(&count)))
	if r == 0 || buffer == nil {
		return ""
	}
	const wtsTypeProcessInfoLevel0 = 0
	defer wtsFreeMemoryEx.Call(wtsTypeProcessInfoLevel0, uintptr(unsafe.Pointer(buffer)), uintptr(count))
	for _, p := range unsafe.Slice(buffer, count) {
		if p.ProcessID == pid && p.SessionID == session && p.UserSID != nil && p.UserSID.IsValid() {
			return p.UserSID.String()
		}
	}
	return ""
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
