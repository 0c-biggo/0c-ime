// Package neural 是本機 ONNX worker 的有界宿主。任何錯誤停用 worker，保留統計引擎。
package neural

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/0c-biggo/0c-ime/core/rerank"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

const frameLimit = 65536

var ErrUnavailable = errors.New("neural worker unavailable")
var ErrTimeout = errors.New("neural deadline exceeded")

// StartupError 是 worker 沒有回報就緒的原因，給狀態摘要與設定程式顯示（#80：精簡版 Windows 缺 C++ 執行階段時，
// 使用者原本只看到「模型無法使用」）。Reason 是固定代碼，不含輸出內容。
type StartupError struct{ Reason, what string }

func (e *StartupError) Error() string { return e.what + " startup failed: " + e.Reason }

const (
	ReasonMissingRuntime = "missing_runtime" // Python 擴充模組載入失敗：缺少系統 DLL
	ReasonTimeout        = "timeout"         // 沒有在時限內回報就緒
	ReasonFailed         = "failed"          // 其他原因：結束、回應不符
	// ReasonRuntimeMissing：找不到 Python，或 Python 匯入不到 numpy、onnxruntime（Linux 系統安裝沒裝
	// python3-numpy、python3-onnxruntime，#181）。health 的狀態直接報 runtime_missing，不當成模型故障。
	ReasonRuntimeMissing = "runtime_missing"
)

// startupStderr 只收集 worker 回報就緒前的 stderr：此時還沒送出任何輸入文字，內容只會是載入錯誤；
// 就緒後的輸出一律丟棄（traceback 可能含使用者文字）。只留前 8 KiB，用來判斷原因，不寫入紀錄。
type startupStderr struct {
	mu   sync.Mutex
	buf  []byte
	done bool
}

func (s *startupStderr) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.done {
		s.buf = append(s.buf, p[:min(len(p), 8192-len(s.buf))]...)
	}
	return len(p), nil
}

// finish 停止收集並回傳已收集的內容。
func (s *startupStderr) finish() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.done = true
	text := string(s.buf)
	s.buf = nil
	return text
}

// awaitExit 等 worker 結束、stderr 讀完再判斷原因；有子行程握著管線時不無限等待。
func awaitExit(done <-chan struct{}) {
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
}

// startupError 依 exchange 的錯誤與 worker 的 stderr 分類；done 關閉後 stderr 才完整。
func startupError(what string, err error, stderr string) *StartupError {
	switch {
	case strings.Contains(stderr, "DLL load failed"):
		return &StartupError{ReasonMissingRuntime, what}
	case strings.Contains(stderr, "No module named 'numpy'") || strings.Contains(stderr, "No module named 'onnxruntime'"):
		return &StartupError{ReasonRuntimeMissing, what}
	case errors.Is(err, ErrTimeout):
		return &StartupError{ReasonTimeout, what}
	}
	return &StartupError{ReasonFailed, what}
}

// startupReason 回傳 StartupError 的代碼；找不到 Python 執行檔是 runtime_missing，其他錯誤歸為 failed。
func startupReason(err error) string {
	var startup *StartupError
	if errors.As(err, &startup) {
		return startup.Reason
	}
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		return ReasonRuntimeMissing
	}
	return ReasonFailed
}

// RuntimeMissingRetry 是缺少推論環境時重試啟動 worker 的間隔：使用者裝好 python3-onnxruntime 後
// 不必重啟引擎，下一次重試就會載入；間隔夠長，缺套件時不會每幾秒啟動一次 Python。
const RuntimeMissingRetry = 10 * time.Minute

// retryCooldown 是啟動失敗後到下一次重試的冷卻。
func retryCooldown(reason string, normal time.Duration) time.Duration {
	if reason == ReasonRuntimeMissing {
		return RuntimeMissingRetry
	}
	return normal
}

type Config struct {
	Python, Script, Package, DictionarySHA256 string
	Threads                                   int
	Timeout                                   time.Duration
}
type Manifest struct {
	AllowPartial     bool              `json:"allow_partial"`
	Schema           string            `json:"schema"`
	ContextPolicy    string            `json:"context_policy"`
	DictionarySHA256 string            `json:"dictionary_sha256"`
	Weight           float64           `json:"weight"`
	Margin           float64           `json:"margin"`
	HintGap          float64           `json:"hint_gap"`
	TextWeight       float64           `json:"text_weight"`
	TextMargin       float64           `json:"text_margin"`
	NeutralExpand    float64           `json:"neutral_expand_penalty"`
	NeutralTolerant  []string          `json:"neutral_tolerant_last"`
	Files            map[string]string `json:"files"`
}

// neutralBases 只在套件宣告 neutral_expand_penalty 時啟用候選擴充；清單為一聲（無聲調符號）的基底。
func neutralBases(m Manifest) map[zhuyin.Syllable]bool {
	if m.NeutralExpand <= 0 {
		return nil
	}
	out := map[zhuyin.Syllable]bool{}
	for _, y := range m.NeutralTolerant {
		out[zhuyin.Syllable(y).Base()] = true
	}
	return out
}

