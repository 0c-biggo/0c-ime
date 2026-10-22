package main

import (
	"errors"
	"fmt"
	"strings"
)

// errOtherAccount：安裝會裝到另一個帳號，沒有安裝（#199）。0c-update 以 otherAccountExit 結束，安裝程式
// （platforms/windows/installer/setup.nsi）靠這個結束碼分辨「要擋下並說明」與「檢查本身沒跑成」。
var errOtherAccount = errors.New("系統管理員權限由另一個帳號確認")

// otherAccountExit 是 errOtherAccount 的結束碼；其他錯誤都是 1。
const otherAccountExit = 3

// installsForOtherAccount 判斷這次安裝是不是其實會裝在另一個帳號（#199）。Windows 的安裝把程式、收據、自動更新、
// 登入工作與「已安裝的應用程式」都放在執行安裝的帳號（%LOCALAPPDATA%\Zuin、HKCU、以 SID 命名的排程）。在系統管理員
// 權限的確認視窗輸入另一個系統管理員帳號（over-the-shoulder）時，提權的安裝程式就是那個帳號：靈犀注音裝給它，
// 桌面使用者沒有登入工作，之後的更新也補不上。
//
// desktop 是登入桌面的使用者，SID 只取自桌面 shell 的擁有者（見 desktopAccount），不由名稱查：只有確定是另一個帳號才擋，
// 沒有桌面 shell（session 0、SSH、部署工具以 SYSTEM 靜默安裝）或查不到 SID 時照舊安裝。一般權限的程序裝不了
// （preflight 要求系統管理員權限），也不在這裡擋。
func installsForOtherAccount(process, desktop account, elevated bool) bool {
	if !elevated || process.sid == "" || desktop.sid == "" {
		return false
	}
	return !strings.EqualFold(process.sid, desktop.sid)
}

// otherAccountError 是擋下安裝時給使用者看的說明（安裝程式直接放進對話框）：白話、不提 UAC、SID 之類的詞。
type otherAccountError struct{ admin, desktop account }

func (e otherAccountError) Unwrap() error { return errOtherAccount }

func (e otherAccountError) Error() string {
	admin, desktop := displayNames(e.admin.name, e.desktop.name)
	who := "另一個帳號"
	if admin != "" {
		who = "另一個帳號「" + admin + "」"
	}
	current, you := "目前登入的帳號", "目前登入的帳號"
	if desktop != "" {
		current, you = "目前登入的「"+desktop+"」", "「"+desktop+"」"
	}
	return fmt.Sprintf("沒有安裝：這次確認系統管理員權限的是%s，不是%s。\n\n"+
		"靈犀注音會裝給確認權限的那個帳號使用，照這樣裝下去，%s用不到。\n\n"+
		"請重新執行安裝程式，並用%s自己的帳號確認系統管理員權限。如果%s不是系統管理員，請先請這台電腦的管理者把它設為系統管理員，再重新安裝。",
		who, current, you, you, you)
}

// displayNames：本機與網域帳號只寫帳號名稱（「網域\帳號」的網域對一般人沒有意義）；兩邊帳號名稱相同、只差網域時保留網域，
// 否則兩個帳號看起來是同一個。
func displayNames(a, b string) (string, string) {
	short := func(name string) string {
		if i := strings.LastIndex(name, `\`); i >= 0 {
			return name[i+1:]
		}
		return name
	}
	sa, sb := short(a), short(b)
	if sa != "" && strings.EqualFold(sa, sb) {
		return a, b
	}
	return sa, sb
}
