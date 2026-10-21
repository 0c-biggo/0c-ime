package update

// 更新備份的清理（#179）。每次安裝、回復、解除安裝都把整個舊版移入備份（macOS 的 App 約 500 MB，Windows 的版本目錄
// 約 460 MB），以前從來不刪，越積越多。回復只用得到收據 previous 指到的那一份，所以每次成功之後只留它，其他的刪掉。
// macOS 在安裝、回復、解除安裝成功後同步清理。Windows 改到背景（PruneWindows）：第一次升級要刪的舊版可達
// 數 GB、上百個目錄（.14 實測 27 份版本目錄約 9 GB 加 110 份備份，安裝精靈停在進度條約 70 秒）。
//
// 安全規則：
//   - 只在持有安裝鎖時、依寫好的收據決定刪什麼；刪除失敗只記錄，不影響安裝、回復的結果，下次再試。
//   - 只刪自己命名格式的目錄（備份 macos-<UTC 時間>-<亂數>、windows-<亂數>；Windows 的版本目錄 <版本>-<12 位雜湊>
//     與中斷安裝留下的 .broken-<數字>），連結、一般檔案與其他名稱都不碰。核心更新的 <版本>-c-<雜湊> 由 PruneCore 管理。
//   - 備份目錄（或它的上層）是符號連結時照舊拒絕，什麼都不刪。
//   - 仍在使用的不刪。macOS：呼叫端回報有程式從裡面執行、開著其中的檔案，或系統的背景項目登記指向它。
//     Windows：版本目錄先刪 DLL、再刪執行檔；仍被應用程式載入的 DLL、執行中的程式刪不掉，就保留其餘檔案。
//     （.14 實測：執行中的 exe、已載入的 DLL 不擋目錄改名，開著的檔案才擋，所以不能只靠改名判斷。）
//     刪除先把整個目錄改名移開再刪：有程式開著其中的檔案時改名失敗，整個保留，不會刪到一半。
//     刪到一半失敗（例如防毒軟體占住檔案）時，移開的目錄下次清理再刪。
import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/0c-biggo/0c-ime/internal/atomicfile"
	"github.com/0c-biggo/0c-ime/internal/enginedir"
)

var (
	macBackupName     = regexp.MustCompile(`^macos-[0-9]{8}-[0-9]{6}-[0-9]+$`)
	windowsBackupName = regexp.MustCompile(`^windows-[0-9]+$`)
	windowsVersionDir = regexp.MustCompile(`^(.+)-[0-9a-f]{12}(\.broken-[0-9]+)?$`)
)

// pruneTrash 是清理時暫時改名的前綴；留下來的表示上次沒刪完。
const pruneTrash = ".removing-"

// PruneWarn 記錄清理失敗或因使用中而保留的目錄；預設寫到標準錯誤，測試可替換。
var PruneWarn = func(path string, err error) {
	fmt.Fprintf(os.Stderr, "0c-update: 舊版暫不刪除 %s：%v\n", path, err)
}

// errInUse：目錄仍在使用，這次保留。
var errInUse = errors.New("仍在使用中，下次更新再刪")

// pruneDir 把 root 底下 owned 認得、不在 keep 的目錄改名移開（.removing-<名稱>），回傳要刪的路徑，
// 由呼叫端 removeTrash 真的刪掉：Windows 在持有安裝鎖時只改名（很快），放開鎖之後才刪（#179）。
// 上次沒刪完的 .removing-* 也一併回傳。使用中或移不開的交給 PruneWarn 記錄後保留。
// release 在改名前確認目錄沒有在用（回傳錯誤就保留）；nil 表示不需確認。
// root 不存在時不做事；root 或上層是連結時回傳錯誤，不動任何東西。
func pruneDir(root string, owned func(string) bool, keep map[string]bool, release func(string) error) ([]string, error) {
	if err := noLinks(root); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var trashed []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || e.Type()&os.ModeSymlink != 0 {
			continue
		}
		path := filepath.Join(root, name)
		if rest, ok := strings.CutPrefix(name, pruneTrash); ok {
			// 上次改名移開後沒刪完的，已經沒有人會用到。
			if owned(rest) {
				trashed = append(trashed, path)
			}
			continue
		}
		if !owned(name) || keep[name] {
			continue
		}
		if release != nil {
			if err := release(path); err != nil {
				PruneWarn(path, err)
				continue
			}
		}
		trash := filepath.Join(root, pruneTrash+name)
		if _, err := os.Lstat(trash); err == nil {
			if err = os.RemoveAll(trash); err != nil {
				PruneWarn(trash, err)
				continue
			}
		}
		if err := os.Rename(path, trash); err != nil {
			// Windows：其中有檔案被程式開著（例如執行中的引擎開著的字典），整個保留。
			PruneWarn(path, err)
			continue
		}
		trashed = append(trashed, trash)
	}
	return trashed, nil
}

