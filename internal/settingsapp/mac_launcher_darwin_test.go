//go:build darwin

package settingsapp

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/0c-biggo/0c-ime/internal/update"
)

// 原生驗收時提供已簽署／公證的產物；一般 Go 測試不自行取憑證、連 Apple 或登記桌面入口。
func TestMacLauncherCopyPreservesNativeSignature(t *testing.T) {
	source := os.Getenv("ZC_TEST_SETTINGS_LAUNCHER")
	if source == "" {
		t.Skip("需提供已簽署的原生啟動器產物")
	}
	files := map[string]string{}
	err := filepath.WalkDir(source, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)], err = update.Hash(path)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	apps := filepath.Join(t.TempDir(), "Applications")
	if changed, err := EnsureMacLauncher(apps, source, files); err != nil || !changed {
		t.Fatalf("%v %v", changed, err)
	}
	dest := filepath.Join(apps, MacLauncherName)
	if err := VerifyBundle(dest, files); err != nil {
		t.Fatal(err)
	}
	commands := [][]string{{"codesign", "--verify", "--deep", "--strict", dest}}
	if os.Getenv("ZC_TEST_SETTINGS_LAUNCHER_NOTARIZED") == "1" {
		commands = append(commands, []string{"xcrun", "stapler", "validate", dest}, []string{"spctl", "--assess", "--type", "execute", "-vv", dest})
	}
	for _, args := range commands {
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		t.Logf("%s: %s", args[0], out)
	}
}
