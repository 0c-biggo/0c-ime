//go:build linux

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/internal/models"
	"github.com/0c-biggo/0c-ime/internal/settings"
)

func hexSum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// modelsFixture：系統安裝的布局（字典、worker、pairing.lock）、一份小的 models.lock，與本機的假 Hugging Face。
type modelsFixture struct {
	env      modelsEnv
	hits     *int
	reloads  int
	notices  int
	noticeOK bool
	opened   []string
}

func writeModelFile(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func upstreamMeta(t *testing.T, role, name string) []byte {
	b, err := os.ReadFile(filepath.Join("..", "..", "models", "upstream", role, name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func fakeModel(role string) []byte { return bytes.Repeat([]byte(role), 50000) }

// fixtureManifest 是上游 manifest，model.onnx 的雜湊換成假模型的（status 顯示 manifest 記的雜湊）。
func fixtureManifest(t *testing.T, role string) []byte {
	b := upstreamMeta(t, role, "manifest.json")
	var m struct{ Files map[string]string }
	json.Unmarshal(b, &m)
	return bytes.Replace(b, []byte(m.Files["model.onnx"]), []byte(hexSum(fakeModel(role))), 1)
}

func newModelsFixture(t *testing.T) *modelsFixture {
	t.Helper()
	root := t.TempDir()
	base := filepath.Join(root, "home", ".local/share/zuin")
	share := filepath.Join(root, "usr/share/0c")
	data := filepath.Join(share, "data")
	writeModelFile(t, filepath.Join(data, "manifest.json"), []byte(`{"dictionary":"a"}`+"\n"))
	writeModelFile(t, filepath.Join(share, "neural", "sequence_runtime.py"), []byte("#"))
	writeModelFile(t, filepath.Join(share, "neural", "punct_runtime.py"), []byte("#"))
	os.MkdirAll(base, 0o700)

	files := map[string][]byte{}
	lock := map[string]any{"schema": 1}
	ms := map[string]any{}
	for i, role := range models.Roles {
		rev := fmt.Sprintf("%040x", i+1)
		repo := "Funmula/test-" + role
		content := map[string][]byte{"model.onnx": fakeModel(role), "LICENSE": []byte("MIT\n"),
			"manifest.json": fixtureManifest(t, role), "vocab.json": upstreamMeta(t, role, "vocab.json")}
		fs := map[string]any{}
		for n, b := range content {
			fs[n] = map[string]any{"sha256": hexSum(b), "size": len(b)}
			files["/"+repo+"/resolve/"+rev+"/"+n] = b
		}
		ms[role] = map[string]any{"repository": repo, "revision": rev, "version": "test-" + role, "license": "MIT", "files": fs}
	}
	lock["models"] = ms
	raw, _ := json.MarshalIndent(lock, "", "  ")
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		b, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	t.Setenv(modelsMirrorEnv, srv.URL)

	f := &modelsFixture{hits: &hits, noticeOK: true}
	f.env = modelsEnv{base: base, data: data, goos: "linux", lock: raw, dial: noEngine,
		runtimeCheck:         func(string) []string { return []string{"python3-onnxruntime"} },
		distributionDisabled: func(string) bool { return false }, now: time.Now}
	f.writePairing(t)
	oldReload, oldSend, oldOpen := reloadEngineForModels, sendModelsNotice, openSettingsPage
	reloadEngineForModels = func() { f.reloads++ }
	sendModelsNotice = func() error {
		f.notices++
		if !f.noticeOK {
			return errors.New("沒有通知服務")
		}
		return nil
	}
	openSettingsPage = func(page string) error { f.opened = append(f.opened, page); return nil }
	t.Cleanup(func() { reloadEngineForModels, sendModelsNotice, openSettingsPage = oldReload, oldSend, oldOpen })
	return f
}

// writePairing 依目前的字典寫 pairing.lock.json（發版時由 scripts/package_release.py 產生）。
func (f *modelsFixture) writePairing(t *testing.T) {
	l, err := models.ParseLock(f.env.lock)
	if err != nil {
		t.Fatal(err)
	}
	dict := f.env.dictionary()
	p := models.Pairing{Schema: 1, Dictionary: dict, ModelsLock: l.SHA256, Packages: map[string]map[string]models.File{}}
	for _, role := range models.Roles {
		out, err := models.Pair(fixtureManifest(t, role), upstreamMeta(t, role, "vocab.json"), dict)
		if err != nil {
			t.Fatal(err)
		}
		p.Packages[role] = map[string]models.File{}
		for n, b := range out {
			p.Packages[role][n] = models.File{SHA256: hexSum(b), Size: int64(len(b))}
		}
	}
	b, _ := json.MarshalIndent(p, "", "  ")
	writeModelFile(t, filepath.Join(filepath.Dir(f.env.data), "neural", pairingLockFile), b)
}

func (f *modelsFixture) status(t *testing.T) modelsStatus {
	t.Helper()
	st, err := f.env.status()
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestModelsStatusWithoutModels(t *testing.T) {
	f := newModelsFixture(t)
	st := f.status(t)
	if st.Source != "none" || !st.CanDownload || st.DisabledReason != "" || st.Need != "download" || st.Operation != nil || st.LastError != nil {
		t.Fatalf("%+v", st)
	}
	if st.Runtime.OK || strings.Join(st.Runtime.Missing, ",") != "python3-onnxruntime" {
		t.Fatalf("runtime %+v", st.Runtime)
	}
	if len(st.Models) != 2 || st.Models[0].Role != "select" || st.Models[1].Label != "補標點" {
		t.Fatalf("models %+v", st.Models)
	}
	for _, m := range st.Models { // 下載前顯示來源、授權與大小
		if m.Path != "" || m.SHA256 != "" || m.Repository == "" || m.Revision == "" || m.License != "MIT" || m.Size == 0 ||
			!strings.HasPrefix(m.URL, "https://huggingface.co/"+m.Repository+"/tree/") {
			t.Fatalf("%+v", m)
		}
	}
	// 契約的欄位都在（未安裝時 operation、last_error 是 null）
	b, _ := json.Marshal(st)
	var doc map[string]any
	json.Unmarshal(b, &doc)
	for _, k := range []string{"schema", "source", "can_download", "disabled_reason", "runtime", "need", "download_size", "models", "operation", "last_error"} {
		if _, ok := doc[k]; !ok {
			t.Fatalf("缺少 %s：%s", k, b)
		}
	}
}

func TestModelsStatusDisabledReasons(t *testing.T) {
	f := newModelsFixture(t)
	f.env.goos = "darwin"
	if st := f.status(t); st.CanDownload || st.DisabledReason != modelsDisabledPlatform {
		t.Fatalf("%+v", st)
	}
	f.env.goos = "linux"
	f.env.distributionDisabled = func(string) bool { return true }
	if st := f.status(t); st.CanDownload || st.DisabledReason != modelsDisabledDistribution {
		t.Fatalf("%+v", st)
	}
	f.env.distributionDisabled = func(string) bool { return false }
	// 發行版自己重建字典：配對清單對不上，不讓使用者白下載
	writeModelFile(t, filepath.Join(f.env.data, "manifest.json"), []byte(`{"dictionary":"rebuilt"}`+"\n"))
	if st := f.status(t); st.CanDownload || st.DisabledReason != modelsDisabledDictionary {
		t.Fatalf("%+v", st)
	}
	out, err := captureStdout(t, func() error {
		old := newModelsEnv
		newModelsEnv = func() modelsEnv { return f.env }
		defer func() { newModelsEnv = old }()
		return modelsCommand([]string{"download", "-json"})
	})
	var doc struct {
		OK    bool
		Error struct{ Code, Message string }
	}
	if !errors.Is(err, errReported) || json.Unmarshal([]byte(out), &doc) != nil || doc.OK || doc.Error.Code != codeModelsUnavailable {
		t.Fatalf("%v %s", err, out)
	}
}

// 官網套件內建模型：只回報資訊。內建的不是 Hugging Face 那一份時不附網址。
func TestModelsStatusBundled(t *testing.T) {
	f := newModelsFixture(t)
	neural := filepath.Join(filepath.Dir(f.env.data), "neural")
	l, _ := models.ParseLock(f.env.lock)
	for _, role := range models.Roles {
		sha := strings.Repeat("c", 64)
		if role == models.RolePunct {
			sha = l.Models[role].Files["model.onnx"].SHA256
		}
		writeModelFile(t, filepath.Join(neural, role, "manifest.json"), []byte(`{"files":{"model.onnx":"`+sha+`"}}`))
		writeModelFile(t, filepath.Join(neural, role, "model.onnx"), []byte("onnx"))
	}
	st := f.status(t)
	if st.Source != "bundled" || st.CanDownload || st.DisabledReason != modelsDisabledBundled || st.Need != "none" {
		t.Fatalf("%+v", st)
	}
	sel, punct := st.Models[0], st.Models[1]
	if sel.Path != filepath.Join(neural, "select") || sel.SHA256 != strings.Repeat("c", 64) || sel.Size != 4 || sel.URL != "" || sel.Repository != "" || sel.License != "" {
		t.Fatalf("內建的選字不是上游版本，不附網址：%+v", sel)
	}
	if punct.URL == "" || punct.Revision != l.Models["punct"].Revision || punct.License != "MIT" {
		t.Fatalf("內建的補標點等於上游版本，附上網址：%+v", punct)
	}
	if _, err := f.env.remove(); models.AsError(err).Code != codeModelsBundled {
		t.Fatalf("內建模型不能刪：%v", err)
	}
}

func TestModelsDownloadRemoveAndReload(t *testing.T) {
	f := newModelsFixture(t)
	if err := f.env.download(context.Background(), io.Discard); err != nil {
		t.Fatal(err)
	}
	st := f.status(t)
	if st.Source != "user" || st.Need != "none" || st.Operation != nil || st.LastError != nil || f.reloads != 1 {
		t.Fatalf("%+v reloads=%d", st, f.reloads)
	}
	for _, m := range st.Models {
		if m.Source != "user" || !strings.HasPrefix(m.Path, filepath.Join(f.env.base, "models", st.Dictionary)) || m.URL == "" || m.SHA256 == "" {
			t.Fatalf("%+v", m)
		}
	}
	if f.env.store().LoadRemind().Stopped != models.StopDownloaded {
		t.Fatal("下載後不再提醒")
	}
	// 引擎的找法找得到（neural.Find 的個人目錄）
	if _, err := os.Stat(filepath.Join(f.env.base, "models", st.Dictionary, "select", "manifest.json")); err != nil {
		t.Fatal(err)
	}
	st, err := f.env.remove()
	if err != nil {
		t.Fatal(err)
	}
	if st.Source != "none" || st.Need != "download" || f.reloads != 2 {
		t.Fatalf("刪除後回到統計：%+v reloads=%d", st, f.reloads)
	}
}

func TestModelsDownloadFailureIsReported(t *testing.T) {
	f := newModelsFixture(t)
	t.Setenv(modelsMirrorEnv, "http://127.0.0.1:1") // 連不上
	err := f.env.download(context.Background(), io.Discard)
	if models.AsError(err).Code != models.CodeNetwork {
		t.Fatalf("%v", err)
	}
	st := f.status(t)
	if st.LastError == nil || st.LastError.Code != models.CodeNetwork || st.Source != "none" || st.Operation != nil || f.reloads != 0 {
		t.Fatalf("%+v", st)
	}
	entries, _ := os.ReadDir(filepath.Join(f.env.base, "models"))
	for _, e := range entries {
		if e.Name() != "state.json" {
			t.Fatalf("失敗後留下 %s", e.Name())
		}
	}
}

// 升級換了字典、模型沒換：引擎叫 remind，在本機重新配對，不連網，再請引擎交接；不送通知。
func TestModelsRemindRepairsAfterDictionaryUpgrade(t *testing.T) {
	f := newModelsFixture(t)
	if err := f.env.download(context.Background(), io.Discard); err != nil {
		t.Fatal(err)
	}
	old := f.env.dictionary()
	writeModelFile(t, filepath.Join(f.env.data, "manifest.json"), []byte(`{"dictionary":"b"}`+"\n"))
	f.writePairing(t) // 新套件帶著新字典的配對清單
	if st := f.status(t); st.Need != "repair" || st.Source != "none" {
		t.Fatalf("%+v", st)
	}
	hits, reloads := *f.hits, f.reloads
	if err := f.env.remind(false); err != nil {
		t.Fatal(err)
	}
	st := f.status(t)
	if st.Source != "user" || st.Need != "none" || *f.hits != hits || f.reloads != reloads+1 || f.notices != 0 {
		t.Fatalf("%+v hits %d→%d reloads %d notices %d", st, hits, *f.hits, f.reloads, f.notices)
	}
	if _, err := os.Stat(filepath.Join(f.env.base, "models", old)); !os.IsNotExist(err) {
		t.Fatal("舊字典的配對應刪除")
	}
}

// 模型換了（models.lock 改變）：remind 不重新配對、不送通知，只由設定頁提示重新下載；引擎之後不再為這份字典叫 remind。
func TestModelsRemindAfterModelUpgradeOnlyFlagsRedownload(t *testing.T) {
	f := newModelsFixture(t)
	if err := f.env.download(context.Background(), io.Discard); err != nil {
		t.Fatal(err)
	}
	var lock map[string]any
	json.Unmarshal(f.env.lock, &lock)
	lock["models"].(map[string]any)["select"].(map[string]any)["revision"] = strings.Repeat("9", 40)
	f.env.lock, _ = json.MarshalIndent(lock, "", "  ")
	writeModelFile(t, filepath.Join(f.env.data, "manifest.json"), []byte(`{"dictionary":"c"}`+"\n"))
	f.writePairing(t)
	if st := f.status(t); st.Need != "redownload" || !st.CanDownload {
		t.Fatalf("%+v", st)
	}
	if err := f.env.remind(true); err != nil {
		t.Fatal(err)
	}
	if f.notices != 0 || f.reloads != 1 {
		t.Fatalf("notices %d reloads %d", f.notices, f.reloads)
	}
	if models.EngineShouldCheck(f.env.base, f.env.dictionary(), time.Now().Add(1000*time.Hour)) {
		t.Fatal("引擎不應再為這份字典叫 remind")
	}
}

func TestModelsReminderSchedule(t *testing.T) {
	f := newModelsFixture(t)
	t0 := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
	now := t0
	f.env.now = func() time.Time { return now }
	step := func(d time.Duration, committed bool) {
		now = t0.Add(d)
		if err := f.env.remind(committed); err != nil {
			t.Fatal(err)
		}
	}
	step(0, false) // 引擎第一次啟動：記下時間
	step(30*time.Minute, true)
	if f.notices != 0 {
		t.Fatal("安裝不到一小時")
	}
	f.noticeOK = false
	step(2*time.Hour, true)
	if f.notices != 1 || f.env.store().LoadRemind().Count != 0 {
		t.Fatal("通知服務不在時不算一次")
	}
	f.noticeOK = true
	step(27*time.Hour, true)
	step(30*time.Hour, true)
	step(52*time.Hour, true)
	step(77*time.Hour, true)
	step(200*time.Hour, true)
	r := f.env.store().LoadRemind()
	if f.notices != 4 || r.Count != 3 || r.Stopped != models.StopDone {
		t.Fatalf("notices %d %+v", f.notices, r)
	}
}

func TestModelsReminderStops(t *testing.T) {
	t.Run("statistical", func(t *testing.T) {
		f := newModelsFixture(t)
		writeModelFile(t, settings.Path(filepath.Join(f.env.base, "user.json")), []byte(`{"schema":1,"selection":"statistical"}`))
		f.env.remind(true)
		if r := f.env.store().LoadRemind(); r.Stopped != models.StopStatistical {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("dismiss", func(t *testing.T) {
		f := newModelsFixture(t)
		if err := f.env.noticeAction(modelsActionDismiss); err != nil {
			t.Fatal(err)
		}
		f.env.now = func() time.Time { return time.Now().Add(100 * time.Hour) }
		f.env.remind(true)
		if r := f.env.store().LoadRemind(); r.Stopped != models.StopDismissed || f.notices != 0 {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("download button opens the models page", func(t *testing.T) {
		f := newModelsFixture(t)
		if err := f.env.noticeAction(modelsActionDownload); err != nil || strings.Join(f.opened, ",") != "models" {
			t.Fatalf("%v %v", err, f.opened)
		}
		if f.env.store().LoadRemind().Stopped != "" {
			t.Fatal("只開設定頁，不算停止（下載後才停止）")
		}
	})
	t.Run("distribution", func(t *testing.T) {
		f := newModelsFixture(t)
		f.env.distributionDisabled = func(string) bool { return true }
		f.env.now = func() time.Time { return time.Now().Add(-100 * time.Hour) }
		f.env.remind(false)
		f.env.now = time.Now
		f.env.remind(true)
		if r := f.env.store().LoadRemind(); f.notices != 0 || r.Stopped != "" {
			t.Fatalf("%+v", r)
		}
	})
}

// 套件管理員模式（Debian）也能用 models。
func TestPackageManagedAllowsModels(t *testing.T) {
	setPackageManaged(t, true)
	if err := refusePackageManaged("models", []string{"status", "-json"}); err != nil {
		t.Fatal(err)
	}
}
