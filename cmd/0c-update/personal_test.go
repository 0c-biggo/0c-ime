package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/bridge/ipc"
	"github.com/0c-biggo/0c-ime/core/engine"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/datadir"
	"github.com/0c-biggo/0c-ime/internal/userlock"
)

// personalBase 建一個有學習資料、自訂詞彙與各平台升級前備份的個人資料目錄。
func personalBase(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(base, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("user.json", `{"Format":7,"Uni":{"秘密":3},"Total":3}`)
	write("user.json.tmp", `{"Format":7,"Uni":{"秘密":4}}`)
	write("vocab.json", `{"format":1,"words":[]}`)
	// Linux：install.py 的 0c-install-*、restore-user 另存的 0c-user-*、make upgrade 的 upgrade-*
	write("backups/0c-install-abcd1234/personal/user.json", "learning")
	write("backups/0c-install-abcd1234/personal/vocab.json", "vocab")
	write("backups/0c-install-abcd1234/home/.local/bin/0c-update", "program")
	write("backups/0c-user-efgh5678/personal/user.json", "learning")
	write("backups/upgrade-20261001-000000-x/personal/user.json", "learning")
	// Windows：每次安裝、升級、回復與移除的 backups\windows-*
	write("backups/windows-1234/user.json", "learning")
	write("backups/windows-1234/vocab.json", "vocab")
	write("backups/windows-1234/previous.json", "{}")
	write(".user-restore-999", "learning")
	return base
}

func checkPersonalCleared(t *testing.T, base string) {
	t.Helper()
	for _, rel := range []string{"user.json.tmp", "backups/0c-install-abcd1234/personal/user.json", "backups/0c-user-efgh5678/personal/user.json",
		"backups/upgrade-20261001-000000-x/personal/user.json", "backups/windows-1234/user.json", ".user-restore-999"} {
		if _, err := os.Lstat(filepath.Join(base, filepath.FromSlash(rel))); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s 沒有刪掉：%v", rel, err)
		}
	}
	for rel, want := range map[string]string{"vocab.json": `{"format":1,"words":[]}`, "backups/0c-install-abcd1234/personal/vocab.json": "vocab",
		"backups/windows-1234/vocab.json": "vocab", "backups/windows-1234/previous.json": "{}", "backups/0c-install-abcd1234/home/.local/bin/0c-update": "program"} {
		if b, err := os.ReadFile(filepath.Join(base, filepath.FromSlash(rel))); err != nil || string(b) != want {
			t.Errorf("%s 要保留：%q %v", rel, b, err)
		}
	}
}

func noEngineDial(time.Duration) (net.Conn, error) { return nil, errors.New("沒有引擎") }

