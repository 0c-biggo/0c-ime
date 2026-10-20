package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/0c-biggo/0c-ime/internal/enginedir"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

func bundleFixture(t *testing.T, version string) string {
	t.Helper()
	return layoutFixture(t, version, windowsCurrent)
}
func layoutFixture(t *testing.T, version string, layout WindowsLayout) string {
	t.Helper()
	root := t.TempDir()
	r := Release{Schema: 1, Product: "0c", Version: version, Channel: "internal-preview", OS: "windows", Arch: "amd64", Files: map[string]string{}}
	for _, name := range append(layout.names(), "0c-update.exe", "data/manifest.json", "data/lexicon.bin", "data/lm.bin", "data/NOTICE.txt", "data/report.txt") {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(version+" "+name), 0600); err != nil {
			t.Fatal(err)
		}
		r.Files[name], _ = Hash(p)
	}
	if err := writeJSON(filepath.Join(root, "release.json"), r); err != nil {
		t.Fatal(err)
	}
	return root
}
func TestBundleRejectsTamperingExtrasLinksAndWrongPlatform(t *testing.T) {
	for _, change := range []string{"tamper", "extra", "link", "platform"} {
		t.Run(change, func(t *testing.T) {
			dir := bundleFixture(t, "0.1.0-dev.1")
			if _, err := Verify(dir, "windows", "amd64"); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "tamper":
				os.WriteFile(filepath.Join(dir, "0c_tsf.dll"), []byte("changed"), 0600)
			case "extra":
				os.WriteFile(filepath.Join(dir, "unexpected.dll"), []byte("extra"), 0600)
			case "link":
				os.Remove(filepath.Join(dir, "0c_tsf.dll"))
				if err := os.Symlink(filepath.Join(dir, "0c-ui.exe"), filepath.Join(dir, "0c_tsf.dll")); err != nil {
					t.Skip("symlink not available")
				}
			case "platform":
				if _, err := Verify(dir, "linux", "amd64"); err == nil {
					t.Fatal("accepted wrong platform")
				}
				return
			}
			if _, err := Verify(dir, "windows", "amd64"); err == nil {
				t.Fatal("accepted damaged bundle")
			}
		})
	}
}
func TestSafeArchiveNames(t *testing.T) {
	for _, name := range []string{"../file", "/absolute", `C:\evil`, `a\..\x`, "dir//file", "a/./b", "a/../b", "NUL.txt", "a/COM1", "a/file.", "a/file ", "a/x:stream"} {
		if SafeName(name) {
			t.Errorf("accepted %q", name)
		}
	}
	if !SafeName("0c-0.1.0/data/manifest.json") {
		t.Fatal("rejected valid path")
	}
}
func TestArchivesRejectTraversalLinksAndDuplicateNames(t *testing.T) {
	for _, kind := range []string{"traversal", "link", "duplicate", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "bad.tar.gz")
			out, err := os.Create(file)
			if err != nil {
				t.Fatal(err)
			}
			gz := gzip.NewWriter(out)
			tw := tar.NewWriter(gz)
			h := &tar.Header{Name: "bundle/file", Mode: 0600, Size: 1, Typeflag: tar.TypeReg}
			switch kind {
			case "traversal":
				h.Name = "../escape"
			case "link":
				h.Typeflag = tar.TypeSymlink
				h.Linkname = "../../escape"
				h.Size = 0
			case "hardlink":
				h.Typeflag = tar.TypeLink
				h.Linkname = "../../escape"
				h.Size = 0
			}
			if err = tw.WriteHeader(h); err != nil {
				t.Fatal(err)
			}
			if h.Size == 1 {
				tw.Write([]byte("x"))
			}
			if kind == "duplicate" {
				h.Name = "bundle/FILE"
				tw.WriteHeader(h)
				tw.Write([]byte("x"))
			}
			tw.Close()
			gz.Close()
			out.Close()
			if err = extract(file, t.TempDir()); err == nil {
				t.Fatal("accepted unsafe archive")
			}
		})
	}
}
func TestStageVerifiesHashAndZip(t *testing.T) {
	file := zipFixture(t, filepath.Join(t.TempDir(), "valid.zip"))
	hash, _ := Hash(file)
	staged, cleanup, err := Stage(file, hash, "windows", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if _, err = Verify(staged, "windows", "amd64"); err != nil {
		t.Fatal(err)
	}
	if _, clean, err := Stage(file, strings.Repeat("0", 64), "windows", "amd64"); err == nil {
		clean()
		t.Fatal("wrong archive hash accepted")
	}
}

// zipFixture 把測試套件壓成 file（根目錄 bundle/）。
func zipFixture(t *testing.T, file string) string {
	t.Helper()
	root := bundleFixture(t, "0.1.0-dev.1")
	f, _ := os.Create(file)
	z := zip.NewWriter(f)
	filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		w, e := z.Create("bundle/" + filepath.ToSlash(rel))
		if e != nil {
			return e
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		_, e = w.Write(b)
		return e
	})
	z.Close()
	f.Close()
	return file
}