type Stats struct {
	Calls       int   `json:"calls"`
	Scored      int   `json:"scored"`
	Timeouts    int   `json:"timeouts"`
	Errors      int   `json:"errors"`
	Unavailable int   `json:"unavailable"`
	TotalUS     int64 `json:"total_us"`
}
type Worker struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	input    io.WriteCloser
	output   *bufio.Scanner
	dead     bool
	done     chan struct{}
	manifest Manifest
	timeout  time.Duration
	next     uint64
	stats    Stats
}

func fileSHA(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// 套件契約：encoder 不看前文；序列模型只收外殼確認過的自身提交文字（最多 64 字）。
var contracts = map[string]string{
	"zuin-encoder-runtime-v1":  "empty",
	"zuin-sequence-runtime-v1": "confirmed_left_64",
}

func validContract(m Manifest) bool { return m.Schema != "" && contracts[m.Schema] == m.ContextPolicy }

func contextRunes(m Manifest) int {
	if m.ContextPolicy == "confirmed_left_64" {
		return rerank.MaxContextRunes
	}
	return 0
}

func Open(c Config) (*Worker, error) {
	m, err := loadManifest(c)
	if err != nil {
		return nil, err
	}
	cmd := pythonCommand(c.Python, c.Script, "--package", c.Package, "--dictionary", c.DictionarySHA256, "--threads", fmt.Sprint(c.Threads))
	return start(cmd, m, c.Timeout)
}

// loadManifest 核對設定、套件契約、字典配對與模型雜湊；不啟動 worker。
func loadManifest(c Config) (Manifest, error) {
	var m Manifest
	if c.Timeout <= 0 || c.Timeout > 100*time.Millisecond {
		return m, errors.New("neural timeout must be in (0,100ms]")
	}
	if c.Threads < 1 || c.Threads > MaxThreads {
		return m, errors.New("invalid neural threads")
	}
	b, err := os.ReadFile(filepath.Join(c.Package, "manifest.json"))
	if err != nil {
		return m, err
	}
	if err = json.Unmarshal(b, &m); err != nil {
		return m, err
	}
	if !validContract(m) || len(m.DictionarySHA256) != 64 || m.DictionarySHA256 != c.DictionarySHA256 || !(m.Weight > 0) || math.IsInf(m.Weight, 0) || !(m.Margin >= 0) || math.IsInf(m.Margin, 0) || !(m.HintGap >= 0) || math.IsInf(m.HintGap, 0) ||
		!(m.TextWeight >= 0) || math.IsInf(m.TextWeight, 0) || !(m.TextMargin >= 0) || math.IsInf(m.TextMargin, 0) ||
		!(m.NeutralExpand >= 0) || math.IsInf(m.NeutralExpand, 0) || m.NeutralExpand > 0 && len(m.NeutralTolerant) == 0 {
		return m, errors.New("neural manifest contract mismatch")
	}
	for _, name := range []string{"model.onnx", "vocab.json"} {
		digest, e := fileSHA(filepath.Join(c.Package, name))
		if e != nil {
			return m, e
		}
		if digest != m.Files[name] {
			return m, errors.New("neural package hash mismatch")
		}
	}
	return m, nil
}

// pythonCommand 以隔離模式啟動 worker：不讀使用者的 PYTHON* 環境變數與 user site；
// 不寫 bytecode（內附 Python 已預先編譯，安裝後的檔案必須維持與收據相同）；
// stdio 一律 UTF-8（Windows 管道預設是系統碼頁，中文會變亂碼）；Windows 不開主控台視窗。
func pythonCommand(python, script string, args ...string) *exec.Cmd {
	cmd := exec.Command(python, append([]string{"-I", "-B", "-X", "utf8", script}, args...)...)
	hideWindow(cmd)
	return cmd
}
func start(cmd *exec.Cmd, m Manifest, timeout time.Duration) (*Worker, error) {
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		input.Close()
		return nil, err
	}
	// stdout 是協定；stderr 只在就緒前收集來判斷啟動失敗的原因（見 startupStderr）。
	stderr := &startupStderr{}
	cmd.Stderr = stderr
	if err = cmd.Start(); err != nil {
		input.Close()
		output.Close()
		return nil, err
	}
	w := &Worker{cmd: cmd, input: input, output: bufio.NewScanner(output), manifest: m, timeout: timeout, done: make(chan struct{})}
	w.output.Buffer(make([]byte, 4096), frameLimit)
	go func() { _ = cmd.Wait(); close(w.done) }()
	b, err := w.exchange(nil, 10*time.Second)
	var ready struct {
		AllowPartial bool   `json:"allow_partial"`
		Ready        bool   `json:"ready"`
		Schema       string `json:"schema"`
		Model        string `json:"model_sha256"`
		Dictionary   string `json:"dictionary_sha256"`
	}
	if err == nil {
		err = json.Unmarshal(b, &ready)
	}
	if err != nil || !ready.Ready || ready.Schema != m.Schema || ready.Model != m.Files["model.onnx"] || ready.Dictionary != m.DictionarySHA256 || ready.AllowPartial != m.AllowPartial {
		w.stop()
		awaitExit(w.done)
		return nil, startupError("neural worker", err, stderr.finish())
	}
	stderr.finish()
	return w, nil
}