// 引擎沒在跑：在寫入者鎖內刪掉 user.json（與中斷留下的 .tmp），升級前的個人詞庫備份一起刪；vocab.json 與其他備份檔保留。
func TestPersonalResetWithoutEngine(t *testing.T) {
	base := personalBase(t)
	res, err := resetPersonal(context.Background(), noEngineDial, base)
	if err != nil || !res.OK || res.Engine != "not_running" || res.BackupsRemoved != 4 || res.Schema != contractSchema {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(base, "user.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("user.json 沒有刪掉：%v", err)
	}
	checkPersonalCleared(t, base)
	// 再按一次也成功（沒有東西可刪）。
	if res, err := resetPersonal(context.Background(), noEngineDial, base); err != nil || res.BackupsRemoved != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	// 從來沒有個人資料目錄。
	if _, err := resetPersonal(context.Background(), noEngineDial, filepath.Join(base, "missing")); err != nil {
		t.Fatal(err)
	}
}

// 有引擎拿著寫入者鎖（正在啟動、還沒開始服務）：不碰檔案，等它開始服務；一直等不到就回 busy，備份也不刪。
func TestPersonalResetWaitsForStartingEngine(t *testing.T) {
	base := personalBase(t)
	lock, err := userlock.TryLock(base)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	defer func(r int, w time.Duration) { personalResetRetry, personalResetWait = r, w }(personalResetRetry, personalResetWait)
	personalResetRetry, personalResetWait = 2, time.Millisecond
	_, err = resetPersonal(context.Background(), noEngineDial, base)
	if !errors.Is(err, errResetBusy) || classify(err).Code != codeBusy {
		t.Fatalf("%v %+v", err, classify(err))
	}
	if b, _ := os.ReadFile(filepath.Join(base, "user.json")); len(b) == 0 {
		t.Fatal("鎖被拿著時不能刪 user.json")
	}
	if _, err := os.Stat(filepath.Join(base, "backups/windows-1234/user.json")); err != nil {
		t.Fatal("學習紀錄沒清掉時不刪備份")
	}
}

// resetEngine 在 unix socket 上跑一個真的 ipc.Server，ResetPersonal 照 0c-server 的做法清空引擎並存檔到 base/user.json。
func resetEngine(t *testing.T, base string, support bool) (autoupdate.Dialer, *engine.Engine) {
	t.Helper()
	dir, err := os.MkdirTemp("", "0c-reset-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "e.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Skip("unix socket：", err)
	}
	e := engine.New(lexicon.NewMem(), lm.NewMem())
	if err := datadir.LoadUser(e, filepath.Join(base, "user.json")); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	srv := &ipc.Server{Engine: e}
	if support {
		srv.ResetPersonal = func() error {
			mu.Lock()
			defer mu.Unlock()
			e.ResetPersonal()
			return datadir.SaveUser(e, filepath.Join(base, "user.json"))
		}
	}
	srv.OnDrained = func() { l.Close() }
	go srv.Serve(l)
	t.Cleanup(func() { l.Close() })
	return func(timeout time.Duration) (net.Conn, error) { return net.DialTimeout("unix", sock, timeout) }, e
}

// 引擎在跑：交給它清空並立刻存檔（直接刪檔會在下一次存檔或結束時被寫回，#297）；備份照樣刪。
func TestPersonalResetThroughRunningEngine(t *testing.T) {
	base := personalBase(t)
	dial, e := resetEngine(t, base, true)
	if e.Model.Store.Uni["秘密"] == 0 {
		t.Fatal("前提：引擎載入了學習資料")
	}
	res, err := resetPersonal(context.Background(), dial, base)
	if err != nil || res.Engine != "running" || res.BackupsRemoved != 4 {
		t.Fatalf("%+v %v", res, err)
	}
	if len(e.Model.Store.Uni) != 0 {
		t.Fatal("記憶體裡的學習資料沒有清掉")
	}
	b, err := os.ReadFile(filepath.Join(base, "user.json"))
	var empty bytes.Buffer
	personal.NewStore().Save(&empty)
	if err != nil || string(b) != empty.String() {
		t.Fatalf("user.json 要是空白的個人資料：%s %v", b, err)
	}
	checkPersonalCleared(t, base)
}

// 認不得 reset_personal 的舊引擎：先請它交接退出（它會存檔），之後在鎖內刪檔。
func TestPersonalResetStopsOldEngineFirst(t *testing.T) {
	base := personalBase(t)
	dial, _ := resetEngine(t, base, false)
	res, err := resetPersonal(context.Background(), dial, base)
	if err != nil || res.Engine != "stopped" || res.BackupsRemoved != 4 {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(base, "user.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("user.json 沒有刪掉：%v", err)
	}
	checkPersonalCleared(t, base)
}

func TestPersonalCommandUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"wipe"}, {"reset", "extra"}, {"reset", "-bogus"}} {
		var ue usageError
		if err := personalCommand(args); !errors.As(err, &ue) {
			t.Fatalf("%v：%v", args, err)
		}
	}
	if !packageModeCommands["personal"] {
		t.Fatal("系統套件安裝也要能清除學習紀錄")
	}
	if !featuresOf().PersonalReset {
		t.Fatal("settings -json 要回報 features.personal_reset")
	}
}
