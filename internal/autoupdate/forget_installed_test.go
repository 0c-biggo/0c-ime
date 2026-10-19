package autoupdate

import (
	"os"
	"testing"
)

func TestForgetInstalledDropsTargetSupersededByManualInstall(t *testing.T) {
	base := t.TempDir()
	if dropped, err := ForgetInstalled(base, "0.1.0-dev.54"); err != nil || dropped {
		t.Fatalf("first install: dropped=%v err=%v", dropped, err)
	}
	if _, err := os.Stat(Path(base)); !os.IsNotExist(err) {
		t.Fatalf("first install created state: %v", err)
	}
	cases := []struct {
		name, version, kind, stage string
		switched, keep             bool
	}{
		{"same version prepared", "0.1.0-dev.54", "shell", StagePrepared, false, false},
		{"older version downloaded", "0.1.0-dev.53", "core", StageDownloaded, false, false},
		{"newer version stays", "0.1.0-dev.55", "shell", StagePrepared, false, true},
		{"switched core is left to the coordinator", "0.1.0-dev.54", "core", StagePrepared, true, true},
		{"shell waiting for relogin is left to the coordinator", "0.1.0-dev.54", "shell", StagePending, false, true},
		{"older shell waiting for relogin is dropped", "0.1.0-dev.53", "shell", StagePending, false, false},
		{"newer shell waiting for relogin stays", "0.1.0-dev.55", "shell", StagePending, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base := t.TempDir()
			s, _ := Load(base)
			s.Target = &Target{Version: c.version, Kind: c.kind, SHA256: "abc"}
			s.Stage, s.Switched = c.stage, c.switched
			s.Download, s.Prepared = "update/downloads/x.tar.gz", "versions/x"
			s.Snooze = &NoticeSnooze{Version: c.version, SHA256: "abc"}
			s.block(s.PreparedAt, "engine_busy", "使用中，等待閒置")
			if err := Save(base, s); err != nil {
				t.Fatal(err)
			}
			dropped, err := ForgetInstalled(base, "0.1.0-dev.54")
			if err != nil {
				t.Fatal(err)
			}
			s, _ = Load(base)
			if c.keep {
				if dropped || s.Target == nil || s.Stage != c.stage || s.Blocked == nil {
					t.Fatalf("dropped a target the coordinator still owns: %+v", s)
				}
				return
			}
			if !dropped || s.Target != nil || s.Download != "" || s.Prepared != "" || s.Snooze != nil || s.Blocked != nil || s.Stage != StageIdle {
				t.Fatalf("stale target survived manual install: %+v", s)
			}
			if v := ViewOf(s, "0.1.0-dev.54"); v.Target != nil {
				t.Fatalf("view still offers an install: %+v", v)
			}
			if last := s.History[len(s.History)-1]; last.Kind != "superseded" || last.Version != c.version {
				t.Fatalf("history: %+v", last)
			}
		})
	}
}
