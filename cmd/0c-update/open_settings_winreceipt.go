package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// installedFromReceipt 回傳 Windows 完整安裝收據記錄的檔案位置與雜湊（name 是版本目錄內的檔名，例如設定程式
// 或 0c-update）；沒有有效收據、收據已停用（解除安裝）、舊套件沒有記錄這個檔案，或目錄不是個人資料目錄內的
// 相對路徑時為空字串。收據的 directory 是 versions/<版本>-<雜湊>，完整安裝把套件的所有檔案放在那裡。
func installedFromReceipt(receiptFile, base, name string) (string, string) {
	b, err := os.ReadFile(receiptFile)
	if err != nil {
		return "", ""
	}
	var r struct {
		Directory string            `json:"directory"`
		Files     map[string]string `json:"files"`
		Active    bool              `json:"active"`
	}
	if json.Unmarshal(b, &r) != nil || !r.Active || r.Files[name] == "" || !safeVersionDirectory(r.Directory) {
		return "", ""
	}
	return filepath.Join(base, filepath.FromSlash(r.Directory), name), r.Files[name]
}

// safeVersionDirectory 只接受 versions/<名稱>：相對、正斜線、沒有磁碟代號與 ..（Windows 收據的版本目錄格式）。
func safeVersionDirectory(dir string) bool {
	rest, ok := strings.CutPrefix(dir, "versions/")
	return ok && rest != "" && !strings.ContainsAny(rest, `/\:`) && rest != "." && rest != ".."
}
