package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/update"
	"github.com/0c-biggo/0c-ime/internal/updatew"
)

// selftestFeed 是以測試金鑰簽章、再以客戶端規則核對過的索引。
func selftestFeed(t *testing.T, version string) func(context.Context, string, uint64) (*update.SignedFeed, error) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now().UTC().Truncate(time.Second)
	f := &update.SignedFeed{Schema: update.SignedFeedSchema, Product: "0c", Serial: 9, Issued: now, Expires: now.Add(time.Hour),
		Channel: "public-beta", Version: version, Rollout: 100,
		Assets: []update.SignedAsset{{Asset: update.Asset{OS: runtime.GOOS, Arch: runtime.GOARCH, URL: "https://example.org/a", SHA256: strings.Repeat("a", 64), Size: 10}, Kind: "shell"}}}
	body, sig, err := update.SignFeed(f, priv)
	if err != nil {
		t.Fatal(err)
	}
	return func(_ context.Context, _ string, last uint64) (*update.SignedFeed, error) {
		return update.VerifySignedFeed(body, sig, []ed25519.PublicKey{pub}, now, last)
	}
}

func selftestEnvFor(t *testing.T, base string) selftestEnv {
	return selftestEnv{base: base, installed: func() (string, error) { return "0.2.14", nil }, shell: func() string { return "0.2.14" }, fetch: selftestFeed(t, "0.2.15"), now: time.Now}
}

// selftest 讀狀態、驗索引、試算下一步：都通過才是 pass，試算看得到下一版。
func TestSelftestPassesAndPlansTheNextStep(t *testing.T) {
	base := t.TempDir()
	r := runSelftest(context.Background(), selftestEnvFor(t, base))
	if r.Result != selftestPass || len(r.Checks) != 4 || !strings.Contains(r.Plan, "0.2.15") {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(base, "update")); !os.IsNotExist(err) {
		t.Fatal("selftest 不能寫任何檔案")
	}
}

// 連不上索引是環境問題（inconclusive，結束碼 75）；簽章不對、狀態檔讀不懂、試算當掉是新版自己的問題（fail）。
func TestSelftestSeparatesEnvironmentFromUpdaterFaults(t *testing.T) {
	base := t.TempDir()
	env := selftestEnvFor(t, base)
	env.fetch = func(context.Context, string, uint64) (*update.SignedFeed, error) {
		return nil, &net.OpError{Op: "dial", Err: errors.New("connection refused")}
	}
	if r := runSelftest(context.Background(), env); r.Result != selftestInconclusive {
		t.Fatalf("network: %+v", r)
	}
	env.fetch = func(context.Context, string, uint64) (*update.SignedFeed, error) {
		return nil, errors.New("feed signature is not from a trusted release key")
	}
	if r := runSelftest(context.Background(), env); r.Result != selftestFail {
		t.Fatalf("signature: %+v", r)
	}
	env = selftestEnvFor(t, base)
	writeFile(t, autoupdate.Path(base), "{not json")
	if r := runSelftest(context.Background(), env); r.Result != selftestFail || r.Checks[0].Name != "state" {
		t.Fatalf("state: %+v", r)
	}
	os.Remove(autoupdate.Path(base))
	env.installed = func() (string, error) { panic("boom") }
	if r := runSelftest(context.Background(), env); r.Result != selftestFail {
		t.Fatalf("panic: %+v", r)
	}
	if selftestExitCode(nil) != 0 || selftestExitCode(errSelftestInconclusive) != updatew.ExitTemporary || selftestExitCode(errReportedSelftest) != 1 {
		t.Fatal("exit codes are part of the contract")
	}
}

// 呼叫端只認「結束碼與 JSON 一致」的結果；讀不到 JSON（舊版不認得命令、當掉）都算失敗。
func TestParseSelftestContract(t *testing.T) {
	pass := `{"schema":1,"result":"pass","version":"0.2.15","checks":[],"plan":"ok"}`
	cases := []struct {
		out  string
		code int
		want string
	}{
		{pass, 0, selftestPass},
		{"some log\n" + pass, 0, selftestPass},
		{pass, 1, selftestFail},
		{`{"schema":1,"result":"inconclusive","checks":[{"name":"feed","ok":false,"detail":"dial"}]}`, 75, selftestInconclusive},
		{`{"schema":1,"result":"inconclusive"}`, 1, selftestFail},
		{"未知命令：selftest", 1, selftestFail},
		{"", 2, selftestFail},
	}
	for _, c := range cases {
		if got, _ := parseSelftest([]byte(c.out), c.code, nil); got != c.want {
			t.Errorf("%q/%d: %s, want %s", c.out, c.code, got, c.want)
		}
	}
	if got, _ := parseSelftest(nil, 0, errors.New("not found")); got != selftestFail {
		t.Fatal("start failure")
	}
}

