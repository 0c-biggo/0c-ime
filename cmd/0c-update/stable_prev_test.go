package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/0c-biggo/0c-ime/internal/atomicfile"
	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/update"
	"github.com/0c-biggo/0c-ime/internal/updatew"
)

// contentOf 是檔案內容；不存在時為 "<missing>"。
func contentOf(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return "<missing>"
	}
	return string(b)
}

func withPreviousKept(t *testing.T) string {
	t.Helper()
	old := keepsPreviousUpdater
	keepsPreviousUpdater = true
	t.Cleanup(func() { keepsPreviousUpdater = old })
	base := filepath.Join(t.TempDir(), "Zuin")
	if err := os.MkdirAll(filepath.Join(base, "update"), 0o700); err != nil {
		t.Fatal(err)
	}
	return base
}

// 換穩定副本時，換下來的那一份成為上一版；只留一份，再換一次時最舊的那份移走。
func TestReplaceStableUpdaterKeepsOnePrevious(t *testing.T) {
	base := withPreviousKept(t)
	stable, prev := stableUpdater(base), previousUpdater(base)
	writeFile(t, stable, "v1")
	for _, v := range []string{"v2", "v3"} {
		src := filepath.Join(base, "versions", v, updaterName())
		writeFile(t, src, v)
		if err := replaceStableUpdater(src, base); err != nil {
			t.Fatal(err)
		}
	}
	if contentOf(t, stable) != "v3" || contentOf(t, prev) != "v2" {
		t.Fatalf("stable %q prev %q", contentOf(t, stable), contentOf(t, prev))
	}
	entries, _ := os.ReadDir(filepath.Join(base, "bin"))
	if len(entries) != 2 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("leftovers in bin: %v", names)
	}
}

// 內容相同（repair、完整安裝重跑）時不動：同一份檔案不能把真正的上一版擠掉。
func TestReplaceStableUpdaterSameContentKeepsPrevious(t *testing.T) {
	base := withPreviousKept(t)
	writeFile(t, stableUpdater(base), "v3")
	writeFile(t, previousUpdater(base), "v2")
	src := filepath.Join(base, "versions", "v3", updaterName())
	writeFile(t, src, "v3")
	if err := replaceStableUpdater(src, base); err != nil {
		t.Fatal(err)
	}
	if contentOf(t, stableUpdater(base)) != "v3" || contentOf(t, previousUpdater(base)) != "v2" {
		t.Fatalf("stable %q prev %q", contentOf(t, stableUpdater(base)), contentOf(t, previousUpdater(base)))
	}
}

// 啟動器已判定目前的穩定副本異常（正在退回）時，上一版把修正版裝上來：異常的那份丟掉，保留可用的上一版。
func TestReplaceStableUpdaterDropsFailingCopy(t *testing.T) {
	base := withPreviousKept(t)
	writeFile(t, stableUpdater(base), "v3-broken")
	writeFile(t, previousUpdater(base), "v2")
	broken, _ := update.Hash(stableUpdater(base))
	s := &updatew.State{Schema: 1, Stable: broken, Failures: 3, Fallback: &updatew.Fallback{Reason: "test"}}
	if err := atomicfile.WriteJSON(updatew.StatePath(base), s); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(base, "versions", "v4", updaterName())
	writeFile(t, src, "v4")
	if err := replaceStableUpdater(src, base); err != nil {
		t.Fatal(err)
	}
	if contentOf(t, stableUpdater(base)) != "v4" || contentOf(t, previousUpdater(base)) != "v2" {
		t.Fatalf("stable %q prev %q", contentOf(t, stableUpdater(base)), contentOf(t, previousUpdater(base)))
	}
}

// 穩定副本不見了（被防毒隔離）時照常換上，沒有可保留的上一版就不動原本的。
func TestReplaceStableUpdaterWhenStableMissing(t *testing.T) {
	base := withPreviousKept(t)
	writeFile(t, previousUpdater(base), "v2")
	src := filepath.Join(base, "versions", "v3", updaterName())
	writeFile(t, src, "v3")
	if err := replaceStableUpdater(src, base); err != nil {
		t.Fatal(err)
	}
	if contentOf(t, stableUpdater(base)) != "v3" || contentOf(t, previousUpdater(base)) != "v2" {
		t.Fatalf("stable %q prev %q", contentOf(t, stableUpdater(base)), contentOf(t, previousUpdater(base)))
	}
}

// Linux、macOS 沒有啟動器，不留上一版。
func TestReplaceStableUpdaterNoPreviousOffWindows(t *testing.T) {
	old := keepsPreviousUpdater
	keepsPreviousUpdater = false
	t.Cleanup(func() { keepsPreviousUpdater = old })
	base := filepath.Join(t.TempDir(), "zuin")
	writeFile(t, stableUpdater(base), "v1")
	src := filepath.Join(base, "versions", "v2", updaterName())
	writeFile(t, src, "v2")
	if err := replaceStableUpdater(src, base); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(previousUpdater(base)); !os.IsNotExist(err) {
		t.Fatal("previous kept off Windows")
	}
}

func TestExitCodeForLauncherContract(t *testing.T) {
	netErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	for _, c := range []struct {
		args []string
		err  error
		want int
	}{
		{[]string{"auto"}, nil, 0},
		{[]string{"auto"}, autoupdate.ErrLocked, updatew.ExitTemporary},
		{[]string{"auto"}, fmt.Errorf("x: %w", autoupdate.ErrUpdateInProgress), updatew.ExitTemporary},
		{[]string{"protocol", "zc-update:finish"}, autoupdate.ErrTargetChanged, updatew.ExitTemporary},
		{[]string{"protocol", "zc-update:finish"}, &url.Error{Op: "Get", URL: "https://example.invalid", Err: netErr}, updatew.ExitTemporary},
		{[]string{"engine"}, context.DeadlineExceeded, updatew.ExitTemporary},
		{[]string{"auto"}, errors.New("unsupported update state"), 1},
		{[]string{"engine"}, errors.New("引擎沒有啟動"), 1},
		// 啟動器不執行的命令維持原本的結束碼。
		{[]string{"apply"}, autoupdate.ErrLocked, 1},
		{[]string{"check-account"}, errOtherAccount, otherAccountExit},
	} {
		if got := exitCodeFor(c.args, c.err); got != c.want {
			t.Errorf("exitCodeFor(%v, %v) = %d, want %d", c.args, c.err, got, c.want)
		}
	}
}
