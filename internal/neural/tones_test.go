package neural

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/rerank"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

func maskedRequest() rerank.Request {
	return rerank.Request{Revision: 7, Reading: []zhuyin.Syllable{"ㄐㄧㄚ", "ㄧ"}, ToneKnown: []bool{false, false}, Candidates: []decode.Hypothesis{
		{Text: "甲乙", LogScore: -2, Segments: []decode.Seg{{Word: "甲乙", Start: 0, End: 2}}},
		{Text: "甲已", LogScore: -3, Segments: []decode.Seg{{Word: "甲已", Start: 0, End: 2}}},
	}}
}

// 桌面出貨的選字模型（#281 起是 v2 套件，宣告了未知聲調）照樣不開未知聲調：沒打聲調的段落照舊走統計引擎，
// 請求也不帶 tone_known；權重照 manifest，不設手機的 ContextlessWeight。
func TestShippedManifestKeepsKnownTones(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "models", "upstream", "select", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err = json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if !validContract(m) {
		t.Fatalf("shipped manifest: %s valid=%v", m.Schema, validContract(m))
	}
	opt := m.options(nil)
	if opt.UnknownTones || opt.ContextlessWeight != 0 || opt.Weight != 2 || m.supports(maskedRequest()) {
		t.Fatalf("桌面不開未知聲調、權重照 manifest：%+v", opt)
	}
}

