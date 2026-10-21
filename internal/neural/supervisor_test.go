package neural

import (
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"
)

func openModes(modes ...string) func(Config) (*Worker, error) {
	next := 0
	return func(Config) (*Worker, error) {
		mode := modes[min(next, len(modes)-1)]
		next++
		cmd := exec.Command(os.Args[0], "-test.run=^TestWorkerProcess$")
		cmd.Env = append(os.Environ(), "ZUIN_TEST_NEURAL_CHILD="+mode)
		return start(cmd, Manifest{Schema: "zuin-encoder-runtime-v1", ContextPolicy: "empty", DictionarySHA256: "dictionary",
			Files: map[string]string{"model.onnx": "model"}, Weight: .1}, 20*time.Millisecond)
	}
}

func waitRestart(t *testing.T, s *Supervisor, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		done := s.restarted >= want && s.current != nil
		s.mu.Unlock()
		if done {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("worker was not restarted")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSupervisorRestartsAfterTimeoutWithoutBlocking(t *testing.T) {
	s, err := openSupervised(Config{}, openModes("timeout", "success"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Cooldown = 0
	if _, err := s.Score(request()); !errors.Is(err, ErrTimeout) {
		t.Fatalf("expected timeout: %v", err)
	}
	started := time.Now()
	if _, err := s.Score(request()); !errors.Is(err, ErrUnavailable) || time.Since(started) > 50*time.Millisecond {
		t.Fatalf("restart blocked or was not reported unavailable: %v", err)
	}
	waitRestart(t, s, 1)
	if scores, err := s.Score(request()); err != nil || len(scores) != 2 {
		t.Fatalf("restarted worker: %v %v", scores, err)
	}
	st := s.Stats()
	if st.Timeouts != 1 || st.Scored != 1 || s.Restarts() != 1 {
		t.Fatalf("stats %+v restarted=%d", st, s.Restarts())
	}
	s.Close()
	if after := s.Stats(); after != st {
		t.Fatalf("stats lost on close: %+v → %+v", st, after)
	}
}

func TestSupervisorRespectsHourlyLimitAndClose(t *testing.T) {
	s, err := openSupervised(Config{}, openModes("crash"))
	if err != nil {
		t.Fatal(err)
	}
	s.Cooldown, s.MaxPerHour = 0, 2
	for i := 0; i < 20 && s.Restarts() < 2; i++ {
		s.Score(request())
		time.Sleep(20 * time.Millisecond)
	}
	waitRestart(t, s, 2)
	s.Score(request()) // 第三次崩潰後不再重啟
	time.Sleep(50 * time.Millisecond)
	for i := 0; i < 5; i++ {
		s.Score(request())
	}
	time.Sleep(50 * time.Millisecond)
	s.mu.Lock()
	restarts, current := s.restarted, s.current
	s.mu.Unlock()
	if restarts != 2 || current != nil {
		t.Fatalf("restart limit ignored: restarts=%d current=%v", restarts, current != nil)
	}
	s.Close()
	if _, err := s.Score(request()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("closed supervisor scored: %v", err)
	}
}

// 第一個 worker 在背景啟動：開啟立即返回，就緒前評分回 ErrUnavailable（保留統計引擎），就緒後正常評分。
func TestSupervisorAsyncStartDoesNotBlock(t *testing.T) {
	release := make(chan struct{})
	open := openModes("success")
	slow := func(c Config) (*Worker, error) { <-release; return open(c) }
	load := func(Config) (Manifest, error) {
		return Manifest{Schema: "zuin-encoder-runtime-v1", ContextPolicy: "empty", Weight: .1}, nil
	}
	s, err := openSupervisedAsync(Config{}, load, slow)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Options().Weight != .1 {
		t.Fatal("options must come from the manifest before the worker is ready")
	}
	if state := s.State(); state != "loading" {
		t.Fatalf("background start must report loading: %s", state)
	}
	if _, err := s.Score(request()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("before ready: %v", err)
	}
	close(release)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if _, err := s.Score(request()); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker never became ready")
		}
	}
	if s.Restarts() != 0 {
		t.Fatal("the first start is not a restart")
	}
	if state := s.State(); state != "ready" {
		t.Fatalf("ready worker: %s", state)
	}
	s.Close()
	if state := s.State(); state != "closed" {
		t.Fatalf("closed worker: %s", state)
	}
}

func TestSupervisorStateReportsStartupFailureAndRecovery(t *testing.T) {
	load := func(Config) (Manifest, error) { return Manifest{}, nil }
	attempts := 0
	open := openModes("success")
	s, err := openSupervisedAsync(Config{}, load, func(c Config) (*Worker, error) {
		attempts++
		if attempts == 1 {
			return nil, &StartupError{Reason: ReasonMissingRuntime, what: "neural worker"}
		}
		return open(c)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for deadline := time.Now().Add(5 * time.Second); s.State() != "fallback"; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("failed background startup did not report fallback")
		}
	}
	if reason := s.Reason(); reason != ReasonMissingRuntime {
		t.Fatalf("startup failure reason: %q", reason)
	}
	// 查詢不能觸發重啟；只有新的評分請求才使用既有重啟策略。
	for i := 0; i < 3; i++ {
		if state := s.State(); state != "fallback" {
			t.Fatalf("status query changed availability: %s", state)
		}
	}
	s.mu.Lock()
	s.Cooldown = 0
	s.mu.Unlock()
	if _, err := s.Score(request()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("recovery must preserve fallback for this request: %v", err)
	}
	waitRestart(t, s, 1)
	if state := s.State(); state != "ready" {
		t.Fatalf("recovered worker: %s", state)
	}
	if reason := s.Reason(); reason != "" {
		t.Fatalf("recovered worker kept reason %q", reason)
	}
}

func TestSupervisorStateDetectsExitBeforeNextScore(t *testing.T) {
	s, err := openSupervised(Config{}, openModes("success"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w := s.current
	if err := w.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-w.done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not exit")
	}
	if state := s.State(); state != "fallback" {
		t.Fatalf("exited worker still reported ready before next score: %s", state)
	}
}
