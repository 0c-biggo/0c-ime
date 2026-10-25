//go:build darwin

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
)

type adoptionFixture struct {
	base, app     string
	actions       []string
	old           []uiProcess
	away, relogin bool
	state         string
	retire        func()
}

func newAdoptionFixture(t *testing.T) *adoptionFixture {
	t.Helper()
	f := &adoptionFixture{base: t.TempDir(), old: []uiProcess{{PID: 123}}, away: true, state: "enabled"}
	f.app = filepath.Join(f.base, "0c.app")
	for name, contents := range map[string]string{
		"0c.app/Contents/MacOS/0c":                                       "executable",
		"0c.app/Contents/Library/LaunchAgents/com.biggo.0c.update.plist": "sealed plist",
		"install-receipt.json":                                           `{"schema":1,"active":true,"app":"0c.app"}`,
	} {
		p := filepath.Join(f.base, name)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s, _ := autoupdate.Load(f.base)
	if err := autoupdate.Save(f.base, s); err != nil {
		t.Fatal(err)
	}
	state, relogin, retire, spawn, kick, printAgent, observe, away := agentScheduleState, agentReloginRequired, retireForAdoption, spawnAdopt, kickAgent, launchctlPrintAgent, observeMacShells, userAway
	t.Cleanup(func() {
		agentScheduleState, agentReloginRequired, retireForAdoption, spawnAdopt, kickAgent, launchctlPrintAgent, observeMacShells, userAway = state, relogin, retire, spawn, kick, printAgent, observe, away
	})
	agentScheduleState = func(app, action string) (string, error) {
		if app != f.app {
			t.Fatal("unexpected App", app)
		}
		f.actions = append(f.actions, action)
		return f.state, nil
	}
	agentReloginRequired = func(string) bool { return f.relogin }
	retireForAdoption = func(string) bool {
		f.actions = append(f.actions, "retire")
		if f.retire != nil {
			f.retire()
		}
		f.old = nil
		return true
	}
	spawnAdopt = func() { f.actions = append(f.actions, "spawn") }
	kickAgent = func() { f.actions = append(f.actions, "kick") }
	launchctlPrintAgent = func() (string, error) {
		f.actions = append(f.actions, "launchctl")
		return "state = waiting\nruns = 3\n", nil
	}
	observeMacShells = func(string) ([]uiProcess, error) { f.actions = append(f.actions, "observe"); return f.old, nil }
	userAway = func() bool { return f.away }
	return f
}

func (f *adoptionFixture) identity() string {
	return agentInstallIdentity(f.base, filepath.Join(f.app, "Contents/MacOS/0c"))
}

func TestAdoptScheduleNativeSafeHandoverAndSteadyStatus(t *testing.T) {
	f := newAdoptionFixture(t)
	// 最近才查過 launchd 也不能擋掉本次安裝的接管。
	noteLaunchdCheck(f.base)
	adoptScheduleAt(f.base, f.app, false)
	if !reflect.DeepEqual(f.actions, []string{"observe", "spawn"}) {
		t.Fatal(f.actions)
	}
	f.actions = nil
	adoptScheduleAt(f.base, f.app, true)
	want := []string{"status", "observe", "retire", "observe", "status", "register", "launchctl", "kick"}
	if !reflect.DeepEqual(f.actions, want) || !agentTakeoverDone(f.base, f.identity()) {
		t.Fatal(f.actions)
	}
	// 新版排程進來代表 kick 已完成；不再查 ps/lsof 或重複註冊。
	t.Setenv("XPC_SERVICE_NAME", agentLabel)
	adoptScheduleAt(f.base, f.app, true)
	f.actions = nil
	adoptScheduleAt(f.base, f.app, false)
	if len(f.actions) != 0 {
		t.Fatal("steady status repeated system work", f.actions)
	}
	// 完成記號仍不能掩蓋使用者移除排程；不查、不重建。
	s, _ := autoupdate.Load(f.base)
	s.ScheduleRemoved = true
	autoupdate.Save(f.base, s)
	adoptScheduleAt(f.base, f.app, true)
	if len(f.actions) != 0 {
		t.Fatal("schedule removed", f.actions)
	}
}

func TestAdoptScheduleNativeDefersWithoutChangingPreference(t *testing.T) {
	for _, reason := range []string{"awake", "relogin", "removed", "requires-approval", "transaction", "corrupt-state"} {
		t.Run(reason, func(t *testing.T) {
			f := newAdoptionFixture(t)
			switch reason {
			case "awake":
				f.away = false
			case "relogin":
				f.relogin = true
			case "removed":
				s, _ := autoupdate.Load(f.base)
				s.ScheduleRemoved = true
				autoupdate.Save(f.base, s)
			case "requires-approval", "not-registered":
				f.state = reason
			case "transaction":
				os.WriteFile(filepath.Join(f.base, "update.lock"), nil, 0600)
			case "corrupt-state":
				os.WriteFile(autoupdate.Path(f.base), []byte("invalid"), 0600)
			}
			before, _ := os.ReadFile(autoupdate.Path(f.base))
			adoptScheduleAt(f.base, f.app, true)
			after, _ := os.ReadFile(autoupdate.Path(f.base))
			if string(before) != string(after) || agentTakeoverDone(f.base, f.identity()) {
				t.Fatal("changed preference or completed unsafe handover")
			}
			for _, a := range f.actions {
				if a == "register" || a == "retire" || a == "kick" {
					t.Fatal(reason, f.actions)
				}
			}
		})
	}
}

func TestAdoptScheduleNativePreferenceChangesDuringRetirement(t *testing.T) {
	for _, reason := range []string{"removed", "requires-approval"} {
		t.Run(reason, func(t *testing.T) {
			f := newAdoptionFixture(t)
			f.retire = func() {
				if reason == "removed" {
					s, _ := autoupdate.Load(f.base)
					s.ScheduleRemoved = true
					autoupdate.Save(f.base, s)
				} else {
					f.state = reason
				}
			}
			adoptScheduleAt(f.base, f.app, true)
			for _, a := range f.actions {
				if a == "register" || a == "kick" {
					t.Fatal(f.actions)
				}
			}
			if agentTakeoverDone(f.base, f.identity()) {
				t.Fatal("must stay pending")
			}
		})
	}
}

func TestAdoptScheduleNativePreservesManualPauseAndRestore(t *testing.T) {
	f := newAdoptionFixture(t)
	s, _ := autoupdate.Load(f.base)
	s.Mode = autoupdate.ModeManual
	s.PausedUntil = time.Now().Add(time.Hour)
	autoupdate.Save(f.base, s)
	before, _ := os.ReadFile(autoupdate.Path(f.base))
	// 舊 rollback 留下 inactive；只修回 sealed 名稱，不依此開啟被使用者停用的背景項目。
	p := appAgent(f.app)
	if err := os.Rename(p, p+inactiveAgentSuffix); err != nil {
		t.Fatal(err)
	}
	f.state = "not-registered"
	adoptScheduleAt(f.base, f.app, false) // status 已先改回，worker 下一次看不到 Restored=true
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
	f.actions = nil
	agentScheduleState = func(app, action string) (string, error) {
		f.actions = append(f.actions, action)
		if action == "register" {
			return "enabled", nil
		}
		return f.state, nil
	}
	adoptScheduleAt(f.base, f.app, true)
	after, _ := os.ReadFile(autoupdate.Path(f.base))
	if string(before) != string(after) || !agentTakeoverDone(f.base, f.identity()) {
		t.Fatal(f.actions)
	}
	if b, err := os.ReadFile(p); err != nil || string(b) != "sealed plist" {
		t.Fatal(fmt.Sprint(err), string(b))
	}
}
