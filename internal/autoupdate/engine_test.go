//go:build !windows

package autoupdate

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/bridge/ipc"
	"github.com/0c-biggo/0c-ime/core/engine"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/internal/enginedir"
	"github.com/0c-biggo/0c-ime/internal/update"
)

// linuxArchive 產生一個最小的 Linux 套件壓縮包；引擎以殼腳本代替，-check-data 依 fail 決定成敗。
func linuxArchive(t *testing.T, version string, fail bool) (string, string) {
	t.Helper()
	return linuxArchiveWith(t, version, fail, nil)
}

// linuxArchiveWith 同上，另外加入 extra 指定的檔案（路徑 → 內容）。
func linuxArchiveWith(t *testing.T, version string, fail bool, extra map[string]string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "0c-"+version)
	server := "#!/bin/sh\nexit 0\n"
	if fail {
		server = "#!/bin/sh\necho broken >&2\nexit 3\n"
	}
	files := map[string]string{"bin/0c-server": server, "bin/0c-update": "u", "install.py": "i", "lib/lib0c.so": "so",
		"data/manifest.json": "{}", "data/lexicon.bin": "l", "data/lm.bin": "m", "data/NOTICE.txt": "n", "data/report.txt": "r",
		"neural/select/manifest.json": "{}"}
	for name, body := range extra {
		files[name] = body
	}
	r := update.Release{Schema: 1, Product: "0c", Version: version, Channel: "public-beta", OS: "linux", Arch: "amd64", Files: map[string]string{}}
	for name, body := range files {
		p := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(p), 0o700)
		mode := os.FileMode(0o600)
		if name == "bin/0c-server" || name == "bin/0c-settings" {
			mode = 0o700
		}
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		r.Files[name], _ = update.Hash(p)
	}
	b, _ := json.Marshal(r)
	os.WriteFile(filepath.Join(root, "release.json"), b, 0o600)
	archive := filepath.Join(dir, "bundle.tar.gz")
	f, _ := os.Create(archive)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		rel, _ := filepath.Rel(dir, p)
		h, _ := tar.FileInfoHeader(info, "")
		h.Name = filepath.ToSlash(rel)
		if info.IsDir() {
			h.Name += "/"
		}
		tw.WriteHeader(h)
		if !info.IsDir() {
			c, _ := os.ReadFile(p)
			tw.Write(c)
		}
		return nil
	})
	tw.Close()
	gz.Close()
	f.Close()
	sum, _ := update.Hash(archive)
	return archive, sum
}

var linuxCore = CoreLayout{Server: "bin/0c-server", Update: "bin/0c-update"}

