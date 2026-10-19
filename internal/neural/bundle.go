package neural

import (
	"os"
	"path/filepath"
	"runtime"
)

// Bundled 是完整套件內附的神經模型：放在系統字典旁的 neural/，與該字典配對出貨。
//
//	neural/python/            可攜 Python 與 numpy、onnxruntime
//	neural/sequence_runtime.py、punct_runtime.py
//	neural/select/            選字模型（manifest.json、model.onnx、vocab.json）
//	neural/punct/             補標點模型
type Bundled struct {
	Python, SelectWorker, SelectPackage, PunctWorker, PunctPackage string
	HasSelect, HasPunct                                            bool
}

func FindBundled(dataDir string) Bundled {
	dir := filepath.Join(filepath.Dir(filepath.Clean(dataDir)), "neural")
	python := filepath.Join(dir, "python", "bin", "python3")
	if runtime.GOOS == "windows" {
		python = filepath.Join(dir, "python", "python.exe")
	}
	b := Bundled{Python: python,
		SelectWorker: filepath.Join(dir, "sequence_runtime.py"), SelectPackage: filepath.Join(dir, "select"),
		PunctWorker: filepath.Join(dir, "punct_runtime.py"), PunctPackage: filepath.Join(dir, "punct")}
	present := func(paths ...string) bool {
		for _, p := range paths {
			if st, err := os.Stat(p); err != nil || !st.Mode().IsRegular() {
				return false
			}
		}
		return true
	}
	b.HasSelect = present(python, b.SelectWorker, filepath.Join(b.SelectPackage, "manifest.json"))
	b.HasPunct = present(python, b.PunctWorker, filepath.Join(b.PunctPackage, "manifest.json"))
	return b
}

// MaxThreads 是 worker 接受的 threads 上限，須與 Python worker 的檢查一致。
const MaxThreads = 8

// DefaultThreads 用滿 CPU 到上限為止：推論只在按鍵與提交時短暫執行，閒置時不占用。
func DefaultThreads() int {
	return max(1, min(MaxThreads, runtime.NumCPU()))
}
