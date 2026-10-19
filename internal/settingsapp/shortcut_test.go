package settingsapp

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testSpec(t *testing.T) (string, ShortcutSpec) {
	t.Helper()
	root := t.TempDir()
	base := filepath.Join(root, "Local", "Zuin")
	if err := os.MkdirAll(filepath.Join(base, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	spec, err := NewShortcutSpec(filepath.Join(root, "StartMenu"), base, filepath.Join(root, "conhost.exe"), filepath.Join(base, "bin", "0c-update.exe"))
	if err != nil {
		t.Fatal(err)
	}
	return base, spec
}

// 假的 .lnk：記下被要求建立幾次，並真的寫出一個檔案讓「捷徑還在」成立。
type creator struct{ calls int }

func (c *creator) create(spec ShortcutSpec) error {
	c.calls++
	return os.WriteFile(spec.Path, []byte("lnk"), 0o644)
}

func TestShortcutRunsTheStableUpdaterHidden(t *testing.T) {
	_, spec := testSpec(t)
	if !strings.HasSuffix(spec.Path, filepath.Join("StartMenu", ShortcutFolder, ShortcutName)) {
		t.Fatal(spec.Path)
	}
	if want := `--headless "` + filepath.Join(filepath.Dir(spec.Icon), "0c-update.exe") + `" open-settings`; spec.Args != want {
		t.Fatalf("%q != %q", spec.Args, want)
	}
	if filepath.Base(spec.Target) != "conhost.exe" {
		t.Fatal("主控台程式要靠 conhost --headless，不然每次都閃出黑視窗")
	}
	for _, bad := range []string{"relative\\0c-update.exe", `C:\x"y\0c-update.exe`} {
		if _, err := NewShortcutSpec(t.TempDir(), t.TempDir(), t.TempDir(), bad); err == nil {
			t.Errorf("應拒絕 %q", bad)
		}
	}
}

func TestEnsureShortcutIsIdempotentAndRepairsWhatWentMissing(t *testing.T) {
	base, spec := testSpec(t)
	c := &creator{}
	changed, err := EnsureShortcut(base, spec, c.create)
	if err != nil || !changed || c.calls != 1 {
		t.Fatalf("第一次要建立：%v %v %d", changed, err, c.calls)
	}
	if got, _ := os.ReadFile(spec.Icon); !bytes.Equal(got, ico) {
		t.Fatal("圖示沒有寫到個人資料目錄")
	}
	if changed, err = EnsureShortcut(base, spec, c.create); err != nil || changed || c.calls != 1 {
		t.Fatalf("內容沒變不該重建：%v %v %d", changed, err, c.calls)
	}
	os.Remove(spec.Path) // 使用者把捷徑刪了：補回來
	if changed, err = EnsureShortcut(base, spec, c.create); err != nil || !changed || c.calls != 2 {
		t.Fatalf("捷徑被刪要補回：%v %v %d", changed, err, c.calls)
	}
	os.Remove(spec.Icon)
	if changed, _ = EnsureShortcut(base, spec, c.create); !changed || c.calls != 3 {
		t.Fatal("圖示被刪也要補回")
	}
	moved := spec
	moved.Args += " --x"
	if changed, _ = EnsureShortcut(base, moved, c.create); !changed || c.calls != 4 {
		t.Fatal("內容改了要更新")
	}
	failing := func(ShortcutSpec) error { return errors.New("COM 失敗") }
	os.Remove(spec.Path)
	if _, err = EnsureShortcut(base, spec, failing); err == nil {
		t.Fatal("建立失敗要回報")
	}
	if b, _ := os.ReadFile(filepath.Join(base, ShortcutMarker)); !strings.Contains(string(b), "--x") {
		t.Fatal("建立失敗時不可更新標記檔，否則下次會以為已經建好")
	}
}

func TestShortcutRenameKeepsOldEntryUntilReplacementSucceeds(t *testing.T) {
	base, spec := testSpec(t)
	legacy := spec
	legacy.Path = filepath.Join(filepath.Dir(spec.Path), "靈犀注音設定.lnk")
	c := &creator{}
	if _, err := EnsureShortcut(base, legacy, c.create); err != nil {
		t.Fatal(err)
	}
	fail := func(ShortcutSpec) error { return errors.New("COM 失敗") }
	if _, err := EnsureShortcut(base, spec, fail); err == nil {
		t.Fatal("應回報建立失敗")
	}
	if !exists(legacy.Path) {
		t.Fatal("建立失敗時舊入口必須保留")
	}
	old, err := readMarker(filepath.Join(base, ShortcutMarker))
	if err != nil || old != legacy {
		t.Fatal("建立失敗時舊標記必須保留")
	}
	if _, err := EnsureShortcut(base, spec, c.create); err != nil {
		t.Fatal(err)
	}
	if exists(legacy.Path) || !exists(spec.Path) || !strings.Contains(filepath.Base(spec.Path), "0c") {
		t.Fatal("成功後應只有可用 0c 搜尋的新入口")
	}
	if err := RemoveShortcut(base); err != nil || exists(spec.Path) {
		t.Fatal("移除必須能清除新入口")
	}
}

func TestRemoveShortcutOnlyTouchesWhatWeCreated(t *testing.T) {
	base, spec := testSpec(t)
	// 使用者自己建立的同名捷徑，沒有標記檔：不碰。
	os.MkdirAll(filepath.Dir(spec.Path), 0o755)
	os.WriteFile(spec.Path, []byte("user's own"), 0o644)
	if err := RemoveShortcut(base); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(spec.Path); err != nil {
		t.Fatal("沒有標記檔就不該刪捷徑")
	}
	os.Remove(spec.Path)

	c := &creator{}
	if _, err := EnsureShortcut(base, spec, c.create); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(filepath.Dir(spec.Path), "使用說明.lnk"), []byte("x"), 0o644) // 安裝精靈的其他項目
	if err := RemoveShortcut(base); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(spec.Path); !os.IsNotExist(err) {
		t.Fatal("應移除自己建立的捷徑")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(spec.Path), "使用說明.lnk")); err != nil {
		t.Fatal("同資料夾的其他項目要保留")
	}
	if _, err := os.Stat(filepath.Join(base, ShortcutMarker)); !os.IsNotExist(err) {
		t.Fatal("標記檔應一併移除")
	}
	if err := RemoveShortcut(base); err != nil {
		t.Fatal("重複移除不是錯誤")
	}
}

func TestEmbeddedIconIsTheOneTheSettingsProgramShips(t *testing.T) {
	want, err := os.ReadFile("../../platforms/settings/icons/icon.ico")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ico, want) {
		t.Fatal("internal/settingsapp/icon.ico 與 platforms/settings/icons/icon.ico 不一致")
	}
}

func TestWebView2VersionsFromTheRegistry(t *testing.T) {
	for versions, want := range map[string]bool{"": false, "0.0.0.0": false, "130.0.2849.80": true} {
		if got := webView2Present("", versions, ""); got != want {
			t.Errorf("%q → %v", versions, got)
		}
	}
	if webView2Present() || !webView2Present("0.0.0.0", "1.0") {
		t.Fatal("任何一處有版本就算已安裝")
	}
}
