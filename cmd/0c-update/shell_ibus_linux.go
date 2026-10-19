//go:build !windows && !darwin

package main

// IBus 外殼（0c-ibus）的不登出更新。與 Fcitx5 相同的組字保護（org.biggo.ime.Update1）與引擎交接，
// 差別在外殼由 ibus-daemon 啟動：更新前請外殼在閒置時自行退出，替換檔案後以 SetGlobalEngine
// 讓 ibus-daemon 重新叫起它。外殼退出後 ibus-daemon 不會自己重啟它，所以每條結束路徑（包括失敗）
// 都要恢復，否則按鍵會直接進應用程式。

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"git.biggo.com/0c/ime/internal/autoupdate"
	"git.biggo.com/0c/ime/internal/update"
	"github.com/godbus/dbus/v5"
)

const (
	frontendIBus  = "ibus"
	ibusEngine    = "0c"
	ibusComponent = ".local/share/ibus/component/0c.xml"
	ibusShellPath = ".local/bin/0c-ibus"
)

func homePath(relative string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, relative)
}

// ibusRegistered：這位使用者以 0c-ibus register 把靈犀注音登錄到 IBus。
func ibusRegistered() bool {
	_, err := os.Stat(homePath(ibusComponent))
	return err == nil
}

func ibusShells() ([]int, error) { return inputProcesses("0c-ibus") }

