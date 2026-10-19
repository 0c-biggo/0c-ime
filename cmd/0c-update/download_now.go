package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"git.biggo.com/0c/ime/internal/autoupdate"
	"git.biggo.com/0c/ime/internal/update"
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
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || !validUpdateTarget(*version, *checksum) {
		return usageError("download-now 需要有效的 -target 與 -target-sha256")
	}
	base := basePath()
	feed := DefaultFeed
	if s, err := autoupdate.Load(base); err == nil && s.Feed != "" {
		feed = s.Feed
	}
	p := &system{base: base, feed: feed}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	s, err := (&autoupdate.Coordinator{Base: base, Platform: p}).DownloadNow(ctx, *version, *checksum)
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
