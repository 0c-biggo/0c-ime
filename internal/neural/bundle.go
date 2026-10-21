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
	// SystemPython 是沒有內附 Python 時的退路（Linux）；空字串不退回（FindBundled、macOS、Windows）。
	// 檔案不存在時仍用這個路徑，見 Bundled。
	SystemPython string
}

// Bundled 是找到的推論環境與模型。HasSelect／HasPunct 表示可以啟動 worker（有模型、worker 與要執行的 Python）。
//
// Python 是要執行的 Python，PythonSource 是它的來源；Python 不為空但 PythonSource 為空，表示 Linux 的系統
// Python 目前不存在（沒裝 python3）。這時照樣啟動 worker：執行檔不存在是 runtime_missing，每 10 分鐘重試，
// 使用者之後裝好 python3 不必重啟引擎（與缺 numpy、onnxruntime 相同）。macOS、Windows 沒有系統 Python 的
// 退路：內附 Python 不見是安裝損毀，Python 為空、不啟動 worker，狀態與以前一樣是 not_installed。
type Bundled struct {
	Python, SelectWorker, SelectPackage, PunctWorker, PunctPackage string
	PythonSource, SelectSource, PunctSource                        string // 空字串：沒有找到
	HasSelect, HasPunct                                            bool
}

// RuntimeMissing 回報找到模型與 worker、要用的系統 Python 卻不存在（Linux 沒裝 python3）。引擎照樣啟動
// worker（HasSelect／HasPunct 為真），啟動時的狀態報 runtime_missing；之後的狀態由 worker 的重試決定。
func (b Bundled) RuntimeMissing(punct bool) bool {
	if b.Python == "" || b.PythonSource != "" {
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
	case o.SystemPython != "":
		b.Python = o.SystemPython
		if regular(o.SystemPython) {
			b.PythonSource = SourceSystem
		}
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
