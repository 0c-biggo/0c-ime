//go:build darwin

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// dropRoot（#198）：以 root 執行（sudo installer、MDM、Homebrew Cask 的 pkg 安裝，或直接 sudo 0c-update）時，
// 先把之前以 root 留在那個帳號的靈犀注音檔案還給使用者，再以那個使用者的身分重新執行同一個命令，
// 所有家目錄的檔案、排程、輸入來源與引擎都以使用者身分處理。執行完以同一個結束碼結束，不會返回；
// 不是 root 時什麼都不做。
func dropRoot(args []string) error {
	if os.Geteuid() != 0 {
		return nil
	}
	account, err := rootAccount()
	if err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	// pkg 的套件在安裝程式 root 的暫存目錄，裡面有 0700 的目錄（例如 neural/python），使用者讀不到。
	// 複製一份給使用者（APFS 上是 clonefile，不佔空間），結束後刪掉。
	var copied string
	if bundle, ok := installBundleArg(args); ok {
		copied, err = bundleForUser(bundle, account.UID)
		if err != nil {
			return fmt.Errorf("無法把套件交給 %s：%w", account.Name, err)
		}
		defer os.RemoveAll(filepath.Dir(copied))
		args = withBundleArg(args, copied)
		if rel, err := filepath.Rel(bundle, self); err == nil && !strings.HasPrefix(rel, "..") {
			self = filepath.Join(copied, rel)
		}
	}
	stopRootEngines(account.Home)
	reclaimed := 0
	for _, rel := range macOwnedPaths {
		n, err := reclaimOwnership(account.Home, rel, 0, account.UID)
		reclaimed += n
		if err != nil {
			fmt.Fprintf(os.Stderr, "0c-update: 無法把 %s 還給 %s：%v\n", filepath.Join(account.Home, rel), account.Name, err)
		}
	}
	if reclaimed > 0 {
		fmt.Fprintf(os.Stderr, "已把 %d 個屬於系統管理員（root）的靈犀注音檔案還給 %s（之前用 sudo 安裝留下的）。\n", reclaimed, account.Name)
	}
	// 都寫到 stderr：status -json 等的 stdout 只能有結果；安裝程式兩者都記進 install.log
	fmt.Fprintf(os.Stderr, "以系統管理員（root）身分執行，改以使用者「%s」的身分執行。\n", account.Name)
	uid := strconv.Itoa(account.UID)
	// launchctl asuser：進入使用者的 launchd 工作階段（排程、輸入來源登記）；sudo -u：換成使用者身分。
	// env -i 只帶必要的環境，不讓 root 的環境（或安裝程式的變數）跟著進去。
	argv := []string{"/bin/launchctl", "asuser", uid, "/usr/bin/sudo", "-u", "#" + uid, "--", "/usr/bin/env", "-i",
		"HOME=" + account.Home, "USER=" + account.Name, "LOGNAME=" + account.Name, "PATH=/usr/bin:/bin:/usr/sbin:/sbin"}
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); k == "LANG" || strings.HasPrefix(k, "LC_") || strings.HasPrefix(k, "ZC_") {
			argv = append(argv, kv)
		}
	}
	argv = append(argv, self)
	argv = append(argv, args...)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err = cmd.Run()
	if copied != "" {
		os.RemoveAll(filepath.Dir(copied))
	}
	code := 0
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		code = exit.ExitCode()
		if code < 0 {
			code = 1
		}
	case err != nil:
		return err
	}
	os.Exit(code)
	return nil
}

// bundleForUser 把套件目錄複製到新的暫存目錄、改成屬於 uid，回傳複本路徑。暫存目錄在改擁有者的期間
// 是 root 的 0700，使用者碰不到；全部改好之後才把最上層交給使用者。
func bundleForUser(bundle string, uid int) (string, error) {
	tmp, err := os.MkdirTemp("/private/tmp", "0c-install-")
	if err != nil {
		return "", err
	}
	dst := filepath.Join(tmp, filepath.Base(bundle))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	// -c：APFS 上以 clonefile 複製；不同磁碟區（例如外接碟上的套件）時退回一般複製。
	if out, err := exec.CommandContext(ctx, "/bin/cp", "-Rc", bundle, dst).CombinedOutput(); err != nil {
		os.RemoveAll(dst)
		if out2, err2 := exec.CommandContext(ctx, "/bin/cp", "-R", bundle, dst).CombinedOutput(); err2 != nil {
			os.RemoveAll(tmp)
			return "", fmt.Errorf("%s %s (%w)", strings.TrimSpace(string(out)), strings.TrimSpace(string(out2)), err2)
		}
	}
	err = filepath.WalkDir(dst, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(path, uid, -1)
	})
	if err == nil {
		err = os.Lchown(tmp, uid, -1)
	}
	if err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	return dst, nil
}

