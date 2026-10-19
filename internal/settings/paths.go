package settings

import (
	"path/filepath"
	"runtime"
)

// Path 回傳設定的寫入路徑。Linux 的標準 zuin 資料目錄改用同層的 0c；
// 指定其他個人詞庫目錄時，仍使用個人詞庫旁的設定，避免隔離測試或自訂設定互相影響。
func Path(userStore string) string {
	dir := filepath.Dir(userStore)
	if linuxDataDir(dir, "zuin") {
		dir = filepath.Join(filepath.Dir(dir), "0c")
	}
	return filepath.Join(dir, FileName)
}

func linuxDataDir(dir, name string) bool {
	return runtime.GOOS == "linux" && filepath.IsAbs(dir) && filepath.Base(dir) == name &&
		filepath.Base(filepath.Dir(dir)) == "share" &&
		filepath.Base(filepath.Dir(filepath.Dir(dir))) == ".local"
}

// legacyPath 只為 Linux 的標準新設定位置提供舊路徑，不改變自訂檔名或其他平台的讀取。
func legacyPath(path string) string {
	dir := filepath.Dir(path)
	if filepath.Base(path) == FileName && linuxDataDir(dir, "0c") {
		return filepath.Join(filepath.Dir(dir), "zuin", FileName)
	}
	return ""
}
