package update

// This transaction is OS independent for failure testing. The Windows command supplies
// real COM registration; tests supply a fake registrar and never touch the desktop.
import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"git.biggo.com/0c/ime/internal/enginedir"
)

type Registrar interface {
	Current() (string, error)
	Register(string) error
	Unregister(string) error
}
type Receipt struct {
	Schema    int               `json:"schema"`
	Product   string            `json:"product"`
	Version   string            `json:"version"`
	Directory string            `json:"directory"`
	Files     map[string]string `json:"files"`
	Previous  string            `json:"previous,omitempty"`
	Active    bool              `json:"active"`
	// Engine 是相容核心更新後 engine.json 指向的版本目錄；空值表示使用 DLL 相鄰的引擎。
	Engine string `json:"engine,omitempty"`

	// registered 是實際註冊的 DLL，只在與收據不符時由 installed 填入（不寫進收據）：nil 表示與收據一致，
	// 空字串表示切換時不要取消任何註冊（沒有註冊，或註冊屬於別的安裝而不動它）。foreign 是後者的註冊路徑。
	registered *string
	foreign    string
}

func noLinks(p string) error {
	for current := p; ; current = filepath.Dir(current) {
		st, err := os.Lstat(current)
		if err == nil && st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("installation path is a link: %s", current)
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
	}
}
func lockInstall(base string) (func(), error) {
	if err := noLinks(base); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(base, 0700); err != nil {
		return nil, err
	}
	file := filepath.Join(base, "update.lock")
	f, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("another update is running, or a prior update was interrupted; inspect %s: %w", file, err)
	}
	fmt.Fprintln(f, os.Getpid())
	f.Close()
	return func() { os.Remove(file) }, nil
}
func writeJSON(file string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(file), ".receipt-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), file)
}
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, st.Mode().Perm())
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	closeErr := out.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func receiptDirectory(base string, r *Receipt) (string, error) {
	if r.Schema != 1 || r.Product != "0c" || r.Directory == "" {
		return "", fmt.Errorf("invalid installation receipt")
	}
	if r.Directory != "." && (!SafeName(r.Directory) || !strings.HasPrefix(r.Directory, "versions/")) {
		return "", fmt.Errorf("invalid installed directory")
	}
	dir := filepath.Join(base, filepath.FromSlash(r.Directory))
	if err := noLinks(dir); err != nil {
		return "", err
	}
	return dir, nil
}
func readReceipt(file string) (*Receipt, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var r Receipt
	if err = json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}
func verifyReceipt(base string, r *Receipt) error {
	dir, err := receiptDirectory(base, r)
	if err != nil {
		return err
	}
	layout, err := WindowsLayoutOf(r.Files)
	if err != nil {
		return fmt.Errorf("invalid receipt: %w", err)
	}
	for _, name := range layout.names() {
		if !hashPattern.MatchString(r.Files[name]) {
			return fmt.Errorf("receipt missing %s", name)
		}
	}
	for name, expected := range r.Files {
		p, err := regular(dir, name)
		if err != nil {
			return err
		}
		sum, err := Hash(p)
		if err != nil {
			return err
		}
		if sum != expected {
			return fmt.Errorf("installed file changed: %s", name)
		}
	}
	return nil
}

// ErrForeignRegistration：輸入法註冊屬於別的安裝（例如其他 Windows 帳號），解除安裝沒有變更它。
var ErrForeignRegistration = errors.New("輸入法註冊屬於其他安裝，未變更")

