//go:build !windows

package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/internal/update"
)

func TestMacShellReadyRequiresNewLogin(t *testing.T) {
	installedAt := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	before, after := installedAt.Add(-24*time.Hour), installedAt.Add(time.Hour)
	for _, tc := range []struct {
		name               string
		installed, current uint64
		boot               time.Time
		version            string
		active, want       bool
	}{
		{"same-session-new-engine", 103, 103, before, "B", true, false},
		{"unlock-or-fast-switch", 103, 103, time.Time{}, "B", true, false},
		{"new-login", 103, 104, before, "B", true, true},
		// #98：開機後 ASID 從同一個起點編號，重開機後的第一個登入可能拿到與安裝時相同的 ASID。
		{"reboot-reuses-asid", 103, 103, after, "B", true, true},
		{"reboot-old-receipt", 0, 104, after, "B", true, true},
		{"reboot-no-aqua", 103, 0, after, "B", true, true},
		{"old-receipt", 0, 104, before, "B", true, false},
		{"no-aqua", 103, 0, time.Time{}, "B", true, false},
		{"wrong-version", 103, 104, after, "A", true, false},
		{"uninstalled", 103, 104, after, "B", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "receipt.json")
			b, _ := json.Marshal(update.MacReceipt{Version: tc.version, App: update.MacApp, Active: tc.active, SessionASID: tc.installed})
			if err := os.WriteFile(path, b, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(path, installedAt, installedAt); err != nil {
				t.Fatal(err)
			}
			got, _ := macShellBoundary(path, "B", tc.current, tc.boot)
			if got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMacShellMappedInode(t *testing.T) {
	path := "/Users/test/Library/Input Methods/0c.app/Contents/MacOS/0c"
	for _, tc := range []struct {
		name, output string
		want         bool
	}{
		{"current", "p20\nftxt\ni123\nn" + path + "\n", true},
		{"old-mapping-at-same-path", "p20\nftxt\ni122\nn" + path + "\n", false},
		{"other-path", "p20\nftxt\ni123\nn/backup/0c\n", false},
		{"different-records", "p20\nftxt\ni123\nn/backup/0c\nftxt\ni122\nn" + path + "\n", false},
		{"not-txt", "p20\nf1\ni123\nn" + path + "\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := mappedMacShell(tc.output, path, 123); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// #98：重新登入後還沒有 App 用到輸入法（外殼與引擎都沒在跑）不是舊外殼的證據；只有真的看到
// 備份裡的舊副本或換掉前的舊執行檔在執行，才繼續要求重新登入。
func TestStaleMacShells(t *testing.T) {
	const home = "/Users/test-user/Library"
	installed := home + "/Input Methods/0c.app/Contents/MacOS/0c"
	backup := home + "/Application Support/0c/backups/macos-20261014-090000-1/0c.app"
	ps := "  101 /usr/libexec/trustd\n" +
		"  202 " + installed + "\n" +
		"  303 " + backup + "/Contents/MacOS/0c\n" +
		"  404 " + home + "/Input Methods/Zuin.app/Contents/MacOS/Zuin\n" +
		"  505 /Applications/0c Settings.app/Contents/MacOS/0c-settings\n" +
		"garbage\n"
	procs := macShellProcesses(ps)
	if len(procs) != 3 || procs[0].PID != "202" || procs[1].App != backup || procs[2].App != home+"/Input Methods/Zuin.app" {
		t.Fatalf("processes: %+v", procs)
	}
	current := func(ok bool, err error) func(string) (bool, error) {
		return func(string) (bool, error) { return ok, err }
	}
	for _, tc := range []struct {
		name    string
		ps      string
		current func(string) (bool, error)
		want    []string
		wantErr bool
	}{
		{"nothing-loaded-yet", "  101 /usr/libexec/trustd\n", current(false, errors.New("unused")), nil, false},
		{"new-shell", "  202 " + installed + "\n", current(true, nil), nil, false},
		{"two-new-shells", "  202 " + installed + "\n  203 " + installed + "\n", current(true, nil), nil, false},
		{"backup-copy", "  303 " + backup + "/Contents/MacOS/0c\n", current(true, nil), []string{backup}, false},
		{"new-and-backup", "  202 " + installed + "\n  303 " + backup + "/Contents/MacOS/0c\n", current(true, nil), []string{backup}, false},
		{"replaced-executable", "  202 " + installed + "\n", current(false, nil), []string{home + "/Input Methods/0c.app"}, false},
		{"legacy-zuin-ignored", "  404 " + home + "/Input Methods/Zuin.app/Contents/MacOS/Zuin\n", current(true, nil), nil, false},
		{"lsof-failed", "  202 " + installed + "\n", current(false, errors.New("lsof")), nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := staleMacShells(macShellProcesses(tc.ps), installed, tc.current)
			if (err != nil) != tc.wantErr || !slices.Equal(got, tc.want) {
				t.Fatalf("got %v %v, want %v (error %v)", got, err, tc.want, tc.wantErr)
			}
		})
	}
}
