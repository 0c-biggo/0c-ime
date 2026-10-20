//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/internal/settings"
)

func settingsMigrationBase(t *testing.T) (base, path string) {
	t.Helper()
	base = filepath.Join(t.TempDir(), ".local", "share", "zuin")
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	return base, settings.Path(filepath.Join(base, "user.json"))
}

func TestLinuxSettingsCommandMigratesOnEditAndStatusFollowsNewFile(t *testing.T) {
	base, path := settingsMigrationBase(t)
	body := `{"schema":1,"selection":"statistical","punctuation":true,"future":{"x":1}}`
	old := writeSettings(t, base, body)
	doc := buildSettingsDoc(base, noEngine)
	if doc.File.Path != old || doc.File.WritePath != path || item(t, doc, "punctuation").Value != "on" {
		t.Fatalf("legacy settings doc: %+v", doc)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("settings query must not migrate: %v", err)
	}
	if res, err := runSet(base, noEngine, setOptions{pairs: [][2]string{{"document-context", "off"}}}, nop); err != nil || !res.OK {
		t.Fatalf("first edit: %+v %v", res, err)
	}
	if readFile(t, old) != body || !strings.Contains(readFile(t, path), `"x": 1`) {
		t.Fatal("first edit must preserve old file and unknown fields")
	}
	doc = buildSettingsDoc(base, noEngine)
	if doc.File.Path != path || doc.File.WritePath != "" || item(t, doc, "punctuation").Value != "on" || item(t, doc, "document-context").Value != "off" {
		t.Fatalf("migrated settings doc: %+v", doc)
	}
	st := statusMap(base, "", noEngine)
	values := st["settings"].(map[string]any)
	if st["settings_state"] != settings.StateOK || values["document_context"] != false || values["punctuation"] != true {
		t.Fatalf("menu status must use migrated settings: %+v", st)
	}
	if _, err := runSet(base, noEngine, setOptions{pairs: [][2]string{{"punctuation", "off"}}}, nop); err != nil {
		t.Fatal(err)
	}
	if s, err := settings.Load(path); err != nil || s.Punctuation || s.DocumentContext || s.Selection != settings.Statistical {
		t.Fatalf("second edit must start from new file: %+v %v", s, err)
	}
	if readFile(t, old) != body {
		t.Fatal("second edit must leave old file untouched")
	}
}

func TestLinuxSettingsFailedEditDoesNotMigrate(t *testing.T) {
	for _, body := range []string{`{"selection":"statistical"}`, `{"schema":9}`, "{broken"} {
		t.Run(body, func(t *testing.T) {
			base, path := settingsMigrationBase(t)
			old := writeSettings(t, base, body)
			if _, err := runSet(base, noEngine, setOptions{pairs: [][2]string{{"punctuation", "off"}, {"nope", "x"}}}, nop); err == nil {
				t.Fatal("invalid, read-only or corrupt settings must reject edit")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) || readFile(t, old) != body {
				t.Fatalf("failed edit must preserve files: %v", err)
			}
		})
	}
}

func TestLinuxSettingsRepairUsesActualCorruptSource(t *testing.T) {
	for _, corruptNew := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "new"}[corruptNew], func(t *testing.T) {
			base, path := settingsMigrationBase(t)
			old := writeSettings(t, base, "{broken")
			source := old
			if corruptNew {
				writeSettings(t, base, `{"punctuation":true}`)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("{broken"), 0600); err != nil {
					t.Fatal(err)
				}
				source = path
			}
			res, err := runSet(base, noEngine, setOptions{pairs: [][2]string{{"selection", "statistical"}}, repair: true}, nop)
			if err != nil || !strings.HasPrefix(res.Quarantined, source+".corrupt-") || readFile(t, res.Quarantined) != "{broken" {
				t.Fatalf("repair source: %+v %v", res, err)
			}
			if s, err := settings.Load(path); err != nil || s.Selection != settings.Statistical || s.Punctuation {
				t.Fatalf("repair must save new file from defaults: %+v %v", s, err)
			}
			if corruptNew && readFile(t, old) != `{"punctuation":true}` {
				t.Fatal("repairing new file must leave legacy settings alone")
			}
		})
	}
}

func TestLinuxSettingsSetDoesNotReplaceDanglingNewSymlink(t *testing.T) {
	base, path := settingsMigrationBase(t)
	old := writeSettings(t, base, `{"selection":"statistical","punctuation":true}`)
	body := readFile(t, old)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing-settings.json", path); err != nil {
		t.Fatal(err)
	}
	doc := buildSettingsDoc(base, noEngine)
	if doc.File.Path != path || doc.File.State != settings.StateUnreadable || doc.File.WritePath != "" {
		t.Fatalf("new symlink must not appear as a successful fallback: %+v", doc.File)
	}
	for _, repair := range []bool{false, true} {
		_, err := runSet(base, noEngine, setOptions{pairs: [][2]string{{"punctuation", "off"}}, repair: repair}, nop)
		if err == nil || classify(err).Code != codeIO {
			t.Fatalf("set must reject unreadable new symlink, repair=%v: %v", repair, err)
		}
	}
	if target, err := os.Readlink(path); err != nil || target != "missing-settings.json" || readFile(t, old) != body {
		t.Fatalf("failed edit must preserve symlink and old file: %s %v", target, err)
	}
}
