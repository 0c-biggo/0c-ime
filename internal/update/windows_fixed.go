package update

// 固定路徑的 TSF DLL（#121）。ESET 等防毒軟體的例外清單只接受完整路徑，而版本目錄每次更新都換名字，
// 所以 0.1.0-dev.62 起 InprocServer32 永遠指向 %LOCALAPPDATA%\Zuin\tsf\0c_tsf.dll（32 位元在 tsf\x86\）。
// 版本目錄仍保留完整檔案當正本，固定路徑是部署出來的複本。載入中的 DLL 不能覆寫、不能刪除，但可以改名，
// 所以換檔是：複製成 .new 並核對 → 舊檔改名成 .old-<名稱> → .new 改名成正式檔名；刪不掉的 .old 留到下次。
// 不用 junction／符號連結：防毒看到的是解析後的路徑，而且 RedirectionGuard 會讓部分行程不跟隨使用者建立的連結。
import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FixedDir 是固定 TSF DLL 所在的目錄（相對安裝目錄）。沙箱應用程式的讀取權限設在這個目錄並由檔案繼承，
// 不開放整個安裝目錄（個人詞庫在那裡）。
const FixedDir = "tsf"

// FixedSince 是第一個從固定路徑載入的版本；更舊的 DLL 只會在自己的版本目錄找引擎與候選窗，仍註冊版本目錄。
const FixedSince = "0.1.0-dev.62"

// fixedSlot：版本目錄裡的檔名與固定路徑（都相對安裝目錄的 FixedDir）。
var fixedSlots = []string{windowsCurrent.DLL, WindowsDLL32}

var (
	fixedRename     = os.Rename // 測試替換成會失敗的版本，模擬防毒掃描或載入中的檔案
	fixedRemove     = os.Remove
	fixedRetries    = 10
	fixedRetryDelay = 200 * time.Millisecond
)

// FixedDirHook 在固定路徑的目錄建立後呼叫（Windows：開放沙箱應用程式讀取並由檔案繼承）。nil 表示不處理。
var FixedDirHook func(dir string) error

// ErrInstallLocked：另一個更新正在進行（安裝鎖存在）。
var ErrInstallLocked = errors.New("另一個更新正在進行")

// FixedDLL 是 x64 DLL 的固定路徑。
func FixedDLL(base string) string { return filepath.Join(base, FixedDir, windowsCurrent.DLL) }

// usesFixed：這個收據的版本從固定路徑載入（新命名、FixedSince 以後）。
func usesFixed(r *Receipt) bool {
	if r == nil || r.Directory == "." {
		return false
	}
	if layout, err := WindowsLayoutOf(r.Files); err != nil || layout != windowsCurrent {
		return false
	}
	order, err := CompareVersion(r.Version, FixedSince)
	return err == nil && order >= 0
}

// retry 重試改名：防毒軟體掃描剛寫入的檔案時會暫時占住它。
func retry(op func() error) error {
	var err error
	for i := 0; i < fixedRetries; i++ {
		if err = op(); err == nil {
			return nil
		}
		if i+1 < fixedRetries {
			time.Sleep(fixedRetryDelay)
		}
	}
	return err
}

