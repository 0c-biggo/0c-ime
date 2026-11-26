package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/update"
)

func validUpdateTarget(version, checksum string) bool {
	_, err := hex.DecodeString(checksum)
	return update.ValidVersion(version) && len(checksum) == 64 && strings.ToLower(checksum) == checksum && err == nil
}

func downloadNowCommand(args []string) (resultErr error) {
	jsonRequested := false
	for _, arg := range args {
		if isJSONFlag(arg) {
			jsonRequested = true
		}
	}
	defer func() {
		if jsonRequested && resultErr != nil && resultErr != errReported {
			resultErr = failJSON(os.Stdout, resultErr)
		}
	}()
	flags := flag.NewFlagSet("download-now", flag.ContinueOnError)
	asJSON := flags.Bool("json", false, "以 JSON 回報下載結果")
	version := flags.String("target", "", "畫面所顯示的待更新版本")
	checksum := flags.String("target-sha256", "", "畫面所顯示的資產 SHA-256")
	report := flags.Bool("notify", false, "完成後以通知回報（輸入法選單、通知的「立即下載」）：下載好時送「立即安裝」的通知")
	if err := flags.Parse(args); err != nil {
		return err
	}
	// 輸入法選單的「立即下載」（#340，手動更新）只帶 -notify、不帶版本：下載目前的待辦版本（選單第一列顯示的那一版）。
	// 只下載與核對，不安裝，所以不必像「立即安裝」那樣綁定通知當時的資產；安裝仍要另按、並綁定版本。
	menu := *report && !*asJSON && *version == "" && *checksum == ""
	if flags.NArg() != 0 || !menu && !validUpdateTarget(*version, *checksum) {
		return usageError("download-now 需要有效的 -target 與 -target-sha256")
	}
	base := basePath()
	feed := DefaultFeed
	if s, err := autoupdate.Load(base); err == nil {
		if s.Feed != "" {
			feed = s.Feed
		}
		if menu && s.Target != nil {
			*version, *checksum = s.Target.Version, s.Target.SHA256
		}
	}
	if menu && !validUpdateTarget(*version, *checksum) {
		err := fmt.Errorf("目前沒有可以下載的新版本")
		notifyOutcome(nil, err)
		return err
	}
	p := &system{base: base, feed: feed}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	s, err := (&autoupdate.Coordinator{Base: base, Platform: p}).DownloadNow(ctx, *version, *checksum)
	if *report {
		notifyDownloaded(s, err)
	}
	if err != nil {
		return err
	}
	installed, _ := p.Installed()
	view := autoupdate.ViewOf(s, installed)
	view.Operation = autoupdate.ReadOperation(base, s.Target)
	if *asJSON {
		return emitJSON(os.Stdout, checkResult{Schema: contractSchema, OK: true, Update: view})
	}
	_, err = fmt.Fprintln(os.Stdout, describeUpdate(view))
	return err
}

// notifyDownloaded 回報從選單或通知按下的「立即下載」（#340）：下載好、等安裝時送附「立即安裝」的通知（綁定這一版），
// 其他結果（失敗、版本已變、已在下載）照 notifyOutcome 說明。
func notifyDownloaded(s *autoupdate.State, err error) {
	if err == nil && s != nil && s.Target != nil && s.Stage == autoupdate.StagePrepared && s.Blocked != nil && s.Blocked.Reason == "needs_consent" {
		n := autoupdate.Notice{Title: "靈犀注音有新版本", Body: s.Target.Version + " 已下載，按「立即安裝」完成更新。",
			Action: "finish", TargetVersion: s.Target.Version, TargetSHA256: s.Target.SHA256}
		if notify(n) == nil {
			_ = autoupdate.NoticeDelivered(basePath(), n, time.Now()) // 背景提醒從這一則往後排
		}
		return
	}
	notifyOutcome(s, err)
}
