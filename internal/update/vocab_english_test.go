package update

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/0c-biggo/0c-ime/core/vocab"
)

// #146／#127：英文詞存在 vocab.json 的 words 裡，備份與還原整個檔案，英文詞跟著走。
func TestWindowsBackupKeepsEnglishVocab(t *testing.T) {
	base := t.TempDir()
	r := &fakeRegistrar{}
	path := filepath.Join(base, VocabStore)
	save := func(entries ...vocab.Entry) {
		t.Helper()
		s := vocab.New()
		if _, err := s.Add(entries, vocab.SourceManual, 1); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := s.Save(f); err != nil {
			t.Fatal(err)
		}
	}
	load := func() *vocab.Set {
		t.Helper()
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		s, err := vocab.Load(f)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	os.WriteFile(filepath.Join(base, UserStore), []byte(`{"Format":5}`), 0600)
	if _, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.70"), base, "amd64", r); err != nil {
		t.Fatal(err)
	}
	save(vocab.Entry{Text: "ls"}, vocab.Entry{Text: "kubectl"}, vocab.Entry{Text: "世傑", Readings: []string{"ㄕˋ ㄐㄧㄝˊ"}})
	if _, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.71"), base, "amd64", r); err != nil {
		t.Fatal(err)
	}
	save(vocab.Entry{Text: "rm"})
	if _, err := RollbackWindows(base, r, true); err != nil {
		t.Fatal(err)
	}
	if s := load(); !s.HasEnglish("ls") || !s.HasEnglish("kubectl") || s.HasEnglish("rm") || s.Len() != 3 {
		t.Fatalf("還原後英文詞跟著回來：%+v", s.Words())
	}
}
