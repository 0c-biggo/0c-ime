package update

// macOS 安裝：InputMethodKit 只從 ~/Library/Input Methods 載入輸入法，App 內附引擎、配對字典與神經模型，
// 因此整個 0c.app 是一個版本單位。新版先在支援目錄暫存並逐檔核對，再以改名切換；舊 App 移入備份供回復。
// 0.1.0-dev.21 以前的 App 叫 Zuin.app（bundle ID org.zuin.inputmethod.Zuin），升級時一併移入備份，回復時照原名放回。
// 邏輯與作業系統無關，測試以暫存目錄進行。
import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/0c-biggo/0c-ime/internal/userlock"
)

// MacApp 是套件內與安裝後的 App 名稱；LegacyMacApp 是 0.1.0-dev.21 以前的名稱，只用於升級與回復。
const (
	MacApp       = "0c.app"
	LegacyMacApp = "Zuin.app"
)

var macApps = []string{MacApp, LegacyMacApp}

// MacReceipt 記錄目前安裝的 App 與可回復的上一版。
type MacReceipt struct {
	Schema      int               `json:"schema"`
	Product     string            `json:"product"`
	Version     string            `json:"version"`
	App         string            `json:"app,omitempty"` // 空值為 0.1.0-dev.21 以前的收據，App 是 Zuin.app
	Files       map[string]string `json:"files"`         // 相對於 App 的路徑
	Previous    string            `json:"previous,omitempty"`
	Active      bool              `json:"active"`
	SessionASID uint64            `json:"session_asid,omitempty"` // 安裝時的 Aqua 工作階段；0 表示舊收據或未知
}

func (r *MacReceipt) appName() string {
	if r.App == "" {
		return LegacyMacApp
	}
	return r.App
}

// MacPaths 是安裝位置：App 目的地與存放收據、備份的支援目錄。
type MacPaths struct {
	InputMethods, Support string
	SessionASID           uint64 // 由平台觀察，與收據一起原子保存；不讀寫個人詞庫。
	// InUse 回報備份目錄（絕對路徑）是否仍在使用：有程式從裡面執行或開著其中的檔案，或系統的背景項目登記指向裡面的 App。
	// 使用中的備份不清理（#179）；nil 表示都沒有在用（測試）。
	InUse func(dir string) bool
	// PrepareRollback 在回復持有安裝鎖、核對備份之前，對要放回的 App（備份裡的絕對路徑）呼叫一次：平台把為了背景項目
	// 改過名的檔案改回來（#205），放回的 App 與收據一致。nil 表示不需要。
	PrepareRollback func(app string)
}

func (p MacPaths) app() string                 { return filepath.Join(p.InputMethods, MacApp) }
func (p MacPaths) appNamed(name string) string { return filepath.Join(p.InputMethods, name) }
func (p MacPaths) receipt() string             { return filepath.Join(p.Support, "install-receipt.json") }

func appFiles(r *Release) map[string]string {
	files := map[string]string{}
	for name, sum := range r.Files {
		if rest, ok := strings.CutPrefix(name, MacApp+"/"); ok {
			files[rest] = sum
		}
	}
	return files
}

// verifyTree 要求 dir 內的一般檔案正好是 files 所列，且雜湊相符；不接受連結或額外檔案。
func verifyTree(dir string, files map[string]string) error {
	seen := 0
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		name := filepath.ToSlash(rel)
		if !d.Type().IsRegular() {
			return fmt.Errorf("non-regular app entry: %s", name)
		}
		expected, ok := files[name]
		if !ok {
			return fmt.Errorf("unexpected app file: %s", name)
		}
		sum, err := Hash(p)
		if err != nil {
			return err
		}
		if sum != expected {
			return fmt.Errorf("app file changed: %s", name)
		}
		seen++
		return nil
	})
	if err != nil {
		return err
	}
	if seen != len(files) {
		return fmt.Errorf("app is missing files")
	}
	return nil
}

// macBundleIDs 是靈犀注音 App 的 bundle ID；org.zuin.inputmethod.Zuin 是 0.1.0-dev.21 以前的 Zuin.app。
var macBundleIDs = map[string]bool{"com.biggo.inputmethod.0c": true, "org.zuin.inputmethod.Zuin": true}

var plistBundleID = regexp.MustCompile(`<key>\s*CFBundleIdentifier\s*</key>\s*<string>([^<]*)</string>`)

