package update

// 更新備份的清理（#179）。每次安裝、回復、解除安裝都把整個舊版移入備份（macOS 的 App 約 500 MB，Windows 的版本目錄
// 約 460 MB），以前從來不刪，越積越多。回復只用得到收據 previous 指到的那一份，所以每次成功之後只留它，其他的刪掉。
//
// 安全規則：
//   - 只在持有安裝鎖時、收據寫好之後清理；刪除失敗只記錄，不影響安裝、回復的結果，下次再試。
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

// pruneDir 刪除 root 底下 owned 認得、不在 keep 的目錄；使用中或刪不掉的交給 PruneWarn 記錄後保留。
// release 在刪除前確認目錄沒有在用（回傳錯誤就保留）；nil 表示不需確認。
// root 不存在時不做事；root 或上層是連結時回傳錯誤，不刪任何東西。
func pruneDir(root string, owned func(string) bool, keep map[string]bool, release func(string) error) error {
	if err := noLinks(root); err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || e.Type()&os.ModeSymlink != 0 {
			continue
		}
		path := filepath.Join(root, name)
		if rest, ok := strings.CutPrefix(name, pruneTrash); ok {
			// 上次改名移開後沒刪完的，已經沒有人會用到。
			if !owned(rest) {
				continue
			}
			if err := os.RemoveAll(path); err != nil {
				PruneWarn(path, err)
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
		if err := os.RemoveAll(trash); err != nil {
			PruneWarn(trash, err)
		}
	}
	return nil
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
	if err := pruneDir(backups, macBackupName.MatchString, kept, release); err != nil {
		PruneWarn(backups, err)
		return
	}
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

// pruneWindows 只保留 final 收據的 previous 備份，以及收據、回復目標與引擎指標用到的版本目錄。
// 個人詞庫備份只在 previous 那一份裡（回復時 -restore-user 用的就是它）。
func pruneWindows(base string, final *Receipt) {
	if final == nil {
		return
	}
	backups := map[string]bool{}
	versions := map[string]bool{}
	unsure := false
	use := func(dir string) {
		if name, ok := strings.CutPrefix(dir, "versions/"); ok && !strings.Contains(name, "/") {
			versions[name] = true
		}
	}
	use(final.Directory)
	use(final.Engine)
	if name, ok := strings.CutPrefix(final.Previous, "backups/"); ok && windowsBackupName.MatchString(name) {
		backups[name] = true
		// 讀不到回復目標就不知道該留哪個版本目錄：這次不清版本目錄。
		var previous *Receipt
		b, err := os.ReadFile(filepath.Join(base, "backups", name, "previous.json"))
		if err == nil {
			err = json.Unmarshal(b, &previous)
		}
		if err != nil {
			PruneWarn(filepath.Join(base, "backups", name), err)
			unsure = true
		} else if previous != nil {
			use(previous.Directory)
			use(previous.Engine)
		}
	}
	if c, err := enginedir.Read(base); err != nil {
		PruneWarn(filepath.Join(base, enginedir.FileName), err)
		unsure = true
	} else {
		for ; c != nil; c = c.Previous {
			use(c.Directory)
		}
	}
	if err := pruneDir(filepath.Join(base, "backups"), windowsBackupName.MatchString, backups, nil); err != nil {
		PruneWarn(filepath.Join(base, "backups"), err)
	}
	if unsure {
		return
	}
	if err := pruneDir(filepath.Join(base, "versions"), windowsOwnedVersion, versions, releasePrograms); err != nil {
		PruneWarn(filepath.Join(base, "versions"), err)
	}
}