// fakeRegistrar 的測試版本都在 FixedSince 之前：註冊版本目錄的 DLL（回到 dev.61 以前的舊版也走這條路）。
// 固定路徑（#121）的測試在 windows_fixed_test.go。
type fakeRegistrar struct {
	path    string
	failNew bool
	calls   []string
}

func (r *fakeRegistrar) Current() (string, error) { return r.path, nil }
func (r *fakeRegistrar) Register(p string) error {
	r.calls = append(r.calls, "register "+p)
	r.path = p
	if r.failNew && strings.Contains(p, "0.1.0-dev.2") {
		return errors.New("registration failure after partial write")
	}
	return nil
}
func (r *fakeRegistrar) Unregister(p string) error {
	r.calls = append(r.calls, "unregister "+p)
	r.path = ""
	return nil
}
func TestWindowsUpgradeRollbackAndUninstallPreserveUserData(t *testing.T) {
	base := t.TempDir()
	user := filepath.Join(base, "user.json")
	os.WriteFile(user, []byte("private learning"), 0600)
	r := &fakeRegistrar{}
	first, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.1"), base, "amd64", r)
	if err != nil {
		t.Fatal(err)
	}
	next, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.2"), base, "amd64", r)
	if err != nil {
		t.Fatal(err)
	}
	if first == next || r.path != filepath.Join(next, "0c_tsf.dll") {
		t.Fatal("not switched")
	}
	if _, err = os.Stat(filepath.Join(first, "0c_tsf.dll")); err != nil {
		t.Fatal("removed loaded old version")
	}
	if _, err = RollbackWindows(base, r, false); err != nil {
		t.Fatal(err)
	}
	if r.path != filepath.Join(first, "0c_tsf.dll") {
		t.Fatal("not rolled back")
	}
	if _, err = UninstallWindows(base, r); err != nil {
		t.Fatal(err)
	}
	if r.path != "" {
		t.Fatal("still registered")
	}
	if _, err = RollbackWindows(base, r, false); err != nil {
		t.Fatal(err)
	}
	if r.path != filepath.Join(first, "0c_tsf.dll") {
		t.Fatal("cannot undo uninstall")
	}
	b, _ := os.ReadFile(user)
	if string(b) != "private learning" {
		t.Fatal("changed user data")
	}
}
func TestWindowsRegistrationFailureRestoresOldVersion(t *testing.T) {
	base := t.TempDir()
	r := &fakeRegistrar{}
	first, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.1"), base, "amd64", r)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(base, "install-receipt.json"))
	r.failNew = true
	if _, _, err = InstallWindows(bundleFixture(t, "0.1.0-dev.2"), base, "amd64", r); err == nil {
		t.Fatal("expected failure")
	}
	if r.path != filepath.Join(first, "0c_tsf.dll") {
		t.Fatal("registration not restored")
	}
	after, _ := os.ReadFile(filepath.Join(base, "install-receipt.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("receipt changed on failure")
	}
}
func TestWindowsRejectsForeignRegistrationAndModifiedRollback(t *testing.T) {
	base := t.TempDir()
	r := &fakeRegistrar{path: filepath.Join(t.TempDir(), "0c_tsf.dll")}
	if _, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.1"), base, "amd64", r); err == nil {
		t.Fatal("overwrote foreign registration")
	}
	if len(r.calls) != 0 {
		t.Fatal("registration was touched")
	}
	r.path = ""
	first, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.1"), base, "amd64", r)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.2"), base, "amd64", r)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(first, "0c_tsf.dll"), []byte("modified"), 0600)
	if _, err = RollbackWindows(base, r, false); err == nil {
		t.Fatal("accepted modified rollback")
	}
	if r.path != filepath.Join(second, "0c_tsf.dll") {
		t.Fatal("modified registration before validation")
	}
}
func TestWindowsMigratesLegacyInstallation(t *testing.T) {
	base := t.TempDir()
	legacyStore := `{"Uni":{"人工":1}}`
	os.WriteFile(filepath.Join(base, "user.json"), []byte(legacyStore), 0600)
	for _, name := range windowsLegacy.names() {
		os.WriteFile(filepath.Join(base, name), []byte("old "+name), 0600)
	}
	r := &fakeRegistrar{path: filepath.Join(base, "zuin_tsf.dll")}
	if _, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.1"), base, "amd64", r); err != nil {
		t.Fatal(err)
	}
	if _, err := RollbackWindows(base, r, false); err != nil {
		t.Fatal(err)
	}
	if r.path != filepath.Join(base, "zuin_tsf.dll") {
		t.Fatal("legacy registration not restored")
	}
	// Once the fixed-location files are no longer the rollback target, they are removed.
	if _, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.1"), base, "amd64", r); err != nil {
		t.Fatal(err)
	}
	next, left, err := InstallWindows(bundleFixture(t, "0.1.0-dev.2"), base, "amd64", r)
	if err != nil || len(left) != 0 {
		t.Fatal(err, left)
	}
	for _, name := range windowsLegacy.names() {
		if _, err := os.Stat(filepath.Join(base, name)); !os.IsNotExist(err) {
			t.Fatal("legacy file kept:", name)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(base, "user.json")); string(b) != legacyStore || r.path != filepath.Join(next, "0c_tsf.dll") {
		t.Fatal("changed user data or registration")
	}
}

