package neural

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// 布局：root/data（字典）、root/neural（worker、內附模型、內附 Python）、home（個人目錄）。
type findLayout struct{ root, data, neural, user, python string }

func newFindLayout(t *testing.T) findLayout {
	t.Helper()
	root := t.TempDir()
	l := findLayout{root: root, data: filepath.Join(root, "data"), neural: filepath.Join(root, "neural"),
		user: filepath.Join(root, "home"), python: filepath.Join(root, "usr", "bin", "python3")}
	os.MkdirAll(l.data, 0o755)
	return l
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func bundledPython(neural string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(neural, "python", "python.exe")
	}
	return filepath.Join(neural, "python", "bin", "python3")
}

const testDictionary = "91620d9640525e694afe417bd03e500a666441f72b59bc92c1913c83c17a3b9b"

func (l findLayout) find() Bundled {
	return Find(Options{DataDir: l.data, UserDir: l.user, DictionarySHA256: testDictionary, SystemPython: l.python})
}

// 官網安裝包：內附 Python、worker、模型都在 <data>/../neural；結果與 FindBundled 相同，不看系統 Python 與個人目錄。
func TestFindBundledLayoutUnchanged(t *testing.T) {
	l := newFindLayout(t)
	touch(t, bundledPython(l.neural))
	touch(t, l.python)
	for _, p := range []string{"sequence_runtime.py", "punct_runtime.py", "select/manifest.json", "punct/manifest.json"} {
		touch(t, filepath.Join(l.neural, p))
	}
	touch(t, filepath.Join(l.user, "models", testDictionary, "select", "manifest.json"))
	for _, b := range []Bundled{l.find(), FindBundled(l.data)} {
		if !b.HasSelect || !b.HasPunct || b.Python != bundledPython(l.neural) || b.PythonSource != SourceBundled ||
			b.SelectSource != SourceBundled || b.PunctSource != SourceBundled ||
			b.SelectPackage != filepath.Join(l.neural, "select") || b.SelectWorker != filepath.Join(l.neural, "sequence_runtime.py") ||
			b.PunctPackage != filepath.Join(l.neural, "punct") || b.PunctWorker != filepath.Join(l.neural, "punct_runtime.py") {
			t.Fatalf("bundled layout: %+v", b)
		}
	}
}

// 系統安裝（Debian）：沒有內附 Python 時用系統的 python3；模型放在個人目錄（#180），依字典雜湊分開。
func TestFindSystemPythonAndUserModels(t *testing.T) {
	l := newFindLayout(t)
	touch(t, l.python)
	touch(t, filepath.Join(l.neural, "sequence_runtime.py"))
	touch(t, filepath.Join(l.neural, "punct_runtime.py"))
	user := filepath.Join(l.user, "models", testDictionary)
	touch(t, filepath.Join(user, "select", "manifest.json"))
	b := l.find()
	if !b.HasSelect || b.Python != l.python || b.PythonSource != SourceSystem || b.SelectSource != SourceUser ||
		b.SelectPackage != filepath.Join(user, "select") || b.SelectWorker != filepath.Join(l.neural, "sequence_runtime.py") {
		t.Fatalf("system install with user model: %+v", b)
	}
	if b.HasPunct || b.PunctSource != "" || b.RuntimeMissing(true) {
		t.Fatalf("punct model is not installed: %+v", b)
	}
	// 只找安裝包內附時（官網安裝包的規則），系統 Python 與個人目錄都不算。
	if fb := FindBundled(l.data); fb.HasSelect || fb.SelectSource != "" || fb.PythonSource != "" {
		t.Fatalf("FindBundled must ignore system python and user models: %+v", fb)
	}
	// 換了字典（雜湊不同）：舊配對不會被找到。
	other := Find(Options{DataDir: l.data, UserDir: l.user, DictionarySHA256: strings.Repeat("0", 64), SystemPython: l.python})
	if other.HasSelect || other.SelectSource != "" {
		t.Fatalf("model paired with another dictionary was used: %+v", other)
	}
	// 安裝包內建優先於個人目錄（發行版另外裝的模型套件）。
	touch(t, filepath.Join(l.neural, "select", "manifest.json"))
	if b := l.find(); b.SelectSource != SourceBundled || b.SelectPackage != filepath.Join(l.neural, "select") {
		t.Fatalf("bundled model must win over user model: %+v", b)
	}
}

// 有模型與 worker，卻沒有 Python：不啟動 worker，回報缺少推論環境。
func TestFindRuntimeMissing(t *testing.T) {
	l := newFindLayout(t)
	touch(t, filepath.Join(l.neural, "sequence_runtime.py"))
	touch(t, filepath.Join(l.user, "models", testDictionary, "select", "manifest.json"))
	b := l.find()
	if b.HasSelect || b.Python != "" || b.PythonSource != "" || b.SelectSource != SourceUser || !b.RuntimeMissing(false) {
		t.Fatalf("missing python: %+v", b)
	}
	// 沒有 worker 腳本時模型也用不了：視同沒有安裝，不是缺 Python。
	os.Remove(filepath.Join(l.neural, "sequence_runtime.py"))
	if b := l.find(); b.SelectSource != "" || b.RuntimeMissing(false) {
		t.Fatalf("missing worker: %+v", b)
	}
}

// 缺少 numpy／onnxruntime 是持續的狀態：回報 runtime_missing，十分鐘才重試一次，不每幾秒啟動 Python。
func TestSupervisorRuntimeMissingStateAndSlowRetry(t *testing.T) {
	load := func(Config) (Manifest, error) { return Manifest{}, nil }
	attempts := 0
	s, err := openSupervisedAsync(Config{}, load, func(c Config) (*Worker, error) {
		attempts++
		return nil, &StartupError{ReasonRuntimeMissing, "neural worker"}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for deadline := time.Now().Add(5 * time.Second); s.State() != "runtime_missing"; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("state %q", s.State())
		}
	}
	s.mu.Lock()
	s.Cooldown = 0
	s.mu.Unlock()
	for i := 0; i < 3; i++ {
		if _, err := s.Score(request()); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("score: %v", err)
		}
	}
	time.Sleep(50 * time.Millisecond)
	s.mu.Lock()
	n := attempts
	s.lastFailure = time.Now().Add(-RuntimeMissingRetry) // 十分鐘後
	s.mu.Unlock()
	if n != 1 {
		t.Fatalf("retried %d times within cooldown", n-1)
	}
	s.Score(request())
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
		s.mu.Lock()
		n, starting := attempts, s.starting
		s.mu.Unlock()
		if n == 2 && !starting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no retry after cooldown: attempts=%d", n)
		}
	}
}

func TestPunctRuntimeMissingState(t *testing.T) {
	dir, _ := punctPackage(t)
	missing := filepath.Join(t.TempDir(), "python3")
	w, err := openPunct(PunctConfig{Package: dir, Threads: 1, Timeout: 100 * time.Millisecond}, func() *exec.Cmd {
		return exec.Command(missing)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for deadline := time.Now().Add(5 * time.Second); w.State() != "runtime_missing"; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("state %q reason %q", w.State(), w.Reason())
		}
	}
	w.mu.Lock()
	w.stoppedAt = time.Now().Add(-10 * time.Second) // 一般的 3 秒冷卻已過
	w.mu.Unlock()
	if _, err := w.Punctuate("我想吃"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("punctuate: %v", err)
	}
	if w.State() != "runtime_missing" {
		t.Fatalf("retried before RuntimeMissingRetry: %s", w.State())
	}
}