// removeTrash 刪掉 pruneDir 移開的目錄；刪到一半失敗（例如防毒軟體占住檔案）的下次再刪。
func removeTrash(paths []string) {
	for _, p := range paths {
		if err := removeAll(p); err != nil {
			PruneWarn(p, err)
		}
	}
}

// removeAll 測試替換，用來確認刪除時已放開安裝鎖。
var removeAll = os.RemoveAll

// prunable 回報 root 底下有沒有 pruneDir 會動的目錄（不改任何東西），讓沒東西可清時不必拿安裝鎖。
func prunable(root string, owned func(string) bool, keep map[string]bool) bool {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || e.Type()&os.ModeSymlink != 0 {
			continue
		}
		if rest, ok := strings.CutPrefix(name, pruneTrash); ok {
			name = rest
			if owned(name) {
				return true
			}
			continue
		}
		if owned(name) && !keep[name] {
			return true
		}
	}
	return false
}

// MacLSForgotten 記下已從 LaunchServices 取消登記的備份目錄名稱（每行一個，見 cmd/0c-update）。
const MacLSForgotten = ".launchservices-forgotten"

// pruneMacBackups 只保留 keep（收據的 previous，例如 backups/macos-…）與使用中的備份，
// 並把已刪除的目錄從 LaunchServices 紀錄中拿掉。
func pruneMacBackups(p MacPaths, keep string) {
	backups := filepath.Join(p.Support, "backups")
	kept := map[string]bool{}
	if dir, ok := strings.CutPrefix(keep, "backups/"); ok && !strings.Contains(dir, "/") {
		kept[dir] = true
	}
	var release func(string) error
	if p.InUse != nil {
		release = func(dir string) error {
			if p.InUse(dir) {
				return errInUse
			}
			return nil
		}
	}
	trashed, err := pruneDir(backups, macBackupName.MatchString, kept, release)
	if err != nil {
		PruneWarn(backups, err)
		return
	}
	removeTrash(trashed)
	if err := SyncLSForgotten(backups); err != nil {
		PruneWarn(filepath.Join(backups, MacLSForgotten), err)
	}
}

// SyncLSForgotten 把 LaunchServices 紀錄中已不存在的備份目錄拿掉：紀錄不會無限變長，
// 日後同名的新備份也不會被誤當成已處理。沒有紀錄檔時不做事。
func SyncLSForgotten(backups string) error {
	file := filepath.Join(backups, MacLSForgotten)
	if err := noLinks(file); err != nil {
		return err
	}
	b, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	names := strings.Fields(string(b))
	var still []string
	for _, name := range names {
		if !macBackupName.MatchString(name) {
			continue
		}
		if info, err := os.Lstat(filepath.Join(backups, name)); err == nil && info.IsDir() {
			still = append(still, name)
		}
	}
	if len(still) == len(names) {
		return nil
	}
	text := ""
	if len(still) > 0 {
		text = strings.Join(still, "\n") + "\n"
	}
	return atomicfile.Write(file, []byte(text))
}

// windowsOwnedVersion 認得完整安裝的版本目錄 <版本>-<12 位雜湊>（含 .broken-<數字>）；核心更新的 -c- 目錄不算。
func windowsOwnedVersion(name string) bool {
	m := windowsVersionDir.FindStringSubmatch(name)
	return m != nil && ValidVersion(m[1]) && !strings.Contains(name, "-c-")
}

// releasePrograms 先刪版本目錄最上層（與 x86）的 DLL，再刪執行檔：DLL 刪掉之後應用程式就不會再載入它、
// 也就不會再從這裡啟動引擎或候選窗。仍被載入的 DLL、執行中的程式刪不掉時停下，其餘檔案保留，下次更新再試。
func releasePrograms(dir string) error {
	var dlls, exes []string
	for _, sub := range []string{".", filepath.Dir(filepath.FromSlash(WindowsDLL32))} {
		entries, err := os.ReadDir(filepath.Join(dir, sub))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.Type().IsRegular() {
				continue
			}
			p := filepath.Join(dir, sub, e.Name())
			switch strings.ToLower(filepath.Ext(e.Name())) {
			case ".dll":
				dlls = append(dlls, p)
			case ".exe":
				exes = append(exes, p)
			}
		}
	}
	for _, p := range append(dlls, exes...) {
		if err := removeProgram(p); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("%w（%v）", errInUse, err)
		}
	}
	return nil
}

