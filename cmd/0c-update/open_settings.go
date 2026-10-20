package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/0c-biggo/0c-ime/internal/settingsapp"
)

// 設定程式不可用時的退路：命令列一樣能看、能改設定（契約見 docs/settings-contract.md）。
const settingsFallback = "可改用 0c-update settings 查看、0c-update set 修改設定。"

// settingsOpener 的欄位都可以替換，測試才不需要真實的桌面、函式庫檢查與通知。
type settingsOpener struct {
	locate  func() (settingsapp.Location, error)
	display func() bool
	missing func(path string) []string
	launch  func(path string, o settingsapp.LaunchOptions) error
	notify  func(title, body string) // 從選單或捷徑啟動時看不到 stderr，所以失敗要以通知說明
	entry   func()                   // 確保應用程式選單的項目
	hint    func(missing []string) string
	updater string // 啟動它的 0c-update：設定程式用同一個更新器讀契約，版本必定一致
	log     string
	wait    time.Duration
}

// openSettings 實作 0c-update open-settings [頁面]：外殼的選單、捷徑與通知共用的穩定入口。
func openSettings(args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("用法：0c-update open-settings [%s]", strings.Join(settingsapp.Pages, "|"))
	}
	page := ""
	if len(args) == 1 {
		page = args[0]
	}
	// 設定程式「我的詞彙」頁顯示 0c-mcp 的路徑與它在不在；先讓穩定副本跟上目前版本（Windows）。
	refreshStableMCP(basePath())
	return newSettingsOpener().open(settingsapp.Page(page))
}

// open 找出並核對設定程式、確認環境，然後以脫離的方式啟動；啟動後立刻返回。
func (o settingsOpener) open(page string) error {
	o.entry()
	loc, err := o.locate()
	switch {
	case errors.Is(err, settingsapp.ErrNotFound):
		return o.fail("這個安裝沒有附設定程式。" + settingsFallback)
	case err != nil:
		return o.fail(fmt.Sprintf("設定程式無法通過核對，不啟動它（%v）。重新安裝可以修復。%s", err, settingsFallback))
	}
	if !o.display() {
		return o.fail("找不到圖形桌面（沒有 WAYLAND_DISPLAY 或 DISPLAY）。" + settingsFallback)
	}
	if libs := o.missing(loc.Path); len(libs) > 0 {
		return o.fail(fmt.Sprintf("設定程式需要 %s，這台電腦找不到。%s%s", strings.Join(libs, "、"), o.hint(libs), settingsFallback))
	}
	err = o.launch(loc.Path, settingsapp.LaunchOptions{Page: page, Updater: o.updater, Log: o.log, Wait: o.wait})
	if err != nil {
		return o.fail(fmt.Sprintf("設定程式沒有啟動：%v。%s", err, settingsFallback))
	}
	return nil
}

func (o settingsOpener) fail(message string) error {
	o.notify("靈犀注音設定無法開啟", message)
	return errors.New(message)
}
