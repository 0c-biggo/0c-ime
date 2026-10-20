package core_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// core 必須是純邏輯：不碰作業系統、網路、cgo（docs/19）。宿主的能力經由介面注入。
// 測試檔不受限。
func TestCoreIsPure(t *testing.T) {
	forbidden := map[string]bool{"C": true, "os": true, "os/exec": true, "net": true, "net/http": true, "syscall": true, "unsafe": true}
	fset := token.NewFileSet()
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if forbidden[p] || strings.HasPrefix(p, "github.com/0c-biggo/0c-ime/") && !strings.HasPrefix(p, "github.com/0c-biggo/0c-ime/core/") {
				t.Errorf("%s 不得 import %q", path, p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
