//go:build !windows && !darwin

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/enginedir"
	"github.com/0c-biggo/0c-ime/internal/update"
)

const (
	pathUpdaterRel = ".local/bin/0c-update"
	receiptRel     = ".local/share/zuin/install-receipt.json"
)

// refreshPathUpdater 讓 PATH 上的 ~/.local/bin/0c-update 跟上核心更新。
//
// 它屬於完整安裝（install.py 寫入並記在收據與該次備份的 transaction.json），核心更新只換排程用的穩定副本，
// 因此使用者在終端機打的 0c-update 會停在完整安裝的版本。條件都成立才換：
//   - 收據有效，且目前檔案與收據記錄相同（使用者沒改過）；
//   - 核心指標的版本比完整安裝新，穩定副本與該核心版本目錄內、已依套件清單核對的 0c-update 相同；
//
// 換檔前先改 transaction.json、換檔後再改收據；中途中斷時下一次執行會接著完成，回復與移除照常核對。
func refreshPathUpdater(base string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	if err := syncPathUpdater(base, home); err != nil {
		fmt.Fprintln(os.Stderr, "0c-update: 更新 ~/.local/bin/0c-update 失敗：", err)
	}
}

func syncPathUpdater(base, home string) error {
	lock, err := os.OpenFile(filepath.Join(base, "upgrade.lock"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return nil // 安裝工具正在執行，下次再做
	}
	receiptPath := filepath.Join(home, filepath.FromSlash(receiptRel))
	raw, err := os.ReadFile(receiptPath)
	if err != nil {
		return nil // 沒有完整安裝
	}
	var receipt map[string]any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err = d.Decode(&receipt); err != nil {
		return err
	}
	files, _ := receipt["files"].(map[string]any)
	recorded, _ := files[pathUpdaterRel].(string)
	version, _ := receipt["version"].(string)
	backup, _ := receipt["backup"].(string)
	if active, _ := receipt["active"].(bool); !active || recorded == "" || !update.ValidVersion(version) {
		return nil
	}
	core := autoupdate.PointerVersion(base)
	if core == "" {
		return nil
	}
	if order, err := update.CompareVersion(core, version); err != nil || order <= 0 {
		return nil
	}
	exe, _, ok := enginedir.Resolve(base)
	if !ok {
		return nil
	}
	stable := stableUpdater(base)
	want, err := regularHash(stable)
	if err != nil {
		return nil
	}
	if packaged, err := regularHash(filepath.Join(filepath.Dir(exe), updaterName())); err != nil || packaged != want {
		return nil // 穩定副本不是這個核心版本的 0c-update
	}
	target := filepath.Join(home, filepath.FromSlash(pathUpdaterRel))
	current, err := regularHash(target)
	if err != nil {
		return nil
	}
	if current == want && recorded == want {
		return nil
	}
	if current != recorded && current != want {
		return nil // 使用者改過，不動
	}
	files[pathUpdaterRel] = want
	next, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	next = append(next, '\n')
	sum := sha256.Sum256(raw)
	oldReceipt := hex.EncodeToString(sum[:])
	sum = sha256.Sum256(next)
	newReceipt := hex.EncodeToString(sum[:])
	if err = updateTransaction(home, backup, map[string][2]string{
		pathUpdaterRel: {recorded, want},
		receiptRel:     {oldReceipt, newReceipt},
	}); err != nil {
		return err
	}
	if current != want {
		if err = copyAtomic(stable, target); err != nil {
			return err
		}
	}
	return writeAtomic(receiptPath, next)
}

// updateTransaction 把該次完整安裝的回復紀錄中 after 由舊值改為新值；已是新值則略過，其他值表示紀錄不一致而中止。
func updateTransaction(home, backup string, changes map[string][2]string) error {
	backups := filepath.Join(home, ".local/share/zuin/backups")
	if backup == "" || filepath.Dir(filepath.Clean(backup)) != backups {
		return nil // 沒有可回復的備份
	}
	path := filepath.Join(backup, "transaction.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var transaction map[string]map[string]any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err = d.Decode(&transaction); err != nil {
		return err
	}
	changed := false
	for rel, c := range changes {
		entry, ok := transaction[rel]
		if !ok {
			continue
		}
		switch after, _ := entry["after"].(string); after {
		case c[1]:
		case c[0]:
			entry["after"], changed = c[1], true
		default:
			return fmt.Errorf("%s 的回復紀錄與目前檔案不符", rel)
		}
	}
	if !changed {
		return nil
	}
	next, err := json.MarshalIndent(transaction, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(next, '\n'))
}

func regularHash(path string) (string, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("%s 不是一般檔案", path)
	}
	return update.Hash(path)
}

func copyAtomic(src, dst string) error {
	st, err := os.Stat(dst)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".0c-update-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Chmod(st.Mode().Perm()); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

func writeAtomic(path string, data []byte) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".0c-json-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Chmod(st.Mode().Perm()); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