// removeProgram 測試替換成會失敗的版本，模擬執行中的程式或仍被載入的 DLL（Linux 上刪得掉）。
var removeProgram = os.Remove

// windowsKeep 算出 final 收據要保留的備份與版本目錄：收據 previous 那一份備份，以及收據、回復目標與引擎指標
// 用到的版本目錄。個人詞庫備份只在 previous 那一份裡（回復時 -restore-user 用的就是它）。
// unsure：讀不到回復目標或引擎指標，不知道該留哪個版本目錄，這次不清版本目錄。
func windowsKeep(base string, final *Receipt, warn func(string, error)) (backups, versions map[string]bool, unsure bool) {
	backups, versions = map[string]bool{}, map[string]bool{}
	use := func(dir string) {
		if name, ok := strings.CutPrefix(dir, "versions/"); ok && !strings.Contains(name, "/") {
			versions[name] = true
		}
	}
	use(final.Directory)
	use(final.Engine)
	if name, ok := strings.CutPrefix(final.Previous, "backups/"); ok && windowsBackupName.MatchString(name) {
		backups[name] = true
		var previous *Receipt
		b, err := os.ReadFile(filepath.Join(base, "backups", name, "previous.json"))
		if err == nil {
			err = json.Unmarshal(b, &previous)
		}
		if err != nil {
			warn(filepath.Join(base, "backups", name), err)
			unsure = true
		} else if previous != nil {
			use(previous.Directory)
			use(previous.Engine)
		}
	}
	if c, err := enginedir.Read(base); err != nil {
		warn(filepath.Join(base, enginedir.FileName), err)
		unsure = true
	} else {
		for ; c != nil; c = c.Previous {
			use(c.Directory)
		}
	}
	return backups, versions, unsure
}

// pruneWindows 把 final 收據不需要的備份與版本目錄改名移開，回傳要刪的路徑（見 windowsKeep、pruneDir）。
// 呼叫端必須持有安裝鎖；真正刪除（removeTrash）可以在放開鎖之後做。
func pruneWindows(base string, final *Receipt) []string {
	if final == nil {
		return nil
	}
	backups, versions, unsure := windowsKeep(base, final, PruneWarn)
	trashed, err := pruneDir(filepath.Join(base, "backups"), windowsBackupName.MatchString, backups, nil)
	if err != nil {
		PruneWarn(filepath.Join(base, "backups"), err)
	}
	if unsure {
		return trashed
	}
	more, err := pruneDir(filepath.Join(base, "versions"), windowsOwnedVersion, versions, releasePrograms)
	if err != nil {
		PruneWarn(filepath.Join(base, "versions"), err)
	}
	return append(trashed, more...)
}

// windowsPrunable 回報依 final 收據有沒有東西可清（不改任何東西）。
func windowsPrunable(base string, final *Receipt) bool {
	if final == nil {
		return false
	}
	backups, versions, unsure := windowsKeep(base, final, func(string, error) {})
	return prunable(filepath.Join(base, "backups"), windowsBackupName.MatchString, backups) ||
		(!unsure && prunable(filepath.Join(base, "versions"), windowsOwnedVersion, versions))
}

// PruneWindows 依目前的收據清掉不再需要的備份與版本目錄（#179）。安裝、回復、解除安裝不再同步清理
// （第一次升級要刪的舊版可達數 GB，安裝精靈會停在進度條一分多鐘），改由它們成功後啟動的背景行程
// （0c-update prune）與每次排程喚起時呼叫。
//
// 沒有東西可清時不拿安裝鎖就回傳。要清時拿安裝鎖、重讀收據，只在鎖內決定要刪什麼並改名移開（很快），
// 放開鎖之後才真的刪：刪除數 GB 期間不擋安裝，行程在刪除中被結束（關機、登出）也不會留下安裝鎖。
// 安裝鎖被占用（正在安裝、回復）時回傳 ErrInstallLocked，什麼都不做，交給下一次。
func PruneWindows(base string) error {
	base, err := filepath.Abs(base)
	if err != nil {
		return err
	}
	file := filepath.Join(base, "install-receipt.json")
	r, err := readReceipt(file)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !windowsPrunable(base, r) {
		return nil
	}
	if _, err := os.Stat(filepath.Join(base, "update.lock")); err == nil {
		return ErrInstallLocked
	}
	unlock, err := lockInstall(base)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInstallLocked, err)
	}
	r, err = readReceipt(file)
	if err == nil {
		_, err = receiptDirectory(base, r)
	}
	var trashed []string
	if err == nil {
		trashed = pruneWindows(base, r)
	}
	unlock()
	removeTrash(trashed)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
