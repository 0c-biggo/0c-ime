//go:build darwin || linux

package main

import (
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// recordReclaim 把 reclaimFchown 換成記錄：一般帳號不能把檔案改給別人，這裡記下會被改的路徑（相對 base）。
func recordReclaim(t *testing.T, base string) *[]string {
	t.Helper()
	var got []string
	old := reclaimFchown
	reclaimFchown = func(fd, uid, gid int) error {
		var st unix.Stat_t
		if err := unix.Fstat(fd, &st); err != nil {
			t.Fatal(err)
		}
		if gid != -1 {
			t.Errorf("群組不應該改：%d", gid)
		}
		got = append(got, inodePath(t, base, st))
		return nil
	}
	t.Cleanup(func() { reclaimFchown = old })
	return &got
}

// inodePath 由 inode 找回 base 底下的路徑（不跟隨連結）；找不到時回傳 "outside"。
func inodePath(t *testing.T, base string, want unix.Stat_t) string {
	found := "outside"
	filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		var st unix.Stat_t
		if unix.Lstat(path, &st) == nil && st.Ino == want.Ino && st.Dev == want.Dev && found == "outside" {
			rel, _ := filepath.Rel(base, path)
			found = filepath.ToSlash(rel)
		}
		return nil
	})
	return found
}

func mustWrite(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// #198：root 安裝留下的整棵樹（App、收據、設定、備份、引擎 socket）都要改回使用者；連結指到外面、
// 硬連結進來的系統檔案、FIFO 都不能碰。測試以自己的 uid 當作 root（from），記錄會改哪些項目。
func TestReclaimOwnershipWalksOnlyOurTree(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	me := os.Getuid()
	mustWrite(t, filepath.Join(outside, "secret"))
	mustWrite(t, filepath.Join(outside, "dir", "file"))

	support := filepath.Join(home, "Library/Application Support/0c")
	mustWrite(t, filepath.Join(support, "install-receipt.json"))
	mustWrite(t, filepath.Join(support, "bin", "0c-update"))
	mustWrite(t, filepath.Join(support, "backups", "macos-1", "previous.json"))
	if err := os.Symlink(filepath.Join(outside, "dir"), filepath.Join(support, "link-dir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(support, "link-file")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(outside, "secret"), filepath.Join(support, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(support, "fifo"), 0o600); err != nil {
		t.Fatal(err)
	}
	caches := filepath.Join(home, "Library/Caches/0c")
	if err := os.MkdirAll(caches, 0o700); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(caches, "engine.sock")
	t.Chdir(caches) // 以相對路徑建立 socket：macOS 的暫存目錄路徑超過 socket 路徑上限
	l, err := net.Listen("unix", "engine.sock")
	if err != nil {
		t.Fatal(err)
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	l.Close()

	got := recordReclaim(t, home)
	n, err := reclaimOwnership(home, "Library/Application Support/0c", me, me)
	if err != nil {
		t.Fatal(err)
	}
	m, err := reclaimOwnership(home, "Library/Caches/0c", me, me)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(*got)
	want := []string{
		"Library/Application Support/0c",
		"Library/Application Support/0c/backups",
		"Library/Application Support/0c/backups/macos-1",
		"Library/Application Support/0c/backups/macos-1/previous.json",
		"Library/Application Support/0c/bin",
		"Library/Application Support/0c/bin/0c-update",
		"Library/Application Support/0c/install-receipt.json",
		"Library/Caches/0c",
	}
	if strings.Join(*got, "\n") != strings.Join(want, "\n") {
		t.Errorf("改了：\n%s\n要：\n%s", strings.Join(*got, "\n"), strings.Join(want, "\n"))
	}
	if n != 7 || m != 2 { // socket 刪掉也算一項
		t.Errorf("回報的項數 %d、%d", n, m)
	}
	if _, err := os.Lstat(sock); !os.IsNotExist(err) {
		t.Errorf("root 留下的 socket 要刪掉：%v", err)
	}
	for _, keep := range []string{"link-dir", "link-file", "hardlink", "fifo"} {
		if _, err := os.Lstat(filepath.Join(support, keep)); err != nil {
			t.Errorf("%s 不能被動到：%v", keep, err)
		}
	}
}

// 擁有者不是 from 的不改，但仍往下找（使用者自己的目錄裡可能有 root 的檔案）；不存在的路徑跳過。
func TestReclaimOwnershipOnlyFromOwner(t *testing.T) {
	home := t.TempDir()
	mustWrite(t, filepath.Join(home, "Library/Input Methods/0c.app/Contents/Info.plist"))
	got := recordReclaim(t, home)
	n, err := reclaimOwnership(home, "Library/Input Methods/0c.app", os.Getuid()+12345, 501)
	if err != nil || n != 0 || len(*got) != 0 {
		t.Errorf("別人的檔案不改：%d %v %v", n, *got, err)
	}
	if n, err := reclaimOwnership(home, "Library/Input Methods/Zuin.app", os.Getuid(), 501); n != 0 || err != nil {
		t.Errorf("不存在的路徑跳過：%d %v", n, err)
	}
}

// 上層任何一層是符號連結（例如 Library/Caches 指到別的帳號或系統目錄）就不進去。
func TestReclaimOwnershipRefusesLinkedParents(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	mustWrite(t, filepath.Join(outside, "0c", "engine.lock"))
	if err := os.MkdirAll(filepath.Join(home, "Library"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, "Library/Caches")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "0c"), filepath.Join(home, "Library/0c-link")); err != nil {
		t.Fatal(err)
	}
	got := recordReclaim(t, home)
	for _, rel := range []string{"Library/Caches/0c", "Library/0c-link"} {
		if n, err := reclaimOwnership(home, rel, os.Getuid(), os.Getuid()); n != 0 || err != nil {
			t.Errorf("%s：%d %v", rel, n, err)
		}
	}
	if len(*got) != 0 {
		t.Errorf("跟著連結改到外面：%v", *got)
	}
	if count, _ := foreignOwned(home, []string{"Library/Caches/0c"}, os.Getuid()+1, 3); count != 0 {
		t.Errorf("使用者身分的檢查也不跟隨上層連結：%d", count)
	}
}

// 使用者身分的檢查：列出擁有者不是自己的項目個數與例子，不跟隨連結。
func TestForeignOwned(t *testing.T) {
	home := t.TempDir()
	mustWrite(t, filepath.Join(home, "Library/Application Support/0c/settings.json"))
	mustWrite(t, filepath.Join(home, "Library/Input Methods/0c.app/Contents/Info.plist"))
	if count, _ := foreignOwned(home, macOwnedPaths, os.Getuid(), 3); count != 0 {
		t.Errorf("都是自己的：%d", count)
	}
	count, sample := foreignOwned(home, macOwnedPaths, os.Getuid()+1, 2)
	if count != 5 || len(sample) != 2 || !strings.HasSuffix(sample[0], "Library/Input Methods/0c.app") {
		t.Errorf("當成別人的：%d %v", count, sample)
	}
}
