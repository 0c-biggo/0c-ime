package update

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/internal/enginedir"
)

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func macReceiptPrevious(t *testing.T, p MacPaths) string {
	t.Helper()
	r, err := readMacReceipt(p.receipt())
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimPrefix(r.Previous, "backups/")
}

// 舊版累積下來的備份：每份都是整個 App。
func oldMacBackup(t *testing.T, backups, name string) string {
	t.Helper()
	dir := filepath.Join(backups, name)
	exe := filepath.Join(dir, MacApp, "Contents/MacOS/0c")
	if err := os.MkdirAll(filepath.Dir(exe), 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(exe, []byte("old shell"), 0o700)
	os.WriteFile(filepath.Join(dir, "previous.json"), []byte("null\n"), 0o600)
	return dir
}

func quietPrune(t *testing.T) *[]string {
	t.Helper()
	var warned []string
	saved := PruneWarn
	PruneWarn = func(path string, err error) { warned = append(warned, path) }
	t.Cleanup(func() { PruneWarn = saved })
	return &warned
}

// #179：安裝、回復、解除安裝成功後只留收據 previous 指到的備份；舊版累積的一次清掉，
// 名稱不是自己格式的、一般檔案與連結都不碰，回復照常可用。
func TestMacPruneKeepsOnlyRollbackBackup(t *testing.T) {
	quietPrune(t)
	home := t.TempDir()
	p := MacPaths{InputMethods: filepath.Join(home, "Library/Input Methods"), Support: filepath.Join(home, "Library/Application Support/0c")}
	backups := filepath.Join(p.Support, "backups")
	if _, _, err := InstallMac(macBundle(t, "0.2.0"), "arm64", p); err != nil {
		t.Fatal(err)
	}
	var old []string
	for _, name := range []string{"macos-20261001-010203-11", "macos-20261002-010203-22", "macos-20261003-010203-33"} {
		old = append(old, oldMacBackup(t, backups, name))
	}
	// 不是自己命名格式的：手動放的、其他格式、一般檔案、指向別處的連結。
	manual := oldMacBackup(t, backups, "manual-keep")
	other := oldMacBackup(t, backups, "macos-old")
	file := filepath.Join(backups, "macos-20261004-010203-44")
	os.WriteFile(file, []byte("not a directory"), 0o600)
	elsewhere := oldMacBackup(t, t.TempDir(), "outside")
	link := filepath.Join(backups, "macos-20261005-010203-55")
	if err := os.Symlink(elsewhere, link); err != nil {
		t.Fatal(err)
	}
	// 上次沒刪完、改名移開的目錄。
	leftover := oldMacBackup(t, backups, pruneTrash+"macos-20261006-010203-66")
	foreignTrash := oldMacBackup(t, backups, pruneTrash+"something-else")

	if _, _, err := InstallMac(macBundle(t, "0.2.1"), "arm64", p); err != nil {
		t.Fatal(err)
	}
	keep := macReceiptPrevious(t, p)
	want := []string{keep, "macos-20261004-010203-44", "macos-20261005-010203-55", "macos-old", "manual-keep", pruneTrash + "something-else"}
	slices.Sort(want)
	if got := dirNames(t, backups); !slices.Equal(got, want) {
		t.Fatalf("backups after install = %v, want %v", got, want)
	}
	for _, dir := range old {
		if _, err := os.Lstat(dir); !os.IsNotExist(err) {
			t.Fatal("old backup kept:", dir)
		}
	}
	for _, kept := range []string{manual, other, file, link, foreignTrash, filepath.Join(elsewhere, MacApp, "Contents/MacOS/0c")} {
		if _, err := os.Lstat(kept); err != nil {
			t.Fatal("removed something not ours:", kept)
		}
	}
	if _, err := os.Lstat(leftover); !os.IsNotExist(err) {
		t.Fatal("leftover from an interrupted prune kept")
	}
	if !strings.Contains(appVersion(t, MacPaths{InputMethods: filepath.Join(backups, keep)}), "0.2.0") {
		t.Fatal("rollback backup does not hold the previous app")
	}

	// 回復：放回的那份只剩收據，跟著清掉；剛移入的目前版本留著，可再回復。
	if _, err := RollbackMac(p); err != nil {
		t.Fatal(err)
	}
	if v := appVersion(t, p); !strings.HasPrefix(v, "0.2.0 ") {
		t.Fatalf("rolled back to %q", v)
	}
	if _, err := os.Lstat(filepath.Join(backups, keep)); !os.IsNotExist(err) {
		t.Fatal("restored backup kept")
	}
	back := macReceiptPrevious(t, p)
	if !strings.Contains(appVersion(t, MacPaths{InputMethods: filepath.Join(backups, back)}), "0.2.1") {
		t.Fatal("rollback did not keep the version it replaced")
	}
	if _, err := RollbackMac(p); err != nil {
		t.Fatal(err)
	}
	if v := appVersion(t, p); !strings.HasPrefix(v, "0.2.1 ") {
		t.Fatalf("undo rollback gave %q", v)
	}

	// 解除安裝：App 移入新備份，只留它，可用 rollback 恢復。
	if _, err := UninstallMac(p); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, name := range dirNames(t, backups) {
		if info, err := os.Lstat(filepath.Join(backups, name)); err == nil && info.IsDir() && macBackupName.MatchString(name) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("uninstall left %d own backups", count)
	}
	if _, err := RollbackMac(p); err != nil {
		t.Fatal(err)
	}
	if v := appVersion(t, p); !strings.HasPrefix(v, "0.2.1 ") {
		t.Fatalf("restore after uninstall gave %q", v)
	}
}

// 使用中的備份（有程式從裡面執行，或系統的背景項目登記指向它）不刪；LaunchServices 的紀錄只留還在的目錄。
func TestMacPruneSkipsBackupsInUseAndSyncsLaunchServicesRecord(t *testing.T) {
	warned := quietPrune(t)
	home := t.TempDir()
	p := MacPaths{InputMethods: filepath.Join(home, "Library/Input Methods"), Support: filepath.Join(home, "Library/Application Support/0c")}
	backups := filepath.Join(p.Support, "backups")
	if _, _, err := InstallMac(macBundle(t, "0.2.0"), "arm64", p); err != nil {
		t.Fatal(err)
	}
	running := oldMacBackup(t, backups, "macos-20261001-010203-11")
	registered := oldMacBackup(t, backups, "macos-20261002-010203-22")
	stale := oldMacBackup(t, backups, "macos-20261003-010203-33")
	first := macReceiptPrevious(t, p)
	record := filepath.Join(backups, MacLSForgotten)
	os.WriteFile(record, []byte(strings.Join([]string{first, "macos-20261001-010203-11", "macos-20261003-010203-33", "macos-20250101-000000-1", "junk"}, "\n")+"\n"), 0o600)
	var asked []string
	p.InUse = func(dir string) bool {
		asked = append(asked, filepath.Base(dir))
		return dir == running || dir == registered
	}
	if _, _, err := InstallMac(macBundle(t, "0.2.1"), "arm64", p); err != nil {
		t.Fatal(err)
	}
	keep := macReceiptPrevious(t, p)
	for _, dir := range []string{running, registered, filepath.Join(backups, keep)} {
		if _, err := os.Lstat(dir); err != nil {
			t.Fatal("removed a backup in use:", dir)
		}
	}
	for _, dir := range []string{stale, filepath.Join(backups, first)} {
		if _, err := os.Lstat(dir); !os.IsNotExist(err) {
			t.Fatal("unused backup kept:", dir)
		}
	}
	if slices.Contains(asked, keep) {
		t.Fatal("the rollback backup is kept without asking")
	}
	if !slices.Contains(*warned, running) || !slices.Contains(*warned, registered) {
		t.Fatalf("backups kept in use were not reported: %v", *warned)
	}
	b, _ := os.ReadFile(record)
	if got := strings.Fields(string(b)); !slices.Equal(got, []string{"macos-20261001-010203-11"}) {
		t.Fatalf("launchservices record = %q", got)
	}
	// 不再使用之後，下一次就清掉。
	p.InUse = nil
	if _, err := RollbackMac(p); err != nil {
		t.Fatal(err)
	}
	if got := dirNames(t, backups); len(got) != 2 || !slices.Contains(got, MacLSForgotten) {
		t.Fatalf("backups after rollback = %v", got)
	}
	if b, _ = os.ReadFile(record); len(b) != 0 {
		t.Fatalf("record keeps removed backups: %q", b)
	}
}

// backups 是符號連結（.12 曾連到外接硬碟）：照舊拒絕安裝，什麼都不刪。
func TestPruneRefusesLinkedBackups(t *testing.T) {
	quietPrune(t)
	root := t.TempDir()
	real := filepath.Join(root, "elsewhere")
	old := oldMacBackup(t, real, "macos-20261001-010203-11")
	link := filepath.Join(root, "backups")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := pruneDir(link, macBackupName.MatchString, nil, nil); err == nil {
		t.Fatal("pruned through a link")
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatal("removed through a link")
	}
	p := MacPaths{InputMethods: filepath.Join(root, "Input Methods"), Support: root}
	if _, _, err := InstallMac(macBundle(t, "0.2.1"), "arm64", p); err == nil || !strings.Contains(err.Error(), "link") {
		t.Fatalf("install through linked backups: %v", err)
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatal("removed through a link")
	}
}

func windowsDirs(t *testing.T, base, sub string) []string {
	t.Helper()
	return dirNames(t, filepath.Join(base, sub))
}

// #179 Windows：備份只留收據 previous 那一份；版本目錄只留目前、回復目標與引擎指標用到的。
// 舊版累積的（含中斷安裝留下的 .broken-）一次清掉；核心更新的 -c- 目錄與其他名稱不碰。
func TestWindowsPruneKeepsCurrentAndRollbackOnly(t *testing.T) {
	quietPrune(t)
	base := t.TempDir()
	os.WriteFile(filepath.Join(base, UserStore), []byte("private learning"), 0o600)
	r := &fakeRegistrar{}
	first, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.1"), base, "amd64", r)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.2"), base, "amd64", r)
	if err != nil {
		t.Fatal(err)
	}
	// 舊版累積下來的：更早的版本目錄與備份、核心更新目錄、中斷安裝留下的、別的名稱。
	for _, name := range []string{"versions/0.1.0-dev.0-0123456789ab", "versions/0.1.0-dev.0-0123456789ab.broken-17",
		"versions/0.1.0-dev.2-c-0123456789ab", "versions/notes", "backups/windows-111", "backups/windows-222", "backups/manual"} {
		os.MkdirAll(filepath.Join(base, filepath.FromSlash(name), "data"), 0o700)
		os.WriteFile(filepath.Join(base, filepath.FromSlash(name), "data", "lm.bin"), []byte("old"), 0o600)
	}
	os.WriteFile(filepath.Join(base, "backups", "windows-333"), []byte("a file"), 0o600)

	third, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.3"), base, "amd64", r)
	if err != nil {
		t.Fatal(err)
	}
	current, _ := readReceipt(filepath.Join(base, "install-receipt.json"))
	gotVersions := windowsDirs(t, base, "versions")
	wantVersions := []string{filepath.Base(second), filepath.Base(third), "0.1.0-dev.2-c-0123456789ab", "notes"}
	slices.Sort(wantVersions)
	if !slices.Equal(gotVersions, wantVersions) {
		t.Fatalf("versions = %v, want %v", gotVersions, wantVersions)
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatal("kept a version that is neither current nor the rollback target")
	}
	wantBackups := []string{strings.TrimPrefix(current.Previous, "backups/"), "manual", "windows-333"}
	slices.Sort(wantBackups)
	if got := windowsDirs(t, base, "backups"); !slices.Equal(got, wantBackups) {
		t.Fatalf("backups = %v, want %v", got, wantBackups)
	}

	// 回復與撤回回復照常；回復後剛換下的版本留著，更早的備份清掉。
	if _, err = RollbackWindows(base, r, true); err != nil {
		t.Fatal(err)
	}
	if r.path != filepath.Join(second, "0c_tsf.dll") {
		t.Fatal("not rolled back")
	}
	if _, err := os.Stat(third); err != nil {
		t.Fatal("removed the version just rolled back from")
	}
	if b, _ := os.ReadFile(filepath.Join(base, UserStore)); string(b) != "private learning" {
		t.Fatal("changed user data")
	}
	if _, err = RollbackWindows(base, r, false); err != nil {
		t.Fatal(err)
	}
	if r.path != filepath.Join(third, "0c_tsf.dll") {
		t.Fatal("cannot undo rollback")
	}
	own := 0
	for _, name := range windowsDirs(t, base, "backups") {
		if windowsBackupName.MatchString(name) {
			own++
		}
	}
	if own != 2 { // previous 那一份，加上一般檔案 windows-333
		t.Fatalf("own backups after rollback = %d", own)
	}

	// 解除安裝：只留回復用的那一份，rollback 可恢復。
	if _, err = UninstallWindows(base, r); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(third); err != nil {
		t.Fatal("uninstall removed the version needed to restore")
	}
	if _, err := os.Stat(second); !os.IsNotExist(err) {
		t.Fatal("uninstall kept an older version")
	}
	if _, err = RollbackWindows(base, r, false); err != nil {
		t.Fatal(err)
	}
	if r.path != filepath.Join(third, "0c_tsf.dll") {
		t.Fatal("cannot undo uninstall")
	}
}

