package main

// #198：macOS 的 .pkg 只裝到使用者自己的家目錄（Distribution 只開 currentUserHome），雙擊安裝時安裝程式以
// 使用者身分執行 postinstall。用 `sudo installer -pkg … -target /`、MDM 或 Homebrew Cask 安裝時，postinstall
// 改以 root 執行，HOME 卻是使用者的（macOS 的 sudoers 保留 HOME）或 /var/root（MDM），0c-update 就以 root
// 在家目錄留下 root 擁有的 App、收據、設定與備份，甚至以 root 啟動引擎；之後使用者自己的更新、設定入口與
// 輸入來源啟用全都失敗。這裡是平台無關、可在 Linux 測試的部分：決定要替哪個帳號做事、解析帳號資料與程序清單、
// 列出靈犀注音自己的路徑。實際降權與修復在 root_darwin.go。

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// macAccount 是帳號資料庫（id -P）裡的一個使用者。
type macAccount struct {
	Name string
	UID  int
	Home string
}

// rootTarget 決定以 root 執行時要替哪個帳號安裝：
//   - 直接 `sudo 0c-update …`：sudo 設的 SUDO_UID 就是下指令的人。
//   - 安裝程式、MDM：postinstall 拿不到 SUDO_*，改用螢幕前登入的使用者（/dev/console 的擁有者）；
//     登入畫面時 /dev/console 屬於 root，表示沒有人登入。
//
// HOME 指向另一個一般帳號的家目錄時（例如透過 ssh 以 A 帳號 sudo，螢幕前登入的卻是 B），不知道要裝給誰，
// 寧可不裝也不裝錯帳號。HOME 屬於 root（MDM 的 /var/root、sudo -H）或讀不到時不列入判斷。
// homeOwner 小於 0 表示 HOME 沒設或讀不到。
func rootTarget(sudoUID string, consoleUID, homeOwner int) (int, error) {
	if sudoUID != "" {
		if uid, err := strconv.Atoi(sudoUID); err == nil && uid > 0 {
			return uid, nil
		}
	}
	if consoleUID <= 0 {
		return 0, errNoConsoleUser
	}
	if homeOwner > 0 && homeOwner != consoleUID {
		return 0, &ambiguousTarget{home: homeOwner, console: consoleUID}
	}
	return consoleUID, nil
}

var errNoConsoleUser = fmt.Errorf("以系統管理員（root）身分執行，但目前沒有登入中的使用者；靈犀注音只裝在使用者自己的帳號，這次沒有安裝，也沒有改動任何家目錄。請在使用者登入後再執行一次，或由使用者自己打開安裝檔")

// ambiguousTarget：名稱由呼叫端查好填入，查不到時以帳號號碼顯示。
type ambiguousTarget struct {
	home, console         int
	homeName, consoleName string
}

func (e *ambiguousTarget) Error() string {
	name := func(n string, uid int) string {
		if n != "" {
			return n
		}
		return fmt.Sprintf("帳號 %d", uid)
	}
	return fmt.Sprintf("以系統管理員（root）身分執行：HOME 是 %s 的家目錄，但螢幕前登入的是 %s。靈犀注音只裝在一個帳號，為了不裝錯，這次沒有安裝，也沒有改動任何家目錄。請在要安裝的帳號雙擊安裝檔，或以那個帳號執行 installer -pkg <安裝檔> -target CurrentUserHomeDirectory（不加 sudo）",
		name(e.homeName, e.home), name(e.consoleName, e.console))
}

// parseAccountRecord 解析 `id -P <使用者>` 的一行（passwd 格式，macOS 有 10 欄：
// name:passwd:uid:gid:class:change:expire:gecos:home:shell）。
func parseAccountRecord(line string) (macAccount, error) {
	f := strings.Split(strings.TrimSpace(line), ":")
	if len(f) != 10 {
		return macAccount{}, fmt.Errorf("無法解析帳號資料：%q", line)
	}
	uid, err := strconv.Atoi(f[2])
	if err != nil || f[0] == "" || !strings.HasPrefix(f[8], "/") {
		return macAccount{}, fmt.Errorf("無法解析帳號資料：%q", line)
	}
	return macAccount{Name: f[0], UID: uid, Home: f[8]}, nil
}

// systemAccount：root 與底線開頭的系統帳號（_mbsetupuser 等）不是可以安裝輸入法的使用者。
func systemAccount(a macAccount) bool {
	return a.UID <= 0 || a.Name == "root" || strings.HasPrefix(a.Name, "_")
}

