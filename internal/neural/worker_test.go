package neural

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/rerank"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

func TestWorkerProcess(t *testing.T) {
	mode := os.Getenv("ZUIN_TEST_NEURAL_CHILD")
	if mode == "" {
		return
	}
	if mode == "dll_missing" { // 模擬 Windows 缺 MSVCP140.dll：import 時失敗，沒有 ready 握手（#80）
		fmt.Fprintln(os.Stderr, "ImportError: DLL load failed while importing onnxruntime_pybind11_state: The specified module could not be found.")
		os.Exit(1)
	}
	if mode == "silent_exit" {
		os.Exit(1)
	}
	schema := os.Getenv("ZUIN_TEST_NEURAL_SCHEMA")
	if schema == "" {
		schema = "zuin-encoder-runtime-v1"
	}
	fmt.Printf(`{"ready":true,"schema":%q,"model_sha256":"model","dictionary_sha256":"dictionary"}`+"\n", schema)
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var r map[string]any
		_ = json.Unmarshal(scanner.Bytes(), &r)
		switch mode {
		case "timeout":
			time.Sleep(time.Second)
		case "crash":
			os.Exit(3)
		case "oversize":
			fmt.Println(strings.Repeat("x", frameLimit+1))
			continue
		}
		if mode == "stale" {
			r["revision"] = 0
		}
		if mode == "wrong_model" {
			r["model_sha256"] = "other"
		}
		if mode == "wrong_id" {
			r["id"] = 0
		}
		r["scores"] = []float64{0, 20}
		if mode == "context" { // 回報收到的前文字數，驗證宿主是否依契約送出
			context, _ := r["context"].(string)
			r["scores"] = []float64{float64(len([]rune(context))), 0}
		}
		b, _ := json.Marshal(r)
		fmt.Println(string(b))
	}
	os.Exit(0)
}
func child(t *testing.T, mode string) *Worker {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestWorkerProcess$")
	cmd.Env = append(os.Environ(), "ZUIN_TEST_NEURAL_CHILD="+mode)
	w, err := start(cmd, Manifest{Schema: "zuin-encoder-runtime-v1", DictionarySHA256: "dictionary", Files: map[string]string{"model.onnx": "model"}, Weight: .1}, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	return w
}
func contextChild(t *testing.T, schema, policy string) *Worker {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestWorkerProcess$")
	cmd.Env = append(os.Environ(), "ZUIN_TEST_NEURAL_CHILD=context", "ZUIN_TEST_NEURAL_SCHEMA="+schema)
	w, err := start(cmd, Manifest{Schema: schema, ContextPolicy: policy, DictionarySHA256: "dictionary", Files: map[string]string{"model.onnx": "model"}, Weight: 1, Margin: 2, HintGap: 1.5}, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	return w
}

func TestContractsAndContextPolicy(t *testing.T) {
	for _, c := range []struct {
		schema, policy string
		ok             bool
		runes          int
	}{
		{"zuin-encoder-runtime-v1", "empty", true, 0},
		{"zuin-sequence-runtime-v1", "confirmed_left_64", true, rerank.MaxContextRunes},
		{"zuin-encoder-runtime-v1", "confirmed_left_64", false, 0},
		{"zuin-sequence-runtime-v1", "empty", false, 0},
		{"other", "empty", false, 0},
	} {
		m := Manifest{Schema: c.schema, ContextPolicy: c.policy}
		if validContract(m) != c.ok || (c.ok && contextRunes(m) != c.runes) {
			t.Fatalf("%+v", c)
		}
	}
	r := request()
	r.Context = "今天天氣很好"
	encoder := contextChild(t, "zuin-encoder-runtime-v1", "empty")
	if scores, err := encoder.Score(r); err != nil || scores[0] != 0 || encoder.Options().ContextRunes != 0 {
		t.Fatalf("encoder received context: %v %v", scores, err)
	}
	sequence := contextChild(t, "zuin-sequence-runtime-v1", "confirmed_left_64")
	opt := sequence.Options()
	if scores, err := sequence.Score(r); err != nil || scores[0] != 6 || opt.ContextRunes != rerank.MaxContextRunes || opt.Weight != 1 || opt.Margin != 2 || opt.HintGap != 1.5 {
		t.Fatalf("sequence context: %v %v %+v", scores, err, opt)
	}
}

func request() rerank.Request {
	return rerank.Request{Revision: 7, Reading: zhuyin.ParseReading("ㄐㄧㄚˇ ㄧˇ"), Candidates: []decode.Hypothesis{
		{Text: "甲乙", LogScore: -2, Segments: []decode.Seg{{Word: "甲乙", Start: 0, End: 2}}},
		{Text: "甲已", LogScore: -3, Segments: []decode.Seg{{Word: "甲已", Start: 0, End: 2}}},
	}}
}
func TestWorkerDeadlineIdentityAndCrash(t *testing.T) {
	for _, mode := range []string{"success", "timeout", "stale", "wrong_model", "wrong_id", "crash", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			w := child(t, mode)
			start := time.Now()
			scores, err := w.Score(request())
			if mode == "success" {
				if err != nil || len(scores) != 2 {
					t.Fatalf("%v %v", scores, err)
				}
				return
			}
			if err == nil {
				t.Fatal("invalid worker accepted")
			}
			if mode == "timeout" && !errors.Is(err, ErrTimeout) {
				t.Fatalf("expected timeout: %v", err)
			}
			if time.Since(start) > 500*time.Millisecond {
				t.Fatal("unbounded worker wait")
			}
			if _, err = w.Score(request()); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("late worker reused: %v", err)
			}
		})
	}
}

