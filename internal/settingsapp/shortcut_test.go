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
	spec, err := NewShortcutSpec(filepath.Join(root, "StartMenu"), base, filepath.Join(base, "bin", "0c-settings.exe"))
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

// #284：conhost --headless 啟動另一個程式的 .lnk 是惡意 LNK 的常見手法，Symantec Endpoint Protection 以
// Scr.Malcode!gen 隔離。捷徑要直接指向設定程式（GUI 程式，本來就沒有主控台視窗）的穩定副本，不帶參數。
func TestShortcutOpensTheStableSettingsProgramDirectly(t *testing.T) {
	_, spec := testSpec(t)
	if !strings.HasSuffix(spec.Path, filepath.Join("StartMenu", ShortcutFolder, ShortcutName)) {
		t.Fatal(spec.Path)
	}
	if want := filepath.Join(filepath.Dir(spec.Icon), "0c-settings.exe"); spec.Target != want {
		t.Fatalf("%q != %q", spec.Target, want)
	}
	if spec.Args != "" || spec.Dir != filepath.Dir(spec.Target) {
		t.Fatalf("不帶參數、工作目錄是穩定副本所在的目錄：%+v", spec)
	}
	for _, lolbin := range []string{"conhost", "powershell", "cmd.exe", "wscript", "cscript", "mshta", "rundll32"} {
		if strings.Contains(strings.ToLower(spec.Target+" "+spec.Args), lolbin) {
			t.Fatalf("捷徑不可經過 %s：%+v", lolbin, spec)
		}
	}
	for _, bad := range []string{"relative\\0c-settings.exe", `C:\x"y\0c-settings.exe`} {
		if _, err := NewShortcutSpec(t.TempDir(), t.TempDir(), bad); err == nil {
			t.Errorf("應拒絕 %q", bad)
		}
	}
}

// #284：舊版建立的 conhost 捷徑在更新後換成新樣式（不論它還在，或已被防毒軟體隔離），之後被移除就不再自動重建。
func TestLegacyConhostShortcutIsReplacedOnceThenWithdrawnStaysGone(t *testing.T) {
	base, spec := testSpec(t)
	legacy := spec
	legacy.Target = `C:\Windows\System32\conhost.exe`
	legacy.Args = `--headless "` + filepath.Join(base, "bin", "0c-update.exe") + `" open-settings`
	legacy.Dir = base
	c := &creator{}
	if _, err := EnsureShortcut(base, legacy, false, c.create); err != nil {
		t.Fatal(err)
	}
	os.Remove(legacy.Path) // SEP 隔離了舊樣式的捷徑
	if Withdrawn(base, spec) {
		t.Fatal("標記檔還是舊樣式，不能算新樣式被移除")
	}
	if changed, err := EnsureShortcut(base, spec, false, c.create); err != nil || !changed || c.calls != 2 {
		t.Fatalf("更新後要以新樣式建一次：%v %v %d", changed, err, c.calls)
	}
	if !Recorded(base, spec) {
		t.Fatal("標記檔要記錄新樣式")
	}
	os.Remove(spec.Path) // 新樣式又被移除（使用者刪掉或防毒軟體）
	if !Withdrawn(base, spec) {
		t.Fatal("應判定為被移除")
	}
	for range 96 { // 一天的 15 分鐘排程
		if changed, err := EnsureShortcut(base, spec, false, c.create); err != nil || changed {
			t.Fatalf("被移除的捷徑不可由排程重建：%v %v", changed, err)
		}
	}
	if c.calls != 2 {
		t.Fatalf("建立了 %d 次", c.calls)
	}
	if changed, err := EnsureShortcut(base, spec, true, c.create); err != nil || !changed || c.calls != 3 {
		t.Fatalf("重新安裝（restore）要補回：%v %v %d", changed, err, c.calls)
	}
}

func TestWithdrawShortcutKeepsTheMarker(t *testing.T) {
	base, spec := testSpec(t)
	if err := WithdrawShortcut(base); err != nil {
		t.Fatal("沒有標記檔時什麼都不做")
	}
	c := &creator{}
	if _, err := EnsureShortcut(base, spec, false, c.create); err != nil {
		t.Fatal(err)
	}
	if err := WithdrawShortcut(base); err != nil || exists(spec.Path) {
		t.Fatal("捷徑應移除")
	}
	if !Withdrawn(base, spec) {
		t.Fatal("標記檔要留著，之後才不會重建")
	}
	if err := RemoveShortcut(base); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(base, ShortcutMarker)); !os.IsNotExist(err) {
		t.Fatal("解除安裝要連標記檔一起移除，重新安裝才會再建立")
	}
}

func TestEnsureShortcutIsIdempotentAndRepairsOnlyOnRestore(t *testing.T) {
	base, spec := testSpec(t)
	c := &creator{}
	changed, err := EnsureShortcut(base, spec, false, c.create)
	if err != nil || !changed || c.calls != 1 {
		t.Fatalf("第一次要建立：%v %v %d", changed, err, c.calls)
	}
	if got, _ := os.ReadFile(spec.Icon); !bytes.Equal(got, ico) {
		t.Fatal("圖示沒有寫到個人資料目錄")
	}
	if changed, err = EnsureShortcut(base, spec, false, c.create); err != nil || changed || c.calls != 1 {
		t.Fatalf("內容沒變不該重建：%v %v %d", changed, err, c.calls)
	}
	os.Remove(spec.Path) // 使用者或防毒軟體把捷徑刪了：排程不補，重新安裝才補（#284）
	if changed, err = EnsureShortcut(base, spec, false, c.create); err != nil || changed || c.calls != 1 {
		t.Fatalf("捷徑被刪不可由排程補回：%v %v %d", changed, err, c.calls)
	}
	if changed, err = EnsureShortcut(base, spec, true, c.create); err != nil || !changed || c.calls != 2 {
		t.Fatalf("重新安裝要補回：%v %v %d", changed, err, c.calls)
	}
	os.Remove(spec.Icon)
	if changed, _ = EnsureShortcut(base, spec, false, c.create); !changed || c.calls != 3 {
		t.Fatal("捷徑還在而圖示被刪要補回")
	}
	moved := spec
	moved.Args += " --x"
	if changed, _ = EnsureShortcut(base, moved, false, c.create); !changed || c.calls != 4 {
		t.Fatal("內容改了要更新")
	}
	failing := func(ShortcutSpec) error { return errors.New("COM 失敗") }
	os.Remove(spec.Path)
	if _, err = EnsureShortcut(base, spec, false, failing); err == nil {
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
	if _, err := EnsureShortcut(base, legacy, false, c.create); err != nil {
		t.Fatal(err)
	}
	fail := func(ShortcutSpec) error { return errors.New("COM 失敗") }
	if _, err := EnsureShortcut(base, spec, false, fail); err == nil {
		t.Fatal("應回報建立失敗")
	}
	if !exists(legacy.Path) {
		t.Fatal("建立失敗時舊入口必須保留")
	}
	old, err := readMarker(filepath.Join(base, ShortcutMarker))
	if err != nil || old != legacy {
		t.Fatal("建立失敗時舊標記必須保留")
	}
	if _, err := EnsureShortcut(base, spec, false, c.create); err != nil {
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
	if _, err := EnsureShortcut(base, spec, false, c.create); err != nil {
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