// 引擎指標（核心更新）與回復目標記錄的引擎目錄都要留著；讀不到回復目標時這次不清版本目錄。
func TestWindowsPruneKeepsEngineDirectoriesAndStopsWhenUnsure(t *testing.T) {
	quietPrune(t)
	base := t.TempDir()
	r := &fakeRegistrar{}
	if _, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.1"), base, "amd64", r); err != nil {
		t.Fatal(err)
	}
	pointed := "versions/0.1.0-dev.0-aaaaaaaaaaaa"
	recorded := "versions/0.1.0-dev.0-bbbbbbbbbbbb"
	unused := "versions/0.1.0-dev.0-cccccccccccc"
	for _, dir := range []string{pointed, recorded, unused} {
		os.MkdirAll(filepath.Join(base, filepath.FromSlash(dir)), 0o700)
	}
	receipt, _ := readReceipt(filepath.Join(base, "install-receipt.json"))
	receipt.Engine = recorded
	writeJSON(filepath.Join(base, "install-receipt.json"), receipt)
	if err := enginedir.Write(base, enginedir.Current{Directory: pointed, Version: "0.1.0-dev.0"}); err != nil {
		t.Fatal(err)
	}
	final := *receipt
	pruneWindows(base, &final)
	for dir, want := range map[string]bool{pointed: true, recorded: true, unused: false, receipt.Directory: true} {
		if _, err := os.Stat(filepath.Join(base, filepath.FromSlash(dir))); (err == nil) != want {
			t.Fatalf("%s exists=%v, want %v", dir, err == nil, want)
		}
	}
	// 回復目標的收據讀不出來：不知道它用哪個版本目錄，版本目錄全部保留。
	os.MkdirAll(filepath.Join(base, filepath.FromSlash(unused)), 0o700)
	os.WriteFile(filepath.Join(base, filepath.FromSlash(receipt.Previous), "previous.json"), []byte("{broken"), 0o600)
	pruneWindows(base, &final)
	if _, err := os.Stat(filepath.Join(base, filepath.FromSlash(unused))); err != nil {
		t.Fatal("pruned versions without knowing the rollback target")
	}
}

