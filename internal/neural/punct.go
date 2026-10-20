package neural

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/0c-biggo/0c-ime/core/punct"
)

// PunctSchema 是提交時補標點 worker 的套件契約（models punct_runtime.py）。
const PunctSchema = "zuin-punct-runtime-v1"

// PunctConfig：Timeout 是單次提交可等待的上限；逾時照原文提交並停掉 worker。
type PunctConfig struct {
	Python, Script, Package string
	Threads                 int
	Timeout                 time.Duration
}

// PunctWorker 是有界的補標點評分器。worker 的啟動（載入 Python 與模型）一律在背景進行，
// 啟動完成前與失效後的提交照原文送出；失效後冷卻 3 秒再重啟，每小時至多 30 次。
type PunctWorker struct {
	mu        sync.Mutex
	cfg       PunctConfig
	model     string
	proc      *punctProc // nil：尚未就緒或已失效
	starting  bool
	closed    bool
	next      uint64
	stats     Stats
	restarts  []time.Time
	stoppedAt time.Time
	reason    string // 最近一次啟動失敗的原因代碼；成功啟動後清空
	command   func() *exec.Cmd
	Log       func(string, ...any)
}

type punctProc struct {
	cmd    *exec.Cmd
	input  io.WriteCloser
	output *bufio.Scanner
	done   chan struct{}
}

func OpenPunct(c PunctConfig) (*PunctWorker, error) {
	return openPunct(c, func() *exec.Cmd {
		return pythonCommand(c.Python, c.Script, "--package", c.Package, "--threads", fmt.Sprint(c.Threads))
	})
}