// withinBase：路徑在這個帳號的安裝目錄（%LOCALAPPDATA%\Zuin）之下，也就是自己的某個版本。
func withinBase(base, p string) bool {
	rel, err := filepath.Rel(base, filepath.Clean(p))
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// installed 讀取收據並與實際的註冊核對。不一致時（#82，更新或移除中途中斷、同一帳號的舊版註冊）：
// 註冊指向自己安裝目錄裡的某個版本或沒有註冊，就以實際註冊為準繼續；指向別處（其他 Windows 帳號的安裝）時，
// 解除安裝（foreignOK）只停用自己的收據、不動那份註冊，安裝與回復則拒絕並說明註冊在哪裡。
func installed(base string, reg Registrar, foreignOK ...bool) (*Receipt, error) {
	current, err := reg.Current()
	if err != nil {
		return nil, err
	}
	receipt, err := readReceipt(filepath.Join(base, "install-receipt.json"))
	if err == nil {
		if _, e := receiptDirectory(base, receipt); e != nil {
			return nil, e
		}
		if _, e := WindowsLayoutOf(receipt.Files); e != nil {
			return nil, e
		}
		// 收據停用時 expected 為空字串；FixedSince 以後是固定路徑（#121）。
		expected, e := receiptDLL(base, receipt)
		if e != nil {
			return nil, e
		}
		if current == expected || current != "" && strings.EqualFold(filepath.Clean(current), expected) {
			return receipt, nil
		}
		switch {
		case current == "" || withinBase(base, current):
			actual := ""
			if current != "" {
				actual = filepath.Clean(current)
			}
			receipt.registered = &actual
		case len(foreignOK) > 0 && foreignOK[0]:
			none := ""
			receipt.registered, receipt.foreign = &none, current
		default:
			return nil, fmt.Errorf("輸入法目前註冊在 %s，不是這個帳號的安裝（可能是其他 Windows 帳號安裝的）；請先在那個帳號移除靈犀注音再試", current)
		}
		return receipt, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	if current == "" {
		return nil, nil
	}
	// Migrate the original fixed-location development installation without moving its files.
	// That layout predates the 0c program names.
	if !strings.EqualFold(filepath.Clean(current), filepath.Join(base, windowsLegacy.DLL)) {
		return nil, fmt.Errorf("existing registration belongs to a different installation: %s", current)
	}
	r := &Receipt{Schema: 1, Product: "0c", Version: "legacy", Directory: ".", Active: true, Files: map[string]string{}}
	for _, name := range windowsLegacy.names() {
		p, err := regular(base, name)
		if err != nil {
			return nil, err
		}
		r.Files[name], err = Hash(p)
		if err != nil {
			return nil, err
		}
	}
	return r, nil
}
func savePrevious(base string, r *Receipt) (string, error) {
	backups := filepath.Join(base, "backups")
	if err := noLinks(backups); err != nil {
		return "", err
	}
	if err := os.MkdirAll(backups, 0700); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(backups, "windows-")
	if err != nil {
		return "", err
	}
	if err = writeJSON(filepath.Join(dir, "previous.json"), r); err != nil {
		return "", err
	}
	// 個人詞庫與自訂詞彙不隨版本切換；每次切換前存一份，回復時可明確選擇還原。
	for _, name := range []string{UserStore, VocabStore} {
		if err = copyFile(filepath.Join(base, name), filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			return "", err
		}
	}
	rel, err := filepath.Rel(base, dir)
	return filepath.ToSlash(rel), err
}

// receiptDLL 是收據的版本應該註冊的 DLL：FixedSince 以後是固定路徑（#121），更舊的版本是版本目錄裡的 DLL。
func receiptDLL(base string, r *Receipt) (string, error) {
	if r == nil || !r.Active {
		return "", nil
	}
	if usesFixed(r) {
		if _, err := receiptDirectory(base, r); err != nil {
			return "", err
		}
		return FixedDLL(base), nil
	}
	dir, err := receiptDirectory(base, r)
	if err != nil {
		return "", err
	}
	layout, err := WindowsLayoutOf(r.Files)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, layout.DLL), nil
}
func switchRegistration(base string, previous, next *Receipt, reg Registrar) error {
	oldDLL, err := receiptDLL(base, previous)
	if err != nil {
		return err
	}
	if previous != nil && previous.registered != nil {
		oldDLL = *previous.registered // 以實際註冊為準（見 installed）
	}
	newDLL, err := receiptDLL(base, next)
	if err != nil {
		return err
	}
	// 固定路徑（#121）：被換下的版本名稱用來命名 .old；舊檔仍被應用程式載入時改名後留著。
	tag := func(r *Receipt) string {
		if r == nil {
			return ""
		}
		return filepath.Base(filepath.FromSlash(r.Directory))
	}
	oldFixed := previous != nil && previous.Active && usesFixed(previous) && oldDLL == FixedDLL(base)
	if oldDLL != "" {
		if err := reg.Unregister(oldDLL); err != nil {
			// Unregistration may have changed COM state before returning an error.
			return errors.Join(err, reg.Register(oldDLL))
		}
	}
	restore := func(cause error) error {
		if newDLL != "" {
			cause = errors.Join(cause, reg.Unregister(newDLL))
		}
		if oldFixed {
			cause = errors.Join(cause, DeployFixed(base, previous, tag(next)))
		}
		if oldDLL != "" {
			cause = errors.Join(cause, reg.Register(oldDLL))
		}
		return cause
	}
	if next != nil && next.Active && usesFixed(next) {
		if err := DeployFixed(base, next, tag(previous)); err != nil {
			return restore(err)
		}
	}
	if newDLL != "" {
		if err := reg.Register(newDLL); err != nil {
			return restore(err)
		}
	}
	if err := writeJSON(filepath.Join(base, "install-receipt.json"), next); err != nil {
		return restore(err)
	}
	if next == nil || !next.Active || !usesFixed(next) {
		// 解除安裝或回到不從固定路徑載入的舊版：移除固定檔；仍被載入的 .old 留到下次。
		RemoveFixed(base)
	}
	return applyEnginePointer(base, next)
}

// applyEnginePointer 讓 engine.json 與收據一致：完整安裝移除指標（DLL 相鄰的引擎），
// 回復到曾做過核心更新的狀態時寫回當時的指標。
func applyEnginePointer(base string, r *Receipt) error {
	if r == nil || r.Engine == "" {
		return enginedir.Remove(base)
	}
	return enginedir.Write(base, enginedir.Current{Directory: r.Engine})
}

// removeLegacy deletes zuin-* program generations that are neither active nor the rollback target.
// A DLL still loaded by a running application cannot be deleted; its directory is kept intact and
// retried by the next update. It returns the paths that remain.
func removeLegacy(base string, keep ...*Receipt) []string {
	kept := map[string]bool{}
	for _, r := range keep {
		if r != nil {
			kept[r.Directory] = true
		}
	}
	var left []string
	// Delete the DLL first: once it is gone nothing can start the old engine or candidate window.
	removePrograms := func(dir string) bool {
		for _, name := range windowsLegacy.names() {
			if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
				left = append(left, filepath.Join(dir, name))
				return false
			}
		}
		return true
	}
	if !kept["."] {
		removePrograms(base)
	}
	entries, err := os.ReadDir(filepath.Join(base, "versions"))
	if err != nil {
		return left
	}
	for _, entry := range entries {
		dir := filepath.Join(base, "versions", entry.Name())
		if kept["versions/"+entry.Name()] || !entry.IsDir() {
			continue
		}
		legacy := false
		for _, name := range windowsLegacy.names() {
			if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
				legacy = true
			}
		}
		if !legacy {
			continue
		}
		if removePrograms(dir) {
			if err := os.RemoveAll(dir); err != nil {
				left = append(left, dir)
			}
		}
	}
	return left
}