func waitIBusStopped(ctx context.Context) error {
	deadline := time.Now().Add(10 * time.Second)
	for {
		pids, err := ibusShells()
		if err != nil {
			return err
		}
		if len(pids) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("IBus 外殼未完成交接；未替換安裝檔案")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// ibusAddresses 是可嘗試的 ibus-daemon 位址：外殼回報的優先，其次是 ~/.config/ibus/bus 下最近寫入的位址檔
// （排程服務的環境不一定有 WAYLAND_DISPLAY，算不出 libibus 用的檔名）。
func ibusAddresses(saved string) []string {
	var out []string
	if saved != "" {
		out = append(out, saved)
	}
	config := os.Getenv("XDG_CONFIG_HOME")
	if config == "" {
		config = homePath(".config")
	}
	entries, _ := os.ReadDir(filepath.Join(config, "ibus/bus"))
	type file struct {
		path string
		mod  time.Time
	}
	var files []file
	for _, e := range entries {
		if info, err := e.Info(); err == nil && info.Mode().IsRegular() {
			files = append(files, file{filepath.Join(config, "ibus/bus", e.Name()), info.ModTime()})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	for _, f := range files {
		b, err := os.ReadFile(f.path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			if a, ok := strings.CutPrefix(line, "IBUS_ADDRESS="); ok && a != "" && a != saved {
				out = append(out, a)
			}
		}
	}
	return out
}

// engineName 取出 IBusEngineDesc（序列化物件：型別名稱、附件、名稱…）的名稱。
func engineName(v dbus.Variant) string {
	fields, ok := v.Value().([]interface{})
	if !ok || len(fields) < 3 {
		return ""
	}
	name, _ := fields[2].(string)
	return name
}

// restoreIBus 在外殼退出前它是選用中的輸入法時，請 ibus-daemon 重新叫起它。使用者在更新期間已改用
// 其他輸入法、或回復到沒有 IBus 外殼的版本（登錄已撤銷）時不叫起，並把 s.IM 清掉：之後只核對安裝的檔案。
func restoreIBus(ctx context.Context, s *shellRestart) error {
	if s.IM != ibusEngine {
		return nil
	}
	if !ibusRegistered() {
		s.IM = ""
		return nil
	}
	var last error = fmt.Errorf("找不到 ibus-daemon")
	for _, address := range ibusAddresses(s.Address) {
		conn, err := dbus.Connect(address)
		if err != nil {
			last = err
			continue
		}
		obj := conn.Object("org.freedesktop.IBus", "/org/freedesktop/IBus")
		var current dbus.Variant
		if obj.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, "org.freedesktop.IBus", "GlobalEngine").Store(&current) == nil {
			if name := engineName(current); name != "" && name != ibusEngine {
				conn.Close()
				s.IM = ""
				return nil
			}
		}
		err = obj.CallWithContext(ctx, "org.freedesktop.IBus.SetGlobalEngine", 0, ibusEngine).Err
		conn.Close()
		if err == nil {
			return nil
		}
		last = err
	}
	return fmt.Errorf("無法重新啟動 IBus 的靈犀注音：%w", last)
}

// ibusShellMatches 核對外殼：已叫起時看它回報的版本，沒有在用時看安裝的檔案。
func ibusShellMatches(ctx context.Context, conn *dbus.Conn, s *shellRestart, r *update.Release) error {
	if s.IM != ibusEngine {
		want := r.Files["bin/0c-ibus"]
		if want == "" {
			return nil
		}
		if got, err := update.Hash(homePath(ibusShellPath)); err != nil || got != want {
			return fmt.Errorf("IBus 外殼檔案與套件不符")
		}
		return nil
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		status, err := guardStatus(ctx, conn)
		if err == nil && status.Version == r.Version {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("IBus 外殼版本 %q，預期 %s：%v", status.Version, r.Version, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// stopIBusShell 請執行中的外殼在沒有組字、閒置夠久時退出，並記下更新後是否要重新叫起它。
// 回復途中（rolling back）受阻時保留 journal，下一次喚起接續回復。
func (p *system) stopIBusShell(ctx context.Context, conn *dbus.Conn, restart *shellRestart, idle time.Duration, rollingBack bool) error {
	pids, err := ibusShells()
	if err != nil {
		return err
	}
	if len(pids) > 1 {
		return fmt.Errorf("有多個 IBus 工作階段在使用靈犀注音，請保留一個後再更新")
	}
	if len(pids) == 0 {
		return nil
	}
	if info, running, e := autoupdate.Health(dialEngine); e != nil {
		return fmt.Errorf("%w：無法確認引擎狀態", autoupdate.ErrShellBusy)
	} else if running && info["composing"] != "0" {
		return fmt.Errorf("%w：請先完成組字，稍後會自動再試", autoupdate.ErrShellBusy)
	}
	callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	status, err := guardStatus(callCtx, conn)
	cancel()
	if err != nil || status.Frontend != frontendIBus {
		return fmt.Errorf("%w：IBus 外殼沒有回應更新保護（%v）", autoupdate.ErrShellBusy, err)
	}
	if status.Busy {
		return fmt.Errorf("%w：IBus 尚有未完成組字", autoupdate.ErrShellBusy)
	}
	if status.Active {
		restart.IM = ibusEngine
	}
	restart.Address = status.IBusAddress
	if !rollingBack {
		restart.Phase = "stopping"
	}
	if err = saveShellRestart(p.base, restart); err != nil {
		return err
	}
	var answer string
	callCtx, cancel = context.WithTimeout(ctx, 3*time.Second)
	err = conn.Object(guardName, "/update").CallWithContext(callCtx, guardInterface+".ExitForUpdate", 0, uint64(idle/time.Second)).Store(&answer)
	cancel()
	if err != nil || answer != "ready" {
		if rollingBack {
			return fmt.Errorf("%w：請完成組字後再回復（%s；%v）", autoupdate.ErrShellBusy, answer, err)
		}
		if remaining, _ := ibusShells(); len(remaining) > 0 {
			restart.Phase = "" // 外殼仍在服務，不需要恢復
		}
		os.Remove(shellJournal(p.base))
		return fmt.Errorf("%w：IBus 外殼正在使用，保留新版稍後再試（%s；%v）", autoupdate.ErrShellBusy, answer, err)
	}
	return waitIBusStopped(ctx)
}

func (p *system) installIBusShell(ctx context.Context, conn *dbus.Conn, root string, r *update.Release, restart shellRestart, resumed bool) (bool, error) {
	if !resumed {
		restart = shellRestart{Frontend: frontendIBus}
		restart.OldVersion, _ = p.Installed()
	}
	// 任一失敗也要讓 ibus-daemon 重新叫起外殼；恢復使用獨立 deadline，不受取消的安裝 context 影響。
	defer func() {
		if restart.Phase != "" {
			recovery, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if e := restoreIBus(recovery, &restart); e != nil {
				fmt.Fprintln(os.Stderr, "0c-update: 恢復輸入法：", e)
			}
		}
	}()
	needed := p.shellIdle
	if p.consent {
		needed = 0
	}
	if err := p.stopIBusShell(ctx, conn, &restart, needed, false); err != nil {
		return false, err
	}
	err := p.Drain(ctx)
	if err == nil {
		err = waitServersStopped(ctx)
	}
	if err != nil {
		return false, fmt.Errorf("%w：%v", autoupdate.ErrShellBusy, err)
	}
	if pids, _ := ibusShells(); len(pids) > 0 {
		// 交接期間 ibus-daemon 又叫起了舊外殼（例如使用者切換輸入法），它會啟動舊引擎；稍後再試。
		return false, fmt.Errorf("%w：IBus 外殼在交接期間重新啟動", autoupdate.ErrShellBusy)
	}
	restart.Phase = "installing"
	if err := saveShellRestart(p.base, &restart); err != nil {
		return false, err
	}
	// 這次真的安裝時，備份一律取自收據（舊版本號讀不到也能回復）；接續先前已裝好的交易時沿用原紀錄。
	if installed, _ := installedVersion(); installed != r.Version {
		if err := installLiveBundle(ctx, root, r); err != nil {
			return false, err
		}
		restart.Backup = receiptBackup()
	}
	if restart.Backup == "" && restart.OldVersion != "" && restart.OldVersion != r.Version {
		restart.Backup = receiptBackup()
	}
	refreshIBusComponent()
	restart.Phase = "verifying"
	if err = saveShellRestart(p.base, &restart); err != nil {
		return false, err
	}
	err = p.StartEngine(ctx)
	if err == nil {
		err = restoreIBus(ctx, &restart)
	}
	if err == nil {
		err = ibusShellMatches(ctx, conn, &restart, r)
	}
	if err == nil {
		v, running, e := p.Running(ctx)
		if e != nil || !running || v != r.Version {
			err = fmt.Errorf("運作引擎 %q，預期 %s：%v", v, r.Version, e)
		}
	}
	if err != nil {
		recovery, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		restart.Phase = "rollback"
		if e := saveShellRestart(p.base, &restart); e != nil {
			return false, fmt.Errorf("%v；無法記錄回復：%v", err, e)
		}
		if e := p.rollbackIBusShell(recovery, conn, &restart); e != nil {
			return false, fmt.Errorf("%v；回復尚未完成：%w", err, e)
		}
		os.Remove(shellJournal(p.base))
		return false, fmt.Errorf("%w：%v", autoupdate.ErrShellHealth, err)
	}
	afterInstall()
	os.Remove(shellJournal(p.base))
	restart.Phase = "" // 已恢復且核對成功
	syncIBusToolkits()
	fmt.Printf("已更新至 %s，IBus 的靈犀注音已重新載入，不需要登出。\n", r.Version)
	return false, nil
}

// refreshIBusComponent 讓已登錄的元件描述檔跟上新版外殼產生的內容。不重啟 IBus：
// 元件檔只影響 ibus-daemon 下次啟動時列出的資訊，執行檔路徑不變。失敗不影響更新。
func refreshIBusComponent() {
	if !ibusRegistered() {
		return
	}
	out, err := exec.Command(homePath(ibusShellPath), "--component").Output()
	path := homePath(ibusComponent)
	if old, e := os.ReadFile(path); err != nil || len(out) == 0 || (e == nil && string(old) == string(out)) {
		return
	}
	if os.WriteFile(path+".new", out, 0o644) == nil {
		os.Rename(path+".new", path)
	}
}

// syncIBusToolkits 讓新版外殼補上它新加入的 GTK／Qt 輸入設定（0c-ibus toolkits）；使用者停用或自己設定過的
// 不動，不重啟 IBus，重新登入後生效。舊版外殼沒有這個子命令，失敗不影響更新。
func syncIBusToolkits() {
	if !ibusRegistered() {
		return
	}
	cmd := exec.Command(homePath(ibusShellPath), "toolkits")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.Run()
}

// receiptBackup 是收據記錄的這次完整安裝備份目錄。
func receiptBackup() string {
	var receipt struct {
		Backup string `json:"backup"`
	}
	if b, err := os.ReadFile(receiptPath()); err == nil {
		_ = json.Unmarshal(b, &receipt)
	}
	return receipt.Backup
}

// rollbackIBusShell 拒絕中斷組字；受阻時保留 journal，下一次喚起接續回復。
func (p *system) rollbackIBusShell(ctx context.Context, conn *dbus.Conn, restart *shellRestart) error {
	if restart.Backup == "" {
		return fmt.Errorf("缺少完整安裝備份")
	}
	if err := p.stopIBusShell(ctx, conn, restart, 0, true); err != nil {
		return err
	}
	if err := p.Drain(ctx); err != nil {
		return err
	}
	if err := waitServersStopped(ctx); err != nil {
		return err
	}
	if installed, _ := installedVersion(); installed != restart.OldVersion {
		if err := rollbackLiveBundle(ctx, p.base, restart.Backup); err != nil {
			return err
		}
	}
	if err := p.StartEngine(ctx); err != nil {
		return err
	}
	if err := restoreIBus(ctx, restart); err != nil {
		return err
	}
	version, running, err := p.Running(ctx)
	if err != nil || !running || version != restart.OldVersion {
		return fmt.Errorf("原版引擎未恢復：%s；%v", version, err)
	}
	return nil
}
