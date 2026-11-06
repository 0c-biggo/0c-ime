package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// tsf.log 的樣本（格式見 platforms/windows/tsf/src/log.rs）：毫秒時間、pid、訊息。
func tsfSample(now time.Time) string {
	at := func(ago time.Duration) int64 { return now.Add(-ago).UnixMilli() }
	lines := []string{
		// 8 天前：超出統計範圍。
		fmt.Sprintf("%d pid=100 activate 開始 exe=old.exe tid=1", at(8*24*time.Hour)),
		// AutoCAD（IMM32 程式）：啟用、文件種類、私人組字、沒有插入點、候選窗退路、組字被終止。
		fmt.Sprintf("%d pid=200 activate flags=0x0 comless=false", at(3*time.Hour)),
		fmt.Sprintf("%d pid=200 activate 開始 exe=acad.exe tid=11", at(3*time.Hour)),
		fmt.Sprintf("%d pid=200 activate 完成 exe=acad.exe tid=11 ui_less=false", at(3*time.Hour)),
		fmt.Sprintf("%d pid=200 文件：IMM32 程式經系統相容層 status=0x4 focus=AfxWnd140u field=Private exe=acad.exe", at(3*time.Hour)),
		fmt.Sprintf("%d pid=200 欄位政策：讀不到 InputScope，以私人方式組字 exe=acad.exe scope=HRESULT(0x80004005) ui_less=false status=0x4/0x0 focus=AfxWnd140u view=AfxWnd140u same=true field=Private", at(3*time.Hour)),
		fmt.Sprintf("%d pid=200 debug OnTestKeyDown vk=83 -> true", at(3*time.Hour)),
		fmt.Sprintf("%d pid=200 debug type_literal vk=85", at(3*time.Hour)),
		fmt.Sprintf("%d pid=200 OnKeyDown vk=51 handled=true", at(3*time.Hour)), // 沒有 debug 前綴但帶鍵碼：一樣排除
		// 除錯紀錄即使長得像判定也不讀。
		fmt.Sprintf("%d pid=200 debug 欄位政策：讀不到 InputScope vk=65 exe=leak.exe", at(3*time.Hour)),
		fmt.Sprintf("%d pid=200 候選窗：vk=66 exe=leak2.exe", at(3*time.Hour)),
		fmt.Sprintf("%d pid=200 英文模式：IMM32 程式的焦點沒有插入點，不開始英文補字，按鍵直接交給程式 exe=acad.exe", at(3*time.Hour)),
		fmt.Sprintf("%d pid=200 候選窗：沒有版面，改用系統插入點 (10,20,30,40)（view 0x1a2b） exe=acad.exe", at(3*time.Hour)),
		fmt.Sprintf("%d pid=200 組字被宿主終止 exe=acad.exe", at(2*time.Hour)),
		fmt.Sprintf("%d pid=200 提交 SetText 失敗：0x80040205，讀回內容不符，未寫入", at(2*time.Hour)),
		fmt.Sprintf("%d pid=200 spawn: 沒有 C:\\Users\\王小明\\AppData\\Local\\Zuin\\versions\\0.2.8\\0c-server.exe", at(2*time.Hour)),
		fmt.Sprintf("%d pid=200 handle: invalid character 'x' looking for value", at(2*time.Hour)),
		fmt.Sprintf("%d pid=200 panic 位置：C:\\Users\\builder\\.cargo\\registry\\src\\windows-core\\src\\lib.rs:12", at(2*time.Hour)),
		fmt.Sprintf("%d pid=200 deactivate", at(2*time.Hour)),
		// 32 位元宿主，啟用開始後沒有完成。
		fmt.Sprintf("%d pid=300 activate flags=0x4 comless=false", at(time.Hour)),
		fmt.Sprintf("%d pid=300 activate 開始 exe=Netmarble Launcher.exe tid=5 x86", at(time.Hour)),
		// 程式自己停用輸入法、密碼欄、沒有焦點文件。
		fmt.Sprintf("%d pid=400 activate 開始 exe=Explorer.EXE tid=7", at(30*time.Minute)),
		fmt.Sprintf("%d pid=400 activate 完成 exe=Explorer.EXE tid=7 ui_less=false", at(30*time.Minute)),
		fmt.Sprintf("%d pid=400 欄位政策：程式停用了這個欄位的輸入法（空的 context），按鍵放行給程式 exe=Explorer.EXE", at(30*time.Minute)),
		fmt.Sprintf("%d pid=400 欄位政策：密碼或 PIN 欄位，按鍵直接交給程式 exe=Explorer.EXE", at(30*time.Minute)),
		fmt.Sprintf("%d pid=400 欄位政策：沒有焦點文件，按鍵放行給程式 exe=Explorer.EXE", at(30*time.Minute)),
		fmt.Sprintf("%d pid=400 ui: 宿主行程內候選窗（層級 3）", at(30*time.Minute)),
		// .14 的 WPF 與記事本實際寫出的格式（#257）：視窗類別裡每個行程不同的 GUID 換掉後同一則只列一次。
		fmt.Sprintf("%d pid=400 文件：TSF 文字儲存 status=0x2 focus=HwndWrapper[DefaultDomain;Pipeline Execution Thread;695894d7-7094-46e0-b982-49cbad457277] field=Open exe=Explorer.EXE", at(30*time.Minute)),
		fmt.Sprintf("%d pid=400 文件：TSF 文字儲存 status=0x2 focus=HwndWrapper[DefaultDomain;Pipeline Execution Thread;0f5894d7-7094-46e0-b982-49cbad457277] field=Open exe=Explorer.EXE", at(30*time.Minute)),
		// pid 重複使用：新行程的 flags 那行在 exe 那行之前，不能算到舊程式。
		fmt.Sprintf("%d pid=200 activate flags=0x40000040 comless=false", at(10*time.Minute)),
		fmt.Sprintf("%d pid=200 activate 開始 exe=WindowsTerminal.exe tid=9", at(10*time.Minute)),
		fmt.Sprintf("%d pid=200 activate 完成 exe=WindowsTerminal.exe tid=9 ui_less=false", at(10*time.Minute)),
		// 不認得 exe 的行程（註冊工具）不列。
		fmt.Sprintf("%d pid=500 register: begin", at(5*time.Minute)),
		"不是紀錄的行",
	}
	return strings.Join(lines, "\r\n") + "\r\n"
}

