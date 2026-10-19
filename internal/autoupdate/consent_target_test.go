package autoupdate

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNotificationConsentIsBoundToVerifiedAsset(t *testing.T) {
	for _, scenario := range []string{"same", "already-superseded", "new-release", "withdrawn", "same-version-new-asset", "missing-hash", "offline"} {
		t.Run(scenario, func(t *testing.T) {
			p := &fakePlatform{installed: "0.1.0-dev.40", running: "0.1.0-dev.40", feed: feedFor("0.1.0-dev.41", "core", 100)}
			co, _ := newCoordinator(t, p)
			s, _ := Load(co.Base)
			s.Mode = ModeAsk
			if err := Save(co.Base, s); err != nil {
				t.Fatal(err)
			}
			s = run(t, co)
			n := notice(s)
			if n.Action != "finish" || n.TargetVersion != s.Target.Version || n.TargetSHA256 != s.Target.SHA256 {
				t.Fatal("notice lacks asset identity", n)
			}
			co.Consent = true // The next scheduled feed check is still six hours away.
			co.ConsentVersion, co.ConsentSHA256 = n.TargetVersion, n.TargetSHA256
			switch scenario {
			case "already-superseded":
				s.Target.Version = "0.1.0-dev.42"
				if err := Save(co.Base, s); err != nil {
					t.Fatal(err)
				}
			case "new-release":
				p.feed = feedFor("0.1.0-dev.42", "core", 100)
			case "withdrawn":
				p.feed = feedFor("0.1.0-dev.41", "core", 100, "0.1.0-dev.41")
			case "same-version-new-asset":
				p.feed.Assets[0].SHA256 = strings.Repeat("b", 64)
			case "missing-hash":
				co.ConsentSHA256 = ""
			case "offline":
				p.fetchErr = errors.New("artificial unavailable signed feed")
			}
			p.calls = nil
			result, err := co.Run(context.Background())
			if scenario == "same" {
				if err != nil || result.Stage != StageActive {
					t.Fatalf("matching consent must finish: %+v %v", result, err)
				}
			} else {
				if scenario == "offline" && (err == nil || !strings.Contains(err.Error(), "無法重新確認")) {
					t.Fatalf("unverified notification consent accepted: %+v %v", result, err)
				}
				if scenario != "offline" && !errors.Is(err, ErrTargetChanged) {
					t.Fatalf("stale consent accepted: %+v %v", result, err)
				}
				for _, call := range p.calls {
					if call != "fetch" {
						t.Fatalf("stale consent performed %s", call)
					}
				}
				saved, _ := Load(co.Base)
				if scenario == "withdrawn" && saved.Target != nil {
					t.Fatal("withdrawal must persist")
				}
				if scenario == "new-release" && saved.Target.Version != "0.1.0-dev.42" {
					t.Fatal("new discovery must persist without install")
				}
			}
		})
	}
}