func TestPrepareCoreSwitchRevertAndPrune(t *testing.T) {
	base := t.TempDir()
	archive, sum := linuxArchive(t, "0.1.0-dev.18", false)
	target := Target{Version: "0.1.0-dev.18", Kind: "core", SHA256: sum}
	dir, err := PrepareCore(base, "", archive, target, "linux", "amd64", linuxCore)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"0c-server", "0c-update", "data/manifest.json", "neural/select/manifest.json"} {
		if _, err := os.Stat(filepath.Join(base, dir, p)); err != nil {
			t.Fatalf("missing %s: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(base, dir, "install.py")); err == nil {
		t.Fatal("shell files must not be copied into a core directory")
	}
	if again, err := PrepareCore(base, "", archive, target, "linux", "amd64", linuxCore); err != nil || again != dir {
		t.Fatalf("prepare must be idempotent: %v %v", again, err)
	}
	if err := SwitchPointer(base, dir, target.Version); err != nil || PointerVersion(base) != "0.1.0-dev.18" {
		t.Fatalf("switch: %v %q", err, PointerVersion(base))
	}
	if err := RevertPointer(base, dir); err != nil {
		t.Fatal(err)
	}
	if c, _ := enginedir.Read(base); c != nil {
		t.Fatal("revert to no pointer must remove engine.json")
	}
	// 切換中途被中止後重跑：原指標保留最初那一個；回復可以重複執行。
	old := "versions/0.1.0-dev.17-c-bbbbbbbbbbbb"
	os.MkdirAll(filepath.Join(base, old, "data"), 0o700)
	os.WriteFile(filepath.Join(base, old, enginedir.Executable()), []byte("x"), 0o700)
	os.WriteFile(filepath.Join(base, old, "data/manifest.json"), []byte("{}"), 0o600)
	if err := SwitchPointer(base, old, "0.1.0-dev.17"); err != nil {
		t.Fatal(err)
	}
	SwitchPointer(base, dir, target.Version)
	SwitchPointer(base, dir, target.Version) // 重跑不可蓋掉原指標
	for i := 0; i < 2; i++ {
		if err := RevertPointer(base, dir); err != nil {
			t.Fatal(err)
		}
		if c, _ := enginedir.Read(base); c == nil || c.Directory != old {
			t.Fatalf("revert #%d: %+v", i+1, c)
		}
	}
	enginedir.Remove(base)
	os.MkdirAll(filepath.Join(base, "versions/0.1.0-dev.17-c-aaaaaaaaaaaa"), 0o700)
	os.MkdirAll(filepath.Join(base, "versions/0.1.0-dev.17-abcdef"), 0o700) // 完整安裝的目錄不歸這裡管
	SwitchPointer(base, dir, target.Version)
	PruneCore(base)
	if _, err := os.Stat(filepath.Join(base, "versions/0.1.0-dev.17-c-aaaaaaaaaaaa")); err == nil {
		t.Fatal("stale core directory must be pruned")
	}
	for _, keep := range []string{dir, "versions/0.1.0-dev.17-abcdef"} {
		if _, err := os.Stat(filepath.Join(base, keep)); err != nil {
			t.Fatalf("%s must be kept", keep)
		}
	}
}

// 設定程式是核心版本目錄的選用成員：複製並記錄雜湊；舊版佈局（不認得它）與沒有它的套件都照常準備。
func TestPrepareCoreCopiesSettingsProgramAndRecordsHashes(t *testing.T) {
	withSettings := CoreLayout{Server: "bin/0c-server", Update: "bin/0c-update", Settings: "bin/0c-settings"}
	archive, sum := linuxArchiveWith(t, "0.1.0-dev.43", false, map[string]string{"bin/0c-settings": "settings-program"})
	target := Target{Version: "0.1.0-dev.43", Kind: "core", SHA256: sum}
	want := sha256.Sum256([]byte("settings-program"))

	base := t.TempDir()
	dir, err := PrepareCore(base, "", archive, target, "linux", "amd64", withSettings)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(base, dir, "0c-settings"))
	if err != nil || st.Mode().Perm()&0o100 == 0 {
		t.Fatalf("設定程式應複製並保持可執行：%v %v", st, err)
	}
	hashes, err := enginedir.ReadFiles(filepath.Join(base, dir))
	if err != nil || hashes["0c-settings"] != hex.EncodeToString(want[:]) || hashes["0c-server"] == "" || hashes["0c-update"] == "" {
		t.Fatalf("應記錄最上層檔案的雜湊：%v %v", hashes, err)
	}
	for name := range hashes {
		if name != "0c-server" && name != "0c-update" && name != "0c-settings" {
			t.Errorf("不該記錄 %s（data／neural 檔案太多，不逐一記錄）", name)
		}
	}
	if got, ok := enginedir.Dir(base); ok {
		t.Fatalf("尚未切換指標，不該有目前版本目錄：%s", got)
	}
	if err := SwitchPointer(base, dir, target.Version); err != nil {
		t.Fatal(err)
	}
	if got, ok := enginedir.Dir(base); !ok || got != filepath.Join(base, dir) {
		t.Fatalf("Dir：%q %v", got, ok)
	}

	// 舊版佈局不認得設定程式：略過它，其餘照常，也不在記錄裡出現。
	old := t.TempDir()
	dir, err = PrepareCore(old, "", archive, target, "linux", "amd64", linuxCore)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(old, dir, "0c-settings")); err == nil {
		t.Fatal("不認得設定程式的佈局不得複製它")
	}
	if hashes, _ := enginedir.ReadFiles(filepath.Join(old, dir)); hashes["0c-settings"] != "" || hashes["0c-server"] == "" {
		t.Fatalf("%v", hashes)
	}

	// 套件沒有設定程式：照常準備，只是沒有它。
	plain, plainSum := linuxArchive(t, "0.1.0-dev.42", false)
	dir, err = PrepareCore(t.TempDir(), "", plain, Target{Version: "0.1.0-dev.42", Kind: "core", SHA256: plainSum}, "linux", "amd64", withSettings)
	if err != nil || dir == "" {
		t.Fatal(err)
	}
}