// copySynced 複製並 fsync（Windows 為 FlushFileBuffers），改名前內容已在磁碟上。
func copySynced(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err = out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func hashMatches(file, want string) bool {
	sum, err := Hash(file)
	return err == nil && sum == want
}

// oldName 回傳還沒被用過的 .old-<tag> 名稱：上一次留下的 .old 可能還被應用程式載入、刪不掉。
func oldName(dst, tag string) string {
	if tag == "" || !SafeName(tag) || strings.ContainsAny(tag, `/\`) {
		tag = "previous"
	}
	name := dst + ".old-" + tag
	for i := 2; ; i++ {
		if _, err := os.Lstat(name); os.IsNotExist(err) {
			return name
		}
		name = fmt.Sprintf("%s.old-%s-%d", dst, tag, i)
	}
}

// deployOne 讓 dst 的內容等於 src（雜湊 want）。也是復原：上一次停在任何一步都從這裡接著做完。
//   - dst 已是 want：只清掉留下的 .new。
//   - .new 已是 want（上次準備好但沒換上）：直接拿來用，否則重新從 src 複製並核對。
//   - dst 存在：先改名成 .old-<tag>；失敗就維持原檔，下次再換。
//   - .new 改名成 dst；重試仍失敗就把 .old 改回 dst。
func deployOne(src, dst, want, tag string) error {
	staged := dst + ".new"
	if hashMatches(dst, want) {
		if err := os.Remove(staged); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if !hashMatches(staged, want) {
		if !hashMatches(src, want) {
			return fmt.Errorf("版本目錄的 %s 與收據不符（可能被防毒軟體隔離）", filepath.Base(src))
		}
		if err := prepareFixedDir(filepath.Dir(dst)); err != nil {
			return err
		}
		os.Remove(staged)
		if err := copySynced(src, staged); err != nil {
			os.Remove(staged)
			return err
		}
		if !hashMatches(staged, want) {
			os.Remove(staged)
			return fmt.Errorf("複製 %s 後核對失敗", filepath.Base(dst))
		}
	}
	old := ""
	if _, err := os.Lstat(dst); err == nil {
		old = oldName(dst, tag)
		if err := retry(func() error { return fixedRename(dst, old) }); err != nil {
			return fmt.Errorf("無法替換 %s，維持原檔：%w", dst, err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := retry(func() error { return fixedRename(staged, dst) }); err != nil {
		if old != "" {
			if back := retry(func() error { return fixedRename(old, dst) }); back != nil {
				return errors.Join(fmt.Errorf("無法換上 %s：%w", dst, err), fmt.Errorf("也無法放回原檔 %s：%w", old, back))
			}
		}
		return fmt.Errorf("無法換上 %s，已放回原檔：%w", dst, err)
	}
	return nil
}

// prepareFixedDir 建立固定路徑的目錄；第一次建立 tsf 目錄時設定沙箱讀取權限（子目錄與檔案繼承）。
func prepareFixedDir(dir string) error {
	root := dir
	for filepath.Base(root) != FixedDir && filepath.Dir(root) != root {
		root = filepath.Dir(root)
	}
	_, statErr := os.Stat(root)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if os.IsNotExist(statErr) && FixedDirHook != nil {
		// 設不上只影響沙箱應用程式；照常部署。
		_ = FixedDirHook(root)
	}
	return nil
}

// cleanupFixed 刪掉所有刪得掉的 .old-*；仍被應用程式載入的留到下次（不用 MOVEFILE_DELAY_UNTIL_REBOOT，那要系統管理員）。
func cleanupFixed(base string) []string {
	var left []string
	for _, dir := range []string{filepath.Join(base, FixedDir), filepath.Join(base, FixedDir, filepath.Dir(filepath.FromSlash(WindowsDLL32)))} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasPrefix(e.Name(), windowsCurrent.DLL+".old-") {
				p := filepath.Join(dir, e.Name())
				if err := fixedRemove(p); err != nil {
					left = append(left, p)
				}
			}
		}
	}
	return left
}

// DeployFixed 從收據的版本目錄部署固定路徑的 DLL（x64 與 32 位元）。tag 是被換下的版本名稱，用來命名 .old。
// 收據沒有 32 位元 DLL 時，固定路徑上的 32 位元檔案改名移開，DLL 註冊時會一併清掉 32 位元的註冊。
func DeployFixed(base string, r *Receipt, tag string) error {
	dir, err := receiptDirectory(base, r)
	if err != nil {
		return err
	}
	for _, name := range fixedSlots {
		dst := filepath.Join(base, FixedDir, filepath.FromSlash(name))
		want, ok := r.Files[name]
		if !ok {
			if err := retireFixed(dst, tag); err != nil {
				return err
			}
			continue
		}
		if err := deployOne(filepath.Join(dir, filepath.FromSlash(name)), dst, want, tag); err != nil {
			return err
		}
	}
	cleanupFixed(base)
	return nil
}

// retireFixed 移除一個固定路徑的檔案；被載入而刪不掉時改名成 .old，留到下次清除。
func retireFixed(dst, tag string) error {
	os.Remove(dst + ".new")
	if err := fixedRemove(dst); err == nil || os.IsNotExist(err) {
		return nil
	}
	return retry(func() error { return fixedRename(dst, oldName(dst, tag)) })
}

// RemoveFixed 移除固定路徑的 DLL 與留下的 .new／.old（解除安裝，或回到不從固定路徑載入的舊版）。
// 仍被應用程式載入的 .old 留著，回傳它們的路徑。
func RemoveFixed(base string) ([]string, error) {
	var errs []error
	for _, name := range fixedSlots {
		if err := retireFixed(filepath.Join(base, FixedDir, filepath.FromSlash(name)), "removed"); err != nil {
			errs = append(errs, err)
		}
	}
	left := cleanupFixed(base)
	if len(left) == 0 {
		os.Remove(filepath.Join(base, FixedDir, filepath.Dir(filepath.FromSlash(WindowsDLL32))))
		os.Remove(filepath.Join(base, FixedDir))
	}
	return left, errors.Join(errs...)
}

// RepairFixed 依目前的收據復原固定路徑（更新器每次執行、schedule repair 時呼叫；不需要系統管理員）：
// 中斷在換檔任何一步、或固定檔被防毒軟體刪掉／改動，都從版本目錄補回。changed 表示這次有換檔或補檔。
func RepairFixed(base string) (changed bool, err error) {
	if _, err := os.Stat(filepath.Join(base, "update.lock")); err == nil {
		return false, ErrInstallLocked
	}
	unlock, err := lockInstall(base)
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrInstallLocked, err)
	}
	defer unlock()
	return repairFixed(base)
}

func repairFixed(base string) (bool, error) {
	r, err := readReceipt(filepath.Join(base, "install-receipt.json"))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !r.Active || !usesFixed(r) {
		// 解除安裝後、或回到不從固定路徑載入的舊版（例如以舊版工具回復）時不該有固定檔：移除，載入中的留到下次。
		before := fixedState(base)
		_, err := RemoveFixed(base)
		return fixedState(base) != before, err
	}
	before := fixedState(base)
	err = DeployFixed(base, r, "repair")
	return fixedState(base) != before, err
}

// fixedState 是固定路徑兩個檔案的雜湊（判斷 repair 有沒有動到）。
func fixedState(base string) string {
	var parts []string
	for _, name := range fixedSlots {
		sum, _ := Hash(filepath.Join(base, FixedDir, filepath.FromSlash(name)))
		parts = append(parts, sum)
	}
	return strings.Join(parts, ",")
}