// InstallWindows installs a verified bundle and returns its directory and any old program files
// that could not be removed yet.
func InstallWindows(bundle, base, arch string, reg Registrar) (string, []string, error) {
	release, err := Verify(bundle, "windows", arch)
	if err != nil {
		return "", nil, err
	}
	base, err = filepath.Abs(base)
	if err != nil {
		return "", nil, err
	}
	unlock, err := lockInstall(base)
	if err != nil {
		return "", nil, err
	}
	defer unlock()
	previous, err := installed(base, reg)
	if err != nil {
		return "", nil, err
	}
	if previous != nil && previous.Active {
		if err = verifyReceipt(base, previous); err != nil {
			return "", nil, err
		}
	}
	manifestHash, err := Hash(filepath.Join(bundle, "release.json"))
	if err != nil {
		return "", nil, err
	}
	directory := "versions/" + release.Version + "-" + manifestHash[:12]
	next := &Receipt{Schema: 1, Product: "0c", Version: release.Version, Directory: directory, Files: release.Files, Active: true}
	destination := filepath.Join(base, filepath.FromSlash(directory))
	if err = noLinks(destination); err != nil {
		return "", nil, err
	}
	if err = os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return "", nil, err
	}
	// 先前中斷的安裝或清理可能留下不完整的版本目錄：核對不過就移開重裝，不讓它擋住安裝。
	if _, err = os.Stat(destination); err == nil {
		if _, verr := Verify(destination, "windows", arch); verr != nil {
			aside := fmt.Sprintf("%s.broken-%d", destination, time.Now().UnixNano())
			if err = os.Rename(destination, aside); err != nil {
				return "", nil, fmt.Errorf("版本目錄 %s 不完整且無法移開（可能仍被執行中的程式使用）：%w", directory, err)
			}
			os.RemoveAll(aside) // 仍被使用的檔案刪不掉就留著，不影響新目錄
		}
	}
	if _, err = os.Stat(destination); os.IsNotExist(err) {
		stage, e := os.MkdirTemp(filepath.Dir(destination), ".stage-")
		if e != nil {
			return "", nil, e
		}
		defer os.RemoveAll(stage)
		names := []string{"release.json"}
		for n := range release.Files {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, name := range names {
			if e = copyFile(filepath.Join(bundle, filepath.FromSlash(name)), filepath.Join(stage, filepath.FromSlash(name))); e != nil {
				return "", nil, e
			}
		}
		if _, e = Verify(stage, "windows", arch); e != nil {
			return "", nil, e
		}
		if e = os.Rename(stage, destination); e != nil {
			return "", nil, e
		}
	} else if err != nil {
		return "", nil, err
	}
	if err = verifyReceipt(base, next); err != nil {
		return "", nil, err
	}
	if previous != nil && previous.Active && previous.Directory == next.Directory && previous.registered == nil {
		// 同一版重新安裝：註冊已經正確，只補回固定路徑的檔案（可能被防毒軟體刪掉）。
		if usesFixed(next) {
			if err = DeployFixed(base, next, "reinstall"); err != nil {
				return "", nil, err
			}
		}
		return destination, removeLegacy(base, next, previous), nil
	}
	next.Previous, err = savePrevious(base, previous)
	if err != nil {
		return "", nil, err
	}
	if err = switchRegistration(base, previous, next, reg); err != nil {
		return "", nil, fmt.Errorf("registration failed; attempted to restore previous registration: %w", err)
	}
	return destination, removeLegacy(base, next, previous), nil
}