// 0c-mcp（#126）同樣是選用成員：複製並記錄雜湊，切換後 Windows 由它換上 bin\0c-mcp.exe；不認得它的佈局略過。
func TestPrepareCoreCopiesMCPAndRecordsItsHash(t *testing.T) {
	withMCP := CoreLayout{Server: "bin/0c-server", Update: "bin/0c-update", MCP: "bin/0c-mcp"}
	archive, sum := linuxArchiveWith(t, "0.2.0-dev.1", false, map[string]string{"bin/0c-mcp": "mcp-bridge"})
	target := Target{Version: "0.2.0-dev.1", Kind: "core", SHA256: sum}
	want := sha256.Sum256([]byte("mcp-bridge"))
	base := t.TempDir()
	dir, err := PrepareCore(base, "", archive, target, "linux", "amd64", withMCP)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(base, dir, "0c-mcp")); err != nil || string(b) != "mcp-bridge" {
		t.Fatalf("0c-mcp 應複製到核心目錄最上層：%q %v", b, err)
	}
	if hashes, err := enginedir.ReadFiles(filepath.Join(base, dir)); err != nil || hashes["0c-mcp"] != hex.EncodeToString(want[:]) {
		t.Fatalf("應記錄 0c-mcp 的雜湊：%v %v", hashes, err)
	}
	old := t.TempDir()
	if dir, err = PrepareCore(old, "", archive, target, "linux", "amd64", linuxCore); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(old, dir, "0c-mcp")); err == nil {
		t.Fatal("不認得 0c-mcp 的佈局不得複製它")
	}
}

func TestReadFilesIsNilForDirectoriesPreparedByOlderUpdaters(t *testing.T) {
	if hashes, err := enginedir.ReadFiles(t.TempDir()); hashes != nil || err != nil {
		t.Fatalf("%v %v", hashes, err)
	}
}

func TestPrepareCoreRejectsBrokenEngineAndWrongVersion(t *testing.T) {
	base := t.TempDir()
	archive, sum := linuxArchive(t, "0.1.0-dev.18", true)
	if _, err := PrepareCore(base, "", archive, Target{Version: "0.1.0-dev.18", SHA256: sum}, "linux", "amd64", linuxCore); err == nil {
		t.Fatal("an engine that fails -check-data must not be prepared")
	}
	entries, _ := os.ReadDir(filepath.Join(base, "versions"))
	if len(entries) != 0 {
		t.Fatalf("failed prepare must leave nothing: %v", entries)
	}
	archive, sum = linuxArchive(t, "0.1.0-dev.18", false)
	if _, err := PrepareCore(base, "", archive, Target{Version: "0.1.0-dev.19", SHA256: sum}, "linux", "amd64", linuxCore); err == nil {
		t.Fatal("bundle version must match the signed feed")
	}
}

func toyEngine(t *testing.T) *engine.Engine {
	lf, _ := os.Open("../../testdata/toy/lexicon.tsv")
	defer lf.Close()
	lex, err := lexicon.Load(lf)
	if err != nil {
		t.Fatal(err)
	}
	mf, _ := os.Open("../../testdata/toy/lm.tsv")
	defer mf.Close()
	sys, err := lm.Load(mf)
	if err != nil {
		t.Fatal(err)
	}
	return engine.New(lex, sys)
}

func TestHealthAndDrainOverSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "e.sock")
	dial := func(d time.Duration) (net.Conn, error) { return net.DialTimeout("unix", sock, d) }
	if _, running, err := Health(dial); running || err != nil {
		t.Fatal("no engine must report not running without error")
	}
	if err := Drain(context.Background(), dial, time.Second); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &ipc.Server{Engine: toyEngine(t), Info: map[string]string{"version": "0.1.0-dev.18"}, DrainTimeout: 2 * time.Second}
	srv.OnDrained = func() { time.Sleep(300 * time.Millisecond); l.Close() } // 模擬存檔需要時間
	go srv.Serve(l)
	info, running, err := Health(dial)
	if !running || err != nil || info["version"] != "0.1.0-dev.18" || info["idle_seconds"] != "0" {
		t.Fatalf("%v %v %v", info, running, err)
	}
	if err := Drain(context.Background(), dial, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, running, _ := Health(dial); running {
		t.Fatal("drain must return only after the engine released its name")
	}
}
