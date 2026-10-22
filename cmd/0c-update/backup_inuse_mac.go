//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/0c-biggo/0c-ime/internal/update"
)

// macOS 清理更新備份（#179）前判斷備份是否仍在使用。只讀文字與檔案，放在這裡讓 Linux 上也跑得到單元測試；
// 查詢系統的部分在 backup_inuse_darwin.go。
//
// 備份裡的 App 仍可能被用到：
//   - 有程式從裡面執行：例如系統開到備份裡的舊外殼（#98）、還沒交接完的舊引擎與它的 Python，
//     或正在執行這次安裝的 0c-update 本身。以 lsof 列出這個帳號所有程序的執行檔（每個程序第一個 txt）、
//     載入的程式庫（.dylib、.so、Contents/MacOS、.framework 裡的 txt）與工作目錄（cwd）核對。
//     只是開著或映射著其中某個檔案不算：系統的輸入法選單（TextInputMenuAgent）會一直映射著登入時那個 App 的
//     圖示（txt，VM 實測），算進去的話那份備份要到重新登入後才刪得掉；刪除後已開啟、已映射的檔案照樣讀得到，
//     舊 App 的程式碼也不會再從那裡載入。ps 看不出來：App 被移入備份前就啟動的程序，ps 顯示的仍是啟動時的
//     路徑，lsof 才是目前的位置。
//   - 系統的背景項目（SMAppService，com.biggo.0c.update）登記指向裡面的 App：macOS 15 背景更新後，
//     登記會跟著被移入備份的 App 走，排程執行的是備份裡的 0c-update（#178）。launchctl print 不顯示
//     登記的路徑，但有登記 App 的版本（parent bundle version）；這個版本的備份裡最早的一份當作使用中
//     （同版本重裝留下的其他幾份不算，見 agentBackup）。原因是仍在執行的舊輸入法程式查詢排程時把登記拉到它所在的
//     備份（#205）；0.2.5 起備份裡的排程 plist 會改名（agent_backup_mac.go），登記不再被拉走，這條留著給更早留下的情況。
//   - 查不到（lsof、launchctl 失敗）時一律當作使用中，這次不刪，下次更新再試。

// backupUsage 是清理當下觀察到的使用情形。
type backupUsage struct {
	open    []string // 這個帳號的程序的執行檔、載入的程式庫與工作目錄
	agent   string   // 背景項目登記的 App 的 CFBundleVersion；空字串表示沒有登記
	unknown bool     // 查不到使用情形
}

// lsofPaths 從 `lsof -F fn` 的輸出取出執行中的程式碼與工作目錄（cwd、rtd）的路徑。
// 程式碼是每個程序的第一個 txt（執行檔），以及看得出是程式庫的 txt；其他映射的檔案（圖示、字型等）不算。
func lsofPaths(out string) []string {
	var paths []string
	fd, exe := "", false
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "p"):
			fd, exe = "", false
		case strings.HasPrefix(line, "f"):
			fd = line[1:]
		case strings.HasPrefix(line, "n/"):
			name := line[1:]
			switch {
			case fd == "cwd" || fd == "rtd":
				paths = append(paths, name)
			case fd == "txt" && (!exe || loadedCode(name)):
				paths = append(paths, name)
			}
			if fd == "txt" {
				exe = true
			}
		}
	}
	return paths
}

func loadedCode(name string) bool {
	return strings.HasSuffix(name, ".dylib") || strings.HasSuffix(name, ".so") ||
		strings.Contains(name, "/Contents/MacOS/") || strings.Contains(name, ".framework/")
}

var agentParentVersion = regexp.MustCompile(`(?m)^\s*parent bundle version = (\S+)\s*$`)

// agentBundleVersion 從 `launchctl print gui/<uid>/com.biggo.0c.update` 取出登記這個背景項目的 App 版本；
// 0.1.0-dev.38 以前的 LaunchAgents plist 沒有這一項（執行的是支援目錄裡的穩定副本，不在備份裡）。
func agentBundleVersion(out string) string {
	if m := agentParentVersion.FindStringSubmatch(out); m != nil {
		return m[1]
	}
	return ""
}

// agentNotRegistered：launchctl 回報沒有這個背景項目（沒有登記，或使用者在系統設定關閉後未載入）。
func agentNotRegistered(out string) bool {
	return strings.Contains(out, "Could not find service")
}

var plistBundleVersion = regexp.MustCompile(`<key>\s*CFBundleVersion\s*</key>\s*<string>([^<]*)</string>`)

// appBundleVersion 讀 App 的 CFBundleVersion（XML 格式的 Info.plist）；讀不到時為空字串。
func appBundleVersion(app string) string {
	plist := filepath.Join(app, "Contents", "Info.plist")
	info, err := os.Lstat(plist)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return ""
	}
	b, err := os.ReadFile(plist)
	if err != nil {
		return ""
	}
	if m := plistBundleVersion.FindSubmatch(b); m != nil {
		return strings.TrimSpace(string(m[1]))
	}
	return ""
}

// inUse 回報備份目錄 dir 是否仍在使用；version 讀備份裡 App 的 CFBundleVersion。
func (u backupUsage) inUse(dir string, version func(app string) string) bool {
	if u.unknown {
		return true
	}
	dirs := []string{filepath.Clean(dir)}
	if real, err := filepath.EvalSymlinks(dir); err == nil && real != dirs[0] {
		dirs = append(dirs, real)
	}
	for _, p := range u.open {
		for _, d := range dirs {
			if p == d || strings.HasPrefix(p, d+"/") {
				return true
			}
		}
	}
	if u.agent != "" {
		return filepath.Base(filepath.Clean(dir)) == agentBackup(filepath.Dir(filepath.Clean(dir)), u.agent, version)
	}
	return false
}

// macBackupDir 是安裝備份的名稱（macos-<UTC 時間>-<亂數>，與 internal/update 的格式相同），依名稱排序就是建立的先後。
var macBackupDir = regexp.MustCompile(`^macos-[0-9]{8}-[0-9]{6}-[0-9]+$`)

// agentBackup 是背景項目登記可能指向的那一份備份：App 版本等於登記版本的備份裡最早建立的一份；沒有時為空字串。
// 登記只指向一個 App，同版本重裝（.pkg 修復安裝）留下的其他同版本備份不必保留。取最早的一份：登記跟著 App
// 被移入備份之後，之後安裝時的重新登記不一定生效（#178），登記會停在第一份移入備份的 App；最新的那一份通常是
// 收據的 previous，清理本來就保留。
func agentBackup(backups, agent string, version func(app string) string) string {
	entries, err := os.ReadDir(backups) // 依名稱排序
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() || !macBackupDir.MatchString(e.Name()) {
			continue
		}
		for _, name := range []string{update.MacApp, update.LegacyMacApp} {
			if v := version(filepath.Join(backups, e.Name(), name)); v != "" && v == agent {
				return e.Name()
			}
		}
	}
	return ""
}
