//go:build linux

package main

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"git.biggo.com/0c/ime/bridge/ipc"
)

// 真引擎先載入舊設定，再經真更新器修改；重啟後必須載入新路徑。
// 兩次都使用舊目錄的個人詞庫，驗證設定路徑可獨立遷移。
func TestLinuxSettingsPathMigrationAcrossEngineRestart(t *testing.T) {
	binary := buildServer(t)
	home := t.TempDir()
	updater := filepath.Join(home, "0c-update")
	if out, err := exec.Command("go", "build", "-o", updater, "../0c-update").CombinedOutput(); err != nil {
		t.Fatalf("build updater: %v\n%s", err, out)
	}
	base := filepath.Join(home, ".local", "share", "zuin")
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(base, "settings.json")
	body := `{"schema":1,"selection":"statistical","document_context":false,"punctuation":true}`
	if err := os.WriteFile(old, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	data, _ := filepath.Abs("../../testdata/toy")
	for _, migrated := range []bool{false, true} {
		if migrated {
			cmd := exec.Command(updater, "set", "document-context", "on", "-json")
			cmd.Env = append(os.Environ(), "HOME="+home)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("save settings: %v\n%s", err, out)
			}
		}
		func() {
			sock := filepath.Join(home, "s.sock")
			server := exec.Command(binary, "-data", data, "-user", filepath.Join(base, "user.json"), "-socket", sock)
			if err := server.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { server.Process.Signal(os.Interrupt); server.Wait() }()
			waitDial(t, sock)
			conn, err := net.Dial("unix", sock)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := ipc.WriteFrame(conn, ipc.Envelope{Op: ipc.OpHealth}); err != nil {
				t.Fatal(err)
			}
			var r ipc.Reply
			if err := ipc.ReadFrame(conn, &r); err != nil || !r.OK {
				t.Fatalf("health: %+v %v", r, err)
			}
			want := "false"
			if migrated {
				want = "true"
			}
			if r.Info["document_context"] != want || r.Info["selection_setting"] != "statistical" {
				t.Fatalf("migrated=%v: runtime settings %+v", migrated, r.Info)
			}
		}()
		if b, err := os.ReadFile(old); err != nil || string(b) != body {
			t.Fatalf("old settings changed: %s %v", b, err)
		}
		path := filepath.Join(home, ".local", "share", "0c", "settings.json")
		if _, err := os.Stat(path); migrated && err != nil || !migrated && !os.IsNotExist(err) {
			t.Fatalf("new settings must appear only after edit, migrated=%v: %v", migrated, err)
		}
	}
}
