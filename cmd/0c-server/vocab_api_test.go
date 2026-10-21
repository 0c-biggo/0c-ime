//go:build !windows

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/bridge/ipc"
	"github.com/0c-biggo/0c-ime/core/engine"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/vocab"
	"github.com/0c-biggo/0c-ime/internal/settings"
	"github.com/0c-biggo/0c-ime/internal/vocabapi"
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

// #196：經本機 API 加入、刪除的詞在回成功時已經寫進 vocab.json（含批次紀錄），引擎被強制結束、
// 沒機會在結束前補存也一樣；重啟後詞與 /v1/batches 都還在。
func TestVocabAPIWritesPersistAcrossRestart(t *testing.T) {
	binary := buildServer(t)
	dir := t.TempDir()
	data, _ := filepath.Abs("../../testdata/toy")
	sock := filepath.Join(dir, "api.sock")
	vocabPath := filepath.Join(dir, "vocab.json")
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	if err := vocabapi.SaveConfig(dir, vocabapi.Config{Enabled: true, Port: port}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, settings.FileName), []byte(`{"schema":1,"selection":"statistical"}`), 0o600)
	var logs bytes.Buffer
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("引擎日誌：\n%s", logs.String())
		}
	})
	start := func() *exec.Cmd {
		t.Helper()
		server := exec.Command(binary, "-data", data, "-user", filepath.Join(dir, "user.json"), "-socket", sock)
		server.Stderr = &logs
		if err := server.Start(); err != nil {
			t.Fatal(err)
		}
		waitDial(t, sock)
		// IPC 回得了話時已經裝好結束訊號的處理（API 比它早開始監聽）：之後的 Interrupt 會照常存檔再結束。
		c, err := net.Dial("unix", sock)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		ipc.WriteFrame(c, ipc.Envelope{Op: ipc.OpHealth})
		var r ipc.Reply
		if err := ipc.ReadFrame(c, &r); err != nil {
			t.Fatal(err)
		}
		return server
	}
	call := func(method, path, body string) (int, map[string]any) {
		t.Helper()
		var resp *http.Response
		var err error
		// 端點先開，API 隨後才監聽（token 也在那時才建立）：等到連得上為止。
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			var tok vocabapi.Token
			if tok, err = vocabapi.LoadToken(dir); err == nil {
				req, _ := http.NewRequest(method, "http://127.0.0.1:"+itoa(port)+path, strings.NewReader(body))
				req.Header.Set("Authorization", "Bearer "+tok.Value)
				if body != "" {
					req.Header.Set("Content-Type", "application/json")
				}
				resp, err = (&http.Client{Timeout: 3 * time.Second}).Do(req)
			}
			if err == nil || time.Now().After(deadline) {
				break
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	stored := func() vocabFile {
		t.Helper()
		var f vocabFile
		b, err := os.ReadFile(vocabPath)
		if err != nil {
			t.Fatalf("API 回成功時 vocab.json 要已經寫入：%v", err)
		}
		json.Unmarshal(b, &f)
		return f
	}
	kill := func(server *exec.Cmd) { server.Process.Kill(); server.Wait() } // 不給引擎結束前補存的機會

	server := start()
	if code, out := call("POST", "/v1/words", `{"words":[{"word":"王小明","readings":["ㄨㄤˊ ㄒㄧㄠˇ ㄇㄧㄥˊ"]},{"word":"kubectl"}]}`); code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	if f := stored(); len(f.Words) != 2 || len(f.Batches) != 1 || f.Batches[0].Source != "assistant" {
		t.Fatalf("加詞後立即寫入：%+v", f)
	}
	if code, out := call("POST", "/v1/words/remove", `{"words":["kubectl"]}`); code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	if f := stored(); len(f.Words) != 1 || len(f.Batches) != 2 {
		t.Fatalf("刪詞後立即寫入：%+v", f)
	}
	if code, out := call("POST", "/v1/snippets", `{"snippets":["台北市"]}`); code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	if f := stored(); len(f.Phrases) != 1 || len(f.Batches) != 3 {
		t.Fatalf("加片語後立即寫入：%+v", f)
	}
	kill(server)

	server = start()
	defer kill(server)
	code, out := call("GET", "/v1/words", "")
	if words, _ := out["words"].([]any); code != 200 || len(words) != 1 || words[0].(map[string]any)["word"] != "王小明" {
		t.Fatalf("重啟後詞還在：%d %v", code, out)
	}
	if code, out = call("GET", "/v1/batches", ""); code != 200 || len(out["batches"].([]any)) != 3 {
		t.Fatalf("重啟後批次紀錄還在：%d %v", code, out)
	}
	kill(server)

	// 寫不進去時（這裡把 vocab.json 換成資料夾）API 回 not_saved，不假裝成功；引擎結束、放鎖前再存一次。
	server = start()
	if code, out := call("GET", "/v1/status", ""); code != 200 { // 讀入之後才換掉檔案
		t.Fatalf("%d %v", code, out)
	}
	os.Remove(vocabPath)
	os.Mkdir(vocabPath, 0o700)
	if code, out := call("POST", "/v1/words", `{"words":[{"word":"git"}]}`); code != 500 || out["error"].(map[string]any)["code"] != vocabapi.CodeNotSaved {
		t.Fatalf("存檔失敗要回錯誤：%d %v", code, out)
	}
	os.Remove(vocabPath)
	server.Process.Signal(os.Interrupt)
	server.Wait()
	if f := stored(); len(f.Words) != 2 {
		t.Fatalf("結束前補存：%+v", f)
	}
}

type vocabFile struct {
	Words   []struct{ Word string }   `json:"words"`
	Phrases []struct{ Text string }   `json:"phrases"`
	Batches []struct{ Source string } `json:"batches"`
}

func itoa(n int) string { return strconv.Itoa(n) }

// #196／#122：刪自訂詞也清掉指向它的學習資料（user.json）。經本機 API 或 IPC 刪詞後不再打字就結束，
// 結束前也要把 user.json 重寫，重啟後不留指向已刪除自訂詞的使用紀錄與更正。
func TestVocabRemoveRewritesUserStoreWithoutTyping(t *testing.T) {
	binary := buildServer(t)
	dir := t.TempDir()
	data, _ := filepath.Abs("../../testdata/toy")
	sock := filepath.Join(dir, "api.sock")
	user := filepath.Join(dir, "user.json")
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	if err := vocabapi.SaveConfig(dir, vocabapi.Config{Enabled: true, Port: port}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, settings.FileName), []byte(`{"schema":1,"selection":"statistical"}`), 0o600)
	var logs bytes.Buffer
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("引擎日誌：\n%s", logs.String())
		}
	})
	ipcCall := func(c net.Conn, env ipc.Envelope) ipc.Reply {
		t.Helper()
		if err := ipc.WriteFrame(c, env); err != nil {
			t.Fatal(err)
		}
		var r ipc.Reply
		if err := ipc.ReadFrame(c, &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	start := func() (*exec.Cmd, net.Conn) {
		t.Helper()
		server := exec.Command(binary, "-data", data, "-user", user, "-socket", sock)
		server.Stderr = &logs
		if err := server.Start(); err != nil {
			t.Fatal(err)
		}
		waitDial(t, sock)
		c, err := net.Dial("unix", sock)
		if err != nil {
			t.Fatal(err)
		}
		// IPC 回得了話時已經裝好結束訊號的處理（API 比它早開始監聽）：之後的 SIGTERM 會照常存檔再結束。
		ipcCall(c, ipc.Envelope{Op: ipc.OpHealth})
		return server, c
	}
	post := func(path, body string) int {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			tok, err := vocabapi.LoadToken(dir)
			if err == nil {
				req, _ := http.NewRequest("POST", "http://127.0.0.1:"+itoa(port)+path, strings.NewReader(body))
				req.Header.Set("Authorization", "Bearer "+tok.Value)
				req.Header.Set("Content-Type", "application/json")
				var resp *http.Response
				if resp, err = (&http.Client{Timeout: 3 * time.Second}).Do(req); err == nil {
					resp.Body.Close()
					return resp.StatusCode
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("連不上本機 API：%v", err)
			}
		}
	}
	stop := func(server *exec.Cmd, c net.Conn) {
		c.Close()
		server.Process.Signal(syscall.SIGTERM)
		server.Wait()
	}

	ipcVocab := func(c net.Conn, req engine.VocabRequest) {
		t.Helper()
		if r := ipcCall(c, ipc.Envelope{Op: ipc.OpVocab, Vocab: &req}); !r.OK || r.Vocab == nil || !r.Vocab.OK {
			t.Fatalf("%s：%+v", req.Op, r)
		}
	}
	// 本機 API（savingDoer）與設定程式走的 IPC（OnVocab）兩條路都要。
	for _, via := range []string{"api", "ipc"} {
		// 加詞、打出來提交：user.json 記下這個詞的使用紀錄。
		server, c := start()
		if via == "api" {
			if code := post("/v1/words", `{"words":[{"word":"麻婆","readings":["ㄇㄚˊ ㄆㄛˊ"]}]}`); code != 200 {
				t.Fatalf("加詞：%d", code)
			}
		} else {
			ipcVocab(c, engine.VocabRequest{Op: engine.VocabAdd, Words: []vocab.Entry{{Text: "麻婆", Readings: []string{"ㄇㄚˊ ㄆㄛˊ"}}}})
		}
		open := ipcCall(c, ipc.Envelope{Op: ipc.OpOpen, Version: proto.Version})
		for _, k := range "a86qi6" {
			ipcCall(c, ipc.Envelope{Op: ipc.OpHandle, Session: open.Session, Req: &proto.Request{Type: proto.Key, Key: string(k)}})
		}
		if r := ipcCall(c, ipc.Envelope{Op: ipc.OpHandle, Session: open.Session, Req: &proto.Request{Type: proto.Key, Key: proto.KeyEnter}}); r.Snapshot == nil || r.Snapshot.Commit != "麻婆" {
			t.Fatalf("%s 提交：%+v", via, r)
		}
		stop(server, c)
		if b, err := os.ReadFile(user); err != nil || !bytes.Contains(b, []byte(`"麻婆"`)) {
			t.Fatalf("%s 提交後 user.json 要有這個詞的使用紀錄：%v %s", via, err, b)
		}

		// 重啟後只刪詞，不打字就結束（SIGTERM）。
		server, c = start()
		if via == "api" {
			if code := post("/v1/words/remove", `{"words":["麻婆"]}`); code != 200 {
				t.Fatalf("刪詞：%d", code)
			}
		} else {
			ipcVocab(c, engine.VocabRequest{Op: engine.VocabRemove, Texts: []string{"麻婆"}})
		}
		stop(server, c)
		if b, err := os.ReadFile(user); err != nil || bytes.Contains(b, []byte(`麻婆"`)) {
			t.Fatalf("%s 刪詞後 user.json 要重寫、不留指向它的學習資料：%v %s", via, err, b)
		}
	}
}
