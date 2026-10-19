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
func TestEngineTaskStartsEngineAtLogon(t *testing.T) {
	stable := `C:\Users\A B\AppData\Local\Zuin\bin\0c-update.exe`
	task := engineTaskXML(`PC\A B`, "S-1-5-21-1", `C:\Windows\System32\conhost.exe`, stable)
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
	if parsed.Exec.Command != `C:\Windows\System32\conhost.exe` || parsed.Exec.Arguments != `--headless "`+stable+`" engine` {
		t.Fatalf("動作不正確：%+v", parsed.Exec)
	}
}

// #120：自動更新與安裝程式叫起引擎時都經過登入工作；系統管理員不直接啟動引擎。
func TestStartResidentEngineGoesThroughLoginTask(t *testing.T) {
	failed := func() error { return os.ErrPermission }
	ok := func() error { return nil }
	for _, c := range []struct {
		name            string
		elevated, task  bool
		run             func() error
		wantRun, wantSp bool
		wantErr         bool
	}{
		{"user with task", false, true, ok, true, false, false},
		{"user task fails", false, true, failed, true, true, false},
		{"user without task", false, false, ok, false, true, false},
		{"admin with task", true, true, ok, true, false, false},
		{"admin task fails", true, true, failed, true, false, true},
		{"admin without task", true, false, ok, false, false, true},
	} {
		ran, spawned := false, false
		err := startResidentEngine(`C:\base`, c.elevated, c.task,
			func() error { ran = true; return c.run() },
			func(string) error { spawned = true; return nil })
		if ran != c.wantRun || spawned != c.wantSp || (err != nil) != c.wantErr {
			t.Errorf("%s：run=%v spawn=%v err=%v", c.name, ran, spawned, err)
		}
	}
}