// openPunct 同步核對套件，worker 在背景啟動；引擎不必等 Python 載入模型才開始服務。
func openPunct(c PunctConfig, command func() *exec.Cmd) (*PunctWorker, error) {
	if c.Timeout <= 0 || c.Timeout > 500*time.Millisecond {
		return nil, errors.New("punct timeout must be in (0,500ms]")
	}
	if c.Threads < 1 || c.Threads > MaxThreads {
		return nil, errors.New("invalid punct threads")
	}
	b, err := os.ReadFile(filepath.Join(c.Package, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var m struct {
		Schema string            `json:"schema"`
		Files  map[string]string `json:"files"`
	}
	if err = json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m.Schema != PunctSchema {
		return nil, errors.New("punct manifest contract mismatch")
	}
	for _, name := range []string{"model.onnx", "vocab.json"} {
		digest, e := fileSHA(filepath.Join(c.Package, name))
		if e != nil {
			return nil, e
		}
		if digest != m.Files[name] {
			return nil, errors.New("punct package hash mismatch")
		}
	}
	w := &PunctWorker{cfg: c, model: m.Files["model.onnx"], command: command}
	w.mu.Lock()
	w.startLocked(false)
	w.mu.Unlock()
	return w, nil
}

// startLocked 需持有 w.mu：在背景啟動 worker，完成後才接手請求。
func (w *PunctWorker) startLocked(restart bool) {
	if w.starting || w.closed {
		return
	}
	w.starting = true
	go func() {
		p, err := launchPunct(w.command, w.model)
		w.mu.Lock()
		defer w.mu.Unlock()
		w.starting = false
		if err != nil {
			w.stoppedAt = time.Now()
			w.reason = startupReason(err)
			if w.Log != nil {
				w.Log("補標點 worker 啟動失敗，照原文提交：%v", err)
			}
			return
		}
		if w.closed {
			p.kill()
			return
		}
		w.proc = p
		w.reason = ""
		if restart && w.Log != nil {
			w.Log("補標點 worker 已重啟（一小時內第 %d 次）", len(w.restarts))
		}
	}()
}

func launchPunct(command func() *exec.Cmd, model string) (*punctProc, error) {
	cmd := command()
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		input.Close()
		return nil, err
	}
	stderr := &startupStderr{} // 只在就緒前收集；traceback 可能含使用者文字，就緒後丟棄
	cmd.Stderr = stderr
	if err = cmd.Start(); err != nil {
		input.Close()
		output.Close()
		return nil, err
	}
	p := &punctProc{cmd: cmd, input: input, output: bufio.NewScanner(output), done: make(chan struct{})}
	p.output.Buffer(make([]byte, 4096), frameLimit)
	go func() { _ = cmd.Wait(); close(p.done) }()
	b, err := p.exchange(nil, 20*time.Second)
	var ready struct {
		Ready  bool   `json:"ready"`
		Schema string `json:"schema"`
		Model  string `json:"model_sha256"`
	}
	if err == nil {
		err = json.Unmarshal(b, &ready)
	}
	if err != nil || !ready.Ready || ready.Schema != PunctSchema || ready.Model != model {
		p.kill()
		awaitExit(p.done)
		return nil, startupError("punct worker", err, stderr.finish())
	}
	stderr.finish()
	return p, nil
}

func (p *punctProc) kill() {
	_ = p.input.Close()
	_ = p.cmd.Process.Kill()
}

// stopLocked 需持有 w.mu：停掉目前的 worker，冷卻後才可重啟。
func (w *PunctWorker) stopLocked() {
	if w.proc != nil {
		w.proc.kill()
		w.proc = nil
	}
	w.stoppedAt = time.Now()
}

// exchange 與 Worker.exchange 相同：至多一個 I/O goroutine，逾時殺掉 worker，不排隊、不可晚套用。
func (p *punctProc) exchange(request []byte, timeout time.Duration) ([]byte, error) {
	type result struct {
		b   []byte
		err error
	}
	ch := make(chan result, 1)
	input, output := p.input, p.output
	go func() {
		if request != nil {
			if _, err := input.Write(request); err != nil {
				ch <- result{err: err}
				return
			}
		}
		if !output.Scan() {
			err := output.Err()
			if err == nil {
				err = io.EOF
			}
			ch <- result{err: err}
			return
		}
		ch <- result{b: append([]byte(nil), output.Bytes()...)}
	}()
	select {
	case r := <-ch:
		return r.b, r.err
	case <-time.After(timeout):
		p.kill()
		return nil, ErrTimeout
	}
}

// reviveLocked 需持有 w.mu：冷卻與每小時上限內在背景重啟；本次請求一律照原文提交。
func (w *PunctWorker) reviveLocked() {
	now := time.Now()
	if w.starting || now.Sub(w.stoppedAt) < retryCooldown(w.reason, 3*time.Second) {
		return
	}
	kept := w.restarts[:0]
	for _, t := range w.restarts {
		if now.Sub(t) < time.Hour {
			kept = append(kept, t)
		}
	}
	w.restarts = kept
	if len(w.restarts) >= 30 {
		return
	}
	w.restarts = append(w.restarts, now)
	w.startLocked(true)
}

func (w *PunctWorker) Punctuate(text string) ([]punct.Edit, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stats.Calls++
	if w.proc == nil {
		w.reviveLocked()
		w.stats.Unavailable++
		return nil, ErrUnavailable
	}
	started := time.Now()
	defer func() { w.stats.TotalUS += time.Since(started).Microseconds() }()
	w.next++
	req, err := json.Marshal(struct {
		ID   uint64 `json:"id"`
		Text string `json:"text"`
	}{w.next, text})
	if err != nil || len(req)+1 > frameLimit {
		w.stats.Errors++
		return nil, errors.New("punct request too large")
	}
	b, err := w.proc.exchange(append(req, '\n'), w.cfg.Timeout)
	if errors.Is(err, ErrTimeout) {
		w.stats.Timeouts++
		w.stopLocked()
		return nil, err
	}
	var reply struct {
		ID    uint64       `json:"id"`
		Edits []punct.Edit `json:"edits"`
		Error string       `json:"error"`
	}
	if err == nil {
		err = json.Unmarshal(b, &reply)
	}
	if err != nil || reply.ID != w.next {
		w.stats.Errors++
		w.stopLocked()
		return nil, errors.New("punct worker protocol error")
	}
	if reply.Error != "" {
		w.stats.Errors++
		return nil, errors.New(reply.Error)
	}
	w.stats.Scored++
	return reply.Edits, nil
}

func (w *PunctWorker) Stats() Stats { w.mu.Lock(); defer w.mu.Unlock(); return w.stats }

// SetLog 設定紀錄函式；背景啟動可能同時讀取，須經由鎖設定。
func (w *PunctWorker) SetLog(f func(string, ...any)) { w.mu.Lock(); w.Log = f; w.mu.Unlock() }

// State 回報當下模型可用性；不把已建立但尚未就緒的 worker 視為啟用。
func (w *PunctWorker) State() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return "closed"
	}
	if w.proc != nil {
		select {
		case <-w.proc.done:
			return "fallback"
		default:
			return "ready"
		}
	}
	if w.starting {
		return "loading"
	}
	if w.reason == ReasonRuntimeMissing {
		return "runtime_missing"
	}
	return "fallback"
}

// Reason 回傳最近一次啟動失敗的原因代碼（StartupError.Reason）；沒有失敗或已恢復時為空字串。
func (w *PunctWorker) Reason() string { w.mu.Lock(); defer w.mu.Unlock(); return w.reason }

// waitReady 供測試等待背景啟動完成。
func (w *PunctWorker) waitReady(timeout time.Duration) bool {
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		w.mu.Lock()
		ready := w.proc != nil
		w.mu.Unlock()
		if ready {
			return true
		}
	}
	return false
}

func (w *PunctWorker) Close() {
	w.mu.Lock()
	w.closed = true
	p := w.proc
	w.proc = nil
	w.mu.Unlock()
	if p != nil {
		p.kill()
		awaitExit(p.done)
	}
}
