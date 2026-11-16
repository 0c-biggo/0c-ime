package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/0c-biggo/0c-ime/bridge/ipc"
	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/userlock"
)

// personal 指令（#301）：設定程式的「清除學習紀錄」。學到的資料（user.json）清成剛安裝時的樣子，升級前保存的
// 個人詞庫備份一起刪掉；自訂詞彙（vocab.json，連同備份裡的那一份）保留。
//
// 引擎在跑時它是唯一的寫入者，記憶體裡留著整份資料，存檔與結束時會整份寫回：直接刪檔會被寫回來（#297）。
// 所以交給引擎（IPC 的 reset_personal）清空並立刻存檔。引擎沒在跑時拿個人資料目錄的寫入者鎖（與 0c-server
// 同一把，internal/userlock）直接刪檔；拿到鎖之前剛好起來的引擎會等鎖，放開後才讀檔，讀到的是刪掉之後的。
// 認不得 reset_personal 的舊引擎（更新後還沒交接）先請它交接退出，再照沒在跑的做法處理。

// userStoreName 是個人資料目錄裡學到的資料（引擎的 -user 一律是這個檔名）。
const userStoreName = "user.json"

const personalUsage = "用法：0c-update personal reset [-json]（清除學習紀錄；自訂詞彙保留）"

// errResetBusy：引擎正在交接或組字、或剛好在啟動，暫時清不了；設定程式顯示「稍後再試」。
var errResetBusy = errors.New("輸入法引擎正在切換，請稍後再試")

// personalResetResult 是 personal reset -json 的輸出（docs/settings-contract.md）。
type personalResetResult struct {
	Schema int  `json:"schema"`
	OK     bool `json:"ok"`
	// Engine：running（執行中的引擎清空並存檔）、stopped（舊引擎先交接退出，再刪檔）、not_running（直接刪檔）。
	Engine string `json:"engine"`
	// BackupsRemoved：刪掉的升級前個人詞庫備份數。
	BackupsRemoved int `json:"backups_removed"`
}

