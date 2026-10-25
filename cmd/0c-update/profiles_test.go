package main

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/0c-biggo/0c-ime/internal/settings"
)

func TestProfileCLITransactionAndRuntimeFingerprint(t *testing.T) {
	base := t.TempDir()
	f := &fakeEngine{composing: "0", info: map[string]string{}}
	res, err := runProfile(base, f.dial, profileOptions{action: "copy", id: "microsoft", name: "中文工作習慣"})
	if err != nil {
		t.Fatal(err)
	}
	id := res.Profiles.Active
	if id == "microsoft" || res.Apply.Requested {
		t.Fatal(res)
	}
	s, err := settings.Load(settings.Path(filepath.Join(base, "user.json")))
	if err != nil {
		t.Fatal(err)
	}
	f.info["input_profile"] = id
	f.info["input_profile_fingerprint"] = s.ProfileFingerprint()
	if buildSettingsDoc(base, f.dial).Profiles.Pending {
		t.Fatal("matching rules pending")
	}
	_, err = runSet(base, f.dial, setOptions{pairs: [][2]string{{"style-shift-space", "space"}}}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	doc := buildSettingsDoc(base, f.dial)
	if !doc.Profiles.Pending || doc.Profiles.Active != id || doc.Profiles.Running != id {
		t.Fatal("same ID rule changes must be pending")
	}
	_, err = runProfile(base, f.dial, profileOptions{action: "select", id: "chewing"})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := settings.Load(settings.Path(filepath.Join(base, "user.json")))
	_, err = runSet(base, f.dial, setOptions{pairs: [][2]string{{"punctuation", "on"}, {"space-selects", "off"}}}, func(string) {})
	var se *settings.SetError
	if !errors.As(err, &se) || se.Code != "locked_by_profile" {
		t.Fatal(err)
	}
	after, _ := settings.Load(settings.Path(filepath.Join(base, "user.json")))
	if before != after {
		t.Fatal("partial transaction saved")
	}
}
func TestParseProfileArgsRejectsAmbiguousOrUnknownActions(t *testing.T) {
	for _, args := range [][]string{{"copy", "0c"}, {"select", "0c", "--name", "x"}, {"delete", "x", "-apply"}, {"foo", "0c"}, {"copy", "0c", "--name", "x", "--name", "y"}} {
		if _, err := parseProfileArgs(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	o, err := parseProfileArgs([]string{"copy", "microsoft", "--name", "我的習慣", "-apply", "-json"})
	if err != nil || o.name != "我的習慣" || !o.apply || !o.json {
		t.Fatal(o, err)
	}
}
