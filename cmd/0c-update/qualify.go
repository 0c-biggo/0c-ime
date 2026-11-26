package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/0c-biggo/0c-ime/internal/atomicfile"
	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/update"
)

// 新更新器先自我測試才換上穩定副本（#293）。
//
// 穩定副本（bin/0c-update，排程、通知、選單執行的那一份）是背景更新唯一的入口。0.2.12 起 Windows 換它時把舊的留成
// 0c-update.prev.exe，啟動器在新版連續異常時退回；但那是出事之後的補救。這裡在換之前先擋：候選的 0c-update 以自己
// 執行 selftest（讀狀態、驗線上索引簽章、試算下一步），通過才換上；沒通過就保留現有的穩定副本並記錄，之後再試。
//
// 結果記在 <個人資料目錄>/update/updater-selftest.json：同一份候選檔（SHA-256）通過後 24 小時內不重跑；
// 失敗 72 小時內不重試（索引一時出錯也不會讓這一版永遠換不上）；環境問題（連不上）1 小時後再試。

const (
	qualifyFile       = "updater-selftest.json"
	qualifyPassTTL    = 24 * time.Hour
	qualifyFailRetry  = 72 * time.Hour
	qualifyRetryAfter = time.Hour
	qualifyTimeout    = 2 * time.Minute
	qualifyKeep       = 10
)

type qualifyResult struct {
	SHA256  string    `json:"sha256"`
	Archive string    `json:"archive,omitempty"` // 候選來自外殼更新時，套件的 SHA-256（還沒解開就能查冷卻）
	Version string    `json:"version,omitempty"`
	Result  string    `json:"result"`
	Detail  string    `json:"detail,omitempty"`
	At      time.Time `json:"at"`
}

// qualifyPending 是還沒換上的候選：記住來源，之後的背景喚起再試（retryPendingUpdater）。
type qualifyPending struct {
	Path    string    `json:"path"`
	Version string    `json:"version,omitempty"`
	SHA256  string    `json:"sha256"`
	Since   time.Time `json:"since"`
	// Source 是 updater 時，候選來自「只換更新器」的資產（#295）：換上後另外記下，修復與核心更新不把它換回舊版。
	Source string `json:"source,omitempty"`
}

type qualifyRecord struct {
	Schema  int             `json:"schema"`
	Results []qualifyResult `json:"results,omitempty"`
	Pending *qualifyPending `json:"pending,omitempty"`
}

func qualifyPath(base string) string { return filepath.Join(base, "update", qualifyFile) }

func loadQualify(base string) *qualifyRecord {
	r := &qualifyRecord{Schema: 1}
	if b, err := os.ReadFile(qualifyPath(base)); err == nil {
		var got qualifyRecord
		if json.Unmarshal(b, &got) == nil && got.Schema == 1 {
			r = &got
		}
	}
	return r
}

func (r *qualifyRecord) save(base string) {
	if err := os.MkdirAll(filepath.Dir(qualifyPath(base)), 0o700); err != nil {
		return
	}
	_ = atomicfile.WriteJSON(qualifyPath(base), r)
}

func (r *qualifyRecord) cached(sha string, now time.Time) (qualifyResult, bool) {
	for i := len(r.Results) - 1; i >= 0; i-- {
		x := r.Results[i]
		if x.SHA256 != sha {
			continue
		}
		age := now.Sub(x.At)
		switch {
		case age < 0:
			return x, false // 時鐘往回跳：重跑
		case x.Result == selftestPass && age < qualifyPassTTL,
			x.Result == selftestFail && age < qualifyFailRetry,
			x.Result == selftestInconclusive && age < qualifyRetryAfter:
			return x, true
		}
		return x, false
	}
	return qualifyResult{}, false
}

func (r *qualifyRecord) add(x qualifyResult) {
	r.Results = append(r.Results, x)
	if len(r.Results) > qualifyKeep {
		r.Results = r.Results[len(r.Results)-qualifyKeep:]
	}
}