// UserStore is the personal dictionary shared by every installed version.
const UserStore = "user.json"

// VocabStore 是自訂詞彙（#122、#127），跟著 UserStore 備份與還原；不認得它的舊版照常輸入、不碰它。
const VocabStore = "vocab.json"

// restoreUserStore replaces the personal dictionary with the copy saved in a backup directory.
// The caller must make sure no engine is running, or it would write its own state back on exit.
// 備份裡有自訂詞彙時一併還原；沒有（升級前還沒加詞）時保留目前的檔案，不刪掉後來加的詞。
func restoreUserStore(base, backup string) error {
	if err := restoreStore(base, backup, UserStore); err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(base, filepath.FromSlash(backup), VocabStore)); os.IsNotExist(err) {
		return nil
	}
	return restoreStore(base, backup, VocabStore)
}

func restoreStore(base, backup, name string) error {
	saved := filepath.Join(base, filepath.FromSlash(backup), name)
	if err := noLinks(saved); err != nil {
		return err
	}
	stage, err := os.CreateTemp(base, ".user-restore-")
	if err != nil {
		return err
	}
	stage.Close()
	defer os.Remove(stage.Name())
	os.Remove(stage.Name())
	if err = copyFile(saved, stage.Name()); err != nil {
		return err
	}
	return os.Rename(stage.Name(), filepath.Join(base, name))
}

