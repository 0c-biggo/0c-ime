package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type member struct{ name, body string }

func sum(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// packageArchive 建立含 release.json 的套件（只放測試需要的檔案）；files 是 release.json 列出的檔案與內容。
func packageArchive(t *testing.T, name, goos, version string, files map[string]string, extra ...member) (string, string) {
	t.Helper()
	listed := map[string]string{}
	for n, b := range files {
		listed[n] = sum(b)
	}
	release, _ := json.Marshal(Release{Schema: 1, Product: "0c", Version: version, Channel: "public-beta", OS: goos, Arch: "amd64", Files: listed})
	members := []member{{"data/lexicon.bin", "big dictionary"}, {"release.json", string(release)}}
	for n, b := range files {
		members = append(members, member{n, b})
	}
	members = append(members, extra...)
	path := filepath.Join(t.TempDir(), name)
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	root := "0c-" + version + "-" + goos + "-amd64/"
	if strings.HasSuffix(name, ".zip") {
		w := zip.NewWriter(out)
		w.Create(root)
		for _, m := range members {
			f, _ := w.Create(root + m.name)
			f.Write([]byte(m.body))
		}
		w.Close()
	} else {
		gz := gzip.NewWriter(out)
		tw := tar.NewWriter(gz)
		tw.WriteHeader(&tar.Header{Name: root, Typeflag: tar.TypeDir, Mode: 0o755})
		for _, m := range members {
			tw.WriteHeader(&tar.Header{Name: root + m.name, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(m.body))})
			tw.Write([]byte(m.body))
		}
		tw.Close()
		gz.Close()
	}
	out.Close()
	h, err := Hash(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, h
}

// 只換更新器（#295）：從完整套件只取出更新器與啟動器，依 release.json 逐一核對，其他檔案不解開。
func TestExtractUpdaterTakesOnlyVerifiedUpdaterFiles(t *testing.T) {
	win, winSum := packageArchive(t, "w.zip", "windows", "0.2.16", map[string]string{"0c-update.exe": "updater 16", "0c-updatew.exe": "launcher", "0c-server.exe": "engine"})
	dest := filepath.Join(t.TempDir(), "stage")
	r, files, err := ExtractUpdater(win, winSum, "windows", "amd64", "0.2.16", dest)
	if err != nil || r.Version != "0.2.16" || len(files) != 2 {
		t.Fatalf("%v %v", err, files)
	}
	if b, _ := os.ReadFile(files[0]); string(b) != "updater 16" {
		t.Fatal(string(b))
	}
	entries, _ := os.ReadDir(dest)
	if len(entries) != 3 { // release.json、0c-update.exe、0c-updatew.exe
		t.Fatalf("extracted %d entries", len(entries))
	}
	linux, linuxSum := packageArchive(t, "l.tar.gz", "linux", "0.2.16", map[string]string{"bin/0c-update": "linux updater", "bin/0c-server": "engine"})
	if _, files, err := ExtractUpdater(linux, linuxSum, "linux", "amd64", "0.2.16", filepath.Join(t.TempDir(), "s")); err != nil || len(files) != 1 {
		t.Fatalf("linux: %v %v", err, files)
	}
	// 舊套件沒有啟動器：只換更新器。
	old, oldSum := packageArchive(t, "o.zip", "windows", "0.2.16", map[string]string{"0c-update.exe": "updater"})
	if _, files, err := ExtractUpdater(old, oldSum, "windows", "amd64", "0.2.16", filepath.Join(t.TempDir(), "s")); err != nil || len(files) != 1 {
		t.Fatalf("no launcher: %v %v", err, files)
	}
}

func TestExtractUpdaterRejectsMismatches(t *testing.T) {
	good, goodSum := packageArchive(t, "w.zip", "windows", "0.2.16", map[string]string{"0c-update.exe": "updater"})
	cases := map[string]func() error{
		"archive hash": func() error {
			_, _, err := ExtractUpdater(good, strings.Repeat("0", 64), "windows", "amd64", "0.2.16", filepath.Join(t.TempDir(), "s"))
			return err
		},
		"version": func() error {
			_, _, err := ExtractUpdater(good, goodSum, "windows", "amd64", "0.2.17", filepath.Join(t.TempDir(), "s"))
			return err
		},
		"platform": func() error {
			_, _, err := ExtractUpdater(good, goodSum, "linux", "amd64", "0.2.16", filepath.Join(t.TempDir(), "s"))
			return err
		},
		"macOS": func() error {
			_, _, err := ExtractUpdater(good, goodSum, "darwin", "amd64", "0.2.16", filepath.Join(t.TempDir(), "s"))
			return err
		},
		"tampered member": func() error {
			// release.json 列的是 "updater" 的雜湊，套件裡的檔案卻是別的內容
			path := filepath.Join(t.TempDir(), "t.zip")
			release, _ := json.Marshal(Release{Schema: 1, Product: "0c", Version: "0.2.16", Channel: "public-beta", OS: "windows", Arch: "amd64", Files: map[string]string{"0c-update.exe": sum("updater")}})
			f, _ := os.Create(path)
			w := zip.NewWriter(f)
			for _, m := range []member{{"r/release.json", string(release)}, {"r/0c-update.exe", "evil"}} {
				x, _ := w.Create(m.name)
				x.Write([]byte(m.body))
			}
			w.Close()
			f.Close()
			h, _ := Hash(path)
			_, _, err := ExtractUpdater(path, h, "windows", "amd64", "0.2.16", filepath.Join(t.TempDir(), "s"))
			return err
		},
		"unlisted updater": func() error {
			path, h := packageArchive(t, "u.zip", "windows", "0.2.16", map[string]string{"0c-server.exe": "x"}, member{"0c-update.exe", "evil"})
			_, _, err := ExtractUpdater(path, h, "windows", "amd64", "0.2.16", filepath.Join(t.TempDir(), "s"))
			return err
		},
		"two roots": func() error {
			path, h := packageArchive(t, "r.tar.gz", "linux", "0.2.16", map[string]string{"bin/0c-update": "u"})
			// 另一個根目錄的同名檔
			f, _ := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
			gz := gzip.NewWriter(f)
			tw := tar.NewWriter(gz)
			for _, n := range []string{"a/release.json", "b/bin/0c-update"} {
				tw.WriteHeader(&tar.Header{Name: n, Typeflag: tar.TypeReg, Mode: 0o644, Size: 1})
				tw.Write([]byte("x"))
			}
			tw.Close()
			gz.Close()
			f.Close()
			h, _ = Hash(path)
			_, _, err := ExtractUpdater(path, h, "linux", "amd64", "0.2.16", filepath.Join(t.TempDir(), "s"))
			return err
		},
	}
	for name, run := range cases {
		if run() == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
