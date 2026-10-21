package main

import (
	"path/filepath"
	"testing"
)

// #179：背景清理不用安裝程式暫存目錄裡的 0c-update（占住暫存目錄），在安裝目錄裡的才用自己。
func TestPruneExecutableStaysInsideInstallation(t *testing.T) {
	base := filepath.Join(t.TempDir(), "Zuin")
	fallback := filepath.Join(base, "versions", "0.2.3-0123456789ab", "0c-update")
	for self, want := range map[string]string{
		filepath.Join(base, "bin", "0c-update"):                            filepath.Join(base, "bin", "0c-update"),
		filepath.Join(base, "versions", "0.2.2-aaaaaaaaaaaa", "0c-update"): filepath.Join(base, "versions", "0.2.2-aaaaaaaaaaaa", "0c-update"),
		filepath.Join(filepath.Dir(base), "nsA1B2.tmp", "0c-update"):       fallback,
		filepath.Join(base+"-other", "0c-update"):                          fallback,
		base: fallback,
		"":   fallback,
	} {
		if got := pruneExecutableFor(self, base, fallback); got != want {
			t.Errorf("pruneExecutableFor(%q) = %q, want %q", self, got, want)
		}
	}
}

// 安裝、回復、解除安裝成功時記下的 0c-update，在結束前啟動一次背景清理；沒記下就不啟動。
func TestLaunchPruneStartsOnceWhenRequested(t *testing.T) {
	var started []string
	saved := startPrune
	startPrune = func(exe string) error { started = append(started, exe); return nil }
	t.Cleanup(func() { startPrune = saved; pruneFrom = "" })
	launchPrune()
	if len(started) != 0 {
		t.Fatal("started without an install")
	}
	pruneFrom = "/x/versions/0.2.3-0123456789ab/0c-update"
	launchPrune()
	launchPrune()
	if len(started) != 1 || started[0] != "/x/versions/0.2.3-0123456789ab/0c-update" {
		t.Fatalf("started %v", started)
	}
}