// exchange 至多有一個 I/O goroutine。逾時關閉管線並殺掉 worker，不排隊、不可晚套用。
func (w *Worker) exchange(request []byte, timeout time.Duration) ([]byte, error) {
	type result struct {
		b   []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		if request != nil {
			if _, err := w.input.Write(request); err != nil {
				done <- result{err: err}
				return
			}
		}
		if !w.output.Scan() {
			err := w.output.Err()
			if err == nil {
				err = io.EOF
			}
			done <- result{err: err}
			return
		}
		done <- result{b: append([]byte(nil), w.output.Bytes()...)}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-done:
		return r.b, r.err
	case <-timer.C:
		w.stop()
		return nil, ErrTimeout
	}
}
func (w *Worker) stop() {
	if !w.dead {
		w.dead = true
		_ = w.input.Close()
		_ = w.cmd.Process.Kill()
	}
}
func (w *Worker) Close() { w.mu.Lock(); w.stop(); w.mu.Unlock(); <-w.done }
func (w *Worker) Options() rerank.Options {
	return rerank.Options{Scorer: w, Weight: w.manifest.Weight, Margin: w.manifest.Margin, AllowPartial: w.manifest.AllowPartial,
		ContextRunes: contextRunes(w.manifest), HintGap: w.manifest.HintGap, TextWeight: w.manifest.TextWeight, TextMargin: w.manifest.TextMargin,
		NeutralExpand: w.manifest.NeutralExpand, NeutralBases: neutralBases(w.manifest)}
}
func (w *Worker) Stats() Stats { w.mu.Lock(); defer w.mu.Unlock(); return w.stats }
func (w *Worker) Dead() bool   { w.mu.Lock(); defer w.mu.Unlock(); return w.dead }
func (w *Worker) Score(r rerank.Request) ([]float64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stats.Calls++
	if w.dead {
		w.stats.Unavailable++
		return nil, ErrUnavailable
	}
	if !rerank.Supported(r) || r.AllowPartial != w.manifest.AllowPartial || (r.Mode == rerank.ModeText && (w.manifest.TextWeight <= 0 || contextRunes(w.manifest) == 0)) {
		return nil, errors.New("unsupported neural request")
	}
	if contextRunes(w.manifest) == 0 {
		r.Context = "" // 未宣告前文的套件永遠收不到文件文字
	}
	started := time.Now()
	defer func() { w.stats.TotalUS += time.Since(started).Microseconds() }()
	w.next++
	request := struct {
		rerank.Request
		ID         uint64 `json:"id"`
		Model      string `json:"model_sha256"`
		Dictionary string `json:"dictionary_sha256"`
	}{r, w.next, w.manifest.Files["model.onnx"], w.manifest.DictionarySHA256}
	b, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	if len(b)+1 > frameLimit {
		return nil, errors.New("neural request frame too large")
	}
	b, err = w.exchange(append(b, '\n'), w.timeout)
	if err != nil {
		if errors.Is(err, ErrTimeout) {
			w.stats.Timeouts++
		} else {
			w.stats.Errors++
		}
		w.stop()
		return nil, err
	}
	var reply struct {
		ID         uint64    `json:"id"`
		Revision   int       `json:"revision"`
		Model      string    `json:"model_sha256"`
		Dictionary string    `json:"dictionary_sha256"`
		Scores     []float64 `json:"scores"`
		Fallback   string    `json:"fallback"`
	}
	err = json.Unmarshal(b, &reply)
	if err != nil || reply.ID != w.next || reply.Revision != r.Revision || reply.Model != request.Model || reply.Dictionary != request.Dictionary {
		w.stats.Errors++
		w.stop()
		return nil, errors.New("neural reply identity mismatch")
	}
	if reply.Fallback != "" {
		return nil, errors.New("neural unsupported input")
	}
	if len(reply.Scores) != len(r.Candidates) {
		w.stats.Errors++
		w.stop()
		return nil, errors.New("neural score count mismatch")
	}
	for _, score := range reply.Scores {
		if math.IsNaN(score) || math.IsInf(score, 0) {
			w.stats.Errors++
			w.stop()
			return nil, errors.New("invalid neural score")
		}
	}
	w.stats.Scored++
	return reply.Scores, nil
}
