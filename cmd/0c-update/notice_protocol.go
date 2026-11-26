package main

import (
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"

	"github.com/0c-biggo/0c-ime/internal/update"
)

// Both buttons retain the identity of the asset shown in the notification.
// download（#340，手動更新的「立即下載」）同樣綁定資產，按下時執行 download-now，只下載不安裝。
func noticeActionURL(action, version, hash string) (string, error) {
	if action != "finish" && action != "snooze" && action != "download" {
		return "", fmt.Errorf("不支援的通知動作")
	}
	_, err := hex.DecodeString(hash)
	if !update.ValidVersion(version) || len(hash) != 64 || err != nil || strings.ToLower(hash) != hash {
		return "", fmt.Errorf("通知更新目標無效")
	}
	return "zc-update:" + action + "?" + url.Values{"version": {version}, "sha256": {hash}}.Encode(), nil
}

func noticeProtocolCommand(raw string) (string, []string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "zc-update" || u.Host != "" || u.User != nil || u.Path != "" || u.Fragment != "" {
		return "", nil, fmt.Errorf("不支援的通知網址")
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q) != 2 || len(q["version"]) != 1 || len(q["sha256"]) != 1 {
		return "", nil, fmt.Errorf("通知缺少版本資訊，請從輸入法選單重新確認更新")
	}
	if _, err := noticeActionURL(u.Opaque, q.Get("version"), q.Get("sha256")); err != nil {
		return "", nil, err
	}
	args := []string{"-target", q.Get("version"), "-target-sha256", q.Get("sha256")}
	switch u.Opaque {
	case "finish":
		args = append(args, "-notify")
	case "download":
		return "download-now", append(args, "-notify"), nil
	}
	return u.Opaque, args, nil
}

// noticeButtonLabel 是通知上執行動作的按鈕文字。
func noticeButtonLabel(action string) string {
	if action == "download" {
		return "立即下載"
	}
	return "立即安裝"
}