func withSelftest(t *testing.T, results ...string) *int {
	t.Helper()
	calls := 0
	old, oldNow := runCandidateSelftest, qualifyNow
	runCandidateSelftest = func(string) (string, string) {
		r := results[min(calls, len(results)-1)]
		calls++
		return r, "detail " + r
	}
	t.Cleanup(func() { runCandidateSelftest, qualifyNow = old, oldNow })
	return &calls
}

// 沒通過自我測試的新版不換上穩定副本，記下候選；冷卻時間到了再試，通過才換。
func TestAdoptStableUpdaterKeepsCurrentUntilCandidateQualifies(t *testing.T) {
	base := withPreviousKept(t)
	stable := stableUpdater(base)
	writeFile(t, stable, "v1")
	src := filepath.Join(base, "versions", "0.2.15", updaterName())
	writeFile(t, src, "v2")
	calls := withSelftest(t, selftestInconclusive, selftestPass)
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	qualifyNow = func() time.Time { return now }

	if err := adoptStableUpdater(src, "0.2.15", base); !errors.Is(err, errNotQualified) {
		t.Fatalf("adopt: %v", err)
	}
	if contentOf(t, stable) != "v1" || loadQualify(base).Pending == nil {
		t.Fatalf("stable %q pending %+v", contentOf(t, stable), loadQualify(base).Pending)
	}
	retryPendingUpdater(base) // 還在冷卻：不重跑
	if *calls != 1 || contentOf(t, stable) != "v1" {
		t.Fatalf("retried too early: %d", *calls)
	}
	now = now.Add(qualifyRetryAfter + time.Minute)
	retryPendingUpdater(base)
	if *calls != 2 || contentOf(t, stable) != "v2" || contentOf(t, previousUpdater(base)) != "v1" || loadQualify(base).Pending != nil {
		t.Fatalf("calls %d stable %q prev %q", *calls, contentOf(t, stable), contentOf(t, previousUpdater(base)))
	}
}

// 失敗的結果 72 小時內不重跑；沒有穩定副本時沒有東西可保留，直接換上；還沒有 selftest 的舊版不擋。
func TestAdoptStableUpdaterFailureCooldownAndExceptions(t *testing.T) {
	base := withPreviousKept(t)
	stable := stableUpdater(base)
	src := filepath.Join(base, "versions", "0.2.15", updaterName())
	writeFile(t, src, "v2")
	calls := withSelftest(t, selftestFail)
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	qualifyNow = func() time.Time { return now }

	if err := adoptStableUpdater(src, "0.2.15", base); err != nil || contentOf(t, stable) != "v2" || *calls != 0 {
		t.Fatalf("no stable copy: %v %q calls %d", err, contentOf(t, stable), *calls)
	}
	writeFile(t, stable, "v1")
	for i := 0; i < 3; i++ {
		if err := adoptStableUpdater(src, "0.2.15", base); !errors.Is(err, errNotQualified) {
			t.Fatal(err)
		}
	}
	if *calls != 1 || contentOf(t, stable) != "v1" {
		t.Fatalf("calls %d", *calls)
	}
	now = now.Add(qualifyFailRetry + time.Minute)
	_ = adoptStableUpdater(src, "0.2.15", base)
	if *calls != 2 {
		t.Fatalf("failure retry after cooldown: %d", *calls)
	}
	old := filepath.Join(base, "versions", "0.2.14", updaterName())
	writeFile(t, old, "v0")
	if err := adoptStableUpdater(old, "0.2.14", base); err != nil || contentOf(t, stable) != "v0" || *calls != 2 {
		t.Fatalf("legacy candidate: %v %q", err, contentOf(t, stable))
	}
}
