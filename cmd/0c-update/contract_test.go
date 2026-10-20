package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/bridge/ipc"
	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/enginedir"
	"github.com/0c-biggo/0c-ime/internal/settings"
	"github.com/0c-biggo/0c-ime/internal/update"
)

func noEngine(time.Duration) (net.Conn, error) { return nil, errors.New("no engine") }

func nop(string) {}

func writeSettings(t *testing.T, base, body string) string {
	t.Helper()
	path := filepath.Join(base, settings.FileName)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// fakeEngine 模擬執行中的引擎：回報 health；收到 drain 後，沒有組字就退出（之後連不上）。
type fakeEngine struct {
	mu        sync.Mutex
	gone      bool
	composing string
	info      map[string]string
}

func (f *fakeEngine) dial(time.Duration) (net.Conn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.gone {
		return nil, errors.New("no engine")
	}
	client, server := net.Pipe()
	go func() {
		defer server.Close()
		var request ipc.Envelope
		if ipc.ReadFrame(server, &request) != nil {
			return
		}
		f.mu.Lock()
		info := map[string]string{"composing": f.composing}
		for k, v := range f.info {
			info[k] = v
		}
		if request.Op == ipc.OpDrain && f.composing == "0" {
			f.gone = true
		}
		f.mu.Unlock()
		ipc.WriteFrame(server, ipc.Reply{OK: true, Info: info})
	}()
	return client, nil
}

func TestParseSetArgs(t *testing.T) {
	o, err := parseSetArgs([]string{"selection", "statistical", "-apply", "-notify"})
	if err != nil || len(o.pairs) != 1 || o.pairs[0] != [2]string{"selection", "statistical"} || !o.apply || !o.notify || o.json {
		t.Fatalf("既有的單項寫法必須照舊：%+v %v", o, err)
	}
	o, err = parseSetArgs([]string{"punctuation", "off", "fuzzy-z-zh", "select", "--json", "-repair"})
	if err != nil || len(o.pairs) != 2 || !o.json || !o.repair || o.apply {
		t.Fatalf("多項與 json／repair：%+v %v", o, err)
	}
	for _, args := range [][]string{
		{}, {"-apply"}, {"selection"}, {"selection", "neural", "punctuation"},
		{"selection", "neural", "-apply", "-apply"}, {"selection", "neural", "-json", "--json"}, {"selection", "neural", "-repair", "-repair"},
		{"selection", "neural", "-other"},
	} {
		var usage usageError
		if _, err := parseSetArgs(args); !errors.As(err, &usage) {
			t.Errorf("%v 應回用法錯誤：%v", args, err)
		}
	}
}

func TestRunSetChangesSeveralKeysAtOnceAndKeepsUnknownFields(t *testing.T) {
	base := t.TempDir()
	path := writeSettings(t, base, `{"selection":"neural","punctuation":true,"sync":{"enabled":true}}`)
	res, err := runSet(base, noEngine, setOptions{pairs: [][2]string{{"punctuation", "off"}, {"fuzzy-s-sh", "off"}}}, nop)
	if err != nil || !res.OK || res.Apply.State != applyNotRequested || res.Apply.Requested {
		t.Fatalf("%+v %v", res, err)
	}
	want := []changeView{{"punctuation", "on", "off"}, {"fuzzy-s-sh", "select", "off"}}
	if len(res.Changes) != 2 || res.Changes[0] != want[0] || res.Changes[1] != want[1] {
		t.Fatalf("變更紀錄：%+v", res.Changes)
	}
	text := readFile(t, path)
	for _, s := range []string{`"punctuation": false`, `"s-sh": "off"`, `"sync"`, `"enabled": true`} {
		if !strings.Contains(text, s) {
			t.Errorf("存檔應包含 %s：%s", s, text)
		}
	}
}

// 任何一組鍵值不合法，整批都不寫：不會留下改一半的設定。
func TestRunSetIsAllOrNothing(t *testing.T) {
	base := t.TempDir()
	path := writeSettings(t, base, `{"selection":"neural"}`)
	before := readFile(t, path)
	_, err := runSet(base, noEngine, setOptions{pairs: [][2]string{{"selection", "statistical"}, {"nope", "x"}}}, nop)
	if c := classify(err); c.Code != settings.CodeUnknownKey || c.Key != "nope" {
		t.Fatalf("%+v %v", c, err)
	}
	_, err = runSet(base, noEngine, setOptions{pairs: [][2]string{{"selection", "statistical"}, {"punctuation", "maybe"}}}, nop)
	if c := classify(err); c.Code != settings.CodeInvalidValue || c.Key != "punctuation" {
		t.Fatalf("%+v %v", c, err)
	}
	if after := readFile(t, path); after != before {
		t.Fatalf("失敗時不得改動檔案：%s", after)
	}
}

func TestRunSetRefusesFileFromNewerVersion(t *testing.T) {
	base := t.TempDir()
	path := writeSettings(t, base, `{"schema":9,"selection":"statistical"}`)
	_, err := runSet(base, noEngine, setOptions{pairs: [][2]string{{"punctuation", "off"}}}, nop)
	if c := classify(err); c.Code != codeReadOnly {
		t.Fatalf("%+v %v", c, err)
	}
	if got := readFile(t, path); got != `{"schema":9,"selection":"statistical"}` {
		t.Fatalf("不得覆寫較新版本的檔案：%s", got)
	}
}

func TestRunSetRepairQuarantinesCorruptFileOnlyWhenAskedAndValid(t *testing.T) {
	base := t.TempDir()
	path := writeSettings(t, base, "{broken")
	pairs := [][2]string{{"selection", "statistical"}}
	if _, err := runSet(base, noEngine, setOptions{pairs: pairs}, nop); classify(err).Code != codeCorrupt || !strings.Contains(classify(err).Message, "-repair") {
		t.Fatalf("沒有 -repair 時應回 corrupt 並提示：%v", err)
	}
	if readFile(t, path) != "{broken" {
		t.Fatal("沒有 -repair 不得動壞檔")
	}
	// 輸入有誤時，即使有 -repair 也不搬走壞檔。
	if _, err := runSet(base, noEngine, setOptions{pairs: [][2]string{{"nope", "x"}}, repair: true}, nop); classify(err).Code != settings.CodeUnknownKey {
		t.Fatal(err)
	}
	if matches, _ := filepath.Glob(path + ".corrupt-*"); len(matches) != 0 {
		t.Fatalf("輸入有誤時不應產生備份：%v", matches)
	}
	res, err := runSet(base, noEngine, setOptions{pairs: pairs, repair: true}, nop)
	if err != nil || res.Quarantined == "" {
		t.Fatalf("%+v %v", res, err)
	}
	if readFile(t, res.Quarantined) != "{broken" {
		t.Fatal("壞檔應原樣改名保留")
	}
	if s, err := settings.Load(path); err != nil || s.Selection != settings.Statistical || s.Punctuation {
		t.Fatalf("應從預設值重新開始並套用這次的修改：%+v %v", s, err)
	}
}

func TestRunSetReportsBusyWhenAnotherProcessHoldsTheLock(t *testing.T) {
	base := t.TempDir()
	writeSettings(t, base, `{}`)
	var err error
	if lockErr := autoupdate.WithLock(base, "settings-write.lock", func() error {
		_, err = runSet(base, noEngine, setOptions{pairs: [][2]string{{"punctuation", "off"}}}, nop)
		return nil
	}); lockErr != nil {
		t.Fatal(lockErr)
	}
	if c := classify(err); c.Code != codeBusy {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestBackgroundUpdateDoesNotBlockSettingsAndCheckReportsProgress(t *testing.T) {
	base := t.TempDir()
	err := autoupdate.WithLock(base, "coordinator.lock", func() error {
		if _, err := runSet(base, noEngine, setOptions{pairs: [][2]string{{"punctuation", "off"}}}, nop); err != nil {
			t.Fatalf("background update must not block saving settings: %v", err)
		}
		var out bytes.Buffer
		err := runCheck(base, checkPlatform{installed: "0.1.0-dev.49"}, 5*time.Second, true, &out)
		if !errors.Is(err, errReported) || !strings.Contains(out.String(), `"code":"in_progress"`) {
			t.Fatalf("%s %v", out.String(), err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRunSetLockFilesystemFailureReportsIO(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "update", "settings-write.lock"), 0700); err != nil {
		t.Fatal(err)
	}
	_, err := runSet(base, noEngine, setOptions{pairs: [][2]string{{"punctuation", "off"}}}, nop)
	if c := classify(err); c.Code != codeIO {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestApplyOutcomes(t *testing.T) {
	old := drainWait
	drainWait = 300 * time.Millisecond
	t.Cleanup(func() { drainWait = old })
	pair := [][2]string{{"punctuation", "off"}}

	// 引擎沒有在執行：設定已儲存，下次啟動就是新設定。
	base := t.TempDir()
	res, err := runSet(base, noEngine, setOptions{pairs: pair, apply: true}, nop)
	if err != nil || res.Apply != (applyView{Requested: true, State: applyEngineStopped}) {
		t.Fatalf("%+v %v", res.Apply, err)
	}

	// 沒有組字：引擎交接退出，下一個按鍵以新設定啟動。
	idle := &fakeEngine{composing: "0"}
	res, err = runSet(t.TempDir(), idle.dial, setOptions{pairs: pair, apply: true}, nop)
	if err != nil || res.Apply.State != applyApplied {
		t.Fatalf("%+v %v", res.Apply, err)
	}

	// 仍在組字：設定已儲存，但引擎沒有交接；要說清楚是因為組字。
	busy := &fakeEngine{composing: "2"}
	var notices []string
	base = t.TempDir()
	res, err = runSet(base, busy.dial, setOptions{pairs: pair, apply: true}, func(s string) { notices = append(notices, s) })
	if err != nil || !res.OK || res.Apply.State != applyDeferred || res.Apply.Detail == "" {
		t.Fatalf("%+v %v", res.Apply, err)
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "完成組字") {
		t.Fatalf("組字中應提示先完成組字：%q", notices)
	}
	if s, _ := settings.Load(filepath.Join(base, settings.FileName)); s.Punctuation {
		t.Fatal("套用被延後不影響設定已儲存")
	}
}

// 文字輸出沿用既有句型：Fcitx5 與 Windows 選單以此判斷「下一個按鍵起生效」。
func TestSetTextOutputKeepsExistingWording(t *testing.T) {
	var out bytes.Buffer
	var notices []string
	record := func(s string) { notices = append(notices, s) }
	printSetText(&out, setResult{Changes: []changeView{{"selection", "neural", "statistical"}}, Apply: applyView{State: applyApplied}}, record)
	if out.String() != "已設定 selection=statistical，下一個按鍵起生效。\n" || notices[0] != "設定已儲存，下一個按鍵起生效。" {
		t.Fatalf("%q %q", out.String(), notices)
	}
	out.Reset()
	notices = nil
	printSetText(&out, setResult{Changes: []changeView{{"selection", "neural", "statistical"}, {"punctuation", "on", "off"}}, Apply: applyView{State: applyNotRequested}}, record)
	if out.String() != "已設定 selection=statistical punctuation=off。"+restartHint+"\n" || !strings.Contains(notices[0], "尚未套用") {
		t.Fatalf("%q %q", out.String(), notices)
	}
}

func TestRunningValuesMapEngineHealthToSettingKeys(t *testing.T) {
	got := runningValues(map[string]string{"selection_setting": "statistical", "document_context": "false", "punctuation_state": "disabled_by_setting", "fuzzy": "z-zh=select,en-eng=select"})
	want := map[string]string{"selection": "statistical", "document-context": "off", "punctuation": "off",
		"fuzzy-z-zh": "select", "fuzzy-c-ch": "off", "fuzzy-s-sh": "off", "fuzzy-en-eng": "select"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q，應為 %q", k, got[k], v)
		}
	}
	// 引擎回報 auto：原樣對應到設定值，設定檔寫 auto 但引擎還在跑舊設定時 pending 才算得出來。
	if got := runningValues(map[string]string{"fuzzy": "z-zh=auto,c-ch=select"}); got["fuzzy-z-zh"] != "auto" || got["fuzzy-c-ch"] != "select" || got["fuzzy-s-sh"] != "off" {
		t.Errorf("auto 的對應：%v", got)
	}
	// 欄位存在但為空字串：全部關閉；欄位不存在（舊版引擎）：不知道。
	if all := runningValues(map[string]string{"fuzzy": ""}); all["fuzzy-z-zh"] != "off" || len(all) != 4 {
		t.Errorf("fuzzy 為空字串表示全部關閉：%v", all)
	}
	if old := runningValues(map[string]string{"version": "old", "selection_setting": "neural"}); len(old) != 1 || old["selection"] != "neural" {
		t.Errorf("舊版引擎沒回報的項目不得猜測：%v", old)
	}
	if on := runningValues(map[string]string{"punctuation_state": "fallback"}); on["punctuation"] != "on" {
		t.Errorf("補標點模型退回時，設定仍是開：%v", on)
	}
	for _, tc := range []struct{ value, want string }{{"true", "on"}, {"false", "off"}, {"", ""}} {
		if got := runningValues(map[string]string{"emoji_equals_first": tc.value})["emoji-equals-first"]; got != tc.want {
			t.Fatalf("equals option runtime %q => %q, want %q", tc.value, got, tc.want)
		}
	}
}

func item(t *testing.T, doc settingsDoc, key string) settingsItem {
	t.Helper()
	for _, it := range doc.Items {
		if it.Key == key {
			return it
		}
	}
	t.Fatalf("沒有 %s", key)
	return settingsItem{}
}

func TestSettingsDocComparesStoredWithRunningValues(t *testing.T) {
	base := t.TempDir()
	writeSettings(t, base, `{"selection":"statistical","fuzzy":{"z-zh":"off","s-sh":"off"}}`)
	info := map[string]string{"version": "0.1.0-dev.50", "selection_setting": "neural", "document_context": "true", "punctuation_state": "ready", "fuzzy": "z-zh=select"}
	doc := buildSettingsDoc(base, modelStatusDial(info, true))
	if doc.Schema != contractSchema || !doc.OK || doc.File.State != settings.StateOK || doc.Engine.State != "running" || doc.Engine.Version != "0.1.0-dev.50" {
		t.Fatalf("%+v", doc)
	}
	check := func(key, value, running string, pending bool) {
		t.Helper()
		it := item(t, doc, key)
		if it.Value != value || it.Running == nil || *it.Running != running || it.Pending != pending {
			t.Errorf("%s：%+v running=%v", key, it.Item, it.Running)
		}
	}
	check("selection", "statistical", "neural", true)
	check("punctuation", "off", "on", true)
	check("document-context", "on", "on", false)
	check("fuzzy-z-zh", "off", "select", true)
	check("fuzzy-c-ch", "select", "off", true)
	check("fuzzy-s-sh", "off", "off", false)
	if !doc.PendingRestart {
		t.Fatal("有項目待套用時 pending_restart 應為 true")
	}

	// 引擎沒有執行：沒有「執行中的值」，也就沒有待套用。
	doc = buildSettingsDoc(base, noEngine)
	if doc.Engine.State != "not_running" || doc.PendingRestart {
		t.Fatalf("%+v", doc.Engine)
	}
	for _, it := range doc.Items {
		if it.Running != nil || it.Pending {
			t.Errorf("引擎未執行時 %s 不應有執行中的值", it.Key)
		}
	}
	// 舊版引擎沒有回報模糊音：該項為未知，而不是猜成關閉。
	doc = buildSettingsDoc(base, modelStatusDial(map[string]string{"version": "old", "selection_setting": "statistical"}, true))
	if it := item(t, doc, "fuzzy-c-ch"); it.Running != nil || it.Pending {
		t.Fatalf("舊版引擎的模糊音應為未知：%+v", it)
	}
	if it := item(t, doc, "selection"); it.Running == nil || it.Pending {
		t.Fatalf("%+v", it)
	}
	// 引擎在但查詢失敗：unavailable。
	if doc = buildSettingsDoc(base, modelStatusDial(nil, false)); doc.Engine.State != "unavailable" {
		t.Fatalf("%+v", doc.Engine)
	}
}

func TestSettingsDocExplainsFileProblems(t *testing.T) {
	base := t.TempDir()
	writeSettings(t, base, `{"selection":"remote","future":1}`)
	doc := buildSettingsDoc(base, noEngine)
	if doc.File.State != settings.StateOK || len(doc.File.Notes) != 2 || item(t, doc, "selection").Value != settings.Neural {
		t.Fatalf("%+v", doc.File)
	}
	writeSettings(t, base, `{"schema":5}`)
	if doc = buildSettingsDoc(base, noEngine); !doc.File.ReadOnly || doc.File.Schema != 5 {
		t.Fatalf("%+v", doc.File)
	}
	writeSettings(t, base, `oops`)
	doc = buildSettingsDoc(base, noEngine)
	if !doc.OK || doc.File.State != settings.StateCorrupt || doc.File.Error == "" || item(t, doc, "selection").Value != settings.Neural {
		t.Fatalf("壞檔仍要產出完整文件（顯示預設值），讓介面能提示修復：%+v", doc.File)
	}
}

// 契約的外形：鍵名與型別是介面依賴的，改動要升 schema 或只新增欄位。
func TestSettingsDocJSONShape(t *testing.T) {
	base := t.TempDir()
	b, err := json.Marshal(buildSettingsDoc(base, noEngine))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err = json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"schema", "ok", "file", "engine", "items", "pending_restart"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("缺少頂層欄位 %s：%s", key, b)
		}
	}
	file := doc["file"].(map[string]any)
	if notes, ok := file["notes"].([]any); !ok || len(notes) != 0 {
		t.Errorf("file.notes 沒有內容時應是空陣列，不是 null：%v", file["notes"])
	}
	first := doc["items"].([]any)[0].(map[string]any)
	for _, key := range []string{"key", "type", "values", "default", "value", "label", "description", "running", "pending"} {
		if _, ok := first[key]; !ok {
			t.Errorf("項目缺少 %s：%v", key, first)
		}
	}
	if first["running"] != nil {
		t.Errorf("running 未知時應是 null：%v", first["running"])
	}
}

func TestClassifyMapsErrorsToStableCodes(t *testing.T) {
	for _, c := range []struct {
		err  error
		code string
	}{
		{usageError("x"), codeUsage},
		{&settings.SetError{Code: settings.CodeUnknownKey, Key: "k", Msg: "m"}, settings.CodeUnknownKey},
		{settings.ErrReadOnly, codeReadOnly},
		{settings.ErrCorrupt, codeCorrupt},
		{autoupdate.ErrLocked, codeBusy},
		{autoupdate.ErrUpdateInProgress, codeInProgress},
		{errors.New("disk"), codeIO},
	} {
		if got := classify(c.err); got.Code != c.code || got.Message == "" {
			t.Errorf("%v → %+v，應為 %s", c.err, got, c.code)
		}
	}
	var out bytes.Buffer
	if err := failJSON(&out, usageError("bad")); !errors.Is(err, errReported) {
		t.Fatal(err)
	}
	var doc struct {
		Schema int
		OK     bool
		Error  contractError
	}
	if json.Unmarshal(out.Bytes(), &doc) != nil || doc.OK || doc.Schema != contractSchema || doc.Error.Code != codeUsage {
		t.Fatalf("%s", out.String())
	}
}

type checkPlatform struct {
	autoupdate.Platform
	installed string
	feed      *update.SignedFeed
	err       error
}

func (p checkPlatform) Installed() (string, error) { return p.installed, nil }
func (p checkPlatform) Fetch(context.Context, uint64) (*update.SignedFeed, error) {
	return p.feed, p.err
}
func (p checkPlatform) Target() (string, string) { return "linux", "amd64" }

func feedOf(version string, rollout int) *update.SignedFeed {
	return &update.SignedFeed{Serial: 1, Version: version, Rollout: rollout,
		Assets: []update.SignedAsset{{Asset: update.Asset{OS: "linux", Arch: "amd64", URL: "https://x/" + version, SHA256: strings.Repeat("a", 64), Size: 1}, Kind: "core"}}}
}

func TestRunCheckReportsAllFourStates(t *testing.T) {
	decode := func(out *bytes.Buffer) checkResult {
		t.Helper()
		var r checkResult
		if err := json.Unmarshal(out.Bytes(), &r); err != nil {
			t.Fatalf("%v：%s", err, out.String())
		}
		return r
	}
	run := func(base string, p checkPlatform) checkResult {
		t.Helper()
		var out bytes.Buffer
		if err := runCheck(base, p, time.Second, true, &out); err != nil {
			t.Fatal(err)
		}
		return decode(&out)
	}
	// 離線：這是查詢結果，不是命令失敗；不得說成已是最新。
	base := t.TempDir()
	r := run(base, checkPlatform{installed: "0.1.0-dev.42", err: errors.New("offline")})
	if !r.OK || r.Schema != contractSchema || r.Update.State != autoupdate.CheckFailed || r.Update.CheckFailures != 1 {
		t.Fatalf("%+v", r)
	}
	// 恢復連線、沒有新版。
	r = run(base, checkPlatform{installed: "0.1.0-dev.42", feed: feedOf("0.1.0-dev.42", 100)})
	if r.Update.State != autoupdate.CheckUpToDate || r.Update.CheckFailures != 0 || r.Update.LastSuccess == nil {
		t.Fatalf("%+v", r)
	}
	// 有新版。
	r = run(base, checkPlatform{installed: "0.1.0-dev.42", feed: feedOf("0.1.0-dev.43", 100)})
	if r.Update.State != autoupdate.CheckUpdateAvailable || r.Update.Target == nil || r.Update.Target.Version != "0.1.0-dev.43" {
		t.Fatalf("%+v", r)
	}
	// 另一台電腦：索引有新版但分批還沒輪到，要說明原因而不是只說最新。
	r = run(t.TempDir(), checkPlatform{installed: "0.1.0-dev.42", feed: feedOf("0.1.0-dev.43", 0)})
	if r.Update.State != autoupdate.CheckUpToDate || r.Update.Latest == nil || r.Update.Latest.Reason != autoupdate.ReasonRollout {
		t.Fatalf("%+v", r)
	}
	if text := describeUpdate(r.Update); !strings.Contains(text, "分批發布還沒輪到") {
		t.Fatalf("%s", text)
	}
	// 更新作業進行中：JSON 錯誤 in_progress。
	busy := t.TempDir()
	s, _ := autoupdate.Load(busy)
	s.Switched = true
	if err := autoupdate.Save(busy, s); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runCheck(busy, checkPlatform{installed: "x", feed: feedOf("0.1.0-dev.43", 100)}, time.Second, true, &out); !errors.Is(err, errReported) {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"code":"in_progress"`) {
		t.Fatalf("%s", out.String())
	}
}

func TestDescribeUpdateNeverSaysLatestWhenUnknown(t *testing.T) {
	for _, c := range []struct {
		view autoupdate.UpdateView
		want string
		not  string
	}{
		{autoupdate.UpdateView{State: autoupdate.CheckNever}, "尚未檢查", "最新"},
		{autoupdate.UpdateView{State: autoupdate.CheckFailed, Error: "offline"}, "無法連線", "已是"},
		{autoupdate.UpdateView{State: autoupdate.CheckUpdateAvailable, Target: &autoupdate.ViewTarget{Version: "0.1.0-dev.43"}}, "有新版 0.1.0-dev.43", "已是"},
	} {
		if got := describeUpdate(c.view); !strings.Contains(got, c.want) || strings.Contains(got, c.not) {
			t.Errorf("%s：%s", c.view.State, got)
		}
	}
}

// 設定程式總覽以 engine（引擎指標）|| installed（收據）判斷「應該在跑的版本」，與 update.installed 一致。
// 核心更新只換引擎，收據仍是外殼的版本；只拿收據比會把執行中的新引擎當成舊版（0.2.1 候選版冒煙）。
func TestStatusMapReportsCorePointerBesideShellReceipt(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "update"), 0o700); err != nil {
		t.Fatal(err)
	}
	s, _ := autoupdate.Load(base)
	s.ScheduleRemoved = true
	if err := autoupdate.Save(base, s); err != nil {
		t.Fatal(err)
	}
	type doc struct {
		Installed, Engine string
		Runtime           struct{ State, Version string }
		Update            struct{ Installed string }
	}
	read := func(dial autoupdate.Dialer) doc {
		b, err := json.Marshal(statusMap(base, "0.2.0", dial))
		if err != nil {
			t.Fatal(err)
		}
		var d doc
		if err = json.Unmarshal(b, &d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	// 沒做過核心更新：engine 是空字串（0.2.0 實際的 status.json 就是這樣），以收據為準。
	if d := read(modelStatusDial(map[string]string{"version": "0.2.0"}, true)); d.Installed != "0.2.0" || d.Engine != "" || d.Runtime.Version != "0.2.0" || d.Update.Installed != "0.2.0" {
		t.Fatalf("no pointer: %+v", d)
	}
	dir := "versions/0.2.1-c-aaaaaaaaaaaa"
	if err := os.MkdirAll(filepath.Join(base, filepath.FromSlash(dir), "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{enginedir.Executable(), "data/manifest.json"} {
		if err := os.WriteFile(filepath.Join(base, filepath.FromSlash(dir), filepath.FromSlash(f)), []byte("{}"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := enginedir.Write(base, enginedir.Current{Directory: dir, Version: "0.2.1"}); err != nil {
		t.Fatal(err)
	}
	// 核心更新後：收據仍是 0.2.0，engine 與 update.installed 是 0.2.1，執行中的引擎也是 0.2.1。
	if d := read(modelStatusDial(map[string]string{"version": "0.2.1"}, true)); d.Installed != "0.2.0" || d.Engine != "0.2.1" || d.Runtime.Version != "0.2.1" || d.Update.Installed != "0.2.1" {
		t.Fatalf("core pointer: %+v", d)
	}
}

func TestStatusMapKeepsOldFieldsAndAddsSchemaFuzzyAndUpdateView(t *testing.T) {
	base := t.TempDir()
	// 排程已移除：避免狀態檢查去查這台機器的系統排程。
	if err := os.MkdirAll(filepath.Join(base, "update"), 0o700); err != nil {
		t.Fatal(err)
	}
	s, _ := autoupdate.Load(base)
	s.ScheduleRemoved = true
	if err := autoupdate.Save(base, s); err != nil {
		t.Fatal(err)
	}
	writeSettings(t, base, `{"selection":"statistical","fuzzy":{"z-zh":"off"}}`)
	out := statusMap(base, "0.1.0-dev.42", noEngine)
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Schema      int
		Installed   string
		Mode, Stage string
		Settings    struct {
			Selection       string
			Punctuation     bool
			DocumentContext bool `json:"document_context"`
			Fuzzy           map[string]string
		}
		SettingsState string `json:"settings_state"`
		Update        struct{ State string }
		Runtime       struct{ State string }
	}
	if err = json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	// 既有外殼讀的欄位原封不動。
	if doc.Installed != "0.1.0-dev.42" || doc.Mode != "auto" || doc.Stage != "idle" || doc.Runtime.State != "not_running" ||
		doc.Settings.Selection != "statistical" || doc.Settings.Punctuation || !doc.Settings.DocumentContext {
		t.Fatalf("%s", b)
	}
	// 新增欄位。
	if doc.Schema != contractSchema || doc.SettingsState != "ok" || doc.Settings.Fuzzy["z-zh"] != "off" || doc.Settings.Fuzzy["c-ch"] != "select" || doc.Update.State != autoupdate.CheckNever {
		t.Fatalf("%s", b)
	}
	// 壞檔：沒有 settings，但要說明狀態；較新版本的檔案：標示只讀。
	writeSettings(t, base, `oops`)
	if m := statusMap(base, "x", noEngine); m["settings_state"] != settings.StateCorrupt || m["settings"] != nil {
		t.Fatalf("%v", m)
	}
	writeSettings(t, base, `{"schema":3}`)
	if m := statusMap(base, "x", noEngine); m["settings_read_only"] != true || m["settings"] == nil {
		t.Fatalf("%v", m)
	}
}