func TestWindowsLegacyRollbackRejectsUnsafePersonalFormat(t *testing.T) {
	for _, store := range []string{`{"Format":2,"Uni":{"測試":3},"Future":{"x":1}}`, `{"Format":3}`, `invalid`} {
		t.Run(store, func(t *testing.T) {
			base := t.TempDir()
			r := &fakeRegistrar{}
			if _, _, err := InstallWindows(layoutFixture(t, "0.1.0-dev.14", windowsLegacy), base, "amd64", r); err != nil {
				t.Fatal(err)
			}
			user := filepath.Join(base, UserStore)
			legacy := []byte(`{"Uni":{"人工":1}}`)
			if err := os.WriteFile(user, legacy, 0600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.42"), base, "amd64", r); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(user, []byte(store), 0600); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(filepath.Join(base, "install-receipt.json"))
			calls := len(r.calls)
			if _, err := RollbackWindows(base, r, false); err == nil {
				t.Fatal("unsafe rollback accepted")
			}
			after, _ := os.ReadFile(filepath.Join(base, "install-receipt.json"))
			got, _ := os.ReadFile(user)
			if string(before) != string(after) || len(r.calls) != calls || string(got) != store {
				t.Fatal("rejected rollback changed registration, receipt or store")
			}
			if _, err := RollbackWindows(base, r, true); err != nil {
				t.Fatal(err)
			}
			got, _ = os.ReadFile(user)
			if string(got) != string(legacy) {
				t.Fatal("compatible backup was not restored")
			}
		})
	}
}

func TestWindowsLegacyRollbackRejectsIncompatibleBackup(t *testing.T) {
	base := t.TempDir()
	r := &fakeRegistrar{}
	if _, _, err := InstallWindows(layoutFixture(t, "0.1.0-dev.14", windowsLegacy), base, "amd64", r); err != nil {
		t.Fatal(err)
	}
	user := filepath.Join(base, UserStore)
	store := []byte(`{"Format":2,"Uni":{"人工":1}}`)
	if err := os.WriteFile(user, store, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.42"), base, "amd64", r); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(base, "install-receipt.json"))
	calls := len(r.calls)
	if _, err := RollbackWindows(base, r, true); err == nil {
		t.Fatal("incompatible backup accepted")
	}
	after, _ := os.ReadFile(filepath.Join(base, "install-receipt.json"))
	got, _ := os.ReadFile(user)
	if string(before) != string(after) || len(r.calls) != calls || string(got) != string(store) {
		t.Fatal("rejected restore changed registration, receipt or store")
	}
}
func TestWindowsUpgradesFromZuinReleaseAndRollsBack(t *testing.T) {
	base := t.TempDir()
	r := &fakeRegistrar{}
	old, _, err := InstallWindows(layoutFixture(t, "0.1.0-dev.15", windowsLegacy), base, "amd64", r)
	if err != nil {
		t.Fatal(err)
	}
	if r.path != filepath.Join(old, "zuin_tsf.dll") {
		t.Fatal("zuin release not registered")
	}
	next, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.16"), base, "amd64", r)
	if err != nil {
		t.Fatal(err)
	}
	if r.path != filepath.Join(next, "0c_tsf.dll") || r.calls[len(r.calls)-2] != "unregister "+filepath.Join(old, "zuin_tsf.dll") {
		t.Fatal("old registration not replaced:", r.calls)
	}
	if _, err = os.Stat(filepath.Join(old, "zuin-server.exe")); err != nil {
		t.Fatal("removed rollback target")
	}
	if _, err = RollbackWindows(base, r, false); err != nil {
		t.Fatal(err)
	}
	if r.path != filepath.Join(old, "zuin_tsf.dll") {
		t.Fatal("not rolled back to zuin release")
	}
	if _, err = RollbackWindows(base, r, false); err != nil {
		t.Fatal(err)
	}
	if r.path != filepath.Join(next, "0c_tsf.dll") {
		t.Fatal("cannot return to 0c release")
	}
	latest, left, err := InstallWindows(bundleFixture(t, "0.1.0-dev.17"), base, "amd64", r)
	if err != nil || len(left) != 0 {
		t.Fatal(err, left)
	}
	if _, err = os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("old zuin version kept after it stopped being the rollback target")
	}
	if _, err = os.Stat(filepath.Join(next, "0c_tsf.dll")); err != nil || r.path != filepath.Join(latest, "0c_tsf.dll") {
		t.Fatal("removed 0c rollback target or wrong registration")
	}
}
func TestWindowsRollbackCanRestoreUserStoreAndUndoIt(t *testing.T) {
	base := t.TempDir()
	user := filepath.Join(base, UserStore)
	read := func() string { b, _ := os.ReadFile(user); return string(b) }
	r := &fakeRegistrar{}
	first, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.1"), base, "amd64", r)
	if err != nil {
		t.Fatal(err)
	}
	// 升級前沒有個人詞庫時，不能宣稱可還原；拒絕發生在變更註冊前。
	next, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.2"), base, "amd64", r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = RollbackWindows(base, r, true); err == nil || r.path != filepath.Join(next, "0c_tsf.dll") {
		t.Fatal("restored without a saved dictionary, or changed registration", err)
	}
	if _, err = RollbackWindows(base, r, false); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(user, []byte("before upgrade"), 0600)
	if _, err = RollbackWindows(base, r, false); err != nil { // back to next; saves "before upgrade"
		t.Fatal(err)
	}
	os.WriteFile(user, []byte("written by newer version"), 0600)
	if _, err = RollbackWindows(base, r, true); err != nil {
		t.Fatal(err)
	}
	if r.path != filepath.Join(first, "0c_tsf.dll") || read() != "before upgrade" {
		t.Fatal("dictionary not restored:", read())
	}
	// 還原本身可以撤回：再回復一次並還原，拿回回復前的內容。
	if _, err = RollbackWindows(base, r, true); err != nil {
		t.Fatal(err)
	}
	if r.path != filepath.Join(next, "0c_tsf.dll") || read() != "written by newer version" {
		t.Fatal("restore could not be undone:", read())
	}
	if _, err = RollbackWindows(base, r, false); err != nil {
		t.Fatal(err)
	}
	if read() != "written by newer version" {
		t.Fatal("rollback without -restore-user changed the dictionary")
	}
}
func TestLinuxBundleNamesByGeneration(t *testing.T) {
	for _, tc := range []struct {
		names  []string
		ok     bool
		legacy bool
	}{
		{[]string{"bin/0c-server", "lib/lib0c.so"}, true, false},
		{[]string{"bin/zuin-server", "lib/libzuin.so"}, true, true},
		{[]string{"bin/0c-server", "lib/libzuin.so"}, false, false},
	} {
		root := t.TempDir()
		r := Release{Schema: 1, Product: "0c", Version: "0.1.0", Channel: "internal-preview", OS: "linux", Arch: "amd64", Files: map[string]string{}}
		for _, name := range append(tc.names, "install.py", "data/manifest.json", "data/lexicon.bin", "data/lm.bin", "data/NOTICE.txt", "data/report.txt") {
			p := filepath.Join(root, filepath.FromSlash(name))
			os.MkdirAll(filepath.Dir(p), 0700)
			os.WriteFile(p, []byte(name), 0600)
			r.Files[name], _ = Hash(p)
		}
		writeJSON(filepath.Join(root, "release.json"), r)
		got, err := Verify(root, "linux", "amd64")
		if (err == nil) != tc.ok {
			t.Fatalf("%v: %v", tc.names, err)
		}
		if _, legacy := r.Files[LinuxLegacyServer]; err == nil && legacy != tc.legacy || got != nil && got.Version != "0.1.0" {
			t.Fatal("wrong generation")
		}
	}
}
func TestWindowsLayoutRejectsMixedNames(t *testing.T) {
	files := map[string]string{"0c_tsf.dll": "", "zuin-server.exe": "", "zuin-ui.exe": "", "0c-ui.exe": ""}
	if _, err := WindowsLayoutOf(files); err == nil {
		t.Fatal("accepted mixed program names")
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(body []byte) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body))}
}
func TestFeedAndDownloadRejectBadMetadataAndContent(t *testing.T) {
	payload := []byte("archive fixture")
	file := filepath.Join(t.TempDir(), "asset")
	os.WriteFile(file, payload, 0600)
	hash, _ := Hash(file)
	asset := Asset{OS: "windows", Arch: "amd64", URL: "https://example.org/release.zip", SHA256: hash, Size: int64(len(payload))}
	feed := Feed{Schema: 1, Product: "0c", Version: "0.1.0-beta.1", Channel: "public-beta", Assets: []Asset{asset}}
	encoded, _ := json.Marshal(feed)
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, ".json") {
			return response(encoded), nil
		}
		return response(payload), nil
	})}
	if _, err := ReadFeed(context.Background(), client, "http://example.org/feed.json", "public-beta"); err == nil {
		t.Fatal("accepted HTTP")
	}
	if _, err := ReadFeed(context.Background(), client, "https://example.org/feed.json", "internal-preview"); err == nil {
		t.Fatal("accepted wrong channel")
	}
	f, err := ReadFeed(context.Background(), client, "https://example.org/feed.json", "public-beta")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Select("linux", "amd64"); err == nil {
		t.Fatal("accepted missing platform")
	}
	path, err := Download(context.Background(), client, asset, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sum, _ := Hash(path)
	if sum != hash {
		t.Fatal("wrong download")
	}
	asset.SHA256 = strings.Repeat("0", 64)
	if _, err = Download(context.Background(), client, asset, t.TempDir()); err == nil {
		t.Fatal("accepted wrong hash")
	}
	asset.SHA256 = hash
	asset.Size--
	if _, err = Download(context.Background(), client, asset, t.TempDir()); err == nil {
		t.Fatal("accepted truncated asset")
	}
}
func TestSemanticVersionPrecedence(t *testing.T) {
	versions := []string{"0.1.0-dev.2", "0.1.0-dev.10", "0.1.0", "0.2.0-beta.1", "0.2.0", "1.0.0"}
	for i := 0; i < len(versions)-1; i++ {
		n, err := CompareVersion(versions[i], versions[i+1])
		if err != nil || n >= 0 {
			t.Fatalf("bad order %s %s", versions[i], versions[i+1])
		}
	}
	// 0.2.0 是第一個沒有預發布後綴的版本：之後的開發版必須是 0.2.1-dev.N，0.2.0-xxx 都比 0.2.0 舊。
	versions = []string{"0.1.0-dev.64", "0.2.0-dev.4", "0.2.0-dev.4.1", "0.2.0", "0.2.1-dev.1", "0.2.1-dev.1.2", "0.2.1"}
	for i := 0; i < len(versions)-1; i++ {
		n, err := CompareVersion(versions[i], versions[i+1])
		if err != nil || n >= 0 {
			t.Fatalf("bad order %s %s", versions[i], versions[i+1])
		}
	}
	if ValidVersion("0.2.0.1") {
		t.Fatal("four-part version accepted")
	}
	for _, v := range []string{"v1.0.0", "1.0", "1.0.0-dev.01", "../1.0.0"} {
		if ValidVersion(v) {
			t.Fatalf("invalid version accepted: %s", v)
		}
	}
}

