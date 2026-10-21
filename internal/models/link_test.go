package models

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// assertUsable：配對目錄與權重都是一般檔案（不是連結），內容與清單相符。
func assertUsable(t *testing.T, s Store, l Lock, dictionary string) {
	t.Helper()
	for _, role := range Roles {
		m := l.Models[role]
		for _, n := range m.FileNames() {
			if err := checkFile(filepath.Join(s.WeightsDir(role, m.Revision), n), m.Files[n]); err != nil {
				t.Fatalf("權重 %s/%s：%v", role, n, err)
			}
			if p := filepath.Join(s.PairDir(dictionary), role, n); n != "manifest.json" && n != "vocab.json" {
				if err := checkFile(p, m.Files[n]); err != nil {
					t.Fatalf("配對的 %s/%s：%v", role, n, err)
				}
			}
			for _, path := range []string{filepath.Join(s.WeightsDir(role, m.Revision), n), filepath.Join(s.PairDir(dictionary), role, n)} {
				st, err := os.Lstat(path)
				if err != nil {
					t.Fatalf("%s：%v", path, err)
				}
				if !st.Mode().IsRegular() {
					t.Fatalf("%s 不是一般檔案（%v）", path, st.Mode())
				}
			}
		}
	}
	if s.Inspect(l, dictionary).Need() != "none" {
		t.Fatal("應可使用")
	}
}

// 檔案系統不支援硬連結（例如某些網路磁碟、FUSE）：下載與之後的重新配對都要留下可以用的檔案。以前改用指向暫存目錄
// 的絕對路徑符號連結，放上去之後全部失效；重新配對時權重的連結甚至指向自己，真正的檔案隨暫存目錄刪掉。
func TestInstallWithoutHardLinks(t *testing.T) {
	old := hardLink
	hardLink = func(string, string) error { return errors.New("operation not permitted") }
	defer func() { hardLink = old }()

	f := newFixture(t)
	s := Store{Root: filepath.Join(t.TempDir(), "models")}
	if err := s.Install(context.Background(), f.lock, f.pairing(t, dictA), dictA, f.fetcher, nil); err != nil {
		t.Fatal(err)
	}
	assertUsable(t, s, f.lock, dictA)
	hits := f.hub.hits.Load()
	if err := s.Install(context.Background(), f.lock, f.pairing(t, dictB), dictB, nil, nil); err != nil {
		t.Fatal(err)
	}
	if f.hub.hits.Load() != hits {
		t.Fatal("重新配對不應連網")
	}
	assertUsable(t, s, f.lock, dictB)
	// 再配對一次（例如字典換回來）：沿用的權重仍然完整。
	if err := s.Install(context.Background(), f.lock, f.pairing(t, dictA), dictA, nil, nil); err != nil {
		t.Fatal(err)
	}
	assertUsable(t, s, f.lock, dictA)
}
