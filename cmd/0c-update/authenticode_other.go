//go:build !windows

package main

// verifyPublisher：Linux 的套件沒有程式碼簽章，信任來自已簽章索引與套件雜湊；macOS 的 App 簽章由安裝程式核對
// （teamID，見 platform_darwin.go）。
func verifyPublisher(string) error { return nil }
