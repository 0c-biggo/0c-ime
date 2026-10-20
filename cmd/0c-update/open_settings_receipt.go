package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/0c-biggo/0c-ime/internal/settingsapp"
	"github.com/0c-biggo/0c-ime/internal/update"
)

// settingsAppPrefix 是設定程式在輸入法 App 內的位置。整個 0c.app 是一個版本單位，所以它的每個檔案
// 都在安裝收據裡，隨輸入法一起安裝、備份、回復。
const settingsAppPrefix = "Contents/Helpers/0c-settings.app"
const settingsLauncherPrefix = "Contents/Helpers/0c-settings-launcher.app"

// macReceipt 只讀這裡需要的欄位；收據由安裝器寫入。
type macReceipt struct {
	App    string            `json:"app"`
	Files  map[string]string `json:"files"`
	Active *bool             `json:"active"`
}

// settingsAppFromReceipt 依安裝收據回傳目前安裝的輸入法 App 裡設定程式的位置，以及收據記錄的檔案雜湊
// （路徑相對設定程式）。沒有收據、收據已停用（解除安裝）或損毀時回傳空字串。
func settingsAppFromReceipt(receiptFile, inputMethods string) (string, map[string]string) {
	r, err := readMacSettingsReceipt(receiptFile)
	if err != nil {
		return "", nil
	}
	return filepath.Join(inputMethods, r.App, filepath.FromSlash(settingsAppPrefix)), settingsapp.BundleFiles(r.Files, settingsAppPrefix)
}

func readMacSettingsReceipt(receiptFile string) (macReceipt, error) {
	b, err := os.ReadFile(receiptFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return macReceipt{}, settingsapp.ErrNotFound
		}
		return macReceipt{}, err
	}
	var r macReceipt
	if err := json.Unmarshal(b, &r); err != nil {
		return r, err
	}
	if r.Active != nil && !*r.Active {
		return r, settingsapp.ErrNotFound
	}
	if r.App == "" {
		r.App = update.LegacyMacApp // 0.1.0-dev.21 以前的收據
	}
	if r.App != update.MacApp && r.App != update.LegacyMacApp {
		return r, fmt.Errorf("收據的 App 名稱無效：%q", r.App)
	}
	return r, nil
}