// enginesUnder 從 `ps -o pid=,comm=` 的輸出挑出執行檔在 prefixes 底下的程序（comm 是完整路徑，可能含空白）。
func enginesUnder(ps string, prefixes []string) []int {
	var pids []int
	for _, line := range strings.Split(ps, "\n") {
		line = strings.TrimSpace(line)
		pid, path, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(pid)
		if err != nil || n <= 1 {
			continue
		}
		path = strings.TrimSpace(path)
		for _, p := range prefixes {
			if strings.HasPrefix(path, p) {
				pids = append(pids, n)
				break
			}
		}
	}
	return pids
}

// installBundleArg：args 是從套件目錄安裝（install，不是 -online）時，回傳套件目錄的絕對路徑
// （-bundle 沒給時是目前目錄，與 run 的預設相同）。
func installBundleArg(args []string) (string, bool) {
	if len(args) == 0 || args[0] != "install" {
		return "", false
	}
	bundle := "."
	for i := 1; i < len(args); i++ {
		name, value, hasValue := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(args[i], "-"), "-"), "=")
		if !strings.HasPrefix(args[i], "-") {
			continue
		}
		switch name {
		case "online":
			if !hasValue || value != "false" {
				return "", false
			}
		case "bundle":
			if hasValue {
				bundle = value
			} else if i+1 < len(args) {
				bundle = args[i+1]
				i++
			}
		}
	}
	abs, err := filepath.Abs(bundle)
	if err != nil {
		return "", false
	}
	return abs, true
}

// withBundleArg 把 install 的 -bundle 換成 path（沒有時加在命令後面第一個）。
func withBundleArg(args []string, path string) []string {
	out := append([]string(nil), args...)
	for i := 1; i < len(out); i++ {
		name, _, hasValue := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(out[i], "-"), "-"), "=")
		if !strings.HasPrefix(out[i], "-") || name != "bundle" {
			continue
		}
		if hasValue {
			out[i] = "-bundle=" + path
		} else if i+1 < len(out) {
			out[i+1] = path
		}
		return out
	}
	return append([]string{out[0], "-bundle", path}, out[1:]...)
}

// macOwnedPaths 是家目錄底下屬於靈犀注音的路徑（相對於家目錄，含 0.1.0-dev.21 以前的 Zuin 名稱）。
// 修復擁有者只動這些路徑，家目錄其他地方一律不碰。
var macOwnedPaths = []string{
	"Library/Input Methods/0c.app",
	"Library/Input Methods/Zuin.app",
	"Library/Application Support/0c",
	"Library/Application Support/Zuin",
	"Library/Caches/0c",
	"Library/Caches/Zuin",
	"Library/LaunchAgents/" + macAgentLabel + ".plist",
}

// macAgentLabel 與 auto_darwin.go 的 agentLabel 相同（這個檔案也在其他平台編譯）。
const macAgentLabel = "com.biggo.0c.update"

// foreignOwnedMessage 說明使用者身分無法修復的情況與要執行的指令。pkg 是安裝程式給的 PACKAGE_PATH，
// 沒有時改用目前這個（已含修復的）0c-update 以 sudo 重跑同一個命令。
func foreignOwnedMessage(count int, sample []string, pkg, self string, args []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "靈犀注音有 %d 個檔案屬於系統管理員（root），不是這個帳號的", count)
	if len(sample) > 0 {
		fmt.Fprintf(&b, "（例如 %s）", sample[0])
	}
	b.WriteString("，通常是之前用 sudo 安裝留下的；這個帳號無法更新或移除它們。請在終端機執行一次下面的指令（會詢問系統管理員密碼）")
	if pkg != "" {
		b.WriteString("，它會把這些檔案還給這個帳號，再以這個帳號重新安裝：\n  ")
		if !strings.HasPrefix(pkg, "<") { // 說明用的佔位字不加引號
			pkg = shellQuote(pkg)
		}
		fmt.Fprintf(&b, "sudo installer -pkg %s -target /", pkg)
	} else {
		b.WriteString("，它會把這些檔案還給這個帳號，再以這個帳號完成這次的動作：\n  ")
		b.WriteString("sudo " + shellQuote(self))
		for _, a := range args {
			b.WriteString(" " + shellQuote(a))
		}
	}
	return b.String()
}

func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r == '/' || r == '-' || r == '.' || r == '_' || r == '=' || r == ':' || r == '@' || r == '+' || r == ',' ||
			(r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
