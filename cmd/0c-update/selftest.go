package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/update"
	"github.com/0c-biggo/0c-ime/internal/updatew"
)

// selftest（#293）：新版 0c-update 換上排程用的穩定副本之前，先以它自己執行一次，證明它在這台電腦上讀得懂現有狀態、
// 驗得過線上索引的簽章、算得出下一步。比照 Chromium updater 的 qualification：新版先通過自我測試才接手，舊版保留。
//
// 輸出與結束碼是固定契約（之後每一版的更新器都會這樣呼叫下一版，見 docs/updating.md〈新更新器先自我測試〉）：
//
//	0c-update selftest -json
//
// stdout 是一份 JSON：{"schema":1,"result":"pass|fail|inconclusive","version":"…","checks":[{"name","ok","detail"}],"plan":"…"}；
// 結束碼 0 是 pass、75（updatew.ExitTemporary）是 inconclusive（連不上索引、索引過期等環境問題，不是這一版的錯）、
// 其他都是 fail。不寫任何檔案、不拿鎖、不下載、不安裝。
const (
	selftestPass         = "pass"
	selftestFail         = "fail"
	selftestInconclusive = "inconclusive"
	selftestSchema       = 1
)

// selftestSince 是第一個有 selftest 的版本；更舊的版本（例如回復到舊版時的更新器）不會有這個命令，不擋。
// 用 -dev.0 讓 0.2.15 的開發與測試建置也走自我測試。
const selftestSince = "0.2.15-dev.0"

type selftestCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

type selftestReport struct {
	Schema  int             `json:"schema"`
	Result  string          `json:"result"`
	Version string          `json:"version"`
	Checks  []selftestCheck `json:"checks"`
	Plan    string          `json:"plan,omitempty"`
}

// selftestEnv 是 selftest 讀取的東西；測試替換。
type selftestEnv struct {
	base      string
	installed func() (string, error)
	shell     func() string
	fetch     func(ctx context.Context, feed string, lastSerial uint64) (*update.SignedFeed, error)
	now       func() time.Time
}

func defaultSelftestEnv() selftestEnv {
	return selftestEnv{
		base:      basePath(),
		installed: func() (string, error) { return (&system{base: basePath()}).Installed() },
		shell:     (&system{}).ShellVersion,
		fetch: func(ctx context.Context, feed string, lastSerial uint64) (*update.SignedFeed, error) {
			f, _, err := update.ReadSignedFeed(ctx, update.Client(), feed, update.TrustedKeys(), time.Now(), lastSerial)
			return f, err
		},
		now: time.Now,
	}
}

func runSelftest(ctx context.Context, env selftestEnv) (r selftestReport) {
	r = selftestReport{Schema: selftestSchema, Result: selftestPass, Version: buildVersion}
	add := func(name string, err error) bool {
		c := selftestCheck{Name: name, OK: err == nil}
		if err != nil {
			c.Detail = err.Error()
		}
		r.Checks = append(r.Checks, c)
		return err == nil
	}
	defer func() {
		// 自我測試本身當掉就是失敗：這正是它要攔下來的情況。
		if p := recover(); p != nil {
			add("panic", fmt.Errorf("%v", p))
			r.Result = selftestFail
		}
	}()
	s, err := autoupdate.Load(env.base)
	if !add("state", err) {
		r.Result = selftestFail
		return r
	}
	installed, err := env.installed()
	if !add("install", err) {
		r.Result = selftestFail
		return r
	}
	feed := DefaultFeed
	if s.Feed != "" {
		feed = s.Feed
	}
	f, err := env.fetch(ctx, feed, s.FeedSerial)
	if !add("feed", err) {
		r.Result = selftestFail
		if environmentFailure(err) {
			r.Result = selftestInconclusive
		}
		return r
	}
	goos, arch := runtime.GOOS, runtime.GOARCH
	in := autoupdate.DryRunInputs{Installed: installed, Shell: env.shell(), Updater: (&system{}).UpdaterVersion(), GOOS: goos, GOARCH: arch}
	plan, err := autoupdate.DryRun(ctx, s, f, in, env.now())
	if !add("plan", err) {
		r.Result = selftestFail
		return r
	}
	r.Plan = autoupdate.Describe(plan)
	return r
}

