package autoupdate

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/internal/update"
)

// rulePlatform 是有外殼版本與更新器版本的假平台（#295 的版本規則）。
type rulePlatform struct {
	*fakePlatform
	shell, updater string
}

func (p *rulePlatform) ShellVersion() string   { return p.shell }
func (p *rulePlatform) UpdaterVersion() string { return p.updater }

func rulesFeed() *update.SignedFeed {
	f := feedFor("0.2.16", "shell", 100)
	asset := func(kind, letter, version string) update.SignedAsset {
		return update.SignedAsset{Asset: update.Asset{URL: "https://x/" + letter, SHA256: strings.Repeat(letter, 64), Size: 1}, Kind: kind, Version: version}
	}
	f.Rules = []update.FeedRule{
		{OS: "linux", Arch: "amd64", InstalledMin: "0.2.15", Assets: []update.SignedAsset{asset("core", "b", ""), asset("updater", "c", "0.2.16")}},
		{OS: "linux", Arch: "amd64", InstalledBelow: "0.2.10"}, // 太舊的外殼：這一版不提供
	}
	return f
}

// 外殼夠新的電腦依規則拿到 core（不必換外殼），另外記下只換更新器的資產；沒有符合的規則時照舊拿 shell。
func TestDiscoverUsesRulesAndRecordsUpdaterOffer(t *testing.T) {
	for _, c := range []struct {
		shell, kind, sha string
		offer            bool
		reason           string
	}{
		{"0.2.15", "core", "b", true, ""},
		{"0.2.12", "shell", "a", false, ""},
		{"0.2.9", "", "", false, ReasonNoAsset},
	} {
		p := &rulePlatform{fakePlatform: &fakePlatform{installed: c.shell, feed: rulesFeed(), cond: Conditions{Idle: time.Hour, IdleKnown: true}}, shell: c.shell, updater: c.shell}
		co, _ := newCoordinator(t, p.fakePlatform)
		co.Platform = p
		co.Force = true
		s, err := co.Check(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case c.kind == "" && (s.Target != nil || s.Latest == nil || s.Latest.Reason != c.reason):
			t.Errorf("%s: target %+v latest %+v", c.shell, s.Target, s.Latest)
		case c.kind != "" && (s.Target == nil || s.Target.Kind != c.kind || s.Target.SHA256[:1] != c.sha || s.Target.Version != "0.2.16"):
			t.Errorf("%s: target %+v", c.shell, s.Target)
		}
		if got := s.UpdaterOffer != nil; got != c.offer {
			t.Errorf("%s: updater offer %+v", c.shell, s.UpdaterOffer)
		} else if got && (s.UpdaterOffer.Version != "0.2.16" || s.UpdaterOffer.Kind != "updater" || s.UpdaterOffer.SHA256[:1] != "c") {
			t.Errorf("%s: updater offer %+v", c.shell, s.UpdaterOffer)
		}
	}
}

// 協調器存檔時保留 auto 另外記下的「只換更新器」結果；撤回的版本不提供只換更新器。
func TestUpdaterAttemptSurvivesCoordinatorSaveAndWithdrawn(t *testing.T) {
	p := &rulePlatform{fakePlatform: &fakePlatform{installed: "0.2.15", feed: rulesFeed()}, shell: "0.2.15", updater: "0.2.15"}
	co, _ := newCoordinator(t, p.fakePlatform)
	co.Platform = p
	co.Force = true
	if _, err := co.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := EditState(co.Base, func(s *State) error {
		s.UpdaterTried = &UpdaterAttempt{Version: "0.2.16", SHA256: strings.Repeat("c", 64), Result: "installed"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	p.feed.Withdrawn = []string{"0.2.16"}
	s, err := co.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.UpdaterTried == nil || s.UpdaterTried.Result != "installed" {
		t.Fatalf("attempt lost: %+v", s.UpdaterTried)
	}
	if s.UpdaterOffer != nil {
		t.Fatalf("withdrawn version offered: %+v", s.UpdaterOffer)
	}
}

// 試算（selftest）用同一套規則。
func TestDryRunFollowsRules(t *testing.T) {
	s := &State{Schema: 1, InstallID: "x", Mode: ModeAuto, Stage: StageIdle}
	plan, err := DryRun(context.Background(), s, rulesFeed(), DryRunInputs{Installed: "0.2.15", Updater: "0.2.15", GOOS: "linux", GOARCH: "amd64"}, time.Now())
	if err != nil || plan.Target == nil || plan.Target.Kind != "core" || plan.UpdaterOffer == nil {
		t.Fatalf("%v %+v", err, plan)
	}
	if s.Target != nil {
		t.Fatal("dry run changed the caller's state")
	}
}
