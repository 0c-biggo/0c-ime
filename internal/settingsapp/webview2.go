package settingsapp

// webView2Present 判斷登錄檔記錄的版本字串：任何一處有非空、非 0.0.0.0 的版本就算已安裝。
func webView2Present(versions ...string) bool {
	for _, v := range versions {
		if v != "" && v != "0.0.0.0" {
			return true
		}
	}
	return false
}
