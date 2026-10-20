package models

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	upstream "github.com/0c-biggo/0c-ime/models"
)

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func upstreamFile(t *testing.T, role, name string) []byte {
	return read(t, filepath.Join("..", "..", "models", "upstream", role, name))
}

// Go 版的 json.dumps(ensure_ascii=False, indent=2)：鍵順序、浮點數、跳脫字元都要跟 Python 逐位元組相同。
// 期望值由 python3 對同一份輸入產生（testdata/pyjson_expected.json）。
func TestPythonJSONMatchesPythonByteForByte(t *testing.T) {
	v, err := parsePyJSON(read(t, "testdata/pyjson_input.json"))
	if err != nil {
		t.Fatal(err)
	}
	got, want := dumpPyJSON(v), read(t, "testdata/pyjson_expected.json")
	if !bytes.Equal(got, want) {
		t.Fatalf("與 Python 不同：\n%s\n--- want ---\n%s", got, want)
	}
	for _, c := range []struct {
		f    float64
		want string
	}{{2, "2.0"}, {0.63, "0.63"}, {1e-5, "1e-05"}, {1e16, "1e+16"}, {1e15, "1000000000000000.0"}, {-0.5, "-0.5"}, {123.456, "123.456"}} {
		if got := pyFloatRepr(c.f); got != c.want {
			t.Errorf("repr(%v) = %s, want %s", c.f, got, c.want)
		}
	}
}

// 上游 metadata 原樣讀進來再寫出去，位元組不變（上游本來就是 json_bytes 寫的）。
func TestUpstreamMetadataRoundTrips(t *testing.T) {
	for _, role := range Roles {
		for _, n := range pairedFiles {
			b := upstreamFile(t, role, n)
			v, err := parsePyJSON(b)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(dumpPyJSON(v), b) {
				t.Fatalf("%s/%s 重新寫出後不同", role, n)
			}
		}
	}
}

// 編進程式的 models.lock 與 0c-ime 的相同（pairing.lock 的 models_lock_sha256），上游 metadata 也對得上它。
func TestEmbeddedLockMatchesUpstream(t *testing.T) {
	l, err := ParseLock(upstream.Lock)
	if err != nil {
		t.Fatal(err)
	}
	if l.SHA256 != "59a021df3441fbfa5db32008bde4b9f84e60cd7eb08e68df479c74466585fd39" {
		t.Fatalf("models.lock 雜湊 %s", l.SHA256)
	}
	if l.Models[RoleSelect].Files["model.onnx"].SHA256 != "71af1f0e77b2752cc71cbc04bf19c8e8f2e765662246f766e65a2822bab4e66e" ||
		l.Models[RolePunct].Files["model.onnx"].SHA256 != "4db3ed40df777347d9c455da01285b01de192d4c210c5f7cd9f0646c727880d8" {
		t.Fatal("models.lock 的權重雜湊不是 Hugging Face 公開版")
	}
	if l.Size() < 260_000_000 || l.Size() > 263_000_000 {
		t.Fatalf("下載大小 %d", l.Size())
	}
	for _, role := range Roles {
		for _, n := range pairedFiles {
			b := upstreamFile(t, role, n)
			if f := l.Models[role].Files[n]; sum(b) != f.SHA256 || int64(len(b)) != f.Size {
				t.Fatalf("models/upstream/%s/%s 與 models.lock 不符", role, n)
			}
		}
	}
}

// Go 版配對的結果等於 0c-ime 以 scripts/pair_models.py 產生的 pairing.lock（兩份不同的字典）。
func TestPairMatchesPythonPairingLocks(t *testing.T) {
	l, _ := ParseLock(upstream.Lock)
	for _, name := range []string{"0c-ime-dfsg-pairing.lock.json", "0c-ime-0.2.3-pairing.lock.json"} {
		p, err := ParsePairing(read(t, filepath.Join("testdata", name)))
		if err != nil {
			t.Fatal(err)
		}
		if p.ModelsLock != l.SHA256 {
			t.Fatalf("%s 不是這份 models.lock 的配對", name)
		}
		for _, role := range Roles {
			out, err := Pair(upstreamFile(t, role, "manifest.json"), upstreamFile(t, role, "vocab.json"), p.Dictionary)
			if err != nil {
				t.Fatal(err)
			}
			if err := checkPaired(p, role, out); err != nil {
				t.Fatalf("%s %s：%v", name, role, err)
			}
		}
	}
}

