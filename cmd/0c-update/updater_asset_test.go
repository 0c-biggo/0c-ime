package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/update"
)

// updaterPackage 建立只有 release.json 與更新器的套件（測試用；真的套件另有引擎與字典）。
func updaterPackage(t *testing.T, version, body string) (string, string) {
	t.Helper()
	files := update.UpdaterFiles(runtime.GOOS)[:1]
	sum := func(s string) string {
		p := filepath.Join(t.TempDir(), "x")
		os.WriteFile(p, []byte(s), 0o600)
		h, _ := update.Hash(p)
		return h
	}
	release, _ := json.Marshal(update.Release{Schema: 1, Product: "0c", Version: version, Channel: "public-beta", OS: runtime.GOOS, Arch: runtime.GOARCH,
		Files: map[string]string{files[0]: sum(body)}})
	root := "0c-" + version + "/"
	path := filepath.Join(t.TempDir(), "0c-"+version+".tar.gz")
	if runtime.GOOS == "windows" {
		path = strings.TrimSuffix(path, ".tar.gz") + ".zip"
	}
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	members := [][2]string{{"release.json", string(release)}, {files[0], body}}
	if runtime.GOOS == "windows" {
		w := zip.NewWriter(out)
		for _, m := range members {
			f, _ := w.Create(root + m[0])
			f.Write([]byte(m[1]))
		}
		w.Close()
	} else {
		gz := gzip.NewWriter(out)
		tw := tar.NewWriter(gz)
		for _, m := range members {
			tw.WriteHeader(&tar.Header{Name: root + m[0], Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(m[1]))})
			tw.Write([]byte(m[1]))
		}
		tw.Close()
		gz.Close()
	}
	out.Close()
	h, _ := update.Hash(path)
	return path, h
}

