package neural

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestPunctProcess(t *testing.T) {
	mode := os.Getenv("ZUIN_TEST_PUNCT_CHILD")
	if mode == "" {
		return
	}
	fmt.Printf(`{"ready":true,"schema":%q,"model_sha256":%q}`+"\n", PunctSchema, os.Getenv("ZUIN_TEST_PUNCT_MODEL"))
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var r map[string]any
		_ = json.Unmarshal(scanner.Bytes(), &r)
		switch mode {
		case "timeout":
			time.Sleep(time.Second)
		case "wrong_id":
			r["id"] = 0
		}
		r["edits"] = []map[string]any{{"offset": 1, "text": "，"}}
		delete(r, "text") // 回覆不回送使用者文字
		b, _ := json.Marshal(r)
		fmt.Println(string(b))
	}
	os.Exit(0)
}

func punctPackage(t *testing.T) (string, string) {
	dir := t.TempDir()
	files := map[string]string{}
	for _, name := range []string{"model.onnx", "vocab.json"} {
		body := []byte(name)
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
		d := sha256.Sum256(body)
		files[name] = hex.EncodeToString(d[:])
	}
	b, _ := json.Marshal(map[string]any{"schema": PunctSchema, "files": files})
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, files["model.onnx"]
}

func openTestPunct(t *testing.T, mode string, timeout time.Duration) (*PunctWorker, error) {
	dir, model := punctPackage(t)
	w, err := openPunct(PunctConfig{Package: dir, Threads: 1, Timeout: timeout}, func() *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestPunctProcess$")
		cmd.Env = append(os.Environ(), "ZUIN_TEST_PUNCT_CHILD="+mode, "ZUIN_TEST_PUNCT_MODEL="+model)
		return cmd
	})
	if err == nil && !w.waitReady(10*time.Second) {
		t.Fatal("punct worker did not become ready")
	}
	return w, err
}

// 啟動在背景進行：開啟後立即可用，就緒前的提交照原文送出，不等待 Python 載入模型。
func TestPunctStartupDoesNotBlock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh to delay the fake worker")
	}
	dir, model := punctPackage(t)
	started := time.Now()
	w, err := openPunct(PunctConfig{Package: dir, Threads: 1, Timeout: 200 * time.Millisecond}, func() *exec.Cmd {
		cmd := exec.Command("sh", "-c", "sleep 1; exec \"$0\" -test.run=^TestPunctProcess$", os.Args[0])
		cmd.Env = append(os.Environ(), "ZUIN_TEST_PUNCT_CHILD=ok", "ZUIN_TEST_PUNCT_MODEL="+model)
		return cmd
	})
	if err != nil || time.Since(started) > 500*time.Millisecond {
		t.Fatalf("open blocked on worker startup: %v %v", err, time.Since(started))
	}
	if _, err = w.Punctuate("我想吃"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("before ready the commit must go through unchanged: %v", err)
	}
	if state := w.State(); state != "loading" {
		t.Fatalf("background start must report loading: %s", state)
	}
	if !w.waitReady(10 * time.Second) {
		t.Fatal("worker never became ready")
	}
	if edits, err := w.Punctuate("我想吃"); err != nil || len(edits) != 1 {
		t.Fatalf("after ready: %v %v", edits, err)
	}
	if state := w.State(); state != "ready" {
		t.Fatalf("ready punct worker: %s", state)
	}
	w.Close()
	if state := w.State(); state != "closed" {
		t.Fatalf("closed punct worker: %s", state)
	}
}

func TestPunctStateReportsStartupFailure(t *testing.T) {
	dir, _ := punctPackage(t)
	w, err := openPunct(PunctConfig{Package: dir, Threads: 1, Timeout: 100 * time.Millisecond}, func() *exec.Cmd {
		// 沒有 ready 握手，行程直接結束；模擬模型依賴無法載入。
		cmd := exec.Command(os.Args[0], "-test.run=^TestPunctProcess$")
		cmd.Env = append(os.Environ(), "ZUIN_TEST_PUNCT_CHILD=")
		return cmd
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for deadline := time.Now().Add(5 * time.Second); w.State() != "fallback"; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("failed background startup did not report fallback")
		}
	}
	if _, err := w.Punctuate("我想吃"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("failed worker should preserve original text: %v", err)
	}
	if reason := w.Reason(); reason != ReasonFailed {
		t.Fatalf("startup failure reason: %q", reason)
	}
}

func TestPunctStateDetectsExitBeforeNextCommit(t *testing.T) {
	w, err := openTestPunct(t, "ok", 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	p := w.proc
	if err := p.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		t.Fatal("punct worker did not exit")
	}
	if state := w.State(); state != "fallback" {
		t.Fatalf("exited punct worker still reported ready before next commit: %s", state)
	}
}

func TestPunctWorkerScoresAndFallsBack(t *testing.T) {
	w, err := openTestPunct(t, "ok", 200*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	edits, err := w.Punctuate("我想吃")
	if err != nil || len(edits) != 1 || edits[0].Offset != 1 || edits[0].Text != "，" {
		t.Fatalf("edits=%v err=%v", edits, err)
	}
	w.Close()

	w, err = openTestPunct(t, "timeout", 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Punctuate("我想吃"); !errors.Is(err, ErrTimeout) {
		t.Fatalf("timeout err=%v", err)
	}
	if _, err = w.Punctuate("我想吃"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("within cooldown the dead worker must stay unavailable: %v", err)
	}
	if s := w.Stats(); s.Timeouts != 1 || s.Unavailable != 1 {
		t.Fatalf("stats %+v", s)
	}
	w.Close()

	w, err = openTestPunct(t, "wrong_id", 200*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Punctuate("我想吃"); err == nil {
		t.Fatal("mismatched reply id must be rejected")
	}
	w.Close()

	if _, err = OpenPunct(PunctConfig{Timeout: time.Second, Threads: 1}); err == nil {
		t.Fatal("timeout above 500ms must be rejected")
	}
}