// 核心更新後 engine.json 指向較新的引擎；完整安裝移除指標，回復時依收據寫回。
func TestWindowsFullInstallAndRollbackManageEnginePointer(t *testing.T) {
	base := t.TempDir()
	r := &fakeRegistrar{}
	first, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.1"), base, "amd64", r)
	if err != nil {
		t.Fatal(err)
	}
	// 模擬協調器完成的核心更新：收據記錄指標，engine.json 指向同一版本目錄。
	receipt, _ := readReceipt(filepath.Join(base, "install-receipt.json"))
	receipt.Engine = receipt.Directory
	if err := writeJSON(filepath.Join(base, "install-receipt.json"), receipt); err != nil {
		t.Fatal(err)
	}
	if err := applyEnginePointer(base, receipt); err != nil {
		t.Fatal(err)
	}
	if c, _ := enginedir.Read(base); c == nil || c.Directory != receipt.Directory {
		t.Fatal("pointer not written")
	}
	if _, _, err = InstallWindows(bundleFixture(t, "0.1.0-dev.2"), base, "amd64", r); err != nil {
		t.Fatal(err)
	}
	if c, _ := enginedir.Read(base); c != nil {
		t.Fatal("full install must remove the engine pointer")
	}
	if _, err = RollbackWindows(base, r, false); err != nil {
		t.Fatal(err)
	}
	if c, _ := enginedir.Read(base); c == nil || filepath.Join(base, filepath.FromSlash(c.Directory)) != first {
		t.Fatal("rollback must restore the engine pointer of the previous state")
	}
}