// environmentFailure：查詢索引失敗的原因在這台電腦或伺服器（連不上、逾時、HTTP 錯誤、索引過期或時鐘不對），
// 舊版更新器也會遇到，不能算成新版的錯。簽章不對、格式讀不懂才是新版自己的問題。
func environmentFailure(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	var urlErr *url.Error
	if errors.As(err, &netErr) || errors.As(err, &urlErr) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	text := err.Error()
	for _, s := range []string{"update server returned HTTP", "feed expired", "issued in the future", "is older than", "feed response too large"} {
		if strings.Contains(text, s) {
			return true
		}
	}
	return false
}

func selftestCommand(args []string) error {
	asJSON := false
	for _, a := range args {
		if !isJSONFlag(a) {
			return fmt.Errorf("用法：0c-update selftest [-json]")
		}
		asJSON = true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	r := runSelftest(ctx, defaultSelftestEnv())
	if asJSON {
		if err := emitJSON(os.Stdout, r); err != nil {
			return err
		}
	} else {
		printSelftest(os.Stdout, r)
	}
	switch r.Result {
	case selftestPass:
		return nil
	case selftestInconclusive:
		return errSelftestInconclusive
	}
	return errReportedSelftest
}

var (
	errSelftestInconclusive = errors.New("自我測試無法完成（環境問題），稍後再試")
	errReportedSelftest     = errors.New("自我測試未通過")
)

func printSelftest(w io.Writer, r selftestReport) {
	fmt.Fprintf(w, "0c-update %s 自我測試：%s\n", r.Version, r.Result)
	for _, c := range r.Checks {
		mark := "通過"
		if !c.OK {
			mark = "失敗"
		}
		fmt.Fprintf(w, "  %s：%s %s\n", c.Name, mark, c.Detail)
	}
	if r.Plan != "" {
		fmt.Fprintln(w, "  試算："+r.Plan)
	}
}

// selftestExitCode：selftest 的結束碼是契約的一部分（0、75、1）。
func selftestExitCode(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, errSelftestInconclusive):
		return updatew.ExitTemporary
	}
	return 1
}

// parseSelftest 解讀候選更新器 selftest -json 的結果（契約見上）。結束碼與 JSON 不一致、讀不到 JSON 都算失敗。
func parseSelftest(out []byte, code int, startErr error) (string, string) {
	if startErr != nil {
		return selftestFail, "無法執行：" + startErr.Error()
	}
	var r selftestReport
	if err := json.Unmarshal(lastJSONLine(out), &r); err != nil || r.Schema < 1 {
		return selftestFail, fmt.Sprintf("沒有自我測試結果（結束碼 %d）", code)
	}
	detail := ""
	for _, c := range r.Checks {
		if !c.OK {
			detail = c.Name + "：" + c.Detail
		}
	}
	switch {
	case code == 0 && r.Result == selftestPass:
		return selftestPass, r.Plan
	case code == updatew.ExitTemporary && r.Result == selftestInconclusive:
		return selftestInconclusive, detail
	}
	if detail == "" {
		detail = fmt.Sprintf("結果 %s、結束碼 %d", r.Result, code)
	}
	return selftestFail, detail
}

// lastJSONLine：stdout 理應只有一份 JSON；萬一前面混了別的輸出，取最後一個非空行。
func lastJSONLine(out []byte) []byte {
	text := strings.TrimSpace(string(out))
	if i := strings.LastIndex(text, "\n{"); i >= 0 && !json.Valid([]byte(text)) {
		text = text[i+1:]
	}
	return []byte(text)
}