// MacAppChanges 是收據記錄的 App 與實際內容不符之處。確認是自己的安裝時只提示，不擋更新、回復與移除（#83）。
type MacAppChanges struct {
	App    string   // App 路徑
	Absent bool     // 整個 App 不存在
	Files  []string // 依路徑排序，例如「已改動 Contents/MacOS/0c-server」
}

func (c *MacAppChanges) Error() string {
	if c.Absent {
		return fmt.Sprintf("%s 不存在", filepath.Base(c.App))
	}
	const limit = 10
	list := c.Files
	more := ""
	if len(list) > limit {
		list, more = list[:limit], fmt.Sprintf(" 等 %d 項", len(c.Files))
	}
	return fmt.Sprintf("%s 與安裝收據不符：%s%s", filepath.Base(c.App), strings.Join(list, "、"), more)
}

// ownMacApp 確認 dir 是靈犀注音的 App：Info.plist 與收據相符，或它的 CFBundleIdentifier 是靈犀注音的。
// Info.plist 不見、不是一般檔案或讀不出 bundle ID（例如改成二進位格式）時無法確認，回傳錯誤。
func ownMacApp(dir string, files map[string]string) error {
	plist := filepath.Join(dir, "Contents", "Info.plist")
	info, err := os.Lstat(plist)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("cannot confirm %s is 0c's app: Info.plist is missing or not a regular file", dir)
	}
	if expected := files["Contents/Info.plist"]; expected != "" {
		if sum, e := Hash(plist); e == nil && sum == expected {
			return nil
		}
	}
	if info.Size() > 1<<20 {
		return fmt.Errorf("cannot confirm %s is 0c's app: Info.plist is too large", dir)
	}
	b, err := os.ReadFile(plist)
	if err != nil {
		return err
	}
	id := ""
	if m := plistBundleID.FindSubmatch(b); m != nil {
		id = strings.TrimSpace(string(m[1]))
	}
	if !macBundleIDs[id] {
		return fmt.Errorf("%s is not 0c's app (bundle ID %q)", dir, id)
	}
	return nil
}

// checkMacApp 核對收據記錄的 App。App 是真正的目錄且確認是靈犀注音的（bundle ID）時，檔案被改動、缺少、
// 多出或變成連結只列在 changed（沒有時為 nil），由呼叫端提示：更新、回復與移除都是把整個 App 移入備份，
// 原本的內容不會遺失（#83：使用者改過 0c-server 並重新簽章後，自動更新永遠卡住）。
// App 本身是連結或不是目錄、不是靈犀注音的 App、無法確認身分時回傳 err，不動它。
// App 整個不存在時 changed 標示 Absent，沒有東西會被覆蓋，由呼叫端決定是否繼續。內容中的連結一律不跟隨。
func checkMacApp(dir string, files map[string]string) (changed error, err error) {
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return &MacAppChanges{App: dir, Absent: true}, nil
	}
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("installed app is a link: %s", dir)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("installed app is not a directory: %s", dir)
	}
	if err = ownMacApp(dir, files); err != nil {
		return nil, err
	}
	diffs := map[string]string{}
	seen := map[string]bool{}
	err = filepath.WalkDir(dir, func(p string, d os.DirEntry, e error) error {
		if p == dir {
			return e
		}
		rel, _ := filepath.Rel(dir, p)
		name := filepath.ToSlash(rel)
		switch {
		case e != nil:
			diffs[name] = "無法讀取"
		case d.IsDir():
		case !d.Type().IsRegular():
			seen[name] = true
			diffs[name] = "不是一般檔案"
		default:
			expected, ok := files[name]
			if !ok {
				diffs[name] = "多出"
				break
			}
			seen[name] = true
			if sum, err := Hash(p); err != nil {
				diffs[name] = "無法讀取"
			} else if sum != expected {
				diffs[name] = "已改動"
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for name := range files {
		if !seen[name] {
			if _, ok := diffs[name]; !ok {
				diffs[name] = "缺少"
			}
		}
	}
	if len(diffs) == 0 {
		return nil, nil
	}
	c := &MacAppChanges{App: dir}
	for name, kind := range diffs {
		c.Files = append(c.Files, kind+" "+name)
	}
	sort.Slice(c.Files, func(i, j int) bool {
		return strings.SplitN(c.Files[i], " ", 2)[1] < strings.SplitN(c.Files[j], " ", 2)[1]
	})
	return c, nil
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("non-regular app entry: %s", rel)
		}
		return copyFile(p, target)
	})
}