// rootAccount 決定要替哪個帳號做事，並從帳號資料庫取得名稱與家目錄（不信 HOME）。
func rootAccount() (macAccount, error) {
	console := -1
	if info, err := os.Stat("/dev/console"); err == nil {
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			console = int(st.Uid)
		}
	}
	if console > 0 {
		if a, err := lookupAccount(console); err != nil || systemAccount(a) {
			console = -1 // 例如 _mbsetupuser（設定輔助程式）
		}
	}
	homeOwner := -1
	if home := os.Getenv("HOME"); home != "" {
		if info, err := os.Stat(home); err == nil {
			if st, ok := info.Sys().(*syscall.Stat_t); ok {
				homeOwner = int(st.Uid)
			}
		}
	}
	uid, err := rootTarget(os.Getenv("SUDO_UID"), console, homeOwner)
	if err != nil {
		var amb *ambiguousTarget
		if errors.As(err, &amb) {
			amb.homeName, amb.consoleName = accountName(amb.home), accountName(amb.console)
		}
		return macAccount{}, err
	}
	a, err := lookupAccount(uid)
	if err != nil {
		return macAccount{}, err
	}
	if systemAccount(a) {
		return macAccount{}, fmt.Errorf("以系統管理員（root）身分執行，但 %s 是系統帳號，不能安裝輸入法", a.Name)
	}
	info, err := os.Lstat(a.Home)
	if err != nil || !info.IsDir() {
		return macAccount{}, fmt.Errorf("以系統管理員（root）身分執行：%s 的家目錄 %s 不存在，這次沒有安裝；請在這個帳號登入過一次後再執行", a.Name, a.Home)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != a.UID {
		return macAccount{}, fmt.Errorf("以系統管理員（root）身分執行：%s 不屬於 %s，這次沒有安裝", a.Home, a.Name)
	}
	return a, nil
}

func lookupAccount(uid int) (macAccount, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/bin/id", "-P", strconv.Itoa(uid)).Output()
	if err != nil {
		return macAccount{}, fmt.Errorf("找不到帳號 %d：%w", uid, err)
	}
	a, err := parseAccountRecord(string(out))
	if err == nil && a.UID != uid {
		err = fmt.Errorf("帳號資料不符：要 %d，得到 %d", uid, a.UID)
	}
	return a, err
}

func accountName(uid int) string {
	if a, err := lookupAccount(uid); err == nil {
		return a.Name
	}
	return ""
}

// stopRootEngines 結束以 root 執行、執行檔在這個帳號的靈犀注音路徑下的程序（之前 root 安裝帶起的引擎與
// 它的神經選字 worker）。它們開著使用者的 socket、存檔時以 root 寫 user.json，要在改回擁有者之前結束：
// SIGTERM 讓引擎照常存檔結束，最多等 10 秒，之後 SIGKILL。
func stopRootEngines(home string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/bin/ps", "-U", "0", "-o", "pid=,comm=").Output()
	if err != nil {
		return
	}
	var prefixes []string
	for _, rel := range macOwnedPaths {
		prefixes = append(prefixes, filepath.Join(home, rel)+"/")
	}
	pids := enginesUnder(string(out), prefixes)
	if len(pids) == 0 {
		return
	}
	for _, pid := range pids {
		syscall.Kill(pid, syscall.SIGTERM)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		alive := 0
		for _, pid := range pids {
			if syscall.Kill(pid, 0) == nil {
				alive++
			}
		}
		if alive == 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	for _, pid := range pids {
		if syscall.Kill(pid, 0) == nil {
			syscall.Kill(pid, syscall.SIGKILL)
		}
	}
	fmt.Fprintf(os.Stderr, "已結束 %d 個以系統管理員（root）身分執行的靈犀注音程序。\n", len(pids))
}

// refuseForeignOwned（#198）：使用者身分執行安裝、套用、回復或移除時，靈犀注音的檔案有不屬於這個帳號的
// （之前 root 安裝留下的），先說明原因與修復指令，不要做到一半才出現 permission denied。root 擁有的目錄
// 一般帳號無法搬移或刪除其中的檔案，只能由 root 修回（見 dropRoot）。
func refuseForeignOwned(args []string) error {
	if os.Geteuid() == 0 {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	count, sample := foreignOwned(home, macOwnedPaths, os.Getuid(), 1)
	if count == 0 {
		return nil
	}
	self, _ := os.Executable()
	return errors.New(foreignOwnedMessage(count, sample, os.Getenv("PACKAGE_PATH"), self, args))
}

// ownershipLine 給 status：有不屬於這個帳號的檔案時說明修復方式。
func ownershipLine() string {
	home, err := os.UserHomeDir()
	if err != nil || os.Geteuid() == 0 {
		return ""
	}
	count, sample := foreignOwned(home, macOwnedPaths, os.Getuid(), 1)
	if count == 0 {
		return ""
	}
	return "注意：" + foreignOwnedMessage(count, sample, "<新版的 .pkg 安裝檔>", "", nil)
}
