//go:build windows

package main

import "golang.org/x/sys/windows"

// refuseOtherAccount：提權的安裝其實會裝在另一個帳號時（見 installsForOtherAccount）回傳 otherAccountError，否則 nil。
// 查不到身分時不擋（照舊安裝）。安裝程式在開始安裝前以 `check-account` 呼叫；apply 與 install 安裝前也再查一次，
// 涵蓋 install.ps1 與直接執行。背景更新不經過這裡：它以使用者自己的帳號執行，要提權時由 finish 核對 UAC 前後是同一個 SID
// （validateElevationOrigin）。
func refuseOtherAccount() error {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return nil
	}
	process, err := processUser()
	if err != nil {
		return nil
	}
	desktop := desktopAccount()
	if !installsForOtherAccount(process, desktop, true) {
		return nil
	}
	return otherAccountError{admin: process, desktop: desktop}
}

// desktopAccount 是登入這個桌面的使用者：SID 取自桌面 shell 的擁有者，名稱取自工作階段。沒有桌面 shell 時 SID 為空字串，
// 不像 sessionAccount 退回以名稱查：擋下安裝只憑確定的證據。
func desktopAccount() account {
	session, name, err := sessionUser()
	if err != nil {
		return account{}
	}
	return account{sid: shellUserSID(session), name: name}
}
