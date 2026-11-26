package main

import (
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
)

// isolatedBase 把個人資料目錄指到暫存目錄。macOS 的路徑在第一次呼叫時就固定（搬移舊目錄），改 HOME 換不掉，
// 所以不在 macOS 跑，免得寫到真的狀態檔。
func isolatedBase(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "darwin" {
		t.Skip("macOS 的個人資料目錄在行程內固定，不能隔離")
	}
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("LOCALAPPDATA", dir)
	return basePath()
}

// 設定程式的「更新方式」下拉選單（#340）：mode -json 改寫狀態檔並回 {schema,ok,mode}，不印文字。
func TestModeJSON(t *testing.T) {
	setPackageManaged(t, false)
	base := isolatedBase(t)
	for _, mode := range []string{autoupdate.ModeAsk, autoupdate.ModeManual, autoupdate.ModeAuto} {
		out, err := captureStdout(t, func() error { return run([]string{"mode", mode, "-json"}) })
		if err != nil {
			t.Fatalf("mode %s -json: %v", mode, err)
		}
		var doc struct {
			Schema int
			OK     bool
			Mode   string
		}
		if json.Unmarshal([]byte(out), &doc) != nil || !doc.OK || doc.Mode != mode || doc.Schema != contractSchema {
			t.Fatalf("mode %s -json 輸出：%q", mode, out)
		}
		s, err := autoupdate.Load(base)
		if err != nil || s.Mode != mode {
			t.Fatalf("狀態檔的 mode=%v（%v），要 %s", s, err, mode)
		}
	}
	// 旗標在前也可以。
	if _, err := captureStdout(t, func() error { return run([]string{"mode", "-json", "ask"}) }); err != nil {
		t.Fatal(err)
	}
	if s, _ := autoupdate.Load(base); s.Mode != autoupdate.ModeAsk {
		t.Fatalf("mode -json ask 後 mode=%s", s.Mode)
	}
}

// 不認得的值：-json 時以契約格式回錯誤、狀態不動；沒有 -json 時照舊回用法。
func TestModeJSONRejectsUnknownValue(t *testing.T) {
	setPackageManaged(t, false)
	base := isolatedBase(t)
	if _, err := captureStdout(t, func() error { return run([]string{"mode", "ask"}) }); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"mode", "off", "-json"}, {"mode", "-json"}, {"mode", "auto", "ask", "-json"}} {
		out, err := captureStdout(t, func() error { return run(args) })
		if !errors.Is(err, errReported) {
			t.Fatalf("%v: %v", args, err)
		}
		var doc struct{ OK bool }
		if json.Unmarshal([]byte(out), &doc) != nil || doc.OK {
			t.Fatalf("%v 輸出：%q", args, out)
		}
	}
	if err := run([]string{"mode", "off"}); err == nil || !strings.Contains(err.Error(), "用法") {
		t.Fatalf("mode off：%v", err)
	}
	if s, _ := autoupdate.Load(base); s.Mode != autoupdate.ModeAsk {
		t.Fatalf("錯誤的值不應改動 mode：%s", s.Mode)
	}
}

// 由套件管理員更新時（#181）設定程式不顯示下拉選單；就算呼叫了，-json 也回 package_managed。
func TestModeJSONPackageManaged(t *testing.T) {
	setPackageManaged(t, true)
	isolatedBase(t)
	out, err := captureStdout(t, func() error { return run([]string{"mode", "ask", "-json"}) })
	if !errors.Is(err, errReported) || !strings.Contains(out, codePackageManaged) {
		t.Fatalf("%v %q", err, out)
	}
}

func TestFeaturesReportUpdateMode(t *testing.T) {
	if !featuresOf().UpdateMode {
		t.Fatal("status／settings -json 要回報 features.update_mode")
	}
}

// #340「手動更新」：查到新版時選單第一列與狀態輸出的動作是「立即下載」（download），下載好後才是「立即安裝」（finish）。
func TestManualModeActionIsDownload(t *testing.T) {
	hash := strings.Repeat("a", 64)
	s := &autoupdate.State{Mode: autoupdate.ModeManual, Stage: autoupdate.StageFound,
		Target:  &autoupdate.Target{Version: "0.2.15", Kind: "core", SHA256: hash},
		Blocked: &autoupdate.Blocked{Reason: "needs_download", Detail: autoupdate.NeedsDownloadDetail}}
	if got := actionFor(s); got != "download" {
		t.Fatalf("needs_download 的動作 %q，要 download", got)
	}
	out := map[string]any{}
	appendUpdateStateStatus(out, s, s.Blocked.Since)
	if out["action"] != "download" || out["target"].(map[string]string)["sha256"] != hash {
		t.Fatalf("status -json：%v", out)
	}
	if body := outcomeBody(s, nil, "0.2.14", s.Blocked.Since); !strings.Contains(body, "0.2.15") || !strings.Contains(body, "立即下載") {
		t.Fatalf("選單按檢查更新查到新版的通知：%q", body)
	}
	s.Stage = autoupdate.StagePrepared
	s.Blocked = &autoupdate.Blocked{Reason: "needs_consent"}
	if got := actionFor(s); got != "finish" {
		t.Fatalf("下載好之後的動作 %q，要 finish", got)
	}
	if body := outcomeBody(s, nil, "0.2.14", s.Blocked.Since); !strings.Contains(body, "立即安裝") {
		t.Fatalf("下載好之後的通知：%q", body)
	}
}

// 通知的「立即下載」按鈕：網址綁定版本與雜湊，按下執行 download-now（只下載），按鈕文字是「立即下載」。
func TestNoticeProtocolDownload(t *testing.T) {
	hash := strings.Repeat("b", 64)
	raw, err := noticeActionURL("download", "0.2.15", hash)
	if err != nil {
		t.Fatal(err)
	}
	command, args, err := noticeProtocolCommand(raw)
	if err != nil || command != "download-now" || strings.Join(args, " ") != "-target 0.2.15 -target-sha256 "+hash+" -notify" {
		t.Fatalf("%s %v %v", command, args, err)
	}
	if noticeButtonLabel("download") != "立即下載" || noticeButtonLabel("finish") != "立即安裝" {
		t.Fatal("按鈕文字")
	}
}

// 選單的「立即下載」只帶 -notify：下載狀態檔裡的待辦版本；沒有待辦時照實回報，不連網。
func TestDownloadNowFromMenuNeedsPendingTarget(t *testing.T) {
	setPackageManaged(t, false)
	base := isolatedBase(t)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/nonexistent/0c-test-bus") // 不送到測試機的桌面通知
	if _, err := autoupdate.Load(base); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"download-now", "-notify"}); err == nil || !strings.Contains(err.Error(), "沒有可以下載") {
		t.Fatalf("沒有待辦時：%v", err)
	}
	// 沒有 -notify、或只有 -target 之一，照舊是用法錯誤。
	for _, args := range [][]string{{"download-now"}, {"download-now", "-notify", "-target", "0.2.15"}} {
		if err := run(args); err == nil || !strings.Contains(err.Error(), "-target") {
			t.Fatalf("%v：%v", args, err)
		}
	}
}
