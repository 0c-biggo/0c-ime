//go:build !windows

package main

import (
	"bytes"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.biggo.com/0c/ime/bridge/ipc"
	"git.biggo.com/0c/ime/core/proto"
	"git.biggo.com/0c/ime/internal/userlock"
)

// lockClient 是測試用的最小外殼：一條連線、一個 session。
type lockClient struct {
	t       *testing.T
	c       net.Conn
	session uint64
}

func dialClient(t *testing.T, sock string) *lockClient {
	t.Helper()
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return &lockClient{t: t, c: c}
}

func (l *lockClient) call(env ipc.Envelope) ipc.Reply {
	l.t.Helper()
	l.c.SetDeadline(time.Now().Add(5 * time.Second))
	if err := ipc.WriteFrame(l.c, env); err != nil {
		l.t.Fatal(err)
	}
	var r ipc.Reply
	if err := ipc.ReadFrame(l.c, &r); err != nil {
		l.t.Fatal(err)
	}
	return r
}

func (l *lockClient) health() map[string]string {
	return l.call(ipc.Envelope{Op: ipc.OpHealth}).Info
}

// learn 提交「麻婆」並明確刪除一筆學習；刪除學習是立即存檔（不等停止打字），用來觀察有沒有寫入。
func (l *lockClient) learn(forget string) {
	l.t.Helper()
	if l.session == 0 {
		l.session = l.call(ipc.Envelope{Op: ipc.OpOpen, Version: proto.Version}).Session
	}
	for _, k := range "a86qi6" {
		l.call(ipc.Envelope{Op: ipc.OpHandle, Session: l.session, Req: &proto.Request{Type: proto.Key, Key: string(k)}})
	}
	if r := l.call(ipc.Envelope{Op: ipc.OpHandle, Session: l.session, Req: &proto.Request{Type: proto.Key, Key: proto.KeyEnter}}); r.Snapshot == nil || r.Snapshot.Commit != "麻婆" {
		l.t.Fatalf("輸入應照常運作：%+v", r)
	}
	l.call(ipc.Envelope{Op: ipc.OpHandle, Session: l.session, Req: &proto.Request{Type: proto.Forget, Word: forget}})
}

func startServer(t *testing.T, binary string, args ...string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	var logs bytes.Buffer
	cmd.Stderr = &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			cmd.Process.Signal(os.Interrupt)
			cmd.Wait()
		}
	})
	return cmd, &logs
}

func waitExit(t *testing.T, cmd *exec.Cmd, within time.Duration) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(within):
		cmd.Process.Kill()
		<-done
		t.Fatalf("引擎 %v 內沒有結束", within)
		return nil
	}
}

func fileContains(path, s string) bool {
	b, err := os.ReadFile(path)
	return err == nil && bytes.Contains(b, []byte(s))
}

// #120：兩個引擎用不同端點、同一份個人檔時（例如不同參數叫起的第二個引擎），拿不到鎖的那個
// 照常輸入、health 回報 busy，但絕不寫入 user.json，以免後存的蓋掉先存的。
func TestSecondEngineWithoutLockNeverWritesUserStore(t *testing.T) {
	binary := buildServer(t)
	dir := t.TempDir()
	data, _ := filepath.Abs("../../testdata/toy")
	user := filepath.Join(dir, "user.json")
	first := filepath.Join(dir, "first.sock")
	second := filepath.Join(dir, "second.sock")
	startServer(t, binary, "-data", data, "-user", user, "-socket", first)
	waitDial(t, first)
	b, logs := startServer(t, binary, "-data", data, "-user", user, "-socket", second, "-lock-wait", "200ms")
	waitDial(t, second)

	writer, reader := dialClient(t, first), dialClient(t, second)
	if got := writer.health()["personal_lock"]; got != "held" {
		t.Fatalf("第一個引擎應持有個人資料鎖：%q", got)
	}
	if got := reader.health()["personal_lock"]; got != "busy" {
		t.Fatalf("第二個引擎應回報拿不到鎖：%q", got)
	}
	reader.learn("第二個引擎")
	time.Sleep(2 * saveTick)
	if fileContains(user, "第二個引擎") {
		t.Fatal("拿不到鎖的引擎寫入了個人檔")
	}
	// 結束時也不寫。
	b.Process.Signal(os.Interrupt)
	waitExit(t, b, 10*time.Second)
	if fileContains(user, "第二個引擎") {
		t.Fatal("拿不到鎖的引擎結束時寫入了個人檔")
	}
	if !strings.Contains(logs.String(), "不存檔") {
		t.Errorf("應記錄不存檔的原因：%s", logs.String())
	}
	// 持有鎖的引擎照常存檔。
	writer.learn("第一個引擎")
	for deadline := time.Now().Add(3 * time.Second); !fileContains(user, "第一個引擎"); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("持有鎖的引擎沒有存檔")
		}
	}
}

