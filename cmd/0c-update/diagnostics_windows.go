//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows/registry"
)

// shellDiagnostics 回報輸入法註冊實際指向哪裡，與這個帳號的安裝收據並列，放進 status -json -diagnostics
// 與設定程式匯出的狀態摘要。註冊與收據不一致時（#82），不必請使用者自己查登錄。
// 另附 tsf.log 整理出的最近用過的程式與判定（tsf_log.apps，#257）。
func shellDiagnostics() map[string]any {
	out := map[string]any{}
	read := func(root registry.Key, path string) string {
		key, err := registry.OpenKey(root, path, registry.QUERY_VALUE|registry.WOW64_64KEY)
		if err != nil {
			return ""
		}
		defer key.Close()
		v, _, _ := key.GetStringValue("")
		return v
	}
	out["registered_dll"] = read(registry.CLASSES_ROOT, clsid)                          // 實際生效（HKCU 優先於 HKLM）
	out["registered_dll_x86"], _ = registeredIn(registry.WOW64_32KEY)                   // 32 位元應用程式載入的那份（#112）
	out["registered_dll_user"] = read(registry.CURRENT_USER, `Software\Classes\`+clsid) // 只屬於這個帳號的註冊
	if b, err := os.ReadFile(receiptPath()); err == nil {
		var r struct {
			Version   string `json:"version"`
			Directory string `json:"directory"`
			Active    bool   `json:"active"`
		}
		if json.Unmarshal(b, &r) == nil {
			out["receipt"] = map[string]any{"version": r.Version, "directory": filepath.ToSlash(r.Directory), "active": r.Active}
		}
	}
	out["install_base"] = basePath()
	// 最近用過輸入法的程式與輸入法在各程式的判定（#257）：只有程式檔名、時間與判定種類，不含輸入內容。
	if apps := tsfAppsFromFile(filepath.Join(basePath(), "tsf.log"), time.Now()); apps != nil {
		out["tsf_log"] = apps
	}
	return out
}