// RollbackWindows switches back to the version recorded before the current one, including a
// zuin-* release, and returns old program files that could not be removed yet. With restoreUser,
// the personal dictionary saved when the current version was installed replaces the current one;
// the current dictionary is kept in the new backup so that the restore can itself be undone.
func RollbackWindows(base string, reg Registrar, restoreUser bool) ([]string, error) {
	unlock, err := lockInstall(base)
	if err != nil {
		return nil, err
	}
	defer unlock()
	current, err := installed(base, reg)
	if err != nil {
		return nil, err
	}
	if current == nil || current.Previous == "" || !SafeName(current.Previous) || !strings.HasPrefix(current.Previous, "backups/windows-") {
		return nil, fmt.Errorf("no rollback receipt")
	}
	if restoreUser {
		if _, err = os.Lstat(filepath.Join(base, filepath.FromSlash(current.Previous), UserStore)); err != nil {
			return nil, fmt.Errorf("no personal dictionary was saved before this version: %w", err)
		}
	}
	if err = verifyReceipt(base, current); err != nil {
		return nil, err
	}
	previousFile := filepath.Join(base, filepath.FromSlash(current.Previous), "previous.json")
	if err = noLinks(previousFile); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(previousFile)
	if err != nil {
		return nil, err
	}
	var previous *Receipt
	if err = json.Unmarshal(data, &previous); err != nil {
		return nil, err
	}
	if previous == nil {
		return nil, fmt.Errorf("no previous version; use uninstall for the first installation")
	}
	if err = verifyReceipt(base, previous); err != nil {
		return nil, err
	}
	// The zuin-* generation predates the newer-format read/write guard. It
	// would silently discard fields when saving a newer personal dictionary.
	// Check the dictionary that will actually be used before changing receipts
	// or registration; an explicit restore is safe only with a legacy backup.
	layout, err := WindowsLayoutOf(previous.Files)
	if err != nil {
		return nil, err
	}
	if layout == windowsLegacy {
		store := filepath.Join(base, UserStore)
		if restoreUser {
			store = filepath.Join(base, filepath.FromSlash(current.Previous), UserStore)
		}
		if err = legacyRollbackStore(store); err != nil {
			return nil, err
		}
	}
	previous.Previous, err = savePrevious(base, current)
	if err != nil {
		return nil, err
	}
	if err = switchRegistration(base, current, previous, reg); err != nil {
		return nil, err
	}
	if restoreUser {
		if err = restoreUserStore(base, current.Previous); err != nil {
			return nil, fmt.Errorf("registration rolled back, but the personal dictionary was not restored (current copy kept in %s): %w", previous.Previous, err)
		}
	}
	return removeLegacy(base, previous, current), nil
}

func legacyRollbackStore(file string) error {
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
	var header struct{ Format int }
	if err := json.Unmarshal(b, &header); err != nil {
		return fmt.Errorf("無法確認舊版個人詞庫格式，拒絕回復至 zuin-*：%w", err)
	}
	if header.Format != 0 {
		return fmt.Errorf("舊版 zuin-* 沒有較新格式保護，無法安全使用個人詞庫格式 %d；請保留新版，或完成組字並結束引擎後使用 rollback -restore-user 還原相容的升級前備份（目前詞庫會另存）", header.Format)
	}
	return nil
}

// UninstallWindows 取消註冊並停用收據；版本檔案、備份與個人詞庫保留。
// 解除安裝只取消註冊，不需要檔案完好：已安裝的檔案被改動或移除（例如防毒軟體隔離 DLL）時照常完成，
// 否則使用者永遠移除不了（#82）。回傳的 changed 是核對失敗的原因（沒有則為 nil），由呼叫端提示。
func UninstallWindows(base string, reg Registrar) (changed error, err error) {
	unlock, err := lockInstall(base)
	if err != nil {
		return nil, err
	}
	defer unlock()
	current, err := installed(base, reg, true)
	if err != nil {
		return nil, err
	}
	if current != nil && current.foreign != "" {
		changed = fmt.Errorf("%w：%s", ErrForeignRegistration, current.foreign)
	}
	if current == nil || !current.Active {
		// 已停用的收據但還留著自己舊版本的註冊：一併取消。
		if current != nil && current.registered != nil && *current.registered != "" {
			return changed, reg.Unregister(*current.registered)
		}
		return changed, nil
	}
	if bad := verifyReceipt(base, current); bad != nil {
		changed = errors.Join(changed, bad)
	}
	next := *current
	next.Active = false
	next.Previous, err = savePrevious(base, current)
	if err != nil {
		return changed, err
	}
	return changed, switchRegistration(base, current, &next, reg)
}