func readMacReceipt(file string) (*MacReceipt, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var r MacReceipt
	if err = json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	if r.Schema != 1 || r.Product != "0c" || !validMacApp(r.App) {
		return nil, fmt.Errorf("invalid installation receipt")
	}
	return &r, nil
}

// validMacApp：收據的 App 欄位只能是兩個已知名稱（空值為 Zuin.app），不能指到 Input Methods 以外。
func validMacApp(name string) bool { return name == "" || name == MacApp || name == LegacyMacApp }

// backupApp 把目前的 App 與收據移入新的備份目錄，回傳相對於支援目錄的路徑。
// 收據寫好之後由 pruneMacBackups 只留收據 previous 指到的那一份（#179）。
func backupApp(p MacPaths, current *MacReceipt) (string, error) {
	backups := filepath.Join(p.Support, "backups")
	if err := noLinks(backups); err != nil {
		return "", err
	}
	if err := os.MkdirAll(backups, 0o700); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(backups, "macos-"+time.Now().UTC().Format("20060102-150405")+"-")
	if err != nil {
		return "", err
	}
	if err = writeJSON(filepath.Join(dir, "previous.json"), current); err != nil {
		return "", err
	}
	for _, name := range macApps {
		if _, err = os.Lstat(p.appNamed(name)); err == nil {
			if err = os.Rename(p.appNamed(name), filepath.Join(dir, name)); err != nil {
				return "", err
			}
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	rel, err := filepath.Rel(p.Support, dir)
	return filepath.ToSlash(rel), err
}

// installedMac 讀取收據並核對目前 App；沒有收據時接受 0c 以前的原型 App（整包移入備份即可回復）。
// 收據啟用中時 App 必須確認是靈犀注音的（見 checkMacApp），否則拒絕覆寫；內容與收據不符只回報在 changed。
func installedMac(p MacPaths) (r *MacReceipt, changed error, err error) {
	r, err = readMacReceipt(p.receipt())
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if r.Active {
		if changed, err = checkMacApp(p.appNamed(r.appName()), r.Files); err != nil {
			return nil, nil, fmt.Errorf("installed app is not this account's 0c installation; refusing to overwrite: %w", err)
		}
	}
	return r, changed, nil
}

// CheckMac 只核對目前安裝的 App 與收據、不做變更，回傳不符之處（沒有時為 nil）；供 status 與狀態摘要說明。
func CheckMac(p MacPaths) (changed error, err error) {
	_, changed, err = installedMac(p)
	return changed, err
}

// InstallMac 安裝已核對的套件並回傳 App 路徑；上一個 App（含原型）保留在備份，可用 RollbackMac 回復。
// 原本的 App 確認是自己的安裝、但檔案被改動或缺少時照常更新（整個 App 換新、舊的移入備份），
// 不符之處回傳在 changed（沒有時為 nil），由呼叫端提示。
func InstallMac(bundle, arch string, p MacPaths) (app string, changed error, err error) {
	release, err := Verify(bundle, "darwin", arch)
	if err != nil {
		return "", nil, err
	}
	for _, dir := range []string{p.InputMethods, p.Support} {
		if err = noLinks(dir); err != nil {
			return "", nil, err
		}
	}
	unlock, err := lockInstall(p.Support)
	if err != nil {
		return "", nil, err
	}
	defer unlock()
	current, changed, err := installedMac(p)
	if err != nil {
		return "", nil, err
	}
	files := appFiles(release)
	if err = os.MkdirAll(p.InputMethods, 0o755); err != nil {
		return "", nil, err
	}
	stage, err := os.MkdirTemp(p.Support, ".stage-")
	if err != nil {
		return "", nil, err
	}
	defer os.RemoveAll(stage)
	staged := filepath.Join(stage, MacApp)
	if err = copyTree(filepath.Join(bundle, MacApp), staged); err != nil {
		return "", nil, err
	}
	if err = verifyTree(staged, files); err != nil {
		return "", nil, err
	}
	previous, err := backupApp(p, current)
	if err != nil {
		return "", nil, err
	}
	restore := func(cause error) error { return fmtJoin(cause, restoreApps(p, previous)) }
	if err = os.Rename(staged, p.app()); err != nil {
		return "", nil, restore(err)
	}
	next := &MacReceipt{Schema: 1, Product: "0c", Version: release.Version, App: MacApp, Files: files, Previous: previous, Active: true, SessionASID: p.SessionASID}
	if err = writeJSON(p.receipt(), next); err != nil {
		_ = os.RemoveAll(p.app())
		return "", nil, restore(err)
	}
	pruneMacBackups(p, previous)
	return p.app(), changed, nil
}

// restoreApps 把備份目錄內的 App（任一名稱）放回 Input Methods。
func restoreApps(p MacPaths, backup string) error {
	var errs error
	for _, name := range macApps {
		saved := filepath.Join(p.Support, filepath.FromSlash(backup), name)
		if _, e := os.Lstat(saved); e == nil {
			if e = os.Rename(saved, p.appNamed(name)); e != nil {
				errs = fmtJoin(e, errs)
			}
		}
	}
	return errs
}

func fmtJoin(a, b error) error {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	return fmt.Errorf("%w; restore: %v", a, b)
}

// loadMacBackup 讀取備份的收據並核對備份中的 App。與收據不符但確認是靈犀注音的 App 時照常回復，
// 不符之處回傳在 changed；備份裡沒有 App、App 是連結或不是靈犀注音的則拒絕。
func loadMacBackup(p MacPaths, rel string) (previous *MacReceipt, changed error, err error) {
	if rel == "" || !SafeName(rel) || !strings.HasPrefix(rel, "backups/macos-") {
		return nil, nil, fmt.Errorf("no rollback receipt")
	}
	file := filepath.Join(p.Support, filepath.FromSlash(rel), "previous.json")
	if err = noLinks(file); err != nil {
		return nil, nil, err
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, nil, err
	}
	if err = json.Unmarshal(b, &previous); err != nil {
		return nil, nil, err
	}
	if previous != nil && !validMacApp(previous.App) {
		return nil, nil, fmt.Errorf("invalid rollback receipt")
	}
	if previous != nil && previous.Active {
		app := filepath.Join(p.Support, filepath.FromSlash(rel), previous.appName())
		if changed, err = checkMacApp(app, previous.Files); err != nil {
			return nil, nil, fmt.Errorf("rollback app is not 0c's: %w", err)
		}
		if c, ok := changed.(*MacAppChanges); ok && c.Absent {
			return nil, nil, fmt.Errorf("no rollback app: %s", app)
		}
	}
	return previous, changed, nil
}

// MacRollback 是回復結果：放回的 App（沒有時為空）與目前 App 移入的備份目錄。
type MacRollback struct {
	App    string // 放回後的 App 路徑；上一版沒有 App 時為空
	Backup string // 回復前的 App 所在備份目錄（絕對路徑）
	// Changed：回復前的 App 與收據不符之處；Restored：放回的 App 與它的收據不符之處（都只是提示，沒有時為 nil）。
	Changed, Restored error
}

// RollbackMac 換回上一個 App（含 0c 以前的原型與 Zuin.app）；目前的 App 移入新備份，可再回復。
// 目前或備份中的 App 檔案被改動時照常回復，不符之處記在結果中。
func RollbackMac(p MacPaths) (MacRollback, error) {
	var result MacRollback
	unlock, err := lockInstall(p.Support)
	if err != nil {
		return result, err
	}
	defer unlock()
	current, changed, err := installedMac(p)
	if err != nil {
		return result, err
	}
	if current == nil {
		return result, fmt.Errorf("no rollback receipt")
	}
	if p.PrepareRollback != nil && SafeName(current.Previous) && strings.HasPrefix(current.Previous, "backups/macos-") {
		for _, name := range macApps {
			if app := filepath.Join(p.Support, filepath.FromSlash(current.Previous), name); noLinks(app) == nil {
				p.PrepareRollback(app)
			}
		}
	}
	previous, restored, err := loadMacBackup(p, current.Previous)
	if err != nil {
		return result, err
	}
	saved := current.Previous
	back, err := backupApp(p, current)
	if err != nil {
		return result, err
	}
	if err = restoreApps(p, saved); err != nil {
		return result, fmtJoin(err, restoreApps(p, back))
	}
	next := previous
	if next == nil {
		// 上一版沒有收據（原型或首次安裝前）：保留回復紀錄，讓這次回復也能撤回。
		next = &MacReceipt{Schema: 1, Product: "0c", Version: "legacy", Files: map[string]string{}, Active: false}
	}
	next.Previous = back
	if err = writeJSON(p.receipt(), next); err != nil {
		return result, err
	}
	// 剛放回的那份備份只剩收據，連同更早的一起清掉；留下剛移入的目前版本，可再回復。
	pruneMacBackups(p, back)
	result.Backup = filepath.Join(p.Support, filepath.FromSlash(back))
	result.Changed, result.Restored = changed, restored
	if c, ok := restored.(*MacAppChanges); ok {
		c.App = p.appNamed(next.appName()) // 提示放回後的位置
	}
	for _, name := range macApps {
		if _, e := os.Lstat(p.appNamed(name)); e == nil {
			result.App = p.appNamed(name)
			break
		}
	}
	return result, nil
}

// MacRollbackTarget 回傳 RollbackMac 會放回的 App（仍在備份目錄中的路徑）；沒有可回復的版本時回傳錯誤。
// 呼叫端用它在搬移之前判斷回復後的 App 是否內附自動更新排程。
func MacRollbackTarget(p MacPaths) (string, error) {
	current, _, err := installedMac(p)
	if err != nil {
		return "", err
	}
	if current == nil || current.Previous == "" || !SafeName(current.Previous) {
		return "", fmt.Errorf("no rollback receipt")
	}
	for _, name := range macApps {
		saved := filepath.Join(p.Support, filepath.FromSlash(current.Previous), name)
		if _, e := os.Lstat(saved); e == nil {
			return saved, nil
		}
	}
	return "", fmt.Errorf("no rollback app")
}

// UninstallMac 把 App 移入備份並停用收據；個人詞庫、設定與備份保留，可用 RollbackMac 恢復。
// App 的檔案被改動或缺少時照常移除（比照 Windows #82），不符之處回傳在 changed。
func UninstallMac(p MacPaths) (changed error, err error) {
	unlock, err := lockInstall(p.Support)
	if err != nil {
		return nil, err
	}
	defer unlock()
	current, changed, err := installedMac(p)
	if err != nil {
		return nil, err
	}
	if current == nil || !current.Active {
		return nil, nil
	}
	back, err := backupApp(p, current)
	if err != nil {
		return changed, err
	}
	next := *current
	next.Active = false
	next.Previous = back
	if err = writeJSON(p.receipt(), &next); err != nil {
		return changed, err
	}
	pruneMacBackups(p, back)
	return changed, nil
}

// MigrateMacSupport 把 0.1.0-dev.21 以前的支援目錄 legacy（Application Support/Zuin）改名為 current，
// 並在原位置留下指向新目錄的相對符號連結：升級當下仍在執行的舊引擎與回復後的 Zuin.app 讀寫同一份個人詞庫。
// 已搬過（legacy 是連結）或沒有舊目錄時不做事；兩個目錄都有內容時拒絕，不合併個人資料。
func MigrateMacSupport(legacy, current string) (bool, error) {
	info, err := os.Lstat(legacy)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, nil
	}
	if !info.IsDir() {
		return false, fmt.Errorf("legacy support path is not a directory: %s", legacy)
	}
	if entries, e := os.ReadDir(current); e == nil {
		// 新引擎啟動時會留下寫入者鎖檔（#120）。只有它而且沒人持有時，目錄仍算空的；
		// 有人持有表示引擎正在使用這個目錄，照樣拒絕。
		if len(entries) == 1 && entries[0].Name() == userlock.FileName {
			l, err := userlock.TryLock(current)
			if err != nil {
				return false, fmt.Errorf("%s is in use by a running engine: %w", current, err)
			}
			l.Release()
			if err := os.Remove(filepath.Join(current, userlock.FileName)); err != nil {
				return false, err
			}
			entries = nil
		}
		if len(entries) > 0 {
			return false, fmt.Errorf("both %s and %s exist; refusing to merge personal data", legacy, current)
		}
		if e = os.Remove(current); e != nil {
			return false, e
		}
	} else if !os.IsNotExist(e) {
		return false, e
	}
	if err = os.MkdirAll(filepath.Dir(current), 0o755); err != nil {
		return false, err
	}
	if err = os.Rename(legacy, current); err != nil {
		if _, e := os.Lstat(legacy); os.IsNotExist(e) {
			return false, nil // 另一個程序剛搬完
		}
		return false, err
	}
	target, err := filepath.Rel(filepath.Dir(legacy), current)
	if err != nil {
		target = current
	}
	if err = os.Symlink(target, legacy); err != nil {
		return true, fmt.Errorf("moved %s but could not leave a link: %w", legacy, err)
	}
	return true, nil
}