func personalCommand(args []string) (resultErr error) {
	asJSON := false
	for _, a := range args {
		if isJSONFlag(a) {
			asJSON = true
		}
	}
	defer func() {
		if asJSON && resultErr != nil && !errors.Is(resultErr, errReported) {
			resultErr = failJSON(os.Stdout, resultErr)
		}
	}()
	flags := flag.NewFlagSet("personal", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Bool("json", false, "")
	if len(args) == 0 || args[0] != "reset" || flags.Parse(args[1:]) != nil || flags.NArg() != 0 {
		return usageError(personalUsage)
	}
	res, err := resetPersonal(context.Background(), dialEngine, basePath())
	if err != nil {
		return err
	}
	if asJSON {
		return emitJSON(os.Stdout, res)
	}
	fmt.Println("已清除學習紀錄；自訂詞彙保留。")
	if res.BackupsRemoved > 0 {
		fmt.Printf("一併刪除 %d 份升級前保存的個人詞庫備份。\n", res.BackupsRemoved)
	}
	return nil
}

// resetPersonal 清掉學到的資料，再刪升級前的個人詞庫備份。清不掉時不碰備份。
func resetPersonal(ctx context.Context, dial autoupdate.Dialer, base string) (personalResetResult, error) {
	how, err := resetPersonalStore(ctx, dial, base)
	if err != nil {
		return personalResetResult{}, err
	}
	n, err := removePersonalBackups(base)
	if err != nil {
		return personalResetResult{}, fmt.Errorf("已清除學習紀錄，但升級前保存的個人詞庫備份沒有刪完：%w", err)
	}
	return personalResetResult{Schema: contractSchema, OK: true, Engine: how, BackupsRemoved: n}, nil
}

// personalResetRetry：引擎正在交接、剛好在啟動（拿了鎖還沒開始服務）時，每次等一秒再試，最多這麼多次。
var personalResetRetry = 8

func resetPersonalStore(ctx context.Context, dial autoupdate.Dialer, base string) (string, error) {
	stopped := false
	for attempt := 0; ; attempt++ {
		info, running, err := autoupdate.Health(dial)
		var busy error
		switch {
		case !running:
			err := removeUserStore(base)
			if errors.Is(err, errEngineBusy) {
				busy = err // 引擎正在啟動：等它開始服務，交給它清
				break
			}
			if err != nil {
				return "", err
			}
			if stopped {
				return "stopped", nil
			}
			return "not_running", nil
		case err != nil:
			busy = err // 連得上但 health 沒有回覆：多半正在交接關閉
		case info["draining"] == "true":
			busy = errors.New("引擎正在交接")
		case info["personal_reset"] != "true" && !stopped:
			// 舊引擎認不得 reset_personal（更新後還沒交接）：請它在沒有組字時存檔退出，之後照沒在跑的做法刪檔。
			if err := autoupdate.Drain(ctx, dial, drainWait); err != nil {
				return "", fmt.Errorf("%w：%v", errResetBusy, err)
			}
			stopped = true
			continue
		case info["personal_reset"] != "true":
			busy = errors.New("交接後又啟動了不支援清除的舊引擎")
		default:
			err := ipcResetPersonal(dial)
			if !errors.Is(err, errResetBusy) {
				return "running", err
			}
			busy = err // 剛好開始交接
		}
		if attempt >= personalResetRetry {
			return "", fmt.Errorf("%w（%v）", errResetBusy, busy)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(personalResetWait):
		}
	}
}

// personalResetWait 是重試的間隔；測試會縮短它。
var personalResetWait = time.Second

// ipcResetPersonal 請執行中的引擎清空並存檔；引擎回報的錯誤原樣帶回（例如寫檔失敗）。
func ipcResetPersonal(dial autoupdate.Dialer) error {
	conn, err := dial(2 * time.Second)
	if err != nil {
		return fmt.Errorf("%w（%v）", errResetBusy, err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	if err := ipc.WriteFrame(conn, ipc.Envelope{Op: ipc.OpResetPersonal}); err != nil {
		return err
	}
	var r ipc.Reply
	if err := ipc.ReadFrame(conn, &r); err != nil {
		return err
	}
	if r.OK {
		return nil
	}
	if r.Error == "引擎正在切換" { // ipc.Server 交接中的回覆
		return errResetBusy
	}
	return errors.New(r.Error)
}

// removeUserStore 在寫入者鎖內刪掉 user.json 與中斷的存檔留下的 user.json.tmp。鎖被引擎拿著時回 errEngineBusy。
func removeUserStore(base string) error {
	if _, err := os.Stat(base); errors.Is(err, os.ErrNotExist) {
		return nil // 從來沒有個人資料
	}
	lock, err := userlock.TryLock(base)
	if errors.Is(err, userlock.ErrBusy) {
		return errEngineBusy
	}
	if err != nil {
		lock = nil // 檔案系統不支援鎖：照舊處理（同 0c-server 與 vocab 的做法）
	}
	defer lock.Release()
	user := filepath.Join(base, userStoreName)
	for _, p := range []string{user, user + ".tmp"} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// removePersonalBackups 刪掉升級前保存的個人詞庫（rollback -restore-user 用的），回傳刪了幾份。
//   - Linux：backups/<目錄>/personal/user.json（安裝的 0c-install-*、還原前另存的 0c-user-*、make upgrade 的 upgrade-*）
//   - Windows：backups\windows-*\user.json（每次安裝、升級、回復與移除）
//   - macOS：回復不另存個人詞庫，沒有這種備份。
//
// 備份裡的 vocab.json 與其他檔案保留。只進真的目錄，不跟著符號連結走到備份以外的地方。
// Windows 回復時中斷留下的 .user-restore-* 暫存檔（內容是備份的個人詞庫）也刪掉。
func removePersonalBackups(base string) (int, error) {
	var errs []error
	removed := 0
	remove := func(p string) {
		fi, err := os.Lstat(p)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
			}
			return
		}
		if fi.IsDir() {
			return
		}
		if err := os.Remove(p); err != nil {
			errs = append(errs, err)
			return
		}
		removed++
	}
	backups := filepath.Join(base, "backups")
	entries, err := os.ReadDir(backups)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	for _, e := range entries {
		if !e.IsDir() { // ReadDir 不跟著符號連結：連結不算目錄
			continue
		}
		dir := filepath.Join(backups, e.Name())
		remove(filepath.Join(dir, userStoreName))
		if fi, err := os.Lstat(filepath.Join(dir, "personal")); err == nil && fi.IsDir() {
			remove(filepath.Join(dir, "personal", userStoreName))
		}
	}
	stages, _ := filepath.Glob(filepath.Join(base, ".user-restore-*"))
	for _, p := range stages {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return removed, errors.Join(errs...)
}
