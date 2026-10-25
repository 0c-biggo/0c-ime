//go:build linux

package settings

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func linuxPaths(t *testing.T) (string, string) {
	t.Helper()
	share := filepath.Join(t.TempDir(), ".local", "share")
	return filepath.Join(share, "0c", FileName), filepath.Join(share, "zuin", FileName)
}

func putSettings(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLinuxSettingsFallbackThenSaveToNewPath(t *testing.T) {
	path, old := linuxPaths(t)
	user := filepath.Join(filepath.Dir(old), "user.json")
	if got := Path(user); got != path {
		t.Fatalf("write path = %s, want %s", got, path)
	}
	body := `{"schema":1,"selection":"statistical","punctuation":true,"future":{"x":1},"candidate_layout":"future-layout"}`
	putSettings(t, old, body)
	r, err := Read(path)
	if err != nil || r.State != StateOK || r.Path != old || r.Settings.Selection != Statistical || !r.Settings.Punctuation {
		t.Fatalf("legacy read: %+v %v", r, err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("read must not create new directory: %v", err)
	}
	if err := r.Settings.Set("document-context", "off"); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, r.Settings); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(old); err != nil || string(got) != body {
		t.Fatalf("old file changed: %s %v", got, err)
	}
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), `"future-layout"`) || !strings.Contains(string(b), `"x": 1`) {
		t.Fatalf("unknown values must survive: %s %v", b, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("settings permissions: %v %v", info, err)
	}
	info, err = os.Stat(filepath.Dir(path))
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("directory permissions: %v %v", info, err)
	}
	// 舊檔之後再變更，也不應搶回新設定的優先權。
	putSettings(t, old, `{"selection":"neural","document_context":true,"punctuation":false}`)
	r, err = Read(path)
	if err != nil || r.Path != path || r.Settings.Selection != Statistical || r.Settings.DocumentContext || !r.Settings.Punctuation {
		t.Fatalf("new file must take precedence: %+v %v", r, err)
	}
}

func TestLinuxSettingsFreshInstallAndCustomPaths(t *testing.T) {
	path, _ := linuxPaths(t)
	r, err := Read(path)
	if err != nil || r.Path != path || r.State != StateMissing || r.Settings != freshSettings() {
		t.Fatalf("fresh read: %+v %v", r, err)
	}
	if err := Save(path, r.Settings); err != nil {
		t.Fatal(err)
	}
	if r, err := Read(path); err != nil || r.Path != path || r.State != StateOK {
		t.Fatalf("fresh save: %+v %v", r, err)
	}
	for _, dir := range []string{t.TempDir(), filepath.Join(t.TempDir(), "zuin"), filepath.Join(t.TempDir(), "share", "zuin")} {
		want := filepath.Join(dir, FileName)
		if got := Path(filepath.Join(dir, "user.json")); got != want {
			t.Errorf("custom path changed: %s, want %s", got, want)
		}
	}
	// 即使是標準目錄，自訂檔名也不套用相容讀取。
	custom := filepath.Join(filepath.Dir(path), "custom.json")
	putSettings(t, filepath.Join(filepath.Dir(filepath.Dir(path)), "zuin", "custom.json"), `{}`)
	if r, err := Read(custom); err != nil || r.State != StateMissing || r.Path != custom {
		t.Fatalf("custom filename must not fall back: %+v %v", r, err)
	}
}

func TestLinuxSettingsExistingNewFileNeverFallsBack(t *testing.T) {
	for _, kind := range []string{"corrupt", "unreadable", "newer", "valid"} {
		t.Run(kind, func(t *testing.T) {
			path, old := linuxPaths(t)
			putSettings(t, old, `{"selection":"statistical","punctuation":true}`)
			state := StateOK
			switch kind {
			case "unreadable":
				if err := os.MkdirAll(path, 0700); err != nil {
					t.Fatal(err)
				}
				state = StateUnreadable
			case "corrupt":
				putSettings(t, path, "{broken")
				state = StateCorrupt
			case "newer":
				putSettings(t, path, `{"schema":9,"selection":"neural"}`)
			default:
				putSettings(t, path, `{}`)
			}
			r, err := Read(path)
			if r.Path != path || r.State != state || r.Settings.Selection != Neural || r.Settings.Punctuation {
				t.Fatalf("must use existing new file: %+v %v", r, err)
			}
			if (err != nil) != (kind == "unreadable" || kind == "corrupt") || r.ReadOnly != (kind == "newer") {
				t.Fatalf("file status: %+v %v", r, err)
			}
		})
	}
}

func TestLinuxSettingsLegacyErrorsAndWriteFailuresPreserveFiles(t *testing.T) {
	for _, kind := range []string{"corrupt", "unreadable", "newer", "write-failure"} {
		t.Run(kind, func(t *testing.T) {
			path, old := linuxPaths(t)
			body := `{"selection":"statistical","punctuation":true}`
			if kind == "unreadable" {
				if err := os.MkdirAll(old, 0700); err != nil {
					t.Fatal(err)
				}
			} else {
				if kind == "corrupt" {
					body = "{broken"
				} else if kind == "newer" {
					body = `{"schema":9,"selection":"statistical"}`
				}
				putSettings(t, old, body)
			}
			r, err := Read(path)
			if r.Path != old {
				t.Fatalf("must report legacy source: %+v %v", r, err)
			}
			switch kind {
			case "corrupt":
				if !errors.Is(err, ErrCorrupt) || r.State != StateCorrupt {
					t.Fatalf("corrupt legacy file: %+v %v", r, err)
				}
			case "unreadable":
				if err == nil || r.State != StateUnreadable {
					t.Fatalf("unreadable legacy file: %+v %v", r, err)
				}
			case "newer":
				if err != nil || !r.ReadOnly || !errors.Is(Save(path, r.Settings), ErrReadOnly) {
					t.Fatalf("newer legacy file: %+v %v", r, err)
				}
			case "write-failure":
				if err != nil {
					t.Fatal(err)
				}
				putSettings(t, filepath.Dir(path), "blocks mkdir")
				if err := Save(path, r.Settings); err == nil {
					t.Fatal("save must fail when destination is blocked")
				}
			}
			if kind != "unreadable" {
				if b, err := os.ReadFile(old); err != nil || string(b) != body {
					t.Fatalf("legacy file changed: %s %v", b, err)
				}
			}
		})
	}
}

func TestLinuxSettingsDanglingSymlinkIsUnreadable(t *testing.T) {
	for _, brokenNew := range []bool{true, false} {
		path, old := linuxPaths(t)
		broken := path
		if brokenNew {
			putSettings(t, old, `{"selection":"statistical","punctuation":true}`)
		} else {
			broken = old
		}
		if err := os.MkdirAll(filepath.Dir(broken), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("missing-settings.json", broken); err != nil {
			t.Fatal(err)
		}
		r, err := Read(path)
		if err == nil || r.State != StateUnreadable || r.Path != broken || r.Settings != Default() {
			t.Fatalf("dangling symlink must report unreadable source: %+v %v", r, err)
		}
		if target, err := os.Readlink(broken); err != nil || target != "missing-settings.json" {
			t.Fatalf("read must preserve dangling symlink: %s %v", target, err)
		}
	}
}
