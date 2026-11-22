//go:build !windows

package autoupdate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/internal/robustio"
)

// #329：只換核心時，.core-* 暫存目錄改名成版本目錄同樣會被端點防護短暫擋下；等它放手再改名。
func TestPrepareCoreRetriesStageRenameDeniedByScanner(t *testing.T) {
	denied := 0
	t.Cleanup(robustio.SetForTest(func(from, to string) error {
		if strings.HasPrefix(filepath.Base(from), ".core-") && denied < 4 {
			denied++
			return &os.LinkError{Op: "rename", Old: from, New: to, Err: robustio.AccessDenied}
		}
		return os.Rename(from, to)
	}, nil))
	base := t.TempDir()
	archive, sum := linuxArchive(t, "0.1.0-dev.18", false)
	dir, err := PrepareCore(base, "", archive, Target{Version: "0.1.0-dev.18", Kind: "core", SHA256: sum}, "linux", "amd64", linuxCore)
	if err != nil {
		t.Fatalf("transient denial must be retried: %v", err)
	}
	if denied != 4 {
		t.Fatalf("denied %d times, want 4", denied)
	}
	if err := checkCore(filepath.Join(base, dir)); err != nil {
		t.Fatal(err)
	}
}