// 拿鎖在載入資料包之前：鎖被別人持有時先等，期間若端點已有人服務（同時啟動的另一個引擎先載入完成），
// 直接讓位結束，不白載資料包。這裡的資料包路徑不存在，載入就會失敗結束（結束碼非 0）。
func TestWaitingEngineYieldsBeforeLoadingData(t *testing.T) {
	binary := buildServer(t)
	dir := t.TempDir()
	held, err := userlock.TryLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	sock := filepath.Join(dir, "0c.sock")
	cmd, logs := startServer(t, binary, "-data", filepath.Join(dir, "missing-data"), "-user", filepath.Join(dir, "user.json"),
		"-socket", sock, "-lock-wait", "10s")
	time.Sleep(300 * time.Millisecond)
	if cmd.ProcessState != nil {
		t.Fatalf("鎖被持有時應等待，不該已結束：%s", logs.String())
	}
	winner, err := net.Listen("unix", sock) // 另一個引擎載入完成、開始服務
	if err != nil {
		t.Fatal(err)
	}
	defer winner.Close()
	go func() {
		for {
			c, err := winner.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	if err := waitExit(t, cmd, 5*time.Second); err != nil {
		t.Fatalf("端點已有人服務時應正常讓位（結束碼 0）：%v\n%s", err, logs.String())
	}
	if strings.Contains(logs.String(), "載入資料包") {
		t.Fatalf("讓位前不該載入資料包：%s", logs.String())
	}
}

// 交接：舊引擎關閉端點後還要做最後一次存檔才放鎖。新引擎在鎖放開前不服務也不讀個人檔，
// 放開後才載入、成為唯一的寫入者。
func TestEngineWaitsForLockBeforeServingAndThenWrites(t *testing.T) {
	binary := buildServer(t)
	dir := t.TempDir()
	data, _ := filepath.Abs("../../testdata/toy")
	user := filepath.Join(dir, "user.json")
	held, err := userlock.TryLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "0c.sock")
	startServer(t, binary, "-data", data, "-user", user, "-socket", sock, "-lock-wait", "10s")
	for deadline := time.Now().Add(700 * time.Millisecond); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if c, err := net.Dial("unix", sock); err == nil {
			c.Close()
			held.Release()
			t.Fatal("鎖還在舊引擎手上時，新引擎不該開始服務")
		}
	}
	// 舊引擎最後一次存檔：新引擎必須讀到這份。
	if err := os.WriteFile(user, []byte(`{"Format":4,"Tombstones":{"舊引擎":true}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	held.Release()
	waitDial(t, sock)
	c := dialClient(t, sock)
	if got := c.health()["personal_lock"]; got != "held" {
		t.Fatalf("鎖放開後新引擎應持有：%q", got)
	}
	c.learn("新引擎")
	for deadline := time.Now().Add(3 * time.Second); !fileContains(user, "新引擎"); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("新引擎沒有存檔")
		}
	}
	if !fileContains(user, "舊引擎") {
		b, _ := os.ReadFile(user)
		t.Fatalf("新引擎應在舊引擎放鎖後才讀個人檔，保留舊引擎最後存的學習：%s", b)
	}
}

// 端點被移除（例如登出時執行期目錄被清掉）或被別的引擎取代時，這個引擎已經沒有新外殼連得到：
// 存檔後結束並放鎖，不留下占著鎖的孤兒；取代者的 socket 不能被它刪掉。
func TestEngineExitsWhenItsSocketIsReplaced(t *testing.T) {
	binary := buildServer(t)
	dir := t.TempDir()
	data, _ := filepath.Abs("../../testdata/toy")
	user := filepath.Join(dir, "user.json")
	sock := filepath.Join(dir, "0c.sock")
	cmd, logs := startServer(t, binary, "-data", data, "-user", user, "-socket", sock)
	waitDial(t, sock)
	c := dialClient(t, sock)
	c.session = c.call(ipc.Envelope{Op: ipc.OpOpen, Version: proto.Version}).Session
	for _, k := range "a86qi6" {
		c.call(ipc.Envelope{Op: ipc.OpHandle, Session: c.session, Req: &proto.Request{Type: proto.Key, Key: string(k)}})
	}
	c.call(ipc.Envelope{Op: ipc.OpHandle, Session: c.session, Req: &proto.Request{Type: proto.Key, Key: proto.KeyEnter}})
	if err := os.Remove(sock); err != nil {
		t.Fatal(err)
	}
	replacement, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if err := waitExit(t, cmd, 5*time.Second); err != nil {
		t.Fatalf("%v\n%s", err, logs.String())
	}
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("取代者的 socket 被刪掉了：%v", err)
	}
	// 提交後 10 秒內不會排程存檔（#129），個人檔只可能是結束前的最後一次存檔寫出的。
	if _, err := os.Stat(user); err != nil {
		t.Fatalf("結束前應存檔：%v\n%s", err, logs.String())
	}
	l, err := userlock.TryLock(dir)
	if err != nil {
		t.Fatalf("結束後應已放鎖：%v", err)
	}
	l.Release()
}
