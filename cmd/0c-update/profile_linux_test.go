//go:build !windows && !darwin

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddInputMethodKeepsUserProfile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "fcitx5", "profile")
	os.MkdirAll(filepath.Dir(path), 0o755)
	original := "[Groups/0]\nName=預設\nDefault Layout=jp\nDefaultIM=mozc\n\n[Groups/0/Items/0]\nName=keyboard-jp\nLayout=\n\n" +
		"[Groups/0/Items/1]\nName=mozc\nLayout=\n\n[GroupOrder]\n0=預設\n"
	os.WriteFile(path, []byte(original), 0o644)
	if err := addInputMethod(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(got), strings.TrimRight(original, "\n")) || !strings.Contains(string(got), "[Groups/0/Items/2]\nName=zuin\n") {
		t.Fatalf("zuin must be appended without changing the user's setup:\n%s", got)
	}
	if b, _ := os.ReadFile(path + ".0c-backup"); string(b) != original {
		t.Fatal("the original profile must be backed up")
	}
	if err := addInputMethod(); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(path)
	if string(again) != string(got) {
		t.Fatal("adding twice must not change the profile")
	}
}

func TestAddInputMethodCreatesProfile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := addInputMethod(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "fcitx5", "profile"))
	if !strings.Contains(string(got), "Name=zuin") || !strings.Contains(string(got), "[GroupOrder]") {
		t.Fatalf("%s", got)
	}
}

// Fcitx5 自己寫出的格式（每個鍵前有註解）：新項目必須接在既有項目之後，不能與它們撞號。
func TestAddInputMethodWithFcitxWrittenProfile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "fcitx5", "profile")
	os.MkdirAll(filepath.Dir(path), 0o755)
	original := "[Groups/0]\n# Group Name\nName=Default\n# Layout\nDefault Layout=us\n# Default Input Method\nDefaultIM=keyboard-us\n\n" +
		"[Groups/0/Items/0]\n# Name\nName=keyboard-us\n# Layout\nLayout=\n\n[GroupOrder]\n0=Default\n"
	os.WriteFile(path, []byte(original), 0o644)
	if err := addInputMethod(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if strings.Count(string(got), "[Groups/0/Items/0]") != 1 || !strings.Contains(string(got), "[Groups/0/Items/1]\nName=zuin\n") {
		t.Fatalf("must append as item 1:\n%s", got)
	}
	os.WriteFile(path, []byte(strings.Replace(string(got), "Name=zuin", "# Name\nName=zuin", 1)), 0o644)
	before, _ := os.ReadFile(path)
	addInputMethod()
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("an existing zuin item with comments must be recognised")
	}
}