// writePackage 寫一個只用來核對 manifest 的套件（model.onnx 不是真的模型）。
func writePackage(t *testing.T, edit func(map[string]any), vocab string) Config {
	t.Helper()
	dir := t.TempDir()
	sum := func(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
	model := []byte("model")
	m := map[string]any{"schema": rerank.SequenceSchemaV2, "context_policy": "confirmed_left_64", "dictionary_sha256": sum([]byte("dict")),
		"tone_policy": map[string]any{"schema": rerank.ToneMaskSchema, "unknown_tone_id": 0}, "weight": 2.0, "margin": 0.0,
		"files": map[string]string{"model.onnx": sum(model), "vocab.json": sum([]byte(vocab))}}
	edit(m)
	b, _ := json.Marshal(m)
	for name, data := range map[string][]byte{"manifest.json": b, "model.onnx": model, "vocab.json": []byte(vocab)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return Config{Package: dir, DictionarySHA256: sum([]byte("dict")), Threads: 1, Timeout: 20 * time.Millisecond}
}

// 桌面拿到 v2 套件：照常載入、打了聲調的段落照樣送，但不開未知聲調（桌面評估前，使用者決定）。
func TestDesktopV2PackageKeepsUnknownTonesOff(t *testing.T) {
	vocab := `{"bases":["ㄐㄧㄚ","ㄧ"],"characters":["甲","乙","已"]}`
	m, err := loadManifest(writePackage(t, func(map[string]any) {}, vocab))
	if err != nil {
		t.Fatal(err)
	}
	if !rerank.UnknownTonesDeclared(m.Schema, m.TonePolicy) {
		t.Fatal("測試套件應宣告未知聲調")
	}
	opt := m.options(nil)
	if opt.UnknownTones || opt.ContextlessWeight != 0 || opt.Weight != 2 || len(opt.Bases) != 2 || m.supports(maskedRequest()) {
		t.Fatalf("桌面 v2：%+v", opt)
	}
	toned := maskedRequest()
	toned.Reading, toned.ToneKnown = zhuyin.ParseReading("ㄐㄧㄚˇ ㄧˇ"), nil
	if !m.supports(toned) {
		t.Fatal("打了聲調的請求應照常送")
	}
	outside := toned
	outside.Reading = zhuyin.ParseReading("ㄐㄧㄚˇ ㄅㄚ")
	if m.supports(outside) {
		t.Fatal("字表外的基底被送出")
	}
	for name, edit := range map[string]func(map[string]any){
		"v2 without tone_policy": func(m map[string]any) { delete(m, "tone_policy") },
		"unknown_tone_id 1": func(m map[string]any) {
			m["tone_policy"] = map[string]any{"schema": rerank.ToneMaskSchema, "unknown_tone_id": 1}
		},
		"v1 with tone_policy": func(m map[string]any) { m["schema"] = rerank.SequenceSchemaV1 },
	} {
		if _, err := loadManifest(writePackage(t, edit, vocab)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// 字表讀不出基底：照舊載入（壞字表由 worker 啟動時擋），只是不在本機檢查基底。
	if m, err := loadManifest(writePackage(t, func(map[string]any) {}, `{"characters":["甲"]}`)); err != nil || m.bases != nil {
		t.Errorf("vocab without bases: %v %v", err, m.bases)
	}
	v1, err := loadManifest(writePackage(t, func(m map[string]any) { m["schema"] = rerank.SequenceSchemaV1; delete(m, "tone_policy") }, vocab))
	if err != nil || v1.options(nil).UnknownTones || v1.options(nil).Bases == nil {
		t.Fatalf("v1 套件：%v %+v", err, v1.options(nil))
	}
}

func TestDesktopWorkerNeverSendsToneKnown(t *testing.T) {
	open := func(m Manifest) *Worker {
		cmd := exec.Command(os.Args[0], "-test.run=^TestWorkerProcess$")
		cmd.Env = append(os.Environ(), "ZUIN_TEST_NEURAL_CHILD=context", "ZUIN_TEST_NEURAL_SCHEMA="+m.Schema)
		w, err := start(cmd, m, 20*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(w.Close)
		return w
	}
	base := Manifest{ContextPolicy: "confirmed_left_64", DictionarySHA256: "dictionary", Files: map[string]string{"model.onnx": "model"}, Weight: 1}
	v1 := base
	v1.Schema = rerank.SequenceSchemaV1
	if _, err := open(v1).Score(maskedRequest()); err == nil {
		t.Fatal("v1 worker accepted tone_known")
	}
	v2 := base
	v2.Schema = rerank.SequenceSchemaV2
	zero := 0
	v2.TonePolicy = &rerank.TonePolicy{Schema: rerank.ToneMaskSchema, UnknownToneID: &zero}
	w := open(v2)
	if _, err := w.Score(maskedRequest()); err == nil || w.Options().UnknownTones || w.Dead() {
		t.Fatalf("桌面 v2 worker 不送 tone_known（本機擋，worker 不受影響）：%v", err)
	}
	if scores, err := w.Score(request()); err != nil || len(scores) != 2 {
		t.Fatalf("桌面 v2 worker 打了聲調照常評分：%v %v", scores, err)
	}
}

// 真的 v2 套件（tone0-only）在桌面：載入、打了聲調照常評分，沒打聲調的請求在本機就擋掉、不送 worker。
func TestONNXDesktopV2Package(t *testing.T) {
	packagePath := os.Getenv("ZUIN_TEST_NEURAL_PACKAGE")
	if packagePath == "" {
		t.Skip("requires isolated ONNX package and runtime")
	}
	var m Manifest
	b, err := os.ReadFile(filepath.Join(packagePath, "manifest.json"))
	if err == nil {
		err = json.Unmarshal(b, &m)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !rerank.UnknownTonesDeclared(m.Schema, m.TonePolicy) {
		t.Skip("package does not declare unknown tones")
	}
	w, err := Open(Config{Python: os.Getenv("ZUIN_TEST_NEURAL_PYTHON"), Script: os.Getenv("ZUIN_TEST_NEURAL_SCRIPT"), Package: packagePath,
		DictionarySHA256: m.DictionarySHA256, Threads: 2, Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	toned := request()
	toned.AllowPartial = m.AllowPartial
	if scores, err := w.Score(toned); err != nil || len(scores) != 2 {
		t.Fatalf("v2 打了聲調：%v %v", scores, err)
	}
	masked := maskedRequest()
	masked.AllowPartial = m.AllowPartial
	before := w.Stats()
	if _, err := w.Score(masked); err == nil || w.Options().UnknownTones || w.Dead() || w.Stats().TotalUS != before.TotalUS {
		t.Fatalf("桌面不送 tone_known：%v %+v", err, w.Stats())
	}
}