// ---- 下載：用本機的假 Hugging Face ----

type fakeHub struct {
	files    map[string][]byte // URL 路徑 → 內容
	hits     atomic.Int64
	truncate string // 這個路徑只送一半就斷線
	corrupt  string // 這個路徑送錯內容（大小相同）
	block    string // 這個路徑送到一半就停住，直到請求被取消
}

func (h *fakeHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.hits.Add(1)
	b, ok := h.files[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Length", fmt.Sprint(len(b)))
	switch r.URL.Path {
	case h.truncate:
		w.Write(b[:len(b)/2])
		if hj, ok := w.(http.Hijacker); ok {
			c, _, _ := hj.Hijack()
			c.Close()
		}
		return
	case h.corrupt:
		w.Write(bytes.Repeat([]byte{'x'}, len(b)))
		return
	case h.block:
		w.Write(b[:len(b)/2])
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		return
	}
	w.Write(b)
}

type fixture struct {
	lock    Lock
	raw     []byte
	hub     *fakeHub
	server  *httptest.Server
	fetcher *Fetcher
}

// newFixture 做一份小的 models.lock：model.onnx 是假的位元組，manifest、vocab 用真的上游 metadata。
func newFixture(t *testing.T, revisions ...string) *fixture {
	t.Helper()
	hub := &fakeHub{files: map[string][]byte{}}
	lock := map[string]any{"schema": 1}
	models := map[string]any{}
	for i, role := range Roles {
		rev := fmt.Sprintf("%040x", i+1)
		if i < len(revisions) {
			rev = revisions[i]
		}
		repo := "Funmula/test-" + role
		content := map[string][]byte{
			"model.onnx":    bytes.Repeat([]byte(role+rev), 4000),
			"LICENSE":       []byte("MIT\n"),
			"manifest.json": upstreamFile(t, role, "manifest.json"),
			"vocab.json":    upstreamFile(t, role, "vocab.json"),
		}
		files := map[string]any{}
		for n, b := range content {
			files[n] = map[string]any{"sha256": sum(b), "size": len(b)}
			hub.files["/"+repo+"/resolve/"+rev+"/"+n] = b
		}
		models[role] = map[string]any{"repository": repo, "revision": rev, "version": "test", "license": "MIT", "files": files}
	}
	lock["models"] = models
	raw, _ := json.MarshalIndent(lock, "", "  ")
	l, err := ParseLock(raw)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)
	return &fixture{lock: l, raw: raw, hub: hub, server: srv, fetcher: &Fetcher{BaseURL: srv.URL, Client: srv.Client(), Stall: 5 * time.Second}}
}

// pairing 產生這份 lock 對某個字典的 pairing.lock（store 的測試用；配對本身對 Python 的核對見上面）。
func (f *fixture) pairing(t *testing.T, dictionary string) Pairing {
	p := Pairing{Schema: 1, Dictionary: dictionary, ModelsLock: f.lock.SHA256, Packages: map[string]map[string]File{}}
	for _, role := range Roles {
		out, err := Pair(upstreamFile(t, role, "manifest.json"), upstreamFile(t, role, "vocab.json"), dictionary)
		if err != nil {
			t.Fatal(err)
		}
		p.Packages[role] = map[string]File{}
		for n, b := range out {
			p.Packages[role][n] = File{SHA256: sum(b), Size: int64(len(b))}
		}
	}
	return p
}

var dictA, dictB = strings.Repeat("a", 64), strings.Repeat("b", 64)