// 先前中斷的安裝或清理留下不完整的版本目錄（例如內附 Python 仍在執行、neural 沒刪乾淨）：
// 同版本再次安裝時要移開重裝，而不是因核對失敗而無法安裝。
func TestWindowsReinstallsOverIncompleteVersionDirectory(t *testing.T) {
	base := t.TempDir()
	bundle := bundleFixture(t, "0.1.0-dev.1")
	dir, _, err := InstallWindows(bundle, base, "amd64", &fakeRegistrar{})
	if err != nil {
		t.Fatal(err)
	}
	// 模擬被清理到一半：收據與註冊回到安裝前，版本目錄少了檔案。
	os.Remove(filepath.Join(base, "install-receipt.json"))
	os.Remove(filepath.Join(dir, "data", "lm.bin"))
	again, _, err := InstallWindows(bundle, base, "amd64", &fakeRegistrar{})
	if err != nil {
		t.Fatalf("must reinstall over an incomplete directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(again, "data", "lm.bin")); err != nil {
		t.Fatal("reinstalled directory must be complete")
	}
}

// 伺服器在傳送途中斷線時以 Range 接續；不支援 Range 時從頭重下；沒有進度的失敗有上限；4xx 不重試。
func TestDownloadResumesAfterDroppedConnection(t *testing.T) {
	downloadRetryDelay = time.Millisecond
	defer func() { downloadRetryDelay = time.Second }()
	payload := bytes.Repeat([]byte("0123456789abcdef"), 4096)
	file := filepath.Join(t.TempDir(), "asset")
	os.WriteFile(file, payload, 0600)
	hash, _ := Hash(file)
	asset := Asset{OS: "linux", Arch: "amd64", URL: "https://example.org/a.tar.gz", SHA256: hash, Size: int64(len(payload))}
	requests := 0
	serve := func(ranges bool, cut int, drops *int, status int) *http.Client {
		return &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			requests++
			if status != 0 {
				return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(nil))}, nil
			}
			start, code, h := 0, 200, http.Header{"Etag": {`"v1"`}}
			if rg := r.Header.Get("Range"); ranges && rg != "" {
				if r.Header.Get("If-Range") != `"v1"` {
					t.Errorf("If-Range = %q", r.Header.Get("If-Range"))
				}
				fmt.Sscanf(rg, "bytes=%d-", &start)
				code = 206
				h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(payload)-1, len(payload)))
			}
			body := io.Reader(bytes.NewReader(payload[start:]))
			if *drops > 0 {
				*drops--
				body = io.MultiReader(io.LimitReader(body, int64(cut)), iotest.ErrReader(io.ErrUnexpectedEOF))
			}
			return &http.Response{StatusCode: code, Header: h, Body: io.NopCloser(body), ContentLength: int64(len(payload) - start)}, nil
		})}
	}
	for _, c := range []struct {
		name   string
		ranges bool
		cut    int
		drops  int
		status int
		ok     bool
	}{
		{"resume", true, 10000, 3, 0, true},
		{"no-range", false, 10000, 3, 0, true},
		{"many drops with progress", true, 5000, 12, 0, true},
		{"no progress", true, 0, 100, 0, false},
		{"not found", true, 0, 0, 404, false},
	} {
		drops := c.drops
		requests = 0
		path, err := Download(context.Background(), serve(c.ranges, c.cut, &drops, c.status), asset, t.TempDir())
		if c.ok != (err == nil) {
			t.Fatalf("%s: err=%v", c.name, err)
		}
		if c.ok {
			if sum, _ := Hash(path); sum != hash {
				t.Fatalf("%s: wrong content", c.name)
			}
		}
		if c.name == "no progress" && drops != 100-downloadAttempts {
			t.Fatalf("no progress: %d attempts", 100-drops)
		}
		if c.name == "not found" && requests != 1 {
			t.Fatal("retried 404")
		}
	}
}

