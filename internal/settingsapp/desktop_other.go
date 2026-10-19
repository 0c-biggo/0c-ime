//go:build !linux

package settingsapp

// 應用程式選單的 .desktop 項目只有 Linux 用得到；其他平台由各自的捷徑機制處理。
func DataHome() string                             { return "" }
func EnsureDesktopEntry(_, _ string) (bool, error) { return false, nil }
func RemoveDesktopEntry(string) error              { return nil }
