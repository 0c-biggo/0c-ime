package main

import (
	"regexp"
	"strconv"
)

// macOS 的「安全輸入」（EnableSecureEventInput）開著時，系統停用第三方輸入法：靈犀在輸入法選單變灰、選不到，
// 打字暫時改用最近用過的系統輸入法（ABC 打出英文，或 Apple 注音）；持有的程式關掉後自動回到靈犀（#266）。
// 喚醒後密碼管理器、瀏覽器的密碼欄或終端機的「安全鍵盤輸入」沒放掉，就是使用者說的「醒來打不出中文」。
// 系統把它記在 IORegistry 根節點 IOConsoleUsers 的 kCGSSessionSecureInputPID；這個 PID 是開啟當下的
// 前景 App，不一定是真正呼叫的程式，所以狀態摘要寫成 foreground_at_enable、只當線索。
var secureInputPIDPattern = regexp.MustCompile(`"kCGSSessionSecureInputPID"=(\d+)`)

// secureInputPIDs 從 `ioreg -l -d 1 -w 0 -k IOConsoleUsers` 的輸出取出開啟安全輸入當下的前景 App PID（去重、照出現順序）。
func secureInputPIDs(ioreg string) []int {
	var pids []int
	seen := map[int]bool{}
	for _, m := range secureInputPIDPattern.FindAllStringSubmatch(ioreg, -1) {
		pid, err := strconv.Atoi(m[1])
		if err != nil || pid <= 0 || seen[pid] {
			continue
		}
		seen[pid] = true
		pids = append(pids, pid)
	}
	return pids
}