func inode(t *testing.T, path string) os.FileInfo {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func same(a, b os.FileInfo) bool { return os.SameFile(a, b) }

// 只剩提醒紀錄以外，什麼都沒留下（沒有權重、配對、暫存目錄）。
func assertEmpty(t *testing.T, s Store) {
	t.Helper()
	entries, _ := os.ReadDir(s.Root)
	for _, e := range entries {
		switch e.Name() {
		case remindFile, stateFile, operationFile:
			continue
		}
		t.Fatalf("留下了 %s", e.Name())
	}
}

func TestInstallDownloadsVerifiesAndPairs(t *testing.T) {
	f := newFixture(t)
	s := Store{Root: filepath.Join(t.TempDir(), "models")}
	if need := s.Inspect(f.lock, dictA).Need(); need != "download" {
		t.Fatalf("need %s", need)
	}
	var phases []string
	err := s.Install(context.Background(), f.lock, f.pairing(t, dictA), dictA, f.fetcher, func(phase string, done, total int64) {
		if len(phases) == 0 || phases[len(phases)-1] != phase {
			phases = append(phases, phase)
		}
		if done > total || total != f.lock.Size() {
			t.Fatalf("進度 %d/%d", done, total)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(phases, ",") != "downloading,verifying,pairing" {
		t.Fatalf("階段 %v", phases)
	}
	if in := s.Inspect(f.lock, dictA); in.Need() != "none" || !in.Weights {
		t.Fatalf("%+v", in)
	}
	for _, role := range Roles {
		pair := filepath.Join(s.PairDir(dictA), role)
		var m struct {
			Dictionary string            `json:"dictionary_sha256"`
			Files      map[string]string `json:"files"`
		}
		json.Unmarshal(read(t, filepath.Join(pair, "manifest.json")), &m)
		if m.Files["vocab.json"] != sum(read(t, filepath.Join(pair, "vocab.json"))) {
			t.Fatalf("%s：manifest 記的 vocab 雜湊不對", role)
		}
		if role == RoleSelect && m.Dictionary != dictA {
			t.Fatalf("選字 manifest 沒有綁到字典：%s", m.Dictionary)
		}
		// 權重共用同一份檔案，不多佔空間
		if !same(inode(t, filepath.Join(pair, "model.onnx")), inode(t, filepath.Join(s.WeightsDir(role, f.lock.Models[role].Revision), "model.onnx"))) {
			t.Fatalf("%s：配對目錄的 model.onnx 不是權重的硬連結", role)
		}
	}
	entries, _ := os.ReadDir(s.Root)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".staging-") {
			t.Fatalf("留下暫存目錄 %s", e.Name())
		}
	}
}

func TestInstallFailureLeavesNothing(t *testing.T) {
	cases := map[string]func(f *fixture) string{
		CodeNetwork:  func(f *fixture) string { p := f.path(RolePunct, "model.onnx"); f.hub.truncate = p; return p },
		CodeChecksum: func(f *fixture) string { p := f.path(RolePunct, "model.onnx"); f.hub.corrupt = p; return p },
	}
	for code, setup := range cases {
		t.Run(code, func(t *testing.T) {
			f := newFixture(t)
			setup(f)
			s := Store{Root: filepath.Join(t.TempDir(), "models")}
			err := s.Install(context.Background(), f.lock, f.pairing(t, dictA), dictA, f.fetcher, nil)
			if AsError(err).Code != code {
				t.Fatalf("錯誤 %v（%s）", err, AsError(err).Code)
			}
			assertEmpty(t, s)
			if s.Inspect(f.lock, dictA).Need() != "download" {
				t.Fatal("失敗後仍應是未安裝")
			}
		})
	}
}

func (f *fixture) path(role, name string) string {
	m := f.lock.Models[role]
	return "/" + m.Repository + "/resolve/" + m.Revision + "/" + name
}

func TestInstallCancelLeavesNothing(t *testing.T) {
	f := newFixture(t)
	f.hub.block = f.path(RolePunct, "model.onnx")
	s := Store{Root: filepath.Join(t.TempDir(), "models")}
	ctx, cancel := context.WithCancel(context.Background())
	err := s.Install(ctx, f.lock, f.pairing(t, dictA), dictA, f.fetcher, func(phase string, done, total int64) {
		if done > f.lock.Models[RoleSelect].Size() { // 選字下載完、補標點送到一半時取消
			cancel()
		}
	})
	if AsError(err).Code != CodeCanceled {
		t.Fatalf("%v", err)
	}
	assertEmpty(t, s)
}

// 網路停住不動時以 Stall 判斷中斷，不會一直等。
func TestInstallStallIsNetworkError(t *testing.T) {
	f := newFixture(t)
	f.hub.block = f.path(RoleSelect, "model.onnx")
	f.fetcher.Stall = 200 * time.Millisecond
	s := Store{Root: filepath.Join(t.TempDir(), "models")}
	err := s.Install(context.Background(), f.lock, f.pairing(t, dictA), dictA, f.fetcher, nil)
	if AsError(err).Code != CodeNetwork {
		t.Fatalf("%v", err)
	}
	assertEmpty(t, s)
}

// 字典沒有對應的配對清單：連網之前就拒絕，不白下載 260 MB。
func TestInstallRejectsUnpairedDictionaryBeforeDownloading(t *testing.T) {
	f := newFixture(t)
	s := Store{Root: filepath.Join(t.TempDir(), "models")}
	err := s.Install(context.Background(), f.lock, f.pairing(t, dictA), dictB, f.fetcher, nil)
	if AsError(err).Code != CodeDictionaryMismatch || f.hub.hits.Load() != 0 {
		t.Fatalf("%v，請求 %d 次", err, f.hub.hits.Load())
	}
	other := f.pairing(t, dictA)
	other.ModelsLock = strings.Repeat("c", 64)
	if err := s.Install(context.Background(), f.lock, other, dictA, f.fetcher, nil); AsError(err).Code != CodeDictionaryMismatch {
		t.Fatalf("配對清單屬於別的 models.lock：%v", err)
	}
}

// 字典換了、模型沒換：用已有的權重在本機重新配對，不連網；舊字典的配對刪掉，權重是同一份檔案。
func TestRepairAfterDictionaryChangeDoesNotDownload(t *testing.T) {
	f := newFixture(t)
	s := Store{Root: filepath.Join(t.TempDir(), "models")}
	if err := s.Install(context.Background(), f.lock, f.pairing(t, dictA), dictA, f.fetcher, nil); err != nil {
		t.Fatal(err)
	}
	weights := filepath.Join(s.WeightsDir(RoleSelect, f.lock.Models[RoleSelect].Revision), "model.onnx")
	before, hits := inode(t, weights), f.hub.hits.Load()
	if in := s.Inspect(f.lock, dictB); in.Need() != "repair" {
		t.Fatalf("換字典後 need=%s", in.Need())
	}
	if err := s.Install(context.Background(), f.lock, f.pairing(t, dictB), dictB, nil, nil); err != nil {
		t.Fatal(err)
	}
	if f.hub.hits.Load() != hits {
		t.Fatal("重新配對不應連網")
	}
	if s.Inspect(f.lock, dictB).Need() != "none" {
		t.Fatal("重新配對後應可使用")
	}
	if _, err := os.Stat(s.PairDir(dictA)); !os.IsNotExist(err) {
		t.Fatal("舊字典的配對應刪除")
	}
	if !same(inode(t, weights), before) || !same(inode(t, filepath.Join(s.PairDir(dictB), RoleSelect, "model.onnx")), before) {
		t.Fatal("權重應是同一份檔案")
	}
}

// 本機權重壞了：重新配對失敗，原本的東西不動。
func TestRepairWithDamagedWeightsFails(t *testing.T) {
	f := newFixture(t)
	s := Store{Root: filepath.Join(t.TempDir(), "models")}
	if err := s.Install(context.Background(), f.lock, f.pairing(t, dictA), dictA, f.fetcher, nil); err != nil {
		t.Fatal(err)
	}
	m := f.lock.Models[RolePunct]
	path := filepath.Join(s.WeightsDir(RolePunct, m.Revision), "model.onnx")
	b := read(t, path)
	b[0] ^= 0xff
	os.Remove(path) // 硬連結：寫新檔，不動配對目錄那一份
	os.WriteFile(path, b, 0o644)
	if err := s.Install(context.Background(), f.lock, f.pairing(t, dictB), dictB, nil, nil); AsError(err).Code != CodeChecksum {
		t.Fatalf("%v", err)
	}
	if s.Inspect(f.lock, dictA).Need() != "none" {
		t.Fatal("原本的配對應保留")
	}
}

// models.lock 換了（上游出了新版）：舊權重不能用，要重新下載；下載成功後才刪舊的。沒換的那一個模型沿用。
func TestModelsLockChangeNeedsRedownload(t *testing.T) {
	old := newFixture(t)
	s := Store{Root: filepath.Join(t.TempDir(), "models")}
	if err := s.Install(context.Background(), old.lock, old.pairing(t, dictA), dictA, old.fetcher, nil); err != nil {
		t.Fatal(err)
	}
	// 新版只換了選字模型
	f := newFixture(t, strings.Repeat("9", 40), old.lock.Models[RolePunct].Revision)
	if need := s.Inspect(f.lock, dictA).Need(); need != "redownload" {
		t.Fatalf("同字典、新 models.lock：need=%s", need)
	}
	if need := s.Inspect(f.lock, dictB).Need(); need != "redownload" {
		t.Fatalf("新字典、新 models.lock：need=%s", need)
	}
	if err := s.Install(context.Background(), f.lock, f.pairing(t, dictB), dictB, nil, nil); AsError(err).Code != CodeChecksum {
		t.Fatalf("不能用舊權重重新配對：%v", err)
	}
	if s.Inspect(old.lock, dictA).Need() != "none" {
		t.Fatal("重新下載成功之前舊模型照常使用")
	}
	f.hub.corrupt = f.path(RoleSelect, "model.onnx")
	if err := s.Install(context.Background(), f.lock, f.pairing(t, dictB), dictB, f.fetcher, nil); err == nil {
		t.Fatal("下載不符應失敗")
	}
	if s.Inspect(old.lock, dictA).Need() != "none" {
		t.Fatal("下載失敗時舊模型照常使用")
	}
	f.hub.corrupt = ""
	hits := f.hub.hits.Load()
	if err := s.Install(context.Background(), f.lock, f.pairing(t, dictB), dictB, f.fetcher, nil); err != nil {
		t.Fatal(err)
	}
	if got := f.hub.hits.Load() - hits; got != int64(len(f.lock.Models[RoleSelect].Files)) {
		t.Fatalf("沒換的補標點應沿用，實際請求 %d 次", got)
	}
	if s.Inspect(f.lock, dictB).Need() != "none" {
		t.Fatal("下載後應可使用")
	}
	if _, err := os.Stat(s.WeightsDir(RoleSelect, old.lock.Models[RoleSelect].Revision)); !os.IsNotExist(err) {
		t.Fatal("舊權重應刪除")
	}
	if _, err := os.Stat(s.PairDir(dictA)); !os.IsNotExist(err) {
		t.Fatal("舊配對應刪除")
	}
}

func TestRemoveKeepsReminderRecord(t *testing.T) {
	f := newFixture(t)
	s := Store{Root: filepath.Join(t.TempDir(), "models")}
	if err := s.Install(context.Background(), f.lock, f.pairing(t, dictA), dictA, f.fetcher, nil); err != nil {
		t.Fatal(err)
	}
	r := s.LoadRemind()
	r.Stop(StopDownloaded, time.Now())
	s.SaveRemind(r)
	s.SetLastError(errors.New("x"), time.Now())
	if err := s.Remove(); err != nil {
		t.Fatal(err)
	}
	if s.Inspect(f.lock, dictA).Need() != "download" || s.LastError() != nil {
		t.Fatal("刪除後應是未安裝、沒有錯誤")
	}
	if s.LoadRemind().Stopped != StopDownloaded {
		t.Fatal("刪了模型的人不再被提醒")
	}
}

// ---- 提醒 ----

func TestRemindScheduleAndStops(t *testing.T) {
	t0 := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
	base := RemindInput{Neural: true}
	at := func(d time.Duration, committed bool) RemindInput {
		in := base
		in.Now, in.Committed = t0.Add(d), committed
		return in
	}
	var r Remind
	if r.Decide(at(0, true)) {
		t.Fatal("剛安裝不提醒")
	}
	if r.Decide(at(2*time.Hour, false)) && !r.Committed {
		t.Fatal("沒送出過字不提醒")
	}
	r = Remind{}
	r.Decide(at(0, false))
	if r.Decide(at(2*time.Hour, false)) {
		t.Fatal("沒送出過字不提醒")
	}
	if !r.Decide(at(2*time.Hour, true)) {
		t.Fatal("滿一小時、送出過字：第一次提醒")
	}
	r.Sent(t0.Add(2 * time.Hour))
	if r.Decide(at(20*time.Hour, true)) {
		t.Fatal("間隔不到 24 小時")
	}
	if !r.NextAt.Equal(t0.Add(26 * time.Hour)) {
		t.Fatalf("下一次 %v", r.NextAt)
	}
	if !r.Decide(at(26*time.Hour, true)) {
		t.Fatal("第二次")
	}
	r.Sent(t0.Add(26 * time.Hour))
	if !r.Decide(at(50*time.Hour, true)) {
		t.Fatal("第三次")
	}
	r.Sent(t0.Add(50 * time.Hour))
	if r.Stopped != StopDone || r.Decide(at(100*time.Hour, true)) {
		t.Fatalf("3 次後停止：%+v", r)
	}

	for reason, in := range map[string]RemindInput{
		StopInstalled:   {HasModel: true, Neural: true},
		StopDownloaded:  {Downloaded: true, Neural: true},
		StopStatistical: {Neural: false},
	} {
		r := Remind{Committed: true}
		in.Now = t0.Add(5 * time.Hour)
		r.FirstSeen = t0
		if r.Decide(in) || r.Stopped != reason {
			t.Fatalf("%s：%+v", reason, r)
		}
		// 永久停止：條件不再成立也不重新開始
		if r.Decide(at(100*time.Hour, true)) || r.Stopped != reason {
			t.Fatalf("%s 之後又開始提醒", reason)
		}
	}
	// 發行版關掉：不送，也不是永久停止
	r = Remind{FirstSeen: t0, Committed: true}
	in := at(5*time.Hour, true)
	in.Disabled = true
	if r.Decide(in) || r.Stopped != "" || !r.NextAt.Equal(t0.Add(29*time.Hour)) {
		t.Fatalf("disabled：%+v", r)
	}
	if !r.Decide(at(30*time.Hour, true)) {
		t.Fatal("打開後照常提醒")
	}
}

func TestEngineShouldCheck(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	now := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
	if !EngineShouldCheck(dir, dictA, now) {
		t.Fatal("沒有紀錄：檢查一次（記下安裝時間）")
	}
	r := Remind{NextAt: now.Add(time.Hour)}
	s.SaveRemind(r)
	if EngineShouldCheck(dir, dictA, now) || !EngineShouldCheck(dir, dictA, now.Add(time.Hour)) {
		t.Fatal("依 next_at")
	}
	r.Stop(StopDismissed, now)
	s.SaveRemind(r)
	if EngineShouldCheck(dir, dictA, now.Add(100*time.Hour)) {
		t.Fatal("停止後不檢查")
	}
	// 有權重、沒有這份字典的配對（升級換了字典）：要重新配對，跟提醒停了沒有無關
	os.MkdirAll(filepath.Join(s.Root, "weights", RoleSelect), 0o700)
	if !EngineShouldCheck(dir, dictA, now) {
		t.Fatal("要重新配對")
	}
	r.RepairFailed = dictA
	s.SaveRemind(r)
	if EngineShouldCheck(dir, dictA, now) {
		t.Fatal("同一份字典重新配對失敗過，不再一直重試")
	}
	if !EngineShouldCheck(dir, dictB, now) {
		t.Fatal("換了字典再試")
	}
}
