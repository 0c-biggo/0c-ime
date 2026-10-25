//go:build !windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestFinishAgentTakeoverOrdersRetireBeforeRegistration(t *testing.T) {
	var actions []string
	old := []uiProcess{{PID: 123, Path: "/backup/0c"}}
	c := agentTakeoverControl{
		identity: func() string { return "installed" },
		shells:   func() ([]uiProcess, error) { actions = append(actions, "observe"); return old, nil },
		away:     func() bool { actions = append(actions, "away"); return true },
		retire:   func() bool { actions = append(actions, "retire"); old = nil; return true },
		pull:     func() bool { actions = append(actions, "register"); return true },
	}
	if !finishAgentTakeover("installed", c) {
		t.Fatal("safe handover")
	}
	if !reflect.DeepEqual(actions, []string{"observe", "away", "retire", "observe", "register"}) {
		t.Fatal(actions)
	}
}

func TestFinishAgentTakeoverCannotRegisterBeforeSafeExit(t *testing.T) {
	for _, fault := range []string{"observation", "awake", "busy-or-returned", "still-alive", "recheck-error", "replaced-during-retire"} {
		t.Run(fault, func(t *testing.T) {
			identity := "installed"
			checks, pulls := 0, 0
			c := agentTakeoverControl{
				identity: func() string { return identity },
				shells: func() ([]uiProcess, error) {
					checks++
					if fault == "observation" || fault == "recheck-error" && checks > 1 {
						return nil, errors.New("unknown")
					}
					if checks == 1 || fault == "still-alive" {
						return []uiProcess{{PID: 123}}, nil
					}
					return nil, nil
				},
				away: func() bool { return fault != "awake" },
				retire: func() bool {
					if fault == "replaced-during-retire" {
						identity = "replacement"
					}
					return fault != "busy-or-returned"
				},
				pull: func() bool { pulls++; return true },
			}
			if finishAgentTakeover("installed", c) || pulls != 0 {
				t.Fatal("must leave pending without registering", pulls)
			}
		})
	}
}

func TestAgentTakeoverIdentityTracksReinstallAndRollback(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "update"), 0o700); err != nil {
		t.Fatal(err)
	}
	exe, receipt := filepath.Join(base, "0c"), filepath.Join(base, "install-receipt.json")
	os.WriteFile(exe, []byte("same release"), 0o755)
	os.WriteFile(receipt, []byte("receipt"), 0o600)
	first := agentInstallIdentity(base, exe)
	if first == "" || agentTakeoverDone(base, first) {
		t.Fatal("first installation is pending")
	}
	os.WriteFile(agentTakeoverStamp(base), []byte(first), 0o600)
	if !agentTakeoverDone(base, first) {
		t.Fatal("completed installation skips repeat work")
	}
	os.Rename(exe, exe+".old")
	os.WriteFile(exe, []byte("same release"), 0o755)
	if agentTakeoverDone(base, agentInstallIdentity(base, exe)) {
		t.Fatal("same version replacement must be observed")
	}
	os.Remove(exe)
	os.Rename(exe+".old", exe)
	os.Rename(receipt, receipt+".old")
	os.WriteFile(receipt, []byte("receipt"), 0o600)
	if agentTakeoverDone(base, agentInstallIdentity(base, exe)) {
		t.Fatal("rollback with original executable inode is a new transaction")
	}
	os.Remove(exe)
	if agentInstallIdentity(base, exe) != "" || agentTakeoverDone(base, "") {
		t.Fatal("missing identity cannot be completed")
	}
}

func TestFinishAgentTakeoverNoOldShellAndRegisterFailures(t *testing.T) {
	for _, fault := range []string{"none", "register", "replace-during-register"} {
		t.Run(fault, func(t *testing.T) {
			identity := "installed"
			c := agentTakeoverControl{
				identity: func() string { return identity },
				shells:   func() ([]uiProcess, error) { return nil, nil },
				away:     func() bool { t.Fatal("no old shell needs no away check"); return false },
				retire:   func() bool { t.Fatal("no old shell needs no retirement"); return false },
				pull: func() bool {
					if fault == "replace-during-register" {
						identity = "replaced"
					}
					return fault != "register"
				},
			}
			if got := finishAgentTakeover("installed", c); got != (fault == "none") {
				t.Fatal(fault, got)
			}
		})
	}
}

func TestClaimAgentSpawnDebouncesSameInstallation(t *testing.T) {
	base := t.TempDir()
	now := time.Now()
	if !claimAgentSpawn(base, "first", now) || claimAgentSpawn(base, "first", time.Now()) {
		t.Fatal("same installation spawned twice")
	}
	if !claimAgentSpawn(base, "replacement", time.Now()) {
		t.Fatal("replacement must not wait for old installation")
	}
	if !claimAgentSpawn(base, "replacement", time.Now().Add(time.Minute)) {
		t.Fatal("failed worker must be retried")
	}
}
