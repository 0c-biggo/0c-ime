package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/0c-biggo/0c-ime/internal/atomicfile"
	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/update"
	"github.com/0c-biggo/0c-ime/internal/updatew"
)

// 只換更新器（#295）。
//
// 索引的規則可以給一台電腦 kind=updater 的資產：一份完整的 core 或 shell 套件，只從裡面取出 0c-update（Windows 另加
// 啟動器 0c-updatew），自我測試通過後換上排程用的穩定副本，其他都不動。Windows 的外殼更新要 UAC，使用者不按的話連
// 更新器的修正都拿不到（#43）；換穩定副本不需要系統管理員，所以這條路讓更新器的修正先到。macOS 的更新器在已簽章的 App
// 裡，不能單獨換。
//
// 這一步只由背景排程的 auto 在協調器之後做（下載可能很大，不讓選單或設定程式的「檢查更新」等它），不影響協調器的
// 待辦與階段。同一份資產失敗後依結果冷卻，不每 15 分鐘重新下載。

// updaterAssetSupported：這個平台能單獨換更新器。
var updaterAssetSupported = len(update.UpdaterFiles(runtime.GOOS)) > 0

// stableFromUpdaterAsset 是穩定副本來自「只換更新器」時的來源標記。
const stableFromUpdaterAsset = "updater"

// 冷卻：沒通過自我測試的候選由 retryPendingUpdater 依自我測試的冷卻再試，不重新下載；下載、核對或換檔失敗一天後再試。
const (
	updaterRetryNotQualified = 72 * time.Hour
	updaterRetryFailed       = 24 * time.Hour
)

// stableMarker 是 update/stable-updater.json：最近一次由這一版以後的更新器換上的穩定副本是哪個版本、從哪裡來。
type stableMarker struct {
	Schema  int       `json:"schema"`
	Version string    `json:"version,omitempty"`
	SHA256  string    `json:"sha256"`
	Source  string    `json:"source,omitempty"` // updater：只換更新器裝上的
	At      time.Time `json:"at"`
}

func stableMarkerPath(base string) string {
	return filepath.Join(base, "update", "stable-updater.json")
}

func writeStableMarker(base, version, sha, source string) {
	if err := os.MkdirAll(filepath.Dir(stableMarkerPath(base)), 0o700); err != nil {
		return
	}
	_ = atomicfile.WriteJSON(stableMarkerPath(base), stableMarker{Schema: 1, Version: version, SHA256: sha, Source: source, At: time.Now().UTC()})
}

func readStableMarker(base string) *stableMarker {
	b, err := os.ReadFile(stableMarkerPath(base))
	if err != nil {
		return nil
	}
	var m stableMarker
	if json.Unmarshal(b, &m) != nil || m.Schema != 1 {
		return nil
	}
	return &m
}

// stableIsNewerAsset：目前的穩定副本（雜湊 have）是只換更新器裝上的、比 version 新的版本，而且啟動器沒判定它異常。
// 這時修復、核心更新與完整安裝都不把它換回較舊的更新器（舊版的修正就是它要帶來的）。
func stableIsNewerAsset(base, have, version string) bool {
	m := readStableMarker(base)
	if m == nil || m.Source != stableFromUpdaterAsset || m.SHA256 != have || updatew.Failing(base, have) {
		return false
	}
	order, err := update.CompareVersion(m.Version, version)
	return err == nil && order > 0
}

// applyUpdaterOffer 處理協調器記下的「只換更新器」資產；背景排程的 auto 在協調器之後呼叫。
func applyUpdaterOffer(base string) {
	s, err := autoupdate.Load(base)
	if err != nil || s.UpdaterOffer == nil || !updaterAssetSupported || !update.ValidVersion(buildVersion) {
		return
	}
	o := *s.UpdaterOffer
	if order, err := update.CompareVersion(o.Version, buildVersion); err != nil || order <= 0 {
		return // 執行中的就是這一版或更新的
	}
	if m := readStableMarker(base); m != nil && m.Version == o.Version {
		if have, err := update.Hash(stableUpdater(base)); err == nil && have == m.SHA256 {
			return // 穩定副本已經是這一版（這次執行的是上一版，例如啟動器退回中）
		}
	}
	now := time.Now()
	if t := s.UpdaterTried; t != nil && t.SHA256 == o.SHA256 && t.Version == o.Version && !now.Before(t.At) {
		wait := map[string]time.Duration{"not_qualified": updaterRetryNotQualified, "failed": updaterRetryFailed}[t.Result]
		if t.Result == "installed" || now.Sub(t.At) < wait {
			return
		}
	}
	result, detail := installUpdaterAsset(base, s, o)
	fmt.Fprintf(os.Stderr, "0c-update: 只換更新器 %s：%s %s\n", o.Version, result, detail)
	_, _ = autoupdate.EditState(base, func(x *autoupdate.State) error {
		x.UpdaterTried = &autoupdate.UpdaterAttempt{Version: o.Version, SHA256: o.SHA256, Result: result, Detail: detail, At: now.UTC()}
		x.Record("updater_"+result, o.Version+" "+detail)
		return nil
	})
}

