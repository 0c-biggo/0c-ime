package neural

import (
	"os"
	"path/filepath"
	"runtime"
)

// 模型與推論環境的來源，health 的 selection_source／punctuation_source、neural_python 回報這些值。
const (
	SourceBundled = "bundled" // 安裝包內附（官網安裝包；發行版另外裝的模型套件也放在這裡）
	SourceUser    = "user"    // 個人目錄（#180 下載後在本機配對）
	SourceSystem  = "system"  // 系統的 python3（Linux 系統安裝，#181）
)

// SystemPython 是沒有內附 Python 時在 Linux 使用的系統 Python（Debian 的 python3；numpy、onnxruntime
// 由 python3-numpy、python3-onnxruntime 提供）。macOS、Windows 的官網安裝包一定內附 Python，不退回系統。
func SystemPython() string {
	if runtime.GOOS == "linux" {
		return "/usr/bin/python3"
	}
	return ""
}

// Options 是找神經推論三樣東西（Python、worker、模型）的位置。
//
//	<data>/../neural/python/             內附 Python 與 numpy、onnxruntime（官網安裝包）
//	<data>/../neural/*_runtime.py        worker（系統安裝即 /usr/share/0c/neural/）
//	<data>/../neural/select|punct/       安裝包內建的模型，與該字典配對出貨
//	<UserDir>/models/<字典 manifest SHA-256>/select|punct/   個人目錄的模型（#180）
//
// 模型依「安裝包內建 → 個人目錄」的順序取第一個；都沒有時由統計引擎選字。個人目錄以字典 manifest 的
// 雜湊分開：worker 啟動時本來就核對這個雜湊（DictionarySHA256），換了字典的舊配對不會被找到。
type Options struct {
	DataDir string
	// UserDir 是個人資料目錄（~/.local/share/zuin）；空字串或 DictionarySHA256 為空時不找個人目錄。
	UserDir          string
	DictionarySHA256 string
	// SystemPython 是沒有內附 Python 時的退路；空字串不退回（FindBundled 的行為）。
	SystemPython string
}

// Bundled 是找到的推論環境與模型。HasSelect／HasPunct 表示三樣都在、可以啟動 worker；
// 找到模型但沒有 Python（RuntimeMissing）時兩者為 false，由呼叫端回報 runtime_missing。
type Bundled struct {
	Python, SelectWorker, SelectPackage, PunctWorker, PunctPackage string
	PythonSource, SelectSource, PunctSource                        string // 空字串：沒有找到
	HasSelect, HasPunct                                            bool
}

// RuntimeMissing 回報找到模型與 worker、卻沒有可用的 Python。
func (b Bundled) RuntimeMissing(punct bool) bool {
	if b.Python != "" {
		return false
	}
	if punct {
		return b.PunctSource != ""
	}
	return b.SelectSource != ""
}

// FindBundled 只找安裝包內附的 Python 與模型（官網安裝包的布局）。
func FindBundled(dataDir string) Bundled { return Find(Options{DataDir: dataDir}) }

// Find 分開找 Python、worker 與模型；見 Options。
func Find(o Options) Bundled {
	dir := filepath.Join(filepath.Dir(filepath.Clean(o.DataDir)), "neural")
	python := filepath.Join(dir, "python", "bin", "python3")
	if runtime.GOOS == "windows" {
		python = filepath.Join(dir, "python", "python.exe")
	}
	var b Bundled
	switch {
	case regular(python):
		b.Python, b.PythonSource = python, SourceBundled
	case o.SystemPython != "" && regular(o.SystemPython):
		b.Python, b.PythonSource = o.SystemPython, SourceSystem
	}
	var user string
	if o.UserDir != "" && len(o.DictionarySHA256) == 64 {
		user = filepath.Join(o.UserDir, "models", o.DictionarySHA256)
	}
	find := func(worker, name string) (string, string, string) {
		script := filepath.Join(dir, worker)
		if !regular(script) {
			return script, filepath.Join(dir, name), ""
		}
		if bundled := filepath.Join(dir, name); regular(filepath.Join(bundled, "manifest.json")) {
			return script, bundled, SourceBundled
		}
		if user != "" {
			if p := filepath.Join(user, name); regular(filepath.Join(p, "manifest.json")) {
				return script, p, SourceUser
			}
		}
		return script, filepath.Join(dir, name), ""
	}
	b.SelectWorker, b.SelectPackage, b.SelectSource = find("sequence_runtime.py", "select")
	b.PunctWorker, b.PunctPackage, b.PunctSource = find("punct_runtime.py", "punct")
	b.HasSelect = b.Python != "" && b.SelectSource != ""
	b.HasPunct = b.Python != "" && b.PunctSource != ""
	return b
}

func regular(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

// MaxThreads 是 worker 接受的 threads 上限，須與 Python worker 的檢查一致。
const MaxThreads = 8

// DefaultThreads 用滿 CPU 到上限為止：推論只在按鍵與提交時短暫執行，閒置時不占用。
func DefaultThreads() int {
	return max(1, min(MaxThreads, runtime.NumCPU()))
}
