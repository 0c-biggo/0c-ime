package main

import (
	"os"
	"path/filepath"
	"testing"
)

// #289：排程與協定改執行 bin\0c-updatew.exe。目前版本附了就跟上（被移除也補回）；沒有附（舊版套件、回復到舊版）
// 時保留原本那一份，因為排程可能正指著它——這點和 0c-mcp 不同。
func TestSyncStableLauncherFollowsTheCurrentVersionButNeverRetires(t *testing.T) {
	root := t.TempDir()
	stable := filepath.Join(root, "bin", launcherName)
	v1 := filepath.Join(root, "versions", "a", launcherName)
	v2 := filepath.Join(root, "versions", "b", launcherName)
	writeFile(t, v1, "launcher 1")
	writeFile(t, v2, "launcher 2")
	for _, step := range []struct {
		source  string
		changed bool
		want    string
	}{{v1, true, "launcher 1"}, {v1, false, "launcher 1"}, {v2, true, "launcher 2"}, {"", false, "launcher 2"}} {
		want := ""
		if step.source != "" {
			want = hashOf(t, step.source)
		}
		changed, err := syncStableLauncher(step.source, want, stable)
		if err != nil || changed != step.changed {
			t.Fatalf("%q: changed=%v err=%v", step.source, changed, err)
		}
		if got := read(t, stable); got != step.want {
			t.Fatalf("stable copy is %q, want %q", got, step.want)
		}
	}
	// 被防毒軟體移除：排程指著它，有來源就補回。
	if err := os.Remove(stable); err != nil {
		t.Fatal(err)
	}
	if changed, err := syncStableLauncher(v2, hashOf(t, v2), stable); err != nil || !changed || read(t, stable) != "launcher 2" {
		t.Fatalf("missing copy must be restored: changed=%v err=%v", changed, err)
	}
}

// 建立排程時從新版更新器旁邊換上啟動器；舊版套件旁邊沒有就保留現有的；更新器就是穩定副本本身時不動。
func TestInstallLauncherBesideTheUpdater(t *testing.T) {
	root := t.TempDir()
	stable := filepath.Join(root, "bin", launcherName)
	withLauncher := filepath.Join(root, "versions", "new", updaterName())
	withoutLauncher := filepath.Join(root, "versions", "old", updaterName())
	writeFile(t, withLauncher, "updater")
	writeFile(t, filepath.Join(filepath.Dir(withLauncher), launcherName), "launcher new")
	writeFile(t, withoutLauncher, "updater")

	if err := installLauncherBeside(withoutLauncher, stable); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(stable); !os.IsNotExist(err) {
		t.Fatalf("no launcher beside an old updater: nothing to install (%v)", err)
	}
	if err := installLauncherBeside(withLauncher, stable); err != nil || read(t, stable) != "launcher new" {
		t.Fatalf("launcher must be installed: %v", err)
	}
	if err := installLauncherBeside(withoutLauncher, stable); err != nil || read(t, stable) != "launcher new" {
		t.Fatalf("an old package must keep the existing launcher: %v", err)
	}
	if err := installLauncherBeside(filepath.Join(root, "bin", updaterName()), stable); err != nil || read(t, stable) != "launcher new" {
		t.Fatalf("the stable updater's own sibling is the stable launcher: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Dir(stable))
	if len(entries) != 1 {
		t.Fatalf("unchanged content must not leave renamed copies: %v", entries)
	}
}
