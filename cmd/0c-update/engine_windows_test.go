//go:build windows

package main

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// 沙箱應用程式（Windows 搜尋框等）要讀得到 DLL；原本繼承的權限（本人等）必須保留。
func TestGrantAppContainerReadKeepsInheritedAccess(t *testing.T) {
	dll := filepath.Join(t.TempDir(), "0c_tsf.dll")
	if err := os.WriteFile(dll, []byte("dll"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := fileSDDL(t, dll)
	if err := grantAppContainerRead(dll); err != nil {
		t.Fatal(err)
	}
	// 重複授權（再次註冊同一版）不累加。
	if err := grantAppContainerRead(dll); err != nil {
		t.Fatal(err)
	}
	after := fileSDDL(t, dll)
	for _, sid := range []string{"AC", "S-1-15-2-2"} {
		aces := regexp.MustCompile(`\(A;;([^;]*);;;`+regexp.QuoteMeta(sid)+`\)`).FindAllStringSubmatch(after, -1)
		if len(aces) != 1 || (aces[0][1] != "0x1200a9" && aces[0][1] != "FRFX") {
			t.Fatalf("%s 的讀取／執行 ACE 不正確：%s", sid, after)
		}
	}
	token := windows.GetCurrentProcessToken()
	u, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if me := u.User.Sid.String(); strings.Contains(before, me) && !strings.Contains(after, me) {
		t.Fatalf("本人的權限不見了：\n前 %s\n後 %s", before, after)
	}
	if b, err := os.ReadFile(dll); err != nil || string(b) != "dll" {
		t.Fatalf("授權後讀不到檔案：%v", err)
	}
}

func fileSDDL(t *testing.T, file string) string {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(file, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	return sd.String()
}

// 登入時立即（不延遲）以穩定副本執行 engine；優先順序一般，路徑含空白時正確引號。
// 啟動器 bin\0c-updatew.exe 在時直接執行它（#289）；還沒有時退回 conhost --headless。
func TestEngineTaskStartsEngineAtLogon(t *testing.T) {
	base := filepath.Join(t.TempDir(), "A B", "Zuin")
	writeFile(t, stableUpdater(base), "updater")
	task := engineTaskXML(`PC\A B`, "S-1-5-21-1", base)
	var parsed struct {
		Triggers struct {
			Logon []struct {
				UserID string `xml:"UserId"`
				Delay  string `xml:"Delay"`
			} `xml:"LogonTrigger"`
			Time []struct{} `xml:"TimeTrigger"`
		} `xml:"Triggers"`
		Settings struct {
			Priority int    `xml:"Priority"`
			Multiple string `xml:"MultipleInstancesPolicy"`
		} `xml:"Settings"`
		Exec struct {
			Command   string `xml:"Command"`
			Arguments string `xml:"Arguments"`
		} `xml:"Actions>Exec"`
	}
	// encoding/xml 不認 UTF-16 宣告；schtasks 讀到的是同一份內容的 UTF-16 編碼。
	body := strings.Replace(task, `encoding="UTF-16"`, `encoding="UTF-8"`, 1)
	if err := xml.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Triggers.Logon) != 1 || parsed.Triggers.Logon[0].UserID != `PC\A B` || parsed.Triggers.Logon[0].Delay != "" || len(parsed.Triggers.Time) != 0 {
		t.Fatalf("登入觸發不正確：%+v", parsed.Triggers)
	}
	if parsed.Settings.Priority != 4 || parsed.Settings.Multiple != "IgnoreNew" {
		t.Fatalf("排程設定不正確：%+v", parsed.Settings)
	}
	conhost := filepath.Join(os.Getenv("SystemRoot"), `System32\conhost.exe`)
	if parsed.Exec.Command != conhost || parsed.Exec.Arguments != `--headless "`+stableUpdater(base)+`" engine` {
		t.Fatalf("沒有啟動器時的動作不正確：%+v", parsed.Exec)
	}
	writeFile(t, stableLauncher(base), "launcher")
	command, args := taskExec(t, engineTaskXML(`PC\A B`, "S-1-5-21-1", base))
	if command != stableLauncher(base) || args != "engine" {
		t.Fatalf("有啟動器時應直接執行它：%q %q", command, args)
	}
}

// taskExec 取出排程定義的動作（命令與參數）。
func taskExec(t *testing.T, task string) (string, string) {
	t.Helper()
	var parsed struct {
		Exec struct {
			Command   string `xml:"Command"`
			Arguments string `xml:"Arguments"`
		} `xml:"Actions>Exec"`
	}
	if err := xml.Unmarshal([]byte(strings.Replace(task, `encoding="UTF-16"`, `encoding="UTF-8"`, 1)), &parsed); err != nil {
		t.Fatal(err)
	}
	return parsed.Exec.Command, parsed.Exec.Arguments
}

// #289：自動更新排程同樣經啟動器執行 auto，不經 conhost --headless；沒有啟動器時退回舊樣式，排程不會失效。
func TestUpdateTaskRunsTheLauncher(t *testing.T) {
	base := filepath.Join(t.TempDir(), "Zuin")
	writeFile(t, stableUpdater(base), "updater")
	command, args := taskExec(t, updateTaskXML(`PC\A`, "S-1-5-21-1", base))
	if !strings.EqualFold(filepath.Base(command), "conhost.exe") || args != "--headless "+stableUpdater(base)+" auto" {
		t.Fatalf("沒有啟動器：%q %q", command, args)
	}
	writeFile(t, stableLauncher(base), "launcher")
	command, args = taskExec(t, updateTaskXML(`PC\A`, "S-1-5-21-1", base))
	if command != stableLauncher(base) || args != "auto" {
		t.Fatalf("有啟動器：%q %q", command, args)
	}
	if strings.Contains(updateTaskXML(`PC\A`, "S-1-5-21-1", base), "conhost") {
		t.Fatal("有啟動器時排程定義不得出現 conhost")
	}
}
