//go:build !linux && !windows

package settingsapp

// 非 Linux 的設定程式用系統內建的 WebView，沒有需要預先檢查的共用函式庫。
func MissingLibraries(string) []string { return nil }

func DisplayAvailable() bool { return true }