func TestWindowsOwnedVersionNames(t *testing.T) {
	for name, want := range map[string]bool{
		"0.2.2-0123456789ab":                    true,
		"0.1.0-dev.63.144-2e497eb26a1a":         true,
		"0.1.0-dev.61-549eabb97acb.broken-1792": true,
		"0.2.2-c-0123456789ab":                  false, // 核心更新，由 PruneCore 管理
		"0.2.2-0123456789AB":                    false,
		"0.2.2-0123456789a":                     false,
		"notes":                                 false,
		".stage-123":                            false,
		"0.2.2-0123456789ab.bak":                false,
	} {
		if got := windowsOwnedVersion(name); got != want {
			t.Errorf("windowsOwnedVersion(%q) = %v, want %v", name, got, want)
		}
	}
}

// Windows：執行中的 exe、已載入的 DLL 不擋目錄改名（.14 實測），所以先刪 DLL、再刪執行檔；刪不掉就保留其餘檔案。
func TestWindowsPruneKeepsVersionsWithProgramsInUse(t *testing.T) {
	warned := quietPrune(t)
	base := t.TempDir()
	r := &fakeRegistrar{}
	if _, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.1"), base, "amd64", r); err != nil {
		t.Fatal(err)
	}
	loaded := filepath.Join(base, "versions", "0.1.0-dev.0-aaaaaaaaaaaa")
	running := filepath.Join(base, "versions", "0.1.0-dev.0-bbbbbbbbbbbb")
	idle := filepath.Join(base, "versions", "0.1.0-dev.0-cccccccccccc")
	for _, dir := range []string{loaded, running, idle} {
		for _, name := range []string{"0c_tsf.dll", "x86/0c_tsf.dll", "0c-server.exe", "0c-ui.exe", "data/lm.bin"} {
			os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o700)
			os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600)
		}
	}
	inUse := map[string]bool{filepath.Join(loaded, "x86", "0c_tsf.dll"): true, filepath.Join(running, "0c-server.exe"): true}
	removeProgram = func(p string) error {
		if inUse[p] {
			return os.ErrPermission
		}
		return os.Remove(p)
	}
	t.Cleanup(func() { removeProgram = os.Remove })
	receipt, _ := readReceipt(filepath.Join(base, "install-receipt.json"))
	pruneWindows(base, receipt)
	if _, err := os.Stat(idle); !os.IsNotExist(err) {
		t.Fatal("idle version kept")
	}
	for _, p := range []string{filepath.Join(loaded, "0c-server.exe"), filepath.Join(loaded, "data/lm.bin"), filepath.Join(running, "0c-server.exe"), filepath.Join(running, "data/lm.bin")} {
		if _, err := os.Stat(p); err != nil {
			t.Fatal("removed a file of a version in use:", p)
		}
	}
	if _, err := os.Stat(filepath.Join(running, "0c_tsf.dll")); !os.IsNotExist(err) {
		t.Fatal("the DLL goes first, so no application loads the old version again")
	}
	if !slices.Contains(*warned, loaded) || !slices.Contains(*warned, running) {
		t.Fatalf("kept versions not reported: %v", *warned)
	}
	// 程式結束、登出之後，下一次就清掉。
	removeProgram = os.Remove
	pruneWindows(base, receipt)
	for _, dir := range []string{loaded, running} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatal("version kept after its programs stopped:", dir)
		}
	}
	if _, err := os.Stat(filepath.Join(base, filepath.FromSlash(receipt.Directory), "0c_tsf.dll")); err != nil {
		t.Fatal("removed the installed version")
	}
}
