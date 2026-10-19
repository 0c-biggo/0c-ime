package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"time"

	"git.biggo.com/0c/ime/internal/autoupdate"
	"git.biggo.com/0c/ime/internal/update"
)

// inputSetupFollows：安裝後由 0c-update 設定輸入法框架（一行安裝），Linux 安裝器不另外提示下一步。
var inputSetupFollows bool

// installOnline 是一行安裝（#19）的後半段：讀已簽章索引、下載並核對本平台套件，完整安裝後建立
// 自動更新排程並設定輸入法框架。已安裝相同或較新版本時不重裝，只修復排程與輸入法設定。
func installOnline(feed, channel string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	f, _, err := update.ReadSignedFeed(ctx, update.Client(), feed, update.TrustedKeys(), time.Now(), 0)
	if err != nil {
		return fmt.Errorf("讀取更新索引：%w", err)
	}
	asset, err := f.Select(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return fmt.Errorf("靈犀注音目前沒有 %s/%s 版本", runtime.GOOS, runtime.GOARCH)
	}
	if feed != DefaultFeed {
		if err = setFeed(basePath(), feed); err != nil {
			return err
		}
	}
	current, err := installedVersion()
	if err != nil {
		return err
	}
	if current != "" {
		if order, err := update.CompareVersion(f.Version, current); err == nil && order <= 0 {
			fmt.Printf("已安裝靈犀注音 %s（索引最新為 %s），不重新安裝。\n", current, f.Version)
			afterInstall()
			return setupInputMethod(basePath())
		}
	}
	fmt.Printf("下載靈犀注音 %s（%.0f MB）…\n", f.Version, float64(asset.Size)/(1<<20))
	dir, err := os.MkdirTemp(scratchRoot(), "0c-install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	archive, err := update.Download(ctx, update.Client(), asset.Asset, dir)
	if err != nil {
		return fmt.Errorf("下載：%w", err)
	}
	root, cleanup, err := update.StageIn(scratchRoot(), archive, asset.SHA256, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	defer cleanup()
	r, err := update.Verify(root, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	if r.Version != f.Version {
		return fmt.Errorf("套件版本 %s 與索引 %s 不符", r.Version, f.Version)
	}
	if err = canInstall(r, channel, false); err != nil {
		return err
	}
	inputSetupFollows = true
	if err = install(root, r); err != nil {
		return err
	}
	afterInstall()
	forgetInstalledTarget(r.Version)
	return setupInputMethod(basePath())
}

// markProfileAtLogin 記下「下次登入、桌面啟動前把靈犀注音加入輸入法清單」。
func markProfileAtLogin(base string) error {
	s, err := autoupdate.Load(base)
	if err != nil {
		return err
	}
	s.ProfileAtLogin = true
	return autoupdate.Save(base, s)
}