func TestONNXPackageIntegration(t *testing.T) {
	packagePath := os.Getenv("ZUIN_TEST_NEURAL_PACKAGE")
	if packagePath == "" {
		t.Skip("requires isolated ONNX package and runtime")
	}
	b, err := os.ReadFile(packagePath + "/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err = json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Python: os.Getenv("ZUIN_TEST_NEURAL_PYTHON"), Script: os.Getenv("ZUIN_TEST_NEURAL_SCRIPT"), Package: packagePath, DictionarySHA256: m.DictionarySHA256, Threads: 2, Timeout: 20 * time.Millisecond}
	w, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	r := request()
	r.AllowPartial = m.AllowPartial
	if r.AllowPartial {
		r.Candidates[1].Segments[0].Partial = true
	}
	scores, err := w.Score(r)
	if err != nil || len(scores) != 2 {
		t.Fatalf("actual ONNX: %v %v", scores, err)
	}
	wrong := cfg
	wrong.DictionarySHA256 = strings.Repeat("0", 64)
	if other, err := Open(wrong); err == nil {
		other.Close()
		t.Fatal("mismatched dictionary accepted")
	}
}

func TestTextModeNeedsDeclaredWeight(t *testing.T) {
	w := contextChild(t, "zuin-sequence-runtime-v1", "confirmed_left_64")
	r := rerank.Request{Revision: 1, Mode: rerank.ModeText, Context: "前文", Reading: zhuyin.ParseReading("ㄐㄧㄚˇ"), Candidates: []decode.Hypothesis{
		{Text: "甲", LogScore: -2, Segments: []decode.Seg{{Word: "甲", Start: 0, End: 1}}},
		{Text: "假", LogScore: -3, Segments: []decode.Seg{{Word: "假", Start: 0, End: 1}}},
	}}
	if _, err := w.Score(r); err == nil || w.Dead() {
		t.Fatalf("undeclared text mode must be rejected locally: err=%v dead=%v", err, w.Dead())
	}
	if o := w.Options(); o.TextWeight != 0 {
		t.Fatalf("text weight leaked into options: %+v", o)
	}
}

// 內附 Python 必須隔離、不寫 bytecode（安裝後檔案要與收據一致）、stdio 用 UTF-8。
func TestPythonCommandIsIsolatedAndReadOnly(t *testing.T) {
	cmd := pythonCommand("python", "worker.py", "--package", "p")
	want := []string{"python", "-I", "-B", "-X", "utf8", "worker.py", "--package", "p"}
	if strings.Join(cmd.Args, " ") != strings.Join(want, " ") {
		t.Fatalf("args = %v", cmd.Args)
	}
}

func TestStartupFailureReason(t *testing.T) {
	m := Manifest{Schema: "zuin-encoder-runtime-v1", DictionarySHA256: "dictionary", Files: map[string]string{"model.onnx": "model"}}
	for mode, want := range map[string]string{"dll_missing": ReasonMissingRuntime, "silent_exit": ReasonFailed} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestWorkerProcess$")
		cmd.Env = append(os.Environ(), "ZUIN_TEST_NEURAL_CHILD="+mode)
		_, err := start(cmd, m, 20*time.Millisecond)
		if got := startupReason(err); got != want {
			t.Fatalf("%s: reason %q (%v), want %q", mode, got, err, want)
		}
	}
	if got := startupReason(errors.New("exec: python.exe not found")); got != ReasonFailed {
		t.Fatalf("plain error reason %q", got)
	}
}

func TestStartupStderrStopsAfterReady(t *testing.T) {
	var s startupStderr
	s.Write([]byte(strings.Repeat("x", 10000)))
	if text := s.finish(); len(text) != 8192 {
		t.Fatalf("startup stderr not capped: %d", len(text))
	}
	// 就緒後的 traceback 可能含使用者文字，不能留下。
	if n, err := s.Write([]byte("使用者文字")); err != nil || n != len("使用者文字") || len(s.buf) != 0 {
		t.Fatalf("stderr kept after ready: %d %v %q", n, err, s.buf)
	}
}
