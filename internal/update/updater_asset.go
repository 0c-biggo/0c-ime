package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// UpdaterFiles 是套件裡「只換更新器」（#295 的 updater 種類）會取出的檔案（套件根目錄下的相對路徑）。第一個是更新器
// 本身，必須有；其餘有列在 release.json 才取。macOS 的更新器在已簽章的 App 裡，不能單獨換，回傳 nil。
func UpdaterFiles(goos string) []string {
	switch goos {
	case "windows":
		return []string{"0c-update.exe", "0c-updatew.exe"}
	case "linux":
		return []string{"bin/0c-update"}
	}
	return nil
}

// maxUpdaterFile 是單一更新器檔案的上限；正常約 15 MB。
const maxUpdaterFile = 256 << 20

// ExtractUpdater 從已下載的完整套件（core 或 shell，SHA-256 須為 expected）只取出更新器（UpdaterFiles）到 dest，
// 以套件內 release.json 的雜湊逐一核對，並確認版本、平台與通道。不解開其他檔案（完整套件解開要 1 GB 以上）。
// 回傳 release.json 與 dest 底下的檔案路徑（順序同 UpdaterFiles，沒有的略過）。
func ExtractUpdater(archive, expected, goos, arch, version, dest string) (*Release, []string, error) {
	want := UpdaterFiles(goos)
	if len(want) == 0 {
		return nil, nil, fmt.Errorf("%s 的更新器不能單獨更換", goos)
	}
	if !hashPattern.MatchString(expected) {
		return nil, nil, fmt.Errorf("a lowercase SHA-256 is required")
	}
	if st, err := os.Stat(archive); err != nil {
		return nil, nil, err
	} else if st.Size() > MaxArchive {
		return nil, nil, fmt.Errorf("archive too large")
	}
	if actual, err := Hash(archive); err != nil {
		return nil, nil, err
	} else if actual != expected {
		return nil, nil, fmt.Errorf("archive SHA-256 mismatch")
	}
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return nil, nil, err
	}
	wanted := map[string]bool{"release.json": true}
	for _, n := range want {
		wanted[n] = true
	}
	root := ""
	got := map[string]string{}
	take := func(name string, size int64, content io.Reader) error {
		name = strings.TrimSuffix(name, "/")
		top, rest, _ := strings.Cut(name, "/")
		if root == "" {
			root = top
		}
		if top != root || !SafeName(name) {
			return fmt.Errorf("archive must contain one root directory")
		}
		if !wanted[rest] {
			return nil
		}
		if got[rest] != "" {
			return fmt.Errorf("duplicate archive entry: %s", name)
		}
		limit := int64(maxUpdaterFile)
		if rest == "release.json" {
			limit = 1 << 20
		}
		if size < 0 || size > limit {
			return fmt.Errorf("archive member too large: %s", name)
		}
		perm := os.FileMode(0o600)
		if rest != "release.json" {
			perm = 0o700
		}
		path := filepath.Join(dest, filepath.FromSlash(rest))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
		if err != nil {
			return err
		}
		_, err = io.CopyN(f, content, size)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
		got[rest] = path
		return nil
	}
	if err := walkArchive(archive, take); err != nil {
		return nil, nil, err
	}
	if got["release.json"] == "" {
		return nil, nil, fmt.Errorf("missing bundle file: release.json")
	}
	b, err := os.ReadFile(got["release.json"])
	if err != nil {
		return nil, nil, err
	}
	var r Release
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, nil, err
	}
	if r.Schema != 1 || r.Product != "0c" || r.Version != version || r.OS != goos || r.Arch != arch ||
		(r.Channel != "public-beta" && r.Channel != "internal-preview") {
		return nil, nil, fmt.Errorf("套件 %s %s/%s 與索引 %s %s/%s 不符", r.Version, r.OS, r.Arch, version, goos, arch)
	}
	var files []string
	for i, n := range want {
		sum, listed := r.Files[n]
		path := got[n]
		switch {
		case !listed && i == 0:
			return nil, nil, fmt.Errorf("missing bundle file: %s", n)
		case !listed:
			if path != "" {
				os.Remove(path) // 沒列在清單裡的不用
			}
			continue
		case path == "":
			return nil, nil, fmt.Errorf("missing bundle file: %s", n)
		}
		actual, err := Hash(path)
		if err != nil {
			return nil, nil, err
		}
		if actual != sum {
			return nil, nil, fmt.Errorf("hash mismatch: %s", n)
		}
		files = append(files, path)
	}
	return &r, files, nil
}

// walkArchive 逐一走過 zip 或 tar.gz 的一般檔案；連結與裝置檔一律拒絕。
func walkArchive(archive string, each func(name string, size int64, content io.Reader) error) error {
	if strings.HasSuffix(strings.ToLower(archive), ".zip") {
		z, err := zip.OpenReader(archive)
		if err != nil {
			return err
		}
		defer z.Close()
		for _, f := range z.File {
			if f.FileInfo().IsDir() {
				continue
			}
			if !f.Mode().IsRegular() {
				return fmt.Errorf("archive links/devices are not allowed")
			}
			if f.UncompressedSize64 > uint64(MaxArchive) {
				return fmt.Errorf("archive member too large")
			}
			rc, err := f.Open()
			if err != nil {
				return err
			}
			err = each(f.Name, int64(f.UncompressedSize64), rc)
			rc.Close()
			if err != nil {
				return err
			}
		}
		return nil
	}
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg:
			if err := each(h.Name, h.Size, tr); err != nil {
				return err
			}
		default:
			return fmt.Errorf("archive links/devices are not allowed")
		}
	}
}
