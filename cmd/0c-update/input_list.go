package main

import (
	"os"
	"path/filepath"
)

// inputTip 是靈犀注音在 Windows 語言清單裡的識別：語言（zh-TW）、TSF 的 CLSID 與語言 profile，
// 與 platforms/windows/tsf/src/guids.rs 的 LANGID、CLSID_ZUIN、GUID_PROFILE 相同。
const inputTip = "0404:{138C35D0-7F08-4CEF-BA90-C34C16301528}{EF62F743-9855-4C94-A658-1F0F98B7B840}"

// inputListMarker 記下已替這個帳號把靈犀注音加入過語言清單（#191）。
func inputListMarker(base string) string { return filepath.Join(base, "update", "input-list") }

// listInputMethodOnce 第一次安裝（或從還沒有這一步的舊版更新）時把靈犀注音加入這個帳號的輸入法清單，
// 之後不再加：使用者自己從清單移除的，更新不會再放回去。add 失敗時不留記號，下次安裝或更新再試。
func listInputMethodOnce(base string, add func() error) error {
	marker := inputListMarker(base)
	if _, err := os.Stat(marker); err == nil {
		return nil
	}
	if err := add(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		return err
	}
	return os.WriteFile(marker, []byte(inputTip+"\n"), 0o600)
}
