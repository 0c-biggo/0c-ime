//go:build !windows

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
)

// oneJSONDocument：b 是單一份 JSON 物件（設定程式的 parse_output 以 serde_json 解析整份 stdout）。
func oneJSONDocument(t *testing.T, b []byte) map[string]any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(b))
	var v map[string]any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("stdout is not a JSON object: %v\n%s", err, b)
	}
	if dec.More() || len(bytes.TrimSpace(b[dec.InputOffset():])) != 0 {
		t.Fatalf("stdout has more than one JSON document:\n%s", b)
	}
	return v
}

// #195 後續：macOS 每次 auto／finish 都會先補做登入時的輸入法清單（retriesInputMethodEveryRun）；那一段印出的狀態
// 不能跑進 -json 的 stdout，否則設定程式「立即安裝」（finish -json）解析失敗。整個命令的 stdout 只留一份 JSON。
func TestFinishJSONStdoutHasOnlyTheResult(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	base := basePath()
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	saved := retriesInputMethodEveryRun
	retriesInputMethodEveryRun = true // 走 macOS 的路徑：不帶 -login 也補做
	t.Cleanup(func() { retriesInputMethodEveryRun = saved })
	if runtime.GOOS == "darwin" {
		// 記號留著、這個登入工作階段已試過：不補做（App 不存在時記號直接清掉，也不補做）。
		if err := os.MkdirAll(filepath.Join(base, "update"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(inputSourceMarker(base), []byte("/nonexistent/0c.app/Contents/MacOS/0c\n/nonexistent/0c.app\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, profileAtLogin := range []bool{true, false} {
		if _, err := autoupdate.EditState(base, func(s *autoupdate.State) error {
			s.Mode, s.Feed, s.ProfileAtLogin = autoupdate.ModeAsk, "https://127.0.0.1:1/feed.json", profileAtLogin
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, action := range []string{"finish", "auto"} {
			out, err := captureStdout(t, func() error { return autoCommand(action, []string{"-json"}) })
			v := oneJSONDocument(t, []byte(out))
			if err != nil && err != errReported {
				t.Fatalf("%s -json: %v", action, err)
			}
			if _, ok := v["schema"]; !ok {
				t.Fatalf("%s -json: not the contract result: %s", action, out)
			}
		}
		s, err := autoupdate.Load(base)
		if err != nil {
			t.Fatal(err)
		}
		if s.ProfileAtLogin {
			t.Fatal("ProfileAtLogin stays set after the input method was added")
		}
	}
}
