package winshell

import (
	"regexp"
	"strconv"
	"strings"
)

// 工作排程器的安全描述元（只用字串處理，各平台都能測試；實際讀寫在 tasks_windows.go）。
//
// 提權的安裝程式建立的排程，擁有者是 Administrators，預設 DACL 只給排程的使用者讀取（.14 實測
// `(A;;FR;;;<使用者>)`），之後一般權限的自動更新改不了也刪不了（E_ACCESSDENIED）。建立時多給使用者本人完全控制：
// 他就能以一般權限換掉舊的排程定義（例如 #289 的 conhost 寫法）、在解除安裝後刪掉留下的排程（#284）。
// 這不會變成提權：一般權限把它改成 RunLevel Highest 不會生效，執行時仍是 Medium（.14 實測）；
// 使用者本來就能以自己的權限建立同樣的排程（工作排程器根資料夾允許 Authenticated Users 建立）。

// TaskOpenSDDL 是提權建立排程時傳給 RegisterTask 的 DACL：只寫一筆「本人完全控制」，繼承自根資料夾的
// Administrators／SYSTEM 與工作排程器自己加的「使用者讀取」照舊（.14 實測結果為預設 DACL 再多這一筆）。
func TaskOpenSDDL(sid string) string { return "D:(A;;FA;;;" + sid + ")" }

var sddlACE = regexp.MustCompile(`\(([^;()]*);([^;()]*);([^;()]*);([^;()]*);([^;()]*);([^;()]*)\)`)

// fileAllAccess 是 FILE_ALL_ACCESS（SDDL 的 FA）。
const fileAllAccess = 0x1f01ff

// TaskWritableBy 回報 sddl 的 DACL 有沒有允許 sid 完全控制（FA、GA 或等值的存取遮罩），也就是本人能不能以一般權限
// 改寫、刪除這個排程。只看明確給這個 SID 的允許項目（使用者自己建立的排程經繼承也有這一筆）。
func TaskWritableBy(sddl, sid string) bool {
	if sid == "" {
		return false
	}
	dacl := sddl
	if i := strings.Index(dacl, "D:"); i >= 0 {
		dacl = dacl[i+2:]
		if j := strings.Index(dacl, "S:"); j >= 0 {
			dacl = dacl[:j]
		}
	} else {
		return false
	}
	for _, m := range sddlACE.FindAllStringSubmatch(dacl, -1) {
		if m[1] != "A" || !strings.EqualFold(m[6], sid) || strings.Contains(m[2], "IO") {
			continue
		}
		switch rights := strings.ToUpper(m[3]); {
		case rights == "FA" || rights == "GA":
			return true
		case strings.HasPrefix(rights, "0X"):
			if v, err := strconv.ParseUint(rights[2:], 16, 32); err == nil && v&fileAllAccess == fileAllAccess {
				return true
			}
		}
	}
	return false
}