func TestSummarizeTSFLogGroupsJudgmentsPerProgram(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	s := summarizeTSFLog(strings.NewReader(tsfSample(now)), now)
	byExe := map[string]*tsfApp{}
	var order []string
	for _, app := range s.Apps {
		byExe[app.Exe] = app
		order = append(order, app.Exe)
	}
	if want := []string{"WindowsTerminal.exe", "Explorer.EXE", "Netmarble Launcher.exe", "acad.exe"}; strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("程式與順序 = %v，要 %v", order, want)
	}
	acad := byExe["acad.exe"]
	wantEvents := map[string]int{
		"activate": 1, "context_imm32": 1, "field_private": 1, "imm32_no_caret": 1, "candidate_fallback": 1,
		"composition_terminated": 1, "edit_failed": 1, "spawn_failed": 1, "engine_error": 1, "panic": 1,
	}
	if fmt.Sprint(acad.Events) != fmt.Sprint(wantEvents) {
		t.Fatalf("acad 判定 = %v，要 %v", acad.Events, wantEvents)
	}
	if fmt.Sprint(acad.Flags) != "[0x0]" || acad.X86 {
		t.Fatalf("acad 啟用旗標 = %v x86=%v", acad.Flags, acad.X86)
	}
	if !acad.LastSeen.Equal(now.Add(-2 * time.Hour)) {
		t.Fatalf("acad 最近一次 = %v", acad.LastSeen)
	}
	netmarble := byExe["Netmarble Launcher.exe"]
	if !netmarble.X86 || netmarble.Events["activate_unfinished"] != 1 || fmt.Sprint(netmarble.Flags) != "[0x4]" {
		t.Fatalf("32 位元、沒完成的啟用 = %+v", netmarble)
	}
	explorer := byExe["Explorer.EXE"]
	for _, kind := range []string{"field_disabled", "field_password", "field_no_document", "candidate_in_host"} {
		if explorer.Events[kind] != 1 {
			t.Fatalf("Explorer 缺 %s：%v", kind, explorer.Events)
		}
	}
	if explorer.Events["context_tsf"] != 2 || !slices.Contains(explorer.Details, "文件：TSF 文字儲存 status=0x2 focus=HwndWrapper[DefaultDomain;Pipeline Execution Thread;GUID] field=Open") || len(explorer.Details) != 4 {
		t.Fatalf("WPF 視窗類別的 GUID 沒換掉：%v %q", explorer.Events, explorer.Details)
	}
	if terminal := byExe["WindowsTerminal.exe"]; fmt.Sprint(terminal.Flags) != "[0x40000040]" || terminal.Events["activate"] != 1 {
		t.Fatalf("pid 重複使用的新程式 = %+v", terminal)
	}
	if s.Since == nil || !s.Since.Equal(now.Add(-3*time.Hour)) {
		t.Fatalf("since = %v", s.Since)
	}
}

