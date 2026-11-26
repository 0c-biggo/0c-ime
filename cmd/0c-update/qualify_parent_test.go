package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 候選說「連不上索引」時以目前的更新器對照：目前的查得到，問題在新版（失敗）；兩邊都查不到才是環境問題。
func TestInconclusiveSelftestComparedWithCurrentUpdater(t *testing.T) {
	base := withPreviousKept(t)
	src := filepath.Join(base, "versions", "0.2.16", updaterName())
	writeFile(t, src, "v2")
	withSelftest(t, selftestInconclusive)
	now := time.Now()
	qualifyNow = func() time.Time { return now }
	old := parentFeedCheck
	t.Cleanup(func() { parentFeedCheck = old })

	parentFeedCheck = func(string) error { return errors.New("offline") }
	if result, _ := qualifyUpdaterFrom(base, src, "0.2.16", strings.Repeat("a", 64)); result != selftestInconclusive {
		t.Fatalf("both offline: %s", result)
	}
	// 冷卻中：外殼更新不必再解開套件就知道要稍後再試。
	if result, _, ok := stagedVerdict(base, strings.Repeat("a", 64)); !ok || result != selftestInconclusive {
		t.Fatalf("staged verdict: %s %v", result, ok)
	}
	if _, _, ok := stagedVerdict(base, strings.Repeat("b", 64)); ok {
		t.Fatal("other archive")
	}
	now = now.Add(qualifyRetryAfter + time.Minute)
	if _, _, ok := stagedVerdict(base, strings.Repeat("a", 64)); ok {
		t.Fatal("cooldown over")
	}
	parentFeedCheck = func(string) error { return nil }
	if result, detail := qualifyUpdaterFrom(base, src, "0.2.16", ""); result != selftestFail || !strings.Contains(detail, "新版查不到") {
		t.Fatalf("current updater reaches the feed: %s %s", result, detail)
	}
}
