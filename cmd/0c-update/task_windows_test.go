//go:build windows

package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/internal/winshell"
	"golang.org/x/sys/windows/registry"
)

// 協定與通知的註冊只在內容不同時寫入（#289 的後續）：第二次呼叫不寫任何值，改了一個值只寫那一個。
// 用 HKCU 底下的暫時機碼，不碰真的 zc-update: 協定。
func TestSetRegistryStringsWritesOnlyChanges(t *testing.T) {
	root := fmt.Sprintf(`Software\0c-test-%d-%d`, os.Getpid(), time.Now().UnixNano())
	t.Cleanup(func() {
		for _, k := range []string{`\shell\open\command`, `\shell\open`, `\shell`, ``} {
			registry.DeleteKey(registry.CURRENT_USER, root+k)
		}
	})
	values := protocolValues(`C:\Users\A B\AppData\Local\Zuin\bin\0c-updatew.exe`, "protocol")
	if n, err := setRegistryStrings(root, values); err != nil || n != 3 {
		t.Fatalf("第一次應寫入 3 個值：%d %v", n, err)
	}
	if n, err := setRegistryStrings(root, values); err != nil || n != 0 {
		t.Fatalf("內容相同時不應寫入：%d %v", n, err)
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, root+`\shell\open\command`, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	command, _, err := key.GetStringValue("")
	key.Close()
	if err != nil || command != `"C:\Users\A B\AppData\Local\Zuin\bin\0c-updatew.exe" protocol "%1"` {
		t.Fatalf("協定命令：%q %v", command, err)
	}
	// 舊樣式（conhost --headless）換成啟動器：只寫命令那一個值。
	old := protocolValues(`C:\Windows\System32\conhost.exe`, `--headless "C:\x\0c-update.exe" protocol`)
	if n, err := setRegistryStrings(root, old); err != nil || n != 1 {
		t.Fatalf("只有命令不同時應只寫 1 個值：%d %v", n, err)
	}
	if n, err := setRegistryStrings(root, values); err != nil || n != 1 {
		t.Fatalf("換回時應只寫 1 個值：%d %v", n, err)
	}
}

// 以真的工作排程器（行程內 COM）建立、比對、改寫與刪除一個暫時的排程。會在根資料夾留下名為 0c-test-* 的工作直到
// 測試結束，所以只在設了 ZC_TASKSCHD_TEST=1 時執行（驗收機上手動跑）。
func TestEnsureTaskWithTaskScheduler(t *testing.T) {
	if os.Getenv("ZC_TASKSCHD_TEST") != "1" {
		t.Skip("設 ZC_TASKSCHD_TEST=1 才會在工作排程器建立測試排程")
	}
	sid := currentSID()
	name := fmt.Sprintf("0c-test-%d", os.Getpid())
	t.Cleanup(func() { winshell.DeleteTask(name) })
	def := func(args string) string {
		return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo><Description>靈犀注音測試</Description></RegistrationInfo>
  <Triggers><TimeTrigger><Enabled>false</Enabled><StartBoundary>2026-01-01T00:00:00</StartBoundary></TimeTrigger></Triggers>
  <Principals><Principal id="Author"><UserId>%s</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals>
  <Settings><MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy><ExecutionTimeLimit>PT1M</ExecutionTimeLimit></Settings>
  <Actions Context="Author"><Exec><Command>cmd.exe</Command><Arguments>%s</Arguments></Exec></Actions>
</Task>
`, sid, xmlEscape(args))
	}
	elevated := elevatedOK()
	if elevated {
		// 0.2.11 以前的安裝程式建立的排程：沒有標記、預設 DACL（本人只能讀）。RegisterTask 取代既有工作時不套用 sddl，
		// 要另外設定安全描述元才開放得了。
		if err := winshell.RegisterTask(name, def("/c exit 2"), ""); err != nil {
			t.Fatal(err)
		}
		if task, err := winshell.GetTask(name); err != nil || winshell.TaskWritableBy(task.SDDL, sid) {
			t.Fatalf("預設 DACL 應只給本人讀取：%v %s", err, task.SDDL)
		}
	}
	if changed, err := ensureTask(name, def("/c exit 0"), sid, elevated); err != nil || !changed {
		t.Fatalf("第一次應建立：%v %v", changed, err)
	}
	task, err := winshell.GetTask(name)
	if err != nil {
		t.Fatal(err)
	}
	_, want := stampTaskDefinition(def("/c exit 0"))
	if got := taskDefinitionStamp(task.XML); got != want {
		t.Fatalf("讀回的定義沒有標記 %q：%s", want, task.XML)
	}
	if !winshell.TaskWritableBy(task.SDDL, sid) {
		t.Fatalf("建立後本人應有完全控制（提權=%v）：%s", elevated, task.SDDL)
	}
	t.Logf("提權=%v SDDL=%s", elevated, task.SDDL)
	if changed, err := ensureTask(name, def("/c exit 0"), sid, elevated); err != nil || changed {
		t.Fatalf("定義相同不應重寫：%v %v", changed, err)
	}
	if changed, err := ensureTask(name, def("/c exit 1"), sid, elevated); err != nil || !changed {
		t.Fatalf("定義不同應改寫：%v %v", changed, err)
	}
	if task, err = winshell.GetTask(name); err != nil || !strings.Contains(task.XML, "/c exit 1") || !winshell.TaskWritableBy(task.SDDL, sid) {
		t.Fatalf("改寫後的定義或權限不正確：%v %s %s", err, task.XML, task.SDDL)
	}
	if removed, err := deleteTask(name, sid); err != nil || !removed {
		t.Fatalf("應刪除：%v %v", removed, err)
	}
	if winshell.TaskExists(name) {
		t.Fatal("刪除後仍存在")
	}
	if removed, err := deleteTask(name, sid); err != nil || removed {
		t.Fatalf("不存在時不算失敗、也不算刪除：%v %v", removed, err)
	}
	if err := winshell.RunTask(name); err == nil {
		t.Fatal("不存在的排程不應執行成功")
	}
}