// runCandidateSelftest 執行候選更新器的 selftest -json；測試替換。
var runCandidateSelftest = func(path string) (string, string) {
	ctx, cancel := context.WithTimeout(context.Background(), qualifyTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "selftest", "-json")
	hideWindow(cmd)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	code := 0
	var exitErr *exec.ExitError
	switch {
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
		err = nil
	case err != nil:
		return parseSelftest(nil, -1, err)
	}
	if ctx.Err() != nil {
		return selftestInconclusive, "自我測試逾時"
	}
	return parseSelftest(out.Bytes(), code, nil)
}

// qualifyNow 是 qualify 用的時間；測試替換。
var qualifyNow = time.Now

// qualifyUpdater 判斷 candidate（版本 version，可為空）能不能換上穩定副本，回傳 pass／fail／inconclusive 與說明。
func qualifyUpdater(base, candidate, version string) (string, string) {
	return qualifyUpdaterFrom(base, candidate, version, "")
}

// parentFeedCheck：目前執行的這一份更新器能不能讀到並驗過索引（測試替換）。候選說「連不上」時拿來對照。
var parentFeedCheck = func(base string) error {
	s, err := autoupdate.Load(base)
	if err != nil {
		return err
	}
	feed := DefaultFeed
	if s.Feed != "" {
		feed = s.Feed
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_, _, err = update.ReadSignedFeed(ctx, update.Client(), feed, update.TrustedKeys(), time.Now(), s.FeedSerial)
	return err
}

// qualifyUpdaterFrom 同 qualifyUpdater；archive 是候選所在套件的 SHA-256（外殼更新），記下來讓下一次不必先解開套件。
//
// 候選回報「無法判定」（連不上、索引過期）時，以目前這一份再查一次：目前的查得到、候選卻查不到，問題在候選
// （例如新版的 TLS 或網址設定壞了），算失敗；兩邊都查不到才是環境問題。
func qualifyUpdaterFrom(base, candidate, version, archive string) (string, string) {
	sha, err := update.Hash(candidate)
	if err != nil {
		return selftestFail, "讀不到候選更新器：" + err.Error()
	}
	if version != "" && update.ValidVersion(version) {
		if order, err := update.CompareVersion(version, selftestSince); err == nil && order < 0 {
			return selftestPass, "這一版還沒有 selftest"
		}
	}
	record := loadQualify(base)
	now := qualifyNow()
	if x, ok := record.cached(sha, now); ok {
		return x.Result, x.Detail
	}
	result, detail := runCandidateSelftest(candidate)
	if result == selftestInconclusive && parentFeedCheck(base) == nil {
		result, detail = selftestFail, "目前的更新器查得到索引，新版查不到："+detail
	}
	record.add(qualifyResult{SHA256: sha, Archive: archive, Version: version, Result: result, Detail: detail, At: now})
	record.save(base)
	return result, detail
}

// stagedVerdict：外殼套件 archive 裡的新版更新器最近一次自我測試還在冷卻中的結果（不是通過）。外殼更新每次喚起都會
// 重試，先查這個，不必每 15 分鐘解開 1 GB 以上的套件，只為了得到同一個「稍後再試」。
func stagedVerdict(base, archive string) (string, string, bool) {
	record := loadQualify(base)
	now := qualifyNow()
	for i := len(record.Results) - 1; i >= 0; i-- {
		if x := record.Results[i]; x.Archive == archive && archive != "" {
			if c, ok := record.cached(x.SHA256, now); ok && c.Result != selftestPass {
				return c.Result, c.Detail, true
			}
			return "", "", false
		}
	}
	return "", "", false
}

// errNotQualified：候選更新器沒通過自我測試，穩定副本保留原本的。
var errNotQualified = errors.New("新版更新器未通過自我測試，保留原本的穩定副本")

// adoptStableUpdater 讓穩定副本換成 src（版本 version）：先自我測試（#293），通過才以 replaceStableUpdater 換上。
// 還沒有穩定副本（第一次安裝、被防毒移除）時沒有東西可保留，直接換上。沒通過時保留原本的並記下候選，之後的背景喚起
// 再試（retryPendingUpdater）；回傳 errNotQualified。穩定副本是「只換更新器」裝上的較新版本時不換回舊版（#295），
// 回傳 errStableNewer。
func adoptStableUpdater(src, version, base string) error { return adoptStable(src, version, base, "") }

// errStableNewer：穩定副本是只換更新器裝上的較新版本，不換回 src 的舊版。
var errStableNewer = errors.New("排程用的 0c-update 已是較新的版本，保留")

func adoptStable(src, version, base, source string) error {
	stable := stableUpdater(base)
	want, err := update.Hash(src)
	if err != nil {
		return err
	}
	if !regularFile(stable) || sameFile(src, stable) {
		if err := replaceStableUpdater(src, base); err != nil {
			return err
		}
		writeStableMarker(base, version, want, source)
		return nil
	}
	have, _ := update.Hash(stable)
	if have == want {
		clearPendingUpdater(base, want)
		if m := readStableMarker(base); m == nil || m.SHA256 != want {
			writeStableMarker(base, version, want, source) // 別人（例如 install.py）先換好了：補上紀錄
		}
		return replaceStableUpdater(src, base) // 內容相同：只清 .old
	}
	if source != stableFromUpdaterAsset && stableIsNewerAsset(base, have, version) {
		return errStableNewer
	}
	result, detail := qualifyUpdater(base, src, version)
	if result != selftestPass {
		record := loadQualify(base)
		if record.Pending == nil || record.Pending.SHA256 != want {
			record.Pending = &qualifyPending{Path: src, Version: version, SHA256: want, Since: qualifyNow(), Source: source}
		} else {
			record.Pending.Path, record.Pending.Source = src, source
		}
		record.save(base)
		fmt.Fprintf(os.Stderr, "0c-update: %s %s（%s：%s）\n", errNotQualified, version, result, detail)
		return fmt.Errorf("%w：%s", errNotQualified, detail)
	}
	if err := replaceStableUpdater(src, base); err != nil {
		return err
	}
	writeStableMarker(base, version, want, source)
	clearPendingUpdater(base, want)
	return nil
}

func clearPendingUpdater(base, sha string) {
	record := loadQualify(base)
	if record.Pending != nil && record.Pending.SHA256 == sha {
		record.Pending = nil
		record.save(base)
	}
}

// retryPendingUpdater：背景喚起時再試一次上次沒換上的候選（例如當時連不上索引）。候選檔不見或內容變了就放棄；
// 冷卻時間由 qualifyUpdater 的紀錄決定，不會每 15 分鐘重跑一次失敗的自我測試。
func retryPendingUpdater(base string) {
	record := loadQualify(base)
	p := record.Pending
	if p == nil {
		return
	}
	if sha, err := update.Hash(p.Path); err != nil || sha != p.SHA256 {
		record.Pending = nil
		record.save(base)
		return
	}
	if qualifyNow().Sub(p.Since) > pendingGiveUp {
		record.Pending = nil // 一個月都沒換上：放棄這份候選，等下一次更新
		record.save(base)
		removeUpdaterStage(base, p.Path)
		return
	}
	if x, ok := record.cached(p.SHA256, qualifyNow()); ok && x.Result != selftestPass {
		return
	}
	if err := adoptStable(p.Path, p.Version, base, p.Source); err == nil && p.Source == stableFromUpdaterAsset {
		finishUpdaterAsset(base, p.Path)
	}
}

// pendingGiveUp：沒換上的候選留多久。
const pendingGiveUp = 30 * 24 * time.Hour

func sameFile(a, b string) bool {
	x, err1 := os.Stat(a)
	y, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(x, y)
}