// downloadUpdaterArchive 下載只換更新器的套件；測試替換。
var downloadUpdaterArchive = func(ctx context.Context, o autoupdate.Target, dir string) (string, error) {
	goos, arch := runtime.GOOS, runtime.GOARCH
	return update.DownloadWithOptions(ctx, update.Client(), update.Asset{OS: goos, Arch: arch, URL: o.URL, SHA256: o.SHA256, Size: o.Size}, dir,
		update.DownloadOptions{BytesPerSecond: update.BackgroundBytesPerSecond})
}

func updaterStage(base, sha string) string {
	return filepath.Join(base, "update", "stage", "updater-"+sha[:12])
}

// installUpdaterAsset 下載（或沿用協調器已下載的同一份套件）、只取出更新器、核對簽章者、自我測試後換上。
func installUpdaterAsset(base string, s *autoupdate.State, o autoupdate.Target) (string, string) {
	archive := ""
	if s.Download != "" && s.Target != nil && s.Target.SHA256 == o.SHA256 && regularFile(s.Download) {
		archive = s.Download // 外殼更新等 UAC 時，套件已經下載好了
	} else {
		dir := filepath.Join(base, "update", "updater-download")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		defer os.RemoveAll(dir)
		var err error
		if archive, err = downloadUpdaterArchive(ctx, o, dir); err != nil {
			return "failed", "下載：" + err.Error()
		}
	}
	stage := updaterStage(base, o.SHA256)
	os.RemoveAll(stage)
	_, files, err := update.ExtractUpdater(archive, o.SHA256, runtime.GOOS, runtime.GOARCH, o.Version, stage)
	if err != nil {
		os.RemoveAll(stage)
		return "failed", "取出更新器：" + err.Error()
	}
	if err := verifyPublisher(files[0]); err != nil {
		os.RemoveAll(stage)
		return "failed", err.Error()
	}
	switch err := adoptStable(files[0], o.Version, base, stableFromUpdaterAsset); {
	case errors.Is(err, errNotQualified):
		return "not_qualified", err.Error() // 候選留在 stage，retryPendingUpdater 冷卻後再試
	case err != nil:
		os.RemoveAll(stage)
		return "failed", "換上：" + err.Error()
	}
	finishUpdaterAsset(base, files[0])
	return "installed", ""
}

// finishUpdaterAsset：只換更新器的候選換上之後，一併換上同一份套件附的啟動器，並清掉暫存。
func finishUpdaterAsset(base, updater string) {
	if err := installLauncherBeside(updater, stableLauncher(base)); err != nil {
		fmt.Fprintln(os.Stderr, "0c-update: 排程用的啟動器未更新：", err)
	}
	removeUpdaterStage(base, updater)
}

// removeUpdaterStage 只刪 update/stage/updater-* 底下的暫存，不碰其他位置的候選（核心目錄、版本目錄）。
func removeUpdaterStage(base, path string) {
	stage := filepath.Join(base, "update", "stage")
	for dir := filepath.Dir(path); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if filepath.Dir(dir) == stage {
			if strings.HasPrefix(filepath.Base(dir), "updater-") {
				os.RemoveAll(dir)
			}
			return
		}
	}
}

// ShellVersion／UpdaterVersion：依版本規則選資產用（autoupdate.RuleInputs）。
func (p *system) ShellVersion() string {
	v, _ := installedVersion()
	return v
}

func (p *system) UpdaterVersion() string {
	if update.ValidVersion(buildVersion) {
		return buildVersion
	}
	return ""
}