func offerUpdater(t *testing.T, base, version, archive, sha string) {
	t.Helper()
	if _, err := autoupdate.EditState(base, func(s *autoupdate.State) error {
		s.UpdaterOffer = &autoupdate.Target{Version: version, Kind: "updater", URL: "https://example.org/" + filepath.Base(archive), SHA256: sha, Size: 1}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func withUpdaterDownloads(t *testing.T, archive string) *int {
	t.Helper()
	count := 0
	old, oldVersion := downloadUpdaterArchive, buildVersion
	downloadUpdaterArchive = func(_ context.Context, _ autoupdate.Target, dir string) (string, error) {
		count++
		os.MkdirAll(dir, 0o700)
		dst := filepath.Join(dir, filepath.Base(archive))
		b, err := os.ReadFile(archive)
		if err != nil {
			return "", err
		}
		return dst, os.WriteFile(dst, b, 0o600)
	}
	buildVersion = "0.2.15"
	t.Cleanup(func() { downloadUpdaterArchive, buildVersion = old, oldVersion })
	return &count
}

// 只換更新器（#295）：從套件取出更新器、自我測試後換上穩定副本，記下來源；之後的修復、核心生效、完整安裝都不換回舊版。
func TestUpdaterOfferReplacesOnlyTheStableUpdater(t *testing.T) {
	if !updaterAssetSupported {
		t.Skip("這個平台的更新器不能單獨換")
	}
	base := withPreviousKept(t)
	stable := stableUpdater(base)
	writeFile(t, stable, "updater 0.2.15")
	archive, sha := updaterPackage(t, "0.2.16", "updater 0.2.16")
	downloads := withUpdaterDownloads(t, archive)
	offerUpdater(t, base, "0.2.16", archive, sha)

	applyUpdaterOffer(base)
	if contentOf(t, stable) != "updater 0.2.16" || *downloads != 1 {
		t.Fatalf("stable %q downloads %d", contentOf(t, stable), *downloads)
	}
	s, _ := autoupdate.Load(base)
	if s.UpdaterTried == nil || s.UpdaterTried.Result != "installed" {
		t.Fatalf("attempt %+v", s.UpdaterTried)
	}
	if m := readStableMarker(base); m == nil || m.Source != stableFromUpdaterAsset || m.Version != "0.2.16" {
		t.Fatalf("marker %+v", m)
	}
	if _, err := os.Stat(filepath.Join(base, "update", "stage")); err == nil {
		entries, _ := os.ReadDir(filepath.Join(base, "update", "stage"))
		if len(entries) != 0 {
			t.Fatal("stage left behind")
		}
	}
	applyUpdaterOffer(base) // 已經換上：不再下載
	if *downloads != 1 {
		t.Fatal("downloaded again")
	}
	// 目前版本（0.2.15）的更新器不把它換回去。
	current := filepath.Join(base, "versions", "0.2.15", updaterName())
	writeFile(t, current, "updater 0.2.15")
	if err := adoptStableUpdater(current, "0.2.15", base); !errors.Is(err, errStableNewer) || contentOf(t, stable) != "updater 0.2.16" {
		t.Fatalf("downgraded: %v %q", err, contentOf(t, stable))
	}
	// 更新的版本照常換上，標記改成一般來源。
	next := filepath.Join(base, "versions", "0.2.17", updaterName())
	writeFile(t, next, "updater 0.2.17")
	if err := adoptStableUpdater(next, "0.2.17", base); err != nil || contentOf(t, stable) != "updater 0.2.17" {
		t.Fatalf("upgrade: %v", err)
	}
	if m := readStableMarker(base); m == nil || m.Source != "" || m.Version != "0.2.17" {
		t.Fatalf("marker %+v", m)
	}
}

// 沒通過自我測試：保留原本的穩定副本，候選留著等冷卻後再試，不重新下載；外殼更新已下載同一份套件時直接用它。
func TestUpdaterOfferKeepsCurrentWhenNotQualified(t *testing.T) {
	if !updaterAssetSupported {
		t.Skip("這個平台的更新器不能單獨換")
	}
	base := withPreviousKept(t)
	stable := stableUpdater(base)
	writeFile(t, stable, "updater 0.2.15")
	archive, sha := updaterPackage(t, "0.2.16", "updater 0.2.16")
	downloads := withUpdaterDownloads(t, archive)
	calls := withSelftest(t, selftestFail, selftestPass)
	now := time.Now()
	qualifyNow = func() time.Time { return now }
	// 外殼更新等 UAC：同一份套件已經下載好
	if _, err := autoupdate.EditState(base, func(s *autoupdate.State) error {
		s.Target = &autoupdate.Target{Version: "0.2.16", Kind: "shell", SHA256: sha}
		s.Download = archive
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	offerUpdater(t, base, "0.2.16", archive, sha)

	applyUpdaterOffer(base)
	s, _ := autoupdate.Load(base)
	if contentOf(t, stable) != "updater 0.2.15" || *downloads != 0 || *calls != 1 || s.UpdaterTried.Result != "not_qualified" {
		t.Fatalf("stable %q downloads %d calls %d %+v", contentOf(t, stable), *downloads, *calls, s.UpdaterTried)
	}
	applyUpdaterOffer(base) // 冷卻中
	if *calls != 1 {
		t.Fatal("retried during cooldown")
	}
	now = now.Add(qualifyFailRetry + time.Hour)
	retryPendingUpdater(base) // 候選還在 stage：直接再測
	if contentOf(t, stable) != "updater 0.2.16" || *calls != 2 || *downloads != 0 {
		t.Fatalf("retry: stable %q calls %d", contentOf(t, stable), *calls)
	}
	if m := readStableMarker(base); m == nil || m.Source != stableFromUpdaterAsset {
		t.Fatalf("marker %+v", m)
	}
	if entries, _ := os.ReadDir(filepath.Join(base, "update", "stage")); len(entries) != 0 {
		t.Fatal("stage left behind")
	}
}

// 開發建置、套件版本不比自己新、套件內容與索引不符：都不換。
func TestUpdaterOfferIgnoresDevAndOlderAndBadPackages(t *testing.T) {
	if !updaterAssetSupported {
		t.Skip("這個平台的更新器不能單獨換")
	}
	base := withPreviousKept(t)
	stable := stableUpdater(base)
	writeFile(t, stable, "current")
	archive, sha := updaterPackage(t, "0.2.16", "updater 0.2.16")
	downloads := withUpdaterDownloads(t, archive)
	offerUpdater(t, base, "0.2.16", archive, sha)
	buildVersion = "dev"
	applyUpdaterOffer(base)
	buildVersion = "0.2.16"
	applyUpdaterOffer(base)
	if *downloads != 0 || contentOf(t, stable) != "current" {
		t.Fatal("dev or same version replaced the updater")
	}
	buildVersion = "0.2.15"
	offerUpdater(t, base, "0.2.17", archive, sha) // 索引說 0.2.17，套件是 0.2.16
	applyUpdaterOffer(base)
	s, _ := autoupdate.Load(base)
	if contentOf(t, stable) != "current" || s.UpdaterTried == nil || s.UpdaterTried.Result != "failed" {
		t.Fatalf("mismatched package: %q %+v", contentOf(t, stable), s.UpdaterTried)
	}
}