// 解開的位置由呼叫端指定（Linux 為使用者資料目錄的暫存區），不放在壓縮包旁邊：使用者指定的壓縮包可能在隨身碟
// 或任何資料夾。暫存目錄是私有的；指定位置建不起來時退回系統暫存目錄，更新不因此失敗。
func TestStageInUsesTheGivenScratchNotTheArchiveFolder(t *testing.T) {
	usb := t.TempDir()
	file := zipFixture(t, filepath.Join(usb, "package.zip"))
	hash, _ := Hash(file)
	scratch := t.TempDir()
	staged, cleanup, err := StageIn(scratch, file, hash, "windows", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	temp := filepath.Dir(staged)
	if filepath.Dir(temp) != scratch || !filepath.IsAbs(staged) {
		t.Fatalf("staged %q is not under the scratch directory %q", staged, scratch)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(temp); err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("stage must be private: %v %v", info, err)
		}
	}
	if entries, _ := os.ReadDir(usb); len(entries) != 1 {
		t.Fatalf("nothing may be written next to the archive: %v", entries)
	}
	cleanup()
	if _, err := os.Stat(temp); !os.IsNotExist(err) {
		t.Fatal("cleanup must remove the stage")
	}

	staged, cleanup, err = StageIn(filepath.Join(scratch, "missing", "deeper"), file, hash, "windows", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if filepath.Dir(filepath.Dir(staged)) != filepath.Clean(os.TempDir()) {
		t.Fatalf("fallback %q is not under the system temp dir %q", staged, os.TempDir())
	}
}

func TestRemoveStaleOnlyRemovesOldPrefixedDirectories(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-48 * time.Hour)
	paths := map[string]bool{ // 路徑 → 應保留
		"0c-download-old": false, "0c-install-old": false, "0c-download-new": true, "keep-me": true,
	}
	for name := range paths {
		if err := os.Mkdir(filepath.Join(dir, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "0c-install-file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	paths["0c-install-file"] = true
	for _, name := range []string{"0c-download-old", "0c-install-old", "keep-me", "0c-install-file"} {
		if err := os.Chtimes(filepath.Join(dir, name), old, old); err != nil {
			t.Fatal(err)
		}
	}
	RemoveStale(dir, 24*time.Hour, "0c-download-", "0c-install-")
	for name, keep := range paths {
		if _, err := os.Stat(filepath.Join(dir, name)); (err == nil) != keep {
			t.Fatalf("%s kept=%v, want %v", name, err == nil, keep)
		}
	}
}

// #82：已安裝的檔案被改動或移除（例如防毒軟體隔離）時，解除安裝仍取消註冊，並回報核對失敗的原因。
func TestWindowsUninstallProceedsWhenInstalledFilesChanged(t *testing.T) {
	base := t.TempDir()
	r := &fakeRegistrar{}
	dir, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.1"), base, "amd64", r)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "0c-server.exe"), []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	changed, err := UninstallWindows(base, r)
	if err != nil {
		t.Fatalf("changed files must not block uninstall: %v", err)
	}
	if changed == nil || !strings.Contains(changed.Error(), "0c-server.exe") {
		t.Fatalf("the changed file must be reported: %v", changed)
	}
	if r.path != "" {
		t.Fatal("still registered")
	}
	receipt, err := readReceipt(filepath.Join(base, "install-receipt.json"))
	if err != nil || receipt.Active {
		t.Fatalf("receipt must be inactive: %+v %v", receipt, err)
	}
}

// #82：註冊與收據不一致。指向這個帳號自己的另一個版本（或沒有註冊）時以實際註冊為準照常安裝與移除；
// 指向別處（其他 Windows 帳號的安裝）時，移除只停用自己的收據、不動那份註冊，安裝則說明註冊在哪裡。
func TestWindowsRegistrationMismatchWithOwnVersion(t *testing.T) {
	base := t.TempDir()
	r := &fakeRegistrar{}
	first, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.1"), base, "amd64", r)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = InstallWindows(bundleFixture(t, "0.1.0-dev.2"), base, "amd64", r); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(first, "0c_tsf.dll")
	r.path = stale // 收據是 dev.2，註冊卻留在 dev.1
	third, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.3"), base, "amd64", r)
	if err != nil {
		t.Fatalf("own stale registration must not block install: %v", err)
	}
	if r.path != filepath.Join(third, "0c_tsf.dll") || !slices.Contains(r.calls, "unregister "+stale) {
		t.Fatalf("must switch from the actual registration: %v", r.calls)
	}
	r.path = stale
	r.calls = nil
	if _, err = UninstallWindows(base, r); err != nil {
		t.Fatalf("own stale registration must not block uninstall: %v", err)
	}
	if r.path != "" || !slices.Equal(r.calls, []string{"unregister " + stale}) {
		t.Fatalf("must unregister what is actually registered: %v", r.calls)
	}
}

func TestWindowsRegistrationMissingOrForeign(t *testing.T) {
	base := t.TempDir()
	r := &fakeRegistrar{}
	if _, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.1"), base, "amd64", r); err != nil {
		t.Fatal(err)
	}
	r.path, r.calls = "", nil // 註冊不見了
	if _, err := UninstallWindows(base, r); err != nil || len(r.calls) != 0 {
		t.Fatalf("missing registration: uninstall without unregistering: %v %v", err, r.calls)
	}
	if _, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.2"), base, "amd64", r); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(t.TempDir(), "other-user", "0c_tsf.dll")
	r.path, r.calls = foreign, nil
	if _, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.3"), base, "amd64", r); err == nil || !strings.Contains(err.Error(), foreign) {
		t.Fatalf("install over another installation must refuse and name it: %v", err)
	}
	changed, err := UninstallWindows(base, r)
	if err != nil || changed == nil || !strings.Contains(changed.Error(), foreign) {
		t.Fatalf("uninstall must deactivate own receipt and report the foreign registration: %v %v", changed, err)
	}
	if r.path != foreign || len(r.calls) != 0 {
		t.Fatalf("foreign registration must stay untouched: %v %v", r.path, r.calls)
	}
	receipt, err := readReceipt(filepath.Join(base, "install-receipt.json"))
	if err != nil || receipt.Active {
		t.Fatalf("own receipt must be inactive: %+v %v", receipt, err)
	}
}

// #127：自訂詞彙跟 user.json 一起備份。預設回復保留目前的詞；-restore-user 一起還原，
// 回復前的詞另存在新備份裡；升級前還沒有自訂詞彙時，還原不刪掉後來加的詞。
func TestWindowsVocabStoreFollowsUserStore(t *testing.T) {
	base := t.TempDir()
	r := &fakeRegistrar{}
	user, vocab := filepath.Join(base, UserStore), filepath.Join(base, VocabStore)
	write := func(path, s string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
	}
	read := func(path string) string { b, _ := os.ReadFile(path); return string(b) }
	write(user, `{"Format":5}`)
	if _, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.70"), base, "amd64", r); err != nil {
		t.Fatal(err)
	}
	write(vocab, "words before 71")
	if _, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.71"), base, "amd64", r); err != nil {
		t.Fatal(err)
	}
	write(vocab, "words added on 71")
	if _, err := RollbackWindows(base, r, false); err != nil {
		t.Fatal(err)
	}
	if read(vocab) != "words added on 71" {
		t.Fatal("default rollback must keep the custom words")
	}
	if _, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.72"), base, "amd64", r); err != nil {
		t.Fatal(err)
	}
	write(vocab, "words added on 72")
	if _, err := RollbackWindows(base, r, true); err != nil {
		t.Fatal(err)
	}
	if read(vocab) != "words added on 71" {
		t.Fatalf("restore-user restores the saved vocabulary: %q", read(vocab))
	}
	current, err := installed(base, r)
	if err != nil || read(filepath.Join(base, filepath.FromSlash(current.Previous), VocabStore)) != "words added on 72" {
		t.Fatal("the replaced vocabulary must be kept in the new backup", err)
	}
}

// 備份裡沒有 vocab.json 時，還原個人詞庫不動目前的自訂詞彙。
func TestWindowsRestoreWithoutVocabKeepsCurrent(t *testing.T) {
	base := t.TempDir()
	r := &fakeRegistrar{}
	os.WriteFile(filepath.Join(base, UserStore), []byte(`{"Format":5}`), 0600)
	if _, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.70"), base, "amd64", r); err != nil {
		t.Fatal(err)
	}
	if _, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.71"), base, "amd64", r); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(base, VocabStore), []byte("new words"), 0600)
	if _, err := RollbackWindows(base, r, true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(base, VocabStore)); string(b) != "new words" {
		t.Fatalf("%q", b)
	}
}