func TestSummarizeTSFLogLeavesOutKeysPathsAndEngineMessages(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	b, err := json.Marshal(summarizeTSFLog(strings.NewReader(tsfSample(now)), now))
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	for _, leak := range []string{"vk=", "OnTestKeyDown", "type_literal", "王小明", "builder", `C:\\`, "Users", "invalid character", "old.exe", "register", "leak"} {
		if strings.Contains(out, leak) {
			t.Fatalf("摘要帶出 %q：%s", leak, out)
		}
	}
	// 細節保留判定需要的類別與旗標，程式名稱只在 exe 欄。
	for _, want := range []string{"focus=AfxWnd140u", "field=Private", "status=0x4", "lib.rs:12", "空的 context"} {
		if !strings.Contains(out, want) {
			t.Fatalf("摘要缺 %q：%s", want, out)
		}
	}
	var s tsfLogSummary
	_ = json.Unmarshal(b, &s)
	for _, app := range s.Apps {
		for _, d := range app.Details {
			if strings.Contains(d, "exe=") {
				t.Fatalf("細節重複程式名稱：%q", d)
			}
		}
	}
}

func TestSummarizeTSFLogLimitsPrograms(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	var b strings.Builder
	for i := 0; i < tsfAppsLimit+10; i++ {
		fmt.Fprintf(&b, "%d pid=%d activate 開始 exe=app%02d.exe tid=1\n", now.Add(time.Duration(i-100)*time.Minute).UnixMilli(), 1000+i, i)
	}
	s := summarizeTSFLog(strings.NewReader(b.String()), now)
	if len(s.Apps) != tsfAppsLimit || s.Apps[0].Exe != fmt.Sprintf("app%02d.exe", tsfAppsLimit+9) {
		t.Fatalf("留下 %d 個，第一個 %s", len(s.Apps), s.Apps[0].Exe)
	}
}

func TestTSFAppsFromFileReadsOnlyTheTail(t *testing.T) {
	now := time.Now()
	path := filepath.Join(t.TempDir(), "tsf.log")
	if s := tsfAppsFromFile(path, now); s != nil {
		t.Fatalf("沒有檔案時 = %+v", s)
	}
	old := fmt.Sprintf("%d pid=1 activate 開始 exe=head.exe tid=1\n", now.Add(-time.Hour).UnixMilli())
	filler := strings.Repeat(fmt.Sprintf("%d pid=2 deactivate\n", now.Add(-time.Hour).UnixMilli()), tsfLogTail/30)
	tail := fmt.Sprintf("%d pid=3 activate 開始 exe=tail.exe tid=1\n", now.Add(-time.Minute).UnixMilli())
	if err := os.WriteFile(path, []byte(old+filler+tail), 0o600); err != nil {
		t.Fatal(err)
	}
	s := tsfAppsFromFile(path, now)
	if s == nil || !s.Partial || len(s.Apps) != 1 || s.Apps[0].Exe != "tail.exe" {
		t.Fatalf("只讀尾端 = %+v", s)
	}
}
