//go:build !windows

package main

import (
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"git.biggo.com/0c/ime/bridge/ipc"
	"git.biggo.com/0c/ime/internal/settings"
	"git.biggo.com/0c/ime/internal/vocabapi"
)

// 本機 API（#126）在 api.json 開啟時監聽 127.0.0.1（0.2.0 起不再看 preview_0_2，舊檔寫 false 也一樣）；
// api.json 關閉時不監聽。health 回報狀態與最後使用時間。
func TestVocabAPIStartsOnlyWhenEnabled(t *testing.T) {
	binary := buildServer(t)
	dir := t.TempDir()
	data, _ := filepath.Abs("../../testdata/toy")
	sock := filepath.Join(dir, "api.sock")
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	if err := vocabapi.SaveConfig(dir, vocabapi.Config{Enabled: true, Port: port}); err != nil {
		t.Fatal(err)
	}
	run := func(legacy string, fn func(health map[string]string)) {
		t.Helper()
		os.WriteFile(filepath.Join(dir, settings.FileName), []byte(`{"schema":1,"selection":"statistical"`+legacy+`}`), 0o600)
		server := exec.Command(binary, "-data", data, "-user", filepath.Join(dir, "user.json"), "-socket", sock)
		if err := server.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { server.Process.Signal(os.Interrupt); server.Wait() }()
		waitDial(t, sock)
		c, _ := net.Dial("unix", sock)
		defer c.Close()
		ipc.WriteFrame(c, ipc.Envelope{Op: ipc.OpHealth})
		var r ipc.Reply
		ipc.ReadFrame(c, &r)
		fn(r.Info)
	}
	get := func(token string) (int, string) {
		req, _ := http.NewRequest("GET", "http://127.0.0.1:"+itoa(port)+"/v1/status", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
		if err != nil {
			return 0, err.Error()
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if err := vocabapi.SaveConfig(dir, vocabapi.Config{Enabled: false, Port: port}); err != nil {
		t.Fatal(err)
	}
	run("", func(h map[string]string) {
		if code, _ := get("x"); code != 0 {
			t.Fatalf("api.json 關閉時不監聽：%d", code)
		}
	})
	if err := vocabapi.SaveConfig(dir, vocabapi.Config{Enabled: true, Port: port}); err != nil {
		t.Fatal(err)
	}
	run(`,"preview_0_2":false`, func(h map[string]string) {
		if h["api_state"] != "listening" || h["api_addr"] != "127.0.0.1:"+itoa(port) {
			t.Fatalf("health：%v", h)
		}
		tok, err := vocabapi.LoadToken(dir)
		if err != nil {
			t.Fatal(err)
		}
		if code, body := get(tok.Value); code != 200 || !strings.Contains(body, `"count":0`) {
			t.Fatalf("%d %s", code, body)
		}
		if code, _ := get("0c_wrong"); code != 401 {
			t.Fatalf("錯的 token：%d", code)
		}
	})
	if code, _ := get("x"); code != 0 {
		t.Fatal("引擎結束時放開埠")
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
